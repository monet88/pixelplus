package persistence

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// MemoryRoutingPolicyStore is the in-process foundation Routing Policy store.
// Each Tenant has at most one singleton policy; Replace is one locked write.
type MemoryRoutingPolicyStore struct {
	mu        sync.Mutex
	byTenant  map[domain.TenantID]domain.RoutingPolicy
	mutations atomic.Int64
	revision  atomic.Int64
}

// NewMemoryRoutingPolicyStore builds an empty in-memory routing policy store.
func NewMemoryRoutingPolicyStore() *MemoryRoutingPolicyStore {
	return &MemoryRoutingPolicyStore{
		byTenant: make(map[domain.TenantID]domain.RoutingPolicy),
	}
}

// Mutations returns how many successful Replace operations have committed.
// Contract fixtures use this to prove zero mutation on rejected writes.
func (store *MemoryRoutingPolicyStore) Mutations() int64 {
	return store.mutations.Load()
}

// Revision returns a monotonic counter advanced on every successful Replace.
func (store *MemoryRoutingPolicyStore) Revision() int64 {
	return store.revision.Load()
}

// Read returns the Tenant singleton or ErrRoutingPolicyNotFound.
func (store *MemoryRoutingPolicyStore) Read(_ context.Context, principal domain.SecurityPrincipal) (domain.RoutingPolicy, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	policy, ok := store.byTenant[principal.TenantID]
	if !ok {
		return domain.RoutingPolicy{}, ports.ErrRoutingPolicyNotFound
	}
	return cloneRoutingPolicy(policy), nil
}

// Replace atomically overwrites the Tenant singleton.
func (store *MemoryRoutingPolicyStore) Replace(_ context.Context, change ports.RoutingPolicyChange) (domain.RoutingPolicy, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	policy := cloneRoutingPolicy(change.Policy)
	store.byTenant[change.Principal.TenantID] = policy
	store.mutations.Add(1)
	store.revision.Add(1)
	return cloneRoutingPolicy(policy), nil
}

// Seed installs a policy for independently controlled fixtures.
func (store *MemoryRoutingPolicyStore) Seed(tenant domain.TenantID, policy domain.RoutingPolicy) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.byTenant[tenant] = cloneRoutingPolicy(policy)
}

func cloneRoutingPolicy(policy domain.RoutingPolicy) domain.RoutingPolicy {
	out := policy
	if policy.CandidateAccounts != nil {
		out.CandidateAccounts = append([]domain.ProviderAccountID(nil), policy.CandidateAccounts...)
	} else {
		out.CandidateAccounts = []domain.ProviderAccountID{}
	}
	if policy.SelectionOrder != nil {
		out.SelectionOrder = append([]domain.ProviderAccountID(nil), policy.SelectionOrder...)
	} else {
		out.SelectionOrder = []domain.ProviderAccountID{}
	}
	if policy.FallbackChain != nil {
		out.FallbackChain = append([]domain.ProviderAccountID(nil), policy.FallbackChain...)
	} else {
		out.FallbackChain = []domain.ProviderAccountID{}
	}
	if policy.FallbackAuthModes != nil {
		out.FallbackAuthModes = append([]domain.AuthMode(nil), policy.FallbackAuthModes...)
	} else {
		out.FallbackAuthModes = []domain.AuthMode{}
	}
	if policy.LeasePolicy.EligibleUnits != nil {
		out.LeasePolicy.EligibleUnits = append([]domain.LeaseUnit(nil), policy.LeasePolicy.EligibleUnits...)
	} else {
		out.LeasePolicy.EligibleUnits = []domain.LeaseUnit{}
	}
	return out
}

var _ ports.RoutingPolicyStore = (*MemoryRoutingPolicyStore)(nil)

// UnavailableRoutingPolicyStore is installed after startup restoration fails.
// Every read/replace returns ErrDependencyUnavailable so product traffic cannot
// observe an empty partial store as "no policy" (fail-closed, same posture as
// UnavailableAccountStore / UnavailableHealthStore).
type UnavailableRoutingPolicyStore struct{}

// NewUnavailableRoutingPolicyStore builds the fail-closed substitute.
func NewUnavailableRoutingPolicyStore() *UnavailableRoutingPolicyStore {
	return &UnavailableRoutingPolicyStore{}
}

// Read always fails closed.
func (*UnavailableRoutingPolicyStore) Read(context.Context, domain.SecurityPrincipal) (domain.RoutingPolicy, error) {
	return domain.RoutingPolicy{}, ports.ErrDependencyUnavailable
}

// Replace always fails closed and never mutates durable state.
func (*UnavailableRoutingPolicyStore) Replace(context.Context, ports.RoutingPolicyChange) (domain.RoutingPolicy, error) {
	return domain.RoutingPolicy{}, ports.ErrDependencyUnavailable
}

var _ ports.RoutingPolicyStore = (*UnavailableRoutingPolicyStore)(nil)

// FileRoutingPolicyStore is a durable Tenant singleton Routing Policy store.
// Durability uses append-only JSONL under an exclusive O_EXCL lock (same
// Windows-safe pattern as FileAccountStore). Restore/Read/Replace reload the
// ledger and apply latest-row-wins per Tenant. Compaction is deferred.
type FileRoutingPolicyStore struct {
	mu       sync.Mutex
	path     string
	lock     string
	byTenant map[domain.TenantID]domain.RoutingPolicy
}

type routingPolicyRecord struct {
	TenantID domain.TenantID      `json:"tenant_id"`
	Policy   domain.RoutingPolicy `json:"policy"`
}

// NewFileRoutingPolicyStore builds a file-backed routing policy store.
func NewFileRoutingPolicyStore(path string) *FileRoutingPolicyStore {
	return &FileRoutingPolicyStore{
		path:     path,
		lock:     path + ".lock",
		byTenant: make(map[domain.TenantID]domain.RoutingPolicy),
	}
}

func (store *FileRoutingPolicyStore) acquireLock() (func(), error) {
	dir := filepath.Dir(store.lock)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, err
		}
	}
	file, err := os.OpenFile(store.lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("%w: routing policy store exclusive lock held", ports.ErrDependencyUnavailable)
		}
		return nil, err
	}
	_, _ = file.WriteString("pixelplus-routing-policy-lock\n")
	if err := file.Close(); err != nil {
		_ = os.Remove(store.lock)
		return nil, err
	}
	return func() { _ = os.Remove(store.lock) }, nil
}

// Restore loads persisted policies. Missing file is empty; corrupt rows fail closed.
func (store *FileRoutingPolicyStore) Restore(_ context.Context) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	unlock, err := store.acquireLock()
	if err != nil {
		return err
	}
	defer unlock()
	return store.reloadLocked()
}

func (store *FileRoutingPolicyStore) reloadLocked() error {
	file, err := os.Open(store.path)
	if err != nil {
		if os.IsNotExist(err) {
			store.byTenant = make(map[domain.TenantID]domain.RoutingPolicy)
			return nil
		}
		return err
	}
	defer file.Close()

	next := make(map[domain.TenantID]domain.RoutingPolicy)
	scanner := bufio.NewScanner(file)
	// Policies can be moderately large; allow multi-MiB lines.
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		if string(line) == "null" {
			return fmt.Errorf("%w: routing policy line %d: null record", ports.ErrDependencyUnavailable, lineNo)
		}
		var record routingPolicyRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return fmt.Errorf("%w: routing policy line %d: invalid json", ports.ErrDependencyUnavailable, lineNo)
		}
		if record.TenantID == "" {
			return fmt.Errorf("%w: routing policy line %d: missing tenant", ports.ErrDependencyUnavailable, lineNo)
		}
		// Reject JSON null policy objects (unmarshal to zero value without audit).
		if err := validateDurableRoutingPolicy(record.Policy); err != nil {
			return fmt.Errorf("%w: routing policy line %d: %v", ports.ErrDependencyUnavailable, lineNo, err)
		}
		// Latest-row semantics for the same Tenant, matching FileAccountStore
		// append-only ledger replay (later line wins for the same key).
		next[record.TenantID] = cloneRoutingPolicy(record.Policy)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	store.byTenant = next
	return nil
}

// validateDurableRoutingPolicy fails closed on semantically invalid persisted
// rows so readiness never opens over untrusted durability (same posture as
// validateAccountRow / health ledger validation).
func validateDurableRoutingPolicy(policy domain.RoutingPolicy) error {
	if policy.UpdatedBy == "" {
		return errors.New("missing updated_by")
	}
	if policy.UpdatedAt.IsZero() {
		return errors.New("missing updated_at")
	}

	candidates, err := uniqueValidAccountIDs(policy.CandidateAccounts)
	if err != nil {
		return err
	}
	selection, err := uniqueValidAccountIDs(policy.SelectionOrder)
	if err != nil {
		return err
	}
	chain, err := uniqueValidAccountIDs(policy.FallbackChain)
	if err != nil {
		return err
	}
	if !subsetOf(selection, candidates) {
		return errors.New("selection_order not subset of candidate_accounts")
	}
	if !subsetOf(chain, candidates) {
		return errors.New("fallback_chain not subset of candidate_accounts")
	}
	if !policy.FallbackEnabled {
		if len(chain) > 0 || len(policy.FallbackAuthModes) > 0 {
			return errors.New("fallback disabled with chain or modes")
		}
	} else if len(chain) == 0 {
		// routing spec §8.1: fallback_chain when fallback_enabled.
		return errors.New("fallback enabled without chain")
	}

	seenModes := make(map[domain.AuthMode]struct{}, len(policy.FallbackAuthModes))
	for _, mode := range policy.FallbackAuthModes {
		if !mode.Valid() {
			return errors.New("invalid fallback auth mode")
		}
		if _, ok := seenModes[mode]; ok {
			return errors.New("duplicate fallback auth mode")
		}
		seenModes[mode] = struct{}{}
	}
	seenUnits := make(map[domain.LeaseUnit]struct{}, len(policy.LeasePolicy.EligibleUnits))
	for _, unit := range policy.LeasePolicy.EligibleUnits {
		if !unit.Valid() {
			return errors.New("invalid lease unit")
		}
		if _, ok := seenUnits[unit]; ok {
			return errors.New("duplicate lease unit")
		}
		seenUnits[unit] = struct{}{}
	}
	return nil
}

func uniqueValidAccountIDs(ids []domain.ProviderAccountID) ([]domain.ProviderAccountID, error) {
	seen := make(map[domain.ProviderAccountID]struct{}, len(ids))
	out := make([]domain.ProviderAccountID, 0, len(ids))
	for _, id := range ids {
		if !id.Valid() {
			return nil, errors.New("invalid provider_account_id")
		}
		if _, ok := seen[id]; ok {
			return nil, errors.New("duplicate provider_account_id")
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

func subsetOf(subset, universe []domain.ProviderAccountID) bool {
	index := make(map[domain.ProviderAccountID]struct{}, len(universe))
	for _, id := range universe {
		index[id] = struct{}{}
	}
	for _, id := range subset {
		if _, ok := index[id]; !ok {
			return false
		}
	}
	return true
}

// appendPolicyLocked appends one JSONL record for the Tenant singleton.
// Append-only is Windows-safe for repeated writes (no rename-over open
// destination). Tradeoff: the ledger grows with each Replace; compaction is
// deferred (FileAccountStore pattern).
func (store *FileRoutingPolicyStore) appendPolicyLocked(tenant domain.TenantID, policy domain.RoutingPolicy) error {
	dir := filepath.Dir(store.path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	entry := routingPolicyRecord{TenantID: tenant, Policy: cloneRoutingPolicy(policy)}
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

// Read returns the Tenant singleton or ErrRoutingPolicyNotFound.
func (store *FileRoutingPolicyStore) Read(_ context.Context, principal domain.SecurityPrincipal) (domain.RoutingPolicy, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	unlock, err := store.acquireLock()
	if err != nil {
		return domain.RoutingPolicy{}, err
	}
	defer unlock()
	if err := store.reloadLocked(); err != nil {
		return domain.RoutingPolicy{}, err
	}
	policy, ok := store.byTenant[principal.TenantID]
	if !ok {
		return domain.RoutingPolicy{}, ports.ErrRoutingPolicyNotFound
	}
	return cloneRoutingPolicy(policy), nil
}

// Replace appends one ledger line for the Tenant singleton under exclusive
// lock after a fresh reload. In-memory map update is rolled back if the append
// fails so process state never diverges from durable failure.
func (store *FileRoutingPolicyStore) Replace(_ context.Context, change ports.RoutingPolicyChange) (domain.RoutingPolicy, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	unlock, err := store.acquireLock()
	if err != nil {
		return domain.RoutingPolicy{}, err
	}
	defer unlock()
	if err := store.reloadLocked(); err != nil {
		return domain.RoutingPolicy{}, err
	}
	tenant := change.Principal.TenantID
	prior, hadPrior := store.byTenant[tenant]
	policy := cloneRoutingPolicy(change.Policy)
	store.byTenant[tenant] = policy
	if err := store.appendPolicyLocked(tenant, policy); err != nil {
		if hadPrior {
			store.byTenant[tenant] = prior
		} else {
			delete(store.byTenant, tenant)
		}
		return domain.RoutingPolicy{}, err
	}
	return cloneRoutingPolicy(policy), nil
}

var _ ports.RoutingPolicyStore = (*FileRoutingPolicyStore)(nil)
