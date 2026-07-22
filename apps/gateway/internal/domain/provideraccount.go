package domain

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
	PendingOrigin       LifecycleState
	PendingOriginHealth HealthSummary
	Health              HealthSummary
	Controls            AdministrativeControls
	CreatedAt           Timestamp
	UpdatedAt           Timestamp
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

// HealthCondition is one scoped operational condition. CredentialVersion is the
// business lifecycle version the observation is fenced to; a draft that has
// never stored a credential uses zero.
type HealthCondition struct {
	Scope             HealthScope
	State             HealthState
	Reason            HealthReason
	CredentialVersion int
	ObservedAt        Timestamp
	Remediation       Remediation
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
	return mode == AuthModeGrokWebSSO
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
		account.PendingOriginHealth = account.Health
		if account.Lifecycle != LifecycleDisabled {
			account.Lifecycle = LifecyclePendingValidation
		}
	} else {
		account.Credential.Version = nextVersion
		account.PendingOrigin = LifecycleDraft
		account.Lifecycle = LifecyclePendingValidation
	}
	account.Credential.ExpiresAt = expiresAt
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
// succeeds for the current credential version. It advances to `pending_probe`
// and records last_validated_at. Validation success alone never activates the
// account; a required probe must still succeed (connection lifecycle spec §4.5,
// I-NO-ACTIVE-ON-FAIL).
func (account ProviderAccount) WithValidatedCredential(now Timestamp) ProviderAccount {
	if account.PendingCredentialVersion == 0 || account.PendingOrigin != LifecycleDisabled {
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
	account.UpdatedAt = now
	return account
}

// WithProbeActivated returns the account after validation plus a required probe
// succeed for the current version. It transitions to `active`, records
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
	account.PendingOriginHealth = HealthSummary{}
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

// WithPendingCredentialRejected discards the pending version and restores the
// origin lifecycle. First-connect failures become reauth_required; planned
// active/disabled replacements do not demote the prior usable/admin state.
func (account ProviderAccount) WithPendingCredentialRejected(now Timestamp) ProviderAccount {
	origin := account.PendingOrigin
	originHealth := account.PendingOriginHealth
	account.PendingCredentialVersion = 0
	account.PendingOrigin = ""
	account.PendingOriginHealth = HealthSummary{}
	switch origin {
	case LifecycleActive, LifecycleDisabled:
		account.Lifecycle = origin
		account.Health = originHealth
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
