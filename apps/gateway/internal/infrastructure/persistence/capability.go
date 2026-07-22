package persistence

import (
	"context"
	"sync"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// MemoryCapabilityStore is the production foundation Capability Snapshot store
// for local composition and contract tests. Snapshots are Tenant-partitioned
// and never cross ownership boundaries.
type MemoryCapabilityStore struct {
	mu       sync.Mutex
	byTenant map[domain.TenantID]map[domain.ProviderAccountID]domain.CapabilitySnapshot
}

// NewMemoryCapabilityStore builds an empty in-memory capability store.
func NewMemoryCapabilityStore() *MemoryCapabilityStore {
	return &MemoryCapabilityStore{
		byTenant: make(map[domain.TenantID]map[domain.ProviderAccountID]domain.CapabilitySnapshot),
	}
}

// Get returns the Tenant-owned snapshot for the account, or not-found.
func (store *MemoryCapabilityStore) Get(_ context.Context, principal domain.SecurityPrincipal, accountID domain.ProviderAccountID) (domain.CapabilitySnapshot, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	accounts, ok := store.byTenant[principal.TenantID]
	if !ok {
		return domain.CapabilitySnapshot{}, ports.ErrCapabilitySnapshotNotFound
	}
	snapshot, ok := accounts[accountID]
	if !ok {
		return domain.CapabilitySnapshot{}, ports.ErrCapabilitySnapshotNotFound
	}
	return snapshot, nil
}

// List returns every Tenant-owned snapshot. Foreign Tenants are never visible.
func (store *MemoryCapabilityStore) List(_ context.Context, principal domain.SecurityPrincipal) ([]domain.CapabilitySnapshot, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	accounts := store.byTenant[principal.TenantID]
	result := make([]domain.CapabilitySnapshot, 0, len(accounts))
	for _, snapshot := range accounts {
		result = append(result, snapshot)
	}
	return result, nil
}

// Put replaces the Tenant-owned snapshot for the account.
func (store *MemoryCapabilityStore) Put(_ context.Context, principal domain.SecurityPrincipal, snapshot domain.CapabilitySnapshot) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	accounts, ok := store.byTenant[principal.TenantID]
	if !ok {
		accounts = make(map[domain.ProviderAccountID]domain.CapabilitySnapshot)
		store.byTenant[principal.TenantID] = accounts
	}
	accounts[snapshot.ProviderAccountID] = snapshot
	return nil
}

var _ ports.CapabilityStore = (*MemoryCapabilityStore)(nil)
