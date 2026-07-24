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

// replaceRoutingPolicyRequest mirrors frozen RoutingPolicyFields. Pointer fields
// distinguish missing/null (invalid) from present empty arrays. Unknown fields
// are rejected so clients cannot smuggle tenant_id.
type replaceRoutingPolicyRequest struct {
	CandidateAccounts *[]string              `json:"candidate_accounts"`
	SelectionOrder    *[]string              `json:"selection_order"`
	FallbackEnabled   *bool                  `json:"fallback_enabled"`
	FallbackChain     *[]string              `json:"fallback_chain"`
	FallbackAuthModes *[]string              `json:"fallback_auth_modes"`
	Affinity          *affinityPolicyRequest `json:"affinity"`
	LeasePolicy       *leasePolicyRequest    `json:"lease_policy"`
}

type affinityPolicyRequest struct {
	Enabled     *bool  `json:"enabled"`
	WindowClass string `json:"window_class"`
}

type leasePolicyRequest struct {
	Enabled       *bool     `json:"enabled"`
	EligibleUnits *[]string `json:"eligible_units"`
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
			// Required fields missing/null after decode (OpenAPI required set).
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

// complete reports whether every frozen RoutingPolicyFields top-level property
// is present and non-null, and lease_policy carries both required nested fields.
func (parsed replaceRoutingPolicyRequest) complete() bool {
	if parsed.CandidateAccounts == nil ||
		parsed.SelectionOrder == nil ||
		parsed.FallbackEnabled == nil ||
		parsed.FallbackChain == nil ||
		parsed.FallbackAuthModes == nil ||
		parsed.Affinity == nil ||
		parsed.LeasePolicy == nil {
		return false
	}
	if parsed.Affinity.Enabled == nil {
		return false
	}
	if parsed.LeasePolicy.Enabled == nil || parsed.LeasePolicy.EligibleUnits == nil {
		return false
	}
	return true
}

func (parsed replaceRoutingPolicyRequest) toDomain() domain.RoutingPolicy {
	candidates := make([]domain.ProviderAccountID, 0, len(*parsed.CandidateAccounts))
	for _, id := range *parsed.CandidateAccounts {
		candidates = append(candidates, domain.ProviderAccountID(id))
	}
	selection := make([]domain.ProviderAccountID, 0, len(*parsed.SelectionOrder))
	for _, id := range *parsed.SelectionOrder {
		selection = append(selection, domain.ProviderAccountID(id))
	}
	chain := make([]domain.ProviderAccountID, 0, len(*parsed.FallbackChain))
	for _, id := range *parsed.FallbackChain {
		chain = append(chain, domain.ProviderAccountID(id))
	}
	modes := make([]domain.AuthMode, 0, len(*parsed.FallbackAuthModes))
	for _, mode := range *parsed.FallbackAuthModes {
		modes = append(modes, domain.AuthMode(mode))
	}
	units := make([]domain.LeaseUnit, 0, len(*parsed.LeasePolicy.EligibleUnits))
	for _, unit := range *parsed.LeasePolicy.EligibleUnits {
		units = append(units, domain.LeaseUnit(unit))
	}
	return domain.RoutingPolicy{
		CandidateAccounts: candidates,
		SelectionOrder:    selection,
		FallbackEnabled:   *parsed.FallbackEnabled,
		FallbackChain:     chain,
		FallbackAuthModes: modes,
		Affinity: domain.AffinityPolicy{
			Enabled:     *parsed.Affinity.Enabled,
			WindowClass: parsed.Affinity.WindowClass,
		},
		LeasePolicy: domain.LeasePolicy{
			Enabled:       *parsed.LeasePolicy.Enabled,
			EligibleUnits: units,
		},
	}
}
