// Package httptransport owns Public API parsing, routing, and serialization.
package httptransport

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

type clock interface {
	Now() time.Time
}

type idGenerator interface {
	New(domain.IdentifierKind) (domain.Identifier, error)
}

// Status describes the composition lifecycle without authorizing product work.
type Status interface {
	Healthy() bool
	Ready() bool
}

type statusHandler struct {
	clock  clock
	ids    idGenerator
	status Status
}

type statusResponse struct {
	Status     string `json:"status"`
	RequestID  string `json:"request_id"`
	ObservedAt string `json:"observed_at"`
}

// NewHandler composes the full public HTTP surface: the operational probes and
// the stable /v1 product routes. Composition is the only caller so the route
// table stays owned by the transport layer while wiring stays in composition.
func NewHandler(clock clock, ids idGenerator, status Status, gateway ProviderAccountGateway, assets AssetGateway, capabilities CapabilityGateway) http.Handler {
	mux := http.NewServeMux()
	registerStatusRoutes(mux, clock, ids, status)
	registerProviderAccountRoutes(mux, gateway, clock, ids)
	registerCapabilityRoutes(mux, capabilities, ids)
	registerAssetRoutes(mux, assets, ids)
	return mux
}

// registerStatusRoutes attaches the unversioned operational probes. They never
// authorize product work.
func registerStatusRoutes(mux *http.ServeMux, clock clock, ids idGenerator, status Status) {
	handler := statusHandler{
		clock:  clock,
		ids:    ids,
		status: status,
	}
	mux.HandleFunc("GET /healthz", handler.health)
	mux.HandleFunc("GET /readyz", handler.readiness)
}

func (handler statusHandler) health(writer http.ResponseWriter, _ *http.Request) {
	if !handler.status.Healthy() {
		handler.write(writer, http.StatusServiceUnavailable, "unhealthy")
		return
	}
	handler.write(writer, http.StatusOK, "healthy")
}

func (handler statusHandler) readiness(writer http.ResponseWriter, _ *http.Request) {
	if !handler.status.Ready() {
		handler.write(writer, http.StatusServiceUnavailable, "not_ready")
		return
	}
	handler.write(writer, http.StatusOK, "ready")
}

func (handler statusHandler) write(writer http.ResponseWriter, statusCode int, status string) {
	requestID, err := handler.ids.New(domain.IdentifierKindRequest)
	if err != nil {
		statusCode = http.StatusServiceUnavailable
		status = "unavailable"
	}

	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(statusCode)
	_ = json.NewEncoder(writer).Encode(statusResponse{
		Status:     status,
		RequestID:  string(requestID),
		ObservedAt: handler.clock.Now().UTC().Format(time.RFC3339Nano),
	})
}
