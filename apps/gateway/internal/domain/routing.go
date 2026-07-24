package domain

import "time"

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
