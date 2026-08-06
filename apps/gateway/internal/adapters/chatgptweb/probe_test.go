package chatgptweb_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/adapters/chatgptweb"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

func probeCommand() ports.ProbeCommand {
	return ports.ProbeCommand{
		AccountID: "pa_lab_web",
		AuthMode:  domain.AuthModeChatGPTWebAccess,
		Version:   1,
		Scope:     domain.HealthScope{Kind: domain.HealthScopeAccount},
	}
}

// prepareTransport scripts a healthy credential-preparation sequence, letting a
// caller override the conversation/init body to vary the quota view.
func prepareTransport(t *testing.T, initSection string) *fixtureTransport {
	t.Helper()
	body := loadFixtureSection(t, "credential_prepare.json", "conversation_init")
	if initSection != "" {
		body = loadFixtureSection(t, "quota_rate.json", initSection)
	}
	return newFixtureTransport().
		on(chatgptweb.PathMe, chatgptweb.Response{
			Status: http.StatusOK,
			Body:   loadFixtureSection(t, "credential_prepare.json", "me"),
		}).
		on(chatgptweb.PathConversationInit, chatgptweb.Response{Status: http.StatusOK, Body: body})
}

func TestProbeProvesAuthWithoutRunningAGeneration(t *testing.T) {
	t.Parallel()

	transport := prepareTransport(t, "")
	adapter := chatgptweb.New(transport)

	outcome, err := adapter.Probe(t.Context(), probeCommand())
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if !outcome.Authenticated {
		t.Fatal("probe did not authenticate on a healthy session")
	}
	if outcome.Signal != ports.ProbeSignalNone {
		t.Errorf("signal = %q, want none on a healthy probe", outcome.Signal)
	}
	// I-PROBE-MINIMAL: the probe proves authentication only. Opening a
	// conversation would be a billable generation.
	if calls := transport.count(chatgptweb.PathConversation); calls != 0 {
		t.Fatalf("probe opened %d conversations, want 0 (probe must not generate)", calls)
	}
}

func TestProbeAuthFailureIsAnOutcomeNotAnError(t *testing.T) {
	t.Parallel()

	transport := newFixtureTransport().
		on(chatgptweb.PathMe, chatgptweb.Response{Status: http.StatusUnauthorized})
	adapter := chatgptweb.New(transport)

	outcome, err := adapter.Probe(t.Context(), probeCommand())
	// An auth-class failure must move the account to reauth_required, which only
	// happens when it is reported as Authenticated=false with a nil error. An
	// error here would surface a dependency 503 instead (ports.ProbeAdapter).
	if err != nil {
		t.Fatalf("Probe() error = %v, want nil so the account moves to reauth_required", err)
	}
	if outcome.Authenticated {
		t.Fatal("probe authenticated on a 401")
	}
	// Web Access has no silent refresh, so there is no refresh attempt to make.
	if calls := transport.count(chatgptweb.PathConversationInit); calls != 0 {
		t.Errorf("probe continued past a 401 (%d init calls), want 0", calls)
	}
}

func TestProbeChallengeIsNotReportedAsAuthFailure(t *testing.T) {
	t.Parallel()

	transport := newFixtureTransport().
		on(chatgptweb.PathMe, chatgptweb.Response{Status: http.StatusForbidden})
	adapter := chatgptweb.New(transport)

	outcome, err := adapter.Probe(t.Context(), probeCommand())
	// A challenged session may hold a perfectly valid credential. Reporting it as
	// unauthenticated would send the Tenant to a pointless reauth.
	if !errors.Is(err, ports.ErrDependencyUnavailable) {
		t.Fatalf("Probe() error = %v, want dependency unavailable", err)
	}
	if outcome.Authenticated {
		t.Error("challenged probe reported as authenticated")
	}
}

func TestProbeQuotaExhaustionActivatesWithAScopedCooldown(t *testing.T) {
	t.Parallel()

	transport := prepareTransport(t, "image_quota_exhausted")
	adapter := chatgptweb.New(transport)

	outcome, err := adapter.Probe(t.Context(), probeCommand())
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	// The credential proved itself: an exhausted image allowance is a cooldown,
	// not an auth failure, so the account still activates.
	if !outcome.Authenticated {
		t.Fatal("quota exhaustion blocked authentication")
	}
	if outcome.Signal != ports.ProbeSignalQuotaExhausted {
		t.Fatalf("signal = %q, want quota_exhausted", outcome.Signal)
	}
	// Scoped to the image operation, so chat stays usable on the same account:
	// limits_progress names one feature, not the whole session.
	if outcome.SignalScope.Kind != domain.HealthScopeOperation {
		t.Errorf("scope kind = %s, want operation", outcome.SignalScope.Kind)
	}
	if outcome.SignalScope.Operation != string(domain.CapabilityOpImageGeneration) {
		t.Errorf("scope operation = %s, want image_generation", outcome.SignalScope.Operation)
	}
	if outcome.RetryAfterSeconds != 1800 {
		t.Errorf("retry after = %d, want 1800", outcome.RetryAfterSeconds)
	}
}

func TestProbeTreatsAMissingImageRowAsNoSignal(t *testing.T) {
	t.Parallel()

	transport := prepareTransport(t, "no_image_row")
	adapter := chatgptweb.New(transport)

	outcome, err := adapter.Probe(t.Context(), probeCommand())
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	// A missing image_gen row means nothing was observed. Treating absence as a
	// zero allowance would invent a cooldown out of silence.
	if outcome.Signal != ports.ProbeSignalNone {
		t.Fatalf("signal = %q, want none when no image_gen row was observed", outcome.Signal)
	}
	if !outcome.Authenticated {
		t.Error("probe did not authenticate")
	}
}

func TestProbeRateLimitActivatesWithAnAccountCooldown(t *testing.T) {
	t.Parallel()

	transport := newFixtureTransport().
		on(chatgptweb.PathMe, chatgptweb.Response{Status: http.StatusTooManyRequests, RetryAfterSeconds: 45})
	adapter := chatgptweb.New(transport)

	outcome, err := adapter.Probe(t.Context(), probeCommand())
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if !outcome.Authenticated {
		t.Fatal("rate limit blocked authentication")
	}
	if outcome.Signal != ports.ProbeSignalRateLimited {
		t.Fatalf("signal = %q, want rate_limited", outcome.Signal)
	}
	if outcome.RetryAfterSeconds != 45 {
		t.Errorf("retry after = %d, want 45", outcome.RetryAfterSeconds)
	}
}

func TestObserveReportsOnlyEvidenceBackedCapability(t *testing.T) {
	t.Parallel()

	transport := newFixtureTransport().
		on(chatgptweb.PathModels, chatgptweb.Response{
			Status: http.StatusOK,
			Body:   loadFixtureSection(t, "credential_prepare.json", "models"),
		})
	adapter := chatgptweb.New(transport)

	observation, err := adapter.Observe(t.Context(), ports.CapabilityObservationCommand{
		AccountID: "pa_lab_web",
		AuthMode:  domain.AuthModeChatGPTWebAccess,
		Version:   1,
	})
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}

	// Evidence §2.1 records every primary operation as conditionally supported and
	// none as verified. The Adapter must not claim more than that even before the
	// domain clamp runs.
	for _, operation := range domain.PrimaryCapabilityOperations() {
		fact, ok := observation.Operations[operation]
		if !ok {
			t.Errorf("operation %s missing from observation", operation)
			continue
		}
		if fact.Status != domain.CapabilityConditionallySupported {
			t.Errorf("operation %s = %s, want conditionally_supported", operation, fact.Status)
		}
	}
	// Streaming on this surface is native SSE, not a synthetic chunking of a
	// non-streaming answer (§2.1).
	if got := observation.Operations[domain.CapabilityOpChatStreaming].StreamingClass; got != domain.StreamingReal {
		t.Errorf("streaming class = %s, want real", got)
	}
	// Model slugs are session-dependent and must come from the probe, never a
	// static catalog.
	if len(observation.Models) != 2 {
		t.Fatalf("models = %d, want 2 observed slugs", len(observation.Models))
	}
	if observation.Models[0].ModelSlug != "gpt-fixture-1" {
		t.Errorf("first model = %q, want the observed slug", observation.Models[0].ModelSlug)
	}
}

func TestObserveFailsClosedWhenTheSessionDies(t *testing.T) {
	t.Parallel()

	transport := newFixtureTransport().
		on(chatgptweb.PathModels, chatgptweb.Response{Status: http.StatusUnauthorized})
	adapter := chatgptweb.New(transport)

	// Minting an empty observation would read as "this account supports nothing",
	// which is a stronger and wrong claim compared with failing closed.
	if _, err := adapter.Observe(t.Context(), ports.CapabilityObservationCommand{
		AuthMode: domain.AuthModeChatGPTWebAccess,
	}); !errors.Is(err, ports.ErrDependencyUnavailable) {
		t.Fatalf("Observe() error = %v, want dependency unavailable", err)
	}
}

func TestAdapterReportsItsSingleAuthMode(t *testing.T) {
	t.Parallel()

	if got := chatgptweb.New(nil).AuthMode(); got != domain.AuthModeChatGPTWebAccess {
		t.Fatalf("AuthMode() = %s, want chatgpt_web_access", got)
	}
}
