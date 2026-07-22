package contracttest_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// activeProbedAccount builds an account that already passed probe activation so
// offerable models may be listed for it.
func activeProbedAccount(id string, mode domain.AuthMode) domain.ProviderAccount {
	return probeableAccount(id, mode).
		WithValidatedCredential(domain.NewTimestamp(spineFixtureTime)).
		WithProbeActivated(domain.NewTimestamp(spineFixtureTime))
}

func sampleObservationSnapshot(accountID domain.ProviderAccountID, mode domain.AuthMode, version int, verifiedAt time.Time) domain.CapabilitySnapshot {
	return domain.NewLiveProbeSnapshot(
		accountID,
		mode,
		version,
		domain.NewTimestamp(verifiedAt),
		map[domain.CapabilityOperation]domain.CapabilityFact{
			domain.CapabilityOpChat: {
				Status:        domain.CapabilityVerified,
				EvidenceClass: domain.EvidenceLiveProbe,
				ProbeSurface:  "/backend-api/models",
			},
			domain.CapabilityOpChatStreaming: {
				Status:         domain.CapabilityVerified,
				EvidenceClass:  domain.EvidenceLiveProbe,
				ProbeSurface:   "/backend-api/models",
				StreamingClass: domain.StreamingReal,
			},
			domain.CapabilityOpImageGeneration: {
				Status:        domain.CapabilityConditionallySupported,
				EvidenceClass: domain.EvidenceLiveProbe,
				ProbeSurface:  "/backend-api/models",
			},
			domain.CapabilityOpImageEdit: {
				Status:        domain.CapabilityConditionallySupported,
				EvidenceClass: domain.EvidenceLiveProbe,
				ProbeSurface:  "/backend-api/models",
			},
			domain.CapabilityOpInpaint: {
				Status:        domain.CapabilityUnsupported,
				EvidenceClass: domain.EvidenceLiveProbe,
				ProbeSurface:  "/backend-api/models",
			},
		},
		[]domain.ModelCapability{{
			ModelSlug: "gpt-4o-mini",
			Operations: map[domain.CapabilityOperation]domain.CapabilityStatus{
				domain.CapabilityOpChat:            domain.CapabilityVerified,
				domain.CapabilityOpChatStreaming:   domain.CapabilityVerified,
				domain.CapabilityOpImageGeneration: domain.CapabilityUnsupported,
				domain.CapabilityOpImageEdit:       domain.CapabilityUnsupported,
				domain.CapabilityOpInpaint:         domain.CapabilityUnsupported,
			},
			SurfaceBinding: "chatgpt_web",
			ObservedAt:     domain.NewTimestamp(verifiedAt),
		}},
		"/backend-api/models",
	)
}

// AC: successful probe mints a credential-version-bound snapshot before the
// account becomes active; getCapabilitySnapshot returns the five operations and
// observed models only.
func TestProbeSuccessMintsCapabilitySnapshot(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", probeableAccount("pa_cap", domain.AuthModeChatGPTCodexOAuth))
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_cap/probe",
		bearer: tenantAKey,
		body:   `{}`,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("probe status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	account := decodeAccount(t, payload)
	if account["lifecycle_state"] != "active" {
		t.Fatalf("lifecycle_state = %v, want active", account["lifecycle_state"])
	}
	if calls := harness.capability.callCount.Load(); calls != 1 {
		t.Fatalf("capability.Observe ran %d times, want 1", calls)
	}
	if calls := harness.capabilities.putCalls.Load(); calls != 1 {
		t.Fatalf("capability.Put ran %d times, want 1", calls)
	}

	response, payload = harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/provider-accounts/pa_cap/capability-snapshot",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("snapshot status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapshot["provider_account_id"] != "pa_cap" {
		t.Fatalf("provider_account_id = %v, want pa_cap", snapshot["provider_account_id"])
	}
	if version, _ := snapshot["credential_version"].(float64); version != 1 {
		t.Fatalf("credential_version = %v, want 1", snapshot["credential_version"])
	}
	if snapshot["freshness"] != "fresh" {
		t.Fatalf("freshness = %v, want fresh", snapshot["freshness"])
	}
	if snapshot["ttl_class"] != "TTL-PROBE-LIVE" {
		t.Fatalf("ttl_class = %v, want TTL-PROBE-LIVE", snapshot["ttl_class"])
	}
	operations, _ := snapshot["operations"].(map[string]any)
	for _, op := range []string{"chat", "chat_streaming", "image_generation", "image_edit", "inpaint"} {
		if _, ok := operations[op]; !ok {
			t.Fatalf("operations missing %s", op)
		}
	}
	models, _ := snapshot["models"].([]any)
	if len(models) != 1 {
		t.Fatalf("models len = %d, want 1", len(models))
	}
	model, _ := models[0].(map[string]any)
	if model["model_slug"] != "gpt-4o-mini" {
		t.Fatalf("model_slug = %v, want gpt-4o-mini", model["model_slug"])
	}
}

// AC: missing snapshot returns capability_unverified without vault decrypt.
func TestGetCapabilitySnapshotMissingIsUnverified(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", usableDraft("pa_nosnap", domain.AuthModeChatGPTCodexOAuth))
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/provider-accounts/pa_nosnap/capability-snapshot",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	body := decodeError(t, payload)
	if body["code"] != "capability_unverified" {
		t.Fatalf("code = %v, want capability_unverified", body["code"])
	}
	if body["status_class"] != "capability" {
		t.Fatalf("status_class = %v, want capability", body["status_class"])
	}
	if body["remediation"] != "capability_unverified" {
		t.Fatalf("remediation = %v, want capability_unverified", body["remediation"])
	}
	if calls := harness.vault.validCalls.Load(); calls != 0 {
		t.Fatalf("vault.Validate ran %d times, want 0", calls)
	}
}

// AC: foreign account snapshot read is non-enumerating resource_not_found and
// never reaches the capability store.
func TestGetCapabilitySnapshotForeignNotFound(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_b", activeProbedAccount("pa_foreign_cap", domain.AuthModeChatGPTCodexOAuth))
		h.capabilities.seed("tenant_b", sampleObservationSnapshot("pa_foreign_cap", domain.AuthModeChatGPTCodexOAuth, 1, spineFixtureTime))
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/provider-accounts/pa_foreign_cap/capability-snapshot",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", response.StatusCode, payload)
	}
	body := decodeError(t, payload)
	if body["code"] != "resource_not_found" {
		t.Fatalf("code = %v, want resource_not_found", body["code"])
	}
	if calls := harness.capabilities.getCalls.Load(); calls != 0 {
		t.Fatalf("capability.Get ran %d times before ownership gate, want 0", calls)
	}
}

// AC: accounts.read alone may inspect a snapshot; capabilities.read alone also
// may; missing both is forbidden.
func TestGetCapabilitySnapshotScopeAnyOf(t *testing.T) {
	t.Parallel()

	const capsOnly = "sk-pxp_locatorC_secretC"

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.principal.principals[capsOnly] = domain.SecurityPrincipal{
			TenantID:       "tenant_a",
			ClientAPIKeyID: "key_c",
			Scopes:         domain.NewScopeSet(domain.ScopeCapabilitiesRead),
		}
		h.accounts.seed("tenant_a", activeProbedAccount("pa_scope", domain.AuthModeChatGPTCodexOAuth))
		h.capabilities.seed("tenant_a", sampleObservationSnapshot("pa_scope", domain.AuthModeChatGPTCodexOAuth, 1, spineFixtureTime))
	})

	// accounts.read only
	response, payload := harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/provider-accounts/pa_scope/capability-snapshot",
		bearer: readOnly,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("accounts.read status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}

	// capabilities.read only
	response, payload = harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/provider-accounts/pa_scope/capability-snapshot",
		bearer: capsOnly,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("capabilities.read status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
}

// AC: listModels returns only fresh offerable pairs for the authenticated
// Tenant; stale/invalid/foreign pairs are omitted.
func TestListModelsOnlyFreshOfferablePairs(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		active := activeProbedAccount("pa_models", domain.AuthModeChatGPTCodexOAuth)
		h.accounts.seed("tenant_a", active)
		h.capabilities.seed("tenant_a", sampleObservationSnapshot("pa_models", domain.AuthModeChatGPTCodexOAuth, 1, spineFixtureTime))

		// Stale snapshot for another same-Tenant account must not offer.
		staleAccount := activeProbedAccount("pa_stale", domain.AuthModeChatGPTCodexOAuth)
		h.accounts.seed("tenant_a", staleAccount)
		stale := sampleObservationSnapshot("pa_stale", domain.AuthModeChatGPTCodexOAuth, 1, spineFixtureTime.Add(-30*time.Minute))
		h.capabilities.seed("tenant_a", stale)

		// Foreign tenant snapshot must never leak.
		h.accounts.seed("tenant_b", activeProbedAccount("pa_foreign_models", domain.AuthModeChatGPTCodexOAuth))
		h.capabilities.seed("tenant_b", sampleObservationSnapshot("pa_foreign_models", domain.AuthModeChatGPTCodexOAuth, 1, spineFixtureTime))
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/models",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("decode models: %v", err)
	}
	if body["object"] != "list" {
		t.Fatalf("object = %v, want list", body["object"])
	}
	data, _ := body["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("data len = %d, want 1 offerable model object", len(data))
	}
	model, _ := data[0].(map[string]any)
	if model["id"] != "gpt-4o-mini" {
		t.Fatalf("model id = %v, want gpt-4o-mini", model["id"])
	}
	x, _ := model["x_pixelplus"].(map[string]any)
	offers, _ := x["offers"].([]any)
	if len(offers) != 2 {
		t.Fatalf("offers len = %d, want 2 (chat + chat_streaming)", len(offers))
	}
	for _, raw := range offers {
		offer, _ := raw.(map[string]any)
		if offer["provider_account_id"] != "pa_models" {
			t.Fatalf("offer provider_account_id = %v, want pa_models", offer["provider_account_id"])
		}
		if offer["freshness"] != "fresh" {
			t.Fatalf("offer freshness = %v, want fresh", offer["freshness"])
		}
		if offer["offerable"] != true {
			t.Fatalf("offer offerable = %v, want true", offer["offerable"])
		}
		op := offer["operation"]
		if op != "chat" && op != "chat_streaming" {
			t.Fatalf("unexpected operation offer %v", op)
		}
	}
}

// AC: listModels requires capabilities.read; accounts.read alone is forbidden.
func TestListModelsRequiresCapabilitiesRead(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", activeProbedAccount("pa_models_scope", domain.AuthModeChatGPTCodexOAuth))
		h.capabilities.seed("tenant_a", sampleObservationSnapshot("pa_models_scope", domain.AuthModeChatGPTCodexOAuth, 1, spineFixtureTime))
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/models",
		bearer: readOnly,
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", response.StatusCode, payload)
	}
	body := decodeError(t, payload)
	if body["code"] != "forbidden" {
		t.Fatalf("code = %v, want forbidden", body["code"])
	}
}

// AC: observation failure prevents activation and stores no snapshot.
func TestProbeCapabilityObservationFailurePreventsActivation(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", probeableAccount("pa_obs_fail", domain.AuthModeChatGPTCodexOAuth))
		h.capability.observeErr = ports.ErrDependencyUnavailable
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_obs_fail/probe",
		bearer: tenantAKey,
		body:   `{}`,
	})
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body=%s)", response.StatusCode, payload)
	}
	if calls := harness.capabilities.putCalls.Load(); calls != 0 {
		t.Fatalf("capability.Put ran %d times after observation failure, want 0", calls)
	}
	// Account may be persisted as validated, but not active.
	response, payload = harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/provider-accounts/pa_obs_fail",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	var account map[string]any
	if err := json.Unmarshal(payload, &account); err != nil {
		t.Fatalf("decode account: %v", err)
	}
	if account["lifecycle_state"] == "active" {
		t.Fatalf("lifecycle_state became active after observation failure")
	}
}

// AC: management snapshot read returns stale evidence as 200 with freshness=stale.
func TestGetCapabilitySnapshotReturnsStaleForInspection(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", activeProbedAccount("pa_stale_read", domain.AuthModeChatGPTCodexOAuth))
		h.capabilities.seed("tenant_a", sampleObservationSnapshot("pa_stale_read", domain.AuthModeChatGPTCodexOAuth, 1, spineFixtureTime.Add(-30*time.Minute)))
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/provider-accounts/pa_stale_read/capability-snapshot",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapshot["freshness"] != "stale" {
		t.Fatalf("freshness = %v, want stale", snapshot["freshness"])
	}
}
