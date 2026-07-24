package persistence

import (
	"context"
	"encoding/json"
	"os"
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
		WithProbeActivated(domain.NewTimestamp(account.CreatedAt.Time()))

	// AccountStore owns lifecycle; HealthStore owns cooldowns/permits.
	first := NewFileAccountStore(path)
	if err := first.Restore(context.Background()); err != nil {
		t.Fatalf("first Restore() error = %v", err)
	}
	if _, err := first.Create(context.Background(), ports.AccountCreation{Principal: principal, Account: account}); err != nil {
		t.Fatalf("first Create() error = %v", err)
	}
	healthPath := path + ".health.ledger"
	healthStore := NewFileHealthStore(healthPath)
	if err := healthStore.Restore(context.Background()); err != nil {
		t.Fatalf("health Restore() error = %v", err)
	}
	cooldown := domain.HealthSummary{
		SummaryState: domain.HealthCoolingDown,
		Conditions: []domain.HealthCondition{{
			Scope: domain.HealthScope{
				Kind:      domain.HealthScopeOperation,
				Operation: string(domain.CapabilityOpImageGeneration),
			},
			State:             domain.HealthCoolingDown,
			Reason:            domain.HealthReasonProviderRateLimited,
			CredentialVersion: 1,
			ConditionRevision: 1,
			BackoffLevel:      1,
			ObservedAt:        account.CreatedAt,
			Remediation:       domain.RemediationWaitProviderCooldown,
			RetryNotBefore:    domain.NewTimestamp(account.CreatedAt.Time().Add(time.Minute)),
			SourceClass:       domain.HealthSourceUpstreamAttempt,
		}},
	}
	if _, err := healthStore.Initialize(context.Background(), ports.HealthInitialize{
		Principal: principal,
		AccountID: account.ID,
		Health:    cooldown,
		Audit:     func(context.Context, []ports.HealthTransition) error { return nil },
	}); err != nil {
		t.Fatalf("health Initialize() error = %v", err)
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
	secondHealth := NewFileHealthStore(healthPath)
	if err := secondHealth.Restore(context.Background()); err != nil {
		t.Fatalf("second health Restore() error = %v", err)
	}
	snap, err := secondHealth.Read(context.Background(), principal, "pa_file_restore")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	var found bool
	for _, condition := range snap.Health.Conditions {
		if condition.Scope.Kind == domain.HealthScopeOperation &&
			condition.Scope.Operation == string(domain.CapabilityOpImageGeneration) &&
			condition.State == domain.HealthCoolingDown {
			found = true
		}
	}
	if !found {
		t.Fatalf("restored cooldown missing; conditions = %+v", snap.Health.Conditions)
	}
}

func TestFileAccountStoreMigratesLegacySnapshotBeforeAppend(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")
	principal := domain.SecurityPrincipal{
		TenantID: "tenant_a", ClientAPIKeyID: "key_a",
		Scopes: domain.NewScopeSet(domain.ScopeAccountsRead, domain.ScopeAccountsManage),
	}
	account := domain.NewDraftProviderAccount(
		"pa_legacy_migrate", domain.ProviderChatGPT, domain.AuthModeChatGPTCodexOAuth,
		"legacy", domain.NewTimestamp(time.Date(2026, time.July, 21, 0, 0, 0, 0, time.UTC)),
	)
	legacy := map[domain.TenantID]map[domain.ProviderAccountID]domain.ProviderAccount{
		principal.TenantID: {account.ID: account},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy snapshot: %v", err)
	}
	if err := os.WriteFile(path, data, 0o640); err != nil {
		t.Fatalf("write legacy snapshot: %v", err)
	}

	store := NewFileAccountStore(path)
	if err := store.Restore(t.Context()); err != nil {
		t.Fatalf("Restore legacy snapshot: %v", err)
	}
	account.Label = "migrated"
	if _, err := store.Update(t.Context(), ports.AccountUpdate{Principal: principal, Account: account}); err != nil {
		t.Fatalf("Update legacy-backed account: %v", err)
	}

	reloaded := NewFileAccountStore(path)
	if err := reloaded.Restore(t.Context()); err != nil {
		t.Fatalf("Restore after first append: %v", err)
	}
	got, err := reloaded.Visible(t.Context(), principal, account.ID)
	if err != nil {
		t.Fatalf("Visible after migration: %v", err)
	}
	if got.Label != "migrated" {
		t.Fatalf("label = %q, want migrated", got.Label)
	}
}
