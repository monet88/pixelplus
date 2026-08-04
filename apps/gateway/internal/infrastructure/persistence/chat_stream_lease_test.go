package persistence

import (
	"context"
	"errors"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// TenantID is part of the lease key, so accepting an empty one would let
// unrelated requests contend on a single shared zero-Tenant key and surface
// misleading cross-Tenant lease-held conflicts.
//
// This is a store-level invariant (defence in depth): the spine only ever passes
// an authenticated principal's TenantID, so the empty case is not reachable
// through the public HTTP seam today. The guard exists so a future caller cannot
// quietly break lease scoping.
func TestChatStreamLeaseRejectsIncompleteIdentity(t *testing.T) {
	t.Parallel()

	complete := ports.ChatStreamLease{TenantID: "tenant_a", AccountID: "pa_1", Holder: "execution_1"}

	cases := map[string]ports.ChatStreamLease{
		"missing tenant":  {AccountID: complete.AccountID, Holder: complete.Holder},
		"missing account": {TenantID: complete.TenantID, Holder: complete.Holder},
		"missing holder":  {TenantID: complete.TenantID, AccountID: complete.AccountID},
	}

	for name, lease := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store := NewMemoryChatStreamLeaseStore()
			err := store.Acquire(context.Background(), lease)
			if !errors.Is(err, ports.ErrDependencyUnavailable) {
				t.Fatalf("Acquire(%+v) error = %v, want ErrDependencyUnavailable: an incompletely scoped lease must fail closed", lease, err)
			}
		})
	}

	// The complete identity still works, so the guard rejects only malformed input.
	store := NewMemoryChatStreamLeaseStore()
	if err := store.Acquire(context.Background(), complete); err != nil {
		t.Fatalf("Acquire(%+v) error = %v, want success", complete, err)
	}
	holder, ok, err := store.Holder(context.Background(), complete.TenantID, complete.AccountID)
	if err != nil || !ok {
		t.Fatalf("Holder() = (%q, %v, %v), want the acquired holder", holder, ok, err)
	}
	if holder != domain.Identifier("execution_1") {
		t.Fatalf("Holder() = %q, want execution_1", holder)
	}
}
