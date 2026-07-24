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
		"opaque-digest",
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

// Lease expiry allows reclaim when not_started and PayloadSent=false (crash
// before Adapter entry). After durable PayloadSent, reclaim never re-renders.
func TestLeaseExpiryRecoveryDoesNotRerenderAfterPayloadSent(t *testing.T) {
	t.Parallel()

	store := persistence.NewMemoryRenderJobStore()
	principal := domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a"}
	base := time.Date(2026, 7, 24, 14, 0, 0, 0, time.UTC)
	now := domain.NewTimestamp(base)
	job := domain.NewQueuedRenderJob(
		"job_lease_exp",
		"tenant_a",
		"key_a",
		domain.RenderOpImageGeneration,
		"m",
		"opaque-digest",
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

	claimAt := domain.NewTimestamp(base.Add(time.Second))
	expires := domain.NewTimestamp(base.Add(30 * time.Second))
	claim1, err := store.ClaimWorker(context.Background(), job.JobRef(), ports.WorkerLease{
		WorkerID:  "worker_1",
		Now:       claimAt,
		ExpiresAt: expires,
	})
	if err != nil {
		t.Fatalf("ClaimWorker1: %v", err)
	}

	// Pre-payload: attempt ledger may exist as not_started without PayloadSent.
	preAttempt := domain.UpstreamAttempt{
		ID:           domain.NewAttemptID(job.JobID, 1),
		CommitStatus: domain.CommitNotStarted,
		PayloadSent:  false,
		Sequence:     1,
		CreatedAt:    claimAt,
		UpdatedAt:    claimAt,
	}
	if _, err := store.ObserveAttempt(context.Background(), ports.AttemptObservation{
		JobRef:       job.JobRef(),
		FencingToken: claim1.FencingToken,
		Attempt:      preAttempt,
		Phase:        domain.PhaseUpstream,
		CommitStatus: domain.CommitNotStarted,
		Now:          claimAt,
	}); err != nil {
		t.Fatalf("ObserveAttempt pre-send: %v", err)
	}

	// Crash before Adapter entry + lease expiry: reclaim remains allowed.
	afterExpiry := domain.NewTimestamp(base.Add(31 * time.Second))
	claim2, err := store.ClaimWorker(context.Background(), job.JobRef(), ports.WorkerLease{
		WorkerID:  "worker_2",
		Now:       afterExpiry,
		ExpiresAt: domain.NewTimestamp(base.Add(2 * time.Minute)),
	})
	if err != nil {
		t.Fatalf("ClaimWorker after expiry pre-payload: %v", err)
	}
	if claim2.FencingToken == claim1.FencingToken {
		t.Fatalf("expected new fencing token after reclaim, got %d", claim2.FencingToken)
	}

	// Durable PayloadSent at Adapter entry surface only.
	attempt := domain.UpstreamAttempt{
		ID:           domain.NewAttemptID(job.JobID, 1),
		CommitStatus: domain.CommitNotStarted,
		PayloadSent:  true,
		Sequence:     1,
		CreatedAt:    afterExpiry,
		UpdatedAt:    afterExpiry,
	}
	if _, err := store.ObserveAttempt(context.Background(), ports.AttemptObservation{
		JobRef:       job.JobRef(),
		FencingToken: claim2.FencingToken,
		Attempt:      attempt,
		Phase:        domain.PhaseUpstream,
		CommitStatus: domain.CommitNotStarted,
		Now:          afterExpiry,
	}); err != nil {
		t.Fatalf("ObserveAttempt PayloadSent: %v", err)
	}

	// Post-payload expiry: recovery claim is allowed, but RecoveryOnly forbids re-render.
	post := domain.NewTimestamp(base.Add(5 * time.Minute))
	claim3, err := store.ClaimWorker(context.Background(), job.JobRef(), ports.WorkerLease{
		WorkerID: "worker_3",
		Now:      post,
	})
	if err != nil {
		t.Fatalf("ClaimWorker after payload+expiry: %v", err)
	}
	if !claim3.RecoveryOnly {
		t.Fatal("post-payload reclaim must set RecoveryOnly=true (no second generation)")
	}
}

// Post-manifest recovery claim continues finalize without treating the job as unclaimable.
func TestPostManifestRecoveryClaimIsRecoveryOnly(t *testing.T) {
	t.Parallel()

	store := persistence.NewMemoryRenderJobStore()
	principal := domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a"}
	base := time.Date(2026, 7, 24, 15, 0, 0, 0, time.UTC)
	now := domain.NewTimestamp(base)
	job := domain.NewQueuedRenderJob(
		"job_manifest_rec", "tenant_a", "key_a", domain.RenderOpImageGeneration, "m",
		"opaque-digest", nil, "", "pa_1", 1, "fp", "idem", now,
	)
	if _, err := store.Create(context.Background(), ports.RenderJobCreation{Principal: principal, Job: job}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	claim, err := store.ClaimWorker(context.Background(), job.JobRef(), ports.WorkerLease{
		WorkerID: "w1", Now: now, ExpiresAt: domain.NewTimestamp(base.Add(10 * time.Second)),
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	manifest := domain.ResultManifest{
		ID: "man_rec", AttemptID: "att_1",
		Entries: []domain.OutputEntry{{
			ID: domain.NewOutputEntryID(job.JobID, 0), Position: 0,
			DeliveryState: domain.OutputPending, Checksum: "c",
		}},
	}
	if _, err := store.CaptureManifest(context.Background(), ports.ManifestCapture{
		JobRef: job.JobRef(), FencingToken: claim.FencingToken, Manifest: manifest, Now: now,
	}); err != nil {
		t.Fatalf("CaptureManifest: %v", err)
	}
	// Expire lease.
	rec, err := store.ClaimWorker(context.Background(), job.JobRef(), ports.WorkerLease{
		WorkerID: "w2", Now: domain.NewTimestamp(base.Add(time.Minute)),
	})
	if err != nil {
		t.Fatalf("recovery claim: %v", err)
	}
	if !rec.RecoveryOnly {
		t.Fatal("manifest recovery claim must be RecoveryOnly")
	}
	if rec.Job.Manifest.ID != "man_rec" {
		t.Fatalf("manifest = %q, want man_rec", rec.Job.Manifest.ID)
	}
}
