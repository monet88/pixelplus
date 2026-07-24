package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

func mustPolicyLine(t *testing.T, tenant domain.TenantID, policy domain.RoutingPolicy) string {
	t.Helper()
	raw, err := json.Marshal(routingPolicyRecord{TenantID: tenant, Policy: policy})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw) + "\n"
}

func TestFileRoutingPolicyStoreRestoreRejectsInvalidSemantics(t *testing.T) {
	t.Parallel()

	now := domain.NewTimestamp(time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC))
	valid := domain.RoutingPolicy{
		CandidateAccounts: []domain.ProviderAccountID{"pa_ok"},
		SelectionOrder:    []domain.ProviderAccountID{"pa_ok"},
		FallbackEnabled:   false,
		FallbackChain:     []domain.ProviderAccountID{},
		FallbackAuthModes: []domain.AuthMode{},
		Affinity:          domain.AffinityPolicy{Enabled: false},
		LeasePolicy:       domain.LeasePolicy{Enabled: false, EligibleUnits: []domain.LeaseUnit{}},
		UpdatedAt:         now,
		UpdatedBy:         "key_a",
	}
	validLine := mustPolicyLine(t, "tenant_a", valid)

	cases := []struct {
		name string
		line string
	}{
		{name: "null_record", line: "null\n"},
		{
			name: "missing_audit",
			line: mustPolicyLine(t, "tenant_a", domain.RoutingPolicy{
				CandidateAccounts: []domain.ProviderAccountID{},
				SelectionOrder:    []domain.ProviderAccountID{},
			}),
		},
		{
			name: "invalid_id",
			line: mustPolicyLine(t, "tenant_a", domain.RoutingPolicy{
				CandidateAccounts: []domain.ProviderAccountID{"bad"},
				SelectionOrder:    []domain.ProviderAccountID{"bad"},
				UpdatedAt:         now,
				UpdatedBy:         "key_a",
			}),
		},
		{
			name: "fallback_enabled_empty_chain",
			line: mustPolicyLine(t, "tenant_a", domain.RoutingPolicy{
				CandidateAccounts: []domain.ProviderAccountID{"pa_ok"},
				SelectionOrder:    []domain.ProviderAccountID{"pa_ok"},
				FallbackEnabled:   true,
				FallbackChain:     []domain.ProviderAccountID{},
				UpdatedAt:         now,
				UpdatedBy:         "key_a",
			}),
		},
		{
			name: "selection_not_subset",
			line: mustPolicyLine(t, "tenant_a", domain.RoutingPolicy{
				CandidateAccounts: []domain.ProviderAccountID{"pa_ok"},
				SelectionOrder:    []domain.ProviderAccountID{"pa_other"},
				UpdatedAt:         now,
				UpdatedBy:         "key_a",
			}),
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "routing.ledger")
			if err := os.WriteFile(path, []byte(tc.line), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			store := NewFileRoutingPolicyStore(path)
			err := store.Restore(context.Background())
			if err == nil {
				t.Fatal("Restore() error = nil, want semantic failure")
			}
			if !errors.Is(err, ports.ErrDependencyUnavailable) {
				t.Fatalf("Restore() error = %v, want ErrDependencyUnavailable", err)
			}
		})
	}

	t.Run("latest_row_wins_same_tenant", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "routing.ledger")
		second := domain.RoutingPolicy{
			CandidateAccounts: []domain.ProviderAccountID{"pa_two"},
			SelectionOrder:    []domain.ProviderAccountID{"pa_two"},
			FallbackEnabled:   false,
			FallbackChain:     []domain.ProviderAccountID{},
			FallbackAuthModes: []domain.AuthMode{},
			Affinity:          domain.AffinityPolicy{Enabled: false},
			LeasePolicy:       domain.LeasePolicy{Enabled: false, EligibleUnits: []domain.LeaseUnit{}},
			UpdatedAt:         domain.NewTimestamp(time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC)),
			UpdatedBy:         "key_b",
		}
		if err := os.WriteFile(path, []byte(validLine+mustPolicyLine(t, "tenant_a", second)), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		store := NewFileRoutingPolicyStore(path)
		if err := store.Restore(context.Background()); err != nil {
			t.Fatalf("Restore() error = %v", err)
		}
		policy, err := store.Read(context.Background(), domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "k"})
		if err != nil {
			t.Fatalf("Read() error = %v", err)
		}
		if len(policy.CandidateAccounts) != 1 || policy.CandidateAccounts[0] != "pa_two" {
			t.Fatalf("latest-row policy = %+v, want pa_two", policy.CandidateAccounts)
		}
		if policy.UpdatedBy != "key_b" {
			t.Fatalf("UpdatedBy = %q, want key_b", policy.UpdatedBy)
		}
	})
}

// Replace twice for the same Tenant, reconstruct/restore, and assert the second
// policy wins while a second Tenant remains intact (append-only JSONL +
// latest-row-wins, FileAccountStore pattern).
func TestFileRoutingPolicyStoreReplaceAppendLatestRowAcrossRestart(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "routing.ledger")
	store := NewFileRoutingPolicyStore(path)
	if err := store.Restore(context.Background()); err != nil {
		t.Fatalf("initial Restore() error = %v", err)
	}

	principalA := domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a"}
	principalB := domain.SecurityPrincipal{TenantID: "tenant_b", ClientAPIKeyID: "key_b"}
	now1 := domain.NewTimestamp(time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC))
	now2 := domain.NewTimestamp(time.Date(2026, 7, 24, 11, 0, 0, 0, time.UTC))
	nowB := domain.NewTimestamp(time.Date(2026, 7, 24, 10, 30, 0, 0, time.UTC))

	firstA := domain.RoutingPolicy{
		CandidateAccounts: []domain.ProviderAccountID{"pa_first"},
		SelectionOrder:    []domain.ProviderAccountID{"pa_first"},
		FallbackEnabled:   false,
		FallbackChain:     []domain.ProviderAccountID{},
		FallbackAuthModes: []domain.AuthMode{},
		Affinity:          domain.AffinityPolicy{Enabled: false},
		LeasePolicy:       domain.LeasePolicy{Enabled: false, EligibleUnits: []domain.LeaseUnit{}},
		UpdatedAt:         now1,
		UpdatedBy:         "key_a",
	}
	secondA := domain.RoutingPolicy{
		CandidateAccounts: []domain.ProviderAccountID{"pa_second"},
		SelectionOrder:    []domain.ProviderAccountID{"pa_second"},
		FallbackEnabled:   false,
		FallbackChain:     []domain.ProviderAccountID{},
		FallbackAuthModes: []domain.AuthMode{},
		Affinity:          domain.AffinityPolicy{Enabled: true, WindowClass: "AFFINITY-WINDOW-CLASS"},
		LeasePolicy:       domain.LeasePolicy{Enabled: false, EligibleUnits: []domain.LeaseUnit{}},
		UpdatedAt:         now2,
		UpdatedBy:         "key_a2",
	}
	policyB := domain.RoutingPolicy{
		CandidateAccounts: []domain.ProviderAccountID{"pa_tenant_b"},
		SelectionOrder:    []domain.ProviderAccountID{"pa_tenant_b"},
		FallbackEnabled:   false,
		FallbackChain:     []domain.ProviderAccountID{},
		FallbackAuthModes: []domain.AuthMode{},
		Affinity:          domain.AffinityPolicy{Enabled: false},
		LeasePolicy:       domain.LeasePolicy{Enabled: false, EligibleUnits: []domain.LeaseUnit{}},
		UpdatedAt:         nowB,
		UpdatedBy:         "key_b",
	}

	if _, err := store.Replace(context.Background(), ports.RoutingPolicyChange{Principal: principalA, Policy: firstA}); err != nil {
		t.Fatalf("Replace first A: %v", err)
	}
	if _, err := store.Replace(context.Background(), ports.RoutingPolicyChange{Principal: principalB, Policy: policyB}); err != nil {
		t.Fatalf("Replace B: %v", err)
	}
	if _, err := store.Replace(context.Background(), ports.RoutingPolicyChange{Principal: principalA, Policy: secondA}); err != nil {
		t.Fatalf("Replace second A: %v", err)
	}

	// Close process view and reconstruct from durable ledger only.
	store = NewFileRoutingPolicyStore(path)
	if err := store.Restore(context.Background()); err != nil {
		t.Fatalf("post-restart Restore() error = %v", err)
	}

	gotA, err := store.Read(context.Background(), principalA)
	if err != nil {
		t.Fatalf("Read A: %v", err)
	}
	if len(gotA.CandidateAccounts) != 1 || gotA.CandidateAccounts[0] != "pa_second" {
		t.Fatalf("tenant A after restart = %+v, want second policy pa_second", gotA.CandidateAccounts)
	}
	if gotA.UpdatedBy != "key_a2" {
		t.Fatalf("tenant A UpdatedBy = %q, want key_a2", gotA.UpdatedBy)
	}
	if !gotA.Affinity.Enabled || gotA.Affinity.WindowClass != "AFFINITY-WINDOW-CLASS" {
		t.Fatalf("tenant A affinity = %+v, want enabled with window class", gotA.Affinity)
	}

	gotB, err := store.Read(context.Background(), principalB)
	if err != nil {
		t.Fatalf("Read B: %v", err)
	}
	if len(gotB.CandidateAccounts) != 1 || gotB.CandidateAccounts[0] != "pa_tenant_b" {
		t.Fatalf("tenant B after restart = %+v, want pa_tenant_b intact", gotB.CandidateAccounts)
	}
	if gotB.UpdatedBy != "key_b" {
		t.Fatalf("tenant B UpdatedBy = %q, want key_b", gotB.UpdatedBy)
	}

	// Ledger must contain multiple JSONL lines (append), not a single rewritten snapshot.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	lines := 0
	for _, line := range splitLines(raw) {
		if len(line) > 0 {
			lines++
		}
	}
	if lines < 3 {
		t.Fatalf("ledger lines = %d, want >= 3 append records (body=%s)", lines, raw)
	}
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}

// Rejected Replace must leave in-memory state and ledger bytes unchanged.
func TestFileRoutingPolicyStoreReplaceRejectsWithoutMutation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "routing.ledger")
	store := NewFileRoutingPolicyStore(path)
	if err := store.Restore(context.Background()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
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
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	// Experimental mode in fallback_auth_modes is fail-closed.
	bad := good
	bad.FallbackEnabled = true
	bad.FallbackChain = []domain.ProviderAccountID{"pa_good"}
	bad.FallbackAuthModes = []domain.AuthMode{domain.AuthModeChatGPTWebAccess}
	if _, err := store.Replace(context.Background(), ports.RoutingPolicyChange{Principal: principal, Policy: bad}); err == nil {
		t.Fatal("Replace bad policy: want error")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger after reject: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("ledger mutated on rejected Replace")
	}
	got, err := store.Read(context.Background(), principal)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.UpdatedBy != "key_a" || len(got.FallbackAuthModes) != 0 {
		t.Fatalf("in-memory policy changed on reject: %+v", got)
	}
}
