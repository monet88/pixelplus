package chatgptweb_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/adapters/chatgptweb"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// streamCommand builds one controlled streaming command.
func streamCommand(model string) ports.ChatStreamCommand {
	return ports.ChatStreamCommand{
		AccountID:   "pa_lab_web",
		AuthMode:    domain.AuthModeChatGPTWebAccess,
		Version:     1,
		Operation:   domain.ChatOpCompletionStreaming,
		Model:       model,
		Messages:    []domain.ChatMessage{{Role: domain.ChatRoleUser, Content: "hello"}},
		ExecutionID: "exec_fixture_0001",
	}
}

// chatCommand builds one controlled non-streaming command.
func chatCommand(model string) ports.ChatCommand {
	return ports.ChatCommand{
		AccountID:   "pa_lab_web",
		AuthMode:    domain.AuthModeChatGPTWebAccess,
		Version:     1,
		Operation:   domain.ChatOpCompletion,
		Model:       model,
		Messages:    []domain.ChatMessage{{Role: domain.ChatRoleUser, Content: "hello"}},
		ExecutionID: "exec_fixture_0001",
	}
}

// conversationTransport scripts a clean sentinel pre-flight plus one SSE body.
func conversationTransport(t *testing.T, transcript string) *fixtureTransport {
	t.Helper()
	return newFixtureTransport().
		on(chatgptweb.PathChatRequirements, chatgptweb.Response{
			Status: http.StatusOK,
			Body:   loadFixtureSection(t, "challenge.json", "no_challenge"),
		}).
		on(chatgptweb.PathConversation, chatgptweb.Response{
			Status: http.StatusOK,
			Stream: newSSEStream(transcript),
		})
}

func TestStreamTranslatesCanonicalDeltasInOrder(t *testing.T) {
	t.Parallel()

	transport := conversationTransport(t, loadFixture(t, "chat_stream.sse"))
	adapter := chatgptweb.New(transport)
	sink := &recordingSink{}
	credential := &staticCredential{material: fixturePlaceholder}

	outcome, err := adapter.Stream(t.Context(), streamCommand("gpt-fixture-1"), credential, sink)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	// Append patch, path-elided delta, and the batch-patch append must arrive in
	// generation order and reconstruct the assistant message exactly.
	if got := sink.joined(); got != "Hello world!" {
		t.Fatalf("delta content = %q, want %q", got, "Hello world!")
	}
	if outcome.Class != domain.ChatOutcomeCommitted {
		t.Errorf("class = %s, want committed", outcome.Class)
	}
	if outcome.Commit != domain.CommitCommitted {
		t.Errorf("commit = %s, want committed", outcome.Commit)
	}
	if outcome.FinishClass != domain.FinishStop {
		t.Errorf("finish class = %s, want stop", outcome.FinishClass)
	}
	// Markers, the resume token, and server metadata carry no content, so they
	// must not have produced deltas.
	if len(sink.content()) != 3 {
		t.Errorf("delta count = %d, want 3 (marker/metadata events must not deliver content)", len(sink.content()))
	}
}

func TestStreamNeverLeaksTheResumeToken(t *testing.T) {
	t.Parallel()

	transport := conversationTransport(t, loadFixture(t, "chat_stream.sse"))
	adapter := chatgptweb.New(transport)
	sink := &recordingSink{}

	if _, err := adapter.Stream(t.Context(), streamCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder}, sink); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	// The upstream resume_conversation_token carries a session token that must
	// never reach the client (evidence: "token 不应该暴露给下游用户").
	if strings.Contains(sink.joined(), fixturePlaceholder) {
		t.Fatal("resume token leaked into canonical delta content")
	}
}

func TestNonStreamingAggregatesTheSameTranscript(t *testing.T) {
	t.Parallel()

	transport := conversationTransport(t, loadFixture(t, "chat_stream.sse"))
	adapter := chatgptweb.New(transport)

	outcome, err := adapter.Run(t.Context(), chatCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Commit != domain.CommitCommitted {
		t.Fatalf("commit = %s, want committed", outcome.Commit)
	}
	if len(outcome.Completion.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(outcome.Completion.Choices))
	}
	// A non-stream response is a client aggregation over the same SSE body, so it
	// must equal the concatenated deltas (§2.1).
	if got := outcome.Completion.Choices[0].Message.Content; got != "Hello world!" {
		t.Fatalf("aggregated content = %q, want %q", got, "Hello world!")
	}
	if outcome.Completion.Choices[0].FinishClass != domain.FinishStop {
		t.Errorf("finish class = %s, want stop", outcome.Completion.Choices[0].FinishClass)
	}
}

func TestModerationBlockedMapsToContentFilter(t *testing.T) {
	t.Parallel()

	transport := conversationTransport(t, loadFixture(t, "moderation_blocked.sse"))
	adapter := chatgptweb.New(transport)
	sink := &recordingSink{}

	outcome, err := adapter.Stream(t.Context(), streamCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder}, sink)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	// The refusal text is real assistant content and is still delivered; the
	// terminal classification is what records the block (evidence "策略拒绝场景").
	if got := sink.joined(); got != "I can't assist with that request." {
		t.Errorf("content = %q, want the refusal text delivered", got)
	}
	if outcome.FinishClass != domain.FinishContentFilter {
		t.Fatalf("finish class = %s, want content_filter", outcome.FinishClass)
	}
}

func TestProtocolDriftWithNoContentIsNotCommitted(t *testing.T) {
	t.Parallel()

	transport := conversationTransport(t, loadFixture(t, "protocol_drift.sse"))
	adapter := chatgptweb.New(transport)
	sink := &recordingSink{}

	outcome, err := adapter.Stream(t.Context(), streamCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder}, sink)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if len(sink.content()) != 0 {
		t.Errorf("drift delivered %d deltas, want 0", len(sink.content()))
	}
	// Drift must be an observable classification, not an empty success: a silent
	// empty completion would hide a moved protocol (evidence §7).
	if outcome.FailureClass != domain.ErrCodeUpstreamProtocolDrift {
		t.Errorf("failure class = %s, want upstream_protocol_drift", outcome.FailureClass)
	}
	if outcome.Commit != domain.CommitNotCommitted {
		t.Errorf("commit = %s, want not_committed (nothing was delivered)", outcome.Commit)
	}
}

func TestImageOutputPointerRequiresToolRoleAndImageGenTask(t *testing.T) {
	t.Parallel()

	// The generate transcript carries a tool message with async_task_type
	// image_gen, so its pointer is a genuine output; the edit transcript ALSO
	// carries a user sediment:// input attachment that must never be mistaken
	// for output. Both translate without drift, which is the observable proof
	// that the three-part rule parsed rather than fell through.
	for _, fixture := range []string{"image_generate.sse", "image_edit.sse"} {
		t.Run(fixture, func(t *testing.T) {
			t.Parallel()
			transport := conversationTransport(t, loadFixture(t, fixture))
			adapter := chatgptweb.New(transport)
			sink := &recordingSink{}

			outcome, err := adapter.Stream(t.Context(), streamCommand("gpt-image-fixture"), &staticCredential{material: fixturePlaceholder}, sink)
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			if outcome.FailureClass == domain.ErrCodeUpstreamProtocolDrift {
				t.Fatalf("image transcript classified as protocol drift")
			}
			// An image turn emits no assistant text deltas.
			if len(sink.content()) != 0 {
				t.Errorf("image turn delivered %d text deltas, want 0", len(sink.content()))
			}
		})
	}
}

func TestChallengeIsClassifiedAndNeverSolved(t *testing.T) {
	t.Parallel()

	for _, section := range []string{"proof_of_work_required", "turnstile_required", "arkose_required"} {
		t.Run(section, func(t *testing.T) {
			t.Parallel()
			transport := newFixtureTransport().
				on(chatgptweb.PathChatRequirements, chatgptweb.Response{
					Status: http.StatusOK,
					Body:   loadFixtureSection(t, "challenge.json", section),
				}).
				on(chatgptweb.PathConversation, chatgptweb.Response{
					Status: http.StatusOK,
					Stream: newSSEStream(loadFixture(t, "chat_stream.sse")),
				})
			adapter := chatgptweb.New(transport)
			sink := &recordingSink{}

			outcome, err := adapter.Stream(t.Context(), streamCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder}, sink)
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			if outcome.FailureClass != domain.ErrCodeProviderChallenged {
				t.Fatalf("failure class = %s, want provider_challenged", outcome.FailureClass)
			}
			// The turn must stop AT the sentinel: the conversation is never opened,
			// so no challenge was solved and no generation was attempted
			// (OP-G6 refuses challenge solving; KS-5 makes new anti-bot reverse
			// engineering a kill trigger).
			if calls := transport.count(chatgptweb.PathConversation); calls != 0 {
				t.Fatalf("conversation opened %d times behind a challenge, want 0", calls)
			}
			if calls := transport.count(chatgptweb.PathChatRequirements); calls != 1 {
				t.Fatalf("sentinel called %d times, want exactly 1 (no challenge retry)", calls)
			}
		})
	}
}

func TestAuthFailureOnConversationIsClassifiedNotRetried(t *testing.T) {
	t.Parallel()

	transport := newFixtureTransport().
		on(chatgptweb.PathChatRequirements, chatgptweb.Response{
			Status: http.StatusOK,
			Body:   loadFixtureSection(t, "challenge.json", "no_challenge"),
		}).
		on(chatgptweb.PathConversation, chatgptweb.Response{Status: http.StatusUnauthorized})
	adapter := chatgptweb.New(transport)

	outcome, err := adapter.Run(t.Context(), chatCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.FailureClass != domain.ErrCodeProviderAuthExpired {
		t.Errorf("failure class = %s, want provider_auth_expired", outcome.FailureClass)
	}
	// Full-operation retry belongs to the spine, which is the only layer that can
	// honor the authoritative-no-commit rule. The Adapter attempts each path once.
	if calls := transport.count(chatgptweb.PathConversation); calls != 1 {
		t.Fatalf("conversation attempted %d times, want exactly 1 (retry is not the Adapter's)", calls)
	}
}

func TestCanceledContextYieldsUnknownCommitAndNoStopClaim(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	transport := conversationTransport(t, loadFixture(t, "chat_stream.sse"))
	adapter := chatgptweb.New(transport)
	sink := &recordingSink{}

	outcome, err := adapter.Stream(ctx, streamCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder}, sink)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	// Web Access has no documented cooperative cancel (§2.1 marks cancel/abort
	// `unverified`), so the upstream may still be generating: commit certainty is
	// UNKNOWN and no fallback re-attempt is authorized.
	if outcome.Commit != domain.CommitUnknown {
		t.Errorf("commit = %s, want unknown after cancel", outcome.Commit)
	}
	// §6.2 rules 3-4 forbid claiming an abort or a stop without proof. Closing a
	// local stream is neither.
	if outcome.UpstreamAbortAttempted {
		t.Error("claimed an upstream abort this surface cannot perform")
	}
	if outcome.UpstreamStopConfirmed {
		t.Error("claimed a confirmed upstream stop without proof")
	}
}

func TestNilTransportFailsClosed(t *testing.T) {
	t.Parallel()

	adapter := chatgptweb.New(nil)

	// Registering the Adapter is not the same as giving it egress: without a
	// transport every surface must fail closed rather than invent a result.
	if _, err := adapter.Probe(t.Context(), ports.ProbeCommand{AuthMode: domain.AuthModeChatGPTWebAccess}); !errors.Is(err, ports.ErrDependencyUnavailable) {
		t.Errorf("Probe error = %v, want dependency unavailable", err)
	}
	if _, err := adapter.Observe(t.Context(), ports.CapabilityObservationCommand{AuthMode: domain.AuthModeChatGPTWebAccess}); !errors.Is(err, ports.ErrDependencyUnavailable) {
		t.Errorf("Observe error = %v, want dependency unavailable", err)
	}
	outcome, err := adapter.Run(t.Context(), chatCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Commit == domain.CommitCommitted {
		t.Error("Run committed without a transport")
	}
}

func TestForeignAuthModeIsRefused(t *testing.T) {
	t.Parallel()

	adapter := chatgptweb.New(conversationTransport(t, loadFixture(t, "chat_stream.sse")))

	// A registry that routed another Auth Mode here would otherwise apply ChatGPT
	// Web framing to a different credential class.
	command := chatCommand("gpt-fixture-1")
	command.AuthMode = domain.AuthModeChatGPTCodexOAuth
	if _, err := adapter.Run(t.Context(), command, &staticCredential{material: fixturePlaceholder}); !errors.Is(err, ports.ErrChatAdapterUnavailable) {
		t.Errorf("Run error = %v, want chat adapter unavailable", err)
	}
}

func TestCredentialIsUsedOnlyInsideTheCallback(t *testing.T) {
	t.Parallel()

	transport := conversationTransport(t, loadFixture(t, "chat_stream.sse"))
	adapter := chatgptweb.New(transport)
	credential := &staticCredential{material: fixturePlaceholder}

	if _, err := adapter.Run(t.Context(), chatCommand("gpt-fixture-1"), credential); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// Exactly one callback-scoped use: the Adapter never resolves the secret more
	// than once per attempt and never caches it across attempts.
	if uses := credential.uses.Load(); uses != 1 {
		t.Fatalf("credential used %d times, want exactly 1", uses)
	}
}

func TestFixturesCarryNoRealSecrets(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no fixtures found")
	}

	// Shapes that would indicate a real credential was pasted into testdata.
	// OP-G3 forbids credential material anywhere it can be read back out.
	forbidden := []string{
		"sk-",
		"eyJhbGciOi", // a real JWT header
		"__Secure-1PSID",
		"accessToken",
		"Bearer ey",
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		body := loadFixture(t, entry.Name())
		for _, needle := range forbidden {
			if strings.Contains(body, needle) {
				t.Errorf("fixture %s contains %q, which looks like real credential material", entry.Name(), needle)
			}
		}
		// Every credential-shaped fixture value must be the single placeholder.
		if strings.Contains(body, "token") && !strings.Contains(body, fixturePlaceholder) {
			t.Errorf("fixture %s mentions a token but not the %q placeholder", entry.Name(), fixturePlaceholder)
		}
	}

	// Guard the guard: the placeholder itself must exist in at least one fixture,
	// otherwise the check above passes vacuously.
	found := false
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.Contains(loadFixture(t, entry.Name()), fixturePlaceholder) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no fixture uses the %q placeholder; the secret guard would pass vacuously", fixturePlaceholder)
	}
	_ = filepath.Separator
}

func TestUnknownTypedEventIsDriftEvenWhenItCarriesAConversationID(t *testing.T) {
	t.Parallel()

	// Regression: an unknown `type` that happened to carry a conversation_id used
	// to fall through to "ignored", so a moved protocol produced an empty
	// completion that still classified as COMMITTED — a silent success hiding
	// drift. A self-describing event this Adapter cannot interpret is drift
	// regardless of which incidental fields it carries (evidence §7, KS-5).
	transcript := `"v1"` + "\n" +
		`{"type":"an_event_type_never_seen","conversation_id":"conv-fixture-0005","payload":{"nested":true}}` + "\n" +
		"[DONE]\n"

	transport := conversationTransport(t, transcript)
	adapter := chatgptweb.New(transport)
	sink := &recordingSink{}

	outcome, err := adapter.Stream(t.Context(), streamCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder}, sink)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if outcome.FailureClass != domain.ErrCodeUpstreamProtocolDrift {
		t.Fatalf("failure class = %s, want upstream_protocol_drift", outcome.FailureClass)
	}
	if outcome.Commit == domain.CommitCommitted {
		t.Error("an undecodable transcript committed; nothing was delivered")
	}
	if len(sink.content()) != 0 {
		t.Errorf("drift delivered %d deltas, want 0", len(sink.content()))
	}
}

func TestTranscriptThatProducesNothingIsNotCommitted(t *testing.T) {
	t.Parallel()

	// Every payload here is one this Adapter recognizes and correctly treats as
	// non-content, so there is no drift to detect — yet the turn carries no
	// content, no image, no moderation block, and no terminal marker.
	//
	// Regression: this used to return COMMITTED with an empty assistant message
	// and a fabricated `stop` finish class the Provider never sent. A caller would
	// have been billed for, and shown, an answer that did not exist. Nothing
	// proved a generation happened, so the only honest answer is not-committed —
	// which also lets the spine re-attempt on another account.
	transcript := `"v1"` + "\n" +
		`{"type":"message_marker","marker":"user_visible_token","event":"first"}` + "\n" +
		`{"type":"server_ste_metadata","metadata":{"tool_invoked":false}}` + "\n" +
		"[DONE]\n"

	t.Run("streaming", func(t *testing.T) {
		t.Parallel()
		adapter := chatgptweb.New(conversationTransport(t, transcript))
		sink := &recordingSink{}
		outcome, err := adapter.Stream(t.Context(), streamCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder}, sink)
		if err != nil {
			t.Fatalf("Stream() error = %v", err)
		}
		if outcome.Commit == domain.CommitCommitted {
			t.Error("committed a turn that produced nothing")
		}
		if outcome.FailureClass != domain.ErrCodeUpstreamProtocolDrift {
			t.Errorf("failure class = %s, want upstream_protocol_drift", outcome.FailureClass)
		}
		if len(sink.content()) != 0 {
			t.Errorf("delivered %d deltas, want 0", len(sink.content()))
		}
	})

	t.Run("non-streaming", func(t *testing.T) {
		t.Parallel()
		adapter := chatgptweb.New(conversationTransport(t, transcript))
		outcome, err := adapter.Run(t.Context(), chatCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if outcome.Commit == domain.CommitCommitted {
			t.Error("committed a turn that produced nothing")
		}
		// No fabricated choice: an empty Choices slice cannot be mistaken for an
		// assistant message that said nothing.
		if len(outcome.Completion.Choices) != 0 {
			t.Errorf("minted %d choices for a turn that produced nothing, want 0", len(outcome.Completion.Choices))
		}
	})
}

func TestNonStreamMidTurnBreakAfterContentForfeitsCommitCertainty(t *testing.T) {
	t.Parallel()

	// The upstream produced content, then the connection broke before the turn
	// finished. `Run` buffers, so the CALLER saw nothing — but the upstream
	// demonstrably generated and may have committed and billed it.
	//
	// Regression: this used to report authoritative `not_committed`, which
	// authorizes the spine's fallback re-attempt and would generate a second time
	// on the Provider for one client request. Client exposure and upstream commit
	// are different questions; only the second one governs re-attempt authority
	// (§6.2 authoritative-no-commit rule).
	stream := newSSEStream(`"v1"` + "\n" +
		`{"p":"/message/content/parts/0","o":"append","v":"Partial"}` + "\n" +
		"[DONE]\n")
	stream.failAt = 2
	stream.err = errors.New("connection reset mid-stream")

	transport := newFixtureTransport().
		on(chatgptweb.PathChatRequirements, chatgptweb.Response{
			Status: http.StatusOK,
			Body:   loadFixtureSection(t, "challenge.json", "no_challenge"),
		}).
		on(chatgptweb.PathConversation, chatgptweb.Response{Status: http.StatusOK, Stream: stream})

	adapter := chatgptweb.New(transport)
	outcome, err := adapter.Run(t.Context(), chatCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Commit != domain.CommitUnknown {
		t.Fatalf("commit = %s, want unknown (the upstream may hold a committed generation)", outcome.Commit)
	}
	if outcome.FailureClass != domain.ErrCodeExecutionPossiblyCommitted {
		t.Errorf("failure class = %s, want execution_possibly_committed", outcome.FailureClass)
	}
	// A partial aggregation must never be returned as if it were the answer.
	if len(outcome.Completion.Choices) != 0 {
		t.Errorf("returned %d choices from a broken turn, want 0", len(outcome.Completion.Choices))
	}
}

func TestNonStreamBreakBeforeAnyContentStaysAuthoritativeNoCommit(t *testing.T) {
	t.Parallel()

	// The control for the test above: breaking BEFORE any content means the
	// upstream never demonstrated a generation, so authoritative not-committed is
	// correct and the fallback re-attempt stays authorized. Without this pair, the
	// fix could have over-corrected every failure into `unknown` and silently
	// disabled fallback for the whole surface.
	stream := newSSEStream(`"v1"` + "\n" + `{"type":"message_marker","marker":"x","event":"first"}` + "\n")
	stream.failAt = 1
	stream.err = errors.New("connection reset before content")

	transport := newFixtureTransport().
		on(chatgptweb.PathChatRequirements, chatgptweb.Response{
			Status: http.StatusOK,
			Body:   loadFixtureSection(t, "challenge.json", "no_challenge"),
		}).
		on(chatgptweb.PathConversation, chatgptweb.Response{Status: http.StatusOK, Stream: stream})

	adapter := chatgptweb.New(transport)
	outcome, err := adapter.Run(t.Context(), chatCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Commit != domain.CommitNotCommitted {
		t.Fatalf("commit = %s, want not_committed so fallback stays authorized", outcome.Commit)
	}
}

func TestMetadataPatchesAreNonContentNotDrift(t *testing.T) {
	t.Parallel()

	// Real transcripts carry metadata replaces alongside content. These must stay
	// non-content rather than drift: classifying them as drift would make every
	// ordinary turn look like a moved protocol, and the FG-5/KS-2 drift counters
	// would be useless. This pins the boundary opposite
	// TestUnknownTypedEventIsDriftEvenWhenItCarriesAConversationID — an unknown
	// `type` is drift, but a known-shaped patch on an uninteresting path is not.
	transcript := `"v1"` + "\n" +
		`{"p":"/message/metadata/model_slug","o":"replace","v":"gpt-fixture-1","conversation_id":"conv-fixture-0006"}` + "\n" +
		`{"p":"/message/content/parts/0","o":"append","v":"Hi"}` + "\n" +
		`{"p":"/message/metadata/finish_details","o":"replace","v":{"type":"stop"},"conversation_id":"conv-fixture-0006"}` + "\n" +
		`{"p":"/message/status","o":"replace","v":"finished_successfully"}` + "\n" +
		"[DONE]\n"

	adapter := chatgptweb.New(conversationTransport(t, transcript))
	sink := &recordingSink{}

	outcome, err := adapter.Stream(t.Context(), streamCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder}, sink)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if outcome.FailureClass == domain.ErrCodeUpstreamProtocolDrift {
		t.Fatal("metadata patches were classified as protocol drift")
	}
	if outcome.Commit != domain.CommitCommitted {
		t.Errorf("commit = %s, want committed", outcome.Commit)
	}
	if got := sink.joined(); got != "Hi" {
		t.Errorf("content = %q, want %q", got, "Hi")
	}
	// The metadata patches must not have produced deltas of their own.
	if len(sink.content()) != 1 {
		t.Errorf("delta count = %d, want 1", len(sink.content()))
	}
}

// truncatedStream ends without [DONE] and without any finish marker, which is
// what a connection drop mid-generation looks like on the wire.
type truncatedStream struct {
	payloads []string
	cursor   int
}

func (stream *truncatedStream) Next() (string, bool, error) {
	if stream.cursor >= len(stream.payloads) {
		return "", false, nil
	}
	payload := stream.payloads[stream.cursor]
	stream.cursor++
	return payload, true, nil
}

func (stream *truncatedStream) Close() error { return nil }

func TestTruncatedStreamIsNotReportedAsACleanStop(t *testing.T) {
	t.Parallel()

	// Content arrived, then the body ended with no finish marker and no [DONE].
	//
	// Regression: this returned committed with FinishClass `stop`, telling the
	// caller the model chose to end there. It did not — the answer is cut off, and
	// the upstream may have kept generating and billed the rest. A completed
	// ChatGPT Web body always ends with [DONE], so its absence is the signal.
	transcript := []string{
		`"v1"`,
		`{"p":"/message/content/parts/0","o":"append","v":"partial answer"}`,
	}

	build := func() *fixtureTransport {
		return newFixtureTransport().
			on(chatgptweb.PathChatRequirements, chatgptweb.Response{
				Status: http.StatusOK,
				Body:   loadFixtureSection(t, "challenge.json", "no_challenge"),
			}).
			on(chatgptweb.PathConversation, chatgptweb.Response{
				Status: http.StatusOK,
				Stream: &truncatedStream{payloads: transcript},
			})
	}

	outcome, err := chatgptweb.New(build()).
		Run(t.Context(), chatCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Commit != domain.CommitUnknown {
		t.Fatalf("commit = %s, want unknown for a truncated body", outcome.Commit)
	}
	// No partial answer may be presented as the completion.
	if len(outcome.Completion.Choices) != 0 {
		t.Errorf("returned %d choices from a truncated turn, want 0", len(outcome.Completion.Choices))
	}

	streamOutcome, err := chatgptweb.New(build()).
		Stream(t.Context(), streamCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder}, &recordingSink{})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if streamOutcome.Commit != domain.CommitUnknown {
		t.Errorf("stream commit = %s, want unknown", streamOutcome.Commit)
	}
	if streamOutcome.FinishClass == domain.FinishStop {
		t.Error("truncated stream claimed a clean stop")
	}
}

func TestDoneWithoutAFinishMarkerStillCompletes(t *testing.T) {
	t.Parallel()

	// The control for the test above: `[DONE]` alone must be sufficient evidence
	// that a body ended normally, so a turn carrying content and no message-status
	// marker still commits rather than classifying as truncated.
	//
	// This uses a text transcript rather than image_generate.sse, because an image
	// turn is UNKNOWN for an unrelated reason (this chat surface cannot carry an
	// asset — see TestImageOnlyTurnIsNotReportedAsAnEmptySuccess), which would
	// make the control pass or fail for the wrong reason.
	transcript := "\"v1\"\n" +
		`{"p":"/message/content/parts/0","o":"append","v":"a complete answer"}` + "\n" +
		"[DONE]\n"
	adapter := chatgptweb.New(conversationTransport(t, transcript))
	sink := &recordingSink{}

	outcome, err := adapter.Stream(t.Context(), streamCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder}, sink)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if outcome.Commit != domain.CommitCommitted {
		t.Fatalf("commit = %s, want committed ([DONE] alone ends a turn normally)", outcome.Commit)
	}
	if outcome.FinishClass != domain.FinishStop {
		t.Errorf("finish class = %s, want stop", outcome.FinishClass)
	}
}

func TestImageOnlyTurnIsNotReportedAsAnEmptySuccess(t *testing.T) {
	t.Parallel()

	// An image turn decodes an asset pointer and no assistant text. The canonical
	// chat vocabulary has no carrier for an asset (ChatChoice.Message and
	// ChatDelta hold text only), so the pointer cannot be delivered.
	//
	// Committing would hand the caller a successful, EMPTY answer that is
	// observably indistinguishable from "the model said nothing", while discarding
	// the one piece of evidence that a generation happened. An authoritative
	// not-committed is equally wrong: it would authorize the spine's fallback to
	// re-attempt, paying for a second image the upstream already produced.
	for _, fixture := range []string{"image_generate.sse", "image_edit.sse"} {
		t.Run(fixture, func(t *testing.T) {
			t.Parallel()

			transcript := loadFixture(t, fixture)

			outcome, err := chatgptweb.New(conversationTransport(t, transcript)).
				Run(t.Context(), chatCommand("gpt-image-fixture"), &staticCredential{material: fixturePlaceholder})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if outcome.Commit != domain.CommitUnknown {
				t.Errorf("commit = %s, want unknown (the asset cannot be delivered on the chat surface)", outcome.Commit)
			}
			if len(outcome.Completion.Choices) != 0 {
				t.Errorf("returned %d choices for an image-only turn, want 0", len(outcome.Completion.Choices))
			}

			sink := &recordingSink{}
			streamOutcome, err := chatgptweb.New(conversationTransport(t, transcript)).
				Stream(t.Context(), streamCommand("gpt-image-fixture"), &staticCredential{material: fixturePlaceholder}, sink)
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			if streamOutcome.Commit != domain.CommitUnknown {
				t.Errorf("stream commit = %s, want unknown", streamOutcome.Commit)
			}
			if streamOutcome.FinishClass == domain.FinishStop {
				t.Error("image-only stream claimed a clean stop with zero deltas")
			}
			// The Provider-specific pointer must never surface downstream.
			if joined := sink.joined(); strings.Contains(joined, "file-service://") || strings.Contains(joined, "sediment://") {
				t.Errorf("asset pointer leaked into canonical deltas: %q", joined)
			}
		})
	}
}

func TestAnEchoedUserMessageDoesNotFinishTheAssistantTurn(t *testing.T) {
	t.Parallel()

	// The upstream echoes the caller's own input message back into the stream
	// carrying "status":"finished_successfully" (see image_edit.sse line 2). That
	// status describes the echoed INPUT being complete and says nothing about the
	// assistant's generation.
	//
	// Treating it as a finish marker silently disables the truncation check: the
	// turn looks finished from its first event, so a body that drops mid-answer
	// reports committed/stop and presents a cut-off answer as the model's chosen
	// ending. This transcript is exactly that shape — user echo, one content
	// delta, then the body ends with no [DONE].
	transcript := "\"v1\"\n" +
		`{"p":"","o":"add","v":{"message":{"author":{"role":"user"},"content":{"content_type":"text","parts":["hi"]},"status":"finished_successfully"},"conversation_id":"conv-fixture-0009"}}` + "\n" +
		`{"p":"/message/content/parts/0","o":"append","v":"partial answer"}` + "\n"

	outcome, err := chatgptweb.New(conversationTransport(t, transcript)).
		Run(t.Context(), chatCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Commit != domain.CommitUnknown {
		t.Errorf("commit = %s, want unknown (a user echo must not end the assistant turn)", outcome.Commit)
	}

	sink := &recordingSink{}
	streamOutcome, err := chatgptweb.New(conversationTransport(t, transcript)).
		Stream(t.Context(), streamCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder}, sink)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if streamOutcome.Commit != domain.CommitUnknown {
		t.Errorf("stream commit = %s, want unknown", streamOutcome.Commit)
	}
	if streamOutcome.FinishClass == domain.FinishStop {
		t.Error("truncated stream behind a user echo claimed a clean stop")
	}
}

func TestAnAssistantFinishMarkerStillEndsTheTurn(t *testing.T) {
	t.Parallel()

	// The control for the test above: the role check must not reject the real
	// marker. An assistant message reporting finished_successfully ends the turn
	// even though the body carries no [DONE], which is what keeps the fix from
	// over-correcting every marker-terminated turn into unknown.
	transcript := "\"v1\"\n" +
		`{"p":"/message/content/parts/0","o":"append","v":"a complete answer"}` + "\n" +
		`{"v":{"message":{"author":{"role":"assistant"},"status":"finished_successfully"},"conversation_id":"conv-fixture-0009"}}` + "\n"

	outcome, err := chatgptweb.New(conversationTransport(t, transcript)).
		Run(t.Context(), chatCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Commit != domain.CommitCommitted {
		t.Fatalf("commit = %s, want committed", outcome.Commit)
	}
	if got := outcome.Completion.Choices[0].Message.Content; got != "a complete answer" {
		t.Errorf("content = %q, want the full answer", got)
	}
}

func TestProtocolDriftAfterContentIsUnknownRatherThanASilentCompletion(t *testing.T) {
	t.Parallel()

	// The regression this guards: the ladder used to test `drifted && !sawContent`,
	// so a single decodable delta arriving BEFORE an undecodable payload made the
	// drift flag irrelevant and the turn fell through to committed/stop.
	//
	// This transcript is exactly that shape. One content delta lands, then the
	// Provider emits a self-describing event type this Adapter has never seen
	// (eventDrift), and only then does the body close normally with a finish
	// marker and [DONE]. Because the terminator is clean, `truncated()` is false
	// and NOTHING else in the ladder would catch the turn: without the fix the
	// caller receives "the first half" as a complete, deliberately-ended answer
	// and the protocol movement is invisible (evidence §7).
	//
	// The upstream demonstrably generated, so an authoritative no-commit would
	// wrongly authorize a second billed generation; the correct answer is UNKNOWN.
	transcript := "\"v1\"\n" +
		`{"p":"/message/content/parts/0","o":"append","v":"the first half"}` + "\n" +
		`{"type":"an_event_type_this_adapter_has_never_seen","payload":{"nested":true}}` + "\n" +
		`{"p":"/message/status","o":"replace","v":"finished_successfully"}` + "\n" +
		"[DONE]\n"

	outcome, err := chatgptweb.New(conversationTransport(t, transcript)).
		Run(t.Context(), chatCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Commit != domain.CommitUnknown {
		t.Errorf("commit = %s, want unknown (drift after content loses certainty)", outcome.Commit)
	}
	if outcome.FailureClass != domain.ErrCodeExecutionPossiblyCommitted {
		t.Errorf("failure class = %s, want execution_possibly_committed", outcome.FailureClass)
	}
	// A partial answer must not be presented as the completion.
	if len(outcome.Completion.Choices) != 0 {
		t.Errorf("returned %d choices from a drifted turn, want 0", len(outcome.Completion.Choices))
	}

	// Both surfaces must agree on the commit question even though the streaming
	// client already consumed the partial text.
	sink := &recordingSink{}
	streamOutcome, err := chatgptweb.New(conversationTransport(t, transcript)).
		Stream(t.Context(), streamCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder}, sink)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if streamOutcome.Commit != domain.CommitUnknown {
		t.Errorf("stream commit = %s, want unknown", streamOutcome.Commit)
	}
	if streamOutcome.FailureClass != domain.ErrCodeExecutionPossiblyCommitted {
		t.Errorf("stream failure class = %s, want execution_possibly_committed", streamOutcome.FailureClass)
	}
	if streamOutcome.FinishClass == domain.FinishStop {
		t.Error("drifted stream claimed a clean stop")
	}
	// The delta itself is still delivered: it was valid content, and withholding
	// it would not make the turn any more certain.
	if got := sink.joined(); got != "the first half" {
		t.Errorf("delta content = %q, want the decoded prefix still delivered", got)
	}
}

func TestProtocolDriftWithNoContentStaysAuthoritativelyNotCommittedOnBothSurfaces(t *testing.T) {
	t.Parallel()

	// The control for the test above, and the reason the drift fix is scoped to
	// "after evidence" rather than applied to every drift.
	//
	// An authoritative not-committed is what AUTHORIZES the spine's fallback walk
	// to re-attempt on another account. If every unparseable payload became
	// UNKNOWN, fallback would be disabled for this entire surface: one Provider
	// protocol change would strand every turn instead of failing over. The
	// ordinary "we could not parse anything" case must therefore keep its
	// authoritative answer, and nothing here proves a generation happened, so
	// re-attempting cannot double-bill.
	transcript := loadFixture(t, "protocol_drift.sse")

	outcome, err := chatgptweb.New(conversationTransport(t, transcript)).
		Run(t.Context(), chatCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Commit != domain.CommitNotCommitted {
		t.Errorf("commit = %s, want not_committed (fallback must stay authorized)", outcome.Commit)
	}
	if outcome.FailureClass != domain.ErrCodeUpstreamProtocolDrift {
		t.Errorf("failure class = %s, want upstream_protocol_drift", outcome.FailureClass)
	}

	sink := &recordingSink{}
	streamOutcome, err := chatgptweb.New(conversationTransport(t, transcript)).
		Stream(t.Context(), streamCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder}, sink)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if streamOutcome.Commit != domain.CommitNotCommitted {
		t.Errorf("stream commit = %s, want not_committed", streamOutcome.Commit)
	}
	if streamOutcome.FailureClass != domain.ErrCodeUpstreamProtocolDrift {
		t.Errorf("stream failure class = %s, want upstream_protocol_drift", streamOutcome.FailureClass)
	}
	if len(sink.content()) != 0 {
		t.Errorf("drift delivered %d deltas, want 0", len(sink.content()))
	}
}

func TestDriftBesideAFinishMarkerButNoContentIsStillAuthoritativelyNotCommitted(t *testing.T) {
	t.Parallel()

	// The second half of the fallback control, and the case that actually pins
	// driftedWithoutEvidence() rather than producedNothing().
	//
	// protocol_drift.sse carries no content, no finish marker, no block and no
	// image, so producedNothing() is true and would return the authoritative
	// not-committed on its own — which means that fixture cannot prove the drift
	// predicate is doing anything. This transcript closes that hole: every content
	// payload is undecodable, but a REAL assistant finish marker arrives, so
	// producedNothing() is false and driftedWithoutEvidence() is the only branch
	// left that can answer authoritatively.
	//
	// The classification must still be not-committed. A finish marker says the
	// upstream turn ended; it does not say a generation was delivered, and nothing
	// decodable reached this Adapter, so there is no evidence to be uncertain
	// about and no risk of double-billing a re-attempt. If this turned UNKNOWN the
	// spine's fallback walk would be disabled for a whole class of drifted turns;
	// if it fell through to the bottom of the ladder it would commit an EMPTY
	// answer with a `stop` finish class.
	transcript := "\"v1\"\n" +
		`{"type":"an_event_type_this_adapter_has_never_seen","payload":{"nested":true}}` + "\n" +
		`{"p":"/message/content/parts/0","o":"append","v":12345}` + "\n" +
		`{"p":"/message/status","o":"replace","v":"finished_successfully"}` + "\n" +
		"[DONE]\n"

	outcome, err := chatgptweb.New(conversationTransport(t, transcript)).
		Run(t.Context(), chatCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Commit != domain.CommitNotCommitted {
		t.Errorf("commit = %s, want not_committed (a finish marker is not a delivered generation)", outcome.Commit)
	}
	if outcome.FailureClass != domain.ErrCodeUpstreamProtocolDrift {
		t.Errorf("failure class = %s, want upstream_protocol_drift", outcome.FailureClass)
	}
	// An empty committed answer would be the fall-through bug.
	if len(outcome.Completion.Choices) != 0 {
		t.Errorf("returned %d choices for a fully drifted turn, want 0", len(outcome.Completion.Choices))
	}

	sink := &recordingSink{}
	streamOutcome, err := chatgptweb.New(conversationTransport(t, transcript)).
		Stream(t.Context(), streamCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder}, sink)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if streamOutcome.Commit != domain.CommitNotCommitted {
		t.Errorf("stream commit = %s, want not_committed", streamOutcome.Commit)
	}
	if streamOutcome.FailureClass != domain.ErrCodeUpstreamProtocolDrift {
		t.Errorf("stream failure class = %s, want upstream_protocol_drift", streamOutcome.FailureClass)
	}
	if len(sink.content()) != 0 {
		t.Errorf("fully drifted turn delivered %d deltas, want 0", len(sink.content()))
	}
}

func TestATurnCarryingBothTextAndAnImageDoesNotCommitAndDiscardTheImage(t *testing.T) {
	t.Parallel()

	// The regression this guards: undeliverableImage() used to require
	// `!sawContent`, so a turn that produced text AND a confirmed image asset
	// committed on the strength of the text alone and dropped the asset silently.
	//
	// That is worse than the image-only case, not better. In the image-only case
	// the caller at least receives an observably empty answer; here the plausible
	// text makes the loss invisible — the caller cannot tell an image was
	// generated (and, on a metered Provider, billed), because the canonical chat
	// vocabulary has no carrier for an asset (ChatChoice.Message and ChatDelta
	// hold text only).
	//
	// The transcript is a normal text turn with a confirmed image-tool output
	// spliced in: role `tool`, async_task_type `image_gen`, resolvable pointer —
	// all three conditions the output rule requires. It terminates cleanly with a
	// finish marker and [DONE], so neither truncated() nor any drift branch can
	// catch it: undeliverableImage() is the only thing standing between this turn
	// and a committed answer with a missing image.
	transcript := "\"v1\"\n" +
		`{"p":"/message/content/parts/0","o":"append","v":"here is your image"}` + "\n" +
		`{"v":{"message":{"author":{"role":"tool"},"content":{"content_type":"multimodal_text","parts":[{"asset_pointer":"file-service://file_fixture_mixed_result"}]},"metadata":{"async_task_type":"image_gen"}},"conversation_id":"conv-fixture-0011"}}` + "\n" +
		`{"p":"/message/status","o":"replace","v":"finished_successfully"}` + "\n" +
		"[DONE]\n"

	outcome, err := chatgptweb.New(conversationTransport(t, transcript)).
		Run(t.Context(), chatCommand("gpt-image-fixture"), &staticCredential{material: fixturePlaceholder})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Commit != domain.CommitUnknown {
		t.Errorf("commit = %s, want unknown (the asset cannot be delivered alongside the text)", outcome.Commit)
	}
	if outcome.FailureClass != domain.ErrCodeExecutionPossiblyCommitted {
		t.Errorf("failure class = %s, want execution_possibly_committed", outcome.FailureClass)
	}
	// Returning the text alone would be the exact silent-discard bug.
	if len(outcome.Completion.Choices) != 0 {
		t.Errorf("returned %d choices for a text+image turn, want 0", len(outcome.Completion.Choices))
	}

	sink := &recordingSink{}
	streamOutcome, err := chatgptweb.New(conversationTransport(t, transcript)).
		Stream(t.Context(), streamCommand("gpt-image-fixture"), &staticCredential{material: fixturePlaceholder}, sink)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if streamOutcome.Commit != domain.CommitUnknown {
		t.Errorf("stream commit = %s, want unknown", streamOutcome.Commit)
	}
	if streamOutcome.FailureClass != domain.ErrCodeExecutionPossiblyCommitted {
		t.Errorf("stream failure class = %s, want execution_possibly_committed", streamOutcome.FailureClass)
	}
	if streamOutcome.FinishClass == domain.FinishStop {
		t.Error("text+image stream claimed a clean stop while dropping the asset")
	}
	// The Provider-specific pointer must never surface downstream, on any path.
	if joined := sink.joined(); strings.Contains(joined, "file-service://") || strings.Contains(joined, "sediment://") {
		t.Errorf("asset pointer leaked into canonical deltas: %q", joined)
	}
}

func TestAnOrdinaryTextTurnStillCommitsOnBothSurfaces(t *testing.T) {
	t.Parallel()

	// The control for both fixes above. Widening drift and image handling to
	// UNKNOWN is only safe if the happy path is untouched: a turn with plain text
	// and a clean [DONE] carries no drift flag and no asset pointer, so it must
	// still return an authoritative committed/stop with the aggregated answer.
	//
	// Without this guard, an over-broad predicate (for example dropping the
	// drift/image conditions entirely and returning UNKNOWN whenever content
	// exists) would pass every other test in this file while making the Adapter
	// incapable of ever reporting success.
	transcript := loadFixture(t, "chat_stream.sse")

	outcome, err := chatgptweb.New(conversationTransport(t, transcript)).
		Run(t.Context(), chatCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Commit != domain.CommitCommitted {
		t.Fatalf("commit = %s, want committed (the happy path must survive both fixes)", outcome.Commit)
	}
	if outcome.Class != domain.ChatOutcomeCommitted {
		t.Errorf("class = %s, want committed", outcome.Class)
	}
	if len(outcome.Completion.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(outcome.Completion.Choices))
	}
	if got := outcome.Completion.Choices[0].Message.Content; got != "Hello world!" {
		t.Errorf("aggregated content = %q, want %q", got, "Hello world!")
	}
	if outcome.Completion.Choices[0].FinishClass != domain.FinishStop {
		t.Errorf("finish class = %s, want stop", outcome.Completion.Choices[0].FinishClass)
	}

	sink := &recordingSink{}
	streamOutcome, err := chatgptweb.New(conversationTransport(t, transcript)).
		Stream(t.Context(), streamCommand("gpt-fixture-1"), &staticCredential{material: fixturePlaceholder}, sink)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if streamOutcome.Commit != domain.CommitCommitted {
		t.Fatalf("stream commit = %s, want committed", streamOutcome.Commit)
	}
	if streamOutcome.FinishClass != domain.FinishStop {
		t.Errorf("stream finish class = %s, want stop", streamOutcome.FinishClass)
	}
	if got := sink.joined(); got != "Hello world!" {
		t.Errorf("delta content = %q, want %q", got, "Hello world!")
	}
}
