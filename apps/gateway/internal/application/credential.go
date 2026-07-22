package application

import (
	"context"
	"errors"
	"unicode/utf8"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// minCredentialMaterialLength mirrors the frozen CredentialSubmission.material
// minLength. A shorter material is a request_validation failure so an obviously
// truncated secret never reaches the Vault.
const minCredentialMaterialLength = 8

// SubmitProviderCredentialCommand is the typed direct credential submission. The
// transport extracts the raw bearer material and the writeOnly credential
// material; the application authenticates the bearer and forwards the secret to
// the Vault without inspecting or retaining it. Client-supplied Tenant identity
// is never trusted (#6 section 2.2).
type SubmitProviderCredentialCommand struct {
	PresentedKeyMaterial string
	AccountID            domain.ProviderAccountID
	CredentialClass      domain.CredentialClass
	Material             string
	IdempotencyKey       string
	RequestID            domain.Identifier
	OversizeBody         bool
	MalformedBody        bool
}

// ProbeProviderAccountCommand is the typed controlled probe request. The probe
// operation carries no Idempotency-Key (the frozen contract omits it) so it is
// not replay-claimed; it validates the stored credential version and runs a
// cost-minimal auth-proving probe.
type ProbeProviderAccountCommand struct {
	PresentedKeyMaterial string
	AccountID            domain.ProviderAccountID
	Scope                domain.HealthScope
	RequestID            domain.Identifier
	OversizeBody         bool
	MalformedBody        bool
}

// SubmitProviderCredential runs the protected spine for a direct credential
// submission. It authenticates (A0), enforces accounts.manage (A1), request-size
// (A2) and request validation, resolves same-Tenant ownership, applies the
// connection usability gates (risk envelope + lifecycle + credential class), and
// only then claims the scoped idempotency key and stores the material through
// the Vault. A successful store lands the account in pending_validation with the
// credential version bumped (frozen 202); the material never enters durable
// Gateway state, logs, audit, or any response (connection lifecycle spec §4.4,
// §9; credential vault spec §3.3).
func (service *ProviderAccountService) SubmitProviderCredential(ctx context.Context, command SubmitProviderCredentialCommand) (ProviderAccountResult, error) {
	sc := spineContext{operation: operationSubmitProviderCredential, requestID: service.resolveRequestID(command.RequestID), start: service.clock.Now()}

	principal, canonical, ok := service.authenticate(ctx, ports.PresentedClientAPIKey{Material: command.PresentedKeyMaterial})
	if !ok {
		return ProviderAccountResult{}, service.fail(ctx, sc, canonical)
	}
	sc.keyID = principal.ClientAPIKeyID

	// A1: scope. Submission requires accounts.manage.
	if !principal.Scopes.Has(domain.ScopeAccountsManage) {
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewForbidden())
	}

	// A2: request-size, then request validation (strict decode, required
	// Idempotency-Key, key length, credential class, material bounds). These run
	// before ownership resolution and any Vault use so a malformed authenticated
	// submit never reaches the protected boundary.
	if command.OversizeBody {
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewRequestTooLarge())
	}
	if command.MalformedBody {
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}
	if command.IdempotencyKey == "" {
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}
	if utf8.RuneCountInString(command.IdempotencyKey) > maxIdempotencyKeyLength {
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}
	if !command.CredentialClass.Valid() {
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}
	if utf8.RuneCountInString(command.Material) < minCredentialMaterialLength {
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}

	// Same-Tenant ownership on the named id. Foreign, unknown, and deleted ids
	// all resolve to the single non-enumerating resource_not_found outcome
	// before any usability gate, Vault use, or replay claim (#6 section 5.1).
	account, err := service.accounts.Visible(ctx, principal, command.AccountID)
	if err != nil {
		return ProviderAccountResult{}, service.fail(ctx, sc, service.visibilityCanonical(err))
	}

	// Connection usability gates. A prohibited Auth Mode fails closed with
	// auth_mode_unavailable; a disabled Auth Mode, an unmet risk acknowledgement,
	// a lifecycle state that does not accept a submission, or a credential class
	// that does not match the Auth Mode all reject with account_not_usable BEFORE
	// any Vault use (connection lifecycle spec §4.2, §4.4, §5.2; risk envelope
	// §5.5, §6.1).
	if canonical, ok := service.submissionGate(account, command.CredentialClass); !ok {
		return ProviderAccountResult{}, service.fail(ctx, sc, canonical)
	}

	// Replay ownership claim BEFORE admission so a terminal/in-progress/conflict
	// replay never stores a second credential version or debits admission
	// (#20 section 5.5). The fingerprint binds the scoped key to the account,
	// Auth Mode, and credential class -- never to the secret material.
	identity := domain.ReplayIdentity{
		Scope: domain.ReplayScope{
			TenantID:       principal.TenantID,
			ClientAPIKeyID: principal.ClientAPIKeyID,
			Key:            command.IdempotencyKey,
		},
		Fingerprint: domain.NewSubmitCredentialFingerprint(account.ID, command.CredentialClass),
	}
	decision, err := service.replay.Claim(ctx, identity)
	if err != nil {
		return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}
	switch decision.Outcome {
	case ports.ReplayClaimed:
		// Sole executor: fall through to admission then Vault store.
	case ports.ReplayTerminal:
		terminal := decision.TerminalAccount
		service.recordTelemetry(ctx, sc.operation, "", 202)
		service.recordRequestLog(ctx, sc.requestID, principal.ClientAPIKeyID, string(sc.operation), 202, "ok", sc.start)
		return ProviderAccountResult{Account: terminal, RequestID: sc.requestID}, nil
	case ports.ReplayInProgress:
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewIdempotencyInProgress())
	case ports.ReplayConflict:
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewIdempotencyConflict())
	case ports.ReplayUncertain:
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewIdempotencyUncertain())
	default:
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewInternalError())
	}

	// A3-A5: admission after the fresh claim. A rejection abandons the claim so
	// this request stored nothing and debited nothing durable.
	reservation, canonical, ok := service.admit(ctx, principal, operationSubmitProviderCredential)
	if !ok {
		service.abandon(ctx, identity)
		return ProviderAccountResult{}, service.fail(ctx, sc, canonical)
	}

	// Re-check single-flight OAuth marker immediately before Vault use so a
	// concurrent OAuth start that claimed the account cannot be overwritten by a
	// stale direct submit snapshot (management contract §4.3).
	latest, err := service.accounts.Visible(ctx, principal, account.ID)
	if err != nil {
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		return ProviderAccountResult{}, service.fail(ctx, sc, service.visibilityCanonical(err))
	}
	if latest.ActiveOAuthAuthorizationID != "" {
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewAccountNotUsable(domain.RemediationCompleteOAuth))
	}
	if !latest.Lifecycle.AcceptsCredentialSubmission() {
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewAccountNotUsable(domain.RemediationAccountRemediation))
	}
	account = latest

	// Store the material through the Vault under the next credential version.
	// The application forwards the secret without inspecting or retaining it and
	// receives nothing secret back.
	nextVersion := account.Credential.Version + 1
	intake := ports.CredentialIntake{
		Principal: principal,
		AccountID: account.ID,
		AuthMode:  account.AuthMode,
		Class:     command.CredentialClass,
		Version:   nextVersion,
		Material:  command.Material,
	}
	if err := service.vault.Put(ctx, intake); err != nil {
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}

	submitted := account.WithSubmittedCredential(domain.NewTimestamp(sc.start), domain.Timestamp{})
	persisted, err := service.accounts.Update(ctx, ports.AccountUpdate{
		Principal:               principal,
		Account:                 submitted,
		RequireEmptyOAuthMarker: true,
	})
	if err != nil {
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		if errors.Is(err, ports.ErrAccountUpdateConflict) {
			return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewAccountNotUsable(domain.RemediationCompleteOAuth))
		}
		return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}

	if err := service.replay.Complete(ctx, identity, ports.ReplayResult{Account: persisted}); err != nil {
		// The credential store and lifecycle advance already happened, so the
		// side effect is durable but its terminal replay record is uncertain.
		// Return idempotency_uncertain (no-steal) and do NOT abandon the claim,
		// so a later retry cannot store a second version for the same scoped key.
		service.release(ctx, reservation)
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewIdempotencyUncertain())
	}

	service.release(ctx, reservation)
	service.observeSuccess(ctx, sc, ports.AuditProviderCredentialSubmitted, principal, persisted.ID, 202)
	return ProviderAccountResult{Account: persisted, RequestID: sc.requestID}, nil
}

// ProbeProviderAccount runs the protected spine for a controlled probe. It
// authenticates (A0), enforces accounts.manage (A1) and request validation,
// resolves same-Tenant ownership, applies the usability gates, then validates
// the stored credential version and runs the cost-minimal probe. Validation
// failure prevents the probe; probe auth-failure never activates the account;
// validation plus probe success activates only when every gate has passed
// (connection lifecycle spec §4.5-§4.7, §5.1 I-USABLE-GATE, I-NO-ACTIVE-ON-FAIL).
func (service *ProviderAccountService) ProbeProviderAccount(ctx context.Context, command ProbeProviderAccountCommand) (ProviderAccountResult, error) {
	sc := spineContext{operation: operationProbeProviderAccount, requestID: service.resolveRequestID(command.RequestID), start: service.clock.Now()}

	principal, canonical, ok := service.authenticate(ctx, ports.PresentedClientAPIKey{Material: command.PresentedKeyMaterial})
	if !ok {
		return ProviderAccountResult{}, service.fail(ctx, sc, canonical)
	}
	sc.keyID = principal.ClientAPIKeyID

	// A1: scope. Probe requires accounts.manage.
	if !principal.Scopes.Has(domain.ScopeAccountsManage) {
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewForbidden())
	}

	// Request validation: strict decode outcome. The probe body is optional; a
	// malformed body (unknown field / bad scope) is a request_validation failure.
	if command.OversizeBody {
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewRequestTooLarge())
	}
	if command.MalformedBody {
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}

	// Same-Tenant ownership before any gate, Vault use, or Adapter call.
	account, err := service.accounts.Visible(ctx, principal, command.AccountID)
	if err != nil {
		return ProviderAccountResult{}, service.fail(ctx, sc, service.visibilityCanonical(err))
	}

	// Usability gates: prohibited/disabled Auth Mode, unmet risk acknowledgement,
	// a lifecycle state that does not accept a probe, or an absent credential
	// version all reject with account_not_usable BEFORE the Vault or Adapter runs
	// (connection lifecycle spec §4.6, §5.2).
	if canonical, ok := service.probeGate(account); !ok {
		return ProviderAccountResult{}, service.fail(ctx, sc, canonical)
	}

	// A3-A5: admission after ownership and the pre-adapter gates.
	reservation, canonical, ok := service.admit(ctx, principal, operationProbeProviderAccount)
	if !ok {
		return ProviderAccountResult{}, service.fail(ctx, sc, canonical)
	}
	defer service.release(ctx, reservation)

	// Required validation runs BEFORE the probe so a malformed stored credential
	// never spends probe budget. A validation failure moves the account to
	// reauth_required and returns without calling the Adapter (validation failure
	// prevents probe: connection lifecycle spec §4.5, §4.6).
	validation, err := service.vault.Validate(ctx, ports.CredentialValidation{
		Principal: principal,
		AccountID: account.ID,
		AuthMode:  account.AuthMode,
		Version:   account.Credential.Version,
	})
	if err != nil {
		return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}
	if !validation.Valid {
		return service.probeRejected(ctx, sc, principal, account)
	}
	validated := account.WithValidatedCredential(domain.NewTimestamp(sc.start))

	// Required probe: cost-minimal, auth-proving. A transient backend failure is
	// a fail-closed dependency error; an auth-class failure is reported as
	// Authenticated=false and moves the account to reauth_required WITHOUT
	// activating it (probe failure never activates: §4.6 rule 5).
	outcome, err := service.probe.Probe(ctx, ports.ProbeCommand{
		Principal: principal,
		AccountID: validated.ID,
		AuthMode:  validated.AuthMode,
		Version:   validated.Credential.Version,
		Scope:     command.Scope,
	})
	if err != nil {
		// Persist the validated state so last_validated_at is durable, then
		// surface the fail-closed dependency error.
		_, _ = service.accounts.Update(ctx, ports.AccountUpdate{Principal: principal, Account: validated})
		return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}
	if !outcome.Authenticated {
		return service.probeRejected(ctx, sc, principal, validated)
	}

	// Mint the credential-version-bound Capability Snapshot before activation so
	// an active account never authorizes work without published evidence for this
	// version (capability semantics section 9; I-CAP-VERSION-BIND).
	if err := service.mintCapabilitySnapshot(ctx, principal, validated); err != nil {
		_, _ = service.accounts.Update(ctx, ports.AccountUpdate{Principal: principal, Account: validated})
		return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}

	// Validation + probe succeeded for the current version and every gate passed:
	// this is the only transition into `active` in this slice.
	activated := validated.WithProbeActivated(domain.NewTimestamp(sc.start))
	persisted, err := service.accounts.Update(ctx, ports.AccountUpdate{Principal: principal, Account: activated})
	if err != nil {
		return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}
	service.observeSuccess(ctx, sc, ports.AuditProviderAccountActivated, principal, persisted.ID, 200)
	return ProviderAccountResult{Account: persisted, RequestID: sc.requestID}, nil
}

// probeRejected persists the credential-rejected transition and returns the
// non-activating result as a 200 success projection (the account exists and the
// operation ran; the observable outcome is the resulting non-active account with
// remediation reauthenticate). It records the probe audit without secrets.
func (service *ProviderAccountService) probeRejected(ctx context.Context, sc spineContext, principal domain.SecurityPrincipal, account domain.ProviderAccount) (ProviderAccountResult, error) {
	rejected := account.WithCredentialRejected(domain.NewTimestamp(sc.start))
	persisted, err := service.accounts.Update(ctx, ports.AccountUpdate{Principal: principal, Account: rejected})
	if err != nil {
		return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}
	service.observeSuccess(ctx, sc, ports.AuditProviderAccountProbed, principal, persisted.ID, 200)
	return ProviderAccountResult{Account: persisted, RequestID: sc.requestID}, nil
}

// submissionGate applies the shared connection usability gates for a direct
// credential submission. It returns the canonical failure and false when the
// account cannot accept the submission, always before any Vault use.
func (service *ProviderAccountService) submissionGate(account domain.ProviderAccount, class domain.CredentialClass) (domain.CanonicalError, bool) {
	if canonical, ok := service.authModeGate(account); !ok {
		return canonical, false
	}
	if !account.Lifecycle.AcceptsCredentialSubmission() {
		return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
	}
	// A server-owned OAuth journey already in flight owns the replacement window.
	// Direct submission must not overwrite or orphan that journey (management
	// contract §4.3 single-flight replacement gate).
	if account.ActiveOAuthAuthorizationID != "" {
		return domain.NewAccountNotUsable(domain.RemediationCompleteOAuth), false
	}
	// The submitted credential class MUST match the Auth Mode so Web and
	// OAuth/CLI credential lifecycles never mix on one account (I-NO-WEB-OAUTH-MIX).
	if class != account.AuthMode.RequiredCredentialClass() {
		return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
	}
	return domain.CanonicalError{}, true
}

// probeGate applies the shared usability gates for a controlled probe. It
// returns the canonical failure and false when the account cannot be probed,
// always before the Vault or Adapter runs.
func (service *ProviderAccountService) probeGate(account domain.ProviderAccount) (domain.CanonicalError, bool) {
	if canonical, ok := service.authModeGate(account); !ok {
		return canonical, false
	}
	if !account.Lifecycle.AcceptsProbe() {
		return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
	}
	// A probe proves a stored credential; an account that never stored one
	// (version zero) cannot be probed.
	if account.Credential.Version == 0 {
		return domain.NewAccountNotUsable(domain.RemediationSubmitCredential), false
	}
	return domain.CanonicalError{}, true
}

// authModeGate rejects a prohibited Auth Mode, a disabled Auth Mode execution
// control, and a gated/experimental mode without the required Tenant risk
// acknowledgement, in that order. A prohibited mode is auth_mode_unavailable;
// the others are account_not_usable with the remediation the Tenant needs
// (risk envelope §5.5, §6.1; connection lifecycle spec §4.2, §5.1).
func (service *ProviderAccountService) authModeGate(account domain.ProviderAccount) (domain.CanonicalError, bool) {
	if account.AuthMode.Prohibited() {
		return domain.NewAuthModeUnavailable(), false
	}
	if !account.Controls.AuthModeExecutionEnabled {
		return domain.NewAccountNotUsable(domain.RemediationEnableAccount), false
	}
	if account.AuthMode.RequiresRiskAck() && !account.RiskAcknowledged {
		return domain.NewAccountNotUsable(domain.RemediationAckRisk), false
	}
	return domain.CanonicalError{}, true
}
