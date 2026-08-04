package contracttest_test

import (
	"net/http"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// #90 / chat lifecycle §6.5 rule 3: "Reconcile the A6 reservation at X6 to final
// actual input+output usage". The quota ledger must observe the SAME token counts
// the client was shown.
//
// Cause and effect: A6 reserves quota capacity against Tenant + Client API Key
// using an estimate. The Adapter then observes what the generation actually cost.
// If settlement cannot carry that observation, occupancy is released correctly but
// the quota ledger keeps only the estimate forever — a Tenant's recorded
// consumption never converges on the truth, which is an anti-abuse accounting
// hole, not a cosmetic gap.
func TestChatSettlementReconcilesActualUsage(t *testing.T) {
	t.Parallel()

	harness := newChatHarness(t, func(h *chatHarness) {
		h.seedActive("tenant_a", "pa_usage", domain.AuthModeChatGPTCodexOAuth)
		outcome := chatSuccess("pa_usage", "", "", chatModel)
		outcome.Completion.Usage = domain.ChatUsage{PromptTokens: 11, CompletionTokens: 7}
		h.adapter.Script(outcome)
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey,
		idemKey: "idem-usage-reconcile", body: chatSuccessBody,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}

	settled := harness.admission.SettledUsage()
	if len(settled) != 1 {
		t.Fatalf("admission settles = %d, want exactly 1", len(settled))
	}
	usage := settled[0]
	if !usage.Known {
		t.Fatalf("settlement reported usage as unknown even though the Adapter observed it: %+v", usage)
	}
	if usage.PromptTokens != 11 || usage.CompletionTokens != 7 {
		t.Fatalf("settled usage = %d prompt / %d completion, want 11/7 — the quota ledger must see the same tokens the client was shown", usage.PromptTokens, usage.CompletionTokens)
	}
}

// #90: the streaming spine settles the usage carried on its `completed` terminal.
func TestChatStreamSettlementReconcilesActualUsage(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_stream_usage", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{delta: "counted"},
			{outcome: ptrStreamOutcome(streamCommitted(domain.FinishStop, domain.ChatUsage{PromptTokens: 9, CompletionTokens: 4}))},
		})
	})

	_, events, payload := harness.streamRequest(t, requestSpec{
		method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey,
		idemKey: "idem-stream-usage-reconcile", body: chatStreamBody,
	})
	terminals := terminalEvents(events)
	if len(terminals) != 1 || terminals[0].Type != "completed" {
		t.Fatalf("expected one completed terminal, got %v (body=%s)", eventTypes(events), payload)
	}
	if terminals[0].Usage == nil || terminals[0].Usage.PromptTokens != 9 || terminals[0].Usage.CompletionTokens != 4 {
		t.Fatalf("client-visible usage = %+v, want 9/4", terminals[0].Usage)
	}

	settled := harness.admission.SettledUsage()
	if len(settled) != 1 {
		t.Fatalf("admission settles = %d, want exactly 1", len(settled))
	}
	usage := settled[0]
	if !usage.Known {
		t.Fatalf("streaming settlement reported usage as unknown even though the terminal carried it: %+v", usage)
	}
	if usage.PromptTokens != 9 || usage.CompletionTokens != 4 {
		t.Fatalf("settled usage = %d prompt / %d completion, want 9/4 to match the terminal the client saw", usage.PromptTokens, usage.CompletionTokens)
	}
}

// #90 / §6.5 rule 3 fail-closed clause: "If final usage cannot be obtained after
// bounded drain/recovery, fail closed for anti-abuse accounting: retain the full
// reservation ... never assume zero."
//
// A commit-uncertain outcome yields no trustworthy usage, so settlement must mark
// usage UNKNOWN rather than settling a zero debit. Settling zero would let a
// Tenant burn real Provider tokens and have the ledger record nothing.
func TestChatUncertainOutcomeNeverSettlesZeroUsage(t *testing.T) {
	t.Parallel()

	harness := newChatHarness(t, func(h *chatHarness) {
		h.seedActive("tenant_a", "pa_uncertain", domain.AuthModeChatGPTCodexOAuth)
		h.adapter.Script(domain.ChatOutcome{
			Class:  domain.ChatOutcomeCommitted,
			Commit: domain.CommitUnknown,
		})
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey,
		idemKey: "idem-usage-uncertain", body: chatSuccessBody,
	})
	if response.StatusCode == http.StatusOK {
		t.Fatalf("commit-unknown answered 200, want a canonical failure (body=%s)", payload)
	}

	for _, usage := range harness.admission.SettledUsage() {
		if usage.Known {
			t.Fatalf("settlement claimed known usage for a commit-uncertain execution: %+v", usage)
		}
	}
}

// #90 / §6.5 rule 3 fail-closed clause, streaming direction: a stream that
// FAILS after emitting deltas has no trustworthy final usage, so settlement must
// leave usage unknown.
//
// Cause and effect: the Adapter broke mid-generation, so `terminal.Usage` is the
// zero value — not an observation that the generation cost nothing. If the spine
// settled that zero as authoritative, a Tenant whose stream died after burning
// real Provider tokens would have those tokens recorded as zero, which is the
// anti-abuse hole §6.5 rule 3 closes with "never assume zero".
func TestChatStreamFailedTerminalNeverSettlesZeroUsage(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_stream_failed_usage", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			// Deltas reached the client, so the generation demonstrably consumed
			// upstream tokens...
			{delta: "partial answer"},
			// ...and then the attempt died without a usable usage report.
			{outcome: ptrStreamOutcome(streamUnknown())},
		})
	})

	_, events, payload := harness.streamRequest(t, requestSpec{
		method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey,
		idemKey: "idem-stream-failed-usage", body: chatStreamBody,
	})

	terminals := terminalEvents(events)
	if len(terminals) != 1 || terminals[0].Type != "failed" {
		t.Fatalf("expected exactly one failed terminal, got %v (body=%s)", eventTypes(events), payload)
	}

	settled := harness.admission.SettledUsage()
	if len(settled) != 1 {
		t.Fatalf("admission settles = %d, want exactly 1", len(settled))
	}
	if settled[0].Known {
		t.Fatalf("settlement claimed known usage for a failed stream with no usable usage report: %+v — §6.5 rule 3 forbids assuming zero", settled[0])
	}
}
