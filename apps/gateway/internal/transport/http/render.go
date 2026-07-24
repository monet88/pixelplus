package httptransport

import (
	"context"
	"net/http"
	"strings"

	"github.com/monet88/pixelplus/apps/gateway/internal/application"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// RenderGateway is the application seam for image jobs and render-job routes.
type RenderGateway interface {
	CreateImageGeneration(context.Context, application.CreateImageGenerationCommand) (application.RenderJobResult, error)
	CreateImageEdit(context.Context, application.CreateImageEditCommand) (application.RenderJobResult, error)
	CreateImageInpaint(context.Context, application.CreateImageInpaintCommand) (application.RenderJobResult, error)
	GetRenderJob(context.Context, application.GetRenderJobQuery) (application.RenderJobResult, error)
	CancelRenderJob(context.Context, application.CancelRenderJobCommand) (application.RenderJobResult, error)
	RetryRenderJobOutput(context.Context, application.RetryRenderJobOutputCommand) (application.OutputDeliveryResult, error)
}

type renderHandler struct {
	gateway RenderGateway
	ids     idGenerator
}

// registerRenderRoutes attaches the stable image and render-job routes.
func registerRenderRoutes(mux *http.ServeMux, gateway RenderGateway, ids idGenerator) {
	if gateway == nil {
		return
	}
	handler := renderHandler{gateway: gateway, ids: ids}
	mux.HandleFunc("POST /v1/images/generations", handler.createGeneration)
	mux.HandleFunc("POST /v1/images/edits", handler.createEdit)
	mux.HandleFunc("POST /v1/images/inpaints", handler.createInpaint)
	mux.HandleFunc("GET /v1/render-jobs/{job_id}", handler.get)
	mux.HandleFunc("POST /v1/render-jobs/{job_id}/cancel", handler.cancel)
	mux.HandleFunc("POST /v1/render-jobs/{job_id}/outputs/{output_entry_id}/retry", handler.retryOutput)
}

func (handler renderHandler) newRequestID() domain.Identifier {
	id, err := handler.ids.New(domain.IdentifierKindRequest)
	if err != nil {
		return ""
	}
	return id
}

type imageCreateBody struct {
	Model        string `json:"model"`
	Prompt       string `json:"prompt"`
	InputAssetID string `json:"input_asset_id"`
	MaskAssetID  string `json:"mask_asset_id"`
}

func (handler renderHandler) createGeneration(writer http.ResponseWriter, request *http.Request) {
	requestID := handler.newRequestID()
	presented, _ := bearerMaterial(request)
	idempotencyKey := strings.TrimSpace(request.Header.Get("Idempotency-Key"))

	body, oversize := readLimitedBody(request)
	var parsed imageCreateBody
	malformed := false
	if !oversize {
		if err := decodeStrictJSON(body, &parsed); err != nil {
			malformed = true
		}
	}

	result, err := handler.gateway.CreateImageGeneration(request.Context(), application.CreateImageGenerationCommand{
		RequestID:            requestID,
		PresentedKeyMaterial: presented,
		Model:                parsed.Model,
		Prompt:               parsed.Prompt,
		IdempotencyKey:       idempotencyKey,
		OversizeBody:         oversize,
		MalformedBody:        malformed,
	})
	if err != nil {
		writeGatewayError(writer, err)
		return
	}
	writeRenderJob(writer, http.StatusAccepted, result.Job)
}

func (handler renderHandler) createEdit(writer http.ResponseWriter, request *http.Request) {
	requestID := handler.newRequestID()
	presented, _ := bearerMaterial(request)
	idempotencyKey := strings.TrimSpace(request.Header.Get("Idempotency-Key"))

	body, oversize := readLimitedBody(request)
	var parsed imageCreateBody
	malformed := false
	if !oversize {
		if err := decodeStrictJSON(body, &parsed); err != nil {
			malformed = true
		}
	}

	result, err := handler.gateway.CreateImageEdit(request.Context(), application.CreateImageEditCommand{
		RequestID:            requestID,
		PresentedKeyMaterial: presented,
		Model:                parsed.Model,
		Prompt:               parsed.Prompt,
		InputAssetID:         domain.AssetID(parsed.InputAssetID),
		IdempotencyKey:       idempotencyKey,
		OversizeBody:         oversize,
		MalformedBody:        malformed,
	})
	if err != nil {
		writeGatewayError(writer, err)
		return
	}
	writeRenderJob(writer, http.StatusAccepted, result.Job)
}

func (handler renderHandler) createInpaint(writer http.ResponseWriter, request *http.Request) {
	requestID := handler.newRequestID()
	presented, _ := bearerMaterial(request)
	idempotencyKey := strings.TrimSpace(request.Header.Get("Idempotency-Key"))

	body, oversize := readLimitedBody(request)
	var parsed imageCreateBody
	malformed := false
	if !oversize {
		if err := decodeStrictJSON(body, &parsed); err != nil {
			malformed = true
		}
	}

	result, err := handler.gateway.CreateImageInpaint(request.Context(), application.CreateImageInpaintCommand{
		RequestID:            requestID,
		PresentedKeyMaterial: presented,
		Model:                parsed.Model,
		Prompt:               parsed.Prompt,
		InputAssetID:         domain.AssetID(parsed.InputAssetID),
		MaskAssetID:          domain.AssetID(parsed.MaskAssetID),
		IdempotencyKey:       idempotencyKey,
		OversizeBody:         oversize,
		MalformedBody:        malformed,
	})
	if err != nil {
		writeGatewayError(writer, err)
		return
	}
	writeRenderJob(writer, http.StatusAccepted, result.Job)
}

func (handler renderHandler) get(writer http.ResponseWriter, request *http.Request) {
	presented, _ := bearerMaterial(request)
	result, err := handler.gateway.GetRenderJob(request.Context(), application.GetRenderJobQuery{
		RequestID:            handler.newRequestID(),
		PresentedKeyMaterial: presented,
		JobID:                domain.Identifier(request.PathValue("job_id")),
	})
	if err != nil {
		writeGatewayError(writer, err)
		return
	}
	writeRenderJob(writer, http.StatusOK, result.Job)
}

func (handler renderHandler) cancel(writer http.ResponseWriter, request *http.Request) {
	presented, _ := bearerMaterial(request)
	result, err := handler.gateway.CancelRenderJob(request.Context(), application.CancelRenderJobCommand{
		RequestID:            handler.newRequestID(),
		PresentedKeyMaterial: presented,
		JobID:                domain.Identifier(request.PathValue("job_id")),
	})
	if err != nil {
		writeGatewayError(writer, err)
		return
	}
	writeRenderJob(writer, http.StatusOK, result.Job)
}

func (handler renderHandler) retryOutput(writer http.ResponseWriter, request *http.Request) {
	presented, _ := bearerMaterial(request)
	// Optional body may carry re_render; it is always forced false by the app.
	body, oversize := readLimitedBody(request)
	if oversize {
		// Still go through application for A0/A1 order with flags via empty parse.
		_ = body
	}

	result, err := handler.gateway.RetryRenderJobOutput(request.Context(), application.RetryRenderJobOutputCommand{
		RequestID:            handler.newRequestID(),
		PresentedKeyMaterial: presented,
		JobID:                domain.Identifier(request.PathValue("job_id")),
		OutputEntryID:        domain.OutputEntryID(request.PathValue("output_entry_id")),
	})
	if err != nil {
		writeGatewayError(writer, err)
		return
	}
	writeRenderJob(writer, http.StatusOK, result.Job)
}
