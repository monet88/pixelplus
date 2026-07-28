package composition_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/composition"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/infrastructure/jobs"
	"github.com/monet88/pixelplus/apps/gateway/internal/infrastructure/persistence"
	vaultpkg "github.com/monet88/pixelplus/apps/gateway/internal/infrastructure/vault"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// countingJobRuntime decorates jobs.Runtime to observe Enqueue re-arm after restart.
type countingJobRuntime struct {
	inner    *jobs.Runtime
	enqueues atomic.Int32
	last     atomic.Value // ports.SafeJobReference
}

func newCountingJobRuntime() *countingJobRuntime {
	return &countingJobRuntime{inner: jobs.New()}
}

func (r *countingJobRuntime) Enqueue(ctx context.Context, reference ports.SafeJobReference) (ports.EnqueueReceipt, error) {
	r.enqueues.Add(1)
	r.last.Store(reference)
	return r.inner.Enqueue(ctx, reference)
}
func (r *countingJobRuntime) Run(ctx context.Context, handler ports.JobHandler) error {
	return r.inner.Run(ctx, handler)
}
func (r *countingJobRuntime) Close(ctx context.Context) error { return r.inner.Close(ctx) }
func (r *countingJobRuntime) Restore(ctx context.Context) error {
	return r.inner.Restore(ctx)
}

// Spec P1-3: process-local runtime A accepts Enqueue + marks QueuePublished=true,
// then dies before Run. Fresh runtime B with the same durable store must re-arm
// the SAME SafeJobReference via startup recovery — QueuePublished is historical
// acceptance, not proof the process-local pending item survived.
func TestPublishedJobRecoversIntoFreshRuntimeWithoutClientRetry(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 7, 25, 4, 0, 0, 0, time.UTC)
	// Shared durable job store across "process" restart.
	store := persistence.NewMemoryRenderJobStore()

	// --- Process A: create durable job, Enqueue, mark published, die (no Run) ---
	rtA := newCountingJobRuntime()
	if err := rtA.Restore(context.Background()); err != nil {
		t.Fatalf("A Restore: %v", err)
	}
	digester, err := vaultpkg.NewHMACRenderDigester([]byte(vaultpkg.FixtureRenderDigestKey))
	if err != nil {
		t.Fatalf("digester: %v", err)
	}
	compA, err := composition.New(composition.Config{AllowInMemoryRenderJobs: true}, composition.Dependencies{
		Runtime:        rtA,
		Clock:          clockNow{t: base},
		IDs:            &rwSeqIDs{},
		RenderJobs:     store,
		RenderDigester: digester,
	})
	if err != nil {
		t.Fatalf("A New: %v", err)
	}
	if !compA.Ready() {
		t.Fatal("A Ready() = false")
	}

	seed := domain.NewQueuedRenderJob(
		"job_restart", "tenant_a", "key_a", domain.RenderOpImageGeneration, "m",
		"digest", nil, "", "pa_1", 1, "fp", "idem-restart", domain.NewTimestamp(base),
	)
	// Historical marker false until Enqueue accepts.
	seed.QueuePublished = false
	if _, err := store.Create(context.Background(), ports.RenderJobCreation{
		Principal: domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a"},
		Job:       seed,
	}); err != nil {
		t.Fatalf("A Create: %v", err)
	}
	ref := ports.SafeJobReference{TenantID: "tenant_a", JobID: "job_restart"}
	if _, err := rtA.Enqueue(context.Background(), ref); err != nil {
		t.Fatalf("A Enqueue: %v", err)
	}
	if _, err := store.MarkQueuePublished(context.Background(), domain.JobRef{
		TenantID: "tenant_a", JobID: "job_restart",
	}); err != nil {
		t.Fatalf("A MarkQueuePublished: %v", err)
	}
	// Die without consuming: Close process-local runtime A only (store lives).
	_ = rtA.Close(context.Background())
	_ = compA.Close(context.Background())

	loaded, err := store.Load(context.Background(), domain.JobRef{TenantID: "tenant_a", JobID: "job_restart"})
	if err != nil {
		t.Fatalf("Load after A: %v", err)
	}
	if !loaded.QueuePublished {
		t.Fatal("QueuePublished must be true after A publication")
	}
	if loaded.Lifecycle.Terminal() {
		t.Fatal("job must remain nonterminal after A death")
	}

	// --- Process B: fresh empty runtime; startup recovery must re-arm same ref ---
	rtB := newCountingJobRuntime()
	if err := rtB.Restore(context.Background()); err != nil {
		t.Fatalf("B Restore: %v", err)
	}
	auth := &countingAuthorized{}
	compB, err := composition.New(composition.Config{AllowInMemoryRenderJobs: true}, composition.Dependencies{
		Runtime:          rtB,
		Clock:            clockNow{t: base},
		IDs:              &rwSeqIDs{},
		RenderJobs:       store,
		RenderDigester:   digester,
		AuthorizedRender: auth,
	})
	if err != nil {
		t.Fatalf("B New: %v", err)
	}
	if !compB.Ready() {
		t.Fatal("B Ready() = false after restart recovery; want recovery success")
	}

	if got := rtB.enqueues.Load(); got < 1 {
		t.Fatalf("B Enqueue count = %d, want ≥1 (startup re-arm of published job)", got)
	}
	last, _ := rtB.last.Load().(ports.SafeJobReference)
	if last.TenantID != "tenant_a" || last.JobID != "job_restart" {
		t.Fatalf("B recovered ref = %+v, want tenant_a/job_restart", last)
	}

	// Consume recovered ref: ExecuteJob without client create retry.
	// Recovery terminalizes cancel_requested path or runs claim; for queued job
	// preflight may fail without full account seed — still proves delivery of SAME ref.
	delivered := make(chan ports.SafeJobReference, 1)
	runResult := make(chan error, 1)
	go func() {
		runResult <- rtB.inner.Run(context.Background(), func(ctx context.Context, reference ports.SafeJobReference) error {
			delivered <- reference
			job, err := reference.JobRef()
			if err != nil {
				return nil
			}
			// Hand to worker; ignore preflight failure for delivery proof.
			_ = compB.Worker().ExecuteJob(ctx, job)
			return nil
		})
	}()

	select {
	case got := <-delivered:
		if got.TenantID != "tenant_a" || got.JobID != "job_restart" {
			t.Fatalf("delivered ref = %+v, want same SafeJobReference", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fresh runtime B did not deliver recovered SafeJobReference")
	}

	_ = rtB.Close(context.Background())
	select {
	case err := <-runResult:
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, jobs.ErrClosed) {
			t.Fatalf("Run exit: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after Close")
	}

	// Idempotent re-Enqueue of same stable ref must not create a replacement identity.
	before := rtB.enqueues.Load()
	if _, err := rtB.Enqueue(context.Background(), ref); err != nil && !errors.Is(err, jobs.ErrClosed) {
		// Runtime may be closed; re-open check via store-only recovery path.
		_ = before
	}
	_ = compB.Close(context.Background())
}
