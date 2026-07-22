package domain

import "time"

// OAuthAuthorizationID is the server-owned OAuth journey identity. Values match
// the frozen Public API pattern ^oauth_[A-Za-z0-9_]+$.
type OAuthAuthorizationID string

// OAuthPurpose names why a server-owned OAuth journey was started. Values mirror
// the frozen OAuthStartRequest.purpose enum.
type OAuthPurpose string

const (
	OAuthPurposeConnect        OAuthPurpose = "connect"
	OAuthPurposeReauthenticate OAuthPurpose = "reauthenticate"
)

// Valid reports whether the purpose is a known enum value.
func (purpose OAuthPurpose) Valid() bool {
	switch purpose {
	case OAuthPurposeConnect, OAuthPurposeReauthenticate:
		return true
	default:
		return false
	}
}

// OAuthFlow names the user-facing authorization channel. Values mirror the
// frozen flow / flow_preference enums.
type OAuthFlow string

const (
	OAuthFlowBrowser OAuthFlow = "browser"
	OAuthFlowDevice  OAuthFlow = "device"
)

// Valid reports whether the flow is a known enum value.
func (flow OAuthFlow) Valid() bool {
	switch flow {
	case OAuthFlowBrowser, OAuthFlowDevice:
		return true
	default:
		return false
	}
}

// OAuthStatus is the safe journey status. Values mirror the frozen
// OAuthAuthorization.status enum.
type OAuthStatus string

const (
	OAuthStatusAuthorizationPending OAuthStatus = "authorization_pending"
	OAuthStatusSucceeded            OAuthStatus = "succeeded"
	OAuthStatusFailed               OAuthStatus = "failed"
)

// Valid reports whether the status is a known enum value.
func (status OAuthStatus) Valid() bool {
	switch status {
	case OAuthStatusAuthorizationPending, OAuthStatusSucceeded, OAuthStatusFailed:
		return true
	default:
		return false
	}
}

// Terminal reports whether the journey can no longer advance.
func (status OAuthStatus) Terminal() bool {
	return status == OAuthStatusSucceeded || status == OAuthStatusFailed
}

// OAuthAuthorization is the safe server-owned OAuth journey projection. It never
// carries access tokens, refresh tokens, authorization codes, device_code, or
// PKCE verifier material (management contract §4.3).
type OAuthAuthorization struct {
	ID                OAuthAuthorizationID
	ProviderAccountID ProviderAccountID
	Purpose           OAuthPurpose
	Flow              OAuthFlow
	Status            OAuthStatus
	VerificationURI   string
	UserCode          string
	ExpiresAt         Timestamp
	Remediation       Remediation
}

// SupportsServerOwnedOAuth reports whether the Auth Mode uses an OAuth/CLI
// credential class that may start a server-owned OAuth journey. Web Access modes
// and prohibited modes must not enter this path (I-NO-WEB-OAUTH-MIX).
func (mode AuthMode) SupportsServerOwnedOAuth() bool {
	switch mode {
	case AuthModeChatGPTCodexOAuth, AuthModeGeminiAntigravityOAuth, AuthModeGrokXAIOAuth:
		return true
	default:
		return false
	}
}

// AcceptsOAuthStart reports whether this lifecycle may accept a server-owned
// OAuth start for the given purpose. Connect requires a pure draft; reauth
// restart in this slice is accepted from reauth_required only (dual-version
// active cutover is owned by #48).
func (state LifecycleState) AcceptsOAuthStart(purpose OAuthPurpose) bool {
	switch purpose {
	case OAuthPurposeConnect:
		return state == LifecycleDraft
	case OAuthPurposeReauthenticate:
		return state == LifecycleActive || state == LifecycleDisabled || state == LifecycleReauthRequired || state == LifecycleRevoked
	default:
		return false
	}
}

// WithOAuthJourneyStarted returns the account after a server-owned OAuth journey
// has been claimed. The account remains non-usable; only the private single-
// flight marker advances so a second start cannot orphan the active journey.
func (account ProviderAccount) WithOAuthJourneyStarted(authorizationID OAuthAuthorizationID, now Timestamp) ProviderAccount {
	account.ActiveOAuthAuthorizationID = authorizationID
	account.UpdatedAt = now
	return account
}

// WithOAuthJourneyCleared returns the account after the active OAuth journey
// reached a terminal outcome. The private single-flight marker is cleared so a
// later connect/reauth can start cleanly.
func (account ProviderAccount) WithOAuthJourneyCleared(now Timestamp) ProviderAccount {
	account.ActiveOAuthAuthorizationID = ""
	account.UpdatedAt = now
	return account
}

// WithOAuthReauthenticationFailed clears a failed replacement journey without
// changing the lifecycle or health of the credential that was active before the
// journey started. A failed replacement must never demote an active or disabled
// origin to reauth_required.
func (account ProviderAccount) WithOAuthReauthenticationFailed(now Timestamp) ProviderAccount {
	return account.WithOAuthJourneyCleared(now)
}

// NewOAuthAuthorizationPending builds the safe authorization_pending projection
// returned by a successful start. Device flows may carry user-facing verification
// fields; browser flows may omit user_code.
func NewOAuthAuthorizationPending(
	id OAuthAuthorizationID,
	accountID ProviderAccountID,
	purpose OAuthPurpose,
	flow OAuthFlow,
	verificationURI string,
	userCode string,
	expiresAt Timestamp,
) OAuthAuthorization {
	return OAuthAuthorization{
		ID:                id,
		ProviderAccountID: accountID,
		Purpose:           purpose,
		Flow:              flow,
		Status:            OAuthStatusAuthorizationPending,
		VerificationURI:   verificationURI,
		UserCode:          userCode,
		ExpiresAt:         expiresAt,
		Remediation:       RemediationCompleteOAuth,
	}
}

// DefaultOAuthExpiry returns the controlled journey expiry used when an adapter
// does not supply a tighter window.
func DefaultOAuthExpiry(now time.Time) Timestamp {
	return NewTimestamp(now.Add(15 * time.Minute))
}

// WithOAuthConnectFailed returns the account after a first-connect OAuth journey
// fails or expires. It restores a pure draft shell (no stored credential version)
// and clears the private single-flight marker so a later connect can start cleanly
// (management contract §4.3: connect failure restores draft).
func (account ProviderAccount) WithOAuthConnectFailed(now Timestamp) ProviderAccount {
	restored := NewDraftProviderAccount(account.ID, account.Provider, account.AuthMode, account.Label, now)
	// Preserve create-time identity fields that NewDraft would rewrite.
	restored.CreatedAt = account.CreatedAt
	restored.RiskAcknowledged = account.RiskAcknowledged
	restored.Controls = account.Controls
	restored.ActiveOAuthAuthorizationID = ""
	restored.UpdatedAt = now
	return restored
}
