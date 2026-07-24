// Package ports owns application-facing outbound Gateway contracts.
package ports

import (
	"context"
	"errors"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// ErrHealthNotFound reports that a visible account has no durable health row.
// The application maps this to dependency_unavailable so product traffic fails
// closed rather than synthesizing healthy state (I-COOLDOWN-DURABLE).
var ErrHealthNotFound = errors.New("provider account health not found")

// AccountHealth is the durable health projection for one Provider Account.
// It is composed with AccountStore lifecycle metadata at application read/use
// boundaries to form the public ProviderAccount wire projection (ADR 0009).
type AccountHealth struct {
	Health         domain.HealthSummary
	RecoveryPermit domain.RecoveryPermit
}

// HealthTransition is the exact prior/new evidence for one logical mutation.
// The HealthStore derives this under its exclusive lock before persistence.
// Application must not invent planned transitions for audit.
type HealthTransition struct {
	Result         AccountHealth
	PriorCondition domain.HealthCondition
	NewCondition   domain.HealthCondition
	PriorPermit    domain.RecoveryPermit
	NewPermit      domain.RecoveryPermit
	Scope          domain.HealthScope
	Outcome        string
}

// HealthMutationAudit is invoked exactly once under the HealthStore exclusive
// lock after the final durable row and all exact transitions for this mutation
// are derived, and BEFORE any append/persist. The slice has one or more exact
// HealthTransition values (e.g. claimed-scope renew + fresh-scope create).
// Returning an error aborts the write with no durable change and no partial
// store-boundary success (health/cooldown spec §19.8).
//
// Implementations should treat the batch as atomic at the HealthStore boundary:
// either accept the full batch or fail without side effects visible as partial
// health-transition audits from this mutation.
type HealthMutationAudit func(context.Context, []HealthTransition) error

// ErrRequiredHealthAudit reports that a required mutation audit callback was nil.
var ErrRequiredHealthAudit = errors.New("health mutation requires audit callback")

// HealthInitialize seeds the first durable health row for a newly created account.
type HealthInitialize struct {
	Principal domain.SecurityPrincipal
	AccountID domain.ProviderAccountID
	Health    domain.HealthSummary
	// Audit is required for durable init. Invoked once under lock before append.
	Audit HealthMutationAudit
}

// CooldownObservation creates or renews one scoped cooldown under CAS merge.
// The store reloads latest state, merges only the named scope, and never
// accepts a caller-computed full HealthSummary as authoritative.
type CooldownObservation struct {
	Principal         domain.SecurityPrincipal
	AccountID         domain.ProviderAccountID
	Scope             domain.HealthScope
	Reason            domain.HealthReason
	CredentialVersion int
	ObservedAt        domain.Timestamp
	// RetryNotBefore is the timer for THIS observation's Scope only.
	RetryNotBefore domain.Timestamp
	SourceClass    domain.HealthSourceClass
	// ConsumePermit, when non-empty, requires and clears that permit (exact
	// owner/scope/revision/version). When the claimed scope differs from Scope,
	// the claimed scope is renewed first using ClaimedScope* fields — never by
	// copying RetryNotBefore from the fresh scope.
	ConsumePermit              domain.RecoveryPermit
	ClaimedScopeReason         domain.HealthReason
	ClaimedScopeRetryNotBefore domain.Timestamp
	ClaimedScopeSourceClass    domain.HealthSourceClass
	// Audit is required. Invoked once under lock with the full batch of exact
	// transitions (claimed renew first when present, then fresh-scope) before
	// a single append of the final record.
	Audit HealthMutationAudit
}

// RecoveryPermitClaim requests the single half-open permit for one condition
// revision after timer eligibility.
type RecoveryPermitClaim struct {
	Principal         domain.SecurityPrincipal
	AccountID         domain.ProviderAccountID
	Owner             domain.Identifier
	Scope             domain.HealthScope
	ConditionRevision int
	CredentialVersion int
	// Audit is optional for claim (permit ownership is private fencing). When
	// set, invoked once under lock before append.
	Audit HealthMutationAudit
}

// ClaimResult is the outcome of a successful permit claim.
type ClaimResult struct {
	Permit domain.RecoveryPermit
	Result AccountHealth
}

// DependencyFailureRenewal renews the exact claimed condition revision with
// progressive backoff and clears the permit (recovery-probe provenance).
type DependencyFailureRenewal struct {
	Principal      domain.SecurityPrincipal
	AccountID      domain.ProviderAccountID
	Permit         domain.RecoveryPermit
	ObservedAt     domain.Timestamp
	RetryNotBefore domain.Timestamp
	Audit          HealthMutationAudit
}

// RecoveryResolution clears only the fenced transient condition authorized by
// a successful recovery permit.
type RecoveryResolution struct {
	Principal  domain.SecurityPrincipal
	AccountID  domain.ProviderAccountID
	Permit     domain.RecoveryPermit
	ObservedAt domain.Timestamp
	Audit      HealthMutationAudit
}

// HardFailureObservation records credential-rejected / hard auth evidence.
// When ConsumePermit is set it must match exact owner+scope+revision+version.
//
// PendingOnly rejects only conditions fenced to CredentialVersion (a failed
// pending replacement version), clears the permit, and preserves all other
// version-scoped evidence so origin usability survives. Full first-connect
// rejections leave PendingOnly false and replace the summary with account-scope
// expired/credential_rejected.
type HardFailureObservation struct {
	Principal         domain.SecurityPrincipal
	AccountID         domain.ProviderAccountID
	CredentialVersion int
	ObservedAt        domain.Timestamp
	ConsumePermit     domain.RecoveryPermit
	PendingOnly       bool
	Audit             HealthMutationAudit
}

// EnableProbeReset resets account-scope epoch health for management enable and
// clears any private recovery permit. Unrelated operation/model scopes are
// preserved (I-HEALTH-SCOPED / I-HEALTH-NO-STALE-CLEAR).
type EnableProbeReset struct {
	Principal         domain.SecurityPrincipal
	AccountID         domain.ProviderAccountID
	CredentialVersion int
	ObservedAt        domain.Timestamp
	Audit             HealthMutationAudit
}

// PermitClear drops a private recovery permit without changing conditions.
//
// ExpectedPermit is the CAS fence for request-owned cleanup:
//   - Zero value (empty Owner): administrative unconditional clear
//     (management disable epoch). Any present permit is removed.
//   - Non-zero: clear only when the durable permit exactly matches
//     Owner + Scope + ConditionRevision + CredentialVersion. On mismatch
//     the store returns ErrAccountUpdateConflict and preserves the newer
//     (or different) permit — stale post-claim cleanup must fail closed.
//
// Control mutation requires audit when it changes observable private fencing state.
type PermitClear struct {
	Principal domain.SecurityPrincipal
	AccountID domain.ProviderAccountID
	// ExpectedPermit, when non-zero, requires exact identity match before clear.
	// Empty ExpectedPermit means administrative unconditional clear.
	ExpectedPermit domain.RecoveryPermit
	Audit          HealthMutationAudit
}

// ActivationHealth records first-connect / enable probe success as a healthy
// account-scope condition (required_probe provenance). Unrelated operation and
// model scopes are always preserved; there is no full wipe mode.
// CredentialVersion MUST be the version this probe actually proved (including
// a pending replacement version), never a pre-cutover stale current version.
type ActivationHealth struct {
	Principal         domain.SecurityPrincipal
	AccountID         domain.ProviderAccountID
	CredentialVersion int
	ObservedAt        domain.Timestamp
	Audit             HealthMutationAudit
}

// CredentialEpochReset advances health fencing when a new credential version is
// stored (first-connect or replacement). Required audit runs under lock before
// append. Clear any private recovery permit.
//
// When PreserveCredentialVersion is 0 (first-connect), conditions are replaced
// with account-scope unknown/initial_unprobed fenced to NewCredentialVersion so
// prior draft evidence cannot authorize the new version.
//
// When PreserveCredentialVersion > 0 (replacement), only conditions fenced to
// that still-current usable version are retained; other-version evidence and the
// permit are dropped so origin usability stays truthful without authorizing the
// pending version.
type CredentialEpochReset struct {
	Principal                 domain.SecurityPrincipal
	AccountID                 domain.ProviderAccountID
	NewCredentialVersion      int
	PreserveCredentialVersion int
	ObservedAt                domain.Timestamp
	Audit                     HealthMutationAudit
}

// HealthStore owns scoped Health Conditions, CAS/revision fencing, recovery
// permit claim/renew/resolve, and Restore. Mutations derive exact transitions
// under lock, invoke required Audit before persistence, then append.
type HealthStore interface {
	Restore(context.Context) error
	Read(context.Context, domain.SecurityPrincipal, domain.ProviderAccountID) (AccountHealth, error)
	Initialize(context.Context, HealthInitialize) (AccountHealth, error)
	ObserveCooldown(context.Context, CooldownObservation) (HealthTransition, error)
	ClaimRecoveryPermit(context.Context, RecoveryPermitClaim) (ClaimResult, error)
	RenewAfterDependencyFailure(context.Context, DependencyFailureRenewal) (HealthTransition, error)
	ResolveRecovery(context.Context, RecoveryResolution) (HealthTransition, error)
	RecordHardFailure(context.Context, HardFailureObservation) (HealthTransition, error)
	ResetForEnableProbe(context.Context, EnableProbeReset) (HealthTransition, error)
	ClearPermit(context.Context, PermitClear) (AccountHealth, error)
	RecordActivation(context.Context, ActivationHealth) (HealthTransition, error)
	ResetForCredentialEpoch(context.Context, CredentialEpochReset) (HealthTransition, error)
}
