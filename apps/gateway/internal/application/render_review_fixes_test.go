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

	// Active lease still blocks other claimants before expiry.
	if _, err := jobs.ClaimWorker(context.Background(), job.JobRef(), ports.WorkerLease{
		WorkerID: "w_live", Now: domain.NewTimestamp(base.Add(30 * time.Second)),
	}); err == nil {
		t.Fatal("active cancel_requested lease must block ClaimWorker")
	}

	counter := &countingNoopAuthorized{}
	prompts := &cleanupPromptStore{material: map[string]string{"tenant_a/job_cr": "secret-prompt"}}
	svc, err := application.NewRenderService(application.RenderDependencies{
		Principal: noopPrincipal{}, Admission: &keyedAdmissionStore{settled: map[string]struct{}{}},
		Replay: noopRenderReplay{}, Jobs: jobs, Accounts: noopAccounts{}, Capabilities: noopCapabilities{},
		Routing: noopRouting{}, Assets: noopAssets{}, Content: noopContent{}, Staging: noopStaging{},
		Vault: noopVault{}, Prompts: prompts, Authorized: counter, Digester: noopDigester{},
		Queue: noopQueue{}, Audit: &failOnceAudit{}, Telemetry: noopTelemetry{}, RequestLog: noopRequestLog{},
		Clock: fixedClock{now: base.Add(5 * time.Minute)}, IDs: fixedIDs{},
	})
	if err != nil {
		t.Fatalf("NewRenderService: %v", err)
	}

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
		t.Fatalf("cleanup incomplete purged=%v settled=%v", jobs.job.PromptPurged, jobs.job.AdmissionSettled)
	}
	if prompts.deleteCalls < 1 {
		t.Fatalf("prompt Delete calls = %d, want >= 1", prompts.deleteCalls)
	}
	if _, ok := prompts.material["tenant_a/job_cr"]; ok {
		t.Fatal("prompt material must be purged after terminal recovery")
	}
	// Stale original fence cannot rewrite canceled.
	_, _ = jobs.Transition(context.Background(), ports.FencedTransition{
		JobRef: job.JobRef(), FencingToken: 1, To: domain.JobFailed,
		Now: domain.NewTimestamp(base.Add(5 * time.Minute)),
	})
	if jobs.job.Lifecycle != domain.JobCanceled {
		t.Fatalf("lifecycle after stale attempt = %v, want canceled", jobs.job.Lifecycle)
	}
	// Terminal redelivery: still zero Provider work.
	if err := svc.ExecuteJob(context.Background(), job.JobRef()); err != nil {
		t.Fatalf("second ExecuteJob: %v", err)
	}
	if counter.calls.Load() != 0 {
		t.Fatalf("render after terminal redelivery = %d, want 0", counter.calls.Load())
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

// --- Staging Delete failure after durable placement (required regression) ---

// flakyStagingDelete fails the first N Delete calls, then succeeds.
type flakyStagingDelete struct {
	mu      sync.Mutex
	failN   int
	deletes int
	// reserveAttempts tracks accidental Asset reserve if wired (must stay 0 on purge-only).
}

func (s *flakyStagingDelete) Put(context.Context, ports.StagingPut) error { return nil }
func (s *flakyStagingDelete) Use(context.Context, ports.StagingAccess, func([]byte) error) error {
	return ports.ErrStagingNotFound
}
func (s *flakyStagingDelete) Delete(context.Context, ports.StagingIdentity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes++
	if s.failN > 0 {
		s.failN--
		return ports.ErrDependencyUnavailable
	}
	return nil
}

type countingReserveAssets struct {
	reserves atomic.Int32
	asset    domain.Asset
}

func (a *countingReserveAssets) Reserve(context.Context, ports.AssetReservation) error {
	a.reserves.Add(1)
	return ports.ErrDependencyUnavailable // purge-only path must never call Reserve
}
func (a *countingReserveAssets) Commit(context.Context, ports.AssetCreation) (domain.Asset, error) {
	return domain.Asset{}, ports.ErrDependencyUnavailable
}
func (a *countingReserveAssets) Release(context.Context, ports.AssetReservation) error { return nil }
func (a *countingReserveAssets) Visible(context.Context, domain.SecurityPrincipal, domain.AssetID) (domain.Asset, error) {
	if a.asset.ID != "" {
		return a.asset, nil
	}
	return domain.Asset{}, ports.ErrAssetNotVisible
}

// Staging Delete fails after placement is durable; redelivery retries purge only
// (zero Authorized.Render, zero Asset.Reserve, asset id unchanged).
func TestStagingDeleteFailureAfterPlacementRetriedWithoutRerender(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 25, 1, 0, 0, 0, time.UTC)
	jobs := &auditJobStore{}
	staging := &flakyStagingDelete{failN: 1}
	assets := &countingReserveAssets{asset: domain.Asset{ID: "asset_stable", ContentType: domain.ContentTypePNG, ByteSize: 4}}
	auth := &countingNoopAuthorized{}
	audit := &failOnceAudit{} // no failures
	prompts := &cleanupPromptStore{material: map[string]string{"tenant_a/job_stg": "p"}}

	job := domain.NewQueuedRenderJob(
		"job_stg", "tenant_a", "key_a", domain.RenderOpImageGeneration, "m",
		"d", nil, "", "pa_1", 1, "fp", "idem", domain.NewTimestamp(now),
	)
	job.Lifecycle = domain.JobCompleted
	job.TerminalAt = domain.NewTimestamp(now)
	job.WorkerID = "w1"
	job.ClaimedAudited = true
	job.OutputPlacedAudited = true
	job.TerminalAudited = true
	job.PromptPurged = true
	job.AdmissionSettled = true
	job.StagingPurgePending = true
	job.Manifest = domain.ResultManifest{
		ID: "man_stg",
		Entries: []domain.OutputEntry{{
			ID: "e0", DeliveryState: domain.OutputAvailable, AssetID: "asset_stable",
			Checksum: "chk1", ContentType: domain.ContentTypePNG, ByteSize: 4,
		}},
	}
	job.OutputEntries = append([]domain.OutputEntry(nil), job.Manifest.Entries...)
	jobs.job = job

	svc, err := application.NewRenderService(application.RenderDependencies{
		Principal: noopPrincipal{}, Admission: &keyedAdmissionStore{settled: map[string]struct{}{}},
		Replay: noopRenderReplay{}, Jobs: jobs, Accounts: noopAccounts{}, Capabilities: noopCapabilities{},
		Routing: noopRouting{}, Assets: assets, Content: noopContent{}, Staging: staging,
		Vault: noopVault{}, Prompts: prompts, Authorized: auth, Digester: noopDigester{},
		Queue: noopQueue{}, Audit: audit, Telemetry: noopTelemetry{}, RequestLog: noopRequestLog{},
		Clock: fixedClock{now: now}, IDs: fixedIDs{},
	})
	if err != nil {
		t.Fatalf("NewRenderService: %v", err)
	}

	// First redelivery: purge Delete fails → error, marker still pending.
	if err := svc.ExecuteJob(context.Background(), job.JobRef()); err == nil {
		t.Fatal("want staging Delete failure")
	}
	if staging.deletes != 1 {
		t.Fatalf("Delete calls = %d, want 1", staging.deletes)
	}
	if !jobs.job.StagingPurgePending {
		t.Fatal("StagingPurgePending must remain true after Delete failure")
	}
	if jobs.job.OutputEntries[0].AssetID != "asset_stable" {
		t.Fatalf("asset id changed to %q", jobs.job.OutputEntries[0].AssetID)
	}
	if auth.calls.Load() != 0 {
		t.Fatalf("render calls = %d, want 0", auth.calls.Load())
	}
	if assets.reserves.Load() != 0 {
		t.Fatalf("Asset.Reserve calls = %d, want 0 (no double reserve)", assets.reserves.Load())
	}

	// Second redelivery: Delete succeeds, purge debt cleared.
	if err := svc.ExecuteJob(context.Background(), job.JobRef()); err != nil {
		t.Fatalf("purge retry: %v", err)
	}
	if staging.deletes != 2 {
		t.Fatalf("Delete calls = %d, want 2", staging.deletes)
	}
	if jobs.job.StagingPurgePending {
		t.Fatal("StagingPurgePending must be false after successful purge")
	}
	if jobs.job.OutputEntries[0].AssetID != "asset_stable" {
		t.Fatal("asset id must stay durable")
	}
	if auth.calls.Load() != 0 {
		t.Fatalf("render after purge retry = %d, want 0", auth.calls.Load())
	}
	if assets.reserves.Load() != 0 {
		t.Fatalf("Reserve after purge retry = %d, want 0", assets.reserves.Load())
	}

	// Third: no extra Delete / reserve / render.
	if err := svc.ExecuteJob(context.Background(), job.JobRef()); err != nil {
		t.Fatalf("third: %v", err)
	}
	if staging.deletes != 2 {
		t.Fatalf("Delete calls after settled = %d, want 2", staging.deletes)
	}
	if auth.calls.Load() != 0 || assets.reserves.Load() != 0 {
		t.Fatalf("side effects after settled render=%d reserve=%d", auth.calls.Load(), assets.reserves.Load())
	}
}

// --- Heartbeat renewal failure cancels Adapter; no capture under lost fence ---

// scriptedWorkerStore is a narrow in-package job store for heartbeat ExecuteJob.
type scriptedWorkerStore struct {
	mu             sync.Mutex
	job            domain.RenderJob
	fence          domain.FencingToken
	renews         int
	failRenewAfter int // 0 = never fail; N = fail on Nth renew
	observeCalls   int
	captureCalls   int
	placeCalls     int
	transitionTo   []domain.JobLifecycleState
}

func (s *scriptedWorkerStore) Create(context.Context, ports.RenderJobCreation) (domain.RenderJob, error) {
	return s.job, nil
}
func (s *scriptedWorkerStore) Visible(context.Context, domain.SecurityPrincipal, domain.Identifier) (domain.RenderJob, error) {
	return s.job, nil
}
func (s *scriptedWorkerStore) Load(context.Context, domain.JobRef) (domain.RenderJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.job, nil
}
func (s *scriptedWorkerStore) ClaimWorker(_ context.Context, ref domain.JobRef, lease ports.WorkerLease) (ports.WorkerClaim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job.JobRef() != ref {
		return ports.WorkerClaim{}, ports.ErrRenderJobNotVisible
	}
	if s.job.Lifecycle.Terminal() {
		return ports.WorkerClaim{}, domain.ErrJobNotClaimable
	}
	s.fence++
	s.job.Lifecycle = domain.JobRunning
	s.job.WorkerID = lease.WorkerID
	s.job.WorkerFencingToken = s.fence
	s.job.LeaseHeld = true
	s.job.LeaseExpiresAt = lease.ExpiresAt
	if s.job.LeaseExpiresAt.IsZero() {
		s.job.LeaseExpiresAt = domain.NewTimestamp(lease.Now.Time().Add(2 * time.Minute))
	}
	return ports.WorkerClaim{Job: s.job, FencingToken: s.fence}, nil
}
func (s *scriptedWorkerStore) ObserveAttempt(_ context.Context, o ports.AttemptObservation) (domain.RenderJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observeCalls++
	if o.FencingToken != s.job.WorkerFencingToken {
		return domain.RenderJob{}, domain.ErrStaleFence
	}
	s.job.Attempt = o.Attempt
	s.job.CommitStatus = o.CommitStatus
	return s.job, nil
}
func (s *scriptedWorkerStore) Transition(_ context.Context, tr ports.FencedTransition) (domain.RenderJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tr.FencingToken != 0 && tr.FencingToken != s.job.WorkerFencingToken {
		return domain.RenderJob{}, domain.ErrStaleFence
	}
	s.job.Lifecycle = tr.To
	s.transitionTo = append(s.transitionTo, tr.To)
	if tr.CommitStatus.Valid() {
		s.job.CommitStatus = tr.CommitStatus
	}
	if tr.To.Terminal() {
		s.job.TerminalAt = tr.Now
		s.job.LeaseHeld = false
	}
	return s.job, nil
}
func (s *scriptedWorkerStore) CaptureManifest(_ context.Context, c ports.ManifestCapture) (domain.RenderJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.captureCalls++
	s.job.Manifest = c.Manifest
	s.job.OutputEntries = append([]domain.OutputEntry(nil), c.Manifest.Entries...)
	return s.job, nil
}
func (s *scriptedWorkerStore) PlaceOutput(context.Context, ports.PlacementRequest) (ports.PlacementResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.placeCalls++
	return ports.PlacementResult{Job: s.job}, nil
}
func (s *scriptedWorkerStore) Cancel(context.Context, ports.CancelMutation) (domain.RenderJob, error) {
	return s.job, nil
}
func (s *scriptedWorkerStore) BindAccountLease(context.Context, domain.JobRef, domain.FencingToken, domain.ProviderAccountID) error {
	return nil
}
func (s *scriptedWorkerStore) AccountLeaseHolder(context.Context, domain.TenantID, domain.ProviderAccountID) (domain.Identifier, bool, error) {
	return "", false, nil
}
func (s *scriptedWorkerStore) ReleaseAccountLease(context.Context, domain.JobRef, domain.FencingToken) error {
	return nil
}
func (s *scriptedWorkerStore) MarkQueuePublished(context.Context, domain.JobRef) (domain.RenderJob, error) {
	return s.job, nil
}
func (s *scriptedWorkerStore) ListUnpublishedQueue(context.Context) ([]domain.RenderJob, error) {
	return nil, nil
}
func (s *scriptedWorkerStore) MarkAdmissionSettled(context.Context, domain.JobRef) (domain.RenderJob, error) {
	s.job.AdmissionSettled = true
	return s.job, nil
}
func (s *scriptedWorkerStore) MarkPromptPurged(context.Context, domain.JobRef) (domain.RenderJob, error) {
	s.job.PromptPurged = true
	return s.job, nil
}
func (s *scriptedWorkerStore) MarkClaimedAudited(context.Context, domain.JobRef) (domain.RenderJob, error) {
	s.job.ClaimedAudited = true
	return s.job, nil
}
func (s *scriptedWorkerStore) MarkOutputPlacedAudited(context.Context, domain.JobRef) (domain.RenderJob, error) {
	s.job.OutputPlacedAudited = true
	return s.job, nil
}
func (s *scriptedWorkerStore) MarkTerminalAudited(context.Context, domain.JobRef) (domain.RenderJob, error) {
	s.job.TerminalAudited = true
	return s.job, nil
}
func (s *scriptedWorkerStore) MarkStagingPurgePending(_ context.Context, _ domain.JobRef, p bool) (domain.RenderJob, error) {
	s.job.StagingPurgePending = p
	return s.job, nil
}
func (s *scriptedWorkerStore) RenewWorkerLease(_ context.Context, _ domain.JobRef, tok domain.FencingToken, lease ports.WorkerLease) (domain.RenderJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.renews++
	if s.failRenewAfter > 0 && s.renews >= s.failRenewAfter {
		return domain.RenderJob{}, domain.ErrStaleFence
	}
	if tok != s.job.WorkerFencingToken {
		return domain.RenderJob{}, domain.ErrStaleFence
	}
	s.job.HeartbeatAt = lease.Now
	s.job.LeaseExpiresAt = lease.ExpiresAt
	return s.job, nil
}

// blockingAuthorized blocks until ctx is canceled (heartbeat cancel path).
type blockingAuthorized struct {
	entered chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

func (a *blockingAuthorized) Render(ctx context.Context, _ ports.AuthorizedRenderRequest) (domain.RenderOutcome, error) {
	a.calls.Add(1)
	a.once.Do(func() {
		if a.entered != nil {
			close(a.entered)
		}
	})
	<-ctx.Done()
	return domain.RenderOutcome{}, ctx.Err()
}

type fixedAccountStore struct {
	account domain.ProviderAccount
}

func (f fixedAccountStore) Create(context.Context, ports.AccountCreation) (domain.ProviderAccount, error) {
	return domain.ProviderAccount{}, ports.ErrDependencyUnavailable
}
func (f fixedAccountStore) Visible(context.Context, domain.SecurityPrincipal, domain.ProviderAccountID) (domain.ProviderAccount, error) {
	return f.account, nil
}
func (f fixedAccountStore) List(context.Context, domain.SecurityPrincipal) ([]domain.ProviderAccount, error) {
	return nil, nil
}
func (f fixedAccountStore) Update(context.Context, ports.AccountUpdate) (domain.ProviderAccount, error) {
	return domain.ProviderAccount{}, ports.ErrDependencyUnavailable
}
func (f fixedAccountStore) Restore(context.Context) error { return nil }

type alwaysValidVault struct{}

func (alwaysValidVault) Put(context.Context, ports.CredentialIntake) error { return nil }
func (alwaysValidVault) Validate(context.Context, ports.CredentialValidation) (ports.CredentialValidationResult, error) {
	return ports.CredentialValidationResult{Valid: true}, nil
}
func (alwaysValidVault) Revoke(context.Context, ports.CredentialValidation) error { return nil }

type offerCapStore struct {
	now time.Time
}

func (o offerCapStore) Get(context.Context, domain.SecurityPrincipal, domain.ProviderAccountID) (domain.CapabilitySnapshot, error) {
	ts := domain.NewTimestamp(o.now)
	ops := map[domain.CapabilityOperation]domain.CapabilityFact{
		domain.CapabilityOpImageGeneration: {
			Status:        domain.CapabilityVerified,
			EvidenceClass: domain.EvidenceLiveProbe,
			ProbeSurface:  "/images/generations",
		},
	}
	return domain.NewLiveProbeSnapshot("pa_1", domain.AuthModeChatGPTCodexOAuth, 1, ts, ops, nil, "/images/generations"), nil
}
func (offerCapStore) List(context.Context, domain.SecurityPrincipal) ([]domain.CapabilitySnapshot, error) {
	return nil, nil
}
func (offerCapStore) Put(context.Context, domain.SecurityPrincipal, domain.CapabilitySnapshot) error {
	return nil
}

func usableReviewAccount() domain.ProviderAccount {
	return domain.ProviderAccount{
		ID:         "pa_1",
		Provider:   domain.ProviderChatGPT,
		AuthMode:   domain.AuthModeChatGPTCodexOAuth,
		Lifecycle:  domain.LifecycleActive,
		Credential: domain.CredentialMetadata{Version: 1},
		Health: domain.HealthSummary{
			SummaryState: domain.HealthHealthy,
		},
		Controls: domain.AdministrativeControls{
			AuthModeExecutionEnabled: true,
		},
		RiskAcknowledged: true,
	}
}

// Heartbeat: blocked Authorized + RenewWorkerLease fails on first tick →
// ExecuteJob returns error, zero CaptureManifest/PlaceOutput/completed.
func TestHeartbeatRenewalFailureCancelsAdapterNoCapture(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 25, 2, 30, 0, 0, time.UTC)
	jobs := &scriptedWorkerStore{
		job: domain.NewQueuedRenderJob(
			"job_hb2", "tenant_a", "key_a", domain.RenderOpImageGeneration, "m",
			"d", nil, "", "pa_1", 1, "fp", "idem", domain.NewTimestamp(now),
		),
		failRenewAfter: 1,
	}
	auth := &blockingAuthorized{entered: make(chan struct{})}
	prompts := &cleanupPromptStore{material: map[string]string{"tenant_a/job_hb2": "prompt"}}

	svc, err := application.NewRenderService(application.RenderDependencies{
		Principal: noopPrincipal{}, Admission: &keyedAdmissionStore{settled: map[string]struct{}{}},
		Replay: noopRenderReplay{}, Jobs: jobs,
		Accounts:          fixedAccountStore{account: usableReviewAccount()},
		Capabilities:      offerCapStore{now: now},
		Routing:           noopRouting{},
		Assets:            noopAssets{},
		Content:           noopContent{},
		Staging:           &flakyStagingDelete{},
		Vault:             alwaysValidVault{},
		Prompts:           prompts,
		Authorized:        auth,
		Digester:          noopDigester{},
		Queue:             noopQueue{},
		Audit:             &failOnceAudit{},
		Telemetry:         noopTelemetry{},
		RequestLog:        noopRequestLog{},
		Clock:             fixedClock{now: now},
		IDs:               fixedIDs{},
		WorkerLeaseTTL:    100 * time.Millisecond,
		HeartbeatInterval: 15 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRenderService: %v", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- svc.ExecuteJob(context.Background(), jobs.job.JobRef()) }()

	select {
	case <-auth.entered:
	case err := <-errCh:
		t.Fatalf("adapter never entered (preflight failed?): %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for Authorized.Render entry")
	}

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("want heartbeat/context error after renew failure")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ExecuteJob hung after heartbeat cancel")
	}

	if jobs.captureCalls != 0 {
		t.Fatalf("CaptureManifest calls = %d, want 0", jobs.captureCalls)
	}
	if jobs.placeCalls != 0 {
		t.Fatalf("PlaceOutput calls = %d, want 0", jobs.placeCalls)
	}
	if jobs.job.Lifecycle == domain.JobCompleted {
		t.Fatal("must not complete after heartbeat loss")
	}
	if jobs.job.Manifest.ID != "" {
		t.Fatal("manifest must not be captured after heartbeat loss")
	}
	if auth.calls.Load() != 1 {
		t.Fatalf("Authorized.Render calls = %d, want 1", auth.calls.Load())
	}
}
