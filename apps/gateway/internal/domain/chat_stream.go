package domain

import "errors"

// ChatStreamEventType is the canonical, Provider-independent streaming event
// vocabulary (chat lifecycle §4.3, OpenAPI ChatStreamEvent oneOf). Provider
// end markers and framing (OpenAI `[DONE]`, Gemini chunk boundaries, Grok event
// names) are normalized into these types before a client sees them, so no
// Provider-specific sentinel is ever part of the contract.
type ChatStreamEventType string

// Frozen canonical stream event types.
const (
	// ChatStreamOpen signals stream start. Exactly one, first, before content.
	ChatStreamOpen ChatStreamEventType = "open"
	// ChatStreamDelta carries incremental canonical content in generation order.
	ChatStreamDelta ChatStreamEventType = "delta"
	// ChatStreamHeartbeat is a keepalive that MUST NOT carry assistant tokens.
	ChatStreamHeartbeat ChatStreamEventType = "heartbeat"
	// ChatStreamCompleted is the natural-completion terminal.
	ChatStreamCompleted ChatStreamEventType = "completed"
	// ChatStreamFailed is the runtime-failure/timeout terminal.
	ChatStreamFailed ChatStreamEventType = "failed"
	// ChatStreamCanceled is the cancellation/disconnect terminal.
	ChatStreamCanceled ChatStreamEventType = "canceled"
)

// Terminal reports whether the event type ends the stream. Exactly one terminal
// event may be emitted per stream (§4.3 rule 1).
func (eventType ChatStreamEventType) Terminal() bool {
	switch eventType {
	case ChatStreamCompleted, ChatStreamFailed, ChatStreamCanceled:
		return true
	default:
		return false
	}
}

// Valid reports whether the event type is in the frozen vocabulary.
func (eventType ChatStreamEventType) Valid() bool {
	switch eventType {
	case ChatStreamOpen, ChatStreamDelta, ChatStreamHeartbeat:
		return true
	default:
		return eventType.Terminal()
	}
}

// Stream ordering violations. The writer treats these as programming/adapter
// faults: the event is dropped rather than corrupting a client's stream.
var (
	// ErrChatStreamNotOpen rejects content or a terminal before the open event.
	ErrChatStreamNotOpen = errors.New("chat stream is not open")
	// ErrChatStreamAlreadyOpen rejects a second open event.
	ErrChatStreamAlreadyOpen = errors.New("chat stream is already open")
	// ErrChatStreamTerminated rejects any event after the single terminal event:
	// a later delta/heartbeat would place content after the terminal and a later
	// terminal would be a second sentinel (§4.3 rules 1-2).
	ErrChatStreamTerminated = errors.New("chat stream already delivered its terminal event")
	// ErrChatStreamUnknownEvent rejects an event outside the frozen vocabulary.
	ErrChatStreamUnknownEvent = errors.New("chat stream event type is not canonical")
)

// chatStreamState is the ordering state of one stream.
type chatStreamState int

const (
	chatStreamAwaitingOpen chatStreamState = iota
	chatStreamOpened
	chatStreamTerminated
)

// ChatStreamOrder enforces the normative canonical event order for one stream:
// open (once, first) → zero or more delta/heartbeat → exactly one terminal, with
// nothing after it (chat lifecycle §4.3, I-CHAT-STREAM-ORDER).
//
// It is the single authority on what event may be written next. Every emitter
// passes through Admit, so ordering cannot be violated by an adapter, a retry,
// or a late goroutine: the illegal event is refused instead of reaching the
// client. The zero value is a fresh, unopened stream.
type ChatStreamOrder struct {
	state chatStreamState
}

// Admit validates that eventType may be emitted next and advances the order.
// It returns a typed error and leaves the state untouched when the event would
// violate canonical ordering.
func (order *ChatStreamOrder) Admit(eventType ChatStreamEventType) error {
	if !eventType.Valid() {
		return ErrChatStreamUnknownEvent
	}
	if order.state == chatStreamTerminated {
		return ErrChatStreamTerminated
	}
	switch eventType {
	case ChatStreamOpen:
		if order.state != chatStreamAwaitingOpen {
			return ErrChatStreamAlreadyOpen
		}
		order.state = chatStreamOpened
		return nil
	case ChatStreamDelta, ChatStreamHeartbeat:
		if order.state != chatStreamOpened {
			return ErrChatStreamNotOpen
		}
		return nil
	default:
		// Terminal: only an opened stream can be terminated, and only once.
		if order.state != chatStreamOpened {
			return ErrChatStreamNotOpen
		}
		order.state = chatStreamTerminated
		return nil
	}
}

// Opened reports whether the open event has been emitted.
func (order *ChatStreamOrder) Opened() bool {
	return order.state != chatStreamAwaitingOpen
}

// Terminated reports whether the single terminal event has been emitted. A
// terminated stream accepts no further events.
func (order *ChatStreamOrder) Terminated() bool {
	return order.state == chatStreamTerminated
}

// TerminalEventForFinishClass maps a canonical finish class onto its terminal
// event type (chat lifecycle §4.5). Natural completion classes map to
// `completed`, cancellation to `canceled`, and failure — including an
// unrecognized class — to `failed`, so an unmapped outcome fails closed as a
// failure rather than being presented as a successful completion.
func TerminalEventForFinishClass(class FinishClass) ChatStreamEventType {
	switch class {
	case FinishStop, FinishLength, FinishContentFilter:
		return ChatStreamCompleted
	case FinishCanceled:
		return ChatStreamCanceled
	default:
		return ChatStreamFailed
	}
}

// ChatStreamDelta events carry canonical incremental content for one choice.
// Content is the non-secret canonical text fragment; concatenating a stream's
// deltas in arrival order reconstructs the assistant message (§4.3 row 2).
type ChatDelta struct {
	// Index is the choice index the fragment belongs to.
	Index int
	// Content is the incremental canonical text.
	Content string
}

// ChatSink is the Provider-facing streaming sink. It deliberately exposes ONLY
// delta and heartbeat: an Adapter has no method with which to emit the `open`
// event or any terminal event, so no Adapter — drifting, buggy, or hostile —
// can produce a second terminal, a post-terminal delta, or a Provider-specific
// sentinel. The Gateway owns both ends of every stream (§4.1, §4.3).
//
// A returned error means delivery stopped (client gone or stream terminated);
// the Adapter MUST stop producing and return.
type ChatSink interface {
	// Delta delivers one canonical content fragment in generation order.
	Delta(ChatDelta) error
	// Heartbeat delivers one keepalive that carries no assistant tokens.
	Heartbeat() error
}

// ChatStreamOutcome is the safe, Provider-independent result of one streaming
// Adapter attempt. It never carries raw Provider payloads or credential
// material. The Adapter reports what it observed; the Gateway decides which
// terminal event the client sees.
type ChatStreamOutcome struct {
	// Class classifies the attempt (committed / not_committed / unknown).
	Class ChatOutcomeClass
	// Commit is the authoritative commit proof. CommitNotCommitted is the only
	// value that authorizes a fallback re-attempt; CommitUnknown fails closed.
	Commit CommitStatus
	// FinishClass is the canonical terminal classification on a committed
	// stream (stop / length / content_filter / canceled).
	FinishClass FinishClass
	// Usage is the canonical token accounting the Adapter observed.
	Usage ChatUsage
	// FailureClass is a safe canonical class when not committed / unknown.
	FailureClass ErrorCode
	// UpstreamAbortAttempted reports whether the Adapter actually attempted an
	// upstream abort for this attempt. It is an Adapter OBSERVATION, never an
	// inference from the terminal class: §6.2 rule 4 forbids claiming an abort
	// that did not happen ("The Gateway MUST NOT claim it was aborted"), and
	// §6.2 rule 2 only requires the attempt where the Auth Mode/Adapter supports
	// abort. A non-cancelable Adapter therefore leaves this false.
	UpstreamAbortAttempted bool
	// UpstreamStopConfirmed reports a CONFIRMED upstream stop. Cancellation is
	// never proof of a stop by itself (§6.2 rule 3: occupancy releases only when
	// cancel completion confirms upstream stopped), so this stays false unless
	// the Adapter proved it.
	UpstreamStopConfirmed bool
}
