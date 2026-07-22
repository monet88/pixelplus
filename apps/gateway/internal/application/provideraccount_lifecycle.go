package application

import (
	"context"

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

	disabled := account.WithDisabled(domain.NewTimestamp(sc.start))
	persisted, err := service.accounts.Update(ctx, ports.AccountUpdate{Principal: principal, Account: disabled})
	if err != nil {
		return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}

	service.observeSuccess(ctx, sc, ports.AuditProviderAccountDisabled, principal, persisted.ID, 200)
	return ProviderAccountResult{Account: persisted, RequestID: sc.requestID}, nil
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

	// Only a `disabled` account may enter the enable probe path.
	if !account.Lifecycle.AcceptsEnable() {
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewAccountNotUsable(domain.RemediationAccountRemediation))
	}
	// A single-flight OAuth journey or an in-flight replacement version owns the
	// window; enable must not race or overwrite it (management contract §4.5,
	// §4.6). Reject before admission so a blocked enable debits nothing durable.
	if account.ActiveOAuthAuthorizationID != "" {
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewAccountNotUsable(domain.RemediationCompleteOAuth))
	}
	if account.PendingCredentialVersion > 0 {
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewAccountNotUsable(domain.RemediationAccountRemediation))
	}

	reservation, canonical, ok := service.admit(ctx, principal, operationEnableProviderAccount)
	if !ok {
		return ProviderAccountResult{}, service.fail(ctx, sc, canonical)
	}
	defer service.release(ctx, reservation)

	pending := account.WithEnableProbePending(domain.NewTimestamp(sc.start))
	persisted, err := service.accounts.Update(ctx, ports.AccountUpdate{Principal: principal, Account: pending})
	if err != nil {
		return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}

	service.observeSuccess(ctx, sc, ports.AuditProviderAccountEnabled, principal, persisted.ID, 202)
	return ProviderAccountResult{Account: persisted, RequestID: sc.requestID}, nil
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
