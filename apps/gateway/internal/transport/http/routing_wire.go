package httptransport

import (
	"net/http"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// Routing Policy wire DTOs mirror frozen RoutingPolicy / RoutingPolicyFields.

type routingPolicyWire struct {
	CandidateAccounts []string           `json:"candidate_accounts"`
	SelectionOrder    []string           `json:"selection_order"`
	FallbackEnabled   bool               `json:"fallback_enabled"`
	FallbackChain     []string           `json:"fallback_chain"`
	FallbackAuthModes []string           `json:"fallback_auth_modes"`
	Affinity          affinityPolicyWire `json:"affinity"`
	LeasePolicy       leasePolicyWire    `json:"lease_policy"`
	UpdatedAt         string             `json:"updated_at"`
	UpdatedBy         string             `json:"updated_by"`
}

type affinityPolicyWire struct {
	Enabled     bool   `json:"enabled"`
	WindowClass string `json:"window_class,omitempty"`
}

type leasePolicyWire struct {
	Enabled       bool     `json:"enabled"`
	EligibleUnits []string `json:"eligible_units"`
}

func toRoutingPolicyWire(policy domain.RoutingPolicy) routingPolicyWire {
	candidates := make([]string, 0, len(policy.CandidateAccounts))
	for _, id := range policy.CandidateAccounts {
		candidates = append(candidates, string(id))
	}
	selection := make([]string, 0, len(policy.SelectionOrder))
	for _, id := range policy.SelectionOrder {
		selection = append(selection, string(id))
	}
	chain := make([]string, 0, len(policy.FallbackChain))
	for _, id := range policy.FallbackChain {
		chain = append(chain, string(id))
	}
	modes := make([]string, 0, len(policy.FallbackAuthModes))
	for _, mode := range policy.FallbackAuthModes {
		modes = append(modes, string(mode))
	}
	units := make([]string, 0, len(policy.LeasePolicy.EligibleUnits))
	for _, unit := range policy.LeasePolicy.EligibleUnits {
		units = append(units, string(unit))
	}
	updatedAt := timestampString(policy.UpdatedAt)
	if updatedAt == "" {
		// System default / never-written policy still exposes a stable required
		// date-time field without inventing a write event.
		updatedAt = "1970-01-01T00:00:00Z"
	}
	return routingPolicyWire{
		CandidateAccounts: candidates,
		SelectionOrder:    selection,
		FallbackEnabled:   policy.FallbackEnabled,
		FallbackChain:     chain,
		FallbackAuthModes: modes,
		Affinity: affinityPolicyWire{
			Enabled:     policy.Affinity.Enabled,
			WindowClass: policy.Affinity.WindowClass,
		},
		LeasePolicy: leasePolicyWire{
			Enabled:       policy.LeasePolicy.Enabled,
			EligibleUnits: units,
		},
		UpdatedAt: updatedAt,
		UpdatedBy: string(policy.UpdatedBy),
	}
}

func writeRoutingPolicy(writer http.ResponseWriter, statusCode int, policy domain.RoutingPolicy) {
	writeJSON(writer, statusCode, toRoutingPolicyWire(policy))
}
