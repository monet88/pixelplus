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

// claimGateJobs fails the first ClaimWorker with dependency unavailable so
// RunWorkers must redeliver the same SafeJobReference via jobs.Runtime pending.
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
	return s.inner.ListUnpublishedQueue(ctx)
}
func (s *claimGateJobs) MarkAdmissionSettled(ctx context.Context, ref domain.JobRef) (domain.RenderJob, error) {
	return s.inner.MarkAdmissionSettled(ctx, ref)
}
func (s *claimGateJobs) MarkPromptPurged(ctx context.Context, ref domain.JobRef) (domain.RenderJob, error) {
	return s.inner.MarkPromptPurged(ctx, ref)
}

// TestRunWorkersRetainsReferenceOnClaimDependency proves composition.RunWorkers
// + jobs.Runtime pending redelivery after ExecuteJob claim dependency failure.
func TestRunWorkersRetainsReferenceOnClaimDependency(t *testing.T) {
	t.Parallel()

	jobRT := jobs.New()
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
		t.Fatal("Ready() = false, want true for RunWorkers")
	}

	// Seed one queued published job into the flaky store.
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

	// Start RunWorkers first — jobs.Runtime.Enqueue blocks until a consumer accepts.
	ctx1, cancel1 := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() { firstDone <- rt.RunWorkers(ctx1) }()

	// Deliver one safe reference; first claim fails → handler error → Run exits.
	if _, err := jobRT.Enqueue(context.Background(), ports.SafeJobReference{
		TenantID: "tenant_a", JobID: "job_rw",
	}); err != nil {
		cancel1()
		t.Fatalf("Enqueue: %v", err)
	}

	var err1 error
	select {
	case err1 = <-firstDone:
	case <-time.After(3 * time.Second):
		cancel1()
		t.Fatal("first RunWorkers did not exit after claim failure")
	}
	cancel1()
	if err1 == nil {
		t.Fatal("first RunWorkers error = nil, want claim dependency failure")
	}
	if !errors.Is(err1, ports.ErrDependencyUnavailable) {
		t.Fatalf("first RunWorkers err = %v, want ErrDependencyUnavailable", err1)
	}
	if got := gate.claims.Load(); got != 1 {
		t.Fatalf("claims after first run = %d, want 1", got)
	}

	// Second RunWorkers: pending redelivery of same SafeJobReference (no new Enqueue).
	// Claim succeeds this time; claim count proves the retained reference was delivered.
	ctx2, cancel2 := context.WithCancel(context.Background())
	secondDone := make(chan error, 1)
	go func() { secondDone <- rt.RunWorkers(ctx2) }()
	// Wait until second claim observed or timeout.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && gate.claims.Load() < 2 {
		time.Sleep(20 * time.Millisecond)
	}
	cancel2()
	select {
	case <-secondDone:
	case <-time.After(2 * time.Second):
		t.Fatal("second RunWorkers did not exit")
	}
	if got := gate.claims.Load(); got < 2 {
		t.Fatalf("claims after second run = %d, want ≥2 (pending redelivery)", got)
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
