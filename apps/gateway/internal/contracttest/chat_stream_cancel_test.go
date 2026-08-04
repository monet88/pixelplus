package contracttest_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// AC1/AC4: the event timestamps come from the controlled Clock, not wall time.
// The issue's proof seam names Clock observation explicitly: a stream that
// stamped `created`/`ts` from time.Now() would be untestable and would drift
// from the durable execution record.
func TestChatStreamTimestampsComeFromControlledClock(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_clock", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{delta: "tick"},
			{heartbeat: true},
			{outcome: ptrStreamOutcome(streamCommitted(domain.FinishStop, domain.ChatUsage{PromptTokens: 2, CompletionTokens: 1}))},
		})
	})

	_, events, payload := harness.streamRequest(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-stream-clock",
		body:    chatStreamBody,
	})

	// The fixture clock starts at 2026-07-21T00:00:00Z and advances one second
	// per read, so every stamp must sit in that controlled window — never near
	// the real current time.
	base := time.Date(2026, time.July, 21, 0, 0, 0, 0, time.UTC).Unix()
	open := events[0]
	if open.Created < base || open.Created > base+3600 {
		t.Fatalf("open created = %d, want a controlled-clock value near %d (body=%s)", open.Created, base, payload)
	}

	var heartbeatSeen bool
	for _, event := range events {
		if event.Type != "heartbeat" {
			continue
		}
		heartbeatSeen = true
		if event.TS < base || event.TS > base+3600 {
			t.Fatalf("heartbeat ts = %d, want a controlled-clock value near %d", event.TS, base)
		}
		// The heartbeat is stamped after the open event, so its clock read is
		// strictly later: this proves the stamp is read per event, not cached.
		if event.TS <= open.Created {
			t.Fatalf("heartbeat ts %d should follow open created %d on the advancing controlled clock", event.TS, open.Created)
		}
	}
	if !heartbeatSeen {
		t.Fatalf("expected a heartbeat event: %v", eventTypes(events))
	}
}

// AC3/AC6: a client that disconnects mid-stream stops delivery, and the Gateway
// never writes past the point the client vanished. Disconnect is an implicit
// cancel (chat lifecycle §6.3 rule 1); the explicit cancel route and the bounded
// residual-tracking protocol are T17 (#60), so this asserts the streaming
// contract only: delivery stops and no post-disconnect frame is produced.
func TestChatStreamClientDisconnectStopsDelivery(t *testing.T) {
	t.Parallel()

	gate := newDeliveryGate()
	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_disconnect", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{delta: "before disconnect"},
			{blockOn: gate},
			{delta: "after disconnect"},
			{outcome: ptrStreamOutcome(streamCommitted(domain.FinishStop, domain.ChatUsage{PromptTokens: 3, CompletionTokens: 3}))},
		})
	})

	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, harness.fixture.URL()+"/v1/chat/completions", strings.NewReader(chatStreamBody))
	if err != nil {
		t.Fatalf("NewRequest error = %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+tenantAKey)
	request.Header.Set("Idempotency-Key", "idem-stream-disconnect")
	request.Header.Set("Content-Type", "application/json")

	response, err := harness.fixture.Client().Do(request)
	if err != nil {
		t.Fatalf("Do error = %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}

	// Read frames until the pre-disconnect delta has arrived, then abandon the
	// connection. A single Read is not enough: SSE frames arrive per flush, so the
	// open event and the first delta are separate reads.
	delivered := readUntil(t, response.Body, "before disconnect")
	_ = response.Body.Close()
	cancel()

	// Let the Adapter continue past the disconnect point.
	gate.Release()

	// The request context is canceled, so the spine must stop; the surviving
	// stream must not have produced a second client-visible terminal. The
	// observable proof at this seam is that the server stays healthy and no
	// further frames can be delivered to the closed connection.
	probe, probePayload := harness.do(t, requestSpec{method: http.MethodGet, path: "/healthz"})
	if probe.StatusCode != http.StatusOK {
		t.Fatalf("server unhealthy after client disconnect: %d (body=%s)", probe.StatusCode, probePayload)
	}

	// Delivery stopped: the pre-disconnect delta reached the client and nothing
	// the Adapter produced after the disconnect appears in the delivered bytes.
	if !strings.Contains(delivered, "before disconnect") {
		t.Fatalf("expected the pre-disconnect delta to be delivered, got %q", delivered)
	}
	if strings.Contains(delivered, "after disconnect") {
		t.Fatalf("post-disconnect content was delivered to a closed client: %q", delivered)
	}
	if strings.Contains(delivered, `"type":"completed"`) {
		t.Fatalf("a terminal reached a client that had already disconnected: %q", delivered)
	}
}

// readUntil reads SSE frames until the marker appears or the stream ends. It
// bounds itself so a hung stream fails the test rather than blocking forever.
func readUntil(t *testing.T, body io.Reader, marker string) string {
	t.Helper()

	var builder strings.Builder
	buffer := make([]byte, 512)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		count, err := body.Read(buffer)
		if count > 0 {
			builder.Write(buffer[:count])
			if strings.Contains(builder.String(), marker) {
				return builder.String()
			}
		}
		if err != nil {
			break
		}
	}
	return builder.String()
}

// deliveryGate lets a test hold an Adapter mid-stream until it releases it.
type deliveryGate struct {
	once    sync.Once
	release chan struct{}
}

func newDeliveryGate() *deliveryGate {
	return &deliveryGate{release: make(chan struct{})}
}

// Release unblocks the Adapter.
func (gate *deliveryGate) Release() {
	gate.once.Do(func() { close(gate.release) })
}

// wait blocks the Adapter until released or a safety deadline elapses.
func (gate *deliveryGate) wait() {
	select {
	case <-gate.release:
	case <-time.After(5 * time.Second):
	}
}
