package contracttest_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/contracttest"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// This file proves GW-061 (#61 / Gateway T18) through the public HTTP seam over
// real production composition. Every test drives the composed handler and
// asserts on controlled-port counters, never on Adapter internals.
//
// The story's central claim is a DIFFERENCE between two compositions, so almost
// every test here appears twice: once with no lab profile (an ordinary
// production deployment) and once with `chatgpt_web_access` deliberately
// enabled. A single-composition assertion could not distinguish "the mode is
// off" from "the mode does not work".

// labModes is the enablement an authorized lab deployment configures.
var labModes = []domain.AuthMode{domain.AuthModeChatGPTWebAccess}

// newProductionHarness composes an ordinary production deployment: no lab
// profile, so every `experimental` Auth Mode stays fail-closed.
func newProductionHarness(t *testing.T, configure func(*spineHarness)) *spineHarness {
	t.Helper()
	return newSpineHarness(t, configure)
}

// newLabHarness composes a deployment whose operator deliberately enabled the
// experimental ChatGPT Web Access mode.
func newLabHarness(t *testing.T, configure func(*spineHarness)) *spineHarness {
	t.Helper()
	return newSpineHarness(t, func(h *spineHarness) {
		h.labAuthModes = labModes
		if configure != nil {
			configure(h)
		}
	})
}

// webAccessCreateBody is a self-service create for the experimental mode.
const webAccessCreateBody = `{"provider":"chatgpt","auth_mode":"chatgpt_web_access","label":"lab"}`

// AC2: ordinary production self-service must not expose the mode. A Tenant that
// asks for it by name is refused, and nothing durable is written.
//
// Concretely: before this story the create path checked only Prohibited(), so a
// Tenant could create a `chatgpt_web_access` account in production and — after
// acknowledging risk — activate it. Risk envelope §6.1 forbids exactly that.
func TestProductionSelfServiceRefusesTheExperimentalMode(t *testing.T) {
	t.Parallel()

	harness := newProductionHarness(t, nil)
	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts",
		bearer:  tenantAKey,
		idemKey: "idem-web-create",
		body:    webAccessCreateBody,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	if code := decodeError(t, payload)["code"]; code != "auth_mode_unavailable" {
		t.Fatalf("code = %v, want auth_mode_unavailable", code)
	}
	// No draft may survive a refused create: a persisted draft would let a later
	// deployment that enables the profile inherit an account production never
	// authorized.
	if calls := harness.accounts.createCalls.Load(); calls != 0 {
		t.Fatalf("account.Create ran %d times, want 0", calls)
	}
}

// AC3 (first half): the same request succeeds when an operator deliberately
// enabled the lab profile. This is the positive counterpart that makes the test
// above a proof of the gate rather than of a permanently broken mode.
func TestLabProfileAdmitsTheExperimentalModeItNamed(t *testing.T) {
	t.Parallel()

	harness := newLabHarness(t, nil)
	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts",
		bearer:  tenantAKey,
		idemKey: "idem-web-create-lab",
		body:    webAccessCreateBody,
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", response.StatusCode, payload)
	}
	account := decodeAccount(t, payload)
	if account["auth_mode"] != "chatgpt_web_access" {
		t.Fatalf("auth_mode = %v, want chatgpt_web_access", account["auth_mode"])
	}
	// Enablement is not activation: the account still starts as an unprobed
	// draft with no stored credential (§5.1 keeps the ordinary lifecycle).
	if account["lifecycle_state"] != "draft" {
		t.Fatalf("lifecycle_state = %v, want draft", account["lifecycle_state"])
	}
}

// AC2 + AC6: a credential submission for a production experimental account is
// refused BEFORE the Vault is touched.
//
// The load-bearing assertion is the counter, not the status code. A 409 alone
// proves only that the request was refused; it cannot distinguish "refused
// before the credential was read" from "read the credential, then refused".
// §3.5.4 requires the former, because the point of an Auth Mode kill is that no
// decrypt and no Provider call happens at all.
func TestProductionRefusesCredentialUseBeforeAnyVaultDecrypt(t *testing.T) {
	t.Parallel()

	harness := newProductionHarness(t, func(h *spineHarness) {
		h.seedAccount("tenant_a", usableDraft("pa_web_prod", domain.AuthModeChatGPTWebAccess))
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts/pa_web_prod/credentials",
		bearer:  tenantAKey,
		idemKey: "idem-web-submit",
		body:    submitBody(domain.CredentialClassWebSession),
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	if code := decodeError(t, payload)["code"]; code != "auth_mode_unavailable" {
		t.Fatalf("code = %v, want auth_mode_unavailable", code)
	}
	if calls := harness.vault.putCalls.Load(); calls != 0 {
		t.Fatalf("vault.Put ran %d times, want 0 (the gate must precede the Vault)", calls)
	}
	if calls := harness.vault.validCalls.Load(); calls != 0 {
		t.Fatalf("vault.Validate ran %d times, want 0", calls)
	}
	if calls := harness.probe.callCount.Load(); calls != 0 {
		t.Fatalf("probe Adapter ran %d times, want 0", calls)
	}
}

// AC3 (second half): the lab profile alone is not enough. Enablement and
// disclosure are separate checks, and both must pass before protected
// credential material is used.
//
// The two sub-cases differ only in whether the Tenant acknowledged the residual
// risk, which is what isolates disclosure as its own gate: with the profile on
// and the acknowledgement missing the request is still refused, and again with
// zero Vault calls.
func TestLabConnectionRequiresDisclosureAsWellAsEnablement(t *testing.T) {
	t.Parallel()

	t.Run("acknowledged", func(t *testing.T) {
		t.Parallel()
		harness := newLabHarness(t, func(h *spineHarness) {
			h.seedAccount("tenant_a", usableDraft("pa_web_ack", domain.AuthModeChatGPTWebAccess))
		})
		response, payload := harness.do(t, requestSpec{
			method:  http.MethodPost,
			path:    "/v1/provider-accounts/pa_web_ack/credentials",
			bearer:  tenantAKey,
			idemKey: "idem-web-ack",
			body:    submitBody(domain.CredentialClassWebSession),
		})
		if response.StatusCode != http.StatusAccepted {
			t.Fatalf("status = %d, want 202 (body=%s)", response.StatusCode, payload)
		}
		if calls := harness.vault.putCalls.Load(); calls != 1 {
			t.Fatalf("vault.Put ran %d times, want 1", calls)
		}
	})

	t.Run("not acknowledged", func(t *testing.T) {
		t.Parallel()
		harness := newLabHarness(t, func(h *spineHarness) {
			account := usableDraft("pa_web_noack", domain.AuthModeChatGPTWebAccess)
			account.RiskAcknowledged = false
			h.seedAccount("tenant_a", account)
		})
		response, payload := harness.do(t, requestSpec{
			method:  http.MethodPost,
			path:    "/v1/provider-accounts/pa_web_noack/credentials",
			bearer:  tenantAKey,
			idemKey: "idem-web-noack",
			body:    submitBody(domain.CredentialClassWebSession),
		})
		if response.StatusCode != http.StatusConflict {
			t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
		}
		body := decodeError(t, payload)
		if body["code"] != "account_not_usable" {
			t.Fatalf("code = %v, want account_not_usable", body["code"])
		}
		// The remediation must name the missing disclosure, not the profile: a
		// Tenant cannot fix an operator's lab setting, but it can acknowledge risk.
		if body["remediation"] != "ack_risk" {
			t.Fatalf("remediation = %v, want ack_risk", body["remediation"])
		}
		if calls := harness.vault.putCalls.Load(); calls != 0 {
			t.Fatalf("vault.Put ran %d times, want 0", calls)
		}
	})
}

// AC6: an Auth Mode kill stops new Adapter use before Vault decrypt and before
// any Provider call, even inside an enabled lab profile.
//
// AuthModeExecutionEnabled=false is the §3.5.4 kill switch. The account is
// otherwise fully active and its mode is enabled, so the only thing refusing
// the request is the kill — and the counters prove it fired first.
func TestAuthModeKillStopsAdapterUseBeforeDecryptInsideTheLab(t *testing.T) {
	t.Parallel()

	harness := newLabHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_web_killed", domain.AuthModeChatGPTWebAccess)
		account.Controls.AuthModeExecutionEnabled = false
		h.seedAccount("tenant_a", account)
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_web_killed/probe",
		bearer: tenantAKey,
		body:   `{}`,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	if code := decodeError(t, payload)["code"]; code != "auth_mode_unavailable" {
		t.Fatalf("code = %v, want auth_mode_unavailable", code)
	}
	if calls := harness.vault.validCalls.Load(); calls != 0 {
		t.Fatalf("vault.Validate ran %d times, want 0 (kill must precede decrypt)", calls)
	}
	if calls := harness.probe.callCount.Load(); calls != 0 {
		t.Fatalf("probe Adapter ran %d times, want 0 (kill must precede the Provider call)", calls)
	}
}

// AC2: /v1/models must not advertise an experimental account in production.
//
// Both harnesses seed the identical account and the identical fresh snapshot, so
// the only variable is the lab profile. Production offers nothing; the lab
// deployment offers the account. Without the paired assertion, an empty
// production list could just mean the snapshot was unusable.
func TestModelCatalogAdvertisesTheExperimentalModeOnlyInTheLab(t *testing.T) {
	t.Parallel()

	seed := func(h *spineHarness) {
		h.seedAccount("tenant_a", activeProbedAccount("pa_web_models", domain.AuthModeChatGPTWebAccess))
		h.capabilities.seed("tenant_a", sampleObservationSnapshot(
			"pa_web_models", domain.AuthModeChatGPTWebAccess, 1, spineFixtureTime))
	}

	offerCount := func(t *testing.T, harness *spineHarness) int {
		t.Helper()
		response, payload := harness.do(t, requestSpec{
			method: http.MethodGet,
			path:   "/v1/models",
			bearer: tenantAKey,
		})
		if response.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
		}
		var body struct {
			Data []struct {
				XPixelplus struct {
					Offers []map[string]any `json:"offers"`
				} `json:"x_pixelplus"`
			} `json:"data"`
		}
		if err := json.Unmarshal(payload, &body); err != nil {
			t.Fatalf("decode models: %v (body=%s)", err, payload)
		}
		total := 0
		for _, model := range body.Data {
			total += len(model.XPixelplus.Offers)
		}
		return total
	}

	if offers := offerCount(t, newProductionHarness(t, seed)); offers != 0 {
		t.Errorf("production advertised %d offers for an experimental account, want 0", offers)
	}
	if offers := offerCount(t, newLabHarness(t, seed)); offers == 0 {
		t.Error("lab profile advertised 0 offers; the production result above would be vacuous")
	}
}

// AC5: a fresh probe cannot raise an operation past its canonical baseline.
//
// The controlled Capability Adapter reports `verified` for chat and
// chat_streaming — a strictly stronger claim than the evidence document allows.
// Evidence §2.1 records every primary operation on both ChatGPT modes as
// `conditionally_supported` and none as `verified`, so the minted snapshot must
// come back clamped. Raising the ceiling requires editing that document, which
// is new authority rather than a probe result.
//
// This runs on `chatgpt_codex_oauth` rather than the experimental mode for a
// deliberate reason: enabling the lab profile registers the real ChatGPT Web
// Adapter in place of the controlled stub (see
// TestLabRegistrationReplacesTheStubAdapterWithoutGrantingEgress), so the
// experimental mode cannot produce a controlled `verified` observation to clamp.
// The clamp lives in the domain and is keyed on Auth Mode, so proving it on the
// sibling ChatGPT mode proves the same code path the experimental mode uses.
func TestFreshProbeCannotRaiseAnOperationPastItsCanonicalBaseline(t *testing.T) {
	t.Parallel()

	harness := newProductionHarness(t, func(h *spineHarness) {
		h.seedAccount("tenant_a", probeableAccount("pa_web_probe", domain.AuthModeChatGPTCodexOAuth))
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_web_probe/probe",
		bearer: tenantAKey,
		body:   `{}`,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("probe status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}

	response, payload = harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/provider-accounts/pa_web_probe/capability-snapshot",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("snapshot status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	var snapshot struct {
		Operations map[string]struct {
			Status string `json:"status"`
		} `json:"operations"`
		Models []struct {
			Operations map[string]string `json:"operations"`
		} `json:"models"`
	}
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v (body=%s)", err, payload)
	}

	for _, operation := range []string{"chat", "chat_streaming"} {
		fact, ok := snapshot.Operations[operation]
		if !ok {
			t.Fatalf("snapshot missing operation %s", operation)
		}
		if fact.Status == "verified" {
			t.Errorf("operation %s = verified; a probe raised it past the conditionally_supported baseline", operation)
		}
		if fact.Status != "conditionally_supported" {
			t.Errorf("operation %s = %s, want conditionally_supported", operation, fact.Status)
		}
	}
	// A weaker observation is NOT raised to the baseline: the clamp is a ceiling,
	// not a floor, so "we saw it fail" must survive as `unsupported`.
	if status := snapshot.Operations["inpaint"].Status; status != "unsupported" {
		t.Errorf("inpaint = %s, want unsupported preserved under the ceiling", status)
	}
	// Per-model rows carry the same claim and must be clamped by the same rule,
	// otherwise a Tenant reading the model list sees a stronger promise than the
	// operation summary.
	for index, model := range snapshot.Models {
		for operation, status := range model.Operations {
			if status == "verified" {
				t.Errorf("model row %d operation %s = verified, want clamped", index, operation)
			}
		}
	}
}

// AC2 (positive): "does not register the mode" needs a counterpart that shows
// registration is real, otherwise the negative claim is untestable.
//
// The two harnesses seed identical accounts and share the same controlled stub
// Probe Adapter, which always authenticates. The only difference is the lab
// profile, and the observable difference is which Adapter answered:
//
//   - Production: no experimental Adapter is built, so the stub answers and the
//     probe succeeds with 200.
//   - Lab: the composition root built the real ChatGPT Web Adapter and
//     registered it for this Auth Mode, so it — not the stub — is asked. With no
//     transport supplied it fails closed with 503, and the stub is never called.
//
// That zero on the stub counter is the proof: the request reached a different
// object graph. It also demonstrates the intended production posture, since
// enabling a mode grants no egress by itself — an operator must deliberately
// supply transport as a separate act.
func TestLabRegistrationReplacesTheStubAdapterWithoutGrantingEgress(t *testing.T) {
	t.Parallel()

	seed := func(h *spineHarness) {
		h.seedAccount("tenant_a", probeableAccount("pa_web_reg", domain.AuthModeChatGPTWebAccess))
	}

	production := newProductionHarness(t, func(h *spineHarness) {
		h.labAuthModes = nil
		seed(h)
	})
	// Production refuses the mode outright at the Auth Mode gate, before any
	// Adapter question arises.
	response, payload := production.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_web_reg/probe",
		bearer: tenantAKey,
		body:   `{}`,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("production probe status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	if calls := production.probe.callCount.Load(); calls != 0 {
		t.Fatalf("production ran the Probe Adapter %d times, want 0", calls)
	}

	lab := newLabHarness(t, seed)
	response, payload = lab.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_web_reg/probe",
		bearer: tenantAKey,
		body:   `{}`,
	})
	// 503 rather than 409: the Auth Mode gate passed, the spine reached the
	// Adapter boundary, and the registered Adapter had no transport.
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("lab probe status = %d, want 503 (body=%s)", response.StatusCode, payload)
	}
	if code := decodeError(t, payload)["code"]; code != "dependency_unavailable" {
		t.Fatalf("lab probe code = %v, want dependency_unavailable", code)
	}
	// The registry dispatched to the real Adapter, so the always-succeeding stub
	// was bypassed. Had the registry not been built, this would be 1 and the
	// probe would have succeeded.
	if calls := lab.probe.callCount.Load(); calls != 0 {
		t.Fatalf("lab fell through to the stub Probe Adapter %d times, want 0 "+
			"(the experimental Adapter must be registered for its Auth Mode)", calls)
	}
}

// AC2: a registered experimental Adapter must not capture another Auth Mode.
//
// The lab profile names `chatgpt_web_access` only. A gated OAuth account in the
// same deployment must still reach the ordinary Adapter, otherwise registering
// one experimental mode would silently reroute unrelated traffic through
// ChatGPT Web protocol framing.
func TestLabRegistrationDoesNotCaptureOtherAuthModes(t *testing.T) {
	t.Parallel()

	harness := newLabHarness(t, func(h *spineHarness) {
		h.seedAccount("tenant_a", probeableAccount("pa_oauth_sibling", domain.AuthModeChatGPTCodexOAuth))
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_oauth_sibling/probe",
		bearer: tenantAKey,
		body:   `{}`,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	if calls := harness.probe.callCount.Load(); calls != 1 {
		t.Fatalf("stub Probe Adapter ran %d times, want 1 (a non-enabled mode must fall through)", calls)
	}
}

// AC2 + AC6: the chat execution path refuses an experimental account in ordinary
// production, and the refusal happens before the Adapter is entered.
//
// This is the highest-value gate of the six because chat is where a Provider
// credential is actually spent. The account here is fully active, healthy, has a
// fresh capability snapshot offering the model, and is the sole routing
// candidate — every gate except the Auth Mode gate passes. So the 409 isolates
// the lab-profile check, and the zero counters prove it fired before the
// credential was read and before the Adapter ran.
func TestProductionChatRefusesTheExperimentalModeBeforeTheAdapter(t *testing.T) {
	t.Parallel()

	harness := newChatHarness(t, func(h *chatHarness) {
		h.seedActive("tenant_a", "pa_web_chat", domain.AuthModeChatGPTWebAccess)
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-web-chat",
		body:    chatSuccessBody,
	})
	// No usable candidate remains once the experimental account is excluded, so
	// the request cannot be served at all.
	if response.StatusCode == http.StatusOK {
		t.Fatalf("production served chat on an experimental account (body=%s)", payload)
	}
	if calls := harness.adapter.CallCount(); calls != 0 {
		t.Fatalf("chat Adapter ran %d times, want 0", calls)
	}
	if calls := harness.vault.validCalls.Load(); calls != 0 {
		t.Fatalf("vault.Validate ran %d times, want 0 (the gate must precede the credential)", calls)
	}
}

// AC2 (paired positive): the identical chat request gets PAST the Auth Mode gate
// when the operator enabled the mode, which is what makes the production refusal
// above a proof of the gate rather than of an unrelated misconfiguration.
//
// The lab harness keeps its controlled ChatAdapter injected. Composition wraps
// that fallback in the Auth-Mode registry, and because no real transport is
// supplied the registry dispatches this mode to the real ChatGPT Web Adapter —
// so this test asserts the request reached the Adapter boundary rather than that
// it completed.
//
// The observable is the CONCRETE post-gate outcome, not merely the absence of a
// refusal. Asserting only "the code is not auth_mode_unavailable" would pass for
// any unrelated breakage on the chat path — a validation rejection, an admission
// denial, no eligible routing candidate, a settlement failure — because each of
// those also yields some other code with zero fallback-Adapter calls. That is a
// test that cannot fail for the one reason it exists, so it is pinned to the
// exact failure the nil-transport Adapter produces:
//
//	503 / upstream_unavailable
//
// This mirrors TestLabRegistrationReplacesTheStubAdapterWithoutGrantingEgress,
// which pins the same boundary on the probe surface. The two codes differ, and
// the difference is the ports contract rather than an inconsistency: the probe
// port reports a missing dependency as ports.ErrDependencyUnavailable
// (dependency_unavailable), while the chat port classifies
// chatgptweb.ErrTransportUnavailable through canonicalFailureClass into
// domain.ErrCodeUpstreamUnavailable (upstream_unavailable). Both are 503, and
// both say the same thing: the gate opened, the Adapter was entered, and
// enabling a mode granted no egress by itself.
func TestLabChatReachesTheAdapterBoundaryForTheEnabledMode(t *testing.T) {
	t.Parallel()

	harness := newChatHarnessWithOptions(t, func(h *chatHarness, options *contracttest.Options) {
		options.ExperimentalLabAuthModes = labModes
		h.seedActive("tenant_a", "pa_web_chat_lab", domain.AuthModeChatGPTWebAccess)
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-web-chat-lab",
		body:    chatSuccessBody,
	})
	body := decodeCompletionBody(t, payload)
	code, _ := body["code"].(string)
	// The Auth Mode gate returns auth_mode_unavailable (409). Anything else means
	// the gate passed and the request reached the execution path. Kept as its own
	// check so a gate regression is named explicitly rather than reported as a
	// generic code mismatch.
	if code == "auth_mode_unavailable" {
		t.Fatalf("lab composition still refused the enabled mode at the Auth Mode gate (status=%d body=%s)",
			response.StatusCode, payload)
	}
	// The positive claim. 503 is only reachable AFTER routing picked the
	// experimental account, admission passed, and the registry dispatched into the
	// real ChatGPT Web Adapter, which then failed on its absent transport. Any
	// earlier failure on the chat path produces a different status or code here.
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("lab chat status = %d, want 503 — the request did not reach the Adapter boundary (body=%s)",
			response.StatusCode, payload)
	}
	if code != "upstream_unavailable" {
		t.Fatalf("lab chat code = %q, want upstream_unavailable — the registered Adapter ships with a nil transport, "+
			"so this is the only outcome that proves the Adapter boundary was entered (body=%s)", code, payload)
	}
	// The controlled fallback Adapter must NOT have served this mode: the registry
	// dispatches it to the real Adapter, which has no transport. Had the registry
	// not been built, this would be 1 and the request would have succeeded with 200.
	if calls := harness.adapter.CallCount(); calls != 0 {
		t.Fatalf("fallback chat Adapter served the enabled experimental mode %d times, want 0", calls)
	}
}

// AC2 + AC6: the render (image) path refuses an experimental account before the
// Adapter is entered — in ordinary production AND inside an enabled lab profile.
//
// Render matters separately from chat because it is asynchronous: the Adapter
// runs on a worker, not in the request. So a gate that only refused at request
// time would still let a job be enqueued and spend the credential later. The
// assertion is therefore that nothing was enqueued at all.
//
// Unlike every other gate site, the lab profile deliberately does NOT open this
// surface. No experimental Adapter implements ports.RenderAdapter and composition
// builds no render registry for one, so an accepted job could only reach the
// fail-closed foundation. Accepting it would answer 202, durably enqueue, and
// fail on the worker — converting a refusal the Gateway can make before any
// durable side effect into a job that dies later.
func TestRenderRefusesTheExperimentalModeInEveryComposition(t *testing.T) {
	t.Parallel()

	seed := func(h *renderHarness) {
		account := activeProbedAccount("pa_web_render", domain.AuthModeChatGPTWebAccess)
		h.seedAccount("tenant_a", account)
		h.capabilities.seed("tenant_a", imageGenerationSnapshot(
			"pa_web_render", domain.AuthModeChatGPTWebAccess, 1, spineFixtureTime))
		h.routing.Seed("tenant_a", domain.RoutingPolicy{
			CandidateAccounts: []domain.ProviderAccountID{"pa_web_render"},
			SelectionOrder:    []domain.ProviderAccountID{"pa_web_render"},
			FallbackEnabled:   false,
			FallbackChain:     []domain.ProviderAccountID{},
			FallbackAuthModes: []domain.AuthMode{},
			Affinity:          domain.AffinityPolicy{Enabled: false},
			LeasePolicy: domain.LeasePolicy{
				Enabled:       true,
				EligibleUnits: []domain.LeaseUnit{domain.LeaseUnitRenderJob},
			},
			UpdatedAt: domain.NewTimestamp(spineFixtureTime),
			UpdatedBy: "key_a",
		})
	}

	const generateBody = `{"model":"gpt-image-1","prompt":"a red circle"}`

	production := newRenderHarness(t, seed)
	response, payload := production.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-web-render",
		body:    generateBody,
	})
	if response.StatusCode == http.StatusAccepted {
		t.Fatalf("production accepted a render job on an experimental account (body=%s)", payload)
	}
	// A queued job would spend the credential on a worker after the request
	// returned, so the refusal must precede publication entirely.
	if refs := production.fixture.EnqueuedReferences(); len(refs) != 0 {
		t.Fatalf("production enqueued %d render jobs, want 0", len(refs))
	}
	if calls := production.vault.validCalls.Load(); calls != 0 {
		t.Fatalf("vault.Validate ran %d times, want 0", calls)
	}

	// Enabling the mode does not open the render surface: the SAME request must
	// still be refused, and still without an enqueue, because no render Adapter
	// exists for it in either composition.
	lab := newRenderHarness(t, func(h *renderHarness) {
		h.labAuthModes = labModes
		seed(h)
	})
	response, payload = lab.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-web-render-lab",
		body:    generateBody,
	})
	if response.StatusCode == http.StatusAccepted {
		t.Fatalf("lab accepted a render job it has no Adapter to serve (body=%s)", payload)
	}
	if code := decodeError(t, payload)["code"]; code != "auth_mode_unavailable" {
		t.Fatalf("lab code = %v, want auth_mode_unavailable (body=%s)", code, payload)
	}
	// The load-bearing half: a 4xx alone would also be produced by a broken
	// fixture. Zero enqueues proves the refusal happened before any durable job
	// existed, which is what stops a worker from spending the credential later.
	if refs := lab.fixture.EnqueuedReferences(); len(refs) != 0 {
		t.Fatalf("lab enqueued %d render jobs, want 0", len(refs))
	}
	if calls := lab.vault.validCalls.Load(); calls != 0 {
		t.Fatalf("lab vault.Validate ran %d times, want 0", calls)
	}

	// Control in the opposite direction: without it, a fixture that refused EVERY
	// render request would pass both halves above. A `gated` mode on the same
	// harness shape is admitted, so the refusals are the experimental gate rather
	// than a render path that never works.
	control := newRenderHarness(t, func(h *renderHarness) {
		h.labAuthModes = labModes
		seedRoutableImageAccount(h, "pa_gated_render")
	})
	response, payload = control.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-gated-render",
		body:    generateBody,
	})
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("control status = %d, want 202 (body=%s)", response.StatusCode, payload)
	}
}

// OP-G3: nothing on the ChatGPT Web paths may emit credential material into an
// audit event, a telemetry event, a request-log projection, or a response body.
//
// The submitted material is a known sentinel, so the check is exact rather than
// heuristic: it drives a real submission through the enabled lab profile (so the
// credential genuinely reaches the Vault) and then scans every observable
// projection for that string. OP-G3 names raw cookies and SSO tokens
// specifically, which is exactly what a `web_session` submission carries.
func TestExperimentalPathsNeverEmitCredentialMaterial(t *testing.T) {
	t.Parallel()

	harness := newLabHarness(t, func(h *spineHarness) {
		h.seedAccount("tenant_a", usableDraft("pa_web_secret", domain.AuthModeChatGPTWebAccess))
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts/pa_web_secret/credentials",
		bearer:  tenantAKey,
		idemKey: "idem-web-secret",
		body:    submitBody(domain.CredentialClassWebSession),
	})
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body=%s)", response.StatusCode, payload)
	}
	// Guard the guard: if the material never reached the Vault, the scans below
	// would pass without the credential having existed on this path at all.
	if calls := harness.vault.putCalls.Load(); calls != 1 {
		t.Fatalf("vault.Put ran %d times, want 1", calls)
	}

	if bytes.Contains(payload, []byte(submitMaterial)) {
		t.Error("response body echoed the submitted credential material")
	}
	// Also scan the account projection, then the read-back, since a later GET is
	// where a leaked field would most plausibly surface.
	_, readBack := harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/provider-accounts/pa_web_secret",
		bearer: tenantAKey,
	})
	if bytes.Contains(readBack, []byte(submitMaterial)) {
		t.Error("account read-back exposed the submitted credential material")
	}

	for _, event := range harness.audit.snapshot() {
		if encoded, err := json.Marshal(event); err == nil && bytes.Contains(encoded, []byte(submitMaterial)) {
			t.Errorf("audit event %s carried credential material", event.Action)
		}
	}
	for _, event := range harness.telemetry.snapshot() {
		if encoded, err := json.Marshal(event); err == nil && bytes.Contains(encoded, []byte(submitMaterial)) {
			t.Error("telemetry event carried credential material")
		}
	}
	for _, entry := range harness.reqLog.snapshot() {
		if encoded, err := json.Marshal(entry); err == nil && bytes.Contains(encoded, []byte(submitMaterial)) {
			t.Error("request log carried credential material")
		}
	}
}

// AC2 + FG-2: cross-mode fallback INTO the experimental mode stays refused even
// inside a lab deployment that enabled it.
//
// Enablement makes the mode usable when a Tenant names it directly. It does not
// make it a silent substitute for a different Auth Mode: §6.3 and FG-2 forbid a
// dead Codex OAuth account being answered by ChatGPT Web Access without the
// Tenant knowing. This is the one place where "the profile is on" deliberately
// does not change the answer.
func TestCrossModeFallbackIntoTheExperimentalModeStaysRefused(t *testing.T) {
	t.Parallel()

	harness := newLabHarness(t, func(h *spineHarness) {
		seedEligibleAccount(h, "tenant_a", "pa_oauth_primary", domain.AuthModeChatGPTCodexOAuth)
		seedEligibleAccount(h, "tenant_a", "pa_web_fallback", domain.AuthModeChatGPTWebAccess)
	})

	// A policy whose fallback chain crosses from the gated OAuth mode into the
	// enabled experimental mode, declaring both modes as required by §8.1.
	body := `{
		"candidate_accounts": ["pa_oauth_primary", "pa_web_fallback"],
		"selection_order": ["pa_oauth_primary"],
		"fallback_enabled": true,
		"fallback_chain": ["pa_web_fallback"],
		"fallback_auth_modes": ["chatgpt_codex_oauth", "chatgpt_web_access"],
		"affinity": {"enabled": false},
		"lease_policy": {"enabled": false, "eligible_units": []}
	}`
	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPut,
		path:    "/v1/routing-policy",
		bearer:  tenantAKey,
		idemKey: "idem-xmode",
		body:    body,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	if code := decodeError(t, payload)["code"]; code != "auth_mode_unavailable" {
		t.Fatalf("code = %v, want auth_mode_unavailable", code)
	}
	// A refused shape must not be persisted: a stored policy naming a forbidden
	// fallback would keep failing at execution time instead of at declaration.
	if calls := harness.routing.replaces.Load(); calls != 0 {
		t.Fatalf("routing.Replace ran %d times, want 0", calls)
	}
}

// AC5 (control): the clamp is scoped to the modes whose evidence document is
// normative here. A gated OAuth mode keeps its observed `verified` status, so
// the test above proves a targeted ceiling rather than a blanket downgrade of
// every snapshot in the system.
func TestTheBaselineClampDoesNotDowngradeOtherAuthModes(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.seedAccount("tenant_a", probeableAccount("pa_gemini", domain.AuthModeGeminiAntigravityOAuth))
	})

	if response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_gemini/probe",
		bearer: tenantAKey,
		body:   `{}`,
	}); response.StatusCode != http.StatusOK {
		t.Fatalf("probe status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}

	response, payload := harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/provider-accounts/pa_gemini/capability-snapshot",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("snapshot status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	var snapshot struct {
		Operations map[string]struct {
			Status string `json:"status"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v (body=%s)", err, payload)
	}
	if status := snapshot.Operations["chat"].Status; status != "verified" {
		t.Errorf("gemini_antigravity_oauth chat = %s, want verified (no baseline applies)", status)
	}
}

// AC2 + AC5: a prohibited Auth Mode cannot be enabled through the lab profile.
//
// The lab profile here names Grok Web SSO alongside the experimental mode.
// domain.NewLabProfile accepts only `experimental` modes, so the prohibited mode
// is dropped at construction and the account is still refused. Without this,
// the lab profile would be a general-purpose bypass rather than a scoped
// experimental enablement.
func TestLabProfileCannotEnableAProhibitedMode(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.labAuthModes = []domain.AuthMode{
			domain.AuthModeGrokWebSSO,
			domain.AuthModeChatGPTWebAccess,
		}
		h.seedAccount("tenant_a", usableDraft("pa_grok", domain.AuthModeGrokWebSSO))
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts/pa_grok/credentials",
		bearer:  tenantAKey,
		idemKey: "idem-grok",
		body:    submitBody(domain.CredentialClassWebSession),
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	if code := decodeError(t, payload)["code"]; code != "auth_mode_unavailable" {
		t.Fatalf("code = %v, want auth_mode_unavailable", code)
	}
	if calls := harness.vault.putCalls.Load(); calls != 0 {
		t.Fatalf("vault.Put ran %d times for a prohibited mode, want 0", calls)
	}
}
