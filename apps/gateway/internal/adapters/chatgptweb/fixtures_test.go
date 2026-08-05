package chatgptweb_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/adapters/chatgptweb"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// fixturePlaceholder is the only credential-shaped value any fixture may carry.
// TestFixturesCarryNoRealSecrets enforces it so a real token cannot be pasted
// into testdata later without failing the suite (OP-G3).
const fixturePlaceholder = "fixture-not-a-real-token"

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
	closed   atomic.Bool
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

func (stream *sseStream) Next() (string, bool, error) {
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
	stream.closed.Store(true)
	return nil
}

// fixtureTransport answers exchanges from a per-path script. It records every
// request so a test can assert what the Adapter did and did NOT call — the
// "never solved a challenge" and "never retried" proofs are absence proofs.
type fixtureTransport struct {
	mu sync.Mutex
	// responses maps an upstream path to the response it should produce.
	responses map[string]chatgptweb.Response
	// requests records every exchange in order.
	requests []chatgptweb.Request
	// err, when non-nil, fails every exchange.
	err error
}

func newFixtureTransport() *fixtureTransport {
	return &fixtureTransport{responses: map[string]chatgptweb.Response{}}
}

func (transport *fixtureTransport) on(path string, response chatgptweb.Response) *fixtureTransport {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	transport.responses[path] = response
	return transport
}

func (transport *fixtureTransport) Exchange(_ context.Context, request chatgptweb.Request) (chatgptweb.Response, error) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	transport.requests = append(transport.requests, request)
	if transport.err != nil {
		return chatgptweb.Response{}, transport.err
	}
	response, ok := transport.responses[request.Path]
	if !ok {
		// An unscripted path is a test authoring bug, not a Provider behavior.
		// Returning 500 keeps the Adapter on its unavailable path rather than
		// silently succeeding on an empty body.
		return chatgptweb.Response{Status: 500}, nil
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
type staticCredential struct {
	material string
	uses     atomic.Int32
}

func (credential *staticCredential) Use(fn func(string) error) error {
	credential.uses.Add(1)
	return fn(credential.material)
}

// recordingSink captures canonical deltas the Adapter delivered.
type recordingSink struct {
	mu         sync.Mutex
	deltas     []string
	heartbeats int
	// failAfter, when > 0, returns an error once that many deltas were delivered
	// so a test can prove the Adapter stops producing into a dead sink.
	failAfter int
	err       error
}

func (sink *recordingSink) Delta(delta domain.ChatDelta) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.failAfter > 0 && len(sink.deltas) >= sink.failAfter {
		return sink.err
	}
	sink.deltas = append(sink.deltas, delta.Content)
	return nil
}

func (sink *recordingSink) Heartbeat() error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.heartbeats++
	return nil
}

func (sink *recordingSink) content() []string {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]string(nil), sink.deltas...)
}

func (sink *recordingSink) joined() string {
	return strings.Join(sink.content(), "")
}
