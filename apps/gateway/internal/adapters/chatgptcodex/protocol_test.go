package chatgptcodex_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/adapters/chatgptcodex"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// codexBundleWithoutRefresh builds an OAuth bundle with no refresh_token so a
// test can prove a 401 is not followed by a refresh attempt.
func codexBundleWithoutRefresh() string {
	return `{"access_token":"fixture-access-token","account_id":"fixture-account-id"}`
}

// TestRunCommitsACleanTurn asserts a healthy Responses stream aggregates into a
// committed completion with the decoded text and a stop finish class.
func TestRunCommitsACleanTurn(t *testing.T) {
	t.Parallel()

	transport := responsesTransport(t, loadFixture(t, "chat_stream.sse"))

	outcome, err := chatgptcodex.New(transport).
		Run(t.Context(), chatCommand("gpt-fixture-codex-1"), &staticCredential{material: codexBundleMaterial()})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Commit != domain.CommitCommitted {
		t.Fatalf("commit = %s, want committed", outcome.Commit)
	}
	if got := outcome.Completion.Choices[0].Message.Content; got != "Hello world" {
		t.Errorf("content = %q, want %q", got, "Hello world")
	}
	if outcome.Completion.Model != "gpt-fixture-codex-1" {
		t.Errorf("model = %q, want gpt-fixture-codex-1", outcome.Completion.Model)
	}
	if got := transport.count(chatgptcodex.PathCodexResponses); got != 1 {
		t.Errorf("Responses exchanged %d times, want 1 (no full-operation retry)", got)
	}
}

// TestRunNothingProducedIsNotCommitted asserts a transcript of nothing the
// Adapter recognizes is authoritatively not-committed: nothing left the Gateway,
// so the spine may re-attempt on another account.
func TestRunNothingProducedIsNotCommitted(t *testing.T) {
	t.Parallel()

	transport := responsesTransport(t, loadFixture(t, "protocol_drift.sse"))

	outcome, _ := chatgptcodex.New(transport).
		Run(t.Context(), chatCommand("gpt-fixture-codex-1"), &staticCredential{material: codexBundleMaterial()})
	if outcome.Commit != domain.CommitNotCommitted {
		t.Fatalf("commit = %s, want not_committed (nothing was produced)", outcome.Commit)
	}
	if outcome.FailureClass != domain.ErrCodeUpstreamProtocolDrift {
		t.Errorf("failure class = %q, want upstream_protocol_drift", outcome.FailureClass)
	}
}

// TestRunDriftAfterEvidenceIsUnknown asserts a turn that produced content and
// then drifted is UNKNOWN: the upstream demonstrably generated, so an
// authoritative no-commit would authorize paying for a second generation.
func TestRunDriftAfterEvidenceIsUnknown(t *testing.T) {
	t.Parallel()

	transcript := strings.Join([]string{
		`{"type":"response.output_text.delta","delta":"partial answer"}`,
		`{"type":"an_event_type_this_adapter_has_never_seen","payload":true}`,
	}, "\n")
	transport := responsesTransport(t, transcript)

	outcome, _ := chatgptcodex.New(transport).
		Run(t.Context(), chatCommand("gpt-fixture-codex-1"), &staticCredential{material: codexBundleMaterial()})
	if outcome.Commit != domain.CommitUnknown {
		t.Fatalf("commit = %s, want unknown (content arrived before the drift)", outcome.Commit)
	}
}

// TestRunTruncatedIsUnknown asserts a stream that ended without [DONE] or a
// finish marker is UNKNOWN: the upstream may have kept generating and billed
// the rest.
func TestRunTruncatedIsUnknown(t *testing.T) {
	t.Parallel()

	transport := responsesTransport(t, `{"type":"response.output_text.delta","delta":"hello"}`)

	outcome, _ := chatgptcodex.New(transport).
		Run(t.Context(), chatCommand("gpt-fixture-codex-1"), &staticCredential{material: codexBundleMaterial()})
	if outcome.Commit != domain.CommitUnknown {
		t.Fatalf("commit = %s, want unknown (truncated mid-generation)", outcome.Commit)
	}
}

// TestRunImageOnlyTurnIsUnknown asserts an image_generation tool output the
// chat surface cannot carry is UNKNOWN: the upstream produced an asset, this
// surface cannot deliver it, and no replacement attempt is authorized.
func TestRunImageOnlyTurnIsUnknown(t *testing.T) {
	t.Parallel()

	transport := responsesTransport(t, loadFixture(t, "image_generate.sse"))

	outcome, _ := chatgptcodex.New(transport).
		Run(t.Context(), chatCommand("gpt-fixture-codex-1"), &staticCredential{material: codexBundleMaterial()})
	if outcome.Commit != domain.CommitUnknown {
		t.Fatalf("commit = %s, want unknown (undeliverable image asset)", outcome.Commit)
	}
}

// TestRunQuotaMidStreamIsNotCommittedWhenNoContent asserts an in-stream
// usage_limit_reached with no prior content is not-committed with a quota
// failure class: auth was proven at open, the quota is a scoped cooldown, and
// nothing was generated.
func TestRunQuotaMidStreamIsNotCommittedWhenNoContent(t *testing.T) {
	t.Parallel()

	transport := responsesTransport(t, compactJSON(t, loadFixtureSection(t, "quota_rate.json", "quota_in_stream_event")))

	outcome, _ := chatgptcodex.New(transport).
		Run(t.Context(), chatCommand("gpt-fixture-codex-1"), &staticCredential{material: codexBundleMaterial()})
	if outcome.Commit != domain.CommitNotCommitted {
		t.Fatalf("commit = %s, want not_committed (quota before any content)", outcome.Commit)
	}
	if outcome.FailureClass != domain.ErrCodeProviderQuotaExhausted {
		t.Errorf("failure class = %q, want provider_quota_exhausted", outcome.FailureClass)
	}
}

// TestStreamCommitsAndDeliversDeltas asserts the streaming surface delivers
// canonical deltas to the sink and commits a clean turn.
func TestStreamCommitsAndDeliversDeltas(t *testing.T) {
	t.Parallel()

	transport := responsesTransport(t, loadFixture(t, "chat_stream.sse"))
	sink := &recordingSink{}

	outcome, err := chatgptcodex.New(transport).
		Stream(t.Context(), streamCommand("gpt-fixture-codex-1"), &staticCredential{material: codexBundleMaterial()}, sink)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if outcome.Commit != domain.CommitCommitted {
		t.Fatalf("commit = %s, want committed", outcome.Commit)
	}
	if got := sink.joined(); got != "Hello world" {
		t.Errorf("delivered deltas = %q, want %q", got, "Hello world")
	}
}

// refreshTransport scripts a 401 on the first Responses exchange, a successful
// OAuth refresh, and a 200 stream on the second Responses exchange. It proves
// the in-boundary refresh-and-retry path: the rotated access_token is used only
// for the re-send and the Adapter does NOT re-run the whole operation from
// scratch on a different account.
type refreshTransport struct {
	responsesCalls int
	refreshCalls   int
	stream         *sseStream
	refreshBody    string
}

func (transport *refreshTransport) Exchange(_ context.Context, request chatgptcodex.Request) (chatgptcodex.Response, error) {
	switch request.Path {
	case chatgptcodex.PathCodexResponses:
		transport.responsesCalls++
		if transport.responsesCalls == 1 {
			return chatgptcodex.Response{Status: http.StatusUnauthorized}, nil
		}
		return chatgptcodex.Response{Status: http.StatusOK, Stream: transport.stream}, nil
	case chatgptcodex.PathOAuthToken:
		transport.refreshCalls++
		return chatgptcodex.Response{Status: http.StatusOK, Body: transport.refreshBody}, nil
	}
	return chatgptcodex.Response{Status: 500}, nil
}

// TestRefreshAndRetryOnAuthFailure asserts a 401 triggers one in-boundary
// refresh and a single re-send of the same exchange, after which the turn
// commits (evidence §3.2 "on 401 refresh-and-retry").
func TestRefreshAndRetryOnAuthFailure(t *testing.T) {
	t.Parallel()

	transport := &refreshTransport{
		stream:      newSSEStream(loadFixture(t, "chat_stream.sse")),
		refreshBody: loadFixtureSection(t, "token_refresh.json", "refresh_success"),
	}

	outcome, err := chatgptcodex.New(transport).
		Run(t.Context(), chatCommand("gpt-fixture-codex-1"), &staticCredential{material: codexBundleMaterial()})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Commit != domain.CommitCommitted {
		t.Fatalf("commit = %s, want committed after refresh-and-retry", outcome.Commit)
	}
	if transport.refreshCalls != 1 {
		t.Errorf("refresh exchanged %d times, want 1", transport.refreshCalls)
	}
	if transport.responsesCalls != 2 {
		t.Errorf("Responses exchanged %d times, want 2 (initial 401 + one re-send)", transport.responsesCalls)
	}
}

// TestRefreshFailureIsAuthExpired asserts a failed refresh (reused/revoked
// refresh_token) classifies as auth-expired and does not loop.
func TestRefreshFailureIsAuthExpired(t *testing.T) {
	t.Parallel()

	transport := &refreshTransport{
		stream: newSSEStream(loadFixture(t, "chat_stream.sse")),
		// A reused/revoked refresh_token returns 200 with an error body and no
		// access_token; refreshAccessToken treats the unparseable token as a
		// refresh failure rather than looping.
		refreshBody: `{"error":"refresh_token_reused","error_description":"revoked"}`,
	}

	outcome, _ := chatgptcodex.New(transport).
		Run(t.Context(), chatCommand("gpt-fixture-codex-1"), &staticCredential{material: codexBundleMaterial()})
	if outcome.Commit != domain.CommitNotCommitted {
		t.Fatalf("commit = %s, want not_committed (a failed refresh generates nothing)", outcome.Commit)
	}
	if outcome.FailureClass != domain.ErrCodeProviderAuthExpired {
		t.Errorf("failure class = %q, want provider_auth_expired", outcome.FailureClass)
	}
	if transport.refreshCalls != 1 {
		t.Errorf("refresh exchanged %d times, want 1 (no refresh loop)", transport.refreshCalls)
	}
}

// TestNoRefreshWithoutRefreshToken asserts a 401 with no refresh_token in the
// bundle does not attempt a refresh — the account moves straight to auth-expired.
func TestNoRefreshWithoutRefreshToken(t *testing.T) {
	t.Parallel()

	transport := &refreshTransport{
		stream:      newSSEStream(loadFixture(t, "chat_stream.sse")),
		refreshBody: loadFixtureSection(t, "token_refresh.json", "refresh_success"),
	}

	outcome, _ := chatgptcodex.New(transport).
		Run(t.Context(), chatCommand("gpt-fixture-codex-1"), &staticCredential{material: codexBundleWithoutRefresh()})
	if outcome.Commit != domain.CommitNotCommitted {
		t.Fatalf("commit = %s, want not_committed", outcome.Commit)
	}
	if outcome.FailureClass != domain.ErrCodeProviderAuthExpired {
		t.Errorf("failure class = %q, want provider_auth_expired", outcome.FailureClass)
	}
	if transport.refreshCalls != 0 {
		t.Errorf("refresh exchanged %d times, want 0 (no refresh_token to use)", transport.refreshCalls)
	}
}
// foreign Auth Mode rather than applying Codex framing to it.
func TestRunNilTransportIsAuthoritativelyNotCommitted(t *testing.T) {
	t.Parallel()

	// A nil transport can never transmit, so the failure is authoritative
	// no-commit: the spine may re-attempt on another account without risking a
	// second generation (chat/stream lifecycle §7.2 rule 2).
	outcome, err := chatgptcodex.New(nil).
		Run(t.Context(), chatCommand("gpt-fixture-codex-1"), &staticCredential{material: codexBundleMaterial()})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Commit != domain.CommitNotCommitted {
		t.Fatalf("commit = %s, want not_committed (a nil transport never transmitted)", outcome.Commit)
	}
}

// TestRunTransportErrorIsUnknown asserts a transport egress failure after the
// POST is attempted is NOT reported authoritatively not-committed: the Gateway
// cannot prove the payload never left the process, so the attempt is possibly
// committed and a fallback re-attempt could bill a second generation (§7.2 rule 3).
func TestRunTransportErrorIsUnknown(t *testing.T) {
	t.Parallel()

	transport := &fixtureTransport{err: errors.New("transport egress failure")}

	outcome, _ := chatgptcodex.New(transport).
		Run(t.Context(), chatCommand("gpt-fixture-codex-1"), &staticCredential{material: codexBundleMaterial()})
	if outcome.Commit != domain.CommitUnknown {
		t.Fatalf("commit = %s, want unknown (payload may have left the process)", outcome.Commit)
	}
	if outcome.FailureClass != domain.ErrCodeExecutionPossiblyCommitted {
		t.Errorf("failure class = %q, want execution_possibly_committed", outcome.FailureClass)
	}
}

// TestStreamTransportErrorIsUnknown mirrors TestRunTransportErrorIsUnknown on
// the streaming surface: the shared classifier must not drift apart.
func TestStreamTransportErrorIsUnknown(t *testing.T) {
	t.Parallel()

	transport := &fixtureTransport{err: errors.New("transport egress failure")}

	outcome, _ := chatgptcodex.New(transport).
		Stream(t.Context(), streamCommand("gpt-fixture-codex-1"), &staticCredential{material: codexBundleMaterial()}, &recordingSink{})
	if outcome.Commit != domain.CommitUnknown {
		t.Fatalf("commit = %s, want unknown (payload may have left the process)", outcome.Commit)
	}
}

// TestRunNonStreaming200ResponseIsUnknown asserts a 200 that carried no SSE
// stream is UNKNOWN, not not-committed: the 200 proves the payload reached the
// Provider, so the attempt is possibly committed (§7.2 rule 3).
func TestRunNonStreaming200ResponseIsUnknown(t *testing.T) {
	t.Parallel()

	transport := newFixtureTransport().on(chatgptcodex.PathCodexResponses, chatgptcodex.Response{Status: http.StatusOK})

	outcome, _ := chatgptcodex.New(transport).
		Run(t.Context(), chatCommand("gpt-fixture-codex-1"), &staticCredential{material: codexBundleMaterial()})
	if outcome.Commit != domain.CommitUnknown {
		t.Fatalf("commit = %s, want unknown (a 200 was answered but no stream arrived)", outcome.Commit)
	}
}

func TestRunRejectsAnotherAuthMode(t *testing.T) {
	t.Parallel()

	command := chatCommand("gpt-fixture-codex-1")
	command.AuthMode = domain.AuthModeChatGPTWebAccess

	_, err := chatgptcodex.New(responsesTransport(t, loadFixture(t, "chat_stream.sse"))).
		Run(t.Context(), command, &staticCredential{material: codexBundleMaterial()})
	if err == nil {
		t.Fatal("Run() error = nil, want an unavailable error for a foreign Auth Mode")
	}
}
