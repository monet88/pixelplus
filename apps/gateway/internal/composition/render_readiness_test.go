package composition_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/composition"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/infrastructure/jobs"
	"github.com/monet88/pixelplus/apps/gateway/internal/infrastructure/persistence"
	vaultpkg "github.com/monet88/pixelplus/apps/gateway/internal/infrastructure/vault"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// ADR 0009: production without durable render recovery ports must not advertise
// execution readiness via /readyz.
func TestProductionMissingRenderDurabilityKeepsReadinessClosed(t *testing.T) {
	t.Parallel()

	runtime, err := composition.New(composition.Config{
		AllowInMemoryRenderJobs: false,
	}, composition.Dependencies{
		Runtime: jobs.New(),
		Clock:   fixedClock{t: time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)},
		IDs:     &seqIDs{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })

	if runtime.Ready() {
		t.Fatal("Ready() = true, want false when render durability is not configured")
	}
	if !runtime.Healthy() {
		t.Fatal("Healthy() = false, want true (process up; readiness closed)")
	}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz status = %d, want 503", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["status"] != "not_ready" {
		t.Fatalf("/readyz body status = %v, want not_ready", body["status"])
	}
}

func TestControlledInMemoryRenderDurabilityAllowsReady(t *testing.T) {
	t.Parallel()

	runtime, err := composition.New(composition.Config{
		AllowInMemoryRenderJobs: true,
	}, composition.Dependencies{
		Runtime: jobs.New(),
		Clock:   fixedClock{t: time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)},
		IDs:     &seqIDs{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })

	if !runtime.Ready() {
		t.Fatal("Ready() = false with AllowInMemoryRenderJobs, want true")
	}
}

// P1-A/C: production with durable injects but missing credential authorizer stays not-ready.
func TestProductionMissingAuthorizerKeepsReadinessClosed(t *testing.T) {
	t.Parallel()

	runtime, err := composition.New(composition.Config{
		AllowInMemoryRenderJobs: false,
	}, composition.Dependencies{
		Runtime: jobs.New(),
		Clock:   fixedClock{t: time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)},
		IDs:     &seqIDs{},
		// Durable ports present but no RenderCredentialAuthorizer inject.
		RenderJobs:    persistence.NewMemoryRenderJobStore(),
		RenderReplay:  persistence.NewMemoryRenderReplayStore(),
		RenderPrompts: vaultpkg.NewMemoryRenderPromptStore(),
		RenderStaging: persistence.NewMemoryRenderStagingStore(),
		// Usable digester key so authorizer is the sole missing readiness gate.
		RenderDigestKey: []byte("0123456789abcdef0123456789abcdef"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })

	if runtime.Ready() {
		t.Fatal("Ready() = true without RenderCredentialAuthorizer, want false")
	}
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz status = %d, want 503", rec.Code)
	}
}

// P1-C: explicit Unavailable job store Restore failure keeps readiness closed.
func TestRenderJobRestoreFailureKeepsReadinessClosed(t *testing.T) {
	t.Parallel()

	runtime, err := composition.New(composition.Config{
		AllowInMemoryRenderJobs: false,
	}, composition.Dependencies{
		Runtime:                    jobs.New(),
		Clock:                      fixedClock{t: time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)},
		IDs:                        &seqIDs{},
		RenderJobs:                 persistence.NewUnavailableRenderJobStore(),
		RenderReplay:               persistence.NewMemoryRenderReplayStore(),
		RenderPrompts:              vaultpkg.NewMemoryRenderPromptStore(),
		RenderStaging:              persistence.NewMemoryRenderStagingStore(),
		RenderCredentialAuthorizer: vaultpkg.NewPermissiveFixtureRenderCredentialAuthorizer(),
		RenderDigestKey:            []byte("0123456789abcdef0123456789abcdef"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })

	if runtime.Ready() {
		t.Fatal("Ready() = true after Restore failure, want false")
	}
	if !runtime.Healthy() {
		t.Fatal("Healthy() = false, want true (process lives; readiness closed)")
	}
}

// jobsOnlyNoRestorer satisfies RenderJobStore methods used by composition wiring
// checks but deliberately does NOT implement ports.Restorer.
type jobsOnlyNoRestorer struct {
	inner *persistence.MemoryRenderJobStore
}

func newJobsOnlyNoRestorer() *jobsOnlyNoRestorer {
	return &jobsOnlyNoRestorer{inner: persistence.NewMemoryRenderJobStore()}
}

func (s *jobsOnlyNoRestorer) Create(ctx context.Context, c ports.RenderJobCreation) (domain.RenderJob, error) {
	return s.inner.Create(ctx, c)
}
func (s *jobsOnlyNoRestorer) Visible(ctx context.Context, p domain.SecurityPrincipal, id domain.Identifier) (domain.RenderJob, error) {
	return s.inner.Visible(ctx, p, id)
}
func (s *jobsOnlyNoRestorer) Load(ctx context.Context, ref domain.JobRef) (domain.RenderJob, error) {
	return s.inner.Load(ctx, ref)
}
func (s *jobsOnlyNoRestorer) ClaimWorker(ctx context.Context, ref domain.JobRef, lease ports.WorkerLease) (ports.WorkerClaim, error) {
	return s.inner.ClaimWorker(ctx, ref, lease)
}
func (s *jobsOnlyNoRestorer) ObserveAttempt(ctx context.Context, o ports.AttemptObservation) (domain.RenderJob, error) {
	return s.inner.ObserveAttempt(ctx, o)
}
func (s *jobsOnlyNoRestorer) Transition(ctx context.Context, t ports.FencedTransition) (domain.RenderJob, error) {
	return s.inner.Transition(ctx, t)
}
func (s *jobsOnlyNoRestorer) CaptureManifest(ctx context.Context, c ports.ManifestCapture) (domain.RenderJob, error) {
	return s.inner.CaptureManifest(ctx, c)
}
func (s *jobsOnlyNoRestorer) PlaceOutput(ctx context.Context, r ports.PlacementRequest) (ports.PlacementResult, error) {
	return s.inner.PlaceOutput(ctx, r)
}
func (s *jobsOnlyNoRestorer) Cancel(ctx context.Context, m ports.CancelMutation) (domain.RenderJob, error) {
	return s.inner.Cancel(ctx, m)
}
func (s *jobsOnlyNoRestorer) BindAccountLease(ctx context.Context, ref domain.JobRef, tok domain.FencingToken, id domain.ProviderAccountID) error {
	return s.inner.BindAccountLease(ctx, ref, tok, id)
}
func (s *jobsOnlyNoRestorer) AccountLeaseHolder(ctx context.Context, t domain.TenantID, id domain.ProviderAccountID) (domain.Identifier, bool, error) {
	return s.inner.AccountLeaseHolder(ctx, t, id)
}
func (s *jobsOnlyNoRestorer) ReleaseAccountLease(ctx context.Context, ref domain.JobRef, tok domain.FencingToken) error {
	return s.inner.ReleaseAccountLease(ctx, ref, tok)
}
func (s *jobsOnlyNoRestorer) MarkQueuePublished(ctx context.Context, ref domain.JobRef) (domain.RenderJob, error) {
	return s.inner.MarkQueuePublished(ctx, ref)
}
func (s *jobsOnlyNoRestorer) ListQueueRecoveryCandidates(ctx context.Context) ([]domain.RenderJob, error) {
	return s.inner.ListQueueRecoveryCandidates(ctx)
}
func (s *jobsOnlyNoRestorer) MarkAdmissionSettled(ctx context.Context, ref domain.JobRef) (domain.RenderJob, error) {
	return s.inner.MarkAdmissionSettled(ctx, ref)
}
func (s *jobsOnlyNoRestorer) MarkPromptPurged(ctx context.Context, ref domain.JobRef) (domain.RenderJob, error) {
	return s.inner.MarkPromptPurged(ctx, ref)
}
func (s *jobsOnlyNoRestorer) RenewWorkerLease(ctx context.Context, ref domain.JobRef, token domain.FencingToken, lease ports.WorkerLease) (domain.RenderJob, error) {
	return s.inner.RenewWorkerLease(ctx, ref, token, lease)
}
func (s *jobsOnlyNoRestorer) MarkClaimedAudited(ctx context.Context, ref domain.JobRef) (domain.RenderJob, error) {
	return s.inner.MarkClaimedAudited(ctx, ref)
}
func (s *jobsOnlyNoRestorer) MarkOutputPlacedAudited(ctx context.Context, ref domain.JobRef) (domain.RenderJob, error) {
	return s.inner.MarkOutputPlacedAudited(ctx, ref)
}
func (s *jobsOnlyNoRestorer) MarkTerminalAudited(ctx context.Context, ref domain.JobRef) (domain.RenderJob, error) {
	return s.inner.MarkTerminalAudited(ctx, ref)
}
func (s *jobsOnlyNoRestorer) MarkStagingPurgePending(ctx context.Context, ref domain.JobRef, pending bool) (domain.RenderJob, error) {
	return s.inner.MarkStagingPurgePending(ctx, ref, pending)
}

// Spec P1-4 CRITICAL: a non-nil required render durability port that satisfies
// its main interface but not ports.Restorer must keep composition not-ready and
// /readyz 503. restoreRenderPorts must fail closed — never silently continue.
func TestRenderDurabilityWithoutRestorerKeepsReadinessClosed(t *testing.T) {
	t.Parallel()

	runtime, err := composition.New(composition.Config{
		AllowInMemoryRenderJobs: false,
	}, composition.Dependencies{
		Runtime: jobs.New(),
		Clock:   fixedClock{t: time.Date(2026, 7, 25, 5, 0, 0, 0, time.UTC)},
		IDs:     &seqIDs{},
		// Main port present; Restorer intentionally absent.
		RenderJobs:                 newJobsOnlyNoRestorer(),
		RenderReplay:               persistence.NewMemoryRenderReplayStore(),
		RenderPrompts:              vaultpkg.NewMemoryRenderPromptStore(),
		RenderStaging:              persistence.NewMemoryRenderStagingStore(),
		RenderCredentialAuthorizer: vaultpkg.NewPermissiveFixtureRenderCredentialAuthorizer(),
		RenderDigestKey:            []byte("0123456789abcdef0123456789abcdef"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })

	if runtime.Ready() {
		t.Fatal("Ready() = true when RenderJobs lacks Restorer, want false")
	}
	if !runtime.Healthy() {
		t.Fatal("Healthy() = false, want true (process up; readiness closed)")
	}
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz status = %d, want 503", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["status"] != "not_ready" {
		t.Fatalf("/readyz body status = %v, want not_ready", body["status"])
	}
}
