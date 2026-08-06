package chatgptcodex_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/adapters/chatgptcodex"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// codexBundleWithoutAccountID builds an OAuth bundle with no account_id so a
// test can prove the bundle is rejected before any exchange.
func codexBundleWithoutAccountID() string {
	return `{"access_token":"fixture-access-token","refresh_token":"fixture-refresh-token"}`
}

// TestRunRejectsBundleWithoutAccountID asserts a bundle without account_id is
// rejected before any exchange, mirroring the existing AccessToken check: a
// bundle without a binding account_id would otherwise reach upstream without
// the Chatgpt-Account-Id header, letting the Provider use its default
// account rather than the one the Vault selected.
func TestRunRejectsBundleWithoutAccountID(t *testing.T) {
	t.Parallel()

	transport := responsesTransport(t, loadFixture(t, "chat_stream.sse"))

	outcome, err := chatgptcodex.New(transport).
		Run(t.Context(), chatCommand("gpt-fixture-codex-1"), &staticCredential{material: codexBundleWithoutAccountID()})
	if err != nil {
		t.Fatalf("Run() error = %v, want nil (a malformed bundle is an outcome)", err)
	}
	if outcome.Commit != domain.CommitNotCommitted {
		t.Fatalf("commit = %s, want not_committed (a malformed bundle never reaches upstream)", outcome.Commit)
	}
	if outcome.FailureClass != domain.ErrCodeUpstreamProtocolDrift {
		t.Errorf("failure class = %q, want upstream_protocol_drift", outcome.FailureClass)
	}
	if got := transport.count(chatgptcodex.PathCodexResponses); got != 0 {
		t.Errorf("Responses exchanged %d times, want 0 (no account_id means no exchange)", got)
	}
}

// TestWithResponsesRejects429UsageLimitBodyAsQuota asserts an initial-open 429
// carrying a usage_limit_reached body is classified as quota exhaustion, not
// generic rate limiting, mirroring adapter.go's Probe path which calls
// parseUsageLimit on a 429. Before the fix, every 429 on this path was mapped
// unconditionally to signalRateLimited without inspecting the body.
func TestWithResponsesRejects429UsageLimitBodyAsQuota(t *testing.T) {
	t.Parallel()

	transport := newFixtureTransport().on(chatgptcodex.PathCodexResponses, chatgptcodex.Response{
		Status: http.StatusTooManyRequests,
		Body:   loadFixtureSection(t, "quota_rate.json", "usage_limit_reached"),
	})

	outcome, _ := chatgptcodex.New(transport).
		Run(t.Context(), chatCommand("gpt-fixture-codex-1"), &staticCredential{material: codexBundleMaterial()})
	if outcome.Commit != domain.CommitNotCommitted {
		t.Fatalf("commit = %s, want not_committed (a refused 429 generates nothing)", outcome.Commit)
	}
	if outcome.FailureClass != domain.ErrCodeProviderQuotaExhausted {
		t.Errorf("failure class = %q, want provider_quota_exhausted (usage_limit_reached body on a 429)", outcome.FailureClass)
	}
}

// TestWithResponsesRejects429RateLimitBodyAsRateLimited asserts an initial-open
// 429 with a plain rate_limit_error body (or no recognizable quota body) is
// still classified as a transient rate limit.
func TestWithResponsesRejects429RateLimitBodyAsRateLimited(t *testing.T) {
	t.Parallel()

	transport := newFixtureTransport().on(chatgptcodex.PathCodexResponses, chatgptcodex.Response{
		Status: http.StatusTooManyRequests,
		Body:   loadFixtureSection(t, "quota_rate.json", "rate_limit_error"),
	})

	outcome, _ := chatgptcodex.New(transport).
		Run(t.Context(), chatCommand("gpt-fixture-codex-1"), &staticCredential{material: codexBundleMaterial()})
	if outcome.Commit != domain.CommitNotCommitted {
		t.Fatalf("commit = %s, want not_committed (a refused 429 generates nothing)", outcome.Commit)
	}
	if outcome.FailureClass != domain.ErrCodeProviderRateLimited {
		t.Errorf("failure class = %q, want provider_rate_limited", outcome.FailureClass)
	}
}

// refreshStatusTransport scripts a controlled status on the OAuth refresh
// endpoint so a test can prove rotateCredential's status split.
type refreshStatusTransport struct {
	refreshStatus int
	refreshCalls  int
}

func (transport *refreshStatusTransport) Exchange(_ context.Context, request chatgptcodex.Request) (chatgptcodex.Response, error) {
	switch request.Path {
	case chatgptcodex.PathCodexResponses:
		return chatgptcodex.Response{Status: http.StatusUnauthorized}, nil
	case chatgptcodex.PathOAuthToken:
		transport.refreshCalls++
		return chatgptcodex.Response{Status: transport.refreshStatus}, nil
	}
	return chatgptcodex.Response{Status: 500}, nil
}

// TestRotateCredentialSplitsUnavailableFromRefreshFailed asserts a transient
// 5xx from the OAuth refresh endpoint is classified as errUnavailable
// (ErrCodeUpstreamUnavailable), NOT the same auth-expired class a refused
// 400/401 grant gets. Before the fix, both fell into the same default branch
// of the status switch, so a healthy account behind a transient backend outage
// was reported as provider_auth_expired and pushed into reauthentication.
func TestRotateCredentialSplitsUnavailableFromRefreshFailed(t *testing.T) {
	t.Parallel()

	t.Run("503 is unavailable, not auth-expired", func(t *testing.T) {
		t.Parallel()
		transport := &refreshStatusTransport{refreshStatus: http.StatusServiceUnavailable}
		credential := &rotatingCredential{material: codexBundleMaterial()}

		outcome, _ := chatgptcodex.New(transport).
			Run(t.Context(), chatCommand("gpt-fixture-codex-1"), credential)
		if transport.refreshCalls != 1 {
			t.Fatalf("refresh exchanged %d times, want 1", transport.refreshCalls)
		}
		// A transient 5xx from the refresh endpoint is NOT proof the
		// refresh_token is bad, so it must not classify as provider_auth_expired
		// (which would push a healthy account into reauthentication). It also
		// has no authoritative not-committed proof (unlike a refused 400/401
		// grant), so it classifies as UNKNOWN/execution_possibly_committed
		// rather than NotCommitted/upstream_unavailable.
		if outcome.FailureClass == domain.ErrCodeProviderAuthExpired {
			t.Errorf("failure class = %q, want NOT provider_auth_expired (a transient 5xx is not proof the refresh_token is bad)", outcome.FailureClass)
		}
		if outcome.Commit == domain.CommitNotCommitted {
			t.Errorf("commit = %s, want NOT not_committed (a transient 5xx is not authoritative proof of anything)", outcome.Commit)
		}
	})

	t.Run("401 is auth-expired", func(t *testing.T) {
		t.Parallel()
		transport := &refreshStatusTransport{refreshStatus: http.StatusUnauthorized}
		credential := &rotatingCredential{material: codexBundleMaterial()}

		outcome, _ := chatgptcodex.New(transport).
			Run(t.Context(), chatCommand("gpt-fixture-codex-1"), credential)
		if transport.refreshCalls != 1 {
			t.Fatalf("refresh exchanged %d times, want 1", transport.refreshCalls)
		}
		if outcome.FailureClass != domain.ErrCodeProviderAuthExpired {
			t.Errorf("failure class = %q, want provider_auth_expired", outcome.FailureClass)
		}
	})
}

// accountIDCapturingTransport records the Chatgpt-Account-Id header sent on
// every Responses exchange, so a test can prove the header survives rotation.
type accountIDCapturingTransport struct {
	responsesCalls int
	accountIDs     []string
	stream         *sseStream
	refreshBody    string
}

func (transport *accountIDCapturingTransport) Exchange(_ context.Context, request chatgptcodex.Request) (chatgptcodex.Response, error) {
	switch request.Path {
	case chatgptcodex.PathCodexResponses:
		transport.responsesCalls++
		transport.accountIDs = append(transport.accountIDs, request.Headers["Chatgpt-Account-Id"])
		if transport.responsesCalls == 1 {
			return chatgptcodex.Response{Status: http.StatusUnauthorized}, nil
		}
		return chatgptcodex.Response{Status: http.StatusOK, Stream: transport.stream}, nil
	case chatgptcodex.PathOAuthToken:
		return chatgptcodex.Response{Status: http.StatusOK, Body: transport.refreshBody}, nil
	}
	return chatgptcodex.Response{Status: 500}, nil
}

// TestRotationPreservesAccountIDWhenRotatedBodyOmitsIt asserts the retried
// exchange still carries the original Chatgpt-Account-Id header when the
// OAuth token endpoint body does not itself carry account_id — the documented
// shape (the token endpoint re-derives account_id from id_token claims rather
// than always echoing it; see .ref/CLIProxyAPI internal/auth/codex/openai_auth.go).
// Before the fix, the rotated bundle's empty AccountID replaced the original
// one and the retry silently dropped the binding header.
func TestRotationPreservesAccountIDWhenRotatedBodyOmitsIt(t *testing.T) {
	t.Parallel()

	transport := &accountIDCapturingTransport{
		stream: newSSEStream(loadFixture(t, "chat_stream.sse")),
		// The rotated body carries no account_id, mirroring the real OAuth
		// token endpoint shape.
		refreshBody: `{"access_token":"fixture-rotated-access-token","refresh_token":"fixture-rotated-refresh-token"}`,
	}
	credential := &rotatingCredential{material: codexBundleMaterial()}

	outcome, err := chatgptcodex.New(transport).
		Run(t.Context(), chatCommand("gpt-fixture-codex-1"), credential)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Commit != domain.CommitCommitted {
		t.Fatalf("commit = %s, want committed", outcome.Commit)
	}
	if len(transport.accountIDs) != 2 {
		t.Fatalf("Responses exchanged %d times, want 2", len(transport.accountIDs))
	}
	for i, accountID := range transport.accountIDs {
		if accountID != "fixture-account-id" {
			t.Errorf("exchange %d Chatgpt-Account-Id = %q, want %q (the original account_id must survive rotation)", i, accountID, "fixture-account-id")
		}
	}
}

// TestRunMidStreamTransportFailureIsUnknown exercises sseStream's failAt/err
// mid-stream failure branch: content already arrived when the stream's
// underlying read fails (as opposed to the usage_limit_reached case, which is
// a well-formed error payload, not a transport failure). Content arrived
// before the break, so commit certainty is forfeited (UNKNOWN), and the
// failure class is the raw egress class (execution_possibly_committed), not a
// quota/rate class.
func TestRunMidStreamTransportFailureIsUnknown(t *testing.T) {
	t.Parallel()

	stream := newSSEStream(`{"type":"response.output_text.delta","delta":"partial"}`)
	stream.failAt = 1
	stream.err = errors.New("fixture: mid-stream transport read failure")
	transport := newFixtureTransport().on(chatgptcodex.PathCodexResponses, chatgptcodex.Response{
		Status: http.StatusOK,
		Stream: stream,
	})

	outcome, _ := chatgptcodex.New(transport).
		Run(t.Context(), chatCommand("gpt-fixture-codex-1"), &staticCredential{material: codexBundleMaterial()})
	if outcome.Commit != domain.CommitUnknown {
		t.Fatalf("commit = %s, want unknown (content arrived before the mid-stream transport failure)", outcome.Commit)
	}
	if outcome.FailureClass != domain.ErrCodeExecutionPossiblyCommitted {
		t.Errorf("failure class = %q, want execution_possibly_committed", outcome.FailureClass)
	}
}

// "cloudflare_block" body into a real test: a 403 carrying that body is
// classified as a challenge (dependency failure), not an auth failure. Before
// this test, the fixture was never loaded by any test.
func TestProbeCloudflareBlockIsChallenged(t *testing.T) {
	t.Parallel()

	transport := newFixtureTransport().on(chatgptcodex.PathMe, chatgptcodex.Response{
		Status: http.StatusForbidden,
		Body:   loadFixtureSection(t, "challenge.json", "cloudflare_block"),
	})

	_, err := chatgptcodex.New(transport).Probe(t.Context(), probeCommand())
	if err == nil {
		t.Fatal("Probe() error = nil, want ErrDependencyUnavailable for a Cloudflare challenge")
	}
}

// token endpoint DOES carry its own account_id, that value is used rather than
// the original — the rotated grant is authoritative when it actually says
// something.
func TestRotationPrefersRotatedAccountIDWhenPresent(t *testing.T) {
	t.Parallel()

	transport := &accountIDCapturingTransport{
		stream:      newSSEStream(loadFixture(t, "chat_stream.sse")),
		refreshBody: `{"access_token":"fixture-rotated-access-token","refresh_token":"fixture-rotated-refresh-token","account_id":"fixture-rotated-account-id"}`,
	}
	credential := &rotatingCredential{material: codexBundleMaterial()}

	outcome, err := chatgptcodex.New(transport).
		Run(t.Context(), chatCommand("gpt-fixture-codex-1"), credential)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Commit != domain.CommitCommitted {
		t.Fatalf("commit = %s, want committed", outcome.Commit)
	}
	if len(transport.accountIDs) != 2 {
		t.Fatalf("Responses exchanged %d times, want 2", len(transport.accountIDs))
	}
	if got := transport.accountIDs[1]; got != "fixture-rotated-account-id" {
		t.Errorf("retried exchange Chatgpt-Account-Id = %q, want the rotated value %q", got, "fixture-rotated-account-id")
	}
}
