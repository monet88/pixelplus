package contracttest_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// AC5/AC6: a fallback candidate must satisfy the STREAMING capability, not the
// non-streaming one. An account that verifies `chat` but leaves `chat_streaming`
// unsupported is not a viable fallback target for a stream: selecting it would
// either fail at its own gate or silently answer a streaming request from a
// non-streaming-capable account (chat lifecycle §3.2 rule 2; routing spec §6.3
// "capability match on the fallback target").
func TestChatStreamFallbackTargetMustSatisfyStreamingCapability(t *testing.T) {
	t.Parallel()

	const primary = "pa_stream_primary"
	const chatOnlyFallback = "pa_chat_only_fallback"

	harness := newStreamHarness(t, func(h *streamHarness) {
		// Primary streams, and reports authoritative no-commit with ZERO deltas so
		// the fallback walk is genuinely reachable.
		h.seedStreamingAccount("tenant_a", primary, domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)

		// Fallback verifies non-streaming chat only — no chat_streaming fact.
		account := activeAccount(chatOnlyFallback, domain.AuthModeChatGPTCodexOAuth)
		stripped, health, permit := seedAccountHealth(account)
		h.accounts.seed("tenant_a", stripped)
		h.health.Seed("tenant_a", account.ID, health, permit)
		h.capabilities.seed("tenant_a", chatCapabilitySnapshot(account.ID, account.AuthMode, account.Credential.Version, chatModel))

		h.routing.Seed("tenant_a", chatRoutingPolicy(
			[]domain.ProviderAccountID{primary},
			[]domain.ProviderAccountID{chatOnlyFallback},
		))
		h.stream.Script(
			[]streamStep{{outcome: ptrStreamOutcome(streamNotCommitted(domain.ErrCodeProviderRateLimited))}},
			// If the chat-only account is ever attempted it would "succeed" here —
			// which is exactly the capability lie this test forbids.
			[]streamStep{{outcome: ptrStreamOutcome(streamCommitted(domain.FinishStop, domain.ChatUsage{PromptTokens: 1, CompletionTokens: 1}))}},
		)
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-fallback-capability",
		body:    chatStreamBody,
	})

	// The chat-only account must never be attempted for a streaming request.
	for _, account := range harness.stream.Accounts() {
		if account == chatOnlyFallback {
			t.Fatalf("streaming fallback selected %q, which does not support chat_streaming: %v", chatOnlyFallback, harness.stream.Accounts())
		}
	}
	if calls := harness.stream.CallCount(); calls != 1 {
		t.Fatalf("streaming Adapter ran %d times, want 1 (no streaming-incapable fallback target)", calls)
	}
	if response.StatusCode == http.StatusOK {
		t.Fatalf("expected the primary's no-commit failure to surface, got 200 (body=%s)", payload)
	}
}

// AC5/AC6 (converse direction): an account that supports `chat_streaming` but
// NOT non-streaming `chat` IS a viable streaming fallback target. The fallback
// filter must vet candidates against the operation the client actually
// requested; vetting against non-streaming `chat` would wrongly discard a
// perfectly capable streaming account and turn a recoverable no-commit failure
// into a client-visible error (routing spec §6.3: the capability match is on
// `op`+`m`, where `op` is the requested operation).
func TestChatStreamFallbackAcceptsStreamingOnlyTarget(t *testing.T) {
	t.Parallel()

	const primary = "pa_stream_primary_b"
	const streamOnlyFallback = "pa_stream_only_fallback"

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", primary, domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)

		// The fallback verifies chat_streaming ONLY: non-streaming chat is
		// unsupported on this account.
		account := activeAccount(streamOnlyFallback, domain.AuthModeChatGPTCodexOAuth)
		stripped, health, permit := seedAccountHealth(account)
		h.accounts.seed("tenant_a", stripped)
		h.health.Seed("tenant_a", account.ID, health, permit)
		snapshot := chatStreamCapabilitySnapshot(account.ID, account.AuthMode, account.Credential.Version, chatModel, domain.StreamingReal)
		snapshot.Operations[domain.CapabilityOpChat] = domain.CapabilityFact{
			Status:        domain.CapabilityUnsupported,
			Offerable:     false,
			EvidenceClass: domain.EvidenceLiveProbe,
			ProbeSurface:  "/backend-api/chat",
		}
		snapshot.Models = []domain.ModelCapability{{
			ModelSlug: chatModel,
			Operations: map[domain.CapabilityOperation]domain.CapabilityStatus{
				domain.CapabilityOpChatStreaming: domain.CapabilityVerified,
			},
			SurfaceBinding: "/backend-api/chat",
			ObservedAt:     domain.NewTimestamp(spineFixtureTime),
		}}
		h.capabilities.seed("tenant_a", snapshot.WithDerivedFreshness(spineFixtureTime))

		h.routing.Seed("tenant_a", chatRoutingPolicy(
			[]domain.ProviderAccountID{primary},
			[]domain.ProviderAccountID{streamOnlyFallback},
		))
		h.stream.Script(
			// Primary: authoritative no-commit with zero deltas → fallback allowed.
			[]streamStep{{outcome: ptrStreamOutcome(streamNotCommitted(domain.ErrCodeProviderRateLimited))}},
			// Fallback: streams successfully.
			[]streamStep{
				{delta: "served by the streaming-only account"},
				{outcome: ptrStreamOutcome(streamCommitted(domain.FinishStop, domain.ChatUsage{PromptTokens: 6, CompletionTokens: 6}))},
			},
		)
	})

	_, events, payload := harness.streamRequest(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-fallback-streaming-only",
		body:    chatStreamBody,
	})

	accounts := harness.stream.Accounts()
	if len(accounts) != 2 {
		t.Fatalf("streaming attempts = %v, want the primary then the streaming-capable fallback (body=%s)", accounts, payload)
	}
	if accounts[1] != streamOnlyFallback {
		t.Fatalf("fallback attempted %q, want %q", accounts[1], streamOnlyFallback)
	}
	if got := joinDeltas(events); got != "served by the streaming-only account" {
		t.Fatalf("delivered content = %q, want the fallback's stream", got)
	}
	terminals := terminalEvents(events)
	if len(terminals) != 1 || terminals[0].Type != "completed" {
		t.Fatalf("expected exactly one completed terminal, got %v", eventTypes(events))
	}
}

// AC6 / §7.2 rule 2: an Adapter transport error is NOT authoritative proof of
// non-commit, so it must never trigger a fallback attempt.
//
// Cause and effect: the Adapter returns an error rather than a classified
// domain.ChatStreamOutcome, so the Gateway knows only "the call broke", never
// whether the Provider already accepted the generation. §7.2 rule 2 is explicit
// that "an HTTP status, missing response, timeout, reset, or absence of
// client-visible deltas is not proof by itself", and §7.2 rule 4 binds fallback
// to the same boundary. Walking to a second account here would run a SECOND
// upstream generation for one accepted request, which is exactly what
// §7.5 I-CHAT-NO-DUPLICATE-EXEC forbids — and the client would receive HTTP 200
// for the second generation while the first may already have been billed.
//
// Zero deltas reached the client, so the correct answer is a canonical
// possibly-committed HTTP status on the FIRST account, with one Adapter call.
func TestChatStreamTransportErrorNeverFallsBack(t *testing.T) {
	t.Parallel()

	const primary = "pa_stream_transport_primary"
	const fallback = "pa_stream_transport_fallback"

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", primary, domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.seedStreamingAccount("tenant_a", fallback, domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.routing.Seed("tenant_a", chatRoutingPolicy(
			[]domain.ProviderAccountID{primary},
			[]domain.ProviderAccountID{fallback},
		))
		h.stream.Script(
			// Primary breaks mid-call with a transport error and zero deltas. This
			// is NOT proof of non-commit: the payload may already have reached the
			// Provider and the generation may already be running.
			[]streamStep{{transportError: errors.New("connection reset by peer")}},
			// If the fallback is ever attempted it would happily serve a second
			// generation — the duplicate execution this test forbids.
			[]streamStep{
				{delta: "second generation"},
				{outcome: ptrStreamOutcome(streamCommitted(domain.FinishStop, domain.ChatUsage{PromptTokens: 4, CompletionTokens: 2}))},
			},
		)
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-transport-error",
		body:    chatStreamBody,
	})

	accounts := harness.stream.Accounts()
	if len(accounts) != 1 {
		t.Fatalf("streaming attempts = %v, want exactly 1: a transport error is not proof of non-commit (§7.2 rule 2), so falling back would run a second generation for one accepted request (body=%s)", accounts, payload)
	}
	if accounts[0] != primary {
		t.Fatalf("attempted account = %q, want the primary %q", accounts[0], primary)
	}
	if response.StatusCode == http.StatusOK {
		t.Fatalf("commit-uncertain transport failure answered 200, want a canonical failure status (body=%s)", payload)
	}
	if !containsAny(string(payload), string(domain.ErrCodeExecutionPossiblyCommitted)) {
		t.Fatalf("error body = %s, want the possibly-committed canonical code (%s)", payload, domain.ErrCodeExecutionPossiblyCommitted)
	}

	// The replay claim must NOT be abandoned: an uncertain claim is never released
	// for automatic re-execution (§7.3 rule 4).
	logs := harness.log.snapshot()
	var abandons int
	for _, entry := range logs {
		if entry == "replay.abandon" {
			abandons++
		}
	}
	if abandons != 0 {
		t.Fatalf("replay.abandon count = %d, want 0: an uncertain claim is never released for automatic re-execution (§7.3 rule 4); log=%v", abandons, logs)
	}
}
