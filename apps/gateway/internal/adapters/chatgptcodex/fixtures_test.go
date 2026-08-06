package chatgptcodex_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/adapters/chatgptcodex"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// fixturePlaceholder is the only credential-shaped value any fixture may carry.
// TestFixturesCarryNoRealSecrets enforces it so a real token cannot be pasted
// into testdata later without failing the suite (OP-G3).
const fixturePlaceholder = "fixture-not-a-real-token"

// codexBundleMaterial is the sanitized OAuth bundle handed to the Adapter inside
// the CredentialInjection callback. It carries only fixture placeholders; the
// access_token / refresh_token / account_id fields are what the Adapter parses
// inside the boundary to build its headers and refresh grant.
func codexBundleMaterial() string {
	bundle := struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		AccountID    string `json:"account_id"`
	}{
		AccessToken:  "fixture-access-token",
		RefreshToken: "fixture-refresh-token",
		AccountID:    "fixture-account-id",
	}
	encoded, _ := json.Marshal(bundle)
	return string(encoded)
}

// loadFixture reads one sanitized fixture file.
func loadFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(data)
}

// loadFixtureSection reads one named object out of a multi-case JSON fixture and
// returns it re-encoded, so a test feeds the Adapter exactly the upstream shape.
func loadFixtureSection(t *testing.T, name, section string) string {
	t.Helper()
	var document map[string]json.RawMessage
	if err := json.Unmarshal([]byte(loadFixture(t, name)), &document); err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
	raw, ok := document[section]
	if !ok {
		t.Fatalf("fixture %s has no section %q", name, section)
	}
	return string(raw)
}

// sseStream replays SSE payload lines from a fixture transcript.
type sseStream struct {
	payloads []string
	cursor   int
	// closes counts Close() calls rather than recording a bare "was closed" flag.
	// See the T18 chatgptweb sseStream for the rationale: counting distinguishes
	// zero closes (a leaked upstream body) from two closes (two owners) from the
	// correct single close; a boolean cannot see either.
	closes atomic.Int32
	// failAt, when >= 0, returns an error instead of the payload at that index so
	// a test can prove mid-stream failure classification.
	failAt int
	err    error
}

func newSSEStream(transcript string) *sseStream {
	var payloads []string
	for _, line := range strings.Split(transcript, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		payloads = append(payloads, trimmed)
	}
	return &sseStream{payloads: payloads, failAt: -1}
}

// errStreamClosed is returned by Next after Close, mirroring what a real closed
// HTTP response body does. This makes the close state load-bearing: if the
// Adapter ever read a payload after releasing the body, a fixture that silently
// kept serving would let that read succeed and the suite would bless a
// use-after-close.
var errStreamClosed = errors.New("fixture stream: read after close")

func (stream *sseStream) Next() (string, bool, error) {
	if stream.closes.Load() > 0 {
		return "", false, errStreamClosed
	}
	if stream.failAt >= 0 && stream.cursor == stream.failAt {
		return "", false, stream.err
	}
	if stream.cursor >= len(stream.payloads) {
		return "", false, nil
	}
	payload := stream.payloads[stream.cursor]
	stream.cursor++
	return payload, true, nil
}

func (stream *sseStream) Close() error {
	stream.closes.Add(1)
	return nil
}

// closeCount reports how many times the Adapter released this stream.
func (stream *sseStream) closeCount() int {
	return int(stream.closes.Load())
}

// fixtureTransport answers exchanges from a per-path script. It records every
// request so a test can assert what the Adapter did and did NOT call — the
// "never solved a challenge" and "no full-operation retry" proofs are absence
// proofs.
type fixtureTransport struct {
	mu sync.Mutex
	// responses maps an upstream path to the response it should produce.
	responses map[string]chatgptcodex.Response
	// requests records every exchange in order.
	requests []chatgptcodex.Request
	// err, when non-nil, fails every exchange.
	err error
}

func newFixtureTransport() *fixtureTransport {
	return &fixtureTransport{responses: map[string]chatgptcodex.Response{}}
}

func (transport *fixtureTransport) on(path string, response chatgptcodex.Response) *fixtureTransport {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	transport.responses[path] = response
	return transport
}

func (transport *fixtureTransport) Exchange(_ context.Context, request chatgptcodex.Request) (chatgptcodex.Response, error) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	transport.requests = append(transport.requests, request)
	if transport.err != nil {
		return chatgptcodex.Response{}, transport.err
	}
	response, ok := transport.responses[request.Path]
	if !ok {
		// An unscripted path is a test authoring bug, not a Provider behavior.
		return chatgptcodex.Response{Status: 500}, nil
	}
	return response, nil
}

// paths returns the ordered upstream paths the Adapter exchanged.
func (transport *fixtureTransport) paths() []string {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	out := make([]string, 0, len(transport.requests))
	for _, request := range transport.requests {
		out = append(out, request.Path)
	}
	return out
}

// count returns how many times the Adapter exchanged one path.
func (transport *fixtureTransport) count(path string) int {
	total := 0
	for _, seen := range transport.paths() {
		if seen == path {
			total++
		}
	}
	return total
}

// staticCredential is a controlled CredentialInjection that hands the Adapter a
// placeholder secret inside a callback, exactly like the real vault boundary.
//
// It deliberately does NOT implement ports.CredentialRotation, so it is also the
// fixture that proves the Adapter refuses to rotate credential material on its
// own when no boundary owns rotation.
type staticCredential struct {
	material string
	uses     atomic.Int32
}

func (credential *staticCredential) Use(fn func(string) error) error {
	credential.uses.Add(1)
	return fn(credential.material)
}

// rotatingCredential is a controlled CredentialInjection that ALSO owns
// rotation, standing in for the authorized Vault boundary. It records what a real
// boundary must do and nothing the Adapter is allowed to do itself: it persists
// the complete rotated set, advances the credential version, dedupes concurrent
// rotations, and audits.
type rotatingCredential struct {
	mu       sync.Mutex
	material string
	version  int
	// audits records one entry per persisted rotation, so a test can prove the
	// Adapter asked the boundary exactly once rather than rotating in a loop. It
	// carries no material.
	audits []string
	// persistErr, when non-nil, fails persistence so a test can prove the Adapter
	// never re-sends on material the Vault does not hold.
	persistErr error
	// rotationUnsupported makes Rotate report ErrCredentialRotationUnsupported,
	// standing in for a boundary whose Vault has no rotation store wired.
	rotationUnsupported bool
	uses                atomic.Int32
}

func (credential *rotatingCredential) Use(fn func(string) error) error {
	credential.uses.Add(1)
	credential.mu.Lock()
	material := credential.material
	credential.mu.Unlock()
	return fn(material)
}

func (credential *rotatingCredential) Rotate(
	_ context.Context,
	exchange func() (string, error),
	use func(string) error,
) error {
	if credential.rotationUnsupported {
		return ports.ErrCredentialRotationUnsupported
	}
	// The lock is the fixture's stand-in for per-(tenant, account) dedupe: two
	// racing rotations cannot each spend the same single-use refresh material.
	credential.mu.Lock()
	defer credential.mu.Unlock()

	rotated, err := exchange()
	if err != nil {
		return err
	}
	if credential.persistErr != nil {
		// Persistence failed, so `use` is never invoked: the Provider may have
		// rotated but the Gateway did not, and re-sending would proceed on material
		// the Vault does not hold.
		return credential.persistErr
	}
	credential.material = rotated
	credential.version++
	credential.audits = append(credential.audits, "credential.rotated")
	return use(rotated)
}

// rotatedRefreshTokenPersisted reports the refresh_token the boundary now holds,
// which is what proves the rotated set — not merely the access token — survived
// the rotation.
func (credential *rotatingCredential) rotatedRefreshTokenPersisted(t *testing.T) string {
	t.Helper()
	credential.mu.Lock()
	defer credential.mu.Unlock()
	var bundle struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal([]byte(credential.material), &bundle); err != nil {
		t.Fatalf("persisted material is not a decodable bundle: %v", err)
	}
	return bundle.RefreshToken
}

func (credential *rotatingCredential) rotationCount() int {
	credential.mu.Lock()
	defer credential.mu.Unlock()
	return len(credential.audits)
}

func (credential *rotatingCredential) persistedVersion() int {
	credential.mu.Lock()
	defer credential.mu.Unlock()
	return credential.version
}

// recordingSink captures canonical deltas the Adapter delivered.
type recordingSink struct {
	mu         sync.Mutex
	deltas     []string
	heartbeats int
}

func (sink *recordingSink) Delta(delta domain.ChatDelta) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.deltas = append(sink.deltas, delta.Content)
	return nil
}

func (sink *recordingSink) Heartbeat() error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.heartbeats++
	return nil
}

func (sink *recordingSink) joined() string {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return strings.Join(append([]string(nil), sink.deltas...), "")
}

// chatCommand builds one controlled non-streaming command.
func chatCommand(model string) ports.ChatCommand {
	return ports.ChatCommand{
		AccountID:   "pa_gated_codex",
		AuthMode:    domain.AuthModeChatGPTCodexOAuth,
		Version:     1,
		Operation:   domain.ChatOpCompletion,
		Model:       model,
		Messages:    []domain.ChatMessage{{Role: domain.ChatRoleUser, Content: "hello"}},
		ExecutionID: "exec_fixture_codex_0001",
	}
}

// streamCommand builds one controlled streaming command.
func streamCommand(model string) ports.ChatStreamCommand {
	return ports.ChatStreamCommand{
		AccountID:   "pa_gated_codex",
		AuthMode:    domain.AuthModeChatGPTCodexOAuth,
		Version:     1,
		Operation:   domain.ChatOpCompletionStreaming,
		Model:       model,
		Messages:    []domain.ChatMessage{{Role: domain.ChatRoleUser, Content: "hello"}},
		ExecutionID: "exec_fixture_codex_0001",
	}
}

// probeCommand builds one controlled probe command.
func probeCommand() ports.ProbeCommand {
	return ports.ProbeCommand{
		AccountID: "pa_gated_codex",
		AuthMode:  domain.AuthModeChatGPTCodexOAuth,
		Version:   1,
		Scope:     domain.HealthScope{Kind: domain.HealthScopeAccount},
	}
}

// capabilityCommand builds one controlled capability observation command.
func capabilityCommand() ports.CapabilityObservationCommand {
	return ports.CapabilityObservationCommand{
		AccountID: "pa_gated_codex",
		AuthMode:  domain.AuthModeChatGPTCodexOAuth,
		Version:   1,
	}
}

// compactJSON re-encodes a JSON document compactly so a pretty-printed fixture
// section becomes a single SSE payload line for newSSEStream.
func compactJSON(t *testing.T, document string) string {
	t.Helper()
	var raw any
	if err := json.Unmarshal([]byte(document), &raw); err != nil {
		t.Fatalf("compactJSON: %v", err)
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("compactJSON re-encode: %v", err)
	}
	return string(encoded)
}

// responsesTransport scripts one SSE body at the Codex Responses path.
func responsesTransport(t *testing.T, transcript string) *fixtureTransport {
	t.Helper()
	stream := newSSEStream(transcript)
	return newFixtureTransport().on(chatgptcodex.PathCodexResponses, chatgptcodex.Response{
		Status: http.StatusOK,
		Stream: stream,
	})
}

// streamOf returns the stream scripted at the Responses path so a test can
// assert close behavior.
func streamOf(t *testing.T, transport *fixtureTransport) *sseStream {
	t.Helper()
	for _, response := range transport.responses {
		if s, ok := response.Stream.(*sseStream); ok {
			return s
		}
	}
	t.Fatal("no sseStream scripted on the transport")
	return nil
}
