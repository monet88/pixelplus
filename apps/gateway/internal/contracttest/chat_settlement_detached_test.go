package contracttest_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// ctxRecordingResidualDrain records whether the context handed to Drain was
// already canceled, and confirms final usage so the happy path can be asserted
// independently of the fault path.
type ctxRecordingResidualDrain struct {
	called      chan struct{}
	calledOnce  sync.Once
	sawCanceled atomic.Bool
	calls       atomic.Int32
	usage       domain.ChatUsage
}

func newCtxRecordingResidualDrain(usage domain.ChatUsage) *ctxRecordingResidualDrain {
	return &ctxRecordingResidualDrain{called: make(chan struct{}), usage: usage}
}

func (drain *ctxRecordingResidualDrain) Drain(ctx context.Context, _ ports.ChatResidualDrainRequest) (ports.ChatResidualOutcome, error) {
	drain.calls.Add(1)
	defer drain.calledOnce.Do(func() { close(drain.called) })
	if ctx.Err() != nil {
		drain.sawCanceled.Store(true)
		return ports.ChatResidualOutcome{}, ctx.Err()
	}
	return ports.ChatResidualOutcome{UsageKnown: true, Usage: drain.usage}, nil
}

// countingResidualStore records acquire/release so a test can assert the hold is
// both taken and released exactly once at X6.
type countingResidualStore struct {
	acquireCalls  atomic.Int32
	releaseCalls  atomic.Int32
	sawCanceled   atomic.Bool
	lastHoldMu    sync.Mutex
	lastHold      ports.ChatResidualHold
	acquireFailed atomic.Int32
}

func (store *countingResidualStore) Acquire(ctx context.Context, hold ports.ChatResidualHold) error {
	store.acquireCalls.Add(1)
	if err := ctx.Err(); err != nil {
		store.sawCanceled.Store(true)
		store.acquireFailed.Add(1)
		return err
	}
	store.lastHoldMu.Lock()
	store.lastHold = hold
	store.lastHoldMu.Unlock()
	return nil
}

func (store *countingResidualStore) Release(ctx context.Context, _ ports.ChatResidualHold) error {
	if err := ctx.Err(); err != nil {
		store.sawCanceled.Store(true)
		return err
	}
	store.releaseCalls.Add(1)
	return nil
}

func (store *countingResidualStore) hold() ports.ChatResidualHold {
	store.lastHoldMu.Lock()
	defer store.lastHoldMu.Unlock()
	return store.lastHold
}

// §6.3 rule 2 / §6.5 rule 4 regression: a client disconnect must NOT abort the
// accounting terminal. Settlement runs after the client is gone, so if it
// inherited the request context it would receive an already-canceled context,
// every ledger write would fail, and the Tenant+key occupancy would be retained
// forever — the untracked work the spec forbids.
//
// The stores here honor ctx exactly as a real datastore-backed store must, so
// this test fails loudly if settlement is ever re-attached to the request
// context.
func TestChatStreamDisconnectSettlesOnDetachedContext(t *testing.T) {
	t.Parallel()

	gate := newContextCancelGate()
	admissionStore := newLimitAdmissionStore(1)
	residualStore := &countingResidualStore{}
	drain := newCtxRecordingResidualDrain(domain.ChatUsage{PromptTokens: 11, CompletionTokens: 7})

	harness := newStreamHarnessWithResidual(t, admissionStore, residualStore, drain, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_detached_settle", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{delta: "before disconnect"},
			{cancelGate: gate},
			// A cancel WITHOUT a confirmed stop: upstream may survive, so this is
			// the X5 != X6 split that exercises hold + drain + settle.
			{outcome: ptrStreamOutcome(streamCanceledWithAbort(domain.ChatUsage{PromptTokens: 4, CompletionTokens: 2}))},
		})
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, harness.fixture.URL()+"/v1/chat/completions", strings.NewReader(chatStreamBody))
	if err != nil {
		t.Fatalf("NewRequest error = %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+tenantAKey)
	request.Header.Set("Idempotency-Key", "idem-detached-settle")
	request.Header.Set("Content-Type", "application/json")

	response, err := harness.fixture.Client().Do(request)
	if err != nil {
		t.Fatalf("stream Do error = %v", err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("stream status = %d, want 200", response.StatusCode)
	}

	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		response.Body.Close()
		t.Fatalf("adapter never reached the cancellation gate")
	}
	if got := admissionStore.occupiedCount(); got != 1 {
		t.Fatalf("occupied = %d while the stream runs, want 1", got)
	}

	// The client abandons the stream mid-generation.
	response.Body.Close()
	cancel()

	select {
	case <-drain.called:
	case <-time.After(5 * time.Second):
		t.Fatalf("residual drain never ran after disconnect")
	}

	deadline := time.Now().Add(5 * time.Second)
	for admissionStore.occupiedCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if drain.sawCanceled.Load() {
		t.Errorf("residual drain received an already-canceled context: settlement is still attached to the request context")
	}
	if residualStore.sawCanceled.Load() {
		t.Errorf("residual store received an already-canceled context: settlement is still attached to the request context")
	}
	if got := admissionStore.occupiedCount(); got != 0 {
		t.Fatalf("occupied = %d after disconnect, want 0: the accounting terminal leaked Tenant occupancy", got)
	}
	if got := admissionStore.reconcileCount(); got != 1 {
		t.Fatalf("Reconcile calls = %d, want exactly 1 at the accounting terminal", got)
	}

	// The residual hold is taken and released exactly once, and stays bound to
	// the originating Tenant/key (§6.5 rule 5).
	if got := residualStore.acquireCalls.Load(); got != 1 {
		t.Fatalf("residual Acquire calls = %d, want 1", got)
	}
	if got := residualStore.releaseCalls.Load(); got != 1 {
		t.Fatalf("residual Release calls = %d, want exactly 1 at X6", got)
	}
	if hold := residualStore.hold(); hold.TenantID != "tenant_a" || hold.AccountID != "pa_detached_settle" {
		t.Fatalf("residual hold = %+v, want tenant_a bound to the serving account", hold)
	}
}

// Review finding 1: the P2 hard lease release must survive the client just like
// the rest of the accounting terminal. A disconnect cancels the request context;
// if Release ran on `ctx`, a resilient lease store would reject the canceled-context
// write and leak the account binding until its TTL — a Tenant+account could not
// open a new stream in the meantime (§6.3 rule 2, §6.5 rule 4).
func TestChatStreamDisconnectReleasesLeaseOnDetachedContext(t *testing.T) {
	t.Parallel()

	gate := newContextCancelGate()
	admissionStore := newLimitAdmissionStore(1)
	residualStore := &countingResidualStore{}
	drain := newCtxRecordingResidualDrain(domain.ChatUsage{PromptTokens: 11, CompletionTokens: 7})

	harness := newStreamHarnessWithResidual(t, admissionStore, residualStore, drain, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_lease_detached", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		// The P2 hard lease is what this test is about, and the harness default
		// leaves LeasePolicy disabled — without opting in, acquireStreamLease
		// returns a nil lease, the release defer is never registered, and every
		// assertion below would pass vacuously.
		policy := chatRoutingPolicy([]domain.ProviderAccountID{"pa_lease_detached"}, nil)
		policy.LeasePolicy = domain.LeasePolicy{Enabled: true, EligibleUnits: []domain.LeaseUnit{domain.LeaseUnitChatStream}}
		h.routing.Seed("tenant_a", policy)
		h.stream.Script([]streamStep{
			{delta: "before disconnect"},
			{cancelGate: gate},
			// Cancel WITHOUT a confirmed stop: upstream may survive, exercising the
			// X5 != X6 split so settlement (and lease release) run after the client.
			{outcome: ptrStreamOutcome(streamCanceledWithAbort(domain.ChatUsage{PromptTokens: 4, CompletionTokens: 2}))},
		})
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, harness.fixture.URL()+"/v1/chat/completions", strings.NewReader(chatStreamBody))
	if err != nil {
		t.Fatalf("NewRequest error = %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+tenantAKey)
	request.Header.Set("Idempotency-Key", "idem-lease-detached")
	request.Header.Set("Content-Type", "application/json")

	response, err := harness.fixture.Client().Do(request)
	if err != nil {
		t.Fatalf("stream Do error = %v", err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
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
	case <-drain.called:
	case <-time.After(5 * time.Second):
		t.Fatalf("residual drain never ran after disconnect")
	}

	// Synchronize on the RELEASE itself, not on occupancy. Reconcile — which
	// zeroes the slot — runs inside settleStream, whereas the lease release fires
	// later, from a defer registered before runStream. Waiting on occupancy would
	// let this guard read sawCanceledRelease() before Release had run at all, so
	// it would still pass with the release re-attached to the canceled request
	// context — exactly the regression it exists to catch.
	deadline := time.Now().Add(5 * time.Second)
	for len(harness.leases.Releases()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := len(harness.leases.Releases()); got != 1 {
		t.Fatalf("lease releases = %d, want 1: the P2 lease was never released after the disconnect", got)
	}

	if harness.leases.sawCanceledRelease() {
		t.Errorf("lease release received an already-canceled context: the P2 lease is still attached to the request context, and a resilient lease store would reject the write and leak the binding (§6.3 rule 2)")
	}

	for admissionStore.occupiedCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := admissionStore.occupiedCount(); got != 0 {
		t.Fatalf("occupied = %d after disconnect, want 0: the accounting terminal leaked Tenant occupancy", got)
	}
}
