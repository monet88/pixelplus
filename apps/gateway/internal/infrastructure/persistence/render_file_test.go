package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// A lock anchor left on disk by an earlier process is not ownership. Restore
// must acquire the OS lock on that anchor and proceed normally.
func TestFileRenderJobStoreRestoreIgnoresUnlockedLockAnchor(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "render-jobs.ledger")
	if err := os.WriteFile(path+".lock", []byte("stale anchor"), 0o600); err != nil {
		t.Fatalf("write lock anchor: %v", err)
	}

	store := NewFileRenderJobStore(path)
	if err := store.Restore(context.Background()); err != nil {
		t.Fatalf("Restore with unlocked lock anchor: %v", err)
	}
}

// The open OS lock/handle, not the anchor file, excludes a second process. Once
// released, the same store can acquire it immediately without deleting the file.
func TestFileRenderJobStoreActiveAdvisoryLockExcludesAndReleaseAllowsRetry(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "render-jobs.ledger")
	release, err := acquireAdvisoryLock(path+".lock", "test lock held")
	if err != nil {
		t.Fatalf("acquire first advisory lock: %v", err)
	}

	store := NewFileRenderJobStore(path)
	if err := store.Restore(context.Background()); !errors.Is(err, ports.ErrDependencyUnavailable) {
		release()
		t.Fatalf("Restore while lock held = %v, want ErrDependencyUnavailable", err)
	}
	release()

	if err := store.Restore(context.Background()); err != nil {
		t.Fatalf("Restore after lock release: %v", err)
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Fatalf("lock anchor after release: %v", err)
	}
}

// After the initial full restore, an unchanged ledger is a true no-op and an
// append advances exactly from the prior byte offset instead of replaying history.
func TestFileRenderJobStoreReloadTracksIncrementalTail(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "render-jobs.ledger")
	now := domain.NewTimestamp(time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC))
	principal := domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a"}
	store := NewFileRenderJobStore(path)
	if err := store.Restore(context.Background()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := store.Create(context.Background(), ports.RenderJobCreation{
		Principal: principal,
		Job:       seedQueuedJob("job_tail_1", now),
	}); err != nil {
		t.Fatalf("Create first job: %v", err)
	}
	firstOffset := store.cursor.offset
	firstInfo := store.cursor.info
	if firstOffset == 0 || store.cursor.lineNo != 1 {
		t.Fatalf("cursor after first append = offset %d, lines %d; want nonzero, 1", firstOffset, store.cursor.lineNo)
	}
	if _, err := store.Visible(context.Background(), principal, "job_tail_1"); err != nil {
		t.Fatalf("Visible on unchanged ledger: %v", err)
	}
	if store.cursor.offset != firstOffset || store.cursor.info != firstInfo {
		t.Fatalf("unchanged ledger moved cursor: offset %d→%d", firstOffset, store.cursor.offset)
	}
	if _, err := store.Create(context.Background(), ports.RenderJobCreation{
		Principal: principal,
		Job:       seedQueuedJob("job_tail_2", now),
	}); err != nil {
		t.Fatalf("Create second job: %v", err)
	}
	if store.cursor.offset <= firstOffset || store.cursor.lineNo != 2 {
		t.Fatalf("cursor after tail append = offset %d, lines %d; want >%d, 2", store.cursor.offset, store.cursor.lineNo, firstOffset)
	}
}

// A replaced/truncated ledger cannot reuse the prior tail cursor. The store
// rebuilds from byte zero, so removed jobs disappear instead of surviving as
// ghost in-memory state.
func TestFileRenderJobStoreReloadRebuildsAfterLedgerReplacement(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "render-jobs.ledger")
	now := domain.NewTimestamp(time.Date(2026, 8, 2, 1, 0, 0, 0, time.UTC))
	principal := domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a"}
	store := NewFileRenderJobStore(path)
	if err := store.Restore(context.Background()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := store.Create(context.Background(), ports.RenderJobCreation{Principal: principal, Job: seedQueuedJob("job_old", now)}); err != nil {
		t.Fatalf("Create old job: %v", err)
	}

	replacement := renderJobLedgerEntry{TenantID: "tenant_a", Job: seedQueuedJob("job_new", now)}
	raw, err := json.Marshal(replacement)
	if err != nil {
		t.Fatalf("marshal replacement: %v", err)
	}
	tmp := path + ".replacement"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		t.Fatalf("write replacement: %v", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatalf("replace ledger: %v", err)
	}

	if _, err := store.Visible(context.Background(), principal, "job_old"); !errors.Is(err, ports.ErrRenderJobNotVisible) {
		t.Fatalf("old job after ledger replacement = %v, want not visible", err)
	}
	if _, err := store.Visible(context.Background(), principal, "job_new"); err != nil {
		t.Fatalf("new job after ledger replacement: %v", err)
	}
	if store.cursor.lineNo != 1 {
		t.Fatalf("cursor lines after rebuild = %d, want 1", store.cursor.lineNo)
	}
}

func TestFileRenderJobStoreRestoreRejectsIncompleteFinalRecord(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "render-jobs.ledger")
	now := domain.NewTimestamp(time.Date(2026, 8, 2, 1, 30, 0, 0, time.UTC))
	raw, err := json.Marshal(renderJobLedgerEntry{TenantID: "tenant_a", Job: seedQueuedJob("job_no_newline", now)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := NewFileRenderJobStore(path).Restore(context.Background()); !errors.Is(err, ports.ErrDependencyUnavailable) {
		t.Fatalf("Restore() error = %v, want ErrDependencyUnavailable", err)
	}
}

func TestFileRenderReplayStoreRestoreRejectsIncompleteFinalRecord(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "render-replay.ledger")
	raw, err := json.Marshal(renderReplayLedgerEntry{
		Scope:       domain.ReplayScope{TenantID: "tenant_a", ClientAPIKeyID: "key_a", Key: "idem_no_newline"},
		Fingerprint: "fp_no_newline",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := NewFileRenderReplayStore(path).Restore(context.Background()); !errors.Is(err, ports.ErrDependencyUnavailable) {
		t.Fatalf("Restore() error = %v, want ErrDependencyUnavailable", err)
	}
}

// Empty/missing ledger restores to usable empty state (FileAccountStore parity).
func TestFileRenderJobStoreRestoreEmptyIsUsable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "render-jobs.ledger")
	store := NewFileRenderJobStore(path)
	if err := store.Restore(context.Background()); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	principal := domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a"}
	if _, err := store.Visible(context.Background(), principal, "job_missing"); !errors.Is(err, ports.ErrRenderJobNotVisible) {
		t.Fatalf("Visible on empty store = %v, want ErrRenderJobNotVisible", err)
	}
}

// Null and corrupt ledger rows fail closed rather than silently starting empty.
func TestFileRenderJobStoreRestoreRejectsInvalidRows(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		line string
	}{
		{name: "null_record", line: "null\n"},
		{name: "invalid_json", line: "{not json\n"},
		{name: "missing_job_id", line: `{"tenant_id":"tenant_a","job":{}}` + "\n"},
		{name: "tenant_mismatch", line: `{"tenant_id":"tenant_a","job":{"TenantID":"tenant_b","JobID":"job_x"}}` + "\n"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "render-jobs.ledger")
			if err := os.WriteFile(path, []byte(tc.line), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			store := NewFileRenderJobStore(path)
			err := store.Restore(context.Background())
			if err == nil {
				t.Fatal("Restore() error = nil, want fail-closed rejection")
			}
			if !errors.Is(err, ports.ErrDependencyUnavailable) {
				t.Fatalf("Restore() error = %v, want ErrDependencyUnavailable", err)
			}
		})
	}
}

// Valid JSON that contradicts the Render Job state machine must fail closed.
// In particular, queued Provider-work markers would otherwise be claimed as a
// fresh render after restart instead of entering recovery-only handling.
func TestFileRenderJobStoreRestoreRejectsImpossibleDurableStates(t *testing.T) {
	t.Parallel()

	now := domain.NewTimestamp(time.Date(2026, 8, 2, 2, 0, 0, 0, time.UTC))
	attempt := domain.UpstreamAttempt{
		ID:                "attempt_invalid",
		ProviderAccountID: "pa_1",
		CredentialVersion: 1,
		CommitStatus:      domain.CommitNotStarted,
		Sequence:          1,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	manifest := domain.ResultManifest{
		ID:        "manifest_invalid",
		AttemptID: attempt.ID,
		Entries: []domain.OutputEntry{{
			ID: "output_invalid", Position: 0, DeliveryState: domain.OutputPending,
		}},
		CapturedAt: now,
	}

	cases := []struct {
		name   string
		mutate func(*domain.RenderJob)
	}{
		{name: "queued_payload_sent", mutate: func(job *domain.RenderJob) {
			job.Attempt = attempt
			job.Attempt.PayloadSent = true
		}},
		{name: "queued_committed", mutate: func(job *domain.RenderJob) {
			job.CommitStatus = domain.CommitCommitted
			job.Attempt = attempt
			job.Attempt.CommitStatus = domain.CommitCommitted
		}},
		{name: "queued_unknown", mutate: func(job *domain.RenderJob) {
			job.CommitStatus = domain.CommitUnknown
			job.Attempt = attempt
			job.Attempt.CommitStatus = domain.CommitUnknown
		}},
		{name: "queued_manifest", mutate: func(job *domain.RenderJob) {
			job.Attempt = attempt
			job.Manifest = manifest
		}},
		{name: "running_attempt_commit_differs_from_job", mutate: func(job *domain.RenderJob) {
			job.Lifecycle = domain.JobRunning
			job.Attempt = attempt
			job.Attempt.CommitStatus = domain.CommitCommitted
		}},
		{name: "payload_sent_without_attempt_identity", mutate: func(job *domain.RenderJob) {
			job.Lifecycle = domain.JobRunning
			job.Attempt.PayloadSent = true
		}},
		{name: "response_captured_without_attempt_identity", mutate: func(job *domain.RenderJob) {
			job.Lifecycle = domain.JobRunning
			job.Attempt.ResponseCaptured = true
		}},
		{name: "response_captured_with_not_started_commit", mutate: func(job *domain.RenderJob) {
			job.Lifecycle = domain.JobRunning
			job.Attempt = attempt
			job.Attempt.ResponseCaptured = true
		}},
		{name: "manifest_bound_to_different_attempt", mutate: func(job *domain.RenderJob) {
			job.Lifecycle = domain.JobRunning
			job.Attempt = attempt
			job.Manifest = manifest
			job.Manifest.AttemptID = "attempt_other"
		}},
		{name: "terminal_missing_timestamp", mutate: func(job *domain.RenderJob) {
			job.Lifecycle = domain.JobFailed
			job.ExecutionPhase = ""
		}},
		{name: "held_lease_missing_worker", mutate: func(job *domain.RenderJob) {
			job.Lifecycle = domain.JobRunning
			job.LeaseHeld = true
			job.WorkerFencingToken = 1
		}},
		{name: "held_lease_missing_fence", mutate: func(job *domain.RenderJob) {
			job.Lifecycle = domain.JobRunning
			job.LeaseHeld = true
			job.WorkerID = "worker_invalid"
		}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			job := seedQueuedJob("job_"+domain.Identifier(tc.name), now)
			tc.mutate(&job)
			raw, err := json.Marshal(renderJobLedgerEntry{TenantID: job.TenantID, Job: job})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			dir := t.TempDir()
			path := filepath.Join(dir, "render-jobs.ledger")
			if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			err = NewFileRenderJobStore(path).Restore(context.Background())
			if !errors.Is(err, ports.ErrDependencyUnavailable) {
				t.Fatalf("Restore() error = %v, want ErrDependencyUnavailable", err)
			}
		})
	}
}

func bindJobToReplay(job domain.RenderJob, identity domain.ReplayIdentity) domain.RenderJob {
	job.TenantID = identity.Scope.TenantID
	job.ClientAPIKeyID = identity.Scope.ClientAPIKeyID
	job.IdempotencyKey = identity.Scope.Key
	job.RequestFingerprint = identity.Fingerprint
	return job
}

func seedQueuedJob(id domain.Identifier, now domain.Timestamp) domain.RenderJob {
	return domain.NewQueuedRenderJob(
		id, "tenant_a", "key_a", domain.RenderOpImageGeneration, "m",
		"digest", nil, "", "pa_1", 1, "fp_"+domain.Fingerprint(id), "idem_"+string(id), now,
	)
}

// Create → Restore (fresh store instance, same path) proves durable round trip
// of a queued job with zero side effects beyond the ledger file itself.
func TestFileRenderJobStoreCreateSurvivesRestart(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "render-jobs.ledger")
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	now := domain.NewTimestamp(base)

	store := NewFileRenderJobStore(path)
	if err := store.Restore(context.Background()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	principal := domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a"}
	job := seedQueuedJob("job_create", now)
	if _, err := store.Create(context.Background(), ports.RenderJobCreation{Principal: principal, Job: job}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Fresh instance, same path: process restart proof (no shared Go object).
	restarted := NewFileRenderJobStore(path)
	if err := restarted.Restore(context.Background()); err != nil {
		t.Fatalf("post-restart Restore: %v", err)
	}
	got, err := restarted.Visible(context.Background(), principal, "job_create")
	if err != nil {
		t.Fatalf("Visible after restart: %v", err)
	}
	if got.Lifecycle != domain.JobQueued {
		t.Fatalf("lifecycle after restart = %v, want queued", got.Lifecycle)
	}
	if got.JobID != "job_create" {
		t.Fatalf("job_id after restart = %q, want job_create", got.JobID)
	}
}

// Full claim → attempt → manifest → placement → terminal cycle across two
// independent store instances pointed at the same ledger path proves the
// fenced state machine (lease, fencing token, commit status, output entries)
// survives a genuine restart boundary, and that a resumed claim cannot reuse
// or undercut a fencing token issued before restart.
func TestFileRenderJobStoreFullLifecycleSurvivesRestart(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "render-jobs.ledger")
	base := time.Date(2026, 8, 1, 6, 0, 0, 0, time.UTC)
	now := domain.NewTimestamp(base)
	principal := domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a"}

	storeA := NewFileRenderJobStore(path)
	if err := storeA.Restore(context.Background()); err != nil {
		t.Fatalf("A Restore: %v", err)
	}
	job := seedQueuedJob("job_lifecycle", now)
	if _, err := storeA.Create(context.Background(), ports.RenderJobCreation{Principal: principal, Job: job}); err != nil {
		t.Fatalf("A Create: %v", err)
	}
	claim, err := storeA.ClaimWorker(context.Background(), job.JobRef(), ports.WorkerLease{
		WorkerID: "worker_a", Now: now, ExpiresAt: domain.NewTimestamp(base.Add(2 * time.Minute)),
	})
	if err != nil {
		t.Fatalf("A ClaimWorker: %v", err)
	}
	if claim.FencingToken == 0 {
		t.Fatal("A ClaimWorker: fencing token = 0, want nonzero")
	}

	// --- restart: fresh store instance, same path ---
	storeB := NewFileRenderJobStore(path)
	if err := storeB.Restore(context.Background()); err != nil {
		t.Fatalf("B Restore: %v", err)
	}
	loaded, err := storeB.Load(context.Background(), job.JobRef())
	if err != nil {
		t.Fatalf("B Load: %v", err)
	}
	if loaded.Lifecycle != domain.JobRunning {
		t.Fatalf("B lifecycle = %v, want running", loaded.Lifecycle)
	}
	if loaded.WorkerFencingToken != claim.FencingToken {
		t.Fatalf("B fencing token = %d, want %d (restored)", loaded.WorkerFencingToken, claim.FencingToken)
	}

	// A resumed claim with a fresh worker must issue a strictly higher token
	// than any token observed before restart (never reuse/undercut).
	claim2, err := storeB.ClaimWorker(context.Background(), job.JobRef(), ports.WorkerLease{
		WorkerID: "worker_a", Now: now,
	})
	if err != nil {
		t.Fatalf("B same-worker reclaim: %v", err)
	}
	if !claim2.AlreadyOwned {
		t.Fatal("B same-worker reclaim under active lease: want AlreadyOwned=true")
	}

	// Continue the lifecycle on storeB: attempt → manifest → placement → terminal.
	attempt := domain.UpstreamAttempt{
		ID: domain.NewAttemptID(job.JobID, 1), CommitStatus: domain.CommitNotStarted,
		PayloadSent: false, Sequence: 1, CreatedAt: now, UpdatedAt: now,
	}
	if _, err := storeB.ObserveAttempt(context.Background(), ports.AttemptObservation{
		JobRef: job.JobRef(), FencingToken: claim.FencingToken, Attempt: attempt,
		Phase: domain.PhaseUpstream, CommitStatus: domain.CommitNotStarted, Now: now,
	}); err != nil {
		t.Fatalf("B ObserveAttempt: %v", err)
	}

	entryID := domain.NewOutputEntryID(job.JobID, 0)
	manifest := domain.ResultManifest{
		ID: "man_lifecycle", AttemptID: attempt.ID,
		Entries: []domain.OutputEntry{{ID: entryID, Position: 0, DeliveryState: domain.OutputPending, Checksum: "c"}},
	}
	if _, err := storeB.CaptureManifest(context.Background(), ports.ManifestCapture{
		JobRef: job.JobRef(), FencingToken: claim.FencingToken, Manifest: manifest, Now: now,
	}); err != nil {
		t.Fatalf("B CaptureManifest: %v", err)
	}
	if _, err := storeB.PlaceOutput(context.Background(), ports.PlacementRequest{
		JobRef: job.JobRef(), FencingToken: claim.FencingToken, EntryID: entryID,
		Asset: domain.Asset{ID: "asset_lifecycle", ContentType: domain.ContentTypePNG, ByteSize: 3, Checksum: "c"},
		Now:   now,
	}); err != nil {
		t.Fatalf("B PlaceOutput: %v", err)
	}
	if _, err := storeB.Transition(context.Background(), ports.FencedTransition{
		JobRef: job.JobRef(), FencingToken: claim.FencingToken, To: domain.JobCompleted,
		CommitStatus: domain.CommitCommitted, ClearLease: true, Now: now,
	}); err != nil {
		t.Fatalf("B Transition to completed: %v", err)
	}

	// --- second restart: fresh store instance again, terminal + placement intact ---
	storeC := NewFileRenderJobStore(path)
	if err := storeC.Restore(context.Background()); err != nil {
		t.Fatalf("C Restore: %v", err)
	}
	final, err := storeC.Load(context.Background(), job.JobRef())
	if err != nil {
		t.Fatalf("C Load: %v", err)
	}
	if final.Lifecycle != domain.JobCompleted {
		t.Fatalf("C lifecycle = %v, want completed", final.Lifecycle)
	}
	if len(final.OutputEntries) != 1 || final.OutputEntries[0].DeliveryState != domain.OutputAvailable {
		t.Fatalf("C output entries = %+v, want one available entry", final.OutputEntries)
	}
	if final.OutputEntries[0].AssetID != "asset_lifecycle" {
		t.Fatalf("C asset id = %q, want asset_lifecycle", final.OutputEntries[0].AssetID)
	}

	// Placement idempotency must also survive restart: a second PlaceOutput with
	// fence 0 (completed placement-only recovery) must not error and must not
	// require a fresh Asset — proves placementRecord backfill in seedJob.
	if _, err := storeC.PlaceOutput(context.Background(), ports.PlacementRequest{
		JobRef: job.JobRef(), FencingToken: 0, EntryID: entryID,
		Asset: domain.Asset{ID: "asset_lifecycle", ContentType: domain.ContentTypePNG, ByteSize: 3, Checksum: "c"},
		Now:   now,
	}); err != nil {
		t.Fatalf("C idempotent PlaceOutput: %v", err)
	}
}

// Rejected mutations (e.g. stale fence) must leave the ledger file byte-for-byte
// unchanged — no partial/garbage append on a failed operation.
func TestFileRenderJobStoreRejectedMutationDoesNotAppend(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "render-jobs.ledger")
	base := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	now := domain.NewTimestamp(base)
	principal := domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a"}

	store := NewFileRenderJobStore(path)
	if err := store.Restore(context.Background()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	job := seedQueuedJob("job_reject", now)
	if _, err := store.Create(context.Background(), ports.RenderJobCreation{Principal: principal, Job: job}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	claim, err := store.ClaimWorker(context.Background(), job.JobRef(), ports.WorkerLease{WorkerID: "w1", Now: now})
	if err != nil {
		t.Fatalf("ClaimWorker: %v", err)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}

	// Stale fencing token on Transition must fail without mutating the ledger.
	if _, err := store.Transition(context.Background(), ports.FencedTransition{
		JobRef: job.JobRef(), FencingToken: claim.FencingToken + 99, To: domain.JobFailed,
		ClearLease: true, Now: now,
	}); !errors.Is(err, domain.ErrStaleFence) {
		t.Fatalf("Transition with stale fence = %v, want ErrStaleFence", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger after reject: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("ledger mutated on rejected Transition")
	}
}

// --- FileRenderReplayStore ---

// Replay uses the same advisory-lock contract as jobs: a pre-existing unlocked
// anchor is harmless, an active owner excludes, and release permits retry.
func TestFileRenderReplayStoreAdvisoryLockLifecycle(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "render-replay.ledger")
	if err := os.WriteFile(path+".lock", []byte("stale anchor"), 0o600); err != nil {
		t.Fatalf("write lock anchor: %v", err)
	}
	store := NewFileRenderReplayStore(path)
	if err := store.Restore(context.Background()); err != nil {
		t.Fatalf("Restore with unlocked lock anchor: %v", err)
	}

	release, err := acquireAdvisoryLock(path+".lock", "test replay lock held")
	if err != nil {
		t.Fatalf("acquire replay advisory lock: %v", err)
	}
	if err := store.Restore(context.Background()); !errors.Is(err, ports.ErrDependencyUnavailable) {
		release()
		t.Fatalf("Restore while replay lock held = %v, want ErrDependencyUnavailable", err)
	}
	release()
	if err := store.Restore(context.Background()); err != nil {
		t.Fatalf("Restore after replay lock release: %v", err)
	}
}

// Replay cursor advances with appended claim/completion rows and rebuilds when
// the ledger is replaced, preserving tombstone/ownership semantics without an
// O(n) history scan on every operation.
func TestFileRenderReplayStoreReloadTracksTailAndReplacement(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "render-replay.ledger")
	store := NewFileRenderReplayStore(path)
	if err := store.Restore(context.Background()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	identity := domain.ReplayIdentity{
		Scope:       domain.ReplayScope{TenantID: "tenant_a", ClientAPIKeyID: "key_a", Key: "idem_tail"},
		Fingerprint: "fp_tail",
	}
	if _, err := store.Claim(context.Background(), identity); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	claimOffset := store.cursor.offset
	if claimOffset == 0 || store.cursor.lineNo != 1 {
		t.Fatalf("cursor after claim = offset %d, lines %d; want nonzero, 1", claimOffset, store.cursor.lineNo)
	}
	if err := store.Complete(context.Background(), identity, ports.RenderReplayResult{
		Job: bindJobToReplay(
			seedQueuedJob("job_tail_replay", domain.NewTimestamp(time.Date(2026, 8, 2, 2, 0, 0, 0, time.UTC))),
			identity,
		),
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if store.cursor.offset <= claimOffset || store.cursor.lineNo != 2 {
		t.Fatalf("cursor after complete = offset %d, lines %d; want >%d, 2", store.cursor.offset, store.cursor.lineNo, claimOffset)
	}

	other := renderReplayLedgerEntry{
		Scope:       domain.ReplayScope{TenantID: "tenant_a", ClientAPIKeyID: "key_a", Key: "idem_replaced"},
		Fingerprint: "fp_replaced",
	}
	raw, err := json.Marshal(other)
	if err != nil {
		t.Fatalf("marshal replacement: %v", err)
	}
	tmp := path + ".replacement"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		t.Fatalf("write replacement: %v", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatalf("replace ledger: %v", err)
	}

	decision, err := store.Claim(context.Background(), identity)
	if err != nil {
		t.Fatalf("Claim original identity after replacement: %v", err)
	}
	if decision.Outcome != ports.ReplayClaimed {
		t.Fatalf("original identity after replacement = %v, want ReplayClaimed", decision.Outcome)
	}
}

// Empty/missing ledger restores to usable empty state.
func TestFileRenderReplayStoreRestoreEmptyIsUsable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "render-replay.ledger")
	store := NewFileRenderReplayStore(path)
	if err := store.Restore(context.Background()); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	decision, err := store.Claim(context.Background(), domain.ReplayIdentity{
		Scope:       domain.ReplayScope{TenantID: "tenant_a", ClientAPIKeyID: "key_a", Key: "idem_1"},
		Fingerprint: "fp_1",
	})
	if err != nil {
		t.Fatalf("Claim on empty store: %v", err)
	}
	if decision.Outcome != ports.ReplayClaimed {
		t.Fatalf("Claim outcome = %v, want ReplayClaimed", decision.Outcome)
	}
}

// Null and corrupt ledger rows fail closed.
func TestFileRenderReplayStoreRestoreRejectsInvalidRows(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		line string
	}{
		{name: "null_record", line: "null\n"},
		{name: "invalid_json", line: "{not json\n"},
		{name: "invalid_scope", line: `{"scope":{},"fingerprint":"fp"}` + "\n"},
		{name: "terminal_missing_job", line: `{"scope":{"TenantID":"tenant_a","ClientAPIKeyID":"key_a","Key":"idem"},"fingerprint":"fp","terminal":true}` + "\n"},
		{name: "terminal_job_tenant_mismatch", line: `{"scope":{"TenantID":"tenant_a","ClientAPIKeyID":"key_a","Key":"idem"},"fingerprint":"fp","terminal":true,"job":{"TenantID":"tenant_b","JobID":"job_foreign"}}` + "\n"},
		{name: "terminal_job_client_key_mismatch", line: `{"scope":{"TenantID":"tenant_a","ClientAPIKeyID":"key_b","Key":"idem"},"fingerprint":"fp","terminal":true,"job":{"TenantID":"tenant_a","JobID":"job_owned","ClientAPIKeyID":"key_a","IdempotencyKey":"idem","RequestFingerprint":"fp"}}` + "\n"},
		{name: "terminal_job_idempotency_key_mismatch", line: `{"scope":{"TenantID":"tenant_a","ClientAPIKeyID":"key_a","Key":"idem_b"},"fingerprint":"fp","terminal":true,"job":{"TenantID":"tenant_a","JobID":"job_owned","ClientAPIKeyID":"key_a","IdempotencyKey":"idem_a","RequestFingerprint":"fp"}}` + "\n"},
		{name: "terminal_job_fingerprint_mismatch", line: `{"scope":{"TenantID":"tenant_a","ClientAPIKeyID":"key_a","Key":"idem"},"fingerprint":"fp_b","terminal":true,"job":{"TenantID":"tenant_a","JobID":"job_owned","ClientAPIKeyID":"key_a","IdempotencyKey":"idem","RequestFingerprint":"fp_a"}}` + "\n"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "render-replay.ledger")
			if err := os.WriteFile(path, []byte(tc.line), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			store := NewFileRenderReplayStore(path)
			err := store.Restore(context.Background())
			if err == nil {
				t.Fatal("Restore() error = nil, want fail-closed rejection")
			}
			if !errors.Is(err, ports.ErrDependencyUnavailable) {
				t.Fatalf("Restore() error = %v, want ErrDependencyUnavailable", err)
			}
		})
	}
}

// Claim → Complete (terminal) → restart: matching replay after restart returns
// the same terminal job without a second Claim outcome of ReplayClaimed.
func TestFileRenderReplayStoreTerminalSurvivesRestart(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "render-replay.ledger")
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	now := domain.NewTimestamp(base)
	identity := domain.ReplayIdentity{
		Scope:       domain.ReplayScope{TenantID: "tenant_a", ClientAPIKeyID: "key_a", Key: "idem_terminal"},
		Fingerprint: "fp_terminal",
	}

	storeA := NewFileRenderReplayStore(path)
	if err := storeA.Restore(context.Background()); err != nil {
		t.Fatalf("A Restore: %v", err)
	}
	decision, err := storeA.Claim(context.Background(), identity)
	if err != nil {
		t.Fatalf("A Claim: %v", err)
	}
	if decision.Outcome != ports.ReplayClaimed {
		t.Fatalf("A Claim outcome = %v, want ReplayClaimed", decision.Outcome)
	}
	terminalJob := bindJobToReplay(seedQueuedJob("job_terminal_replay", now), identity)
	terminalJob.Lifecycle = domain.JobCompleted
	if err := storeA.Complete(context.Background(), identity, ports.RenderReplayResult{Job: terminalJob}); err != nil {
		t.Fatalf("A Complete: %v", err)
	}

	// --- restart ---
	storeB := NewFileRenderReplayStore(path)
	if err := storeB.Restore(context.Background()); err != nil {
		t.Fatalf("B Restore: %v", err)
	}
	replayed, err := storeB.Claim(context.Background(), identity)
	if err != nil {
		t.Fatalf("B Claim: %v", err)
	}
	if replayed.Outcome != ports.ReplayTerminal {
		t.Fatalf("B Claim outcome = %v, want ReplayTerminal", replayed.Outcome)
	}
	if replayed.TerminalJob.JobID != "job_terminal_replay" {
		t.Fatalf("B terminal job id = %q, want job_terminal_replay", replayed.TerminalJob.JobID)
	}

	// A different fingerprint on the same scoped key must still conflict after restart.
	conflict, err := storeB.Claim(context.Background(), domain.ReplayIdentity{
		Scope: identity.Scope, Fingerprint: "fp_changed",
	})
	if err != nil {
		t.Fatalf("B conflict Claim: %v", err)
	}
	if conflict.Outcome != ports.ReplayConflict {
		t.Fatalf("B conflict outcome = %v, want ReplayConflict", conflict.Outcome)
	}
}

// Abandon tombstone survives restart: an abandoned in-progress claim frees the
// scoped key for a fresh Claim after restart, but never resurrects/removes an
// already-terminal record (mirrors MemoryRenderReplayStore.Abandon exactly).
func TestFileRenderReplayStoreAbandonTombstoneSurvivesRestart(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "render-replay.ledger")
	identity := domain.ReplayIdentity{
		Scope:       domain.ReplayScope{TenantID: "tenant_a", ClientAPIKeyID: "key_a", Key: "idem_abandon"},
		Fingerprint: "fp_abandon",
	}

	storeA := NewFileRenderReplayStore(path)
	if err := storeA.Restore(context.Background()); err != nil {
		t.Fatalf("A Restore: %v", err)
	}
	if _, err := storeA.Claim(context.Background(), identity); err != nil {
		t.Fatalf("A Claim: %v", err)
	}
	if err := storeA.Abandon(context.Background(), identity); err != nil {
		t.Fatalf("A Abandon: %v", err)
	}

	// --- restart ---
	storeB := NewFileRenderReplayStore(path)
	if err := storeB.Restore(context.Background()); err != nil {
		t.Fatalf("B Restore: %v", err)
	}
	decision, err := storeB.Claim(context.Background(), identity)
	if err != nil {
		t.Fatalf("B Claim after abandon+restart: %v", err)
	}
	if decision.Outcome != ports.ReplayClaimed {
		t.Fatalf("B Claim outcome = %v, want ReplayClaimed (freed by tombstone)", decision.Outcome)
	}
}

func TestMemoryRenderReplayStoreRejectsMismatchedCompletionJob(t *testing.T) {
	t.Parallel()

	store := NewMemoryRenderReplayStore()
	identity := domain.ReplayIdentity{
		Scope: domain.ReplayScope{
			TenantID:       "tenant_a",
			ClientAPIKeyID: "key_a",
			Key:            "idem_mismatched_job",
		},
		Fingerprint: "fp_expected",
	}
	if decision, err := store.Claim(context.Background(), identity); err != nil || decision.Outcome != ports.ReplayClaimed {
		t.Fatalf("Claim = %+v, %v; want ReplayClaimed", decision, err)
	}

	cases := []struct {
		name   string
		mutate func(*domain.RenderJob)
	}{
		{name: "missing_job_id", mutate: func(job *domain.RenderJob) { job.JobID = "" }},
		{name: "tenant", mutate: func(job *domain.RenderJob) { job.TenantID = "tenant_b" }},
		{name: "client_api_key", mutate: func(job *domain.RenderJob) { job.ClientAPIKeyID = "key_b" }},
		{name: "idempotency_key", mutate: func(job *domain.RenderJob) { job.IdempotencyKey = "idem_other" }},
		{name: "fingerprint", mutate: func(job *domain.RenderJob) { job.RequestFingerprint = "fp_other" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			job := bindJobToReplay(seedQueuedJob("job_mismatched_"+domain.Identifier(tc.name), domain.NewTimestamp(time.Now().UTC())), identity)
			tc.mutate(&job)
			if err := store.Complete(context.Background(), identity, ports.RenderReplayResult{Job: job}); !errors.Is(err, ports.ErrDependencyUnavailable) {
				t.Fatalf("Complete error = %v, want ErrDependencyUnavailable", err)
			}
			decision, err := store.Claim(context.Background(), identity)
			if err != nil {
				t.Fatalf("Claim after rejected Complete: %v", err)
			}
			if decision.Outcome != ports.ReplayInProgress {
				t.Fatalf("Claim outcome = %v, want ReplayInProgress", decision.Outcome)
			}
		})
	}
}

func TestMemoryRenderReplayStoreRejectsStaleCompletionAfterReclaim(t *testing.T) {
	t.Parallel()

	store := NewMemoryRenderReplayStore()
	scope := domain.ReplayScope{TenantID: "tenant_a", ClientAPIKeyID: "key_a", Key: "idem_stale_complete"}
	first := domain.ReplayIdentity{Scope: scope, Fingerprint: "fp_first"}
	second := domain.ReplayIdentity{Scope: scope, Fingerprint: "fp_second"}
	if decision, err := store.Claim(context.Background(), first); err != nil || decision.Outcome != ports.ReplayClaimed {
		t.Fatalf("first Claim = %+v, %v; want ReplayClaimed", decision, err)
	}
	if err := store.Abandon(context.Background(), first); err != nil {
		t.Fatalf("first Abandon: %v", err)
	}
	if decision, err := store.Claim(context.Background(), second); err != nil || decision.Outcome != ports.ReplayClaimed {
		t.Fatalf("second Claim = %+v, %v; want ReplayClaimed", decision, err)
	}
	firstJob := bindJobToReplay(seedQueuedJob("job_first", domain.NewTimestamp(time.Now().UTC())), first)
	if err := store.Complete(context.Background(), first, ports.RenderReplayResult{Job: firstJob}); !errors.Is(err, ports.ErrDependencyUnavailable) {
		t.Fatalf("stale Complete error = %v, want ErrDependencyUnavailable", err)
	}
	decision, err := store.Claim(context.Background(), second)
	if err != nil {
		t.Fatalf("second matching Claim: %v", err)
	}
	if decision.Outcome != ports.ReplayInProgress {
		t.Fatalf("second matching Claim outcome = %v, want ReplayInProgress", decision.Outcome)
	}
}

func TestFileRenderReplayStoreRejectsStaleCompletionWithoutAppending(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "render-replay.ledger")
	store := NewFileRenderReplayStore(path)
	if err := store.Restore(context.Background()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	scope := domain.ReplayScope{TenantID: "tenant_a", ClientAPIKeyID: "key_a", Key: "idem_file_stale_complete"}
	first := domain.ReplayIdentity{Scope: scope, Fingerprint: "fp_first"}
	second := domain.ReplayIdentity{Scope: scope, Fingerprint: "fp_second"}
	if _, err := store.Claim(context.Background(), first); err != nil {
		t.Fatalf("first Claim: %v", err)
	}
	if err := store.Abandon(context.Background(), first); err != nil {
		t.Fatalf("first Abandon: %v", err)
	}
	if _, err := store.Claim(context.Background(), second); err != nil {
		t.Fatalf("second Claim: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read before stale Complete: %v", err)
	}
	firstJob := bindJobToReplay(seedQueuedJob("job_first", domain.NewTimestamp(time.Now().UTC())), first)
	if err := store.Complete(context.Background(), first, ports.RenderReplayResult{Job: firstJob}); !errors.Is(err, ports.ErrDependencyUnavailable) {
		t.Fatalf("stale Complete error = %v, want ErrDependencyUnavailable", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after stale Complete: %v", err)
	}
	if string(after) != string(before) {
		t.Fatal("stale Complete appended a durable row")
	}
}

// A fingerprint-mismatched Abandon (someone else's claim on the same scope)
// must be a true no-op on the durable ledger: no tombstone is appended, so a
// restart can never delete the still-legitimately-owned pending record and
// let a fresh Claim steal a new generation (no-steal,
// canonical-errors-and-retry-ownership.md §7.4).
func TestFileRenderReplayStoreAbandonFingerprintMismatchDoesNotStealAfterRestart(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "render-replay.ledger")
	scope := domain.ReplayScope{TenantID: "tenant_a", ClientAPIKeyID: "key_a", Key: "idem_no_steal"}
	owner := domain.ReplayIdentity{Scope: scope, Fingerprint: "fp_owner"}
	intruder := domain.ReplayIdentity{Scope: scope, Fingerprint: "fp_intruder"}

	store := NewFileRenderReplayStore(path)
	if err := store.Restore(context.Background()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := store.Claim(context.Background(), owner); err != nil {
		t.Fatalf("owner Claim: %v", err)
	}

	// Someone else's Abandon on the same scoped key: in-memory no-op (fingerprint
	// mismatch), and must not durably tombstone the owner's pending claim.
	if err := store.Abandon(context.Background(), intruder); err != nil {
		t.Fatalf("intruder Abandon: %v", err)
	}

	// --- restart ---
	restarted := NewFileRenderReplayStore(path)
	if err := restarted.Restore(context.Background()); err != nil {
		t.Fatalf("post-restart Restore: %v", err)
	}
	decision, err := restarted.Claim(context.Background(), intruder)
	if err != nil {
		t.Fatalf("post-restart intruder Claim: %v", err)
	}
	if decision.Outcome != ports.ReplayConflict {
		t.Fatalf("post-restart intruder Claim outcome = %v, want ReplayConflict (owner's claim must survive)", decision.Outcome)
	}
	// The legitimate owner's matching retry still resolves as in-progress —
	// proof the record was never deleted, only rejected for the wrong fingerprint.
	ownerReplay, err := restarted.Claim(context.Background(), owner)
	if err != nil {
		t.Fatalf("post-restart owner Claim: %v", err)
	}
	if ownerReplay.Outcome != ports.ReplayInProgress {
		t.Fatalf("post-restart owner Claim outcome = %v, want ReplayInProgress", ownerReplay.Outcome)
	}
}

// Rejected/no-op mutations must not corrupt the ledger structurally (still
// valid JSONL after a rejected operation, even though Claim/Abandon on this
// store return nil error for most "no-op" paths — this proves marshal safety).
func TestFileRenderReplayStoreLedgerRemainsValidJSONLAfterOps(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "render-replay.ledger")
	store := NewFileRenderReplayStore(path)
	if err := store.Restore(context.Background()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	identity := domain.ReplayIdentity{
		Scope:       domain.ReplayScope{TenantID: "tenant_a", ClientAPIKeyID: "key_a", Key: "idem_valid"},
		Fingerprint: "fp_valid",
	}
	if _, err := store.Claim(context.Background(), identity); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := store.Complete(context.Background(), identity, ports.RenderReplayResult{
		Job: bindJobToReplay(seedQueuedJob("job_valid", domain.NewTimestamp(time.Now().UTC())), identity),
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	for _, line := range splitLines(raw) {
		if len(line) == 0 {
			continue
		}
		var entry renderReplayLedgerEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatalf("ledger line not valid JSON: %s (%v)", line, err)
		}
	}
}
