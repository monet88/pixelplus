package contracttest_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/composition"
	"github.com/monet88/pixelplus/apps/gateway/internal/contracttest"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// newStreamHarnessWithResidual is a stream harness that also swaps in a control
// admission store and residual ports, so an occupancy/exhaustion test can prove
// the residual protocol through the public HTTP seam without touching the shared
// permissive admission stub.
func newStreamHarnessWithResidual(
	t *testing.T,
	admission ports.AdmissionStore,
	store ports.ChatResidualStore,
	drain ports.ChatResidualDrain,
	configure func(*streamHarness),
) *streamHarness {
	t.Helper()
	harness := &streamHarness{}
	harness.chatHarness = newChatHarnessWithOptions(t, func(chat *chatHarness, options *contracttest.Options) {
		harness.chatHarness = chat
		harness.stream = newScriptedChatStreamAdapter(chat.log)
		harness.leases = newRecordingStreamLeases(chat.log, composition.NewControlledChatStreamLeaseStore())
		options.ChatStreamAdapter = harness.stream
		options.ChatStreamLeases = harness.leases
		options.Admission = admission
		options.ResidualStore = store
		options.ResidualDrain = drain
		if configure != nil {
			configure(harness)
		}
	})
	return harness
}

// limitAdmissionStore enforces a strict occupancy cap (0 = unlimited), so a
// second A6 is rejected with the concurrency stage while a retained execution
// keeps the slot full.
type limitAdmissionStore struct {
	mu             sync.Mutex
	limit          int
	occupied       int
	admitted       int
	rejected       int
	reconcileCalls int
}

func newLimitAdmissionStore(limit int) *limitAdmissionStore {
	return &limitAdmissionStore{limit: limit}
}

func (store *limitAdmissionStore) Admit(_ context.Context, request ports.AdmissionRequest) (ports.AdmissionDecision, ports.AdmissionReservation, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.limit > 0 && store.occupied >= store.limit {
		store.rejected++
		return ports.AdmissionDecision{Admitted: false, Stage: ports.AdmissionStageConcurrency}, ports.AdmissionReservation{}, nil
	}
	store.occupied++
	store.admitted++
	return ports.AdmissionDecision{Admitted: true},
		ports.AdmissionReservation{Principal: request.Principal, Operation: request.Operation},
		nil
}

// Reconcile releases the occupancy slot. It honors ctx for the same reason a
// real ledger must: a canceled context means the write never reached the store,
// so reporting success would let an occupancy leak pass as a clean settle.
func (store *limitAdmissionStore) Reconcile(ctx context.Context, _ ports.AdmissionReservation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.reconcileCalls++
	if store.occupied > 0 {
		store.occupied--
	}
	return nil
}

func (store *limitAdmissionStore) occupiedCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.occupied
}

func (store *limitAdmissionStore) admittedCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.admitted
}

func (store *limitAdmissionStore) rejectedCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.rejected
}

func (store *limitAdmissionStore) reconcileCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.reconcileCalls
}

// fullResidualStore reports ErrChatResidualCapacityFull on every acquire,
// modeling a same-Tenant residual tracking limit that is already exhausted.
type fullResidualStore struct {
	acquired atomic.Int32
}

func (store *fullResidualStore) Acquire(_ context.Context, _ ports.ChatResidualHold) error {
	store.acquired.Add(1)
	return ports.ErrChatResidualCapacityFull
}

func (store *fullResidualStore) Release(_ context.Context, _ ports.ChatResidualHold) error {
	return nil
}

// blockingResidualDrain holds the surviving upstream until the test releases it,
// so the occupancy stays retained (X5 != X6) while a second request arrives.
type blockingResidualDrain struct {
	entered     chan struct{}
	release     chan struct{}
	enteredOnce sync.Once
	releaseOnce sync.Once
}

func newBlockingResidualDrain() *blockingResidualDrain {
	return &blockingResidualDrain{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (drain *blockingResidualDrain) Drain(ctx context.Context, _ ports.ChatResidualDrainRequest) (ports.ChatResidualOutcome, error) {
	drain.enteredOnce.Do(func() { close(drain.entered) })
	select {
	case <-drain.release:
		return ports.ChatResidualOutcome{UsageKnown: false}, nil
	case <-ctx.Done():
		return ports.ChatResidualOutcome{}, ctx.Err()
	}
}

func (drain *blockingResidualDrain) Release() {
	drain.releaseOnce.Do(func() { close(drain.release) })
}

// §10.2 item 8: when residual tracking is exhausted, the original Tenant/key
// occupancy remains held while the surviving upstream drains (X5 != X6), so a
// new A6 request is rejected by the admission cap. Occupancy is released exactly
// once at the accounting terminal (X6).
func TestChatCancelResidualExhaustedRetainsOccupancyAndRejectsNewA6(t *testing.T) {
	t.Parallel()

	admissionStore := newLimitAdmissionStore(1)
	residualStore := &fullResidualStore{}
	drain := newBlockingResidualDrain()
	harness := newStreamHarnessWithResidual(t, admissionStore, residualStore, drain, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_residual_full", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{delta: "residual work"},
			{outcome: ptrStreamOutcome(streamCanceledNonCancelable(domain.ChatUsage{PromptTokens: 2, CompletionTokens: 1}))},
		})
	})

	type streamBodyResult struct {
		resp    *http.Response
		payload []byte
		err     error
	}
	done := make(chan streamBodyResult, 1)
	go func() {
		request, err := http.NewRequest(http.MethodPost, harness.fixture.URL()+"/v1/chat/completions", strings.NewReader(chatStreamBody))
		if err != nil {
			done <- streamBodyResult{err: err}
			return
		}
		request.Header.Set("Authorization", "Bearer "+tenantAKey)
		request.Header.Set("Idempotency-Key", "idem-residual-full")
		request.Header.Set("Content-Type", "application/json")
		resp, err := harness.fixture.Client().Do(request)
		if err != nil {
			done <- streamBodyResult{resp: resp, err: err}
			return
		}
		payload, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		done <- streamBodyResult{resp: resp, payload: payload}
	}()

	select {
	case <-drain.entered:
	case <-time.After(5 * time.Second):
		t.Fatalf("stream 1 never reached the residual drain")
	}

	// The client already saw the canceled terminal, but the surviving upstream
	// still occupies the single slot; it must not be freed at X5.
	if got := admissionStore.occupiedCount(); got != 1 {
		t.Fatalf("occupied = %d while residual drains, want 1 (occupancy must be held until X6)", got)
	}

	// A new A6 request must be rejected because the retained occupancy keeps the
	// cap full (item 8: no path frees capacity for another accept while the
	// prior upstream survives).
	resp2, payload2 := harness.do(t, requestSpec{
		method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey,
		idemKey: "idem-residual-full-2", body: chatStreamBody,
	})
	if resp2.StatusCode == http.StatusOK {
		t.Fatalf("new A6 was admitted while retained occupancy kept the limit full (body=%s)", payload2)
	}
	if got := admissionStore.rejectedCount(); got != 1 {
		t.Fatalf("admission rejections = %d, want 1 (the new request must be rejected at A6)", got)
	}
	if got := admissionStore.admittedCount(); got != 1 {
		t.Fatalf("admitted = %d, want exactly 1 (only the first execution was admitted)", got)
	}

	// The surviving upstream finishes; X6 releases the occupancy exactly once.
	drain.Release()
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("stream 1 error = %v", result.err)
		}
		if !strings.Contains(string(result.payload), `"type":"canceled"`) {
			t.Fatalf("stream 1 did not deliver the canceled terminal (body=%s)", result.payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("stream 1 did not finish after the drain released")
	}

	if got := admissionStore.reconcileCount(); got != 1 {
		t.Fatalf("Reconcile calls = %d, want exactly 1 at the accounting terminal", got)
	}
	if got := admissionStore.occupiedCount(); got != 0 {
		t.Fatalf("occupied = %d after X6, want 0 (occupancy released exactly once)", got)
	}
}
