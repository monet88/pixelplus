// Package persistence owns physical durable state and atomic transitions.
package persistence

import (
	"context"
	"sync"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// Foundation Tenant storage caps. These are the MVP defaults for
// L-TENANT-ASSET-BYTES and L-TENANT-ASSET-COUNT; #17 may retune the numbers,
// not the bounded-cap obligation (#13 section 6). They are constructor
// overridable so a test can drive the cap edge without uploading gigabytes.
const (
	// DefaultTenantAssetBytes is the committed + reserved byte ceiling per
	// Tenant (5 GiB).
	DefaultTenantAssetBytes int64 = 5 << 30
	// DefaultTenantAssetCount is the committed + reserved object-count ceiling
	// per Tenant.
	DefaultTenantAssetCount int = 10000
)

// tenantStorage tracks one Tenant's atomic committed + reserved accounting. A
// reservation is held in reserved* until it is either committed (moved to
// committed*) or released (subtracted) exactly once, so the store never admits
// storage by forgetting an uncertain hold (#13 section 6.1).
type tenantStorage struct {
	committedBytes int64
	reservedBytes  int64
	committedCount int
	reservedCount  int
	assets         map[domain.AssetID]domain.Asset
}

// MemoryAssetMetadataStore is the production foundation Asset metadata store. It
// owns durable metadata, atomic committed + reserved storage reservation,
// same-Tenant non-enumerating visibility, and one-time accounting release on
// deletion or expiry. All state lives under one mutex so a reservation decision
// and its accounting effect are atomic (#13 section 6.1). Expiry is evaluated
// against the injected clock so an expired Asset is invisible without a
// separate sweep.
type MemoryAssetMetadataStore struct {
	clock    ports.Clock
	capBytes int64
	capCount int

	mu       sync.Mutex
	byTenant map[domain.TenantID]*tenantStorage
}

// NewMemoryAssetMetadataStore builds an empty foundation metadata store with
// the MVP Tenant caps.
func NewMemoryAssetMetadataStore(clock ports.Clock) *MemoryAssetMetadataStore {
	return NewMemoryAssetMetadataStoreWithCaps(clock, DefaultTenantAssetBytes, DefaultTenantAssetCount)
}

// NewMemoryAssetMetadataStoreWithCaps builds a foundation metadata store with
// explicit caps. It exists so a test can exercise the cap edge deterministically
// without materializing cap-sized uploads.
func NewMemoryAssetMetadataStoreWithCaps(clock ports.Clock, capBytes int64, capCount int) *MemoryAssetMetadataStore {
	return &MemoryAssetMetadataStore{
		clock:    clock,
		capBytes: capBytes,
		capCount: capCount,
		byTenant: make(map[domain.TenantID]*tenantStorage),
	}
}

func (store *MemoryAssetMetadataStore) tenant(id domain.TenantID) *tenantStorage {
	state, ok := store.byTenant[id]
	if !ok {
		state = &tenantStorage{assets: make(map[domain.AssetID]domain.Asset)}
		store.byTenant[id] = state
	}
	return state
}

// Reserve acquires an atomic committed + reserved hold or fails closed with
// ErrStorageCapExceeded. It never admits an overrun and it counts the pending
// object so the object-count cap is enforced before any durable Asset exists.
func (store *MemoryAssetMetadataStore) Reserve(_ context.Context, reservation ports.AssetReservation) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	state := store.tenant(reservation.TenantID)
	if state.committedBytes+state.reservedBytes+reservation.Bytes > store.capBytes {
		return ports.ErrStorageCapExceeded
	}
	if state.committedCount+state.reservedCount+1 > store.capCount {
		return ports.ErrStorageCapExceeded
	}
	state.reservedBytes += reservation.Bytes
	state.reservedCount++
	return nil
}

// Commit converts a prior hold to committed usage exactly once and persists the
// immutable Asset for the owning Tenant.
func (store *MemoryAssetMetadataStore) Commit(_ context.Context, creation ports.AssetCreation) (domain.Asset, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	state := store.tenant(creation.Principal.TenantID)
	store.consumeHold(state, creation.Reservation.Bytes)
	state.committedBytes += creation.Reservation.Bytes
	state.committedCount++
	state.assets[creation.Asset.ID] = creation.Asset
	return creation.Asset, nil
}

// Release settles an un-committed hold exactly once. It is a no-op past the
// point where the hold was already consumed so a double release cannot drive
// the accounting negative.
func (store *MemoryAssetMetadataStore) Release(_ context.Context, reservation ports.AssetReservation) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	state, ok := store.byTenant[reservation.TenantID]
	if !ok {
		return nil
	}
	store.consumeHold(state, reservation.Bytes)
	return nil
}

// consumeHold subtracts one held reservation from the reserved accounting,
// clamping at zero so a redundant release is inert.
func (store *MemoryAssetMetadataStore) consumeHold(state *tenantStorage, bytes int64) {
	if state.reservedCount == 0 {
		return
	}
	state.reservedBytes -= bytes
	if state.reservedBytes < 0 {
		state.reservedBytes = 0
	}
	state.reservedCount--
}

// Visible returns the owning-Tenant Asset or the single non-enumerating
// visibility failure. Foreign, unknown, expired, and deleted identifiers all
// return ErrAssetNotVisible so the outcome is indistinguishable (#13 sections
// 4.5, 5.5, 8).
func (store *MemoryAssetMetadataStore) Visible(_ context.Context, principal domain.SecurityPrincipal, id domain.AssetID) (domain.Asset, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	state, ok := store.byTenant[principal.TenantID]
	if !ok {
		return domain.Asset{}, ports.ErrAssetNotVisible
	}
	asset, ok := state.assets[id]
	if !ok || !asset.Retrievable(store.clock.Now()) {
		return domain.Asset{}, ports.ErrAssetNotVisible
	}
	return asset, nil
}

// Delete stamps a committed Asset deleted and releases its committed accounting
// exactly once, leaving a bytes-free tombstone so the id resolves to the same
// non-enumerating not-found as an unknown id while its headroom is reclaimed
// (#13 sections 5.3-5.5). A repeated delete is inert. There is no public delete
// route in the frozen v1 contract, so this lifecycle transition is proven at
// the store seam rather than the wire.
func (store *MemoryAssetMetadataStore) Delete(_ context.Context, principal domain.SecurityPrincipal, id domain.AssetID) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	state, ok := store.byTenant[principal.TenantID]
	if !ok {
		return ports.ErrAssetNotVisible
	}
	asset, ok := state.assets[id]
	if !ok {
		return ports.ErrAssetNotVisible
	}
	if !asset.DeletedAt.IsZero() {
		return nil
	}
	asset.DeletedAt = domain.NewTimestamp(store.clock.Now())
	state.assets[id] = asset
	store.releaseCommitted(state, asset.ByteSize)
	return nil
}

// releaseCommitted subtracts one committed Asset from the committed accounting,
// clamping at zero so a double release cannot drive the totals negative.
func (store *MemoryAssetMetadataStore) releaseCommitted(state *tenantStorage, bytes int64) {
	if state.committedCount == 0 {
		return
	}
	state.committedBytes -= bytes
	if state.committedBytes < 0 {
		state.committedBytes = 0
	}
	state.committedCount--
}

// MemoryAssetContentStore is the production foundation Asset content store. It
// keeps immutable bytes keyed by Asset id. Fetch is the second gate behind the
// metadata authority: the application resolves same-Tenant visibility first, so
// a foreign, unknown, expired, or deleted id never reaches this store; an
// id with no stored bytes still returns the non-enumerating ErrAssetNotVisible.
type MemoryAssetContentStore struct {
	mu    sync.Mutex
	bytes map[domain.AssetID]storedContent
}

type storedContent struct {
	contentType string
	data        []byte
}

// NewMemoryAssetContentStore builds an empty foundation content store.
func NewMemoryAssetContentStore() *MemoryAssetContentStore {
	return &MemoryAssetContentStore{bytes: make(map[domain.AssetID]storedContent)}
}

// Put stores the immutable bytes for a committed Asset. The content type is
// sniffed from the payload so a later Fetch can label the download without a
// second metadata read.
func (store *MemoryAssetContentStore) Put(_ context.Context, id domain.AssetID, data []byte) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	copied := make([]byte, len(data))
	copy(copied, data)
	store.bytes[id] = storedContent{contentType: domain.SniffImageType(copied), data: copied}
	return nil
}

// Fetch returns the stored bytes for an Asset id. It is reached only after the
// metadata store authorized same-Tenant, still-retrievable access, so a missing
// id here is a fail-closed non-enumerating outcome.
func (store *MemoryAssetContentStore) Fetch(_ context.Context, _ domain.SecurityPrincipal, id domain.AssetID) (ports.AssetContent, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	stored, ok := store.bytes[id]
	if !ok {
		return ports.AssetContent{}, ports.ErrAssetNotVisible
	}
	data := make([]byte, len(stored.data))
	copy(data, stored.data)
	return ports.AssetContent{ContentType: stored.contentType, Data: data}, nil
}

// MemoryAssetReplayStore is the production foundation Asset idempotency store.
// It performs an atomic claim, fingerprint match, terminal replay, and
// owner-only abandon under a single mutex so exactly one concurrent matching
// upload becomes the executor and a terminal record replays the original Asset
// without a new side effect (#20 section 5.5).
type MemoryAssetReplayStore struct {
	mu      sync.Mutex
	records map[domain.ReplayScope]*assetReplayRecord
}

type assetReplayRecord struct {
	fingerprint domain.Fingerprint
	terminal    bool
	asset       domain.Asset
}

// NewMemoryAssetReplayStore builds an empty foundation replay store.
func NewMemoryAssetReplayStore() *MemoryAssetReplayStore {
	return &MemoryAssetReplayStore{records: make(map[domain.ReplayScope]*assetReplayRecord)}
}

// Claim atomically binds the scope+key to the fingerprint or resolves a repeat.
func (store *MemoryAssetReplayStore) Claim(_ context.Context, identity domain.ReplayIdentity) (ports.AssetReplayDecision, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	existing, ok := store.records[identity.Scope]
	if !ok {
		store.records[identity.Scope] = &assetReplayRecord{fingerprint: identity.Fingerprint}
		return ports.AssetReplayDecision{Outcome: ports.ReplayClaimed}, nil
	}
	if existing.fingerprint != identity.Fingerprint {
		return ports.AssetReplayDecision{Outcome: ports.ReplayConflict}, nil
	}
	if existing.terminal {
		return ports.AssetReplayDecision{Outcome: ports.ReplayTerminal, TerminalAsset: existing.asset}, nil
	}
	return ports.AssetReplayDecision{Outcome: ports.ReplayInProgress}, nil
}

// Complete records the terminal Asset so later matching replays are stable.
func (store *MemoryAssetReplayStore) Complete(_ context.Context, identity domain.ReplayIdentity, result ports.AssetReplayResult) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	record, ok := store.records[identity.Scope]
	if !ok {
		record = &assetReplayRecord{fingerprint: identity.Fingerprint}
		store.records[identity.Scope] = record
	}
	record.terminal = true
	record.asset = result.Asset
	return nil
}

// Abandon clears an in-progress claim still owned by this request so a later
// retry can re-claim the scoped key. It never removes a terminal record.
func (store *MemoryAssetReplayStore) Abandon(_ context.Context, identity domain.ReplayIdentity) error {
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

var (
	_ ports.AssetMetadataStore = (*MemoryAssetMetadataStore)(nil)
	_ ports.AssetContentStore  = (*MemoryAssetContentStore)(nil)
	_ ports.AssetReplayStore   = (*MemoryAssetReplayStore)(nil)
)
