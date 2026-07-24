package domain

import (
	"errors"
	"fmt"
	"time"
)

// LeaseUnit names a unit of work that may acquire an account lease under
// Tenant Routing Policy (routing/fallback/affinity/leases spec §5.2).
type LeaseUnit string

// Frozen lease unit vocabulary (OpenAPI LeasePolicy.eligible_units).
const (
	LeaseUnitChatStream LeaseUnit = "chat_stream"
	LeaseUnitRenderJob  LeaseUnit = "render_job"
)

// Valid reports whether the lease unit is in the frozen enum.
func (unit LeaseUnit) Valid() bool {
	switch unit {
	case LeaseUnitChatStream, LeaseUnitRenderJob:
		return true
	default:
		return false
	}
}

// AffinityPolicy is the soft preference configuration for reusing an account
// across related requests (routing spec §5.1, OpenAPI AffinityPolicy).
type AffinityPolicy struct {
	Enabled     bool
	WindowClass string
}

// LeasePolicy declares whether units of work acquire hard account leases
// (routing spec §5.2, OpenAPI LeasePolicy).
type LeasePolicy struct {
	Enabled       bool
	EligibleUnits []LeaseUnit
}

// RoutingPolicy is the Tenant-owned singleton configuration that orders and
// optionally falls back among that Tenant's own Provider Accounts. It never
// carries foreign accounts or client-supplied tenant_id (routing spec §8,
// I-ROUTE-POLICY-SCOPE).
type RoutingPolicy struct {
	CandidateAccounts []ProviderAccountID
	SelectionOrder    []ProviderAccountID
	FallbackEnabled   bool
	FallbackChain     []ProviderAccountID
	FallbackAuthModes []AuthMode
	Affinity          AffinityPolicy
	LeasePolicy       LeasePolicy
	// UpdatedAt is server-owned audit time of the last successful replace.
	UpdatedAt Timestamp
	// UpdatedBy is the Client API Key id (or system_default) that last wrote.
	UpdatedBy ClientAPIKeyID
}

// SystemDefaultUpdatedBy is the audit actor for the fail-closed projection
// returned when no durable policy row exists yet.
const SystemDefaultUpdatedBy ClientAPIKeyID = "system_default"

// SystemDefaultUpdatedAt is the deterministic server-owned audit instant for the
// fail-closed projection when no durable policy row exists. It is not a
// wall-clock write event; Unix epoch UTC keeps required OpenAPI date-time
// `updated_at` stable and RFC3339-parseable without inventing a Tenant actor.
var SystemDefaultUpdatedAt = NewTimestamp(time.Date(1970, time.January, 1, 0, 0, 0, 0, time.UTC))

// FailClosedDefaultRoutingPolicy returns the system default singleton when no
// Tenant policy has been written: empty candidates and fallback disabled
// (routing spec §6.5 / §8.2, NF-NOPOLICY posture). updated_at/updated_by are
// always server-owned and valid on the wire.
func FailClosedDefaultRoutingPolicy() RoutingPolicy {
	return RoutingPolicy{
		CandidateAccounts: []ProviderAccountID{},
		SelectionOrder:    []ProviderAccountID{},
		FallbackEnabled:   false,
		FallbackChain:     []ProviderAccountID{},
		FallbackAuthModes: []AuthMode{},
		Affinity:          AffinityPolicy{Enabled: false},
		LeasePolicy:       LeasePolicy{Enabled: false, EligibleUnits: []LeaseUnit{}},
		UpdatedAt:         SystemDefaultUpdatedAt,
		UpdatedBy:         SystemDefaultUpdatedBy,
	}
}

// ErrRoutingPolicyShape is returned by ValidateRoutingPolicyShape for structural
// failures (invalid request vocabulary at the application boundary).
var ErrRoutingPolicyShape = errors.New("routing policy shape invalid")

// ErrRoutingPolicyModeUnavailable is returned when a declared Auth Mode is
// prohibited or experimental under production fail-closed posture.
var ErrRoutingPolicyModeUnavailable = errors.New("routing policy auth mode unavailable")

// ValidateRoutingPolicyShape enforces the shared structural invariants used by
// both application Replace and durable File store restore/Replace:
// unique/pattern ids, ordered subsets, fallback opt-in, lease enums, and
// fail-closed rejection of prohibited/experimental modes in fallback_auth_modes.
// It does not require audit fields (application owns updated_at/by on write).
func ValidateRoutingPolicyShape(policy RoutingPolicy) error {
	candidates, err := uniqueValidIDs(policy.CandidateAccounts)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRoutingPolicyShape, err)
	}
	selection, err := uniqueValidIDs(policy.SelectionOrder)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRoutingPolicyShape, err)
	}
	chain, err := uniqueValidIDs(policy.FallbackChain)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRoutingPolicyShape, err)
	}
	if !idsSubset(selection, candidates) {
		return fmt.Errorf("%w: selection_order not subset of candidate_accounts", ErrRoutingPolicyShape)
	}
	if !idsSubset(chain, candidates) {
		return fmt.Errorf("%w: fallback_chain not subset of candidate_accounts", ErrRoutingPolicyShape)
	}
	if !policy.FallbackEnabled {
		if len(chain) > 0 || len(policy.FallbackAuthModes) > 0 {
			return fmt.Errorf("%w: fallback disabled with chain or modes", ErrRoutingPolicyShape)
		}
	} else if len(chain) == 0 {
		// §8.1 "fallback_chain when fallback_enabled"; I-ROUTE-FALLBACK-OPTIN.
		return fmt.Errorf("%w: fallback enabled without chain", ErrRoutingPolicyShape)
	}

	seenModes := make(map[AuthMode]struct{}, len(policy.FallbackAuthModes))
	for _, mode := range policy.FallbackAuthModes {
		if !mode.Valid() {
			return fmt.Errorf("%w: invalid fallback auth mode", ErrRoutingPolicyShape)
		}
		if mode.Prohibited() || mode.Experimental() {
			// Production fail-closed: no lab profile; Grok Web SSO prohibited.
			return fmt.Errorf("%w: %s", ErrRoutingPolicyModeUnavailable, mode)
		}
		if _, ok := seenModes[mode]; ok {
			return fmt.Errorf("%w: duplicate fallback auth mode", ErrRoutingPolicyShape)
		}
		seenModes[mode] = struct{}{}
	}
	seenUnits := make(map[LeaseUnit]struct{}, len(policy.LeasePolicy.EligibleUnits))
	for _, unit := range policy.LeasePolicy.EligibleUnits {
		if !unit.Valid() {
			return fmt.Errorf("%w: invalid lease unit", ErrRoutingPolicyShape)
		}
		if _, ok := seenUnits[unit]; ok {
			return fmt.Errorf("%w: duplicate lease unit", ErrRoutingPolicyShape)
		}
		seenUnits[unit] = struct{}{}
	}
	return nil
}

// ValidateRoutingPolicyDurable adds server-owned audit field requirements on
// top of ValidateRoutingPolicyShape for persisted ledger rows.
func ValidateRoutingPolicyDurable(policy RoutingPolicy) error {
	if policy.UpdatedBy == "" {
		return fmt.Errorf("%w: missing updated_by", ErrRoutingPolicyShape)
	}
	if policy.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: missing updated_at", ErrRoutingPolicyShape)
	}
	return ValidateRoutingPolicyShape(policy)
}

func uniqueValidIDs(ids []ProviderAccountID) ([]ProviderAccountID, error) {
	seen := make(map[ProviderAccountID]struct{}, len(ids))
	out := make([]ProviderAccountID, 0, len(ids))
	for _, id := range ids {
		if !id.Valid() {
			return nil, errors.New("invalid provider_account_id")
		}
		if _, ok := seen[id]; ok {
			return nil, errors.New("duplicate provider_account_id")
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

func idsSubset(subset, universe []ProviderAccountID) bool {
	index := make(map[ProviderAccountID]struct{}, len(universe))
	for _, id := range universe {
		index[id] = struct{}{}
	}
	for _, id := range subset {
		if _, ok := index[id]; !ok {
			return false
		}
	}
	return true
}
