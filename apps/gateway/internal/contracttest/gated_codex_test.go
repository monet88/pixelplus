package contracttest_test

import (
	"net/http"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/contracttest"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// This file proves #62 (T19, the gated ChatGPT Codex OAuth Adapter) AC1 through
// the public HTTP seam over real production composition. Every test drives the
// composed handler and asserts on controlled-port counters, never on Adapter
// internals (issue #62 public proof seam).
//
// The story's central claim is a DIFFERENCE between two compositions: a gated
// mode rejected without the operator feature flag vs. admitted when the operator
// opts in. The spineHarness defaults gatedAuthModes to every gated mode (matching
// its standard Codex fixture accounts), so the refusal tests override it to empty.

// newGatedRefusalHarness composes a deployment whose operator did NOT enable any
// gated mode — an ordinary production deployment where every gated Auth Mode
// stays fail-closed (decision 0014, §5.2).
func newGatedRefusalHarness(t *testing.T, configure func(*spineHarness)) *spineHarness {
	t.Helper()
	return newSpineHarness(t, func(h *spineHarness) {
		h.gatedAuthModes = nil
		if configure != nil {
			configure(h)
		}
	})
}

// newGatedHarness composes a deployment whose operator deliberately enabled the
// gated ChatGPT Codex OAuth mode.
func newGatedHarness(t *testing.T, configure func(*spineHarness)) *spineHarness {
	t.Helper()
	return newSpineHarness(t, func(h *spineHarness) {
		h.gatedAuthModes = []domain.AuthMode{domain.AuthModeChatGPTCodexOAuth}
		if configure != nil {
			configure(h)
		}
	})
}

// AC1 (operator flag, storage): a Tenant cannot even create a gated Codex OAuth
// account in a deployment whose operator did not enable the mode. The create
// gate refuses before any credential or Provider state.
func TestGatedCodexCreateRefusedWithoutOperatorFlag(t *testing.T) {
	t.Parallel()

	harness := newGatedRefusalHarness(t, nil)
	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts",
		bearer:  tenantAKey,
		idemKey: "idem-codex-create-refused",
		body:    validCreateBody,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	if code := decodeError(t, payload)["code"]; code != "auth_mode_unavailable" {
		t.Fatalf("code = %v, want auth_mode_unavailable", code)
	}
}

// AC1 control (operator flag on): with the mode enabled by the operator, the
// same create is admitted and yields an ordinary unprobed draft (enablement is
// not activation).
func TestGatedCodexCreateAllowedWhenOperatorEnablesTheMode(t *testing.T) {
	t.Parallel()

	harness := newGatedHarness(t, nil)
	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts",
		bearer:  tenantAKey,
		idemKey: "idem-codex-create-allowed",
		body:    validCreateBody,
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", response.StatusCode, payload)
	}
	account := decodeAccount(t, payload)
	if account["auth_mode"] != "chatgpt_codex_oauth" {
		t.Fatalf("auth_mode = %v, want chatgpt_codex_oauth", account["auth_mode"])
	}
	if account["lifecycle_state"] != "draft" {
		t.Fatalf("lifecycle_state = %v, want draft", account["lifecycle_state"])
	}
}

// AC1 (operator flag, use): a credential submission for a gated Codex account in
// a deployment without the operator flag is refused BEFORE the Vault is touched.
// The load-bearing assertion is the zero vault.Put counter — §3.5.4 requires that
// an Auth Mode kill stops use before any decrypt or Provider call.
func TestGatedCodexCredentialUseRefusedBeforeAnyVaultWriteWithoutFlag(t *testing.T) {
	t.Parallel()

	harness := newGatedRefusalHarness(t, func(h *spineHarness) {
		h.seedAccount("tenant_a", usableDraft("pa_codex_noflag", domain.AuthModeChatGPTCodexOAuth))
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts/pa_codex_noflag/credentials",
		bearer:  tenantAKey,
		idemKey: "idem-codex-submit-refused",
		body:    submitBody(domain.CredentialClassOAuthTokenImport),
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	if code := decodeError(t, payload)["code"]; code != "auth_mode_unavailable" {
		t.Fatalf("code = %v, want auth_mode_unavailable", code)
	}
	if put := harness.vault.putCalls.Load(); put != 0 {
		t.Fatalf("vault.Put ran %d times, want 0 (the flag gate must run before any decrypt/write)", put)
	}
}

// AC1 (Tenant acknowledgement, use): with the operator flag on, a gated Codex
// account the Tenant has NOT explicitly risk-acknowledged is still refused at
// credential use with account_not_usable / ack_risk. The two gates are
// sequential and neither substitutes for the other (§5.2, §6.2).
func TestGatedCodexCredentialUseRequiresExplicitTenantAck(t *testing.T) {
	t.Parallel()

	harness := newGatedHarness(t, func(h *spineHarness) {
		account := usableDraft("pa_codex_noack", domain.AuthModeChatGPTCodexOAuth)
		account.RiskAcknowledged = false
		h.seedAccount("tenant_a", account)
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts/pa_codex_noack/credentials",
		bearer:  tenantAKey,
		idemKey: "idem-codex-submit-noack",
		body:    submitBody(domain.CredentialClassOAuthTokenImport),
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	body := decodeError(t, payload)
	if body["code"] != "account_not_usable" {
		t.Fatalf("code = %v, want account_not_usable", body["code"])
	}
	// The remediation must name the missing disclosure, not the operator flag: a
	// Tenant cannot change the operator's enablement, but it can acknowledge risk.
	if body["remediation"] != "ack_risk" {
		t.Fatalf("remediation = %v, want ack_risk", body["remediation"])
	}
	// And nothing durable was written: the ack gate precedes the Vault.
	if put := harness.vault.putCalls.Load(); put != 0 {
		t.Fatalf("vault.Put ran %d times, want 0", put)
	}
}

// AC1 (operator flag, chat execution): a chat request for a gated Codex account
// in a deployment without the operator flag is refused BEFORE the Vault is
// touched. candidateRejection now runs BlocksGated ahead of vault.Validate, so
// the load-bearing assertion is a zero vault.Validate count and a zero Adapter
// call (decision 0014 §5.2: the operator flag must be the first gate, ahead of
// any credential decrypt or Provider call).
func TestGatedCodexChatRefusedBeforeVaultWithoutFlag(t *testing.T) {
	t.Parallel()

	harness := newChatHarnessWithOptions(t, func(harness *chatHarness, options *contracttest.Options) {
		options.GatedAuthModes = nil
		harness.seedActive("tenant_a", "pa_chat_codex", domain.AuthModeChatGPTCodexOAuth)
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-chat-gated-refused",
		body:    chatSuccessBody,
	})
	if response.StatusCode == http.StatusOK {
		t.Fatalf("status = %d, want a non-200 refusal (body=%s)", response.StatusCode, payload)
	}
	if code := decodeError(t, payload)["code"]; code != "auth_mode_unavailable" {
		t.Fatalf("code = %v, want auth_mode_unavailable", code)
	}
	if calls := harness.vault.validCalls.Load(); calls != 0 {
		t.Fatalf("vault.Validate ran %d times, want 0 (BlocksGated must run before any Vault decrypt)", calls)
	}
	if calls := harness.adapter.CallCount(); calls != 0 {
		t.Fatalf("chat Adapter ran %d times, want 0 (the operator flag gate precedes all Provider calls)", calls)
	}
}
