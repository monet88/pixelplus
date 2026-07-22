package contracttest_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// The direct credential submission material used across these tests. It is a
// writeOnly secret: it enters once over TLS and must never appear in any
// response, audit, telemetry, or request-log projection (connection lifecycle
// spec §4.4 rule 6, credential vault spec §3.3).
const submitMaterial = "tok_secret_abcdefgh"

// submitBody builds a frozen DirectCredentialSubmissionRequest body for the
// given credential class carrying submitMaterial.
func submitBody(class domain.CredentialClass) string {
	return `{"credential":{"credential_class":"` + string(class) + `","material":"` + submitMaterial + `"}}`
}

// providerForMode returns a provider consistent with the Auth Mode so a seeded
// account looks like one create would have produced. The store never validates
// this pairing, so it is cosmetic, but it keeps fixtures honest.
func providerForMode(mode domain.AuthMode) domain.Provider {
	switch mode {
	case domain.AuthModeGeminiWebCookie, domain.AuthModeGeminiAntigravityOAuth:
		return domain.ProviderGemini
	case domain.AuthModeGrokWebSSO, domain.AuthModeGrokXAIOAuth:
		return domain.ProviderGrok
	default:
		return domain.ProviderChatGPT
	}
}

// usableDraft builds a draft account whose owning Tenant has acknowledged the
// residual risk, so the Auth Mode gate passes and a first submission is
// accepted. It starts in `draft` with no stored credential version.
func usableDraft(id string, mode domain.AuthMode) domain.ProviderAccount {
	account := domain.NewDraftProviderAccount(
		domain.ProviderAccountID(id),
		providerForMode(mode),
		mode,
		"primary",
		domain.NewTimestamp(spineFixtureTime),
	)
	account.RiskAcknowledged = true
	return account
}

// probeableAccount builds an account that already stored its first credential
// version and sits in `pending_validation`, so a controlled probe is allowed.
func probeableAccount(id string, mode domain.AuthMode) domain.ProviderAccount {
	return usableDraft(id, mode).WithSubmittedCredential(domain.NewTimestamp(spineFixtureTime), domain.Timestamp{})
}

// decodeAccount unwraps the AccountOperationResponse envelope returned by the
// submit (202) and probe (200) operations.
func decodeAccount(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var envelope struct {
		Account map[string]any `json:"account"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("decode account operation response: %v (body=%s)", err, payload)
	}
	if envelope.Account == nil {
		t.Fatalf("account operation response missing account (body=%s)", payload)
	}
	return envelope.Account
}

// AC (submission accepted for the required class + valid lifecycle after every
// gate): a first oauth_token_import submission to a gated OAuth account with the
// risk acknowledged lands `pending_validation` with credential.version bumped to
// 1 and health unknown/initial_unprobed, and the material is forwarded to the
// Vault under the right binding exactly once.
func TestSubmitCredentialAcceptedLandsPendingValidation(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", usableDraft("pa_submit", domain.AuthModeChatGPTCodexOAuth))
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts/pa_submit/credentials",
		bearer:  tenantAKey,
		idemKey: "idem-submit-1",
		body:    submitBody(domain.CredentialClassOAuthTokenImport),
	})
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body=%s)", response.StatusCode, payload)
	}

	account := decodeAccount(t, payload)
	if account["lifecycle_state"] != "pending_validation" {
		t.Fatalf("lifecycle_state = %v, want pending_validation", account["lifecycle_state"])
	}
	credential, _ := account["credential"].(map[string]any)
	if version, _ := credential["version"].(float64); version != 1 {
		t.Fatalf("credential.version = %v, want 1", credential["version"])
	}
	health, _ := account["health"].(map[string]any)
	if health["summary_state"] != "unknown" {
		t.Fatalf("health.summary_state = %v, want unknown", health["summary_state"])
	}

	// The material was forwarded to the Vault exactly once under the account's
	// own Tenant/account/Auth Mode/version binding, and the account persisted.
	if calls := harness.vault.putCalls.Load(); calls != 1 {
		t.Fatalf("vault.Put ran %d times, want 1", calls)
	}
	if calls := harness.accounts.updateCalls.Load(); calls != 1 {
		t.Fatalf("account.Update ran %d times, want 1", calls)
	}
	intake := harness.vault.intake()
	if intake.Principal.TenantID != "tenant_a" {
		t.Fatalf("intake tenant = %q, want tenant_a", intake.Principal.TenantID)
	}
	if intake.AccountID != "pa_submit" {
		t.Fatalf("intake account = %q, want pa_submit", intake.AccountID)
	}
	if intake.AuthMode != domain.AuthModeChatGPTCodexOAuth {
		t.Fatalf("intake auth mode = %q, want chatgpt_codex_oauth", intake.AuthMode)
	}
	if intake.Class != domain.CredentialClassOAuthTokenImport {
		t.Fatalf("intake class = %q, want oauth_token_import", intake.Class)
	}
	if intake.Version != 1 {
		t.Fatalf("intake version = %d, want 1", intake.Version)
	}

	// The material never appears in the accepted response body.
	if strings.Contains(string(payload), submitMaterial) {
		t.Fatalf("submission response leaked credential material")
	}
}

// AC (credential class must match the Auth Mode; reject before Vault use): a
// web_session submission to an OAuth account is account_not_usable and never
// reaches the Vault.
func TestSubmitCredentialWrongClassRejectedBeforeVault(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", usableDraft("pa_wrongclass", domain.AuthModeChatGPTCodexOAuth))
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts/pa_wrongclass/credentials",
		bearer:  tenantAKey,
		idemKey: "idem-wrongclass-1",
		body:    submitBody(domain.CredentialClassWebSession),
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	body := decodeError(t, payload)
	if body["code"] != "account_not_usable" {
		t.Fatalf("code = %v, want account_not_usable", body["code"])
	}
	if body["status_class"] != "account_policy" {
		t.Fatalf("status_class = %v, want account_policy", body["status_class"])
	}
	if body["retryability"] != "not_retryable" {
		t.Fatalf("retryability = %v, want not_retryable", body["retryability"])
	}
	if calls := harness.vault.putCalls.Load(); calls != 0 {
		t.Fatalf("vault.Put ran %d times before class gate, want 0", calls)
	}
}

// AC (prohibited Auth Mode rejects before Vault/Adapter): a submission to a
// prohibited-mode account fails closed with auth_mode_unavailable and never
// touches the Vault.
func TestSubmitCredentialProhibitedModeUnavailable(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", usableDraft("pa_prohibited", domain.AuthModeGrokWebSSO))
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts/pa_prohibited/credentials",
		bearer:  tenantAKey,
		idemKey: "idem-prohibited-1",
		body:    submitBody(domain.CredentialClassWebSession),
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	body := decodeError(t, payload)
	if body["code"] != "auth_mode_unavailable" {
		t.Fatalf("code = %v, want auth_mode_unavailable", body["code"])
	}
	if calls := harness.vault.putCalls.Load(); calls != 0 {
		t.Fatalf("vault.Put ran %d times for prohibited mode, want 0", calls)
	}
}

// AC (gated Auth Mode without the required Tenant risk acknowledgement rejects
// before Vault use): a submission to a gated account whose Tenant has not
// acknowledged risk is account_not_usable with remediation ack_risk.
func TestSubmitCredentialGatedWithoutAckRejected(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		account := usableDraft("pa_noack", domain.AuthModeChatGPTCodexOAuth)
		account.RiskAcknowledged = false
		h.accounts.seed("tenant_a", account)
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts/pa_noack/credentials",
		bearer:  tenantAKey,
		idemKey: "idem-noack-1",
		body:    submitBody(domain.CredentialClassOAuthTokenImport),
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	body := decodeError(t, payload)
	if body["code"] != "account_not_usable" {
		t.Fatalf("code = %v, want account_not_usable", body["code"])
	}
	if body["remediation"] != "ack_risk" {
		t.Fatalf("remediation = %v, want ack_risk", body["remediation"])
	}
	if calls := harness.vault.putCalls.Load(); calls != 0 {
		t.Fatalf("vault.Put ran %d times without risk ack, want 0", calls)
	}
}

// AC (accounts.manage is required): a read-only key is forbidden from submitting
// a credential and the request never reaches the Vault.
func TestSubmitCredentialRequiresManageScope(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", usableDraft("pa_scope", domain.AuthModeChatGPTCodexOAuth))
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts/pa_scope/credentials",
		bearer:  readOnly,
		idemKey: "idem-scope-1",
		body:    submitBody(domain.CredentialClassOAuthTokenImport),
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", response.StatusCode, payload)
	}
	if body := decodeError(t, payload); body["code"] != "forbidden" {
		t.Fatalf("code = %v, want forbidden", body["code"])
	}
	if calls := harness.vault.putCalls.Load(); calls != 0 {
		t.Fatalf("vault.Put ran %d times for a forbidden scope, want 0", calls)
	}
}

// AC (required Idempotency-Key): a submission without an Idempotency-Key is a
// request-validation failure before ownership resolution or Vault use.
func TestSubmitCredentialRequiresIdempotencyKey(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", usableDraft("pa_noidem", domain.AuthModeChatGPTCodexOAuth))
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_noidem/credentials",
		bearer: tenantAKey,
		body:   submitBody(domain.CredentialClassOAuthTokenImport),
	})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", response.StatusCode, payload)
	}
	if body := decodeError(t, payload); body["code"] != "invalid_request" {
		t.Fatalf("code = %v, want invalid_request", body["code"])
	}
	if calls := harness.vault.putCalls.Load(); calls != 0 {
		t.Fatalf("vault.Put ran %d times for a malformed request, want 0", calls)
	}
}

// AC (foreign, unknown, and deleted identifiers are indistinguishable): a
// submission to an id the requesting Tenant cannot see is resource_not_found
// before any usability gate or Vault use.
func TestSubmitCredentialNonEnumerationIsIndistinguishable(t *testing.T) {
	t.Parallel()

	foreign := usableDraft("pa_foreign", domain.AuthModeChatGPTCodexOAuth)
	deleted := usableDraft("pa_deleted", domain.AuthModeChatGPTCodexOAuth)
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
			method:  http.MethodPost,
			path:    "/v1/provider-accounts/" + testCase.id + "/credentials",
			bearer:  tenantAKey,
			idemKey: "idem-missing-" + testCase.name,
			body:    submitBody(domain.CredentialClassOAuthTokenImport),
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
		if calls := harness.vault.putCalls.Load(); calls != 0 {
			t.Fatalf("%s: vault.Put ran before ownership resolved, want 0", testCase.name)
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

// AC (validation failure prevents the probe): when required validation fails,
// the Probe Adapter is never called and the account moves to reauth_required.
func TestProbeValidationFailurePreventsProbe(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", probeableAccount("pa_valfail", domain.AuthModeChatGPTCodexOAuth))
		h.vault.validResult.Valid = false
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_valfail/probe",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}

	account := decodeAccount(t, payload)
	if account["lifecycle_state"] != "reauth_required" {
		t.Fatalf("lifecycle_state = %v, want reauth_required", account["lifecycle_state"])
	}
	if calls := harness.vault.validCalls.Load(); calls != 1 {
		t.Fatalf("vault.Validate ran %d times, want 1", calls)
	}
	if calls := harness.probe.callCount.Load(); calls != 0 {
		t.Fatalf("probe ran %d times after a validation failure, want 0", calls)
	}
}

// AC (probe failure never activates): an auth-class probe failure moves the
// account to reauth_required and never to active.
func TestProbeAuthFailureNeverActivates(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", probeableAccount("pa_probefail", domain.AuthModeChatGPTCodexOAuth))
		h.probe.outcome.Authenticated = false
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_probefail/probe",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}

	account := decodeAccount(t, payload)
	if account["lifecycle_state"] != "reauth_required" {
		t.Fatalf("lifecycle_state = %v, want reauth_required", account["lifecycle_state"])
	}
	if calls := harness.probe.callCount.Load(); calls != 1 {
		t.Fatalf("probe ran %d times, want 1", calls)
	}
}

// AC (validation + probe activates only when every gate passes): a valid
// credential plus a successful probe is the sole transition into `active`, with
// health healthy/probe_succeeded for the current version.
func TestProbeSuccessActivatesAccount(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", probeableAccount("pa_active", domain.AuthModeChatGPTCodexOAuth))
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_active/probe",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}

	account := decodeAccount(t, payload)
	if account["lifecycle_state"] != "active" {
		t.Fatalf("lifecycle_state = %v, want active", account["lifecycle_state"])
	}
	credential, _ := account["credential"].(map[string]any)
	if version, _ := credential["version"].(float64); version != 1 {
		t.Fatalf("credential.version = %v, want 1", credential["version"])
	}
	health, _ := account["health"].(map[string]any)
	if health["summary_state"] != "healthy" {
		t.Fatalf("health.summary_state = %v, want healthy", health["summary_state"])
	}
	conditions, _ := health["conditions"].([]any)
	if len(conditions) == 0 {
		t.Fatalf("health.conditions empty, want at least one")
	}
	first, _ := conditions[0].(map[string]any)
	if first["reason"] != "probe_succeeded" {
		t.Fatalf("health condition reason = %v, want probe_succeeded", first["reason"])
	}
	if calls := harness.probe.callCount.Load(); calls != 1 {
		t.Fatalf("probe ran %d times, want 1", calls)
	}
}

// AC (a lifecycle state that cannot be probed rejects before Vault/Adapter): a
// probe on a `draft` account (no stored credential) is account_not_usable and
// never touches the Vault or the Adapter.
func TestProbeOnDraftRejectedBeforeAdapter(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", usableDraft("pa_draftprobe", domain.AuthModeChatGPTCodexOAuth))
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_draftprobe/probe",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	if body := decodeError(t, payload); body["code"] != "account_not_usable" {
		t.Fatalf("code = %v, want account_not_usable", body["code"])
	}
	if calls := harness.vault.validCalls.Load(); calls != 0 {
		t.Fatalf("vault.Validate ran %d times for a draft probe, want 0", calls)
	}
	if calls := harness.probe.callCount.Load(); calls != 0 {
		t.Fatalf("probe ran %d times for a draft probe, want 0", calls)
	}
}

// AC (probe requires accounts.manage): a read-only key cannot probe.
func TestProbeRequiresManageScope(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", probeableAccount("pa_probescope", domain.AuthModeChatGPTCodexOAuth))
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_probescope/probe",
		bearer: readOnly,
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", response.StatusCode, payload)
	}
	if calls := harness.probe.callCount.Load(); calls != 0 {
		t.Fatalf("probe ran %d times for a forbidden scope, want 0", calls)
	}
}

// AC (credential material is absent from every projection): after a full submit
// then probe activation, no response body, audit event, telemetry event, or
// request log carries the material or any secret handle vocabulary.
func TestCredentialMaterialAbsentFromProjections(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", usableDraft("pa_redact", domain.AuthModeChatGPTCodexOAuth))
	})

	_, submitPayload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts/pa_redact/credentials",
		bearer:  tenantAKey,
		idemKey: "idem-redact-1",
		body:    submitBody(domain.CredentialClassOAuthTokenImport),
	})
	_, probePayload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_redact/probe",
		bearer: tenantAKey,
	})

	if strings.Contains(string(submitPayload), submitMaterial) {
		t.Fatalf("submit response leaked credential material")
	}
	if strings.Contains(string(probePayload), submitMaterial) {
		t.Fatalf("probe response leaked credential material")
	}

	for _, event := range harness.audit.snapshot() {
		blob, _ := json.Marshal(event)
		if strings.Contains(string(blob), submitMaterial) {
			t.Fatalf("audit event leaked credential material")
		}
	}
	for _, event := range harness.telemetry.snapshot() {
		blob, _ := json.Marshal(event)
		if strings.Contains(string(blob), submitMaterial) {
			t.Fatalf("telemetry event leaked credential material")
		}
	}
	for _, log := range harness.reqLog.snapshot() {
		blob, _ := json.Marshal(log)
		if strings.Contains(string(blob), submitMaterial) {
			t.Fatalf("request log leaked credential material")
		}
	}
}
