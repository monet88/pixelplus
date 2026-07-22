// Package persistence owns physical durable state and atomic transitions.
package persistence

import (
	"context"
	"sync"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// FailClosedPrincipalStore is the production foundation authenticator. No
// Client API Keys are provisioned until the key-lifecycle ticket lands, so it
// authenticates nothing and every presented key returns the single
// indistinguishable authentication failure (#8 section 4.3). This keeps the
// real production composition constructor safe rather than open by default.
type FailClosedPrincipalStore struct{}

// NewFailClosedPrincipalStore builds the empty, fail-closed authenticator.
func NewFailClosedPrincipalStore() *FailClosedPrincipalStore {
	return &FailClosedPrincipalStore{}
}

// Authenticate always fails closed because no keys exist yet.
func (*FailClosedPrincipalStore) Authenticate(context.Context, ports.PresentedClientAPIKey) (domain.SecurityPrincipal, error) {
	return domain.SecurityPrincipal{}, ports.ErrAuthentication
}

// AlwaysAdmitStore is the production foundation admission store. The current
// slice configures no rate, concurrency, or quota limits for Provider Account
// operations, so it admits and issues a reservation that Reconcile settles as a
// no-op. Real per-(Tenant, Client API Key) limit windows arrive with a later
// admission ticket; this foundation never fails open on unavailable state
// because there is no external limit state to lose.
type AlwaysAdmitStore struct{}

// NewAlwaysAdmitStore builds the foundation admission store.
func NewAlwaysAdmitStore() *AlwaysAdmitStore {
	return &AlwaysAdmitStore{}
}

// Admit accepts the request and returns a reservation bound to the operation.
func (*AlwaysAdmitStore) Admit(_ context.Context, request ports.AdmissionRequest) (ports.AdmissionDecision, ports.AdmissionReservation, error) {
	return ports.AdmissionDecision{Admitted: true},
		ports.AdmissionReservation{Principal: request.Principal, Operation: request.Operation},
		nil
}

// Reconcile settles a reservation. The foundation holds no durable occupancy.
func (*AlwaysAdmitStore) Reconcile(context.Context, ports.AdmissionReservation) error {
	return nil
}

// MemoryReplayStore is the production foundation idempotency store. It performs
// an atomic claim, fingerprint match, terminal replay, and owner-only abandon
// under a single mutex so exactly one concurrent matching request becomes the
// executor and a terminal record is replayed without a new side effect
// (#20 section 5.5).
type MemoryReplayStore struct {
	mu      sync.Mutex
	records map[domain.ReplayScope]*replayRecord
}

type replayRecord struct {
	fingerprint domain.Fingerprint
	terminal    bool
	account     domain.ProviderAccount
}

// NewMemoryReplayStore builds an empty foundation replay store.
func NewMemoryReplayStore() *MemoryReplayStore {
	return &MemoryReplayStore{records: make(map[domain.ReplayScope]*replayRecord)}
}

// Claim atomically binds the scope+key to the fingerprint or resolves a repeat.
func (store *MemoryReplayStore) Claim(_ context.Context, identity domain.ReplayIdentity) (ports.ReplayDecision, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	existing, ok := store.records[identity.Scope]
	if !ok {
		store.records[identity.Scope] = &replayRecord{fingerprint: identity.Fingerprint}
		return ports.ReplayDecision{Outcome: ports.ReplayClaimed}, nil
	}
	if existing.fingerprint != identity.Fingerprint {
		return ports.ReplayDecision{Outcome: ports.ReplayConflict}, nil
	}
	if existing.terminal {
		return ports.ReplayDecision{Outcome: ports.ReplayTerminal, TerminalAccount: existing.account}, nil
	}
	return ports.ReplayDecision{Outcome: ports.ReplayInProgress}, nil
}

// Complete records the terminal result so later matching replays are stable.
func (store *MemoryReplayStore) Complete(_ context.Context, identity domain.ReplayIdentity, result ports.ReplayResult) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	record, ok := store.records[identity.Scope]
	if !ok {
		record = &replayRecord{fingerprint: identity.Fingerprint}
		store.records[identity.Scope] = record
	}
	record.terminal = true
	record.account = result.Account
	return nil
}

// Abandon clears an in-progress claim still owned by this request so a later
// retry can re-claim the scoped key. It never removes a terminal record.
func (store *MemoryReplayStore) Abandon(_ context.Context, identity domain.ReplayIdentity) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	record, ok := store.records[identity.Scope]
	if !ok {
		return nil
	}
	if record.terminal || record.fingerprint != identity.Fingerprint {
		return nil
	}
	delete(store.records, identity.Scope)
	return nil
}

// MemoryAccountStore is the production foundation Provider Account store. It
// keeps same-Tenant, non-enumerating visibility: foreign, unknown, and deleted
// identifiers all return ErrAccountNotVisible so the outcome is
// indistinguishable (#6 section 5.1).
type MemoryAccountStore struct {
	mu       sync.Mutex
	byTenant map[domain.TenantID]map[domain.ProviderAccountID]domain.ProviderAccount
}

// NewMemoryAccountStore builds an empty foundation account store.
func NewMemoryAccountStore() *MemoryAccountStore {
	return &MemoryAccountStore{byTenant: make(map[domain.TenantID]map[domain.ProviderAccountID]domain.ProviderAccount)}
}

// Create persists a new draft for the owning Tenant derived from the principal.
func (store *MemoryAccountStore) Create(_ context.Context, creation ports.AccountCreation) (domain.ProviderAccount, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	tenant := creation.Principal.TenantID
	accounts, ok := store.byTenant[tenant]
	if !ok {
		accounts = make(map[domain.ProviderAccountID]domain.ProviderAccount)
		store.byTenant[tenant] = accounts
	}
	accounts[creation.Account.ID] = creation.Account
	return creation.Account, nil
}

// Update persists a mutated account for the owning Tenant. It rejects an
// account that is not already visible under the principal's Tenant so a
// mutation can never create a cross-Tenant row or resurrect a deleted account
// (#6 section 5.1). Foreign, unknown, and deleted ids return the single
// non-enumerating visibility failure.
func (store *MemoryAccountStore) Update(_ context.Context, update ports.AccountUpdate) (domain.ProviderAccount, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	accounts, ok := store.byTenant[update.Principal.TenantID]
	if !ok {
		return domain.ProviderAccount{}, ports.ErrAccountNotVisible
	}
	existing, ok := accounts[update.Account.ID]
	if !ok || existing.Lifecycle == domain.LifecycleDeleted {
		return domain.ProviderAccount{}, ports.ErrAccountNotVisible
	}
	accounts[update.Account.ID] = update.Account
	return update.Account, nil
}

// Visible returns the owning-Tenant account or the single non-enumerating
// visibility failure for foreign, unknown, and deleted identifiers.
func (store *MemoryAccountStore) Visible(_ context.Context, principal domain.SecurityPrincipal, id domain.ProviderAccountID) (domain.ProviderAccount, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	accounts, ok := store.byTenant[principal.TenantID]
	if !ok {
		return domain.ProviderAccount{}, ports.ErrAccountNotVisible
	}
	account, ok := accounts[id]
	if !ok || account.Lifecycle == domain.LifecycleDeleted {
		return domain.ProviderAccount{}, ports.ErrAccountNotVisible
	}
	return account, nil
}

// List returns only the authenticated Tenant's non-deleted accounts.
func (store *MemoryAccountStore) List(_ context.Context, principal domain.SecurityPrincipal) ([]domain.ProviderAccount, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	accounts := store.byTenant[principal.TenantID]
	result := make([]domain.ProviderAccount, 0, len(accounts))
	for _, account := range accounts {
		if account.Lifecycle == domain.LifecycleDeleted {
			continue
		}
		result = append(result, account)
	}
	return result, nil
}

var (
	_ ports.PrincipalStore = (*FailClosedPrincipalStore)(nil)
	_ ports.AdmissionStore = (*AlwaysAdmitStore)(nil)
	_ ports.ReplayStore    = (*MemoryReplayStore)(nil)
	_ ports.AccountStore   = (*MemoryAccountStore)(nil)
)
