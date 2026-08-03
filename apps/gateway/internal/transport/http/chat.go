package httptransport

import (
	"context"
	"encoding/json"
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
// request body. `stream` must be absent or false on this non-streaming route
// (JSON null is rejected, distinguishing an absent field from a present null).
// Optional x_pixelplus routing fields (provider_account_id, allow_fallback,
// conversation_id) are carried rather than rejected as unknown (OpenAI-compatible
// contract §3.5). tenant_id is never accepted here.
type chatRequestWire struct {
	Model      string                  `json:"model"`
	Messages   []chatMessageReq        `json:"messages"`
	Stream     json.RawMessage         `json:"stream"`
	Xpixelplus *chatRequestXpixelplus  `json:"x_pixelplus"`
}

// chatRequestXpixelplus is the documented optional routing extension. It never
// accepts tenant_id (DisallowUnknownFields rejects it).
type chatRequestXpixelplus struct {
	ProviderAccountID string `json:"provider_account_id"`
	AllowFallback     bool   `json:"allow_fallback"`
	// ConversationID is the documented affinity-key hint; it is accepted and
	// carried without changing routing behavior in the non-streaming surface.
	ConversationID string `json:"conversation_id"`
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

	// This route is strictly non-streaming (T16 owns streaming). `stream` must be
	// absent or false; a present true, a non-boolean, or a JSON null are invalid.
	// RawMessage keeps "stream": null distinct from an absent field so null is
	// rejected rather than silently executed as non-streaming.
	if !malformed && len(wire.Stream) > 0 {
		var stream bool
		if err := json.Unmarshal(wire.Stream, &stream); err != nil || stream {
			malformed = true
		}
	}

	var providerAccountID domain.ProviderAccountID
	var allowFallback bool
	if !malformed && wire.Xpixelplus != nil {
		providerAccountID = domain.ProviderAccountID(wire.Xpixelplus.ProviderAccountID)
		allowFallback = wire.Xpixelplus.AllowFallback
	}

	command := application.CreateChatCompletionCommand{
		RequestID:            requestID,
		PresentedKeyMaterial: presented,
		IdempotencyKey:       idempotencyKey,
		Model:                wire.Model,
		Messages:             toDomainMessages(wire.Messages),
		ProviderAccountID:    providerAccountID,
		AllowFallback:        allowFallback,
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
