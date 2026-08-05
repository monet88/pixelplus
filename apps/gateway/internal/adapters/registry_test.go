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

	nonStreaming := &countingChatAdapter{}
	registry := adapters.NewChatStreamAdapterRegistry(nil, nil)

	// The streaming registry holds no reference to a non-streaming Adapter at
	// all, so a missing stream Adapter cannot be answered with a buffered body
	// (chat lifecycle §3.2 rule 2).
	if _, err := registry.Stream(t.Context(), ports.ChatStreamCommand{}, nil, nil); err == nil {
		t.Fatal("missing stream adapter succeeded")
	}
	if nonStreaming.calls != 0 {
		t.Errorf("non-streaming adapter called %d times, want 0", nonStreaming.calls)
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
