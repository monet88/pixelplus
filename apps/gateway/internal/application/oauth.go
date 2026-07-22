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
//
// Replay is claimed before the single-flight usability gate so a lost 202 can
// recover the original authorization_id while the journey is still pending.
// The OAuth marker is claimed atomically before adapter.Start so concurrent
// starts cannot orphan journeys or overwrite a concurrent direct submit.
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

	// Soft gates that do not depend on the single-flight marker run first so a
	// prohibited/disabled/unacked mode still rejects before any claim work.
	if canonical, ok := service.oauthStartSoftGate(account, command.Purpose); !ok {
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, canonical)
	}

	// Replay claim BEFORE the marker gate so a successful start whose 202 was
	// lost can still be recovered from TerminalOAuth while the journey is active.
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

	// Single-flight gate after the claim so a concurrent second start with a
	// different key still rejects with account_not_usable / complete_oauth, while
	// the same-key terminal path above already returned.
	if account.ActiveOAuthAuthorizationID != "" {
		service.abandon(ctx, identity)
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, domain.NewAccountNotUsable(domain.RemediationCompleteOAuth))
	}

	reservation, canonical, ok := service.admit(ctx, principal, operationStartOAuthAuthorization)
	if !ok {
		service.abandon(ctx, identity)
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, canonical)
	}

	// Claim the journey identity and single-flight marker BEFORE the adapter so
	// concurrent starts cannot both call Start, and a concurrent direct submit
	// cannot be overwritten by a full late replace.
	authID, err := service.newOAuthAuthorizationID()
	if err != nil {
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, domain.NewInternalError())
	}
	marked := account.WithOAuthJourneyStarted(authID, domain.NewTimestamp(sc.start))
	claimed, err := service.accounts.Update(ctx, ports.AccountUpdate{
		Principal:               principal,
		Account:                 marked,
		RequireEmptyOAuthMarker: true,
		RequireDraftLifecycle:   command.Purpose == domain.OAuthPurposeConnect,
	})
	if err != nil {
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		if errors.Is(err, ports.ErrAccountUpdateConflict) {
			return OAuthAuthorizationResult{}, service.fail(ctx, sc, domain.NewAccountNotUsable(domain.RemediationCompleteOAuth))
		}
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}

	started, err := service.oauth.Start(ctx, ports.OAuthStartCommand{
		Principal:       principal,
		AccountID:       claimed.ID,
		AuthorizationID: authID,
		AuthMode:        claimed.AuthMode,
		Purpose:         command.Purpose,
		Flow:            command.FlowPreference,
	})
	if err != nil {
		// Best-effort clear the claimed marker so a failed adapter start does not
		// leave the account permanently blocked. A clear failure fails closed.
		cleared := claimed.WithOAuthJourneyCleared(domain.NewTimestamp(service.clock.Now()))
		_, _ = service.accounts.Update(ctx, ports.AccountUpdate{
			Principal:          principal,
			Account:            cleared,
			RequireOAuthMarker: authID,
		})
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}
	if started.Authorization.ID != authID {
		// Adapter must honor the claimed identity; refuse to complete a mismatched
		// journey and clear the marker.
		cleared := claimed.WithOAuthJourneyCleared(domain.NewTimestamp(service.clock.Now()))
		_, _ = service.accounts.Update(ctx, ports.AccountUpdate{
			Principal:          principal,
			Account:            cleared,
			RequireOAuthMarker: authID,
		})
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, domain.NewInternalError())
	}

	if err := service.replay.Complete(ctx, identity, ports.ReplayResult{Account: claimed, OAuth: started.Authorization}); err != nil {
		service.release(ctx, reservation)
		return OAuthAuthorizationResult{}, service.fail(ctx, sc, domain.NewIdempotencyUncertain())
	}

	service.release(ctx, reservation)
	service.observeSuccess(ctx, sc, ports.AuditProviderOAuthStarted, principal, claimed.ID, 202)
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

	// Pending journeys must carry a finite expiry. A zero expiry is an adapter
	// defect: force the journey failed so the single-flight marker cannot stick.
	if polled.Authorization.Status == domain.OAuthStatusAuthorizationPending && polled.Authorization.ExpiresAt.IsZero() {
		polled.Authorization.Status = domain.OAuthStatusFailed
		polled.ExchangedMaterial = ""
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
	// observes empty material while settlement is still in flight gets a retryable
	// dependency outcome instead of a permanent internal error.
	if account.ActiveOAuthAuthorizationID == polled.Authorization.ID {
		switch polled.Authorization.Status {
		case domain.OAuthStatusSucceeded:
			// Re-read before settlement so concurrent polls observe a winner that
			// already advanced the credential version and never put a second one.
			latest, err := service.accounts.Visible(ctx, principal, account.ID)
			if err != nil {
				return OAuthAuthorizationResult{}, service.fail(ctx, sc, service.visibilityCanonical(err))
			}
			switch {
			case latest.Credential.Version > 0 && polled.Authorization.Purpose == domain.OAuthPurposeConnect:
				if latest.ActiveOAuthAuthorizationID == polled.Authorization.ID {
					cleared := latest.WithOAuthJourneyCleared(domain.NewTimestamp(sc.start))
					if _, err := service.accounts.Update(ctx, ports.AccountUpdate{
						Principal:          principal,
						Account:            cleared,
						RequireOAuthMarker: polled.Authorization.ID,
					}); err != nil && !errors.Is(err, ports.ErrAccountUpdateConflict) {
						return OAuthAuthorizationResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
					}
				}
			case polled.ExchangedMaterial != "":
				var pending domain.ProviderAccount
				nextVersion := latest.PendingCredentialVersion
				if polled.Authorization.Purpose == domain.OAuthPurposeReauthenticate && nextVersion == 0 {
					// Fence the allocated version before handing material to the Vault.
					// A retry after Vault success can then reuse this exact pending
					// version instead of allocating another one.
					pending = latest.WithReplacementCredential(domain.NewTimestamp(sc.start), domain.Timestamp{})
					var stageErr error
					pending, stageErr = service.accounts.Update(ctx, ports.AccountUpdate{
						Principal:          principal,
						Account:            pending,
						RequireOAuthMarker: polled.Authorization.ID,
					})
					if stageErr != nil {
						if errors.Is(stageErr, ports.ErrAccountUpdateConflict) {
							return OAuthAuthorizationResult{}, service.fail(ctx, sc, domain.NewDependencyUnavailable())
						}
						return OAuthAuthorizationResult{}, service.fail(ctx, sc, service.dependencyCanonical(stageErr))
					}
					latest = pending
					nextVersion = pending.PendingCredentialVersion
				}
				if nextVersion == 0 {
					nextVersion = latest.Credential.LastAllocatedVersion + 1
					if nextVersion <= latest.Credential.Version {
						nextVersion = latest.Credential.Version + 1
					}
				}
				if err := service.vault.Put(ctx, ports.CredentialIntake{
					Principal: principal,
					AccountID: latest.ID,
					AuthMode:  latest.AuthMode,
					Class:     latest.AuthMode.RequiredCredentialClass(),
					Version:   nextVersion,
					Material:  polled.ExchangedMaterial,
				}); err != nil {
					if polled.Authorization.Purpose == domain.OAuthPurposeReauthenticate && latest.PendingCredentialVersion > 0 {
						restored := latest.WithPendingCredentialRejected(domain.NewTimestamp(sc.start)).WithOAuthJourneyCleared(domain.NewTimestamp(sc.start))
						_, _ = service.accounts.Update(ctx, ports.AccountUpdate{
							Principal:             principal,
							Account:               restored,
							RequireOAuthMarker:    polled.Authorization.ID,
							RequirePendingVersion: latest.PendingCredentialVersion,
						})
					}
					return OAuthAuthorizationResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
				}
				// Consume material immediately; never retain on the result projection.
				polled.ExchangedMaterial = ""
				var submitted domain.ProviderAccount
				if polled.Authorization.Purpose == domain.OAuthPurposeReauthenticate {
					if latest.PendingCredentialVersion == 0 {
						return OAuthAuthorizationResult{}, service.fail(ctx, sc, domain.NewInternalError())
					}
					submitted = latest.WithOAuthJourneyCleared(domain.NewTimestamp(sc.start))
				} else {
					submitted = latest.WithSubmittedCredential(domain.NewTimestamp(sc.start), domain.Timestamp{})
					submitted = submitted.WithOAuthJourneyCleared(domain.NewTimestamp(sc.start))
				}
				if _, err := service.accounts.Update(ctx, ports.AccountUpdate{
					Principal:          principal,
					Account:            submitted,
					RequireOAuthMarker: polled.Authorization.ID,
					RequirePendingVersion: func() int {
						if polled.Authorization.Purpose == domain.OAuthPurposeReauthenticate {
							return nextVersion
						}
						return 0
					}(),
				}); err != nil {
					if errors.Is(err, ports.ErrAccountUpdateConflict) {
						// Another poll already settled this journey.
						settled, visibleErr := service.accounts.Visible(ctx, principal, account.ID)
						if visibleErr != nil {
							return OAuthAuthorizationResult{}, service.fail(ctx, sc, service.visibilityCanonical(visibleErr))
						}
						if settled.Credential.Version > 0 {
							break
						}
					}
					// Vault already accepted the version. Surface a retryable
					// dependency so a later poll can finish settlement without a
					// permanent 500; do not invent a second put without material.
					return OAuthAuthorizationResult{}, service.fail(ctx, sc, domain.NewDependencyUnavailable())
				}
			default:
				// Material was already consumed and settlement has not landed yet.
				// Concurrent polls treat this as in-flight settlement, not internal_error.
				if latest.ActiveOAuthAuthorizationID == polled.Authorization.ID {
					return OAuthAuthorizationResult{}, service.fail(ctx, sc, domain.NewDependencyUnavailable())
				}
				return OAuthAuthorizationResult{}, service.fail(ctx, sc, domain.NewInternalError())
			}
		case domain.OAuthStatusFailed:
			var restored domain.ProviderAccount
			if polled.Authorization.Purpose == domain.OAuthPurposeConnect {
				// Failed first connect restores pure draft and keeps version zero.
				restored = account.WithOAuthConnectFailed(domain.NewTimestamp(sc.start))
			} else {
				restored = account.WithOAuthReauthenticationFailed(domain.NewTimestamp(sc.start))
			}
			if _, err := service.accounts.Update(ctx, ports.AccountUpdate{
				Principal:          principal,
				Account:            restored,
				RequireOAuthMarker: polled.Authorization.ID,
			}); err != nil {
				if errors.Is(err, ports.ErrAccountUpdateConflict) {
					// Another poll already settled the failure.
					break
				}
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

// oauthStartSoftGate applies the Auth Mode, risk, and purpose gates for an OAuth
// start without checking the single-flight marker. The marker is enforced after
// the replay claim so terminal same-key retries can still recover.
func (service *ProviderAccountService) oauthStartSoftGate(account domain.ProviderAccount, purpose domain.OAuthPurpose) (domain.CanonicalError, bool) {
	if canonical, ok := service.authModeGate(account); !ok {
		return canonical, false
	}
	if !account.AuthMode.SupportsServerOwnedOAuth() {
		return domain.NewInvalidRequest(), false
	}
	if account.PendingCredentialVersion > 0 {
		return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
	}
	if !account.Lifecycle.AcceptsOAuthStart(purpose) {
		return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
	}
	return domain.CanonicalError{}, true
}

// oauthStartGate is retained for call sites that need the full pre-adapter gate
// including the single-flight marker (for example diagnostics).
func (service *ProviderAccountService) oauthStartGate(account domain.ProviderAccount, purpose domain.OAuthPurpose) (domain.CanonicalError, bool) {
	if canonical, ok := service.oauthStartSoftGate(account, purpose); !ok {
		return canonical, false
	}
	if account.ActiveOAuthAuthorizationID != "" {
		return domain.NewAccountNotUsable(domain.RemediationCompleteOAuth), false
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

// newOAuthAuthorizationID mints a server-owned oauth_* journey identity.
func (service *ProviderAccountService) newOAuthAuthorizationID() (domain.OAuthAuthorizationID, error) {
	id, err := service.ids.New(domain.IdentifierKindOAuth)
	if err != nil {
		return "", err
	}
	return domain.OAuthAuthorizationID(id), nil
}
