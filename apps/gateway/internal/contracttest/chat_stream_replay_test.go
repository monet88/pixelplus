package contracttest_test

import (
	"net/http"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// A matching terminal replay of a STREAMING request must return the original
// generated content.
//
// Cause and effect: the durable replay record is what a replay reconstructs from.
// If the streaming spine persists an empty assistant message, the client that
// retries with the same Idempotency-Key receives `open` + `completed` with no
// content at all — the idempotent retry silently loses the text the first call
// already delivered, while still reporting success.
func TestChatStreamReplayPreservesGeneratedContent(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_stream_replay", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{delta: "Hel"},
			{delta: "lo"},
			{outcome: ptrStreamOutcome(streamCommitted(domain.FinishStop, domain.ChatUsage{PromptTokens: 5, CompletionTokens: 2}))},
		})
	})

	spec := requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-replay-content",
		body:    chatStreamBody,
	}

	_, firstEvents, firstPayload := harness.streamRequest(t, spec)
	if got := joinDeltas(firstEvents); got != "Hello" {
		t.Fatalf("first stream content = %q, want %q (body=%s)", got, "Hello", firstPayload)
	}

	_, replayEvents, replayPayload := harness.streamRequest(t, spec)
	if calls := harness.stream.CallCount(); calls != 1 {
		t.Fatalf("streaming Adapter ran %d times, want exactly 1: a replay must not re-run generation", calls)
	}
	if got := joinDeltas(replayEvents); got != "Hello" {
		t.Fatalf("replayed stream content = %q, want %q — the idempotent replay lost the delivered text (body=%s)", got, "Hello", replayPayload)
	}
	terminals := terminalEvents(replayEvents)
	if len(terminals) != 1 || terminals[0].Type != "completed" {
		t.Fatalf("replay terminals = %v, want exactly one completed", eventTypes(replayEvents))
	}
}

// A replayed stream is reconstructed from a durable record, never re-streamed
// from the Provider, so it must disclose `synthetic` rather than leave the
// streaming class ambiguous or imply Provider-native streaming.
func TestChatStreamReplayDisclosesSyntheticClass(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_stream_replay_class", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{delta: "recorded"},
			{outcome: ptrStreamOutcome(streamCommitted(domain.FinishStop, domain.ChatUsage{PromptTokens: 2, CompletionTokens: 1}))},
		})
	})

	spec := requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-replay-class",
		body:    chatStreamBody,
	}

	_, firstEvents, _ := harness.streamRequest(t, spec)
	if got := firstEvents[0].Xpixelplus.StreamingClass; got != string(domain.StreamingReal) {
		t.Fatalf("live stream disclosed class %q, want real", got)
	}

	_, replayEvents, replayPayload := harness.streamRequest(t, spec)
	open := replayEvents[0]
	if open.Type != "open" {
		t.Fatalf("replay first event = %q, want open (body=%s)", open.Type, replayPayload)
	}
	if got := open.Xpixelplus.StreamingClass; got != string(domain.StreamingSynthetic) {
		t.Fatalf("replay disclosed streaming_class %q, want synthetic: a replay is reconstructed from a record, not Provider-native streaming", got)
	}
}

// A replayed stream must stay distinguishable from a live generation in the
// audit trail. Labelling it with the live terminal action would make a
// no-Adapter-call replay indistinguishable from a fresh billed generation.
func TestChatStreamReplayAuditUsesReplayAction(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_stream_replay_audit", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{delta: "once"},
			{outcome: ptrStreamOutcome(streamCommitted(domain.FinishStop, domain.ChatUsage{PromptTokens: 2, CompletionTokens: 1}))},
		})
	})

	spec := requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-replay-audit",
		body:    chatStreamBody,
	}

	harness.streamRequest(t, spec)
	before := len(harness.chatAudit.snapshot())

	harness.streamRequest(t, spec)
	replayEvents := harness.chatAudit.snapshot()[before:]

	var sawReplayed bool
	for _, event := range replayEvents {
		if event.Action == ports.AuditChatReplayed {
			sawReplayed = true
		}
		if event.Action == ports.AuditChatStreamTerminal {
			t.Fatalf("replay recorded as %q, the same action a live terminal gets: a replay must stay distinguishable", event.Action)
		}
	}
	if !sawReplayed {
		t.Fatalf("replay emitted no %q audit action; events=%+v", ports.AuditChatReplayed, replayEvents)
	}
}

// A `canceled` terminal is a COMMITTED generation: the Provider accepted it and
// consumed tokens before the client stopped it. Retrying under the same
// Idempotency-Key must therefore never launch a second generation
// (§7.5 `I-CHAT-NO-DUPLICATE-EXEC`).
//
// Cause and effect: `canceled` carries a zero-value canonical error, so a
// terminal-bookkeeping branch that only excludes `execution_possibly_committed`
// treats it as an authoritative non-commit and abandons the replay claim. The
// next request with the same key then wins a fresh claim and calls the Adapter
// again — one accepted request, two committed upstream generations, both billed.
func TestChatStreamCanceledTerminalIsNotRetriedAsNewGeneration(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_stream_canceled_replay", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{delta: "partial"},
			{outcome: ptrStreamOutcome(streamCanceledWithAbort(domain.ChatUsage{PromptTokens: 6, CompletionTokens: 2}))},
		})
	})

	spec := requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-canceled-replay",
		body:    chatStreamBody,
	}

	_, firstEvents, firstPayload := harness.streamRequest(t, spec)
	if terminals := terminalEvents(firstEvents); len(terminals) != 1 || terminals[0].Type != "canceled" {
		t.Fatalf("first stream terminals = %v, want exactly one canceled (body=%s)", eventTypes(firstEvents), firstPayload)
	}

	response, replayEvents, replayPayload := harness.streamRequest(t, spec)

	if calls := harness.stream.CallCount(); calls != 1 {
		t.Fatalf("streaming Adapter ran %d times, want exactly 1: a canceled terminal is a COMMITTED generation, so retrying the same Idempotency-Key must not launch a second one (§7.5 I-CHAT-NO-DUPLICATE-EXEC)", calls)
	}

	// Blocking the abandon is not enough on its own: if the claim is never
	// completed either, it stays in_progress forever and every retry gets a 409
	// that can never resolve. The recorded terminal must be replayable.
	if response.StatusCode != http.StatusOK {
		t.Fatalf("replay of a canceled stream answered HTTP %d (body=%s), want 200 replaying the recorded terminal: an unreplayable claim leaves the client permanently stuck on idempotency_in_progress", response.StatusCode, replayPayload)
	}
	replayTerminals := terminalEvents(replayEvents)
	if len(replayTerminals) != 1 || replayTerminals[0].Type != "canceled" {
		t.Fatalf("replay terminals = %v, want exactly one canceled matching the durable outcome", eventTypes(replayEvents))
	}
}
