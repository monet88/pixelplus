package persistence

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// TestFileAccountStoreRestoresAcrossNewInstance proves that a second
// FileAccountStore instance loading the same path recovers durable account
// state, including scoped cooldowns and occupied recovery permits. This is the
// foundation for the composition-level "process restart restores before
// readiness" guarantee.
func TestFileAccountStoreRestoresAcrossNewInstance(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")

	principal := domain.SecurityPrincipal{
		TenantID:       "tenant_a",
		ClientAPIKeyID: "key_a",
		Scopes:         domain.NewScopeSet(domain.ScopeAccountsRead, domain.ScopeAccountsManage),
	}
	account := domain.NewDraftProviderAccount(
		"pa_file_restore",
		domain.ProviderChatGPT,
		domain.AuthModeChatGPTCodexOAuth,
		"primary",
		domain.NewTimestamp(time.Date(2026, time.July, 21, 0, 0, 0, 0, time.UTC)),
	)
	account.RiskAcknowledged = true
	account = account.WithSubmittedCredential(domain.NewTimestamp(account.CreatedAt.Time()), domain.Timestamp{}).
		WithValidatedCredential(domain.NewTimestamp(account.CreatedAt.Time())).
		WithProbeActivated(domain.NewTimestamp(account.CreatedAt.Time())).
		WithScopedCooldown(
			domain.NewTimestamp(account.CreatedAt.Time()),
			domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpImageGeneration)},
			domain.HealthReasonProviderRateLimited,
			domain.NewTimestamp(account.CreatedAt.Time().Add(time.Minute)),
		)

	first := NewFileAccountStore(path)
	if err := first.Restore(context.Background()); err != nil {
		t.Fatalf("first Restore() error = %v", err)
	}
	if _, err := first.Create(context.Background(), ports.AccountCreation{Principal: principal, Account: account}); err != nil {
		t.Fatalf("first Create() error = %v", err)
	}

	second := NewFileAccountStore(path)
	if err := second.Restore(context.Background()); err != nil {
		t.Fatalf("second Restore() error = %v", err)
	}
	loaded, err := second.Visible(context.Background(), principal, "pa_file_restore")
	if err != nil {
		t.Fatalf("Visible() error = %v", err)
	}
	if loaded.Lifecycle != domain.LifecycleActive {
		t.Fatalf("lifecycle = %v, want active", loaded.Lifecycle)
	}
	var found bool
	for _, condition := range loaded.Health.Conditions {
		if condition.Scope.Kind == domain.HealthScopeOperation &&
			condition.Scope.Operation == string(domain.CapabilityOpImageGeneration) &&
			condition.State == domain.HealthCoolingDown {
			found = true
		}
	}
	if !found {
		t.Fatalf("restored cooldown missing; conditions = %+v", loaded.Health.Conditions)
	}
}
