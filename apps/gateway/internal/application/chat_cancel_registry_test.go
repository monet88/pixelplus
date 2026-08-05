package application

import (
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

	registry.register("tenant_a", "key_a", "exec_1", cancelFn)
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
	registry.register("tenant_a", "key_a", "exec_2", cancelFn)
	registry.markTerminal("exec_2", false, true)
	if _, _, _, ok := registry.cancel("tenant_a", "exec_2"); !ok {
		t.Fatalf("in-window terminal cancel of exec_2 unexpectedly 404ed")
	}

	// Advance past retention; the next register reaps both terminal entries and
	// unregisters them.
	clock.advance(chatCancelRetention + time.Second)
	registry.register("tenant_a", "key_a", "exec_3", cancelFn)

	for _, id := range []string{"exec_1", "exec_2"} {
		if _, _, _, ok := registry.cancel("tenant_a", domain.Identifier(id)); ok {
			t.Fatalf("cancel of %s after retention resolved an entry that should have been evicted", id)
		}
	}
	if _, _, _, ok := registry.cancel("tenant_a", "exec_3"); !ok {
		t.Fatalf("newly registered exec_3 should still resolve")
	}
}
