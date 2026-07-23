package contracttest_test

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/contracttest"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// AC1 (Health State and Health Reason remain separate and conditions retain
// account, operation, or model scope with version/revision fencing): an account
// carrying an account-scope healthy condition plus a narrower operation-scope
// cooling_down condition projects both through the management read. The summary
// severity reflects the worst matching scope, yet each condition keeps its own
// scope, state, and reason so a narrow rate limit never looks like a total
// account outage. The internal fencing fields (condition_revision, backoff_level,
// retry_not_before, source_class) drive concurrency safety but are never
// projected onto the frozen wire schema.
func TestScopedHealthConditionsProjectSeparatelyWithoutFencingFields(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_scoped_health", domain.AuthModeChatGPTCodexOAuth)
		// A narrower operation-scope cooldown coexists with the account-scope
		// probe_succeeded evidence. State (cooling_down) and reason
		// (provider_rate_limited) are distinct axes, and the condition carries
		// internal fencing metadata that must not reach the wire.
		account.Health.SummaryState = domain.HealthCoolingDown
		account.Health.Conditions = append(account.Health.Conditions, domain.HealthCondition{
			Scope: domain.HealthScope{
				Kind:      domain.HealthScopeOperation,
				Operation: string(domain.CapabilityOpImageGeneration),
			},
			State:             domain.HealthCoolingDown,
			Reason:            domain.HealthReasonProviderRateLimited,
			CredentialVersion: 1,
			ObservedAt:        domain.NewTimestamp(spineFixtureTime),
			Remediation:       domain.RemediationWaitProviderCooldown,
			ConditionRevision: 5,
			BackoffLevel:      2,
			RetryNotBefore:    domain.NewTimestamp(spineFixtureTime.Add(30_000_000_000)), // +30s
			SourceClass:       domain.HealthSourceUpstreamAttempt,
		})
		h.accounts.seed("tenant_a", account)
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/provider-accounts/pa_scoped_health",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}

	// GET returns the account projection directly (no operation envelope).
	account := decodeError(t, payload)
	health, _ := account["health"].(map[string]any)
	if health["summary_state"] != "cooling_down" {
		t.Fatalf("summary_state = %v, want cooling_down (worst matching scope)", health["summary_state"])
	}
	conditions, _ := health["conditions"].([]any)
	if len(conditions) != 2 {
		t.Fatalf("conditions len = %d, want 2 (account + operation scope preserved)", len(conditions))
	}

	// Locate the operation-scope condition and assert state/reason orthogonality
	// plus scope retention.
	var opCondition map[string]any
	for _, raw := range conditions {
		condition, _ := raw.(map[string]any)
		scope, _ := condition["scope"].(map[string]any)
		if scope["kind"] == "operation" {
			opCondition = condition
			break
		}
	}
	if opCondition == nil {
		t.Fatalf("operation-scope condition missing; conditions = %v", conditions)
	}
	scope, _ := opCondition["scope"].(map[string]any)
	if scope["operation"] != string(domain.CapabilityOpImageGeneration) {
		t.Fatalf("scope.operation = %v, want image_generation", scope["operation"])
	}
	if opCondition["state"] != "cooling_down" {
		t.Fatalf("state = %v, want cooling_down", opCondition["state"])
	}
	if opCondition["reason"] != "provider_rate_limited" {
		t.Fatalf("reason = %v, want provider_rate_limited (state and reason are separate axes)", opCondition["reason"])
	}

	// The internal fencing fields drive CAS/backoff but are not part of the frozen
	// wire schema. They must never leak onto the public projection.
	for _, forbidden := range []string{"condition_revision", "backoff_level", "retry_not_before", "source_class", "provider_reset_at", "resolved_at"} {
		if _, ok := opCondition[forbidden]; ok {
			t.Fatalf("wire leaked internal fencing field %q; condition = %v", forbidden, opCondition)
		}
	}
}

// AC2 (rate/quota signals create durable scoped cooldowns) — Example A model/
// operation cooldown: a probe that authenticates but carries a validated
// operation-bucket rate-limit signal activates the account (auth proven) yet
// overlays a durable cooling_down condition at the evidenced operation scope.
// The account-scope condition stays healthy (chat remains routable), the
// summary reflects the worst matching scope, and the cooldown survives a
// re-read through the store (durable, not in-request only).
func TestProbeRateSignalCreatesDurableScopedCooldown(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", probeableAccount("pa_cooldown_op", domain.AuthModeChatGPTCodexOAuth))
		// The probe proves authentication AND surfaces a validated operation-bucket
		// rate-limit signal. The evidenced scope is operation image_generation, so
		// the narrowest proven scope is operation, never the whole account.
		h.probe.outcome = ports.ProbeOutcome{
			Authenticated: true,
			Signal:        ports.ProbeSignalRateLimited,
			SignalScope: domain.HealthScope{
				Kind:      domain.HealthScopeOperation,
				Operation: string(domain.CapabilityOpImageGeneration),
			},
			RetryAfterSeconds: 30,
		}
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_cooldown_op/probe",
		bearer: tenantAKey,
		body:   `{"scope":{"kind":"operation","operation":"image_generation"}}`,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}

	// Probe operation returns the AccountOperationResponse envelope.
	account := decodeAccount(t, payload)
	// Auth proved usable, so the account activates; the cooldown is a scoped
	// overlay, not a credential failure that blocks the whole account.
	if account["lifecycle_state"] != "active" {
		t.Fatalf("lifecycle_state = %v, want active (auth proven; cooldown is scoped)", account["lifecycle_state"])
	}
	health, _ := account["health"].(map[string]any)
	if health["summary_state"] != "cooling_down" {
		t.Fatalf("summary_state = %v, want cooling_down (worst matching scope)", health["summary_state"])
	}

	conditions, _ := health["conditions"].([]any)
	var opCondition, accountCondition map[string]any
	for _, raw := range conditions {
		condition, _ := raw.(map[string]any)
		scope, _ := condition["scope"].(map[string]any)
		switch scope["kind"] {
		case "operation":
			opCondition = condition
		case "account":
			accountCondition = condition
		}
	}
	if opCondition == nil {
		t.Fatalf("operation-scope cooldown missing; conditions = %v", conditions)
	}
	scope, _ := opCondition["scope"].(map[string]any)
	if scope["operation"] != "image_generation" {
		t.Fatalf("cooldown scope.operation = %v, want image_generation", scope["operation"])
	}
	if opCondition["state"] != "cooling_down" {
		t.Fatalf("cooldown state = %v, want cooling_down", opCondition["state"])
	}
	if opCondition["reason"] != "provider_rate_limited" {
		t.Fatalf("cooldown reason = %v, want provider_rate_limited", opCondition["reason"])
	}
	if opCondition["remediation"] != "wait_provider_cooldown" {
		t.Fatalf("cooldown remediation = %v, want wait_provider_cooldown", opCondition["remediation"])
	}
	if retryAfter, _ := opCondition["retry_after_seconds"].(float64); retryAfter < 1 || retryAfter > 30 {
		t.Fatalf("retry_after_seconds = %v, want finite value in [1,30]", opCondition["retry_after_seconds"])
	}
	// Example A: the account-scope evidence stays healthy so chat remains routable;
	// the cooldown never flattens into an account-wide failure.
	if accountCondition == nil {
		t.Fatalf("account-scope condition missing (chat must remain routable); conditions = %v", conditions)
	}
	if accountCondition["state"] != "healthy" {
		t.Fatalf("account-scope state = %v, want healthy (only image_generation cooled)", accountCondition["state"])
	}

	// Durable in the account row: a fresh HTTP read through the same composed
	// runtime observes the persisted scoped cooldown. Restart/readiness restoration
	// is proven separately by TestCooldownRestoresBeforeRestartedFixtureIsReady.
	getResponse, getPayload := harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/provider-accounts/pa_cooldown_op",
		bearer: tenantAKey,
	})
	if getResponse.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d, want 200 (body=%s)", getResponse.StatusCode, getPayload)
	}
	reread := decodeError(t, getPayload)
	rereadHealth, _ := reread["health"].(map[string]any)
	if rereadHealth["summary_state"] != "cooling_down" {
		t.Fatalf("re-read summary_state = %v, want cooling_down (cooldown must be durable)", rereadHealth["summary_state"])
	}
	rereadConditions, _ := rereadHealth["conditions"].([]any)
	foundOp := false
	for _, raw := range rereadConditions {
		condition, _ := raw.(map[string]any)
		scope, _ := condition["scope"].(map[string]any)
		if scope["kind"] == "operation" && condition["state"] == "cooling_down" {
			foundOp = true
		}
	}
	if !foundOp {
		t.Fatalf("durable operation cooldown missing on re-read; conditions = %v", rereadConditions)
	}
}

func TestMalformedProviderRetryHintUsesSafePolicyWaitAndAuditsClassification(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		signal     ports.ProbeSignalClass
		retryAfter int
	}{
		{name: "rate_over_plausibility_bound", signal: ports.ProbeSignalRateLimited, retryAfter: int((48 * time.Hour) / time.Second)},
		{name: "quota_over_plausibility_bound", signal: ports.ProbeSignalQuotaExhausted, retryAfter: int((32 * 24 * time.Hour) / time.Second)},
		{name: "duration_overflow", signal: ports.ProbeSignalRateLimited, retryAfter: int(^uint(0) >> 1)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			harness := newSpineHarness(t, func(h *spineHarness) {
				h.accounts.seed("tenant_a", probeableAccount("pa_malformed_hint_"+tc.name, domain.AuthModeChatGPTCodexOAuth))
				h.probe.outcome = ports.ProbeOutcome{
					Authenticated:     true,
					Signal:            tc.signal,
					SignalScope:       domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpImageGeneration)},
					RetryAfterSeconds: tc.retryAfter,
				}
			})

			response, payload := harness.do(t, requestSpec{
				method: http.MethodPost,
				path:   "/v1/provider-accounts/pa_malformed_hint_" + tc.name + "/probe",
				bearer: tenantAKey,
				body:   `{"scope":{"kind":"operation","operation":"image_generation"}}`,
			})
			if response.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200 with safe policy fallback (body=%s)", response.StatusCode, payload)
			}

			principal := harness.principal.principals[tenantAKey]
			stored, err := harness.accounts.Visible(t.Context(), principal, domain.ProviderAccountID("pa_malformed_hint_"+tc.name))
			if err != nil {
				t.Fatalf("Visible() error = %v", err)
			}
			var cooldown domain.HealthCondition
			for _, condition := range stored.Health.Conditions {
				if condition.Scope.Kind == domain.HealthScopeOperation {
					cooldown = condition
					break
				}
			}
			if cooldown.RetryNotBefore.IsZero() || !cooldown.RetryNotBefore.Time().After(spineFixtureTime) {
				t.Fatalf("retry_not_before = %v, want future policy wait", cooldown.RetryNotBefore.Time())
			}
			if cooldown.RetryNotBefore.Time().After(spineFixtureTime.Add(quotaCooldownTestCeiling)) {
				t.Fatalf("retry_not_before = %v, want bounded policy fallback", cooldown.RetryNotBefore.Time())
			}

			foundClassification := false
			for _, event := range harness.audit.snapshot() {
				if event.Action == ports.AuditProviderHintMalformed && event.Outcome == "malformed_provider_hint" {
					foundClassification = true
				}
			}
			if !foundClassification {
				t.Fatal("missing operator-visible malformed Provider hint audit classification")
			}
		})
	}
}

const quotaCooldownTestCeiling = 24 * time.Hour

// AC2 restart proof: a second real composition over the same durable AccountStore
// restores cooldown state before /readyz opens. The restarted HTTP surface must
// reject a matching probe before Vault or Adapter work, proving restart cannot
// erase Provider backoff.
func TestCooldownRestoresBeforeRestartedFixtureIsReady(t *testing.T) {
	accounts := newStubAccountStore(&spineLog{})
	account := activeAccount("pa_restart_cooldown", domain.AuthModeChatGPTCodexOAuth)
	account = account.WithScopedCooldown(
		domain.NewTimestamp(spineFixtureTime),
		domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpImageGeneration)},
		domain.HealthReasonProviderRateLimited,
		domain.NewTimestamp(spineFixtureTime.Add(time.Minute)),
	)
	accounts.seed("tenant_a", account)

	first := newSpineHarness(t, func(h *spineHarness) {
		h.accounts = accounts
	})
	if first.fixture.Runtime().Ready() != true {
		t.Fatal("first runtime not ready after account restore")
	}
	closeFixture(t, first.fixture)

	second := newSpineHarness(t, func(h *spineHarness) {
		h.accounts = accounts
	})
	if second.fixture.Runtime().Ready() != true {
		t.Fatal("restarted runtime not ready after account restore")
	}

	response, payload := second.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_restart_cooldown/probe",
		bearer: tenantAKey,
		body:   `{"scope":{"kind":"operation","operation":"image_generation"}}`,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("probe status = %d, want 409 after restart (body=%s)", response.StatusCode, payload)
	}
	body := decodeError(t, payload)
	if body["retry_after_class"] != "provider_cooldown" {
		t.Fatalf("retry_after_class = %v, want provider_cooldown", body["retry_after_class"])
	}
	if calls := second.vault.validCalls.Load(); calls != 0 {
		t.Fatalf("vault validate calls = %d, want 0 after restored cooldown gate", calls)
	}
	if calls := second.probe.callCount.Load(); calls != 0 {
		t.Fatalf("probe adapter calls = %d, want 0 after restored cooldown gate", calls)
	}
}

// AC2 fail-closed proof: if durable Provider Account state is unreadable during
// startup, health remains live, readiness stays closed, and direct product traffic
// cannot treat an empty/partial store as "no cooldown" or reach protected work.
func TestUnreadableCooldownDurabilityKeepsReadinessClosed(t *testing.T) {
	accounts := newStubAccountStore(&spineLog{})
	accounts.restoreErr = ports.ErrDependencyUnavailable
	accounts.seed("tenant_a", activeAccount("pa_unreadable_cooldown", domain.AuthModeChatGPTCodexOAuth))
	vault := newStubCredentialVault(&spineLog{})
	probe := newStubProbeAdapter(&spineLog{})
	principal := &stubPrincipalStore{
		log: &spineLog{},
		principals: map[string]domain.SecurityPrincipal{
			tenantAKey: {
				TenantID:       "tenant_a",
				ClientAPIKeyID: "key_a",
				Scopes:         domain.NewScopeSet(domain.ScopeAccountsRead, domain.ScopeAccountsManage, domain.ScopeCapabilitiesRead),
			},
		},
	}
	fixture, err := contracttest.NewFixture(contracttest.Options{
		Principal: principal,
		Accounts:  accounts,
		Vault:     vault,
		Probe:     probe,
	})
	if err != nil {
		t.Fatalf("NewFixture() error = %v", err)
	}
	t.Cleanup(func() {
		closeFixture(t, fixture)
	})

	response, err := fixture.Client().Get(fixture.URL() + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz status = %d, want 503", response.StatusCode)
	}

	probeResponse, err := fixture.Client().Post(
		fixture.URL()+"/v1/provider-accounts/pa_unreadable_cooldown/probe",
		"application/json",
		strings.NewReader(`{}`),
	)
	if err != nil {
		t.Fatalf("POST probe error = %v", err)
	}
	probeResponse.Body.Close()
	if probeResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated probe status = %d, want 401 before account durability lookup", probeResponse.StatusCode)
	}

	request, err := http.NewRequest(http.MethodPost, fixture.URL()+"/v1/provider-accounts/pa_unreadable_cooldown/probe", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+tenantAKey)
	request.Header.Set("Content-Type", "application/json")
	probeResponse, err = fixture.Client().Do(request)
	if err != nil {
		t.Fatalf("authenticated POST probe error = %v", err)
	}
	defer probeResponse.Body.Close()
	if probeResponse.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("authenticated probe status = %d, want 503 when account durability is unreadable", probeResponse.StatusCode)
	}
	if calls := vault.validCalls.Load(); calls != 0 {
		t.Fatalf("vault validate calls = %d, want 0 while account durability is unreadable", calls)
	}
	if calls := probe.callCount.Load(); calls != 0 {
		t.Fatalf("probe calls = %d, want 0 while account durability is unreadable", calls)
	}
}

// AC6: finite retry timing is suppressed whenever a non-time gate dominates,
// even if another scoped cooldown has a known retry_not_before.
func TestNonTimeHealthGateSuppressesRetryTiming(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		mut  func(*domain.ProviderAccount)
	}{
		{
			name: "blocked_health",
			mut: func(account *domain.ProviderAccount) {
				account.Health.SummaryState = domain.HealthBlocked
				account.Health.Conditions = append(account.Health.Conditions, domain.HealthCondition{
					Scope:             domain.HealthScope{Kind: domain.HealthScopeAccount},
					State:             domain.HealthBlocked,
					Reason:            domain.HealthReasonProviderAccountBanned,
					CredentialVersion: account.Credential.Version,
					ObservedAt:        domain.NewTimestamp(spineFixtureTime),
					Remediation:       domain.RemediationContactOperator,
				})
			},
		},
		{
			name: "disabled_lifecycle",
			mut: func(account *domain.ProviderAccount) {
				*account = account.WithDisabled(domain.NewTimestamp(spineFixtureTime))
			},
		},
		{
			name: "quarantine",
			mut: func(account *domain.ProviderAccount) {
				account.Controls.Quarantine = domain.QuarantineQuarantined
			},
		},
		{
			name: "auth_mode_kill",
			mut: func(account *domain.ProviderAccount) {
				account.Controls.AuthModeExecutionEnabled = false
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			accountID := domain.ProviderAccountID("pa_retry_suppressed_" + tc.name)
			harness := newSpineHarness(t, func(h *spineHarness) {
				account := activeProbedAccount(string(accountID), domain.AuthModeChatGPTCodexOAuth)
				account = account.WithScopedCooldown(
					domain.NewTimestamp(spineFixtureTime),
					domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpChat)},
					domain.HealthReasonProviderRateLimited,
					domain.NewTimestamp(spineFixtureTime.Add(30*time.Second)),
				)
				tc.mut(&account)
				h.accounts.seed("tenant_a", account)
			})

			response, payload := harness.do(t, requestSpec{
				method: http.MethodGet,
				path:   "/v1/provider-accounts/" + string(accountID),
				bearer: tenantAKey,
			})
			if response.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
			}
			account := decodeError(t, payload)
			health, _ := account["health"].(map[string]any)
			conditions, _ := health["conditions"].([]any)
			for _, raw := range conditions {
				condition, _ := raw.(map[string]any)
				if _, ok := condition["retry_after_seconds"]; ok {
					t.Fatalf("retry_after_seconds must be suppressed by %s: %v", tc.name, condition)
				}
			}
		})
	}
}

func TestProbeRateSignalWithoutHintUsesBoundedPolicyCooldown(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", probeableAccount("pa_cooldown_default", domain.AuthModeChatGPTCodexOAuth))
		h.probe.outcome = ports.ProbeOutcome{
			Authenticated: true,
			Signal:        ports.ProbeSignalRateLimited,
			SignalScope: domain.HealthScope{
				Kind:      domain.HealthScopeOperation,
				Operation: string(domain.CapabilityOpImageGeneration),
			},
		}
	})

	probe := func() (*http.Response, []byte) {
		return harness.do(t, requestSpec{
			method: http.MethodPost,
			path:   "/v1/provider-accounts/pa_cooldown_default/probe",
			bearer: tenantAKey,
			body:   `{"scope":{"kind":"operation","operation":"image_generation"}}`,
		})
	}

	response, payload := probe()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("first status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	account := decodeAccount(t, payload)
	health, _ := account["health"].(map[string]any)
	conditions, _ := health["conditions"].([]any)
	var retryAfter float64
	for _, raw := range conditions {
		condition, _ := raw.(map[string]any)
		scope, _ := condition["scope"].(map[string]any)
		if scope["kind"] == "operation" && scope["operation"] == "image_generation" {
			retryAfter, _ = condition["retry_after_seconds"].(float64)
		}
	}
	if retryAfter < 29 || retryAfter > 33 {
		t.Fatalf("retry_after_seconds = %v, want H-TRANSIENT-COOLDOWN-BASE plus bounded deterministic jitter at response time", retryAfter)
	}

	secondResponse, secondPayload := probe()
	if secondResponse.StatusCode != http.StatusConflict {
		t.Fatalf("second status = %d, want 409 before policy retry_not_before (body=%s)", secondResponse.StatusCode, secondPayload)
	}
	if calls := harness.probe.callCount.Load(); calls != 1 {
		t.Fatalf("probe calls = %d, want 1; no-hint cooldown must not open immediately", calls)
	}
}

// AC2 (unknown scope fails account-wide) — Example B: a probe that authenticates
// but carries a rate-limit signal with no safe bucket evidence normalizes to an
// account-scope cooldown, pausing all new work on the account rather than a
// single operation. The unknown/empty evidenced scope MUST widen, never guess a
// narrower bucket (spec §6.3, I-HEALTH-SCOPED).
func TestProbeRateSignalUnknownScopeNormalizesAccountWide(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", probeableAccount("pa_cooldown_unknown", domain.AuthModeChatGPTCodexOAuth))
		// The probe proves authentication but the rate-limit signal carries no safe
		// bucket metadata: the evidenced scope kind is empty/unknown.
		h.probe.outcome = ports.ProbeOutcome{
			Authenticated:     true,
			Signal:            ports.ProbeSignalRateLimited,
			SignalScope:       domain.HealthScope{},
			RetryAfterSeconds: 30,
		}
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_cooldown_unknown/probe",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}

	account := decodeAccount(t, payload)
	health, _ := account["health"].(map[string]any)
	if health["summary_state"] != "cooling_down" {
		t.Fatalf("summary_state = %v, want cooling_down", health["summary_state"])
	}

	conditions, _ := health["conditions"].([]any)
	// Unknown bucket → the cooldown MUST be account scope. No operation/model
	// condition may be invented from evidence that never proved a narrower bucket.
	var accountCooling bool
	for _, raw := range conditions {
		condition, _ := raw.(map[string]any)
		scope, _ := condition["scope"].(map[string]any)
		if scope["kind"] == "account" && condition["state"] == "cooling_down" {
			accountCooling = true
		}
		if scope["kind"] == "operation" || scope["kind"] == "model" {
			t.Fatalf("narrower scope invented from unknown bucket: %v", condition)
		}
	}
	if !accountCooling {
		t.Fatalf("account-wide cooldown missing for unknown bucket; conditions = %v", conditions)
	}
}

// scopedCooldownAccount builds an active account carrying its account-scope
// healthy evidence plus two operation-scope cooldowns, mirroring an account that
// activated and then took two independent rate-limit signals. It is the fixture
// for recovery-scope and no-stale-clear assertions.
func scopedCooldownAccount(id string, mode domain.AuthMode) domain.ProviderAccount {
	account := activeAccount(id, mode)
	// image_generation cooling with an already-elapsed retry_not_before so a
	// timer-only heal would (wrongly) look eligible.
	account = account.WithScopedCooldown(
		domain.NewTimestamp(spineFixtureTime),
		domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpImageGeneration)},
		domain.HealthReasonProviderRateLimited,
		domain.NewTimestamp(spineFixtureTime), // retry_not_before == observed_at (elapsed)
	)
	// chat cooling at a distinct operation scope.
	account = account.WithScopedCooldown(
		domain.NewTimestamp(spineFixtureTime),
		domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpChat)},
		domain.HealthReasonProviderRateLimited,
		domain.NewTimestamp(spineFixtureTime),
	)
	return account
}

// AC3 (timer expiry does not mark the account healthy by time alone): an active
// account carrying an operation-scope cooldown whose retry_not_before has already
// elapsed is still reported cooling_down on a plain read. Nothing heals a
// condition by the passage of time; only an authorized recovery may resolve it
// (spec §7.7, §20 I-COOLDOWN-HALF-OPEN).
func TestElapsedCooldownTimerDoesNotHealByTime(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", scopedCooldownAccount("pa_no_heal", domain.AuthModeChatGPTCodexOAuth))
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/provider-accounts/pa_no_heal",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}

	account := decodeError(t, payload)
	health, _ := account["health"].(map[string]any)
	if health["summary_state"] != "cooling_down" {
		t.Fatalf("summary_state = %v, want cooling_down (timer expiry alone never heals)", health["summary_state"])
	}
	conditions, _ := health["conditions"].([]any)
	stillCooling := 0
	for _, raw := range conditions {
		condition, _ := raw.(map[string]any)
		if condition["state"] == "cooling_down" {
			stillCooling++
			if _, ok := condition["retry_after_seconds"]; ok {
				t.Fatalf("elapsed cooldown must omit retry_after_seconds; condition = %v", condition)
			}
		}
	}
	if stillCooling != 2 {
		t.Fatalf("cooling_down conditions = %d, want 2 (no condition healed by elapsed timer); conditions = %v", stillCooling, conditions)
	}
}

// AC3 (cooldown expiry grants a bounded single-flight recovery permit that
// resolves only the matching scope): a Tenant-triggered re-probe (§9.12) of an
// active account that carries two operation-scope cooldowns, scoped to the
// image_generation bucket and returning an authenticated outcome with no fresh
// rate/quota signal, resolves ONLY the image_generation cooldown. The chat
// cooldown at the other scope survives untouched (a scoped authoritative success
// resolves only the matching condition, §11 recovery outcomes, §7.9), and the
// summary stays cooling_down because a narrower scope is still cooling.
func TestRecoveryProbeResolvesOnlyMatchingScope(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", scopedCooldownAccount("pa_recover_scope", domain.AuthModeChatGPTCodexOAuth))
		// The recovery probe authenticates and surfaces NO fresh rate/quota signal,
		// so it is a scoped authoritative success for the probed bucket.
		h.probe.outcome = ports.ProbeOutcome{Authenticated: true}
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_recover_scope/probe",
		bearer: tenantAKey,
		body:   `{"scope":{"kind":"operation","operation":"image_generation"}}`,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}

	account := decodeAccount(t, payload)
	if account["lifecycle_state"] != "active" {
		t.Fatalf("lifecycle_state = %v, want active", account["lifecycle_state"])
	}
	health, _ := account["health"].(map[string]any)
	conditions, _ := health["conditions"].([]any)

	var imageCondition, chatCondition map[string]any
	for _, raw := range conditions {
		condition, _ := raw.(map[string]any)
		scope, _ := condition["scope"].(map[string]any)
		if scope["kind"] != "operation" {
			continue
		}
		switch scope["operation"] {
		case "image_generation":
			imageCondition = condition
		case "chat":
			chatCondition = condition
		}
	}

	// The matching scope is resolved: no active image_generation cooldown remains.
	if imageCondition != nil && imageCondition["state"] == "cooling_down" {
		t.Fatalf("image_generation cooldown was not resolved by the matching recovery probe; condition = %v", imageCondition)
	}
	// The other scope is untouched: recovery resolves only the scope it verified.
	if chatCondition == nil {
		t.Fatalf("chat cooldown missing; recovery must not clear out-of-scope conditions; conditions = %v", conditions)
	}
	if chatCondition["state"] != "cooling_down" {
		t.Fatalf("chat state = %v, want cooling_down (out-of-scope condition preserved)", chatCondition["state"])
	}
	// A narrower scope still cools, so the summary remains cooling_down.
	if health["summary_state"] != "cooling_down" {
		t.Fatalf("summary_state = %v, want cooling_down (chat still cooling)", health["summary_state"])
	}
}

// AC5: quarantine is a hard administrative control. A Tenant-authenticated
// generic probe cannot use it as an incident-remediation bypass, so the request
// stops before Vault validation and before the Probe Adapter.
func TestQuarantinedAccountBlocksGenericProbeBeforeProtectedWork(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_probe_quarantined", domain.AuthModeChatGPTCodexOAuth)
		account.Controls.Quarantine = domain.QuarantineQuarantined
		h.accounts.seed("tenant_a", account)
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_probe_quarantined/probe",
		bearer: tenantAKey,
		body:   `{}`,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 quarantine gate (body=%s)", response.StatusCode, payload)
	}
	errBody := decodeError(t, payload)
	if errBody["code"] != "account_not_usable" || errBody["remediation"] != "contact_operator" {
		t.Fatalf("error = %v, want account_not_usable/contact_operator", errBody)
	}
	if calls := harness.vault.validCalls.Load(); calls != 0 {
		t.Fatalf("vault validate calls = %d, want 0 while quarantined", calls)
	}
	if calls := harness.probe.callCount.Load(); calls != 0 {
		t.Fatalf("probe calls = %d, want 0 while quarantined", calls)
	}
}

func TestAuthModeKillProbeUsesAuthModeRemediation(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_probe_auth_mode_kill", domain.AuthModeChatGPTCodexOAuth)
		account.Controls.AuthModeExecutionEnabled = false
		h.accounts.seed("tenant_a", account)
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_probe_auth_mode_kill/probe",
		bearer: tenantAKey,
		body:   `{}`,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 auth mode gate (body=%s)", response.StatusCode, payload)
	}
	body := decodeError(t, payload)
	if body["code"] != "auth_mode_unavailable" || body["remediation"] != "auth_mode_unavailable" {
		t.Fatalf("error = %v, want auth_mode_unavailable remediation", body)
	}
	if calls := harness.vault.validCalls.Load(); calls != 0 {
		t.Fatalf("vault validate calls = %d, want 0 while auth mode is killed", calls)
	}
	if calls := harness.probe.callCount.Load(); calls != 0 {
		t.Fatalf("probe calls = %d, want 0 while auth mode is killed", calls)
	}
}

// AC5: an open shared circuit blocks a new matching connection probe. The key
// is platform-safe (Provider + Auth Mode surface family + optional operation),
// and the gate runs before Vault/decrypt or Adapter work.
func TestOpenProviderSurfaceCircuitBlocksMatchingConnectionProbe(t *testing.T) {
	t.Parallel()

	surface := ports.CircuitSurface{
		Provider:  domain.ProviderChatGPT,
		AuthMode:  domain.AuthModeChatGPTCodexOAuth,
		Surface:   "/backend-api/models",
		Operation: domain.CapabilityOpImageGeneration,
	}
	tests := []struct {
		name         string
		body         string
		seedSnapshot bool
	}{
		{
			name:         "matching operation",
			body:         `{"scope":{"kind":"operation","operation":"image_generation"}}`,
			seedSnapshot: true,
		},
		{
			name:         "account scope overlaps operation circuit",
			body:         `{}`,
			seedSnapshot: true,
		},
		{
			name: "missing snapshot queries any safe surface",
			body: `{}`,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			accountID := domain.ProviderAccountID("pa_probe_circuit_" + strings.ReplaceAll(testCase.name, " ", "_"))
			harness := newSpineHarness(t, func(h *spineHarness) {
				account := activeAccount(string(accountID), domain.AuthModeChatGPTCodexOAuth)
				h.accounts.seed("tenant_a", account)
				if testCase.seedSnapshot {
					h.capabilities.seed("tenant_a", sampleObservationSnapshot(account.ID, account.AuthMode, account.Credential.Version, spineFixtureTime))
				}
				h.circuits.set(surface, ports.CircuitState{Open: true})
			})

			response, payload := harness.do(t, requestSpec{
				method: http.MethodPost,
				path:   "/v1/provider-accounts/" + string(accountID) + "/probe",
				bearer: tenantAKey,
				body:   testCase.body,
			})
			if response.StatusCode != http.StatusConflict {
				t.Fatalf("status = %d, want 409 open-circuit gate (body=%s)", response.StatusCode, payload)
			}
			errBody := decodeError(t, payload)
			if errBody["retryability"] != "retry_after" || errBody["retry_after_class"] != "provider_cooldown" {
				t.Fatalf("error retry metadata = %v, want retry_after/provider_cooldown", errBody)
			}
			if _, ok := errBody["retry_after_seconds"]; ok {
				t.Fatalf("circuit without a finite open-until must omit retry_after_seconds: %v", errBody)
			}
			if calls := harness.vault.validCalls.Load(); calls != 0 {
				t.Fatalf("vault validate calls = %d, want 0 while matching circuit is open", calls)
			}
			if calls := harness.probe.callCount.Load(); calls != 0 {
				t.Fatalf("probe calls = %d, want 0 while matching circuit is open", calls)
			}
		})
	}
}

func TestAccountScopeProbeCannotBypassCircuitOnAnotherSnapshotSurface(t *testing.T) {
	t.Parallel()

	accountID := domain.ProviderAccountID("pa_probe_circuit_multi_surface")
	openSurface := ports.CircuitSurface{
		Provider:  domain.ProviderChatGPT,
		AuthMode:  domain.AuthModeChatGPTCodexOAuth,
		Surface:   "/backend-api/images",
		Operation: domain.CapabilityOpImageGeneration,
	}
	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount(string(accountID), domain.AuthModeChatGPTCodexOAuth)
		h.accounts.seed("tenant_a", account)
		snapshot := sampleObservationSnapshot(account.ID, account.AuthMode, account.Credential.Version, spineFixtureTime)
		snapshot.Provenance[0].ProbeSurface = "/backend-api/chat"
		snapshot.Operations[domain.CapabilityOpChat] = domain.CapabilityFact{
			Status:        domain.CapabilityVerified,
			EvidenceClass: domain.EvidenceLiveProbe,
			ProbeSurface:  "/backend-api/chat",
		}
		snapshot.Operations[domain.CapabilityOpImageGeneration] = domain.CapabilityFact{
			Status:        domain.CapabilityConditionallySupported,
			EvidenceClass: domain.EvidenceLiveProbe,
			ProbeSurface:  "/backend-api/images",
		}
		h.capabilities.seed("tenant_a", snapshot)
		h.circuits.set(openSurface, ports.CircuitState{Open: true})
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/" + string(accountID) + "/probe",
		bearer: tenantAKey,
		body:   `{}`,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 for open circuit on another snapshot surface (body=%s)", response.StatusCode, payload)
	}
	if calls := harness.vault.validCalls.Load(); calls != 0 {
		t.Fatalf("vault validate calls = %d, want 0 while any matching surface circuit is open", calls)
	}
	if calls := harness.probe.callCount.Load(); calls != 0 {
		t.Fatalf("probe calls = %d, want 0 while any matching surface circuit is open", calls)
	}
}

func TestProbeRejectsInvalidCircuitScopeBeforeProtectedWork(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{
			name: "unknown operation",
			body: `{"scope":{"kind":"operation","operation":"provider_private_admin"}}`,
		},
		{
			name: "unknown scope kind",
			body: `{"scope":{"kind":"provider_surface","operation":"image_generation"}}`,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			accountID := "pa_probe_invalid_" + strings.ReplaceAll(testCase.name, " ", "_")
			harness := newSpineHarness(t, func(h *spineHarness) {
				h.accounts.seed("tenant_a", activeAccount(accountID, domain.AuthModeChatGPTCodexOAuth))
			})

			response, payload := harness.do(t, requestSpec{
				method: http.MethodPost,
				path:   "/v1/provider-accounts/" + accountID + "/probe",
				bearer: tenantAKey,
				body:   testCase.body,
			})
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 invalid scope (body=%s)", response.StatusCode, payload)
			}
			if body := decodeError(t, payload); body["code"] != "invalid_request" {
				t.Fatalf("code = %v, want invalid_request", body["code"])
			}
			if calls := harness.circuits.callCount.Load(); calls != 0 {
				t.Fatalf("circuit queries = %d, want 0 for invalid scope", calls)
			}
			if calls := harness.vault.validCalls.Load(); calls != 0 {
				t.Fatalf("vault validate calls = %d, want 0 for invalid scope", calls)
			}
			if calls := harness.probe.callCount.Load(); calls != 0 {
				t.Fatalf("probe calls = %d, want 0 for invalid scope", calls)
			}
		})
	}
}

// AC5: a wired-but-unreadable circuit evaluator fails closed. Dependency state
// cannot be treated as evidence that the matching surface is safe.
func TestUnavailableProviderSurfaceCircuitFailsConnectionProbeClosed(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", activeAccount("pa_probe_circuit_unavailable", domain.AuthModeChatGPTCodexOAuth))
		h.circuits.queryErr = ports.ErrCircuitUnavailable
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_probe_circuit_unavailable/probe",
		bearer: tenantAKey,
		body:   `{}`,
	})
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 unreadable-circuit failure (body=%s)", response.StatusCode, payload)
	}
	if calls := harness.vault.validCalls.Load(); calls != 0 {
		t.Fatalf("vault validate calls = %d, want 0 when circuit state is unreadable", calls)
	}
	if calls := harness.probe.callCount.Load(); calls != 0 {
		t.Fatalf("probe calls = %d, want 0 when circuit state is unreadable", calls)
	}
}

// AC3: a cooldown before retry_not_before remains closed. The request may pass
// admission, but neither Vault validation nor the Probe Adapter may run because
// elapsed time is the half-open eligibility gate, not a suggestion.
func TestRecoveryProbeBeforeRetryNotBeforeDoesNotReachAdapter(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_recover_early", domain.AuthModeChatGPTCodexOAuth)
		account = account.WithScopedCooldown(
			domain.NewTimestamp(spineFixtureTime),
			domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpImageGeneration)},
			domain.HealthReasonProviderRateLimited,
			domain.NewTimestamp(spineFixtureTime.Add(time.Minute)),
		)
		h.accounts.seed("tenant_a", account)
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_recover_early/probe",
		bearer: tenantAKey,
		body:   `{"scope":{"kind":"operation","operation":"image_generation"}}`,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 before retry_not_before (body=%s)", response.StatusCode, payload)
	}
	errBody := decodeError(t, payload)
	if errBody["retryability"] != "retry_after" || errBody["retry_after_class"] != "provider_cooldown" {
		t.Fatalf("error retry metadata = %v, want retry_after/provider_cooldown", errBody)
	}
	retryAfter, ok := errBody["retry_after_seconds"].(float64)
	if !ok || retryAfter < 1 || retryAfter > 60 {
		t.Fatalf("retry_after_seconds = %v, want finite 1..60", errBody["retry_after_seconds"])
	}
	if calls := harness.vault.validCalls.Load(); calls != 0 {
		t.Fatalf("vault validate calls = %d, want 0 before half-open eligibility", calls)
	}
	if calls := harness.probe.callCount.Load(); calls != 0 {
		t.Fatalf("probe calls = %d, want 0 before half-open eligibility", calls)
	}
}

// AC3: two concurrent requests for the same account/scope/revision compete on
// the durable AccountStore CAS. Exactly one owns the permit and enters the Probe
// Adapter; the loser fails before protected Provider work.
func TestConcurrentRecoveryProbesGrantExactlyOnePermit(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_recover_singleflight", domain.AuthModeChatGPTCodexOAuth)
		account = account.WithScopedCooldown(
			domain.NewTimestamp(spineFixtureTime),
			domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpImageGeneration)},
			domain.HealthReasonProviderRateLimited,
			domain.NewTimestamp(spineFixtureTime),
		)
		h.accounts.seed("tenant_a", account)
		h.probe.entered = entered
		h.probe.release = release
	})

	type result struct {
		status int
		body   []byte
		err    error
	}
	results := make(chan result, 2)
	request := func() {
		httpRequest, err := http.NewRequest(
			http.MethodPost,
			harness.fixture.URL()+"/v1/provider-accounts/pa_recover_singleflight/probe",
			strings.NewReader(`{"scope":{"kind":"operation","operation":"image_generation"}}`),
		)
		if err != nil {
			results <- result{err: err}
			return
		}
		httpRequest.Header.Set("Authorization", "Bearer "+tenantAKey)
		httpRequest.Header.Set("Content-Type", "application/json")
		response, err := harness.fixture.Client().Do(httpRequest)
		if err != nil {
			results <- result{err: err}
			return
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		results <- result{status: response.StatusCode, body: body, err: readErr}
	}

	go request()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first recovery did not enter Probe Adapter")
	}
	go request()

	var loser result
	select {
	case loser = <-results:
		if loser.err != nil {
			t.Fatalf("loser request error = %v", loser.err)
		}
		if loser.status != http.StatusServiceUnavailable {
			t.Fatalf("loser status = %d, want 503 CAS conflict (body=%s)", loser.status, loser.body)
		}
	case <-time.After(time.Second):
		t.Fatal("second recovery did not fail while permit was owned")
	}

	close(release)
	winner := <-results
	if winner.err != nil {
		t.Fatalf("winner request error = %v", winner.err)
	}
	if winner.status != http.StatusOK {
		t.Fatalf("winner status = %d, want 200 (body=%s)", winner.status, winner.body)
	}
	if calls := harness.probe.callCount.Load(); calls != 1 {
		t.Fatalf("probe calls = %d, want exactly 1 for one condition revision", calls)
	}
}

func TestRecoveryDependencyFailureKeepsActiveLifecycleAndConsumesRevisionPermit(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_recover_dependency", domain.AuthModeChatGPTCodexOAuth)
		account = account.WithScopedCooldown(
			domain.NewTimestamp(spineFixtureTime),
			domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpImageGeneration)},
			domain.HealthReasonProviderRateLimited,
			domain.NewTimestamp(spineFixtureTime),
		)
		h.accounts.seed("tenant_a", account)
		h.probe.probeErr = ports.ErrDependencyUnavailable
	})

	probe := func() (*http.Response, []byte) {
		return harness.do(t, requestSpec{
			method: http.MethodPost,
			path:   "/v1/provider-accounts/pa_recover_dependency/probe",
			bearer: tenantAKey,
			body:   `{"scope":{"kind":"operation","operation":"image_generation"}}`,
		})
	}

	firstResponse, firstPayload := probe()
	if firstResponse.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("first status = %d, want 503 dependency failure (body=%s)", firstResponse.StatusCode, firstPayload)
	}

	principal := harness.principal.principals[tenantAKey]
	stored, err := harness.accounts.Visible(t.Context(), principal, "pa_recover_dependency")
	if err != nil {
		t.Fatalf("Visible() after dependency failure error = %v", err)
	}
	if stored.Lifecycle != domain.LifecycleActive {
		t.Fatalf("lifecycle = %s, want active after non-authoritative dependency failure", stored.Lifecycle)
	}
	if stored.Health.SummaryState != domain.HealthCoolingDown {
		t.Fatalf("health = %s, want cooling_down unchanged", stored.Health.SummaryState)
	}
	if stored.RecoveryPermit.Owner == "" {
		t.Fatal("recovery permit was released after dependency failure; revision could be retried without bound")
	}

	secondResponse, secondPayload := probe()
	if secondResponse.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("second status = %d, want 503 occupied-permit conflict (body=%s)", secondResponse.StatusCode, secondPayload)
	}
	if calls := harness.vault.validCalls.Load(); calls != 1 {
		t.Fatalf("vault validate calls = %d, want 1 across both requests", calls)
	}
	if calls := harness.probe.callCount.Load(); calls != 1 {
		t.Fatalf("probe calls = %d, want exactly 1 for the consumed condition revision", calls)
	}
}

// AC3: an auth-class recovery failure is an authoritative Provider outcome. It
// replaces the transient cooldown with account-scope expired evidence and consumes
// the private permit so a dead cooldown owner cannot survive reauthentication.
func TestRecoveryCredentialRejectionConsumesPermit(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_recover_rejected", domain.AuthModeChatGPTCodexOAuth)
		account = account.WithScopedCooldown(
			domain.NewTimestamp(spineFixtureTime),
			domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpImageGeneration)},
			domain.HealthReasonProviderRateLimited,
			domain.NewTimestamp(spineFixtureTime),
		)
		h.accounts.seed("tenant_a", account)
		h.probe.outcome = ports.ProbeOutcome{Authenticated: false}
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_recover_rejected/probe",
		bearer: tenantAKey,
		body:   `{"scope":{"kind":"operation","operation":"image_generation"}}`,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 credential-rejected transition (body=%s)", response.StatusCode, payload)
	}

	principal := harness.principal.principals[tenantAKey]
	stored, err := harness.accounts.Visible(t.Context(), principal, "pa_recover_rejected")
	if err != nil {
		t.Fatalf("Visible() after rejection error = %v", err)
	}
	if stored.Lifecycle != domain.LifecycleReauthRequired {
		t.Fatalf("lifecycle = %s, want reauth_required", stored.Lifecycle)
	}
	if stored.Health.SummaryState != domain.HealthExpired {
		t.Fatalf("health = %s, want expired", stored.Health.SummaryState)
	}
	if stored.RecoveryPermit.Owner != "" {
		t.Fatalf("recovery permit owner = %q, want cleared after authoritative rejection", stored.RecoveryPermit.Owner)
	}
}

func TestRecoveryFreshSignalAtDifferentScopeRenewsClaimedRevision(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_recover_cross_signal", domain.AuthModeChatGPTCodexOAuth)
		account = account.WithScopedCooldown(
			domain.NewTimestamp(spineFixtureTime),
			domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpImageGeneration)},
			domain.HealthReasonProviderRateLimited,
			domain.NewTimestamp(spineFixtureTime),
		)
		h.accounts.seed("tenant_a", account)
		h.probe.outcome = ports.ProbeOutcome{
			Authenticated:     true,
			Signal:            ports.ProbeSignalRateLimited,
			SignalScope:       domain.HealthScope{Kind: domain.HealthScopeAccount},
			RetryAfterSeconds: 30,
		}
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_recover_cross_signal/probe",
		bearer: tenantAKey,
		body:   `{"scope":{"kind":"operation","operation":"image_generation"}}`,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 fresh-signal settlement (body=%s)", response.StatusCode, payload)
	}

	principal := harness.principal.principals[tenantAKey]
	stored, err := harness.accounts.Visible(t.Context(), principal, "pa_recover_cross_signal")
	if err != nil {
		t.Fatalf("Visible() after fresh signal error = %v", err)
	}
	if stored.RecoveryPermit.Owner != "" {
		t.Fatalf("recovery permit owner = %q, want consumed by renewed claimed revision", stored.RecoveryPermit.Owner)
	}

	var imageRevision int
	var accountCooling bool
	for _, condition := range stored.Health.Conditions {
		switch {
		case condition.Scope.Kind == domain.HealthScopeOperation && condition.Scope.Operation == string(domain.CapabilityOpImageGeneration):
			imageRevision = condition.ConditionRevision
		case condition.Scope.Kind == domain.HealthScopeAccount && condition.State == domain.HealthCoolingDown:
			accountCooling = true
		}
	}
	if imageRevision != 2 {
		t.Fatalf("claimed image condition revision = %d, want renewed revision 2", imageRevision)
	}
	if !accountCooling {
		t.Fatal("fresh account-scope signal was not overlaid")
	}
}

// AC4: renewing the same scoped condition while its recovery probe is in flight
// advances condition_revision. The late success is fenced out and cannot clear
// that newer failure, even though it verified the same operation scope.
func TestLateRecoverySuccessCannotClearRenewedConditionRevision(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_recover_stale", domain.AuthModeChatGPTCodexOAuth)
		account = account.WithScopedCooldown(
			domain.NewTimestamp(spineFixtureTime),
			domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpImageGeneration)},
			domain.HealthReasonProviderRateLimited,
			domain.NewTimestamp(spineFixtureTime),
		)
		h.accounts.seed("tenant_a", account)
		h.probe.entered = entered
		h.probe.release = release
	})

	var wg sync.WaitGroup
	wg.Add(1)
	var responseStatus int
	var responseBody []byte
	go func() {
		defer wg.Done()
		request, err := http.NewRequest(
			http.MethodPost,
			harness.fixture.URL()+"/v1/provider-accounts/pa_recover_stale/probe",
			strings.NewReader(`{"scope":{"kind":"operation","operation":"image_generation"}}`),
		)
		if err != nil {
			return
		}
		request.Header.Set("Authorization", "Bearer "+tenantAKey)
		request.Header.Set("Content-Type", "application/json")
		response, err := harness.fixture.Client().Do(request)
		if err != nil {
			return
		}
		responseStatus = response.StatusCode
		responseBody, _ = io.ReadAll(response.Body)
		_ = response.Body.Close()
	}()

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("recovery did not enter Probe Adapter")
	}

	principal := harness.principal.principals[tenantAKey]
	current, err := harness.accounts.Visible(t.Context(), principal, "pa_recover_stale")
	if err != nil {
		t.Fatalf("Visible() error = %v", err)
	}
	renewed := current.WithScopedCooldown(
		domain.NewTimestamp(spineFixtureTime.Add(time.Second)),
		domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpImageGeneration)},
		domain.HealthReasonProviderRateLimited,
		domain.NewTimestamp(spineFixtureTime.Add(time.Minute)),
	)
	if _, err := harness.accounts.Update(t.Context(), ports.AccountUpdate{Principal: principal, Account: renewed}); err != nil {
		t.Fatalf("renew cooldown Update() error = %v", err)
	}

	close(release)
	wg.Wait()
	if responseStatus != http.StatusServiceUnavailable {
		t.Fatalf("late recovery status = %d, want 503 stale-settlement conflict (body=%s)", responseStatus, responseBody)
	}

	stored, err := harness.accounts.Visible(t.Context(), principal, "pa_recover_stale")
	if err != nil {
		t.Fatalf("Visible() after recovery error = %v", err)
	}
	var found bool
	for _, condition := range stored.Health.Conditions {
		if condition.Scope.Kind == domain.HealthScopeOperation &&
			condition.Scope.Operation == string(domain.CapabilityOpImageGeneration) &&
			condition.State == domain.HealthCoolingDown {
			found = true
			if condition.ConditionRevision != 2 {
				t.Fatalf("condition revision = %d, want renewed revision 2", condition.ConditionRevision)
			}
		}
	}
	if !found {
		t.Fatal("renewed cooling_down condition was cleared by late success")
	}
}

// mixedScopeCooldownAccount builds an active account carrying an account-scope
// cooldown AND a narrower operation-scope cooldown. It is the fixture for the
// two no-stale-clear directions of §4: a narrower success must not clear the
// broader account failure, and an account-scope success must not clear the
// narrower operation failure.
func mixedScopeCooldownAccount(id string, mode domain.AuthMode) domain.ProviderAccount {
	account := activeAccount(id, mode)
	// Account-scope cooldown: renews the existing account-scope healthy evidence
	// in place, so the account-scope condition becomes cooling_down (broad failure).
	account = account.WithScopedCooldown(
		domain.NewTimestamp(spineFixtureTime),
		domain.HealthScope{Kind: domain.HealthScopeAccount},
		domain.HealthReasonProviderRateLimited,
		domain.NewTimestamp(spineFixtureTime),
	)
	// Operation-scope cooldown at image_generation (narrow failure).
	account = account.WithScopedCooldown(
		domain.NewTimestamp(spineFixtureTime),
		domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpImageGeneration)},
		domain.HealthReasonProviderRateLimited,
		domain.NewTimestamp(spineFixtureTime),
	)
	return account
}

// AC3/AC4: a broader account cooldown blocks a narrower recovery probe before
// protected work. The operation condition may be independently eligible, but it
// cannot be claimed while an account-wide condition still covers the request.
func TestOperationScopeRecoveryCannotBypassAccountScopeCooldown(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", mixedScopeCooldownAccount("pa_op_no_clear_account", domain.AuthModeChatGPTCodexOAuth))
		h.probe.outcome = ports.ProbeOutcome{Authenticated: true}
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_op_no_clear_account/probe",
		bearer: tenantAKey,
		body:   `{"scope":{"kind":"operation","operation":"image_generation"}}`,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 broader cooldown gate (body=%s)", response.StatusCode, payload)
	}
	if body := decodeError(t, payload); body["retry_after_class"] != "provider_cooldown" {
		t.Fatalf("retry_after_class = %v, want provider_cooldown", body["retry_after_class"])
	}
	if calls := harness.vault.validCalls.Load(); calls != 0 {
		t.Fatalf("vault validate calls = %d, want 0 under account-wide cooldown", calls)
	}
	if calls := harness.probe.callCount.Load(); calls != 0 {
		t.Fatalf("probe calls = %d, want 0 under account-wide cooldown", calls)
	}

	stored, err := harness.accounts.Visible(t.Context(), managePrincipal(), "pa_op_no_clear_account")
	if err != nil {
		t.Fatalf("visible: %v", err)
	}
	if stored.RecoveryPermit.Owner != "" {
		t.Fatalf("recovery permit owner = %q, want no narrower claim", stored.RecoveryPermit.Owner)
	}
	for _, condition := range stored.Health.Conditions {
		if condition.State != domain.HealthCoolingDown {
			t.Fatalf("condition state = %s, want cooling_down after blocked probe", condition.State)
		}
	}
}

func TestOperationScopeCooldownBlocksCoveredModelProbe(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_operation_covers_model", domain.AuthModeChatGPTCodexOAuth)
		account = account.WithScopedCooldown(
			domain.NewTimestamp(spineFixtureTime),
			domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpImageGeneration)},
			domain.HealthReasonProviderRateLimited,
			domain.NewTimestamp(spineFixtureTime),
		)
		h.accounts.seed("tenant_a", account)
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_operation_covers_model/probe",
		bearer: tenantAKey,
		body:   `{"scope":{"kind":"model","operation":"image_generation","model_slug":"gpt-image-1"}}`,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 operation cooldown gate (body=%s)", response.StatusCode, payload)
	}
	if calls := harness.vault.validCalls.Load(); calls != 0 {
		t.Fatalf("vault validate calls = %d, want 0 under covering operation cooldown", calls)
	}
	if calls := harness.probe.callCount.Load(); calls != 0 {
		t.Fatalf("probe calls = %d, want 0 under covering operation cooldown", calls)
	}
}

// AC4 (generic identity success cannot clear a narrower failure): an
// account-scope recovery probe (no scope body) that authenticates and surfaces
// no fresh signal resolves ONLY the account-scope cooldown. The narrower
// image_generation cooldown survives because a generic account success does not
// verify the operation bucket (§4 rule 4, §11 recovery outcomes). The summary
// stays cooling_down because the operation scope is still cooling.
func TestAccountScopeRecoveryCannotClearOperationCooldown(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", mixedScopeCooldownAccount("pa_account_no_clear_op", domain.AuthModeChatGPTCodexOAuth))
		h.probe.outcome = ports.ProbeOutcome{Authenticated: true}
	})

	// No scope body → the probe is an account-scope recovery (generic identity).
	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_account_no_clear_op/probe",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}

	acc := decodeAccount(t, payload)
	health, _ := acc["health"].(map[string]any)
	conditions, _ := health["conditions"].([]any)

	var accountCondition, imageCondition map[string]any
	for _, raw := range conditions {
		condition, _ := raw.(map[string]any)
		scope, _ := condition["scope"].(map[string]any)
		switch scope["kind"] {
		case "account":
			accountCondition = condition
		case "operation":
			if scope["operation"] == "image_generation" {
				imageCondition = condition
			}
		}
	}

	// The narrower operation cooldown MUST survive a generic account success.
	if imageCondition == nil {
		t.Fatalf("image_generation cooldown missing; a generic account success cleared a narrower failure; conditions = %v", conditions)
	}
	if imageCondition["state"] != "cooling_down" {
		t.Fatalf("image_generation state = %v, want cooling_down (generic identity success does not verify the operation bucket)", imageCondition["state"])
	}
	// The account scope this probe verified is resolved.
	if accountCondition != nil && accountCondition["state"] == "cooling_down" {
		t.Fatalf("account-scope cooldown was not resolved by its matching recovery; condition = %v", accountCondition)
	}
	if health["summary_state"] != "cooling_down" {
		t.Fatalf("summary_state = %v, want cooling_down (operation scope still cooling)", health["summary_state"])
	}
}

// Review-fix: a recovery permit claim must fail if the account is disabled
// concurrently. The probe claims the permit, the account is disabled while the
// probe is in flight, then the stale success is fenced out by the lifecycle
// precondition and the account stays disabled.
func TestRecoveryPermitCannotResurrectDisabledAccount(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_recover_disable_race", domain.AuthModeChatGPTCodexOAuth)
		account = account.WithScopedCooldown(
			domain.NewTimestamp(spineFixtureTime),
			domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpImageGeneration)},
			domain.HealthReasonProviderRateLimited,
			domain.NewTimestamp(spineFixtureTime),
		)
		h.accounts.seed("tenant_a", account)
		h.probe.entered = entered
		h.probe.release = release
		h.probe.outcome = ports.ProbeOutcome{Authenticated: true}
	})

	var wg sync.WaitGroup
	wg.Add(1)
	var responseStatus int
	var responseBody []byte
	go func() {
		defer wg.Done()
		request, err := http.NewRequest(
			http.MethodPost,
			harness.fixture.URL()+"/v1/provider-accounts/pa_recover_disable_race/probe",
			strings.NewReader(`{"scope":{"kind":"operation","operation":"image_generation"}}`),
		)
		if err != nil {
			return
		}
		request.Header.Set("Authorization", "Bearer "+tenantAKey)
		request.Header.Set("Content-Type", "application/json")
		response, err := harness.fixture.Client().Do(request)
		if err != nil {
			return
		}
		responseStatus = response.StatusCode
		responseBody, _ = io.ReadAll(response.Body)
		_ = response.Body.Close()
	}()

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("recovery did not enter Probe Adapter")
	}

	principal := harness.principal.principals[tenantAKey]
	current, err := harness.accounts.Visible(t.Context(), principal, "pa_recover_disable_race")
	if err != nil {
		t.Fatalf("Visible() error = %v", err)
	}
	disabled := current.WithDisabled(domain.NewTimestamp(spineFixtureTime.Add(time.Second)))
	if _, err := harness.accounts.Update(t.Context(), ports.AccountUpdate{Principal: principal, Account: disabled}); err != nil {
		t.Fatalf("disable Update() error = %v", err)
	}

	close(release)
	wg.Wait()
	if responseStatus != http.StatusOK {
		t.Fatalf("status = %d, want 200 with disabled account preserved (body=%s)", responseStatus, responseBody)
	}

	stored, err := harness.accounts.Visible(t.Context(), principal, "pa_recover_disable_race")
	if err != nil {
		t.Fatalf("Visible() after race error = %v", err)
	}
	if stored.Lifecycle != domain.LifecycleDisabled {
		t.Fatalf("lifecycle = %v, want disabled", stored.Lifecycle)
	}
}

// Review-fix: independent scoped cooldown creation/renewal in separate probe
// requests must not clobber each other. Renewing chat while creating
// image_generation leaves both durable conditions intact.
func TestIndependentScopedCooldownsDoNotOverwrite(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		account := activeAccount("pa_independent_scopes", domain.AuthModeChatGPTCodexOAuth)
		account = account.WithScopedCooldown(
			domain.NewTimestamp(spineFixtureTime),
			domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpChat)},
			domain.HealthReasonProviderRateLimited,
			domain.NewTimestamp(spineFixtureTime),
		)
		h.accounts.seed("tenant_a", account)
		h.probe.outcome = ports.ProbeOutcome{
			Authenticated: true,
			Signal:        ports.ProbeSignalRateLimited,
			SignalScope:   domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpChat)},
		}
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_independent_scopes/probe",
		bearer: tenantAKey,
		body:   `{"scope":{"kind":"operation","operation":"chat"}}`,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("chat probe status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}

	harness.probe.outcome = ports.ProbeOutcome{
		Authenticated: true,
		Signal:        ports.ProbeSignalQuotaExhausted,
		SignalScope:   domain.HealthScope{Kind: domain.HealthScopeOperation, Operation: string(domain.CapabilityOpImageGeneration)},
	}
	response, payload = harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_independent_scopes/probe",
		bearer: tenantAKey,
		body:   `{"scope":{"kind":"operation","operation":"image_generation"}}`,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("image probe status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}

	principal := harness.principal.principals[tenantAKey]
	stored, err := harness.accounts.Visible(t.Context(), principal, "pa_independent_scopes")
	if err != nil {
		t.Fatalf("Visible() error = %v", err)
	}
	var chatCondition, imageCondition domain.HealthCondition
	for _, condition := range stored.Health.Conditions {
		if condition.Scope.Kind == domain.HealthScopeOperation && condition.Scope.Operation == string(domain.CapabilityOpChat) {
			chatCondition = condition
		}
		if condition.Scope.Kind == domain.HealthScopeOperation && condition.Scope.Operation == string(domain.CapabilityOpImageGeneration) {
			imageCondition = condition
		}
	}
	if chatCondition.State != domain.HealthCoolingDown || chatCondition.ConditionRevision != 2 {
		t.Fatalf("chat condition = %+v, want cooling_down revision 2", chatCondition)
	}
	if imageCondition.State != domain.HealthCoolingDown || imageCondition.ConditionRevision != 1 {
		t.Fatalf("image condition = %+v, want cooling_down revision 1", imageCondition)
	}
}

// Review-fix: an unrecognized ProbeSignalClass is a dependency/protocol
// classification failure and must fail closed rather than continuing as a
// successful probe.
func TestUnknownProbeSignalFailsClosed(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, func(h *spineHarness) {
		h.accounts.seed("tenant_a", probeableAccount("pa_unknown_signal", domain.AuthModeChatGPTCodexOAuth))
		h.probe.outcome = ports.ProbeOutcome{
			Authenticated: true,
			Signal:        "totally_unknown_signal",
		}
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_unknown_signal/probe",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 fail-closed (body=%s)", response.StatusCode, payload)
	}
	body := decodeError(t, payload)
	if body["code"] != "dependency_unavailable" {
		t.Fatalf("code = %v, want dependency_unavailable", body["code"])
	}

	principal := harness.principal.principals[tenantAKey]
	stored, err := harness.accounts.Visible(t.Context(), principal, "pa_unknown_signal")
	if err != nil {
		t.Fatalf("Visible() error = %v", err)
	}
	if stored.Lifecycle == domain.LifecycleActive {
		t.Fatal("account activated on unknown probe signal")
	}
}
