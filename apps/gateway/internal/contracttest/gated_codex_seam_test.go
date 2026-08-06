package contracttest_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/composition"
	"github.com/monet88/pixelplus/apps/gateway/internal/contracttest"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// This file closes the #62 F6 gap: before it, the only Codex proofs reachable
// through the public HTTP seam were the REFUSALS (no operator flag, no Tenant
// ack). The Adapter's success paths — probe, chat, and stream — had package unit
// coverage only, which is not completion evidence for a story whose claim is that
// an operator-enabled deployment can actually serve the gated mode.
//
// Every test here drives the composed handler over real production composition
// with the gated Codex Adapter registered and given controlled egress, and
// asserts on the HTTP result plus what the Adapter did or did not exchange
// upstream. Nothing reaches into Adapter internals.
//
// The egress is supplied through composition.GatedCodexResponder rather than
// chatgptcodex.Transport because ADR 0009 forbids contracttest from importing
// internal/adapters, and TestGatewayImportsRespectDependencyDirection enforces
// that. The seam mirrors the transport's data shape and adds no behavior.

// Upstream Codex paths, spelled here so a drift in the Adapter's routing shows up
// as a failing seam proof rather than a silently unscripted exchange. They
// deliberately duplicate the adapter package's constants: contracttest may not
// import it, and a proof that shares the constant could not detect a change to
// it.
const (
	codexResponsesPath = "/backend-api/codex/responses"
	codexIdentityPath  = "/backend-api/me"
	codexModelsPath    = "/backend-api/models"
	codexTokenPath     = "https://auth.openai.com/oauth/token"
)

// codexSeamStream replays scripted SSE payload lines as the Adapter's stream.
type codexSeamStream struct {
	mu       sync.Mutex
	payloads []string
	cursor   int
	closed   bool
}

func newCodexSeamStream(payloads ...string) *codexSeamStream {
	return &codexSeamStream{payloads: payloads}
}

func (stream *codexSeamStream) Next() (string, bool, error) {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.closed || stream.cursor >= len(stream.payloads) {
		return "", false, nil
	}
	payload := stream.payloads[stream.cursor]
	stream.cursor++
	return payload, true, nil
}

func (stream *codexSeamStream) Close() error {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	stream.closed = true
	return nil
}

// codexSeamResponder answers gated Codex exchanges from a per-path script and
// records every exchange, so a test can assert both what the Adapter sent and
// what it never sent (the absence proofs).
type codexSeamResponder struct {
	mu sync.Mutex
	// replies maps an upstream path to the scripted reply. A nil stream factory
	// means a non-streaming reply.
	replies map[string]func() composition.GatedCodexReply
	paths   []string
}

func newCodexSeamResponder() *codexSeamResponder {
	return &codexSeamResponder{replies: map[string]func() composition.GatedCodexReply{}}
}

func (responder *codexSeamResponder) on(path string, reply func() composition.GatedCodexReply) *codexSeamResponder {
	responder.mu.Lock()
	defer responder.mu.Unlock()
	responder.replies[path] = reply
	return responder
}

// onJSON scripts a plain 200 JSON body at a path.
func (responder *codexSeamResponder) onJSON(path, body string) *codexSeamResponder {
	return responder.on(path, func() composition.GatedCodexReply {
		return composition.GatedCodexReply{Status: http.StatusOK, Body: body}
	})
}

// onStatus scripts a bare status at a path.
func (responder *codexSeamResponder) onStatus(path string, status int) *codexSeamResponder {
	return responder.on(path, func() composition.GatedCodexReply {
		return composition.GatedCodexReply{Status: status}
	})
}

// onSSE scripts a 200 SSE stream at a path. A fresh stream is built per exchange
// so a retry does not replay an exhausted body.
func (responder *codexSeamResponder) onSSE(path string, payloads ...string) *codexSeamResponder {
	return responder.on(path, func() composition.GatedCodexReply {
		return composition.GatedCodexReply{Status: http.StatusOK, Stream: newCodexSeamStream(payloads...)}
	})
}

func (responder *codexSeamResponder) Respond(
	_ context.Context,
	exchange composition.GatedCodexExchange,
) (composition.GatedCodexReply, error) {
	responder.mu.Lock()
	defer responder.mu.Unlock()
	responder.paths = append(responder.paths, exchange.Path)
	reply, ok := responder.replies[exchange.Path]
	if !ok {
		// An unscripted path is a test-authoring bug, not Provider behavior.
		return composition.GatedCodexReply{Status: http.StatusInternalServerError}, nil
	}
	return reply(), nil
}

// exchanged reports how many times the Adapter hit one upstream path.
func (responder *codexSeamResponder) exchanged(path string) int {
	responder.mu.Lock()
	defer responder.mu.Unlock()
	total := 0
	for _, seen := range responder.paths {
		if seen == path {
			total++
		}
	}
	return total
}

func (responder *codexSeamResponder) exchangedPaths() []string {
	responder.mu.Lock()
	defer responder.mu.Unlock()
	return append([]string(nil), responder.paths...)
}

// codexOAuthBundle is the credential material the authorized boundary releases to
// the gated Adapter. It is a sanitized OAuth bundle in the shape the Adapter
// parses inside its CredentialInjection callback; every value is a fixture
// placeholder.
const codexOAuthBundle = `{"access_token":"fixture-access-token","refresh_token":"fixture-refresh-token","account_id":"fixture-account-id"}`

// codexBundleAuthorizer is the controlled authorized-credential boundary for
// these proofs. It releases the Codex OAuth bundle only inside the callback,
// exactly like the real Vault boundary, and it OWNS rotation
// (ports.CredentialRotation) so the 401 rotate-and-retry path can be proved
// end-to-end through the seam.
type codexBundleAuthorizer struct {
	mu       sync.Mutex
	material string
	// rotations counts persisted rotations so a seam proof can assert the boundary,
	// not the Adapter, owned the rotation.
	rotations int
	version   int
}

func newCodexBundleAuthorizer() *codexBundleAuthorizer {
	return &codexBundleAuthorizer{material: codexOAuthBundle}
}

func (authorizer *codexBundleAuthorizer) Authorize(
	_ context.Context,
	validation ports.CredentialValidation,
	fn func(ports.CredentialInjection) error,
) error {
	if fn == nil || validation.AccountID == "" {
		return ports.ErrCredentialAbsent
	}
	return fn(&codexBundleInjection{authorizer: authorizer})
}

func (authorizer *codexBundleAuthorizer) rotationCount() int {
	authorizer.mu.Lock()
	defer authorizer.mu.Unlock()
	return authorizer.rotations
}

func (authorizer *codexBundleAuthorizer) persistedVersion() int {
	authorizer.mu.Lock()
	defer authorizer.mu.Unlock()
	return authorizer.version
}

// codexBundleInjection is the Use-scoped view of the bundle. It also implements
// ports.CredentialRotation, which is what lets the Adapter delegate a rotation
// rather than performing one itself.
type codexBundleInjection struct {
	authorizer *codexBundleAuthorizer
}

func (injection *codexBundleInjection) Use(fn func(string) error) error {
	if fn == nil {
		return ports.ErrChatAdapterUnavailable
	}
	injection.authorizer.mu.Lock()
	material := injection.authorizer.material
	injection.authorizer.mu.Unlock()
	return fn(material)
}

func (injection *codexBundleInjection) Rotate(
	_ context.Context,
	exchange func() (string, error),
	use func(string) error,
) error {
	injection.authorizer.mu.Lock()
	defer injection.authorizer.mu.Unlock()
	rotated, err := exchange()
	if err != nil {
		return err
	}
	injection.authorizer.material = rotated
	injection.authorizer.rotations++
	injection.authorizer.version++
	return use(rotated)
}

// codexSeamHarness composes a deployment where the operator enabled the gated
// Codex mode AND supplied egress, which is the only configuration in which the
// Adapter's success paths are reachable.
type codexSeamHarness struct {
	*chatHarness
	responder  *codexSeamResponder
	authorizer *codexBundleAuthorizer
}

func newCodexSeamHarness(t *testing.T, configure func(*codexSeamHarness, *contracttest.Options)) *codexSeamHarness {
	t.Helper()

	harness := &codexSeamHarness{
		responder:  newCodexSeamResponder(),
		authorizer: newCodexBundleAuthorizer(),
	}
	harness.chatHarness = newChatHarnessWithOptions(t, func(chat *chatHarness, options *contracttest.Options) {
		harness.chatHarness = chat
		// The chat harness principal carries only the chat/read scopes, but the
		// probe surface is an account-management operation. Granting the scope here
		// keeps one harness serving both the execution and probe proofs; without it
		// the probe tests would fail at authorization and never reach the Adapter.
		principal := chat.principal.principals[tenantAKey]
		principal.Scopes = domain.NewScopeSet(
			domain.ScopeChatCompletions,
			domain.ScopeCapabilitiesRead,
			domain.ScopeRoutingRead,
			domain.ScopeAccountsManage,
		)
		chat.principal.principals[tenantAKey] = principal
		// The gated Adapter only reaches dispatch when no explicit ChatAdapter
		// shadows it, so the counting fake is cleared: this suite proves the REAL
		// Adapter, not a stand-in.
		options.ChatAdapter = nil
		options.Probe = nil
		options.Capability = nil
		options.ChatCredentialAuthorizer = harness.authorizer
		options.GatedAuthModes = []domain.AuthMode{domain.AuthModeChatGPTCodexOAuth}
		options.GatedChatGPTCodexResponder = harness.responder
		if configure != nil {
			configure(harness, options)
		}
	})
	return harness
}

// AC5 (probe through the seam): with the mode enabled and egress supplied, a
// probe request reaches the real gated Adapter, which proves the credential with
// the minimal identity call and activates the account.
//
// The load-bearing assertions are the upstream ones: exactly one identity
// exchange, and ZERO exchanges on the Responses execution path. A probe that ran
// a generation would be billable, which I-PROBE-MINIMAL forbids.
func TestGatedCodexProbeProvesCredentialThroughTheSeam(t *testing.T) {
	t.Parallel()

	harness := newCodexSeamHarness(t, func(h *codexSeamHarness, _ *contracttest.Options) {
		h.responder.
			onJSON(codexIdentityPath, `{"email":"fixture-tenant@example.com","account_id":"fixture-account-id"}`).
			onJSON(codexModelsPath, `{"models":[{"slug":"`+chatModel+`"}]}`)
		account := probeableAccount("pa_codex_seam_probe", domain.AuthModeChatGPTCodexOAuth)
		stripped, health, permit := seedAccountHealth(account)
		h.accounts.seed("tenant_a", stripped)
		h.health.Seed("tenant_a", account.ID, health, permit)
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_codex_seam_probe/probe",
		bearer: tenantAKey,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	if got := harness.responder.exchanged(codexIdentityPath); got != 1 {
		t.Errorf("identity exchanges = %d, want 1 (paths=%v)", got, harness.responder.exchangedPaths())
	}
	if got := harness.responder.exchanged(codexResponsesPath); got != 0 {
		t.Errorf("Responses exchanges = %d, want 0 — a probe must never run a billable generation", got)
	}
}

// AC5 (probe auth verdict through the seam): a 401 on the identity call is a
// credential verdict, not a dependency outage. The account must move to
// reauth_required rather than the request answering 503, because the two lead an
// operator to opposite remediations.
func TestGatedCodexProbeReportsAuthFailureThroughTheSeam(t *testing.T) {
	t.Parallel()

	harness := newCodexSeamHarness(t, func(h *codexSeamHarness, _ *contracttest.Options) {
		h.responder.onStatus(codexIdentityPath, http.StatusUnauthorized)
		account := probeableAccount("pa_codex_seam_401", domain.AuthModeChatGPTCodexOAuth)
		stripped, health, permit := seedAccountHealth(account)
		h.accounts.seed("tenant_a", stripped)
		h.health.Seed("tenant_a", account.ID, health, permit)
	})

	response, payload := harness.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/provider-accounts/pa_codex_seam_401/probe",
		bearer: tenantAKey,
	})
	if response.StatusCode == http.StatusServiceUnavailable {
		t.Fatalf("status = 503, want a credential verdict rather than a dependency outage (body=%s)", payload)
	}
	if got := harness.responder.exchanged(codexIdentityPath); got != 1 {
		t.Errorf("identity exchanges = %d, want 1", got)
	}
	account, err := harness.accounts.Visible(t.Context(), managePrincipal(), "pa_codex_seam_401")
	if err != nil {
		t.Fatalf("Visible: %v", err)
	}
	if account.Lifecycle == domain.LifecycleActive {
		t.Fatalf("lifecycle = %s, want a non-active state after a rejected credential", account.Lifecycle)
	}
}

// AC2/AC3 (chat through the seam): an enabled deployment serves a non-streaming
// completion through the real gated Adapter, and the answer the client receives
// is the text the upstream SSE transcript produced.
func TestGatedCodexChatCompletesThroughTheSeam(t *testing.T) {
	t.Parallel()

	harness := newCodexSeamHarness(t, func(h *codexSeamHarness, _ *contracttest.Options) {
		h.responder.onSSE(codexResponsesPath,
			`{"type":"response.output_text.delta","delta":"Hel"}`,
			`{"type":"response.output_text.delta","delta":"lo"}`,
			`{"type":"response.completed"}`,
			`[DONE]`,
		)
		h.seedActive("tenant_a", "pa_codex_seam_chat", domain.AuthModeChatGPTCodexOAuth)
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-codex-seam-chat",
		body:    chatSuccessBody,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	if got := harness.responder.exchanged(codexResponsesPath); got != 1 {
		t.Errorf("Responses exchanges = %d, want exactly 1 (paths=%v)", got, harness.responder.exchangedPaths())
	}
	if content := chatCompletionContent(t, payload); content != "Hello" {
		t.Errorf("completion content = %q, want %q — the seam must carry the upstream text", content, "Hello")
	}
}

// A 200 that carries NO stream must fail closed rather than reading as an empty
// generation. This also pins the seam's nil-stream translation: assigning a nil
// GatedCodexStream straight onto the adapter's Stream field would produce a
// non-nil interface holding a nil value, so the Adapter's `opened.Stream == nil`
// fail-closed check would miss it and the turn would be committed as an empty
// answer the Provider never gave.
func TestGatedCodexChatFailsClosedOnATwoHundredWithoutAStreamThroughTheSeam(t *testing.T) {
	t.Parallel()

	harness := newCodexSeamHarness(t, func(h *codexSeamHarness, _ *contracttest.Options) {
		// 200 with a body and an explicitly absent stream.
		h.responder.onJSON(codexResponsesPath, `{"acknowledged":true}`)
		h.seedActive("tenant_a", "pa_codex_seam_nostream", domain.AuthModeChatGPTCodexOAuth)
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-codex-seam-nostream",
		body:    chatSuccessBody,
	})
	if response.StatusCode == http.StatusOK {
		t.Fatalf("status = 200, want a refusal: a 200 with no stream is not a generation (body=%s)", payload)
	}
	// The payload demonstrably reached the Provider, so the attempt must NOT be
	// reported as an authoritative non-commit — that would authorize a fallback
	// re-attempt that could bill a second generation.
	if code := decodeError(t, payload)["code"]; code == "" {
		t.Fatalf("no canonical error code in the refusal (body=%s)", payload)
	}
	if got := harness.responder.exchanged(codexResponsesPath); got != 1 {
		t.Errorf("Responses exchanges = %d, want 1 (no re-send)", got)
	}
}

// AC4 (no silent cross-account re-attempt on an authoritative refusal): a 401
// with no rotation available is refused, and the Adapter does NOT re-send the
// Responses exchange. A second send could bill a second generation.
func TestGatedCodexChatAuthFailureDoesNotReSendThroughTheSeam(t *testing.T) {
	t.Parallel()

	harness := newCodexSeamHarness(t, func(h *codexSeamHarness, _ *contracttest.Options) {
		h.responder.
			onStatus(codexResponsesPath, http.StatusUnauthorized).
			// A refused rotation grant: the account must reauthenticate.
			onJSON(codexTokenPath, `{"error":"refresh_token_reused"}`)
		h.seedActive("tenant_a", "pa_codex_seam_401_chat", domain.AuthModeChatGPTCodexOAuth)
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-codex-seam-chat-401",
		body:    chatSuccessBody,
	})
	if response.StatusCode == http.StatusOK {
		t.Fatalf("status = 200, want a refusal after a rejected credential (body=%s)", payload)
	}
	if got := harness.responder.exchanged(codexResponsesPath); got != 1 {
		t.Errorf("Responses exchanges = %d, want 1 — a refused rotation must not re-send the generation", got)
	}
	if got := harness.authorizer.rotationCount(); got != 0 {
		t.Errorf("boundary persisted %d rotations, want 0 on a refused grant", got)
	}
}

// F2/F10 through the seam: a 401 followed by a successful rotation completes the
// turn, and the ROTATION IS OWNED BY THE CREDENTIAL BOUNDARY — the rotated set is
// persisted and the credential version advances. This is the end-to-end
// counterpart of the Adapter unit proofs: it shows the delegation actually
// reaches the composed boundary rather than only the fixture in the adapter
// package.
func TestGatedCodexChatRotatesThroughTheCredentialBoundary(t *testing.T) {
	t.Parallel()

	var responsesCalls int
	var mu sync.Mutex
	harness := newCodexSeamHarness(t, func(h *codexSeamHarness, _ *contracttest.Options) {
		h.responder.
			on(codexResponsesPath, func() composition.GatedCodexReply {
				mu.Lock()
				defer mu.Unlock()
				responsesCalls++
				if responsesCalls == 1 {
					return composition.GatedCodexReply{Status: http.StatusUnauthorized}
				}
				return composition.GatedCodexReply{
					Status: http.StatusOK,
					Stream: newCodexSeamStream(
						`{"type":"response.output_text.delta","delta":"after rotation"}`,
						`{"type":"response.completed"}`,
						`[DONE]`,
					),
				}
			}).
			onJSON(codexTokenPath, `{"access_token":"fixture-rotated-access-token","refresh_token":"fixture-rotated-refresh-token","account_id":"fixture-account-id"}`)
		h.seedActive("tenant_a", "pa_codex_seam_rotate", domain.AuthModeChatGPTCodexOAuth)
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-codex-seam-rotate",
		body:    chatSuccessBody,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 after a boundary-owned rotation (body=%s)", response.StatusCode, payload)
	}
	if got := harness.responder.exchanged(codexTokenPath); got != 1 {
		t.Errorf("rotation grants = %d, want exactly 1 (no rotation loop)", got)
	}
	if got := harness.authorizer.rotationCount(); got != 1 {
		t.Errorf("boundary persisted %d rotations, want 1 — the boundary must own the rotation, not the Adapter", got)
	}
	if got := harness.authorizer.persistedVersion(); got != 1 {
		t.Errorf("credential version = %d, want 1 after a persisted rotation", got)
	}
	if content := chatCompletionContent(t, payload); content != "after rotation" {
		t.Errorf("completion content = %q, want the post-rotation text", content)
	}
}

// AC2/AC3 (stream through the seam): an enabled deployment serves a streaming
// completion through the real gated Adapter, and the canonical SSE event sequence
// the client receives carries the upstream deltas in order with exactly one
// terminal.
func TestGatedCodexStreamDeliversCanonicalEventsThroughTheSeam(t *testing.T) {
	t.Parallel()

	harness := newCodexSeamHarness(t, func(h *codexSeamHarness, options *contracttest.Options) {
		h.responder.onSSE(codexResponsesPath,
			`{"type":"response.output_text.delta","delta":"Hel"}`,
			`{"type":"response.output_text.delta","delta":"lo"}`,
			`{"type":"response.completed"}`,
			`[DONE]`,
		)
		// The gated Adapter must be the streaming Adapter too, so no scripted
		// streaming fake is injected.
		options.ChatStreamAdapter = nil
		options.ChatStreamLeases = nil

		account := activeAccount("pa_codex_seam_stream", domain.AuthModeChatGPTCodexOAuth)
		stripped, health, permit := seedAccountHealth(account)
		h.accounts.seed("tenant_a", stripped)
		h.health.Seed("tenant_a", account.ID, health, permit)
		h.capabilities.seed("tenant_a", chatStreamCapabilitySnapshot(
			account.ID, account.AuthMode, account.Credential.Version, chatModel, domain.StreamingReal))
		h.routing.Seed("tenant_a", chatRoutingPolicy([]domain.ProviderAccountID{account.ID}, nil))
	})

	response, payload := harness.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/chat/completions",
		bearer:  tenantAKey,
		idemKey: "idem-codex-seam-stream",
		body:    chatStreamBody,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", response.StatusCode, payload)
	}
	if got := response.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream (body=%s)", got, payload)
	}
	events := parseSSE(t, payload)
	if len(events) == 0 {
		t.Fatalf("no canonical events emitted (body=%s)", payload)
	}
	if events[0].Type != "open" {
		t.Fatalf("first event = %q, want open (types=%v)", events[0].Type, eventTypes(events))
	}
	if joined := joinDeltas(events); joined != "Hello" {
		t.Errorf("delivered deltas = %q, want %q", joined, "Hello")
	}
	if got := harness.responder.exchanged(codexResponsesPath); got != 1 {
		t.Errorf("Responses exchanges = %d, want exactly 1", got)
	}
}

// chatCompletionContent extracts the single assistant message content from a
// non-streaming completion payload.
func chatCompletionContent(t *testing.T, payload []byte) string {
	t.Helper()
	var body struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("decode completion: %v (body=%s)", err, payload)
	}
	if len(body.Choices) != 1 {
		t.Fatalf("choices = %d, want 1 (body=%s)", len(body.Choices), payload)
	}
	return body.Choices[0].Message.Content
}
