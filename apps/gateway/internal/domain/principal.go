package domain

// TenantID is the immutable ownership boundary identity (#6).
type TenantID string

// ClientAPIKeyID is the stable, safe-to-expose Client API Key identity.
type ClientAPIKeyID string

// Scope names a least-privilege operation id a Client API Key may exercise.
type Scope string

// MVP operation-id scope vocabulary consumed by the request spine. Only the
// scopes required by the current vertical slice are enumerated; the set can
// grow as later operations land.
const (
	ScopeAccountsRead   Scope = "accounts.read"
	ScopeAccountsManage Scope = "accounts.manage"
	// Asset scopes (#8 section 5.2, consumed by the Asset exchange spine).
	// Read/list/download/reference require assets.read; upload/delete require
	// assets.write.
	ScopeAssetsRead  Scope = "assets.read"
	ScopeAssetsWrite Scope = "assets.write"
	// ScopeCapabilitiesRead authorizes Capability Snapshot inspection and the
	// offerable model list (capability semantics section 11; OpenAPI x-required-scopes).
	ScopeCapabilitiesRead Scope = "capabilities.read"
	// ScopeRoutingRead authorizes reading the Tenant singleton Routing Policy
	// (routing/fallback/affinity/leases §8.4; OpenAPI getRoutingPolicy).
	ScopeRoutingRead Scope = "routing.read"
	// ScopeRoutingManage authorizes atomic replacement of the Tenant singleton
	// Routing Policy (routing/fallback/affinity/leases §8.4; OpenAPI replaceRoutingPolicy).
	ScopeRoutingManage Scope = "routing.manage"
)

// ScopeSet is the set of operation ids granted to a Security Principal.
type ScopeSet map[Scope]struct{}

// NewScopeSet builds a scope set from the granted operation ids.
func NewScopeSet(scopes ...Scope) ScopeSet {
	set := make(ScopeSet, len(scopes))
	for _, scope := range scopes {
		set[scope] = struct{}{}
	}
	return set
}

// Has reports whether the scope is granted.
func (set ScopeSet) Has(scope Scope) bool {
	_, ok := set[scope]
	return ok
}

// SecurityPrincipal is the authenticated identity of one Public API request.
// It derives Tenant and Client API Key identity server-side; client-supplied
// Tenant authority is never trusted (#6 section 2.2).
type SecurityPrincipal struct {
	TenantID       TenantID
	ClientAPIKeyID ClientAPIKeyID
	Scopes         ScopeSet
	// ProviderAccountAllowlist is the Client API Key account allowlist (#8 §5.1/§5.4).
	// Tri-state:
	//   - nil: unrestricted (all same-Tenant accounts permitted)
	//   - non-nil empty: deny-all
	//   - non-nil non-empty: membership required
	// Ownership/non-enumeration still wins: foreign/unknown/deleted never become
	// allowlist denials (they stay resource_not_found).
	ProviderAccountAllowlist *[]ProviderAccountID
}

// Valid reports whether both ownership and key identities are present.
func (principal SecurityPrincipal) Valid() bool {
	return principal.TenantID != "" && principal.ClientAPIKeyID != ""
}

// AllowsProviderAccount reports whether the allowlist permits a same-Tenant
// account id. Call only after ownership visibility is established.
func (principal SecurityPrincipal) AllowsProviderAccount(id ProviderAccountID) bool {
	if principal.ProviderAccountAllowlist == nil {
		return true
	}
	for _, allowed := range *principal.ProviderAccountAllowlist {
		if allowed == id {
			return true
		}
	}
	return false
}
