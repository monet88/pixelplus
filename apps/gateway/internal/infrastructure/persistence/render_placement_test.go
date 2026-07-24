package persistence_test

import (
	"context"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/infrastructure/persistence"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

type clock struct{ t time.Time }

func (c clock) Now() time.Time { return c.t }

// Stable-id Commit is idempotent: second Commit of the same Asset ID does not
// double-count Tenant storage and returns the existing Asset.
func TestAssetCommitIdempotentForStableOutputID(t *testing.T) {
	t.Parallel()

	meta := persistence.NewMemoryAssetMetadataStore(clock{t: time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)})
	principal := domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "k"}
	stable := domain.StableOutputAssetID("tenant_a", "job_1", "out_0")
	asset := domain.Asset{
		ID: stable, TenantID: "tenant_a", Kind: domain.AssetKindOutput,
		ContentType: domain.ContentTypePNG, ByteSize: 10, Checksum: "c",
		Origin: domain.AssetOriginGenerated, RetentionClass: domain.RetentionClassOutput,
		CreatedAt: domain.NewTimestamp(time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)),
	}
	hold := ports.AssetReservation{TenantID: "tenant_a", Bytes: 10}
	if err := meta.Reserve(context.Background(), hold); err != nil {
		t.Fatalf("Reserve1: %v", err)
	}
	if _, err := meta.Commit(context.Background(), ports.AssetCreation{Principal: principal, Asset: asset, Reservation: hold}); err != nil {
		t.Fatalf("Commit1: %v", err)
	}
	// Second placement attempt: reserve again then re-commit same id.
	hold2 := ports.AssetReservation{TenantID: "tenant_a", Bytes: 10}
	if err := meta.Reserve(context.Background(), hold2); err != nil {
		t.Fatalf("Reserve2: %v", err)
	}
	got, err := meta.Commit(context.Background(), ports.AssetCreation{Principal: principal, Asset: asset, Reservation: hold2})
	if err != nil {
		t.Fatalf("Commit2: %v", err)
	}
	if got.ID != stable {
		t.Fatalf("asset id = %s, want %s", got.ID, stable)
	}
	// Visible once only — no second object.
	if _, err := meta.Visible(context.Background(), principal, stable); err != nil {
		t.Fatalf("Visible: %v", err)
	}
}

// Placement-keyed Reserve is idempotent: crash after Reserve before Commit
// re-Reserve must not double-count reserved accounting.
func TestPlacementKeyedReserveDoesNotDoubleCount(t *testing.T) {
	t.Parallel()

	// Cap: 100 bytes so a double-count (2×60) would exceed.
	meta := persistence.NewMemoryAssetMetadataStoreWithCaps(
		clock{t: time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)},
		100,
		10,
	)
	key := domain.PlacementKey{
		TenantID: "tenant_a", JobID: "job_1", OutputEntryID: "out_0",
	}.String()
	hold := ports.AssetReservation{TenantID: "tenant_a", Bytes: 60, PlacementKey: key}
	if err := meta.Reserve(context.Background(), hold); err != nil {
		t.Fatalf("Reserve1: %v", err)
	}
	// Simulated crash recovery: same placement key Reserve again.
	if err := meta.Reserve(context.Background(), hold); err != nil {
		t.Fatalf("Reserve2 (same key) must be idempotent, got %v", err)
	}
	// A different placement still competes for remaining headroom (100-60=40).
	other := ports.AssetReservation{
		TenantID: "tenant_a", Bytes: 50,
		PlacementKey: domain.PlacementKey{TenantID: "tenant_a", JobID: "job_2", OutputEntryID: "out_0"}.String(),
	}
	if err := meta.Reserve(context.Background(), other); err == nil {
		t.Fatal("Reserve other 50 bytes should hit cap after one 60-byte hold")
	}
}
