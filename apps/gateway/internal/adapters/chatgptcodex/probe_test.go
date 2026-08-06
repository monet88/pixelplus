package chatgptcodex_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/adapters/chatgptcodex"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// TestProbeProvesAuthWithoutRunningAGeneration asserts the probe proves the
// access_token with a minimal identity call and never posts to the billable
// Responses endpoint (I-PROBE-MINIMAL).
func TestProbeProvesAuthWithoutRunningAGeneration(t *testing.T) {
	t.Parallel()

	transport := newFixtureTransport().on(chatgptcodex.PathMe, chatgptcodex.Response{
		Status: http.StatusOK,
		Body:   loadFixtureSection(t, "token_refresh.json", "identity_ok"),
	})

	outcome, err := chatgptcodex.New(transport).Probe(t.Context(), probeCommand())
	if err != nil {
		t.Fatalf("Probe() error = %v, want nil (auth success is an outcome)", err)
	}
	if !outcome.Authenticated {
		t.Error("Authenticated = false, want true")
	}
	if outcome.Signal != ports.ProbeSignalNone {
		t.Errorf("Signal = %q, want none", outcome.Signal)
	}
	if got := transport.count(chatgptcodex.PathCodexResponses); got != 0 {
		t.Errorf("Responses exchanged %d times, want 0 (a probe must not run a billable generation)", got)
	}
	if got := transport.count(chatgptcodex.PathMe); got != 1 {
		t.Errorf("Me exchanged %d times, want 1", got)
	}
}

// TestProbeAuthFailureIsAnOutcomeNotAnError asserts a 401 maps to
// Authenticated=false with a nil error so the account moves to reauth_required
// rather than surfacing a dependency 503.
func TestProbeAuthFailureIsAnOutcomeNotAnError(t *testing.T) {
	t.Parallel()

	transport := newFixtureTransport().on(chatgptcodex.PathMe, chatgptcodex.Response{Status: http.StatusUnauthorized})

	outcome, err := chatgptcodex.New(transport).Probe(t.Context(), probeCommand())
	if err != nil {
		t.Fatalf("Probe() error = %v, want nil (auth failure is an outcome)", err)
	}
	if outcome.Authenticated {
		t.Error("Authenticated = true, want false on 401")
	}
}

// TestProbeChallengeIsNotReportedAsAuthFailure asserts a 403 Cloudflare/bot
// block is a dependency failure, not an auth failure: the credential may be
// valid behind the block, so reporting it unauthenticated would send the
// Tenant to a pointless reauth.
func TestProbeChallengeIsNotReportedAsAuthFailure(t *testing.T) {
	t.Parallel()

	transport := newFixtureTransport().on(chatgptcodex.PathMe, chatgptcodex.Response{Status: http.StatusForbidden})

	outcome, err := chatgptcodex.New(transport).Probe(t.Context(), probeCommand())
	if !errors.Is(err, ports.ErrDependencyUnavailable) {
		t.Fatalf("Probe() error = %v, want ErrDependencyUnavailable (a challenge is not an auth failure)", err)
	}
	if outcome.Authenticated {
		t.Error("Authenticated = true on a challenge; the account must not activate")
	}
}

// TestProbeQuotaExhaustionActivatesWithAScopedCooldown asserts a 429 carrying a
// usage_limit_reached body activates the account (auth proven) with a quota
// signal and the upstream reset hint.
func TestProbeQuotaExhaustionActivatesWithAScopedCooldown(t *testing.T) {
	t.Parallel()

	transport := newFixtureTransport().on(chatgptcodex.PathMe, chatgptcodex.Response{
		Status: http.StatusTooManyRequests,
		Body:   loadFixtureSection(t, "quota_rate.json", "usage_limit_reached"),
	})

	outcome, err := chatgptcodex.New(transport).Probe(t.Context(), probeCommand())
	if err != nil {
		t.Fatalf("Probe() error = %v, want nil", err)
	}
	if !outcome.Authenticated {
		t.Fatal("Authenticated = false, want true (auth proven before the quota signal)")
	}
	if outcome.Signal != ports.ProbeSignalQuotaExhausted {
		t.Errorf("Signal = %q, want quota_exhausted", outcome.Signal)
	}
	if outcome.RetryAfterSeconds != 3600 {
		t.Errorf("RetryAfterSeconds = %d, want 3600", outcome.RetryAfterSeconds)
	}
}

// TestProbeRateLimitActivatesWithAnAccountCooldown asserts a 429 without a
// usage_limit body is the transient rate-limit class.
func TestProbeRateLimitActivatesWithAnAccountCooldown(t *testing.T) {
	t.Parallel()

	transport := newFixtureTransport().on(chatgptcodex.PathMe, chatgptcodex.Response{
		Status: http.StatusTooManyRequests,
		Body:   loadFixtureSection(t, "quota_rate.json", "rate_limit_error"),
	})

	outcome, err := chatgptcodex.New(transport).Probe(t.Context(), probeCommand())
	if err != nil {
		t.Fatalf("Probe() error = %v, want nil", err)
	}
	if !outcome.Authenticated {
		t.Fatal("Authenticated = false, want true")
	}
	if outcome.Signal != ports.ProbeSignalRateLimited {
		t.Errorf("Signal = %q, want rate_limited", outcome.Signal)
	}
}

// TestProbeRejectsAnotherAuthMode asserts a registry misconfiguration that
// routes another mode here fails closed rather than applying Codex framing to a
// different credential class.
func TestProbeRejectsAnotherAuthMode(t *testing.T) {
	t.Parallel()

	command := probeCommand()
	command.AuthMode = domain.AuthModeChatGPTWebAccess

	_, err := chatgptcodex.New(newFixtureTransport()).Probe(t.Context(), command)
	if !errors.Is(err, ports.ErrDependencyUnavailable) {
		t.Fatalf("Probe() error = %v, want ErrDependencyUnavailable for a foreign Auth Mode", err)
	}
}

// TestObserveReportsOnlyEvidenceBackedCapability asserts every operation is
// reported at conditionally_supported (the accepted evidence ceiling for Codex,
// §2.2), chat_streaming is real, and the probe surface binds to the Codex
// Responses path.
func TestObserveReportsOnlyEvidenceBackedCapability(t *testing.T) {
	t.Parallel()

	transport := newFixtureTransport().on(chatgptcodex.PathModels, chatgptcodex.Response{
		Status: http.StatusOK,
		Body:   loadFixtureSection(t, "token_refresh.json", "models_listing"),
	})

	observation, err := chatgptcodex.New(transport).Observe(t.Context(), capabilityCommand())
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if observation.ProbeSurface != chatgptcodex.PathCodexResponses {
		t.Errorf("ProbeSurface = %q, want %q", observation.ProbeSurface, chatgptcodex.PathCodexResponses)
	}
	for _, operation := range domain.PrimaryCapabilityOperations() {
		fact, ok := observation.Operations[operation]
		if !ok {
			t.Errorf("operation %s missing from observation", operation)
			continue
		}
		if fact.Status != domain.CapabilityConditionallySupported {
			t.Errorf("operation %s status = %q, want conditionally_supported", operation, fact.Status)
		}
		if fact.EvidenceClass != domain.EvidenceReferenceLearned {
			t.Errorf("operation %s evidence = %q, want reference_learned", operation, fact.EvidenceClass)
		}
		if operation == domain.CapabilityOpChatStreaming && fact.StreamingClass != domain.StreamingReal {
			t.Errorf("chat_streaming class = %q, want real", fact.StreamingClass)
		}
	}
	if len(observation.Models) != 2 {
		t.Fatalf("Models = %d rows, want 2", len(observation.Models))
	}
	if observation.Models[0].ModelSlug != "gpt-fixture-codex-1" {
		t.Errorf("first model slug = %q, want gpt-fixture-codex-1", observation.Models[0].ModelSlug)
	}
}

// TestObserveFailsClosedWhenTheSessionDies asserts a 401 on the models call
// fails closed rather than minting an empty snapshot that would read as "this
// account supports nothing".
func TestObserveFailsClosedWhenTheSessionDies(t *testing.T) {
	t.Parallel()

	transport := newFixtureTransport().on(chatgptcodex.PathModels, chatgptcodex.Response{Status: http.StatusUnauthorized})

	_, err := chatgptcodex.New(transport).Observe(t.Context(), capabilityCommand())
	if !errors.Is(err, ports.ErrDependencyUnavailable) {
		t.Fatalf("Observe() error = %v, want ErrDependencyUnavailable on a dead session", err)
	}
}
