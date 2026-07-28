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
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
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
	// AllowInMemoryRenderJobs permits the process-local MemoryRenderJobStore
	// when RenderJobs is nil. Production must leave this false so a missing
	// durable job store fails closed (UnavailableRenderJobStore). Contract
	// fixtures set true for controlled in-process proofs only.
	AllowInMemoryRenderJobs bool
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
	RenderStaging    ports.RenderStagingStore
	AuthorizedRender ports.AuthorizedRender
	RenderAudit      ports.RenderAuditRecorder
	// RenderCredentialAuthorizer mints callback-scoped credential material for
	// Adapter entry (ADR 0009). Production missing inject stays not-ready;
	// AllowInMemory fixtures install a permissive fixture authorizer.
	RenderCredentialAuthorizer ports.RenderCredentialAuthorizer
	// RenderDigester produces keyed create fingerprints. Production without an
	// injected digester (or fixture AllowInMemory key) fails readiness closed.
	RenderDigester ports.RenderDigester
	// RenderDigestKey is optional raw key material for composing an HMAC digester
	// when RenderDigester is nil. Never logged. Empty in production without inject
	// keeps readiness closed (no restart-unstable auto key).
	RenderDigestKey []byte
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
	// Resolve digester before readiness: only a usable digester (probe mint
	// succeeds) counts as ready. Injected FailClosed or short keys keep Ready
	// closed even when the digester pointer is non-nil (#54 pre-review follow-up).
	digester, digesterUsable := resolveRenderDigester(config, dependencies)
	dependencies.RenderDigester = digester

	// Materialize controlled in-memory render ports before Restore so recovery
	// runs on the instances that will serve product work (Standards P1-C).
	if config.AllowInMemoryRenderJobs {
		ensureInMemoryRenderPorts(&dependencies)
	}

	// Recovery-before-ready (P1-C): restore job/replay/prompt/staging when present.
	// Missing production injects keep readiness closed via durability gate below;
	// explicit Unavailable injects fail Restore and also keep readiness closed.
	renderRestoreErr := restoreRenderPorts(startupContext, dependencies)
	if renderRestoreErr != nil {
		logger.Error("render durability startup recovery failed; readiness stays closed", "error", renderRestoreErr)
	}

	// ADR 0009 / P1-A/C: production without durable render ports + credential
	// authorizer + usable digester must not advertise execution readiness.
	// Fixtures use AllowInMemoryRenderJobs (auto memory + fixture authorizer).
	renderDurabilityReady := digesterUsable && renderRestoreErr == nil && (config.AllowInMemoryRenderJobs ||
		(dependencies.RenderJobs != nil &&
			dependencies.RenderReplay != nil &&
			dependencies.RenderPrompts != nil &&
			dependencies.RenderStaging != nil &&
			dependencies.RenderCredentialAuthorizer != nil))
	if !renderDurabilityReady {
		logger.Error("render job durability, authorizer, or usable digester not configured; readiness stays closed")
	}

	service, err := newProviderAccountService(dependencies)
	if err != nil {
		return nil, err
	}

	// Share one Asset metadata/content pair across AssetService and RenderService
	// so upload→edit/inpaint sees the same same-Tenant Assets (ADR 0009 single
	// composition root). Creating independent Memory stores per service would
	// make Visible/Fetch miss committed uploads.
	if dependencies.AssetMetadata == nil {
		dependencies.AssetMetadata = persistence.NewMemoryAssetMetadataStore(dependencies.Clock)
	}
	if dependencies.AssetContent == nil {
		dependencies.AssetContent = persistence.NewMemoryAssetContentStore()
	}

	assetService, err := newAssetService(dependencies)
	if err != nil {
		return nil, err
	}
	renderService, err := newRenderService(config, dependencies)
	if err != nil {
		return nil, err
	}
	// Wire the real JobExecutor when the render spine is available.
	runtime.worker = renderService
	runtime.handler = httptransport.NewHandler(dependencies.Clock, dependencies.IDs, runtime, service, assetService, service, service, renderService)

	// Pre-ready queue publication recovery (P1-C / Spec P1-3): re-arm every
	// nonterminal SafeJobReference into the process-local runtime (including
	// jobs already marked QueuePublished). Failure keeps Ready closed.
	queueRecoverErr := runtime.recoverQueuePublications(startupContext)
	if queueRecoverErr != nil {
		logger.Error("queue publication recovery failed; readiness stays closed", "error", queueRecoverErr)
	}

	runtime.ready.Store(accountRestoreErr == nil && healthRestoreErr == nil && routingRestoreErr == nil &&
		jobRestoreErr == nil && renderRestoreErr == nil && queueRecoverErr == nil && renderDurabilityReady)

	return runtime, nil
}

// ensureInMemoryRenderPorts installs process-local controlled render foundations
// for fixture AllowInMemory mode before Restore/Ready evaluation.
func ensureInMemoryRenderPorts(dependencies *Dependencies) {
	if dependencies == nil {
		return
	}
	if dependencies.RenderJobs == nil {
		dependencies.RenderJobs = persistence.NewMemoryRenderJobStore()
	}
	if dependencies.RenderReplay == nil {
		dependencies.RenderReplay = persistence.NewMemoryRenderReplayStore()
	}
	if dependencies.RenderPrompts == nil {
		dependencies.RenderPrompts = vaultpkg.NewMemoryRenderPromptStore()
	}
	if dependencies.RenderStaging == nil {
		dependencies.RenderStaging = persistence.NewMemoryRenderStagingStore()
	}
	if dependencies.RenderCredentialAuthorizer == nil {
		dependencies.RenderCredentialAuthorizer = vaultpkg.NewPermissiveFixtureRenderCredentialAuthorizer()
	}
}

// restoreRenderPorts runs Restorer.Restore on present required render durability
// ports. Any non-nil candidate that does not implement ports.Restorer is a
// fail-closed dependency error — never silently skip (Spec P1-4).
func restoreRenderPorts(ctx context.Context, dependencies Dependencies) error {
	var first error
	for _, candidate := range []any{
		dependencies.RenderJobs,
		dependencies.RenderReplay,
		dependencies.RenderPrompts,
		dependencies.RenderStaging,
	} {
		if candidate == nil {
			continue
		}
		restorer, ok := candidate.(ports.Restorer)
		if !ok {
			if first == nil {
				first = ports.ErrDependencyUnavailable
			}
			continue
		}
		if err := restorer.Restore(ctx); err != nil && first == nil {
			first = err
		}
	}
	return first
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
// back to fail-closed foundations so production composition is safe by default.
func newRenderService(config Config, dependencies Dependencies) (*application.RenderService, error) {
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
		// Production default: fail closed. Process-local memory is not restart-
		// durable and must not silently stand in for a foundation ledger.
		if config.AllowInMemoryRenderJobs {
			jobs = persistence.NewMemoryRenderJobStore()
		} else {
			jobs = persistence.NewUnavailableRenderJobStore()
		}
	}
	staging := dependencies.RenderStaging
	if staging == nil {
		if config.AllowInMemoryRenderJobs {
			staging = persistence.NewMemoryRenderStagingStore()
		} else {
			staging = persistence.NewUnavailableRenderStagingStore()
		}
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
	// Confidential prompt store: production fail-closed unless controlled memory
	// is explicitly allowed (fixtures) or injected. Never auto-create Memory when
	// AllowInMemoryRenderJobs is false.
	prompts := dependencies.RenderPrompts
	if prompts == nil {
		if config.AllowInMemoryRenderJobs {
			prompts = vaultpkg.NewMemoryRenderPromptStore()
		} else {
			prompts = vaultpkg.NewFailClosedRenderPromptStore()
		}
	}
	audit := dependencies.RenderAudit
	if audit == nil {
		audit = observability.NewSlogRenderAuditRecorder(dependencies.Logger)
	}
	authorized := dependencies.AuthorizedRender
	if authorized == nil {
		adapter := dependencies.RenderAdapter
		if adapter == nil {
			adapter = vaultpkg.NewFailClosedRenderAdapter()
		}
		// P1-A: credential capability is vault-owned; production without inject
		// uses fail-closed authorizer (Adapter never entered). Fixtures use the
		// permissive fixture authorizer installed by ensureInMemoryRenderPorts.
		authorizer := dependencies.RenderCredentialAuthorizer
		if authorizer == nil {
			if config.AllowInMemoryRenderJobs {
				authorizer = vaultpkg.NewPermissiveFixtureRenderCredentialAuthorizer()
			} else {
				authorizer = vaultpkg.NewFailClosedRenderCredentialAuthorizer()
			}
		}
		if prompts != nil && adapter != nil && staging != nil {
			// content is required so edit/inpaint can inject same-Tenant Asset
			// bytes inside the authorized boundary without application seeing them.
			authorized = vaultpkg.NewAuthorizedRenderService(prompts, authorizer, adapter, staging, content, audit)
		} else {
			authorized = vaultpkg.NewFailClosedAuthorizedRender()
		}
	}
	// Digester was resolved in New for readiness; rebuild if missing (defensive).
	digester := dependencies.RenderDigester
	if digester == nil {
		digester, _ = resolveRenderDigester(config, dependencies)
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
		Staging:      staging,
		Vault:        vault,
		Prompts:      prompts,
		Authorized:   authorized,
		Digester:     digester,
		Queue:        dependencies.Runtime,
		Audit:        audit,
		Telemetry:    telemetry,
		RequestLog:   requestLog,
		Clock:        dependencies.Clock,
		IDs:          dependencies.IDs,
	})
}

// resolveRenderDigester builds the digester used for product create fingerprints
// and reports whether it can actually mint digests. Short keys and FailClosed
// implementations are not usable for readiness.
func resolveRenderDigester(config Config, dependencies Dependencies) (ports.RenderDigester, bool) {
	if dependencies.RenderDigester != nil {
		return dependencies.RenderDigester, renderDigesterUsable(dependencies.RenderDigester)
	}
	key := dependencies.RenderDigestKey
	if len(key) == 0 && config.AllowInMemoryRenderJobs {
		// Fixture-only deterministic key; never used as production default.
		key = []byte(vaultpkg.FixtureRenderDigestKey)
	}
	if len(key) >= vaultpkg.MinRenderDigestKeyBytes {
		if d, err := vaultpkg.NewHMACRenderDigester(key); err == nil {
			return d, true
		}
	}
	// Empty/weak/missing key: process may start, product digests fail closed.
	return vaultpkg.FailClosedRenderDigester{}, false
}

// renderDigesterUsable probes whether the digester can mint a durable fingerprint.
func renderDigesterUsable(d ports.RenderDigester) bool {
	if d == nil {
		return false
	}
	_, err := d.CreateFingerprint(domain.RenderOpImageGeneration, "ready-probe", "ready-probe", nil, "")
	return err == nil
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

// RecoverQueuePublications re-arms SafeJobReference delivery for all durable
// nonterminal jobs without a client retry. Public entry requires Ready.
func (runtime *Runtime) RecoverQueuePublications(ctx context.Context) error {
	if runtime == nil || !runtime.Ready() {
		return ErrNotReady
	}
	return runtime.recoverQueuePublications(ctx)
}

// recoverQueuePublications is the internal recovery path used before Ready and
// from RunWorkers. It must not check Ready (avoids circular startup guard, P1-C).
func (runtime *Runtime) recoverQueuePublications(ctx context.Context) error {
	if runtime == nil {
		return ErrNotReady
	}
	if recoverer, ok := runtime.worker.(interface {
		RecoverQueuePublications(context.Context) error
	}); ok {
		return recoverer.RecoverQueuePublications(ctx)
	}
	return nil
}

// RunWorkers connects safe queue references to the exported JobExecutor.
func (runtime *Runtime) RunWorkers(ctx context.Context) error {
	if !runtime.Ready() {
		return ErrNotReady
	}

	// Autonomous publication recovery before consuming the queue (#14 §3.3).
	// Required recovery failures fail closed — never warn-and-continue (P1-C).
	if err := runtime.recoverQueuePublications(ctx); err != nil {
		return err
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

	// Handler contract (ADR 0009 / #14):
	// - invalid SafeJobReference → discard (nil) so poison messages do not stall
	// - ExecuteJob nil (benign non-claim / terminal / concurrent lose) → accept
	// - durable ExecuteJob errors → propagate so JobRuntime can redeliver and
	//   RunWorkers surfaces the failure instead of silently dropping the ref
	err := runtime.jobs.Run(workerContext, func(ctx context.Context, reference ports.SafeJobReference) error {
		job, err := reference.JobRef()
		if err != nil {
			runtime.logger.Warn("discarding invalid queue reference", "error", err)
			return nil
		}
		if err := runtime.worker.ExecuteJob(ctx, job); err != nil {
			runtime.logger.Warn("job execution failed; propagating for redelivery",
				"tenant_id", string(job.TenantID),
				"job_id", string(job.JobID),
				"error", err)
			return err
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
