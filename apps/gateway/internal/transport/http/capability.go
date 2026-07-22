package httptransport

import (
	"context"
	"net/http"

	"github.com/monet88/pixelplus/apps/gateway/internal/application"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// CapabilityGateway is the application seam for Capability Snapshot reads and
// the offerable model list. It is a narrow local interface so transport never
// imports concrete application wiring beyond command/query types.
type CapabilityGateway interface {
	GetCapabilitySnapshot(context.Context, application.GetCapabilitySnapshotQuery) (application.CapabilitySnapshotResult, error)
	ListModels(context.Context, application.ListModelsQuery) (application.ModelListResult, error)
}

type capabilityHandler struct {
	gateway CapabilityGateway
	ids     idGenerator
}

// registerCapabilityRoutes attaches the stable capability snapshot and models
// routes. Snapshot reads hang under provider accounts; models is a Tenant-wide
// list at /v1/models.
func registerCapabilityRoutes(mux *http.ServeMux, gateway CapabilityGateway, ids idGenerator) {
	handler := capabilityHandler{gateway: gateway, ids: ids}
	mux.HandleFunc("GET /v1/provider-accounts/{provider_account_id}/capability-snapshot", handler.getSnapshot)
	mux.HandleFunc("GET /v1/models", handler.listModels)
}

func (handler capabilityHandler) newRequestID() domain.Identifier {
	id, err := handler.ids.New(domain.IdentifierKindRequest)
	if err != nil {
		return ""
	}
	return id
}

func (handler capabilityHandler) getSnapshot(writer http.ResponseWriter, request *http.Request) {
	presented, _ := bearerMaterial(request)
	accountID := request.PathValue("provider_account_id")

	query := application.GetCapabilitySnapshotQuery{
		RequestID:            handler.newRequestID(),
		PresentedKeyMaterial: presented,
		AccountID:            domain.ProviderAccountID(accountID),
	}
	result, err := handler.gateway.GetCapabilitySnapshot(request.Context(), query)
	if err != nil {
		writeGatewayError(writer, err)
		return
	}
	writeCapabilitySnapshot(writer, http.StatusOK, result.Snapshot)
}

func (handler capabilityHandler) listModels(writer http.ResponseWriter, request *http.Request) {
	presented, _ := bearerMaterial(request)

	query := application.ListModelsQuery{
		RequestID:            handler.newRequestID(),
		PresentedKeyMaterial: presented,
	}
	result, err := handler.gateway.ListModels(request.Context(), query)
	if err != nil {
		writeGatewayError(writer, err)
		return
	}
	writeModelList(writer, result.Offers)
}
