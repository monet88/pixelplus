// Package persistence owns physical durable state and atomic transitions.
package persistence

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	oauth       domain.OAuthAuthorization
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
		return ports.ReplayDecision{Outcome: ports.ReplayTerminal, TerminalAccount: existing.account, TerminalOAuth: existing.oauth}, nil
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
	record.oauth = result.OAuth
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

// applyAccountUpdate checks the caller-supplied preconditions and returns the
// account row that should be persisted. It supports both full-row replacement
// and narrow patch-mode updates so callers can touch LastProbedAt or clear a
// recovery permit without resurrecting concurrent health changes.
func applyAccountUpdate(existing domain.ProviderAccount, update ports.AccountUpdate) (domain.ProviderAccount, error) {
	if update.RequireEmptyOAuthMarker && existing.ActiveOAuthAuthorizationID != "" {
		return domain.ProviderAccount{}, ports.ErrAccountUpdateConflict
	}
	if update.RequireOAuthMarker != "" && existing.ActiveOAuthAuthorizationID != update.RequireOAuthMarker {
		return domain.ProviderAccount{}, ports.ErrAccountUpdateConflict
	}
	if update.RequireDraftLifecycle && existing.Lifecycle != domain.LifecycleDraft {
		return domain.ProviderAccount{}, ports.ErrAccountUpdateConflict
	}
	if update.RequirePendingVersion > 0 && existing.PendingCredentialVersion != update.RequirePendingVersion {
		return domain.ProviderAccount{}, ports.ErrAccountUpdateConflict
	}
	if update.RequireEmptyPendingVersion && existing.PendingCredentialVersion != 0 {
		return domain.ProviderAccount{}, ports.ErrAccountUpdateConflict
	}
	if update.RequireEmptyRecoveryPermit && existing.RecoveryPermit.Owner != "" {
		return domain.ProviderAccount{}, ports.ErrAccountUpdateConflict
	}
	if update.RequireRecoveryPermitOwner != "" && existing.RecoveryPermit.Owner != update.RequireRecoveryPermitOwner {
		return domain.ProviderAccount{}, ports.ErrAccountUpdateConflict
	}
	if update.RequireLifecycle != "" && existing.Lifecycle != update.RequireLifecycle {
		return domain.ProviderAccount{}, ports.ErrAccountUpdateConflict
	}
	if update.RequireControlsMatch && existing.Controls != update.RequireControls {
		return domain.ProviderAccount{}, ports.ErrAccountUpdateConflict
	}
	if required := update.RequireRecoveryCondition; required.Owner != "" && !accountHasRecoveryCondition(existing, required) {
		return domain.ProviderAccount{}, ports.ErrAccountUpdateConflict
	}

	if update.PatchLastProbedAt || update.PatchClearRecoveryPermit {
		if update.PatchLastProbedAt {
			existing.Credential.LastProbedAt = update.LastProbedAt
			existing.UpdatedAt = update.LastProbedAt
		}
		if update.PatchClearRecoveryPermit {
			existing.RecoveryPermit = domain.RecoveryPermit{}
		}
		return existing, nil
	}
	return update.Account, nil
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

// Restore satisfies the startup recovery contract for the in-process foundation
// store. A durable implementation must load and validate its persisted rows here;
// this store already owns its complete state for its process lifetime.
func (*MemoryAccountStore) Restore(context.Context) error {
	return nil
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
	result, err := applyAccountUpdate(existing, update)
	if err != nil {
		return domain.ProviderAccount{}, err
	}
	accounts[update.Account.ID] = result
	return result, nil
}

func accountHasRecoveryCondition(account domain.ProviderAccount, required domain.RecoveryPermit) bool {
	if account.Credential.Version != required.CredentialVersion {
		return false
	}
	for _, condition := range account.Health.Conditions {
		if condition.Scope == required.Scope &&
			condition.ConditionRevision == required.ConditionRevision &&
			condition.CredentialVersion == required.CredentialVersion {
			return true
		}
	}
	return false
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

// FileAccountStore is a durable foundation Provider Account store that persists
// same-Tenant account rows to a local JSON file. It satisfies the startup
// recovery contract by loading persisted cooldowns and recovery permits in
// Restore before composition reports readiness.
type FileAccountStore struct {
	mu       sync.Mutex
	path     string
	byTenant map[domain.TenantID]map[domain.ProviderAccountID]domain.ProviderAccount
}

// NewFileAccountStore builds a file-backed foundation account store.
func NewFileAccountStore(path string) *FileAccountStore {
	return &FileAccountStore{
		path:     path,
		byTenant: make(map[domain.TenantID]map[domain.ProviderAccountID]domain.ProviderAccount),
	}
}

// Restore loads persisted account rows from the configured file. A missing file
// is treated as an empty store; a corrupted file fails closed.
func (store *FileAccountStore) Restore(_ context.Context) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	data, err := os.ReadFile(store.path)
	if err != nil {
		if os.IsNotExist(err) {
			store.byTenant = make(map[domain.TenantID]map[domain.ProviderAccountID]domain.ProviderAccount)
			return nil
		}
		return err
	}
	var loaded map[domain.TenantID]map[domain.ProviderAccountID]domain.ProviderAccount
	if err := json.Unmarshal(data, &loaded); err != nil {
		return err
	}
	store.byTenant = loaded
	return nil
}

// Create persists a new draft for the owning Tenant derived from the principal.
func (store *FileAccountStore) Create(ctx context.Context, creation ports.AccountCreation) (domain.ProviderAccount, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	return store.createLocked(ctx, creation)
}

func (store *FileAccountStore) createLocked(_ context.Context, creation ports.AccountCreation) (domain.ProviderAccount, error) {
	tenant := creation.Principal.TenantID
	accounts, ok := store.byTenant[tenant]
	if !ok {
		accounts = make(map[domain.ProviderAccountID]domain.ProviderAccount)
		store.byTenant[tenant] = accounts
	}
	accounts[creation.Account.ID] = creation.Account
	if err := store.saveLocked(); err != nil {
		delete(accounts, creation.Account.ID)
		if len(accounts) == 0 {
			delete(store.byTenant, tenant)
		}
		return domain.ProviderAccount{}, err
	}
	return creation.Account, nil
}

// Update persists a mutated account for the owning Tenant. It rejects an
// account that is not already visible under the principal's Tenant so a
// mutation can never create a cross-Tenant row or resurrect a deleted account
// (#6 section 5.1).
func (store *FileAccountStore) Update(_ context.Context, update ports.AccountUpdate) (domain.ProviderAccount, error) {
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
	result, err := applyAccountUpdate(existing, update)
	if err != nil {
		return domain.ProviderAccount{}, err
	}
	accounts[update.Account.ID] = result
	if err := store.saveLocked(); err != nil {
		accounts[update.Account.ID] = existing
		return domain.ProviderAccount{}, err
	}
	return result, nil
}

// Visible returns the owning-Tenant account or the single non-enumerating
// visibility failure for foreign, unknown, and deleted identifiers.
func (store *FileAccountStore) Visible(_ context.Context, principal domain.SecurityPrincipal, id domain.ProviderAccountID) (domain.ProviderAccount, error) {
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
func (store *FileAccountStore) List(_ context.Context, principal domain.SecurityPrincipal) ([]domain.ProviderAccount, error) {
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

func (store *FileAccountStore) saveLocked() error {
	dir := filepath.Dir(store.path)
	if dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	temp := store.path + ".tmp"
	data, err := json.Marshal(store.byTenant)
	if err != nil {
		return err
	}
	if err := os.WriteFile(temp, data, 0o640); err != nil {
		return err
	}
	return os.Rename(temp, store.path)
}

// UnavailableAccountStore is installed after startup restoration fails. It
// rejects every account read or mutation with the same dependency outcome so
// direct product traffic cannot observe an empty/partial store and interpret it
// as "no cooldown" (health/cooldown spec §7.1-§7.2).
type UnavailableAccountStore struct{}

func NewUnavailableAccountStore() *UnavailableAccountStore {
	return &UnavailableAccountStore{}
}

func (*UnavailableAccountStore) Restore(context.Context) error {
	return ports.ErrDependencyUnavailable
}

func (*UnavailableAccountStore) Create(context.Context, ports.AccountCreation) (domain.ProviderAccount, error) {
	return domain.ProviderAccount{}, ports.ErrDependencyUnavailable
}

func (*UnavailableAccountStore) Update(context.Context, ports.AccountUpdate) (domain.ProviderAccount, error) {
	return domain.ProviderAccount{}, ports.ErrDependencyUnavailable
}

func (*UnavailableAccountStore) Visible(context.Context, domain.SecurityPrincipal, domain.ProviderAccountID) (domain.ProviderAccount, error) {
	return domain.ProviderAccount{}, ports.ErrDependencyUnavailable
}

func (*UnavailableAccountStore) List(context.Context, domain.SecurityPrincipal) ([]domain.ProviderAccount, error) {
	return nil, ports.ErrDependencyUnavailable
}

// ClosedCircuitStore is the production foundation Provider Surface Circuit gate.
// No correlation engine that collects the cross-Tenant bounded evidence
// (§12.3-§12.4) is wired yet, so there is genuinely nothing to open a circuit
// from: every surface reports closed (no open circuit). This is NOT a fail-open
// on lost external state — it is the absence of any evidence to block on. When a
// real circuit evaluator lands it replaces this default and MUST surface
// ErrCircuitUnavailable so an unreadable-but-wired circuit fails closed.
type ClosedCircuitStore struct{}

// NewClosedCircuitStore builds the foundation circuit gate that blocks nothing.
func NewClosedCircuitStore() *ClosedCircuitStore {
	return &ClosedCircuitStore{}
}

// SurfaceOpen reports every surface closed because no correlation evidence
// exists to open a circuit in this slice.
func (*ClosedCircuitStore) SurfaceOpen(context.Context, ports.CircuitSurface) (ports.CircuitState, error) {
	return ports.CircuitState{Open: false}, nil
}

var (
	_ ports.PrincipalStore = (*FailClosedPrincipalStore)(nil)
	_ ports.AdmissionStore = (*AlwaysAdmitStore)(nil)
	_ ports.ReplayStore    = (*MemoryReplayStore)(nil)
	_ ports.AccountStore   = (*MemoryAccountStore)(nil)
	_ ports.AccountStore   = (*UnavailableAccountStore)(nil)
	_ ports.AccountStore   = (*FileAccountStore)(nil)
	_ ports.CircuitStore   = (*ClosedCircuitStore)(nil)
)
