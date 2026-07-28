package composition_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/composition"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/infrastructure/jobs"
	"github.com/monet88/pixelplus/apps/gateway/internal/infrastructure/persistence"
	vaultpkg "github.com/monet88/pixelplus/apps/gateway/internal/infrastructure/vault"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// TestFileRenderStoresSurviveRealRestartWithoutDuplicateProviderWork proves
// #56 AC5 (durable restart) against a genuine process boundary: process A and
// process B share ONLY a file path — never a Go object — unlike
// TestPublishedJobRecoversIntoFreshRuntimeWithoutClientRetry (queue_recovery_test.go),
// which shares one in-memory store instance across two composition.New calls.
//
// Process A drives one job through claim → attempt → manifest → placement →
// completed and completes one matching-replay terminal record, then dies.
// Process B opens fresh FileRenderJobStore/FileRenderReplayStore instances at
// the same paths, recovers via composition.New, and must observe: (1) the
// durable job/lease/attempt/manifest/output state, (2) a redelivered
// ExecuteJob on the completed job triggers terminal cleanup only — zero
// Provider Adapter calls (no duplicate render), and (3) a matching
// idempotency-key replay still resolves to the same terminal job.
func TestFileRenderStoresSurviveRealRestartWithoutDuplicateProviderWork(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	jobLedgerPath := filepath.Join(dir, "render-jobs.ledger")
	replayLedgerPath := filepath.Join(dir, "render-replay.ledger")
	base := time.Date(2026, 7, 26, 9, 0, 0, 0, time.UTC)

	digester, err := vaultpkg.NewHMACRenderDigester([]byte(vaultpkg.FixtureRenderDigestKey))
	if err != nil {
		t.Fatalf("digester: %v", err)
	}

	principal := domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a"}
	const fingerprint domain.Fingerprint = "fp_restart_durable"
	const idemKey = "idem-restart-durable"

	// --- Process A: fresh File-backed stores at the ledger paths ---
	jobStoreA := persistence.NewFileRenderJobStore(jobLedgerPath)
	replayStoreA := persistence.NewFileRenderReplayStore(replayLedgerPath)
	compA, err := composition.New(composition.Config{AllowInMemoryRenderJobs: true}, composition.Dependencies{
		Runtime:        jobs.New(),
		Clock:          clockNow{t: base},
		IDs:            &rwSeqIDs{},
		RenderJobs:     jobStoreA,
		RenderReplay:   replayStoreA,
		RenderDigester: digester,
	})
	if err != nil {
		t.Fatalf("A New: %v", err)
	}
	if !compA.Ready() {
		t.Fatal("A Ready() = false, want true")
	}

	seed := domain.NewQueuedRenderJob(
		"job_restart_durable", "tenant_a", "key_a", domain.RenderOpImageGeneration, "m",
		"digest", nil, "", "pa_1", 1, fingerprint, idemKey, domain.NewTimestamp(base),
	)
	created, err := jobStoreA.Create(context.Background(), ports.RenderJobCreation{Principal: principal, Job: seed})
	if err != nil {
		t.Fatalf("A Create: %v", err)
	}
	ref := created.JobRef()

	claimAt := domain.NewTimestamp(base.Add(time.Second))
	claim, err := jobStoreA.ClaimWorker(context.Background(), ref, ports.WorkerLease{
		WorkerID: "worker_a", Now: claimAt, ExpiresAt: domain.NewTimestamp(base.Add(2 * time.Minute)),
	})
	if err != nil {
		t.Fatalf("A ClaimWorker: %v", err)
	}

	attemptAt := domain.NewTimestamp(base.Add(2 * time.Second))
	attempt := domain.UpstreamAttempt{
		ID: domain.NewAttemptID(seed.JobID, 1), ProviderAccountID: "pa_1", CredentialVersion: 1,
		CommitStatus: domain.CommitCommitted, PayloadSent: true, ResponseCaptured: true,
		Sequence: 1, CreatedAt: attemptAt, UpdatedAt: attemptAt,
	}
	if _, err := jobStoreA.ObserveAttempt(context.Background(), ports.AttemptObservation{
		JobRef: ref, FencingToken: claim.FencingToken, Attempt: attempt,
		Phase: domain.PhaseUpstream, CommitStatus: domain.CommitCommitted, Now: attemptAt,
	}); err != nil {
		t.Fatalf("A ObserveAttempt: %v", err)
	}

	entryID := domain.NewOutputEntryID(seed.JobID, 0)
	manifestAt := domain.NewTimestamp(base.Add(3 * time.Second))
	manifest := domain.ResultManifest{
		ID: domain.NewResultManifestID(attempt.ID), AttemptID: attempt.ID,
		Entries:    []domain.OutputEntry{{ID: entryID, Position: 0, DeliveryState: domain.OutputPending, Checksum: "c"}},
		CapturedAt: manifestAt,
	}
	if _, err := jobStoreA.CaptureManifest(context.Background(), ports.ManifestCapture{
		JobRef: ref, FencingToken: claim.FencingToken, Manifest: manifest,
		Phase: domain.PhasePlacingOutput, Now: manifestAt,
	}); err != nil {
		t.Fatalf("A CaptureManifest: %v", err)
	}

	placeAt := domain.NewTimestamp(base.Add(4 * time.Second))
	if _, err := jobStoreA.PlaceOutput(context.Background(), ports.PlacementRequest{
		JobRef: ref, FencingToken: claim.FencingToken, EntryID: entryID,
		Asset: domain.Asset{ID: "asset_restart_durable", ContentType: domain.ContentTypePNG, ByteSize: 3, Checksum: "c"},
		Now:   placeAt,
	}); err != nil {
		t.Fatalf("A PlaceOutput: %v", err)
	}

	completeAt := domain.NewTimestamp(base.Add(5 * time.Second))
	completed, err := jobStoreA.Transition(context.Background(), ports.FencedTransition{
		JobRef: ref, FencingToken: claim.FencingToken, To: domain.JobCompleted,
		CommitStatus: domain.CommitCommitted, ClearLease: true, Now: completeAt,
	})
	if err != nil {
		t.Fatalf("A Transition completed: %v", err)
	}
	if completed.Lifecycle != domain.JobCompleted {
		t.Fatalf("A completed lifecycle = %v, want completed", completed.Lifecycle)
	}

	identity := domain.ReplayIdentity{
		Scope:       domain.ReplayScope{TenantID: "tenant_a", ClientAPIKeyID: "key_a", Key: idemKey},
		Fingerprint: fingerprint,
	}
	claimedDecision, err := replayStoreA.Claim(context.Background(), identity)
	if err != nil {
		t.Fatalf("A replay Claim: %v", err)
	}
	if claimedDecision.Outcome != ports.ReplayClaimed {
		t.Fatalf("A replay Claim outcome = %v, want ReplayClaimed", claimedDecision.Outcome)
	}
	if err := replayStoreA.Complete(context.Background(), identity, ports.RenderReplayResult{Job: completed}); err != nil {
		t.Fatalf("A replay Complete: %v", err)
	}

	// Process A dies without further mutation. No Go object is shared with B.
	_ = compA.Close(context.Background())

	// --- Process B: fresh store instances at the SAME paths only ---
	jobStoreB := persistence.NewFileRenderJobStore(jobLedgerPath)
	replayStoreB := persistence.NewFileRenderReplayStore(replayLedgerPath)
	auth := &countingAuthorized{}
	compB, err := composition.New(composition.Config{AllowInMemoryRenderJobs: true}, composition.Dependencies{
		Runtime:          jobs.New(),
		Clock:            clockNow{t: base},
		IDs:              &rwSeqIDs{},
		RenderJobs:       jobStoreB,
		RenderReplay:     replayStoreB,
		RenderDigester:   digester,
		AuthorizedRender: auth,
	})
	if err != nil {
		t.Fatalf("B New: %v", err)
	}
	if !compB.Ready() {
		t.Fatal("B Ready() = false after restart recovery; want true")
	}

	// (1) durable job/lease/attempt/manifest/output state survived the restart.
	loaded, err := jobStoreB.Load(context.Background(), ref)
	if err != nil {
		t.Fatalf("B Load: %v", err)
	}
	if loaded.Lifecycle != domain.JobCompleted {
		t.Fatalf("B lifecycle = %v, want completed", loaded.Lifecycle)
	}
	if loaded.Manifest.ID != manifest.ID {
		t.Fatalf("B manifest id = %q, want %q", loaded.Manifest.ID, manifest.ID)
	}
	if loaded.Attempt.CommitStatus != domain.CommitCommitted || !loaded.Attempt.PayloadSent {
		t.Fatalf("B attempt = %+v, want committed+payload sent", loaded.Attempt)
	}
	if len(loaded.OutputEntries) != 1 || loaded.OutputEntries[0].AssetID != "asset_restart_durable" {
		t.Fatalf("B output entries = %+v, want asset_restart_durable placed", loaded.OutputEntries)
	}
	if loaded.OutputEntries[0].DeliveryState != domain.OutputAvailable {
		t.Fatalf("B delivery state = %v, want available", loaded.OutputEntries[0].DeliveryState)
	}
	if loaded.LeaseHeld {
		t.Fatal("B LeaseHeld = true, want false (cleared on terminal transition)")
	}

	// (2) redelivery of the completed job after restart is cleanup-only — the
	// worker seam must not re-enter the Provider Adapter.
	if err := compB.Worker().ExecuteJob(context.Background(), ref); err != nil {
		t.Fatalf("B ExecuteJob redelivery: %v", err)
	}
	if calls := auth.calls.Load(); calls != 0 {
		t.Fatalf("B Adapter calls after redelivery = %d, want 0 (no duplicate Provider work)", calls)
	}

	// (3) matching idempotency-key replay still resolves to the same terminal job.
	decision, err := replayStoreB.Claim(context.Background(), identity)
	if err != nil {
		t.Fatalf("B replay Claim: %v", err)
	}
	if decision.Outcome != ports.ReplayTerminal {
		t.Fatalf("B replay Claim outcome = %v, want ReplayTerminal", decision.Outcome)
	}
	if decision.TerminalJob.JobID != seed.JobID || decision.TerminalJob.Lifecycle != domain.JobCompleted {
		t.Fatalf("B replay terminal job = %+v, want completed job_restart_durable", decision.TerminalJob)
	}

	_ = compB.Close(context.Background())
}
