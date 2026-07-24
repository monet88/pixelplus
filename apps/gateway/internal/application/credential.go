package application

import (
	"context"
	"encoding/binary"
	"errors"
	"hash/fnv"
	"time"
	"unicode/utf8"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// minCredentialMaterialLength mirrors the frozen CredentialSubmission.material
// minLength. A shorter material is a request_validation failure so an obviously
// truncated secret never reaches the Vault.
const (
	minCredentialMaterialLength = 8

	providerRateHintMaxPlausible  = 24 * time.Hour
	providerQuotaHintMaxPlausible = 31 * 24 * time.Hour
)

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
	Replacement          bool
}

// ReauthenticateProviderAccount stages direct replacement material through the
// same protected credential path while selecting the stable reauthentication
// operation identity and pending-version semantics.
func (service *ProviderAccountService) ReauthenticateProviderAccount(ctx context.Context, command SubmitProviderCredentialCommand) (ProviderAccountResult, error) {
	command.Replacement = true
	return service.submitCredential(ctx, command)
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
	command.Replacement = false
	return service.submitCredential(ctx, command)
}

func (service *ProviderAccountService) submitCredential(ctx context.Context, command SubmitProviderCredentialCommand) (ProviderAccountResult, error) {
	operation := operationSubmitProviderCredential
	if command.Replacement {
		operation = operationReauthenticateProviderAccount
	}
	sc := spineContext{operation: operation, requestID: service.resolveRequestID(command.RequestID), start: service.clock.Now()}

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
	if canonical, ok := service.submissionGate(account, command.CredentialClass, command.Replacement); !ok {
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
		Fingerprint: domain.NewSubmitCredentialFingerprint(account.ID, command.CredentialClass, command.Replacement),
	}
	decision, err := service.replay.Claim(ctx, identity)
	if err != nil {
		return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}
	switch decision.Outcome {
	case ports.ReplayClaimed:
		// Sole executor: fall through to admission then Vault store.
	case ports.ReplayTerminal:
		// Terminal record must already hold the effective projection; re-project
		// defensively so a legacy empty-health record cannot leak.
		terminal := projectAccountHealth(decision.TerminalAccount)
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
	reservation, canonical, ok := service.admit(ctx, principal, operation)
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
	if !command.Replacement && !latest.Lifecycle.AcceptsCredentialSubmission() {
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewAccountNotUsable(domain.RemediationAccountRemediation))
	}
	account = latest
	var replacementMarker domain.OAuthAuthorizationID
	if command.Replacement {
		id, idErr := service.newOAuthAuthorizationID()
		if idErr != nil {
			service.release(ctx, reservation)
			service.abandon(ctx, identity)
			return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewInternalError())
		}
		replacementMarker = id
		claimed := latest.WithOAuthJourneyStarted(replacementMarker, domain.NewTimestamp(sc.start))
		if claimed.PendingCredentialVersion == 0 {
			claimed = claimed.WithReplacementCredential(domain.NewTimestamp(sc.start), domain.Timestamp{})
		}
		if _, err := service.accounts.Update(ctx, ports.AccountUpdate{Principal: principal, Account: claimed, RequireEmptyOAuthMarker: true}); err != nil {
			service.release(ctx, reservation)
			service.abandon(ctx, identity)
			if errors.Is(err, ports.ErrAccountUpdateConflict) {
				return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewAccountNotUsable(domain.RemediationCompleteOAuth))
			}
			return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
		}
		account = claimed
	}

	// Allocate the next version before Vault.Put. Replacement already reserved its
	// pending version while claiming the OAuth marker above. First-connect reserves
	// only LastAllocatedVersion under CAS, leaving lifecycle/current credential
	// unchanged until the Vault + HealthStore side effects succeed. A failed side
	// effect therefore consumes the version and a retry cannot reuse its slot.
	var submitted domain.ProviderAccount
	nextVersion := account.PendingCredentialVersion
	if !command.Replacement {
		submitted = account.WithSubmittedCredential(domain.NewTimestamp(sc.start), domain.Timestamp{}).
			WithOAuthJourneyCleared(domain.NewTimestamp(sc.start))
		nextVersion = submitted.Credential.Version
		reserved, reserveErr := service.accounts.Update(ctx, ports.AccountUpdate{
			Principal:                        principal,
			Account:                          account,
			RequireEmptyOAuthMarker:          true,
			RequireEmptyPendingVersion:       true,
			RequireLifecycle:                 account.Lifecycle,
			RequireControlsMatch:             true,
			RequireControls:                  account.Controls,
			PatchLastAllocatedVersion:        true,
			RequireLastAllocatedVersionMatch: true,
			RequireLastAllocatedVersion:      account.Credential.LastAllocatedVersion,
			LastAllocatedVersion:             nextVersion,
		})
		if reserveErr != nil {
			service.release(ctx, reservation)
			service.abandon(ctx, identity)
			if errors.Is(reserveErr, ports.ErrAccountUpdateConflict) {
				return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewDependencyUnavailable())
			}
			return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(reserveErr))
		}
		account = reserved
	}
	// The application forwards the secret without inspecting or retaining it and
	// receives nothing secret back.
	intake := ports.CredentialIntake{
		Principal: principal,
		AccountID: account.ID,
		AuthMode:  account.AuthMode,
		Class:     command.CredentialClass,
		Version:   nextVersion,
		Material:  command.Material,
	}
	if err := service.vault.Put(ctx, intake); err != nil {
		if replacementMarker != "" {
			// Fence was claimed before Vault storage. Restore must succeed before
			// the idempotency claim can be abandoned; otherwise a same-key or
			// new-key retry can race an incomplete replacement fence.
			restored := account.WithPendingCredentialRejected(domain.NewTimestamp(service.clock.Now())).WithOAuthJourneyCleared(domain.NewTimestamp(service.clock.Now()))
			if _, restoreErr := service.accounts.Update(ctx, ports.AccountUpdate{
				Principal:             principal,
				Account:               restored,
				RequireOAuthMarker:    replacementMarker,
				RequirePendingVersion: account.PendingCredentialVersion,
			}); restoreErr != nil {
				service.release(ctx, reservation)
				return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewIdempotencyUncertain())
			}
		}
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}

	if command.Replacement {
		if account.PendingCredentialVersion == 0 {
			service.release(ctx, reservation)
			service.abandon(ctx, identity)
			return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewInternalError())
		}
		submitted = account.WithOAuthJourneyCleared(domain.NewTimestamp(sc.start))
	}
	// Lifecycle/metadata only — strip health authority from AccountStore write.
	submitted.Health = domain.HealthSummary{}
	submitted.RecoveryPermit = domain.RecoveryPermit{}
	// Credential epoch: fence HealthStore to the newly stored version before the
	// AccountStore lifecycle write so product traffic never sees pending/current
	// metadata advanced while health still authorizes an old credential epoch.
	nowSubmit := domain.NewTimestamp(sc.start)
	epoch := ports.CredentialEpochReset{
		Principal: principal, AccountID: account.ID,
		NewCredentialVersion: nextVersion, ObservedAt: nowSubmit,
		Audit: service.healthAudit(principal, account.ID, account.AuthMode, sc.requestID),
	}
	if command.Replacement {
		// Keep still-current usable version evidence; drop foreign-version rows + permit.
		epoch.PreserveCredentialVersion = account.Credential.Version
		if epoch.PreserveCredentialVersion <= 0 {
			epoch.PreserveCredentialVersion = 0
			// Disabled/draft replacement without a current version: full reset.
			epoch.NewCredentialVersion = nextVersion
		}
	}
	if _, err := service.health.ResetForCredentialEpoch(ctx, epoch); err != nil {
		// Missing health is an invariant failure: never advance AccountStore or
		// complete replay. Vault already holds nextVersion — revoke only that
		// version so residual material cannot become usable.
		_ = service.vault.Revoke(ctx, ports.CredentialValidation{
			Principal: principal, AccountID: account.ID, AuthMode: account.AuthMode, Version: nextVersion,
		})
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}
	update := ports.AccountUpdate{
		Principal:               principal,
		Account:                 submitted,
		RequireEmptyOAuthMarker: replacementMarker == "",
		RequireOAuthMarker:      replacementMarker,
		RequirePendingVersion:   account.PendingCredentialVersion,
	}
	if !command.Replacement {
		update.RequireLifecycle = account.Lifecycle
		update.RequireControlsMatch = true
		update.RequireControls = account.Controls
		update.RequireLastAllocatedVersionMatch = true
		update.RequireLastAllocatedVersion = nextVersion
	}
	persisted, err := service.accounts.Update(ctx, update)
	if err != nil {
		revokeErr := service.vault.Revoke(ctx, ports.CredentialValidation{
			Principal: principal,
			AccountID: account.ID,
			AuthMode:  account.AuthMode,
			Version:   nextVersion,
		})
		if revokeErr != nil {
			service.release(ctx, reservation)
			return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewIdempotencyUncertain())
		}
		if replacementMarker != "" {
			// Restore the origin before abandoning replay. First-connect leaves its
			// reserved counter in place so the next request allocates a fresh version.
			restored := account.WithPendingCredentialRejected(domain.NewTimestamp(service.clock.Now())).WithOAuthJourneyCleared(domain.NewTimestamp(service.clock.Now()))
			if _, restoreErr := service.accounts.Update(ctx, ports.AccountUpdate{
				Principal:             principal,
				Account:               restored,
				RequireOAuthMarker:    replacementMarker,
				RequirePendingVersion: account.PendingCredentialVersion,
			}); restoreErr != nil {
				service.release(ctx, reservation)
				return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewIdempotencyUncertain())
			}
		}
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		if errors.Is(err, ports.ErrAccountUpdateConflict) {
			if command.Replacement {
				return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewAccountNotUsable(domain.RemediationCompleteOAuth))
			}
			return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewDependencyUnavailable())
		}
		return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}

	// Compose + project BEFORE replay.Complete so the terminal replay record
	// stores the same safe effective-summary projection as the HTTP response
	// (not AccountStore-stripped empty health).
	if snap, err := service.health.Read(ctx, principal, persisted.ID); err != nil {
		service.release(ctx, reservation)
		// Lifecycle/credential already advanced; do not abandon (no-steal).
		return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	} else {
		persisted = composeAccountHealth(persisted, snap)
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
	if !validProbeScope(command.Scope) {
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}

	// Same-Tenant ownership before any gate, Vault use, or Adapter call.
	// Compose AccountStore lifecycle with HealthStore conditions/permits.
	account, err := service.loadAccount(ctx, principal, command.AccountID)
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
	probeVersion := account.Credential.Version
	if account.PendingCredentialVersion > 0 {
		probeVersion = account.PendingCredentialVersion
	}
	// A prior health-first hard rejection may have committed before its
	// AccountStore lifecycle write failed. Converge lifecycle metadata without
	// decrypting or probing the already-rejected credential again.
	if credentialRejectedAtVersion(account.Health, probeVersion) {
		if account.PendingCredentialVersion > 0 {
			return service.pendingProbeRejected(ctx, sc, principal, account)
		}
		return service.probeRejected(ctx, sc, principal, account, domain.RecoveryPermit{})
	}

	// Soft health gates BEFORE admission so blocked cooldown/occupied-permit
	// attempts do not take capacity (thread G). Claim stays after admission.
	var pendingClaim domain.RecoveryPermit
	if account.Lifecycle == domain.LifecycleActive {
		decision := account.ScopedRecoveryPermit(domain.NewTimestamp(sc.start), command.Scope, sc.requestID)
		if decision.Occupied {
			return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewRecoveryPermitOccupied())
		}
		if decision.Cooling && !decision.Eligible {
			return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewProviderCooldownBlocked(decision.RetryAfterSeconds))
		}
		if decision.Eligible {
			pendingClaim = decision.Permit
		}
	}

	// A3-A5: admission after ownership and the pre-adapter soft gates.
	reservation, canonical, ok := service.admit(ctx, principal, operationProbeProviderAccount)
	if !ok {
		return ProviderAccountResult{}, service.fail(ctx, sc, canonical)
	}
	defer service.release(ctx, reservation)

	// Provider Surface Circuit state is a shared new-work gate, distinct from
	// account Health. Resolve its upstream surface from the current credential-
	// version-bound Capability Snapshot when available so connection probes and
	// model selection query the same coordinate. Missing/stale evidence uses an
	// empty safe query coordinate that overlaps any concrete surface in the bounded
	// Provider/Auth Mode domain; unreadable evidence fails closed. Tenant/account identity and client-selected URLs never
	// enter the shared key. Designated operator canaries require a separate
	// purpose-bound command and are intentionally not represented here.
	circuitSurface, err := service.probeCircuitSurface(ctx, principal, account, command.Scope)
	if err != nil {
		return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}
	circuit, err := service.circuits.SurfaceOpen(ctx, circuitSurface)
	if err != nil {
		if errors.Is(err, ports.ErrCircuitUnavailable) {
			return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewDependencyUnavailable())
		}
		return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}
	if circuit.Open {
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewProviderCooldownBlocked(0))
	}

	// Atomic recovery permit claim after admission when the soft gate found an
	// eligible half-open slot. CAS remains the hard fence. A CAS loser (another
	// request claimed first) maps to the same stable 409 occupied outcome as a
	// pre-claim Occupied decision — never a generic 503.
	var recoveryPermit domain.RecoveryPermit
	if pendingClaim.Owner != "" {
		// Close the admission/circuit window before touching HealthStore. This
		// re-read prevents a disable or administrative-control change that completed
		// before the claim from granting any half-open protected work.
		latestBeforeClaim, loadErr := service.loadAccount(ctx, principal, account.ID)
		if loadErr != nil {
			return ProviderAccountResult{}, service.fail(ctx, sc, service.visibilityCanonical(loadErr))
		}
		if latestBeforeClaim.Lifecycle != domain.LifecycleActive ||
			latestBeforeClaim.Controls != account.Controls ||
			latestBeforeClaim.PendingCredentialVersion != 0 {
			if canonical, ok := service.probeGate(latestBeforeClaim); !ok {
				return ProviderAccountResult{}, service.fail(ctx, sc, canonical)
			}
			return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewDependencyUnavailable())
		}
		decision := latestBeforeClaim.ScopedRecoveryPermit(domain.NewTimestamp(sc.start), command.Scope, sc.requestID)
		if decision.Occupied {
			return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewRecoveryPermitOccupied())
		}
		if !decision.Eligible || decision.Permit != pendingClaim {
			return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewProviderCooldownBlocked(decision.RetryAfterSeconds))
		}
		account = latestBeforeClaim
		claimed, err := service.health.ClaimRecoveryPermit(ctx, ports.RecoveryPermitClaim{
			Principal:         principal,
			AccountID:         account.ID,
			Owner:             pendingClaim.Owner,
			Scope:             pendingClaim.Scope,
			ConditionRevision: pendingClaim.ConditionRevision,
			CredentialVersion: pendingClaim.CredentialVersion,
		})
		if err != nil {
			if errors.Is(err, ports.ErrAccountUpdateConflict) {
				return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewRecoveryPermitOccupied())
			}
			return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
		}
		recoveryPermit = claimed.Permit
		// Post-claim re-read fence: disable (or other gate change) may have won
		// between the pre-admission load and this claim. Never reach Vault/Adapter
		// with a permit if the account is no longer probeable; clear only the
		// exact permit this request claimed (CAS). A newer/different owner must
		// be preserved — stale cleanup fails closed without wiping foreign fencing.
		// Our own successful claim is not treated as foreign Occupied below.
		latest, loadErr := service.loadAccount(ctx, principal, account.ID)
		if loadErr != nil {
			_, _ = service.health.ClearPermit(ctx, ports.PermitClear{
				Principal: principal, AccountID: account.ID,
				ExpectedPermit: recoveryPermit,
				Audit:          service.healthAudit(principal, account.ID, account.AuthMode, sc.requestID),
			})
			return ProviderAccountResult{}, service.fail(ctx, sc, service.visibilityCanonical(loadErr))
		}
		if canonical, ok := service.probeGate(latest); !ok {
			_, _ = service.health.ClearPermit(ctx, ports.PermitClear{
				Principal: principal, AccountID: latest.ID,
				ExpectedPermit: recoveryPermit,
				Audit:          service.healthAudit(principal, latest.ID, latest.AuthMode, sc.requestID),
			})
			return ProviderAccountResult{}, service.fail(ctx, sc, canonical)
		}
		// Durable permit must still be ours after the re-read (no other owner).
		// Own claim is valid ownership — only a different owner is foreign occupied.
		if latest.RecoveryPermit.Owner != "" && latest.RecoveryPermit.Owner != recoveryPermit.Owner {
			return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewRecoveryPermitOccupied())
		}
		if latest.RecoveryPermit.Owner == "" {
			// Claim was lost between write and re-read — fail closed, do not probe.
			return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewDependencyUnavailable())
		}
		if latest.Lifecycle != domain.LifecycleActive || latest.Controls != account.Controls || latest.PendingCredentialVersion != 0 {
			_, _ = service.health.ClearPermit(ctx, ports.PermitClear{
				Principal: principal, AccountID: latest.ID,
				ExpectedPermit: recoveryPermit,
				Audit:          service.healthAudit(principal, latest.ID, latest.AuthMode, sc.requestID),
			})
			return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewDependencyUnavailable())
		}
		latest.RecoveryPermit = recoveryPermit
		account = latest
	}

	// Required validation runs BEFORE the probe so a malformed stored credential
	// never spends probe budget. A validation failure moves the account to
	// reauth_required and returns without calling the Adapter (validation failure
	// prevents probe: connection lifecycle spec §4.5, §4.6).
	probeVersion = account.Credential.Version
	if account.PendingCredentialVersion > 0 {
		probeVersion = account.PendingCredentialVersion
	}
	validation, err := service.vault.Validate(ctx, ports.CredentialValidation{
		Principal: principal,
		AccountID: account.ID,
		AuthMode:  account.AuthMode,
		Version:   probeVersion,
	})
	if err != nil {
		if recoveryPermit.Owner != "" {
			if renewErr := service.renewCooldownAfterDependencyFailure(ctx, sc, principal, account, recoveryPermit); renewErr != nil {
				return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(renewErr))
			}
		}
		return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}
	if !validation.Valid {
		if account.PendingCredentialVersion > 0 {
			return service.pendingProbeRejected(ctx, sc, principal, account)
		}
		return service.probeRejected(ctx, sc, principal, account, recoveryPermit)
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
		Version:   probeVersion,
		Scope:     command.Scope,
	})
	if err != nil {
		// Probe Adapter dependency failure: renew claimed cooldown + consume permit.
		if recoveryPermit.Owner != "" {
			_ = service.renewCooldownAfterDependencyFailure(ctx, sc, principal, validated, recoveryPermit)
		}
		return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}
	if !validProbeSignal(outcome.Signal) {
		// An unrecognized signal class is a dependency/protocol classification
		// failure. Fail closed rather than treating it as a successful probe.
		if recoveryPermit.Owner != "" {
			_ = service.renewCooldownAfterDependencyFailure(ctx, sc, principal, validated, recoveryPermit)
		}
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewDependencyUnavailable())
	}
	if !outcome.Authenticated {
		if account.PendingCredentialVersion > 0 {
			return service.pendingProbeRejected(ctx, sc, principal, validated)
		}
		return service.probeRejected(ctx, sc, principal, validated, recoveryPermit)
	}

	// Mint the credential-version-bound Capability Snapshot before activation so
	// an active account never authorizes work without published evidence for this
	// version (capability semantics section 9; I-CAP-VERSION-BIND). A scoped
	// cooldown recovery does not mint capability evidence from identity success
	// alone (§9.11); it only settles the fenced Health Condition.
	if recoveryPermit.Owner == "" {
		evidence := validated
		evidence.Credential.Version = probeVersion
		if err := service.mintCapabilitySnapshot(ctx, principal, evidence); err != nil {
			// Capability publication is non-authoritative for lifecycle metadata.
			// Do not persist this stale snapshot: a concurrent disable, revoke,
			// quarantine, or control change must remain authoritative.
			return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
		}
	}

	// Validation + probe succeeded for the version this request proved. Re-read
	// before activation so sticky disable (PendingOrigin / lifecycle) and any
	// concurrent cutover are observed at commit time rather than on the
	// pre-admission snapshot (management contract §4.6 disable-intent-wins).
	latest, err := service.loadAccount(ctx, principal, account.ID)
	if err != nil {
		return ProviderAccountResult{}, service.fail(ctx, sc, service.visibilityCanonical(err))
	}
	now := domain.NewTimestamp(sc.start)
	base := latest
	// Carry this request's validation evidence onto the durable row.
	if !validated.Credential.LastValidatedAt.IsZero() {
		base.Credential.LastValidatedAt = validated.Credential.LastValidatedAt
	}

	var activated domain.ProviderAccount
	var priorVersion int
	update := ports.AccountUpdate{Principal: principal}
	switch {
	case account.PendingCredentialVersion > 0:
		// This request probed a pending replacement. A concurrent settlement that
		// cleared or replaced the pending version must lose cleanly.
		if latest.PendingCredentialVersion != account.PendingCredentialVersion {
			return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewDependencyUnavailable())
		}
		priorVersion = latest.Credential.Version
		// WithReplacementProbeActivated honors latest.PendingOrigin, so a disable
		// that rewrote origin to disabled after this probe loaded still lands disabled.
		activated = base.WithReplacementProbeActivated(now)
		update.RequirePendingVersion = account.PendingCredentialVersion
		// Fence against a concurrent lifecycle or control change so a stale probe
		// snapshot cannot resurrect a disabled/quarantined account.
		update.RequireLifecycle = latest.Lifecycle
		update.RequireControlsMatch = true
		update.RequireControls = latest.Controls
	case latest.Lifecycle == domain.LifecycleDisabled:
		// Sticky disable mid first-connect / enable probe: record probe success
		// evidence but do not re-activate. Mirrors disabled-origin replacement cutover.
		activated = base.WithProbeActivated(now)
		activated.Lifecycle = domain.LifecycleDisabled
		update.RequireEmptyPendingVersion = true
		update.RequireLifecycle = latest.Lifecycle
		update.RequireControlsMatch = true
		update.RequireControls = latest.Controls
	case !latest.Lifecycle.AcceptsProbe():
		// Concurrent lifecycle transition (e.g. revoke/delete path edge) rejected
		// activation after the probe ran; fail closed without promoting use.
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewAccountNotUsable(domain.RemediationAccountRemediation))
	case latest.Lifecycle == domain.LifecycleActive:
		// Re-probe of an already-active account is a recovery observation, not a
		// first activation: it MUST NOT wipe scoped health evidence and re-assert a
		// blanket healthy summary. An authenticated probe with no fresh rate/quota
		// signal is an authoritative scoped success that resolves only the probed
		// scope (§9.12, §11); the cooldown overlay below still renews/adds a scope
		// when a fresh signal is present, so an out-of-scope cooldown survives
		// (§4 rules 4-5, §7.9, I-HEALTH-NO-STALE-CLEAR).
		switch {
		case outcome.Signal == ports.ProbeSignalNone && recoveryPermit.Owner == "":
			// No fresh signal and no claimed recovery: record a heartbeat without
			// replacing the whole row, so concurrent scoped health changes survive.
			update.PatchLastProbedAt = true
			update.LastProbedAt = now
			update.RequireLifecycle = latest.Lifecycle
			update.RequireControlsMatch = true
			update.RequireControls = latest.Controls
			update.RequireEmptyPendingVersion = true
		case outcome.Signal == ports.ProbeSignalNone && recoveryPermit.Owner != "":
			// Health resolve happens below; AccountStore only fences lifecycle.
			activated = base
			activated.Credential.LastProbedAt = now
			activated.UpdatedAt = now
			update.RequireLifecycle = latest.Lifecycle
			update.RequireControlsMatch = true
			update.RequireControls = latest.Controls
			update.RequireEmptyPendingVersion = true
		default:
			// Fresh rate/quota signal: AccountStore records probe time; HealthStore
			// observes the cooldown below.
			activated = base
			activated.Credential.LastProbedAt = now
			activated.UpdatedAt = now
			update.RequireLifecycle = latest.Lifecycle
			update.RequireControlsMatch = true
			update.RequireControls = latest.Controls
			update.RequireEmptyPendingVersion = true
		}
	default:
		activated = base.WithProbeActivated(now)
		// Fence against a concurrent replacement staging after this load.
		update.RequireEmptyPendingVersion = true
		update.RequireLifecycle = latest.Lifecycle
		update.RequireControlsMatch = true
		update.RequireControls = latest.Controls
	}

	// Order: required audit -> logical HealthStore mutation -> AccountStore.
	// Never commit health after AccountStore mutation.
	var healthResult ports.AccountHealth
	healthResult.Health = latest.Health
	healthResult.RecoveryPermit = latest.RecoveryPermit

	// First-connect / enable activation records healthy evidence when this request
	// is promoting into an activated lifecycle (or sticky-disabled with probe success).
	needsActivation := !update.PatchLastProbedAt && recoveryPermit.Owner == "" &&
		outcome.Signal == ports.ProbeSignalNone &&
		(latest.Lifecycle == domain.LifecyclePendingProbe || latest.Lifecycle == domain.LifecyclePendingValidation ||
			latest.Lifecycle == domain.LifecycleDraft || latest.Lifecycle == domain.LifecycleReauthRequired)
	// Also activate when a rate/quota signal arrives on first connect: healthy base
	// then scoped cooldown overlay (auth proven, cooldown orthogonal).
	if !update.PatchLastProbedAt && recoveryPermit.Owner == "" &&
		(latest.Lifecycle == domain.LifecyclePendingProbe || latest.Lifecycle == domain.LifecyclePendingValidation ||
			latest.Lifecycle == domain.LifecycleDraft || latest.Lifecycle == domain.LifecycleReauthRequired) {
		needsActivation = true
	}
	// HealthStore derives exact transitions under lock and invokes audit before
	// append. Application never invents planned audit payloads.
	healthAudit := service.healthAudit(principal, latest.ID, latest.AuthMode, sc.requestID)

	// probeVersion is the credential version this request proved (pending
	// replacement or current). Activation/cooldown evidence must fence to it —
	// never a pre-cutover stale current version.
	activationVersion := probeVersion
	if activationVersion <= 0 {
		activationVersion = latest.Credential.Version
	}

	if needsActivation {
		tr, err := service.health.RecordActivation(ctx, ports.ActivationHealth{
			Principal: principal, AccountID: latest.ID, CredentialVersion: activationVersion,
			ObservedAt: now, Audit: healthAudit,
		})
		if err != nil {
			return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
		}
		healthResult = tr.Result
		latest.Health = tr.Result.Health
	}

	// DEVIATION (recovery success only): ResolveRecovery is the one product
	// gate-relaxing health mutation on an already-active account. Clearing a
	// cooling_down condition before a failed AccountStore lifecycle/metadata CAS
	// would make the account more routable while the fence failed.
	//
	// Therefore ONLY this path does AccountStore metadata/fence first, then
	// HealthStore resolve. Failure before resolve stays conservative (cooling
	// remains). This must NOT be generalized to blocking transitions
	// (ObserveCooldown, RecordHardFailure, epoch reset, enable reset, activation
	// while non-active): those stay health-first because partial health progress
	// either adds a block or cannot authorize product routing alone.
	resolveAfterAccount := outcome.Signal == ports.ProbeSignalNone && recoveryPermit.Owner != "" && latest.Lifecycle == domain.LifecycleActive

	if reason, ok := cooldownReasonForSignal(outcome.Signal); ok {
		retryNotBefore, malformedHint := providerRetryNotBefore(latest, outcome.SignalScope, reason, outcome.RetryAfterSeconds, now)
		if malformedHint {
			_ = service.audit.Record(ctx, ports.AuditEvent{
				Action: ports.AuditProviderHintMalformed, TenantID: principal.TenantID,
				ClientAPIKeyID: principal.ClientAPIKeyID, ProviderAccountID: latest.ID,
				RequestID: sc.requestID, Outcome: "malformed_provider_hint",
			})
		}
		obs := ports.CooldownObservation{
			Principal: principal, AccountID: latest.ID, Scope: outcome.SignalScope,
			Reason: reason, CredentialVersion: activationVersion,
			ObservedAt: now, RetryNotBefore: retryNotBefore,
			SourceClass: domain.HealthSourceUpstreamAttempt, ConsumePermit: recoveryPermit,
			Audit: healthAudit,
		}
		if recoveryPermit.Owner != "" && !recoveryPermit.MatchesScope(outcome.SignalScope) {
			// Claimed scope renews with EXISTING reason and ITS own timer/source.
			claimedReason := conditionReasonAtScope(latest, recoveryPermit.Scope)
			if claimedReason == "" {
				claimedReason = reason
			}
			obs.ClaimedScopeReason = claimedReason
			obs.ClaimedScopeRetryNotBefore = defaultCooldownRetryNotBefore(latest, recoveryPermit.Scope, claimedReason, now)
			obs.ClaimedScopeSourceClass = domain.HealthSourceRecoveryProbe
		}
		tr, err := service.health.ObserveCooldown(ctx, obs)
		if err != nil {
			return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
		}
		healthResult = tr.Result
	} else if latest.Lifecycle != domain.LifecycleActive && recoveryPermit.Owner != "" {
		// Request-owned cleanup only: fence to the permit this probe claimed.
		if _, err := service.health.ClearPermit(ctx, ports.PermitClear{
			Principal: principal, AccountID: latest.ID,
			ExpectedPermit: recoveryPermit, Audit: healthAudit,
		}); err != nil {
			// Conflict means a newer/different permit won — preserve it and fail closed.
			if errors.Is(err, ports.ErrAccountUpdateConflict) {
				return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewRecoveryPermitOccupied())
			}
			return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
		}
		healthResult.RecoveryPermit = domain.RecoveryPermit{}
	}

	// AccountStore lifecycle/metadata.
	if update.PatchLastProbedAt {
		update.Account = domain.ProviderAccount{ID: latest.ID}
	} else {
		lifecycleAccount := activated
		if lifecycleAccount.ID == "" {
			lifecycleAccount = latest
			lifecycleAccount.Credential.LastProbedAt = now
			lifecycleAccount.UpdatedAt = now
		}
		lifecycleAccount.Health = domain.HealthSummary{}
		lifecycleAccount.RecoveryPermit = domain.RecoveryPermit{}
		update.Account = lifecycleAccount
	}
	persisted, err := service.accounts.Update(ctx, update)
	if err != nil {
		// Health-first paths (blocking/non-routable): fail closed without relaxing
		// lifecycle. Recovery resolve is deferred past this write (see DEVIATION
		// above): AccountStore failure here means resolve never ran, so cooling stays.
		return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}

	if resolveAfterAccount {
		// Account fence already durable. If resolve fails, lifecycle metadata may
		// have advanced (e.g. last_probed_at) but the cooling condition remains —
		// still fail-closed / not more routable than pre-attempt cooling.
		tr, err := service.health.ResolveRecovery(ctx, ports.RecoveryResolution{
			Principal: principal, AccountID: latest.ID, Permit: recoveryPermit, ObservedAt: now, Audit: healthAudit,
		})
		if err != nil {
			return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
		}
		healthResult = tr.Result
	}
	persisted.Health = healthResult.Health
	persisted.RecoveryPermit = healthResult.RecoveryPermit
	persisted = projectAccountHealth(persisted)

	if priorVersion > 0 {
		// Cutover is already public (credential.version advanced, origin lifecycle
		// restored). A revoke failure leaves decryptable prior material longer than
		// dual-version prefers, but must not surface as a client error that invites
		// a second full reauth; reconcile is idempotent (ADR 0011 residue tradeoff).
		_ = service.vault.Revoke(ctx, ports.CredentialValidation{Principal: principal, AccountID: persisted.ID, AuthMode: persisted.AuthMode, Version: priorVersion})
	}
	service.observeSuccess(ctx, sc, ports.AuditProviderAccountActivated, principal, persisted.ID, 200)
	return ProviderAccountResult{Account: persisted, RequestID: sc.requestID}, nil
}

// probeRejected persists the credential-rejected transition and returns the
// non-activating result as a 200 success projection (the account exists and the
// operation ran; the observable outcome is the resulting non-active account with
// remediation reauthenticate). It records the probe audit without secrets. It
// re-reads the durable row before committing so a concurrent disable or control
// change is observed and preserved rather than overwritten by the stale probe
// snapshot.
func (service *ProviderAccountService) probeRejected(ctx context.Context, sc spineContext, principal domain.SecurityPrincipal, account domain.ProviderAccount, recoveryPermit domain.RecoveryPermit) (ProviderAccountResult, error) {
	latest, err := service.loadAccount(ctx, principal, account.ID)
	if err != nil {
		return ProviderAccountResult{}, service.fail(ctx, sc, service.visibilityCanonical(err))
	}
	now := domain.NewTimestamp(sc.start)
	healthResult := ports.AccountHealth{Health: latest.Health, RecoveryPermit: latest.RecoveryPermit}
	if !credentialRejectedAtVersion(latest.Health, latest.Credential.Version) {
		// Audit under HealthStore lock before persist -> AccountStore lifecycle.
		tr, healthErr := service.health.RecordHardFailure(ctx, ports.HardFailureObservation{
			Principal: principal, AccountID: latest.ID, CredentialVersion: latest.Credential.Version,
			ObservedAt: now, ConsumePermit: recoveryPermit,
			Audit: service.healthAudit(principal, latest.ID, latest.AuthMode, sc.requestID),
		})
		if healthErr != nil {
			return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(healthErr))
		}
		healthResult = tr.Result
	}
	rejected := latest.WithCredentialRejected(now)
	if latest.Lifecycle == domain.LifecycleDisabled || latest.Lifecycle == domain.LifecycleRevoked {
		rejected.Lifecycle = latest.Lifecycle
	}
	lifecycle := rejected
	lifecycle.Health = domain.HealthSummary{}
	lifecycle.RecoveryPermit = domain.RecoveryPermit{}
	persisted, err := service.accounts.Update(ctx, ports.AccountUpdate{
		Principal: principal, Account: lifecycle,
		RequireLifecycle: latest.Lifecycle, RequireControlsMatch: true, RequireControls: latest.Controls,
	})
	if err != nil {
		return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}
	persisted.Health = healthResult.Health
	persisted.RecoveryPermit = domain.RecoveryPermit{}
	persisted = projectAccountHealth(persisted)
	service.observeSuccess(ctx, sc, ports.AuditProviderAccountProbed, principal, persisted.ID, 200)
	return ProviderAccountResult{Account: persisted, RequestID: sc.requestID}, nil
}

// renewCooldownAfterDependencyFailure uses the logical HealthStore renewal with
// recovery-probe provenance. No AccountStore lifecycle rewrite.
func (service *ProviderAccountService) renewCooldownAfterDependencyFailure(
	ctx context.Context,
	sc spineContext,
	principal domain.SecurityPrincipal,
	account domain.ProviderAccount,
	permit domain.RecoveryPermit,
) error {
	now := domain.NewTimestamp(sc.start)
	reason := conditionReasonAtScope(account, permit.Scope)
	if reason == "" {
		reason = domain.HealthReasonProviderRateLimited
	}
	// Timing from progressive backoff policy for the claimed revision.
	retryNotBefore := defaultCooldownRetryNotBefore(account, permit.Scope, reason, now)
	_, err := service.health.RenewAfterDependencyFailure(ctx, ports.DependencyFailureRenewal{
		Principal: principal, AccountID: account.ID, Permit: permit,
		ObservedAt: now, RetryNotBefore: retryNotBefore,
		Audit: service.healthAudit(principal, account.ID, account.AuthMode, sc.requestID),
	})
	return err
}

func conditionReasonAtScope(account domain.ProviderAccount, scope domain.HealthScope) domain.HealthReason {
	for _, condition := range account.Health.Conditions {
		if condition.Scope == scope || (condition.Scope.Kind == scope.Kind &&
			condition.Scope.Operation == scope.Operation &&
			condition.Scope.ModelSlug == scope.ModelSlug) {
			return condition.Reason
		}
	}
	return ""
}

func (service *ProviderAccountService) pendingProbeRejected(ctx context.Context, sc spineContext, principal domain.SecurityPrincipal, account domain.ProviderAccount) (ProviderAccountResult, error) {
	latest, err := service.loadAccount(ctx, principal, account.ID)
	if err != nil {
		return ProviderAccountResult{}, service.fail(ctx, sc, service.visibilityCanonical(err))
	}
	pendingVersion := latest.PendingCredentialVersion
	if pendingVersion == 0 || (account.PendingCredentialVersion > 0 && account.PendingCredentialVersion != pendingVersion) {
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewDependencyUnavailable())
	}
	now := domain.NewTimestamp(sc.start)
	// Health first (required audit): pending-only hard fence drops evidence fenced
	// to the failed pending version and clears the permit, without demoting origin
	// usability scopes. AccountStore then restores origin lifecycle. First-connect
	// rejections use probeRejected (full hard failure) instead.
	healthResult := ports.AccountHealth{Health: latest.Health, RecoveryPermit: latest.RecoveryPermit}
	if !credentialRejectedAtVersion(latest.Health, pendingVersion) {
		tr, healthErr := service.health.RecordHardFailure(ctx, ports.HardFailureObservation{
			Principal: principal, AccountID: latest.ID, CredentialVersion: pendingVersion,
			ObservedAt: now, PendingOnly: true,
			Audit: service.healthAudit(principal, latest.ID, latest.AuthMode, sc.requestID),
		})
		if healthErr != nil {
			return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(healthErr))
		}
		healthResult = tr.Result
	}
	rejected := latest.WithPendingCredentialRejected(now).WithOAuthJourneyCleared(now)
	lifecycle := rejected
	lifecycle.Health = domain.HealthSummary{}
	lifecycle.RecoveryPermit = domain.RecoveryPermit{}
	persisted, err := service.accounts.Update(ctx, ports.AccountUpdate{
		Principal: principal, Account: lifecycle, RequirePendingVersion: pendingVersion,
		RequireLifecycle: latest.Lifecycle, RequireControlsMatch: true, RequireControls: latest.Controls,
	})
	if err != nil {
		// Health already conservatively dropped pending-version evidence only —
		// origin conditions remain; lifecycle is not relaxed (still pending until
		// retry). Fail closed without inventing active/usable state.
		return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}
	persisted.Health = healthResult.Health
	persisted.RecoveryPermit = domain.RecoveryPermit{}
	// Origin version is current after rollback: summary ignores historical pending
	// hard evidence while conditions still list it.
	persisted = projectAccountHealth(persisted)
	_ = service.vault.Revoke(ctx, ports.CredentialValidation{Principal: principal, AccountID: latest.ID, AuthMode: latest.AuthMode, Version: pendingVersion})
	service.observeSuccess(ctx, sc, ports.AuditProviderAccountProbed, principal, persisted.ID, 200)
	return ProviderAccountResult{Account: persisted, RequestID: sc.requestID}, nil
}

func credentialRejectedAtVersion(health domain.HealthSummary, version int) bool {
	for _, condition := range health.Conditions {
		if condition.CredentialVersion == version &&
			condition.State == domain.HealthExpired &&
			condition.Reason == domain.HealthReasonCredentialRejected {
			return true
		}
	}
	return false
}

// submissionGate applies the shared connection usability gates for a direct
// credential submission. It returns the canonical failure and false when the
// account cannot accept the submission, always before any Vault use.
func (service *ProviderAccountService) submissionGate(account domain.ProviderAccount, class domain.CredentialClass, replacement bool) (domain.CanonicalError, bool) {
	if canonical, ok := service.authModeGate(account); !ok {
		return canonical, false
	}
	if replacement {
		// Any in-flight pending version owns the single-flight replacement window
		// (same as oauthStartSoftGate). A fresh reauth must never re-Put under an
		// already-allocated pending version; recovery uses the same idempotency
		// claim only.
		if account.PendingCredentialVersion > 0 {
			return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
		}
		switch account.Lifecycle {
		case domain.LifecycleActive, domain.LifecycleDisabled, domain.LifecycleReauthRequired, domain.LifecycleRevoked:
		default:
			return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
		}
	} else if !account.Lifecycle.AcceptsCredentialSubmission() {
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
	// Quarantine blocks generic Tenant probes, Vault decrypt/validation, and
	// Adapter work. Only a distinct privileged incident-remediation purpose may
	// bypass it; this public probe command intentionally carries no such purpose.
	if account.Controls.Quarantine == domain.QuarantineQuarantined {
		return domain.NewAccountNotUsable(domain.RemediationContactOperator), false
	}
	// A replacement marker means Vault may not yet hold the pending version (or
	// a write is in flight). Fail closed before Validate/Probe so a concurrent
	// probe cannot revoke/restore mid-stage and leave a stuck fence.
	if account.ActiveOAuthAuthorizationID != "" {
		return domain.NewAccountNotUsable(domain.RemediationCompleteOAuth), false
	}
	if !account.Lifecycle.AcceptsProbe() && account.PendingCredentialVersion == 0 {
		return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
	}
	// A probe proves a stored credential; an account that never stored one
	// (version zero) cannot be probed.
	if account.Credential.Version == 0 {
		return domain.NewAccountNotUsable(domain.RemediationSubmitCredential), false
	}
	// Current-version hard health is a hard stop even when AccountStore lifecycle
	// is still pending_probe (partial hard-rejection: health expired, lifecycle
	// write failed). Retry must not reach Vault/Adapter.
	for _, condition := range account.Health.Conditions {
		if condition.CredentialVersion != account.Credential.Version {
			continue
		}
		switch condition.State {
		case domain.HealthExpired:
			return domain.NewAccountNotUsable(domain.RemediationReauthenticate), false
		case domain.HealthChallenged, domain.HealthBlocked:
			return domain.NewAccountNotUsable(domain.RemediationContactOperator), false
		}
	}
	return domain.CanonicalError{}, true
}

// validProbeScope accepts only account scope or a fully specified operation/model
// scope using the frozen capability operation vocabulary. Client input never
// creates a new shared circuit coordinate.
func validProbeScope(scope domain.HealthScope) bool {
	switch scope.Kind {
	case domain.HealthScopeAccount:
		return scope.Operation == "" && scope.ModelSlug == ""
	case domain.HealthScopeOperation:
		return scope.ModelSlug == "" && domain.CapabilityOperation(scope.Operation).Valid()
	case domain.HealthScopeModel:
		return scope.ModelSlug != "" && domain.CapabilityOperation(scope.Operation).Valid()
	default:
		return false
	}
}

// probeCircuitSurface derives the shared circuit coordinates this Tenant probe
// can prove without trusting client-selected URLs. Once current-version Capability
// evidence exists, it uses the same operation ProbeSurface consumed by /v1/models;
// before first activation or when evidence is stale, an empty safe coordinate
// asks the evaluator about any concrete surface in the Provider/Auth Mode domain.
func (service *ProviderAccountService) probeCircuitSurface(
	ctx context.Context,
	principal domain.SecurityPrincipal,
	account domain.ProviderAccount,
	scope domain.HealthScope,
) (ports.CircuitSurface, error) {
	surface := ports.CircuitSurface{
		Provider: account.Provider,
		AuthMode: account.AuthMode,
	}
	if scope.Kind == domain.HealthScopeOperation || scope.Kind == domain.HealthScopeModel {
		surface.Operation = domain.CapabilityOperation(scope.Operation)
	}

	snapshot, err := service.capabilities.Get(ctx, principal, account.ID)
	if err != nil {
		if errors.Is(err, ports.ErrCapabilitySnapshotNotFound) {
			return surface, nil
		}
		return ports.CircuitSurface{}, err
	}
	if snapshot.CredentialVersion != account.Credential.Version || snapshot.AuthMode != account.AuthMode {
		return surface, nil
	}
	if fact, ok := snapshot.Operations[surface.Operation]; ok && fact.ProbeSurface != "" {
		surface.Surface = fact.ProbeSurface
	}
	return surface, nil
}

// authModeGate rejects a prohibited Auth Mode, a disabled Auth Mode execution
// control, and a gated/experimental mode without the required Tenant risk
// acknowledgement, in that order. A prohibited or execution-disabled mode is
// auth_mode_unavailable; missing Tenant risk acknowledgement remains
// account_not_usable/ack_risk (risk envelope §3.5, §5.5, §6.1; connection
// lifecycle spec §4.2, §5.1).
func (service *ProviderAccountService) authModeGate(account domain.ProviderAccount) (domain.CanonicalError, bool) {
	if account.AuthMode.Prohibited() {
		return domain.NewAuthModeUnavailable(), false
	}
	if !account.Controls.AuthModeExecutionEnabled {
		return domain.NewAuthModeUnavailable(), false
	}
	if account.AuthMode.RequiresRiskAck() && !account.RiskAcknowledged {
		return domain.NewAccountNotUsable(domain.RemediationAckRisk), false
	}
	return domain.CanonicalError{}, true
}

// cooldownReasonForSignal maps a normalized probe runtime signal to the canonical
// cooldown Health Reason. Only the time-waitable rate/quota classes create a
// cooldown (§6 rule 1); ProbeSignalNone and any unrecognized class report false
// so no cooldown is overlaid (§6 rule 4: auth/challenge/ban are not signals here).
func cooldownReasonForSignal(signal ports.ProbeSignalClass) (domain.HealthReason, bool) {
	switch signal {
	case ports.ProbeSignalRateLimited:
		return domain.HealthReasonProviderRateLimited, true
	case ports.ProbeSignalQuotaExhausted:
		return domain.HealthReasonProviderQuotaExhausted, true
	default:
		return "", false
	}
}

// validProbeSignal reports whether the adapter returned a recognized signal
// class. Unknown values fail closed as a dependency/protocol classification
// error instead of being treated as a successful probe with a signal.
func validProbeSignal(signal ports.ProbeSignalClass) bool {
	switch signal {
	case ports.ProbeSignalNone, ports.ProbeSignalRateLimited, ports.ProbeSignalQuotaExhausted:
		return true
	default:
		return false
	}
}

func providerRetryNotBefore(
	account domain.ProviderAccount,
	scope domain.HealthScope,
	reason domain.HealthReason,
	retryAfterSeconds int,
	now domain.Timestamp,
) (domain.Timestamp, bool) {
	if retryAfterSeconds <= 0 {
		return defaultCooldownRetryNotBefore(account, scope, reason, now), false
	}
	maximum := providerRateHintMaxPlausible
	if reason == domain.HealthReasonProviderQuotaExhausted {
		maximum = providerQuotaHintMaxPlausible
	}
	if int64(retryAfterSeconds) > int64(maximum/time.Second) {
		return defaultCooldownRetryNotBefore(account, scope, reason, now), true
	}
	return domain.NewTimestamp(now.Time().Add(time.Duration(retryAfterSeconds) * time.Second)), false
}

// defaultCooldownRetryNotBefore applies the frozen no-hint health policy. The
// exponential duration is bounded by its reason class; deterministic positive
// jitter (0-10%) is derived from the account/scope/next revision so workers do
// not synchronize while identical evidence remains reproducible in tests.
func defaultCooldownRetryNotBefore(
	account domain.ProviderAccount,
	scope domain.HealthScope,
	reason domain.HealthReason,
	now domain.Timestamp,
) domain.Timestamp {
	normalized, revision, backoffLevel := account.NextCooldownFence(scope)
	base, maximum := domain.CooldownBaseAndMax(reason)
	duration := boundedExponentialCooldown(base, maximum, backoffLevel)
	jitterRange := duration / 10
	if jitterRange > 0 {
		hash := fnv.New64a()
		_, _ = hash.Write([]byte(account.ID))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(normalized.Kind))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(normalized.Operation))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(normalized.ModelSlug))
		var encoded [8]byte
		binary.LittleEndian.PutUint64(encoded[:], uint64(revision))
		_, _ = hash.Write(encoded[:])
		duration += time.Duration(hash.Sum64() % uint64(jitterRange+1))
		if duration > maximum {
			duration = maximum
		}
	}
	return domain.NewTimestamp(now.Time().Add(duration))
}

func boundedExponentialCooldown(base, maximum time.Duration, level int) time.Duration {
	if level < 1 {
		level = 1
	}
	duration := base
	for current := 1; current < level && duration < maximum; current++ {
		if duration > maximum/2 {
			return maximum
		}
		duration *= 2
	}
	if duration > maximum {
		return maximum
	}
	return duration
}
