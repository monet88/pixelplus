package contracttest_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// countingRoutingPolicyStore is a Tenant-partitioned fixture store with mutation
// counters so public proofs can assert atomic Replace and zero writes on reject.
// It lives in contracttest_test to avoid forbidden infrastructure imports.
type countingRoutingPolicyStore struct {
	mu        sync.Mutex
	byTenant  map[domain.TenantID]domain.RoutingPolicy
	mutations atomic.Int64
	revision  atomic.Int64
	reads     atomic.Int64
	replaces  atomic.Int64
}

func newCountingRoutingPolicyStore() *countingRoutingPolicyStore {
	return &countingRoutingPolicyStore{
		byTenant: make(map[domain.TenantID]domain.RoutingPolicy),
	}
}

func (store *countingRoutingPolicyStore) Read(_ context.Context, principal domain.SecurityPrincipal) (domain.RoutingPolicy, error) {
	store.reads.Add(1)
	store.mu.Lock()
	defer store.mu.Unlock()
	policy, ok := store.byTenant[principal.TenantID]
	if !ok {
		return domain.RoutingPolicy{}, ports.ErrRoutingPolicyNotFound
	}
	return cloneFixturePolicy(policy), nil
}

func (store *countingRoutingPolicyStore) Replace(_ context.Context, change ports.RoutingPolicyChange) (domain.RoutingPolicy, error) {
	store.replaces.Add(1)
	store.mu.Lock()
	defer store.mu.Unlock()
	policy := cloneFixturePolicy(change.Policy)
	store.byTenant[change.Principal.TenantID] = policy
	store.mutations.Add(1)
	store.revision.Add(1)
	return cloneFixturePolicy(policy), nil
}

func (store *countingRoutingPolicyStore) Seed(tenant domain.TenantID, policy domain.RoutingPolicy) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.byTenant[tenant] = cloneFixturePolicy(policy)
}

func (store *countingRoutingPolicyStore) mutationsCount() int64 {
	return store.mutations.Load()
}

func (store *countingRoutingPolicyStore) revisionCount() int64 {
	return store.revision.Load()
}

func cloneFixturePolicy(policy domain.RoutingPolicy) domain.RoutingPolicy {
	out := policy
	out.CandidateAccounts = append([]domain.ProviderAccountID(nil), policy.CandidateAccounts...)
	out.SelectionOrder = append([]domain.ProviderAccountID(nil), policy.SelectionOrder...)
	out.FallbackChain = append([]domain.ProviderAccountID(nil), policy.FallbackChain...)
	out.FallbackAuthModes = append([]domain.AuthMode(nil), policy.FallbackAuthModes...)
	out.LeasePolicy.EligibleUnits = append([]domain.LeaseUnit(nil), policy.LeasePolicy.EligibleUnits...)
	if out.CandidateAccounts == nil {
		out.CandidateAccounts = []domain.ProviderAccountID{}
	}
	if out.SelectionOrder == nil {
		out.SelectionOrder = []domain.ProviderAccountID{}
	}
	if out.FallbackChain == nil {
		out.FallbackChain = []domain.ProviderAccountID{}
	}
	if out.FallbackAuthModes == nil {
		out.FallbackAuthModes = []domain.AuthMode{}
	}
	if out.LeasePolicy.EligibleUnits == nil {
		out.LeasePolicy.EligibleUnits = []domain.LeaseUnit{}
	}
	return out
}

func seedEligibleAccount(h *spineHarness, tenant domain.TenantID, id string, mode domain.AuthMode) {
	account := activeProbedAccount(id, mode)
	h.seedAccount(tenant, account)
	h.capabilities.seed(tenant, sampleObservationSnapshot(domain.ProviderAccountID(id), mode, 1, spineFixtureTime))
}

func validPolicyBody(accountIDs ...string) string {
	if len(accountIDs) == 0 {
		return `{
			"candidate_accounts": [],
			"selection_order": [],
			"fallback_enabled": false,
			"fallback_chain": [],
			"fallback_auth_modes": [],
			"affinity": {"enabled": false},
			"lease_policy": {"enabled": false, "eligible_units": []}
		}`
	}
	quoted := make([]string, len(accountIDs))
	for i, id := range accountIDs {
		quoted[i] = `"` + id + `"`
	}
	joined := strings.Join(quoted, ",")
	return `{
		"candidate_accounts": [` + joined + `],
		"selection_order": [` + joined + `],
		"fallback_enabled": false,
		"fallback_chain": [],
		"fallback_auth_modes": [],
		"affinity": {"enabled": true, "window_class": "AFFINITY-WINDOW-CLASS"},
		"lease_policy": {"enabled": false, "eligible_units": []}
	}`
}

func decodeRoutingPolicy(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("decode routing policy: %v (body=%s)", err, payload)
	}
	return body
}

// AC: missing policy fails closed to system default (empty candidates, fallback off).
func TestRoutingPolicyMissingFailsClosedDefault(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, nil)

	response, payload := harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/routing-policy",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	body := decodeRoutingPolicy(t, payload)
	if body["fallback_enabled"] != false {
		t.Fatalf("fallback_enabled = %v, want false", body["fallback_enabled"])
	}
	if chain, _ := body["fallback_chain"].([]any); len(chain) != 0 {
		t.Fatalf("fallback_chain = %v, want empty", chain)
	}
	if accounts, _ := body["candidate_accounts"].([]any); len(accounts) != 0 {
		t.Fatalf("candidate_accounts = %v, want empty", accounts)
	}
	if body["updated_by"] != "system_default" {
		t.Fatalf("updated_by = %v, want system_default", body["updated_by"])
	}
	if harness.vault.putCalls.Load() != 0 || harness.vault.validCalls.Load() != 0 {
		t.Fatalf("vault was accessed on GET routing policy")
	}
	if harness.probe.callCount.Load() != 0 {
		t.Fatalf("probe adapter was accessed on GET routing policy")
	}
}

// AC: Tenant A cannot read or mutate Tenant B policy; auth derives Tenant.
func TestRoutingPolicyTenantIsolation(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		seedEligibleAccount(h, "tenant_a", "pa_route_a", domain.AuthModeChatGPTCodexOAuth)
		seedEligibleAccount(h, "tenant_b", "pa_route_b", domain.AuthModeChatGPTCodexOAuth)
	})

	// Tenant B writes its own policy.
	response, payload := harness.do(t, requestSpec{
		method: http.MethodPut,
		path:   "/v1/routing-policy",
		bearer: tenantBKey,
		body:   validPolicyBody("pa_route_b"),
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("tenant B put status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}

	// Tenant A GET still fails closed empty (does not see B).
	response, payload = harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/routing-policy",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("tenant A get status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	body := decodeRoutingPolicy(t, payload)
	if accounts, _ := body["candidate_accounts"].([]any); len(accounts) != 0 {
		t.Fatalf("tenant A saw foreign candidates %v", accounts)
	}

	// Tenant A cannot put Tenant B account id.
	before := harness.routing.mutationsCount()
	response, payload = harness.do(t, requestSpec{
		method: http.MethodPut,
		path:   "/v1/routing-policy",
		bearer: tenantAKey,
		body:   validPolicyBody("pa_route_b"),
	})
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign candidate status = %d, want 404 (body=%s)", response.StatusCode, payload)
	}
	errBody := decodeError(t, payload)
	if errBody["code"] != "resource_not_found" {
		t.Fatalf("code = %v, want resource_not_found", errBody["code"])
	}
	if _, ok := errBody["resource_reference"]; ok {
		t.Fatalf("resource_reference must be omitted, got %v", errBody["resource_reference"])
	}
	if harness.routing.mutationsCount() != before {
		t.Fatalf("mutations changed on foreign reject: before=%d after=%d", before, harness.routing.mutationsCount())
	}
}

// AC: GET requires routing.read; PUT requires routing.manage.
func TestRoutingPolicyScopes(t *testing.T) {
	t.Parallel()

	const (
		routingRead   = "sk-pxp_locatorRR_secretRR"
		routingManage = "sk-pxp_locatorRM_secretRM"
		inferenceOnly = "sk-pxp_locatorInf_secretInf"
	)

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.principal.principals[routingRead] = domain.SecurityPrincipal{
			TenantID:       "tenant_a",
			ClientAPIKeyID: "key_rr",
			Scopes:         domain.NewScopeSet(domain.ScopeRoutingRead),
		}
		h.principal.principals[routingManage] = domain.SecurityPrincipal{
			TenantID:       "tenant_a",
			ClientAPIKeyID: "key_rm",
			Scopes:         domain.NewScopeSet(domain.ScopeRoutingManage),
		}
		h.principal.principals[inferenceOnly] = domain.SecurityPrincipal{
			TenantID:       "tenant_a",
			ClientAPIKeyID: "key_inf",
			Scopes:         domain.NewScopeSet(domain.ScopeCapabilitiesRead),
		}
		seedEligibleAccount(h, "tenant_a", "pa_scope_route", domain.AuthModeChatGPTCodexOAuth)
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/routing-policy",
		bearer: inferenceOnly,
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("GET without routing.read status = %d, want 403 (body=%s)", response.StatusCode, payload)
	}

	response, payload = harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/routing-policy",
		bearer: routingRead,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET with routing.read status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}

	response, payload = harness.do(t, requestSpec{
		method: http.MethodPut,
		path:   "/v1/routing-policy",
		bearer: routingRead,
		body:   validPolicyBody("pa_scope_route"),
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("PUT with only routing.read status = %d, want 403 (body=%s)", response.StatusCode, payload)
	}

	response, payload = harness.do(t, requestSpec{
		method: http.MethodPut,
		path:   "/v1/routing-policy",
		bearer: routingManage,
		body:   validPolicyBody("pa_scope_route"),
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("PUT with routing.manage status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	body := decodeRoutingPolicy(t, payload)
	if body["updated_by"] != "key_rm" {
		t.Fatalf("updated_by = %v, want key_rm", body["updated_by"])
	}
}

// AC: unauthenticated malformed/oversized PUT returns authentication_failed,
// not validation detail (A0 before A1/A2/shape).
func TestRoutingPolicyAuthBeforeValidation(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, nil)

	response, payload := harness.do(t, requestSpec{
		method:   http.MethodPut,
		path:     "/v1/routing-policy",
		skipAuth: true,
		body:     `{"tenant_id":"evil","not":"a policy"`,
	})
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated malformed status = %d, want 401 (body=%s)", response.StatusCode, payload)
	}
	if decodeError(t, payload)["code"] != "authentication_failed" {
		t.Fatalf("code = %v, want authentication_failed", decodeError(t, payload)["code"])
	}

	oversized := strings.Repeat("x", (2<<20)+8)
	response, payload = harness.do(t, requestSpec{
		method:   http.MethodPut,
		path:     "/v1/routing-policy",
		skipAuth: true,
		rawBody:  []byte(`{"candidate_accounts":["` + oversized + `"]}`),
	})
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated oversized status = %d, want 401 (body=%s)", response.StatusCode, payload)
	}
	if decodeError(t, payload)["code"] != "authentication_failed" {
		t.Fatalf("code = %v, want authentication_failed", decodeError(t, payload)["code"])
	}
}

// AC: strict body rejects additional fields; unique arrays and subset rules apply.
func TestRoutingPolicyStrictShapeAndSubsets(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		seedEligibleAccount(h, "tenant_a", "pa_shape", domain.AuthModeChatGPTCodexOAuth)
	})

	// additional field tenant_id
	response, payload := harness.do(t, requestSpec{
		method: http.MethodPut,
		path:   "/v1/routing-policy",
		bearer: tenantAKey,
		body: `{
			"candidate_accounts": ["pa_shape"],
			"selection_order": ["pa_shape"],
			"fallback_enabled": false,
			"fallback_chain": [],
			"fallback_auth_modes": [],
			"affinity": {"enabled": false},
			"lease_policy": {"enabled": false, "eligible_units": []},
			"tenant_id": "tenant_a"
		}`,
	})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("additional field status = %d, want 400 (body=%s)", response.StatusCode, payload)
	}

	// selection_order not subset of candidates
	response, payload = harness.do(t, requestSpec{
		method: http.MethodPut,
		path:   "/v1/routing-policy",
		bearer: tenantAKey,
		body: `{
			"candidate_accounts": ["pa_shape"],
			"selection_order": ["pa_shape", "pa_missing"],
			"fallback_enabled": false,
			"fallback_chain": [],
			"fallback_auth_modes": [],
			"affinity": {"enabled": false},
			"lease_policy": {"enabled": false, "eligible_units": []}
		}`,
	})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("subset violation status = %d, want 400 (body=%s)", response.StatusCode, payload)
	}

	// fallback disabled cannot authorize chain
	response, payload = harness.do(t, requestSpec{
		method: http.MethodPut,
		path:   "/v1/routing-policy",
		bearer: tenantAKey,
		body: `{
			"candidate_accounts": ["pa_shape"],
			"selection_order": ["pa_shape"],
			"fallback_enabled": false,
			"fallback_chain": ["pa_shape"],
			"fallback_auth_modes": [],
			"affinity": {"enabled": false},
			"lease_policy": {"enabled": false, "eligible_units": []}
		}`,
	})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("disabled fallback with chain status = %d, want 400 (body=%s)", response.StatusCode, payload)
	}

	// Grok Web SSO never accepted in fallback modes
	response, payload = harness.do(t, requestSpec{
		method: http.MethodPut,
		path:   "/v1/routing-policy",
		bearer: tenantAKey,
		body: `{
			"candidate_accounts": ["pa_shape"],
			"selection_order": ["pa_shape"],
			"fallback_enabled": true,
			"fallback_chain": ["pa_shape"],
			"fallback_auth_modes": ["grok_web_sso"],
			"affinity": {"enabled": false},
			"lease_policy": {"enabled": false, "eligible_units": []}
		}`,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("grok web sso mode status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	if decodeError(t, payload)["code"] != "auth_mode_unavailable" {
		t.Fatalf("code = %v, want auth_mode_unavailable", decodeError(t, payload)["code"])
	}
}

// AC: unknown/deleted candidates are resource_not_found with zero mutation;
// successful replace is atomic and increments store revision once.
func TestRoutingPolicyAtomicReplaceAndNotFound(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		seedEligibleAccount(h, "tenant_a", "pa_atomic", domain.AuthModeChatGPTCodexOAuth)
		deleted := activeProbedAccount("pa_deleted_route", domain.AuthModeChatGPTCodexOAuth)
		deleted.Lifecycle = domain.LifecycleDeleted
		h.seedAccount("tenant_a", deleted)
	})

	before := harness.routing.mutationsCount()
	beforeRev := harness.routing.revisionCount()

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPut,
		path:   "/v1/routing-policy",
		bearer: tenantAKey,
		body:   validPolicyBody("pa_unknown_xyz"),
	})
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown candidate status = %d, want 404 (body=%s)", response.StatusCode, payload)
	}
	if harness.routing.mutationsCount() != before || harness.routing.revisionCount() != beforeRev {
		t.Fatalf("store mutated on unknown candidate reject")
	}

	response, payload = harness.do(t, requestSpec{
		method: http.MethodPut,
		path:   "/v1/routing-policy",
		bearer: tenantAKey,
		body:   validPolicyBody("pa_deleted_route"),
	})
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted candidate status = %d, want 404 (body=%s)", response.StatusCode, payload)
	}
	if harness.routing.mutationsCount() != before {
		t.Fatalf("store mutated on deleted candidate reject")
	}

	response, payload = harness.do(t, requestSpec{
		method: http.MethodPut,
		path:   "/v1/routing-policy",
		bearer: tenantAKey,
		body:   validPolicyBody("pa_atomic"),
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("valid put status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	if harness.routing.mutationsCount() != before+1 {
		t.Fatalf("mutations = %d, want %d", harness.routing.mutationsCount(), before+1)
	}
	if harness.routing.revisionCount() != beforeRev+1 {
		t.Fatalf("revision = %d, want %d", harness.routing.revisionCount(), beforeRev+1)
	}
	if harness.vault.putCalls.Load() != 0 || harness.probe.callCount.Load() != 0 {
		t.Fatalf("vault/adapter accessed on routing replace")
	}

	// GET returns the written singleton.
	response, payload = harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/routing-policy",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("get after put status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	body := decodeRoutingPolicy(t, payload)
	accounts, _ := body["candidate_accounts"].([]any)
	if len(accounts) != 1 || accounts[0] != "pa_atomic" {
		t.Fatalf("candidate_accounts = %v, want [pa_atomic]", accounts)
	}
	if body["updated_by"] != "key_a" {
		t.Fatalf("updated_by = %v, want key_a", body["updated_by"])
	}
}

// AC: unusable / gated-without-ack / stale capability candidates reject with
// frozen classes and leave prior policy unchanged.
func TestRoutingPolicyCandidateEligibilityRejects(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		// Prior good policy seed via store so reject can prove no mutation.
		seedEligibleAccount(h, "tenant_a", "pa_good_prior", domain.AuthModeChatGPTCodexOAuth)
		h.routing.Seed("tenant_a", domain.RoutingPolicy{
			CandidateAccounts: []domain.ProviderAccountID{"pa_good_prior"},
			SelectionOrder:    []domain.ProviderAccountID{"pa_good_prior"},
			FallbackEnabled:   false,
			FallbackChain:     []domain.ProviderAccountID{},
			FallbackAuthModes: []domain.AuthMode{},
			Affinity:          domain.AffinityPolicy{Enabled: false},
			LeasePolicy:       domain.LeasePolicy{Enabled: false, EligibleUnits: []domain.LeaseUnit{}},
			UpdatedBy:         "seed",
		})

		// Active but missing risk ack for gated mode.
		noAck := activeProbedAccount("pa_no_ack", domain.AuthModeChatGPTCodexOAuth)
		noAck.RiskAcknowledged = false
		h.seedAccount("tenant_a", noAck)
		h.capabilities.seed("tenant_a", sampleObservationSnapshot("pa_no_ack", domain.AuthModeChatGPTCodexOAuth, 1, spineFixtureTime))

		// Stale snapshot.
		stale := activeProbedAccount("pa_stale_route", domain.AuthModeChatGPTCodexOAuth)
		h.seedAccount("tenant_a", stale)
		h.capabilities.seed("tenant_a", sampleObservationSnapshot("pa_stale_route", domain.AuthModeChatGPTCodexOAuth, 1, spineFixtureTime.Add(-30*time.Minute)))

		// Disabled lifecycle.
		disabled := activeProbedAccount("pa_disabled_route", domain.AuthModeChatGPTCodexOAuth)
		disabled.Lifecycle = domain.LifecycleDisabled
		h.seedAccount("tenant_a", disabled)
		h.capabilities.seed("tenant_a", sampleObservationSnapshot("pa_disabled_route", domain.AuthModeChatGPTCodexOAuth, 1, spineFixtureTime))
	})

	before := harness.routing.mutationsCount()

	// gated without ack
	response, payload := harness.do(t, requestSpec{
		method: http.MethodPut,
		path:   "/v1/routing-policy",
		bearer: tenantAKey,
		body:   validPolicyBody("pa_no_ack"),
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("no-ack status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	errBody := decodeError(t, payload)
	if errBody["code"] != "account_not_usable" {
		t.Fatalf("code = %v, want account_not_usable", errBody["code"])
	}
	if errBody["remediation"] != "ack_risk" {
		t.Fatalf("remediation = %v, want ack_risk", errBody["remediation"])
	}

	// stale
	response, payload = harness.do(t, requestSpec{
		method: http.MethodPut,
		path:   "/v1/routing-policy",
		bearer: tenantAKey,
		body:   validPolicyBody("pa_stale_route"),
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("stale status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	if decodeError(t, payload)["code"] != "snapshot_stale" {
		t.Fatalf("code = %v, want snapshot_stale", decodeError(t, payload)["code"])
	}

	// disabled
	response, payload = harness.do(t, requestSpec{
		method: http.MethodPut,
		path:   "/v1/routing-policy",
		bearer: tenantAKey,
		body:   validPolicyBody("pa_disabled_route"),
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("disabled status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	if decodeError(t, payload)["code"] != "account_not_usable" {
		t.Fatalf("code = %v, want account_not_usable", decodeError(t, payload)["code"])
	}

	if harness.routing.mutationsCount() != before {
		t.Fatalf("mutations changed on eligibility rejects: before=%d after=%d", before, harness.routing.mutationsCount())
	}

	// Prior policy still readable.
	response, payload = harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/routing-policy",
		bearer: tenantAKey,
	})
	body := decodeRoutingPolicy(t, payload)
	accounts, _ := body["candidate_accounts"].([]any)
	if len(accounts) != 1 || accounts[0] != "pa_good_prior" {
		t.Fatalf("prior policy lost: %v", accounts)
	}
}

// AC: /v1/models remains same-Tenant offerable pairs only; policy cannot widen.
func TestRoutingPolicyDoesNotWidenModels(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		seedEligibleAccount(h, "tenant_a", "pa_models_a", domain.AuthModeChatGPTCodexOAuth)
		// Foreign offerable account.
		seedEligibleAccount(h, "tenant_b", "pa_models_b", domain.AuthModeChatGPTCodexOAuth)
		// Write a same-Tenant policy (cannot name B).
		h.routing.Seed("tenant_a", domain.RoutingPolicy{
			CandidateAccounts: []domain.ProviderAccountID{"pa_models_a"},
			SelectionOrder:    []domain.ProviderAccountID{"pa_models_a"},
			FallbackEnabled:   false,
			FallbackChain:     []domain.ProviderAccountID{},
			FallbackAuthModes: []domain.AuthMode{},
			Affinity:          domain.AffinityPolicy{Enabled: false},
			LeasePolicy:       domain.LeasePolicy{Enabled: false, EligibleUnits: []domain.LeaseUnit{}},
			UpdatedBy:         "seed",
		})
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
	for _, item := range data {
		model, _ := item.(map[string]any)
		x, _ := model["x_pixelplus"].(map[string]any)
		offers, _ := x["offers"].([]any)
		for _, offerAny := range offers {
			offer, _ := offerAny.(map[string]any)
			if offer["provider_account_id"] == "pa_models_b" {
				t.Fatalf("models leaked foreign account via policy surface")
			}
		}
	}
}
