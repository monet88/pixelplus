package httptransport

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/monet88/pixelplus/apps/gateway/internal/application"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// SSE event payload wires. Each mirrors one member of the published
// ChatStreamEvent oneOf. `additionalProperties: false` in the schema means these
// structs must not gain fields that the schema does not declare.
type chatOpenEventWire struct {
	Type       string               `json:"type"`
	ID         string               `json:"id"`
	Model      string               `json:"model"`
	Created    int64                `json:"created"`
	Xpixelplus chatSafeMetadataWire `json:"x_pixelplus"`
}

type chatDeltaEventWire struct {
	Type    string                `json:"type"`
	ID      string                `json:"id"`
	Choices []chatDeltaChoiceWire `json:"choices"`
}

type chatDeltaChoiceWire struct {
	Index int               `json:"index"`
	Delta chatDeltaBodyWire `json:"delta"`
}

type chatDeltaBodyWire struct {
	Content string `json:"content"`
}

type chatHeartbeatEventWire struct {
	Type string `json:"type"`
	TS   int64  `json:"ts"`
}

type chatCompletedEventWire struct {
	Type        string               `json:"type"`
	ID          string               `json:"id"`
	FinishClass string               `json:"finish_class"`
	Usage       usageWire            `json:"usage"`
	Xpixelplus  chatSafeMetadataWire `json:"x_pixelplus"`
}

type chatFailedEventWire struct {
	Type        string               `json:"type"`
	ID          string               `json:"id"`
	FinishClass string               `json:"finish_class"`
	Error       canonicalErrorWire   `json:"error"`
	Xpixelplus  chatSafeMetadataWire `json:"x_pixelplus"`
}

type chatCanceledEventWire struct {
	Type        string               `json:"type"`
	ID          string               `json:"id"`
	FinishClass string               `json:"finish_class"`
	Xpixelplus  chatSafeMetadataWire `json:"x_pixelplus"`
}

// chatSafeMetadataWire is the ChatSafeMetadata projection: server-owned safe
// ids and classifications only. It never carries credential material, raw
// Provider shapes, prompt content, or foreign-Tenant ids.
type chatSafeMetadataWire struct {
	RequestID              string `json:"request_id"`
	ExecutionID            string `json:"execution_id"`
	ProviderAccountID      string `json:"provider_account_id,omitempty"`
	FinishClass            string `json:"finish_class,omitempty"`
	StreamingClass         string `json:"streaming_class,omitempty"`
	UpstreamAbortAttempted *bool  `json:"upstream_abort_attempted,omitempty"`
	UpstreamStopConfirmed  *bool  `json:"upstream_stop_confirmed,omitempty"`
}

// sseStream is the transport-owned canonical SSE writer for one chat stream.
//
// It is the single place stream bytes are produced, and every write passes
// through domain.ChatStreamOrder. That is what makes the ordering invariant
// structural: an out-of-order write (second open, delta after terminal, second
// terminal) is refused here and never reaches the client, regardless of what the
// application or an Adapter attempts. There is deliberately NO `[DONE]`
// sentinel — the terminal event is authoritative (chat lifecycle §4.1/§4.3).
type sseStream struct {
	writer  http.ResponseWriter
	flusher http.Flusher
	clock   clock

	// mu guards the order state, delta count, and the writer. A streaming
	// Adapter may deliver from a different goroutine than the one that writes
	// the terminal, so serialization here is what keeps event framing intact.
	mu     sync.Mutex
	order  domain.ChatStreamOrder
	deltas int
	// executionID/requestID are captured at open so later events carry stable
	// safe metadata without the Adapter supplying any identity.
	executionID string
	requestID   string
}

// newSSEStream prepares the response for canonical event streaming. It returns
// ok=false when the ResponseWriter cannot flush, because an unflushable writer
// would buffer the whole stream and silently break incremental delivery.
func newSSEStream(writer http.ResponseWriter, clk clock) (*sseStream, bool) {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		return nil, false
	}
	return &sseStream{writer: writer, flusher: flusher, clock: clk}, true
}

// Open writes the single canonical open event and commits the 200 status with
// the streaming media type. Because the status line is committed here, every
// pre-upstream rejection must have already been decided by the application.
func (stream *sseStream) Open(handshake application.ChatStreamHandshake) error {
	stream.mu.Lock()
	defer stream.mu.Unlock()

	if err := stream.order.Admit(domain.ChatStreamOpen); err != nil {
		return err
	}
	stream.executionID = string(handshake.ExecutionID)
	stream.requestID = string(handshake.RequestID)

	stream.writer.Header().Set("Content-Type", "text/event-stream")
	stream.writer.Header().Set("Cache-Control", "no-store")
	stream.writer.Header().Set("Connection", "keep-alive")
	// Defeat proxy buffering that would batch events and destroy incremental
	// delivery for a real-streaming mode.
	stream.writer.Header().Set("X-Accel-Buffering", "no")
	stream.writer.WriteHeader(http.StatusOK)

	return stream.write(chatOpenEventWire{
		Type:    string(domain.ChatStreamOpen),
		ID:      string(handshake.ExecutionID),
		Model:   handshake.Model,
		Created: stream.clock.Now().UTC().Unix(),
		Xpixelplus: chatSafeMetadataWire{
			RequestID:         string(handshake.RequestID),
			ExecutionID:       string(handshake.ExecutionID),
			ProviderAccountID: string(handshake.ProviderAccountID),
			StreamingClass:    string(handshake.StreamingClass),
		},
	})
}

// Sink returns the delta/heartbeat-only sink for the Provider Adapter.
func (stream *sseStream) Sink() domain.ChatSink {
	return stream
}

// Opened reports whether the canonical open event was written, and therefore
// whether the HTTP status is already committed to a 200 text/event-stream
// response.
//
// The handler needs this because it cannot otherwise tell a pre-stream rejection
// (safe to answer with a canonical HTTP error) from a failure raised after the
// stream was already committed. Writing an HTTP error in the latter case would
// attempt a second WriteHeader on a committed response and append a JSON error
// body to the open SSE frame, corrupting the stream for the client.
func (stream *sseStream) Opened() bool {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.order.Opened()
}

// Delta writes one canonical content event in arrival order. Ordering is
// preserved and content is never merged or reordered, so concatenating the
// delivered deltas reconstructs the assistant message (§4.3 rule 4).
func (stream *sseStream) Delta(delta domain.ChatDelta) error {
	stream.mu.Lock()
	defer stream.mu.Unlock()

	if err := stream.order.Admit(domain.ChatStreamDelta); err != nil {
		return err
	}
	if err := stream.write(chatDeltaEventWire{
		Type: string(domain.ChatStreamDelta),
		ID:   stream.executionID,
		Choices: []chatDeltaChoiceWire{{
			Index: delta.Index,
			Delta: chatDeltaBodyWire{Content: delta.Content},
		}},
	}); err != nil {
		return err
	}
	stream.deltas++
	return nil
}

// Heartbeat writes one keepalive event. It carries no assistant tokens by
// construction: the wire type has no content field (§4.3 row 3).
func (stream *sseStream) Heartbeat() error {
	stream.mu.Lock()
	defer stream.mu.Unlock()

	if err := stream.order.Admit(domain.ChatStreamHeartbeat); err != nil {
		return err
	}
	return stream.write(chatHeartbeatEventWire{
		Type: string(domain.ChatStreamHeartbeat),
		TS:   stream.clock.Now().UTC().Unix(),
	})
}

// Terminal writes the single canonical terminal event. A second call is refused
// by the ordering state machine, so no stream can carry two sentinels.
func (stream *sseStream) Terminal(terminal application.ChatStreamTerminal) error {
	stream.mu.Lock()
	defer stream.mu.Unlock()

	event := terminal.Event
	if !event.Terminal() {
		event = domain.ChatStreamFailed
	}
	if err := stream.order.Admit(event); err != nil {
		return err
	}

	metadata := chatSafeMetadataWire{
		RequestID:   stream.requestID,
		ExecutionID: stream.executionID,
		FinishClass: string(terminal.FinishClass),
	}

	switch event {
	case domain.ChatStreamCompleted:
		return stream.write(chatCompletedEventWire{
			Type:        string(domain.ChatStreamCompleted),
			ID:          stream.executionID,
			FinishClass: string(terminal.FinishClass),
			Usage: usageWire{
				PromptTokens:     terminal.Usage.PromptTokens,
				CompletionTokens: terminal.Usage.CompletionTokens,
				TotalTokens:      terminal.Usage.PromptTokens + terminal.Usage.CompletionTokens,
			},
			Xpixelplus: metadata,
		})
	case domain.ChatStreamCanceled:
		abortAttempted := terminal.UpstreamAbortAttempted
		stopConfirmed := terminal.UpstreamStopConfirmed
		metadata.FinishClass = string(domain.FinishCanceled)
		metadata.UpstreamAbortAttempted = &abortAttempted
		metadata.UpstreamStopConfirmed = &stopConfirmed
		return stream.write(chatCanceledEventWire{
			Type:        string(domain.ChatStreamCanceled),
			ID:          stream.executionID,
			FinishClass: string(domain.FinishCanceled),
			Xpixelplus:  metadata,
		})
	default:
		canonical := terminal.Error
		if canonical.Code == "" {
			canonical = domain.NewInternalError()
		}
		metadata.FinishClass = string(domain.FinishFailed)
		return stream.write(chatFailedEventWire{
			Type:        string(domain.ChatStreamFailed),
			ID:          stream.executionID,
			FinishClass: string(domain.FinishFailed),
			Error:       toCanonicalErrorWire(canonical),
			Xpixelplus:  metadata,
		})
	}
}

// DeltaCount reports how many delta events already reached the client.
func (stream *sseStream) DeltaCount() int {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.deltas
}

// write serializes one event as a single SSE `data:` frame and flushes it so the
// client observes incremental delivery. Callers must hold mu.
func (stream *sseStream) write(payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := stream.writer.Write([]byte("data: ")); err != nil {
		return err
	}
	if _, err := stream.writer.Write(encoded); err != nil {
		return err
	}
	if _, err := stream.writer.Write([]byte("\n\n")); err != nil {
		return err
	}
	stream.flusher.Flush()
	return nil
}

var (
	_ application.ChatStreamTransport = (*sseStream)(nil)
	_ domain.ChatSink                 = (*sseStream)(nil)
)
