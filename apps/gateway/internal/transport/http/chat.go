package httptransport

import (
	"context"
	"net/http"
	"strings"

	"github.com/monet88/pixelplus/apps/gateway/internal/application"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// ChatGateway is the application seam for the non-streaming chat completion
// route. Streaming (T16) is a separate surface.
type ChatGateway interface {
	CreateChatCompletion(context.Context, application.CreateChatCompletionCommand) (application.ChatResult, error)
}

type chatHandler struct {
	gateway ChatGateway
	ids     idGenerator
}

// registerChatRoutes attaches the stable non-streaming chat route. The client
// opts out of streaming by omitting/`stream=false`, which this route enforces.
func registerChatRoutes(mux *http.ServeMux, gateway ChatGateway, ids idGenerator) {
	if gateway == nil {
		return
	}
	handler := chatHandler{gateway: gateway, ids: ids}
	mux.HandleFunc("POST /v1/chat/completions", handler.completions)
}

func (handler chatHandler) newRequestID() domain.Identifier {
	id, err := handler.ids.New(domain.IdentifierKindRequest)
	if err != nil {
		return ""
	}
	return id
}

// chatRequestWire mirrors the OpenAI-compatible non-streaming chat completion
// request body. `stream` must be absent or false on this non-streaming route.
type chatRequestWire struct {
	Model    string           `json:"model"`
	Messages []chatMessageReq `json:"messages"`
	Stream   *bool            `json:"stream"`
}

type chatMessageReq struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (handler chatHandler) completions(writer http.ResponseWriter, request *http.Request) {
	requestID := handler.newRequestID()
	presented, _ := bearerMaterial(request)
	idempotencyKey := strings.TrimSpace(request.Header.Get("Idempotency-Key"))

	body, oversize := readLimitedBody(request)
	var wire chatRequestWire
	malformed := decodeStrictJSON(body, &wire) != nil

	// This route is strictly non-streaming. A stream=true request is invalid on
	// the non-streaming surface (T16 owns streaming).
	if !malformed && wire.Stream != nil && *wire.Stream {
		malformed = true
	}

	command := application.CreateChatCompletionCommand{
		RequestID:            requestID,
		PresentedKeyMaterial: presented,
		IdempotencyKey:       idempotencyKey,
		Model:                wire.Model,
		Messages:             toDomainMessages(wire.Messages),
		OversizeBody:         oversize,
		MalformedBody:        malformed,
	}

	result, err := handler.gateway.CreateChatCompletion(request.Context(), command)
	if err != nil {
		writeGatewayError(writer, err)
		return
	}
	writeChatCompletion(writer, http.StatusOK, result)
}

func toDomainMessages(messages []chatMessageReq) []domain.ChatMessage {
	out := make([]domain.ChatMessage, 0, len(messages))
	for _, message := range messages {
		out = append(out, domain.ChatMessage{
			Role:    domain.ChatRole(message.Role),
			Content: message.Content,
		})
	}
	return out
}
