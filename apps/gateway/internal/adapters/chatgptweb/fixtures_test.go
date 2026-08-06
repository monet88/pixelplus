package chatgptweb_test

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
	// closes counts Close() calls rather than recording a bare "was closed" flag.
	//
	// A boolean could only answer "closed at least once", which is not the property
	// worth holding. consumeStream closes the body in a `defer` on the single
	// function that owns it, so the two ways that can go wrong are closing ZERO
	// times (the upstream connection is leaked for every turn — under load the
	// Adapter exhausts sockets and starts failing turns that have nothing wrong
	// with them) and closing TWICE (a real net/http body double-close is a latent
	// panic or error the fixture would otherwise hide). Counting distinguishes
	// both from the correct single close; a boolean cannot see either.
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
// HTTP response body does (`http: read on closed response body`).
//
// This is what makes the close state load-bearing instead of decorative. If the
// Adapter ever read a payload after releasing the body, a fixture that silently
// kept serving from its slice would let that read succeed and the test suite
// would bless a use-after-close that fails against a real transport.
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

// TestTheAdapterReleasesTheUpstreamStreamExactlyOnce asserts the close signal the
// sseStream fixture records. Without this test the counter would be written and
// never read — dead state carrying no signal, which is exactly the defect the
// bare `closed atomic.Bool` had.
//
// Why exactly once, on both surfaces:
//
//   - Zero closes leaks the upstream body. consumeStream owns the stream and
//     releases it in a `defer`, so deleting that defer compiles, passes every
//     content assertion in this package (the payloads still decode identically),
//     and silently leaks one connection per chat turn. Against a real transport
//     that is socket exhaustion under sustained load: turns start failing for
//     reasons unrelated to their own content.
//   - Two closes means two owners think they hold the body. A fixture Close is a
//     harmless counter increment, but net/http's is not idempotent in the way
//     callers assume, so a double-close that a fixture tolerates is a defect that
//     only appears in a lab deployment with a real transport wired in.
//
// The streaming and non-streaming surfaces are checked separately because they
// are different call paths into the same decoder (Run aggregates with a nil
// deliver, Stream produces into the sink), and ownership could regress on one
// without the other.
func TestTheAdapterReleasesTheUpstreamStreamExactlyOnce(t *testing.T) {
	t.Parallel()

	newTransportWithStream := func(t *testing.T) (*fixtureTransport, *sseStream) {
		t.Helper()
		stream := newSSEStream(loadFixture(t, "chat_stream.sse"))
		transport := newFixtureTransport().
			on(chatgptweb.PathChatRequirements, chatgptweb.Response{
				Status: http.StatusOK,
				Body:   loadFixtureSection(t, "challenge.json", "no_challenge"),
			}).
			on(chatgptweb.PathConversation, chatgptweb.Response{
				Status: http.StatusOK,
				Stream: stream,
			})
		return transport, stream
	}

	t.Run("non-streaming Run", func(t *testing.T) {
		t.Parallel()
		transport, stream := newTransportWithStream(t)
		outcome, err := chatgptweb.New(transport).
			Run(t.Context(), chatCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		// Guard the guard: if the turn did not actually consume the body, a close
		// count of one would prove nothing about the streaming path.
		if outcome.Commit != domain.CommitCommitted {
			t.Fatalf("commit = %s, want committed (the stream must have been consumed for the close count to mean anything)", outcome.Commit)
		}
		if got := stream.closeCount(); got != 1 {
			t.Errorf("Close() called %d times, want exactly 1 (0 leaks the upstream body, >1 means two owners released it)", got)
		}
	})

	t.Run("streaming Stream", func(t *testing.T) {
		t.Parallel()
		transport, stream := newTransportWithStream(t)
		sink := &recordingSink{}
		outcome, err := chatgptweb.New(transport).
			Stream(t.Context(), streamCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder}, sink)
		if err != nil {
			t.Fatalf("Stream() error = %v", err)
		}
		if outcome.Commit != domain.CommitCommitted {
			t.Fatalf("commit = %s, want committed", outcome.Commit)
		}
		if got := stream.closeCount(); got != 1 {
			t.Errorf("Close() called %d times, want exactly 1", got)
		}
	})

	t.Run("a read after close is refused", func(t *testing.T) {
		t.Parallel()
		// Proves the close state actually gates Next, so the two assertions above
		// rest on a fixture that would surface a use-after-close rather than
		// quietly serving the next payload from its slice.
		stream := newSSEStream("first\nsecond\n")
		if _, ok, err := stream.Next(); !ok || err != nil {
			t.Fatalf("first Next() = (ok=%v, err=%v), want a payload", ok, err)
		}
		if err := stream.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if _, ok, err := stream.Next(); ok || !errors.Is(err, errStreamClosed) {
			t.Errorf("Next() after Close = (ok=%v, err=%v), want (false, read after close)", ok, err)
		}
	})
}
