package contracttest_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// recordingResidualStore is a controlled ChatResidualStore that records
// acquire/release calls and bounds per-Tenant capacity.
type recordingResidualStore struct {
	mu       sync.Mutex
	limit    int
	held     map[string]int
	acquired []ports.ChatResidualHold
	released []ports.ChatResidualHold
}

func newRecordingResidualStore(limit int) *recordingResidualStore {
	return &recordingResidualStore{limit: limit, held: make(map[string]int)}
}

func (store *recordingResidualStore) Acquire(_ context.Context, hold ports.ChatResidualHold) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.held[string(hold.TenantID)] >= store.limit {
		return ports.ErrChatResidualCapacityFull
	}
	store.held[string(hold.TenantID)]++
	store.acquired = append(store.acquired, hold)
	return nil
}

func (store *recordingResidualStore) Release(_ context.Context, hold ports.ChatResidualHold) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.held[string(hold.TenantID)] > 0 {
		store.held[string(hold.TenantID)]--
	}
	store.released = append(store.released, hold)
	return nil
}

func (store *recordingResidualStore) Acquired() []ports.ChatResidualHold {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]ports.ChatResidualHold(nil), store.acquired...)
}

func (store *recordingResidualStore) Released() []ports.ChatResidualHold {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]ports.ChatResidualHold(nil), store.released...)
}

var _ ports.ChatResidualStore = (*recordingResidualStore)(nil)

// cancelResponseWire mirrors the published ChatCancelResponse schema.
type cancelResponseWire struct {
	ExecutionID            string `json:"execution_id"`
	CancelState            string `json:"cancel_state"`
	UpstreamAbortAttempted bool   `json:"upstream_abort_attempted"`
	UpstreamStopConfirmed  bool   `json:"upstream_stop_confirmed"`
	RequestID              string `json:"request_id,omitempty"`
}

func (harness *streamHarness) cancelExecution(t *testing.T, bearer, executionID string) (*http.Response, cancelResponseWire, []byte) {
	t.Helper()
	resp, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/chat/executions/" + executionID + "/cancel",
		bearer: bearer,
	})
	var body cancelResponseWire
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(payload, &body); err != nil {
			t.Fatalf("decode cancel response: %v (body=%s)", err, payload)
		}
	}
	return resp, body, payload
}

func extractExecutionID(t *testing.T, events []sseEvent) string {
	t.Helper()
	for _, event := range events {
		if event.Type == "open" && event.Xpixelplus != nil {
			return event.Xpixelplus.ExecutionID
		}
	}
	t.Fatalf("no open event with execution_id found in %d events", len(events))
	return ""
}

// §10.2 item 12: Cancel is idempotent. A second cancel on an already-terminal
// execution is a success no-op, not an error. It emits no second terminal and
// creates/releases no second hold.
func TestChatCancelIsIdempotent(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_cancel_idem", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{delta: "partial"},
			{outcome: ptrStreamOutcome(streamCanceledNonCancelable(domain.ChatUsage{PromptTokens: 3, CompletionTokens: 2}))},
		})
	})

	_, events, payload := harness.streamRequest(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-cancel-idem",
		body:    chatStreamBody,
	})

	terminals := terminalEvents(events)
	if len(terminals) != 1 {
		t.Fatalf("expected exactly 1 terminal, got %d (body=%s)", len(terminals), payload)
	}
	if terminals[0].Type != "canceled" {
		t.Fatalf("terminal = %q, want canceled (body=%s)", terminals[0].Type, payload)
	}

	executionID := extractExecutionID(t, events)

	// First cancel after terminal: idempotent success no-op.
	resp1, body1, p1 := harness.cancelExecution(t, tenantAKey, executionID)
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first cancel status = %d, want 200 (body=%s)", resp1.StatusCode, p1)
	}
	if body1.CancelState != "canceled" {
		t.Fatalf("first cancel_state = %q, want canceled (body=%s)", body1.CancelState, p1)
	}
	if body1.UpstreamStopConfirmed {
		t.Fatalf("first cancel claims upstream_stop_confirmed: %s", p1)
	}

	// Second cancel: same idempotent no-op, no error.
	resp2, body2, p2 := harness.cancelExecution(t, tenantAKey, executionID)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("second cancel status = %d, want 200 (body=%s)", resp2.StatusCode, p2)
	}
	if body2.CancelState != "canceled" {
		t.Fatalf("second cancel_state = %q, want canceled (body=%s)", body2.CancelState, p2)
	}
}

// §10.2 item 13: Cancel on Tenant A does not change Tenant B counters. A
// Tenant B cancel of Tenant A's execution returns the same 404 as an unknown
// execution (non-enumeration). Tenant A cancel of the same execution returns
// the idempotent canceled acknowledgement.
func TestChatCancelForeignTenantReturns404(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_cancel_xtenant", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.seedStreamingAccount("tenant_b", "pa_cancel_xtenant_b", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{delta: "tenant A work"},
			{outcome: ptrStreamOutcome(streamCommitted(domain.FinishStop, domain.ChatUsage{PromptTokens: 2, CompletionTokens: 1}))},
		})
	})

	_, events, payload := harness.streamRequest(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-cancel-xtenant",
		body:    chatStreamBody,
	})
	if len(events) == 0 {
		t.Fatalf("no stream events (body=%s)", payload)
	}
	executionID := extractExecutionID(t, events)

	// Tenant B cancel of Tenant A's terminal execution: 404 (non-enumeration).
	respB, _, pB := harness.cancelExecution(t, tenantBKey, executionID)
	if respB.StatusCode != http.StatusNotFound {
		t.Fatalf("tenant B cancel of tenant A execution: status = %d, want 404 (body=%s)", respB.StatusCode, pB)
	}
	if !strings.Contains(string(pB), "resource_not_found") {
		t.Fatalf("tenant B cancel body = %s, want resource_not_found", pB)
	}

	// Tenant A cancel of the same execution: idempotent success (canceled).
	respA, bodyA, pA := harness.cancelExecution(t, tenantAKey, executionID)
	if respA.StatusCode != http.StatusOK {
		t.Fatalf("tenant A cancel of own terminal execution: status = %d, want 200 (body=%s)", respA.StatusCode, pA)
	}
	if bodyA.CancelState != "canceled" {
		t.Fatalf("tenant A cancel_state = %q, want canceled (body=%s)", bodyA.CancelState, pA)
	}
}

// §10.2 item 6: Cancel of an unknown execution_id returns 404
// resource_not_found. Unknown, foreign, and already-gone executions share the
// same non-enumerating shape.
func TestChatCancelUnknownExecutionReturns404(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_cancel_unknown", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
	})

	resp, _, payload := harness.cancelExecution(t, tenantAKey, "exec_does_not_exist")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cancel unknown execution: status = %d, want 404 (body=%s)", resp.StatusCode, payload)
	}
	if !strings.Contains(string(payload), "resource_not_found") {
		t.Fatalf("cancel unknown body = %s, want resource_not_found", payload)
	}
}

// §10.2 item 6: Cancel requires authentication and the chat.completions scope.
func TestChatCancelRequiresAuthentication(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_cancel_auth", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
	})

	resp, _, payload := harness.cancelExecution(t, "invalid-key", "exec_anything")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("cancel with invalid key: status = %d, want 401 (body=%s)", resp.StatusCode, payload)
	}
}

// §10.2 items 7 and 11: A non-cancelable canceled terminal holds occupancy and
// reservation until the accounting terminal (X6). The settleStream split path
// keeps the reservation alive when the upstream may survive. With a nil
// residual drain (production default), FINAL usage cannot be confirmed, so the
// settlement settles the Adapter's observed usage as a KNOWN conservative floor
// (a debit no smaller than known usage) and records an operator-visible
// accounting fault for the unknown remainder (§6.5 rule 3).
//
// This test proves the X5/X6 split is wired through composition: a canceled
// stream with UpstreamStopConfirmed=false triggers the residual path, the
// admission store records exactly one settle (not zero, not two), and the
// accounting-fault path is exercised (item 11).
func TestChatCancelNonCancelableSettlesOnceConservatively(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_cancel_settle", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{delta: "before cancel"},
			{outcome: ptrStreamOutcome(streamCanceledNonCancelable(domain.ChatUsage{PromptTokens: 4, CompletionTokens: 3}))},
		})
	})

	_, events, payload := harness.streamRequest(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-cancel-settle",
		body:    chatStreamBody,
	})

	terminals := terminalEvents(events)
	if len(terminals) != 1 || terminals[0].Type != "canceled" {
		t.Fatalf("expected exactly 1 canceled terminal, got %v (body=%s)", terminals, payload)
	}

	// The non-cancelable terminal has UpstreamStopConfirmed=false, so the
	// settleStream path holds the reservation and runs the (nil) drain. The key
	// proof: admission settles exactly once (not zero, not twice).
	if got := harness.admission.ReconcileCalls(); got != 1 {
		t.Fatalf("admission Reconcile calls = %d, want 1 (exactly one settle per execution)", got)
	}

	// The settled usage reflects the known tokens the Adapter already observed
	// (a conservative debit no smaller than known usage), never zero and never
	// an optimistic refund of the unknown remainder (§6.5 rule 3).
	settled := harness.admission.SettledUsage()
	if len(settled) != 1 {
		t.Fatalf("settled usage count = %d, want 1", len(settled))
	}
	if !settled[0].Known {
		t.Fatalf("settled usage Known = false, want true: the Adapter observed usage on a committed canceled outcome (§6.2 rule 3)")
	}
	if settled[0].PromptTokens != 4 || settled[0].CompletionTokens != 3 {
		t.Fatalf("settled usage = %d/%d, want 4/3", settled[0].PromptTokens, settled[0].CompletionTokens)
	}

	// §10.2 item 11: final usage is missing after the bounded drain, so the
	// conservative settlement must be operator-visible. The audit trail carries
	// the residual accounting-fault action.
	audited := harness.chatAudit.snapshot()
	var accountingFault bool
	for _, event := range audited {
		if event.Action == ports.AuditChatResidual {
			accountingFault = true
			break
		}
	}
	if !accountingFault {
		t.Fatalf("no residual accounting-fault audit recorded; the conservative settlement was not operator-visible (len=%d)", len(audited))
	}
}
