package composition_test

import (
	"context"
	"errors"
	"sync"
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

// claimGateJobs fails the first ClaimWorker with dependency unavailable so the
// jobs.Runtime handler path used by RunWorkers redelivers the same reference.
type claimGateJobs struct {
	inner  *persistence.MemoryRenderJobStore
	mu     sync.Mutex
	failed bool
	claims atomic.Int32
}

func (s *claimGateJobs) Create(ctx context.Context, c ports.RenderJobCreation) (domain.RenderJob, error) {
	return s.inner.Create(ctx, c)
}
func (s *claimGateJobs) Visible(ctx context.Context, p domain.SecurityPrincipal, id domain.Identifier) (domain.RenderJob, error) {
	return s.inner.Visible(ctx, p, id)
}
func (s *claimGateJobs) Load(ctx context.Context, ref domain.JobRef) (domain.RenderJob, error) {
	return s.inner.Load(ctx, ref)
}
func (s *claimGateJobs) ClaimWorker(ctx context.Context, ref domain.JobRef, lease ports.WorkerLease) (ports.WorkerClaim, error) {
	s.claims.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.failed {
		s.failed = true
		return ports.WorkerClaim{}, ports.ErrDependencyUnavailable
	}
	return s.inner.ClaimWorker(ctx, ref, lease)
}
func (s *claimGateJobs) ObserveAttempt(ctx context.Context, o ports.AttemptObservation) (domain.RenderJob, error) {
	return s.inner.ObserveAttempt(ctx, o)
}
func (s *claimGateJobs) Transition(ctx context.Context, t ports.FencedTransition) (domain.RenderJob, error) {
	return s.inner.Transition(ctx, t)
}
func (s *claimGateJobs) CaptureManifest(ctx context.Context, c ports.ManifestCapture) (domain.RenderJob, error) {
	return s.inner.CaptureManifest(ctx, c)
}
func (s *claimGateJobs) PlaceOutput(ctx context.Context, r ports.PlacementRequest) (ports.PlacementResult, error) {
	return s.inner.PlaceOutput(ctx, r)
}
func (s *claimGateJobs) Cancel(ctx context.Context, m ports.CancelMutation) (domain.RenderJob, error) {
	return s.inner.Cancel(ctx, m)
}
func (s *claimGateJobs) BindAccountLease(ctx context.Context, ref domain.JobRef, tok domain.FencingToken, id domain.ProviderAccountID) error {
	return s.inner.BindAccountLease(ctx, ref, tok, id)
}
func (s *claimGateJobs) AccountLeaseHolder(ctx context.Context, t domain.TenantID, id domain.ProviderAccountID) (domain.Identifier, bool, error) {
	return s.inner.AccountLeaseHolder(ctx, t, id)
}
func (s *claimGateJobs) ReleaseAccountLease(ctx context.Context, ref domain.JobRef, tok domain.FencingToken) error {
	return s.inner.ReleaseAccountLease(ctx, ref, tok)
}
func (s *claimGateJobs) MarkQueuePublished(ctx context.Context, ref domain.JobRef) (domain.RenderJob, error) {
	return s.inner.MarkQueuePublished(ctx, ref)
}
func (s *claimGateJobs) ListUnpublishedQueue(ctx context.Context) ([]domain.RenderJob, error) {
	// Never surface unpublished work — avoids RecoverUnpublishedQueues Enqueue
	// blocking before jobs.Runtime.Run starts (same deadlock class as RunWorkers).
	return nil, nil
}
func (s *claimGateJobs) MarkAdmissionSettled(ctx context.Context, ref domain.JobRef) (domain.RenderJob, error) {
	return s.inner.MarkAdmissionSettled(ctx, ref)
}
func (s *claimGateJobs) MarkPromptPurged(ctx context.Context, ref domain.JobRef) (domain.RenderJob, error) {
	return s.inner.MarkPromptPurged(ctx, ref)
}
func (s *claimGateJobs) RenewWorkerLease(ctx context.Context, ref domain.JobRef, token domain.FencingToken, lease ports.WorkerLease) (domain.RenderJob, error) {
	return s.inner.RenewWorkerLease(ctx, ref, token, lease)
}
func (s *claimGateJobs) MarkClaimedAudited(ctx context.Context, ref domain.JobRef) (domain.RenderJob, error) {
	return s.inner.MarkClaimedAudited(ctx, ref)
}
func (s *claimGateJobs) MarkOutputPlacedAudited(ctx context.Context, ref domain.JobRef) (domain.RenderJob, error) {
	return s.inner.MarkOutputPlacedAudited(ctx, ref)
}
func (s *claimGateJobs) MarkTerminalAudited(ctx context.Context, ref domain.JobRef) (domain.RenderJob, error) {
	return s.inner.MarkTerminalAudited(ctx, ref)
}
func (s *claimGateJobs) MarkStagingPurgePending(ctx context.Context, ref domain.JobRef, pending bool) (domain.RenderJob, error) {
	return s.inner.MarkStagingPurgePending(ctx, ref, pending)
}

// TestRunWorkersRetainsReferenceOnClaimDependency mirrors
// jobs.TestRuntimeRedeliversSameReferenceAfterHandlerError: start Run, Enqueue
// one item, first handler fails (claim dependency), second Run redelivers the
// same SafeJobReference without a new Enqueue, then Close stops the idle loop.
//
// Uses the same jobs.Runtime handler contract as composition.RunWorkers
// (SafeJobReference → JobRef → Worker.ExecuteJob) with a bounded one-item runner.
func TestRunWorkersRetainsReferenceOnClaimDependency(t *testing.T) {
	t.Parallel()

	jobRT := jobs.New()
	if err := jobRT.Restore(context.Background()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	inner := persistence.NewMemoryRenderJobStore()
	gate := &claimGateJobs{inner: inner}

	now := time.Date(2026, 7, 24, 20, 0, 0, 0, time.UTC)
	digester, err := vaultpkg.NewHMACRenderDigester([]byte(vaultpkg.FixtureRenderDigestKey))
	if err != nil {
		t.Fatalf("digester: %v", err)
	}

	rt, err := composition.New(composition.Config{AllowInMemoryRenderJobs: true}, composition.Dependencies{
		Runtime:        jobRT,
		Clock:          clockNow{t: now},
		IDs:            &rwSeqIDs{},
		RenderJobs:     gate,
		RenderDigester: digester,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !rt.Ready() {
		t.Fatal("Ready() = false, want true")
	}

	seed := domain.NewQueuedRenderJob(
		"job_rw", "tenant_a", "key_a", domain.RenderOpImageGeneration, "m",
		"digest", nil, "", "pa_1", 1, "fp", "idem", domain.NewTimestamp(now),
	)
	seed.QueuePublished = true
	if _, err := gate.Create(context.Background(), ports.RenderJobCreation{
		Principal: domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a"},
		Job:       seed,
	}); err != nil {
		t.Fatalf("seed Create: %v", err)
	}

	// Same handler shape as composition.RunWorkers (without RecoverUnpublishedQueues
	// side paths that can block Enqueue before Run accepts).
	runHandler := func(ctx context.Context, reference ports.SafeJobReference) error {
		job, err := reference.JobRef()
		if err != nil {
			return nil
		}
		return rt.Worker().ExecuteJob(ctx, job)
	}

	// --- first Run: one item, claim fails, pending retain ---
	firstDone := make(chan struct{})
	firstResult := make(chan error, 1)
	go func() {
		firstResult <- jobRT.Run(context.Background(), func(ctx context.Context, ref ports.SafeJobReference) error {
			err := runHandler(ctx, ref)
			close(firstDone)
			return err
		})
	}()

	if _, err := jobRT.Enqueue(context.Background(), ports.SafeJobReference{
		TenantID: "tenant_a",
		JobID:    "job_rw",
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("first handler not invoked")
	}
	err1 := <-firstResult
	if err1 == nil {
		t.Fatal("first Run() error = nil, want claim dependency failure")
	}
	if !errors.Is(err1, ports.ErrDependencyUnavailable) {
		t.Fatalf("first Run() err = %v, want ErrDependencyUnavailable", err1)
	}
	if got := gate.claims.Load(); got != 1 {
		t.Fatalf("claims after first Run = %d, want 1", got)
	}

	// --- second Run: redelivery of same SafeJobReference, no new Enqueue ---
	redelivered := make(chan struct{})
	secondResult := make(chan error, 1)
	go func() {
		secondResult <- jobRT.Run(context.Background(), func(ctx context.Context, ref ports.SafeJobReference) error {
			if ref.JobID != "job_rw" || ref.TenantID != "tenant_a" {
				t.Errorf("redelivery ref = %+v, want tenant_a/job_rw", ref)
			}
			err := runHandler(ctx, ref)
			close(redelivered)
			return err
		})
	}()

	select {
	case <-redelivered:
	case <-time.After(2 * time.Second):
		t.Fatal("pending reference was not redelivered on second Run")
	}
	if got := gate.claims.Load(); got < 2 {
		t.Fatalf("claims after redelivery = %d, want ≥2", got)
	}

	// Stop idle second Run exactly like jobs.Runtime redelivery test.
	_ = jobRT.Close(context.Background())
	select {
	case <-secondResult:
	case <-time.After(2 * time.Second):
		t.Fatal("second Run did not exit after Close")
	}
}

type clockNow struct{ t time.Time }

func (c clockNow) Now() time.Time { return c.t }

type rwSeqIDs struct{ n atomic.Int64 }

func (s *rwSeqIDs) New(kind domain.IdentifierKind) (domain.Identifier, error) {
	n := s.n.Add(1)
	return domain.Identifier(string(kind) + "_" + rwItoa(n)), nil
}

func rwItoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
