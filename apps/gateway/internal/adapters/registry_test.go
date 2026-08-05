package adapters_test

import (
	"context"
	"errors"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/adapters"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// countingChatAdapter records which Auth Mode reached it.
type countingChatAdapter struct {
	label string
	calls int
	modes []domain.AuthMode
}

func (adapter *countingChatAdapter) Run(_ context.Context, command ports.ChatCommand, _ ports.CredentialInjection) (domain.ChatOutcome, error) {
	adapter.calls++
	adapter.modes = append(adapter.modes, command.AuthMode)
	return domain.ChatOutcome{Class: domain.ChatOutcomeCommitted}, nil
}

type countingProbeAdapter struct {
	calls int
}

func (adapter *countingProbeAdapter) Probe(_ context.Context, _ ports.ProbeCommand) (ports.ProbeOutcome, error) {
	adapter.calls++
	return ports.ProbeOutcome{Authenticated: true}, nil
}

type countingCapabilityAdapter struct {
	calls int
}

func (adapter *countingCapabilityAdapter) Observe(_ context.Context, _ ports.CapabilityObservationCommand) (ports.CapabilityObservation, error) {
	adapter.calls++
	return ports.CapabilityObservation{}, nil
}

type countingStreamAdapter struct {
	calls int
}

func (adapter *countingStreamAdapter) Stream(_ context.Context, _ ports.ChatStreamCommand, _ ports.CredentialInjection, _ domain.ChatSink) (domain.ChatStreamOutcome, error) {
	adapter.calls++
	return domain.ChatStreamOutcome{}, nil
}

// dualSurfaceAdapter implements BOTH ports.ChatStreamAdapter and
// ports.ChatAdapter on a single object, and counts the two surfaces separately.
//
// Why one object rather than two doubles: the degradation this guards against
// is not "some unrelated Adapter ran", it is "the streaming registry held an
// Adapter and reached for its NON-streaming method". Real Adapters expose both
// surfaces — chatgptweb.Adapter asserts `_ ports.ChatAdapter` and
// `_ ports.ChatStreamAdapter` on the same struct — so a registry that type-
// asserted its way onto Run() would still be handing work to "the right"
// Adapter while silently producing a buffered body. Counting Run() and Stream()
// on the same receiver is the only way to observe that: two separate doubles
// would leave the non-streaming counter unreachable, which is precisely the
// vacuous assertion this test used to make.
type dualSurfaceAdapter struct {
	label       string
	streamCalls int
	runCalls    int
	modes       []domain.AuthMode
}

func (adapter *dualSurfaceAdapter) Stream(_ context.Context, command ports.ChatStreamCommand, _ ports.CredentialInjection, _ domain.ChatSink) (domain.ChatStreamOutcome, error) {
	adapter.streamCalls++
	adapter.modes = append(adapter.modes, command.AuthMode)
	return domain.ChatStreamOutcome{}, nil
}

func (adapter *dualSurfaceAdapter) Run(_ context.Context, _ ports.ChatCommand, _ ports.CredentialInjection) (domain.ChatOutcome, error) {
	adapter.runCalls++
	return domain.ChatOutcome{Class: domain.ChatOutcomeCommitted}, nil
}

var (
	_ ports.ChatStreamAdapter = (*dualSurfaceAdapter)(nil)
	_ ports.ChatAdapter       = (*dualSurfaceAdapter)(nil)
)

func TestChatRegistryDispatchesOnAuthModeAndFallsBackOtherwise(t *testing.T) {
	t.Parallel()

	experimental := &countingChatAdapter{label: "experimental"}
	fallback := &countingChatAdapter{label: "fallback"}
	registry := adapters.NewChatAdapterRegistry(fallback, map[domain.AuthMode]ports.ChatAdapter{
		domain.AuthModeChatGPTWebAccess: experimental,
	})

	if _, err := registry.Run(t.Context(), ports.ChatCommand{AuthMode: domain.AuthModeChatGPTWebAccess}, nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// A different Auth Mode must not be handed to the experimental Adapter: it
	// would apply ChatGPT Web protocol framing to another credential class.
	if _, err := registry.Run(t.Context(), ports.ChatCommand{AuthMode: domain.AuthModeChatGPTCodexOAuth}, nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if experimental.calls != 1 {
		t.Errorf("experimental adapter calls = %d, want 1", experimental.calls)
	}
	if fallback.calls != 1 {
		t.Errorf("fallback calls = %d, want 1", fallback.calls)
	}
	if len(experimental.modes) != 1 || experimental.modes[0] != domain.AuthModeChatGPTWebAccess {
		t.Errorf("experimental adapter saw %v, want only chatgpt_web_access", experimental.modes)
	}
}

func TestRegistriesFailClosedWithoutAFallback(t *testing.T) {
	t.Parallel()

	// A registry composed with neither a mapped Adapter nor a fallback must
	// refuse rather than return an empty success, which would read as a
	// committed-but-empty result to the spine.
	if _, err := adapters.NewChatAdapterRegistry(nil, nil).
		Run(t.Context(), ports.ChatCommand{}, nil); !errors.Is(err, ports.ErrChatAdapterUnavailable) {
		t.Errorf("chat error = %v, want chat adapter unavailable", err)
	}
	if _, err := adapters.NewChatStreamAdapterRegistry(nil, nil).
		Stream(t.Context(), ports.ChatStreamCommand{}, nil, nil); !errors.Is(err, ports.ErrChatStreamAdapterUnavailable) {
		t.Errorf("stream error = %v, want chat stream adapter unavailable", err)
	}
	if _, err := adapters.NewProbeAdapterRegistry(nil, nil).
		Probe(t.Context(), ports.ProbeCommand{}); !errors.Is(err, ports.ErrDependencyUnavailable) {
		t.Errorf("probe error = %v, want dependency unavailable", err)
	}
	if _, err := adapters.NewCapabilityAdapterRegistry(nil, nil).
		Observe(t.Context(), ports.CapabilityObservationCommand{}); !errors.Is(err, ports.ErrDependencyUnavailable) {
		t.Errorf("capability error = %v, want dependency unavailable", err)
	}
}

func TestStreamRegistryNeverDegradesToTheNonStreamingPath(t *testing.T) {
	t.Parallel()

	// Both Adapters in play expose a streaming AND a non-streaming surface, so
	// every dispatch below has a non-streaming method available to degrade onto.
	// That is what makes the zero on runCalls a real observation rather than a
	// statement about an object nobody wired in.
	registered := &dualSurfaceAdapter{label: "registered"}
	fallback := &dualSurfaceAdapter{label: "fallback"}
	registry := adapters.NewChatStreamAdapterRegistry(fallback, map[domain.AuthMode]ports.ChatStreamAdapter{
		domain.AuthModeChatGPTWebAccess: registered,
	})

	// Mode 1 — registered. The mapped Adapter must be entered through Stream().
	if _, err := registry.Stream(t.Context(), ports.ChatStreamCommand{AuthMode: domain.AuthModeChatGPTWebAccess}, nil, nil); err != nil {
		t.Fatalf("Stream() on the registered mode error = %v", err)
	}
	// Mode 2 — NOT registered. This is the interesting half: the lookup misses, and
	// the registry must still resolve to a STREAMING Adapter (the fallback's
	// Stream), never to any buffered path. A streaming request answered with a
	// non-streaming body would hand the client a complete completion where it
	// contracted for incremental deltas — chat lifecycle §3.2 rule 2 forbids that
	// substitution even when the buffered result would be "correct" content.
	if _, err := registry.Stream(t.Context(), ports.ChatStreamCommand{AuthMode: domain.AuthModeChatGPTCodexOAuth}, nil, nil); err != nil {
		t.Fatalf("Stream() on the unregistered mode error = %v", err)
	}

	if registered.streamCalls != 1 {
		t.Errorf("registered adapter Stream calls = %d, want 1", registered.streamCalls)
	}
	if fallback.streamCalls != 1 {
		t.Errorf("streaming fallback Stream calls = %d, want 1 (a missing mode must resolve to the streaming fallback)", fallback.streamCalls)
	}
	// The core rule. If either counter is non-zero the registry degraded a
	// streaming dispatch onto the non-streaming surface of the very same Adapter.
	if registered.runCalls != 0 {
		t.Errorf("registered adapter Run calls = %d, want 0; a streaming dispatch degraded to the non-streaming path", registered.runCalls)
	}
	if fallback.runCalls != 0 {
		t.Errorf("fallback Run calls = %d, want 0; a missing stream Adapter degraded to the non-streaming path", fallback.runCalls)
	}
	// Dispatch must not smear one mode's traffic onto the other Adapter: the
	// registered Adapter may only ever see the mode it was registered for.
	if len(registered.modes) != 1 || registered.modes[0] != domain.AuthModeChatGPTWebAccess {
		t.Errorf("registered adapter saw %v, want only chatgpt_web_access", registered.modes)
	}
	if len(fallback.modes) != 1 || fallback.modes[0] != domain.AuthModeChatGPTCodexOAuth {
		t.Errorf("fallback saw %v, want only chatgpt_codex_oauth", fallback.modes)
	}
}

func TestRegistryIgnoresLaterMutationOfTheSourceMap(t *testing.T) {
	t.Parallel()

	fallback := &countingChatAdapter{}
	source := map[domain.AuthMode]ports.ChatAdapter{}
	registry := adapters.NewChatAdapterRegistry(fallback, source)

	// Adding an Auth Mode to the caller's map after composition must not
	// retroactively register it: registration is a composition-time decision the
	// risk gate already made (§7 rule 1).
	sneaked := &countingChatAdapter{}
	source[domain.AuthModeChatGPTWebAccess] = sneaked

	if _, err := registry.Run(t.Context(), ports.ChatCommand{AuthMode: domain.AuthModeChatGPTWebAccess}, nil); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if sneaked.calls != 0 {
		t.Errorf("late-added adapter received %d calls, want 0", sneaked.calls)
	}
	if fallback.calls != 1 {
		t.Errorf("fallback calls = %d, want 1", fallback.calls)
	}
}

func TestProbeAndCapabilityRegistriesDispatchOnAuthMode(t *testing.T) {
	t.Parallel()

	probe := &countingProbeAdapter{}
	probeFallback := &countingProbeAdapter{}
	probeRegistry := adapters.NewProbeAdapterRegistry(probeFallback, map[domain.AuthMode]ports.ProbeAdapter{
		domain.AuthModeChatGPTWebAccess: probe,
	})
	if _, err := probeRegistry.Probe(t.Context(), ports.ProbeCommand{AuthMode: domain.AuthModeChatGPTWebAccess}); err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if _, err := probeRegistry.Probe(t.Context(), ports.ProbeCommand{AuthMode: domain.AuthModeGeminiWebCookie}); err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if probe.calls != 1 || probeFallback.calls != 1 {
		t.Errorf("probe dispatch = (%d, %d), want (1, 1)", probe.calls, probeFallback.calls)
	}

	capability := &countingCapabilityAdapter{}
	capabilityFallback := &countingCapabilityAdapter{}
	capabilityRegistry := adapters.NewCapabilityAdapterRegistry(capabilityFallback, map[domain.AuthMode]ports.CapabilityAdapter{
		domain.AuthModeChatGPTWebAccess: capability,
	})
	if _, err := capabilityRegistry.Observe(t.Context(), ports.CapabilityObservationCommand{AuthMode: domain.AuthModeChatGPTWebAccess}); err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if _, err := capabilityRegistry.Observe(t.Context(), ports.CapabilityObservationCommand{AuthMode: domain.AuthModeGeminiWebCookie}); err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if capability.calls != 1 || capabilityFallback.calls != 1 {
		t.Errorf("capability dispatch = (%d, %d), want (1, 1)", capability.calls, capabilityFallback.calls)
	}

	stream := &countingStreamAdapter{}
	streamFallback := &countingStreamAdapter{}
	streamRegistry := adapters.NewChatStreamAdapterRegistry(streamFallback, map[domain.AuthMode]ports.ChatStreamAdapter{
		domain.AuthModeChatGPTWebAccess: stream,
	})
	if _, err := streamRegistry.Stream(t.Context(), ports.ChatStreamCommand{AuthMode: domain.AuthModeChatGPTWebAccess}, nil, nil); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if _, err := streamRegistry.Stream(t.Context(), ports.ChatStreamCommand{AuthMode: domain.AuthModeGeminiWebCookie}, nil, nil); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if stream.calls != 1 || streamFallback.calls != 1 {
		t.Errorf("stream dispatch = (%d, %d), want (1, 1)", stream.calls, streamFallback.calls)
	}
}
