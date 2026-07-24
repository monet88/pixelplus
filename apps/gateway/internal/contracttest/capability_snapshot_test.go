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
		h.seedAccount("tenant_a", probeableAccount("pa_cap", domain.AuthModeChatGPTCodexOAuth))
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
	modelOps, _ := model["operations"].(map[string]any)
	for _, op := range []string{"chat", "chat_streaming", "image_generation", "image_edit", "inpaint"} {
		if _, ok := modelOps[op]; !ok {
			t.Fatalf("model operations missing %s", op)
		}
	}
}

// AC: missing snapshot returns capability_unverified without vault decrypt.
func TestGetCapabilitySnapshotMissingIsUnverified(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.seedAccount("tenant_a", usableDraft("pa_nosnap", domain.AuthModeChatGPTCodexOAuth))
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
		h.seedAccount("tenant_b", activeProbedAccount("pa_foreign_cap", domain.AuthModeChatGPTCodexOAuth))
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

	const (
		capsOnly   = "sk-pxp_locatorC_secretC"
		assetsOnly = "sk-pxp_locatorZ_secretZ"
	)

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.principal.principals[capsOnly] = domain.SecurityPrincipal{
			TenantID:       "tenant_a",
			ClientAPIKeyID: "key_c",
			Scopes:         domain.NewScopeSet(domain.ScopeCapabilitiesRead),
		}
		h.principal.principals[assetsOnly] = domain.SecurityPrincipal{
			TenantID:       "tenant_a",
			ClientAPIKeyID: "key_z",
			Scopes:         domain.NewScopeSet(domain.ScopeAssetsRead, domain.ScopeAssetsWrite),
		}
		h.seedAccount("tenant_a", activeProbedAccount("pa_scope", domain.AuthModeChatGPTCodexOAuth))
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

	// neither accounts.read nor capabilities.read
	response, payload = harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/provider-accounts/pa_scope/capability-snapshot",
		bearer: assetsOnly,
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("missing both scopes status = %d, want 403 (body=%s)", response.StatusCode, payload)
	}
	body := decodeError(t, payload)
	if body["code"] != "forbidden" {
		t.Fatalf("code = %v, want forbidden", body["code"])
	}
}

// AC: listModels returns only fresh offerable pairs for the authenticated
// Tenant; stale/invalid/foreign pairs are omitted.
func TestListModelsOnlyFreshOfferablePairs(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		active := activeProbedAccount("pa_models", domain.AuthModeChatGPTCodexOAuth)
		h.seedAccount("tenant_a", active)
		h.capabilities.seed("tenant_a", sampleObservationSnapshot("pa_models", domain.AuthModeChatGPTCodexOAuth, 1, spineFixtureTime))

		// Stale snapshot for another same-Tenant account must not offer.
		staleAccount := activeProbedAccount("pa_stale", domain.AuthModeChatGPTCodexOAuth)
		h.seedAccount("tenant_a", staleAccount)
		stale := sampleObservationSnapshot("pa_stale", domain.AuthModeChatGPTCodexOAuth, 1, spineFixtureTime.Add(-30*time.Minute))
		h.capabilities.seed("tenant_a", stale)

		// Foreign tenant snapshot must never leak.
		h.seedAccount("tenant_b", activeProbedAccount("pa_foreign_models", domain.AuthModeChatGPTCodexOAuth))
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

// AC: an operation-scoped cooldown removes only the matching offer. The account
// summary may still report cooling_down because it is the worst scoped state,
// but that summary must not flatten image_generation evidence into unrelated
// chat/chat_streaming buckets (§3.8, Example A, I-HEALTH-SCOPED).
func TestListModelsHonorsMatchingScopedHealth(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		accountID := domain.ProviderAccountID("pa_scoped_health")
		account := activeProbedAccount(string(accountID), domain.AuthModeChatGPTCodexOAuth)
		account = account.WithScopedCooldown(
			domain.NewTimestamp(spineFixtureTime),
			domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpImageGeneration)},
			domain.HealthReasonProviderRateLimited,
			domain.NewTimestamp(spineFixtureTime.Add(time.Minute)),
		)
		h.seedAccount("tenant_a", account)
		h.capabilities.seed("tenant_a", sampleObservationSnapshot(accountID, domain.AuthModeChatGPTCodexOAuth, 1, spineFixtureTime))
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
	data, _ := body["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("data len = %d, want chat model preserved by unrelated image cooldown", len(data))
	}
	model, _ := data[0].(map[string]any)
	x, _ := model["x_pixelplus"].(map[string]any)
	offers, _ := x["offers"].([]any)
	if len(offers) != 2 {
		t.Fatalf("offers len = %d, want chat + chat_streaming preserved", len(offers))
	}
}

// AC: a Provider Surface Circuit blocks only its matching operation and never
// rewrites the owning account's lifecycle, health, or administrative controls.
func TestListModelsHonorsScopedProviderSurfaceCircuit(t *testing.T) {
	t.Parallel()

	accountID := domain.ProviderAccountID("pa_circuit")
	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeProbedAccount(string(accountID), domain.AuthModeChatGPTCodexOAuth)
		h.seedAccount("tenant_a", account)
		h.capabilities.seed("tenant_a", sampleObservationSnapshot(accountID, domain.AuthModeChatGPTCodexOAuth, 1, spineFixtureTime))
		h.circuits.set(ports.CircuitSurface{
			Provider:  domain.ProviderChatGPT,
			AuthMode:  domain.AuthModeChatGPTCodexOAuth,
			Surface:   "/backend-api/models",
			Operation: domain.CapabilityOpChat,
		}, ports.CircuitState{Open: true})
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/models",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("models status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("decode models: %v", err)
	}
	data, _ := body["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("data len = %d, want 1 model with the non-matching offer", len(data))
	}
	model, _ := data[0].(map[string]any)
	x, _ := model["x_pixelplus"].(map[string]any)
	offers, _ := x["offers"].([]any)
	if len(offers) != 1 {
		t.Fatalf("offers len = %d, want only chat_streaming", len(offers))
	}
	offer, _ := offers[0].(map[string]any)
	if offer["operation"] != "chat_streaming" {
		t.Fatalf("remaining operation = %v, want chat_streaming", offer["operation"])
	}
	if calls := harness.circuits.callCount.Load(); calls != 2 {
		t.Fatalf("circuit checks = %d, want 2 offerable pairs", calls)
	}

	response, payload = harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/provider-accounts/" + string(accountID),
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("account status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	var account map[string]any
	if err := json.Unmarshal(payload, &account); err != nil {
		t.Fatalf("decode account: %v", err)
	}
	if account["lifecycle_state"] != "active" {
		t.Fatalf("lifecycle_state = %v, want active", account["lifecycle_state"])
	}
	health, _ := account["health"].(map[string]any)
	if health["summary_state"] != "healthy" {
		t.Fatalf("health.summary_state = %v, want healthy", health["summary_state"])
	}
	controls, _ := account["administrative_controls"].(map[string]any)
	if controls["drain_state"] != "off" || controls["quarantine_state"] != "off" || controls["auth_mode_execution_enabled"] != true {
		t.Fatalf("administrative_controls changed after circuit gate: %v", controls)
	}
}

// AC: a wired but unavailable circuit store fails closed without exposing its
// corroborating evidence or allowing a possibly-open matching surface.
func TestListModelsFailsClosedWhenCircuitStateUnavailable(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		accountID := domain.ProviderAccountID("pa_circuit_unavailable")
		h.seedAccount("tenant_a", activeProbedAccount(string(accountID), domain.AuthModeChatGPTCodexOAuth))
		h.capabilities.seed("tenant_a", sampleObservationSnapshot(accountID, domain.AuthModeChatGPTCodexOAuth, 1, spineFixtureTime))
		h.circuits.queryErr = ports.ErrCircuitUnavailable
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/models",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want safe 200 projection (body=%s)", response.StatusCode, payload)
	}
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("decode models: %v", err)
	}
	data, _ := body["data"].([]any)
	if len(data) != 0 {
		t.Fatalf("data len = %d, want 0 when circuit state is unavailable", len(data))
	}
}

// AC: version-mismatched snapshot remains inspectable but never offerable.
func TestListModelsOmitsCredentialVersionMismatch(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeProbedAccount("pa_version_bind", domain.AuthModeChatGPTCodexOAuth)
		// Current account is version 2; stored snapshot is still bound to v1.
		account.Credential.Version = 2
		h.seedAccount("tenant_a", account)
		h.capabilities.seed("tenant_a", sampleObservationSnapshot("pa_version_bind", domain.AuthModeChatGPTCodexOAuth, 1, spineFixtureTime))
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/models",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("models status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("decode models: %v", err)
	}
	data, _ := body["data"].([]any)
	if len(data) != 0 {
		t.Fatalf("data len = %d, want 0 for version-mismatched snapshot", len(data))
	}

	response, payload = harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/provider-accounts/pa_version_bind/capability-snapshot",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("snapshot status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if version, _ := snapshot["credential_version"].(float64); version != 1 {
		t.Fatalf("credential_version = %v, want 1 stored evidence", snapshot["credential_version"])
	}
}

// AC: hard-block health / drain / quarantine keep /v1/models empty even when
// the lifecycle is still active and the snapshot is fresh.
func TestListModelsOmitsNonRoutableActiveAccounts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		mut  func(*domain.ProviderAccount)
	}{
		{
			name: "blocked",
			mut: func(account *domain.ProviderAccount) {
				account.Health.SummaryState = domain.HealthBlocked
				account.Health.Conditions = []domain.HealthCondition{{
					Scope:             domain.HealthScope{Kind: domain.HealthScopeAccount},
					State:             domain.HealthBlocked,
					Reason:            domain.HealthReasonProviderAccountBanned,
					CredentialVersion: account.Credential.Version,
					Remediation:       domain.RemediationContactOperator,
				}}
			},
		},
		{
			name: "cooling_down",
			mut: func(account *domain.ProviderAccount) {
				account.Health.SummaryState = domain.HealthCoolingDown
				account.Health.Conditions = []domain.HealthCondition{{
					Scope:             domain.HealthScope{Kind: domain.HealthScopeAccount},
					State:             domain.HealthCoolingDown,
					Reason:            domain.HealthReasonProviderRateLimited,
					CredentialVersion: account.Credential.Version,
					Remediation:       domain.RemediationWaitProviderCooldown,
					ConditionRevision: 1,
					BackoffLevel:      1,
				}}
			},
		},
		{
			name: "draining",
			mut: func(account *domain.ProviderAccount) {
				account.Controls.Drain = domain.DrainDraining
			},
		},
		{
			name: "quarantined",
			mut: func(account *domain.ProviderAccount) {
				account.Controls.Quarantine = domain.QuarantineQuarantined
			},
		},
		{
			name: "tenant_disabled",
			mut: func(account *domain.ProviderAccount) {
				*account = account.WithDisabled(domain.NewTimestamp(spineFixtureTime.Add(time.Second)))
			},
		},
		{
			name: "auth_mode_killed",
			mut: func(account *domain.ProviderAccount) {
				account.Controls.AuthModeExecutionEnabled = false
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			accountID := domain.ProviderAccountID("pa_nonroute_" + tc.name)
			harness := newSpineHarness(t, func(h *spineHarness) {
				account := activeProbedAccount(string(accountID), domain.AuthModeChatGPTCodexOAuth)
				tc.mut(&account)
				h.seedAccount("tenant_a", account)
				h.capabilities.seed("tenant_a", sampleObservationSnapshot(accountID, domain.AuthModeChatGPTCodexOAuth, 1, spineFixtureTime))
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
			data, _ := body["data"].([]any)
			if len(data) != 0 {
				t.Fatalf("data len = %d, want 0 for non-routable account %s", len(data), tc.name)
			}

			// Inspect remains available, but fact-level offerable is false.
			response, payload = harness.do(t, requestSpec{
				method: http.MethodGet,
				path:   "/v1/provider-accounts/" + string(accountID) + "/capability-snapshot",
				bearer: tenantAKey,
			})
			if response.StatusCode != http.StatusOK {
				t.Fatalf("snapshot status = %d, want 200 (body=%s)", response.StatusCode, payload)
			}
			var snapshot map[string]any
			if err := json.Unmarshal(payload, &snapshot); err != nil {
				t.Fatalf("decode snapshot: %v", err)
			}
			operations, _ := snapshot["operations"].(map[string]any)
			chat, _ := operations["chat"].(map[string]any)
			if chat["offerable"] != false {
				t.Fatalf("chat.offerable = %v, want false for non-usable account", chat["offerable"])
			}
		})
	}
}

// AC #51 / PR #81: GET capability-snapshot with a matching open CircuitStore
// remains 200 inspectable. Matching operation fact is offerable=false; an
// unrelated operation stays offerable=true. Circuit evidence is never exposed.
func TestGetCapabilitySnapshotHonorsMatchingOpenCircuit(t *testing.T) {
	t.Parallel()

	accountID := domain.ProviderAccountID("pa_snap_circuit")
	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeProbedAccount(string(accountID), domain.AuthModeChatGPTCodexOAuth)
		h.seedAccount("tenant_a", account)
		h.capabilities.seed("tenant_a", sampleObservationSnapshot(accountID, domain.AuthModeChatGPTCodexOAuth, 1, spineFixtureTime))
		// Open circuit only for chat (matches sample snapshot probe surface).
		h.circuits.set(ports.CircuitSurface{
			Provider:  domain.ProviderChatGPT,
			AuthMode:  domain.AuthModeChatGPTCodexOAuth,
			Surface:   "/backend-api/models",
			Operation: domain.CapabilityOpChat,
		}, ports.CircuitState{Open: true})
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/provider-accounts/" + string(accountID) + "/capability-snapshot",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 inspectable snapshot (body=%s)", response.StatusCode, payload)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	// Circuit gate is applied privately: no circuit_* / open-circuit keys on the wire.
	for _, leak := range []string{"circuit", "circuit_state", "circuit_open", "surface_open", "open_until"} {
		if _, ok := snapshot[leak]; ok {
			t.Fatalf("snapshot top-level leaked %q: %s", leak, payload)
		}
	}
	operations, _ := snapshot["operations"].(map[string]any)
	chat, _ := operations["chat"].(map[string]any)
	image, _ := operations["image_generation"].(map[string]any)
	if chat["offerable"] != false {
		t.Fatalf("chat.offerable = %v, want false for matching open circuit", chat["offerable"])
	}
	if image["offerable"] != true {
		t.Fatalf("image_generation.offerable = %v, want true for unrelated operation", image["offerable"])
	}
	// chat_streaming shares the same probe surface; open circuit is scoped by Operation=chat only.
	stream, _ := operations["chat_streaming"].(map[string]any)
	if stream["offerable"] != true {
		t.Fatalf("chat_streaming.offerable = %v, want true (circuit scoped to chat operation)", stream["offerable"])
	}
	if harness.circuits.callCount.Load() == 0 {
		t.Fatal("circuit SurfaceOpen never called")
	}
}

// Unreadable CircuitStore: snapshot stays 200 inspectable but every operation
// fact is fail-closed offerable=false without exposing circuit errors/evidence.
func TestGetCapabilitySnapshotFailsClosedWhenCircuitUnreadable(t *testing.T) {
	t.Parallel()

	accountID := domain.ProviderAccountID("pa_snap_circuit_unavail")
	harness := newSpineHarness(t, func(h *spineHarness) {
		h.seedAccount("tenant_a", activeProbedAccount(string(accountID), domain.AuthModeChatGPTCodexOAuth))
		h.capabilities.seed("tenant_a", sampleObservationSnapshot(accountID, domain.AuthModeChatGPTCodexOAuth, 1, spineFixtureTime))
		h.circuits.queryErr = ports.ErrCircuitUnavailable
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/provider-accounts/" + string(accountID) + "/capability-snapshot",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 inspectable (body=%s)", response.StatusCode, payload)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	for _, leak := range []string{"circuit", "circuit_state", "circuit_open", "error", "code", "dependency_unavailable"} {
		if _, ok := snapshot[leak]; ok {
			t.Fatalf("snapshot top-level leaked %q on unreadable circuit: %s", leak, payload)
		}
	}
	operations, _ := snapshot["operations"].(map[string]any)
	if len(operations) == 0 {
		t.Fatal("operations empty; want inspectable facts with offerable=false")
	}
	for name, raw := range operations {
		fact, _ := raw.(map[string]any)
		if fact["offerable"] != false {
			t.Fatalf("operations[%s].offerable = %v, want false when circuit unreadable", name, fact["offerable"])
		}
	}
}

// AC #51: management inspection stays available but must not advertise an
// operation as offerable when a matching operation-scoped cooldown blocks it.
// An unrelated operation remains truthful and offerable.
func TestGetCapabilitySnapshotHonorsMatchingScopedHealth(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeProbedAccount("pa_snapshot_scoped_health", domain.AuthModeChatGPTCodexOAuth)
		account = account.WithScopedCooldown(
			domain.NewTimestamp(spineFixtureTime),
			domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpImageGeneration)},
			domain.HealthReasonProviderRateLimited,
			domain.NewTimestamp(spineFixtureTime.Add(time.Minute)),
		)
		h.seedAccount("tenant_a", account)
		h.capabilities.seed("tenant_a", sampleObservationSnapshot(account.ID, account.AuthMode, account.Credential.Version, spineFixtureTime))
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/provider-accounts/pa_snapshot_scoped_health/capability-snapshot",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 inspectable snapshot (body=%s)", response.StatusCode, payload)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	operations, _ := snapshot["operations"].(map[string]any)
	chat, _ := operations["chat"].(map[string]any)
	image, _ := operations["image_generation"].(map[string]any)
	if chat["offerable"] != true {
		t.Fatalf("chat.offerable = %v, want true for unrelated operation", chat["offerable"])
	}
	if image["offerable"] != false {
		t.Fatalf("image_generation.offerable = %v, want false for matching cooldown", image["offerable"])
	}
}

// AC: dual-level status projection advertises the weaker of model and op facts.
func TestListModelsProjectsWeakerOperationStatus(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeProbedAccount("pa_weaker", domain.AuthModeChatGPTCodexOAuth)
		h.seedAccount("tenant_a", account)
		snapshot := sampleObservationSnapshot("pa_weaker", domain.AuthModeChatGPTCodexOAuth, 1, spineFixtureTime)
		// Model says verified chat; account-level chat fact is only conditional.
		snapshot.Operations[domain.CapabilityOpChat] = domain.CapabilityFact{
			Status:        domain.CapabilityConditionallySupported,
			EvidenceClass: domain.EvidenceLiveProbe,
			ProbeSurface:  "/backend-api/models",
		}
		h.capabilities.seed("tenant_a", snapshot)
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
	data, _ := body["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("data len = %d, want 1", len(data))
	}
	model, _ := data[0].(map[string]any)
	x, _ := model["x_pixelplus"].(map[string]any)
	offers, _ := x["offers"].([]any)
	var chatStatus string
	for _, raw := range offers {
		offer, _ := raw.(map[string]any)
		if offer["operation"] == "chat" {
			chatStatus, _ = offer["operation_status"].(string)
		}
	}
	if chatStatus != "conditionally_supported" {
		t.Fatalf("chat operation_status = %q, want conditionally_supported", chatStatus)
	}
}

// AC: listModels requires capabilities.read; accounts.read alone is forbidden.
func TestListModelsRequiresCapabilitiesRead(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.seedAccount("tenant_a", activeProbedAccount("pa_models_scope", domain.AuthModeChatGPTCodexOAuth))
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
		h.seedAccount("tenant_a", probeableAccount("pa_obs_fail", domain.AuthModeChatGPTCodexOAuth))
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

// AC: CapabilityStore.Put failure after successful Observe still blocks activation
// and leaves the account non-authorizing for offers.
func TestProbeCapabilityStorePutFailurePreventsActivation(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.seedAccount("tenant_a", probeableAccount("pa_put_fail", domain.AuthModeChatGPTCodexOAuth))
		h.capabilities.putErr = ports.ErrDependencyUnavailable
		h.capabilities.putHook = func() {
			concurrent, err := h.accounts.Visible(t.Context(), managePrincipal(), "pa_put_fail")
			if err != nil {
				t.Errorf("concurrent Visible: %v", err)
				return
			}
			concurrent.Lifecycle = domain.LifecycleDisabled
			concurrent.Controls.Quarantine = domain.QuarantineQuarantined
			if _, err := h.accounts.Update(t.Context(), ports.AccountUpdate{
				Principal: managePrincipal(), Account: concurrent,
			}); err != nil {
				t.Errorf("concurrent control update: %v", err)
			}
		}
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_put_fail/probe",
		bearer: tenantAKey,
		body:   `{}`,
	})
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body=%s)", response.StatusCode, payload)
	}
	if calls := harness.capability.callCount.Load(); calls != 1 {
		t.Fatalf("capability.Observe ran %d times, want 1", calls)
	}
	if calls := harness.capabilities.putCalls.Load(); calls != 1 {
		t.Fatalf("capability.Put ran %d times, want 1", calls)
	}

	response, payload = harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/provider-accounts/pa_put_fail",
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
		t.Fatalf("lifecycle_state became active after capability put failure")
	}
	if account["lifecycle_state"] != "disabled" {
		t.Fatalf("lifecycle_state = %v, want concurrent disabled state preserved", account["lifecycle_state"])
	}
	stored, err := harness.accounts.Visible(t.Context(), managePrincipal(), "pa_put_fail")
	if err != nil {
		t.Fatalf("visible after capability failure: %v", err)
	}
	if stored.Controls.Quarantine != domain.QuarantineQuarantined {
		t.Fatalf("quarantine = %v, want concurrent quarantine preserved", stored.Controls.Quarantine)
	}

	// No durable authorizing snapshot was published.
	response, payload = harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/provider-accounts/pa_put_fail/capability-snapshot",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("snapshot status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	body := decodeError(t, payload)
	if body["code"] != "capability_unverified" {
		t.Fatalf("code = %v, want capability_unverified", body["code"])
	}
}

// AC: management snapshot read returns stale evidence as 200 with freshness=stale.
func TestGetCapabilitySnapshotReturnsStaleForInspection(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.seedAccount("tenant_a", activeProbedAccount("pa_stale_read", domain.AuthModeChatGPTCodexOAuth))
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
