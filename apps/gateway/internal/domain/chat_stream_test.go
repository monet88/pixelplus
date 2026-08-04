package domain

import (
	"testing"
)

// AC: the streaming operation resolves the chat_streaming capability operation,
// never the non-streaming chat one. A snapshot that verifies `chat` but not
// `chat_streaming` must therefore never satisfy a stream request (capability
// spec §3.1: chat_streaming is a separate operation, not a flag).
func TestChatStreamingOperationMapsToStreamingCapability(t *testing.T) {
	if !ChatOpCompletionStreaming.Valid() {
		t.Fatalf("ChatOpCompletionStreaming should be valid")
	}
	if got := ChatOpCompletionStreaming.CapabilityOperation(); got != CapabilityOpChatStreaming {
		t.Fatalf("streaming operation maps to %q, want %q", got, CapabilityOpChatStreaming)
	}
	if ChatOpCompletionStreaming.CapabilityOperation() == ChatOpCompletion.CapabilityOperation() {
		t.Fatalf("streaming and non-streaming operations must not share one capability operation")
	}
	if got := ChatOpCompletionStreaming.RequiredScope(); got != ScopeChatCompletions {
		t.Fatalf("streaming operation requires scope %q, want %q", got, ScopeChatCompletions)
	}
}

// AC: exactly one open event opens the stream, and it must precede content.
func TestChatStreamOrderRequiresOpenBeforeContent(t *testing.T) {
	var order ChatStreamOrder

	if err := order.Admit(ChatStreamDelta); err == nil {
		t.Fatalf("a delta before open must be rejected")
	}
	if err := order.Admit(ChatStreamHeartbeat); err == nil {
		t.Fatalf("a heartbeat before open must be rejected")
	}
	if err := order.Admit(ChatStreamOpen); err != nil {
		t.Fatalf("first open rejected: %v", err)
	}
	if err := order.Admit(ChatStreamOpen); err == nil {
		t.Fatalf("a second open must be rejected (exactly one, first)")
	}
	if err := order.Admit(ChatStreamDelta); err != nil {
		t.Fatalf("delta after open rejected: %v", err)
	}
	if err := order.Admit(ChatStreamHeartbeat); err != nil {
		t.Fatalf("heartbeat after open rejected: %v", err)
	}
}

// AC: exactly one terminal ends the stream; nothing may follow it — no delta, no
// heartbeat, and no second terminal of any class (chat lifecycle §4.3 rules
// 1–2, I-CHAT-STREAM-ORDER).
func TestChatStreamOrderAdmitsExactlyOneTerminal(t *testing.T) {
	terminals := []ChatStreamEventType{ChatStreamCompleted, ChatStreamFailed, ChatStreamCanceled}

	for _, terminal := range terminals {
		var order ChatStreamOrder
		if err := order.Admit(ChatStreamOpen); err != nil {
			t.Fatalf("%s: open rejected: %v", terminal, err)
		}
		if err := order.Admit(ChatStreamDelta); err != nil {
			t.Fatalf("%s: delta rejected: %v", terminal, err)
		}
		if err := order.Admit(terminal); err != nil {
			t.Fatalf("%s: terminal rejected: %v", terminal, err)
		}
		if !order.Terminated() {
			t.Fatalf("%s: order should report terminated", terminal)
		}
		if err := order.Admit(ChatStreamDelta); err == nil {
			t.Fatalf("%s: delta after terminal must be rejected (no content after terminal)", terminal)
		}
		if err := order.Admit(ChatStreamHeartbeat); err == nil {
			t.Fatalf("%s: heartbeat after terminal must be rejected", terminal)
		}
		for _, second := range terminals {
			if err := order.Admit(second); err == nil {
				t.Fatalf("%s: second terminal %s must be rejected (exactly one terminal)", terminal, second)
			}
		}
	}
}

// AC: a terminal may close a stream that carried zero deltas (§4.3 row 2 allows
// zero or more), but never a stream that never opened.
func TestChatStreamOrderTerminalRequiresOpenStream(t *testing.T) {
	var unopened ChatStreamOrder
	if err := unopened.Admit(ChatStreamCompleted); err == nil {
		t.Fatalf("a terminal before open must be rejected")
	}

	var opened ChatStreamOrder
	if err := opened.Admit(ChatStreamOpen); err != nil {
		t.Fatalf("open rejected: %v", err)
	}
	if err := opened.Admit(ChatStreamCompleted); err != nil {
		t.Fatalf("zero-delta completion rejected: %v", err)
	}
}

// AC: finish classes map onto terminal event types exactly as the lifecycle
// table locks (§4.5). A canceled outcome never reports `completed`, and a
// failure never reports a natural finish class.
func TestTerminalEventForFinishClass(t *testing.T) {
	cases := map[FinishClass]ChatStreamEventType{
		FinishStop:          ChatStreamCompleted,
		FinishLength:        ChatStreamCompleted,
		FinishContentFilter: ChatStreamCompleted,
		FinishCanceled:      ChatStreamCanceled,
		FinishFailed:        ChatStreamFailed,
	}
	for class, want := range cases {
		if got := TerminalEventForFinishClass(class); got != want {
			t.Fatalf("finish class %q maps to %q, want %q", class, got, want)
		}
	}
	if got := TerminalEventForFinishClass(FinishClass("bogus")); got != ChatStreamFailed {
		t.Fatalf("an unknown finish class must fail closed to %q, got %q", ChatStreamFailed, got)
	}
}

// AC: only the three terminal event types are terminal; open/delta/heartbeat
// never are (guards the SSE writer against admitting a non-terminal as the
// stream's single sentinel).
func TestChatStreamEventTypeTerminalClassification(t *testing.T) {
	for _, terminal := range []ChatStreamEventType{ChatStreamCompleted, ChatStreamFailed, ChatStreamCanceled} {
		if !terminal.Terminal() {
			t.Fatalf("%q should be terminal", terminal)
		}
	}
	for _, nonTerminal := range []ChatStreamEventType{ChatStreamOpen, ChatStreamDelta, ChatStreamHeartbeat} {
		if nonTerminal.Terminal() {
			t.Fatalf("%q should not be terminal", nonTerminal)
		}
	}
}
