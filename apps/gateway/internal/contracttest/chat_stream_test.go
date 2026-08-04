package contracttest_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/composition"
	"github.com/monet88/pixelplus/apps/gateway/internal/contracttest"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

const chatStreamBody = `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"stream":true}`

// chatStreamCapabilitySnapshot builds a snapshot that offers BOTH `chat` and
// `chat_streaming` for the model, with the given streaming class. Streaming is a
// separate capability operation, so a snapshot must declare it explicitly for a
// stream request to be eligible.
func chatStreamCapabilitySnapshot(
	accountID domain.ProviderAccountID,
	mode domain.AuthMode,
	version int,
	model string,
	class domain.StreamingClass,
) domain.CapabilitySnapshot {
	snapshot := chatCapabilitySnapshot(accountID, mode, version, model)
	snapshot.Operations[domain.CapabilityOpChatStreaming] = domain.CapabilityFact{
		Status:         domain.CapabilityVerified,
		Offerable:      true,
		EvidenceClass:  domain.EvidenceLiveProbe,
		ProbeSurface:   "/backend-api/chat",
		StreamingClass: class,
	}
	snapshot.Models = []domain.ModelCapability{{
		ModelSlug: model,
		Operations: map[domain.CapabilityOperation]domain.CapabilityStatus{
			domain.CapabilityOpChat:          domain.CapabilityVerified,
			domain.CapabilityOpChatStreaming: domain.CapabilityVerified,
		},
		SurfaceBinding: "/backend-api/chat",
		ObservedAt:     domain.NewTimestamp(spineFixtureTime),
	}}
	return snapshot.WithDerivedFreshness(spineFixtureTime)
}

// streamHarness wires the streaming chat spine through real composition. Tests
// enter through HTTP POST /v1/chat/completions with stream=true and assert on
// the parsed canonical SSE event sequence.
type streamHarness struct {
	*chatHarness
	stream *scriptedChatStreamAdapter
	leases *recordingStreamLeases
}

func newStreamHarness(t *testing.T, configure func(*streamHarness)) *streamHarness {
	t.Helper()

	harness := &streamHarness{}
	harness.chatHarness = newChatHarnessWithOptions(t, func(chat *chatHarness, options *contracttest.Options) {
		harness.chatHarness = chat
		harness.stream = newScriptedChatStreamAdapter(chat.log)
		harness.leases = newRecordingStreamLeases(chat.log, composition.NewControlledChatStreamLeaseStore())
		options.ChatStreamAdapter = harness.stream
		options.ChatStreamLeases = harness.leases
		if configure != nil {
			configure(harness)
		}
	})
	return harness
}

// seedStreamingAccount seeds one active same-Tenant account whose snapshot
// offers chat_streaming with the given class.
func (harness *streamHarness) seedStreamingAccount(tenant domain.TenantID, accountID string, mode domain.AuthMode, class domain.StreamingClass) {
	account := activeAccount(accountID, mode)
	stripped, health, permit := seedAccountHealth(account)
	harness.accounts.seed(tenant, stripped)
	harness.health.Seed(tenant, account.ID, health, permit)
	harness.capabilities.seed(tenant, chatStreamCapabilitySnapshot(account.ID, account.AuthMode, account.Credential.Version, chatModel, class))
	harness.routing.Seed(tenant, chatRoutingPolicy([]domain.ProviderAccountID{account.ID}, nil))
}

// streamRequest issues the streaming request and parses the canonical events.
func (harness *streamHarness) streamRequest(t *testing.T, spec requestSpec) (*http.Response, []sseEvent, []byte) {
	t.Helper()
	response, payload := harness.do(t, spec)
	if response.StatusCode != http.StatusOK {
		return response, nil, payload
	}
	if got := response.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream (body=%s)", got, payload)
	}
	return response, parseSSE(t, payload), payload
}

// AC1: every accepted stream emits exactly one open event carrying the
// server-owned execution identity BEFORE any content delta.
func TestChatStreamEmitsOpenBeforeFirstDelta(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_stream", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{delta: "Hel"},
			{delta: "lo"},
			{outcome: ptrStreamOutcome(streamCommitted(domain.FinishStop, domain.ChatUsage{PromptTokens: 5, CompletionTokens: 2}))},
		})
	})

	_, events, payload := harness.streamRequest(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-open",
		body:    chatStreamBody,
	})

	if len(events) == 0 {
		t.Fatalf("no canonical events emitted (body=%s)", payload)
	}
	if events[0].Type != "open" {
		t.Fatalf("first event = %q, want open (types=%v)", events[0].Type, eventTypes(events))
	}
	for _, event := range events[1:] {
		if event.Type == "open" {
			t.Fatalf("more than one open event: %v", eventTypes(events))
		}
	}

	open := events[0]
	if open.Xpixelplus == nil {
		t.Fatalf("open event carries no x_pixelplus safe metadata: %s", open.raw)
	}
	if open.Xpixelplus.ExecutionID == "" {
		t.Fatalf("open event carries no server-owned execution_id: %s", open.raw)
	}
	if open.Xpixelplus.RequestID == "" {
		t.Fatalf("open event carries no server-owned request_id: %s", open.raw)
	}
	if open.ID == "" {
		t.Fatalf("open event carries no id: %s", open.raw)
	}
	if open.Model != chatModel {
		t.Fatalf("open model = %q, want %q", open.Model, chatModel)
	}

	// The open event must precede the first delta positionally, not merely exist.
	firstDelta := indexOfEventType(events, "delta")
	if firstDelta <= 0 {
		t.Fatalf("expected a delta after open, got %v", eventTypes(events))
	}

	// Every later event must reference the same server-owned execution identity,
	// so a client can correlate the whole stream.
	for _, event := range events {
		if event.Type == "heartbeat" {
			continue
		}
		if event.ID != open.ID {
			t.Fatalf("event %q id = %q, want stable execution identity %q", event.Type, event.ID, open.ID)
		}
	}
}

// AC2: zero or more canonical deltas follow and concatenate back into the
// assistant message; no Provider wire framing is exposed.
func TestChatStreamDeltasReconstructMessageWithoutProviderFraming(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_stream", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{delta: "The "},
			{delta: "quick "},
			{delta: "brown fox"},
			{outcome: ptrStreamOutcome(streamCommitted(domain.FinishStop, domain.ChatUsage{PromptTokens: 8, CompletionTokens: 4}))},
		})
	})

	_, events, payload := harness.streamRequest(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-reconstruct",
		body:    chatStreamBody,
	})

	// parseSSE already fails on a leaked `[DONE]` sentinel or a non-`data:` frame.
	if got := joinDeltas(events); got != "The quick brown fox" {
		t.Fatalf("reconstructed message = %q, want %q (body=%s)", got, "The quick brown fox", payload)
	}
	if got := eventTypes(events); len(got) != 5 {
		t.Fatalf("event sequence = %v, want open + 3 deltas + terminal", got)
	}

	// Deltas carry canonical choices only — index plus delta content.
	for _, event := range events {
		if event.Type != "delta" {
			continue
		}
		if len(event.Choices) != 1 {
			t.Fatalf("delta carries %d choices, want exactly 1: %s", len(event.Choices), event.raw)
		}
		if event.Choices[0].Delta.Content == "" {
			t.Fatalf("delta carries empty canonical content: %s", event.raw)
		}
	}
}

// AC2: heartbeats may interleave and never carry assistant tokens.
func TestChatStreamHeartbeatCarriesNoContent(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_stream", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{delta: "part one"},
			{heartbeat: true},
			{delta: " part two"},
			{outcome: ptrStreamOutcome(streamCommitted(domain.FinishStop, domain.ChatUsage{PromptTokens: 4, CompletionTokens: 4}))},
		})
	})

	_, events, payload := harness.streamRequest(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-heartbeat",
		body:    chatStreamBody,
	})

	heartbeats := 0
	for _, event := range events {
		if event.Type != "heartbeat" {
			continue
		}
		heartbeats++
		if len(event.Choices) != 0 {
			t.Fatalf("heartbeat carries choices/content: %s", event.raw)
		}
	}
	if heartbeats != 1 {
		t.Fatalf("heartbeat count = %d, want 1 (types=%v)", heartbeats, eventTypes(events))
	}
	// A heartbeat must not disturb content reconstruction.
	if got := joinDeltas(events); got != "part one part two" {
		t.Fatalf("reconstructed message = %q, want %q (body=%s)", got, "part one part two", payload)
	}
}

// AC2: a stream with zero deltas still completes canonically.
func TestChatStreamZeroDeltaCompletesCleanly(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_stream", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{outcome: ptrStreamOutcome(streamCommitted(domain.FinishStop, domain.ChatUsage{PromptTokens: 3, CompletionTokens: 0}))},
		})
	})

	_, events, _ := harness.streamRequest(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-zero-delta",
		body:    chatStreamBody,
	})

	if got := eventTypes(events); len(got) != 2 || got[0] != "open" || got[1] != "completed" {
		t.Fatalf("event sequence = %v, want [open completed]", got)
	}
}

// AC3: exactly one terminal ends the stream and nothing follows it.
func TestChatStreamExactlyOneTerminalNoPostTerminalData(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_stream", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{delta: "answer"},
			{outcome: ptrStreamOutcome(streamCommitted(domain.FinishStop, domain.ChatUsage{PromptTokens: 2, CompletionTokens: 1}))},
		})
	})

	_, events, payload := harness.streamRequest(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-one-terminal",
		body:    chatStreamBody,
	})

	terminals := terminalEvents(events)
	if len(terminals) != 1 {
		t.Fatalf("terminal count = %d, want exactly 1 (types=%v)", len(terminals), eventTypes(events))
	}
	if last := events[len(events)-1]; !isTerminalType(last.Type) {
		t.Fatalf("last event = %q, want the terminal (types=%v)", last.Type, eventTypes(events))
	}
	// No trailing sentinel of any kind after the terminal frame.
	if got := eventTypes(events); got[len(got)-1] != "completed" {
		t.Fatalf("event sequence = %v, want completed last (body=%s)", got, payload)
	}
}

// AC3: a drifting Adapter that keeps writing after its attempt returned cannot
// place content after the terminal event. This is the ordering invariant's real
// threat model — a Provider client whose reader goroutine outlives the call —
// and it is refused structurally: the sink rejects post-terminal writes and the
// Adapter has no method with which to emit a terminal at all.
func TestChatStreamRogueAdapterCannotWriteAfterTerminal(t *testing.T) {
	t.Parallel()

	rogue := newRogueWriter()
	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_stream", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{delta: "first"},
			{rogueAfter: rogue},
			{outcome: ptrStreamOutcome(streamCommitted(domain.FinishStop, domain.ChatUsage{PromptTokens: 2, CompletionTokens: 1}))},
		})
	})

	// The client stream completes here: the response body is fully read, so the
	// terminal event has already been written.
	_, events, payload := harness.streamRequest(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-rogue-adapter",
		body:    chatStreamBody,
	})

	if terminals := terminalEvents(events); len(terminals) != 1 {
		t.Fatalf("terminal count = %d, want exactly 1 (types=%v)", len(terminals), eventTypes(events))
	}
	beforeTypes := eventTypes(events)

	// Now let the leaked goroutine attempt its post-terminal writes.
	rogue.Release()
	rogue.Wait(t)

	// Every rogue write must have been REFUSED. If the sink accepted them, the
	// ordering guard is broken even if the already-read body looks canonical.
	results := rogue.Results()
	if len(results) != 2 {
		t.Fatalf("rogue write attempts = %d, want 2 (delta + heartbeat)", len(results))
	}
	for index, err := range results {
		if err == nil {
			t.Fatalf("rogue post-terminal write %d was ACCEPTED; the stream ordering guard is not enforced", index)
		}
		if !errors.Is(err, domain.ErrChatStreamTerminated) {
			t.Fatalf("rogue write %d rejected with %v, want %v", index, err, domain.ErrChatStreamTerminated)
		}
	}

	// The delivered stream is unchanged by the rogue attempts.
	if got := strings.Join(beforeTypes, ","); got != strings.Join(eventTypes(parseSSE(t, payload)), ",") {
		t.Fatalf("delivered event sequence changed after rogue writes: %v", got)
	}
	if got := joinDeltas(events); got != "first" {
		t.Fatalf("delivered content = %q, want %q (no leaked post-terminal content)", got, "first")
	}
}

// AC3: the Adapter-facing sink exposes ONLY delta/heartbeat. If a terminal or
// open method were ever added, an Adapter could forge a second sentinel — this
// pins the seam shape so that regression is caught at compile/test time.
func TestChatSinkExposesNoTerminalOrOpenMethod(t *testing.T) {
	t.Parallel()

	var sink domain.ChatSink = discardSink{}
	if _, ok := sink.(interface{ Terminal() error }); ok {
		t.Fatalf("domain.ChatSink exposes a terminal method; Adapters must not be able to terminate a stream")
	}
	if _, ok := sink.(interface{ Open() error }); ok {
		t.Fatalf("domain.ChatSink exposes an open method; Adapters must not be able to open a stream")
	}
}

// AC3/AC4: deltas followed by a runtime failure end the stream `failed`, so the
// client can tell the message is incomplete without a Provider marker.
func TestChatStreamFailureAfterDeltasIsFailedTerminal(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_stream", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{delta: "partial answer"},
			{outcome: ptrStreamOutcome(streamNotCommitted(domain.ErrCodeUpstreamUnavailable))},
		})
	})

	_, events, payload := harness.streamRequest(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-partial-failure",
		body:    chatStreamBody,
	})

	terminals := terminalEvents(events)
	if len(terminals) != 1 {
		t.Fatalf("terminal count = %d, want 1 (types=%v)", len(terminals), eventTypes(events))
	}
	terminal := terminals[0]
	if terminal.Type != "failed" {
		t.Fatalf("terminal = %q, want failed (body=%s)", terminal.Type, payload)
	}
	if terminal.FinishClass != "failed" {
		t.Fatalf("terminal finish_class = %q, want failed", terminal.FinishClass)
	}
	if terminal.Error == nil || terminal.Error["code"] == "" {
		t.Fatalf("failed terminal carries no canonical error: %s", terminal.raw)
	}
	// The partial content stays delivered; the terminal class (not a Provider
	// marker) is what tells the client the message is incomplete.
	if got := joinDeltas(events); got != "partial answer" {
		t.Fatalf("delivered partial content = %q, want %q", got, "partial answer")
	}
}

// AC4: the completed terminal carries the finish class and safe usage/account
// metadata matching the durable execution outcome.
func TestChatStreamCompletedCarriesFinishClassAndUsage(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_stream", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{delta: "bounded"},
			{outcome: ptrStreamOutcome(streamCommitted(domain.FinishLength, domain.ChatUsage{PromptTokens: 11, CompletionTokens: 7}))},
		})
	})

	_, events, payload := harness.streamRequest(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-usage",
		body:    chatStreamBody,
	})

	terminal := terminalEvents(events)[0]
	if terminal.Type != "completed" {
		t.Fatalf("terminal = %q, want completed (body=%s)", terminal.Type, payload)
	}
	// The Adapter observed `length`, so the client must see `length`, not `stop`.
	if terminal.FinishClass != "length" {
		t.Fatalf("finish_class = %q, want length (the durable outcome)", terminal.FinishClass)
	}
	if terminal.Usage == nil {
		t.Fatalf("completed terminal carries no usage: %s", terminal.raw)
	}
	if terminal.Usage.PromptTokens != 11 || terminal.Usage.CompletionTokens != 7 {
		t.Fatalf("usage = %+v, want prompt=11 completion=7 (as observed)", *terminal.Usage)
	}
	if terminal.Usage.TotalTokens != 18 {
		t.Fatalf("usage total = %d, want 18", terminal.Usage.TotalTokens)
	}
	if terminal.Xpixelplus == nil || terminal.Xpixelplus.FinishClass != "length" {
		t.Fatalf("safe metadata finish_class mismatch: %s", terminal.raw)
	}

	// Safe account metadata is disclosed on open and never leaks credentials.
	open := events[0]
	if open.Xpixelplus.ProviderAccountID != "pa_stream" {
		t.Fatalf("open provider_account_id = %q, want pa_stream", open.Xpixelplus.ProviderAccountID)
	}
	if strings := string(payload); containsAny(strings, "fixture-chat-credential-material", "secretA") {
		t.Fatalf("credential material leaked into the stream:\n%s", payload)
	}
}

// AC4: a canceled outcome reports `canceled` honestly and never claims the
// upstream stopped without confirmation.
func TestChatStreamTerminalMatchesDurableOutcome(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_stream", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{delta: "interrupted"},
			{outcome: ptrStreamOutcome(streamCanceledWithAbort(domain.ChatUsage{PromptTokens: 6, CompletionTokens: 2}))},
		})
	})

	_, events, payload := harness.streamRequest(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-canceled",
		body:    chatStreamBody,
	})

	terminal := terminalEvents(events)[0]
	if terminal.Type != "canceled" {
		t.Fatalf("terminal = %q, want canceled (body=%s)", terminal.Type, payload)
	}
	if terminal.FinishClass != "canceled" {
		t.Fatalf("finish_class = %q, want canceled", terminal.FinishClass)
	}
	if terminal.Xpixelplus == nil {
		t.Fatalf("canceled terminal carries no safe metadata: %s", terminal.raw)
	}
	// Cancellation is not proof the upstream stopped; the Gateway must not claim
	// it did.
	if terminal.Xpixelplus.UpstreamStopConfirmed == nil {
		t.Fatalf("canceled terminal omits upstream_stop_confirmed: %s", terminal.raw)
	}
	if *terminal.Xpixelplus.UpstreamStopConfirmed {
		t.Fatalf("canceled terminal claims upstream stop was confirmed without proof: %s", terminal.raw)
	}
	if terminal.Xpixelplus.UpstreamAbortAttempted == nil || !*terminal.Xpixelplus.UpstreamAbortAttempted {
		t.Fatalf("canceled terminal should report the abort attempt: %s", terminal.raw)
	}
}

// AC4 / §6.2 rule 4: a NON-CANCELABLE upstream produces terminal `canceled`
// without any abort attempt, and the Gateway "MUST NOT claim it was aborted".
//
// Cause and effect: the Adapter reports a committed `canceled` outcome but does
// not report an abort attempt, because this Auth Mode cannot abort upstream. If
// the Gateway derived `upstream_abort_attempted` from the terminal class instead
// of the Adapter observation, this terminal would publish `true` and an operator
// reading the audit/metadata would believe the generation was stopped — while it
// actually keeps running and keeps consuming Provider quota.
func TestChatStreamCanceledDoesNotClaimUnattemptedAbort(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_noncancelable", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{delta: "still running upstream"},
			{outcome: ptrStreamOutcome(streamCanceledNonCancelable(domain.ChatUsage{PromptTokens: 4, CompletionTokens: 3}))},
		})
	})

	_, events, payload := harness.streamRequest(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-noncancelable",
		body:    chatStreamBody,
	})

	terminal := terminalEvents(events)[0]
	if terminal.Type != "canceled" {
		t.Fatalf("terminal = %q, want canceled (body=%s)", terminal.Type, payload)
	}
	if terminal.Xpixelplus == nil {
		t.Fatalf("canceled terminal carries no safe metadata: %s", terminal.raw)
	}
	if terminal.Xpixelplus.UpstreamAbortAttempted == nil {
		t.Fatalf("canceled terminal omits upstream_abort_attempted: %s", terminal.raw)
	}
	if *terminal.Xpixelplus.UpstreamAbortAttempted {
		t.Fatalf("canceled terminal claims an abort that the Adapter never attempted (§6.2 rule 4 \"MUST NOT claim it was aborted\"): %s", terminal.raw)
	}
	if terminal.Xpixelplus.UpstreamStopConfirmed == nil || *terminal.Xpixelplus.UpstreamStopConfirmed {
		t.Fatalf("canceled terminal must not claim a confirmed upstream stop: %s", terminal.raw)
	}
}

// AC5: synthetic streaming is disclosed as synthetic, with identical ordering.
//
// The streaming class is a Capability Snapshot property, so the disclosure is
// asserted through a snapshot that marks chat_streaming synthetic. A policy-
// permitted Auth Mode is used deliberately: the experimental Gemini Web Cookie
// mode is rejected by the risk gate before capability disclosure is reachable,
// so it could not prove that the Gateway reports the class honestly.
func TestChatStreamSyntheticClassDisclosed(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_synthetic", domain.AuthModeChatGPTCodexOAuth, domain.StreamingSynthetic)
		h.stream.Script([]streamStep{
			{delta: "chunk one"},
			{delta: " chunk two"},
			{outcome: ptrStreamOutcome(streamCommitted(domain.FinishStop, domain.ChatUsage{PromptTokens: 4, CompletionTokens: 4}))},
		})
	})

	_, events, payload := harness.streamRequest(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-synthetic",
		body:    chatStreamBody,
	})

	open := events[0]
	if open.Xpixelplus == nil || open.Xpixelplus.StreamingClass != "synthetic" {
		t.Fatalf("open streaming_class not disclosed as synthetic: %s (body=%s)", open.raw, payload)
	}
	// Ordering is identical to a real stream, so clients render both the same.
	if got := eventTypes(events); len(got) != 4 || got[0] != "open" || got[3] != "completed" {
		t.Fatalf("synthetic event sequence = %v, want open + 2 deltas + completed", got)
	}
	if got := joinDeltas(events); got != "chunk one chunk two" {
		t.Fatalf("synthetic reconstruction = %q, want %q", got, "chunk one chunk two")
	}
}

// AC5: a request for streaming on an account that verifies only non-streaming
// chat is REJECTED before upstream and never silently served as non-streaming.
func TestChatStreamNeverDowngradesToNonStreaming(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		// seedActive offers `chat` only — no chat_streaming capability fact.
		h.seedActive("tenant_a", "pa_chat_only", domain.AuthModeChatGPTCodexOAuth)
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-no-downgrade",
		body:    chatStreamBody,
	})

	if response.StatusCode == http.StatusOK {
		t.Fatalf("a stream request on a chat-only account returned 200 (body=%s)", payload)
	}
	if got := response.Header.Get("Content-Type"); got == "text/event-stream" {
		t.Fatalf("rejection was served as a stream: %q", got)
	}
	// The response must be a canonical JSON error, never a non-streaming
	// completion body: answering with `chat.completion` would be the silent
	// downgrade the spec forbids.
	body := decodeCompletionBody(t, payload)
	if _, ok := body["choices"]; ok {
		t.Fatalf("stream request was answered with a non-streaming completion: %s", payload)
	}
	code, _ := getString(body, "code")
	if code == "" {
		t.Fatalf("expected a canonical error code, got %s", payload)
	}
	if harness.stream.CallCount() != 0 {
		t.Fatalf("streaming Adapter ran %d times, want 0 (capability gate precedes upstream)", harness.stream.CallCount())
	}
	if harness.adapter.CallCount() != 0 {
		t.Fatalf("non-streaming Adapter ran %d times for a stream request, want 0", harness.adapter.CallCount())
	}
}

// AC6: the stream binds to exactly one account for its duration and the lease is
// released at the terminal.
func TestChatStreamLeaseBindsSingleAccount(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_leased", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		policy := chatRoutingPolicy([]domain.ProviderAccountID{"pa_leased"}, nil)
		policy.LeasePolicy = domain.LeasePolicy{Enabled: true, EligibleUnits: []domain.LeaseUnit{domain.LeaseUnitChatStream}}
		h.routing.Seed("tenant_a", policy)
		h.stream.Script([]streamStep{
			{delta: "leased"},
			{outcome: ptrStreamOutcome(streamCommitted(domain.FinishStop, domain.ChatUsage{PromptTokens: 3, CompletionTokens: 1}))},
		})
	})

	_, events, _ := harness.streamRequest(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-lease",
		body:    chatStreamBody,
	})

	acquisitions := harness.leases.Acquisitions()
	if len(acquisitions) != 1 {
		t.Fatalf("lease acquisitions = %d, want exactly 1 hard lease", len(acquisitions))
	}
	if acquisitions[0].AccountID != "pa_leased" {
		t.Fatalf("lease bound account %q, want pa_leased", acquisitions[0].AccountID)
	}
	if acquisitions[0].TenantID != "tenant_a" {
		t.Fatalf("lease tenant = %q, want tenant_a (never cross-Tenant)", acquisitions[0].TenantID)
	}
	if acquisitions[0].Holder == "" {
		t.Fatalf("lease holder must be the server-owned execution identity")
	}

	// The lease must be acquired BEFORE the stream opens and released after the
	// terminal, so no window exists where the stream runs unbound.
	logs := harness.log.snapshot()
	if !containsSeq(logs, "lease.acquire", "stream.adapter.run", "lease.release") {
		t.Fatalf("lease lifecycle order = %v, want acquire → adapter → release", logs)
	}
	if len(harness.leases.Releases()) != 1 {
		t.Fatalf("lease releases = %d, want exactly 1", len(harness.leases.Releases()))
	}

	// Only one account ever served the stream.
	if accounts := harness.stream.Accounts(); len(accounts) != 1 || accounts[0] != "pa_leased" {
		t.Fatalf("stream visited accounts %v, want exactly [pa_leased] (no mid-stream hop)", accounts)
	}
	if terminals := terminalEvents(events); len(terminals) != 1 || terminals[0].Type != "completed" {
		t.Fatalf("expected exactly one completed terminal, got %v", eventTypes(events))
	}
}

// AC6: once a delta has reached the client, the Gateway must NOT restart
// generation on a fallback account even under authoritative no-commit proof —
// splicing two generations would corrupt reconstruction.
func TestChatStreamNoFallbackAfterDeltaEmitted(t *testing.T) {
	t.Parallel()

	const primary = "pa_primary_stream"
	const fallback = "pa_fallback_stream"

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", primary, domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.seedStreamingAccount("tenant_a", fallback, domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.routing.Seed("tenant_a", chatRoutingPolicy(
			[]domain.ProviderAccountID{primary},
			[]domain.ProviderAccountID{fallback},
		))
		h.stream.Script(
			// Primary delivers content, then reports authoritative no-commit.
			[]streamStep{
				{delta: "half an answer"},
				{outcome: ptrStreamOutcome(streamNotCommitted(domain.ErrCodeProviderRateLimited))},
			},
			// Fallback would succeed — it must never run.
			[]streamStep{
				{delta: "a whole different answer"},
				{outcome: ptrStreamOutcome(streamCommitted(domain.FinishStop, domain.ChatUsage{PromptTokens: 9, CompletionTokens: 9}))},
			},
		)
	})

	_, events, payload := harness.streamRequest(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-no-splice",
		body:    chatStreamBody,
	})

	if calls := harness.stream.CallCount(); calls != 1 {
		t.Fatalf("streaming Adapter ran %d times, want 1 (no re-attempt after a delivered delta)", calls)
	}
	if accounts := harness.stream.Accounts(); len(accounts) != 1 || accounts[0] != primary {
		t.Fatalf("accounts visited = %v, want only the primary %q", accounts, primary)
	}
	// The client sees exactly the primary's partial content plus one failed
	// terminal — never two spliced generations.
	if got := joinDeltas(events); got != "half an answer" {
		t.Fatalf("delivered content = %q, want only the primary's partial content (body=%s)", got, payload)
	}
	terminals := terminalEvents(events)
	if len(terminals) != 1 || terminals[0].Type != "failed" {
		t.Fatalf("terminal = %v, want exactly one failed (types=%v)", eventTypes(events), eventTypes(events))
	}
}

// AC6: commit-unknown fails closed — no fallback, no replacement, and the client
// receives a terminal that says the execution may have been committed.
func TestChatStreamNoFallbackWhenCommitUnknown(t *testing.T) {
	t.Parallel()

	const primary = "pa_unknown_stream"
	const fallback = "pa_fallback_unknown"

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", primary, domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.seedStreamingAccount("tenant_a", fallback, domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.routing.Seed("tenant_a", chatRoutingPolicy(
			[]domain.ProviderAccountID{primary},
			[]domain.ProviderAccountID{fallback},
		))
		h.stream.Script(
			[]streamStep{{outcome: ptrStreamOutcome(streamUnknown())}},
			[]streamStep{{outcome: ptrStreamOutcome(streamCommitted(domain.FinishStop, domain.ChatUsage{}))}},
		)
	})

	_, events, payload := harness.streamRequest(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-commit-unknown",
		body:    chatStreamBody,
	})

	if calls := harness.stream.CallCount(); calls != 1 {
		t.Fatalf("streaming Adapter ran %d times, want 1 (commit unknown forbids replacement)", calls)
	}
	terminals := terminalEvents(events)
	if len(terminals) != 1 {
		t.Fatalf("terminal count = %d, want 1 (types=%v)", len(terminals), eventTypes(events))
	}
	if terminals[0].Type != "failed" {
		t.Fatalf("terminal = %q, want failed (body=%s)", terminals[0].Type, payload)
	}
	if code, _ := terminals[0].Error["code"].(string); code != string(domain.ErrCodeExecutionPossiblyCommitted) {
		t.Fatalf("terminal error code = %q, want %q", code, domain.ErrCodeExecutionPossiblyCommitted)
	}
}

// Supporting invariant: accounting/concurrency occupancy settles exactly once
// against the original Tenant + Client API Key, and streaming counts as one
// request rather than one per event.
func TestChatStreamSettlesAccountingOnce(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_settle", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{delta: "one"},
			{delta: "two"},
			{delta: "three"},
			{outcome: ptrStreamOutcome(streamCommitted(domain.FinishStop, domain.ChatUsage{PromptTokens: 5, CompletionTokens: 3}))},
		})
	})

	_, events, _ := harness.streamRequest(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-settle-once",
		body:    chatStreamBody,
	})

	if settles := harness.admission.logicalSettleCount.Load(); settles != 1 {
		t.Fatalf("logical settle count = %d, want exactly 1 for the whole stream", settles)
	}
	if admits := harness.admission.OperationCount(domain.OperationChatCompletionStreaming); admits != 1 {
		t.Fatalf("streaming admissions = %d, want 1 (a stream is one request, not one per event)", admits)
	}
	if !harness.settledKeysAllContain("tenant_a/key_a/") {
		t.Fatalf("settlement keys must stay scoped to the originating Tenant+key: %v", harness.admission.settledKeys)
	}
	if !harness.settledKeysContain("/chat_occupancy") {
		t.Fatalf("expected a chat occupancy settlement key: %v", harness.admission.settledKeys)
	}
	if deltas := countEvents(eventTypes(events), "delta"); deltas != 3 {
		t.Fatalf("delta count = %d, want 3", deltas)
	}
}

// Supporting invariant: a stream request whose account is health-blocked is
// rejected with a real HTTP status and no partial stream is written.
func TestChatStreamCapabilityGateUsesStreamingOperation(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		// Offer chat_streaming as explicitly unsupported: the streaming gate must
		// consult THIS fact, not the verified non-streaming `chat` fact.
		account := activeAccount("pa_no_stream", domain.AuthModeChatGPTCodexOAuth)
		stripped, health, permit := seedAccountHealth(account)
		h.accounts.seed("tenant_a", stripped)
		h.health.Seed("tenant_a", account.ID, health, permit)
		snapshot := chatCapabilitySnapshot(account.ID, account.AuthMode, account.Credential.Version, chatModel)
		snapshot.Operations[domain.CapabilityOpChatStreaming] = domain.CapabilityFact{
			Status:        domain.CapabilityUnsupported,
			Offerable:     false,
			EvidenceClass: domain.EvidenceLiveProbe,
			ProbeSurface:  "/backend-api/chat",
		}
		h.capabilities.seed("tenant_a", snapshot.WithDerivedFreshness(spineFixtureTime))
		h.routing.Seed("tenant_a", chatRoutingPolicy([]domain.ProviderAccountID{account.ID}, nil))
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-unsupported",
		body:    chatStreamBody,
	})

	// Capability rejections carry the `capability` status class, which the frozen
	// error model maps to 409 (domain.StatusCapability.HTTPStatus).
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 capability rejection (body=%s)", response.StatusCode, payload)
	}
	body := decodeCompletionBody(t, payload)
	if code, _ := getString(body, "code"); code != string(domain.ErrCodeCapabilityUnsupported) {
		t.Fatalf("code = %q, want %q (body=%s)", code, domain.ErrCodeCapabilityUnsupported, payload)
	}
	if harness.stream.CallCount() != 0 {
		t.Fatalf("streaming Adapter ran despite an unsupported streaming capability")
	}
	// Zero Vault decrypts for a pre-upstream capability rejection.
	if countEvents(harness.log.snapshot(), "vault.validate") != 0 {
		t.Fatalf("capability rejection must precede any credential access: %v", harness.log.snapshot())
	}
}

// Supporting invariant: with no streaming Adapter composed, a stream request
// fails closed rather than degrading to the non-streaming Adapter.
func TestChatStreamFailsClosedWithoutStreamingAdapter(t *testing.T) {
	t.Parallel()

	harness := newChatHarness(t, func(h *chatHarness) {
		h.seedActive("tenant_a", "pa_no_adapter", domain.AuthModeChatGPTCodexOAuth)
		h.capabilities.seed("tenant_a", chatStreamCapabilitySnapshot("pa_no_adapter", domain.AuthModeChatGPTCodexOAuth, 1, chatModel, domain.StreamingReal))
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-no-adapter",
		body:    chatStreamBody,
	})

	if response.StatusCode == http.StatusOK {
		t.Fatalf("stream without a composed streaming Adapter returned 200 (body=%s)", payload)
	}
	if harness.adapter.CallCount() != 0 {
		t.Fatalf("non-streaming Adapter served a streaming request %d times, want 0", harness.adapter.CallCount())
	}
}

// discardSink is a domain.ChatSink used only for the static shape assertion that
// Adapters cannot open or terminate a stream.
type discardSink struct{}

func (discardSink) Delta(domain.ChatDelta) error { return nil }
func (discardSink) Heartbeat() error             { return nil }

func ptrStreamOutcome(outcome domain.ChatStreamOutcome) *domain.ChatStreamOutcome {
	return &outcome
}

func indexOfEventType(events []sseEvent, target string) int {
	for index, event := range events {
		if event.Type == target {
			return index
		}
	}
	return -1
}

func isTerminalType(eventType string) bool {
	switch eventType {
	case "completed", "failed", "canceled":
		return true
	default:
		return false
	}
}

func containsAny(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if needle != "" && strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}

var _ ports.ChatStreamAdapter = (*scriptedChatStreamAdapter)(nil)
var _ ports.ChatStreamLeaseStore = (*recordingStreamLeases)(nil)

// Supporting invariant: the streaming spine must label its audit events with the
// streaming audit actions. Reusing the non-streaming `chat_completion.completed`
// action makes the audit trail claim a synchronous completion for what was
// actually a stream, and leaves the declared streaming actions never emitted.
func TestChatStreamAuditUsesStreamingActions(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_stream_audit", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{delta: "audited"},
			{outcome: ptrStreamOutcome(streamCommitted(domain.FinishStop, domain.ChatUsage{PromptTokens: 2, CompletionTokens: 1}))},
		})
	})

	_, events, _ := harness.streamRequest(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-audit",
		body:    chatStreamBody,
	})
	if len(events) == 0 {
		t.Fatalf("stream produced no events")
	}

	var opened, terminal int
	for _, ev := range harness.chatAudit.snapshot() {
		switch ev.Action {
		case ports.AuditChatStreamOpened:
			opened++
			if ev.Outcome != "stream_opened" {
				t.Fatalf("stream_opened audit outcome = %q, want stream_opened", ev.Outcome)
			}
		case ports.AuditChatStreamTerminal:
			terminal++
			if ev.Outcome != string(domain.ChatStreamCompleted) {
				t.Fatalf("stream terminal audit outcome = %q, want completed", ev.Outcome)
			}
		case ports.AuditChatCompleted, ports.AuditChatReplayed:
			t.Fatalf("streaming spine emitted non-streaming audit action %q", ev.Action)
		}
		if ev.ProviderAccountID != "pa_stream_audit" {
			continue
		}
	}
	if opened != 1 {
		t.Fatalf("stream_opened audit events = %d, want exactly 1", opened)
	}
	if terminal != 1 {
		t.Fatalf("stream terminal audit events = %d, want exactly 1", terminal)
	}
}

// Supporting invariant: a leaked Adapter goroutine that writes AFTER the handler
// returned must not touch the ResponseWriter. An http.ResponseWriter is invalid
// once its handler returns, so a late write would panic in the server goroutine
// and take down the connection — the stream must refuse it instead.
//
// This is the un-opened variant: the attempt reports authoritative no-commit
// with zero deltas, so the request is answered with a canonical HTTP status and
// the leaked goroutine's writes arrive after the handler is done.
func TestChatStreamLateAdapterWriteAfterHandlerReturn(t *testing.T) {
	t.Parallel()

	rogue := newRogueWriter()
	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_late", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{rogueAfter: rogue},
			{outcome: ptrStreamOutcome(streamNotCommitted(domain.ErrCodeUpstreamUnavailable))},
		})
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-late-write",
		body:    chatStreamBody,
	})

	// Zero deltas were delivered, so the outcome is still a canonical HTTP error.
	if response.StatusCode == http.StatusOK {
		t.Fatalf("zero-delta no-commit returned 200 (body=%s)", payload)
	}
	if got := response.Header.Get("Content-Type"); got == "text/event-stream" {
		t.Fatalf("a pre-stream rejection was served as a stream: %q", got)
	}

	// The handler has returned. Release the leaked goroutine: its writes must be
	// refused without touching the dead ResponseWriter.
	rogue.Release()
	rogue.Wait(t)

	for index, err := range rogue.Results() {
		if err == nil {
			t.Fatalf("late write %d after handler return was ACCEPTED; the ResponseWriter is no longer valid", index)
		}
	}

	// The fixture server must still be healthy — a panic in the server goroutine
	// would have killed the connection handling.
	probe, probePayload := harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/healthz",
	})
	if probe.StatusCode != http.StatusOK {
		t.Fatalf("server unhealthy after a late Adapter write: %d (body=%s)", probe.StatusCode, probePayload)
	}
}
