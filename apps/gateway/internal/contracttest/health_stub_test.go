package contracttest_test

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// stubHealthStore is an independently controlled in-process HealthStore for
// public composition proofs. Self-contained (no infrastructure import).
type stubHealthStore struct {
	mu        sync.Mutex
	byTenant  map[domain.TenantID]map[domain.ProviderAccountID]stubHealthRow
	initErr   error
	initCalls atomic.Int32
	// forceClaimConflict makes ClaimRecoveryPermit return ErrAccountUpdateConflict
	// without mutating state. Used to prove CAS-loser transport mapping while
	// soft-gate still sees Eligible from an empty-permit snapshot.
	forceClaimConflict bool
	// resolveErr, when set, makes ResolveRecovery fail closed without mutating
	// health/permit. Used for the recovery-success "health fails after account
	// fence" direction.
	resolveErr error
	// claimEntered/claimRelease serialize ClaimRecoveryPermit for disable-before-
	// claim race proofs: the first claim blocks until claimRelease is closed.
	claimEntered chan struct{}
	claimRelease <-chan struct{}
	// epochErr forces ResetForCredentialEpoch to fail (e.g. missing health row).
	epochErr error
	// enableResetHook runs after the HealthStore reset commits but before the
	// application performs its AccountStore lifecycle CAS.
	enableResetHook func()
	// swapPermitBeforeClear, when non-nil, replaces the durable permit at the
	// start of ClearPermit (under lock, before CAS). Proves request-owned
	// cleanup with ExpectedPermit fails closed against a newer owner.
	swapPermitBeforeClear *domain.RecoveryPermit
}

type stubHealthRow struct {
	Health domain.HealthSummary
	Permit domain.RecoveryPermit
}

func newStubHealthStore() *stubHealthStore {
	return &stubHealthStore{byTenant: make(map[domain.TenantID]map[domain.ProviderAccountID]stubHealthRow)}
}

func (s *stubHealthStore) Seed(tenant domain.TenantID, id domain.ProviderAccountID, health domain.HealthSummary, permit domain.RecoveryPermit) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byTenant[tenant] == nil {
		s.byTenant[tenant] = make(map[domain.ProviderAccountID]stubHealthRow)
	}
	s.byTenant[tenant][id] = stubHealthRow{Health: health, Permit: permit}
}

func (s *stubHealthStore) Restore(context.Context) error { return nil }

func (s *stubHealthStore) Read(_ context.Context, principal domain.SecurityPrincipal, id domain.ProviderAccountID) (ports.AccountHealth, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.byTenant[principal.TenantID][id]
	if !ok {
		return ports.AccountHealth{}, ports.ErrHealthNotFound
	}
	return ports.AccountHealth{Health: row.Health, RecoveryPermit: row.Permit}, nil
}

func (s *stubHealthStore) put(tenant domain.TenantID, id domain.ProviderAccountID, row stubHealthRow) {
	if s.byTenant[tenant] == nil {
		s.byTenant[tenant] = make(map[domain.ProviderAccountID]stubHealthRow)
	}
	s.byTenant[tenant][id] = row
}

func (s *stubHealthStore) get(tenant domain.TenantID, id domain.ProviderAccountID) (stubHealthRow, bool) {
	row, ok := s.byTenant[tenant][id]
	return row, ok
}

func (s *stubHealthStore) commit(ctx context.Context, audit ports.HealthMutationAudit, required bool, transitions []ports.HealthTransition, tenant domain.TenantID, id domain.ProviderAccountID, row stubHealthRow) (ports.HealthTransition, error) {
	if required && audit == nil {
		return ports.HealthTransition{}, ports.ErrRequiredHealthAudit
	}
	row.Health.SummaryState = stubWorst(row.Health.Conditions)
	result := ports.AccountHealth{Health: row.Health, RecoveryPermit: row.Permit}
	for i := range transitions {
		transitions[i].Result = result
	}
	if audit != nil {
		if err := audit(ctx, transitions); err != nil {
			return ports.HealthTransition{}, err
		}
	}
	s.put(tenant, id, row)
	last := transitions[len(transitions)-1]
	last.Result = result
	return last, nil
}

func (s *stubHealthStore) Initialize(ctx context.Context, init ports.HealthInitialize) (ports.AccountHealth, error) {
	s.initCalls.Add(1)
	if s.initErr != nil {
		return ports.AccountHealth{}, s.initErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.get(init.Principal.TenantID, init.AccountID); ok {
		return ports.AccountHealth{}, ports.ErrAccountUpdateConflict
	}
	row := stubHealthRow{Health: init.Health}
	tr, err := s.commit(ctx, init.Audit, true, []ports.HealthTransition{{
		NewCondition: findCondition(init.Health, domain.HealthScope{Kind: domain.HealthScopeAccount}),
		Scope:        domain.HealthScope{Kind: domain.HealthScopeAccount},
		Outcome:      "health_initialize",
	}}, init.Principal.TenantID, init.AccountID, row)
	if err != nil {
		return ports.AccountHealth{}, err
	}
	return tr.Result, nil
}

func (s *stubHealthStore) ObserveCooldown(ctx context.Context, obs ports.CooldownObservation) (ports.HealthTransition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if obs.Audit == nil {
		return ports.HealthTransition{}, ports.ErrRequiredHealthAudit
	}
	row, ok := s.get(obs.Principal.TenantID, obs.AccountID)
	if !ok {
		return ports.HealthTransition{}, ports.ErrHealthNotFound
	}
	priorPermit := row.Permit
	if obs.ConsumePermit.Owner != "" {
		if row.Permit.Owner != obs.ConsumePermit.Owner || !rowHasCondition(row, obs.ConsumePermit) {
			return ports.HealthTransition{}, ports.ErrAccountUpdateConflict
		}
	}

	var batch []ports.HealthTransition
	// Derive both transitions under the same lock, then one batch audit before put.
	if obs.ConsumePermit.Owner != "" && !permitMatchesScope(obs.ConsumePermit.Scope, obs.Scope) && obs.ClaimedScopeReason != "" {
		priorClaimed := findCondition(row.Health, obs.ConsumePermit.Scope)
		claimedSource := obs.ClaimedScopeSourceClass
		if claimedSource == "" {
			claimedSource = domain.HealthSourceRecoveryProbe
		}
		proj := domain.ProviderAccount{Health: row.Health, Credential: domain.CredentialMetadata{Version: obs.CredentialVersion}}
		proj = proj.WithScopedCooldownSource(obs.ObservedAt, obs.ConsumePermit.Scope, obs.ClaimedScopeReason, obs.ClaimedScopeRetryNotBefore, obs.CredentialVersion, claimedSource)
		row.Health = proj.Health
		row.Permit = domain.RecoveryPermit{}
		claimedNew := findCondition(row.Health, obs.ConsumePermit.Scope)
		batch = append(batch, ports.HealthTransition{
			PriorCondition: priorClaimed, NewCondition: claimedNew, PriorPermit: priorPermit,
			Scope: claimedNew.Scope, Outcome: "cooldown_renew",
		})
		priorPermit = domain.RecoveryPermit{}
	}
	prior := findCondition(row.Health, obs.Scope)
	source := obs.SourceClass
	if source == "" {
		source = domain.HealthSourceUpstreamAttempt
	}
	proj := domain.ProviderAccount{Health: row.Health, Credential: domain.CredentialMetadata{Version: obs.CredentialVersion}}
	proj = proj.WithScopedCooldownSource(obs.ObservedAt, obs.Scope, obs.Reason, obs.RetryNotBefore, obs.CredentialVersion, source)
	row.Health = proj.Health
	if obs.ConsumePermit.Owner != "" {
		row.Permit = domain.RecoveryPermit{}
	}
	newCond := findCondition(row.Health, obs.Scope)
	outcome := "cooldown_create"
	if prior.ConditionRevision > 0 {
		outcome = "cooldown_renew"
	}
	batch = append(batch, ports.HealthTransition{
		PriorCondition: prior, NewCondition: newCond, PriorPermit: priorPermit, NewPermit: row.Permit,
		Scope: newCond.Scope, Outcome: outcome,
	})
	return s.commit(ctx, obs.Audit, true, batch, obs.Principal.TenantID, obs.AccountID, row)
}

func (s *stubHealthStore) ClaimRecoveryPermit(ctx context.Context, claim ports.RecoveryPermitClaim) (ports.ClaimResult, error) {
	// Signal and wait outside the mutex so disable can progress under its own lock.
	if s.claimEntered != nil {
		select {
		case s.claimEntered <- struct{}{}:
		default:
		}
	}
	if s.claimRelease != nil {
		<-s.claimRelease
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.forceClaimConflict {
		return ports.ClaimResult{}, ports.ErrAccountUpdateConflict
	}
	row, ok := s.get(claim.Principal.TenantID, claim.AccountID)
	if !ok {
		return ports.ClaimResult{}, ports.ErrHealthNotFound
	}
	if row.Permit.Owner != "" {
		return ports.ClaimResult{}, ports.ErrAccountUpdateConflict
	}
	permit := domain.RecoveryPermit{
		Owner: claim.Owner, Scope: claim.Scope,
		ConditionRevision: claim.ConditionRevision, CredentialVersion: claim.CredentialVersion,
	}
	if !rowHasCondition(row, permit) {
		return ports.ClaimResult{}, ports.ErrAccountUpdateConflict
	}
	prior := row.Permit
	row.Permit = permit
	tr, err := s.commit(ctx, claim.Audit, false, []ports.HealthTransition{{
		PriorPermit: prior, NewPermit: permit, Scope: permit.Scope, Outcome: "recovery_permit_claim",
		PriorCondition: findCondition(row.Health, permit.Scope), NewCondition: findCondition(row.Health, permit.Scope),
	}}, claim.Principal.TenantID, claim.AccountID, row)
	if err != nil {
		return ports.ClaimResult{}, err
	}
	return ports.ClaimResult{Permit: permit, Result: tr.Result}, nil
}

func (s *stubHealthStore) RenewAfterDependencyFailure(ctx context.Context, renew ports.DependencyFailureRenewal) (ports.HealthTransition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.get(renew.Principal.TenantID, renew.AccountID)
	if !ok {
		return ports.HealthTransition{}, ports.ErrHealthNotFound
	}
	if row.Permit.Owner != renew.Permit.Owner || !rowHasCondition(row, renew.Permit) {
		return ports.HealthTransition{}, ports.ErrAccountUpdateConflict
	}
	prior := findCondition(row.Health, renew.Permit.Scope)
	reason := prior.Reason
	if reason == "" {
		reason = domain.HealthReasonProviderRateLimited
	}
	priorPermit := row.Permit
	proj := domain.ProviderAccount{Health: row.Health, Credential: domain.CredentialMetadata{Version: renew.Permit.CredentialVersion}}
	proj = proj.WithScopedCooldownSource(renew.ObservedAt, renew.Permit.Scope, reason, renew.RetryNotBefore, renew.Permit.CredentialVersion, domain.HealthSourceRecoveryProbe)
	row.Health = proj.Health
	row.Permit = domain.RecoveryPermit{}
	return s.commit(ctx, renew.Audit, true, []ports.HealthTransition{{
		PriorCondition: prior, NewCondition: findCondition(row.Health, renew.Permit.Scope),
		PriorPermit: priorPermit, Scope: renew.Permit.Scope, Outcome: "dependency_failure_renewal",
	}}, renew.Principal.TenantID, renew.AccountID, row)
}

func (s *stubHealthStore) ResolveRecovery(ctx context.Context, resolution ports.RecoveryResolution) (ports.HealthTransition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resolveErr != nil {
		return ports.HealthTransition{}, s.resolveErr
	}
	row, ok := s.get(resolution.Principal.TenantID, resolution.AccountID)
	if !ok {
		return ports.HealthTransition{}, ports.ErrHealthNotFound
	}
	if row.Permit.Owner != resolution.Permit.Owner || !rowHasCondition(row, resolution.Permit) {
		return ports.HealthTransition{}, ports.ErrAccountUpdateConflict
	}
	prior := findCondition(row.Health, resolution.Permit.Scope)
	priorPermit := row.Permit
	proj := domain.ProviderAccount{Health: row.Health, RecoveryPermit: row.Permit, Credential: domain.CredentialMetadata{Version: resolution.Permit.CredentialVersion}}
	proj = proj.WithScopedRecovery(resolution.ObservedAt, resolution.Permit)
	row.Health = proj.Health
	row.Permit = domain.RecoveryPermit{}
	return s.commit(ctx, resolution.Audit, true, []ports.HealthTransition{{
		PriorCondition: prior, NewCondition: findCondition(row.Health, resolution.Permit.Scope),
		PriorPermit: priorPermit, Scope: resolution.Permit.Scope, Outcome: "recovery_success",
	}}, resolution.Principal.TenantID, resolution.AccountID, row)
}

func (s *stubHealthStore) RecordHardFailure(ctx context.Context, obs ports.HardFailureObservation) (ports.HealthTransition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.get(obs.Principal.TenantID, obs.AccountID)
	if !ok {
		return ports.HealthTransition{}, ports.ErrHealthNotFound
	}
	if obs.ConsumePermit.Owner != "" {
		if row.Permit.Owner != obs.ConsumePermit.Owner ||
			!permitMatchesScope(row.Permit.Scope, obs.ConsumePermit.Scope) ||
			row.Permit.ConditionRevision != obs.ConsumePermit.ConditionRevision ||
			row.Permit.CredentialVersion != obs.ConsumePermit.CredentialVersion ||
			!rowHasCondition(row, obs.ConsumePermit) {
			return ports.HealthTransition{}, ports.ErrAccountUpdateConflict
		}
	}
	priorPermit := row.Permit
	prior := findCondition(row.Health, domain.HealthScope{Kind: domain.HealthScopeAccount})
	row.Permit = domain.RecoveryPermit{}
	if obs.PendingOnly {
		kept := make([]domain.HealthCondition, 0, len(row.Health.Conditions)+1)
		priorPending := domain.HealthCondition{}
		for _, c := range row.Health.Conditions {
			if c.CredentialVersion == obs.CredentialVersion {
				if c.Scope.Kind == domain.HealthScopeAccount {
					priorPending = c
				}
				continue
			}
			kept = append(kept, c)
		}
		if priorPending.State == "" {
			priorPending = prior
		}
		rejected := domain.HealthCondition{
			Scope: domain.HealthScope{Kind: domain.HealthScopeAccount}, State: domain.HealthExpired,
			Reason: domain.HealthReasonCredentialRejected, CredentialVersion: obs.CredentialVersion,
			ObservedAt: obs.ObservedAt, Remediation: domain.RemediationReauthenticate,
			ConditionRevision: 1, SourceClass: domain.HealthSourceRequiredProbe,
		}
		if priorPending.ConditionRevision > 0 {
			rejected.ConditionRevision = priorPending.ConditionRevision + 1
		}
		kept = append(kept, rejected)
		row.Health.Conditions = kept
		return s.commit(ctx, obs.Audit, true, []ports.HealthTransition{{
			PriorCondition: priorPending, NewCondition: rejected, PriorPermit: priorPermit,
			Scope: rejected.Scope, Outcome: "pending_hard_rejection",
		}}, obs.Principal.TenantID, obs.AccountID, row)
	}
	proj := domain.ProviderAccount{Health: row.Health, Credential: domain.CredentialMetadata{Version: obs.CredentialVersion}}
	proj = proj.WithCredentialRejected(obs.ObservedAt)
	row.Health = proj.Health
	return s.commit(ctx, obs.Audit, true, []ports.HealthTransition{{
		PriorCondition: prior, NewCondition: findCondition(row.Health, domain.HealthScope{Kind: domain.HealthScopeAccount}),
		PriorPermit: priorPermit, Scope: domain.HealthScope{Kind: domain.HealthScopeAccount}, Outcome: "hard_auth_rejection",
	}}, obs.Principal.TenantID, obs.AccountID, row)
}

func (s *stubHealthStore) ResetForEnableProbe(ctx context.Context, reset ports.EnableProbeReset) (ports.HealthTransition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.get(reset.Principal.TenantID, reset.AccountID)
	if !ok {
		return ports.HealthTransition{}, ports.ErrHealthNotFound
	}
	priorPermit := row.Permit
	prior := findCondition(row.Health, domain.HealthScope{Kind: domain.HealthScopeAccount})
	kept := make([]domain.HealthCondition, 0, len(row.Health.Conditions)+1)
	for _, c := range row.Health.Conditions {
		if c.Scope.Kind == domain.HealthScopeAccount {
			continue
		}
		kept = append(kept, c)
	}
	accountUnknown := domain.HealthCondition{
		Scope: domain.HealthScope{Kind: domain.HealthScopeAccount}, State: domain.HealthUnknown,
		Reason: domain.HealthReasonInitialUnprobed, CredentialVersion: reset.CredentialVersion,
		ObservedAt: reset.ObservedAt, Remediation: domain.RemediationNone,
		SourceClass: domain.HealthSourceRequiredProbe,
	}
	kept = append(kept, accountUnknown)
	row.Health.Conditions = kept
	row.Permit = domain.RecoveryPermit{}
	transition, err := s.commit(ctx, reset.Audit, true, []ports.HealthTransition{{
		PriorCondition: prior, NewCondition: accountUnknown, PriorPermit: priorPermit,
		Scope: accountUnknown.Scope, Outcome: "enable_probe_reset",
	}}, reset.Principal.TenantID, reset.AccountID, row)
	if err == nil && s.enableResetHook != nil {
		s.enableResetHook()
	}
	return transition, err
}

func (s *stubHealthStore) ClearPermit(ctx context.Context, clear ports.PermitClear) (ports.AccountHealth, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Optional race injection: replace durable permit before CAS so request-owned
	// cleanup can prove it fails closed against a newer owner.
	if s.swapPermitBeforeClear != nil {
		row, ok := s.get(clear.Principal.TenantID, clear.AccountID)
		if ok {
			row.Permit = *s.swapPermitBeforeClear
			s.put(clear.Principal.TenantID, clear.AccountID, row)
		}
	}
	row, ok := s.get(clear.Principal.TenantID, clear.AccountID)
	if !ok {
		return ports.AccountHealth{}, ports.ErrHealthNotFound
	}
	prior := row.Permit
	if prior.Owner == "" {
		return ports.AccountHealth{Health: row.Health, RecoveryPermit: row.Permit}, nil
	}
	// Empty ExpectedPermit = administrative unconditional clear (management disable).
	// Non-empty = request-owned CAS fence (owner+scope+revision+credential version).
	if clear.ExpectedPermit.Owner != "" {
		if prior.Owner != clear.ExpectedPermit.Owner ||
			!permitMatchesScope(prior.Scope, clear.ExpectedPermit.Scope) ||
			prior.ConditionRevision != clear.ExpectedPermit.ConditionRevision ||
			prior.CredentialVersion != clear.ExpectedPermit.CredentialVersion {
			return ports.AccountHealth{}, ports.ErrAccountUpdateConflict
		}
	}
	row.Permit = domain.RecoveryPermit{}
	tr, err := s.commit(ctx, clear.Audit, true, []ports.HealthTransition{{
		PriorPermit: prior, Scope: prior.Scope, Outcome: "permit_clear",
	}}, clear.Principal.TenantID, clear.AccountID, row)
	if err != nil {
		return ports.AccountHealth{}, err
	}
	return tr.Result, nil
}

func (s *stubHealthStore) RecordActivation(ctx context.Context, act ports.ActivationHealth) (ports.HealthTransition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.get(act.Principal.TenantID, act.AccountID)
	if !ok {
		return ports.HealthTransition{}, ports.ErrHealthNotFound
	}
	prior := findCondition(row.Health, domain.HealthScope{Kind: domain.HealthScopeAccount})
	healthy := domain.HealthCondition{
		Scope: domain.HealthScope{Kind: domain.HealthScopeAccount}, State: domain.HealthHealthy,
		Reason: domain.HealthReasonProbeSucceeded, CredentialVersion: act.CredentialVersion,
		ObservedAt: act.ObservedAt, Remediation: domain.RemediationNone,
		SourceClass: domain.HealthSourceRequiredProbe,
	}
	conditions := make([]domain.HealthCondition, 0, len(row.Health.Conditions)+1)
	replaced := false
	for _, c := range row.Health.Conditions {
		if c.Scope.Kind == domain.HealthScopeAccount {
			if !replaced {
				conditions = append(conditions, healthy)
				replaced = true
			}
			continue
		}
		conditions = append(conditions, c)
	}
	if !replaced {
		conditions = append(conditions, healthy)
	}
	row.Health.Conditions = conditions
	return s.commit(ctx, act.Audit, true, []ports.HealthTransition{{
		PriorCondition: prior, NewCondition: healthy, Scope: healthy.Scope, Outcome: "activation",
	}}, act.Principal.TenantID, act.AccountID, row)
}

func (s *stubHealthStore) ResetForCredentialEpoch(ctx context.Context, reset ports.CredentialEpochReset) (ports.HealthTransition, error) {
	if s.epochErr != nil {
		return ports.HealthTransition{}, s.epochErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.get(reset.Principal.TenantID, reset.AccountID)
	if !ok {
		return ports.HealthTransition{}, ports.ErrHealthNotFound
	}
	priorPermit := row.Permit
	prior := findCondition(row.Health, domain.HealthScope{Kind: domain.HealthScopeAccount})
	row.Permit = domain.RecoveryPermit{}
	if reset.PreserveCredentialVersion > 0 {
		kept := make([]domain.HealthCondition, 0, len(row.Health.Conditions))
		for _, c := range row.Health.Conditions {
			if c.CredentialVersion == reset.PreserveCredentialVersion {
				kept = append(kept, c)
			}
		}
		row.Health.Conditions = kept
		return s.commit(ctx, reset.Audit, true, []ports.HealthTransition{{
			PriorCondition: prior, NewCondition: findCondition(row.Health, domain.HealthScope{Kind: domain.HealthScopeAccount}),
			PriorPermit: priorPermit, Scope: domain.HealthScope{Kind: domain.HealthScopeAccount},
			Outcome: "credential_epoch_preserve",
		}}, reset.Principal.TenantID, reset.AccountID, row)
	}
	unknown := domain.HealthCondition{
		Scope: domain.HealthScope{Kind: domain.HealthScopeAccount}, State: domain.HealthUnknown,
		Reason: domain.HealthReasonInitialUnprobed, CredentialVersion: reset.NewCredentialVersion,
		ObservedAt: reset.ObservedAt, Remediation: domain.RemediationNone,
		SourceClass: domain.HealthSourceRequiredProbe,
	}
	row.Health.Conditions = []domain.HealthCondition{unknown}
	return s.commit(ctx, reset.Audit, true, []ports.HealthTransition{{
		PriorCondition: prior, NewCondition: unknown, PriorPermit: priorPermit,
		Scope: unknown.Scope, Outcome: "credential_epoch_reset",
	}}, reset.Principal.TenantID, reset.AccountID, row)
}

func findCondition(summary domain.HealthSummary, scope domain.HealthScope) domain.HealthCondition {
	for _, c := range summary.Conditions {
		if permitMatchesScope(c.Scope, scope) {
			return c
		}
	}
	return domain.HealthCondition{}
}

func permitMatchesScope(a, b domain.HealthScope) bool {
	return a.Kind == b.Kind && a.Operation == b.Operation && a.ModelSlug == b.ModelSlug
}

func rowHasCondition(row stubHealthRow, required domain.RecoveryPermit) bool {
	for _, c := range row.Health.Conditions {
		if permitMatchesScope(c.Scope, required.Scope) &&
			c.ConditionRevision == required.ConditionRevision &&
			c.CredentialVersion == required.CredentialVersion {
			return true
		}
	}
	return false
}

func stubWorst(conditions []domain.HealthCondition) domain.HealthState {
	if len(conditions) == 0 {
		return domain.HealthUnknown
	}
	order := map[domain.HealthState]int{
		domain.HealthHealthy: 1, domain.HealthUnknown: 2, domain.HealthDegraded: 3,
		domain.HealthCoolingDown: 4, domain.HealthExpired: 5, domain.HealthChallenged: 6, domain.HealthBlocked: 7,
	}
	worst := conditions[0].State
	for _, c := range conditions[1:] {
		if order[c.State] > order[worst] {
			worst = c.State
		}
	}
	return worst
}

var _ ports.HealthStore = (*stubHealthStore)(nil)
