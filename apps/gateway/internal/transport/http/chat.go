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

// chatRequestWire mirrors the published ChatCompletionRequest schema
// (OpenAI-compatible contract). Every schema-declared field is accepted —
// DisallowUnknownFields rejects only fields the schema does not declare.
// `stream` must be absent or false on this non-streaming route (JSON null is
// rejected, distinguishing an absent field from a present null). Generation
// tuning fields (temperature, max_tokens, top_p, n, stop, user, message name)
// are shape-validated at this boundary but not carried into the canonical
// command until real Provider Adapters consume them (T19–T23, decision 0012).
// tenant_id is never accepted here.
type chatRequestWire struct {
	Model       string                 `json:"model"`
	Messages    []chatMessageReq       `json:"messages"`
	Stream      json.RawMessage        `json:"stream"`
	Temperature *float64               `json:"temperature"`
	MaxTokens   *int                   `json:"max_tokens"`
	TopP        *float64               `json:"top_p"`
	N           *int                   `json:"n"`
	Stop        json.RawMessage        `json:"stop"`
	User        string                 `json:"user"`
	Xpixelplus  *chatRequestXpixelplus `json:"x_pixelplus"`
}

// validOptionalFields shape-validates the contract-declared generation fields
// whose values the schema bounds: max_tokens/n are >= 1 when present, and stop
// is a string or an array of strings.
func (wire chatRequestWire) validOptionalFields() bool {
	if wire.MaxTokens != nil && *wire.MaxTokens < 1 {
		return false
	}
	if wire.N != nil && *wire.N < 1 {
		return false
	}
	return validStopWire(wire.Stop)
}

// validStopWire accepts an absent stop, a single string, or a string array
// (the published oneOf). A JSON null is treated as absent.
func validStopWire(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return true
	}
	var many []string
	return json.Unmarshal(raw, &many) == nil
}

// chatRequestXpixelplus is the documented optional routing extension. It never
// accepts tenant_id (DisallowUnknownFields rejects it).
type chatRequestXpixelplus struct {
	ProviderAccountID string `json:"provider_account_id"`
	AllowFallback     bool   `json:"allow_fallback"`
	// ConversationID is the documented affinity key: when the Tenant Routing
	// Policy enables affinity it drives the P3 soft preference for the account
	// that last served this conversation (routing spec §5.1, chat spec §5.2).
	ConversationID string `json:"conversation_id"`
}

type chatMessageReq struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Name    string          `json:"name"`
}

// textContent canonicalizes the published content shapes — a plain string or
// an array of text parts — into the canonical message text. An array holding a
// non-text part is rejected: the canonical non-streaming surface carries text
// only, and silently dropping parts would corrupt the prompt (decision 0012).
func (message chatMessageReq) textContent() (string, bool) {
	if len(message.Content) == 0 {
		return "", false
	}
	var text string
	if err := json.Unmarshal(message.Content, &text); err == nil {
		return text, true
	}
	var parts []chatContentPart
	if err := json.Unmarshal(message.Content, &parts); err != nil {
		return "", false
	}
	var builder strings.Builder
	for _, part := range parts {
		if part.Type != "text" {
			return "", false
		}
		builder.WriteString(part.Text)
	}
	return builder.String(), true
}

// chatContentPart is one array content part; only text parts are canonicalized.
type chatContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
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

	// Contract-declared optional fields are shape-validated; out-of-shape values
	// are the same invalid_request outcome as a malformed body.
	if !malformed && !wire.validOptionalFields() {
		malformed = true
	}

	var providerAccountID domain.ProviderAccountID
	var allowFallback bool
	var conversationID string
	if !malformed && wire.Xpixelplus != nil {
		providerAccountID = domain.ProviderAccountID(wire.Xpixelplus.ProviderAccountID)
		allowFallback = wire.Xpixelplus.AllowFallback
		conversationID = wire.Xpixelplus.ConversationID
	}

	var messages []domain.ChatMessage
	if !malformed {
		var ok bool
		messages, ok = toDomainMessages(wire.Messages)
		malformed = !ok
	}

	command := application.CreateChatCompletionCommand{
		RequestID:            requestID,
		PresentedKeyMaterial: presented,
		IdempotencyKey:       idempotencyKey,
		Model:                wire.Model,
		Messages:             messages,
		ProviderAccountID:    providerAccountID,
		AllowFallback:        allowFallback,
		ConversationID:       conversationID,
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

// toDomainMessages canonicalizes wire messages; ok=false when any content
// shape is outside the published string | text-part-array forms.
func toDomainMessages(messages []chatMessageReq) ([]domain.ChatMessage, bool) {
	out := make([]domain.ChatMessage, 0, len(messages))
	for _, message := range messages {
		content, ok := message.textContent()
		if !ok {
			return nil, false
		}
		out = append(out, domain.ChatMessage{
			Role:    domain.ChatRole(message.Role),
			Content: content,
		})
	}
	return out, true
}
