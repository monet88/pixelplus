package application_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/application"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// P1-8: Reconcile with SettlementKey is logically once even when MarkAdmissionSettled
// fails and redelivery retries releaseJobAdmission.
func TestAdmissionSettleLogicalOnceWhenMarkerFailsThenRetries(t *testing.T) {
	t.Parallel()

	jobs := &cleanupJobStore{}
	admission := &keyedAdmissionStore{}
	prompts := &cleanupPromptStore{}
	now := time.Date(2026, 7, 24, 18, 0, 0, 0, time.UTC)
	svc := mustRenderService(t, admission, jobs, prompts, now)

	job := domain.NewQueuedRenderJob(
		"job_settle", "tenant_a", "key_a", domain.RenderOpImageGeneration, "m",
		"digest", nil, "", "pa_1", 1, "fp", "idem", domain.NewTimestamp(now),
	)
	job.Lifecycle = domain.JobCompleted
	job.TerminalAt = domain.NewTimestamp(now)
	jobs.job = job

	// First cleanup: Reconcile succeeds, marker write fails.
	jobs.failMarkAdmission = errors.New("marker unavailable")
	if err := svc.ExecuteJob(context.Background(), job.JobRef()); err == nil {
		t.Fatal("expected cleanup error when MarkAdmissionSettled fails")
	}
	if got := admission.logicalSettles; got != 1 {
		t.Fatalf("logical settles after first attempt = %d, want 1", got)
	}
	if admission.reconcileCalls != 1 {
		t.Fatalf("reconcile calls = %d, want 1", admission.reconcileCalls)
	}

	// Second redelivery: Reconcile is keyed no-op for occupancy; marker succeeds.
	jobs.failMarkAdmission = nil
	if err := svc.ExecuteJob(context.Background(), job.JobRef()); err != nil {
		t.Fatalf("second cleanup: %v", err)
	}
	if got := admission.logicalSettles; got != 1 {
		t.Fatalf("logical settles after redelivery = %d, want 1 (idempotent Reconcile)", got)
	}
	if !jobs.job.AdmissionSettled {
		t.Fatal("AdmissionSettled must be true after successful marker")
	}
	if !jobs.job.PromptPurged {
		t.Fatal("PromptPurged must be true after terminal cleanup")
	}
}

// P1-8/P1-13: prompt purge failure still settles admission; redelivery purges
// prompt with zero Provider (claim not claimable path).
func TestPromptPurgeFailureStillSettlesAdmissionThenRetriesPurge(t *testing.T) {
	t.Parallel()

	jobs := &cleanupJobStore{}
	admission := &keyedAdmissionStore{}
	prompts := &cleanupPromptStore{failDelete: errors.New("purge unavailable")}
	now := time.Date(2026, 7, 24, 18, 0, 0, 0, time.UTC)
	svc := mustRenderService(t, admission, jobs, prompts, now)
	job := domain.NewQueuedRenderJob(
		"job_purge", "tenant_a", "key_a", domain.RenderOpImageGeneration, "m",
		"digest", nil, "", "pa_1", 1, "fp", "idem", domain.NewTimestamp(now),
	)
	job.Lifecycle = domain.JobFailed
	job.TerminalAt = domain.NewTimestamp(now)
	jobs.job = job

	if err := svc.ExecuteJob(context.Background(), job.JobRef()); err == nil {
		t.Fatal("expected purge error (joined cleanup debt)")
	}
	if prompts.deleteCalls != 1 {
		t.Fatalf("delete calls = %d, want 1", prompts.deleteCalls)
	}
	// Admission must settle even when prompt purge failed (occupancy not leaked).
	if admission.logicalSettles != 1 {
		t.Fatalf("logical settles after prompt failure = %d, want 1", admission.logicalSettles)
	}
	if !jobs.job.AdmissionSettled {
		t.Fatal("AdmissionSettled must be true after independent settle")
	}
	if jobs.job.PromptPurged {
		t.Fatal("PromptPurged must remain false after purge failure")
	}

	prompts.failDelete = nil
	if err := svc.ExecuteJob(context.Background(), job.JobRef()); err != nil {
		t.Fatalf("retry cleanup: %v", err)
	}
	if prompts.deleteCalls != 2 {
		t.Fatalf("delete calls after retry = %d, want 2", prompts.deleteCalls)
	}
	if !jobs.job.PromptPurged {
		t.Fatal("PromptPurged must be true after successful retry")
	}
	// Third redelivery: no extra admission settle.
	if err := svc.ExecuteJob(context.Background(), job.JobRef()); err != nil {
		t.Fatalf("third cleanup: %v", err)
	}
	if admission.logicalSettles != 1 {
		t.Fatalf("logical settles = %d, want 1", admission.logicalSettles)
	}
}

// P1-11: ClaimWorker dependency errors must propagate for redelivery.
func TestClaimWorkerDependencyErrorPropagates(t *testing.T) {
	t.Parallel()
	jobs := &cleanupJobStore{claimErr: ports.ErrDependencyUnavailable}
	admission := &keyedAdmissionStore{}
	prompts := &cleanupPromptStore{}
	now := time.Date(2026, 7, 24, 18, 0, 0, 0, time.UTC)
	svc := mustRenderService(t, admission, jobs, prompts, now)
	job := domain.NewQueuedRenderJob(
		"job_dep", "tenant_a", "key_a", domain.RenderOpImageGeneration, "m",
		"digest", nil, "", "pa_1", 1, "fp", "idem", domain.NewTimestamp(now),
	)
	jobs.job = job
	err := svc.ExecuteJob(context.Background(), job.JobRef())
	if !errors.Is(err, ports.ErrDependencyUnavailable) {
		t.Fatalf("ExecuteJob err = %v, want ErrDependencyUnavailable", err)
	}
}

func mustRenderService(t *testing.T, admission ports.AdmissionStore, jobs ports.RenderJobStore, prompts ports.RenderPromptStore, now time.Time) *application.RenderService {
	t.Helper()
	svc, err := application.NewRenderService(application.RenderDependencies{
		Principal:    noopPrincipal{},
		Admission:    admission,
		Replay:       noopRenderReplay{},
		Jobs:         jobs,
		Accounts:     noopAccounts{},
		Capabilities: noopCapabilities{},
		Routing:      noopRouting{},
		Assets:       noopAssets{},
		Content:      noopContent{},
		Staging:      noopStaging{},
		Vault:        noopVault{},
		Prompts:      prompts,
		Authorized:   noopAuthorized{},
		Digester:     noopDigester{},
		Queue:        noopQueue{},
		Audit:        noopRenderAudit{},
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

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type fixedIDs struct{}

func (fixedIDs) New(kind domain.IdentifierKind) (domain.Identifier, error) {
	return domain.Identifier("id_" + string(kind)), nil
}

type noopPrincipal struct{}

func (noopPrincipal) Authenticate(context.Context, ports.PresentedClientAPIKey) (domain.SecurityPrincipal, error) {
	return domain.SecurityPrincipal{}, ports.ErrAuthentication
}

type noopRenderReplay struct{}

func (noopRenderReplay) Claim(context.Context, domain.ReplayIdentity) (ports.RenderReplayDecision, error) {
	return ports.RenderReplayDecision{}, ports.ErrDependencyUnavailable
}
func (noopRenderReplay) Complete(context.Context, domain.ReplayIdentity, ports.RenderReplayResult) error {
	return nil
}
func (noopRenderReplay) Abandon(context.Context, domain.ReplayIdentity) error { return nil }

type noopAccounts struct{}

func (noopAccounts) Create(context.Context, ports.AccountCreation) (domain.ProviderAccount, error) {
	return domain.ProviderAccount{}, ports.ErrDependencyUnavailable
}
func (noopAccounts) Visible(context.Context, domain.SecurityPrincipal, domain.ProviderAccountID) (domain.ProviderAccount, error) {
	return domain.ProviderAccount{}, ports.ErrAccountNotVisible
}
func (noopAccounts) List(context.Context, domain.SecurityPrincipal) ([]domain.ProviderAccount, error) {
	return nil, nil
}
func (noopAccounts) Update(context.Context, ports.AccountUpdate) (domain.ProviderAccount, error) {
	return domain.ProviderAccount{}, ports.ErrDependencyUnavailable
}
func (noopAccounts) Restore(context.Context) error { return nil }

type noopCapabilities struct{}

func (noopCapabilities) Get(context.Context, domain.SecurityPrincipal, domain.ProviderAccountID) (domain.CapabilitySnapshot, error) {
	return domain.CapabilitySnapshot{}, ports.ErrCapabilitySnapshotNotFound
}
func (noopCapabilities) List(context.Context, domain.SecurityPrincipal) ([]domain.CapabilitySnapshot, error) {
	return nil, nil
}
func (noopCapabilities) Put(context.Context, domain.SecurityPrincipal, domain.CapabilitySnapshot) error {
	return nil
}

type noopRouting struct{}

func (noopRouting) Read(context.Context, domain.SecurityPrincipal) (domain.RoutingPolicy, error) {
	return domain.RoutingPolicy{}, nil
}
func (noopRouting) Replace(context.Context, ports.RoutingPolicyChange) (domain.RoutingPolicy, error) {
	return domain.RoutingPolicy{}, nil
}

type noopAssets struct{}

func (noopAssets) Reserve(context.Context, ports.AssetReservation) error { return nil }
func (noopAssets) Commit(context.Context, ports.AssetCreation) (domain.Asset, error) {
	return domain.Asset{}, nil
}
func (noopAssets) Release(context.Context, ports.AssetReservation) error { return nil }
func (noopAssets) Visible(context.Context, domain.SecurityPrincipal, domain.AssetID) (domain.Asset, error) {
	return domain.Asset{}, ports.ErrAssetNotVisible
}

type noopContent struct{}

func (noopContent) Put(context.Context, domain.AssetID, []byte) error { return nil }
func (noopContent) Fetch(context.Context, domain.SecurityPrincipal, domain.AssetID) (ports.AssetContent, error) {
	return ports.AssetContent{}, ports.ErrAssetNotVisible
}

type noopStaging struct{}

func (noopStaging) Put(context.Context, ports.StagingPut) error { return nil }
func (noopStaging) Use(context.Context, ports.StagingAccess, func([]byte) error) error {
	return ports.ErrStagingNotFound
}

type noopVault struct{}

func (noopVault) Put(context.Context, ports.CredentialIntake) error { return nil }
func (noopVault) Validate(context.Context, ports.CredentialValidation) (ports.CredentialValidationResult, error) {
	return ports.CredentialValidationResult{Valid: true}, nil
}
func (noopVault) Revoke(context.Context, ports.CredentialValidation) error { return nil }

type noopAuthorized struct{}

func (noopAuthorized) Render(context.Context, ports.AuthorizedRenderRequest) (domain.RenderOutcome, error) {
	return domain.RenderOutcome{}, ports.ErrRenderAdapterUnavailable
}

type noopDigester struct{}

func (noopDigester) DigestPrompt(string) (string, error) { return "d", nil }
func (noopDigester) CreateFingerprint(domain.RenderOperation, string, string, []domain.AssetID, domain.AssetID) (domain.Fingerprint, error) {
	return "fp", nil
}

type noopQueue struct{}

func (noopQueue) Restore(context.Context) error { return nil }
func (noopQueue) Enqueue(_ context.Context, ref ports.SafeJobReference) (ports.EnqueueReceipt, error) {
	return ports.EnqueueReceipt{Reference: ref}, nil
}
func (noopQueue) Run(context.Context, ports.JobHandler) error { return nil }
func (noopQueue) Close(context.Context) error                 { return nil }

type noopRenderAudit struct{}

func (noopRenderAudit) Record(context.Context, ports.RenderAuditEvent) error { return nil }

type noopTelemetry struct{}

func (noopTelemetry) Record(context.Context, ports.TelemetryEvent) error { return nil }

type noopRequestLog struct{}

func (noopRequestLog) Record(context.Context, ports.RequestLog) error { return nil }

type keyedAdmissionStore struct {
	mu             sync.Mutex
	settled        map[string]struct{}
	reconcileCalls int
	logicalSettles int
}

func (s *keyedAdmissionStore) Admit(context.Context, ports.AdmissionRequest) (ports.AdmissionDecision, ports.AdmissionReservation, error) {
	return ports.AdmissionDecision{Admitted: true}, ports.AdmissionReservation{}, nil
}

func (s *keyedAdmissionStore) Reconcile(_ context.Context, reservation ports.AdmissionReservation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reconcileCalls++
	if reservation.SettlementKey == "" {
		return nil
	}
	if s.settled == nil {
		s.settled = make(map[string]struct{})
	}
	if _, ok := s.settled[reservation.SettlementKey]; ok {
		return nil
	}
	s.settled[reservation.SettlementKey] = struct{}{}
	s.logicalSettles++
	return nil
}

type cleanupPromptStore struct {
	failDelete  error
	deleteCalls int
	mu          sync.Mutex
	material    map[string]string
}

func (s *cleanupPromptStore) Put(_ context.Context, intake ports.RenderPromptIntake) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.material == nil {
		s.material = make(map[string]string)
	}
	s.material[string(intake.TenantID)+"/"+string(intake.JobID)] = intake.Material
	return nil
}

func (s *cleanupPromptStore) Use(_ context.Context, access ports.RenderPromptAccess, fn func(string) error) error {
	s.mu.Lock()
	m := s.material[string(access.TenantID)+"/"+string(access.JobID)]
	s.mu.Unlock()
	if m == "" {
		return ports.ErrRenderAdapterUnavailable
	}
	return fn(m)
}

func (s *cleanupPromptStore) Delete(_ context.Context, access ports.RenderPromptAccess) error {
	s.deleteCalls++
	if s.failDelete != nil {
		return s.failDelete
	}
	s.mu.Lock()
	delete(s.material, string(access.TenantID)+"/"+string(access.JobID))
	s.mu.Unlock()
	return nil
}

// cleanupJobStore is a minimal RenderJobStore for terminal cleanup paths.
type cleanupJobStore struct {
	job               domain.RenderJob
	failMarkAdmission error
	failMarkPurge     error
	claimErr          error
}

func (s *cleanupJobStore) Create(context.Context, ports.RenderJobCreation) (domain.RenderJob, error) {
	return s.job, nil
}
func (s *cleanupJobStore) Visible(context.Context, domain.SecurityPrincipal, domain.Identifier) (domain.RenderJob, error) {
	return s.job, nil
}
func (s *cleanupJobStore) Load(context.Context, domain.JobRef) (domain.RenderJob, error) {
	return s.job, nil
}
func (s *cleanupJobStore) ClaimWorker(context.Context, domain.JobRef, ports.WorkerLease) (ports.WorkerClaim, error) {
	if s.claimErr != nil {
		return ports.WorkerClaim{}, s.claimErr
	}
	// Terminal jobs are not claimable → ExecuteJob takes cleanup path.
	return ports.WorkerClaim{}, domain.ErrJobNotClaimable
}
func (s *cleanupJobStore) ObserveAttempt(context.Context, ports.AttemptObservation) (domain.RenderJob, error) {
	return s.job, nil
}
func (s *cleanupJobStore) Transition(context.Context, ports.FencedTransition) (domain.RenderJob, error) {
	return s.job, nil
}
func (s *cleanupJobStore) CaptureManifest(context.Context, ports.ManifestCapture) (domain.RenderJob, error) {
	return s.job, nil
}
func (s *cleanupJobStore) PlaceOutput(context.Context, ports.PlacementRequest) (ports.PlacementResult, error) {
	return ports.PlacementResult{Job: s.job}, nil
}
func (s *cleanupJobStore) Cancel(context.Context, ports.CancelMutation) (domain.RenderJob, error) {
	return s.job, nil
}
func (s *cleanupJobStore) BindAccountLease(context.Context, domain.JobRef, domain.FencingToken, domain.ProviderAccountID) error {
	return nil
}
func (s *cleanupJobStore) AccountLeaseHolder(context.Context, domain.TenantID, domain.ProviderAccountID) (domain.Identifier, bool, error) {
	return "", false, nil
}
func (s *cleanupJobStore) ReleaseAccountLease(context.Context, domain.JobRef, domain.FencingToken) error {
	return nil
}
func (s *cleanupJobStore) MarkQueuePublished(context.Context, domain.JobRef) (domain.RenderJob, error) {
	return s.job, nil
}
func (s *cleanupJobStore) ListUnpublishedQueue(context.Context) ([]domain.RenderJob, error) {
	return nil, nil
}
func (s *cleanupJobStore) MarkAdmissionSettled(context.Context, domain.JobRef) (domain.RenderJob, error) {
	if s.failMarkAdmission != nil {
		return s.job, s.failMarkAdmission
	}
	s.job.AdmissionSettled = true
	return s.job, nil
}
func (s *cleanupJobStore) MarkPromptPurged(context.Context, domain.JobRef) (domain.RenderJob, error) {
	if s.failMarkPurge != nil {
		return s.job, s.failMarkPurge
	}
	s.job.PromptPurged = true
	return s.job, nil
}
