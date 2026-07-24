package domain

import "time"

const (
	// accountCooldownTransientBase/Max bound rate-limit/backoff progressive waits.
	accountCooldownTransientBase = 30 * time.Second
	accountCooldownTransientMax  = 15 * time.Minute
	// accountCooldownQuotaBase/Max bound entitlement/quota-reset progressive waits.
	accountCooldownQuotaBase = 15 * time.Minute
	accountCooldownQuotaMax  = 24 * time.Hour
)

// Provider names a supported upstream Provider. Values mirror the frozen
// Public API contract enum.
type Provider string

// Supported Provider values.
const (
	ProviderChatGPT Provider = "chatgpt"
	ProviderGemini  Provider = "gemini"
	ProviderGrok    Provider = "grok"
)

// Valid reports whether the Provider is a known enum value.
func (provider Provider) Valid() bool {
	switch provider {
	case ProviderChatGPT, ProviderGemini, ProviderGrok:
		return true
	default:
		return false
	}
}

// AuthMode classifies exactly how a Provider Account authenticates. It is
// immutable after create. Values mirror the frozen Public API contract enum.
type AuthMode string

// Supported Auth Mode values.
const (
	AuthModeChatGPTWebAccess       AuthMode = "chatgpt_web_access"
	AuthModeChatGPTCodexOAuth      AuthMode = "chatgpt_codex_oauth"
	AuthModeGeminiWebCookie        AuthMode = "gemini_web_cookie"
	AuthModeGeminiAntigravityOAuth AuthMode = "gemini_antigravity_oauth"
	AuthModeGrokWebSSO             AuthMode = "grok_web_sso"
	AuthModeGrokXAIOAuth           AuthMode = "grok_xai_oauth"
)

// Valid reports whether the Auth Mode is a known enum value.
func (mode AuthMode) Valid() bool {
	switch mode {
	case AuthModeChatGPTWebAccess,
		AuthModeChatGPTCodexOAuth,
		AuthModeGeminiWebCookie,
		AuthModeGeminiAntigravityOAuth,
		AuthModeGrokWebSSO,
		AuthModeGrokXAIOAuth:
		return true
	default:
		return false
	}
}

// CredentialClass names the class-specific credential set a direct submission
// carries. Values mirror the frozen Public API `CredentialSubmission`
// credential_class enum. Web Access modes submit a `web_session` set; OAuth/CLI
// modes accept an `oauth_token_import` set for the lab direct-submit path.
type CredentialClass string

// Supported credential classes.
const (
	CredentialClassWebSession       CredentialClass = "web_session"
	CredentialClassOAuthTokenImport CredentialClass = "oauth_token_import"
)

// Valid reports whether the credential class is a known enum value.
func (class CredentialClass) Valid() bool {
	switch class {
	case CredentialClassWebSession, CredentialClassOAuthTokenImport:
		return true
	default:
		return false
	}
}

// RequiredCredentialClass returns the single credential class a direct
// submission MUST carry for this Auth Mode. Web Access modes require a
// `web_session` set; OAuth/CLI modes require an `oauth_token_import` set. A
// submission that carries any other class is rejected before the Vault is used
// so Web and OAuth/CLI credential lifecycles never mix on one account
// (connection lifecycle spec §8, I-NO-WEB-OAUTH-MIX).
func (mode AuthMode) RequiredCredentialClass() CredentialClass {
	switch mode {
	case AuthModeChatGPTCodexOAuth, AuthModeGeminiAntigravityOAuth, AuthModeGrokXAIOAuth:
		return CredentialClassOAuthTokenImport
	default:
		return CredentialClassWebSession
	}
}

// RiskStatus is the product risk envelope status of an Auth Mode. Values follow
// the auth-mode risk envelope §2/§5 vocabulary. It is server-owned policy and
// never crosses the Public API wire.
type RiskStatus string

// Auth Mode risk statuses.
const (
	RiskAllowed      RiskStatus = "allowed"
	RiskGated        RiskStatus = "gated"
	RiskExperimental RiskStatus = "experimental"
	RiskProhibited   RiskStatus = "prohibited"
)

// RiskStatus classifies the Auth Mode against the product risk envelope. The
// mapping mirrors auth-mode-risk-envelope-and-kill-criteria.md §5: no mode is
// currently `allowed`; the OAuth/CLI modes are `gated`, the consumer Web modes
// are `experimental`, and Grok Web SSO is `prohibited`. An unknown mode fails
// closed to `prohibited` so it can never be connected.
func (mode AuthMode) RiskStatus() RiskStatus {
	switch mode {
	case AuthModeChatGPTCodexOAuth, AuthModeGeminiAntigravityOAuth, AuthModeGrokXAIOAuth:
		return RiskGated
	case AuthModeChatGPTWebAccess, AuthModeGeminiWebCookie:
		return RiskExperimental
	default:
		return RiskProhibited
	}
}

// RequiresRiskAck reports whether the Auth Mode requires an explicit Tenant
// residual-risk acknowledgement before a stored credential may become usable.
// Every `gated` and `experimental` mode requires it (risk envelope §6.1); a
// `prohibited` mode is never connectable so the question does not arise, and no
// mode is `allowed` in the current revision.
func (mode AuthMode) RequiresRiskAck() bool {
	switch mode.RiskStatus() {
	case RiskGated, RiskExperimental:
		return true
	default:
		return false
	}
}

// LifecycleState is the durable Provider Account connection state. Values
// mirror the frozen Public API contract enum.
type LifecycleState string

// Provider Account lifecycle states.
const (
	LifecycleDraft             LifecycleState = "draft"
	LifecyclePendingValidation LifecycleState = "pending_validation"
	LifecyclePendingProbe      LifecycleState = "pending_probe"
	LifecycleActive            LifecycleState = "active"
	LifecycleReauthRequired    LifecycleState = "reauth_required"
	LifecycleDisabled          LifecycleState = "disabled"
	LifecycleRevoked           LifecycleState = "revoked"
	LifecycleDeleted           LifecycleState = "deleted"
)

// HealthState is the canonical operational Health State of an account.
type HealthState string

// Health states. A freshly created draft is observationally unknown.
const (
	HealthUnknown     HealthState = "unknown"
	HealthHealthy     HealthState = "healthy"
	HealthDegraded    HealthState = "degraded"
	HealthCoolingDown HealthState = "cooling_down"
	HealthChallenged  HealthState = "challenged"
	HealthExpired     HealthState = "expired"
	HealthBlocked     HealthState = "blocked"
)

// DrainState is an administrative selection-blocking control.
type DrainState string

// Drain states.
const (
	DrainOff      DrainState = "off"
	DrainDraining DrainState = "draining"
)

// QuarantineState is an administrative isolation control.
type QuarantineState string

// Quarantine states.
const (
	QuarantineOff         QuarantineState = "off"
	QuarantineQuarantined QuarantineState = "quarantined"
)

// ProviderAccount is the owning-Tenant safe projection of a Provider Account.
// It never carries tenant_id on the wire or Provider Credential material; the
// vaulted secret lifecycle is a separate concern. Only non-secret metadata and
// observable state cross this boundary (#6, #9).
type ProviderAccount struct {
	ID         ProviderAccountID
	Provider   Provider
	AuthMode   AuthMode
	Label      string
	Lifecycle  LifecycleState
	Credential CredentialMetadata
	// PendingCredentialVersion is a monotonic version stored for validation and
	// probe. It is never an execution source until promotion succeeds.
	PendingCredentialVersion int
	// PendingOrigin preserves the lifecycle state that must survive a failed
	// replacement or a successful replacement of a disabled account.
	PendingOrigin LifecycleState
	Health        HealthSummary
	Controls      AdministrativeControls
	CreatedAt     Timestamp
	UpdatedAt     Timestamp
	// RiskAcknowledged records whether the owning Tenant accepted the residual
	// risk themes a `gated`/`experimental` Auth Mode requires (risk envelope
	// §6.1). It is server-owned account state, never projected to the Public API
	// wire (the frozen ProviderAccount schema has no such field and forbids
	// additional properties). A stored credential MUST NOT become usable while
	// this is false for a mode that RequiresRiskAck.
	RiskAcknowledged bool
	// ActiveOAuthAuthorizationID is the private single-flight marker for the
	// server-owned OAuth journey currently in flight on this account. It is never
	// projected on the Public API ProviderAccount wire (frozen schema has no such
	// field). An empty value means no OAuth journey is active.
	ActiveOAuthAuthorizationID OAuthAuthorizationID
	// RecoveryPermit is the private durable ownership marker for one half-open
	// scoped recovery attempt. It binds the owner to the exact condition revision
	// and credential version observed at claim time and is never projected on the
	// Public API wire. Only the owning request may settle it.
	RecoveryPermit RecoveryPermit
}

// RecoveryPermit binds one half-open recovery owner to a scoped health condition
// revision. The zero value means no recovery is in flight.
type RecoveryPermit struct {
	Owner             Identifier
	Scope             HealthScope
	ConditionRevision int
	CredentialVersion int
}

// MatchesScope reports whether evidence names the exact normalized bucket bound
// to this permit. Unknown or malformed evidence normalizes account-wide.
func (permit RecoveryPermit) MatchesScope(scope HealthScope) bool {
	return sameScope(permit.Scope, normalizeCooldownScope(scope))
}

// CredentialMetadata is the safe, non-secret credential projection. It never
// carries a handle, ciphertext, token, cookie, or OAuth exchange material.
type CredentialMetadata struct {
	// Version is the business credential lifecycle version. It is absent (zero)
	// for a draft that has never stored a credential.
	Version int
	// LastAllocatedVersion is the monotonic business counter. It is internal and
	// never projected on the Public API.
	LastAllocatedVersion int
	// RefreshSupported is derived from the Auth Mode credential class.
	RefreshSupported bool
	ExpiresAt        Timestamp
	LastValidatedAt  Timestamp
	LastProbedAt     Timestamp
}

// HealthReason is the canonical observed cause of a health condition. Values
// mirror the frozen Public API `HealthReason` enum.
type HealthReason string

// Health reasons used by the request spine. A fresh draft is always
// initial_unprobed.
const (
	HealthReasonInitialUnprobed HealthReason = "initial_unprobed"
	HealthReasonProbeSucceeded  HealthReason = "probe_succeeded"
	// HealthReasonCredentialRejected marks an auth-class probe or validation
	// failure: the stored credential was rejected by the Auth Mode surface, so
	// the account is not usable until reauthentication (connection lifecycle
	// spec §4.6 rule 5, §5.3 Example B). Values mirror the frozen HealthReason
	// enum.
	HealthReasonCredentialRejected HealthReason = "credential_rejected"
	// HealthReasonSuccessWindow marks bounded current success evidence that
	// satisfies recovery policy without an explicit probe (§2 reason table).
	HealthReasonSuccessWindow HealthReason = "success_window"
	// HealthReasonElevatedErrorRate marks a transient error ratio/consecutive
	// threshold exceeded below the circuit-open threshold (typical `degraded`).
	HealthReasonElevatedErrorRate HealthReason = "elevated_error_rate"
	// HealthReasonUpstreamUnavailable marks a Provider/surface unavailable
	// signal without auth/challenge/ban evidence (`degraded` or `cooling_down`).
	HealthReasonUpstreamUnavailable HealthReason = "upstream_unavailable"
	// HealthReasonUpstreamTimeout marks bounded upstream timeout evidence; retry
	// safety still comes from #12/#14/#16 (`degraded` or `cooling_down`).
	HealthReasonUpstreamTimeout HealthReason = "upstream_timeout"
	// HealthReasonProviderRateLimited marks a Provider rate-limit/backoff signal
	// after admission; it creates or renews a `cooling_down` cooldown (§6 rule 1).
	HealthReasonProviderRateLimited HealthReason = "provider_rate_limited"
	// HealthReasonProviderQuotaExhausted marks a Provider entitlement/quota reset
	// signal after admission; it creates or renews a `cooling_down` cooldown.
	HealthReasonProviderQuotaExhausted HealthReason = "provider_quota_exhausted"
	// HealthReasonChallengeDetected marks a bot/challenge/interstitial class
	// detection; it transitions to the hard `challenged` state (§2 reason table).
	HealthReasonChallengeDetected HealthReason = "challenge_detected"
	// HealthReasonCredentialExpired marks a credential that reached known expiry
	// or that the Provider reported expired; it transitions to `expired`.
	HealthReasonCredentialExpired HealthReason = "credential_expired"
	// HealthReasonProtocolDrift marks a Provider protocol that no longer matches
	// the Adapter contract; affected capability may become invalid (§2 reason
	// table; `degraded` or `blocked`).
	HealthReasonProtocolDrift HealthReason = "protocol_drift"
	// HealthReasonProviderAccountBanned marks permanent ban/provider revocation
	// evidence for the account; it transitions to the hard `blocked` state.
	HealthReasonProviderAccountBanned HealthReason = "provider_account_banned"
	// HealthReasonRecoveryProbeFailed marks a probe that failed without a more
	// specific canonical classification; its resulting state is deterministic
	// from the prior effective state (§2.1 transition table).
	HealthReasonRecoveryProbeFailed HealthReason = "recovery_probe_failed"
)

// HealthScopeKind names the breadth of a health condition. Values mirror the
// frozen Public API `HealthScope.kind` enum.
type HealthScopeKind string

// Health scope kinds.
const (
	HealthScopeAccount   HealthScopeKind = "account"
	HealthScopeOperation HealthScopeKind = "operation"
	HealthScopeModel     HealthScopeKind = "model"
)

// HealthScope binds a condition to an account, operation, or model. Operation
// and ModelSlug are only set for the narrower scopes.
type HealthScope struct {
	Kind      HealthScopeKind
	Operation string
	ModelSlug string
}

// HealthSummary is a safe scoped projection of operational health. The summary
// never erases narrower operation/model conditions and carries no raw Provider
// response.
type HealthSummary struct {
	SummaryState HealthState
	Conditions   []HealthCondition
}

// HealthSourceClass is the bounded provenance class of a health observation. It
// records how the evidence was obtained so audit and stale-write fencing can
// reason about it; it never leaks onto the safe wire projection (§3 rule 3
// source_class).
type HealthSourceClass string

// Bounded health evidence source classes (§3 rule 3).
const (
	HealthSourceRequiredProbe          HealthSourceClass = "required_probe"
	HealthSourceRecoveryProbe          HealthSourceClass = "recovery_probe"
	HealthSourceUpstreamAttempt        HealthSourceClass = "upstream_attempt"
	HealthSourceProviderResetHint      HealthSourceClass = "provider_reset_hint"
	HealthSourceOperatorClassification HealthSourceClass = "operator_classification"
	HealthSourceAggregateCircuit       HealthSourceClass = "aggregate_circuit"
)

// HealthCondition is one scoped operational condition. CredentialVersion is the
// business lifecycle version the observation is fenced to; a draft that has
// never stored a credential uses zero.
//
// ConditionRevision, BackoffLevel, RetryNotBefore, and SourceClass are internal
// fencing/recovery fields (§3 rule 3, §4 rule 7). They drive compare-and-swap
// stale-write rejection, progressive cooldown, half-open timing, and audit
// provenance. They are deliberately NOT projected onto the safe wire schema:
// the frozen HealthCondition exposes only scope/state/reason/credential_version/
// observed_at/remediation plus the derived retry_after_seconds.
type HealthCondition struct {
	Scope             HealthScope
	State             HealthState
	Reason            HealthReason
	CredentialVersion int
	ObservedAt        Timestamp
	Remediation       Remediation

	// ConditionRevision is the monotonic fencing version for concurrent updates.
	// A success may only resolve a condition whose revision it is authorized to
	// verify; last-write-wins by completion time is forbidden (§3 rule 3, §4).
	ConditionRevision int
	// BackoffLevel is the monotonic bounded escalation level for a progressive
	// cooldown. A repeated matching rate/quota failure increments it (§7 rule 10).
	BackoffLevel int
	// RetryNotBefore is the earliest half-open eligibility time for a waitable
	// condition. It is not proof of health: timer expiry only opens one bounded
	// recovery permit (§3 retry_not_before, §7 rule 7).
	RetryNotBefore Timestamp
	// SourceClass records how the evidence was obtained (§3 rule 3). It is bounded
	// provenance for audit/fencing and never appears on the wire.
	SourceClass HealthSourceClass
}

// AdministrativeControls are separate from lifecycle and health. Health success
// never bypasses them.
type AdministrativeControls struct {
	Drain                    DrainState
	Quarantine               QuarantineState
	AuthModeExecutionEnabled bool
}

// NewDraftProviderAccount builds the canonical draft shell produced by create.
// The account starts in `draft` with unknown/initial_unprobed health and never
// carries a stored credential version. It is never active on create (#9 section
// 4.1). ID and timestamps are supplied by the server-owned ID and Clock ports.
func NewDraftProviderAccount(id ProviderAccountID, provider Provider, mode AuthMode, label string, now Timestamp) ProviderAccount {
	return ProviderAccount{
		ID:        id,
		Provider:  provider,
		AuthMode:  mode,
		Label:     label,
		Lifecycle: LifecycleDraft,
		Credential: CredentialMetadata{
			// Refresh support is derived from the Auth Mode credential class.
			// OAuth/CLI modes support silent refresh; Web modes do not.
			RefreshSupported: mode.SupportsRefresh(),
		},
		Health: HealthSummary{
			SummaryState: HealthUnknown,
			Conditions: []HealthCondition{
				{
					Scope:             HealthScope{Kind: HealthScopeAccount},
					State:             HealthUnknown,
					Reason:            HealthReasonInitialUnprobed,
					CredentialVersion: 0,
					ObservedAt:        now,
					Remediation:       RemediationSubmitCredential,
				},
			},
		},
		Controls: AdministrativeControls{
			Drain:                    DrainOff,
			Quarantine:               QuarantineOff,
			AuthModeExecutionEnabled: true,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// SupportsRefresh reports whether the Auth Mode credential class supports
// system-initiated silent refresh (typically OAuth refresh tokens). Web access
// modes require Tenant re-entry and therefore do not.
func (mode AuthMode) SupportsRefresh() bool {
	switch mode {
	case AuthModeChatGPTCodexOAuth, AuthModeGeminiAntigravityOAuth, AuthModeGrokXAIOAuth:
		return true
	default:
		return false
	}
}

// Prohibited reports whether the Auth Mode is outside the product risk envelope
// and must never be offered or persisted. Per the Auth Mode risk envelope
// (#7 sections 4 and 5.5), Grok Web SSO is the sole `prohibited` mode: the xAI
// AUP explicitly forbids bot/script access, so a create for it fails closed
// before any durable side effect. Gated/experimental modes are a separate
// feature-flag + Tenant-acknowledgement concern (#7/#9) and are not gated here.
func (mode AuthMode) Prohibited() bool {
	return mode.RiskStatus() == RiskProhibited
}

// Experimental reports whether the Auth Mode is experimental under the product
// risk envelope. Production has no lab-profile signal, so routing policy
// candidates and durable mode lists fail closed for experimental modes.
func (mode AuthMode) Experimental() bool {
	return mode.RiskStatus() == RiskExperimental
}

// AcceptsCredentialSubmission reports whether a direct credential submission is
// allowed from this lifecycle state. The connection lifecycle spec §4.4 accepts
// a first submit from `draft` and a reauth submit from `reauth_required` or
// `revoked`. All other states (already `pending_*`, `active`, `disabled`, or
// `deleted`) reject the direct-submit path in this slice; rotate-while-active,
// enable, and disable/revoke transitions are owned by later tickets.
func (state LifecycleState) AcceptsCredentialSubmission() bool {
	switch state {
	case LifecycleDraft, LifecycleReauthRequired, LifecycleRevoked:
		return true
	default:
		return false
	}
}

// AcceptsDisable reports whether a management disable is allowed from this
// lifecycle state. A pure `draft` shell has no usable credentialed connection to
// pause, so disable is rejected with a stable class rather than a silent no-op;
// a `deleted` id is not visible. A `revoked` account is excluded because the
// transition matrix marks its disable column `—` and recovery is reauth with new
// material, never enable: allowing `revoked -> disabled -> enable` would let a
// revoked credential re-enter the probe/activation ceremony, violating
// I-REVOKE-NONUSE. Disable on an already-`disabled` account is idempotent, and
// every other credentialed state (`pending_*`, `active`, `reauth_required`) may
// be disabled (connection lifecycle spec §4.10 rule 5, §4.11, §4.13 matrix,
// management contract §4.5).
func (state LifecycleState) AcceptsDisable() bool {
	switch state {
	case LifecyclePendingValidation, LifecyclePendingProbe, LifecycleActive, LifecycleReauthRequired, LifecycleDisabled:
		return true
	default:
		return false
	}
}

// AcceptsEnable reports whether a management enable is allowed from this
// lifecycle state. Enable is the disabled-to-probe recovery path, so it is
// accepted only from `disabled`; every other state has no disable intent to
// clear (management contract §4.5, I-ACCOUNT-ENABLE-PROBED).
func (state LifecycleState) AcceptsEnable() bool {
	return state == LifecycleDisabled
}

// AcceptsProbe reports whether a controlled probe is allowed from this state. A
// probe proves credential usability, so it is allowed once a credential has
// been submitted and validation is pending (`pending_validation`), a prior
// validation succeeded (`pending_probe`), or an already-`active` account is
// re-probed (connection lifecycle spec §4.6). Draft, disabled, revoked,
// reauth_required, and deleted reject before any Vault use or Adapter call.
func (state LifecycleState) AcceptsProbe() bool {
	switch state {
	case LifecyclePendingValidation, LifecyclePendingProbe, LifecycleActive:
		return true
	default:
		return false
	}
}

// WithSubmittedCredential returns the account after a successful direct
// credential submission. It advances to `pending_validation`, bumps the
// credential version, records the stored expiry hint, and refreshes the account
// health condition to unknown/initial_unprobed for the new version. The
// submission never activates the account (connection lifecycle spec §4.4 rule 5,
// frozen 202 lands pending_validation with credential.version 1). It carries no
// secret material; the version and expiry hint are the only safe projections.
func (account ProviderAccount) WithSubmittedCredential(now Timestamp, expiresAt Timestamp) ProviderAccount {
	return account.withPendingCredential(now, expiresAt, false)
}

// WithReplacementCredential stages a new credential version without changing
// the version authorized for new work. The account remains observable as a
// pending replacement (or disabled while a disabled-origin replacement is
// validated).
func (account ProviderAccount) WithReplacementCredential(now Timestamp, expiresAt Timestamp) ProviderAccount {
	return account.withPendingCredential(now, expiresAt, true)
}

func (account ProviderAccount) withPendingCredential(now Timestamp, expiresAt Timestamp, replacement bool) ProviderAccount {
	nextVersion := account.Credential.LastAllocatedVersion
	if nextVersion < account.Credential.Version {
		nextVersion = account.Credential.Version
	}
	nextVersion++
	account.Credential.LastAllocatedVersion = nextVersion
	if replacement {
		account.PendingCredentialVersion = nextVersion
		account.PendingOrigin = account.Lifecycle
		if account.Lifecycle != LifecycleDisabled {
			account.Lifecycle = LifecyclePendingValidation
		}
	} else {
		account.Credential.Version = nextVersion
		account.PendingOrigin = LifecycleDraft
		account.Lifecycle = LifecyclePendingValidation
	}
	account.Credential.ExpiresAt = expiresAt
	account.RecoveryPermit = RecoveryPermit{}
	account.Health = HealthSummary{
		SummaryState: HealthUnknown,
		Conditions: []HealthCondition{{
			Scope:             HealthScope{Kind: HealthScopeAccount},
			State:             HealthUnknown,
			Reason:            HealthReasonInitialUnprobed,
			CredentialVersion: account.Credential.Version,
			ObservedAt:        now,
			Remediation:       RemediationNone,
		}},
	}
	account.UpdatedAt = now
	return account
}

// WithValidatedCredential returns the account after required validation
// succeeds for the current credential version. First-connect and replacement
// flows advance to `pending_probe`; validation during an already-active recovery
// preserves `active` because a dependency failure after validation is not an
// authoritative lifecycle transition. Validation success alone never activates a
// non-active account; a required probe must still succeed (connection lifecycle
// spec §4.5, I-NO-ACTIVE-ON-FAIL).
func (account ProviderAccount) WithValidatedCredential(now Timestamp) ProviderAccount {
	if account.Lifecycle != LifecycleActive &&
		(account.PendingCredentialVersion == 0 || account.PendingOrigin != LifecycleDisabled) {
		account.Lifecycle = LifecyclePendingProbe
	}
	account.Credential.LastValidatedAt = now
	account.UpdatedAt = now
	return account
}

// WithCredentialRejected returns the account after required validation or an
// auth-class probe fails for the current version. It moves to `reauth_required`
// with health expired/credential_rejected and remediation reauthenticate, and
// never activates (connection lifecycle spec §4.6 rule 5, §5.3 B). The credential
// version is preserved so the Tenant knows which version was rejected.
func (account ProviderAccount) WithCredentialRejected(now Timestamp) ProviderAccount {
	account.Lifecycle = LifecycleReauthRequired
	account.Health = HealthSummary{
		SummaryState: HealthExpired,
		Conditions: []HealthCondition{{
			Scope:             HealthScope{Kind: HealthScopeAccount},
			State:             HealthExpired,
			Reason:            HealthReasonCredentialRejected,
			CredentialVersion: account.Credential.Version,
			ObservedAt:        now,
			Remediation:       RemediationReauthenticate,
		}},
	}
	account.RecoveryPermit = RecoveryPermit{}
	account.UpdatedAt = now
	return account
}

// WithProbeActivated returns the account after validation plus a required probe
// succeeds for the current version. It transitions to `active`, records
// last_probed_at, and sets health healthy/probe_succeeded. This is the only
// transition into `active` in this slice, and it is reached only after every
// usability gate has already passed (connection lifecycle spec §4.7, §5.1
// I-USABLE-GATE).
func (account ProviderAccount) WithProbeActivated(now Timestamp) ProviderAccount {
	return account.withProbeActivated(now, false)
}

// WithReplacementProbeActivated promotes the pending credential version. A
// disabled-origin account remains disabled; all other origins become active.
func (account ProviderAccount) WithReplacementProbeActivated(now Timestamp) ProviderAccount {
	return account.withProbeActivated(now, true)
}

func (account ProviderAccount) withProbeActivated(now Timestamp, replacement bool) ProviderAccount {
	if replacement && account.PendingOrigin == LifecycleDisabled {
		account.Lifecycle = LifecycleDisabled
	} else {
		account.Lifecycle = LifecycleActive
	}
	if account.PendingCredentialVersion > 0 {
		account.Credential.Version = account.PendingCredentialVersion
		account.PendingCredentialVersion = 0
	}
	account.PendingOrigin = ""
	account.RecoveryPermit = RecoveryPermit{}
	account.Credential.LastProbedAt = now
	account.Health = HealthSummary{
		SummaryState: HealthHealthy,
		Conditions: []HealthCondition{{
			Scope:             HealthScope{Kind: HealthScopeAccount},
			State:             HealthHealthy,
			Reason:            HealthReasonProbeSucceeded,
			CredentialVersion: account.Credential.Version,
			ObservedAt:        now,
			Remediation:       RemediationNone,
		}},
	}
	account.UpdatedAt = now
	return account
}

// normalizeCooldownScope widens an unknown, malformed, or under-specified scope
// to account scope. A rate/quota signal narrows to operation/model only when the
// bounded upstream evidence actually proved that bucket; a missing operation, a
// model without an operation, or any unrecognized kind defaults to account scope
// so an unknown bucket never invents a narrower condition (§3.4, §6.3,
// I-HEALTH-SCOPED).
func normalizeCooldownScope(scope HealthScope) HealthScope {
	switch scope.Kind {
	case HealthScopeOperation:
		if scope.Operation == "" {
			return HealthScope{Kind: HealthScopeAccount}
		}
		return HealthScope{Kind: HealthScopeOperation, Operation: scope.Operation}
	case HealthScopeModel:
		// Model scope is valid only with an operation because the same model slug
		// may have different semantics across operations (§3.4).
		if scope.Operation == "" || scope.ModelSlug == "" {
			return HealthScope{Kind: HealthScopeAccount}
		}
		return HealthScope{Kind: HealthScopeModel, Operation: scope.Operation, ModelSlug: scope.ModelSlug}
	default:
		return HealthScope{Kind: HealthScopeAccount}
	}
}

// sameScope reports whether two scopes name the exact same account/operation/
// model bucket.
func sameScope(a, b HealthScope) bool {
	return a.Kind == b.Kind && a.Operation == b.Operation && a.ModelSlug == b.ModelSlug
}

// conditionSeverity ranks a Health State for availability precedence (§3.8):
// blocked > expired > challenged > cooling_down > degraded > healthy > unknown.
func conditionSeverity(state HealthState) int {
	switch state {
	case HealthBlocked:
		return 6
	case HealthExpired:
		return 5
	case HealthChallenged:
		return 4
	case HealthCoolingDown:
		return 3
	case HealthDegraded:
		return 2
	case HealthHealthy:
		return 1
	default:
		return 0
	}
}

// worstConditionState computes the summary state as the most severe state among
// all conditions by the §3.8 precedence ordering. The summary never erases the
// narrower conditions; it only reports the worst matching scope so a model-only
// cooldown is never flattened into an account-wide failure (§18 rule 4).
func worstConditionState(conditions []HealthCondition) HealthState {
	worst := HealthUnknown
	worstRank := conditionSeverity(HealthUnknown)
	for _, condition := range conditions {
		if rank := conditionSeverity(condition.State); rank > worstRank {
			worst = condition.State
			worstRank = rank
		}
	}
	return worst
}

// ProjectEffectiveHealthSummary recomputes SummaryState from conditions fenced
// to the current usable credential version while retaining the full conditions
// list (including historical other-version evidence for audit/projection).
//
// Durable HealthStore may store SummaryState as worst-across-versions; the
// Public API / management summary is the most severe *effective* condition for
// the current credential (§2, §2.6, I-HEALTH-CURRENT-VERSION). Draft accounts
// with Credential.Version 0 use only version-0 conditions (initial_unprobed).
func ProjectEffectiveHealthSummary(credentialVersion int, health HealthSummary) HealthSummary {
	current := make([]HealthCondition, 0, len(health.Conditions))
	for _, condition := range health.Conditions {
		if condition.CredentialVersion == credentialVersion {
			current = append(current, condition)
		}
	}
	if len(current) == 0 {
		// Historical-only evidence must not invent a hard current summary.
		health.SummaryState = HealthUnknown
		return health
	}
	health.SummaryState = worstConditionState(current)
	return health
}

// WithEffectiveHealthProjection returns a copy whose Health.SummaryState is
// derived only from current credential-version conditions. Conditions are
// preserved in full.
func (account ProviderAccount) WithEffectiveHealthProjection() ProviderAccount {
	account.Health = ProjectEffectiveHealthSummary(account.Credential.Version, account.Health)
	return account
}

// NextCooldownFence returns the normalized scope plus the condition revision and
// bounded backoff level a newly observed cooldown will receive. Policy timing can
// therefore be computed before WithScopedCooldown persists the same fence.
func (account ProviderAccount) NextCooldownFence(scope HealthScope) (HealthScope, int, int) {
	normalized := normalizeCooldownScope(scope)
	for _, condition := range account.Health.Conditions {
		if !sameScope(condition.Scope, normalized) {
			continue
		}
		backoffLevel := 1
		if condition.State == HealthCoolingDown {
			backoffLevel = condition.BackoffLevel + 1
		}
		return normalized, condition.ConditionRevision + 1, backoffLevel
	}
	return normalized, 1, 1
}

// WithScopedCooldown creates or renews a durable cooling_down Scoped Health
// Condition from a validated Provider rate/quota signal. The scope is normalized
// to the narrowest proven bucket (unknown → account, §6.2-§6.3). A matching
// existing condition is renewed in place: a repeated cooldown increments the
// bounded backoff_level, while a first cooldown replacing a non-cooldown state
// at that scope starts backoff at 1. condition_revision is monotonic and always
// advances so a stale or out-of-scope success cannot clear this newer failure
// (§4 rule 7, §7 rule 10). A never-seen scope appends a fresh condition, leaving
// the account-scope healthy evidence intact so unaffected operations remain
// routable (Example A). The summary state is recomputed from precedence. Cooldown
// never changes lifecycle: auth was proven, so an active account stays active
// with a scoped overlay (§20 I-HEALTH-ORTHOGONAL).
func (account ProviderAccount) WithScopedCooldown(now Timestamp, scope HealthScope, reason HealthReason, retryNotBefore Timestamp) ProviderAccount {
	return account.WithScopedCooldownSource(now, scope, reason, retryNotBefore, account.Credential.Version, HealthSourceUpstreamAttempt)
}

// WithScopedCooldownSource creates or renews a cooling_down condition with an
// explicit source class and credential version fence. Dependency-failure
// renewal during a recovery probe MUST use HealthSourceRecoveryProbe rather
// than fabricating HealthSourceUpstreamAttempt (§11, §19).
func (account ProviderAccount) WithScopedCooldownSource(
	now Timestamp,
	scope HealthScope,
	reason HealthReason,
	retryNotBefore Timestamp,
	credentialVersion int,
	source HealthSourceClass,
) ProviderAccount {
	normalized := normalizeCooldownScope(scope)
	if source == "" {
		source = HealthSourceUpstreamAttempt
	}
	conditions := make([]HealthCondition, len(account.Health.Conditions))
	copy(conditions, account.Health.Conditions)

	matched := false
	for i := range conditions {
		if !sameScope(conditions[i].Scope, normalized) {
			continue
		}
		// Merge only same credential-version fencing (version coexistence).
		if conditions[i].CredentialVersion != credentialVersion {
			continue
		}
		if conditions[i].State == HealthCoolingDown {
			conditions[i].BackoffLevel = boundedBackoffLevel(reason, conditions[i].BackoffLevel+1)
		} else {
			conditions[i].BackoffLevel = 1
		}
		conditions[i].State = HealthCoolingDown
		conditions[i].Reason = reason
		conditions[i].CredentialVersion = credentialVersion
		conditions[i].ObservedAt = now
		conditions[i].Remediation = RemediationWaitProviderCooldown
		conditions[i].ConditionRevision++
		conditions[i].RetryNotBefore = retryNotBefore
		conditions[i].SourceClass = source
		matched = true
		break
	}
	if !matched {
		conditions = append(conditions, HealthCondition{
			Scope:             normalized,
			State:             HealthCoolingDown,
			Reason:            reason,
			CredentialVersion: credentialVersion,
			ObservedAt:        now,
			Remediation:       RemediationWaitProviderCooldown,
			ConditionRevision: 1,
			BackoffLevel:      1,
			RetryNotBefore:    retryNotBefore,
			SourceClass:       source,
		})
	}
	account.Health.Conditions = conditions
	account.Health.SummaryState = worstConditionState(conditions)
	if sameScope(account.RecoveryPermit.Scope, normalized) {
		account.RecoveryPermit = RecoveryPermit{}
	}
	account.UpdatedAt = now
	return account
}

// isTransientHealthState reports whether a state may be resolved by an authorized
// scoped recovery success. Only the soft transient states qualify: hard
// observations (`blocked`, `expired`, `challenged`) dominate a concurrent
// transient success and remain until their authorized recovery path completes
// (§4 rule 6, §11). `unknown`/`healthy` carry nothing to resolve.
func isTransientHealthState(state HealthState) bool {
	return state == HealthCoolingDown || state == HealthDegraded
}

// RecoveryPermitDecision reports whether the requested scope carries a cooldown
// and whether its half-open time makes the single permit claimable now.
type RecoveryPermitDecision struct {
	Permit   RecoveryPermit
	Cooling  bool
	Eligible bool
	// Occupied is true when a half-open permit already owns this cooling
	// condition. Pre-claim Occupied and post-claim CAS conflict both map to the
	// same stable operator-facing 409 recovery_permit_occupied outcome.
	Occupied          bool
	RetryAfterSeconds int
}

// ScopedRecoveryPermit applies the pre-attempt cooldown hierarchy before it
// considers half-open ownership. A broader account/operation condition covers a
// narrower request and must block it; only an exact-scope cooldown may grant the
// single recovery permit for its own revision. An already-occupied permit for a
// covering exact cooling condition is reported as Occupied (not Eligible).
func (account ProviderAccount) ScopedRecoveryPermit(now Timestamp, scope HealthScope, owner Identifier) RecoveryPermitDecision {
	normalized := normalizeCooldownScope(scope)
	var exact *HealthCondition
	for index := range account.Health.Conditions {
		condition := &account.Health.Conditions[index]
		// I-HEALTH-CURRENT-VERSION: only the usable credential version may
		// authorize or block recovery for selection/probe routing.
		if condition.CredentialVersion != account.Credential.Version {
			continue
		}
		if condition.State != HealthCoolingDown || !cooldownScopeCovers(condition.Scope, normalized) {
			continue
		}
		if !sameScope(condition.Scope, normalized) {
			return blockedRecoveryDecision(now, *condition)
		}
		exact = condition
	}
	if exact == nil {
		return RecoveryPermitDecision{}
	}

	decision := RecoveryPermitDecision{Cooling: true}
	if exact.CredentialVersion != account.Credential.Version {
		return decision
	}
	// Occupied permit is recognized before timer eligibility so a dependency-
	// failure residual owner is not mistaken for a timer-open half-open slot.
	if account.RecoveryPermit.Owner != "" &&
		sameScope(account.RecoveryPermit.Scope, exact.Scope) &&
		account.RecoveryPermit.ConditionRevision == exact.ConditionRevision &&
		account.RecoveryPermit.CredentialVersion == exact.CredentialVersion {
		decision.Occupied = true
		return decision
	}
	if !exact.RetryNotBefore.IsZero() && now.Time().Before(exact.RetryNotBefore.Time()) {
		return blockedRecoveryDecision(now, *exact)
	}
	decision.Permit = RecoveryPermit{
		Owner:             owner,
		Scope:             normalized,
		ConditionRevision: exact.ConditionRevision,
		CredentialVersion: exact.CredentialVersion,
	}
	decision.Eligible = true
	return decision
}

func cooldownScopeCovers(conditionScope HealthScope, requestScope HealthScope) bool {
	conditionScope = normalizeCooldownScope(conditionScope)
	requestScope = normalizeCooldownScope(requestScope)
	switch conditionScope.Kind {
	case HealthScopeAccount:
		return true
	case HealthScopeOperation:
		return conditionScope.Operation == requestScope.Operation &&
			(requestScope.Kind == HealthScopeOperation || requestScope.Kind == HealthScopeModel)
	case HealthScopeModel:
		return sameScope(conditionScope, requestScope)
	default:
		return false
	}
}

func blockedRecoveryDecision(now Timestamp, condition HealthCondition) RecoveryPermitDecision {
	decision := RecoveryPermitDecision{Cooling: true}
	if condition.RetryNotBefore.IsZero() || !now.Time().Before(condition.RetryNotBefore.Time()) {
		return decision
	}
	wait := condition.RetryNotBefore.Time().Sub(now.Time())
	decision.RetryAfterSeconds = int((wait + time.Second - 1) / time.Second)
	if decision.RetryAfterSeconds < 1 {
		decision.RetryAfterSeconds = 1
	}
	return decision
}

// WithRecoveryPermitClaimed records durable ownership without changing health,
// lifecycle, or public timestamps. Claim persistence is the fence itself.
func (account ProviderAccount) WithRecoveryPermitClaimed(permit RecoveryPermit) ProviderAccount {
	account.RecoveryPermit = permit
	return account
}

// WithScopedRecovery applies an authorized scoped-recovery success from a probe
// that authenticated and surfaced no fresh rate/quota signal. It resolves ONLY
// the transient conditions at the exact recovered scope: an operation/model
// success never clears an account-scope condition, and an account-scope success
// never clears a narrower operation/model condition, because generic identity
// success does not prove a narrower bucket (§4 rules 4-5, §7.9, §11 recovery
// outcomes). Hard observations at the matching scope survive (§4 rule 6). It never
// changes lifecycle: auth was already proven, so an active account stays active
// with the surviving scoped evidence (§20 I-HEALTH-ORTHOGONAL). If resolving
// empties the summary, an account-scope healthy condition is recorded so an active
// account never lands the `active + unknown` defect (§5.9).
func (account ProviderAccount) WithScopedRecovery(now Timestamp, permit RecoveryPermit) ProviderAccount {
	kept := make([]HealthCondition, 0, len(account.Health.Conditions))
	resolved := false
	for _, condition := range account.Health.Conditions {
		if sameScope(condition.Scope, permit.Scope) &&
			isTransientHealthState(condition.State) &&
			condition.ConditionRevision == permit.ConditionRevision &&
			condition.CredentialVersion == permit.CredentialVersion {
			resolved = true
			continue
		}
		kept = append(kept, condition)
	}
	account.RecoveryPermit = RecoveryPermit{}
	if !resolved {
		return account
	}
	if len(kept) == 0 {
		kept = append(kept, HealthCondition{
			Scope:             HealthScope{Kind: HealthScopeAccount},
			State:             HealthHealthy,
			Reason:            HealthReasonProbeSucceeded,
			CredentialVersion: account.Credential.Version,
			ObservedAt:        now,
			Remediation:       RemediationNone,
		})
	}
	account.Health.Conditions = kept
	account.Health.SummaryState = worstConditionState(kept)
	account.Credential.LastProbedAt = now
	account.UpdatedAt = now
	return account
}

// WithPendingCredentialRejected discards the pending version and restores the
// origin lifecycle. First-connect failures become reauth_required; planned
// active/disabled replacements do not demote the prior usable/admin state.
func (account ProviderAccount) WithPendingCredentialRejected(now Timestamp) ProviderAccount {
	origin := account.PendingOrigin
	account.PendingCredentialVersion = 0
	account.PendingOrigin = ""
	switch origin {
	case LifecycleActive, LifecycleDisabled:
		account.Lifecycle = origin
		account.UpdatedAt = now
		return account
	case LifecycleRevoked:
		account.Lifecycle = LifecycleRevoked
	default:
		account.Lifecycle = LifecycleReauthRequired
	}
	account.Health = HealthSummary{
		SummaryState: HealthExpired,
		Conditions: []HealthCondition{{
			Scope:             HealthScope{Kind: HealthScopeAccount},
			State:             HealthExpired,
			Reason:            HealthReasonCredentialRejected,
			CredentialVersion: account.Credential.Version,
			ObservedAt:        now,
			Remediation:       RemediationReauthenticate,
		}},
	}
	account.UpdatedAt = now
	return account
}

// WithDisabled returns the account after a management disable. It moves to
// `disabled` and preserves credential material and the last truthful health
// evidence: disable blocks new use but never invents an upstream health failure
// or falsely claims a credential failure (connection lifecycle spec §4.10 rules
// 1 and 6, management contract §4.5). When a replacement version is in flight,
// the pending origin is rewritten to `disabled` so a concurrent validation,
// exchange, probe, or replacement completion lands back in `disabled` rather
// than re-activating the account: disable intent wins over the replacement
// journey (management contract §4.6, AC "disable intent wins"). Disable is
// idempotent for an already-`disabled` account.
func (account ProviderAccount) WithDisabled(now Timestamp) ProviderAccount {
	account.Lifecycle = LifecycleDisabled
	account.RecoveryPermit = RecoveryPermit{}
	if account.PendingCredentialVersion > 0 {
		account.PendingOrigin = LifecycleDisabled
	}
	account.UpdatedAt = now
	return account
}

// WithEnableProbePending returns the account after a management enable. Every
// disabled-to-active recovery first enters `pending_probe` and requires a
// separate current-credential-version probe before use; the enable response
// never predicts probe success (management contract §4.5, §4.10 rule 4,
// I-ACCOUNT-ENABLE-PROBED). Health resets to unknown/initial_unprobed for the
// re-probe so a stale healthy summary from before the disable cannot authorize
// new work until the current version is re-proven; the credential version is
// preserved so the probe targets the version the account already stored.
func (account ProviderAccount) WithEnableProbePending(now Timestamp) ProviderAccount {
	account.Lifecycle = LifecyclePendingProbe
	account.RecoveryPermit = RecoveryPermit{}
	account.Health = HealthSummary{
		SummaryState: HealthUnknown,
		Conditions: []HealthCondition{{
			Scope:             HealthScope{Kind: HealthScopeAccount},
			State:             HealthUnknown,
			Reason:            HealthReasonInitialUnprobed,
			CredentialVersion: account.Credential.Version,
			ObservedAt:        now,
			Remediation:       RemediationNone,
		}},
	}
	account.UpdatedAt = now
	return account
}

// WithDeleted returns the account after a management delete. It moves to the
// terminal `deleted` state so the id behaves as not-found for ordinary Public
// API reads (connection lifecycle spec §4.12 rule 4, I-DELETE-TERMINAL). The
// application revokes every current and pending credential version before this
// transition persists so all credentials lose use authority before the account
// disappears; a retention hold may keep encrypted evidence but cannot restore
// retrieval, decrypt, or usability (management contract §3.3, vault spec §6.5,
// §8.5).
func (account ProviderAccount) WithDeleted(now Timestamp) ProviderAccount {
	account.Lifecycle = LifecycleDeleted
	account.RecoveryPermit = RecoveryPermit{}
	account.UpdatedAt = now
	return account
}

// CooldownBaseAndMax returns the exponential policy bounds for a cooldown reason.
func CooldownBaseAndMax(reason HealthReason) (time.Duration, time.Duration) {
	switch reason {
	case HealthReasonProviderQuotaExhausted:
		return accountCooldownQuotaBase, accountCooldownQuotaMax
	default:
		return accountCooldownTransientBase, accountCooldownTransientMax
	}
}

// boundedBackoffLevel caps the backoff escalation level at the point where the
// reason-class duration is already clamped to its maximum. This keeps the
// stored policy value bounded even under repeated failures.
func boundedBackoffLevel(reason HealthReason, level int) int {
	base, maximum := CooldownBaseAndMax(reason)
	maxLevel := maxBackoffLevel(base, maximum)
	if level < 1 {
		level = 1
	}
	if level > maxLevel {
		return maxLevel
	}
	return level
}

func maxBackoffLevel(base, maximum time.Duration) int {
	if base <= 0 || maximum <= 0 || base >= maximum {
		return 1
	}
	level := 1
	duration := base
	for duration < maximum {
		level++
		if duration > maximum/2 {
			duration = maximum
		} else {
			duration *= 2
		}
	}
	return level
}
