package application

import (
	"context"
	"errors"
	"unicode/utf8"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// StartOAuthAuthorizationCommand is the typed server-owned OAuth start request.
// Purpose and flow preference are non-secret; exchange material is never part of
// the command.
type StartOAuthAuthorizationCommand struct {
	PresentedKeyMaterial string
	AccountID            domain.ProviderAccountID
	Purpose              domain.OAuthPurpose
	FlowPreference       domain.OAuthFlow
	IdempotencyKey       string
	RequestID            domain.Identifier
	OversizeBody         bool
	MalformedBody        bool
}

// GetOAuthAuthorizationQuery is the typed server-owned OAuth poll request.
type GetOAuthAuthorizationQuery struct {
	PresentedKeyMaterial string
	AccountID            domain.ProviderAccountID
	AuthorizationID      domain.OAuthAuthorizationID
	RequestID            domain.Identifier
}

// OAuthAuthorizationResult carries one safe OAuth journey projection plus the
// server-owned request id.
type OAuthAuthorizationResult struct {
	Authorization domain.OAuthAuthorization
	RequestID     domain.Identifier
}

// StartOAuthAuthorization runs the protected spine for a server-owned OAuth
// start. Connect requires a pure draft OAuth Auth Mode account; risk and mode
// gates run before any exchange adapter call. The durable side effect is one
// claimed journey identity plus the account single-flight marker. Tokens, codes,
// and PKCE secrets never leave the adapter (connection lifecycle §4.3, §4.4;
// management contract §4.3).
func (service *ProviderAccountService) StartOAuthAuthorization(ctx context.Context, command StartOAuthAuthorizationCommand) (OAuthAuthorizationResult, error) {
	sc := spineContext{operation: operationStartOAuthAuthorization, requestID: service.resolveRequestID(command.RequestID), start: service.clock.Now()}

	principal, canonical, ok := service.authenticate(ctx, ports.PresentedClientAPIKey{Material: command.PresentedKeyMaterial})
	if !ok {
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, canonical)
	}
	sc.keyID = principal.ClientAPIKeyID

	if !principal.Scopes.Has(domain.ScopeAccountsManage) {
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, domain.NewForbidden())
	}
	if command.OversizeBody {
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, domain.NewRequestTooLarge())
	}
	if command.MalformedBody {
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}
	if command.IdempotencyKey == "" || utf8.RuneCountInString(command.IdempotencyKey) > maxIdempotencyKeyLength {
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}
	if !command.Purpose.Valid() || !command.FlowPreference.Valid() {
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}

	account, err := service.accounts.Visible(ctx, principal, command.AccountID)
	if err != nil {
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, service.visibilityCanonical(err))
	}
	if canonical, ok := service.oauthStartGate(account, command.Purpose); !ok {
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, canonical)
	}

	identity := domain.ReplayIdentity{
		Scope: domain.ReplayScope{
			TenantID:       principal.TenantID,
			ClientAPIKeyID: principal.ClientAPIKeyID,
			Key:            command.IdempotencyKey,
		},
		Fingerprint: domain.NewStartOAuthAuthorizationFingerprint(account.ID, command.Purpose, command.FlowPreference),
	}
	decision, err := service.replay.Claim(ctx, identity)
	if err != nil {
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}
	switch decision.Outcome {
	case ports.ReplayClaimed:
		// sole executor
	case ports.ReplayTerminal:
		service.recordTelemetry(ctx, sc.operation, "", 202)
		service.recordRequestLog(ctx, sc.requestID, principal.ClientAPIKeyID, string(sc.operation), 202, "ok", sc.start)
		return OAuthAuthorizationResult{Authorization: decision.TerminalOAuth, RequestID: sc.requestID}, nil
	case ports.ReplayInProgress:
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, domain.NewIdempotencyInProgress())
	case ports.ReplayConflict:
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, domain.NewIdempotencyConflict())
	case ports.ReplayUncertain:
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, domain.NewIdempotencyUncertain())
	default:
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, domain.NewInternalError())
	}

	reservation, canonical, ok := service.admit(ctx, principal, operationStartOAuthAuthorization)
	if !ok {
		service.abandon(ctx, identity)
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, canonical)
	}

	started, err := service.oauth.Start(ctx, ports.OAuthStartCommand{
		Principal: principal,
		AccountID: account.ID,
		AuthMode:  account.AuthMode,
		Purpose:   command.Purpose,
		Flow:      command.FlowPreference,
	})
	if err != nil {
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}

	marked := account.WithOAuthJourneyStarted(started.Authorization.ID, domain.NewTimestamp(sc.start))
	if _, err := service.accounts.Update(ctx, ports.AccountUpdate{Principal: principal, Account: marked}); err != nil {
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}

	if err := service.replay.Complete(ctx, identity, ports.ReplayResult{Account: marked, OAuth: started.Authorization}); err != nil {
		service.release(ctx, reservation)
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, domain.NewIdempotencyUncertain())
	}

	service.release(ctx, reservation)
	service.observeSuccess(ctx, sc, ports.AuditProviderOAuthStarted, principal, marked.ID, 202)
	return OAuthAuthorizationResult{Authorization: started.Authorization, RequestID: sc.requestID}, nil
}

// GetOAuthAuthorization runs the protected spine for a server-owned OAuth poll.
// A first successful exchange stores credential material through the Vault and
// lands pending_validation without activating the account. Failed/expired
// journeys store no credential and restore the correct non-usable state. Poll
// responses never include tokens, codes, or PKCE secrets.
func (service *ProviderAccountService) GetOAuthAuthorization(ctx context.Context, query GetOAuthAuthorizationQuery) (OAuthAuthorizationResult, error) {
	sc := spineContext{operation: operationGetOAuthAuthorization, requestID: service.resolveRequestID(query.RequestID), start: service.clock.Now()}

	principal, canonical, ok := service.authenticate(ctx, ports.PresentedClientAPIKey{Material: query.PresentedKeyMaterial})
	if !ok {
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, canonical)
	}
	sc.keyID = principal.ClientAPIKeyID

	if !principal.Scopes.Has(domain.ScopeAccountsManage) {
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, domain.NewForbidden())
	}
	if query.AuthorizationID == "" {
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}

	account, err := service.accounts.Visible(ctx, principal, query.AccountID)
	if err != nil {
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, service.visibilityCanonical(err))
	}

	reservation, canonical, ok := service.admit(ctx, principal, operationGetOAuthAuthorization)
	if !ok {
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, canonical)
	}
	defer service.release(ctx, reservation)

	polled, err := service.oauth.Poll(ctx, ports.OAuthPollCommand{
		Principal:       principal,
		AccountID:       account.ID,
		AuthorizationID: query.AuthorizationID,
	})
	if err != nil {
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, service.oauthVisibilityCanonical(err))
	}

	// A still-pending journey that has passed expires_at is terminal failed: no
	// usable credential is stored and the account returns to the correct
	// non-usable state (connection lifecycle §4.3, management contract §4.3).
	if polled.Authorization.Status == domain.OAuthStatusAuthorizationPending &&
		!polled.Authorization.ExpiresAt.IsZero() &&
		!sc.start.Before(polled.Authorization.ExpiresAt.Time()) {
		polled.Authorization.Status = domain.OAuthStatusFailed
		polled.ExchangedMaterial = ""
	}

	// Apply first terminal side effects for the owning journey only. A poll of a
	// foreign or already-settled journey never mutates account credential state.
	// Concurrent polls are safe: exchanged material is one-shot, and a loser that
	// observes empty material after another poll already advanced the account
	// only clears residual marker state instead of putting a second version.
	if account.ActiveOAuthAuthorizationID == polled.Authorization.ID {
		switch polled.Authorization.Status {
		case domain.OAuthStatusSucceeded:
			switch {
			case polled.ExchangedMaterial != "":
				nextVersion := account.Credential.Version + 1
				if err := service.vault.Put(ctx, ports.CredentialIntake{
					Principal: principal,
					AccountID: account.ID,
					AuthMode:  account.AuthMode,
					Class:     account.AuthMode.RequiredCredentialClass(),
					Version:   nextVersion,
					Material:  polled.ExchangedMaterial,
				}); err != nil {
					return OAuthAuthorizationResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
				}
				// Consume material immediately; never retain on the result projection.
				polled.ExchangedMaterial = ""
				submitted := account.WithSubmittedCredential(domain.NewTimestamp(sc.start), domain.Timestamp{})
				submitted = submitted.WithOAuthJourneyCleared(domain.NewTimestamp(sc.start))
				if _, err := service.accounts.Update(ctx, ports.AccountUpdate{Principal: principal, Account: submitted}); err != nil {
					// Vault already accepted the version. Surface dependency so the
					// operator can recover; do not invent a second put on retry without
					// material. Marker remains until a later consistent poll/update.
					return OAuthAuthorizationResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
				}
			default:
				// Material was one-shot and already consumed. Re-read so a concurrent
				// winner that already landed pending_validation is treated as settled
				// instead of an internal error.
				latest, err := service.accounts.Visible(ctx, principal, account.ID)
				if err != nil {
					return OAuthAuthorizationResult{}, service.fail(ctx, sc, service.visibilityCanonical(err))
				}
				if latest.Credential.Version == 0 {
					return OAuthAuthorizationResult{}, service.fail(ctx, sc, domain.NewInternalError())
				}
				if latest.ActiveOAuthAuthorizationID == polled.Authorization.ID {
					cleared := latest.WithOAuthJourneyCleared(domain.NewTimestamp(sc.start))
					if _, err := service.accounts.Update(ctx, ports.AccountUpdate{Principal: principal, Account: cleared}); err != nil {
						return OAuthAuthorizationResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
					}
				}
			}
		case domain.OAuthStatusFailed:
			var restored domain.ProviderAccount
			if polled.Authorization.Purpose == domain.OAuthPurposeConnect {
				// Failed first connect restores pure draft and keeps version zero.
				restored = account.WithOAuthConnectFailed(domain.NewTimestamp(sc.start))
			} else {
				restored = account.WithCredentialRejected(domain.NewTimestamp(sc.start)).WithOAuthJourneyCleared(domain.NewTimestamp(sc.start))
			}
			if _, err := service.accounts.Update(ctx, ports.AccountUpdate{Principal: principal, Account: restored}); err != nil {
				return OAuthAuthorizationResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
			}
		}
	}

	// Never project exchange material, even if an adapter misbehaves.
	polled.ExchangedMaterial = ""
	if polled.Authorization.Status == domain.OAuthStatusSucceeded {
		polled.Authorization.Remediation = domain.RemediationNone
		polled.Authorization.UserCode = ""
		polled.Authorization.VerificationURI = ""
	}
	if polled.Authorization.Status == domain.OAuthStatusFailed {
		polled.Authorization.Remediation = domain.RemediationCompleteOAuth
		polled.Authorization.UserCode = ""
		polled.Authorization.VerificationURI = ""
	}

	service.observeSuccess(ctx, sc, ports.AuditProviderOAuthPolled, principal, account.ID, 200)
	return OAuthAuthorizationResult{Authorization: polled.Authorization, RequestID: sc.requestID}, nil
}

// oauthStartGate applies the shared usability and purpose gates for an OAuth
// start. It always rejects before the exchange adapter runs.
func (service *ProviderAccountService) oauthStartGate(account domain.ProviderAccount, purpose domain.OAuthPurpose) (domain.CanonicalError, bool) {
	if canonical, ok := service.authModeGate(account); !ok {
		return canonical, false
	}
	if !account.AuthMode.SupportsServerOwnedOAuth() {
		return domain.NewInvalidRequest(), false
	}
	if account.ActiveOAuthAuthorizationID != "" {
		return domain.NewAccountNotUsable(domain.RemediationCompleteOAuth), false
	}
	if !account.Lifecycle.AcceptsOAuthStart(purpose) {
		return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
	}
	return domain.CanonicalError{}, true
}

// oauthVisibilityCanonical maps an OAuth adapter not-found outcome to the
// single non-enumerating resource_not_found, or a fail-closed dependency error.
func (service *ProviderAccountService) oauthVisibilityCanonical(err error) domain.CanonicalError {
	if errors.Is(err, ports.ErrOAuthAuthorizationNotVisible) {
		return domain.NewResourceNotFound()
	}
	return service.dependencyCanonical(err)
}
