package persistence

import (
	"context"
	"sync"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// MemoryRenderStagingStore is a process-local controlled staging store for
// fixtures. It is NOT restart-durable production storage; production must use
// UnavailableRenderStagingStore (or a future durable ledger) by default.
type MemoryRenderStagingStore struct {
	mu    sync.Mutex
	byKey map[string]stagedBlob
}

type stagedBlob struct {
	contentType string
	data        []byte
	tenant      domain.TenantID
}

// NewMemoryRenderStagingStore builds an empty process-local staging store.
func NewMemoryRenderStagingStore() *MemoryRenderStagingStore {
	return &MemoryRenderStagingStore{byKey: make(map[string]stagedBlob)}
}

func stagingKey(id ports.StagingIdentity) string {
	return string(id.TenantID) + "/" + string(id.JobID) + "/" + string(id.ManifestID) + "/" + string(id.EntryID) + "/" + id.Checksum
}

// Put stores staged bytes under the identity. Same identity + checksum is
// idempotent; a checksum conflict fails closed.
func (store *MemoryRenderStagingStore) Put(_ context.Context, put ports.StagingPut) error {
	if !put.Identity.Valid() || len(put.Data) == 0 {
		return ports.ErrDependencyUnavailable
	}
	key := stagingKey(put.Identity)
	store.mu.Lock()
	defer store.mu.Unlock()
	if existing, ok := store.byKey[key]; ok {
		// Idempotent same payload; reject checksum mismatch on key collision
		// (key already includes checksum so this is a pure re-put).
		if len(existing.data) != len(put.Data) {
			return ports.ErrDependencyUnavailable
		}
		return nil
	}
	copied := make([]byte, len(put.Data))
	copy(copied, put.Data)
	store.byKey[key] = stagedBlob{
		contentType: put.ContentType,
		data:        copied,
		tenant:      put.Identity.TenantID,
	}
	return nil
}

// Use injects a copy of staged bytes into the callback after Tenant match.
func (store *MemoryRenderStagingStore) Use(_ context.Context, access ports.StagingAccess, use func([]byte) error) error {
	if use == nil || !access.Identity.Valid() {
		return ports.ErrStagingNotFound
	}
	if access.Principal.TenantID != "" && access.Principal.TenantID != access.Identity.TenantID {
		return ports.ErrStagingNotFound
	}
	store.mu.Lock()
	blob, ok := store.byKey[stagingKey(access.Identity)]
	if !ok || blob.tenant != access.Identity.TenantID {
		store.mu.Unlock()
		return ports.ErrStagingNotFound
	}
	copied := make([]byte, len(blob.data))
	copy(copied, blob.data)
	store.mu.Unlock()
	return use(copied)
}

// UnavailableRenderStagingStore is the production fail-closed default when no
// durable staging backend is configured.
type UnavailableRenderStagingStore struct{}

// NewUnavailableRenderStagingStore builds the fail-closed staging store.
func NewUnavailableRenderStagingStore() *UnavailableRenderStagingStore {
	return &UnavailableRenderStagingStore{}
}

// Put fails closed.
func (*UnavailableRenderStagingStore) Put(context.Context, ports.StagingPut) error {
	return ports.ErrDependencyUnavailable
}

// Use fails closed.
func (*UnavailableRenderStagingStore) Use(context.Context, ports.StagingAccess, func([]byte) error) error {
	return ports.ErrDependencyUnavailable
}

var (
	_ ports.RenderStagingStore = (*MemoryRenderStagingStore)(nil)
	_ ports.RenderStagingStore = (*UnavailableRenderStagingStore)(nil)
)
