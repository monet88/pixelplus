package application

import (
	"errors"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// registryTestClock is a manually advanced ports.Clock for the registry tests.
type registryTestClock struct {
	now time.Time
}

func (clock *registryTestClock) Now() time.Time { return clock.now }

func (clock *registryTestClock) advance(duration time.Duration) {
	clock.now = clock.now.Add(duration)
}

// The registry keeps terminal executions resolvable for an idempotent no-op
// cancel, but only within a bounded retention window. After the window it must
// evict them (via unregister/reap) so the process-local map does not grow for
// the process lifetime (review finding: unregister never called).
func TestChatExecutionRegistryEvictsTerminalEntriesAfterRetention(t *testing.T) {
	clock := &registryTestClock{now: time.Date(2026, time.July, 21, 0, 0, 0, 0, time.UTC)}
	registry := newChatExecutionRegistry(clock)
	cancelFn := func() {}

	registry.register("tenant_a", "exec_1", cancelFn)
	state, _, _, ok := registry.cancel("tenant_a", "exec_1")
	if !ok || state != ChatCancelRequested {
		t.Fatalf("running cancel before terminal: ok=%v state=%s, want cancel_requested", ok, state)
	}

	registry.markTerminal("exec_1", false, true)
	canceled, _, stopConfirmed, ok := registry.cancel("tenant_a", "exec_1")
	if !ok || canceled != ChatCanceled || !stopConfirmed {
		t.Fatalf("in-window terminal cancel: ok=%v state=%s stop=%v, want canceled with stop confirmed", ok, canceled, stopConfirmed)
	}

	// Quickly register+mark a second execution so the earlier one is still
	// present but not yet expired; a cancel must still resolve it.
	registry.register("tenant_a", "exec_2", cancelFn)
	registry.markTerminal("exec_2", false, true)
	if _, _, _, ok := registry.cancel("tenant_a", "exec_2"); !ok {
		t.Fatalf("in-window terminal cancel of exec_2 unexpectedly 404ed")
	}

	// Advance past retention; the next register reaps both terminal entries and
	// unregisters them.
	clock.advance(chatCancelRetention + time.Second)
	registry.register("tenant_a", "exec_3", cancelFn)

	for _, id := range []string{"exec_1", "exec_2"} {
		if _, _, _, ok := registry.cancel("tenant_a", domain.Identifier(id)); ok {
			t.Fatalf("cancel of %s after retention resolved an entry that should have been evicted", id)
		}
	}
	if _, _, _, ok := registry.cancel("tenant_a", "exec_3"); !ok {
		t.Fatalf("newly registered exec_3 should still resolve")
	}
}

// Review finding 3: markTerminal must merge abortAttempted by OR, never overwrite
// it. An explicit cancel on a running execution records abortAttempted=true; if
// the stream later terminates as genuinely non-aborted, that observation must be
// preserved so an idempotent retry cancel answers "upstream_abort_attempted:true"
// for the abort that really happened.
func TestChatExecutionRegistryMarkTerminalMergesAbortAttempted(t *testing.T) {
	clock := &registryTestClock{now: time.Date(2026, time.July, 21, 0, 0, 0, 0, time.UTC)}
	registry := newChatExecutionRegistry(clock)
	cancelFn := func() {}

	registry.register("tenant_a", "exec_1", cancelFn)

	// A running-execution cancel records an abort attempt.
	state, abort, _, ok := registry.cancel("tenant_a", "exec_1")
	if !ok || state != ChatCancelRequested || !abort {
		t.Fatalf("running cancel: ok=%v state=%s abort=%v, want cancel_requested with abort attempted", ok, state, abort)
	}

	// The stream then terminates reporting NO abort attempt (e.g. a non-cancelable
	// Adapter). markTerminal must NOT erase the earlier abort observation.
	registry.markTerminal("exec_1", false, false)

	// A terminal idempotent cancel must still report the abort that WAS attempted.
	_, abort2, _, ok := registry.cancel("tenant_a", "exec_1")
	if !ok {
		t.Fatalf("terminal cancel unexpectedly 404ed")
	}
	if !abort2 {
		t.Fatalf("terminal cancel reported upstream_abort_attempted=false; the explicit cancel at t0 really attempted an abort, and markTerminal overwrote it (review finding 3)")
	}
}

// Review finding 7: an entry that never reached a terminal state must be
// reclaimed when the spine unwinds. unregisterIfNotTerminal removes a running
// entry (e.g. after a panic before markTerminal), while a terminal entry is kept
// for the idempotent-cancel window.
func TestChatExecutionRegistryUnregisterIfNotTerminal(t *testing.T) {
	clock := &registryTestClock{now: time.Date(2026, time.July, 21, 0, 0, 0, 0, time.UTC)}
	registry := newChatExecutionRegistry(clock)
	cancelFn := func() {}

	// Running entry: unregisterIfNotTerminal must remove it (panic-safety path).
	registry.register("tenant_a", "exec_running", cancelFn)
	registry.unregisterIfNotTerminal("exec_running")
	if _, _, _, ok := registry.cancel("tenant_a", "exec_running"); ok {
		t.Fatalf("a running entry that never became terminal was not reclaimed")
	}

	// Terminal entry: unregisterIfNotTerminal must preserve it so an idempotent
	// cancel still resolves within the retention window.
	registry.register("tenant_a", "exec_terminal", cancelFn)
	registry.markTerminal("exec_terminal", false, true)
	registry.unregisterIfNotTerminal("exec_terminal")
	if _, _, _, ok := registry.cancel("tenant_a", "exec_terminal"); !ok {
		t.Fatalf("a terminal entry was reclaimed before its retention window; unregisterIfNotTerminal must only remove never-terminal entries")
	}
}

// Review finding 4: authoritativeNonCommit must fail closed. Only codes that
// PROVE the upstream never started a generation are authoritative; every unknown
// or possibly-committed code must return false so the residual protocol is not
// skipped (§6.4 rule 3, §6.5 rule 1).
func TestAuthoritativeNonCommitFailsClosed(t *testing.T) {
	t.Parallel()

	// Known non-commit codes: pre-upstream gates and Adapter-verified provider
	// rejections.
	for _, code := range []domain.ErrorCode{
		domain.ErrCodeAuthenticationFailed,
		domain.ErrCodeInvalidRequest,
		domain.ErrCodeAccountNotUsable,
		domain.ErrCodeProviderRateLimited,
		domain.ErrCodeProviderRejected,
		domain.ErrCodeDependencyUnavailable,
	} {
		if !authoritativeNonCommit(code) {
			t.Fatalf("authoritativeNonCommit(%q) = false, want true: this code proves no generation started", code)
		}
	}

	// Possibly-committed / surviving-upstream codes must NOT be treated as
	// authoritative non-commit (residual path must be kept).
	for _, code := range []domain.ErrorCode{
		domain.ErrCodeUpstreamTimeout,
		domain.ErrCodeUpstreamUnavailable,
		domain.ErrCodeUpstreamProtocolDrift,
		domain.ErrCodeExecutionPossiblyCommitted,
	} {
		if authoritativeNonCommit(code) {
			t.Fatalf("authoritativeNonCommit(%q) = true, want false: this code means the upstream MAY have committed, so the residual protocol must run", code)
		}
	}

	// A future/unrecognized code must fail closed (never assumed non-commit).
	if authoritativeNonCommit(domain.ErrorCode("future_maybe_committed")) {
		t.Fatalf("authoritativeNonCommit(unknown) = true, want false: an unknown code might mean the upstream accepted the generation; it must not release occupancy and skip the residual protocol")
	}
}

// Review finding 5: the accounting fault (FINAL usage unknown) must be
// distinguishable from a dependency outage (Reconcile itself failed), so
// operators can triage the audit trail correctly.
func TestIsResidualAccountingFaultDistinguishesFaultKinds(t *testing.T) {
	t.Parallel()

	if !isResidualAccountingFault(errResidualAccountingFault) {
		t.Fatalf("isResidualAccountingFault(accounting fault) = false, want true: an unknown-FINAL-usage outcome is an accounting fault")
	}
	if isResidualAccountingFault(errors.New("some dependency outage")) {
		t.Fatalf("isResidualAccountingFault(dependency error) = true, want false: a Reconcile/ledger outage is NOT a final-usage-unknown accounting fault")
	}
	if isResidualAccountingFault(nil) {
		t.Fatalf("isResidualAccountingFault(nil) = true, want false")
	}
}
