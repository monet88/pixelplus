package contracttest_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/contracttest"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

const chatModel = "gpt-4o"

const chatSuccessBody = `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`

// chatHarness wires the non-streaming chat spine through real composition with
// controlled fakes. Tests enter through HTTP POST /v1/chat/completions and
// assert on fake observations (adapter runs, replay, admission settlement).
type chatHarness struct {
	fixture      *contracttest.Fixture
	log          *spineLog
	principal    *stubPrincipalStore
	admission    *stubAdmissionStore
	accounts     *stubAccountStore
	health       *stubHealthStore
	audit        *captureAudit
	telemetry    *captureTelemetry
	reqLog       *captureRequestLog
	vault        *stubCredentialVault
	capabilities *stubCapabilityStore
	circuits     *stubCircuitStore
	routing      *countingRoutingPolicyStore
	adapter      *countingChatAdapter
	replay       *recordingChatReplay
	digester     *stubChatDigester
}

func newChatHarness(t *testing.T, configure func(*chatHarness)) *chatHarness {
	t.Helper()

	log := &spineLog{}
	principal := &stubPrincipalStore{
		log: log,
		principals: map[string]domain.SecurityPrincipal{
			tenantAKey: {
				TenantID:       "tenant_a",
				ClientAPIKeyID: "key_a",
				Scopes: domain.NewScopeSet(
					domain.ScopeChatCompletions,
					domain.ScopeCapabilitiesRead,
					domain.ScopeRoutingRead,
				),
			},
			tenantBKey: {
				TenantID:       "tenant_b",
				ClientAPIKeyID: "key_b",
				Scopes: domain.NewScopeSet(
					domain.ScopeChatCompletions,
					domain.ScopeCapabilitiesRead,
					domain.ScopeRoutingRead,
				),
			},
			readOnly: {
				TenantID:       "tenant_a",
				ClientAPIKeyID: "key_r",
				Scopes:         domain.NewScopeSet(domain.ScopeAccountsRead),
			},
		},
	}
	adapter := newCountingChatAdapter(log)
	harness := &chatHarness{
		log:          log,
		principal:    principal,
		admission:    &stubAdmissionStore{log: log},
		accounts:     newStubAccountStore(log),
		health:       newStubHealthStore(),
		audit:        &captureAudit{},
		telemetry:    &captureTelemetry{},
		reqLog:       &captureRequestLog{},
		vault:        newStubCredentialVault(log),
		capabilities: newStubCapabilityStore(log),
		circuits:     newStubCircuitStore(log),
		routing:      newCountingRoutingPolicyStore(),
		adapter:      adapter,
		replay:       newRecordingChatReplay(log),
		digester:     newStubChatDigester(log),
	}
	if configure != nil {
		configure(harness)
	}

	fixture, err := contracttest.NewFixture(contracttest.Options{
		Principal:    harness.principal,
		Admission:    harness.admission,
		Accounts:     harness.accounts,
		Health:       harness.health,
		Audit:        harness.audit,
		Telemetry:    harness.telemetry,
		RequestLog:   harness.reqLog,
		Vault:        harness.vault,
		Capabilities: harness.capabilities,
		Circuits:     harness.circuits,
		Routing:      harness.routing,
		ChatAdapter:  harness.adapter,
		ChatReplay:   harness.replay,
		ChatDigester: harness.digester,
	})
	if err != nil {
		t.Fatalf("NewFixture() error = %v", err)
	}
	harness.fixture = fixture
	t.Cleanup(func() {
		closeFixture(t, fixture)
	})
	return harness
}

// seedActive seeds AccountStore lifecycle, HealthStore, Capability, and the
// routing policy for one active same-tenant account.
func (harness *chatHarness) seedActive(tenant domain.TenantID, accountID string, mode domain.AuthMode) {
	account := activeAccount(accountID, mode)
	stripped, health, permit := seedAccountHealth(account)
	harness.accounts.seed(tenant, stripped)
	harness.health.Seed(tenant, account.ID, health, permit)
	harness.capabilities.seed(tenant, chatCapabilitySnapshot(account.ID, account.AuthMode, account.Credential.Version, chatModel))
	harness.routing.Seed(tenant, chatRoutingPolicy(
		[]domain.ProviderAccountID{account.ID},
		nil,
	))
}

func (harness *chatHarness) do(t *testing.T, spec requestSpec) (*http.Response, []byte) {
	t.Helper()

	var reader io.Reader
	switch {
	case spec.rawBody != nil:
		reader = bytes.NewReader(spec.rawBody)
	case spec.body != "":
		reader = strings.NewReader(spec.body)
	}
	request, err := http.NewRequest(spec.method, harness.fixture.URL()+spec.path, reader)
	if err != nil {
		t.Fatalf("NewRequest(%s %s) error = %v", spec.method, spec.path, err)
	}
	if !spec.skipAuth && spec.bearer != "" {
		request.Header.Set("Authorization", "Bearer "+spec.bearer)
	}
	if spec.idemKey != "" {
		request.Header.Set("Idempotency-Key", spec.idemKey)
	}
	if reader != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := harness.fixture.Client().Do(request)
	if err != nil {
		t.Fatalf("Do(%s %s) error = %v", spec.method, spec.path, err)
	}
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body error = %v", err)
	}
	_ = response.Body.Close()
	return response, payload
}

// containsSeq reports whether events appears as an ordered subsequence of logs.
func containsSeq(logs []string, events ...string) bool {
	i := 0
	for _, entry := range logs {
		if i < len(events) && entry == events[i] {
			i++
		}
	}
	return i == len(events)
}

// settledKeysContain reports whether any settlement key contains the substring.
func (harness *chatHarness) settledKeysContain(sub string) bool {
	for key := range harness.admission.settledKeys {
		if strings.Contains(key, sub) {
			return true
		}
	}
	return false
}

// settledKeysAllContain reports whether every non-empty settlement key contains
// the substring (proves settlement never widens across Tenant boundaries).
func (harness *chatHarness) settledKeysAllContain(sub string) bool {
	if len(harness.admission.settledKeys) == 0 {
		return false
	}
	for key := range harness.admission.settledKeys {
		if !strings.Contains(key, sub) {
			return false
		}
	}
	return true
}

func decodeCompletionBody(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("decode completion: %v (body=%s)", err, payload)
	}
	return body
}

// AC: an authenticated Tenant receives a canonical non-streaming completion
// through the full path. Response is Provider-independent with stable safe
// metadata; Adapter runs exactly once; accounting settles once.
func TestChatCompletionHappyPathThroughComposition(t *testing.T) {
	t.Parallel()

	harness := newChatHarness(t, func(h *chatHarness) {
		h.seedActive("tenant_a", "pa_chat", domain.AuthModeChatGPTCodexOAuth)
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-1",
		body:    chatSuccessBody,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}

	body := decodeCompletionBody(t, payload)
	if body["object"] != "chat.completion" {
		t.Fatalf("object = %v, want chat.completion", body["object"])
	}
	if body["model"] != chatModel {
		t.Fatalf("model = %v, want %s", body["model"], chatModel)
	}
	choices, ok := body["choices"].([]any)
	if !ok || len(choices) != 1 {
		t.Fatalf("choices = %#v, want one canonical choice", body["choices"])
	}
	choice, _ := choices[0].(map[string]any)
	if choice["finish_class"] != string(domain.FinishStop) {
		t.Fatalf("choices[0].finish_class = %v, want stop", choice["finish_class"])
	}
	message, _ := choice["message"].(map[string]any)
	if message["role"] != string(domain.ChatRoleAssistant) {
		t.Fatalf("choices[0].message.role = %v, want assistant", message["role"])
	}
	// Safe metadata block present; no raw Provider framing leaks.
	meta, ok := body["x_pixelplus"].(map[string]any)
	if !ok {
		t.Fatalf("x_pixelplus metadata absent (body=%s)", payload)
	}
	if meta["provider_account_id"] != "pa_chat" {
		t.Fatalf("x_pixelplus.provider_account_id = %v, want pa_chat", meta["provider_account_id"])
	}
	if _, leaked := getString(meta, "prompt"); leaked {
		t.Fatalf("x_pixelplus leaked the prompt")
	}

	if calls := harness.adapter.CallCount(); calls != 1 {
		t.Fatalf("adapter calls = %d, want exactly 1", calls)
	}
	if settles := harness.admission.logicalSettleCount.Load(); settles != 1 {
		t.Fatalf("logical settle count = %d, want exactly 1", settles)
	}
	if !harness.settledKeysContain("tenant_a") {
		t.Fatalf("settlement key must be scoped to the original Tenant tenant_a")
	}
}

func getString(m map[string]any, key string) (string, bool) {
	value, ok := m[key].(string)
	return value, ok && value != ""
}

// AC: all gates pass in normative order before Adapter execution.
func TestChatGateOrderBeforeAdapterExecution(t *testing.T) {
	t.Parallel()

	harness := newChatHarness(t, func(h *chatHarness) {
		h.seedActive("tenant_a", "pa_chat", domain.AuthModeChatGPTCodexOAuth)
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-order",
		body:    chatSuccessBody,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}

	logs := harness.log.snapshot()
	if !containsSeq(logs,
		"authenticate", "digest", "replay.claim", "admit", "account.visible", "capability.get", "vault.validate", "adapter.run", "replay.complete",
	) {
		t.Fatalf("gate order violated: %v", logs)
	}
	// Adapter must run after all gates, before replay completion/settlement.
	if indexOf(logs, "adapter.run") >= indexOf(logs, "replay.complete") {
		t.Fatalf("adapter.run must precede replay.complete: %v", logs)
	}
}

func indexOf(list []string, target string) int {
	for i, value := range list {
		if value == target {
			return i
		}
	}
	return -1
}

// AC: selection precedence picks the deterministic first viable same-Tenant
// account and never widens silently (a foreign account is never chosen).
func TestChatSelectionDeterministicWhileSkippingUnusableAndForeign(t *testing.T) {
	t.Parallel()

	var primary domain.ProviderAccountID = "pa_checked"
	var fallback domain.ProviderAccountID = "pa_fresh"

	harness := newChatHarness(t, func(h *chatHarness) {
		// pa_primary in tenant_a is not active (draft) and must be skipped.
		draft := activeAccount(string(primary), domain.AuthModeChatGPTCodexOAuth)
		draft.Lifecycle = domain.LifecycleDraft
		stripped, health, permit := seedAccountHealth(draft)
		h.accounts.seed("tenant_a", stripped)
		h.health.Seed("tenant_a", draft.ID, health, permit)
		// pa_fresh in tenant_a is active and chat-verified.
		h.seedActive("tenant_a", string(fallback), domain.AuthModeChatGPTCodexOAuth)
		// A foreign tenant_b account must never even be considered.
		h.accounts.seed("tenant_b", activeAccount("pa_foreign", domain.AuthModeChatGPTCodexOAuth))
		h.routing.Seed("tenant_a", chatRoutingPolicy(
			[]domain.ProviderAccountID{primary, fallback},
			nil,
		))
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-sel",
		body:    chatSuccessBody,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	accounts := harness.adapter.Accounts()
	if len(accounts) != 1 || accounts[0] != fallback {
		t.Fatalf("adapter served %v, want deterministic first viable %s", accounts, fallback)
	}
	body := decodeCompletionBody(t, payload)
	meta, _ := body["x_pixelplus"].(map[string]any)
	if meta["provider_account_id"] != string(fallback) {
		t.Fatalf("response provider_account_id = %v, want %s", meta["provider_account_id"], fallback)
	}
}

// AC: a matching terminal replay returns the original result with no new
// Adapter call; conflict and uncertainty never steal execution.
func TestChatReplayReturnsOriginalWithoutNewAdapterCall(t *testing.T) {
	t.Parallel()

	harness := newChatHarness(t, func(h *chatHarness) {
		h.seedActive("tenant_a", "pa_chat", domain.AuthModeChatGPTCodexOAuth)
	})

	firstResponse, firstBody := harness.do(t, requestSpec{
		method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey, idemKey: "idem-replay", body: chatSuccessBody,
	})
	if firstResponse.StatusCode != http.StatusOK {
		t.Fatalf("first status = %d (body=%s)", firstResponse.StatusCode, firstBody)
	}
	first := decodeCompletionBody(t, firstBody)

	secondResponse, secondBody := harness.do(t, requestSpec{
		method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey, idemKey: "idem-replay", body: chatSuccessBody,
	})
	if secondResponse.StatusCode != http.StatusOK {
		t.Fatalf("replay status = %d (body=%s)", secondResponse.StatusCode, secondBody)
	}
	second := decodeCompletionBody(t, secondBody)

	if calls := harness.adapter.CallCount(); calls != 1 {
		t.Fatalf("adapter calls = %d, want exactly 1 (replay must not re-call the Adapter)", calls)
	}
	if second["id"] != first["id"] {
		t.Fatalf("replayed completion id differs: %v vs %v", second["id"], first["id"])
	}
	meta, _ := second["x_pixelplus"].(map[string]any)
	if meta["provider_account_id"] != "pa_chat" {
		t.Fatalf("replay lost provider_account_id metadata")
	}
}

func TestChatReplayConflictNeverStealsExecution(t *testing.T) {
	t.Parallel()

	harness := newChatHarness(t, func(h *chatHarness) {
		h.seedActive("tenant_a", "pa_chat", domain.AuthModeChatGPTCodexOAuth)
	})

	// First request binds the key to one fingerprint.
	harness.do(t, requestSpec{method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey, idemKey: "idem-conflict", body: chatSuccessBody})

	// Same key, different message content → different fingerprint → conflict.
	conflictBody := `{"model":"gpt-4o","messages":[{"role":"user","content":"different"}]}`
	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey, idemKey: "idem-conflict", body: conflictBody,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 conflict (body=%s)", response.StatusCode, payload)
	}
	body := decodeError(t, payload)
	if body["code"] != "idempotency_conflict" {
		t.Fatalf("error code = %v, want idempotency_conflict", body["code"])
	}
	if calls := harness.adapter.CallCount(); calls != 1 {
		t.Fatalf("adapter calls = %d, want 1 (conflict must not steal execution)", calls)
	}
	abandons := countEvents(harness.log.snapshot(), "replay.abandon")
	if abandons != 0 {
		t.Fatalf("conflict path must not abandon another owner's claim, abandons=%d", abandons)
	}
}

func countEvents(logs []string, target string) int {
	count := 0
	for _, value := range logs {
		if value == target {
			count++
		}
	}
	return count
}

// AC: retry/fallback share one owner and require authoritative no-commit proof.
// A fallback single-walk runs exactly once on a proven not_committed outcome.
func TestChatFallbackSingleWalkOnAuthoritativeNoCommit(t *testing.T) {
	t.Parallel()

	var primary domain.ProviderAccountID = "pa_primary"
	var fallback domain.ProviderAccountID = "pa_fallback"

	harness := newChatHarness(t, func(h *chatHarness) {
		h.seedActive("tenant_a", string(primary), domain.AuthModeChatGPTCodexOAuth)
		// A second active account for the fallback chain (route policy covers it).
		h.seedActive("tenant_a", string(fallback), domain.AuthModeChatGPTCodexOAuth)
		h.routing.Seed("tenant_a", chatRoutingPolicy(
			[]domain.ProviderAccountID{primary},
			[]domain.ProviderAccountID{fallback},
		))
		h.adapter.Script(
			notCommittedOutcome(domain.ErrCodeUpstreamUnavailable), // primary: authoritative no-commit
			chatSuccess(fallback, "", "", chatModel),               // fallback: committed
		)
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey, idemKey: "idem-fallback", body: chatSuccessBody,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	accounts := harness.adapter.Accounts()
	if len(accounts) != 2 || accounts[0] != primary || accounts[1] != fallback {
		t.Fatalf("adapter walked %v, want primary then exactly one fallback", accounts)
	}
}

// AC: without authoritative no-commit proof (commit unknown), the Gateway fails
// closed and never falls back or replaces execution.
func TestChatFallbackBlockedWhenCommitUnknown(t *testing.T) {
	t.Parallel()

	var primary domain.ProviderAccountID = "pa_primary"
	var fallback domain.ProviderAccountID = "pa_fallback"

	harness := newChatHarness(t, func(h *chatHarness) {
		h.seedActive("tenant_a", string(primary), domain.AuthModeChatGPTCodexOAuth)
		h.seedActive("tenant_a", string(fallback), domain.AuthModeChatGPTCodexOAuth)
		h.routing.Seed("tenant_a", chatRoutingPolicy(
			[]domain.ProviderAccountID{primary},
			[]domain.ProviderAccountID{fallback},
		))
		h.adapter.Script(unknownOutcome())
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey, idemKey: "idem-unknown", body: chatSuccessBody,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	body := decodeError(t, payload)
	if body["code"] != "execution_possibly_committed" {
		t.Fatalf("error code = %v, want execution_possibly_committed", body["code"])
	}
	if calls := harness.adapter.CallCount(); calls != 1 {
		t.Fatalf("adapter calls = %d, want 1 (commit-unknown must not fall back)", calls)
	}
}

// AC: scope/admission vetoes before any routing/Adapter work.
func TestChatScopeForbiddenBeforeAdapter(t *testing.T) {
	t.Parallel()

	harness := newChatHarness(t, func(h *chatHarness) {
		h.seedActive("tenant_a", "pa_chat", domain.AuthModeChatGPTCodexOAuth)
	})

	// readOnly key lacks chat.completions scope.
	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost, path: "/v1/chat/completions", bearer: readOnly, idemKey: "idem-scope", body: chatSuccessBody,
	})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", response.StatusCode, payload)
	}
	if calls := harness.adapter.CallCount(); calls != 0 {
		t.Fatalf("adapter calls = %d, want 0 (scope gate precedes Adapter)", calls)
	}
	logs := harness.log.snapshot()
	if indexOf(logs, "adapter.run") != -1 {
		t.Fatalf("adapter ran despite scope denial: %v", logs)
	}
}

// AC: accounting/concurrency occupancy settles exactly once against the
// original Tenant + Client API Key (a second identical call is a no-op replay).
func TestChatAccountingSettlesOncePerExecution(t *testing.T) {
	t.Parallel()

	harness := newChatHarness(t, func(h *chatHarness) {
		h.seedActive("tenant_a", "pa_chat", domain.AuthModeChatGPTCodexOAuth)
	})

	harness.do(t, requestSpec{method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey, idemKey: "idem-settle-1", body: chatSuccessBody})
	harness.do(t, requestSpec{method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey, idemKey: "idem-settle-2", body: chatSuccessBody})

	if settles := harness.admission.logicalSettleCount.Load(); settles != 2 {
		t.Fatalf("logical settle count = %d, want 2 (one per distinct execution)", settles)
	}
	if !harness.settledKeysAllContain("tenant_a") {
		t.Fatalf("all settlements must be scoped to the original Tenant tenant_a")
	}
}

// AC: model_unavailable surfaces as a distinct canonical capability outcome.
func TestChatModelUnavailableCanonical(t *testing.T) {
	t.Parallel()

	harness := newChatHarness(t, func(h *chatHarness) {
		account := activeAccount("pa_chat", domain.AuthModeChatGPTCodexOAuth)
		stripped, health, permit := seedAccountHealth(account)
		h.accounts.seed("tenant_a", stripped)
		h.health.Seed("tenant_a", account.ID, health, permit)
		// Snapshot offers chat but NOT gpt-4o (only another model).
		snapshot := chatCapabilitySnapshot(account.ID, account.AuthMode, account.Credential.Version, "other-model")
		h.capabilities.seed("tenant_a", snapshot)
		h.routing.Seed("tenant_a", chatRoutingPolicy([]domain.ProviderAccountID{account.ID}, nil))
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey, idemKey: "idem-model", body: chatSuccessBody,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	body := decodeError(t, payload)
	if body["code"] != "model_unavailable" {
		t.Fatalf("error code = %v, want model_unavailable", body["code"])
	}
	if calls := harness.adapter.CallCount(); calls != 0 {
		t.Fatalf("adapter calls = %d, want 0 for model_unavailable", calls)
	}
}

// AC: lifecycle/risk gates reject before Any Adapter work. A gated Auth Mode
// whose Tenant has not acknowledged residual risk is rejected risk_ack_required
// with zero Adapter calls.
func TestChatRiskAckGateRejectedBeforeAdapter(t *testing.T) {
	t.Parallel()

	harness := newChatHarness(t, func(h *chatHarness) {
		account := activeAccount("pa_risk", domain.AuthModeChatGPTCodexOAuth)
		account.RiskAcknowledged = false
		stripped, health, permit := seedAccountHealth(account)
		h.accounts.seed("tenant_a", stripped)
		h.health.Seed("tenant_a", account.ID, health, permit)
		h.routing.Seed("tenant_a", chatRoutingPolicy([]domain.ProviderAccountID{account.ID}, nil))
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey, idemKey: "idem-risk", body: chatSuccessBody,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	body := decodeError(t, payload)
	if body["code"] != "risk_ack_required" {
		t.Fatalf("error code = %v, want risk_ack_required", body["code"])
	}
	if calls := harness.adapter.CallCount(); calls != 0 {
		t.Fatalf("adapter calls = %d, want 0 (risk gate precedes Adapter)", calls)
	}
}

// AC: health gate rejects a cooling/blocked account before Any Adapter work.
func TestChatHealthBlockedGateRejectedBeforeAdapter(t *testing.T) {
	t.Parallel()

	harness := newChatHarness(t, func(h *chatHarness) {
		account := activeAccount("pa_health", domain.AuthModeChatGPTCodexOAuth)
		// Overlay a cooling_down account-scope condition to block selection.
		stripped, _, permit := seedAccountHealth(account)
		h.accounts.seed("tenant_a", stripped)
		h.health.Seed("tenant_a", account.ID, domain.HealthSummary{
			SummaryState: domain.HealthCoolingDown,
			Conditions: []domain.HealthCondition{{
				Scope:             domain.HealthScope{Kind: domain.HealthScopeAccount},
				State:             domain.HealthCoolingDown,
				Reason:            domain.HealthReasonProviderRateLimited,
				CredentialVersion: account.Credential.Version,
				Remediation:       domain.RemediationWaitProviderCooldown,
			}},
		}, permit)
		h.routing.Seed("tenant_a", chatRoutingPolicy([]domain.ProviderAccountID{account.ID}, nil))
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey, idemKey: "idem-health", body: chatSuccessBody,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	if calls := harness.adapter.CallCount(); calls != 0 {
		t.Fatalf("adapter calls = %d, want 0 (health gate precedes Adapter)", calls)
	}
}
