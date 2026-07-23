package contracttest_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// activeAccount builds a healthy, active account for the given Auth Mode: a
// draft with risk acknowledged that stored version 1 and then validated + probe
// activated, so its health is healthy/probe_succeeded for version 1. Disable and
// delete tests start from this observable state.
func activeAccount(id string, mode domain.AuthMode) domain.ProviderAccount {
	account := probeableAccount(id, mode)
	account = account.WithValidatedCredential(domain.NewTimestamp(spineFixtureTime))
	return account.WithProbeActivated(domain.NewTimestamp(spineFixtureTime))
}

// AC (disable blocks new use without rewriting health or claiming a credential
// failure): disabling a healthy active account lands `disabled`, preserves the
// healthy/probe_succeeded evidence, and touches no Vault decrypt or Adapter.
func TestDisableBlocksUseWithoutRewritingHealth(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", activeAccount("pa_disable", domain.AuthModeChatGPTCodexOAuth))
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_disable/disable",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}

	account := decodeAccount(t, payload)
	if account["lifecycle_state"] != "disabled" {
		t.Fatalf("lifecycle_state = %v, want disabled", account["lifecycle_state"])
	}
	// Disable preserves the last truthful health evidence; it never invents a
	// health failure or claims the credential was rejected.
	health, _ := account["health"].(map[string]any)
	if health["summary_state"] != "healthy" {
		t.Fatalf("health.summary_state = %v, want healthy (disable must not rewrite health)", health["summary_state"])
	}
	conditions, _ := health["conditions"].([]any)
	if len(conditions) == 0 {
		t.Fatalf("health.conditions empty, want the preserved condition")
	}
	first, _ := conditions[0].(map[string]any)
	if first["reason"] != "probe_succeeded" {
		t.Fatalf("health reason = %v, want probe_succeeded (no false credential failure)", first["reason"])
	}
	// Disable has no vault decrypt purpose and calls no Adapter.
	if calls := harness.vault.validCalls.Load(); calls != 0 {
		t.Fatalf("vault.Validate ran %d times on disable, want 0", calls)
	}
	if calls := harness.probe.callCount.Load(); calls != 0 {
		t.Fatalf("probe ran %d times on disable, want 0", calls)
	}
}

// AC (disable does not apply to a pure draft): disabling a draft shell is
// rejected with a stable account_not_usable class and persists nothing.
func TestDisableRejectsDraft(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", usableDraft("pa_draftdisable", domain.AuthModeChatGPTCodexOAuth))
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_draftdisable/disable",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	if body := decodeError(t, payload); body["code"] != "account_not_usable" {
		t.Fatalf("code = %v, want account_not_usable", body["code"])
	}
	if calls := harness.accounts.updateCalls.Load(); calls != 0 {
		t.Fatalf("account.Update ran %d times for a draft disable, want 0", calls)
	}
}

// AC (disable is idempotent): disabling an already-disabled account stays
// disabled and succeeds.
func TestDisableIsIdempotent(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_disable_idem", domain.AuthModeChatGPTCodexOAuth)
		account.Lifecycle = domain.LifecycleDisabled
		h.accounts.seed("tenant_a", account)
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_disable_idem/disable",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	if account := decodeAccount(t, payload); account["lifecycle_state"] != "disabled" {
		t.Fatalf("lifecycle_state = %v, want disabled", account["lifecycle_state"])
	}
}

// AC (a revoked account cannot re-enter the probe ceremony through disable):
// disabling a `revoked` account is rejected. The §4.13 matrix marks the disable
// column `—` for the revoked row, and recovery is reauth with new material, not
// enable. Allowing revoked -> disabled -> enable -> pending_probe would let a
// revoked credential re-enter the activation ceremony, violating I-REVOKE-NONUSE.
func TestDisableRejectsRevoked(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_disable_revoked", domain.AuthModeChatGPTCodexOAuth)
		account.Lifecycle = domain.LifecycleRevoked
		h.accounts.seed("tenant_a", account)
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_disable_revoked/disable",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	if body := decodeError(t, payload); body["code"] != "account_not_usable" {
		t.Fatalf("code = %v, want account_not_usable", body["code"])
	}
	if calls := harness.accounts.updateCalls.Load(); calls != 0 {
		t.Fatalf("account.Update ran %d times on a revoked disable, want 0", calls)
	}
}

// AC (disable requires accounts.manage): a read-only key cannot disable.
func TestDisableRequiresManageScope(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", activeAccount("pa_disable_scope", domain.AuthModeChatGPTCodexOAuth))
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_disable_scope/disable",
		bearer: readOnly,
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", response.StatusCode, payload)
	}
	if body := decodeError(t, payload); body["code"] != "forbidden" {
		t.Fatalf("code = %v, want forbidden", body["code"])
	}
	if calls := harness.accounts.updateCalls.Load(); calls != 0 {
		t.Fatalf("account.Update ran %d times on scope denial, want 0", calls)
	}
}

// AC (foreign, unknown, and deleted identifiers cause zero protected mutation):
// disable on an id the requesting Tenant cannot see is an indistinguishable
// resource_not_found before any admission, Vault, or Update.
func TestDisableNonEnumerationIsIndistinguishable(t *testing.T) {
	t.Parallel()

	foreign := activeAccount("pa_foreign", domain.AuthModeChatGPTCodexOAuth)
	deleted := activeAccount("pa_deleted", domain.AuthModeChatGPTCodexOAuth)
	deleted.Lifecycle = domain.LifecycleDeleted
	seed := func(h *spineHarness) {
		h.accounts.seed("tenant_b", foreign)
		h.accounts.seed("tenant_a", deleted)
	}

	cases := []struct {
		name string
		id   string
	}{
		{name: "foreign", id: "pa_foreign"},
		{name: "unknown", id: "pa_missing"},
		{name: "deleted", id: "pa_deleted"},
	}

	var bodies []string
	for _, testCase := range cases {
		harness := newSpineHarness(t, seed)
		response, payload := harness.do(t, requestSpec{
			method: http.MethodPost,
			path:   "/v1/provider-accounts/" + testCase.id + "/disable",
			bearer: tenantAKey,
		})
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404 (body=%s)", testCase.name, response.StatusCode, payload)
		}
		body := decodeError(t, payload)
		if body["code"] != "resource_not_found" {
			t.Fatalf("%s: code = %v, want resource_not_found", testCase.name, body["code"])
		}
		if _, ok := body["resource_reference"]; ok {
			t.Fatalf("%s: non-enumeration leaked resource_reference", testCase.name)
		}
		if calls := harness.admission.admitCalls.Load(); calls != 0 {
			t.Fatalf("%s: admission ran %d times before non-enumeration, want 0", testCase.name, calls)
		}
		if calls := harness.accounts.updateCalls.Load(); calls != 0 {
			t.Fatalf("%s: account.Update ran %d times before ownership resolved, want 0", testCase.name, calls)
		}
		delete(body, "request_id")
		normalized, _ := json.Marshal(body)
		bodies = append(bodies, string(normalized))
	}
	for index := 1; index < len(bodies); index++ {
		if bodies[index] != bodies[0] {
			t.Fatalf("non-enumeration outcomes are distinguishable:\n %s\n %s", bodies[0], bodies[index])
		}
	}
}

// AC (enable returns a pending proof state and activates only after the current
// version passes the required probe): enable lands `pending_probe` without a
// probe, and a following current-version probe is the transition into `active`.
func TestEnableReturnsPendingProbeThenActivates(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_enable", domain.AuthModeChatGPTCodexOAuth)
		account.Lifecycle = domain.LifecycleDisabled
		h.accounts.seed("tenant_a", account)
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_enable/enable",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("enable status = %d, want 202 (body=%s)", response.StatusCode, payload)
	}
	account := decodeAccount(t, payload)
	if account["lifecycle_state"] != "pending_probe" {
		t.Fatalf("lifecycle_state = %v, want pending_probe (enable never predicts probe success)", account["lifecycle_state"])
	}
	// Enable schedules the probe path only; it runs no probe and decrypts nothing.
	if calls := harness.vault.validCalls.Load(); calls != 0 {
		t.Fatalf("vault.Validate ran %d times on enable, want 0", calls)
	}
	if calls := harness.probe.callCount.Load(); calls != 0 {
		t.Fatalf("probe ran %d times on enable, want 0", calls)
	}

	// The current-version probe is the sole transition back into active.
	response, payload = harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_enable/probe",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("probe status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	activated := decodeAccount(t, payload)
	if activated["lifecycle_state"] != "active" {
		t.Fatalf("lifecycle_state = %v, want active after enable probe", activated["lifecycle_state"])
	}
	if calls := harness.probe.callCount.Load(); calls != 1 {
		t.Fatalf("probe ran %d times, want exactly 1 on enable path", calls)
	}
}

// AC (enable auth-class probe failure never activates): after enable, a probe
// that fails authentication lands `reauth_required`, never a half-enabled active.
func TestEnableProbeAuthFailureLandsReauthRequired(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_enable_fail", domain.AuthModeChatGPTCodexOAuth)
		account.Lifecycle = domain.LifecycleDisabled
		h.accounts.seed("tenant_a", account)
		h.probe.outcome.Authenticated = false
	})

	response, _ := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_enable_fail/enable",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("enable status = %d, want 202", response.StatusCode)
	}
	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_enable_fail/probe",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("probe status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	if account := decodeAccount(t, payload); account["lifecycle_state"] != "reauth_required" {
		t.Fatalf("lifecycle_state = %v, want reauth_required", account["lifecycle_state"])
	}
}

// AC (enable is rejected for a non-disabled account): enabling an active account
// is account_not_usable and mutates nothing.
func TestEnableRejectsNonDisabled(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", activeAccount("pa_enable_active", domain.AuthModeChatGPTCodexOAuth))
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_enable_active/enable",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	if body := decodeError(t, payload); body["code"] != "account_not_usable" {
		t.Fatalf("code = %v, want account_not_usable", body["code"])
	}
	if calls := harness.accounts.updateCalls.Load(); calls != 0 {
		t.Fatalf("account.Update ran %d times enabling an active account, want 0", calls)
	}
}

// AC (enable rejects while a replacement journey is in flight): a disabled
// account carrying a pending replacement version cannot be enabled, so the
// administrative enable never races or overwrites the replacement.
func TestEnableRejectedWhileReplacementInFlight(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_enable_pending", domain.AuthModeChatGPTCodexOAuth)
		account.Lifecycle = domain.LifecycleDisabled
		account.Credential.LastAllocatedVersion = 2
		account.PendingCredentialVersion = 2
		account.PendingOrigin = domain.LifecycleDisabled
		h.accounts.seed("tenant_a", account)
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_enable_pending/enable",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	if body := decodeError(t, payload); body["code"] != "account_not_usable" {
		t.Fatalf("code = %v, want account_not_usable", body["code"])
	}
	if calls := harness.accounts.updateCalls.Load(); calls != 0 {
		t.Fatalf("account.Update ran %d times enabling over a pending replacement, want 0", calls)
	}
}

// AC (disable intent wins over concurrent replacement completion): after disable
// rewrites a pending replacement's origin to `disabled`, a following pending-
// version probe cuts over the credential version but the account stays
// `disabled` and non-usable. Probe success cannot reactivate a disabled account.
func TestDisableIntentWinsOverReplacementProbeCutover(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_disable_wins", domain.AuthModeChatGPTCodexOAuth)
		// A replacement version is staged and pending probe (active origin).
		account.Credential.LastAllocatedVersion = 2
		account.PendingCredentialVersion = 2
		account.PendingOrigin = domain.LifecycleActive
		h.accounts.seed("tenant_a", account)
	})

	// Disable while the replacement is pending: intent must become sticky.
	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_disable_wins/disable",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("disable status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	if account := decodeAccount(t, payload); account["lifecycle_state"] != "disabled" {
		t.Fatalf("lifecycle_state = %v, want disabled", account["lifecycle_state"])
	}

	// The pending-version probe would promote the replacement; because disable
	// rewrote the origin, cutover keeps the account disabled and non-usable.
	response, payload = harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_disable_wins/probe",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("probe status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	account := decodeAccount(t, payload)
	if account["lifecycle_state"] != "disabled" {
		t.Fatalf("lifecycle_state = %v, want disabled (probe success must not reactivate)", account["lifecycle_state"])
	}
	credential, _ := account["credential"].(map[string]any)
	if version, _ := credential["version"].(float64); version != 2 {
		t.Fatalf("credential.version = %v, want 2 (cutover happened but stayed disabled)", credential["version"])
	}
}

// AC (disable intent wins over a concurrent validation completion): an account
// whose replacement is staged but not yet validated is disabled, then the
// pending-version probe (which validates then promotes) must still land
// `disabled` rather than active, proving disable wins over the validation that
// completes after the disable (management contract §4.6).
func TestDisableIntentWinsOverPendingValidation(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_disable_wins_val", domain.AuthModeChatGPTCodexOAuth)
		// A replacement version is staged in pending_validation (active origin);
		// its required validation has not run yet.
		account.Lifecycle = domain.LifecyclePendingValidation
		account.Credential.LastAllocatedVersion = 2
		account.PendingCredentialVersion = 2
		account.PendingOrigin = domain.LifecycleActive
		h.accounts.seed("tenant_a", account)
	})

	// Disable while validation is still pending: intent must become sticky by
	// rewriting the pending origin to disabled.
	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_disable_wins_val/disable",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("disable status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	if account := decodeAccount(t, payload); account["lifecycle_state"] != "disabled" {
		t.Fatalf("lifecycle_state = %v, want disabled", account["lifecycle_state"])
	}

	// The pending-version probe validates then promotes the replacement; because
	// disable rewrote the origin, the completion lands disabled, not active.
	response, payload = harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_disable_wins_val/probe",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("probe status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	account := decodeAccount(t, payload)
	if account["lifecycle_state"] != "disabled" {
		t.Fatalf("lifecycle_state = %v, want disabled (validation completion must not reactivate)", account["lifecycle_state"])
	}
}

// AC (delete revokes every current and pending credential version before
// removing use authority): delete revokes both versions, returns 204, and the
// id then behaves as not-found for ordinary reads.
func TestDeleteRevokesAllVersionsThenNotFound(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_delete", domain.AuthModeChatGPTCodexOAuth)
		// A replacement version is also staged, so delete must revoke both.
		account.Credential.LastAllocatedVersion = 2
		account.PendingCredentialVersion = 2
		account.PendingOrigin = domain.LifecycleActive
		h.accounts.seed("tenant_a", account)
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodDelete,
		path:   "/v1/provider-accounts/pa_delete",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body=%s)", response.StatusCode, payload)
	}
	if len(payload) != 0 {
		t.Fatalf("delete 204 returned a body: %s", payload)
	}

	// Every stored current and pending version lost use authority.
	if !harness.vault.wasRevoked("pa_delete", 1) {
		t.Fatal("current credential version 1 was not revoked before delete")
	}
	if !harness.vault.wasRevoked("pa_delete", 2) {
		t.Fatal("pending credential version 2 was not revoked before delete")
	}

	// Revoke happens before the durable deleted transition (deletion ordering:
	// revoke credential authority before removing the record).
	events := harness.log.snapshot()
	revokeIndex, updateIndex := -1, -1
	for index, event := range events {
		if event == "vault.revoke" && revokeIndex == -1 {
			revokeIndex = index
		}
		if event == "account.update" {
			updateIndex = index
		}
	}
	if revokeIndex == -1 || updateIndex == -1 || revokeIndex > updateIndex {
		t.Fatalf("revoke must precede the deleted persist; events = %v", events)
	}

	// The deleted id is not-found for ordinary reads.
	response, payload = harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/provider-accounts/pa_delete",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete status = %d, want 404 (body=%s)", response.StatusCode, payload)
	}
	if body := decodeError(t, payload); body["code"] != "resource_not_found" {
		t.Fatalf("code = %v, want resource_not_found", body["code"])
	}
}

// AC (retention hold may keep evidence but cannot restore retrieval, decrypt, or
// execution): after delete, the internal tombstone remains in the store, yet the
// account is neither visible to ordinary reads nor probeable (zero Vault decrypt).
func TestDeletedAccountEvidenceIsNotRestorable(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", activeAccount("pa_delete_hold", domain.AuthModeChatGPTCodexOAuth))
	})

	response, _ := harness.do(t, requestSpec{
		method: http.MethodDelete,
		path:   "/v1/provider-accounts/pa_delete_hold",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", response.StatusCode)
	}

	// A retention tombstone remains internally (audit evidence) but ordinary
	// visibility is gone: Visible resolves to the non-enumerating failure.
	if _, err := harness.accounts.Visible(t.Context(), managePrincipal(), "pa_delete_hold"); err == nil {
		t.Fatal("deleted account is still visible; retention evidence must not restore retrieval")
	}

	// A probe on the deleted id decrypts nothing and cannot execute.
	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_delete_hold/probe",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("probe after delete status = %d, want 404 (body=%s)", response.StatusCode, payload)
	}
	if calls := harness.vault.validCalls.Load(); calls != 0 {
		t.Fatalf("vault.Validate ran %d times against a deleted account, want 0", calls)
	}
	if calls := harness.probe.callCount.Load(); calls != 0 {
		t.Fatalf("probe ran %d times against a deleted account, want 0", calls)
	}
}

// AC (foreign, unknown, and deleted identifiers cause zero Vault or mutation):
// delete on an id the requesting Tenant cannot see is an indistinguishable
// resource_not_found with zero revoke and zero Update.
func TestDeleteNonEnumerationIsIndistinguishable(t *testing.T) {
	t.Parallel()

	foreign := activeAccount("pa_foreign", domain.AuthModeChatGPTCodexOAuth)
	deleted := activeAccount("pa_deleted", domain.AuthModeChatGPTCodexOAuth)
	deleted.Lifecycle = domain.LifecycleDeleted
	seed := func(h *spineHarness) {
		h.accounts.seed("tenant_b", foreign)
		h.accounts.seed("tenant_a", deleted)
	}

	cases := []struct {
		name string
		id   string
	}{
		{name: "foreign", id: "pa_foreign"},
		{name: "unknown", id: "pa_missing"},
		{name: "deleted", id: "pa_deleted"},
	}

	var bodies []string
	for _, testCase := range cases {
		harness := newSpineHarness(t, seed)
		response, payload := harness.do(t, requestSpec{
			method: http.MethodDelete,
			path:   "/v1/provider-accounts/" + testCase.id,
			bearer: tenantAKey,
		})
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404 (body=%s)", testCase.name, response.StatusCode, payload)
		}
		body := decodeError(t, payload)
		if body["code"] != "resource_not_found" {
			t.Fatalf("%s: code = %v, want resource_not_found", testCase.name, body["code"])
		}
		if _, ok := body["resource_reference"]; ok {
			t.Fatalf("%s: non-enumeration leaked resource_reference", testCase.name)
		}
		for _, event := range harness.log.snapshot() {
			if event == "vault.revoke" {
				t.Fatalf("%s: vault.Revoke ran before ownership resolved", testCase.name)
			}
		}
		if calls := harness.accounts.updateCalls.Load(); calls != 0 {
			t.Fatalf("%s: account.Update ran %d times before ownership resolved, want 0", testCase.name, calls)
		}
		delete(body, "request_id")
		normalized, _ := json.Marshal(body)
		bodies = append(bodies, string(normalized))
	}
	for index := 1; index < len(bodies); index++ {
		if bodies[index] != bodies[0] {
			t.Fatalf("non-enumeration outcomes are distinguishable:\n %s\n %s", bodies[0], bodies[index])
		}
	}
}

// AC (delete requires accounts.manage): a read-only key cannot delete and the
// request reaches no Vault revoke or Update.
func TestDeleteRequiresManageScope(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", activeAccount("pa_delete_scope", domain.AuthModeChatGPTCodexOAuth))
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodDelete,
		path:   "/v1/provider-accounts/pa_delete_scope",
		bearer: readOnly,
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", response.StatusCode, payload)
	}
	if body := decodeError(t, payload); body["code"] != "forbidden" {
		t.Fatalf("code = %v, want forbidden", body["code"])
	}
	for _, event := range harness.log.snapshot() {
		if event == "vault.revoke" {
			t.Fatal("vault.Revoke ran on a forbidden delete")
		}
	}
	if calls := harness.accounts.updateCalls.Load(); calls != 0 {
		t.Fatalf("account.Update ran %d times on a forbidden delete, want 0", calls)
	}
}

// AC (disable intent wins under a concurrent probe that loaded a pre-disable
// snapshot): a probe writer that still carries PendingOrigin=active after disable
// rewrote origin must not reactivate when it commits. The probe path re-reads
// sticky origin at activation so cutover lands disabled.
func TestDisableIntentWinsOverStaleProbeSnapshot(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_disable_stale_probe", domain.AuthModeChatGPTCodexOAuth)
		account.Credential.LastAllocatedVersion = 2
		account.PendingCredentialVersion = 2
		account.PendingOrigin = domain.LifecycleActive
		h.accounts.seed("tenant_a", account)
	})

	// Capture the pre-disable snapshot a concurrent probe would have loaded.
	stale, err := harness.accounts.Visible(t.Context(), managePrincipal(), "pa_disable_stale_probe")
	if err != nil {
		t.Fatalf("visible pre-disable: %v", err)
	}
	if stale.PendingOrigin != domain.LifecycleActive {
		t.Fatalf("pre-disable PendingOrigin = %q, want active", stale.PendingOrigin)
	}

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_disable_stale_probe/disable",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("disable status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}

	// Stale full-record activation that ignores the re-read would write active.
	// The real probe path re-reads and honors PendingOrigin=disabled.
	response, payload = harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_disable_stale_probe/probe",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("probe status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	account := decodeAccount(t, payload)
	if account["lifecycle_state"] != "disabled" {
		t.Fatalf("lifecycle_state = %v, want disabled after stale-snapshot probe race", account["lifecycle_state"])
	}

	// A blind full replace from the pre-disable snapshot must lose the pending fence
	// after cutover (pending cleared) rather than roll the row back to active/v1.
	current, err := harness.accounts.Visible(t.Context(), managePrincipal(), "pa_disable_stale_probe")
	if err != nil {
		t.Fatalf("visible post-probe: %v", err)
	}
	staleActive := stale.WithReplacementProbeActivated(domain.NewTimestamp(spineFixtureTime))
	_, err = harness.accounts.Update(t.Context(), ports.AccountUpdate{
		Principal:             managePrincipal(),
		Account:               staleActive,
		RequirePendingVersion: 2,
	})
	if !errors.Is(err, ports.ErrAccountUpdateConflict) {
		t.Fatalf("stale activation error = %v, want ErrAccountUpdateConflict", err)
	}
	after, err := harness.accounts.Visible(t.Context(), managePrincipal(), "pa_disable_stale_probe")
	if err != nil {
		t.Fatalf("visible after stale write: %v", err)
	}
	if after.Lifecycle != domain.LifecycleDisabled {
		t.Fatalf("stale writer rewrote lifecycle = %s, want disabled", after.Lifecycle)
	}
	if after.Credential.Version != current.Credential.Version {
		t.Fatalf("stale writer rewrote version = %d, want %d", after.Credential.Version, current.Credential.Version)
	}
}

// AC (enable is rejected while a single-flight OAuth journey owns the window): a
// disabled account with ActiveOAuthAuthorizationID set cannot be enabled; the
// write is fenced so it cannot clear the marker either.
func TestEnableRejectedWhileOAuthInFlight(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_enable_oauth", domain.AuthModeChatGPTCodexOAuth)
		account.Lifecycle = domain.LifecycleDisabled
		account.ActiveOAuthAuthorizationID = "oa_inflight_enable"
		h.accounts.seed("tenant_a", account)
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_enable_oauth/enable",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	body := decodeError(t, payload)
	if body["code"] != "account_not_usable" {
		t.Fatalf("code = %v, want account_not_usable", body["code"])
	}
	if body["remediation"] != "complete_oauth" {
		t.Fatalf("remediation = %v, want complete_oauth", body["remediation"])
	}
	if calls := harness.accounts.updateCalls.Load(); calls != 0 {
		t.Fatalf("account.Update ran %d times enabling over OAuth, want 0", calls)
	}

	// Durable OAuth marker is preserved (enable must not orphan the journey).
	current, err := harness.accounts.Visible(t.Context(), managePrincipal(), "pa_enable_oauth")
	if err != nil {
		t.Fatalf("visible: %v", err)
	}
	if current.ActiveOAuthAuthorizationID != "oa_inflight_enable" {
		t.Fatalf("OAuth marker = %q, want preserved", current.ActiveOAuthAuthorizationID)
	}
	if current.Lifecycle != domain.LifecycleDisabled {
		t.Fatalf("lifecycle = %s, want disabled", current.Lifecycle)
	}
}

// AC (enable requires accounts.manage and is non-enumerating for foreign ids).
func TestEnableRequiresManageScopeAndNonEnumeration(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_enable_scope", domain.AuthModeChatGPTCodexOAuth)
		account.Lifecycle = domain.LifecycleDisabled
		h.accounts.seed("tenant_a", account)
		h.accounts.seed("tenant_b", activeAccount("pa_enable_foreign", domain.AuthModeChatGPTCodexOAuth))
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_enable_scope/enable",
		bearer: readOnly,
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("scope status = %d, want 403 (body=%s)", response.StatusCode, payload)
	}
	if body := decodeError(t, payload); body["code"] != "forbidden" {
		t.Fatalf("scope code = %v, want forbidden", body["code"])
	}

	response, payload = harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_enable_foreign/enable",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign status = %d, want 404 (body=%s)", response.StatusCode, payload)
	}
	if body := decodeError(t, payload); body["code"] != "resource_not_found" {
		t.Fatalf("foreign code = %v, want resource_not_found", body["code"])
	}
	if calls := harness.accounts.updateCalls.Load(); calls != 0 {
		t.Fatalf("account.Update ran %d times on gated enable, want 0", calls)
	}
}

// Enable's empty-OAuth fence rejects a concurrent marker claim: a late enable
// full replace cannot clear ActiveOAuthAuthorizationID once OAuth claimed it.
func TestEnableEmptyOAuthFenceRejectsConcurrentMarkerClaim(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_enable_fence", domain.AuthModeChatGPTCodexOAuth)
		account.Lifecycle = domain.LifecycleDisabled
		h.accounts.seed("tenant_a", account)
	})

	// Simulate OAuth claiming the single-flight marker after enable's soft gate.
	claimed, err := harness.accounts.Visible(t.Context(), managePrincipal(), "pa_enable_fence")
	if err != nil {
		t.Fatalf("visible: %v", err)
	}
	claimed.ActiveOAuthAuthorizationID = "oa_claimed_before_enable_write"
	if _, err := harness.accounts.Update(t.Context(), ports.AccountUpdate{
		Principal:               managePrincipal(),
		Account:                 claimed,
		RequireEmptyOAuthMarker: true,
	}); err != nil {
		t.Fatalf("oauth claim: %v", err)
	}

	// A stale enable writer (empty marker snapshot) must conflict, not wipe the claim.
	stale := claimed
	stale.ActiveOAuthAuthorizationID = ""
	stale = stale.WithEnableProbePending(domain.NewTimestamp(spineFixtureTime))
	_, err = harness.accounts.Update(t.Context(), ports.AccountUpdate{
		Principal:                  managePrincipal(),
		Account:                    stale,
		RequireEmptyOAuthMarker:    true,
		RequireEmptyPendingVersion: true,
	})
	if !errors.Is(err, ports.ErrAccountUpdateConflict) {
		t.Fatalf("stale enable error = %v, want ErrAccountUpdateConflict", err)
	}
	current, err := harness.accounts.Visible(t.Context(), managePrincipal(), "pa_enable_fence")
	if err != nil {
		t.Fatalf("visible after conflict: %v", err)
	}
	if current.ActiveOAuthAuthorizationID != "oa_claimed_before_enable_write" {
		t.Fatalf("marker after stale enable = %q, want preserved claim", current.ActiveOAuthAuthorizationID)
	}
	if current.Lifecycle != domain.LifecycleDisabled {
		t.Fatalf("lifecycle after stale enable = %s, want disabled", current.Lifecycle)
	}
}

// AC (delete fail-closed when vault revoke fails): a revoke dependency error
// must not mark the account deleted; non-use is preserved for conservative retry.
func TestDeleteRevokeFailureDoesNotMarkDeleted(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", activeAccount("pa_delete_revoke_fail", domain.AuthModeChatGPTCodexOAuth))
		h.vault.revokeErr = ports.ErrDependencyUnavailable
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodDelete,
		path:   "/v1/provider-accounts/pa_delete_revoke_fail",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body=%s)", response.StatusCode, payload)
	}
	if body := decodeError(t, payload); body["code"] != "dependency_unavailable" {
		t.Fatalf("code = %v, want dependency_unavailable", body["code"])
	}
	if calls := harness.accounts.updateCalls.Load(); calls != 0 {
		t.Fatalf("account.Update ran %d times after revoke failure, want 0", calls)
	}
	// Account remains visible and non-deleted for retry.
	if _, err := harness.accounts.Visible(t.Context(), managePrincipal(), "pa_delete_revoke_fail"); err != nil {
		t.Fatalf("account not visible after revoke failure: %v", err)
	}
}

// Concurrent delete loser after revoke is product-idempotent: when Update sees
// ErrAccountNotVisible (peer already deleted the row), the response is still 204
// rather than internal_error, because credentials already lost use authority.
func TestDeleteConcurrentLoserIsIdempotent(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", activeAccount("pa_delete_race", domain.AuthModeChatGPTCodexOAuth))
		// Force the deleted transition write to observe "already gone" after revoke,
		// which is the concurrent-loser shape (Visible passed, Update lost the race).
		h.accounts.updateErr = ports.ErrAccountNotVisible
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodDelete,
		path:   "/v1/provider-accounts/pa_delete_race",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 concurrent-loser idempotent (body=%s)", response.StatusCode, payload)
	}
	if len(payload) != 0 {
		t.Fatalf("delete 204 returned a body: %s", payload)
	}
	// Revoke still ran before the lost Update (fail-closed non-use ordering).
	if !harness.vault.wasRevoked("pa_delete_race", 1) {
		t.Fatal("current credential version 1 was not revoked before the lost delete update")
	}
}

// Enable health evidence is self-describing: pending_probe carries
// unknown/initial_unprobed for the current credential version.
func TestEnableHealthCarriesInitialUnprobedCondition(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_enable_health", domain.AuthModeChatGPTCodexOAuth)
		account.Lifecycle = domain.LifecycleDisabled
		h.accounts.seed("tenant_a", account)
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_enable_health/enable",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body=%s)", response.StatusCode, payload)
	}
	account := decodeAccount(t, payload)
	health, _ := account["health"].(map[string]any)
	if health["summary_state"] != "unknown" {
		t.Fatalf("health.summary_state = %v, want unknown", health["summary_state"])
	}
	conditions, _ := health["conditions"].([]any)
	if len(conditions) != 1 {
		t.Fatalf("health.conditions len = %d, want 1 initial_unprobed", len(conditions))
	}
	first, _ := conditions[0].(map[string]any)
	if first["reason"] != "initial_unprobed" {
		t.Fatalf("health reason = %v, want initial_unprobed", first["reason"])
	}
}
