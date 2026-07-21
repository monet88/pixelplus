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
	Health     HealthSummary
	Controls   AdministrativeControls
	CreatedAt  Timestamp
	UpdatedAt  Timestamp
}

// CredentialMetadata is the safe, non-secret credential projection. It never
// carries a handle, ciphertext, token, cookie, or OAuth exchange material.
type CredentialMetadata struct {
	// Version is the business credential lifecycle version. It is absent (zero)
	// for a draft that has never stored a credential.
	Version int
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
