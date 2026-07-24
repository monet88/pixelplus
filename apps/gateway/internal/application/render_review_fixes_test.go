package application_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/application"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// failOnceAudit fails the first Record for a target action, then succeeds.
type failOnceAudit struct {
	mu         sync.Mutex
	actions    []ports.RenderAuditAction
	failAction ports.RenderAuditAction
	failed     bool
}

func (a *failOnceAudit) Record(_ context.Context, event ports.RenderAuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if event.Action == a.failAction && !a.failed {
		a.failed = true
		return ports.ErrDependencyUnavailable
	}
	a.actions = append(a.actions, event.Action)
	return nil
}

func (a *failOnceAudit) count(action ports.RenderAuditAction) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, act := range a.actions {
		if act == action {
			n++
		}
	}
	return n
}

// auditJobStore is a process-local job store for terminal cleanup / audit debt tests.
// No infrastructure imports (architecture boundary).
type auditJobStore struct {
	mu  sync.Mutex
	job domain.RenderJob
}

func (s *auditJobStore) Create(_ context.Context, c ports.RenderJobCreation) (domain.RenderJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.job = c.Job
	return s.job, nil
}
func (s *auditJobStore) Visible(_ context.Context, p domain.SecurityPrincipal, id domain.Identifier) (domain.RenderJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job.JobID != id || s.job.TenantID != p.TenantID {
		return domain.RenderJob{}, ports.ErrRenderJobNotVisible
	}
	return s.job, nil
}
func (s *auditJobStore) Load(_ context.Context, ref domain.JobRef) (domain.RenderJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job.JobRef() != ref {
		return domain.RenderJob{}, ports.ErrRenderJobNotVisible
	}
	return s.job, nil
}
func (s *auditJobStore) ClaimWorker(context.Context, domain.JobRef, ports.WorkerLease) (ports.WorkerClaim, error) {
	return ports.WorkerClaim{}, domain.ErrJobNotClaimable
}
func (s *auditJobStore) ObserveAttempt(context.Context, ports.AttemptObservation) (domain.RenderJob, error) {
	return s.job, nil
}
func (s *auditJobStore) Transition(context.Context, ports.FencedTransition) (domain.RenderJob, error) {
	return s.job, nil
}
func (s *auditJobStore) CaptureManifest(context.Context, ports.ManifestCapture) (domain.RenderJob, error) {
	return s.job, nil
}
func (s *auditJobStore) PlaceOutput(context.Context, ports.PlacementRequest) (ports.PlacementResult, error) {
	return ports.PlacementResult{Job: s.job}, nil
}
func (s *auditJobStore) Cancel(context.Context, ports.CancelMutation) (domain.RenderJob, error) {
	return s.job, nil
}
func (s *auditJobStore) BindAccountLease(context.Context, domain.JobRef, domain.FencingToken, domain.ProviderAccountID) error {
	return nil
}
func (s *auditJobStore) AccountLeaseHolder(context.Context, domain.TenantID, domain.ProviderAccountID) (domain.Identifier, bool, error) {
	return "", false, nil
}
func (s *auditJobStore) ReleaseAccountLease(context.Context, domain.JobRef, domain.FencingToken) error {
	return nil
}
func (s *auditJobStore) MarkQueuePublished(context.Context, domain.JobRef) (domain.RenderJob, error) {
	return s.job, nil
}
func (s *auditJobStore) ListUnpublishedQueue(context.Context) ([]domain.RenderJob, error) {
	return nil, nil
}
func (s *auditJobStore) MarkAdmissionSettled(context.Context, domain.JobRef) (domain.RenderJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.job.AdmissionSettled = true
	return s.job, nil
}
func (s *auditJobStore) MarkPromptPurged(context.Context, domain.JobRef) (domain.RenderJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.job.PromptPurged = true
	return s.job, nil
}
func (s *auditJobStore) MarkClaimedAudited(context.Context, domain.JobRef) (domain.RenderJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.job.ClaimedAudited = true
	return s.job, nil
}
func (s *auditJobStore) MarkOutputPlacedAudited(context.Context, domain.JobRef) (domain.RenderJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.job.OutputPlacedAudited = true
	return s.job, nil
}
func (s *auditJobStore) MarkTerminalAudited(context.Context, domain.JobRef) (domain.RenderJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.job.TerminalAudited = true
	return s.job, nil
}
func (s *auditJobStore) MarkStagingPurgePending(_ context.Context, _ domain.JobRef, pending bool) (domain.RenderJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.job.StagingPurgePending = pending
	return s.job, nil
}
func (s *auditJobStore) RenewWorkerLease(context.Context, domain.JobRef, domain.FencingToken, ports.WorkerLease) (domain.RenderJob, error) {
	return s.job, nil
}

func minimalReviewService(t *testing.T, jobs ports.RenderJobStore, audit ports.RenderAuditRecorder, now time.Time, authorized ports.AuthorizedRender) *application.RenderService {
	t.Helper()
	if authorized == nil {
		authorized = noopAuthorized{}
	}
	svc, err := application.NewRenderService(application.RenderDependencies{
		Principal:    noopPrincipal{},
		Admission:    &keyedAdmissionStore{settled: map[string]struct{}{}},
		Replay:       noopRenderReplay{},
		Jobs:         jobs,
		Accounts:     noopAccounts{},
		Capabilities: noopCapabilities{},
		Routing:      noopRouting{},
		Assets:       noopAssets{},
		Content:      noopContent{},
		Staging:      noopStaging{},
		Vault:        noopVault{},
		Prompts:      &cleanupPromptStore{},
		Authorized:   authorized,
		Digester:     noopDigester{},
		Queue:        noopQueue{},
		Audit:        audit,
		Telemetry:    noopTelemetry{},
		RequestLog:   noopRequestLog{},
		Clock:        fixedClock{now: now},
		IDs:          fixedIDs{},
	})
	if err != nil {
		t.Fatalf("NewRenderService: %v", err)
	}
	return svc
}

func TestTerminalAuditFailureRetriedExactlyOnce(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 24, 21, 0, 0, 0, time.UTC)
	jobs := &auditJobStore{}
	audit := &failOnceAudit{failAction: ports.AuditRenderJobCompleted}
	svc := minimalReviewService(t, jobs, audit, now, nil)

	job := domain.NewQueuedRenderJob(
		"job_ta", "tenant_a", "key_a", domain.RenderOpImageGeneration, "m",
		"d", nil, "", "pa_1", 1, "fp", "idem", domain.NewTimestamp(now),
	)
	job.Lifecycle = domain.JobCompleted
	job.TerminalAt = domain.NewTimestamp(now)
	job.ClaimedAudited = true
	job.OutputPlacedAudited = true
	job.PromptPurged = true
	job.AdmissionSettled = true
	job.WorkerID = "w1"
	job.TerminalAudited = false
	jobs.job = job

	if err := svc.ExecuteJob(context.Background(), job.JobRef()); err == nil {
		t.Fatal("want terminal audit failure")
	}
	if audit.count(ports.AuditRenderJobCompleted) != 0 {
		t.Fatalf("successful completed audits = %d, want 0", audit.count(ports.AuditRenderJobCompleted))
	}
	if jobs.job.TerminalAudited || jobs.job.Lifecycle != domain.JobCompleted {
		t.Fatalf("TerminalAudited=%v lifecycle=%v", jobs.job.TerminalAudited, jobs.job.Lifecycle)
	}

	if err := svc.ExecuteJob(context.Background(), job.JobRef()); err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if audit.count(ports.AuditRenderJobCompleted) != 1 {
		t.Fatalf("completed audits = %d, want 1", audit.count(ports.AuditRenderJobCompleted))
	}
	if !jobs.job.TerminalAudited {
		t.Fatal("TerminalAudited must be true")
	}
	if err := svc.ExecuteJob(context.Background(), job.JobRef()); err != nil {
		t.Fatalf("third: %v", err)
	}
	if audit.count(ports.AuditRenderJobCompleted) != 1 {
		t.Fatalf("duplicate completed audit: %d", audit.count(ports.AuditRenderJobCompleted))
	}
}

func TestOutputPlacedAuditFailureRetriedExactlyOnce(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 24, 21, 30, 0, 0, time.UTC)
	jobs := &auditJobStore{}
	audit := &failOnceAudit{failAction: ports.AuditRenderOutputPlaced}
	svc := minimalReviewService(t, jobs, audit, now, nil)

	job := domain.NewQueuedRenderJob(
		"job_op", "tenant_a", "key_a", domain.RenderOpImageGeneration, "m",
		"d", nil, "", "pa_1", 1, "fp", "idem", domain.NewTimestamp(now),
	)
	job.Lifecycle = domain.JobCompleted
	job.TerminalAt = domain.NewTimestamp(now)
	job.ClaimedAudited = true
	job.PromptPurged = true
	job.AdmissionSettled = true
	job.WorkerID = "w1"
	job.TerminalAudited = true
	job.OutputPlacedAudited = false
	job.Manifest = domain.ResultManifest{ID: "man_1", Entries: []domain.OutputEntry{{
		ID: "e0", DeliveryState: domain.OutputAvailable, AssetID: "asset_1", Checksum: "c",
	}}}
	job.OutputEntries = job.Manifest.Entries
	jobs.job = job

	if err := svc.ExecuteJob(context.Background(), job.JobRef()); err == nil {
		t.Fatal("want output-placed audit failure")
	}
	if jobs.job.OutputEntries[0].AssetID != "asset_1" {
		t.Fatal("placement must remain durable")
	}
	if err := svc.ExecuteJob(context.Background(), job.JobRef()); err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if audit.count(ports.AuditRenderOutputPlaced) != 1 {
		t.Fatalf("output-placed audits = %d, want 1", audit.count(ports.AuditRenderOutputPlaced))
	}
	if err := svc.ExecuteJob(context.Background(), job.JobRef()); err != nil {
		t.Fatalf("third: %v", err)
	}
	if audit.count(ports.AuditRenderOutputPlaced) != 1 {
		t.Fatalf("duplicate output-placed: %d", audit.count(ports.AuditRenderOutputPlaced))
	}
}

// cancelRecoverStore supports claim of cancel_requested when lease expired.
type cancelRecoverStore struct {
	auditJobStore
	fence domain.FencingToken
}

func (s *cancelRecoverStore) ClaimWorker(_ context.Context, ref domain.JobRef, lease ports.WorkerLease) (ports.WorkerClaim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job.JobRef() != ref {
		return ports.WorkerClaim{}, ports.ErrRenderJobNotVisible
	}
	if s.job.Lifecycle != domain.JobCancelRequested {
		return ports.WorkerClaim{}, domain.ErrJobNotClaimable
	}
	// Active lease blocks.
	if s.job.LeaseHeld && !s.job.LeaseExpiresAt.IsZero() && lease.Now.Time().Before(s.job.LeaseExpiresAt.Time()) {
		return ports.WorkerClaim{}, domain.ErrJobNotClaimable
	}
	s.fence++
	s.job.WorkerFencingToken = s.fence
	s.job.WorkerID = lease.WorkerID
	s.job.LeaseHeld = true
	s.job.LeaseExpiresAt = lease.ExpiresAt
	if s.job.LeaseExpiresAt.IsZero() {
		s.job.LeaseExpiresAt = domain.NewTimestamp(lease.Now.Time().Add(2 * time.Minute))
	}
	return ports.WorkerClaim{Job: s.job, FencingToken: s.fence, RecoveryOnly: true}, nil
}

func (s *cancelRecoverStore) Transition(_ context.Context, tr ports.FencedTransition) (domain.RenderJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tr.FencingToken != 0 && s.job.WorkerFencingToken != tr.FencingToken {
		return domain.RenderJob{}, domain.ErrStaleFence
	}
	s.job.Lifecycle = tr.To
	if tr.To.Terminal() {
		s.job.TerminalAt = tr.Now
		s.job.LeaseHeld = false
	}
	if tr.CommitStatus.Valid() {
		s.job.CommitStatus = tr.CommitStatus
	}
	return s.job, nil
}

func TestCancelRequestedExpiredLeaseRecoversWithoutRender(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 7, 24, 22, 0, 0, 0, time.UTC)
	jobs := &cancelRecoverStore{}
	job := domain.NewQueuedRenderJob(
		"job_cr", "tenant_a", "key_a", domain.RenderOpImageGeneration, "m",
		"d", nil, "", "pa_1", 1, "fp", "idem", domain.NewTimestamp(base),
	)
	job.Lifecycle = domain.JobCancelRequested
	job.LeaseHeld = true
	job.LeaseExpiresAt = domain.NewTimestamp(base.Add(time.Minute))
	job.WorkerID = "w_dead"
	job.WorkerFencingToken = 1
	job.PromptPurged = false
	job.AdmissionSettled = false
	jobs.job = job
	jobs.fence = 1

	counter := &countingNoopAuthorized{}
	// Clock past lease expiry.
	svc := minimalReviewService(t, jobs, &failOnceAudit{}, base.Add(5*time.Minute), counter)

	if err := svc.ExecuteJob(context.Background(), job.JobRef()); err != nil {
		t.Fatalf("recovery ExecuteJob: %v", err)
	}
	if counter.calls.Load() != 0 {
		t.Fatalf("Provider render calls = %d, want 0", counter.calls.Load())
	}
	if jobs.job.Lifecycle != domain.JobCanceled {
		t.Fatalf("lifecycle = %v, want canceled", jobs.job.Lifecycle)
	}
	if !jobs.job.PromptPurged || !jobs.job.AdmissionSettled {
		t.Fatalf("cleanup purged=%v settled=%v", jobs.job.PromptPurged, jobs.job.AdmissionSettled)
	}
	// Stale fence cannot mutate.
	if _, err := jobs.Transition(context.Background(), ports.FencedTransition{
		JobRef: job.JobRef(), FencingToken: 1, To: domain.JobFailed,
		Now: domain.NewTimestamp(base.Add(5 * time.Minute)),
	}); err == nil && jobs.job.Lifecycle != domain.JobCanceled {
		t.Fatalf("stale mutation changed lifecycle to %v", jobs.job.Lifecycle)
	}
	if jobs.job.Lifecycle != domain.JobCanceled {
		t.Fatalf("lifecycle after stale attempt = %v", jobs.job.Lifecycle)
	}
}

type countingNoopAuthorized struct {
	calls atomic.Int32
}

func (a *countingNoopAuthorized) Render(context.Context, ports.AuthorizedRenderRequest) (domain.RenderOutcome, error) {
	a.calls.Add(1)
	return domain.RenderOutcome{}, ports.ErrRenderAdapterUnavailable
}

// P1: replay Load dependency fails closed (no stale snapshot, zero enqueue).
func TestReplayTerminalLoadErrorDependencyUnavailable(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)
	terminal := domain.NewQueuedRenderJob(
		"job_rp", "tenant_a", "key_a", domain.RenderOpImageGeneration, "m",
		"d", nil, "", "pa_1", 1, "fp", "idem-key", domain.NewTimestamp(now),
	)
	jobs := &loadFailStore{err: ports.ErrDependencyUnavailable}
	replay := &terminalReplay{job: terminal}
	queue := &countQueue{}
	svc, err := application.NewRenderService(application.RenderDependencies{
		Principal: keyPrincipal{}, Admission: &keyedAdmissionStore{settled: map[string]struct{}{}},
		Replay: replay, Jobs: jobs, Accounts: noopAccounts{}, Capabilities: noopCapabilities{},
		Routing: noopRouting{}, Assets: noopAssets{}, Content: noopContent{}, Staging: noopStaging{},
		Vault: noopVault{}, Prompts: &cleanupPromptStore{},
		Authorized: noopAuthorized{}, Digester: noopDigester{}, Queue: queue,
		Audit: &failOnceAudit{}, Telemetry: noopTelemetry{}, RequestLog: noopRequestLog{},
		Clock: fixedClock{now: now}, IDs: fixedIDs{},
	})
	if err != nil {
		t.Fatalf("NewRenderService: %v", err)
	}
	_, cerr := svc.CreateImageGeneration(context.Background(), application.CreateImageGenerationCommand{
		PresentedKeyMaterial: "mat", IdempotencyKey: "idem-key", Model: "m", Prompt: "p",
	})
	if cerr == nil {
		t.Fatal("want error")
	}
	can, ok := cerr.(domain.CanonicalError)
	if !ok {
		t.Fatalf("err type %T: %v", cerr, cerr)
	}
	if can.Code != domain.ErrCodeDependencyUnavailable {
		t.Fatalf("code = %v, want dependency_unavailable", can.Code)
	}
	if queue.n.Load() != 0 {
		t.Fatalf("enqueues = %d, want 0", queue.n.Load())
	}
}

type keyPrincipal struct{}

func (keyPrincipal) Authenticate(context.Context, ports.PresentedClientAPIKey) (domain.SecurityPrincipal, error) {
	return domain.SecurityPrincipal{
		TenantID: "tenant_a", ClientAPIKeyID: "key_a",
		Scopes: domain.NewScopeSet(domain.ScopeImagesGenerate, domain.ScopeJobsManage, domain.ScopeJobsRead),
	}, nil
}

type terminalReplay struct{ job domain.RenderJob }

func (r *terminalReplay) Claim(context.Context, domain.ReplayIdentity) (ports.RenderReplayDecision, error) {
	return ports.RenderReplayDecision{Outcome: ports.ReplayTerminal, TerminalJob: r.job}, nil
}
func (*terminalReplay) Complete(context.Context, domain.ReplayIdentity, ports.RenderReplayResult) error {
	return nil
}
func (*terminalReplay) Abandon(context.Context, domain.ReplayIdentity) error { return nil }

type loadFailStore struct {
	err error
}

func (s *loadFailStore) Create(context.Context, ports.RenderJobCreation) (domain.RenderJob, error) {
	return domain.RenderJob{}, ports.ErrDependencyUnavailable
}
func (s *loadFailStore) Visible(context.Context, domain.SecurityPrincipal, domain.Identifier) (domain.RenderJob, error) {
	return domain.RenderJob{}, ports.ErrRenderJobNotVisible
}
func (s *loadFailStore) Load(context.Context, domain.JobRef) (domain.RenderJob, error) {
	return domain.RenderJob{}, s.err
}
func (s *loadFailStore) ClaimWorker(context.Context, domain.JobRef, ports.WorkerLease) (ports.WorkerClaim, error) {
	return ports.WorkerClaim{}, ports.ErrDependencyUnavailable
}
func (s *loadFailStore) ObserveAttempt(context.Context, ports.AttemptObservation) (domain.RenderJob, error) {
	return domain.RenderJob{}, ports.ErrDependencyUnavailable
}
func (s *loadFailStore) Transition(context.Context, ports.FencedTransition) (domain.RenderJob, error) {
	return domain.RenderJob{}, ports.ErrDependencyUnavailable
}
func (s *loadFailStore) CaptureManifest(context.Context, ports.ManifestCapture) (domain.RenderJob, error) {
	return domain.RenderJob{}, ports.ErrDependencyUnavailable
}
func (s *loadFailStore) PlaceOutput(context.Context, ports.PlacementRequest) (ports.PlacementResult, error) {
	return ports.PlacementResult{}, ports.ErrDependencyUnavailable
}
func (s *loadFailStore) Cancel(context.Context, ports.CancelMutation) (domain.RenderJob, error) {
	return domain.RenderJob{}, ports.ErrDependencyUnavailable
}
func (s *loadFailStore) BindAccountLease(context.Context, domain.JobRef, domain.FencingToken, domain.ProviderAccountID) error {
	return ports.ErrDependencyUnavailable
}
func (s *loadFailStore) AccountLeaseHolder(context.Context, domain.TenantID, domain.ProviderAccountID) (domain.Identifier, bool, error) {
	return "", false, ports.ErrDependencyUnavailable
}
func (s *loadFailStore) ReleaseAccountLease(context.Context, domain.JobRef, domain.FencingToken) error {
	return ports.ErrDependencyUnavailable
}
func (s *loadFailStore) MarkQueuePublished(context.Context, domain.JobRef) (domain.RenderJob, error) {
	return domain.RenderJob{}, ports.ErrDependencyUnavailable
}
func (s *loadFailStore) ListUnpublishedQueue(context.Context) ([]domain.RenderJob, error) {
	return nil, ports.ErrDependencyUnavailable
}
func (s *loadFailStore) MarkAdmissionSettled(context.Context, domain.JobRef) (domain.RenderJob, error) {
	return domain.RenderJob{}, ports.ErrDependencyUnavailable
}
func (s *loadFailStore) MarkPromptPurged(context.Context, domain.JobRef) (domain.RenderJob, error) {
	return domain.RenderJob{}, ports.ErrDependencyUnavailable
}
func (s *loadFailStore) MarkClaimedAudited(context.Context, domain.JobRef) (domain.RenderJob, error) {
	return domain.RenderJob{}, ports.ErrDependencyUnavailable
}
func (s *loadFailStore) MarkOutputPlacedAudited(context.Context, domain.JobRef) (domain.RenderJob, error) {
	return domain.RenderJob{}, ports.ErrDependencyUnavailable
}
func (s *loadFailStore) MarkTerminalAudited(context.Context, domain.JobRef) (domain.RenderJob, error) {
	return domain.RenderJob{}, ports.ErrDependencyUnavailable
}
func (s *loadFailStore) MarkStagingPurgePending(context.Context, domain.JobRef, bool) (domain.RenderJob, error) {
	return domain.RenderJob{}, ports.ErrDependencyUnavailable
}
func (s *loadFailStore) RenewWorkerLease(context.Context, domain.JobRef, domain.FencingToken, ports.WorkerLease) (domain.RenderJob, error) {
	return domain.RenderJob{}, ports.ErrDependencyUnavailable
}

type countQueue struct{ n atomic.Int32 }

func (q *countQueue) Restore(context.Context) error { return nil }
func (q *countQueue) Enqueue(_ context.Context, ref ports.SafeJobReference) (ports.EnqueueReceipt, error) {
	q.n.Add(1)
	return ports.EnqueueReceipt{Reference: ref}, nil
}
func (*countQueue) Run(context.Context, ports.JobHandler) error { return nil }
func (*countQueue) Close(context.Context) error                 { return nil }
