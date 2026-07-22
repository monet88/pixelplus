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
	Principal  ports.PrincipalStore
	Admission  ports.AdmissionStore
	Replay     ports.ReplayStore
	Accounts   ports.AccountStore
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

	// Asset exchange request-spine ports (#53). When a port is nil, New
	// substitutes the production foundation implementation so the real
	// production composition constructor is safe and fail-closed by default.
	// Contract tests inject controlled fakes through these same fields to
	// exercise the protected Asset spine through real composition.
	AssetReplay   ports.AssetReplayStore
	AssetMetadata ports.AssetMetadataStore
	AssetContent  ports.AssetContentStore
	AssetAudit    ports.AssetAuditRecorder
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

	runtime := &Runtime{
		worker: application.NewFoundationJobExecutor(),
		jobs:   dependencies.Runtime,
		logger: logger,
		done:   make(chan struct{}),
	}
	runtime.healthy.Store(true)

	startupContext, cancelStartup := context.WithTimeout(context.Background(), startupTimeout)
	defer cancelStartup()

	if err := runtime.jobs.Restore(startupContext); err != nil {
		logger.Error("gateway startup recovery failed; readiness stays closed", "error", err)
		runtime.ready.Store(false)
	} else {
		runtime.ready.Store(true)
	}

	service, err := newProviderAccountService(dependencies)
	if err != nil {
		return nil, err
	}

	assetService, err := newAssetService(dependencies)
	if err != nil {
		return nil, err
	}
	runtime.handler = httptransport.NewHandler(dependencies.Clock, dependencies.IDs, runtime, service, assetService)

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

	return application.NewProviderAccountService(application.ProviderAccountDependencies{
		Principal:  principal,
		Admission:  admission,
		Replay:     replay,
		Accounts:   accounts,
		Vault:      vault,
		Probe:      probe,
		OAuth:      oauth,
		Audit:      audit,
		Telemetry:  telemetry,
		RequestLog: requestLog,
		Clock:      dependencies.Clock,
		IDs:        dependencies.IDs,
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
