package contracttest_test

import (
	"context"
	"encoding/json"
	"fmt"
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
// Required wire fields updated_at (RFC3339 date-time) and updated_by are
// server-owned on the fail-closed singleton (domain SystemDefault*).
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
	for _, field := range []string{
		"candidate_accounts", "selection_order", "fallback_enabled", "fallback_chain",
		"fallback_auth_modes", "affinity", "lease_policy", "updated_at", "updated_by",
	} {
		if _, ok := body[field]; !ok {
			t.Fatalf("missing required RoutingPolicy field %q (body=%s)", field, payload)
		}
	}
	if body["fallback_enabled"] != false {
		t.Fatalf("fallback_enabled = %v, want false", body["fallback_enabled"])
	}
	if chain, _ := body["fallback_chain"].([]any); len(chain) != 0 {
		t.Fatalf("fallback_chain = %v, want empty", chain)
	}
	if accounts, _ := body["candidate_accounts"].([]any); len(accounts) != 0 {
		t.Fatalf("candidate_accounts = %v, want empty", accounts)
	}
	if modes, _ := body["fallback_auth_modes"].([]any); len(modes) != 0 {
		t.Fatalf("fallback_auth_modes = %v, want empty", modes)
	}
	if order, _ := body["selection_order"].([]any); len(order) != 0 {
		t.Fatalf("selection_order = %v, want empty", order)
	}
	if body["updated_by"] != string(domain.SystemDefaultUpdatedBy) {
		t.Fatalf("updated_by = %v, want exact %q", body["updated_by"], domain.SystemDefaultUpdatedBy)
	}
	updatedAt, ok := body["updated_at"].(string)
	if !ok || updatedAt == "" {
		t.Fatalf("updated_at = %v, want non-empty date-time string", body["updated_at"])
	}
	parsed, err := time.Parse(time.RFC3339, updatedAt)
	if err != nil {
		parsed, err = time.Parse(time.RFC3339Nano, updatedAt)
	}
	if err != nil {
		t.Fatalf("updated_at = %q is not RFC3339/RFC3339Nano: %v", updatedAt, err)
	}
	wantAt := domain.SystemDefaultUpdatedAt.Time().UTC()
	if !parsed.UTC().Equal(wantAt) {
		t.Fatalf("updated_at instant = %v, want deterministic system default %v", parsed.UTC(), wantAt)
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

// AC: missing/null required RoutingPolicyFields arrays are invalid_request;
// malformed provider_account_id is 400 before lookup; fallback_enabled=true
// with empty chain is invalid (spec §8.1 "fallback_chain when fallback_enabled").
func TestRoutingPolicyStrictRequestShape(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		seedEligibleAccount(h, "tenant_a", "pa_shape_ok", domain.AuthModeChatGPTCodexOAuth)
	})
	before := harness.routing.mutationsCount()
	beforeVisible := harness.accounts.visibleCalls.Load()

	cases := []struct {
		name string
		body string
	}{
		{
			name: "null_candidate_accounts",
			body: `{
				"candidate_accounts": null,
				"selection_order": [],
				"fallback_enabled": false,
				"fallback_chain": [],
				"fallback_auth_modes": [],
				"affinity": {"enabled": false},
				"lease_policy": {"enabled": false, "eligible_units": []}
			}`,
		},
		{
			name: "missing_fallback_chain",
			body: `{
				"candidate_accounts": [],
				"selection_order": [],
				"fallback_enabled": false,
				"fallback_auth_modes": [],
				"affinity": {"enabled": false},
				"lease_policy": {"enabled": false, "eligible_units": []}
			}`,
		},
		{
			name: "null_lease_eligible_units",
			body: `{
				"candidate_accounts": [],
				"selection_order": [],
				"fallback_enabled": false,
				"fallback_chain": [],
				"fallback_auth_modes": [],
				"affinity": {"enabled": false},
				"lease_policy": {"enabled": false, "eligible_units": null}
			}`,
		},
		{
			name: "malformed_provider_account_id",
			body: `{
				"candidate_accounts": ["not-a-pa-id"],
				"selection_order": ["not-a-pa-id"],
				"fallback_enabled": false,
				"fallback_chain": [],
				"fallback_auth_modes": [],
				"affinity": {"enabled": false},
				"lease_policy": {"enabled": false, "eligible_units": []}
			}`,
		},
		{
			// Authority: docs/spec/tenant-scoped-routing-fallback-affinity-leases.md
			// §8.1 "fallback_chain when fallback_enabled" + I-ROUTE-FALLBACK-OPTIN.
			name: "fallback_enabled_empty_chain",
			body: `{
				"candidate_accounts": ["pa_shape_ok"],
				"selection_order": ["pa_shape_ok"],
				"fallback_enabled": true,
				"fallback_chain": [],
				"fallback_auth_modes": [],
				"affinity": {"enabled": false},
				"lease_policy": {"enabled": false, "eligible_units": []}
			}`,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			response, payload := harness.do(t, requestSpec{
				method: http.MethodPut,
				path:   "/v1/routing-policy",
				bearer: tenantAKey,
				body:   tc.body,
			})
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%s)", response.StatusCode, payload)
			}
			if decodeError(t, payload)["code"] != "invalid_request" {
				t.Fatalf("code = %v, want invalid_request", decodeError(t, payload)["code"])
			}
		})
	}

	if harness.routing.mutationsCount() != before {
		t.Fatalf("store mutated on shape rejects")
	}
	if harness.accounts.visibleCalls.Load() != beforeVisible {
		t.Fatalf("account.Visible ran on pure shape rejects, want 0 extra")
	}
	if harness.vault.putCalls.Load() != 0 || harness.probe.callCount.Load() != 0 {
		t.Fatalf("vault/adapter accessed on shape rejects")
	}
}

// AC: over-quota admission rejects with 429 before candidate existence/
// capability validation (auth → scope → size/shape → admission → candidates).
func TestRoutingPolicyQuotaBeforeCandidateValidation(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.admission.rejectStage = ports.AdmissionStageQuota
	})
	beforeMut := harness.routing.mutationsCount()
	beforeVisible := harness.accounts.visibleCalls.Load()
	beforeCapGet := harness.capabilities.getCalls.Load()

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPut,
		path:   "/v1/routing-policy",
		bearer: tenantAKey,
		// Unknown candidate would 404 if validation ran before admission.
		body: validPolicyBody("pa_quota_unknown"),
	})
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 (body=%s)", response.StatusCode, payload)
	}
	if decodeError(t, payload)["code"] != "quota_exhausted" {
		t.Fatalf("code = %v, want quota_exhausted", decodeError(t, payload)["code"])
	}
	if harness.accounts.visibleCalls.Load() != beforeVisible {
		t.Fatalf("account.Visible ran before admission reject")
	}
	if harness.capabilities.getCalls.Load() != beforeCapGet {
		t.Fatalf("capability.Get ran before admission reject")
	}
	if harness.routing.mutationsCount() != beforeMut {
		t.Fatalf("routing store mutated on quota reject")
	}
	if harness.vault.putCalls.Load() != 0 || harness.probe.callCount.Load() != 0 {
		t.Fatalf("vault/adapter accessed on quota reject")
	}
}

// AC: foreign, unknown, and deleted candidate ids are non-enumerating
// resource_not_found with identical status/body (strip request_id only) and
// zero Vault/Adapter/routing-store mutation.
func TestRoutingPolicyCandidateNotFoundNonEnumeration(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		seedEligibleAccount(h, "tenant_b", "pa_foreign_enum", domain.AuthModeChatGPTCodexOAuth)
		deleted := activeProbedAccount("pa_deleted_enum", domain.AuthModeChatGPTCodexOAuth)
		deleted.Lifecycle = domain.LifecycleDeleted
		h.seedAccount("tenant_a", deleted)
	})
	beforeMut := harness.routing.mutationsCount()

	cases := []struct {
		name string
		id   string
	}{
		{name: "foreign", id: "pa_foreign_enum"},
		{name: "unknown", id: "pa_unknown_enum_xyz"},
		{name: "deleted", id: "pa_deleted_enum"},
	}

	var baseline map[string]any
	var baselineStatus int
	for i, tc := range cases {
		response, payload := harness.do(t, requestSpec{
			method: http.MethodPut,
			path:   "/v1/routing-policy",
			bearer: tenantAKey,
			body:   validPolicyBody(tc.id),
		})
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404 (body=%s)", tc.name, response.StatusCode, payload)
		}
		body := decodeError(t, payload)
		if body["code"] != "resource_not_found" {
			t.Fatalf("%s code = %v, want resource_not_found", tc.name, body["code"])
		}
		if _, ok := body["resource_reference"]; ok {
			t.Fatalf("%s leaked resource_reference: %v", tc.name, body["resource_reference"])
		}
		normalized := stripRequestID(body)
		if i == 0 {
			baseline = normalized
			baselineStatus = response.StatusCode
			continue
		}
		if response.StatusCode != baselineStatus {
			t.Fatalf("%s status diverged: got %d want %d", tc.name, response.StatusCode, baselineStatus)
		}
		if !mapsEqual(normalized, baseline) {
			t.Fatalf("%s body diverged after stripping request_id:\n got  %#v\n want %#v", tc.name, normalized, baseline)
		}
	}

	if harness.routing.mutationsCount() != beforeMut {
		t.Fatalf("routing store mutated on not-found rejects")
	}
	if harness.vault.putCalls.Load() != 0 || harness.probe.callCount.Load() != 0 {
		t.Fatalf("vault/adapter accessed on not-found rejects")
	}
}

func stripRequestID(body map[string]any) map[string]any {
	out := make(map[string]any, len(body))
	for k, v := range body {
		if k == "request_id" {
			continue
		}
		out[k] = v
	}
	return out
}

func mapsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok {
			return false
		}
		// JSON numbers decode as float64; strings compare directly.
		if fmt.Sprint(av) != fmt.Sprint(bv) {
			return false
		}
	}
	return true
}

// AC: selection_order ∪ fallback_chain multi-mode requires fallback_auth_modes
// to enumerate every distinct mode when fallback is enabled (invalid_request
// when incomplete). Authority: §8.1 "fallback_auth_modes when cross-mode
// fallback intended"; NF-XMODE.
func TestRoutingPolicyCrossModeRequiresDeclaredModes(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		seedEligibleAccount(h, "tenant_a", "pa_mode_a", domain.AuthModeChatGPTCodexOAuth)
		seedEligibleAccount(h, "tenant_a", "pa_mode_b", domain.AuthModeGrokXAIOAuth)
	})
	before := harness.routing.mutationsCount()

	// Two modes in selection_order + chain, but only one listed in fallback_auth_modes.
	body := `{
		"candidate_accounts": ["pa_mode_a", "pa_mode_b"],
		"selection_order": ["pa_mode_a", "pa_mode_b"],
		"fallback_enabled": true,
		"fallback_chain": ["pa_mode_b"],
		"fallback_auth_modes": ["chatgpt_codex_oauth"],
		"affinity": {"enabled": false},
		"lease_policy": {"enabled": false, "eligible_units": []}
	}`
	response, payload := harness.do(t, requestSpec{
		method: http.MethodPut,
		path:   "/v1/routing-policy",
		bearer: tenantAKey,
		body:   body,
	})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", response.StatusCode, payload)
	}
	if decodeError(t, payload)["code"] != "invalid_request" {
		t.Fatalf("code = %v, want invalid_request", decodeError(t, payload)["code"])
	}
	if harness.routing.mutationsCount() != before {
		t.Fatalf("store mutated on incomplete cross-mode declaration")
	}

	// Complete declaration succeeds.
	bodyOK := `{
		"candidate_accounts": ["pa_mode_a", "pa_mode_b"],
		"selection_order": ["pa_mode_a", "pa_mode_b"],
		"fallback_enabled": true,
		"fallback_chain": ["pa_mode_b"],
		"fallback_auth_modes": ["chatgpt_codex_oauth", "grok_xai_oauth"],
		"affinity": {"enabled": false},
		"lease_policy": {"enabled": false, "eligible_units": []}
	}`
	response, payload = harness.do(t, requestSpec{
		method: http.MethodPut,
		path:   "/v1/routing-policy",
		bearer: tenantAKey,
		body:   bodyOK,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("complete cross-mode status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
}

// AC (normative correction): multi-mode selection_order with fallback_enabled=false
// and empty fallback_chain/fallback_auth_modes MUST succeed. fallback_auth_modes
// is required only for cross-mode *fallback* (§8.1 "when cross-mode fallback
// intended"; shape already requires empty modes when fallback is off).
// On 2da8446 this is red: validation required modes whenever |modeSet|>1.
func TestRoutingPolicyMultiModeSelectionFallbackOffSucceeds(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		seedEligibleAccount(h, "tenant_a", "pa_sel_a", domain.AuthModeChatGPTCodexOAuth)
		seedEligibleAccount(h, "tenant_a", "pa_sel_b", domain.AuthModeGrokXAIOAuth)
	})
	before := harness.routing.mutationsCount()

	body := `{
		"candidate_accounts": ["pa_sel_a", "pa_sel_b"],
		"selection_order": ["pa_sel_a", "pa_sel_b"],
		"fallback_enabled": false,
		"fallback_chain": [],
		"fallback_auth_modes": [],
		"affinity": {"enabled": false},
		"lease_policy": {"enabled": false, "eligible_units": []}
	}`
	response, payload := harness.do(t, requestSpec{
		method: http.MethodPut,
		path:   "/v1/routing-policy",
		bearer: tenantAKey,
		body:   body,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("multi-mode selection with fallback off status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	got := decodeRoutingPolicy(t, payload)
	if got["fallback_enabled"] != false {
		t.Fatalf("fallback_enabled = %v, want false", got["fallback_enabled"])
	}
	order, _ := got["selection_order"].([]any)
	if len(order) != 2 {
		t.Fatalf("selection_order = %v, want two accounts", order)
	}
	if harness.routing.mutationsCount() != before+1 {
		t.Fatalf("mutations = %d, want %d", harness.routing.mutationsCount(), before+1)
	}
}

// AC: allowlist tri-state — empty deny-all is 403 for visible same-Tenant;
// foreign stays 404. Ownership precedes allowlist.
func TestRoutingPolicyAllowlistTriState(t *testing.T) {
	t.Parallel()

	const allowKey = "sk-pxp_locatorAL_secretAL"
	emptyList := []domain.ProviderAccountID{}
	onlyOne := []domain.ProviderAccountID{"pa_allow_ok"}

	harness := newSpineHarness(t, func(h *spineHarness) {
		seedEligibleAccount(h, "tenant_a", "pa_allow_ok", domain.AuthModeChatGPTCodexOAuth)
		seedEligibleAccount(h, "tenant_a", "pa_allow_blocked", domain.AuthModeChatGPTCodexOAuth)
		seedEligibleAccount(h, "tenant_b", "pa_allow_foreign", domain.AuthModeChatGPTCodexOAuth)
		h.principal.principals[allowKey] = domain.SecurityPrincipal{
			TenantID:                 "tenant_a",
			ClientAPIKeyID:           "key_al",
			Scopes:                   domain.NewScopeSet(domain.ScopeRoutingRead, domain.ScopeRoutingManage),
			ProviderAccountAllowlist: &onlyOne,
		}
	})

	// Visible same-Tenant but not on allowlist → 403.
	response, payload := harness.do(t, requestSpec{
		method: http.MethodPut,
		path:   "/v1/routing-policy",
		bearer: allowKey,
		body:   validPolicyBody("pa_allow_blocked"),
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("allowlist block status = %d, want 403 (body=%s)", response.StatusCode, payload)
	}
	if decodeError(t, payload)["code"] != "forbidden" {
		t.Fatalf("code = %v, want forbidden", decodeError(t, payload)["code"])
	}

	// Foreign still 404 (non-enumeration beats allowlist).
	response, payload = harness.do(t, requestSpec{
		method: http.MethodPut,
		path:   "/v1/routing-policy",
		bearer: allowKey,
		body:   validPolicyBody("pa_allow_foreign"),
	})
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign under allowlist status = %d, want 404 (body=%s)", response.StatusCode, payload)
	}

	// Allowed id succeeds.
	response, payload = harness.do(t, requestSpec{
		method: http.MethodPut,
		path:   "/v1/routing-policy",
		bearer: allowKey,
		body:   validPolicyBody("pa_allow_ok"),
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("allowlist member status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}

	// Explicit empty allowlist deny-all → 403 for any same-Tenant id.
	denyKey := "sk-pxp_locatorDN_secretDN"
	harness.principal.principals[denyKey] = domain.SecurityPrincipal{
		TenantID:                 "tenant_a",
		ClientAPIKeyID:           "key_dn",
		Scopes:                   domain.NewScopeSet(domain.ScopeRoutingManage, domain.ScopeRoutingRead),
		ProviderAccountAllowlist: &emptyList,
	}
	response, payload = harness.do(t, requestSpec{
		method: http.MethodPut,
		path:   "/v1/routing-policy",
		bearer: denyKey,
		body:   validPolicyBody("pa_allow_ok"),
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("deny-all status = %d, want 403 (body=%s)", response.StatusCode, payload)
	}
}

// AC: lifecycle=active with effective health unknown is not eligible.
func TestRoutingPolicyRejectsActiveUnknownHealth(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeProbedAccount("pa_unknown_h", domain.AuthModeChatGPTCodexOAuth)
		account.Health = domain.HealthSummary{
			SummaryState: domain.HealthUnknown,
			Conditions: []domain.HealthCondition{{
				Scope:             domain.HealthScope{Kind: domain.HealthScopeAccount},
				State:             domain.HealthUnknown,
				Reason:            domain.HealthReasonInitialUnprobed,
				CredentialVersion: account.Credential.Version,
				Remediation:       domain.RemediationNone,
				SourceClass:       domain.HealthSourceRequiredProbe,
			}},
		}
		h.seedAccount("tenant_a", account)
		h.capabilities.seed("tenant_a", sampleObservationSnapshot("pa_unknown_h", domain.AuthModeChatGPTCodexOAuth, 1, spineFixtureTime))
	})
	response, payload := harness.do(t, requestSpec{
		method: http.MethodPut,
		path:   "/v1/routing-policy",
		bearer: tenantAKey,
		body:   validPolicyBody("pa_unknown_h"),
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	if decodeError(t, payload)["code"] != "account_not_usable" {
		t.Fatalf("code = %v, want account_not_usable", decodeError(t, payload)["code"])
	}
}

// AC: experimental Auth Modes fail closed in production (no lab profile).
func TestRoutingPolicyRejectsExperimentalAuthMode(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		// ChatGPT Web Access is experimental under the risk envelope.
		seedEligibleAccount(h, "tenant_a", "pa_exp_web", domain.AuthModeChatGPTWebAccess)
	})
	response, payload := harness.do(t, requestSpec{
		method: http.MethodPut,
		path:   "/v1/routing-policy",
		bearer: tenantAKey,
		body:   validPolicyBody("pa_exp_web"),
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	if decodeError(t, payload)["code"] != "auth_mode_unavailable" {
		t.Fatalf("code = %v, want auth_mode_unavailable", decodeError(t, payload)["code"])
	}
}

// AC: CircuitStore.SurfaceOpen dependency error is dependency_unavailable (503),
// not capability_unsupported.
func TestRoutingPolicyCircuitDependencyUnavailable(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		seedEligibleAccount(h, "tenant_a", "pa_circ_dep", domain.AuthModeChatGPTCodexOAuth)
		h.circuits.queryErr = ports.ErrDependencyUnavailable
	})
	response, payload := harness.do(t, requestSpec{
		method: http.MethodPut,
		path:   "/v1/routing-policy",
		bearer: tenantAKey,
		body:   validPolicyBody("pa_circ_dep"),
	})
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body=%s)", response.StatusCode, payload)
	}
	if decodeError(t, payload)["code"] != "dependency_unavailable" {
		t.Fatalf("code = %v, want dependency_unavailable", decodeError(t, payload)["code"])
	}
}

// Characterization / acceptance proofs for mandatory candidate rejection classes
// (C2/C5 / administrative controls / open circuit). Public Runtime.Handler only.

// AC: account-scoped non-routable Health (cooling_down) rejects candidate with
// account_not_usable + wait_provider_cooldown (NewProviderCooldownBlocked).
func TestRoutingPolicyRejectsScopedHealthBlock(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeProbedAccount("pa_health_block", domain.AuthModeChatGPTCodexOAuth)
		account.Health = domain.HealthSummary{
			SummaryState: domain.HealthCoolingDown,
			Conditions: []domain.HealthCondition{{
				Scope:             domain.HealthScope{Kind: domain.HealthScopeAccount},
				State:             domain.HealthCoolingDown,
				Reason:            domain.HealthReasonProviderRateLimited,
				CredentialVersion: account.Credential.Version,
				Remediation:       domain.RemediationWaitProviderCooldown,
				SourceClass:       domain.HealthSourceUpstreamAttempt,
			}},
		}
		h.seedAccount("tenant_a", account)
		h.capabilities.seed("tenant_a", sampleObservationSnapshot("pa_health_block", domain.AuthModeChatGPTCodexOAuth, 1, spineFixtureTime))
	})
	before := harness.routing.mutationsCount()
	response, payload := harness.do(t, requestSpec{
		method: http.MethodPut,
		path:   "/v1/routing-policy",
		bearer: tenantAKey,
		body:   validPolicyBody("pa_health_block"),
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	errBody := decodeError(t, payload)
	if errBody["code"] != "account_not_usable" {
		t.Fatalf("code = %v, want account_not_usable", errBody["code"])
	}
	if errBody["remediation"] != "wait_provider_cooldown" {
		t.Fatalf("remediation = %v, want wait_provider_cooldown", errBody["remediation"])
	}
	if harness.routing.mutationsCount() != before {
		t.Fatalf("store mutated on health reject")
	}
}

// AC: hard administrative controls (drain / quarantine) reject with
// account_not_usable / account_remediation and zero mutation.
func TestRoutingPolicyRejectsDrainAndQuarantine(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		mut  func(*domain.ProviderAccount)
	}{
		{
			name: "drain",
			mut: func(a *domain.ProviderAccount) {
				a.Controls.Drain = domain.DrainDraining
			},
		},
		{
			name: "quarantine",
			mut: func(a *domain.ProviderAccount) {
				a.Controls.Quarantine = domain.QuarantineQuarantined
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			id := "pa_ctrl_" + tc.name
			harness := newSpineHarness(t, func(h *spineHarness) {
				account := activeProbedAccount(id, domain.AuthModeChatGPTCodexOAuth)
				tc.mut(&account)
				h.seedAccount("tenant_a", account)
				h.capabilities.seed("tenant_a", sampleObservationSnapshot(domain.ProviderAccountID(id), domain.AuthModeChatGPTCodexOAuth, 1, spineFixtureTime))
			})
			before := harness.routing.mutationsCount()
			response, payload := harness.do(t, requestSpec{
				method: http.MethodPut,
				path:   "/v1/routing-policy",
				bearer: tenantAKey,
				body:   validPolicyBody(id),
			})
			if response.StatusCode != http.StatusConflict {
				t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
			}
			errBody := decodeError(t, payload)
			if errBody["code"] != "account_not_usable" {
				t.Fatalf("code = %v, want account_not_usable", errBody["code"])
			}
			if errBody["remediation"] != "account_remediation" {
				t.Fatalf("remediation = %v, want account_remediation", errBody["remediation"])
			}
			if harness.routing.mutationsCount() != before {
				t.Fatalf("store mutated on %s reject", tc.name)
			}
		})
	}
}

// AC: candidate eligibility uses one request-start instant for freshness and
// pair offerability (ListModels precedent). At the TTL boundary, a multi-Now
// path can see Fresh then later unofferable pairs and wrongly return
// capability_unsupported; the request-start instant that is still within TTL
// must accept (authority-derived success), never flip mid-request.
func TestRoutingPolicyCandidateFreshnessUsesRequestStartInstant(t *testing.T) {
	t.Parallel()

	// Snapshot verified at fixture time; clock starts one second before
	// DefaultProbeLiveTTL expiry. Each Now() advances 1s: sc.start is still
	// Fresh, but a second Now() for IsOfferablePair crosses the budget and
	// makes every pair unofferable under the multi-instant bug.
	verifiedAt := spineFixtureTime
	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeProbedAccount("pa_ttl_edge", domain.AuthModeChatGPTCodexOAuth)
		h.seedAccount("tenant_a", account)
		h.capabilities.seed("tenant_a", sampleObservationSnapshot(
			"pa_ttl_edge", domain.AuthModeChatGPTCodexOAuth, 1, verifiedAt,
		))
		// Position clock so request-start is within TTL, but a later Now() is not.
		h.clock.mu.Lock()
		h.clock.now = verifiedAt.Add(domain.DefaultProbeLiveTTL - time.Second)
		h.clock.mu.Unlock()
	})
	before := harness.routing.mutationsCount()
	response, payload := harness.do(t, requestSpec{
		method: http.MethodPut,
		path:   "/v1/routing-policy",
		bearer: tenantAKey,
		body:   validPolicyBody("pa_ttl_edge"),
	})
	// Normative: sc.start is still within DefaultProbeLiveTTL → candidate eligible.
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 at request-start-fresh boundary (body=%s)", response.StatusCode, payload)
	}
	if harness.routing.mutationsCount() != before+1 {
		t.Fatalf("mutations = %d, want %d", harness.routing.mutationsCount(), before+1)
	}
}

// AC: open Provider Surface Circuit omits all offerable pairs → candidate is
// capability_unsupported (distinct from circuit dependency 503).
func TestRoutingPolicyRejectsOpenCircuit(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		seedEligibleAccount(h, "tenant_a", "pa_circ_open", domain.AuthModeChatGPTCodexOAuth)
		// Open circuit on the live-probe surface used by sampleObservationSnapshot
		// so every offerable pair is omitted (capability_unsupported).
		h.circuits.set(ports.CircuitSurface{
			Provider: domain.ProviderChatGPT,
			AuthMode: domain.AuthModeChatGPTCodexOAuth,
			Surface:  "/backend-api/models",
		}, ports.CircuitState{Open: true})
	})
	before := harness.routing.mutationsCount()
	response, payload := harness.do(t, requestSpec{
		method: http.MethodPut,
		path:   "/v1/routing-policy",
		bearer: tenantAKey,
		body:   validPolicyBody("pa_circ_open"),
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	errBody := decodeError(t, payload)
	if errBody["code"] != "capability_unsupported" {
		t.Fatalf("code = %v, want capability_unsupported (open circuit omits all pairs)", errBody["code"])
	}
	if harness.routing.mutationsCount() != before {
		t.Fatalf("store mutated on open-circuit reject")
	}
}
