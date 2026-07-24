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
