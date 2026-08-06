package contracttest_test

import (
	"net/http"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/contracttest"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// This file closes the #62 F11 gap: AC6 asks for kill/reopen evidence, and the
// branch had a config that turns the mode on plus a test refusing Codex→Web
// fallback, but nothing proved the two properties the Gateway itself is
// responsible for under the reopen checklist (risk envelope §3.5.4-§3.5.5):
//
//  1. A killed Auth Mode is UNREGISTERED, not merely rejected late. §3.5.4 makes
//     Auth Mode the Adapter registration unit, so a kill must stop the Adapter
//     path — every surface closed before any Vault decrypt or Provider call.
//  2. Reopen is NEVER automatic. R3 requires a deliberate audited config change;
//     no amount of time passing, retrying, or per-account recovery may bring the
//     mode back.
//
// The remaining checklist steps (R0 incident note, R1 fix evidence, R2 operator
// probe suite, R4 soak) are operator process recorded outside the binary, so they
// are deliberately NOT asserted here — a test that pretended to check them would
// be theatre. What is testable is that the Gateway keeps the door shut until the
// operator's config change opens it, and these tests pin exactly that.

// killedCodexPolicy is a routing policy naming a killed gated Codex account.
const killedCodexPolicy = `{
	"candidate_accounts": ["pa_killed_codex"],
	"selection_order": ["pa_killed_codex"],
	"fallback_enabled": false,
	"fallback_chain": [],
	"fallback_auth_modes": ["chatgpt_codex_oauth"],
	"affinity": {"enabled": false},
	"lease_policy": {"enabled": false, "eligible_units": []}
}`

// §3.5.4 (kill is an Adapter-path pause): with the gated mode killed — the
// operator flag off — EVERY surface refuses, and each refusal lands before the
// Vault. The per-surface loop is what makes this a kill rather than a patchwork:
// a mode that stayed shut on chat but answered on probe would still be reachable.
//
// The zero-Vault assertions are load-bearing. §3.5.4 requires the kill to stop
// use, and a refusal that happened AFTER a decrypt would mean credential material
// was released for a mode the operator had killed.
func TestKilledGatedCodexModeClosesEverySurfaceBeforeTheVault(t *testing.T) {
	t.Parallel()

	surfaces := []struct {
		name    string
		method  string
		path    string
		body    string
		idemKey string
	}{
		{
			name:    "credential submission",
			method:  http.MethodPost,
			path:    "/v1/provider-accounts/pa_killed_codex/credentials",
			body:    submitBody(domain.CredentialClassOAuthTokenImport),
			idemKey: "idem-killed-submit",
		},
		{
			name:   "probe",
			method: http.MethodPost,
			path:   "/v1/provider-accounts/pa_killed_codex/probe",
		},
		{
			name:   "reauthentication",
			method: http.MethodPost,
			path:   "/v1/provider-accounts/pa_killed_codex/reauthentication",
		},
	}

	for _, surface := range surfaces {
		t.Run(surface.name, func(t *testing.T) {
			t.Parallel()

			harness := newGatedRefusalHarness(t, func(h *spineHarness) {
				h.seedAccount("tenant_a", activeAccount("pa_killed_codex", domain.AuthModeChatGPTCodexOAuth))
			})

			response, payload := harness.do(t, requestSpec{
				method:  surface.method,
				path:    surface.path,
				bearer:  tenantAKey,
				idemKey: surface.idemKey,
				body:    surface.body,
			})
			if response.StatusCode == http.StatusOK || response.StatusCode == http.StatusCreated {
				t.Fatalf("status = %d, want a refusal on a killed Auth Mode (body=%s)", response.StatusCode, payload)
			}
			if put := harness.vault.putCalls.Load(); put != 0 {
				t.Errorf("vault.Put ran %d times, want 0 — a kill must stop use before any decrypt/write", put)
			}
			if valid := harness.vault.validCalls.Load(); valid != 0 {
				t.Errorf("vault.Validate ran %d times, want 0 — a kill must precede every credential decrypt", valid)
			}
		})
	}
}

// §3.5.4 (execution surfaces): the same kill closes chat and stream, and the
// Adapter is never entered. This is the execution counterpart of the management
// surfaces above.
func TestKilledGatedCodexModeClosesChatAndStreamBeforeTheAdapter(t *testing.T) {
	t.Parallel()

	for _, execution := range []struct {
		name string
		body string
	}{
		{name: "chat", body: chatSuccessBody},
		{name: "stream", body: chatStreamBody},
	} {
		t.Run(execution.name, func(t *testing.T) {
			t.Parallel()

			harness := newChatHarnessWithOptions(t, func(h *chatHarness, options *contracttest.Options) {
				options.GatedAuthModes = nil
				h.seedActive("tenant_a", "pa_killed_exec", domain.AuthModeChatGPTCodexOAuth)
			})

			response, payload := harness.do(t, requestSpec{
				method:  http.MethodPost,
				path:    "/v1/chat/completions",
				bearer:  tenantAKey,
				idemKey: "idem-killed-" + execution.name,
				body:    execution.body,
			})
			if response.StatusCode == http.StatusOK {
				t.Fatalf("status = 200, want a refusal on a killed Auth Mode (body=%s)", payload)
			}
			if code := decodeError(t, payload)["code"]; code != "auth_mode_unavailable" {
				t.Errorf("code = %v, want auth_mode_unavailable", code)
			}
			if calls := harness.vault.validCalls.Load(); calls != 0 {
				t.Errorf("vault.Validate ran %d times, want 0", calls)
			}
			if calls := harness.adapter.CallCount(); calls != 0 {
				t.Errorf("Adapter ran %d times, want 0 — a killed mode must not reach a Provider", calls)
			}
		})
	}
}

// §3.5.5 (reopen is never automatic): a killed mode must NOT come back through
// any in-band recovery action a Tenant or the system can take on its own.
//
// The three attempts below are the plausible accidental reopens: retrying the
// same request, recovering the account (the per-account control §3.5.4 explicitly
// keeps separate from an Auth Mode status change), and declaring a routing policy
// that names the mode. All must stay refused, because R3 requires an operator
// config change and none of these is one.
func TestKilledGatedCodexModeDoesNotReopenWithoutAnOperatorConfigChange(t *testing.T) {
	t.Parallel()

	harness := newGatedRefusalHarness(t, func(h *spineHarness) {
		h.seedAccount("tenant_a", activeAccount("pa_killed_codex", domain.AuthModeChatGPTCodexOAuth))
	})

	// Attempt 1: retry the refused probe. Repetition is not evidence.
	for attempt := 1; attempt <= 3; attempt++ {
		response, payload := harness.do(t, requestSpec{
			method: http.MethodPost,
			path:   "/v1/provider-accounts/pa_killed_codex/probe",
			bearer: tenantAKey,
		})
		if response.StatusCode == http.StatusOK {
			t.Fatalf("probe attempt %d succeeded; retries must not reopen a killed Auth Mode (body=%s)", attempt, payload)
		}
	}

	// Attempt 2: per-account recovery. §3.5.4 keeps per-account cooldown recovery
	// separate from an Auth Mode status change precisely so this cannot double as
	// a reopen.
	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts/pa_killed_codex/recovery",
		bearer:  tenantAKey,
		idemKey: "idem-killed-recovery",
	})
	if response.StatusCode == http.StatusOK {
		t.Fatalf("account recovery succeeded on a killed Auth Mode; that is a status change it must not perform (body=%s)", payload)
	}

	// Attempt 3: declare a routing policy naming the killed mode. A stored policy
	// would defer the refusal to execution time instead of refusing the
	// declaration, so the mode must be refused here too and nothing persisted.
	response, payload = harness.do(t, requestSpec{
		method:  http.MethodPut,
		path:    "/v1/routing-policy",
		bearer:  tenantAKey,
		idemKey: "idem-killed-routing",
		body:    killedCodexPolicy,
	})
	if response.StatusCode == http.StatusOK {
		t.Fatalf("routing policy naming a killed Auth Mode was accepted (body=%s)", payload)
	}
	if calls := harness.routing.replaces.Load(); calls != 0 {
		t.Errorf("routing.Replace ran %d times, want 0 — a refused policy must not be persisted", calls)
	}

	// And after all of it the Vault was never touched: nothing in the sequence
	// released credential material for the killed mode.
	if put := harness.vault.putCalls.Load(); put != 0 {
		t.Errorf("vault.Put ran %d times across every reopen attempt, want 0", put)
	}
}

// §3.5.5 R3 (the config change IS the reopen): the control counterpart. The same
// account and the same request that stayed refused above are admitted once — and
// only once — the operator names the mode in the deployment config.
//
// Without this pairing the tests above would be satisfied by a Gateway that
// refused the mode unconditionally, which would prove a permanent ban rather than
// a reopenable kill.
func TestGatedCodexModeReopensOnlyThroughTheOperatorConfigChange(t *testing.T) {
	t.Parallel()

	harness := newGatedHarness(t, func(h *spineHarness) {
		h.seedAccount("tenant_a", probeableAccount("pa_reopened_codex", domain.AuthModeChatGPTCodexOAuth))
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_reopened_codex/probe",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 once the operator enabled the mode (body=%s)", response.StatusCode, payload)
	}
	account := decodeAccount(t, payload)
	if account["auth_mode"] != "chatgpt_codex_oauth" {
		t.Fatalf("auth_mode = %v, want chatgpt_codex_oauth", account["auth_mode"])
	}
}
