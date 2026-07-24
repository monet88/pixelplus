package persistence

import (
	"bufio"
	"context"
	"encoding/json"
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

// FileRoutingPolicyStore is a durable Tenant singleton Routing Policy store.
// Replace rewrites the snapshot under an exclusive lock so a concurrent
// reader never observes a partial policy.
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
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var record routingPolicyRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return fmt.Errorf("%w: invalid routing policy record", ports.ErrDependencyUnavailable)
		}
		if record.TenantID == "" {
			return fmt.Errorf("%w: routing policy record missing tenant", ports.ErrDependencyUnavailable)
		}
		next[record.TenantID] = cloneRoutingPolicy(record.Policy)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	store.byTenant = next
	return nil
}

func (store *FileRoutingPolicyStore) persistLocked() error {
	dir := filepath.Dir(store.path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	temp := store.path + ".tmp"
	file, err := os.OpenFile(temp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	for tenant, policy := range store.byTenant {
		if err := encoder.Encode(routingPolicyRecord{TenantID: tenant, Policy: policy}); err != nil {
			_ = file.Close()
			_ = os.Remove(temp)
			return err
		}
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temp)
		return err
	}
	if err := os.Rename(temp, store.path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
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

// Replace atomically overwrites the Tenant singleton on disk.
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
	policy := cloneRoutingPolicy(change.Policy)
	store.byTenant[change.Principal.TenantID] = policy
	if err := store.persistLocked(); err != nil {
		return domain.RoutingPolicy{}, err
	}
	return cloneRoutingPolicy(policy), nil
}

var _ ports.RoutingPolicyStore = (*FileRoutingPolicyStore)(nil)
