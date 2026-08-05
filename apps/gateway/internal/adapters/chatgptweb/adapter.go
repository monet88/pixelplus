package chatgptweb

import (
	"context"
	"fmt"
	"net/http"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// dependencyUnavailable wraps a transport-layer failure so it still satisfies
// errors.Is(err, ports.ErrDependencyUnavailable) — which the probe spine relies
// on to fail admission closed — while preserving the underlying cause.
//
// Cause and effect: without the wrap, a nil-transport composition and a genuine
// upstream timeout reach the spine as the same bare sentinel, so an operator who
// enabled the lab profile but forgot to supply transport sees only "dependency
// unavailable" with nothing to distinguish it from a Provider outage.
//
// The stage label is a fixed literal chosen by this package, never a Provider
// body, header, or credential value (OP-G3), and the wrapped cause is always an
// Adapter-internal or transport error for the same reason.
func dependencyUnavailable(stage string, cause error) error {
	return fmt.Errorf("chatgpt web %s: %w: %w", stage, ports.ErrDependencyUnavailable, cause)
}

// Adapter is the ChatGPT Web Access protocol Adapter. It is stateless: the only
// field is the transport seam, so nothing observed on one request can influence
// another. That is what makes "owns no durable state" checkable rather than
// merely asserted.
type Adapter struct {
	transport Transport
}

// New builds the Adapter over a transport. A nil transport is legal and makes
// every method fail closed with ErrTransportUnavailable, which is the intended
// production posture: registering the Adapter is not the same as giving it
// egress.
func New(transport Transport) *Adapter {
	return &Adapter{transport: transport}
}

// AuthMode reports the single Auth Mode this Adapter translates. It never
// handles another mode, so a registry misconfiguration is detectable rather
// than silently serving the wrong protocol.
func (adapter *Adapter) AuthMode() domain.AuthMode {
	return domain.AuthModeChatGPTWebAccess
}

// exchange runs one upstream exchange, failing closed without a transport.
func (adapter *Adapter) exchange(ctx context.Context, request Request) (Response, error) {
	if adapter == nil || adapter.transport == nil {
		return Response{}, ErrTransportUnavailable
	}
	return adapter.transport.Exchange(ctx, request)
}

// Probe proves the stored credential against the Web backend and reports any
// normalized rate/quota signal it observed along the way.
//
// The sequence follows the evidence's credential bootstrap probe (§3.1 lifecycle
// example, §10.2 probe 1): prove identity on /backend-api/me, then read
// conversation/init for the image_gen allowance. It is auth-proving and
// cost-minimal — it never runs a billable generation (I-PROBE-MINIMAL).
//
// An auth-class failure is returned as Authenticated=false with a nil error so
// the account moves to reauth_required; a transient backend failure is returned
// as ErrDependencyUnavailable so admission fails closed. Web Access has no
// silent refresh (AuthMode.SupportsRefresh is false for it), so there is no
// refresh attempt to make on 401.
func (adapter *Adapter) Probe(ctx context.Context, command ports.ProbeCommand) (ports.ProbeOutcome, error) {
	if command.AuthMode != domain.AuthModeChatGPTWebAccess {
		// Defensive: a registry that routed another mode here would otherwise get
		// ChatGPT Web protocol framing applied to a different credential class.
		return ports.ProbeOutcome{}, ports.ErrDependencyUnavailable
	}

	identity, err := adapter.exchange(ctx, Request{Method: http.MethodGet, Path: PathMe})
	if err != nil {
		return ports.ProbeOutcome{}, dependencyUnavailable("probe identity", err)
	}
	switch classifyStatus(identity.Status) {
	case signalAuthFailed:
		return ports.ProbeOutcome{Authenticated: false}, nil
	case signalChallenged:
		// A challenge is not an auth failure: the credential may be perfectly
		// valid behind an interstitial. Reporting it as unauthenticated would
		// send the Tenant to a pointless reauth; reporting it as a dependency
		// failure keeps the account out of service without claiming the
		// credential is bad, which is the honest classification.
		return ports.ProbeOutcome{}, ports.ErrDependencyUnavailable
	case signalRateLimited:
		return ports.ProbeOutcome{
			Authenticated:     true,
			Signal:            ports.ProbeSignalRateLimited,
			SignalScope:       domain.HealthScope{Kind: domain.HealthScopeAccount},
			RetryAfterSeconds: identity.RetryAfterSeconds,
		}, nil
	case signalOK:
	default:
		return ports.ProbeOutcome{}, ports.ErrDependencyUnavailable
	}

	initial, err := adapter.exchange(ctx, Request{Method: http.MethodPost, Path: PathConversationInit, Body: "{}"})
	if err != nil {
		return ports.ProbeOutcome{}, dependencyUnavailable("probe conversation init", err)
	}
	switch classifyStatus(initial.Status) {
	case signalAuthFailed:
		return ports.ProbeOutcome{Authenticated: false}, nil
	case signalRateLimited:
		return ports.ProbeOutcome{
			Authenticated:     true,
			Signal:            ports.ProbeSignalRateLimited,
			SignalScope:       domain.HealthScope{Kind: domain.HealthScopeAccount},
			RetryAfterSeconds: initial.RetryAfterSeconds,
		}, nil
	case signalOK:
	default:
		return ports.ProbeOutcome{}, ports.ErrDependencyUnavailable
	}

	// The credential proved itself; an exhausted image allowance is a scoped
	// cooldown on the image operation, not a failure to authenticate. Scoping it
	// to image_generation keeps chat usable on the same account, which is what
	// the evidence supports: limits_progress names one feature, not the session.
	if quota := parseImageQuota(initial.Body); quota.Present && quota.Remaining <= 0 {
		return ports.ProbeOutcome{
			Authenticated: true,
			Signal:        ports.ProbeSignalQuotaExhausted,
			SignalScope: domain.HealthScope{
				Kind:      domain.HealthScopeOperation,
				Operation: string(domain.CapabilityOpImageGeneration),
			},
			RetryAfterSeconds: quota.ResetAfterSeconds,
		}, nil
	}

	return ports.ProbeOutcome{Authenticated: true}, nil
}

// Observe records the capability evidence this session actually exposes.
//
// Every operation is reported at `conditionally_supported`, matching the
// accepted evidence for ChatGPT Web Access (§2.1: every primary operation is
// reference-learned and conditionally supported, none is verified). The domain
// clamps this to the canonical baseline anyway, so an over-confident report here
// cannot raise the ceiling — the value of stating it honestly is that the
// snapshot's own provenance stays truthful.
//
// Cancel/abort is the one surface the evidence marks `unverified` (§2.1 "cancel
// / abort ... no dedicated stop/cancel conversation API found"), but it is not
// one of the five primary capability operations, so it has no fact to report.
// Its absence is why a canceled stream cannot claim UpstreamStopConfirmed.
func (adapter *Adapter) Observe(ctx context.Context, command ports.CapabilityObservationCommand) (ports.CapabilityObservation, error) {
	if command.AuthMode != domain.AuthModeChatGPTWebAccess {
		return ports.CapabilityObservation{}, ports.ErrDependencyUnavailable
	}

	listed, err := adapter.exchange(ctx, Request{Method: http.MethodGet, Path: PathModels})
	if err != nil {
		return ports.CapabilityObservation{}, dependencyUnavailable("observe models", err)
	}
	switch classifyStatus(listed.Status) {
	case signalOK:
	case signalAuthFailed:
		// The probe already proved auth; a 401 here means the session died
		// between calls. Fail closed rather than minting an empty snapshot that
		// would read as "this account supports nothing".
		return ports.CapabilityObservation{}, ports.ErrDependencyUnavailable
	default:
		return ports.CapabilityObservation{}, ports.ErrDependencyUnavailable
	}

	operations := make(map[domain.CapabilityOperation]domain.CapabilityFact, len(domain.PrimaryCapabilityOperations()))
	for _, operation := range domain.PrimaryCapabilityOperations() {
		fact := domain.CapabilityFact{
			Status:        domain.CapabilityConditionallySupported,
			EvidenceClass: domain.EvidenceReferenceLearned,
			ProbeSurface:  PathConversation,
		}
		if operation == domain.CapabilityOpChatStreaming {
			// The Web surface streams natively: the upstream conversation endpoint
			// is SSE and the Gateway aggregates it for non-streaming, not the
			// reverse (§2.1 "Non-stream is a client aggregation over SSE").
			fact.StreamingClass = domain.StreamingReal
		}
		operations[operation] = fact
	}

	slugs := modelSlugs(listed.Body)
	models := make([]domain.ModelCapability, 0, len(slugs))
	for _, slug := range slugs {
		perModel := make(map[domain.CapabilityOperation]domain.CapabilityStatus, len(domain.PrimaryCapabilityOperations()))
		for _, operation := range domain.PrimaryCapabilityOperations() {
			perModel[operation] = domain.CapabilityConditionallySupported
		}
		models = append(models, domain.ModelCapability{
			ModelSlug:      slug,
			Operations:     perModel,
			SurfaceBinding: PathConversation,
		})
	}

	return ports.CapabilityObservation{
		Operations:   operations,
		Models:       models,
		ProbeSurface: PathConversation,
	}, nil
}

var (
	_ ports.ProbeAdapter      = (*Adapter)(nil)
	_ ports.CapabilityAdapter = (*Adapter)(nil)
)
