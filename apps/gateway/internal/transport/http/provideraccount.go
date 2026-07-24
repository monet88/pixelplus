package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/monet88/pixelplus/apps/gateway/internal/application"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// maxJSONBody is the L-JSON-BODY-MAX request-size ceiling (2 MiB) enforced at
// the transport boundary as admission step A2 before the application spine runs
// (#8 section 7.2, 7.7).
const maxJSONBody = 2 << 20

// ProviderAccountGateway is the application seam the transport calls. It is a
// narrow local interface so the transport package never imports concrete
// application wiring beyond the command/query types.
type ProviderAccountGateway interface {
	CreateProviderAccount(context.Context, application.CreateProviderAccountCommand) (application.ProviderAccountResult, error)
	GetProviderAccount(context.Context, application.GetProviderAccountQuery) (application.ProviderAccountResult, error)
	ListProviderAccounts(context.Context, application.ListProviderAccountsQuery) (application.ProviderAccountsResult, error)
	SubmitProviderCredential(context.Context, application.SubmitProviderCredentialCommand) (application.ProviderAccountResult, error)
	ReauthenticateProviderAccount(context.Context, application.SubmitProviderCredentialCommand) (application.ProviderAccountResult, error)
	ProbeProviderAccount(context.Context, application.ProbeProviderAccountCommand) (application.ProviderAccountResult, error)
	DisableProviderAccount(context.Context, application.DisableProviderAccountCommand) (application.ProviderAccountResult, error)
	EnableProviderAccount(context.Context, application.EnableProviderAccountCommand) (application.ProviderAccountResult, error)
	DeleteProviderAccount(context.Context, application.DeleteProviderAccountCommand) (application.ProviderAccountResult, error)
	StartOAuthAuthorization(context.Context, application.StartOAuthAuthorizationCommand) (application.OAuthAuthorizationResult, error)
	GetOAuthAuthorization(context.Context, application.GetOAuthAuthorizationQuery) (application.OAuthAuthorizationResult, error)
	GetCapabilitySnapshot(context.Context, application.GetCapabilitySnapshotQuery) (application.CapabilitySnapshotResult, error)
	ListModels(context.Context, application.ListModelsQuery) (application.ModelListResult, error)
}

type providerAccountHandler struct {
	gateway ProviderAccountGateway
	clock   clock
	ids     idGenerator
}

// registerProviderAccountRoutes attaches the stable /v1 Provider Account routes
// to the mux. The server base is /v1, matching the frozen OpenAPI server url.
func registerProviderAccountRoutes(mux *http.ServeMux, gateway ProviderAccountGateway, clock clock, ids idGenerator) {
	handler := providerAccountHandler{gateway: gateway, clock: clock, ids: ids}
	mux.HandleFunc("POST /v1/provider-accounts", handler.create)
	mux.HandleFunc("GET /v1/provider-accounts", handler.list)
	mux.HandleFunc("GET /v1/provider-accounts/{provider_account_id}", handler.get)
	mux.HandleFunc("DELETE /v1/provider-accounts/{provider_account_id}", handler.delete)
	mux.HandleFunc("POST /v1/provider-accounts/{provider_account_id}/disable", handler.disable)
	mux.HandleFunc("POST /v1/provider-accounts/{provider_account_id}/enable", handler.enable)
	mux.HandleFunc("POST /v1/provider-accounts/{provider_account_id}/credentials", handler.submitCredential)
	mux.HandleFunc("POST /v1/provider-accounts/{provider_account_id}/reauthentication", handler.reauthenticate)
	mux.HandleFunc("POST /v1/provider-accounts/{provider_account_id}/probe", handler.probe)
	mux.HandleFunc("POST /v1/provider-accounts/{provider_account_id}/oauth-authorizations", handler.startOAuth)
	mux.HandleFunc("GET /v1/provider-accounts/{provider_account_id}/oauth-authorizations/{authorization_id}", handler.getOAuth)
}

// newRequestID creates the server-owned request id at the Public API boundary
// so both the application spine and pre-spine transport failures share one
// correlation id (#16 section 3.3).
func (handler providerAccountHandler) newRequestID() domain.Identifier {
	id, err := handler.ids.New(domain.IdentifierKindRequest)
	if err != nil {
		return ""
	}
	return id
}

// createProviderAccountRequest mirrors the frozen CreateProviderAccountRequest
// wire schema. Unknown fields are rejected so a client cannot smuggle a
// tenant_id or credential material into the draft-create path.
type createProviderAccountRequest struct {
	Provider string `json:"provider"`
	AuthMode string `json:"auth_mode"`
	Label    string `json:"label"`
}

func (handler providerAccountHandler) create(writer http.ResponseWriter, request *http.Request) {
	requestID := handler.newRequestID()
	presented, _ := bearerMaterial(request)
	idempotencyKey := strings.TrimSpace(request.Header.Get("Idempotency-Key"))

	// The transport observes A2 request-size and strict-decode outcomes but does
	// not short-circuit on them. It forwards them as flags so the application
	// spine enforces the single normative order A0 auth -> A1 scope -> A2 size
	// -> request validation. An unauthenticated oversize or malformed request
	// therefore still fails as authentication_failed, never leaking a
	// distinguishable 413/400 before 401 (#8 section 6).
	body, oversize := readLimitedBody(request)

	var parsed createProviderAccountRequest
	malformed := false
	if !oversize {
		if err := decodeStrictJSON(body, &parsed); err != nil {
			malformed = true
		}
	}

	command := application.CreateProviderAccountCommand{
		RequestID:            requestID,
		PresentedKeyMaterial: presented,
		Provider:             domain.Provider(parsed.Provider),
		AuthMode:             domain.AuthMode(parsed.AuthMode),
		Label:                parsed.Label,
		IdempotencyKey:       idempotencyKey,
		OversizeBody:         oversize,
		MalformedBody:        malformed,
	}

	result, err := handler.gateway.CreateProviderAccount(request.Context(), command)
	if err != nil {
		writeGatewayError(writer, err)
		return
	}
	writeAccountOperation(writer, http.StatusCreated, result, handler.clock.Now())
}

// directCredentialSubmissionRequest mirrors the frozen
// DirectCredentialSubmissionRequest wire schema. Unknown fields are rejected so
// a client cannot smuggle a tenant_id or extra field past the typed contract.
// The material is writeOnly: it enters here once over TLS and is never echoed.
type directCredentialSubmissionRequest struct {
	Credential credentialSubmissionRequest `json:"credential"`
}

type credentialSubmissionRequest struct {
	CredentialClass string `json:"credential_class"`
	Material        string `json:"material"`
}

func (handler providerAccountHandler) submitCredential(writer http.ResponseWriter, request *http.Request) {
	handler.submitCredentialFor(writer, request, false)
}

func (handler providerAccountHandler) reauthenticate(writer http.ResponseWriter, request *http.Request) {
	handler.submitCredentialFor(writer, request, true)
}

func (handler providerAccountHandler) submitCredentialFor(writer http.ResponseWriter, request *http.Request, replacement bool) {
	requestID := handler.newRequestID()
	presented, _ := bearerMaterial(request)
	accountID := request.PathValue("provider_account_id")
	idempotencyKey := strings.TrimSpace(request.Header.Get("Idempotency-Key"))

	// Like create, the transport observes A2 request-size and strict-decode
	// outcomes but forwards them as flags so the application enforces the single
	// normative order A0 auth -> A1 scope -> A2 size -> request validation. The
	// credential material is never logged and never re-serialized.
	body, oversize := readLimitedBody(request)

	var parsed directCredentialSubmissionRequest
	malformed := false
	if !oversize {
		if err := decodeStrictJSON(body, &parsed); err != nil {
			malformed = true
		}
	}

	command := application.SubmitProviderCredentialCommand{
		RequestID:            requestID,
		PresentedKeyMaterial: presented,
		AccountID:            domain.ProviderAccountID(accountID),
		CredentialClass:      domain.CredentialClass(parsed.Credential.CredentialClass),
		Material:             parsed.Credential.Material,
		IdempotencyKey:       idempotencyKey,
		OversizeBody:         oversize,
		MalformedBody:        malformed,
	}

	var result application.ProviderAccountResult
	var err error
	if replacement {
		result, err = handler.gateway.ReauthenticateProviderAccount(request.Context(), command)
	} else {
		result, err = handler.gateway.SubmitProviderCredential(request.Context(), command)
	}
	if err != nil {
		writeGatewayError(writer, err)
		return
	}
	writeAccountOperation(writer, http.StatusAccepted, result, handler.clock.Now())
}

// probeRequest mirrors the frozen ProbeRequest wire schema. The optional scope
// selects the breadth of the probe; unknown fields are rejected.
type probeRequest struct {
	Scope *probeScopeRequest `json:"scope"`
}

type probeScopeRequest struct {
	Kind      string `json:"kind"`
	Operation string `json:"operation"`
	ModelSlug string `json:"model_slug"`
}

func (handler providerAccountHandler) probe(writer http.ResponseWriter, request *http.Request) {
	requestID := handler.newRequestID()
	presented, _ := bearerMaterial(request)
	accountID := request.PathValue("provider_account_id")

	// The probe body is optional. An empty body is a bare account-scope probe; a
	// present body must strictly decode. A2 size is observed and forwarded like
	// the other routes so the normative order holds.
	body, oversize := readLimitedBody(request)

	var parsed probeRequest
	malformed := false
	if !oversize && len(body) > 0 {
		if err := decodeStrictJSON(body, &parsed); err != nil {
			malformed = true
		}
	}

	scope := domain.HealthScope{Kind: domain.HealthScopeAccount}
	if parsed.Scope != nil {
		scope = domain.HealthScope{
			Kind:      domain.HealthScopeKind(parsed.Scope.Kind),
			Operation: parsed.Scope.Operation,
			ModelSlug: parsed.Scope.ModelSlug,
		}
	}

	command := application.ProbeProviderAccountCommand{
		RequestID:            requestID,
		PresentedKeyMaterial: presented,
		AccountID:            domain.ProviderAccountID(accountID),
		Scope:                scope,
		OversizeBody:         oversize,
		MalformedBody:        malformed,
	}

	result, err := handler.gateway.ProbeProviderAccount(request.Context(), command)
	if err != nil {
		writeGatewayError(writer, err)
		return
	}
	writeAccountOperation(writer, http.StatusOK, result, handler.clock.Now())
}

func (handler providerAccountHandler) get(writer http.ResponseWriter, request *http.Request) {
	presented, _ := bearerMaterial(request)
	accountID := request.PathValue("provider_account_id")

	query := application.GetProviderAccountQuery{
		RequestID:            handler.newRequestID(),
		PresentedKeyMaterial: presented,
		AccountID:            domain.ProviderAccountID(accountID),
	}
	result, err := handler.gateway.GetProviderAccount(request.Context(), query)
	if err != nil {
		writeGatewayError(writer, err)
		return
	}
	writeAccount(writer, http.StatusOK, result.Account, handler.clock.Now())
}

// disable blocks new use of a Provider Account without deleting the record. It
// carries no body and no Idempotency-Key; a 200 returns the safe disabled
// projection.
func (handler providerAccountHandler) disable(writer http.ResponseWriter, request *http.Request) {
	presented, _ := bearerMaterial(request)
	accountID := request.PathValue("provider_account_id")

	command := application.DisableProviderAccountCommand{
		RequestID:            handler.newRequestID(),
		PresentedKeyMaterial: presented,
		AccountID:            domain.ProviderAccountID(accountID),
	}
	result, err := handler.gateway.DisableProviderAccount(request.Context(), command)
	if err != nil {
		writeGatewayError(writer, err)
		return
	}
	writeAccountOperation(writer, http.StatusOK, result, handler.clock.Now())
}

// enable enters the current-credential-version probe path for a disabled
// account. The 202 response returns the pending_probe projection and never
// predicts probe success.
func (handler providerAccountHandler) enable(writer http.ResponseWriter, request *http.Request) {
	presented, _ := bearerMaterial(request)
	accountID := request.PathValue("provider_account_id")

	command := application.EnableProviderAccountCommand{
		RequestID:            handler.newRequestID(),
		PresentedKeyMaterial: presented,
		AccountID:            domain.ProviderAccountID(accountID),
	}
	result, err := handler.gateway.EnableProviderAccount(request.Context(), command)
	if err != nil {
		writeGatewayError(writer, err)
		return
	}
	writeAccountOperation(writer, http.StatusAccepted, result, handler.clock.Now())
}

// delete stops new use/decrypt, revokes every stored credential version, and
// removes the account from ordinary list/get. It returns 204 with no body.
func (handler providerAccountHandler) delete(writer http.ResponseWriter, request *http.Request) {
	presented, _ := bearerMaterial(request)
	accountID := request.PathValue("provider_account_id")

	command := application.DeleteProviderAccountCommand{
		RequestID:            handler.newRequestID(),
		PresentedKeyMaterial: presented,
		AccountID:            domain.ProviderAccountID(accountID),
	}
	if _, err := handler.gateway.DeleteProviderAccount(request.Context(), command); err != nil {
		writeGatewayError(writer, err)
		return
	}
	writeNoContent(writer)
}

func (handler providerAccountHandler) list(writer http.ResponseWriter, request *http.Request) {
	presented, _ := bearerMaterial(request)

	query := application.ListProviderAccountsQuery{
		RequestID:            handler.newRequestID(),
		PresentedKeyMaterial: presented,
	}
	result, err := handler.gateway.ListProviderAccounts(request.Context(), query)
	if err != nil {
		writeGatewayError(writer, err)
		return
	}
	writeAccountList(writer, result.Accounts, handler.clock.Now())
}

// bearerMaterial extracts the raw Client API Key bearer string. The value is
// transient and never logged.
func bearerMaterial(request *http.Request) (string, bool) {
	header := request.Header.Get("Authorization")
	if header == "" {
		return "", false
	}
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(header[len(prefix):]), true
}

// readLimitedBody observes the A2 request-size ceiling without buffering an
// unbounded body. It returns the read bytes and an oversize flag; a known
// Content-Length over the max, a read failure, or streamed bytes over the max
// all report oversize. The application spine, not the transport, decides the
// A2 outcome so the normative A0 -> A1 -> A2 order is enforced in one place.
func readLimitedBody(request *http.Request) ([]byte, bool) {
	if request.ContentLength > maxJSONBody {
		return nil, true
	}
	limited := io.LimitReader(request.Body, maxJSONBody+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, true
	}
	if len(body) > maxJSONBody {
		return nil, true
	}
	return body, false
}

// decodeStrictJSON rejects unknown fields and trailing content so a client
// cannot smuggle tenant_id or credential material past the typed contract.
func decodeStrictJSON(body []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("unexpected trailing content")
	}
	return nil
}

type oauthStartRequest struct {
	Purpose        string `json:"purpose"`
	FlowPreference string `json:"flow_preference"`
}

func (handler providerAccountHandler) startOAuth(writer http.ResponseWriter, request *http.Request) {
	requestID := handler.newRequestID()
	presented, _ := bearerMaterial(request)
	accountID := request.PathValue("provider_account_id")
	idempotencyKey := strings.TrimSpace(request.Header.Get("Idempotency-Key"))

	body, oversize := readLimitedBody(request)
	var parsed oauthStartRequest
	malformed := false
	if !oversize {
		if err := decodeStrictJSON(body, &parsed); err != nil {
			malformed = true
		}
	}

	command := application.StartOAuthAuthorizationCommand{
		RequestID:            requestID,
		PresentedKeyMaterial: presented,
		AccountID:            domain.ProviderAccountID(accountID),
		Purpose:              domain.OAuthPurpose(parsed.Purpose),
		FlowPreference:       domain.OAuthFlow(parsed.FlowPreference),
		IdempotencyKey:       idempotencyKey,
		OversizeBody:         oversize,
		MalformedBody:        malformed,
	}
	result, err := handler.gateway.StartOAuthAuthorization(request.Context(), command)
	if err != nil {
		writeGatewayError(writer, err)
		return
	}
	writeOAuthAuthorization(writer, http.StatusAccepted, result.Authorization)
}

func (handler providerAccountHandler) getOAuth(writer http.ResponseWriter, request *http.Request) {
	requestID := handler.newRequestID()
	presented, _ := bearerMaterial(request)
	accountID := request.PathValue("provider_account_id")
	authorizationID := request.PathValue("authorization_id")

	query := application.GetOAuthAuthorizationQuery{
		RequestID:            requestID,
		PresentedKeyMaterial: presented,
		AccountID:            domain.ProviderAccountID(accountID),
		AuthorizationID:      domain.OAuthAuthorizationID(authorizationID),
	}
	result, err := handler.gateway.GetOAuthAuthorization(request.Context(), query)
	if err != nil {
		writeGatewayError(writer, err)
		return
	}
	writeOAuthAuthorization(writer, http.StatusOK, result.Authorization)
}
