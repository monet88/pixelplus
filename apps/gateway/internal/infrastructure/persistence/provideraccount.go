// Package persistence owns physical durable state and atomic transitions.
package persistence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
		ports.AdmissionReservation(request),
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
	if update.RequireLifecycle != "" && existing.Lifecycle != update.RequireLifecycle {
		return domain.ProviderAccount{}, ports.ErrAccountUpdateConflict
	}
	if update.RequireControlsMatch && existing.Controls != update.RequireControls {
		return domain.ProviderAccount{}, ports.ErrAccountUpdateConflict
	}
	if update.RequireLastAllocatedVersionMatch && existing.Credential.LastAllocatedVersion != update.RequireLastAllocatedVersion {
		return domain.ProviderAccount{}, ports.ErrAccountUpdateConflict
	}

	if update.PatchLastProbedAt {
		existing.Credential.LastProbedAt = update.LastProbedAt
		existing.UpdatedAt = update.LastProbedAt
		// AccountStore never carries health authority.
		existing.Health = domain.HealthSummary{}
		existing.RecoveryPermit = domain.RecoveryPermit{}
		return existing, nil
	}
	if update.PatchLastAllocatedVersion {
		if update.LastAllocatedVersion <= existing.Credential.LastAllocatedVersion ||
			update.LastAllocatedVersion <= existing.Credential.Version {
			return domain.ProviderAccount{}, ports.ErrAccountUpdateConflict
		}
		existing.Credential.LastAllocatedVersion = update.LastAllocatedVersion
		existing.Health = domain.HealthSummary{}
		existing.RecoveryPermit = domain.RecoveryPermit{}
		return existing, nil
	}
	// Lifecycle/metadata only: strip any embedded health from the write payload.
	next := update.Account
	next.Health = domain.HealthSummary{}
	next.RecoveryPermit = domain.RecoveryPermit{}
	return next, nil
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
// Health and recovery permits are never retained on AccountStore rows.
func (store *MemoryAccountStore) Create(_ context.Context, creation ports.AccountCreation) (domain.ProviderAccount, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	tenant := creation.Principal.TenantID
	accounts, ok := store.byTenant[tenant]
	if !ok {
		accounts = make(map[domain.ProviderAccountID]domain.ProviderAccount)
		store.byTenant[tenant] = accounts
	}
	safe := creation.Account
	safe.Health = domain.HealthSummary{}
	safe.RecoveryPermit = domain.RecoveryPermit{}
	accounts[creation.Account.ID] = safe
	return safe, nil
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
// same-Tenant account rows to an append-only ledger under exclusive lock.
// Health conditions and recovery permits are owned by HealthStore, not this store.
type FileAccountStore struct {
	mu             sync.Mutex
	path           string
	lock           string
	legacySnapshot bool
	byTenant       map[domain.TenantID]map[domain.ProviderAccountID]domain.ProviderAccount
}

// NewFileAccountStore builds a file-backed foundation account store.
func NewFileAccountStore(path string) *FileAccountStore {
	return &FileAccountStore{
		path:     path,
		lock:     path + ".lock",
		byTenant: make(map[domain.TenantID]map[domain.ProviderAccountID]domain.ProviderAccount),
	}
}

func (store *FileAccountStore) acquireLock() (func(), error) {
	dir := filepath.Dir(store.lock)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, err
		}
	}
	file, err := os.OpenFile(store.lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("%w: account store exclusive lock held", ports.ErrDependencyUnavailable)
		}
		return nil, err
	}
	_, _ = file.WriteString("pixelplus-account-lock\n")
	if err := file.Close(); err != nil {
		_ = os.Remove(store.lock)
		return nil, err
	}
	return func() { _ = os.Remove(store.lock) }, nil
}

// Restore loads persisted account rows from the append-only ledger. A missing
// file is empty state; null, corrupt, or invalid rows fail closed so readiness
// cannot open over untrusted durability (health/cooldown spec §7.1-§7.2).
// Restore acquires the same O_EXCL exclusive lock as Read/writes so startup
// cannot observe a partial append under concurrent writers.
func (store *FileAccountStore) Restore(_ context.Context) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	unlock, err := store.acquireLock()
	if err != nil {
		return err
	}
	defer unlock()
	return store.reloadLocked()
}

// Create persists a new draft for the owning Tenant derived from the principal.
func (store *FileAccountStore) Create(ctx context.Context, creation ports.AccountCreation) (domain.ProviderAccount, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	unlock, err := store.acquireLock()
	if err != nil {
		return domain.ProviderAccount{}, err
	}
	defer unlock()
	if err := store.reloadLocked(); err != nil {
		return domain.ProviderAccount{}, err
	}
	return store.createLocked(ctx, creation)
}

func (store *FileAccountStore) createLocked(_ context.Context, creation ports.AccountCreation) (domain.ProviderAccount, error) {
	tenant := creation.Principal.TenantID
	accounts, ok := store.byTenant[tenant]
	if !ok {
		accounts = make(map[domain.ProviderAccountID]domain.ProviderAccount)
		store.byTenant[tenant] = accounts
	}
	// Persist lifecycle/metadata only; HealthStore owns conditions and permits.
	safe := creation.Account
	safe.Health = domain.HealthSummary{}
	safe.RecoveryPermit = domain.RecoveryPermit{}
	accounts[creation.Account.ID] = safe
	if err := store.appendAccountLocked(tenant, safe); err != nil {
		delete(accounts, creation.Account.ID)
		if len(accounts) == 0 {
			delete(store.byTenant, tenant)
		}
		return domain.ProviderAccount{}, err
	}
	return safe, nil
}

// Update persists a mutated account for the owning Tenant. It rejects an
// account that is not already visible under the principal's Tenant so a
// mutation can never create a cross-Tenant row or resurrect a deleted account
// (#6 section 5.1).
func (store *FileAccountStore) Update(_ context.Context, update ports.AccountUpdate) (domain.ProviderAccount, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	unlock, err := store.acquireLock()
	if err != nil {
		return domain.ProviderAccount{}, err
	}
	defer unlock()
	if err := store.reloadLocked(); err != nil {
		return domain.ProviderAccount{}, err
	}

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
	if err := store.appendAccountLocked(update.Principal.TenantID, result); err != nil {
		accounts[update.Account.ID] = existing
		return domain.ProviderAccount{}, err
	}
	return result, nil
}

// Visible returns the owning-Tenant account. Under exclusive lock + reload so
// another process's append is visible and partial writes are not observed.
func (store *FileAccountStore) Visible(_ context.Context, principal domain.SecurityPrincipal, id domain.ProviderAccountID) (domain.ProviderAccount, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	unlock, err := store.acquireLock()
	if err != nil {
		return domain.ProviderAccount{}, err
	}
	defer unlock()
	if err := store.reloadLocked(); err != nil {
		return domain.ProviderAccount{}, err
	}

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

// List returns only the authenticated Tenant's non-deleted accounts under
// exclusive lock + reload for cross-process freshness.
func (store *FileAccountStore) List(_ context.Context, principal domain.SecurityPrincipal) ([]domain.ProviderAccount, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	unlock, err := store.acquireLock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := store.reloadLocked(); err != nil {
		return nil, err
	}

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

// accountLedgerEntry is one append-only durable account projection. Health and
// recovery permits are owned by HealthStore; AccountStore rows carry lifecycle
// and credential metadata only for the logical port split (ADR 0009).
type accountLedgerEntry struct {
	TenantID domain.TenantID        `json:"tenant_id"`
	Account  domain.ProviderAccount `json:"account"`
}

func (store *FileAccountStore) reloadLocked() error {
	data, err := os.ReadFile(store.path)
	if err != nil {
		if os.IsNotExist(err) {
			store.legacySnapshot = false
			store.byTenant = make(map[domain.TenantID]map[domain.ProviderAccountID]domain.ProviderAccount)
			return nil
		}
		return err
	}
	if len(data) == 0 {
		store.legacySnapshot = false
		store.byTenant = make(map[domain.TenantID]map[domain.ProviderAccountID]domain.ProviderAccount)
		return nil
	}
	// Reject top-level JSON null so Restore fails closed instead of leaving a
	// nil map that panics on later Create/Update.
	trimmed := string(data)
	if trimmed == "null" || trimmed == "null\n" {
		return errors.New("account ledger: null root")
	}

	// Prefer append-only JSONL (Windows-safe repeated writes). Fall back to the
	// legacy single JSON object snapshot only when the whole file is a tenant map
	// (no per-line tenant_id ledger entries).
	if !looksLikeAccountJSONL(data) {
		var loaded map[domain.TenantID]map[domain.ProviderAccountID]domain.ProviderAccount
		if err := json.Unmarshal(data, &loaded); err != nil {
			return err
		}
		if loaded == nil {
			return errors.New("account ledger: null root object")
		}
		if err := validateAccountTree(loaded); err != nil {
			return err
		}
		// Strip health authority from legacy snapshots.
		for tenant, accounts := range loaded {
			for id, account := range accounts {
				account.Health = domain.HealthSummary{}
				account.RecoveryPermit = domain.RecoveryPermit{}
				accounts[id] = account
			}
			loaded[tenant] = accounts
		}
		store.byTenant = loaded
		store.legacySnapshot = true
		return nil
	}

	next := make(map[domain.TenantID]map[domain.ProviderAccountID]domain.ProviderAccount)
	offset := 0
	lineNo := 0
	for offset < len(data) {
		lineNo++
		end := offset
		for end < len(data) && data[end] != '\n' {
			end++
		}
		line := data[offset:end]
		if end < len(data) {
			end++
		}
		offset = end
		if len(line) == 0 {
			continue
		}
		if string(line) == "null" {
			return fmt.Errorf("account ledger line %d: null record", lineNo)
		}
		var entry accountLedgerEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return fmt.Errorf("account ledger line %d: %w", lineNo, err)
		}
		if err := validateAccountRow(entry.TenantID, entry.Account); err != nil {
			return fmt.Errorf("account ledger line %d: %w", lineNo, err)
		}
		accounts, ok := next[entry.TenantID]
		if !ok {
			accounts = make(map[domain.ProviderAccountID]domain.ProviderAccount)
			next[entry.TenantID] = accounts
		}
		// AccountStore no longer owns health authority; strip embedded health so
		// a legacy row cannot resurrect HealthStore state on restore.
		entry.Account.Health = domain.HealthSummary{}
		entry.Account.RecoveryPermit = domain.RecoveryPermit{}
		accounts[entry.Account.ID] = entry.Account
	}
	store.byTenant = next
	store.legacySnapshot = false
	return nil
}

// looksLikeAccountJSONL reports whether data is the append-only ledger format
// (lines of accountLedgerEntry) rather than a legacy whole-map snapshot.
func looksLikeAccountJSONL(data []byte) bool {
	// First non-empty line with a tenant_id field is the JSONL shape.
	offset := 0
	for offset < len(data) {
		end := offset
		for end < len(data) && data[end] != '\n' {
			end++
		}
		line := data[offset:end]
		if end < len(data) {
			end++
		}
		offset = end
		if len(line) == 0 {
			continue
		}
		var probe struct {
			TenantID string           `json:"tenant_id"`
			Account  *json.RawMessage `json:"account"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			return false
		}
		return probe.TenantID != "" && probe.Account != nil
	}
	return false
}

func (store *FileAccountStore) appendAccountLocked(tenant domain.TenantID, account domain.ProviderAccount) error {
	dir := filepath.Dir(store.path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	// A legacy whole-map snapshot must be rewritten before the first append.
	// Otherwise the ledger entry is concatenated to the legacy JSON document and
	// the next Restore fails closed on two adjacent top-level values.
	if store.legacySnapshot {
		return store.rewriteLedgerLocked()
	}
	// Append-only JSONL under process mutex. Unlike rename-over-destination this
	// is Windows-safe for repeated writes (no replace of an open destination).
	// Tradeoff: the ledger grows with each mutation; compaction is deferred.
	safe := account
	safe.Health = domain.HealthSummary{}
	safe.RecoveryPermit = domain.RecoveryPermit{}
	entry := accountLedgerEntry{TenantID: tenant, Account: safe}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(store.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	return file.Sync()
}

func (store *FileAccountStore) rewriteLedgerLocked() error {
	var data bytes.Buffer
	for tenant, accounts := range store.byTenant {
		for _, account := range accounts {
			safe := account
			safe.Health = domain.HealthSummary{}
			safe.RecoveryPermit = domain.RecoveryPermit{}
			encoded, err := json.Marshal(accountLedgerEntry{TenantID: tenant, Account: safe})
			if err != nil {
				return err
			}
			data.Write(encoded)
			data.WriteByte('\n')
		}
	}
	file, err := os.OpenFile(store.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	if _, err := file.Write(data.Bytes()); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	store.legacySnapshot = false
	return nil
}

func validateAccountTree(tree map[domain.TenantID]map[domain.ProviderAccountID]domain.ProviderAccount) error {
	if tree == nil {
		return errors.New("account ledger: nil tenant map")
	}
	for tenant, accounts := range tree {
		if tenant == "" {
			return errors.New("account ledger: empty tenant id")
		}
		if accounts == nil {
			return fmt.Errorf("account ledger: nil account map for tenant %q", tenant)
		}
		for id, account := range accounts {
			if err := validateAccountRow(tenant, account); err != nil {
				return err
			}
			if account.ID != id {
				return fmt.Errorf("account ledger: id mismatch %q != %q", account.ID, id)
			}
		}
	}
	return nil
}

func validateAccountRow(tenant domain.TenantID, account domain.ProviderAccount) error {
	if tenant == "" {
		return errors.New("missing tenant_id")
	}
	if account.ID == "" {
		return errors.New("missing account id")
	}
	if !account.Provider.Valid() {
		return errors.New("invalid provider")
	}
	if !account.AuthMode.Valid() {
		return errors.New("invalid auth_mode")
	}
	switch account.Lifecycle {
	case domain.LifecycleDraft, domain.LifecyclePendingValidation, domain.LifecyclePendingProbe,
		domain.LifecycleActive, domain.LifecycleDisabled, domain.LifecycleReauthRequired,
		domain.LifecycleRevoked, domain.LifecycleDeleted:
	default:
		return fmt.Errorf("invalid lifecycle %q", account.Lifecycle)
	}
	switch account.Controls.Drain {
	case domain.DrainOff, domain.DrainDraining, "":
		// Empty drain is treated as off for legacy rows that pre-date explicit controls.
		if account.Controls.Drain == "" {
			// Documented backward compatibility: missing drain maps to off.
		}
	default:
		return fmt.Errorf("invalid drain control %q", account.Controls.Drain)
	}
	switch account.Controls.Quarantine {
	case domain.QuarantineOff, domain.QuarantineQuarantined, "":
	default:
		return fmt.Errorf("invalid quarantine control %q", account.Controls.Quarantine)
	}
	if account.Credential.Version < 0 || account.Credential.LastAllocatedVersion < 0 {
		return errors.New("negative credential version")
	}
	if account.Credential.LastAllocatedVersion < account.Credential.Version {
		return errors.New("last_allocated_version must be >= credential version")
	}
	if account.PendingCredentialVersion < 0 {
		return errors.New("negative pending credential version")
	}
	if account.PendingCredentialVersion > 0 {
		// Pending is strictly newer than the current usable version (when one exists)
		// and never invents a version beyond LastAllocatedVersion.
		if account.Credential.Version != 0 && account.PendingCredentialVersion <= account.Credential.Version {
			return errors.New("pending credential version must exceed current version")
		}
		if account.PendingCredentialVersion > account.Credential.LastAllocatedVersion {
			return errors.New("pending credential version exceeds last allocated")
		}
		switch account.PendingOrigin {
		case domain.LifecycleDraft, domain.LifecycleActive, domain.LifecycleDisabled, domain.LifecycleRevoked,
			domain.LifecyclePendingValidation, domain.LifecyclePendingProbe, domain.LifecycleReauthRequired:
		case "":
			return errors.New("pending origin required when pending credential version is set")
		default:
			return fmt.Errorf("invalid pending origin lifecycle %q", account.PendingOrigin)
		}
	} else if account.PendingOrigin != "" {
		return errors.New("pending origin allowed only when pending credential version exists")
	}
	// ActiveOAuth marker must not pair with a settled non-pending lifecycle without
	// a journey — empty is always OK; non-empty requires a restorable in-flight state.
	if account.ActiveOAuthAuthorizationID != "" {
		switch account.Lifecycle {
		case domain.LifecycleDraft, domain.LifecyclePendingValidation, domain.LifecyclePendingProbe,
			domain.LifecycleDisabled, domain.LifecycleReauthRequired, domain.LifecycleActive:
		default:
			return fmt.Errorf("active oauth marker inconsistent with lifecycle %q", account.Lifecycle)
		}
	}
	if account.CreatedAt.IsZero() {
		return errors.New("created_at required")
	}
	if account.UpdatedAt.IsZero() {
		return errors.New("updated_at required")
	}
	if account.UpdatedAt.Time().Before(account.CreatedAt.Time()) {
		return errors.New("updated_at before created_at")
	}
	if !account.Credential.LastValidatedAt.IsZero() && account.Credential.LastValidatedAt.Time().Before(account.CreatedAt.Time()) {
		return errors.New("last_validated_at before created_at")
	}
	if !account.Credential.LastProbedAt.IsZero() && account.Credential.LastProbedAt.Time().Before(account.CreatedAt.Time()) {
		return errors.New("last_probed_at before created_at")
	}
	// Credential class is derived from AuthMode when set via lifecycle; no free-form class on the row.
	return nil
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
