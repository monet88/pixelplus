package composition_test

import (
	"context"
	"path/filepath"
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

// TestFileRenderStoresReconcileReplayCrashWindow proves the exact crash between
// Jobs.Create and Replay.Complete: process A leaves a durable queued job plus an
// in-progress replay claim; fresh process B must terminalize that replay during
// startup before queue recovery, preserving the same job id without client-side
// re-creation.
func TestFileRenderStoresReconcileReplayCrashWindow(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	jobPath := filepath.Join(dir, "render-jobs.ledger")
	replayPath := filepath.Join(dir, "render-replay.ledger")
	base := time.Date(2026, 7, 26, 8, 0, 0, 0, time.UTC)
	digester, err := vaultpkg.NewHMACRenderDigester([]byte(vaultpkg.FixtureRenderDigestKey))
	if err != nil {
		t.Fatalf("digester: %v", err)
	}
	principal := domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a"}
	identity := domain.ReplayIdentity{
		Scope:       domain.ReplayScope{TenantID: "tenant_a", ClientAPIKeyID: "key_a", Key: "idem-crash-window"},
		Fingerprint: "fp_crash_window",
	}

	jobsA := persistence.NewFileRenderJobStore(jobPath)
	replayA := persistence.NewFileRenderReplayStore(replayPath)
	if err := jobsA.Restore(context.Background()); err != nil {
		t.Fatalf("A jobs Restore: %v", err)
	}
	if err := replayA.Restore(context.Background()); err != nil {
		t.Fatalf("A replay Restore: %v", err)
	}
	if decision, err := replayA.Claim(context.Background(), identity); err != nil || decision.Outcome != ports.ReplayClaimed {
		t.Fatalf("A replay Claim = %+v, %v; want ReplayClaimed", decision, err)
	}
	job := domain.NewQueuedRenderJob(
		"job_crash_window", "tenant_a", "key_a", domain.RenderOpImageGeneration, "m", "digest",
		nil, "", "pa_1", 1, identity.Fingerprint, identity.Scope.Key, domain.NewTimestamp(base),
	)
	if _, err := jobsA.Create(context.Background(), ports.RenderJobCreation{Principal: principal, Job: job}); err != nil {
		t.Fatalf("A Jobs.Create: %v", err)
	}
	// Crash here: deliberately no Replay.Complete.

	jobsB := persistence.NewFileRenderJobStore(jobPath)
	replayB := persistence.NewFileRenderReplayStore(replayPath)
	compB, err := composition.New(composition.Config{AllowInMemoryRenderJobs: true}, composition.Dependencies{
		Runtime: jobs.New(), Clock: clockNow{t: base}, IDs: &rwSeqIDs{},
		RenderJobs: jobsB, RenderReplay: replayB, RenderDigester: digester,
	})
	if err != nil {
		t.Fatalf("B New: %v", err)
	}
	if !compB.Ready() {
		t.Fatal("B Ready() = false after replay reconciliation")
	}
	decision, err := replayB.Claim(context.Background(), identity)
	if err != nil {
		t.Fatalf("B replay Claim: %v", err)
	}
	if decision.Outcome != ports.ReplayTerminal || decision.TerminalJob.JobID != job.JobID {
		t.Fatalf("B replay decision = %+v, want terminal job_crash_window", decision)
	}
	_ = compB.Close(context.Background())
}

// crashAfterPayloadAuthorized simulates abrupt process death immediately after
// the protected send boundary durably records PayloadSent=true. Panicking is
// intentional: returning an error would let ExecuteJob terminalize the attempt,
// which is not the crash window this restart test must preserve.
type crashAfterPayloadAuthorized struct {
	calls atomic.Int32
}

func (a *crashAfterPayloadAuthorized) Render(ctx context.Context, request ports.AuthorizedRenderRequest) (domain.RenderOutcome, error) {
	a.calls.Add(1)
	if request.SendBoundary == nil {
		panic("missing payload send boundary")
	}
	if err := request.SendBoundary.MarkPayloadSent(ctx); err != nil {
		panic(err)
	}
	panic("simulated process crash after payload send")
}

type restartAccountStore struct {
	account domain.ProviderAccount
}

func (s restartAccountStore) Create(context.Context, ports.AccountCreation) (domain.ProviderAccount, error) {
	return domain.ProviderAccount{}, ports.ErrDependencyUnavailable
}
func (s restartAccountStore) Visible(context.Context, domain.SecurityPrincipal, domain.ProviderAccountID) (domain.ProviderAccount, error) {
	return s.account, nil
}
func (s restartAccountStore) List(context.Context, domain.SecurityPrincipal) ([]domain.ProviderAccount, error) {
	return []domain.ProviderAccount{s.account}, nil
}
func (s restartAccountStore) Update(context.Context, ports.AccountUpdate) (domain.ProviderAccount, error) {
	return domain.ProviderAccount{}, ports.ErrDependencyUnavailable
}
func (s restartAccountStore) Restore(context.Context) error { return nil }

type restartCapabilityStore struct {
	now time.Time
}

func (s restartCapabilityStore) Get(context.Context, domain.SecurityPrincipal, domain.ProviderAccountID) (domain.CapabilitySnapshot, error) {
	return domain.NewLiveProbeSnapshot(
		"pa_1",
		domain.AuthModeChatGPTCodexOAuth,
		1,
		domain.NewTimestamp(s.now),
		map[domain.CapabilityOperation]domain.CapabilityFact{
			domain.CapabilityOpImageGeneration: {
				Status:        domain.CapabilityVerified,
				EvidenceClass: domain.EvidenceLiveProbe,
				ProbeSurface:  "/images/generations",
			},
		},
		nil,
		"/images/generations",
	), nil
}
func (restartCapabilityStore) List(context.Context, domain.SecurityPrincipal) ([]domain.CapabilitySnapshot, error) {
	return nil, nil
}
func (restartCapabilityStore) Put(context.Context, domain.SecurityPrincipal, domain.CapabilitySnapshot) error {
	return nil
}

type restartValidVault struct{}

func (restartValidVault) Put(context.Context, ports.CredentialIntake) error { return nil }
func (restartValidVault) Validate(context.Context, ports.CredentialValidation) (ports.CredentialValidationResult, error) {
	return ports.CredentialValidationResult{Valid: true}, nil
}
func (restartValidVault) Revoke(context.Context, ports.CredentialValidation) error { return nil }

func usableRestartAccount() domain.ProviderAccount {
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

// TestFileRenderStoresRecoverPostPayloadCrashWithoutDuplicateProviderWork proves
// #56 AC5 against a durable restart boundary. Process A crosses the protected
// payload-send boundary and dies while the durable job is still running. Fresh
// process B reclaims the expired lease in RecoveryOnly mode and fails the
// uncertain attempt closed without entering the Provider Adapter a second time.
func TestFileRenderStoresRecoverPostPayloadCrashWithoutDuplicateProviderWork(t *testing.T) {
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
	seed := domain.NewQueuedRenderJob(
		"job_restart_durable", "tenant_a", "key_a", domain.RenderOpImageGeneration, "m",
		"digest", nil, "", "pa_1", 1, fingerprint, idemKey, domain.NewTimestamp(base),
	)
	ref := seed.JobRef()
	identity := domain.ReplayIdentity{
		Scope:       domain.ReplayScope{TenantID: "tenant_a", ClientAPIKeyID: "key_a", Key: idemKey},
		Fingerprint: fingerprint,
	}

	// --- Process A: real worker crosses MarkPayloadSent, then dies abruptly. ---
	jobStoreA := persistence.NewFileRenderJobStore(jobLedgerPath)
	replayStoreA := persistence.NewFileRenderReplayStore(replayLedgerPath)
	crashAdapter := &crashAfterPayloadAuthorized{}
	compA, err := composition.New(composition.Config{AllowInMemoryRenderJobs: true}, composition.Dependencies{
		Runtime:          jobs.New(),
		Clock:            clockNow{t: base},
		IDs:              &rwSeqIDs{},
		RenderJobs:       jobStoreA,
		RenderReplay:     replayStoreA,
		RenderDigester:   digester,
		Accounts:         restartAccountStore{account: usableRestartAccount()},
		Capabilities:     restartCapabilityStore{now: base},
		Vault:            restartValidVault{},
		AuthorizedRender: crashAdapter,
	})
	if err != nil {
		t.Fatalf("A New: %v", err)
	}
	if !compA.Ready() {
		t.Fatal("A Ready() = false, want true")
	}
	if _, err := replayStoreA.Claim(context.Background(), identity); err != nil {
		t.Fatalf("A replay Claim: %v", err)
	}
	if _, err := jobStoreA.Create(context.Background(), ports.RenderJobCreation{Principal: principal, Job: seed}); err != nil {
		t.Fatalf("A Create: %v", err)
	}

	executeCtx, cancelExecute := context.WithCancel(context.Background())
	func() {
		defer func() {
			if recovered := recover(); recovered == nil {
				t.Fatal("A ExecuteJob did not simulate process crash")
			}
			cancelExecute()
		}()
		_ = compA.Worker().ExecuteJob(executeCtx, ref)
	}()
	if calls := crashAdapter.calls.Load(); calls != 1 {
		t.Fatalf("A Provider boundary calls = %d, want 1", calls)
	}

	crashed, err := jobStoreA.Load(context.Background(), ref)
	if err != nil {
		t.Fatalf("A Load after crash: %v", err)
	}
	if crashed.Lifecycle != domain.JobRunning || !crashed.LeaseHeld {
		t.Fatalf("A crash state = lifecycle %v lease %v, want running with held lease", crashed.Lifecycle, crashed.LeaseHeld)
	}
	if !crashed.Attempt.PayloadSent || crashed.Attempt.CommitStatus != domain.CommitNotStarted {
		t.Fatalf("A attempt after crash = %+v, want payload sent with unresolved commit", crashed.Attempt)
	}
	if crashed.Manifest.ID != "" {
		t.Fatalf("A manifest after crash = %q, want empty", crashed.Manifest.ID)
	}
	_ = compA.Close(context.Background())

	// --- Process B: fresh stores, expired lease, recovery-only execution. ---
	jobStoreB := persistence.NewFileRenderJobStore(jobLedgerPath)
	replayStoreB := persistence.NewFileRenderReplayStore(replayLedgerPath)
	authB := &countingAuthorized{}
	compB, err := composition.New(composition.Config{AllowInMemoryRenderJobs: true}, composition.Dependencies{
		Runtime:          jobs.New(),
		Clock:            clockNow{t: base.Add(10 * time.Minute)},
		IDs:              &rwSeqIDs{},
		RenderJobs:       jobStoreB,
		RenderReplay:     replayStoreB,
		RenderDigester:   digester,
		AuthorizedRender: authB,
	})
	if err != nil {
		t.Fatalf("B New: %v", err)
	}
	if !compB.Ready() {
		t.Fatal("B Ready() = false after restart recovery; want true")
	}
	if err := compB.Worker().ExecuteJob(context.Background(), ref); err != nil {
		t.Fatalf("B ExecuteJob recovery: %v", err)
	}
	if calls := authB.calls.Load(); calls != 0 {
		t.Fatalf("B Provider calls = %d, want 0 (RecoveryOnly must not re-render)", calls)
	}

	recovered, err := jobStoreB.Load(context.Background(), ref)
	if err != nil {
		t.Fatalf("B Load recovered job: %v", err)
	}
	if recovered.Lifecycle != domain.JobFailed || recovered.FailureStage != domain.StageRecovery {
		t.Fatalf("B recovered state = lifecycle %v stage %v, want failed/recovery", recovered.Lifecycle, recovered.FailureStage)
	}
	if recovered.CommitStatus != domain.CommitUnknown || recovered.LeaseHeld {
		t.Fatalf("B recovered commit/lease = %v/%v, want unknown/false", recovered.CommitStatus, recovered.LeaseHeld)
	}

	decision, err := replayStoreB.Claim(context.Background(), identity)
	if err != nil {
		t.Fatalf("B replay Claim: %v", err)
	}
	if decision.Outcome != ports.ReplayTerminal || decision.TerminalJob.JobID != seed.JobID {
		t.Fatalf("B replay decision = %+v, want terminal owner job_restart_durable", decision)
	}
	_ = compB.Close(context.Background())
}
