package contracttest_test

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// errResidualStoreDown models a residual-tracking dependency outage, as opposed
// to the bounded-capacity rejection ErrChatResidualCapacityFull represents.
var errResidualStoreDown = errors.New("residual store unavailable")

// outageResidualStore fails every Acquire with a NON-capacity error. §6.5 rule 2
// gives capacity exhaustion a defined meaning — the bound held and the original
// request state is retained — which an outage does not share: the surviving
// upstream ends up with no residual record at all.
type outageResidualStore struct {
	acquireCalls atomic.Int32
	releaseCalls atomic.Int32
}

func (store *outageResidualStore) Acquire(_ context.Context, _ ports.ChatResidualHold) error {
	store.acquireCalls.Add(1)
	return errResidualStoreDown
}

func (store *outageResidualStore) Release(_ context.Context, _ ports.ChatResidualHold) error {
	store.releaseCalls.Add(1)
	return nil
}

// confirmingResidualDrain confirms FINAL usage, so the accounting-fault path is
// NOT what a test using it observes: any residual fault it sees comes from the
// store, not from unknown usage.
type confirmingResidualDrain struct {
	usage domain.ChatUsage
}

func (drain *confirmingResidualDrain) Drain(_ context.Context, _ ports.ChatResidualDrainRequest) (ports.ChatResidualOutcome, error) {
	return ports.ChatResidualOutcome{UsageKnown: true, Usage: drain.usage}, nil
}

// residualAuditOutcome returns the outcome recorded under the residual audit
// action, and whether any such record exists.
func residualAuditOutcome(events []ports.ChatAuditEvent) (string, bool) {
	for _, event := range events {
		if event.Action == ports.AuditChatResidual {
			return event.Outcome, true
		}
	}
	return "", false
}

// A residual-store OUTAGE is not capacity exhaustion (§6.5 rule 2). Treating it
// as capacity-full reconciled the reservation and closed the books as if the
// surviving upstream were tracked, when in fact no residual hold exists and the
// work can outlive the drain untracked. The fault must reach the operator-visible
// audit trail, and it must be the DEPENDENCY fault, not the unknown-usage
// accounting fault: the drain here confirms FINAL usage.
func TestChatResidualStoreOutageSurfacesDependencyFault(t *testing.T) {
	t.Parallel()

	admissionStore := newLimitAdmissionStore(1)
	residualStore := &outageResidualStore{}
	drain := &confirmingResidualDrain{usage: domain.ChatUsage{PromptTokens: 9, CompletionTokens: 5}}

	harness := newStreamHarnessWithResidual(t, admissionStore, residualStore, drain, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_residual_outage", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{delta: "before cancel"},
			// Canceled WITHOUT a confirmed stop: the X5 != X6 residual path.
			{outcome: ptrStreamOutcome(streamCanceledNonCancelable(domain.ChatUsage{PromptTokens: 4, CompletionTokens: 3}))},
		})
	})

	_, events, payload := harness.streamRequest(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-residual-outage",
		body:    chatStreamBody,
	})

	terminals := terminalEvents(events)
	if len(terminals) != 1 || terminals[0].Type != "canceled" {
		t.Fatalf("expected exactly 1 canceled terminal, got %v (body=%s)", terminals, payload)
	}

	if got := residualStore.acquireCalls.Load(); got != 1 {
		t.Fatalf("residual Acquire calls = %d, want 1 (the residual path must be entered)", got)
	}
	// Nothing was acquired, so nothing may be released: releasing a hold this
	// execution never owns would decrement another execution's slot.
	if got := residualStore.releaseCalls.Load(); got != 0 {
		t.Fatalf("residual Release calls = %d, want 0 (Acquire failed, so there is no hold to release)", got)
	}

	outcome, found := residualAuditOutcome(harness.chatAudit.snapshot())
	if !found {
		t.Fatalf("no residual audit recorded: a residual-store outage left the surviving upstream untracked with no operator-visible fault")
	}
	if outcome != "canceled_settle_fault" {
		t.Fatalf("residual audit outcome = %q, want canceled_settle_fault: a store outage is a DEPENDENCY fault, distinct from the unknown-FINAL-usage accounting fault (the drain confirmed usage here)", outcome)
	}
}

// failingReconcileAdmission admits once and then fails every Reconcile, modeling
// a ledger outage at the accounting terminal.
type failingReconcileAdmission struct {
	reconcileCalls atomic.Int32
}

func (store *failingReconcileAdmission) Admit(_ context.Context, request ports.AdmissionRequest) (ports.AdmissionDecision, ports.AdmissionReservation, error) {
	return ports.AdmissionDecision{Admitted: true},
		ports.AdmissionReservation{Principal: request.Principal, Operation: request.Operation},
		nil
}

func (store *failingReconcileAdmission) Reconcile(_ context.Context, _ ports.AdmissionReservation) error {
	store.reconcileCalls.Add(1)
	return ports.ErrDependencyUnavailable
}

// §6.5 rule 4 releases occupancy and residual tracking together AT the accounting
// terminal. When Reconcile fails, that terminal was never reached: the reservation
// is still outstanding and the upstream may still be running. Releasing the hold
// anyway hands the Tenant's bounded residual slot to another execution while the
// original work remains unaccounted, so the hold must be RETAINED.
func TestChatResidualHoldRetainedWhenReconcileFails(t *testing.T) {
	t.Parallel()

	admissionStore := &failingReconcileAdmission{}
	residualStore := &countingResidualStore{}
	drain := &confirmingResidualDrain{usage: domain.ChatUsage{PromptTokens: 9, CompletionTokens: 5}}

	harness := newStreamHarnessWithResidual(t, admissionStore, residualStore, drain, func(h *streamHarness) {
		h.seedStreamingAccount("tenant_a", "pa_residual_reconcile_fail", domain.AuthModeChatGPTCodexOAuth, domain.StreamingReal)
		h.stream.Script([]streamStep{
			{delta: "before cancel"},
			{outcome: ptrStreamOutcome(streamCanceledNonCancelable(domain.ChatUsage{PromptTokens: 4, CompletionTokens: 3}))},
		})
	})

	_, events, payload := harness.streamRequest(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-residual-reconcile-fail",
		body:    chatStreamBody,
	})

	terminals := terminalEvents(events)
	if len(terminals) != 1 || terminals[0].Type != "canceled" {
		t.Fatalf("expected exactly 1 canceled terminal, got %v (body=%s)", terminals, payload)
	}

	if got := residualStore.acquireCalls.Load(); got != 1 {
		t.Fatalf("residual Acquire calls = %d, want 1", got)
	}
	if got := admissionStore.reconcileCalls.Load(); got != 1 {
		t.Fatalf("Reconcile calls = %d, want 1", got)
	}
	if got := residualStore.releaseCalls.Load(); got != 0 {
		t.Fatalf("residual Release calls = %d, want 0: the accounting terminal failed, so the hold is the only remaining record of the surviving upstream and must not be handed to another execution", got)
	}

	outcome, found := residualAuditOutcome(harness.chatAudit.snapshot())
	if !found {
		t.Fatalf("no residual audit recorded for a failed accounting terminal")
	}
	if outcome != "canceled_settle_fault" {
		t.Fatalf("residual audit outcome = %q, want canceled_settle_fault (the ledger failed to record the debit)", outcome)
	}
}
