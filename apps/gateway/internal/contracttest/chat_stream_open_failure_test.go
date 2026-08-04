package contracttest_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// A generation that committed upstream but whose first SSE write failed is
// possibly committed, even though the client never received a valid open frame.
// The replay claim must remain owned: abandoning it would let the same
// Idempotency-Key launch a second billed generation (§7.3 rule 4, §7.5).
//
// This test still enters through the public proof seam: it sends a real HTTP POST
// through Runtime.Handler() over httptest. The wrapper changes only the network
// writer, making the first body write fail the way a broken client connection can
// fail after the upstream generation already committed.
func TestChatStreamCommittedOpenWriteFailureKeepsReplayClaim(t *testing.T) {
	t.Parallel()

	harness := newStreamHarness(t, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_open_write_failure", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		// Zero deltas: the lazy stream does not try to open until it owes the
		// committed terminal. The first SSE body write is therefore the open frame.
		h.stream.Script([]streamStep{
			{outcome: ptrStreamOutcome(streamCommitted(domain.FinishStop, domain.ChatUsage{PromptTokens: 4, CompletionTokens: 1}))},
		})
	})

	var failWrites atomic.Bool
	failWrites.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		wrapped := http.ResponseWriter(writer)
		if failWrites.Load() && request.Method == http.MethodPost && request.URL.Path == "/v1/chat/completions" {
			wrapped = &failingBodyWriter{ResponseWriter: writer}
		}
		harness.fixture.Runtime().Handler().ServeHTTP(wrapped, request)
	}))
	t.Cleanup(server.Close)

	do := func() (*http.Response, []byte) {
		request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(chatStreamBody))
		if err != nil {
			t.Fatalf("NewRequest error = %v", err)
		}
		request.Header.Set("Authorization", "Bearer "+tenantAKey)
		request.Header.Set("Idempotency-Key", "idem-open-write-failure")
		request.Header.Set("Content-Type", "application/json")

		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatalf("Do error = %v", err)
		}
		defer response.Body.Close()
		payload, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatalf("ReadAll error = %v", err)
		}
		return response, payload
	}

	firstResponse, firstPayload := do()
	if strings.Contains(string(firstPayload), `"code":`) {
		t.Fatalf("handler appended a JSON error body to an already-committed SSE response: status=%d body=%s", firstResponse.StatusCode, firstPayload)
	}

	// The second request uses a healthy writer. The uncertain claim must still be
	// owned, so it returns idempotency_in_progress and never calls the Adapter a
	// second time. If the claim were abandoned, this request would run another
	// generation and return 200.
	failWrites.Store(false)
	secondResponse, secondPayload := do()
	if secondResponse.StatusCode != http.StatusConflict {
		t.Fatalf("second status = %d, want 409 idempotency_in_progress: the committed write failure must keep the claim (body=%s)", secondResponse.StatusCode, secondPayload)
	}
	if !strings.Contains(string(secondPayload), string(domain.ErrCodeIdempotencyInProgress)) {
		t.Fatalf("second body = %s, want %s", secondPayload, domain.ErrCodeIdempotencyInProgress)
	}
	if calls := harness.stream.CallCount(); calls != 1 {
		t.Fatalf("streaming Adapter ran %d times, want exactly 1: abandoning the claim after a committed write failure would launch a second generation", calls)
	}
	for _, entry := range harness.log.snapshot() {
		if entry == "replay.abandon" {
			t.Fatalf("committed write failure abandoned the replay claim; log=%v", harness.log.snapshot())
		}
	}
}

// failingBodyWriter preserves the real server's headers and Flush support but
// fails every body write. It models a connection that breaks exactly when the
// Gateway tries to deliver the first SSE frame.
type failingBodyWriter struct {
	http.ResponseWriter
}

func (*failingBodyWriter) Write([]byte) (int, error) {
	return 0, errors.New("client connection failed during first SSE write")
}

func (writer *failingBodyWriter) Flush() {
	if flusher, ok := writer.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
