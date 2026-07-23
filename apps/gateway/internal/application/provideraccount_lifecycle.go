package application

import (
	"context"
	"errors"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// DisableProviderAccountCommand is the typed management disable request. Disable
// carries no request body and no Idempotency-Key (the frozen contract omits
// both); it is idempotent at the product level, so it is not replay-claimed.
type DisableProviderAccountCommand struct {
	PresentedKeyMaterial string
	AccountID            domain.ProviderAccountID
	RequestID            domain.Identifier
}

// EnableProviderAccountCommand is the typed management enable request. Like
// disable it carries no body and no Idempotency-Key.
type EnableProviderAccountCommand struct {
	PresentedKeyMaterial string
	AccountID            domain.ProviderAccountID
	RequestID            domain.Identifier
}

// DeleteProviderAccountCommand is the typed management delete request. Delete
// carries no body and no Idempotency-Key; it is idempotent at the product level.
type DeleteProviderAccountCommand struct {
	PresentedKeyMaterial string
	AccountID            domain.ProviderAccountID
	RequestID            domain.Identifier
}

// DisableProviderAccount runs the protected spine for a management disable. It
// authenticates (A0), enforces accounts.manage (A1), resolves same-Tenant
// ownership, rejects a pure `draft` shell before admission, then persists the
// `disabled` transition. Disable blocks new routing, execution, and credential
// decrypt without rewriting health or claiming a credential failure, and it has
// no vault decrypt purpose (connection lifecycle spec §4.10, management contract
// §4.5). Disable intent wins over an in-flight replacement journey: the durable
// transition rewrites the pending origin to `disabled` so a concurrent
// validation, exchange, probe, or replacement completion lands back in
// `disabled` rather than re-activating the account (management contract §4.6).
//
// Persistence is fenced (OAuth marker + pending version) and re-reads the row
// immediately before write so a concurrent cutover/OAuth claim is not clobbered
// by a stale full-record snapshot.
func (service *ProviderAccountService) DisableProviderAccount(ctx context.Context, command DisableProviderAccountCommand) (ProviderAccountResult, error) {
	sc := spineContext{operation: operationDisableProviderAccount, requestID: service.resolveRequestID(command.RequestID), start: service.clock.Now()}

	principal, canonical, ok := service.authenticate(ctx, ports.PresentedClientAPIKey{Material: command.PresentedKeyMaterial})
	if !ok {
		return ProviderAccountResult{}, service.fail(ctx, sc, canonical)
	}
	sc.keyID = principal.ClientAPIKeyID

	// A1: scope. Disable requires accounts.manage.
	if !principal.Scopes.Has(domain.ScopeAccountsManage) {
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewForbidden())
	}

	// Same-Tenant ownership on the named id. Foreign, unknown, and deleted ids
	// all resolve to the single non-enumerating resource_not_found outcome
	// before any admission or protected mutation (#6 section 5.1).
	account, err := service.accounts.Visible(ctx, principal, command.AccountID)
	if err != nil {
		return ProviderAccountResult{}, service.fail(ctx, sc, service.visibilityCanonical(err))
	}

	// A pure `draft` shell has no usable credentialed connection to pause, so
	// disable is rejected with a stable class rather than a silent no-op
	// (connection lifecycle spec §4.10 rule 5, management contract §4.5).
	if !account.Lifecycle.AcceptsDisable() {
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewAccountNotUsable(domain.RemediationAccountRemediation))
	}

	reservation, canonical, ok := service.admit(ctx, principal, operationDisableProviderAccount)
	if !ok {
		return ProviderAccountResult{}, service.fail(ctx, sc, canonical)
	}
	defer service.release(ctx, reservation)

	persisted, canonical, ok := service.persistDisable(ctx, principal, command.AccountID, domain.NewTimestamp(sc.start))
	if !ok {
		return ProviderAccountResult{}, service.fail(ctx, sc, canonical)
	}

	service.observeSuccess(ctx, sc, ports.AuditProviderAccountDisabled, principal, persisted.ID, 200)
	return ProviderAccountResult{Account: persisted, RequestID: sc.requestID}, nil
}

// persistDisable re-reads the account, re-checks the lifecycle gate, and writes
// WithDisabled under OAuth/pending fences. One conflict retry absorbs a single
// concurrent claim/cutover without clobbering the newer row.
func (service *ProviderAccountService) persistDisable(ctx context.Context, principal domain.SecurityPrincipal, accountID domain.ProviderAccountID, now domain.Timestamp) (domain.ProviderAccount, domain.CanonicalError, bool) {
	for attempt := 0; attempt < 2; attempt++ {
		latest, err := service.accounts.Visible(ctx, principal, accountID)
		if err != nil {
			return domain.ProviderAccount{}, service.visibilityCanonical(err), false
		}
		if !latest.Lifecycle.AcceptsDisable() {
			return domain.ProviderAccount{}, domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
		}

		disabled := latest.WithDisabled(now)
		update := ports.AccountUpdate{Principal: principal, Account: disabled}
		if latest.PendingCredentialVersion > 0 {
			// Fence to the pending version observed at write time so a concurrent
			// promotion that clears pending loses cleanly instead of being rolled back.
			update.RequirePendingVersion = latest.PendingCredentialVersion
		} else {
			update.RequireEmptyPendingVersion = true
		}
		if latest.ActiveOAuthAuthorizationID == "" {
			// Do not wipe a single-flight OAuth claim that lands after this load.
			update.RequireEmptyOAuthMarker = true
		} else {
			// Only settle against the journey we observed; a concurrent clear/claim loses.
			update.RequireOAuthMarker = latest.ActiveOAuthAuthorizationID
		}

		persisted, err := service.accounts.Update(ctx, update)
		if err == nil {
			return persisted, domain.CanonicalError{}, true
		}
		if errors.Is(err, ports.ErrAccountUpdateConflict) && attempt == 0 {
			continue
		}
		if errors.Is(err, ports.ErrAccountUpdateConflict) {
			// A second conflict means the row is still racing; fail closed as a
			// dependency so the client can retry without a permanent 500.
			return domain.ProviderAccount{}, domain.NewDependencyUnavailable(), false
		}
		if errors.Is(err, ports.ErrAccountNotVisible) {
			return domain.ProviderAccount{}, service.visibilityCanonical(err), false
		}
		return domain.ProviderAccount{}, service.dependencyCanonical(err), false
	}
	return domain.ProviderAccount{}, domain.NewInternalError(), false
}

// EnableProviderAccount runs the protected spine for a management enable. Every
// disabled-to-active recovery first enters `pending_probe`; the 202 response
// never predicts probe success and activation still requires a separate
// current-credential-version probe. The management contract §4.5 rule
// I-ACCOUNT-ENABLE-PROBED overrides the older optional short-disable skip path
// in connection lifecycle spec §4.10 rule 4 (management contract §1.2): every
// enable re-probes unconditionally rather than sometimes returning to `active`
// without a probe, which is a safe superset of the §4.10 MUST. Enable is
// rejected for any non-`disabled` account and while an OAuth authorization or
// replacement version is still in flight, so an administrative enable can never
// race or overwrite the journey that kept the account `disabled` publicly.
func (service *ProviderAccountService) EnableProviderAccount(ctx context.Context, command EnableProviderAccountCommand) (ProviderAccountResult, error) {
	sc := spineContext{operation: operationEnableProviderAccount, requestID: service.resolveRequestID(command.RequestID), start: service.clock.Now()}

	principal, canonical, ok := service.authenticate(ctx, ports.PresentedClientAPIKey{Material: command.PresentedKeyMaterial})
	if !ok {
		return ProviderAccountResult{}, service.fail(ctx, sc, canonical)
	}
	sc.keyID = principal.ClientAPIKeyID

	if !principal.Scopes.Has(domain.ScopeAccountsManage) {
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewForbidden())
	}

	account, err := service.accounts.Visible(ctx, principal, command.AccountID)
	if err != nil {
		return ProviderAccountResult{}, service.fail(ctx, sc, service.visibilityCanonical(err))
	}

	// Soft gates before admission so a blocked enable debits nothing durable.
	if canonical, ok := service.enableSoftGate(account); !ok {
		return ProviderAccountResult{}, service.fail(ctx, sc, canonical)
	}

	reservation, canonical, ok := service.admit(ctx, principal, operationEnableProviderAccount)
	if !ok {
		return ProviderAccountResult{}, service.fail(ctx, sc, canonical)
	}
	defer service.release(ctx, reservation)

	// Re-read after admission so the write observes the latest OAuth/pending state
	// rather than the pre-admission snapshot (OAuth poll pattern).
	latest, err := service.accounts.Visible(ctx, principal, command.AccountID)
	if err != nil {
		return ProviderAccountResult{}, service.fail(ctx, sc, service.visibilityCanonical(err))
	}
	if canonical, ok := service.enableSoftGate(latest); !ok {
		return ProviderAccountResult{}, service.fail(ctx, sc, canonical)
	}

	pending := latest.WithEnableProbePending(domain.NewTimestamp(sc.start))
	persisted, err := service.accounts.Update(ctx, ports.AccountUpdate{
		Principal:                  principal,
		Account:                    pending,
		RequireEmptyOAuthMarker:    true,
		RequireEmptyPendingVersion: true,
	})
	if err != nil {
		if errors.Is(err, ports.ErrAccountUpdateConflict) {
			// Map the fence loss to the same soft-gate classes: re-read to choose
			// complete_oauth vs account_remediation rather than internal_error.
			current, visibleErr := service.accounts.Visible(ctx, principal, command.AccountID)
			if visibleErr != nil {
				return ProviderAccountResult{}, service.fail(ctx, sc, service.visibilityCanonical(visibleErr))
			}
			if canonical, ok := service.enableSoftGate(current); !ok {
				return ProviderAccountResult{}, service.fail(ctx, sc, canonical)
			}
			return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewDependencyUnavailable())
		}
		return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}

	service.observeSuccess(ctx, sc, ports.AuditProviderAccountEnabled, principal, persisted.ID, 202)
	return ProviderAccountResult{Account: persisted, RequestID: sc.requestID}, nil
}

// enableSoftGate enforces enable-only-from-disabled, the Auth Mode execution and
// quarantine controls, plus the single-flight OAuth/replacement ownership windows
// (management contract §4.5, §4.6; health controls §13.8, §15.2, §16.3).
func (service *ProviderAccountService) enableSoftGate(account domain.ProviderAccount) (domain.CanonicalError, bool) {
	if !account.Lifecycle.AcceptsEnable() {
		return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
	}
	if !account.Controls.AuthModeExecutionEnabled {
		return domain.NewAuthModeUnavailable(), false
	}
	if account.Controls.Quarantine == domain.QuarantineQuarantined {
		return domain.NewAccountNotUsable(domain.RemediationContactOperator), false
	}
	if account.ActiveOAuthAuthorizationID != "" {
		return domain.NewAccountNotUsable(domain.RemediationCompleteOAuth), false
	}
	if account.PendingCredentialVersion > 0 {
		return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
	}
	return domain.CanonicalError{}, true
}

// DeleteProviderAccount runs the protected spine for a management delete. It
// authenticates (A0), enforces accounts.manage (A1), resolves same-Tenant
// ownership, then revokes every stored current and pending credential version
// before persisting the terminal `deleted` transition, so all credentials lose
// use authority before the account disappears from ordinary list/get
// (connection lifecycle spec §4.12, management contract §3.3, vault spec §6.5,
// §8.5). The delete command itself has no vault decrypt purpose; a foreign,
// unknown, or deleted id causes zero revoke or mutation (#6 section 5.1). Delete
// is idempotent at the product level and returns no body (204).
func (service *ProviderAccountService) DeleteProviderAccount(ctx context.Context, command DeleteProviderAccountCommand) (ProviderAccountResult, error) {
	sc := spineContext{operation: operationDeleteProviderAccount, requestID: service.resolveRequestID(command.RequestID), start: service.clock.Now()}

	principal, canonical, ok := service.authenticate(ctx, ports.PresentedClientAPIKey{Material: command.PresentedKeyMaterial})
	if !ok {
		return ProviderAccountResult{}, service.fail(ctx, sc, canonical)
	}
	sc.keyID = principal.ClientAPIKeyID

	if !principal.Scopes.Has(domain.ScopeAccountsManage) {
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewForbidden())
	}

	account, err := service.accounts.Visible(ctx, principal, command.AccountID)
	if err != nil {
		return ProviderAccountResult{}, service.fail(ctx, sc, service.visibilityCanonical(err))
	}

	reservation, canonical, ok := service.admit(ctx, principal, operationDeleteProviderAccount)
	if !ok {
		return ProviderAccountResult{}, service.fail(ctx, sc, canonical)
	}
	defer service.release(ctx, reservation)

	// Revoke every stored credential version before removing use authority. The
	// current version and any in-flight pending replacement version both lose
	// decrypt authority first; revoke is idempotent so a retry after a partial
	// delete is safe (vault spec §6.5, §8.5 deletion ordering). A revoke failure
	// is a fail-closed dependency error and MUST NOT mark the account deleted:
	// non-use is preserved and the delete can be retried conservatively.
	if err := service.revokeAllCredentialVersions(ctx, principal, account); err != nil {
		return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}

	deleted := account.WithDeleted(domain.NewTimestamp(sc.start))
	persisted, err := service.accounts.Update(ctx, ports.AccountUpdate{Principal: principal, Account: deleted})
	if err != nil {
		// Concurrent delete already made the row non-visible: product-level
		// delete is idempotent, credentials were revoked, and clients must not
		// observe internal_error for "already gone."
		if errors.Is(err, ports.ErrAccountNotVisible) {
			service.observeSuccess(ctx, sc, ports.AuditProviderAccountDeleted, principal, deleted.ID, 204)
			return ProviderAccountResult{Account: deleted, RequestID: sc.requestID}, nil
		}
		return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}

	service.observeSuccess(ctx, sc, ports.AuditProviderAccountDeleted, principal, persisted.ID, 204)
	return ProviderAccountResult{Account: persisted, RequestID: sc.requestID}, nil
}

// revokeAllCredentialVersions revokes the current and pending credential
// versions the account still holds. A version of zero means none was stored, so
// nothing is revoked for that slot; a duplicate (pending equal to current) is
// revoked once. Revoke is idempotent, so repeating it on a delete retry is safe.
func (service *ProviderAccountService) revokeAllCredentialVersions(ctx context.Context, principal domain.SecurityPrincipal, account domain.ProviderAccount) error {
	revoke := func(version int) error {
		if version <= 0 {
			return nil
		}
		return service.vault.Revoke(ctx, ports.CredentialValidation{
			Principal: principal,
			AccountID: account.ID,
			AuthMode:  account.AuthMode,
			Version:   version,
		})
	}
	if err := revoke(account.Credential.Version); err != nil {
		return err
	}
	if account.PendingCredentialVersion != account.Credential.Version {
		if err := revoke(account.PendingCredentialVersion); err != nil {
			return err
		}
	}
	return nil
}
