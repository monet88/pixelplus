package contracttest_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// Disable-before-claim: claim blocks until disable finishes; post-claim fence
// clears the permit and never reaches Vault/Adapter.
//
// Ordering: soft gate Eligible → admit probe → ClaimRecoveryPermit blocks →
// disable admits and administratively clears → claim proceeds → post-claim
// probeGate fails → request-owned ClearPermit(Expected=claimed) → no Vault/Adapter.
func TestDisableBeforeClaimRaceClearsPermitAndSkipsProtectedWork(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_disable_claim_race", domain.AuthModeChatGPTCodexOAuth)
		account = account.WithScopedCooldown(
			domain.NewTimestamp(spineFixtureTime),
			domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpImageGeneration)},
			domain.HealthReasonProviderRateLimited,
			domain.NewTimestamp(spineFixtureTime),
		)
		h.seedAccount("tenant_a", account)
		h.health.claimEntered = entered
		h.health.claimRelease = release
	})

	type result struct {
		status int
		body   []byte
	}
	done := make(chan result, 1)
	go func() {
		resp, body := harness.do(t, requestSpec{
			method: http.MethodPost,
			path:   "/v1/provider-accounts/pa_disable_claim_race/probe",
			bearer: tenantAKey,
			body:   `{"scope":{"kind":"operation","operation":"image_generation"}}`,
		})
		done <- result{status: resp.StatusCode, body: body}
	}()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("claim did not enter HealthStore.ClaimRecoveryPermit")
	}
	// Probe already passed soft gate and admitted before claim (normative order).
	if got := harness.admission.admitCalls.Load(); got != 1 {
		t.Fatalf("admitCalls while claim blocked = %d, want 1 (probe admitted before claim)", got)
	}

	disResp, disBody := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_disable_claim_race/disable",
		bearer: tenantAKey,
	})
	if disResp.StatusCode != http.StatusOK {
		t.Fatalf("disable status = %d want 200 (body=%s)", disResp.StatusCode, disBody)
	}
	// Disable also admits (probe=1 + disable=1).
	if got := harness.admission.admitCalls.Load(); got != 2 {
		t.Fatalf("admitCalls after disable = %d, want 2 (probe+disable)", got)
	}
	close(release)

	probeRes := <-done
	if probeRes.status != http.StatusConflict {
		t.Fatalf("probe status = %d, want 409 fail-closed after disable (body=%s)", probeRes.status, probeRes.body)
	}
	if harness.vault.validCalls.Load() != 0 {
		t.Fatalf("vault validate = %d, want 0", harness.vault.validCalls.Load())
	}
	if harness.probe.callCount.Load() != 0 {
		t.Fatalf("adapter probe = %d, want 0", harness.probe.callCount.Load())
	}
	// No additional admission after post-claim abort (still probe+disable only).
	if got := harness.admission.admitCalls.Load(); got != 2 {
		t.Fatalf("admitCalls after probe abort = %d, want 2", got)
	}
	stored := harness.storedAccount(t, managePrincipal(), "pa_disable_claim_race")
	if stored.Lifecycle != domain.LifecycleDisabled {
		t.Fatalf("lifecycle = %v, want disabled", stored.Lifecycle)
	}
	if stored.RecoveryPermit.Owner != "" {
		t.Fatalf("permit stranded owner=%q", stored.RecoveryPermit.Owner)
	}
}

// Request-owned post-claim ClearPermit with ExpectedPermit must not wipe a
// newer/different owner's permit when durable identity changed before cleanup.
func TestStaleRequestOwnedPermitClearPreservesNewerPermit(t *testing.T) {
	t.Parallel()

	scope := domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpImageGeneration)}
	newer := domain.RecoveryPermit{
		Owner: "req_foreign_newer", Scope: scope, ConditionRevision: 1, CredentialVersion: 1,
	}
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_stale_clear", domain.AuthModeChatGPTCodexOAuth)
		account = account.WithScopedCooldown(
			domain.NewTimestamp(spineFixtureTime),
			scope,
			domain.HealthReasonProviderRateLimited,
			domain.NewTimestamp(spineFixtureTime),
		)
		h.seedAccount("tenant_a", account)
		h.health.claimEntered = entered
		h.health.claimRelease = release
		// When request-owned cleanup runs ClearPermit(Expected=claimed), inject a
		// different owner first so CAS must fail closed and preserve newer.
		h.health.swapPermitBeforeClear = &newer
	})

	type result struct {
		status int
		body   []byte
	}
	done := make(chan result, 1)
	go func() {
		resp, body := harness.do(t, requestSpec{
			method: http.MethodPost,
			path:   "/v1/provider-accounts/pa_stale_clear/probe",
			bearer: tenantAKey,
			body:   `{"scope":{"kind":"operation","operation":"image_generation"}}`,
		})
		done <- result{status: resp.StatusCode, body: body}
	}()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("claim did not enter HealthStore.ClaimRecoveryPermit")
	}

	// Disable so post-claim probeGate fails and request-owned ClearPermit runs.
	disResp, disBody := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_stale_clear/disable",
		bearer: tenantAKey,
	})
	if disResp.StatusCode != http.StatusOK {
		t.Fatalf("disable status = %d want 200 (body=%s)", disResp.StatusCode, disBody)
	}
	// Admin clear (empty Expected) still runs after swap inject and drops the
	// injected permit; claim then owns the slot; post-claim fenced clear sees
	// another swap-to-newer and must fail closed, leaving newer durable.
	close(release)

	probeRes := <-done
	if probeRes.status != http.StatusConflict {
		t.Fatalf("probe status = %d, want 409 fail-closed (body=%s)", probeRes.status, probeRes.body)
	}
	if harness.vault.validCalls.Load() != 0 {
		t.Fatalf("vault validate = %d, want 0", harness.vault.validCalls.Load())
	}
	if harness.probe.callCount.Load() != 0 {
		t.Fatalf("adapter probe = %d, want 0", harness.probe.callCount.Load())
	}
	stored := harness.storedAccount(t, managePrincipal(), "pa_stale_clear")
	if stored.RecoveryPermit != newer {
		t.Fatalf("recovery permit = %+v, want newer foreign permit preserved", stored.RecoveryPermit)
	}
}

// Missing health row on submit: epoch fails closed, AccountStore unchanged,
// replay not completed, just-written vault version revoked.
func TestSubmitCredentialMissingHealthFailsClosed(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.seedAccount("tenant_a", usableDraft("pa_submit_no_health", domain.AuthModeChatGPTCodexOAuth))
		h.health.epochErr = ports.ErrHealthNotFound
	})

	beforeReplay := harness.replay.completeCalls.Load()
	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts/pa_submit_no_health/credentials",
		bearer:  tenantAKey,
		idemKey: "idem-no-health",
		body:    submitBody(domain.CredentialClassOAuthTokenImport),
	})
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 dependency (body=%s)", response.StatusCode, payload)
	}
	body := decodeError(t, payload)
	if body["code"] != "dependency_unavailable" {
		t.Fatalf("code = %v, want dependency_unavailable", body["code"])
	}
	stored, err := harness.accounts.Visible(context.Background(), managePrincipal(), "pa_submit_no_health")
	if err != nil {
		t.Fatalf("Visible: %v", err)
	}
	if stored.Lifecycle != domain.LifecycleDraft {
		t.Fatalf("lifecycle = %v, want draft (AccountStore not advanced)", stored.Lifecycle)
	}
	if stored.Credential.Version != 0 {
		t.Fatalf("credential.version = %d, want 0", stored.Credential.Version)
	}
	if stored.Credential.LastAllocatedVersion != 1 {
		t.Fatalf("last_allocated_version = %d, want failed version 1 consumed", stored.Credential.LastAllocatedVersion)
	}
	if harness.replay.completeCalls.Load() != beforeReplay {
		t.Fatal("replay.Complete must not run when epoch fails")
	}
	if harness.vault.putCalls.Load() != 1 {
		t.Fatalf("vault put = %d, want 1 before fail-closed cleanup", harness.vault.putCalls.Load())
	}
	if !harness.vault.wasRevoked("pa_submit_no_health", 1) {
		t.Fatal("want vault revoke of just-written version 1")
	}

	// A fresh request after dependency recovery allocates v2; v1 is never reused.
	harness.health.epochErr = nil
	response, payload = harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts/pa_submit_no_health/credentials",
		bearer:  tenantAKey,
		idemKey: "idem-no-health-retry",
		body:    submitBody(domain.CredentialClassOAuthTokenImport),
	})
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("retry status = %d, want 202 (body=%s)", response.StatusCode, payload)
	}
	if intake := harness.vault.intake(); intake.Version != 2 {
		t.Fatalf("retry vault version = %d, want 2 after failed v1", intake.Version)
	}
}

// A final AccountStore failure after Vault.Put also consumes the reserved
// version. The lifecycle remains draft, the material is revoked, and a new
// idempotency key retries at the next version.
func TestSubmitCredentialAccountCommitFailureConsumesVersion(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.seedAccount("tenant_a", usableDraft("pa_submit_commit_fail", domain.AuthModeChatGPTCodexOAuth))
		h.accounts.updateFailAtCall.Store(2)
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts/pa_submit_commit_fail/credentials",
		bearer:  tenantAKey,
		idemKey: "idem-commit-fail",
		body:    submitBody(domain.CredentialClassOAuthTokenImport),
	})
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body=%s)", response.StatusCode, payload)
	}
	stored, err := harness.accounts.Visible(t.Context(), managePrincipal(), "pa_submit_commit_fail")
	if err != nil {
		t.Fatalf("Visible: %v", err)
	}
	if stored.Lifecycle != domain.LifecycleDraft || stored.Credential.Version != 0 {
		t.Fatalf("failed commit advanced account: lifecycle=%s version=%d", stored.Lifecycle, stored.Credential.Version)
	}
	if stored.Credential.LastAllocatedVersion != 1 {
		t.Fatalf("last_allocated_version = %d, want 1", stored.Credential.LastAllocatedVersion)
	}
	if !harness.vault.wasRevoked("pa_submit_commit_fail", 1) {
		t.Fatal("failed commit did not revoke version 1")
	}

	response, payload = harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts/pa_submit_commit_fail/credentials",
		bearer:  tenantAKey,
		idemKey: "idem-commit-fail-retry",
		body:    submitBody(domain.CredentialClassOAuthTokenImport),
	})
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("retry status = %d, want 202 (body=%s)", response.StatusCode, payload)
	}
	if intake := harness.vault.intake(); intake.Version != 2 {
		t.Fatalf("retry vault version = %d, want 2", intake.Version)
	}
}

// Multi-event ObserveCooldown: forced audit batch failure leaves zero
// health-transition events and no durable cooldown for the fresh scope.
func TestMultiScopeCooldownAuditBatchFailureIsAtomic(t *testing.T) {
	t.Parallel()

	// Direct HealthStore path under composed Runtime is covered by seed + public
	// probe that emits multi-scope observe. Use Memory-free public path: active
	// account with eligible claim on chat, probe image with rate signal while
	// holding chat permit after claim — ObserveCooldown batch (renew claimed +
	// create image). Fail audit on that mutation.
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_batch_audit", domain.AuthModeChatGPTCodexOAuth)
		account = account.WithScopedCooldown(
			domain.NewTimestamp(spineFixtureTime),
			domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpChat)},
			domain.HealthReasonProviderRateLimited,
			domain.NewTimestamp(spineFixtureTime),
		)
		h.seedAccount("tenant_a", account)
		// Hold claim so we can set failNext before ObserveCooldown after probe returns rate signal.
		h.health.claimEntered = entered
		h.health.claimRelease = release
		h.probe.outcome = ports.ProbeOutcome{
			Authenticated:     true,
			Signal:            ports.ProbeSignalRateLimited,
			SignalScope:       domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpImageGeneration)},
			RetryAfterSeconds: 30,
		}
	})

	type result struct {
		status int
		body   []byte
	}
	done := make(chan result, 1)
	go func() {
		resp, body := harness.do(t, requestSpec{
			method: http.MethodPost,
			path:   "/v1/provider-accounts/pa_batch_audit/probe",
			bearer: tenantAKey,
			body:   `{"scope":{"kind":"operation","operation":"chat"}}`,
		})
		done <- result{status: resp.StatusCode, body: body}
	}()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("claim did not enter")
	}
	// Fail the next health mutation batch (ObserveCooldown multi-scope).
	harness.audit.failNext.Store(1)
	beforeEvents := len(harness.audit.snapshot())
	close(release)

	res := <-done
	// Expect fail-closed (503) when multi-scope cooldown observe audit fails.
	if res.status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 on atomic audit batch failure (body=%s)", res.status, res.body)
	}
	var healthTransitions int
	for _, e := range harness.audit.snapshot()[beforeEvents:] {
		if e.Action == ports.AuditProviderHealthTransition {
			healthTransitions++
		}
	}
	if healthTransitions != 0 {
		t.Fatalf("partial health-transition audits visible = %d, want 0", healthTransitions)
	}
	stored := harness.storedAccount(t, managePrincipal(), "pa_batch_audit")
	// Image scope must not have been created (mutation aborted).
	for _, c := range stored.Health.Conditions {
		if c.Scope.Operation == string(domain.CapabilityOpImageGeneration) {
			t.Fatalf("image scope persisted despite failed batch audit: %+v", c)
		}
	}
}
