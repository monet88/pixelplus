package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

func noopAudit(context.Context, []ports.HealthTransition) error { return nil }

func testNow() domain.Timestamp {
	return domain.NewTimestamp(time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC))
}

func healthyAccountCond(now domain.Timestamp, credVersion int) domain.HealthCondition {
	return domain.HealthCondition{
		Scope: domain.HealthScope{Kind: domain.HealthScopeAccount}, State: domain.HealthHealthy,
		Reason: domain.HealthReasonProbeSucceeded, CredentialVersion: credVersion,
		ObservedAt: now, Remediation: domain.RemediationNone,
		SourceClass: domain.HealthSourceRequiredProbe,
	}
}

func coolingCond(scope domain.HealthScope, now domain.Timestamp, rev, cred, backoff int) domain.HealthCondition {
	return domain.HealthCondition{
		Scope: scope, State: domain.HealthCoolingDown, Reason: domain.HealthReasonProviderRateLimited,
		CredentialVersion: cred, ConditionRevision: rev, BackoffLevel: backoff,
		ObservedAt: now, Remediation: domain.RemediationWaitProviderCooldown,
		RetryNotBefore: domain.NewTimestamp(now.Time().Add(time.Minute)),
		SourceClass:    domain.HealthSourceUpstreamAttempt,
	}
}

func TestFileHealthStoreCrossInstanceSinglePermitClaim(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "health.ledger")
	principal := domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a"}
	scope := domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpImageGeneration)}
	now := testNow()
	health := domain.HealthSummary{
		SummaryState: domain.HealthCoolingDown,
		Conditions:   []domain.HealthCondition{coolingCond(scope, now, 1, 1, 1)},
	}

	seed := NewFileHealthStore(path)
	if err := seed.Restore(context.Background()); err != nil {
		t.Fatalf("seed Restore() error = %v", err)
	}
	if _, err := seed.Initialize(context.Background(), ports.HealthInitialize{
		Principal: principal, AccountID: "pa_race", Health: health, Audit: noopAudit,
	}); err != nil {
		t.Fatalf("seed Initialize() error = %v", err)
	}

	first := NewFileHealthStore(path)
	second := NewFileHealthStore(path)
	_ = first.Restore(context.Background())
	_ = second.Restore(context.Background())

	var wg sync.WaitGroup
	var mu sync.Mutex
	winners, losers := 0, 0
	claim := func(store *FileHealthStore, owner domain.Identifier) {
		defer wg.Done()
		_, err := store.ClaimRecoveryPermit(context.Background(), ports.RecoveryPermitClaim{
			Principal: principal, AccountID: "pa_race", Owner: owner,
			Scope: scope, ConditionRevision: 1, CredentialVersion: 1,
		})
		mu.Lock()
		defer mu.Unlock()
		if err == nil {
			winners++
		} else {
			losers++
		}
	}
	wg.Add(2)
	go claim(first, "req_1")
	go claim(second, "req_2")
	wg.Wait()
	if winners != 1 || losers != 1 {
		t.Fatalf("winners=%d losers=%d, want 1/1", winners, losers)
	}
}

func TestFileHealthStoreConcurrentDifferentScopesMerge(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "health.ledger")
	principal := domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a"}
	now := testNow()
	seed := NewFileHealthStore(path)
	_ = seed.Restore(context.Background())
	if _, err := seed.Initialize(context.Background(), ports.HealthInitialize{
		Principal: principal, AccountID: "pa_merge",
		Health: domain.HealthSummary{
			SummaryState: domain.HealthHealthy,
			Conditions:   []domain.HealthCondition{healthyAccountCond(now, 1)},
		},
		Audit: noopAudit,
	}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	a := NewFileHealthStore(path)
	b := NewFileHealthStore(path)
	_ = a.Restore(context.Background())
	_ = b.Restore(context.Background())

	observeWithRetry := func(store *FileHealthStore, operation string) error {
		obs := ports.CooldownObservation{
			Principal: principal, AccountID: "pa_merge",
			Scope:  domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: operation},
			Reason: domain.HealthReasonProviderRateLimited, CredentialVersion: 1,
			ObservedAt: now, RetryNotBefore: domain.NewTimestamp(now.Time().Add(time.Minute)),
			SourceClass: domain.HealthSourceUpstreamAttempt, Audit: noopAudit,
		}
		var last error
		for attempt := 0; attempt < 50; attempt++ {
			_, last = store.ObserveCooldown(context.Background(), obs)
			if last == nil {
				return nil
			}
			time.Sleep(time.Millisecond)
		}
		return last
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		errs <- observeWithRetry(a, string(domain.CapabilityOpChat))
	}()
	go func() {
		defer wg.Done()
		errs <- observeWithRetry(b, string(domain.CapabilityOpImageGeneration))
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("ObserveCooldown with retry error = %v", err)
		}
	}

	reload := NewFileHealthStore(path)
	_ = reload.Restore(context.Background())
	snap, err := reload.Read(context.Background(), principal, "pa_merge")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	var chat, image bool
	for _, c := range snap.Health.Conditions {
		if c.Scope.Operation == string(domain.CapabilityOpChat) && c.State == domain.HealthCoolingDown {
			chat = true
		}
		if c.Scope.Operation == string(domain.CapabilityOpImageGeneration) && c.State == domain.HealthCoolingDown {
			image = true
		}
	}
	if !chat || !image {
		t.Fatalf("expected both scopes; conditions=%+v", snap.Health.Conditions)
	}
}

func TestMemoryHealthStoreConcurrentDifferentScopesMerge(t *testing.T) {
	t.Parallel()
	store := NewMemoryHealthStore()
	principal := domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a"}
	now := testNow()
	_, err := store.Initialize(context.Background(), ports.HealthInitialize{
		Principal: principal, AccountID: "pa_mem_merge",
		Health: domain.HealthSummary{
			SummaryState: domain.HealthHealthy,
			Conditions:   []domain.HealthCondition{healthyAccountCond(now, 1)},
		},
		Audit: noopAudit,
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = store.ObserveCooldown(context.Background(), ports.CooldownObservation{
			Principal: principal, AccountID: "pa_mem_merge",
			Scope:  domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpChat)},
			Reason: domain.HealthReasonProviderRateLimited, CredentialVersion: 1,
			ObservedAt: now, RetryNotBefore: domain.NewTimestamp(now.Time().Add(time.Minute)),
			SourceClass: domain.HealthSourceUpstreamAttempt, Audit: noopAudit,
		})
	}()
	go func() {
		defer wg.Done()
		_, _ = store.ObserveCooldown(context.Background(), ports.CooldownObservation{
			Principal: principal, AccountID: "pa_mem_merge",
			Scope:  domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpImageGeneration)},
			Reason: domain.HealthReasonProviderRateLimited, CredentialVersion: 1,
			ObservedAt: now, RetryNotBefore: domain.NewTimestamp(now.Time().Add(time.Minute)),
			SourceClass: domain.HealthSourceUpstreamAttempt, Audit: noopAudit,
		})
	}()
	wg.Wait()
	snap, err := store.Read(context.Background(), principal, "pa_mem_merge")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	var chat, image bool
	for _, c := range snap.Health.Conditions {
		if c.Scope.Operation == string(domain.CapabilityOpChat) {
			chat = true
		}
		if c.Scope.Operation == string(domain.CapabilityOpImageGeneration) {
			image = true
		}
	}
	if !chat || !image {
		t.Fatalf("memory store lost a scope; conditions=%+v", snap.Health.Conditions)
	}
}

func TestFileHealthStoreRejectsNullAndCorruptRestore(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	nullPath := filepath.Join(dir, "null.ledger")
	_ = os.WriteFile(nullPath, []byte("null\n"), 0o640)
	if err := NewFileHealthStore(nullPath).Restore(context.Background()); err == nil {
		t.Fatal("Restore(null) want error")
	}
	corruptPath := filepath.Join(dir, "corrupt.ledger")
	_ = os.WriteFile(corruptPath, []byte("{not-json\n"), 0o640)
	if err := NewFileHealthStore(corruptPath).Restore(context.Background()); err == nil {
		t.Fatal("Restore(corrupt) want error")
	}
}

func TestFileHealthStoreRepeatedWritesWindowsSafe(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "health.ledger")
	store := NewFileHealthStore(path)
	_ = store.Restore(context.Background())
	principal := domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a"}
	now := testNow()
	_, err := store.Initialize(context.Background(), ports.HealthInitialize{
		Principal: principal, AccountID: "pa_repeat",
		Health: domain.HealthSummary{
			SummaryState: domain.HealthHealthy,
			Conditions:   []domain.HealthCondition{healthyAccountCond(now, 1)},
		},
		Audit: noopAudit,
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := store.ObserveCooldown(context.Background(), ports.CooldownObservation{
			Principal: principal, AccountID: "pa_repeat",
			Scope:  domain.HealthScope{Kind: domain.HealthScopeAccount},
			Reason: domain.HealthReasonProviderRateLimited, CredentialVersion: 1,
			ObservedAt: now, RetryNotBefore: domain.NewTimestamp(now.Time().Add(time.Minute)),
			SourceClass: domain.HealthSourceUpstreamAttempt, Audit: noopAudit,
		}); err != nil {
			t.Fatalf("ObserveCooldown %d: %v", i, err)
		}
	}
	reload := NewFileHealthStore(path)
	_ = reload.Restore(context.Background())
	snap, err := reload.Read(context.Background(), principal, "pa_repeat")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(snap.Health.Conditions) != 1 || snap.Health.Conditions[0].ConditionRevision != 5 {
		t.Fatalf("revision=%+v want 5", snap.Health.Conditions)
	}
}

func TestFileAccountStoreRejectsNullRestore(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")
	_ = os.WriteFile(path, []byte("null"), 0o640)
	if err := NewFileAccountStore(path).Restore(context.Background()); err == nil {
		t.Fatal("Restore(null) want error")
	}
}

func TestStaleHealthLockFailsClosed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "health.ledger")
	_ = os.WriteFile(path+".lock", []byte("stale\n"), 0o600)
	store := NewFileHealthStore(path)
	// Restore itself requires O_EXCL and must fail closed on lock collision.
	if err := store.Restore(context.Background()); err == nil {
		t.Fatal("Restore with stale lock want error")
	}
	_, err := store.Initialize(context.Background(), ports.HealthInitialize{
		Principal: domain.SecurityPrincipal{TenantID: "tenant_a"},
		AccountID: "pa_locked",
		Health: domain.HealthSummary{
			SummaryState: domain.HealthUnknown,
			Conditions: []domain.HealthCondition{{
				Scope: domain.HealthScope{Kind: domain.HealthScopeAccount}, State: domain.HealthUnknown,
				Reason: domain.HealthReasonInitialUnprobed, ObservedAt: testNow(),
				Remediation: domain.RemediationSubmitCredential, SourceClass: domain.HealthSourceRequiredProbe,
			}},
		},
		Audit: noopAudit,
	})
	if err == nil {
		t.Fatal("Initialize with stale lock want error")
	}
}

func TestFileAccountStoreRestoreLockCollision(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")
	_ = os.WriteFile(path+".lock", []byte("held\n"), 0o600)
	if err := NewFileAccountStore(path).Restore(context.Background()); err == nil {
		t.Fatal("Restore under foreign lock want fail-closed")
	}
	if !errors.Is(NewFileAccountStore(path).Restore(context.Background()), ports.ErrDependencyUnavailable) &&
		NewFileAccountStore(path).Restore(context.Background()) == nil {
		// re-check via errors.Is on a fresh store
	}
	err := NewFileAccountStore(path).Restore(context.Background())
	if err == nil || !errors.Is(err, ports.ErrDependencyUnavailable) {
		t.Fatalf("Restore lock collision = %v, want ErrDependencyUnavailable", err)
	}
}

func TestFileAccountStoreConcurrentUpdateLock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")
	principal := domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a"}
	account := domain.NewDraftProviderAccount("pa_lock", domain.ProviderChatGPT, domain.AuthModeChatGPTCodexOAuth, "x", testNow())
	first := NewFileAccountStore(path)
	_ = first.Restore(context.Background())
	if _, err := first.Create(context.Background(), ports.AccountCreation{Principal: principal, Account: account}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	unlock, err := first.acquireLock()
	if err != nil {
		t.Fatalf("acquireLock: %v", err)
	}
	second := NewFileAccountStore(path)
	_, err = second.Update(context.Background(), ports.AccountUpdate{
		Principal: principal,
		Account:   account,
	})
	unlock()
	if err == nil {
		t.Fatal("Update under foreign lock want fail-closed")
	}
}

func TestMemoryAccountStoreStripsHealth(t *testing.T) {
	t.Parallel()
	store := NewMemoryAccountStore()
	principal := domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a"}
	account := domain.NewDraftProviderAccount("pa_strip", domain.ProviderChatGPT, domain.AuthModeChatGPTCodexOAuth, "x", testNow())
	account.Health = domain.HealthSummary{SummaryState: domain.HealthCoolingDown, Conditions: []domain.HealthCondition{{
		Scope: domain.HealthScope{Kind: domain.HealthScopeAccount}, State: domain.HealthCoolingDown,
		Reason: domain.HealthReasonProviderRateLimited, ConditionRevision: 1,
	}}}
	account.RecoveryPermit = domain.RecoveryPermit{Owner: "x", Scope: domain.HealthScope{Kind: domain.HealthScopeAccount}, ConditionRevision: 1}
	created, err := store.Create(context.Background(), ports.AccountCreation{Principal: principal, Account: account})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Health.SummaryState != "" || created.RecoveryPermit.Owner != "" {
		t.Fatalf("Create returned health authority: %+v permit=%+v", created.Health, created.RecoveryPermit)
	}
	loaded, err := store.Visible(context.Background(), principal, "pa_strip")
	if err != nil {
		t.Fatalf("Visible: %v", err)
	}
	if loaded.Health.SummaryState != "" || loaded.RecoveryPermit.Owner != "" {
		t.Fatalf("Visible retained health: %+v", loaded.Health)
	}
}

func TestHardFailureMismatchedPermitNoMutation(t *testing.T) {
	t.Parallel()
	store := NewMemoryHealthStore()
	principal := domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a"}
	scope := domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpImageGeneration)}
	now := testNow()
	health := domain.HealthSummary{SummaryState: domain.HealthCoolingDown, Conditions: []domain.HealthCondition{
		coolingCond(scope, now, 3, 1, 1),
	}}
	store.Seed("tenant_a", "pa_hf", health, domain.RecoveryPermit{Owner: "owner_a", Scope: scope, ConditionRevision: 3, CredentialVersion: 1})
	audits := 0
	_, err := store.RecordHardFailure(context.Background(), ports.HardFailureObservation{
		Principal: principal, AccountID: "pa_hf", CredentialVersion: 1,
		ObservedAt:    now,
		ConsumePermit: domain.RecoveryPermit{Owner: "owner_a", Scope: scope, ConditionRevision: 2, CredentialVersion: 1},
		Audit: func(context.Context, []ports.HealthTransition) error {
			audits++
			return nil
		},
	})
	if err == nil {
		t.Fatal("mismatched revision must fail")
	}
	if audits != 0 {
		t.Fatalf("audit calls = %d, want 0 on fence failure", audits)
	}
	snap, _ := store.Read(context.Background(), principal, "pa_hf")
	if snap.RecoveryPermit.Owner != "owner_a" || snap.Health.Conditions[0].ConditionRevision != 3 {
		t.Fatalf("state mutated on fence failure: %+v", snap)
	}
}

func TestAuditFailureBlocksPersist(t *testing.T) {
	t.Parallel()
	store := NewMemoryHealthStore()
	principal := domain.SecurityPrincipal{TenantID: "tenant_a"}
	now := testNow()
	_, _ = store.Initialize(context.Background(), ports.HealthInitialize{
		Principal: principal, AccountID: "pa_audit",
		Health: domain.HealthSummary{SummaryState: domain.HealthHealthy, Conditions: []domain.HealthCondition{
			healthyAccountCond(now, 1),
		}},
		Audit: noopAudit,
	})
	_, err := store.ObserveCooldown(context.Background(), ports.CooldownObservation{
		Principal: principal, AccountID: "pa_audit",
		Scope:  domain.HealthScope{Kind: domain.HealthScopeAccount},
		Reason: domain.HealthReasonProviderRateLimited, CredentialVersion: 1,
		ObservedAt: now, RetryNotBefore: domain.NewTimestamp(now.Time().Add(time.Minute)),
		SourceClass: domain.HealthSourceUpstreamAttempt,
		Audit:       func(context.Context, []ports.HealthTransition) error { return ports.ErrDependencyUnavailable },
	})
	if err == nil {
		t.Fatal("audit failure must abort")
	}
	snap, _ := store.Read(context.Background(), principal, "pa_audit")
	if snap.Health.SummaryState != domain.HealthHealthy {
		t.Fatalf("health mutated after audit failure: %v", snap.Health.SummaryState)
	}
}

func TestMultiScopeObserveBatchAuditAtomicity(t *testing.T) {
	t.Parallel()
	store := NewMemoryHealthStore()
	principal := domain.SecurityPrincipal{TenantID: "tenant_a"}
	now := testNow()
	claimedScope := domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpChat)}
	freshScope := domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpImageGeneration)}
	health := domain.HealthSummary{
		SummaryState: domain.HealthCoolingDown,
		Conditions: []domain.HealthCondition{
			coolingCond(claimedScope, now, 2, 1, 1),
			coolingCond(freshScope, now, 1, 1, 1),
		},
	}
	// Fix summary: two cooling = cooling
	permit := domain.RecoveryPermit{Owner: "owner_1", Scope: claimedScope, ConditionRevision: 2, CredentialVersion: 1}
	store.Seed("tenant_a", "pa_batch", health, permit)

	var got []ports.HealthTransition
	claimedRetry := domain.NewTimestamp(now.Time().Add(2 * time.Minute))
	freshRetry := domain.NewTimestamp(now.Time().Add(3 * time.Minute))
	tr, err := store.ObserveCooldown(context.Background(), ports.CooldownObservation{
		Principal: principal, AccountID: "pa_batch",
		Scope: freshScope, Reason: domain.HealthReasonProviderRateLimited, CredentialVersion: 1,
		ObservedAt: now, RetryNotBefore: freshRetry, SourceClass: domain.HealthSourceUpstreamAttempt,
		ConsumePermit:              permit,
		ClaimedScopeReason:         domain.HealthReasonRecoveryProbeFailed,
		ClaimedScopeRetryNotBefore: claimedRetry,
		ClaimedScopeSourceClass:    domain.HealthSourceRecoveryProbe,
		Audit: func(_ context.Context, batch []ports.HealthTransition) error {
			got = append([]ports.HealthTransition(nil), batch...)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("ObserveCooldown: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("batch size = %d, want 2 exact transitions", len(got))
	}
	// First: claimed-scope renew
	if got[0].Outcome != "cooldown_renew" || !sameHealthScope(got[0].Scope, claimedScope) {
		t.Fatalf("event[0] = outcome=%s scope=%+v", got[0].Outcome, got[0].Scope)
	}
	if got[0].NewCondition.Reason != domain.HealthReasonRecoveryProbeFailed {
		t.Fatalf("claimed reason = %s, want recovery_probe_failed", got[0].NewCondition.Reason)
	}
	if got[0].NewCondition.ConditionRevision != 3 {
		t.Fatalf("claimed rev = %d, want 3", got[0].NewCondition.ConditionRevision)
	}
	if !got[0].NewCondition.RetryNotBefore.Time().Equal(claimedRetry.Time()) {
		t.Fatalf("claimed retry = %v, want %v", got[0].NewCondition.RetryNotBefore, claimedRetry)
	}
	if got[0].NewCondition.SourceClass != domain.HealthSourceRecoveryProbe {
		t.Fatalf("claimed source = %s", got[0].NewCondition.SourceClass)
	}
	if got[0].PriorPermit.Owner != "owner_1" || got[0].NewPermit.Owner != "" {
		t.Fatalf("claimed permit transition prior=%+v new=%+v", got[0].PriorPermit, got[0].NewPermit)
	}
	// Second: fresh-scope create/renew
	if !sameHealthScope(got[1].Scope, freshScope) {
		t.Fatalf("event[1] scope = %+v", got[1].Scope)
	}
	if got[1].NewCondition.ConditionRevision != 2 {
		t.Fatalf("fresh rev = %d, want 2", got[1].NewCondition.ConditionRevision)
	}
	if !got[1].NewCondition.RetryNotBefore.Time().Equal(freshRetry.Time()) {
		t.Fatalf("fresh retry mismatch")
	}
	if tr.Result.RecoveryPermit.Owner != "" {
		t.Fatalf("permit must be consumed: %+v", tr.Result.RecoveryPermit)
	}

	// Forced batch failure: zero recorded transitions, state unchanged.
	store2 := NewMemoryHealthStore()
	store2.Seed("tenant_a", "pa_batch_fail", health, permit)
	var failCalls int
	_, err = store2.ObserveCooldown(context.Background(), ports.CooldownObservation{
		Principal: principal, AccountID: "pa_batch_fail",
		Scope: freshScope, Reason: domain.HealthReasonProviderRateLimited, CredentialVersion: 1,
		ObservedAt: now, RetryNotBefore: freshRetry, SourceClass: domain.HealthSourceUpstreamAttempt,
		ConsumePermit: permit, ClaimedScopeReason: domain.HealthReasonRecoveryProbeFailed,
		ClaimedScopeRetryNotBefore: claimedRetry, ClaimedScopeSourceClass: domain.HealthSourceRecoveryProbe,
		Audit: func(context.Context, []ports.HealthTransition) error {
			failCalls++
			return ports.ErrDependencyUnavailable
		},
	})
	if err == nil {
		t.Fatal("batch audit failure must abort")
	}
	if failCalls != 1 {
		t.Fatalf("audit calls = %d, want exactly 1 batch invocation", failCalls)
	}
	snap, _ := store2.Read(context.Background(), principal, "pa_batch_fail")
	if snap.RecoveryPermit.Owner != "owner_1" {
		t.Fatalf("permit mutated after failed batch audit: %+v", snap.RecoveryPermit)
	}
	for _, c := range snap.Health.Conditions {
		if sameHealthScope(c.Scope, claimedScope) && c.ConditionRevision != 2 {
			t.Fatalf("claimed scope mutated on failed audit: %+v", c)
		}
		if sameHealthScope(c.Scope, freshScope) && c.ConditionRevision != 1 {
			t.Fatalf("fresh scope mutated on failed audit: %+v", c)
		}
	}
}

func TestNilRequiredAuditLeavesStateUnchanged(t *testing.T) {
	t.Parallel()
	store := NewMemoryHealthStore()
	principal := domain.SecurityPrincipal{TenantID: "tenant_a"}
	now := testNow()
	_, err := store.Initialize(context.Background(), ports.HealthInitialize{
		Principal: principal, AccountID: "pa_nil",
		Health: domain.HealthSummary{SummaryState: domain.HealthHealthy, Conditions: []domain.HealthCondition{
			healthyAccountCond(now, 1),
		}},
		// nil Audit required
	})
	if !errors.Is(err, ports.ErrRequiredHealthAudit) {
		t.Fatalf("Initialize nil audit = %v, want ErrRequiredHealthAudit", err)
	}
	if _, err := store.Read(context.Background(), principal, "pa_nil"); !errors.Is(err, ports.ErrHealthNotFound) {
		t.Fatalf("nil-audit Initialize must not persist: %v", err)
	}

	_, _ = store.Initialize(context.Background(), ports.HealthInitialize{
		Principal: principal, AccountID: "pa_nil2",
		Health: domain.HealthSummary{SummaryState: domain.HealthHealthy, Conditions: []domain.HealthCondition{
			healthyAccountCond(now, 1),
		}},
		Audit: noopAudit,
	})
	before, _ := store.Read(context.Background(), principal, "pa_nil2")
	_, err = store.ObserveCooldown(context.Background(), ports.CooldownObservation{
		Principal: principal, AccountID: "pa_nil2",
		Scope:  domain.HealthScope{Kind: domain.HealthScopeAccount},
		Reason: domain.HealthReasonProviderRateLimited, CredentialVersion: 1,
		ObservedAt: now, RetryNotBefore: domain.NewTimestamp(now.Time().Add(time.Minute)),
		SourceClass: domain.HealthSourceUpstreamAttempt,
	})
	if !errors.Is(err, ports.ErrRequiredHealthAudit) {
		t.Fatalf("ObserveCooldown nil audit = %v", err)
	}
	after, _ := store.Read(context.Background(), principal, "pa_nil2")
	if after.Health.SummaryState != before.Health.SummaryState {
		t.Fatalf("state changed on nil audit: before=%v after=%v", before.Health.SummaryState, after.Health.SummaryState)
	}

	// ClearPermit with occupied permit requires audit.
	scope := domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpChat)}
	store.Seed("tenant_a", "pa_clear", domain.HealthSummary{
		SummaryState: domain.HealthCoolingDown,
		Conditions:   []domain.HealthCondition{coolingCond(scope, now, 1, 1, 1)},
	}, domain.RecoveryPermit{Owner: "o", Scope: scope, ConditionRevision: 1, CredentialVersion: 1})
	_, err = store.ClearPermit(context.Background(), ports.PermitClear{Principal: principal, AccountID: "pa_clear"})
	if !errors.Is(err, ports.ErrRequiredHealthAudit) {
		t.Fatalf("ClearPermit nil audit = %v", err)
	}
	snap, _ := store.Read(context.Background(), principal, "pa_clear")
	if snap.RecoveryPermit.Owner != "o" {
		t.Fatalf("ClearPermit nil audit mutated permit")
	}
}

func TestClaimCASLoserExactConflict(t *testing.T) {
	t.Parallel()
	store := NewMemoryHealthStore()
	principal := domain.SecurityPrincipal{TenantID: "tenant_a"}
	now := testNow()
	scope := domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpImageGeneration)}
	// Same pre-claim snapshot: both callers see empty permit + eligible cooling condition.
	// RetryNotBefore is after ObservedAt (restore invariant) but before decision time.
	account := domain.ProviderAccount{
		Credential: domain.CredentialMetadata{Version: 1},
		Health: domain.HealthSummary{
			SummaryState: domain.HealthCoolingDown,
			Conditions:   []domain.HealthCondition{coolingCond(scope, now, 1, 1, 1)},
		},
	}
	account.Health.Conditions[0].RetryNotBefore = domain.NewTimestamp(now.Time().Add(30 * time.Second))
	store.Seed("tenant_a", "pa_cas", account.Health, domain.RecoveryPermit{})

	decisionAt := domain.NewTimestamp(now.Time().Add(time.Minute))
	d1 := account.ScopedRecoveryPermit(decisionAt, scope, "req_a")
	d2 := account.ScopedRecoveryPermit(decisionAt, scope, "req_b")
	if !d1.Eligible || !d2.Eligible || d1.Occupied || d2.Occupied {
		t.Fatalf("both must be Eligible from same snapshot: d1=%+v d2=%+v", d1, d2)
	}
	if _, err := store.ClaimRecoveryPermit(context.Background(), ports.RecoveryPermitClaim{
		Principal: principal, AccountID: "pa_cas", Owner: d1.Permit.Owner,
		Scope: d1.Permit.Scope, ConditionRevision: d1.Permit.ConditionRevision, CredentialVersion: d1.Permit.CredentialVersion,
	}); err != nil {
		t.Fatalf("winner claim: %v", err)
	}
	_, err := store.ClaimRecoveryPermit(context.Background(), ports.RecoveryPermitClaim{
		Principal: principal, AccountID: "pa_cas", Owner: d2.Permit.Owner,
		Scope: d2.Permit.Scope, ConditionRevision: d2.Permit.ConditionRevision, CredentialVersion: d2.Permit.CredentialVersion,
	})
	if !errors.Is(err, ports.ErrAccountUpdateConflict) {
		t.Fatalf("CAS loser = %v, want ErrAccountUpdateConflict", err)
	}
}

// TestClearPermitStaleExpectedPreservesNewer proves request-owned cleanup CAS:
// a ClearPermit fenced to a stale ExpectedPermit must not delete a newer or
// different owner's permit, and must return ErrAccountUpdateConflict.
// Runs against both Memory and File HealthStore implementations.
func TestClearPermitStaleExpectedPreservesNewer(t *testing.T) {
	t.Parallel()

	principal := domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a"}
	now := testNow()
	scope := domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpChat)}
	health := domain.HealthSummary{
		SummaryState: domain.HealthCoolingDown,
		Conditions:   []domain.HealthCondition{coolingCond(scope, now, 2, 1, 1)},
	}
	stale := domain.RecoveryPermit{
		Owner: "req_stale", Scope: scope, ConditionRevision: 2, CredentialVersion: 1,
	}
	newer := domain.RecoveryPermit{
		Owner: "req_newer", Scope: scope, ConditionRevision: 2, CredentialVersion: 1,
	}

	assertStaleClearPreserves := func(t *testing.T, store ports.HealthStore) {
		t.Helper()
		_, err := store.ClearPermit(context.Background(), ports.PermitClear{
			Principal: principal, AccountID: "pa_clear_cas",
			ExpectedPermit: stale, Audit: noopAudit,
		})
		if !errors.Is(err, ports.ErrAccountUpdateConflict) {
			t.Fatalf("stale ExpectedPermit clear = %v, want ErrAccountUpdateConflict", err)
		}
		snap, err := store.Read(context.Background(), principal, "pa_clear_cas")
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if snap.RecoveryPermit != newer {
			t.Fatalf("permit after stale clear = %+v, want newer %+v preserved", snap.RecoveryPermit, newer)
		}
		// Matching expected clears the permit.
		if _, err := store.ClearPermit(context.Background(), ports.PermitClear{
			Principal: principal, AccountID: "pa_clear_cas",
			ExpectedPermit: newer, Audit: noopAudit,
		}); err != nil {
			t.Fatalf("matching ExpectedPermit clear: %v", err)
		}
		snap, err = store.Read(context.Background(), principal, "pa_clear_cas")
		if err != nil {
			t.Fatalf("Read after match: %v", err)
		}
		if snap.RecoveryPermit.Owner != "" {
			t.Fatalf("matching clear left owner=%q", snap.RecoveryPermit.Owner)
		}
	}

	assertAdminClear := func(t *testing.T, store ports.HealthStore, reseed func()) {
		t.Helper()
		reseed()
		if _, err := store.ClearPermit(context.Background(), ports.PermitClear{
			Principal: principal, AccountID: "pa_clear_cas", Audit: noopAudit,
		}); err != nil {
			t.Fatalf("admin ClearPermit: %v", err)
		}
		snap, err := store.Read(context.Background(), principal, "pa_clear_cas")
		if err != nil {
			t.Fatalf("Read after admin: %v", err)
		}
		if snap.RecoveryPermit.Owner != "" {
			t.Fatalf("admin clear left owner=%q", snap.RecoveryPermit.Owner)
		}
	}

	t.Run("memory", func(t *testing.T) {
		t.Parallel()
		mem := NewMemoryHealthStore()
		mem.Seed("tenant_a", "pa_clear_cas", health, newer)
		assertStaleClearPreserves(t, mem)
		assertAdminClear(t, mem, func() {
			mem.Seed("tenant_a", "pa_clear_cas", health, newer)
		})
	})

	t.Run("file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "health.ledger")
		file := NewFileHealthStore(path)
		if err := file.Restore(context.Background()); err != nil {
			t.Fatalf("Restore: %v", err)
		}
		if _, err := file.Initialize(context.Background(), ports.HealthInitialize{
			Principal: principal, AccountID: "pa_clear_cas", Health: health, Audit: noopAudit,
		}); err != nil {
			t.Fatalf("Initialize: %v", err)
		}
		if _, err := file.ClaimRecoveryPermit(context.Background(), ports.RecoveryPermitClaim{
			Principal: principal, AccountID: "pa_clear_cas",
			Owner: newer.Owner, Scope: newer.Scope,
			ConditionRevision: newer.ConditionRevision, CredentialVersion: newer.CredentialVersion,
		}); err != nil {
			t.Fatalf("seed claim newer: %v", err)
		}
		assertStaleClearPreserves(t, file)
		assertAdminClear(t, file, func() {
			if _, err := file.ClaimRecoveryPermit(context.Background(), ports.RecoveryPermitClaim{
				Principal: principal, AccountID: "pa_clear_cas",
				Owner: newer.Owner, Scope: newer.Scope,
				ConditionRevision: newer.ConditionRevision, CredentialVersion: newer.CredentialVersion,
			}); err != nil {
				t.Fatalf("reseed claim: %v", err)
			}
		})
	})
}

func TestDeepRestoreHealthConditionFamilies(t *testing.T) {
	t.Parallel()
	now := testNow()
	dir := t.TempDir()

	cases := []struct {
		name    string
		mutate  func(r *healthRecord)
		wantErr string
	}{
		{
			name: "invalid_reason",
			mutate: func(r *healthRecord) {
				r.Health.Conditions[0].Reason = domain.HealthReason("not_a_reason")
			},
			wantErr: "invalid reason",
		},
		{
			name: "cooling_wrong_remediation",
			mutate: func(r *healthRecord) {
				r.Health.Conditions[0] = coolingCond(domain.HealthScope{Kind: domain.HealthScopeAccount}, now, 1, 1, 1)
				r.Health.Conditions[0].Remediation = domain.RemediationNone
				r.Health.SummaryState = domain.HealthCoolingDown
			},
			wantErr: "wait_provider_cooldown",
		},
		{
			name: "cooling_missing_retry",
			mutate: func(r *healthRecord) {
				c := coolingCond(domain.HealthScope{Kind: domain.HealthScopeAccount}, now, 1, 1, 1)
				c.RetryNotBefore = domain.Timestamp{}
				r.Health.Conditions[0] = c
				r.Health.SummaryState = domain.HealthCoolingDown
			},
			wantErr: "RetryNotBefore",
		},
		{
			name: "expired_with_retry",
			mutate: func(r *healthRecord) {
				r.Health.Conditions[0] = domain.HealthCondition{
					Scope: domain.HealthScope{Kind: domain.HealthScopeAccount}, State: domain.HealthExpired,
					Reason: domain.HealthReasonCredentialRejected, CredentialVersion: 1, ConditionRevision: 1,
					ObservedAt: now, Remediation: domain.RemediationReauthenticate,
					RetryNotBefore: domain.NewTimestamp(now.Time().Add(time.Minute)),
				}
				r.Health.SummaryState = domain.HealthExpired
			},
			wantErr: "must not carry RetryNotBefore",
		},
		{
			name: "account_scope_with_operation",
			mutate: func(r *healthRecord) {
				r.Health.Conditions[0].Scope.Operation = "chat"
			},
			wantErr: "empty operation",
		},
		{
			name: "operation_scope_empty_op",
			mutate: func(r *healthRecord) {
				r.Health.Conditions[0].Scope = domain.HealthScope{Kind: domain.HealthScopeOperation}
			},
			wantErr: "requires operation",
		},
		{
			name: "model_scope_missing_slug",
			mutate: func(r *healthRecord) {
				r.Health.Conditions[0].Scope = domain.HealthScope{Kind: domain.HealthScopeModel, Operation: string(domain.CapabilityOpChat)}
			},
			wantErr: "operation and model",
		},
		{
			name: "missing_observed_at",
			mutate: func(r *healthRecord) {
				r.Health.Conditions[0].ObservedAt = domain.Timestamp{}
			},
			wantErr: "ObservedAt",
		},
		{
			name: "permit_partial",
			mutate: func(r *healthRecord) {
				r.Health.Conditions[0] = coolingCond(domain.HealthScope{Kind: domain.HealthScopeAccount}, now, 1, 1, 1)
				r.Health.SummaryState = domain.HealthCoolingDown
				r.RecoveryPermit = domain.RecoveryPermit{Owner: "o"} // incomplete
			},
			wantErr: "all present",
		},
		{
			name: "permit_not_cooling",
			mutate: func(r *healthRecord) {
				r.RecoveryPermit = domain.RecoveryPermit{
					Owner: "o", Scope: domain.HealthScope{Kind: domain.HealthScopeAccount},
					ConditionRevision: 0, CredentialVersion: 1,
				}
				// healthy condition rev 0 — permit requires positive rev + cooling
			},
			wantErr: "positive",
		},
		{
			name: "backoff_over_bound",
			mutate: func(r *healthRecord) {
				c := coolingCond(domain.HealthScope{Kind: domain.HealthScopeAccount}, now, 1, 1, 99)
				r.Health.Conditions[0] = c
				r.Health.SummaryState = domain.HealthCoolingDown
			},
			wantErr: "backoff_level",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".ledger")
			record := healthRecord{
				TenantID:  "tenant_a",
				AccountID: "pa_deep",
				Health: domain.HealthSummary{
					SummaryState: domain.HealthHealthy,
					Conditions:   []domain.HealthCondition{healthyAccountCond(now, 1)},
				},
			}
			tc.mutate(&record)
			raw, _ := json.Marshal(record)
			_ = os.WriteFile(path, append(raw, '\n'), 0o640)
			err := NewFileHealthStore(path).Restore(context.Background())
			if err == nil {
				t.Fatal("want restore error")
			}
			if tc.wantErr != "" && !containsFold(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestDeepRestoreAccountRowFamilies(t *testing.T) {
	t.Parallel()
	now := testNow()
	dir := t.TempDir()

	base := domain.NewDraftProviderAccount("pa_acc", domain.ProviderChatGPT, domain.AuthModeChatGPTCodexOAuth, "label", now)

	cases := []struct {
		name    string
		mutate  func(a *domain.ProviderAccount)
		wantErr string
	}{
		{
			name: "invalid_drain",
			mutate: func(a *domain.ProviderAccount) {
				a.Controls.Drain = domain.DrainState("weird")
			},
			wantErr: "drain",
		},
		{
			name: "invalid_quarantine",
			mutate: func(a *domain.ProviderAccount) {
				a.Controls.Quarantine = domain.QuarantineState("weird")
			},
			wantErr: "quarantine",
		},
		{
			name: "updated_before_created",
			mutate: func(a *domain.ProviderAccount) {
				a.UpdatedAt = domain.NewTimestamp(now.Time().Add(-time.Hour))
			},
			wantErr: "updated_at before created_at",
		},
		{
			name: "pending_without_origin",
			mutate: func(a *domain.ProviderAccount) {
				a.Credential.Version = 1
				a.Credential.LastAllocatedVersion = 2
				a.PendingCredentialVersion = 2
				a.PendingOrigin = ""
			},
			wantErr: "pending origin",
		},
		{
			name: "origin_without_pending",
			mutate: func(a *domain.ProviderAccount) {
				a.PendingOrigin = domain.LifecycleActive
			},
			wantErr: "pending origin allowed only",
		},
		{
			name: "allocated_below_version",
			mutate: func(a *domain.ProviderAccount) {
				a.Credential.Version = 3
				a.Credential.LastAllocatedVersion = 1
			},
			wantErr: "last_allocated_version",
		},
		{
			name: "zero_created_at",
			mutate: func(a *domain.ProviderAccount) {
				a.CreatedAt = domain.Timestamp{}
			},
			wantErr: "created_at",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".json")
			account := base
			tc.mutate(&account)
			// FileAccountStore format: JSONL of account rows with tenant.
			type entry struct {
				TenantID domain.TenantID        `json:"tenant_id"`
				Account  domain.ProviderAccount `json:"account"`
			}
			// Match store's on-disk shape via package-visible reload path by
			// writing through Create then patching file is hard; write raw ledger.
			// Inspect actual format:
			raw, err := marshalAccountLedger("tenant_a", account)
			if err != nil {
				t.Fatal(err)
			}
			_ = os.WriteFile(path, raw, 0o640)
			err = NewFileAccountStore(path).Restore(context.Background())
			if err == nil {
				t.Fatal("want restore error")
			}
			if tc.wantErr != "" && !containsFold(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func marshalAccountLedger(tenant domain.TenantID, account domain.ProviderAccount) ([]byte, error) {
	// Mirror FileAccountStore append shape: one JSON object per line.
	// Read reloadLocked to confirm field names.
	type row struct {
		TenantID domain.TenantID        `json:"tenant_id"`
		Account  domain.ProviderAccount `json:"account"`
	}
	// Try common shapes used by this package.
	data, err := json.Marshal(struct {
		TenantID domain.TenantID        `json:"tenant_id"`
		Account  domain.ProviderAccount `json:"account"`
	}{TenantID: tenant, Account: account})
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func containsFold(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if equalFoldASCII(s[i:i+len(sub)], sub) {
				return true
			}
		}
		return false
	})())
}

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

func TestCredentialEpochResetFirstConnectAndPreserve(t *testing.T) {
	t.Parallel()
	store := NewMemoryHealthStore()
	principal := domain.SecurityPrincipal{TenantID: "tenant_a"}
	now := testNow()
	// Draft-like v0 health
	_, err := store.Initialize(context.Background(), ports.HealthInitialize{
		Principal: principal, AccountID: "pa_epoch",
		Health: domain.HealthSummary{SummaryState: domain.HealthUnknown, Conditions: []domain.HealthCondition{{
			Scope: domain.HealthScope{Kind: domain.HealthScopeAccount}, State: domain.HealthUnknown,
			Reason: domain.HealthReasonInitialUnprobed, ObservedAt: now, Remediation: domain.RemediationSubmitCredential,
			SourceClass: domain.HealthSourceRequiredProbe,
		}}},
		Audit: noopAudit,
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	tr, err := store.ResetForCredentialEpoch(context.Background(), ports.CredentialEpochReset{
		Principal: principal, AccountID: "pa_epoch", NewCredentialVersion: 1, ObservedAt: now, Audit: noopAudit,
	})
	if err != nil {
		t.Fatalf("epoch first-connect: %v", err)
	}
	if tr.NewCondition.CredentialVersion != 1 || tr.NewCondition.Reason != domain.HealthReasonInitialUnprobed {
		t.Fatalf("first-connect epoch = %+v", tr.NewCondition)
	}

	// Replacement preserve: seed v1 cooling + permit, epoch to pending v2 keeps v1 only
	scope := domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpImageGeneration)}
	store.Seed("tenant_a", "pa_epoch2", domain.HealthSummary{
		SummaryState: domain.HealthCoolingDown,
		Conditions:   []domain.HealthCondition{coolingCond(scope, now, 1, 1, 1)},
	}, domain.RecoveryPermit{Owner: "o", Scope: scope, ConditionRevision: 1, CredentialVersion: 1})
	tr, err = store.ResetForCredentialEpoch(context.Background(), ports.CredentialEpochReset{
		Principal: principal, AccountID: "pa_epoch2", NewCredentialVersion: 2,
		PreserveCredentialVersion: 1, ObservedAt: now, Audit: noopAudit,
	})
	if err != nil {
		t.Fatalf("epoch preserve: %v", err)
	}
	if tr.Result.RecoveryPermit.Owner != "" {
		t.Fatal("permit must clear on epoch")
	}
	if len(tr.Result.Health.Conditions) != 1 || tr.Result.Health.Conditions[0].CredentialVersion != 1 {
		t.Fatalf("preserve conditions = %+v", tr.Result.Health.Conditions)
	}
	// Audit failure aborts
	_, err = store.ResetForCredentialEpoch(context.Background(), ports.CredentialEpochReset{
		Principal: principal, AccountID: "pa_epoch2", NewCredentialVersion: 3,
		PreserveCredentialVersion: 1, ObservedAt: now,
		Audit: func(context.Context, []ports.HealthTransition) error { return ports.ErrDependencyUnavailable },
	})
	if err == nil {
		t.Fatal("want audit failure")
	}
	snap, _ := store.Read(context.Background(), principal, "pa_epoch2")
	if len(snap.Health.Conditions) != 1 {
		t.Fatalf("audit fail mutated: %+v", snap.Health.Conditions)
	}
}

func TestPendingOnlyHardFailurePersistsV2ExpiredAndPreservesOrigin(t *testing.T) {
	t.Parallel()
	store := NewMemoryHealthStore()
	principal := domain.SecurityPrincipal{TenantID: "tenant_a"}
	now := testNow()
	scope := domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpChat)}
	store.Seed("tenant_a", "pa_pend_hf", domain.HealthSummary{
		SummaryState: domain.HealthCoolingDown,
		Conditions: []domain.HealthCondition{
			coolingCond(scope, now, 1, 1, 1),
			healthyAccountCond(now, 1),
		},
	}, domain.RecoveryPermit{})
	tr, err := store.RecordHardFailure(context.Background(), ports.HardFailureObservation{
		Principal: principal, AccountID: "pa_pend_hf", CredentialVersion: 2,
		ObservedAt: now, PendingOnly: true, Audit: noopAudit,
	})
	if err != nil {
		t.Fatalf("pending hard: %v", err)
	}
	if tr.NewCondition.State != domain.HealthExpired ||
		tr.NewCondition.Reason != domain.HealthReasonCredentialRejected ||
		tr.NewCondition.CredentialVersion != 2 ||
		tr.NewCondition.Remediation != domain.RemediationReauthenticate {
		t.Fatalf("NewCondition = %+v, want account expired/credential_rejected @ v2", tr.NewCondition)
	}
	var foundOriginCooling, foundOriginHealthy, foundV2Expired bool
	for _, c := range tr.Result.Health.Conditions {
		if c.CredentialVersion == 1 && c.State == domain.HealthCoolingDown {
			foundOriginCooling = true
		}
		if c.CredentialVersion == 1 && c.State == domain.HealthHealthy {
			foundOriginHealthy = true
		}
		if c.CredentialVersion == 2 && c.State == domain.HealthExpired {
			foundV2Expired = true
			if c.ConditionRevision < 1 {
				t.Fatalf("v2 revision = %d, want positive", c.ConditionRevision)
			}
		}
	}
	if !foundOriginCooling || !foundOriginHealthy || !foundV2Expired {
		t.Fatalf("want origin v1 cooling+healthy and durable v2 expired; got %+v", tr.Result.Health.Conditions)
	}
}

func TestRestoreAcceptsSameScopeDifferentVersions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "multi_ver.ledger")
	now := testNow()
	record := healthRecord{
		TenantID: "tenant_a", AccountID: "pa_mv",
		Health: domain.HealthSummary{
			SummaryState: domain.HealthExpired,
			Conditions: []domain.HealthCondition{
				healthyAccountCond(now, 1),
				{
					Scope: domain.HealthScope{Kind: domain.HealthScopeAccount}, State: domain.HealthExpired,
					Reason: domain.HealthReasonCredentialRejected, CredentialVersion: 2, ConditionRevision: 1,
					ObservedAt: now, Remediation: domain.RemediationReauthenticate,
					SourceClass: domain.HealthSourceRequiredProbe,
				},
			},
		},
	}
	raw, _ := json.Marshal(record)
	_ = os.WriteFile(path, append(raw, '\n'), 0o640)
	if err := NewFileHealthStore(path).Restore(context.Background()); err != nil {
		t.Fatalf("Restore multi-version: %v", err)
	}
	// Duplicate same-scope same-version must fail.
	record.Health.Conditions = append(record.Health.Conditions, healthyAccountCond(now, 1))
	raw, _ = json.Marshal(record)
	dupPath := filepath.Join(dir, "dup.ledger")
	_ = os.WriteFile(dupPath, append(raw, '\n'), 0o640)
	if err := NewFileHealthStore(dupPath).Restore(context.Background()); err == nil {
		t.Fatal("duplicate same-scope same-version want error")
	}
}
