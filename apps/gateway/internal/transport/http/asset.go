package httptransport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/monet88/pixelplus/apps/gateway/internal/application"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// maxAssetUpload is the L-ASSET-UPLOAD-MAX request-size ceiling (20 MiB)
// enforced at the transport boundary as admission step A2 for the multipart
// upload surface. It is deliberately distinct from maxJSONBody (2 MiB): an
// image upload is bounded by a different limit than a JSON control request
// (#13 section 4.2.1, #8 section 7.2).
const maxAssetUpload = 20 << 20

// AssetGateway is the application seam the transport calls for the immutable
// Asset exchange surface. It is a narrow local interface so the transport
// package never imports concrete application wiring beyond the command/query
// types (mirrors ProviderAccountGateway).
type AssetGateway interface {
	CreateAsset(context.Context, application.CreateAssetCommand) (application.AssetResult, error)
	GetAsset(context.Context, application.GetAssetQuery) (application.AssetResult, error)
	GetAssetContent(context.Context, application.GetAssetContentQuery) (application.AssetContentResult, error)
}

type assetHandler struct {
	gateway AssetGateway
	ids     idGenerator
}

// registerAssetRoutes attaches the stable /v1 Asset routes to the mux. The
// server base is /v1, matching the frozen OpenAPI server url.
func registerAssetRoutes(mux *http.ServeMux, gateway AssetGateway, ids idGenerator) {
	handler := assetHandler{gateway: gateway, ids: ids}
	mux.HandleFunc("POST /v1/assets", handler.create)
	mux.HandleFunc("GET /v1/assets/{asset_id}", handler.get)
	mux.HandleFunc("GET /v1/assets/{asset_id}/content", handler.getContent)
}

func (handler assetHandler) newRequestID() domain.Identifier {
	id, err := handler.ids.New(domain.IdentifierKindRequest)
	if err != nil {
		return ""
	}
	return id
}

func (handler assetHandler) create(writer http.ResponseWriter, request *http.Request) {
	requestID := handler.newRequestID()
	presented, _ := bearerMaterial(request)
	idempotencyKey := strings.TrimSpace(request.Header.Get("Idempotency-Key"))

	// The transport observes the A2 request-size and multipart-parse outcomes
	// but does not short-circuit on them. It forwards them as flags so the
	// application spine enforces the single normative order A0 auth -> A1 scope
	// -> A2 size -> request validation. An unauthenticated oversize or malformed
	// upload therefore still fails as authentication_failed, never leaking a
	// distinguishable 413/400 before 401 (#8 section 6, #13 section 4.2.1).
	kind, content, oversize, malformed := readAssetUpload(request)

	command := application.CreateAssetCommand{
		RequestID:            requestID,
		PresentedKeyMaterial: presented,
		Kind:                 domain.AssetKind(kind),
		DeclaredType:         detectDeclaredType(request, content),
		Content:              content,
		IdempotencyKey:       idempotencyKey,
		OversizeBody:         oversize,
		MalformedBody:        malformed,
	}

	result, err := handler.gateway.CreateAsset(request.Context(), command)
	if err != nil {
		writeGatewayError(writer, err)
		return
	}
	writeAsset(writer, http.StatusCreated, result.Asset)
}

func (handler assetHandler) get(writer http.ResponseWriter, request *http.Request) {
	presented, _ := bearerMaterial(request)
	assetID := request.PathValue("asset_id")

	query := application.GetAssetQuery{
		RequestID:            handler.newRequestID(),
		PresentedKeyMaterial: presented,
		AssetID:              domain.AssetID(assetID),
	}
	result, err := handler.gateway.GetAsset(request.Context(), query)
	if err != nil {
		writeGatewayError(writer, err)
		return
	}
	writeAsset(writer, http.StatusOK, result.Asset)
}

func (handler assetHandler) getContent(writer http.ResponseWriter, request *http.Request) {
	presented, _ := bearerMaterial(request)
	assetID := request.PathValue("asset_id")

	query := application.GetAssetContentQuery{
		RequestID:            handler.newRequestID(),
		PresentedKeyMaterial: presented,
		AssetID:              domain.AssetID(assetID),
	}
	result, err := handler.gateway.GetAssetContent(request.Context(), query)
	if err != nil {
		writeGatewayError(writer, err)
		return
	}
	writeAssetContent(writer, result)
}

// readAssetUpload parses the multipart upload into a kind and file bytes while
// observing the A2 upload-size ceiling without buffering an unbounded body. It
// returns the read kind and content plus oversize/malformed flags; a known
// Content-Length over the max, a body that streams past the max, or a parse
// failure are reported so the application spine, not the transport, decides the
// A2 and request-validation outcomes in the single normative order.
func readAssetUpload(request *http.Request) (kind string, content []byte, oversize bool, malformed bool) {
	if request.ContentLength > maxAssetUpload {
		return "", nil, true, false
	}
	request.Body = http.MaxBytesReader(nil, request.Body, maxAssetUpload)
	if err := request.ParseMultipartForm(maxAssetUpload); err != nil {
		if isMaxBytesError(err) {
			return "", nil, true, false
		}
		return "", nil, false, true
	}

	kind = request.FormValue("kind")
	file, _, err := request.FormFile("file")
	if err != nil {
		return "", nil, false, true
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(file)
	if err != nil {
		if isMaxBytesError(err) {
			return "", nil, true, false
		}
		return "", nil, false, true
	}
	return kind, data, false, false
}

// isMaxBytesError reports whether the error came from the A2 size limiter so an
// oversize body is classified as request_too_large rather than a malformed
// parse.
func isMaxBytesError(err error) bool {
	var maxErr *http.MaxBytesError
	return errors.As(err, &maxErr)
}

// detectDeclaredType resolves the client-declared media type for the uploaded
// file. The multipart part Content-Type is the declared type; when a client
// omits it, the sniffed content type stands in so a well-formed upload with a
// missing part header is still validated against its actual bytes rather than
// rejected as unsupported. The domain still cross-checks the declared type
// against the decoded content (smuggling defense, #13 section 4.1).
func detectDeclaredType(request *http.Request, content []byte) string {
	if request.MultipartForm != nil {
		if files := request.MultipartForm.File["file"]; len(files) > 0 {
			if declared := files[0].Header.Get("Content-Type"); declared != "" {
				return declared
			}
		}
	}
	if len(content) == 0 {
		return ""
	}
	return http.DetectContentType(content)
}
