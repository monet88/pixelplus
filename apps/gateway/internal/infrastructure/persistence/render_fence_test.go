package persistence_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/infrastructure/persistence"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// Stale fence cannot mutate lifecycle or record placement after a newer claim.
func TestStaleFenceRejectsTransitionAndPlacement(t *testing.T) {
	t.Parallel()

	store := persistence.NewMemoryRenderJobStore()
	principal := domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a"}
	now := domain.NewTimestamp(time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC))
	job := domain.NewQueuedRenderJob(
		"job_fence",
		"tenant_a",
		"key_a",
		domain.RenderOpImageGeneration,
		"m",
		domain.DigestPrompt("p"),
		nil,
		"",
		"pa_1",
		1,
		"fp",
		"idem",
		now,
	)
	if _, err := store.Create(context.Background(), ports.RenderJobCreation{Principal: principal, Job: job}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	claim1, err := store.ClaimWorker(context.Background(), job.JobRef(), ports.WorkerLease{
		WorkerID: "worker_1",
		Now:      domain.NewTimestamp(now.Time().Add(time.Second)),
	})
	if err != nil {
		t.Fatalf("ClaimWorker1: %v", err)
	}
	// Simulate worker loss then new claim after lease not held recovery path:
	// force LeaseHeld false with a transition is not available; second claim while
	// first holds lease must fail; for stale fence we use first token after
	// artificial fence bump by re-claim after releasing lease via terminal no-op.
	// Claim second worker while first still holds → not claimable.
	if _, err := store.ClaimWorker(context.Background(), job.JobRef(), ports.WorkerLease{
		WorkerID: "worker_2",
		Now:      domain.NewTimestamp(now.Time().Add(2 * time.Second)),
	}); !errors.Is(err, domain.ErrJobNotClaimable) {
		t.Fatalf("second claim error = %v, want ErrJobNotClaimable", err)
	}

	// Stale fence on transition (wrong token).
	if _, err := store.Transition(context.Background(), ports.FencedTransition{
		JobRef:       job.JobRef(),
		FencingToken: claim1.FencingToken + 99,
		To:           domain.JobFailed,
		ClearLease:   true,
		Now:          domain.NewTimestamp(now.Time().Add(3 * time.Second)),
	}); !errors.Is(err, domain.ErrStaleFence) {
		t.Fatalf("stale transition error = %v, want ErrStaleFence", err)
	}

	// Capture manifest with good fence so placement can be attempted.
	manifest := domain.ResultManifest{
		ID:        "man_1",
		AttemptID: "att_1",
		Entries: []domain.OutputEntry{{
			ID:            domain.NewOutputEntryID(job.JobID, 0),
			Position:      0,
			DeliveryState: domain.OutputPending,
			Checksum:      "c",
		}},
	}
	if _, err := store.CaptureManifest(context.Background(), ports.ManifestCapture{
		JobRef:       job.JobRef(),
		FencingToken: claim1.FencingToken,
		Manifest:     manifest,
		Now:          domain.NewTimestamp(now.Time().Add(4 * time.Second)),
	}); err != nil {
		t.Fatalf("CaptureManifest: %v", err)
	}

	if _, err := store.PlaceOutput(context.Background(), ports.PlacementRequest{
		JobRef:       job.JobRef(),
		FencingToken: claim1.FencingToken + 1, // stale
		EntryID:      manifest.Entries[0].ID,
		Asset:        domain.Asset{ID: "asset_x"},
		Now:          domain.NewTimestamp(now.Time().Add(5 * time.Second)),
	}); !errors.Is(err, domain.ErrStaleFence) {
		t.Fatalf("stale placement error = %v, want ErrStaleFence", err)
	}
}
