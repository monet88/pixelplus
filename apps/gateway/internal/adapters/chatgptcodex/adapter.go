package chatgptcodex

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
// enabled the gated profile but forgot to supply transport sees only "dependency
// unavailable" with nothing to distinguish it from a Provider outage.
//
// The stage label is a fixed literal chosen by this package, never a Provider
// body, header, or credential value (OP-G3), and the wrapped cause is always an
// Adapter-internal or transport error for the same reason.
func dependencyUnavailable(stage string, cause error) error {
	return fmt.Errorf("chatgpt codex %s: %w: %w", stage, ports.ErrDependencyUnavailable, cause)
}

// Adapter is the ChatGPT Codex OAuth protocol Adapter. It is stateless: the only
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
	return domain.AuthModeChatGPTCodexOAuth
}

// exchange runs one upstream exchange, failing closed without a transport.
func (adapter *Adapter) exchange(ctx context.Context, request Request) (Response, error) {
	if adapter == nil || adapter.transport == nil {
		return Response{}, ErrTransportUnavailable
	}
	return adapter.transport.Exchange(ctx, request)
}

// Probe proves the stored credential against the Codex surface and reports any
// normalized rate/quota signal it observed along the way.
//
// The sequence follows the evidence's credential bootstrap probe (§3.2
// lifecycle example, §10.2 probe 1): prove the access_token is alive with a
// minimal authenticated identity call. It is auth-proving and cost-minimal — it
// never runs a billable generation (I-PROBE-MINIMAL).
//
// An auth-class failure is returned as Authenticated=false with a nil error so
// the account moves to reauth_required; a transient backend failure is returned
// as ErrDependencyUnavailable so admission fails closed. A challenged
// (Cloudflare/bot) response is a dependency failure rather than an auth failure:
// the credential may be valid behind the block, so reporting it unauthenticated
// would send the Tenant to a pointless reauth.
//
// The Adapter does not refresh on a 401 here because the probe ports carry no
// credential (decision 0014 / #111): a refresh belongs inside the
// CredentialInjection callback the chat surface holds, which the probe surface
// does not. With a nil transport the probe fails closed before any exchange.
func (adapter *Adapter) Probe(ctx context.Context, command ports.ProbeCommand) (ports.ProbeOutcome, error) {
	if command.AuthMode != domain.AuthModeChatGPTCodexOAuth {
		// Defensive: a registry that routed another mode here would otherwise get
		// Codex Responses framing applied to a different credential class.
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
		// valid behind a Cloudflare/bot block. Reporting it as unauthenticated
		// would send the Tenant to a pointless reauth; reporting it as a
		// dependency failure keeps the account out of service without claiming
		// the credential is bad, which is the honest classification.
		return ports.ProbeOutcome{}, ports.ErrDependencyUnavailable
	case signalRateLimited:
		// A 429 may carry either a transient rate_limit_error or a
		// usage_limit_reached body (evidence §5). The quota form has a reset
		// hint and is a cooldown-worthy exhaustion; the rate form is a
		// transient backoff. Distinguishing them here keeps the scoped
		// cooldown honest.
		if quota := parseUsageLimit(identity.Body); quota.Present {
			return ports.ProbeOutcome{
				Authenticated:     true,
				Signal:            ports.ProbeSignalQuotaExhausted,
				SignalScope:       domain.HealthScope{Kind: domain.HealthScopeAccount},
				RetryAfterSeconds: quota.ResetsAfterSeconds,
			}, nil
		}
		return ports.ProbeOutcome{
			Authenticated:     true,
			Signal:            ports.ProbeSignalRateLimited,
			SignalScope:       domain.HealthScope{Kind: domain.HealthScopeAccount},
			RetryAfterSeconds: identity.RetryAfterSeconds,
		}, nil
	case signalOK:
		// A 200 body can still carry a usage_limit_reached error event (evidence
		// §5: usage_limit_reached surfaces inside a Responses body). Surface it
		// as a scoped quota signal so the account activates (auth proven) with a
		// durable cooldown overlay, exactly like the Web surface's image_gen
		// quota.
		if quota := parseUsageLimit(identity.Body); quota.Present {
			return ports.ProbeOutcome{
				Authenticated:     true,
				Signal:            ports.ProbeSignalQuotaExhausted,
				SignalScope:       domain.HealthScope{Kind: domain.HealthScopeAccount},
				RetryAfterSeconds: quota.ResetsAfterSeconds,
			}, nil
		}
		return ports.ProbeOutcome{Authenticated: true}, nil
	default:
		return ports.ProbeOutcome{}, ports.ErrDependencyUnavailable
	}
}

// Observe records the capability evidence this session actually exposes.
//
// Every operation is reported at `conditionally_supported`, matching the
// accepted evidence for ChatGPT Codex OAuth (§2.2: every primary operation is
// reference-learned and conditionally supported, none is verified). The domain
// clamps this to the canonical baseline anyway, so an over-confident report here
// cannot raise the ceiling — the value of stating it honestly is that the
// snapshot's own provenance stays truthful.
//
// Model discovery is `conditionally_supported` and reference-learned from a
// static catalog (§2.2 "model listing / discovery"), so the slugs observed here
// are what the session exposed, not a static provider catalog hardcoded in this
// Adapter. A real Transport MUST NOT ship before probe-time discovery is
// resolved (decision 0014 Follow-Up).
func (adapter *Adapter) Observe(ctx context.Context, command ports.CapabilityObservationCommand) (ports.CapabilityObservation, error) {
	if command.AuthMode != domain.AuthModeChatGPTCodexOAuth {
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
			ProbeSurface:  PathCodexResponses,
		}
		if operation == domain.CapabilityOpChatStreaming {
			// The Codex surface streams natively: /backend-api/codex/responses
			// with Accept: text/event-stream is the streaming surface, and a
			// websocket lite path exists for responses-lite models (§2.2
			// "chat streaming").
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
			SurfaceBinding: PathCodexResponses,
		})
	}

	return ports.CapabilityObservation{
		Operations:   operations,
		Models:       models,
		ProbeSurface: PathCodexResponses,
	}, nil
}

var (
	_ ports.ProbeAdapter      = (*Adapter)(nil)
	_ ports.CapabilityAdapter = (*Adapter)(nil)
)
