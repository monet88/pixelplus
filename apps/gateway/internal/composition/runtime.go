// Package composition is the only Gateway package allowed to wire dependencies.
package composition

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/application"
	"github.com/monet88/pixelplus/apps/gateway/internal/infrastructure/observability"
	"github.com/monet88/pixelplus/apps/gateway/internal/infrastructure/persistence"
	vaultpkg "github.com/monet88/pixelplus/apps/gateway/internal/infrastructure/vault"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
	httptransport "github.com/monet88/pixelplus/apps/gateway/internal/transport/http"
)

const defaultStartupTimeout = 5 * time.Second

// ErrNotReady blocks workers when required startup recovery did not complete.
var ErrNotReady = errors.New("gateway runtime is not ready")

// Config contains parsed, non-secret composition settings.
type Config struct {
	StartupTimeout time.Duration
	// ProviderAccountStorePath is the durable file path for Provider Account
	// state. Either this or an explicit Accounts port is required so production
	// never silently loses cooldowns and recovery permits across restarts.
	ProviderAccountStorePath string
}

// Dependencies contains the controlled foundation ports owned by this slice.
// Later vertical slices add their application-owned ports here.
type Dependencies struct {
	Runtime ports.JobRuntime
	Clock   ports.Clock
	IDs     ports.IDGenerator
	Logger  *slog.Logger

	// Provider Account request-spine ports (#45). When a port is nil, New
	// substitutes the production foundation implementation so the real
	// production composition constructor is safe and fail-closed by default.
	// Contract tests inject controlled fakes through these same fields to
	// exercise the protected spine through real composition.
	Principal ports.PrincipalStore
	Admission ports.AdmissionStore
	Replay    ports.ReplayStore
	Accounts  ports.AccountStore
	// Health owns scoped conditions and recovery permits independently of
	// AccountStore lifecycle metadata (ADR 0009 HealthStore catalogue).
	Health     ports.HealthStore
	Audit      ports.AuditRecorder
	Telemetry  ports.TelemetryRecorder
	RequestLog ports.RequestLogRecorder

	// Provider Credential Vault and Probe Adapter ports (#46). A nil port keeps
	// the fail-closed foundation implementation composition substitutes by
	// default so no account can activate in production until a real Vault and
	// Provider probe adapter land; contract tests inject controlled fakes.
	Vault ports.CredentialVault
	Probe ports.ProbeAdapter
	// OAuth exchange adapter port (#47). A nil port keeps the fail-closed
	// foundation implementation so production cannot start a real OAuth journey
	// until a Provider OAuth surface lands; contract tests inject controlled fakes.
	OAuth ports.OAuthExchangeAdapter

	// Capability Snapshot ports (#50). A nil port keeps the fail-closed
	// foundation implementation composition substitutes by default so production
	// never invents model evidence; contract tests inject controlled fakes.
	Capabilities ports.CapabilityStore
	Capability   ports.CapabilityAdapter

	// Provider Surface Circuit gate (#51). A nil port keeps the foundation
	// closed-circuit store composition substitutes by default: with no
	// correlation evaluator wired there is no corroborated evidence, so every
	// surface reports closed (nothing to block). A wired-but-failing store fails
	// closed instead (health/cooldown spec §12). Contract tests inject a
	// controlled fake to prove an open surface blocks matching new work.
	Circuits ports.CircuitStore

	// Routing Policy store (#52). A nil port keeps the in-process foundation
	// Memory store (or a file companion when ProviderAccountStorePath is set)
	// so production composition is safe without inventing cross-Tenant policy.
	// Contract tests inject a controlled fake with mutation counters.
	Routing ports.RoutingPolicyStore

	// Asset exchange request-spine ports (#53). When a port is nil, New
	// substitutes the production foundation implementation so the real
	// production composition constructor is safe and fail-closed by default.
	// Contract tests inject controlled fakes through these same fields to
	// exercise the protected Asset spine through real composition.
	AssetReplay   ports.AssetReplayStore
	AssetMetadata ports.AssetMetadataStore
	AssetContent  ports.AssetContentStore
	AssetAudit    ports.AssetAuditRecorder

	// Render Job ports (#54). When a port is nil, New substitutes foundation
	// implementations. Contract tests inject controlled fakes. RenderAdapter is
	// the low-level controlled surface wrapped by AuthorizedRender so application
	// never receives a port that accepts confidential material.
	RenderJobs       ports.RenderJobStore
	RenderReplay     ports.RenderReplayStore
	RenderAdapter    ports.RenderAdapter
	RenderPrompts    ports.RenderPromptStore
	AuthorizedRender ports.AuthorizedRender
	RenderAudit      ports.RenderAuditRecorder
}

// Runtime is the single composition result shared by production and fixtures.
type Runtime struct {
	handler http.Handler
	worker  application.JobExecutor
	jobs    ports.JobRuntime
	logger  *slog.Logger

	healthy atomic.Bool
	ready   atomic.Bool
	done    chan struct{}

	shutdownOnce sync.Once
	closeMu      sync.Mutex
	closed       bool
}

// New validates dependencies, restores required state, and builds one runtime.
// Recovery failures keep the operational HTTP surface live while readiness and
// worker execution fail closed.
func New(config Config, dependencies Dependencies) (*Runtime, error) {
	if dependencies.Runtime == nil {
		return nil, errors.New("composition: job runtime is required")
	}
	if dependencies.Clock == nil {
		return nil, errors.New("composition: clock is required")
	}
	if dependencies.IDs == nil {
		return nil, errors.New("composition: ID generator is required")
	}
	startupTimeout := config.StartupTimeout
	if startupTimeout == 0 {
		startupTimeout = defaultStartupTimeout
	}
	if startupTimeout < 0 {
		return nil, errors.New("composition: startup timeout must be positive")
	}

	logger := dependencies.Logger
	if logger == nil {
		logger = slog.Default()
	}

	accounts := dependencies.Accounts
	if accounts == nil {
		if config.ProviderAccountStorePath != "" {
			accounts = persistence.NewFileAccountStore(config.ProviderAccountStorePath)
		} else {
			// Tests and fixtures rely on the in-process foundation store. Production
			// entry points must supply a durable ProviderAccountStorePath.
			accounts = persistence.NewMemoryAccountStore()
		}
	}
	dependencies.Accounts = accounts

	health := dependencies.Health
	if health == nil {
		if config.ProviderAccountStorePath != "" {
			// Companion ledger beside the account path so health durability is
			// independent of AccountStore while sharing the same volume root.
			health = persistence.NewFileHealthStore(config.ProviderAccountStorePath + ".health.ledger")
		} else {
			health = persistence.NewMemoryHealthStore()
		}
	}
	dependencies.Health = health

	routing := dependencies.Routing
	if routing == nil {
		if config.ProviderAccountStorePath != "" {
			routing = persistence.NewFileRoutingPolicyStore(config.ProviderAccountStorePath + ".routing-policy.ledger")
		} else {
			routing = persistence.NewMemoryRoutingPolicyStore()
		}
	}
	dependencies.Routing = routing

	runtime := &Runtime{
		worker: application.NewFoundationJobExecutor(),
		jobs:   dependencies.Runtime,
		logger: logger,
		done:   make(chan struct{}),
	}
	runtime.healthy.Store(true)

	startupContext, cancelStartup := context.WithTimeout(context.Background(), startupTimeout)
	defer cancelStartup()

	accountRestoreErr := accounts.Restore(startupContext)
	if accountRestoreErr != nil {
		logger.Error("provider account startup recovery failed; readiness stays closed", "error", accountRestoreErr)
		// Do not expose a partially restored or empty account view to direct product
		// traffic. The fail-closed substitute makes every account-backed path return
		// dependency_unavailable until a new composition successfully restores state.
		dependencies.Accounts = persistence.NewUnavailableAccountStore()
	}
	healthRestoreErr := health.Restore(startupContext)
	if healthRestoreErr != nil {
		logger.Error("provider health startup recovery failed; readiness stays closed", "error", healthRestoreErr)
		dependencies.Health = persistence.NewUnavailableHealthStore()
	}
	routingRestoreErr := error(nil)
	if restorer, ok := routing.(interface{ Restore(context.Context) error }); ok {
		routingRestoreErr = restorer.Restore(startupContext)
		if routingRestoreErr != nil {
			logger.Error("routing policy startup recovery failed; readiness stays closed", "error", routingRestoreErr)
			// Fail closed: do not substitute an empty Memory store that would look
			// like "no policy" and accept writes against lost durability.
			dependencies.Routing = persistence.NewUnavailableRoutingPolicyStore()
		}
	}
	jobRestoreErr := runtime.jobs.Restore(startupContext)
	if jobRestoreErr != nil {
		logger.Error("gateway startup recovery failed; readiness stays closed", "error", jobRestoreErr)
	}
	runtime.ready.Store(accountRestoreErr == nil && healthRestoreErr == nil && routingRestoreErr == nil && jobRestoreErr == nil)

	service, err := newProviderAccountService(dependencies)
	if err != nil {
		return nil, err
	}

	assetService, err := newAssetService(dependencies)
	if err != nil {
		return nil, err
	}
	renderService, err := newRenderService(dependencies)
	if err != nil {
		return nil, err
	}
	// Wire the real JobExecutor when the render spine is available.
	runtime.worker = renderService
	runtime.handler = httptransport.NewHandler(dependencies.Clock, dependencies.IDs, runtime, service, assetService, service, service, renderService)

	return runtime, nil
}

// newProviderAccountService wires the Provider Account request-spine service.
// Each nil port falls back to the fail-closed/foundation production
// implementation so the real production composition constructor is safe by
// default; contract tests override any subset through Dependencies.
func newProviderAccountService(dependencies Dependencies) (*application.ProviderAccountService, error) {
	principal := dependencies.Principal
	if principal == nil {
		principal = persistence.NewFailClosedPrincipalStore()
	}
	admission := dependencies.Admission
	if admission == nil {
		admission = persistence.NewAlwaysAdmitStore()
	}
	replay := dependencies.Replay
	if replay == nil {
		replay = persistence.NewMemoryReplayStore()
	}
	accounts := dependencies.Accounts
	if accounts == nil {
		accounts = persistence.NewMemoryAccountStore()
	}
	health := dependencies.Health
	if health == nil {
		health = persistence.NewMemoryHealthStore()
	}
	audit := dependencies.Audit
	if audit == nil {
		audit = observability.NewSlogAuditRecorder(dependencies.Logger)
	}
	telemetry := dependencies.Telemetry
	if telemetry == nil {
		telemetry = observability.NewSlogTelemetryRecorder(dependencies.Logger)
	}
	requestLog := dependencies.RequestLog
	if requestLog == nil {
		requestLog = observability.NewSlogRequestLogRecorder(dependencies.Logger)
	}

	vault := dependencies.Vault
	if vault == nil {
		vault = vaultpkg.NewFailClosedCredentialVault()
	}
	probe := dependencies.Probe
	if probe == nil {
		probe = vaultpkg.NewFailClosedProbeAdapter()
	}
	oauth := dependencies.OAuth
	if oauth == nil {
		oauth = vaultpkg.NewFailClosedOAuthExchangeAdapter()
	}
	capabilities := dependencies.Capabilities
	if capabilities == nil {
		capabilities = vaultpkg.NewFailClosedCapabilityStore()
	}
	capability := dependencies.Capability
	if capability == nil {
		capability = vaultpkg.NewFailClosedCapabilityAdapter()
	}
	circuits := dependencies.Circuits
	if circuits == nil {
		circuits = persistence.NewClosedCircuitStore()
	}
	routing := dependencies.Routing
	if routing == nil {
		routing = persistence.NewMemoryRoutingPolicyStore()
	}

	return application.NewProviderAccountService(application.ProviderAccountDependencies{
		Principal:    principal,
		Admission:    admission,
		Replay:       replay,
		Accounts:     accounts,
		Health:       health,
		Vault:        vault,
		Probe:        probe,
		OAuth:        oauth,
		Capabilities: capabilities,
		Capability:   capability,
		Circuits:     circuits,
		Routing:      routing,
		Audit:        audit,
		Telemetry:    telemetry,
		RequestLog:   requestLog,
		Clock:        dependencies.Clock,
		IDs:          dependencies.IDs,
	})
}

// newAssetService wires the immutable Asset exchange request-spine service.
// It reuses the same Principal, Admission, Telemetry, and RequestLog
// foundations as the Provider Account slice and adds the Asset-owned replay,
// metadata, content, and audit foundations. Each nil port falls back to the
// fail-closed/foundation production implementation so the real production
// composition constructor is safe by default; contract tests override any
// subset through Dependencies.
func newAssetService(dependencies Dependencies) (*application.AssetService, error) {
	principal := dependencies.Principal
	if principal == nil {
		principal = persistence.NewFailClosedPrincipalStore()
	}
	admission := dependencies.Admission
	if admission == nil {
		admission = persistence.NewAlwaysAdmitStore()
	}
	replay := dependencies.AssetReplay
	if replay == nil {
		replay = persistence.NewMemoryAssetReplayStore()
	}
	metadata := dependencies.AssetMetadata
	if metadata == nil {
		metadata = persistence.NewMemoryAssetMetadataStore(dependencies.Clock)
	}
	content := dependencies.AssetContent
	if content == nil {
		content = persistence.NewMemoryAssetContentStore()
	}
	audit := dependencies.AssetAudit
	if audit == nil {
		audit = observability.NewSlogAssetAuditRecorder(dependencies.Logger)
	}
	telemetry := dependencies.Telemetry
	if telemetry == nil {
		telemetry = observability.NewSlogTelemetryRecorder(dependencies.Logger)
	}
	requestLog := dependencies.RequestLog
	if requestLog == nil {
		requestLog = observability.NewSlogRequestLogRecorder(dependencies.Logger)
	}

	return application.NewAssetService(application.AssetDependencies{
		Principal:  principal,
		Admission:  admission,
		Replay:     replay,
		Metadata:   metadata,
		Content:    content,
		Audit:      audit,
		Telemetry:  telemetry,
		RequestLog: requestLog,
		Clock:      dependencies.Clock,
		IDs:        dependencies.IDs,
	})
}

// newRenderService wires the Render Job spine and JobExecutor. Nil ports fall
// back to foundation implementations so production composition is fail-closed
// by default (no Provider render surface until injected).
func newRenderService(dependencies Dependencies) (*application.RenderService, error) {
	principal := dependencies.Principal
	if principal == nil {
		principal = persistence.NewFailClosedPrincipalStore()
	}
	admission := dependencies.Admission
	if admission == nil {
		admission = persistence.NewAlwaysAdmitStore()
	}
	replay := dependencies.RenderReplay
	if replay == nil {
		replay = persistence.NewMemoryRenderReplayStore()
	}
	jobs := dependencies.RenderJobs
	if jobs == nil {
		jobs = persistence.NewMemoryRenderJobStore()
	}
	accounts := dependencies.Accounts
	if accounts == nil {
		accounts = persistence.NewMemoryAccountStore()
	}
	health := dependencies.Health
	if health == nil {
		health = persistence.NewMemoryHealthStore()
	}
	capabilities := dependencies.Capabilities
	if capabilities == nil {
		capabilities = vaultpkg.NewFailClosedCapabilityStore()
	}
	circuits := dependencies.Circuits
	if circuits == nil {
		circuits = persistence.NewClosedCircuitStore()
	}
	routing := dependencies.Routing
	if routing == nil {
		routing = persistence.NewMemoryRoutingPolicyStore()
	}
	metadata := dependencies.AssetMetadata
	if metadata == nil {
		metadata = persistence.NewMemoryAssetMetadataStore(dependencies.Clock)
	}
	content := dependencies.AssetContent
	if content == nil {
		content = persistence.NewMemoryAssetContentStore()
	}
	vault := dependencies.Vault
	if vault == nil {
		vault = vaultpkg.NewFailClosedCredentialVault()
	}
	// Confidential prompt store + authorized render share one memory boundary so
	// create-time Put and worker-time resolution agree. Explicit overrides are
	// honored when both are supplied (advanced fixtures).
	prompts := dependencies.RenderPrompts
	authorized := dependencies.AuthorizedRender
	if prompts == nil || authorized == nil {
		adapter := dependencies.RenderAdapter
		if adapter == nil {
			adapter = vaultpkg.NewFailClosedRenderAdapter()
		}
		mem := vaultpkg.NewMemoryRenderPromptStore()
		auth := vaultpkg.NewAuthorizedRenderService(mem, vault, adapter)
		if prompts == nil {
			prompts = auth.PromptStore()
		}
		if authorized == nil {
			authorized = auth
		}
	}
	audit := dependencies.RenderAudit
	if audit == nil {
		audit = observability.NewSlogRenderAuditRecorder(dependencies.Logger)
	}
	telemetry := dependencies.Telemetry
	if telemetry == nil {
		telemetry = observability.NewSlogTelemetryRecorder(dependencies.Logger)
	}
	requestLog := dependencies.RequestLog
	if requestLog == nil {
		requestLog = observability.NewSlogRequestLogRecorder(dependencies.Logger)
	}

	return application.NewRenderService(application.RenderDependencies{
		Principal:    principal,
		Admission:    admission,
		Replay:       replay,
		Jobs:         jobs,
		Accounts:     accounts,
		Health:       health,
		Capabilities: capabilities,
		Circuits:     circuits,
		Routing:      routing,
		Assets:       metadata,
		Content:      content,
		Vault:        vault,
		Prompts:      prompts,
		Authorized:   authorized,
		Queue:        dependencies.Runtime,
		Audit:        audit,
		Telemetry:    telemetry,
		RequestLog:   requestLog,
		Clock:        dependencies.Clock,
		IDs:          dependencies.IDs,
	})
}

// Handler returns the real composed HTTP surface.
func (runtime *Runtime) Handler() http.Handler {
	return runtime.handler
}

// Worker returns the exported application worker seam.
func (runtime *Runtime) Worker() application.JobExecutor {
	return runtime.worker
}

// Healthy reports process lifecycle only and never authorizes product work.
func (runtime *Runtime) Healthy() bool {
	return runtime.healthy.Load()
}

// Ready reports whether required recovery succeeded and shutdown has not begun.
func (runtime *Runtime) Ready() bool {
	return runtime.ready.Load() && runtime.healthy.Load()
}

// RunWorkers connects safe queue references to the exported JobExecutor.
func (runtime *Runtime) RunWorkers(ctx context.Context) error {
	if !runtime.Ready() {
		return ErrNotReady
	}

	workerContext, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()

	bridgeDone := make(chan struct{})
	go func() {
		defer close(bridgeDone)
		select {
		case <-runtime.done:
			cancelWorkers()
		case <-workerContext.Done():
		}
	}()

	err := runtime.jobs.Run(workerContext, func(ctx context.Context, reference ports.SafeJobReference) error {
		job, err := reference.JobRef()
		if err != nil {
			runtime.logger.Warn("discarding invalid queue reference", "error", err)
			return nil
		}
		if err := runtime.worker.ExecuteJob(ctx, job); err != nil {
			runtime.logger.Warn("discarding failed job",
				"tenant_id", string(job.TenantID),
				"job_id", string(job.JobID),
				"error", err)
		}
		return nil
	})
	cancelWorkers()
	<-bridgeDone
	return err
}

// Close cancels lifecycle work, then closes owned resources in reverse order.
func (runtime *Runtime) Close(ctx context.Context) error {
	runtime.shutdownOnce.Do(func() {
		runtime.ready.Store(false)
		runtime.healthy.Store(false)
		close(runtime.done)
	})

	runtime.closeMu.Lock()
	defer runtime.closeMu.Unlock()
	if runtime.closed {
		return nil
	}
	if err := runtime.jobs.Close(ctx); err != nil {
		return fmt.Errorf("close job runtime: %w", err)
	}
	runtime.closed = true
	return nil
}

var _ httptransport.Status = (*Runtime)(nil)
