package contracttest_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// AC1: an explicit cancel signals the RUNNING execution, not just the registry.
// The Adapter blocks on a context-cancel gate and returns a canceled outcome
// only after its execution context is canceled. If the cancel route signaled a
// dead context (the reviewed bug), the gate would never observe cancellation and
// the stream would never reach its canceled terminal.
func TestChatCancelSignalsRunningExecution(t *testing.T) {
	t.Parallel()

	gate := newContextCancelGate()
	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_cancel_signal", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{delta: "before cancel"},
			{cancelGate: gate},
			{outcome: ptrStreamOutcome(streamCanceledWithAbort(domain.ChatUsage{PromptTokens: 6, CompletionTokens: 3}))},
		})
	})

	request, err := http.NewRequest(http.MethodPost, harness.fixture.URL()+"/v1/chat/completions", strings.NewReader(chatStreamBody))
	if err != nil {
		t.Fatalf("NewRequest error = %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+tenantAKey)
	request.Header.Set("Idempotency-Key", "idem-cancel-signal")
	request.Header.Set("Content-Type", "application/json")

	response, err := harness.fixture.Client().Do(request)
	if err != nil {
		t.Fatalf("stream Do error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d, want 200", response.StatusCode)
	}

	execID := make(chan string, 1)
	var mu sync.Mutex
	var collected []sseEvent
	var terminal string
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			line := strings.TrimRight(scanner.Text(), "\r")
			payload, ok := strings.CutPrefix(line, "data: ")
			if !ok {
				continue
			}
			var event sseEvent
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				continue
			}
			mu.Lock()
			collected = append(collected, event)
			if event.Type == "canceled" {
				terminal = event.Type
			}
			mu.Unlock()
			if event.Type == "open" && event.Xpixelplus != nil && event.Xpixelplus.ExecutionID != "" {
				select {
				case execID <- event.Xpixelplus.ExecutionID:
				default:
				}
			}
		}
	}()

	// Wait until the Adapter is in-flight and blocked on its execution context.
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatalf("adapter never reached the cancellation gate")
	}
	executionID := <-execID

	// Cancel the running execution through the public route.
	resp, body, payload := harness.cancelExecution(t, tenantAKey, executionID)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel status = %d, want 200 (body=%s)", resp.StatusCode, payload)
	}
	if body.CancelState != "cancel_requested" {
		t.Fatalf("cancel_state = %q, want cancel_requested (body=%s)", body.CancelState, payload)
	}
	if !body.UpstreamAbortAttempted {
		t.Fatalf("cancel did not report upstream_abort_attempted (body=%s)", payload)
	}
	if body.UpstreamStopConfirmed {
		t.Fatalf("cancel claimed upstream_stop_confirmed without the Adapter proving it (body=%s)", payload)
	}

	// The decisive proof: the Adapter observed the cancellation of its context.
	select {
	case <-gate.Canceled():
	case <-time.After(5 * time.Second):
		t.Fatalf("adapter never observed the canceled execution context: the cancel signal did not reach the running execution")
	}

	<-scanDone
	mu.Lock()
	events := append([]sseEvent(nil), collected...)
	gotTerminal := terminal
	mu.Unlock()

	if gotTerminal != "canceled" {
		t.Fatalf("expected a canceled client terminal after signaling, got %q over %d events (%v)", gotTerminal, len(events), eventTypes(events))
	}
	// The cancel acknowledgement must not emit a second client terminal.
	if got := len(terminalEvents(events)); got != 1 {
		t.Fatalf("client saw %d terminals after an explicit cancel, want exactly 1 (%v)", got, eventTypes(events))
	}
}

// §10.2 item 9: a client disconnect is an implicit cancel. The Adapter observes
// the canceled execution context and the spine still runs its accounting
// terminal (Reconcile exactly once) instead of leaking an untracked execution.
func TestChatStreamDisconnectSettlesImplicitCancel(t *testing.T) {
	t.Parallel()

	gate := newContextCancelGate()
	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_disconnect_account", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{delta: "before disconnect"},
			{cancelGate: gate},
			{outcome: ptrStreamOutcome(streamCanceledWithAbort(domain.ChatUsage{PromptTokens: 5, CompletionTokens: 2}))},
		})
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, harness.fixture.URL()+"/v1/chat/completions", strings.NewReader(chatStreamBody))
	if err != nil {
		t.Fatalf("NewRequest error = %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+tenantAKey)
	request.Header.Set("Idempotency-Key", "idem-disconnect-account")
	request.Header.Set("Content-Type", "application/json")

	response, err := harness.fixture.Client().Do(request)
	if err != nil {
		t.Fatalf("stream Do error = %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d, want 200", response.StatusCode)
	}

	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		response.Body.Close()
		t.Fatalf("adapter never reached the cancellation gate")
	}

	// The client abandons the stream mid-generation.
	response.Body.Close()
	cancel()

	select {
	case <-gate.Canceled():
	case <-time.After(5 * time.Second):
		t.Fatalf("adapter never observed the implicit cancel after disconnect")
	}

	// The accounting terminal still runs once (never zero, e.g. an untracked leak).
	deadline := time.Now().Add(5 * time.Second)
	for harness.admission.ReconcileCalls() < 1 {
		if time.Now().After(deadline) {
			t.Fatalf("admission never settled after disconnect; the implicit cancel left untracked occupancy")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := harness.admission.ReconcileCalls(); got != 1 {
		t.Fatalf("admission Reconcile calls = %d, want exactly 1 after implicit cancel", got)
	}
}

// §10.2 item 10: a timeout is a runtime failure with a distinct canonical cause
// (upstream_timeout + execution_recovery remediation), never a generic failure.
func TestChatStreamTimeoutYieldsDistinctFailureClass(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_timeout", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{delta: "partial before timeout"},
			{outcome: ptrStreamOutcome(streamNotCommitted(domain.ErrCodeUpstreamTimeout))},
		})
	})

	_, events, payload := harness.streamRequest(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-timeout",
		body:    chatStreamBody,
	})

	terminals := terminalEvents(events)
	if len(terminals) != 1 || terminals[0].Type != "failed" {
		t.Fatalf("expected exactly one failed terminal, got %v (body=%s)", eventTypes(events), payload)
	}
	code, _ := terminals[0].Error["code"].(string)
	if code != "upstream_timeout" {
		t.Fatalf("timeout failure code = %q, want upstream_timeout (error=%v)", code, terminals[0].Error)
	}
	remediation, _ := terminals[0].Error["remediation"].(string)
	if remediation != "execution_recovery" {
		t.Fatalf("timeout remediation = %q, want execution_recovery (error=%v)", remediation, terminals[0].Error)
	}
}
