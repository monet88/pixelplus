package contracttest_test

import (
	"net/http"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// The `stream_opened` audit must describe what actually happened. A streaming
// Adapter that fails closed never opens a stream — the request is answered with
// a canonical HTTP status — so no `stream_opened` record may exist.
//
// Cause and effect: recording the audit before the Adapter runs makes the trail
// claim a stream was opened for a request the client only ever saw as an HTTP
// error. An operator auditing "which streams opened against which account" would
// count generations that never happened.
func TestChatStreamFailClosedRecordsNoOpenedAudit(t *testing.T) {
	t.Parallel()

	harness := newChatHarness(t, func(h *chatHarness) {
		// No ChatStreamAdapter is injected, so composition keeps the production
		// fail-closed streaming Adapter.
		h.seedActive("tenant_a", "pa_no_stream_adapter", domain.AuthModeChatGPTCodexOAuth)
		h.capabilities.seed("tenant_a", chatStreamCapabilitySnapshot("pa_no_stream_adapter", domain.AuthModeChatGPTCodexOAuth, 1, chatModel, domain.StreamingReal))
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-failclosed-audit",
		body:    chatStreamBody,
	})
	if response.StatusCode == http.StatusOK {
		t.Fatalf("fail-closed streaming Adapter answered 200, want a canonical error (body=%s)", payload)
	}

	for _, event := range harness.chatAudit.snapshot() {
		if event.Action == ports.AuditChatStreamOpened {
			t.Fatalf("audit recorded %q for a request whose stream never opened (client got HTTP %d)", event.Action, response.StatusCode)
		}
	}
}

// One accepted stream produces exactly ONE `stream_opened` record, even when the
// Gateway walks a fallback chain. Recording per attempt would report several
// opened streams for attempts that never opened, and would attribute opens to
// accounts that served nothing.
func TestChatStreamFallbackRecordsSingleOpenedAudit(t *testing.T) {
	t.Parallel()

	const primary = "pa_audit_primary"
	const fallback = "pa_audit_fallback"

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", primary, domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.seedStreamingAccount("tenant_a", fallback, domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.routing.Seed("tenant_a", chatRoutingPolicy(
			[]domain.ProviderAccountID{primary},
			[]domain.ProviderAccountID{fallback},
		))
		h.stream.Script(
			// Primary: authoritative no-commit with zero deltas → fallback allowed,
			// and nothing was ever opened on this account.
			[]streamStep{{outcome: ptrStreamOutcome(streamNotCommitted(domain.ErrCodeProviderRateLimited))}},
			[]streamStep{
				{delta: "served by fallback"},
				{outcome: ptrStreamOutcome(streamCommitted(domain.FinishStop, domain.ChatUsage{PromptTokens: 4, CompletionTokens: 3}))},
			},
		)
	})

	_, events, payload := harness.streamRequest(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-fallback-audit",
		body:    chatStreamBody,
	})
	if got := joinDeltas(events); got != "served by fallback" {
		t.Fatalf("content = %q, want the fallback's stream (body=%s)", got, payload)
	}

	var opened []ports.ChatAuditEvent
	for _, event := range harness.chatAudit.snapshot() {
		if event.Action == ports.AuditChatStreamOpened {
			opened = append(opened, event)
		}
	}
	if len(opened) != 1 {
		t.Fatalf("stream_opened audit records = %d, want exactly 1 for one accepted stream: %+v", len(opened), opened)
	}
	if opened[0].ProviderAccountID != fallback {
		t.Fatalf("stream_opened attributed to %q, want the account that actually served the stream (%q)", opened[0].ProviderAccountID, fallback)
	}
}
