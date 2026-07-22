package contracttest_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

const oauthExchangedMaterial = "oauth_exchanged_material_secret"

func oauthStartBody(purpose, flow string) string {
	return `{"purpose":"` + purpose + `","flow_preference":"` + flow + `"}`
}

func decodeOAuth(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("decode oauth authorization: %v (body=%s)", err, payload)
	}
	return body
}

// AC: OAuth start is account-scoped, risk-gated, idempotently claimed, and
// creates one safe server-owned authorization identity.
func TestOAuthStartConnectAcceptedCreatesPendingJourney(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", usableDraft("pa_oauth", domain.AuthModeChatGPTCodexOAuth))
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts/pa_oauth/oauth-authorizations",
		bearer:  tenantAKey,
		idemKey: "idem-oauth-start-1",
		body:    oauthStartBody("connect", "device"),
	})
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body=%s)", response.StatusCode, payload)
	}
	body := decodeOAuth(t, payload)
	if body["status"] != "authorization_pending" {
		t.Fatalf("status = %v, want authorization_pending", body["status"])
	}
	if body["purpose"] != "connect" {
		t.Fatalf("purpose = %v, want connect", body["purpose"])
	}
	if body["flow"] != "device" {
		t.Fatalf("flow = %v, want device", body["flow"])
	}
	if body["provider_account_id"] != "pa_oauth" {
		t.Fatalf("provider_account_id = %v, want pa_oauth", body["provider_account_id"])
	}
	if body["remediation"] != "complete_oauth" {
		t.Fatalf("remediation = %v, want complete_oauth", body["remediation"])
	}
	authID, _ := body["authorization_id"].(string)
	if !strings.HasPrefix(authID, "oauth_") {
		t.Fatalf("authorization_id = %q, want oauth_ prefix", authID)
	}
	if body["user_code"] == "" || body["verification_uri"] == "" {
		t.Fatalf("device start missing verification fields: %s", payload)
	}
	if strings.Contains(string(payload), oauthExchangedMaterial) || strings.Contains(string(payload), "device_code") || strings.Contains(string(payload), "pkce") {
		t.Fatalf("start response leaked exchange material: %s", payload)
	}
	if calls := harness.oauth.startCalls.Load(); calls != 1 {
		t.Fatalf("oauth.Start ran %d times, want 1", calls)
	}
	if calls := harness.vault.putCalls.Load(); calls != 0 {
		t.Fatalf("vault.Put ran %d times on start, want 0", calls)
	}
	// Account remains draft and non-usable; single-flight marker is private.
	account, err := harness.accounts.Visible(t.Context(), domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a", Scopes: domain.NewScopeSet(domain.ScopeAccountsManage)}, "pa_oauth")
	if err != nil {
		t.Fatalf("visible account: %v", err)
	}
	if account.Lifecycle != domain.LifecycleDraft {
		t.Fatalf("lifecycle after start = %s, want draft", account.Lifecycle)
	}
	if account.ActiveOAuthAuthorizationID != domain.OAuthAuthorizationID(authID) {
		t.Fatalf("active journey marker = %q, want %q", account.ActiveOAuthAuthorizationID, authID)
	}
}

// AC: Polling exposes only canonical journey status and never tokens/codes/PKCE.
func TestOAuthPollPendingSafeProjection(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", usableDraft("pa_oauth_poll", domain.AuthModeChatGPTCodexOAuth))
		h.oauth.nextStatus = domain.OAuthStatusAuthorizationPending
	})
	startResp, startPayload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts/pa_oauth_poll/oauth-authorizations",
		bearer:  tenantAKey,
		idemKey: "idem-oauth-poll-pending",
		body:    oauthStartBody("connect", "device"),
	})
	if startResp.StatusCode != http.StatusAccepted {
		t.Fatalf("start status = %d (body=%s)", startResp.StatusCode, startPayload)
	}
	authID, _ := decodeOAuth(t, startPayload)["authorization_id"].(string)

	response, payload := harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/provider-accounts/pa_oauth_poll/oauth-authorizations/" + authID,
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	body := decodeOAuth(t, payload)
	if body["status"] != "authorization_pending" {
		t.Fatalf("status = %v, want authorization_pending", body["status"])
	}
	if body["remediation"] != "complete_oauth" {
		t.Fatalf("remediation = %v, want complete_oauth", body["remediation"])
	}
	if strings.Contains(string(payload), oauthExchangedMaterial) {
		t.Fatalf("poll pending leaked material: %s", payload)
	}
	if calls := harness.vault.putCalls.Load(); calls != 0 {
		t.Fatalf("vault.Put ran on pending poll, want 0")
	}
}

// AC: Successful exchange stores credential through Vault, lands pending_validation,
// and does NOT activate until the required probe succeeds.
func TestOAuthPollSucceededStoresCredentialWithoutActivating(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", usableDraft("pa_oauth_ok", domain.AuthModeChatGPTCodexOAuth))
		h.oauth.nextStatus = domain.OAuthStatusSucceeded
		h.oauth.material = oauthExchangedMaterial
	})
	startResp, startPayload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts/pa_oauth_ok/oauth-authorizations",
		bearer:  tenantAKey,
		idemKey: "idem-oauth-ok-start",
		body:    oauthStartBody("connect", "device"),
	})
	if startResp.StatusCode != http.StatusAccepted {
		t.Fatalf("start status = %d (body=%s)", startResp.StatusCode, startPayload)
	}
	authID, _ := decodeOAuth(t, startPayload)["authorization_id"].(string)

	response, payload := harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/provider-accounts/pa_oauth_ok/oauth-authorizations/" + authID,
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	body := decodeOAuth(t, payload)
	if body["status"] != "succeeded" {
		t.Fatalf("status = %v, want succeeded", body["status"])
	}
	if body["remediation"] != "none" {
		t.Fatalf("remediation = %v, want none", body["remediation"])
	}
	if strings.Contains(string(payload), oauthExchangedMaterial) {
		t.Fatalf("succeeded poll leaked material: %s", payload)
	}
	if calls := harness.vault.putCalls.Load(); calls != 1 {
		t.Fatalf("vault.Put ran %d times, want 1", calls)
	}
	intake := harness.vault.intake()
	if intake.Material != oauthExchangedMaterial {
		t.Fatalf("vault material not forwarded")
	}
	if intake.Version != 1 || intake.AccountID != "pa_oauth_ok" {
		t.Fatalf("vault intake binding = %#v", intake)
	}

	account, err := harness.accounts.Visible(t.Context(), domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a", Scopes: domain.NewScopeSet(domain.ScopeAccountsManage)}, "pa_oauth_ok")
	if err != nil {
		t.Fatalf("visible: %v", err)
	}
	if account.Lifecycle != domain.LifecyclePendingValidation {
		t.Fatalf("lifecycle = %s, want pending_validation", account.Lifecycle)
	}
	if account.Credential.Version != 1 {
		t.Fatalf("credential.version = %d, want 1", account.Credential.Version)
	}
	if account.ActiveOAuthAuthorizationID != "" {
		t.Fatalf("journey marker still set after success: %q", account.ActiveOAuthAuthorizationID)
	}

	// Activation still requires the protected probe path.
	probeResp, probePayload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_oauth_ok/probe",
		bearer: tenantAKey,
	})
	if probeResp.StatusCode != http.StatusOK {
		t.Fatalf("probe status = %d (body=%s)", probeResp.StatusCode, probePayload)
	}
	activated := decodeAccount(t, probePayload)
	if activated["lifecycle_state"] != "active" {
		t.Fatalf("lifecycle after probe = %v, want active", activated["lifecycle_state"])
	}
}

// AC: Failed authorization stores no usable credential and restores draft.
func TestOAuthPollFailedRestoresDraftWithoutVault(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", usableDraft("pa_oauth_fail", domain.AuthModeChatGPTCodexOAuth))
		h.oauth.nextStatus = domain.OAuthStatusFailed
	})
	startResp, startPayload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts/pa_oauth_fail/oauth-authorizations",
		bearer:  tenantAKey,
		idemKey: "idem-oauth-fail-start",
		body:    oauthStartBody("connect", "device"),
	})
	authID, _ := decodeOAuth(t, startPayload)["authorization_id"].(string)
	if startResp.StatusCode != http.StatusAccepted {
		t.Fatalf("start status = %d (body=%s)", startResp.StatusCode, startPayload)
	}

	response, payload := harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/provider-accounts/pa_oauth_fail/oauth-authorizations/" + authID,
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	body := decodeOAuth(t, payload)
	if body["status"] != "failed" {
		t.Fatalf("status = %v, want failed", body["status"])
	}
	if body["remediation"] != "complete_oauth" {
		t.Fatalf("remediation = %v, want complete_oauth", body["remediation"])
	}
	if calls := harness.vault.putCalls.Load(); calls != 0 {
		t.Fatalf("vault.Put ran %d times on failed journey, want 0", calls)
	}
	account, err := harness.accounts.Visible(t.Context(), domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a", Scopes: domain.NewScopeSet(domain.ScopeAccountsManage)}, "pa_oauth_fail")
	if err != nil {
		t.Fatalf("visible: %v", err)
	}
	if account.Lifecycle != domain.LifecycleDraft {
		t.Fatalf("lifecycle = %s, want draft", account.Lifecycle)
	}
	if account.Credential.Version != 0 {
		t.Fatalf("credential.version = %d, want 0", account.Credential.Version)
	}
	if account.ActiveOAuthAuthorizationID != "" {
		t.Fatalf("journey marker still set after failure: %q", account.ActiveOAuthAuthorizationID)
	}
}

// AC: Concurrent second OAuth journey cannot overwrite/orphan the active journey.
func TestOAuthStartSingleFlightRejectsSecondJourney(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", usableDraft("pa_oauth_sf", domain.AuthModeChatGPTCodexOAuth))
	})
	first, firstPayload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts/pa_oauth_sf/oauth-authorizations",
		bearer:  tenantAKey,
		idemKey: "idem-oauth-sf-1",
		body:    oauthStartBody("connect", "device"),
	})
	if first.StatusCode != http.StatusAccepted {
		t.Fatalf("first start status = %d (body=%s)", first.StatusCode, firstPayload)
	}
	second, secondPayload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts/pa_oauth_sf/oauth-authorizations",
		bearer:  tenantAKey,
		idemKey: "idem-oauth-sf-2",
		body:    oauthStartBody("connect", "device"),
	})
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("second start status = %d, want 409 (body=%s)", second.StatusCode, secondPayload)
	}
	body := decodeError(t, secondPayload)
	if body["code"] != "account_not_usable" {
		t.Fatalf("code = %v, want account_not_usable", body["code"])
	}
	if body["remediation"] != "complete_oauth" {
		t.Fatalf("remediation = %v, want complete_oauth", body["remediation"])
	}
	if calls := harness.oauth.startCalls.Load(); calls != 1 {
		t.Fatalf("oauth.Start ran %d times, want 1", calls)
	}
}

// AC: Web mode / wrong purpose / gated-without-ack reject before OAuth adapter.
func TestOAuthStartGatesRejectBeforeAdapter(t *testing.T) {
	t.Parallel()

	run := func(t *testing.T, path string, seed func(*spineHarness), body, wantCode string, wantHTTP int) {
		t.Helper()
		harness := newSpineHarness(t, seed)
		response, payload := harness.do(t, requestSpec{
			method:  http.MethodPost,
			path:    path,
			bearer:  tenantAKey,
			idemKey: "idem-gate-" + wantCode + path,
			body:    body,
		})
		if response.StatusCode != wantHTTP {
			t.Fatalf("status = %d, want %d (body=%s)", response.StatusCode, wantHTTP, payload)
		}
		if body := decodeError(t, payload); body["code"] != wantCode {
			t.Fatalf("code = %v, want %s", body["code"], wantCode)
		}
		if calls := harness.oauth.startCalls.Load(); calls != 0 {
			t.Fatalf("oauth.Start ran %d times before gate, want 0", calls)
		}
		if calls := harness.vault.putCalls.Load(); calls != 0 {
			t.Fatalf("vault.Put ran before gate, want 0")
		}
	}

	run(t, "/v1/provider-accounts/pa_web/oauth-authorizations", func(h *spineHarness) {
		h.accounts.seed("tenant_a", usableDraft("pa_web", domain.AuthModeChatGPTWebAccess))
	}, oauthStartBody("connect", "device"), "invalid_request", http.StatusBadRequest)

	run(t, "/v1/provider-accounts/pa_pending/oauth-authorizations", func(h *spineHarness) {
		h.accounts.seed("tenant_a", probeableAccount("pa_pending", domain.AuthModeChatGPTCodexOAuth))
	}, oauthStartBody("connect", "device"), "account_not_usable", http.StatusConflict)

	run(t, "/v1/provider-accounts/pa_noack_oauth/oauth-authorizations", func(h *spineHarness) {
		account := usableDraft("pa_noack_oauth", domain.AuthModeChatGPTCodexOAuth)
		account.RiskAcknowledged = false
		h.accounts.seed("tenant_a", account)
	}, oauthStartBody("connect", "device"), "account_not_usable", http.StatusConflict)

	run(t, "/v1/provider-accounts/pa_bad_purpose/oauth-authorizations", func(h *spineHarness) {
		h.accounts.seed("tenant_a", usableDraft("pa_bad_purpose", domain.AuthModeChatGPTCodexOAuth))
	}, oauthStartBody("upgrade", "device"), "invalid_request", http.StatusBadRequest)
}

// AC: scope + non-enumeration before OAuth adapter / vault.
func TestOAuthStartScopeAndNonEnumeration(t *testing.T) {
	t.Parallel()

	// manage scope required
	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", usableDraft("pa_oauth_scope", domain.AuthModeChatGPTCodexOAuth))
	})
	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts/pa_oauth_scope/oauth-authorizations",
		bearer:  readOnly,
		idemKey: "idem-oauth-scope",
		body:    oauthStartBody("connect", "device"),
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", response.StatusCode, payload)
	}
	if calls := harness.oauth.startCalls.Load(); calls != 0 {
		t.Fatalf("oauth.Start ran for forbidden scope")
	}

	// foreign/unknown/deleted indistinguishable
	foreign := usableDraft("pa_oauth_foreign", domain.AuthModeChatGPTCodexOAuth)
	deleted := usableDraft("pa_oauth_deleted", domain.AuthModeChatGPTCodexOAuth)
	deleted.Lifecycle = domain.LifecycleDeleted
	seed := func(h *spineHarness) {
		h.accounts.seed("tenant_b", foreign)
		h.accounts.seed("tenant_a", deleted)
	}
	var bodies []string
	for _, id := range []string{"pa_oauth_foreign", "pa_missing_oauth", "pa_oauth_deleted"} {
		h := newSpineHarness(t, seed)
		resp, body := h.do(t, requestSpec{
			method:  http.MethodPost,
			path:    "/v1/provider-accounts/" + id + "/oauth-authorizations",
			bearer:  tenantAKey,
			idemKey: "idem-oauth-ne-" + id,
			body:    oauthStartBody("connect", "device"),
		})
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404 (body=%s)", id, resp.StatusCode, body)
		}
		errBody := decodeError(t, body)
		if errBody["code"] != "resource_not_found" {
			t.Fatalf("%s code = %v", id, errBody["code"])
		}
		if calls := h.oauth.startCalls.Load(); calls != 0 {
			t.Fatalf("%s oauth.Start ran before ownership", id)
		}
		delete(errBody, "request_id")
		normalized, _ := json.Marshal(errBody)
		bodies = append(bodies, string(normalized))
	}
	for i := 1; i < len(bodies); i++ {
		if bodies[i] != bodies[0] {
			t.Fatalf("non-enumeration outcomes differ:\n%s\n%s", bodies[0], bodies[i])
		}
	}
}

// AC: Concurrent direct credential replacement cannot overwrite an active OAuth
// journey. Direct submit is rejected before Vault use while the single-flight
// marker is set (management contract §4.3).
func TestDirectSubmitRejectedWhileOAuthJourneyInFlight(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", usableDraft("pa_oauth_replace", domain.AuthModeChatGPTCodexOAuth))
	})
	startResp, startPayload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts/pa_oauth_replace/oauth-authorizations",
		bearer:  tenantAKey,
		idemKey: "idem-oauth-replace-start",
		body:    oauthStartBody("connect", "device"),
	})
	if startResp.StatusCode != http.StatusAccepted {
		t.Fatalf("start status = %d (body=%s)", startResp.StatusCode, startPayload)
	}

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts/pa_oauth_replace/credentials",
		bearer:  tenantAKey,
		idemKey: "idem-oauth-replace-direct",
		body:    submitBody(domain.CredentialClassOAuthTokenImport),
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
	if calls := harness.vault.putCalls.Load(); calls != 0 {
		t.Fatalf("vault.Put ran %d times during blocked replacement, want 0", calls)
	}
}

// AC: Poll foreign/unknown authorization ids are non-enumerating resource_not_found
// before any Vault use.
func TestOAuthPollNonEnumeration(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", usableDraft("pa_oauth_ne_poll", domain.AuthModeChatGPTCodexOAuth))
		h.accounts.seed("tenant_b", usableDraft("pa_oauth_ne_foreign", domain.AuthModeChatGPTCodexOAuth))
	})
	// Start a journey on tenant_b so an id exists but is foreign to tenant_a.
	startResp, startPayload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts/pa_oauth_ne_foreign/oauth-authorizations",
		bearer:  tenantBKey,
		idemKey: "idem-oauth-ne-foreign-start",
		body:    oauthStartBody("connect", "device"),
	})
	if startResp.StatusCode != http.StatusAccepted {
		t.Fatalf("foreign start status = %d (body=%s)", startResp.StatusCode, startPayload)
	}
	foreignAuthID, _ := decodeOAuth(t, startPayload)["authorization_id"].(string)

	cases := []struct {
		name string
		path string
	}{
		{name: "unknown auth id on owned account", path: "/v1/provider-accounts/pa_oauth_ne_poll/oauth-authorizations/oauth_missing"},
		{name: "foreign account", path: "/v1/provider-accounts/pa_oauth_ne_foreign/oauth-authorizations/" + foreignAuthID},
		{name: "owned account wrong auth id", path: "/v1/provider-accounts/pa_oauth_ne_poll/oauth-authorizations/" + foreignAuthID},
	}
	var bodies []string
	for _, testCase := range cases {
		response, payload := harness.do(t, requestSpec{
			method: http.MethodGet,
			path:   testCase.path,
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
			t.Fatalf("%s: leaked resource_reference", testCase.name)
		}
		if calls := harness.vault.putCalls.Load(); calls != 0 {
			t.Fatalf("%s: vault.Put ran, want 0", testCase.name)
		}
		delete(body, "request_id")
		normalized, _ := json.Marshal(body)
		bodies = append(bodies, string(normalized))
	}
	for i := 1; i < len(bodies); i++ {
		if bodies[i] != bodies[0] {
			t.Fatalf("poll non-enumeration outcomes differ:\n%s\n%s", bodies[0], bodies[i])
		}
	}
}

// AC: Expired authorization stores no usable credential and restores draft.
func TestOAuthPollExpiredRestoresDraftWithoutVault(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", usableDraft("pa_oauth_exp", domain.AuthModeChatGPTCodexOAuth))
		// Keep adapter pending so expiry is enforced by the application clock gate.
		h.oauth.nextStatus = domain.OAuthStatusAuthorizationPending
	})
	startResp, startPayload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts/pa_oauth_exp/oauth-authorizations",
		bearer:  tenantAKey,
		idemKey: "idem-oauth-exp-start",
		body:    oauthStartBody("connect", "device"),
	})
	if startResp.StatusCode != http.StatusAccepted {
		t.Fatalf("start status = %d (body=%s)", startResp.StatusCode, startPayload)
	}
	authID, _ := decodeOAuth(t, startPayload)["authorization_id"].(string)

	// Force the stored journey past expiry before polling.
	harness.oauth.mu.Lock()
	if record, ok := harness.oauth.records[domain.OAuthAuthorizationID(authID)]; ok {
		record.authorization.ExpiresAt = domain.NewTimestamp(spineFixtureTime.Add(-time.Minute))
	}
	harness.oauth.mu.Unlock()

	response, payload := harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/provider-accounts/pa_oauth_exp/oauth-authorizations/" + authID,
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	body := decodeOAuth(t, payload)
	if body["status"] != "failed" {
		t.Fatalf("status = %v, want failed", body["status"])
	}
	if body["remediation"] != "complete_oauth" {
		t.Fatalf("remediation = %v, want complete_oauth", body["remediation"])
	}
	if calls := harness.vault.putCalls.Load(); calls != 0 {
		t.Fatalf("vault.Put ran %d times on expired journey, want 0", calls)
	}
	account, err := harness.accounts.Visible(t.Context(), domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a", Scopes: domain.NewScopeSet(domain.ScopeAccountsManage)}, "pa_oauth_exp")
	if err != nil {
		t.Fatalf("visible: %v", err)
	}
	if account.Lifecycle != domain.LifecycleDraft {
		t.Fatalf("lifecycle = %s, want draft", account.Lifecycle)
	}
	if account.ActiveOAuthAuthorizationID != "" {
		t.Fatalf("journey marker still set after expiry: %q", account.ActiveOAuthAuthorizationID)
	}
}
