// Package application owns Gateway use cases and retry ownership policy.
package application

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// Operation tokens for the Provider Account request spine. They match the
// stable operation ids consumed by telemetry and admission.
const (
	operationCreateProviderAccount domain.OperationToken = "create_provider_account"
	operationGetProviderAccount    domain.OperationToken = "get_provider_account"
	operationListProviderAccounts  domain.OperationToken = "list_provider_accounts"
)

// maxIdempotencyKeyLength mirrors the frozen OpenAPI RequiredIdempotencyKey
// maxLength. A longer key is a request_validation failure so an oversized key
// never enters the replay scope map.
const maxIdempotencyKeyLength = 255

// CreateProviderAccountCommand is the typed create request. PresentedKey is the
// raw bearer material the transport extracted; the application authenticates it
// and derives Tenant authority server-side. Client-supplied Tenant identity is
// never part of this command (#6 section 2.2).
type CreateProviderAccountCommand struct {
	PresentedKeyMaterial string
	Provider             domain.Provider
	AuthMode             domain.AuthMode
	Label                string
	IdempotencyKey       string
	// RequestID is the server-owned correlation id created at the transport
	// boundary. The application never trusts a client-supplied value.
	RequestID domain.Identifier
	// OversizeBody reports that the transport observed a request body over the
	// L-JSON-BODY-MAX ceiling (admission step A2). The flag is carried instead
	// of short-circuiting at the transport so the normative order A0 auth ->
	// A1 scope -> A2 size is enforced in one place and an unauthenticated
	// oversize request still fails as authentication_failed (#8 section 6).
	OversizeBody bool
	// MalformedBody reports that the transport could not strictly decode the
	// JSON body into the typed request (framing/syntax/unknown field). It is a
	// request_validation outcome evaluated after A0-A2 so a malformed body from
	// an unauthenticated caller never leaks a distinguishable 400 before 401.
	MalformedBody bool
}

// GetProviderAccountQuery is the typed read-one request.
type GetProviderAccountQuery struct {
	PresentedKeyMaterial string
	AccountID            domain.ProviderAccountID
	RequestID            domain.Identifier
}

// ListProviderAccountsQuery is the typed list request.
type ListProviderAccountsQuery struct {
	PresentedKeyMaterial string
	RequestID            domain.Identifier
}

// ProviderAccountResult carries one safe account projection plus the
// server-owned request id.
type ProviderAccountResult struct {
	Account   domain.ProviderAccount
	RequestID domain.Identifier
}

// ProviderAccountsResult carries the owning-Tenant account list plus the
// server-owned request id.
type ProviderAccountsResult struct {
	Accounts  []domain.ProviderAccount
	RequestID domain.Identifier
}

// spineContext bundles the per-request correlation fields that every failure
// and success projection needs. Grouping them removes the repeated positional
// arguments that previously threaded through fail/observeSuccess on every error
// branch (data-clump cleanup). keyID is empty until authentication succeeds.
type spineContext struct {
	operation domain.OperationToken
	requestID domain.Identifier
	keyID     domain.ClientAPIKeyID
	start     time.Time
}

// ProviderAccountService runs the protected Public API request spine for
// Provider Account drafts. It owns the normative gate order: authenticate (A0),
// scope and same-Tenant ownership on named ids (A1), request-size (A2) and
// request validation, then a replay ownership claim, then rate/concurrency/
// quota admission (A3-A5) before any durable side effect (#20 section 5.5).
// Request-size (A2) and strict decode are observed at the transport boundary
// but carried as flags so this single normative order is enforced here.
type ProviderAccountService struct {
	principal  ports.PrincipalStore
	admission  ports.AdmissionStore
	replay     ports.ReplayStore
	accounts   ports.AccountStore
	audit      ports.AuditRecorder
	telemetry  ports.TelemetryRecorder
	requestLog ports.RequestLogRecorder
	clock      ports.Clock
	ids        ports.IDGenerator
}

// ProviderAccountDependencies bundles the controlled ports this slice owns.
type ProviderAccountDependencies struct {
	Principal  ports.PrincipalStore
	Admission  ports.AdmissionStore
	Replay     ports.ReplayStore
	Accounts   ports.AccountStore
	Audit      ports.AuditRecorder
	Telemetry  ports.TelemetryRecorder
	RequestLog ports.RequestLogRecorder
	Clock      ports.Clock
	IDs        ports.IDGenerator
}

// NewProviderAccountService validates and wires the request spine dependencies.
func NewProviderAccountService(dependencies ProviderAccountDependencies) (*ProviderAccountService, error) {
	switch {
	case dependencies.Principal == nil:
		return nil, errors.New("application: principal store is required")
	case dependencies.Admission == nil:
		return nil, errors.New("application: admission store is required")
	case dependencies.Replay == nil:
		return nil, errors.New("application: replay store is required")
	case dependencies.Accounts == nil:
		return nil, errors.New("application: account store is required")
	case dependencies.Audit == nil:
		return nil, errors.New("application: audit recorder is required")
	case dependencies.Telemetry == nil:
		return nil, errors.New("application: telemetry recorder is required")
	case dependencies.RequestLog == nil:
		return nil, errors.New("application: request log recorder is required")
	case dependencies.Clock == nil:
		return nil, errors.New("application: clock is required")
	case dependencies.IDs == nil:
		return nil, errors.New("application: ID generator is required")
	}
	return &ProviderAccountService{
		principal:  dependencies.Principal,
		admission:  dependencies.Admission,
		replay:     dependencies.Replay,
		accounts:   dependencies.Accounts,
		audit:      dependencies.Audit,
		telemetry:  dependencies.Telemetry,
		requestLog: dependencies.RequestLog,
		clock:      dependencies.Clock,
		ids:        dependencies.IDs,
	}, nil
}

// CreateProviderAccount runs the full protected spine and persists exactly one
// draft when this request wins the replay claim.
func (service *ProviderAccountService) CreateProviderAccount(ctx context.Context, command CreateProviderAccountCommand) (ProviderAccountResult, error) {
	sc := spineContext{operation: operationCreateProviderAccount, requestID: service.resolveRequestID(command.RequestID), start: service.clock.Now()}

	principal, canonical, ok := service.authenticate(ctx, ports.PresentedClientAPIKey{Material: command.PresentedKeyMaterial})
	if !ok {
		return ProviderAccountResult{}, service.fail(ctx, sc, canonical)
	}
	sc.keyID = principal.ClientAPIKeyID

	// A1: scope. Create requires accounts.manage.
	if !principal.Scopes.Has(domain.ScopeAccountsManage) {
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewForbidden())
	}

	// A2: request-size. An oversize body is rejected only after authentication
	// and scope so the normative order A0 -> A1 -> A2 holds and an
	// unauthenticated oversize request already failed as authentication_failed
	// (#8 section 6).
	if command.OversizeBody {
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewRequestTooLarge())
	}

	// Request validation (post-A2): strict decode outcome, required
	// Idempotency-Key, key length, and enum validity. These run before any
	// capacity reservation so a malformed authenticated create never debits
	// admission.
	if command.MalformedBody {
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}
	if command.IdempotencyKey == "" {
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}
	if utf8.RuneCountInString(command.IdempotencyKey) > maxIdempotencyKeyLength {
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}
	if !command.Provider.Valid() || !command.AuthMode.Valid() {
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}

	// A prohibited Auth Mode is outside the product risk envelope and must fail
	// closed before any replay claim or durable side effect. Only grok_web_sso
	// is prohibited in this slice (auth-mode risk spec §4/§5.5). Gated and
	// experimental gating (operator flag + Tenant acknowledgement) is owned by
	// #7/#9 and is out of scope here.
	if command.AuthMode.Prohibited() {
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewAuthModeUnavailable())
	}

	// Replay ownership claim BEFORE admission so a terminal/in-progress/conflict
	// replay never creates a new admission reservation or quota debit
	// (#20 section 5.5). Only a fresh claim proceeds to the A3-A5 gates; if
	// those gates then reject, the fresh claim is abandoned so a later retry can
	// re-claim the scoped key without this request having debited admission.
	identity := domain.ReplayIdentity{
		Scope: domain.ReplayScope{
			TenantID:       principal.TenantID,
			ClientAPIKeyID: principal.ClientAPIKeyID,
			Key:            command.IdempotencyKey,
		},
		Fingerprint: createFingerprint(command),
	}
	decision, err := service.replay.Claim(ctx, identity)
	if err != nil {
		return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}

	switch decision.Outcome {
	case ports.ReplayClaimed:
		// Sole executor: fall through to admission then persist.
	case ports.ReplayTerminal:
		// A matching terminal replay returns the original draft without a new
		// durable side effect, so it MUST NOT emit a second
		// provider_account.created audit event. Only the safe telemetry and the
		// single request-log line are recorded for the replayed 201.
		account := decision.TerminalAccount
		service.recordTelemetry(ctx, sc.operation, "", 201)
		service.recordRequestLog(ctx, sc.requestID, principal.ClientAPIKeyID, string(sc.operation), 201, "ok", sc.start)
		return ProviderAccountResult{Account: account, RequestID: sc.requestID}, nil
	case ports.ReplayInProgress:
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewIdempotencyInProgress())
	case ports.ReplayConflict:
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewIdempotencyConflict())
	case ports.ReplayUncertain:
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewIdempotencyUncertain())
	default:
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewInternalError())
	}

	// A3-A5: rate, concurrency, quota admission in normative order. A rejection
	// abandons the fresh replay claim so this request debited nothing durable.
	reservation, canonical, ok := service.admit(ctx, principal, operationCreateProviderAccount)
	if !ok {
		service.abandon(ctx, identity)
		return ProviderAccountResult{}, service.fail(ctx, sc, canonical)
	}

	accountID, err := service.newAccountID()
	if err != nil {
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewInternalError())
	}
	draft := domain.NewDraftProviderAccount(accountID, command.Provider, command.AuthMode, command.Label, domain.NewTimestamp(sc.start))

	persisted, err := service.accounts.Create(ctx, ports.AccountCreation{Principal: principal, Account: draft})
	if err != nil {
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		return ProviderAccountResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}

	if err := service.replay.Complete(ctx, identity, ports.ReplayResult{Account: persisted}); err != nil {
		// The draft is already durably persisted, so the create side effect
		// happened but its terminal replay record is uncertain. Return
		// idempotency_uncertain (operator_action_required, no-steal) rather than
		// a dependency error, and do NOT abandon the claim: abandoning would let
		// a later retry create a duplicate draft for the same scoped key
		// (#20 section 5.5). The admission reservation is still released.
		service.release(ctx, reservation)
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewIdempotencyUncertain())
	}

	service.release(ctx, reservation)
	service.observeSuccess(ctx, sc, ports.AuditProviderAccountCreated, principal, persisted.ID, 201)
	return ProviderAccountResult{Account: persisted, RequestID: sc.requestID}, nil
}

// GetProviderAccount reads one owning-Tenant account. Foreign, unknown, and
// deleted identifiers share the same non-enumerating resource_not_found outcome
// before any admission or protected access (#6 section 5.1).
func (service *ProviderAccountService) GetProviderAccount(ctx context.Context, query GetProviderAccountQuery) (ProviderAccountResult, error) {
	sc := spineContext{operation: operationGetProviderAccount, requestID: service.resolveRequestID(query.RequestID), start: service.clock.Now()}

	principal, canonical, ok := service.authenticate(ctx, ports.PresentedClientAPIKey{Material: query.PresentedKeyMaterial})
	if !ok {
		return ProviderAccountResult{}, service.fail(ctx, sc, canonical)
	}
	sc.keyID = principal.ClientAPIKeyID

	// A1: scope then same-Tenant ownership on the named id.
	if !principal.Scopes.Has(domain.ScopeAccountsRead) {
		return ProviderAccountResult{}, service.fail(ctx, sc, domain.NewForbidden())
	}

	account, err := service.accounts.Visible(ctx, principal, query.AccountID)
	if err != nil {
		return ProviderAccountResult{}, service.fail(ctx, sc, service.visibilityCanonical(err))
	}

	// A3-A5: read admission after ownership is established.
	reservation, canonical, ok := service.admit(ctx, principal, operationGetProviderAccount)
	if !ok {
		return ProviderAccountResult{}, service.fail(ctx, sc, canonical)
	}
	service.release(ctx, reservation)

	service.observeSuccess(ctx, sc, ports.AuditProviderAccountRead, principal, account.ID, 200)
	return ProviderAccountResult{Account: account, RequestID: sc.requestID}, nil
}

// ListProviderAccounts returns only the authenticated Tenant's accounts.
func (service *ProviderAccountService) ListProviderAccounts(ctx context.Context, query ListProviderAccountsQuery) (ProviderAccountsResult, error) {
	sc := spineContext{operation: operationListProviderAccounts, requestID: service.resolveRequestID(query.RequestID), start: service.clock.Now()}

	principal, canonical, ok := service.authenticate(ctx, ports.PresentedClientAPIKey{Material: query.PresentedKeyMaterial})
	if !ok {
		return ProviderAccountsResult{}, service.fail(ctx, sc, canonical)
	}
	sc.keyID = principal.ClientAPIKeyID

	if !principal.Scopes.Has(domain.ScopeAccountsRead) {
		return ProviderAccountsResult{}, service.fail(ctx, sc, domain.NewForbidden())
	}

	reservation, canonical, ok := service.admit(ctx, principal, operationListProviderAccounts)
	if !ok {
		return ProviderAccountsResult{}, service.fail(ctx, sc, canonical)
	}

	accounts, err := service.accounts.List(ctx, principal)
	if err != nil {
		service.release(ctx, reservation)
		return ProviderAccountsResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}
	service.release(ctx, reservation)

	service.observeSuccess(ctx, sc, ports.AuditProviderAccountListed, principal, "", 200)
	return ProviderAccountsResult{Accounts: accounts, RequestID: sc.requestID}, nil
}

// authenticate resolves the Security Principal. All authentication failures map
// to one indistinguishable canonical outcome.
func (service *ProviderAccountService) authenticate(ctx context.Context, key ports.PresentedClientAPIKey) (domain.SecurityPrincipal, domain.CanonicalError, bool) {
	principal, err := service.principal.Authenticate(ctx, key)
	if err != nil {
		return domain.SecurityPrincipal{}, domain.NewAuthenticationFailed(), false
	}
	if !principal.Valid() {
		return domain.SecurityPrincipal{}, domain.NewAuthenticationFailed(), false
	}
	return principal, domain.CanonicalError{}, true
}

// admit runs the A3-A5 admission gates and maps the failing stage to its
// canonical code without inventing a new mapping.
func (service *ProviderAccountService) admit(ctx context.Context, principal domain.SecurityPrincipal, operation domain.OperationToken) (ports.AdmissionReservation, domain.CanonicalError, bool) {
	decision, reservation, err := service.admission.Admit(ctx, ports.AdmissionRequest{Principal: principal, Operation: operation})
	if err != nil {
		return ports.AdmissionReservation{}, service.dependencyCanonical(err), false
	}
	if decision.Admitted {
		return reservation, domain.CanonicalError{}, true
	}
	switch decision.Stage {
	case ports.AdmissionStageRateLimit:
		return ports.AdmissionReservation{}, domain.NewRateLimit(), false
	case ports.AdmissionStageConcurrency:
		return ports.AdmissionReservation{}, domain.NewConcurrencyLimit(), false
	case ports.AdmissionStageQuota:
		return ports.AdmissionReservation{}, domain.NewQuotaExhausted(), false
	default:
		return ports.AdmissionReservation{}, domain.NewInternalError(), false
	}
}

// release settles an admission reservation, ignoring a nil reservation.
func (service *ProviderAccountService) release(ctx context.Context, reservation ports.AdmissionReservation) {
	_ = service.admission.Reconcile(ctx, reservation)
}

// abandon releases a fresh replay claim this request will not carry to a
// durable side effect because a later same-request admission gate rejected it.
// It never removes a terminal record, so a legitimate later retry can re-claim
// the scoped key without this request having debited admission or quota
// (#20 section 5.5).
func (service *ProviderAccountService) abandon(ctx context.Context, identity domain.ReplayIdentity) {
	_ = service.replay.Abandon(ctx, identity)
}

// visibilityCanonical maps a visibility failure to the non-enumerating
// resource_not_found outcome, or a fail-closed dependency error.
func (service *ProviderAccountService) visibilityCanonical(err error) domain.CanonicalError {
	if errors.Is(err, ports.ErrAccountNotVisible) {
		return domain.NewResourceNotFound()
	}
	return service.dependencyCanonical(err)
}

// dependencyCanonical maps an infrastructure failure to a fail-closed dependency
// code, never leaking the underlying cause.
func (service *ProviderAccountService) dependencyCanonical(err error) domain.CanonicalError {
	if errors.Is(err, ports.ErrDependencyUnavailable) {
		return domain.NewDependencyUnavailable()
	}
	return domain.NewInternalError()
}

// fail records safe telemetry and the audit-safe request log, then returns the
// canonical error carrying the server-owned request id. The HTTP status code is
// projected from the canonical status class; transport owns emitting it to the
// wire while the application uses it only for the request-log status_code field
// required by ADR 0009.
func (service *ProviderAccountService) fail(ctx context.Context, sc spineContext, canonical domain.CanonicalError) domain.CanonicalError {
	canonical = canonical.WithRequestID(sc.requestID)
	statusCode := canonical.HTTPStatus()
	service.recordTelemetry(ctx, sc.operation, canonical.Code, statusCode)
	service.recordRequestLog(ctx, sc.requestID, sc.keyID, string(sc.operation), statusCode, string(canonical.Code), sc.start)
	return canonical
}

// observeSuccess records the audit, telemetry, and request-log projections for
// a successful operation.
func (service *ProviderAccountService) observeSuccess(ctx context.Context, sc spineContext, action ports.AuditAction, principal domain.SecurityPrincipal, accountID domain.ProviderAccountID, statusCode int) {
	_ = service.audit.Record(ctx, ports.AuditEvent{
		Action:            action,
		TenantID:          principal.TenantID,
		ClientAPIKeyID:    principal.ClientAPIKeyID,
		ProviderAccountID: accountID,
		RequestID:         sc.requestID,
		Outcome:           "success",
	})
	service.recordTelemetry(ctx, sc.operation, "", statusCode)
	service.recordRequestLog(ctx, sc.requestID, principal.ClientAPIKeyID, string(sc.operation), statusCode, "ok", sc.start)
}

func (service *ProviderAccountService) recordTelemetry(ctx context.Context, operation domain.OperationToken, code domain.ErrorCode, statusCode int) {
	_ = service.telemetry.Record(ctx, ports.TelemetryEvent{
		Operation:  operation,
		Code:       code,
		StatusCode: statusCode,
	})
}

func (service *ProviderAccountService) recordRequestLog(ctx context.Context, requestID domain.Identifier, keyID domain.ClientAPIKeyID, action string, statusCode int, message string, start time.Time) {
	_ = service.requestLog.Record(ctx, ports.RequestLog{
		RequestID:  requestID,
		UserID:     keyID,
		Action:     action,
		DurationMS: service.durationMS(start),
		StatusCode: statusCode,
		Message:    message,
	})
}

func (service *ProviderAccountService) durationMS(start time.Time) int64 {
	elapsed := service.clock.Now().Sub(start)
	if elapsed < 0 {
		return 0
	}
	return elapsed.Milliseconds()
}

// resolveRequestID keeps the server-owned request id created at the transport
// boundary, or mints one when the transport could not (fail-closed but still
// correlatable). The id is never taken from client input.
func (service *ProviderAccountService) resolveRequestID(boundaryID domain.Identifier) domain.Identifier {
	if boundaryID != "" {
		return boundaryID
	}
	id, err := service.ids.New(domain.IdentifierKindRequest)
	if err != nil {
		return ""
	}
	return id
}

func (service *ProviderAccountService) newAccountID() (domain.ProviderAccountID, error) {
	id, err := service.ids.New(domain.IdentifierKindProviderAccount)
	if err != nil {
		return "", err
	}
	return domain.ProviderAccountID(id), nil
}

// createFingerprint binds the scoped idempotency key to the create inputs that
// can change the durable side effect. A repeat with different inputs conflicts.
func createFingerprint(command CreateProviderAccountCommand) domain.Fingerprint {
	return domain.NewCreateProviderAccountFingerprint(command.Provider, command.AuthMode, command.Label)
}
