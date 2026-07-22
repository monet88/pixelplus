package ports

import (
	"context"
	"errors"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// ErrOAuthAuthorizationNotVisible reports that an authorization id is foreign,
// unknown, or not owned by the named account from the principal's perspective.
// It is the single non-enumerating OAuth journey visibility failure.
var ErrOAuthAuthorizationNotVisible = errors.New("oauth authorization not visible")

// OAuthStartCommand authorizes creation of one server-owned OAuth journey for a
// visible Provider Account. The application never supplies or receives token
// material here; only safe flow preference and ownership context travel.
type OAuthStartCommand struct {
	Principal domain.SecurityPrincipal
	AccountID domain.ProviderAccountID
	// AuthorizationID is the server-owned journey identity claimed before Start.
	// The adapter MUST mint the journey under this identity so a single-flight
	// marker can be written before any Provider exchange begins.
	AuthorizationID domain.OAuthAuthorizationID
	AuthMode        domain.AuthMode
	Purpose         domain.OAuthPurpose
	Flow            domain.OAuthFlow
}

// OAuthStartResult is the safe start projection plus the server-owned journey
// identity. VerificationURI and UserCode are user-facing only; device_code,
// authorization codes, tokens, and PKCE verifiers remain inside the adapter.
type OAuthStartResult struct {
	Authorization domain.OAuthAuthorization
}

// OAuthPollCommand authorizes a status poll for one server-owned journey. The
// poll has no vault decrypt purpose (management contract §4.3).
type OAuthPollCommand struct {
	Principal       domain.SecurityPrincipal
	AccountID       domain.ProviderAccountID
	AuthorizationID domain.OAuthAuthorizationID
}

// OAuthPollResult is the safe poll projection. When Status becomes succeeded the
// adapter may expose one-shot ExchangedMaterial for immediate Vault.Put; the
// application must not retain, log, or project that material.
type OAuthPollResult struct {
	Authorization     domain.OAuthAuthorization
	ExchangedMaterial string
}

// OAuthExchangeAdapter owns the server-side OAuth authorization/exchange
// lifecycle. Start creates one journey identity; Poll advances it and, on first
// success, may hand exchanged material once to the application for Vault storage.
// Failures and expiry produce Status=failed with no material. Unavailable state
// MUST fail closed with ErrDependencyUnavailable.
type OAuthExchangeAdapter interface {
	Start(context.Context, OAuthStartCommand) (OAuthStartResult, error)
	Poll(context.Context, OAuthPollCommand) (OAuthPollResult, error)
}
