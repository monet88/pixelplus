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
//
// Order: auth → scope → size/shape → admission → ownership/non-enumeration +
// allowlist + eligibility → atomic write.
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

	reservation, canonical, ok := service.admit(ctx, principal, operationReplaceRoutingPolicy)
	if !ok {
		return RoutingPolicyResult{}, service.fail(ctx, sc, canonical)
	}

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

// normalizeRoutingPolicyFields applies domain.ValidateRoutingPolicyShape — the
// same structural invariants as durable restore/Replace — then returns a
// normalized policy copy for further candidate checks.
func normalizeRoutingPolicyFields(input domain.RoutingPolicy) (domain.RoutingPolicy, *domain.CanonicalError) {
	if err := domain.ValidateRoutingPolicyShape(input); err != nil {
		if errors.Is(err, domain.ErrRoutingPolicyModeUnavailable) {
			canonical := domain.NewAuthModeUnavailable()
			return domain.RoutingPolicy{}, &canonical
		}
		return domain.RoutingPolicy{}, ptrInvalid()
	}
	// Defensive copies so later mutation of input slices cannot escape.
	return domain.RoutingPolicy{
		CandidateAccounts: append([]domain.ProviderAccountID(nil), input.CandidateAccounts...),
		SelectionOrder:    append([]domain.ProviderAccountID(nil), input.SelectionOrder...),
		FallbackEnabled:   input.FallbackEnabled,
		FallbackChain:     append([]domain.ProviderAccountID(nil), input.FallbackChain...),
		FallbackAuthModes: append([]domain.AuthMode(nil), input.FallbackAuthModes...),
		Affinity:          input.Affinity,
		LeasePolicy: domain.LeasePolicy{
			Enabled:       input.LeasePolicy.Enabled,
			EligibleUnits: append([]domain.LeaseUnit(nil), input.LeasePolicy.EligibleUnits...),
		},
	}, nil
}

func ptrInvalid() *domain.CanonicalError {
	canonical := domain.NewInvalidRequest()
	return &canonical
}

// validatePolicyCandidates loads and eligibility-checks every referenced id
// before a single Replace. Order per id: ownership visibility → allowlist →
// eligibility. Cross-mode set is selection_order ∪ fallback_chain.
func (service *ProviderAccountService) validatePolicyCandidates(ctx context.Context, principal domain.SecurityPrincipal, policy domain.RoutingPolicy) (domain.CanonicalError, bool) {
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
			// Foreign/unknown/deleted: non-enumerating 404 before allowlist (I-ROUTE-TENANT).
			return service.visibilityCanonical(err), false
		}
		// Same-Tenant allowlist denial is 403, never 404 (#8 allowlist, non-enum).
		if !principal.AllowsProviderAccount(id) {
			return domain.NewForbidden(), false
		}
		if canonical, ok := service.policyCandidateRejection(ctx, principal, account); !ok {
			return canonical, false
		}
		modesByAccount[id] = account.AuthMode
	}

	// Cross-auth-mode fallback declaration (§8.1 "fallback_auth_modes when
	// cross-mode fallback intended"; NF-XMODE). Only when fallback is enabled:
	// modes from selection_order ∪ fallback_chain with more than one distinct
	// mode require fallback_auth_modes to enumerate every mode. Multi-mode
	// selection_order alone with fallback_enabled=false is allowed (shape
	// already forces empty fallback_auth_modes when fallback is off).
	if policy.FallbackEnabled {
		modeSet := make(map[domain.AuthMode]struct{})
		for _, id := range policy.SelectionOrder {
			modeSet[modesByAccount[id]] = struct{}{}
		}
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
				if mode.Prohibited() || mode.Experimental() {
					return domain.NewAuthModeUnavailable(), false
				}
			}
		}
	}

	return domain.CanonicalError{}, true
}

// policyCandidateRejection applies usability/risk/health/capability/circuit
// gates for a same-Tenant, allowlist-permitted account.
func (service *ProviderAccountService) policyCandidateRejection(ctx context.Context, principal domain.SecurityPrincipal, account domain.ProviderAccount) (domain.CanonicalError, bool) {
	// Production fail-closed: experimental modes have no lab profile.
	if account.AuthMode.Experimental() {
		return domain.NewAuthModeUnavailable(), false
	}
	if canonical, ok := service.authModeGate(account); !ok {
		return canonical, false
	}
	if account.Lifecycle != domain.LifecycleActive {
		return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
	}
	// Active alone is not enough: effective unknown health is not eligible.
	if account.Health.SummaryState == domain.HealthUnknown {
		return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
	}
	if account.Controls.Drain == domain.DrainDraining || account.Controls.Quarantine == domain.QuarantineQuarantined {
		return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
	}
	if accountHasNonRoutableAccountHealth(account) {
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
			if condition.State == domain.HealthUnknown {
				return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
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
	case domain.SnapshotStale, domain.SnapshotInvalid:
		return domain.NewSnapshotStale(), false
	case domain.SnapshotFresh:
	default:
		return domain.NewCapabilityUnverified(), false
	}
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
			if err != nil {
				// Circuit store dependency failure is 503, not capability_unsupported.
				return service.dependencyCanonical(err), false
			}
			if circuit.Open {
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
