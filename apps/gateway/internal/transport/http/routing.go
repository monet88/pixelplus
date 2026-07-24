package httptransport

import (
	"context"
	"net/http"

	"github.com/monet88/pixelplus/apps/gateway/internal/application"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// RoutingPolicyGateway is the application seam for Tenant Routing Policy
// read/replace. Transport never imports concrete application wiring beyond
// command/query types.
type RoutingPolicyGateway interface {
	GetRoutingPolicy(context.Context, application.GetRoutingPolicyQuery) (application.RoutingPolicyResult, error)
	ReplaceRoutingPolicy(context.Context, application.ReplaceRoutingPolicyCommand) (application.RoutingPolicyResult, error)
}

type routingPolicyHandler struct {
	gateway RoutingPolicyGateway
	ids     idGenerator
}

// registerRoutingPolicyRoutes attaches the stable Tenant singleton Routing
// Policy routes at /v1/routing-policy.
func registerRoutingPolicyRoutes(mux *http.ServeMux, gateway RoutingPolicyGateway, ids idGenerator) {
	handler := routingPolicyHandler{gateway: gateway, ids: ids}
	mux.HandleFunc("GET /v1/routing-policy", handler.get)
	mux.HandleFunc("PUT /v1/routing-policy", handler.replace)
}

func (handler routingPolicyHandler) newRequestID() domain.Identifier {
	id, err := handler.ids.New(domain.IdentifierKindRequest)
	if err != nil {
		return ""
	}
	return id
}

func (handler routingPolicyHandler) get(writer http.ResponseWriter, request *http.Request) {
	presented, _ := bearerMaterial(request)
	result, err := handler.gateway.GetRoutingPolicy(request.Context(), application.GetRoutingPolicyQuery{
		RequestID:            handler.newRequestID(),
		PresentedKeyMaterial: presented,
	})
	if err != nil {
		writeGatewayError(writer, err)
		return
	}
	writeRoutingPolicy(writer, http.StatusOK, result.Policy)
}

// replaceRoutingPolicyRequest mirrors frozen RoutingPolicyFields. Unknown
// fields are rejected so clients cannot smuggle tenant_id.
type replaceRoutingPolicyRequest struct {
	CandidateAccounts []string               `json:"candidate_accounts"`
	SelectionOrder    []string               `json:"selection_order"`
	FallbackEnabled   *bool                  `json:"fallback_enabled"`
	FallbackChain     []string               `json:"fallback_chain"`
	FallbackAuthModes []string               `json:"fallback_auth_modes"`
	Affinity          *affinityPolicyRequest `json:"affinity"`
	LeasePolicy       *leasePolicyRequest    `json:"lease_policy"`
}

type affinityPolicyRequest struct {
	Enabled     *bool  `json:"enabled"`
	WindowClass string `json:"window_class"`
}

type leasePolicyRequest struct {
	Enabled       *bool    `json:"enabled"`
	EligibleUnits []string `json:"eligible_units"`
}

func (handler routingPolicyHandler) replace(writer http.ResponseWriter, request *http.Request) {
	requestID := handler.newRequestID()
	presented, _ := bearerMaterial(request)

	// Observe A2 size and strict decode without short-circuiting so A0 auth and
	// A1 scope still run first for unauthenticated malformed/oversized bodies.
	body, oversize := readLimitedBody(request)
	var parsed replaceRoutingPolicyRequest
	malformed := false
	if !oversize {
		if err := decodeStrictJSON(body, &parsed); err != nil {
			malformed = true
		} else if !parsed.complete() {
			// Required object fields missing after decode (null affinity, etc.).
			malformed = true
		}
	}

	command := application.ReplaceRoutingPolicyCommand{
		RequestID:            requestID,
		PresentedKeyMaterial: presented,
		OversizeBody:         oversize,
		MalformedBody:        malformed,
	}
	if !oversize && !malformed {
		command.Policy = parsed.toDomain()
	}

	result, err := handler.gateway.ReplaceRoutingPolicy(request.Context(), command)
	if err != nil {
		writeGatewayError(writer, err)
		return
	}
	writeRoutingPolicy(writer, http.StatusOK, result.Policy)
}

func (parsed replaceRoutingPolicyRequest) complete() bool {
	if parsed.FallbackEnabled == nil || parsed.Affinity == nil || parsed.LeasePolicy == nil {
		return false
	}
	if parsed.Affinity.Enabled == nil || parsed.LeasePolicy.Enabled == nil {
		return false
	}
	// Arrays may be empty but must be present as non-null after required decode.
	// A null JSON array decodes to nil; treat as present empty via toDomain.
	// Missing keys leave nil slices which we accept as empty after shape normalize.
	return true
}

func (parsed replaceRoutingPolicyRequest) toDomain() domain.RoutingPolicy {
	candidates := make([]domain.ProviderAccountID, 0, len(parsed.CandidateAccounts))
	for _, id := range parsed.CandidateAccounts {
		candidates = append(candidates, domain.ProviderAccountID(id))
	}
	selection := make([]domain.ProviderAccountID, 0, len(parsed.SelectionOrder))
	for _, id := range parsed.SelectionOrder {
		selection = append(selection, domain.ProviderAccountID(id))
	}
	chain := make([]domain.ProviderAccountID, 0, len(parsed.FallbackChain))
	for _, id := range parsed.FallbackChain {
		chain = append(chain, domain.ProviderAccountID(id))
	}
	modes := make([]domain.AuthMode, 0, len(parsed.FallbackAuthModes))
	for _, mode := range parsed.FallbackAuthModes {
		modes = append(modes, domain.AuthMode(mode))
	}
	units := make([]domain.LeaseUnit, 0, len(parsed.LeasePolicy.EligibleUnits))
	for _, unit := range parsed.LeasePolicy.EligibleUnits {
		units = append(units, domain.LeaseUnit(unit))
	}
	fallbackEnabled := false
	if parsed.FallbackEnabled != nil {
		fallbackEnabled = *parsed.FallbackEnabled
	}
	affinityEnabled := false
	windowClass := ""
	if parsed.Affinity != nil {
		if parsed.Affinity.Enabled != nil {
			affinityEnabled = *parsed.Affinity.Enabled
		}
		windowClass = parsed.Affinity.WindowClass
	}
	leaseEnabled := false
	if parsed.LeasePolicy != nil && parsed.LeasePolicy.Enabled != nil {
		leaseEnabled = *parsed.LeasePolicy.Enabled
	}
	return domain.RoutingPolicy{
		CandidateAccounts: candidates,
		SelectionOrder:    selection,
		FallbackEnabled:   fallbackEnabled,
		FallbackChain:     chain,
		FallbackAuthModes: modes,
		Affinity: domain.AffinityPolicy{
			Enabled:     affinityEnabled,
			WindowClass: windowClass,
		},
		LeasePolicy: domain.LeasePolicy{
			Enabled:       leaseEnabled,
			EligibleUnits: units,
		},
	}
}
