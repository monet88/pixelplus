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

// ChatGateway is the application seam for the chat completion route. The same
// published route serves both branches: the client selects streaming with
// `stream: true`, which the handler dispatches to StreamChat (T16) while
// `stream` absent/false dispatches to CreateChatCompletion (T15).
type ChatGateway interface {
	CreateChatCompletion(context.Context, application.CreateChatCompletionCommand) (application.ChatResult, error)
	StreamChat(context.Context, application.StreamChatCommand, application.ChatStreamTransport) error
	CancelChatExecution(context.Context, application.CancelChatExecutionCommand) (application.ChatCancelResult, error)
}

type chatHandler struct {
	gateway ChatGateway
	ids     idGenerator
	clock   clock
}

// registerChatRoutes attaches the stable chat route. One route serves both the
// non-streaming JSON completion and the canonical SSE stream, as published.
func registerChatRoutes(mux *http.ServeMux, gateway ChatGateway, ids idGenerator, clk clock) {
	if gateway == nil {
		return
	}
	handler := chatHandler{gateway: gateway, ids: ids, clock: clk}
	mux.HandleFunc("POST /v1/chat/completions", handler.completions)
	mux.HandleFunc("POST /v1/chat/executions/{execution_id}/cancel", handler.cancelExecution)
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
// `stream` selects the response branch: absent/false yields the canonical JSON
// completion, true yields the canonical SSE stream. Presence-aware RawMessage
// decoding keeps a present JSON null distinct from an absent field: the schema
// declares non-nullable types, so a present null is rejected for stream and the
// numeric options instead of being silently treated as an omitted field.
// Generation tuning fields (temperature, max_tokens, top_p, n, stop, user,
// message name) are shape-validated at this boundary and bound into the
// idempotency fingerprint; the Adapter does not consume them until real
// Provider Adapters land (T19–T23, decision 0012). tenant_id is never accepted
// here.
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

	// `stream` selects the branch. A present JSON null or non-boolean is invalid
	// rather than silently treated as non-streaming: RawMessage keeps
	// "stream": null distinct from an absent field, and the schema declares a
	// non-nullable boolean.
	streaming := false
	if !malformed && len(wire.Stream) > 0 {
		if isJSONNull(wire.Stream) {
			malformed = true
		} else if err := json.Unmarshal(wire.Stream, &streaming); err != nil {
			malformed = true
			streaming = false
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

	if streaming {
		handler.stream(writer, request, streamChatCommand{
			requestID:      requestID,
			presented:      presented,
			idempotencyKey: idempotencyKey,
			wire:           wire,
			messages:       messages,
			options:        options,
			oversize:       oversize,
			malformed:      malformed,
		})
		return
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

// streamChatCommand carries the parsed request through to the streaming branch
// so the parse/validate logic stays shared with the non-streaming branch.
type streamChatCommand struct {
	requestID      domain.Identifier
	presented      string
	idempotencyKey string
	wire           chatRequestWire
	messages       []domain.ChatMessage
	options        domain.ChatRequestOptions
	oversize       bool
	malformed      bool
}

// stream serves the canonical SSE branch. Pre-upstream rejections are returned
// as real HTTP status codes because no stream has been opened yet; once
// StreamChat opens the stream the status is committed and every outcome is
// delivered as exactly one canonical terminal event.
func (handler chatHandler) stream(writer http.ResponseWriter, request *http.Request, parsed streamChatCommand) {
	stream, ok := newSSEStream(writer, handler.clock)
	if !ok {
		// The response writer cannot flush, so incremental delivery is
		// impossible. Fail closed rather than buffer a fake "stream".
		writeCanonical(writer, domain.NewDependencyUnavailable().WithRequestID(parsed.requestID))
		return
	}

	err := handler.gateway.StreamChat(request.Context(), application.StreamChatCommand{
		RequestID:            parsed.requestID,
		PresentedKeyMaterial: parsed.presented,
		IdempotencyKey:       parsed.idempotencyKey,
		Model:                parsed.wire.Model,
		Messages:             parsed.messages,
		Options:              parsed.options,
		OversizeBody:         parsed.oversize,
		MalformedBody:        parsed.malformed,
	}, stream)
	if err != nil {
		if stream.Opened() {
			// Defence in depth: StreamChat's contract is that a non-nil error means a
			// PRE-stream rejection, and every post-open outcome is delivered as the
			// single terminal event. If that contract is ever broken, writing an HTTP
			// error here would attempt a second WriteHeader on an already-committed
			// 200 and append a JSON body to the open SSE frame, corrupting the stream.
			// The client already holds an open stream, so the least-harmful action is
			// to write nothing further and let the terminal/EOF stand.
			return
		}
		// Pre-stream rejection: nothing was written, so a canonical HTTP error is
		// still expressible and no partial stream is left behind.
		writeGatewayError(writer, err)
	}
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

// chatCancelResponseWire mirrors the published ChatCancelResponse schema
// (additionalProperties: false). The fields are exactly the schema-declared
// ones: no extra fields are added.
type chatCancelResponseWire struct {
	ExecutionID            string `json:"execution_id"`
	CancelState            string `json:"cancel_state"`
	UpstreamAbortAttempted bool   `json:"upstream_abort_attempted"`
	UpstreamStopConfirmed  bool   `json:"upstream_stop_confirmed"`
	RequestID              string `json:"request_id,omitempty"`
}

// cancelExecution handles POST /v1/chat/executions/{execution_id}/cancel.
// It authenticates, resolves the in-flight execution under same-Tenant
// ownership, signals it when possible, and returns the honest acknowledgement
// (chat lifecycle §6.2).
func (handler chatHandler) cancelExecution(writer http.ResponseWriter, request *http.Request) {
	presented, _ := bearerMaterial(request)
	result, err := handler.gateway.CancelChatExecution(request.Context(), application.CancelChatExecutionCommand{
		RequestID:            handler.newRequestID(),
		PresentedKeyMaterial: presented,
		ExecutionID:          domain.Identifier(request.PathValue("execution_id")),
	})
	if err != nil {
		writeGatewayError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, chatCancelResponseWire{
		ExecutionID:            string(result.ExecutionID),
		CancelState:            result.CancelState,
		UpstreamAbortAttempted: result.UpstreamAbortAttempted,
		UpstreamStopConfirmed:  result.UpstreamStopConfirmed,
		RequestID:              string(result.RequestID),
	})
}
