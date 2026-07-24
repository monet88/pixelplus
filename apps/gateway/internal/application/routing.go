package application

import (
	"context"
	"errors"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

const (
	operationGetRoutingPolicy     domain.OperationToken = "get_routing_policy"
	operationReplaceRoutingPolicy domain.OperationToken = "replace_routing_policy"
)

// GetRoutingPolicyQuery is the typed Tenant singleton Routing Policy read.
type GetRoutingPolicyQuery struct {
	PresentedKeyMaterial string
	RequestID            domain.Identifier
}

// ReplaceRoutingPolicyCommand is the typed atomic Routing Policy replace.
// Transport observes size and strict-decode outcomes as flags so the spine
// enforces A0 auth → A1 scope → A2 size → validation before any mutation.
type ReplaceRoutingPolicyCommand struct {
	PresentedKeyMaterial string
	RequestID            domain.Identifier
	// Policy is the decoded candidate body. Ignored when MalformedBody/Oversize.
	Policy        domain.RoutingPolicy
	OversizeBody  bool
	MalformedBody bool
}

// RoutingPolicyResult carries one safe policy projection plus the server-owned
// request id.
type RoutingPolicyResult struct {
	Policy    domain.RoutingPolicy
	RequestID domain.Identifier
}

// GetRoutingPolicy reads the authenticated Tenant's singleton Routing Policy.
// Missing durable policy fails closed to the system default (empty candidates,
// fallback off). Requires routing.read. Never accepts tenant_id and never
// decrypts credentials (routing spec §8, OpenAPI getRoutingPolicy).
func (service *ProviderAccountService) GetRoutingPolicy(ctx context.Context, query GetRoutingPolicyQuery) (RoutingPolicyResult, error) {
	sc := spineContext{operation: operationGetRoutingPolicy, requestID: service.resolveRequestID(query.RequestID), start: service.clock.Now()}

	principal, canonical, ok := service.authenticate(ctx, ports.PresentedClientAPIKey{Material: query.PresentedKeyMaterial})
	if !ok {
		return RoutingPolicyResult{}, service.fail(ctx, sc, canonical)
	}
	sc.keyID = principal.ClientAPIKeyID

	if !principal.Scopes.Has(domain.ScopeRoutingRead) {
		return RoutingPolicyResult{}, service.fail(ctx, sc, domain.NewForbidden())
	}

	reservation, canonical, ok := service.admit(ctx, principal, operationGetRoutingPolicy)
	if !ok {
		return RoutingPolicyResult{}, service.fail(ctx, sc, canonical)
	}
	service.release(ctx, reservation)

	policy, err := service.routing.Read(ctx, principal)
	if err != nil {
		if errors.Is(err, ports.ErrRoutingPolicyNotFound) {
			policy = domain.FailClosedDefaultRoutingPolicy()
		} else {
			return RoutingPolicyResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
		}
	}

	service.observeSuccess(ctx, sc, ports.AuditRoutingPolicyRead, principal, "", 200)
	return RoutingPolicyResult{Policy: policy, RequestID: sc.requestID}, nil
}

// ReplaceRoutingPolicy atomically replaces the authenticated Tenant's Routing
// Policy after shape validation and eligibility checks for every referenced
// account. Foreign/unknown/deleted ids are non-enumerating resource_not_found
// with zero mutation. Requires routing.manage.
func (service *ProviderAccountService) ReplaceRoutingPolicy(ctx context.Context, command ReplaceRoutingPolicyCommand) (RoutingPolicyResult, error) {
	sc := spineContext{operation: operationReplaceRoutingPolicy, requestID: service.resolveRequestID(command.RequestID), start: service.clock.Now()}

	principal, canonical, ok := service.authenticate(ctx, ports.PresentedClientAPIKey{Material: command.PresentedKeyMaterial})
	if !ok {
		return RoutingPolicyResult{}, service.fail(ctx, sc, canonical)
	}
	sc.keyID = principal.ClientAPIKeyID

	if !principal.Scopes.Has(domain.ScopeRoutingManage) {
		return RoutingPolicyResult{}, service.fail(ctx, sc, domain.NewForbidden())
	}

	// A2 size, then strict shape. Normative order is auth → scope → size/shape →
	// admission → candidate validation → atomic write (issue #52 / #8 §6).
	if command.OversizeBody {
		return RoutingPolicyResult{}, service.fail(ctx, sc, domain.NewRequestTooLarge())
	}
	if command.MalformedBody {
		return RoutingPolicyResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}

	policy, shapeErr := normalizeRoutingPolicyFields(command.Policy)
	if shapeErr != nil {
		return RoutingPolicyResult{}, service.fail(ctx, sc, *shapeErr)
	}

	// A3–A5 admission before any candidate existence/capability lookup so an
	// over-quota caller never enumerates accounts and never mutates the store.
	reservation, canonical, ok := service.admit(ctx, principal, operationReplaceRoutingPolicy)
	if !ok {
		return RoutingPolicyResult{}, service.fail(ctx, sc, canonical)
	}

	// Validate every referenced id only after admission. Failures never call
	// Replace (I-ROUTE-POLICY-SCOPE, issue #52 proof 4).
	if canonical, ok := service.validatePolicyCandidates(ctx, principal, policy); !ok {
		service.release(ctx, reservation)
		return RoutingPolicyResult{}, service.fail(ctx, sc, canonical)
	}

	policy.UpdatedAt = domain.NewTimestamp(sc.start)
	policy.UpdatedBy = principal.ClientAPIKeyID

	persisted, err := service.routing.Replace(ctx, ports.RoutingPolicyChange{
		Principal: principal,
		Policy:    policy,
	})
	service.release(ctx, reservation)
	if err != nil {
		return RoutingPolicyResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}

	service.observeSuccess(ctx, sc, ports.AuditRoutingPolicyReplaced, principal, "", 200)
	return RoutingPolicyResult{Policy: persisted, RequestID: sc.requestID}, nil
}

// normalizeRoutingPolicyFields enforces unique arrays, ordered subsets, fallback
// opt-in posture, and closed enums before any account lookup.
func normalizeRoutingPolicyFields(input domain.RoutingPolicy) (domain.RoutingPolicy, *domain.CanonicalError) {
	candidates, err := uniqueAccountIDs(input.CandidateAccounts)
	if err != nil {
		return domain.RoutingPolicy{}, err
	}
	selection, err := uniqueAccountIDs(input.SelectionOrder)
	if err != nil {
		return domain.RoutingPolicy{}, err
	}
	fallback, err := uniqueAccountIDs(input.FallbackChain)
	if err != nil {
		return domain.RoutingPolicy{}, err
	}
	modes, err := uniqueAuthModes(input.FallbackAuthModes)
	if err != nil {
		return domain.RoutingPolicy{}, err
	}
	units, err := uniqueLeaseUnits(input.LeasePolicy.EligibleUnits)
	if err != nil {
		return domain.RoutingPolicy{}, err
	}

	if !isOrderedSubset(selection, candidates) {
		return domain.RoutingPolicy{}, ptrInvalid()
	}
	if !isOrderedSubset(fallback, candidates) {
		return domain.RoutingPolicy{}, ptrInvalid()
	}

	// Fallback defaults off; a disabled flag must not authorize a chain or modes
	// (routing spec §6.5, I-ROUTE-FALLBACK-OPTIN).
	if !input.FallbackEnabled {
		if len(fallback) > 0 || len(modes) > 0 {
			return domain.RoutingPolicy{}, ptrInvalid()
		}
	}
	// When fallback is enabled, an ordered chain is required (§8.1
	// "fallback_chain when fallback_enabled"; I-ROUTE-FALLBACK-OPTIN / §6.6).
	// An empty chain with enabled=true is not a declared ordered policy chain.
	if input.FallbackEnabled && len(fallback) == 0 {
		return domain.RoutingPolicy{}, ptrInvalid()
	}

	// Grok Web SSO is never accepted as a fallback Auth Mode (#7 §6.3 / §5.5).
	for _, mode := range modes {
		if !mode.Valid() || mode.Prohibited() {
			if mode.Prohibited() {
				canonical := domain.NewAuthModeUnavailable()
				return domain.RoutingPolicy{}, &canonical
			}
			return domain.RoutingPolicy{}, ptrInvalid()
		}
	}

	// Cross-mode fallback requires explicit allowed modes when the ordered
	// fallback chain spans more than one Auth Mode. Application re-checks this
	// after loading accounts (mode identity lives on the account, not the body).

	return domain.RoutingPolicy{
		CandidateAccounts: candidates,
		SelectionOrder:    selection,
		FallbackEnabled:   input.FallbackEnabled,
		FallbackChain:     fallback,
		FallbackAuthModes: modes,
		Affinity: domain.AffinityPolicy{
			Enabled:     input.Affinity.Enabled,
			WindowClass: input.Affinity.WindowClass,
		},
		LeasePolicy: domain.LeasePolicy{
			Enabled:       input.LeasePolicy.Enabled,
			EligibleUnits: units,
		},
	}, nil
}

func ptrInvalid() *domain.CanonicalError {
	canonical := domain.NewInvalidRequest()
	return &canonical
}

func uniqueAccountIDs(ids []domain.ProviderAccountID) ([]domain.ProviderAccountID, *domain.CanonicalError) {
	if ids == nil {
		return []domain.ProviderAccountID{}, nil
	}
	seen := make(map[domain.ProviderAccountID]struct{}, len(ids))
	out := make([]domain.ProviderAccountID, 0, len(ids))
	for _, id := range ids {
		// Pattern check before any store lookup so malformed ids never become
		// resource_not_found (OpenAPI `^pa_[A-Za-z0-9_]+$`).
		if !id.Valid() {
			return nil, ptrInvalid()
		}
		if _, ok := seen[id]; ok {
			return nil, ptrInvalid()
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

func uniqueAuthModes(modes []domain.AuthMode) ([]domain.AuthMode, *domain.CanonicalError) {
	if modes == nil {
		return []domain.AuthMode{}, nil
	}
	seen := make(map[domain.AuthMode]struct{}, len(modes))
	out := make([]domain.AuthMode, 0, len(modes))
	for _, mode := range modes {
		if mode == "" || !mode.Valid() {
			return nil, ptrInvalid()
		}
		if _, ok := seen[mode]; ok {
			return nil, ptrInvalid()
		}
		seen[mode] = struct{}{}
		out = append(out, mode)
	}
	return out, nil
}

func uniqueLeaseUnits(units []domain.LeaseUnit) ([]domain.LeaseUnit, *domain.CanonicalError) {
	if units == nil {
		return []domain.LeaseUnit{}, nil
	}
	seen := make(map[domain.LeaseUnit]struct{}, len(units))
	out := make([]domain.LeaseUnit, 0, len(units))
	for _, unit := range units {
		if !unit.Valid() {
			return nil, ptrInvalid()
		}
		if _, ok := seen[unit]; ok {
			return nil, ptrInvalid()
		}
		seen[unit] = struct{}{}
		out = append(out, unit)
	}
	return out, nil
}

// isOrderedSubset reports whether every id in subset appears in universe while
// preserving subset order (OpenAPI unique arrays + routing subset rule).
func isOrderedSubset(subset, universe []domain.ProviderAccountID) bool {
	index := make(map[domain.ProviderAccountID]struct{}, len(universe))
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

// validatePolicyCandidates loads and eligibility-checks every referenced id
// before a single Replace. Foreign/unknown/deleted collapse to resource_not_found
// without a resource_reference.
func (service *ProviderAccountService) validatePolicyCandidates(ctx context.Context, principal domain.SecurityPrincipal, policy domain.RoutingPolicy) (domain.CanonicalError, bool) {
	// Collect unique references preserving first-seen order for stable errors.
	refs := make([]domain.ProviderAccountID, 0, len(policy.CandidateAccounts)+len(policy.SelectionOrder)+len(policy.FallbackChain))
	seen := make(map[domain.ProviderAccountID]struct{})
	for _, id := range append(append(append([]domain.ProviderAccountID{}, policy.CandidateAccounts...), policy.SelectionOrder...), policy.FallbackChain...) {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		refs = append(refs, id)
	}

	modesByAccount := make(map[domain.ProviderAccountID]domain.AuthMode, len(refs))
	for _, id := range refs {
		account, err := service.loadAccount(ctx, principal, id)
		if err != nil {
			return service.visibilityCanonical(err), false
		}
		if canonical, ok := service.policyCandidateRejection(ctx, principal, account); !ok {
			return canonical, false
		}
		modesByAccount[id] = account.AuthMode
	}

	// Cross-mode fallback requires explicit allowed modes listing every mode
	// that appears on the fallback chain (routing spec §6.2, NF-XMODE).
	if policy.FallbackEnabled && len(policy.FallbackChain) > 0 {
		modeSet := make(map[domain.AuthMode]struct{})
		for _, id := range policy.FallbackChain {
			modeSet[modesByAccount[id]] = struct{}{}
		}
		if len(modeSet) > 1 {
			allowed := make(map[domain.AuthMode]struct{}, len(policy.FallbackAuthModes))
			for _, mode := range policy.FallbackAuthModes {
				allowed[mode] = struct{}{}
			}
			for mode := range modeSet {
				if _, ok := allowed[mode]; !ok {
					return domain.NewInvalidRequest(), false
				}
				if mode.Prohibited() {
					return domain.NewAuthModeUnavailable(), false
				}
			}
		}
	}

	return domain.CanonicalError{}, true
}

// policyCandidateRejection applies composed eligibility: usability/risk/health
// (accountAllowsOffers + authModeGate) and capability/circuit offerability.
func (service *ProviderAccountService) policyCandidateRejection(ctx context.Context, principal domain.SecurityPrincipal, account domain.ProviderAccount) (domain.CanonicalError, bool) {
	if canonical, ok := service.authModeGate(account); !ok {
		return canonical, false
	}
	if account.Lifecycle != domain.LifecycleActive {
		return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
	}
	if account.Controls.Drain == domain.DrainDraining || account.Controls.Quarantine == domain.QuarantineQuarantined {
		return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
	}
	if accountHasNonRoutableAccountHealth(account) {
		// Prefer cooldown remediation when a current-version cooling condition is present.
		for _, condition := range account.Health.Conditions {
			if condition.CredentialVersion != account.Credential.Version {
				continue
			}
			if condition.Scope.Kind != domain.HealthScopeAccount {
				continue
			}
			if condition.State == domain.HealthCoolingDown {
				return domain.NewProviderCooldownBlocked(0), false
			}
		}
		return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
	}
	if !service.accountAllowsOffers(account) {
		return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
	}

	snapshot, err := service.capabilities.Get(ctx, principal, account.ID)
	if err != nil {
		if errors.Is(err, ports.ErrCapabilitySnapshotNotFound) {
			return domain.NewCapabilityUnverified(), false
		}
		return service.dependencyCanonical(err), false
	}
	derived := snapshot.WithDerivedFreshness(service.clock.Now())
	switch derived.Freshness {
	case domain.SnapshotStale:
		return domain.NewSnapshotStale(), false
	case domain.SnapshotInvalid:
		return domain.NewSnapshotStale(), false
	case domain.SnapshotFresh:
		// continue
	default:
		return domain.NewCapabilityUnverified(), false
	}
	// Version binding: snapshot for an old credential version cannot authorize.
	if derived.CredentialVersion != account.Credential.Version || derived.AuthMode != account.AuthMode {
		return domain.NewSnapshotStale(), false
	}

	hasOffer := false
	for _, model := range derived.Models {
		for _, op := range domain.PrimaryCapabilityOperations() {
			if !derived.IsOfferablePair(op, model, service.clock.Now()) {
				continue
			}
			if accountHealthBlocksPair(account, op, model.ModelSlug) {
				continue
			}
			fact, hasFact := derived.Operations[op]
			surface := model.SurfaceBinding
			if hasFact && fact.ProbeSurface != "" {
				surface = fact.ProbeSurface
			}
			circuit, err := service.circuits.SurfaceOpen(ctx, ports.CircuitSurface{
				Provider:  account.Provider,
				AuthMode:  account.AuthMode,
				Surface:   surface,
				Operation: op,
			})
			if err != nil || circuit.Open {
				continue
			}
			hasOffer = true
			break
		}
		if hasOffer {
			break
		}
	}
	if !hasOffer {
		return domain.NewCapabilityUnsupported(), false
	}
	return domain.CanonicalError{}, true
}
