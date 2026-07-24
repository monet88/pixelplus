package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// AC: MemoryRoutingPolicyStore.Replace enforces the same durable invariants as
// FileRoutingPolicyStore — invalid policies return dependency_unavailable and
// leave map state plus Mutations/Revision counters unchanged.
func TestMemoryRoutingPolicyStoreReplaceRejectsInvalidWithoutMutation(t *testing.T) {
	t.Parallel()

	store := NewMemoryRoutingPolicyStore()
	principal := domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a"}
	good := domain.RoutingPolicy{
		CandidateAccounts: []domain.ProviderAccountID{"pa_good"},
		SelectionOrder:    []domain.ProviderAccountID{"pa_good"},
		FallbackEnabled:   false,
		FallbackChain:     []domain.ProviderAccountID{},
		FallbackAuthModes: []domain.AuthMode{},
		Affinity:          domain.AffinityPolicy{Enabled: false},
		LeasePolicy:       domain.LeasePolicy{Enabled: false, EligibleUnits: []domain.LeaseUnit{}},
		UpdatedAt:         domain.NewTimestamp(time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)),
		UpdatedBy:         "key_a",
	}
	if _, err := store.Replace(context.Background(), ports.RoutingPolicyChange{Principal: principal, Policy: good}); err != nil {
		t.Fatalf("seed Replace: %v", err)
	}
	beforeMut := store.Mutations()
	beforeRev := store.Revision()

	// Missing UpdatedBy fails durable validation (same authority as File Replace).
	missingAudit := good
	missingAudit.UpdatedBy = ""
	if _, err := store.Replace(context.Background(), ports.RoutingPolicyChange{Principal: principal, Policy: missingAudit}); err == nil {
		t.Fatal("Replace missing UpdatedBy: want error")
	} else if !errors.Is(err, ports.ErrDependencyUnavailable) {
		t.Fatalf("Replace missing UpdatedBy error = %v, want ErrDependencyUnavailable", err)
	}

	// Experimental mode in fallback_auth_modes is fail-closed for durable rows.
	experimental := good
	experimental.FallbackEnabled = true
	experimental.FallbackChain = []domain.ProviderAccountID{"pa_good"}
	experimental.FallbackAuthModes = []domain.AuthMode{domain.AuthModeChatGPTWebAccess}
	if _, err := store.Replace(context.Background(), ports.RoutingPolicyChange{Principal: principal, Policy: experimental}); err == nil {
		t.Fatal("Replace experimental mode: want error")
	} else if !errors.Is(err, ports.ErrDependencyUnavailable) {
		t.Fatalf("Replace experimental mode error = %v, want ErrDependencyUnavailable", err)
	}

	if store.Mutations() != beforeMut {
		t.Fatalf("Mutations = %d, want %d (no successful commits)", store.Mutations(), beforeMut)
	}
	if store.Revision() != beforeRev {
		t.Fatalf("Revision = %d, want %d (no successful commits)", store.Revision(), beforeRev)
	}
	got, err := store.Read(context.Background(), principal)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.UpdatedBy != "key_a" || len(got.FallbackAuthModes) != 0 || got.FallbackEnabled {
		t.Fatalf("prior map state mutated on reject: %+v", got)
	}

	// Valid replace still commits and advances counters.
	next := good
	next.UpdatedBy = "key_b"
	if _, err := store.Replace(context.Background(), ports.RoutingPolicyChange{Principal: principal, Policy: next}); err != nil {
		t.Fatalf("valid Replace: %v", err)
	}
	if store.Mutations() != beforeMut+1 || store.Revision() != beforeRev+1 {
		t.Fatalf("valid Replace counters: mut=%d rev=%d, want mut=%d rev=%d",
			store.Mutations(), store.Revision(), beforeMut+1, beforeRev+1)
	}
}
