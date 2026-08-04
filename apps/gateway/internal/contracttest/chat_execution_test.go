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
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
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
	chatAudit    *stubChatAuditRecorder
}

func newChatHarness(t *testing.T, configure func(*chatHarness)) *chatHarness {
	t.Helper()
	return newChatHarnessWithOptions(t, func(harness *chatHarness, _ *contracttest.Options) {
		if configure != nil {
			configure(harness)
		}
	})
}

// newChatHarnessWithOptions builds the chat harness and lets the caller inject
// extra composition Options (for example the streaming Adapter and lease store)
// alongside the harness configuration, so both the non-streaming and streaming
// suites share one wiring path.
func newChatHarnessWithOptions(t *testing.T, configure func(*chatHarness, *contracttest.Options)) *chatHarness {
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
		chatAudit:    newStubChatAuditRecorder(log),
	}
	options := contracttest.Options{
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
		ChatAudit:    harness.chatAudit,
	}
	if configure != nil {
		configure(harness, &options)
	}

	fixture, err := contracttest.NewFixture(options)
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
	if choice["finish_reason"] != string(domain.FinishStop) {
		t.Fatalf("choices[0].finish_reason = %v, want stop", choice["finish_reason"])
	}
	message, _ := choice["message"].(map[string]any)
	if message["role"] != string(domain.ChatRoleAssistant) {
		t.Fatalf("choices[0].message.role = %v, want assistant", message["role"])
	}
	// The created timestamp is a canonical integer Unix-seconds value.
	if created, ok := body["created"].(float64); !ok || created <= 0 {
		t.Fatalf("created = %v, want a positive integer Unix-seconds timestamp", body["created"])
	}
	// Safe metadata block present; no raw Provider framing leaks.
	meta, ok := body["x_pixelplus"].(map[string]any)
	if !ok {
		t.Fatalf("x_pixelplus metadata absent (body=%s)", payload)
	}
	if meta["provider_account_id"] != "pa_chat" {
		t.Fatalf("x_pixelplus.provider_account_id = %v, want pa_chat", meta["provider_account_id"])
	}
	if meta["finish_class"] != string(domain.FinishStop) {
		t.Fatalf("x_pixelplus.finish_class = %v, want stop", meta["finish_class"])
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
	// The normative evaluation sequence: authenticate → scope/A2 digest → replay
	// claim → admission → deterministic routing (account visible) → capability
	// (C4) → circuit → Vault presence → audit-before-allow → Adapter → replay
	// completion/settlement. Each gate below is asserted explicitly so a regression
	// that drops a gate (audit-before-allow, risk, health, circuit, X2) fails this
	// test instead of passing silently.
	if !containsSeq(logs,
		"authenticate", "digest", "replay.claim", "admit", "account.visible",
		"capability.get", "circuit.surface_open", "vault.validate", "chat.audit", "adapter.run", "replay.complete",
	) {
		t.Fatalf("gate order violated: %v", logs)
	}
	// Adapter must run after all gates, before replay completion/settlement.
	if indexOf(logs, "adapter.run") >= indexOf(logs, "replay.complete") {
		t.Fatalf("adapter.run must precede replay.complete: %v", logs)
	}

	// X2 reaffirmation: candidateRejection re-runs (risk → health → capability →
	// circuit) immediately before Vault, so each protected gate is evaluated at
	// least twice for the single primary account. Risk is pinned transitively: it
	// is the first gate inside candidateRejection, so reaching health/capability/
	// circuit (asserted below) proves the risk-ack gate passed.
	if got := countEvents(logs, "capability.get"); got < 2 {
		t.Fatalf("capability.get evaluated %d times, want >= 2 (C4 + X2)", got)
	}
	if got := countEvents(logs, "circuit.surface_open"); got < 2 {
		t.Fatalf("circuit.surface_open evaluated %d times, want >= 2 (circuit + X2)", got)
	}
	if got := harness.health.readCalls.Load(); got < 2 {
		t.Fatalf("health gate evaluated %d times, want >= 2 (health + X2)", got)
	}

	// audit-before-allow: the protected-access intent audit must be recorded
	// before any credential release (AuthorizedChatService emits it synchronously
	// immediately before the Adapter runs).
	var sawProtectedAccess bool
	for _, ev := range harness.chatAudit.snapshot() {
		if ev.Action != ports.AuditChatProtectedAccess {
			continue
		}
		sawProtectedAccess = true
		if ev.Outcome != "intent" {
			t.Fatalf("protected_access audit outcome = %q, want intent", ev.Outcome)
		}
		if ev.ProviderAccountID != "pa_chat" {
			t.Fatalf("protected_access audit provider_account_id = %v, want pa_chat", ev.ProviderAccountID)
		}
	}
	if !sawProtectedAccess {
		t.Fatalf("no audit-before-allow protected_access audit recorded")
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

// AC: the idempotency fingerprint binds every accepted request field
// (idempotency policy §5.2, canonical-errors §7.1, chat spec obligation 22):
// a same-key request differing in any contracted field — generation tuning,
// message name, or x_pixelplus routing inputs — returns idempotency_conflict
// instead of replaying the original completion, even though the Adapter does
// not consume the tuning values yet (T19–T23). An exact resend still replays.
func TestChatReplayFingerprintCoversAcceptedRequestFields(t *testing.T) {
	t.Parallel()

	harness := newChatHarness(t, func(h *chatHarness) {
		h.seedActive("tenant_a", "pa_chat", domain.AuthModeChatGPTCodexOAuth)
	})

	base := `{"model":"gpt-4o","messages":[` +
		`{"role":"system","content":"be kind"},` +
		`{"role":"user","name":"dao","content":[{"type":"text","text":"hello "},{"type":"text","text":"world"}]}` +
		`],"temperature":0.7,"max_tokens":64,"top_p":0.9,"n":1,"stop":["\n","END"],"user":"u_1",` +
		`"x_pixelplus":{"conversation_id":"conv-wire"}}`

	first, firstPayload := harness.do(t, requestSpec{
		method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey, idemKey: "idem-fp", body: base,
	})
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first status = %d (body=%s)", first.StatusCode, firstPayload)
	}
	firstID := decodeCompletionBody(t, firstPayload)["id"]

	// Each variation differs from the base in exactly one accepted field.
	variations := []struct {
		name     string
		fragment [2]string // base fragment -> varied fragment
	}{
		{"temperature", [2]string{`"temperature":0.7`, `"temperature":0.8`}},
		{"max_tokens", [2]string{`"max_tokens":64`, `"max_tokens":65`}},
		{"top_p", [2]string{`"top_p":0.9`, `"top_p":0.8`}},
		{"n", [2]string{`"n":1`, `"n":2`}},
		{"stop", [2]string{`"stop":["\n","END"]`, `"stop":["\n","DONE"]`}},
		{"user", [2]string{`"user":"u_1"`, `"user":"u_2"`}},
		{"message name", [2]string{`"name":"dao"`, `"name":"ren"`}},
		{"conversation_id", [2]string{`"conversation_id":"conv-wire"`, `"conversation_id":"conv-other"`}},
		{"provider_account_id", [2]string{`"conversation_id":"conv-wire"`, `"conversation_id":"conv-wire","provider_account_id":"pa_chat"`}},
		{"allow_fallback", [2]string{`"conversation_id":"conv-wire"`, `"conversation_id":"conv-wire","allow_fallback":true`}},
	}
	for _, tc := range variations {
		if !strings.Contains(base, tc.fragment[0]) {
			t.Fatalf("%s: base body lost fragment %s", tc.name, tc.fragment[0])
		}
		body := strings.Replace(base, tc.fragment[0], tc.fragment[1], 1)
		response, payload := harness.do(t, requestSpec{
			method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey, idemKey: "idem-fp", body: body,
		})
		if response.StatusCode != http.StatusConflict {
			t.Fatalf("%s: status = %d, want 409 (body=%s)", tc.name, response.StatusCode, payload)
		}
		if code := decodeError(t, payload)["code"]; code != "idempotency_conflict" {
			t.Fatalf("%s: error code = %v, want idempotency_conflict", tc.name, code)
		}
	}

	// An exact resend still replays the original completion.
	replay, replayPayload := harness.do(t, requestSpec{
		method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey, idemKey: "idem-fp", body: base,
	})
	if replay.StatusCode != http.StatusOK {
		t.Fatalf("replay status = %d (body=%s)", replay.StatusCode, replayPayload)
	}
	if got := decodeCompletionBody(t, replayPayload)["id"]; got != firstID {
		t.Fatalf("replay id = %v, want original %v", got, firstID)
	}
	if calls := harness.adapter.CallCount(); calls != 1 {
		t.Fatalf("adapter calls = %d, want 1 (conflicts and replays never re-execute)", calls)
	}
}

// AC: semantically equal stop forms share one fingerprint — the single-string
// form is canonicalized to a one-element list, so `"stop":"END"` and
// `"stop":["END"]` replay instead of falsely conflicting.
func TestChatReplayFingerprintCanonicalizesStopForm(t *testing.T) {
	t.Parallel()

	harness := newChatHarness(t, func(h *chatHarness) {
		h.seedActive("tenant_a", "pa_chat", domain.AuthModeChatGPTCodexOAuth)
	})

	first, firstPayload := harness.do(t, requestSpec{
		method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey, idemKey: "idem-fp-stop",
		body: `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"stop":"END"}`,
	})
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first status = %d (body=%s)", first.StatusCode, firstPayload)
	}
	firstID := decodeCompletionBody(t, firstPayload)["id"]

	replay, replayPayload := harness.do(t, requestSpec{
		method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey, idemKey: "idem-fp-stop",
		body: `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"stop":["END"]}`,
	})
	if replay.StatusCode != http.StatusOK {
		t.Fatalf("replay status = %d, want 200 for the canonical-equal stop form (body=%s)", replay.StatusCode, replayPayload)
	}
	if got := decodeCompletionBody(t, replayPayload)["id"]; got != firstID {
		t.Fatalf("replay id = %v, want original %v", got, firstID)
	}
	if calls := harness.adapter.CallCount(); calls != 1 {
		t.Fatalf("adapter calls = %d, want 1 (canonical-equal forms replay)", calls)
	}
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

// AC: an authoritative no-commit proof with an unrecognized/empty failure
// class still counts as not-committed evidence — the single-owner fallback
// walk continues instead of failing closed (decision 0012).
func TestChatFallbackContinuesOnUnclassifiedNotCommitted(t *testing.T) {
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
		h.adapter.Script(
			notCommittedOutcome(domain.ErrorCode("")), // primary: no-commit proof, unclassified
			chatSuccess(fallback, "", "", chatModel),  // fallback: committed
		)
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey, idemKey: "idem-unclassified", body: chatSuccessBody,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	accounts := harness.adapter.Accounts()
	if len(accounts) != 2 || accounts[0] != primary || accounts[1] != fallback {
		t.Fatalf("adapter walked %v, want primary then exactly one fallback", accounts)
	}
}

// AC: an unclassified not-committed terminal failure surfaces the generic
// provider_rejected (never possibly_committed) and abandons the replay claim,
// so a later retry with the same Idempotency-Key re-executes instead of
// sticking on a leaked in-progress claim (decision 0012).
func TestChatUnclassifiedNotCommittedIsProviderRejected(t *testing.T) {
	t.Parallel()

	harness := newChatHarness(t, func(h *chatHarness) {
		h.seedActive("tenant_a", "pa_chat", domain.AuthModeChatGPTCodexOAuth)
		h.adapter.Script(notCommittedOutcome(domain.ErrorCode("")))
	})

	for attempt := 1; attempt <= 2; attempt++ {
		response, payload := harness.do(t, requestSpec{
			method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey, idemKey: "idem-unclassified-terminal", body: chatSuccessBody,
		})
		if response.StatusCode != http.StatusBadGateway {
			t.Fatalf("attempt %d: status = %d, want 502 (body=%s)", attempt, response.StatusCode, payload)
		}
		body := decodeError(t, payload)
		if body["code"] != "provider_rejected" {
			t.Fatalf("attempt %d: error code = %v, want provider_rejected", attempt, body["code"])
		}
		// Provider/upstream runtime errors surface as upstream_rejection (502),
		// never the old pre-admission account_policy (409) class.
		if body["status_class"] != "upstream_rejection" {
			t.Fatalf("attempt %d: status_class = %v, want upstream_rejection", attempt, body["status_class"])
		}
		if body["category"] != "execution" {
			t.Fatalf("attempt %d: category = %v, want execution", attempt, body["category"])
		}
	}
	if calls := harness.adapter.CallCount(); calls != 2 {
		t.Fatalf("adapter calls = %d, want 2 (retry must re-claim, not stick on a leaked claim)", calls)
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

// AC: an explicit x_pixelplus.provider_account_id pin (P1) selects the pinned
// same-Tenant account even when policy order prefers another — selection
// precedence chooses a deterministic same-Tenant account and never widens.
func TestChatExplicitPinSelectsPinnedAccount(t *testing.T) {
	t.Parallel()

	var first domain.ProviderAccountID = "pa_first"
	var second domain.ProviderAccountID = "pa_second"

	harness := newChatHarness(t, func(h *chatHarness) {
		h.seedActive("tenant_a", string(first), domain.AuthModeChatGPTCodexOAuth)
		h.seedActive("tenant_a", string(second), domain.AuthModeChatGPTCodexOAuth)
		h.routing.Seed("tenant_a", chatRoutingPolicy(
			[]domain.ProviderAccountID{first, second},
			nil,
		))
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-pin",
		body:    `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"x_pixelplus":{"provider_account_id":"pa_second"}}`,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	accounts := harness.adapter.Accounts()
	if len(accounts) != 1 || accounts[0] != second {
		t.Fatalf("adapter served %v, want exactly the pinned %s", accounts, second)
	}
	body := decodeCompletionBody(t, payload)
	meta, _ := body["x_pixelplus"].(map[string]any)
	if meta["provider_account_id"] != string(second) {
		t.Fatalf("response provider_account_id = %v, want %s", meta["provider_account_id"], second)
	}
}

// AC: a foreign or unknown explicit pin fails closed 404-class
// (resource_not_found) — non-enumerating, zero Adapter calls, zero Vault
// decrypts (routing spec §3.2/§4.1 P1/§7.2 NF-XTENANT; chat spec §8 rule 1,
// test obligation 26). Foreign and unknown ids are indistinguishable. The
// zero-decrypt obligation is asserted through the Vault validate boundary
// counter: the pin path must never reach credential work.
func TestChatExplicitPinForeignIsNotFound(t *testing.T) {
	t.Parallel()

	harness := newChatHarness(t, func(h *chatHarness) {
		h.seedActive("tenant_a", "pa_chat", domain.AuthModeChatGPTCodexOAuth)
		// A foreign tenant_b account must never be enumerated through the pin.
		h.accounts.seed("tenant_b", activeAccount("pa_foreign", domain.AuthModeChatGPTCodexOAuth))
	})

	for _, pin := range []string{"pa_foreign", "pa_unknown"} {
		response, payload := harness.do(t, requestSpec{
			method:  http.MethodPost,
			path:    "/v1/chat/completions",
			bearer:  tenantAKey,
			idemKey: "idem-pin-404-" + pin,
			body:    `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"x_pixelplus":{"provider_account_id":"` + pin + `"}}`,
		})
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("pin %s: status = %d, want 404 (body=%s)", pin, response.StatusCode, payload)
		}
		body := decodeError(t, payload)
		if body["code"] != "resource_not_found" {
			t.Fatalf("pin %s: error code = %v, want resource_not_found", pin, body["code"])
		}
	}
	if calls := harness.adapter.CallCount(); calls != 0 {
		t.Fatalf("adapter calls = %d, want 0 (foreign/unknown pin never reaches execution)", calls)
	}
	if validates := harness.vault.validCalls.Load(); validates != 0 {
		t.Fatalf("vault validate calls = %d, want 0 (foreign/unknown pin does no credential work)", validates)
	}
}

// AC: a same-Tenant pin that is visible but gated fails closed with the
// SPECIFIC gate class (routing spec §4.1 P1), never the 404 reserved for
// foreign/unknown ids.
func TestChatExplicitPinSameTenantGatedKeepsSpecificClass(t *testing.T) {
	t.Parallel()

	harness := newChatHarness(t, func(h *chatHarness) {
		account := activeAccount("pa_gated", domain.AuthModeChatGPTCodexOAuth)
		account.RiskAcknowledged = false
		stripped, health, permit := seedAccountHealth(account)
		h.accounts.seed("tenant_a", stripped)
		h.health.Seed("tenant_a", account.ID, health, permit)
		h.routing.Seed("tenant_a", chatRoutingPolicy([]domain.ProviderAccountID{account.ID}, nil))
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-pin-gated",
		body:    `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"x_pixelplus":{"provider_account_id":"pa_gated"}}`,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	body := decodeError(t, payload)
	if body["code"] != "risk_ack_required" {
		t.Fatalf("error code = %v, want risk_ack_required (specific gate class, not 404)", body["code"])
	}
	if calls := harness.adapter.CallCount(); calls != 0 {
		t.Fatalf("adapter calls = %d, want 0 (gate precedes Adapter)", calls)
	}
}

// AC: cross-Auth-Mode fallback fails closed unless the Tenant policy names
// BOTH modes in fallback_auth_modes (routing spec §6.2, §7.1 NF-XMODE). With
// no modes listed, the walk stops at the primary's own outcome.
func TestChatFallbackCrossModeRequiresPolicyListedModes(t *testing.T) {
	t.Parallel()

	var primary domain.ProviderAccountID = "pa_primary"
	var fallback domain.ProviderAccountID = "pa_fallback_xmode"

	harness := newChatHarness(t, func(h *chatHarness) {
		h.seedActive("tenant_a", string(primary), domain.AuthModeChatGPTCodexOAuth)
		// Cross-mode fallback target: a different, product-enabled Auth Mode.
		h.seedActive("tenant_a", string(fallback), domain.AuthModeGeminiAntigravityOAuth)
		h.routing.Seed("tenant_a", chatRoutingPolicyWithModes(
			[]domain.ProviderAccountID{primary},
			[]domain.ProviderAccountID{fallback},
			[]domain.AuthMode{}, // policy names no modes: cross-mode fallback forbidden
		))
		h.adapter.Script(
			notCommittedOutcome(domain.ErrCodeUpstreamUnavailable), // primary: authoritative no-commit
			chatSuccess(fallback, "", "", chatModel),               // would succeed — must never run
		)
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey, idemKey: "idem-xmode", body: chatSuccessBody,
	})
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (primary's own outcome, body=%s)", response.StatusCode, payload)
	}
	body := decodeError(t, payload)
	if body["code"] != "upstream_unavailable" {
		t.Fatalf("error code = %v, want upstream_unavailable (fail closed on primary)", body["code"])
	}
	accounts := harness.adapter.Accounts()
	if len(accounts) != 1 || accounts[0] != primary {
		t.Fatalf("adapter walked %v, want only the primary (NF-XMODE fail closed)", accounts)
	}
}

// AC: cross-Auth-Mode fallback is permitted when the policy names both the
// primary's and the target's mode — the single-owner walk then runs exactly
// once on authoritative no-commit proof (routing spec §6.2).
func TestChatFallbackCrossModeWithListedModes(t *testing.T) {
	t.Parallel()

	var primary domain.ProviderAccountID = "pa_primary"
	var fallback domain.ProviderAccountID = "pa_fallback_xmode"

	harness := newChatHarness(t, func(h *chatHarness) {
		h.seedActive("tenant_a", string(primary), domain.AuthModeChatGPTCodexOAuth)
		h.seedActive("tenant_a", string(fallback), domain.AuthModeGeminiAntigravityOAuth)
		h.routing.Seed("tenant_a", chatRoutingPolicyWithModes(
			[]domain.ProviderAccountID{primary},
			[]domain.ProviderAccountID{fallback},
			[]domain.AuthMode{domain.AuthModeChatGPTCodexOAuth, domain.AuthModeGeminiAntigravityOAuth},
		))
		h.adapter.Script(
			notCommittedOutcome(domain.ErrCodeUpstreamUnavailable),
			chatSuccess(fallback, "", "", chatModel),
		)
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey, idemKey: "idem-xmode-ok", body: chatSuccessBody,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	accounts := harness.adapter.Accounts()
	if len(accounts) != 2 || accounts[0] != primary || accounts[1] != fallback {
		t.Fatalf("adapter walked %v, want primary then exactly one cross-mode fallback", accounts)
	}
}

// AC: conversation affinity (P3) prefers the account that last served the
// conversation over fresh policy order, and yields to P4 the moment that
// account leaves the candidate set — never a foreign or cross-mode account
// (routing spec §5.1, chat spec §5.2, test obligation 14).
func TestChatConversationAffinityPrefersPriorAccount(t *testing.T) {
	t.Parallel()

	var first domain.ProviderAccountID = "pa_affinity_a"
	var second domain.ProviderAccountID = "pa_affinity_b"

	harness := newChatHarness(t, func(h *chatHarness) {
		h.seedActive("tenant_a", string(first), domain.AuthModeChatGPTCodexOAuth)
		h.seedActive("tenant_a", string(second), domain.AuthModeChatGPTCodexOAuth)
		policy := chatRoutingPolicy([]domain.ProviderAccountID{first, second}, nil)
		policy.Affinity = domain.AffinityPolicy{Enabled: true, WindowClass: "AFFINITY-WINDOW-CLASS"}
		h.routing.Seed("tenant_a", policy)
	})

	affinityBody := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"x_pixelplus":{"conversation_id":"conv-1"}}`

	// Turn 1: no preference recorded — P4 picks the first policy candidate.
	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey, idemKey: "idem-aff-1", body: affinityBody,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("turn 1: status = %d (body=%s)", response.StatusCode, payload)
	}

	// Policy order flips: P4 alone would now pick the second account.
	flipped := chatRoutingPolicy([]domain.ProviderAccountID{second, first}, nil)
	flipped.Affinity = domain.AffinityPolicy{Enabled: true, WindowClass: "AFFINITY-WINDOW-CLASS"}
	harness.routing.Seed("tenant_a", flipped)

	// Turn 2: P3 affinity still prefers the first account (it remains a candidate).
	response, payload = harness.do(t, requestSpec{
		method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey, idemKey: "idem-aff-2", body: affinityBody,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("turn 2: status = %d (body=%s)", response.StatusCode, payload)
	}

	// The preferred account leaves the candidate set (scoped cooling_down).
	accountA := activeAccount(string(first), domain.AuthModeChatGPTCodexOAuth)
	harness.health.Seed("tenant_a", accountA.ID, domain.HealthSummary{
		SummaryState: domain.HealthCoolingDown,
		Conditions: []domain.HealthCondition{{
			Scope:             domain.HealthScope{Kind: domain.HealthScopeAccount},
			State:             domain.HealthCoolingDown,
			Reason:            domain.HealthReasonProviderRateLimited,
			CredentialVersion: accountA.Credential.Version,
			Remediation:       domain.RemediationWaitProviderCooldown,
		}},
	}, domain.RecoveryPermit{})

	// Turn 3: affinity yields — P4 policy picks the surviving second account.
	response, payload = harness.do(t, requestSpec{
		method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey, idemKey: "idem-aff-3", body: affinityBody,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("turn 3: status = %d (body=%s)", response.StatusCode, payload)
	}

	accounts := harness.adapter.Accounts()
	if len(accounts) != 3 || accounts[0] != first || accounts[1] != first || accounts[2] != second {
		t.Fatalf("adapter served %v, want [%s %s %s] (policy, affinity, affinity-yield)", accounts, first, first, second)
	}
}

// AC: the request wire accepts every field the published ChatCompletionRequest
// schema declares — generation tuning fields, message name, stop as string
// array, and array text content — instead of rejecting them as unknown.
func TestChatRequestWireAcceptsContractDeclaredFields(t *testing.T) {
	t.Parallel()

	harness := newChatHarness(t, func(h *chatHarness) {
		h.seedActive("tenant_a", "pa_chat", domain.AuthModeChatGPTCodexOAuth)
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-wire-full",
		body: `{"model":"gpt-4o","messages":[` +
			`{"role":"system","content":"be kind"},` +
			`{"role":"user","name":"dao","content":[{"type":"text","text":"hello "},{"type":"text","text":"world"}]}` +
			`],"stream":false,"temperature":0.7,"max_tokens":64,"top_p":0.9,"n":1,"stop":["\n","END"],"user":"u_1",` +
			`"x_pixelplus":{"conversation_id":"conv-wire"}}`,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 for contract-declared fields (body=%s)", response.StatusCode, payload)
	}
	if calls := harness.adapter.CallCount(); calls != 1 {
		t.Fatalf("adapter calls = %d, want 1", calls)
	}
}

// AC: contract-declared fields with out-of-shape values fail as
// invalid_request, not as unknown fields: a non-text content part (the
// canonical surface carries text only), out-of-range max_tokens, a present
// JSON null on the non-nullable numeric options or stream (the schema types
// are not nullable — null is not omission), and null items inside a stop
// array (the schema declares string items).
func TestChatRequestWireRejectsInvalidShapes(t *testing.T) {
	t.Parallel()

	harness := newChatHarness(t, func(h *chatHarness) {
		h.seedActive("tenant_a", "pa_chat", domain.AuthModeChatGPTCodexOAuth)
	})

	cases := []struct {
		name string
		body string
	}{
		{"non-text content part", `{"model":"gpt-4o","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.test/x.png"}}]}]}`},
		{"max_tokens below minimum", `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"max_tokens":0}`},
		{"stop wrong shape", `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"stop":42}`},
		{"null temperature", `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"temperature":null}`},
		{"null max_tokens", `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"max_tokens":null}`},
		{"null top_p", `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"top_p":null}`},
		{"null n", `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"n":null}`},
		{"null stream", `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"stream":null}`},
		{"stop array with null item", `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"stop":["ok",null]}`},
	}
	for _, tc := range cases {
		response, payload := harness.do(t, requestSpec{
			method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey, idemKey: "idem-wire-bad-" + tc.name, body: tc.body,
		})
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400 (body=%s)", tc.name, response.StatusCode, payload)
		}
		body := decodeError(t, payload)
		if body["code"] != "invalid_request" {
			t.Fatalf("%s: error code = %v, want invalid_request", tc.name, body["code"])
		}
	}
	if calls := harness.adapter.CallCount(); calls != 0 {
		t.Fatalf("adapter calls = %d, want 0 (validation precedes Adapter)", calls)
	}
}

// AC: health gate rejects a cooling/blocked account before Any Adapter work. The
// cooling/blocked health condition must be what prevents Adapter execution — the
// request is seeded with a fresh chat capability snapshot so it passes the
// capability gate and reaches the health gate first.
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
		// Seed the same fresh chat capability snapshot the successful health-gate
		// cases use so the request passes capability and is vetoed by the health
		// condition (not by a missing capability snapshot).
		h.capabilities.seed("tenant_a", chatCapabilitySnapshot(account.ID, account.AuthMode, account.Credential.Version, chatModel))
		h.routing.Seed("tenant_a", chatRoutingPolicy([]domain.ProviderAccountID{account.ID}, nil))
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost, path: "/v1/chat/completions", bearer: tenantAKey, idemKey: "idem-health", body: chatSuccessBody,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	// The cooling/blocked condition is what blocks: account_not_usable with the
	// provider cooldown remediation (not capability_unverified / snapshot error).
	body := decodeError(t, payload)
	if body["code"] != "account_not_usable" {
		t.Fatalf("error code = %v, want account_not_usable (health gate)", body["code"])
	}
	if body["remediation"] != string(domain.RemediationWaitProviderCooldown) {
		t.Fatalf("remediation = %v, want %s", body["remediation"], domain.RemediationWaitProviderCooldown)
	}
	if calls := harness.adapter.CallCount(); calls != 0 {
		t.Fatalf("adapter calls = %d, want 0 (health gate precedes Adapter)", calls)
	}
}
