package httptransport

import (
	"bytes"
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
// `stream` must be absent or false on this non-streaming route. Presence-aware
// RawMessage decoding keeps a present JSON null distinct from an absent field:
// the schema declares non-nullable types, so a present null is rejected for
// stream and the numeric options instead of being silently treated as an
// omitted field. Generation tuning fields (temperature, max_tokens, top_p, n,
// stop, user, message name) are shape-validated at this boundary and bound
// into the idempotency fingerprint; the Adapter does not consume them until
// real Provider Adapters land (T19–T23, decision 0012). tenant_id is never
// accepted here.
type chatRequestWire struct {
	Model       string                 `json:"model"`
	Messages    []chatMessageReq       `json:"messages"`
	Stream      json.RawMessage        `json:"stream"`
	Temperature json.RawMessage        `json:"temperature"`
	MaxTokens   json.RawMessage        `json:"max_tokens"`
	TopP        json.RawMessage        `json:"top_p"`
	N           json.RawMessage        `json:"n"`
	Stop        json.RawMessage        `json:"stop"`
	User        string                 `json:"user"`
	Xpixelplus  *chatRequestXpixelplus `json:"x_pixelplus"`
}

// parseOptions shape-validates and canonicalizes the contract-declared
// generation fields: max_tokens/n are >= 1 when present, stop is a string or
// an array of strings without null items, and a present JSON null is rejected
// for the non-nullable numeric options rather than treated as omission.
func (wire chatRequestWire) parseOptions() (domain.ChatRequestOptions, bool) {
	var options domain.ChatRequestOptions
	temperature, ok := parseFloatOption(wire.Temperature)
	if !ok {
		return options, false
	}
	options.Temperature = temperature
	topP, ok := parseFloatOption(wire.TopP)
	if !ok {
		return options, false
	}
	options.TopP = topP
	maxTokens, ok := parseIntOption(wire.MaxTokens)
	if !ok || (maxTokens != nil && *maxTokens < 1) {
		return domain.ChatRequestOptions{}, false
	}
	options.MaxTokens = maxTokens
	n, ok := parseIntOption(wire.N)
	if !ok || (n != nil && *n < 1) {
		return domain.ChatRequestOptions{}, false
	}
	options.N = n
	stop, ok := parseStopWire(wire.Stop)
	if !ok {
		return domain.ChatRequestOptions{}, false
	}
	options.Stop = stop
	options.User = wire.User
	return options, true
}

// parseFloatOption parses one non-nullable numeric option: an absent field is
// nil, a present JSON null or a non-number is invalid (ok=false).
func parseFloatOption(raw json.RawMessage) (*float64, bool) {
	if len(raw) == 0 {
		return nil, true
	}
	if isJSONNull(raw) {
		return nil, false
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false
	}
	return &value, true
}

// parseIntOption parses one non-nullable integer option: an absent field is
// nil, a present JSON null or a non-integer is invalid (ok=false).
func parseIntOption(raw json.RawMessage) (*int, bool) {
	if len(raw) == 0 {
		return nil, true
	}
	if isJSONNull(raw) {
		return nil, false
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false
	}
	return &value, true
}

// isJSONNull reports whether the raw value is the JSON null literal.
func isJSONNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

// parseStopWire canonicalizes the published stop oneOf: an absent stop (a
// present JSON null keeps the documented treated-as-absent behavior), a single
// string normalized to a one-element list, or a string array. Null array items
// are invalid — the schema declares string items, and decoding through
// nullable-aware pointers keeps a null element distinct from an empty string.
func parseStopWire(raw json.RawMessage) ([]string, bool) {
	if len(raw) == 0 || isJSONNull(raw) {
		return nil, true
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}, true
	}
	var many []*string
	if err := json.Unmarshal(raw, &many); err != nil {
		return nil, false
	}
	stop := make([]string, 0, len(many))
	for _, item := range many {
		if item == nil {
			return nil, false
		}
		stop = append(stop, *item)
	}
	return stop, true
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
		if isJSONNull(wire.Stream) {
			malformed = true
		} else if err := json.Unmarshal(wire.Stream, &stream); err != nil || stream {
			malformed = true
		}
	}

	// Contract-declared optional fields are shape-validated and canonicalized;
	// out-of-shape values are the same invalid_request outcome as a malformed
	// body.
	var options domain.ChatRequestOptions
	if !malformed {
		var ok bool
		options, ok = wire.parseOptions()
		malformed = !ok
	}

	if !malformed && wire.Xpixelplus != nil {
		options.ProviderAccountID = domain.ProviderAccountID(wire.Xpixelplus.ProviderAccountID)
		options.AllowFallback = wire.Xpixelplus.AllowFallback
		options.ConversationID = wire.Xpixelplus.ConversationID
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
		Options:              options,
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
			Name:    message.Name,
		})
	}
	return out, true
}
