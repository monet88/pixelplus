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
}

type providerAccountHandler struct {
	gateway ProviderAccountGateway
	ids     idGenerator
}

// registerProviderAccountRoutes attaches the stable /v1 Provider Account routes
// to the mux. The server base is /v1, matching the frozen OpenAPI server url.
func registerProviderAccountRoutes(mux *http.ServeMux, gateway ProviderAccountGateway, ids idGenerator) {
	handler := providerAccountHandler{gateway: gateway, ids: ids}
	mux.HandleFunc("POST /v1/provider-accounts", handler.create)
	mux.HandleFunc("GET /v1/provider-accounts", handler.list)
	mux.HandleFunc("GET /v1/provider-accounts/{provider_account_id}", handler.get)
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
	writeAccountOperation(writer, http.StatusCreated, result)
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
	writeAccount(writer, http.StatusOK, result.Account)
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
	writeAccountList(writer, result.Accounts)
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
