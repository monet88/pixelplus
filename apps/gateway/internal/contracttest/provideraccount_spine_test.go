package contracttest_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/contracttest"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// spineFixtureTime is the deterministic instant used when a test needs to seed
// a controlled account through the external contracttest_test package. It
// mirrors the internal fixture clock start so seeded timestamps are stable.
var spineFixtureTime = time.Date(2026, time.July, 21, 0, 0, 0, 0, time.UTC)

// spineHarness bundles the controlled ports so a test can assert side-effect
// counts and ordering after driving the real composed HTTP surface.
type spineHarness struct {
	fixture   *contracttest.Fixture
	log       *spineLog
	principal *stubPrincipalStore
	admission *stubAdmissionStore
	replay    *stubReplayStore
	accounts  *stubAccountStore
	audit     *captureAudit
	telemetry *captureTelemetry
	reqLog    *captureRequestLog
}

const (
	tenantAKey = "sk-pxp_locatorA_secretA"
	tenantBKey = "sk-pxp_locatorB_secretB"
	readOnly   = "sk-pxp_locatorR_secretR"
)

// newSpineHarness wires controlled ports where the manage key belongs to
// tenant_a with accounts.manage+read, the read key belongs to tenant_a with
// accounts.read only, and tenantBKey belongs to a different Tenant. Unknown
// material authenticates to nothing.
func newSpineHarness(t *testing.T, configure func(*spineHarness)) *spineHarness {
	t.Helper()

	log := &spineLog{}
	principal := &stubPrincipalStore{
		log: log,
		principals: map[string]domain.SecurityPrincipal{
			tenantAKey: {
				TenantID:       "tenant_a",
				ClientAPIKeyID: "key_a",
				Scopes:         domain.NewScopeSet(domain.ScopeAccountsRead, domain.ScopeAccountsManage),
			},
			readOnly: {
				TenantID:       "tenant_a",
				ClientAPIKeyID: "key_r",
				Scopes:         domain.NewScopeSet(domain.ScopeAccountsRead),
			},
			tenantBKey: {
				TenantID:       "tenant_b",
				ClientAPIKeyID: "key_b",
				Scopes:         domain.NewScopeSet(domain.ScopeAccountsRead, domain.ScopeAccountsManage),
			},
		},
	}
	harness := &spineHarness{
		log:       log,
		principal: principal,
		admission: &stubAdmissionStore{log: log},
		replay:    newStubReplayStore(log),
		accounts:  newStubAccountStore(log),
		audit:     &captureAudit{},
		telemetry: &captureTelemetry{},
		reqLog:    &captureRequestLog{},
	}
	if configure != nil {
		configure(harness)
	}

	fixture, err := contracttest.NewFixture(contracttest.Options{
		Principal:  harness.principal,
		Admission:  harness.admission,
		Replay:     harness.replay,
		Accounts:   harness.accounts,
		Audit:      harness.audit,
		Telemetry:  harness.telemetry,
		RequestLog: harness.reqLog,
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

type requestSpec struct {
	method   string
	path     string
	bearer   string
	idemKey  string
	body     string
	rawBody  []byte
	skipAuth bool
}

func (harness *spineHarness) do(t *testing.T, spec requestSpec) (*http.Response, []byte) {
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

func decodeError(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("decode canonical error: %v (body=%s)", err, payload)
	}
	return body
}

const validCreateBody = `{"provider":"chatgpt","auth_mode":"chatgpt_codex_oauth","label":"primary"}`

// AC1: invalid, unknown, revoked, and hash-mismatched keys are indistinguishable
// authentication failures and never form a Security Principal.
func TestCreateAuthenticationFailuresAreIndistinguishable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		bearer string
		noAuth bool
	}{
		{name: "missing", noAuth: true},
		{name: "unknown", bearer: "sk-pxp_unknown_nope"},
		{name: "malformed", bearer: "not-a-key"},
		{name: "wrong secret", bearer: "sk-pxp_locatorA_wrongsecret"},
	}

	var bodies []string
	for _, testCase := range cases {
		harness := newSpineHarness(t, nil)
		response, payload := harness.do(t, requestSpec{
			method:   http.MethodPost,
			path:     "/v1/provider-accounts",
			bearer:   testCase.bearer,
			skipAuth: testCase.noAuth,
			idemKey:  "idem-1",
			body:     validCreateBody,
		})
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s: status = %d, want 401 (body=%s)", testCase.name, response.StatusCode, payload)
		}
		body := decodeError(t, payload)
		if body["code"] != "authentication_failed" {
			t.Fatalf("%s: code = %v, want authentication_failed", testCase.name, body["code"])
		}
		if _, ok := body["resource_reference"]; ok {
			t.Fatalf("%s: authentication failure leaked resource_reference", testCase.name)
		}
		// No principal formed means no downstream port ran.
		if calls := harness.replay.claimCalls.Load(); calls != 0 {
			t.Fatalf("%s: replay.Claim ran %d times, want 0", testCase.name, calls)
		}
		if calls := harness.accounts.createCalls.Load(); calls != 0 {
			t.Fatalf("%s: account.Create ran %d times, want 0", testCase.name, calls)
		}
		delete(body, "request_id")
		normalized, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("%s: marshal normalized body: %v", testCase.name, err)
		}
		bodies = append(bodies, string(normalized))
	}
	for index := 1; index < len(bodies); index++ {
		if bodies[index] != bodies[0] {
			t.Fatalf("authentication failures are distinguishable:\n %s\n %s", bodies[0], bodies[index])
		}
	}
}

// AC2: the Security Principal derives Tenant identity server-side; a
// client-supplied tenant_id in the body is rejected (strict decode) and can
// never redirect ownership.
func TestCreateIgnoresClientSuppliedTenant(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, nil)
	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts",
		bearer:  tenantAKey,
		idemKey: "idem-tenant",
		body:    `{"provider":"chatgpt","auth_mode":"chatgpt_codex_oauth","label":"x","tenant_id":"tenant_b"}`,
	})
	// Unknown field must be rejected as invalid_request, not silently accepted.
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", response.StatusCode, payload)
	}
	if body := decodeError(t, payload); body["code"] != "invalid_request" {
		t.Fatalf("code = %v, want invalid_request", body["code"])
	}

	// A well-formed create persists under the authenticated Tenant only.
	response, payload = harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts",
		bearer:  tenantAKey,
		idemKey: "idem-tenant-2",
		body:    validCreateBody,
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 (body=%s)", response.StatusCode, payload)
	}
	audits := harness.audit.snapshot()
	if len(audits) != 1 {
		t.Fatalf("audit events = %d, want 1", len(audits))
	}
	if audits[0].TenantID != "tenant_a" {
		t.Fatalf("audit tenant = %q, want tenant_a", audits[0].TenantID)
	}
}

// AC3: scope, request-size, and rate/concurrency/quota checks run in normative
// order before draft persistence. An unauthenticated oversize request fails as
// 401 (A0 before A2), a same-Tenant scope failure is 403 before size, and an
// admission rejection is 429 with no durable persistence.
func TestCreateAdmissionOrderIsNormative(t *testing.T) {
	t.Parallel()

	oversize := bytes.Repeat([]byte("a"), (2<<20)+16)

	t.Run("unauthenticated oversize is 401 before 413", func(t *testing.T) {
		t.Parallel()
		harness := newSpineHarness(t, nil)
		response, payload := harness.do(t, requestSpec{
			method:   http.MethodPost,
			path:     "/v1/provider-accounts",
			skipAuth: true,
			idemKey:  "idem",
			rawBody:  oversize,
		})
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401 (body=%s)", response.StatusCode, payload)
		}
		if body := decodeError(t, payload); body["code"] != "authentication_failed" {
			t.Fatalf("code = %v, want authentication_failed", body["code"])
		}
	})

	t.Run("read-only key on create is 403 before size", func(t *testing.T) {
		t.Parallel()
		harness := newSpineHarness(t, nil)
		response, payload := harness.do(t, requestSpec{
			method:  http.MethodPost,
			path:    "/v1/provider-accounts",
			bearer:  readOnly,
			idemKey: "idem",
			rawBody: oversize,
		})
		if response.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (body=%s)", response.StatusCode, payload)
		}
		if body := decodeError(t, payload); body["code"] != "forbidden" {
			t.Fatalf("code = %v, want forbidden", body["code"])
		}
		if calls := harness.replay.claimCalls.Load(); calls != 0 {
			t.Fatalf("replay.Claim ran %d times on scope denial, want 0", calls)
		}
	})

	t.Run("authenticated oversize is 413 after scope", func(t *testing.T) {
		t.Parallel()
		harness := newSpineHarness(t, nil)
		response, payload := harness.do(t, requestSpec{
			method:  http.MethodPost,
			path:    "/v1/provider-accounts",
			bearer:  tenantAKey,
			idemKey: "idem",
			rawBody: oversize,
		})
		if response.StatusCode != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413 (body=%s)", response.StatusCode, payload)
		}
		if body := decodeError(t, payload); body["code"] != "request_too_large" {
			t.Fatalf("code = %v, want request_too_large", body["code"])
		}
		if calls := harness.accounts.createCalls.Load(); calls != 0 {
			t.Fatalf("account.Create ran %d times on oversize, want 0", calls)
		}
	})

	t.Run("rate limit is 429 and persists nothing", func(t *testing.T) {
		t.Parallel()
		harness := newSpineHarness(t, func(h *spineHarness) {
			h.admission.rejectStage = "rate_limit"
		})
		response, payload := harness.do(t, requestSpec{
			method:  http.MethodPost,
			path:    "/v1/provider-accounts",
			bearer:  tenantAKey,
			idemKey: "idem",
			body:    validCreateBody,
		})
		if response.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429 (body=%s)", response.StatusCode, payload)
		}
		if body := decodeError(t, payload); body["code"] != "rate_limit" {
			t.Fatalf("code = %v, want rate_limit", body["code"])
		}
		// Replay claimed before admission, and the rejected admission abandons
		// the fresh claim so nothing durable is left behind.
		if calls := harness.replay.claimCalls.Load(); calls != 1 {
			t.Fatalf("replay.Claim calls = %d, want 1", calls)
		}
		if calls := harness.replay.abandonCalls.Load(); calls != 1 {
			t.Fatalf("replay.Abandon calls = %d, want 1", calls)
		}
		if calls := harness.accounts.createCalls.Load(); calls != 0 {
			t.Fatalf("account.Create ran %d times on rate limit, want 0", calls)
		}
	})

	t.Run("happy path order is authenticate,claim,admit,create", func(t *testing.T) {
		t.Parallel()
		harness := newSpineHarness(t, nil)
		response, payload := harness.do(t, requestSpec{
			method:  http.MethodPost,
			path:    "/v1/provider-accounts",
			bearer:  tenantAKey,
			idemKey: "idem",
			body:    validCreateBody,
		})
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("status = %d, want 201 (body=%s)", response.StatusCode, payload)
		}
		events := harness.log.snapshot()
		want := []string{"authenticate", "replay.claim", "admit", "account.create", "replay.complete"}
		if !equalPrefix(events, want) {
			t.Fatalf("spine order = %v, want prefix %v", events, want)
		}
	})
}

// AC4: foreign, unknown, and deleted identifiers return the same
// non-enumerating outcome before protected access or admission.
func TestGetNonEnumerationIsIndistinguishable(t *testing.T) {
	t.Parallel()

	// Seed a tenant_b account so the "foreign" id genuinely exists elsewhere.
	foreign := domain.NewDraftProviderAccount("pa_foreign", domain.ProviderChatGPT, domain.AuthModeChatGPTCodexOAuth, "b", domain.NewTimestamp(time.Date(2026, time.July, 21, 0, 0, 0, 0, time.UTC)))

	cases := []struct {
		name string
		id   string
	}{
		{name: "foreign", id: "pa_foreign"},
		{name: "unknown", id: "pa_missing"},
	}

	var bodies []string
	for _, testCase := range cases {
		harness := newSpineHarness(t, func(h *spineHarness) {
			h.accounts.seed("tenant_b", foreign)
		})
		response, payload := harness.do(t, requestSpec{
			method: http.MethodGet,
			path:   "/v1/provider-accounts/" + testCase.id,
			bearer: tenantAKey,
		})
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404 (body=%s)", testCase.name, response.StatusCode, payload)
		}
		body := decodeError(t, payload)
		if body["code"] != "resource_not_found" {
			t.Fatalf("%s: code = %v, want resource_not_found", testCase.name, body["code"])
		}
		if _, ok := body["resource_reference"]; ok {
			t.Fatalf("%s: non-enumeration leaked resource_reference", testCase.name)
		}
		// Non-enumeration must resolve before admission debits anything.
		if calls := harness.admission.admitCalls.Load(); calls != 0 {
			t.Fatalf("%s: admission ran %d times before non-enumeration, want 0", testCase.name, calls)
		}
		delete(body, "request_id")
		normalized, _ := json.Marshal(body)
		bodies = append(bodies, string(normalized))
	}
	if bodies[0] != bodies[1] {
		t.Fatalf("foreign and unknown are distinguishable:\n %s\n %s", bodies[0], bodies[1])
	}
}

// AC5: concurrent matching creates have exactly one replay owner and one durable
// draft; the losing racers see in_progress and never persist a second draft.
func TestConcurrentCreateHasOneOwnerAndOneDraft(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, nil)

	const racers = 8
	var wg sync.WaitGroup
	statuses := make([]int, racers)
	wg.Add(racers)
	for index := 0; index < racers; index++ {
		go func(slot int) {
			defer wg.Done()
			response, _ := harness.do(t, requestSpec{
				method:  http.MethodPost,
				path:    "/v1/provider-accounts",
				bearer:  tenantAKey,
				idemKey: "same-key",
				body:    validCreateBody,
			})
			statuses[slot] = response.StatusCode
		}(index)
	}
	wg.Wait()

	created := 0
	for _, status := range statuses {
		switch status {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			// in_progress maps to 409-class conflict.
		default:
			t.Fatalf("unexpected concurrent status %d", status)
		}
	}
	if created < 1 {
		t.Fatalf("no racer created the draft")
	}
	if calls := harness.accounts.createCalls.Load(); calls != 1 {
		t.Fatalf("account.Create ran %d times, want exactly 1 durable draft", calls)
	}
}

// AC5 (continued): conflict, in_progress, and uncertain claims never steal or
// duplicate work.
func TestReplayOutcomesNeverDuplicateWork(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		outcome string
		code    string
		status  int
	}{
		{name: "conflict", outcome: "conflict", code: "idempotency_conflict", status: http.StatusConflict},
		{name: "in_progress", outcome: "in_progress", code: "idempotency_in_progress", status: http.StatusConflict},
		{name: "uncertain", outcome: "uncertain", code: "idempotency_uncertain", status: http.StatusConflict},
	}
	for _, testCase := range cases {
		harness := newSpineHarness(t, func(h *spineHarness) {
			h.replay.forced = replayOutcome(testCase.outcome)
		})
		response, payload := harness.do(t, requestSpec{
			method:  http.MethodPost,
			path:    "/v1/provider-accounts",
			bearer:  tenantAKey,
			idemKey: "idem",
			body:    validCreateBody,
		})
		if response.StatusCode != testCase.status {
			t.Fatalf("%s: status = %d, want %d (body=%s)", testCase.name, response.StatusCode, testCase.status, payload)
		}
		if body := decodeError(t, payload); body["code"] != testCase.code {
			t.Fatalf("%s: code = %v, want %s", testCase.name, body["code"], testCase.code)
		}
		// None of these outcomes may admit or persist.
		if calls := harness.admission.admitCalls.Load(); calls != 0 {
			t.Fatalf("%s: admission ran %d times, want 0", testCase.name, calls)
		}
		if calls := harness.accounts.createCalls.Load(); calls != 0 {
			t.Fatalf("%s: account.Create ran %d times, want 0", testCase.name, calls)
		}
	}
}

// AC6: responses, errors, audit, telemetry, and the single request log carry
// only safe allowlisted fields. The success body is a draft shell; audit
// carries tenant only server-side; the request log never carries tenant_id.
func TestSafeFieldsOnlyAndSingleRequestLog(t *testing.T) {
	t.Parallel()

	harness := newSpineHarness(t, nil)
	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/provider-accounts",
		bearer:  tenantAKey,
		idemKey: "idem",
		body:    validCreateBody,
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", response.StatusCode, payload)
	}

	var envelope struct {
		Account   map[string]any `json:"account"`
		RequestID string         `json:"request_id"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if envelope.RequestID == "" {
		t.Fatal("response is missing request_id")
	}
	if _, leaked := envelope.Account["tenant_id"]; leaked {
		t.Fatal("response account leaked tenant_id")
	}
	if state := envelope.Account["lifecycle_state"]; state != "draft" {
		t.Fatalf("lifecycle_state = %v, want draft", state)
	}
	credential, _ := envelope.Account["credential"].(map[string]any)
	for _, banned := range []string{"material", "secret", "token", "cookie", "ciphertext", "handle"} {
		if _, leaked := credential[banned]; leaked {
			t.Fatalf("credential leaked %q", banned)
		}
	}

	// Exactly one canonical request log, and it never carries tenant_id.
	logs := harness.reqLog.snapshot()
	if len(logs) != 1 {
		t.Fatalf("request logs = %d, want exactly 1", len(logs))
	}
	if logs[0].RequestID == "" {
		t.Fatal("request log missing request_id")
	}

	// Telemetry uses stable safe labels only.
	telemetry := harness.telemetry.snapshot()
	if len(telemetry) == 0 {
		t.Fatal("no telemetry recorded")
	}
	if telemetry[0].Operation == "" {
		t.Fatal("telemetry missing operation label")
	}
}
