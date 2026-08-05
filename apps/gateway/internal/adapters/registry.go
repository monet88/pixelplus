package adapters

import (
	"context"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// The registries below dispatch one port call to the Adapter registered for the
// command's Auth Mode, falling back to a default when no Adapter claims it.
//
// Dispatch is on the Auth Mode already carried by every command struct, so no
// registry re-decides which account or Tenant serves a request: routing chose
// that before the Adapter boundary was reached (risk envelope §3.5.4 "Auth Mode
// is the Adapter registration unit").
//
// A nil registry, or one with no entry for the mode, uses the fallback. In
// production the fallback is the fail-closed foundation, so a mode without an
// Adapter cannot execute. Composition builds a registry only when an operator
// enabled a mode that has one, which is what keeps a non-enabled Auth Mode
// absent from the composed object graph rather than merely bypassed at runtime.

// ChatAdapterRegistry dispatches non-streaming chat by Auth Mode.
type ChatAdapterRegistry struct {
	fallback ports.ChatAdapter
	byMode   map[domain.AuthMode]ports.ChatAdapter
}

// NewChatAdapterRegistry builds a registry over a fail-closed fallback.
func NewChatAdapterRegistry(fallback ports.ChatAdapter, byMode map[domain.AuthMode]ports.ChatAdapter) *ChatAdapterRegistry {
	return &ChatAdapterRegistry{fallback: fallback, byMode: cloneChatAdapters(byMode)}
}

// Run dispatches to the Auth Mode's Adapter, or the fallback.
func (registry *ChatAdapterRegistry) Run(ctx context.Context, command ports.ChatCommand, credential ports.CredentialInjection) (domain.ChatOutcome, error) {
	if registry != nil {
		if adapter, ok := registry.byMode[command.AuthMode]; ok && adapter != nil {
			return adapter.Run(ctx, command, credential)
		}
		if registry.fallback != nil {
			return registry.fallback.Run(ctx, command, credential)
		}
	}
	return domain.ChatOutcome{}, ports.ErrChatAdapterUnavailable
}

// ChatStreamAdapterRegistry dispatches streaming chat by Auth Mode.
type ChatStreamAdapterRegistry struct {
	fallback ports.ChatStreamAdapter
	byMode   map[domain.AuthMode]ports.ChatStreamAdapter
}

// NewChatStreamAdapterRegistry builds a registry over a fail-closed fallback.
func NewChatStreamAdapterRegistry(fallback ports.ChatStreamAdapter, byMode map[domain.AuthMode]ports.ChatStreamAdapter) *ChatStreamAdapterRegistry {
	return &ChatStreamAdapterRegistry{fallback: fallback, byMode: cloneChatStreamAdapters(byMode)}
}

// Stream dispatches to the Auth Mode's Adapter, or the fallback. A missing
// Adapter never degrades to the non-streaming path: the caller receives the
// streaming port's own unavailable error so a streaming request is never
// answered with a non-streaming body (chat lifecycle §3.2 rule 2).
func (registry *ChatStreamAdapterRegistry) Stream(
	ctx context.Context,
	command ports.ChatStreamCommand,
	credential ports.CredentialInjection,
	sink domain.ChatSink,
) (domain.ChatStreamOutcome, error) {
	if registry != nil {
		if adapter, ok := registry.byMode[command.AuthMode]; ok && adapter != nil {
			return adapter.Stream(ctx, command, credential, sink)
		}
		if registry.fallback != nil {
			return registry.fallback.Stream(ctx, command, credential, sink)
		}
	}
	return domain.ChatStreamOutcome{}, ports.ErrChatStreamAdapterUnavailable
}

// ProbeAdapterRegistry dispatches credential probes by Auth Mode.
type ProbeAdapterRegistry struct {
	fallback ports.ProbeAdapter
	byMode   map[domain.AuthMode]ports.ProbeAdapter
}

// NewProbeAdapterRegistry builds a registry over a fail-closed fallback.
func NewProbeAdapterRegistry(fallback ports.ProbeAdapter, byMode map[domain.AuthMode]ports.ProbeAdapter) *ProbeAdapterRegistry {
	return &ProbeAdapterRegistry{fallback: fallback, byMode: cloneProbeAdapters(byMode)}
}

// Probe dispatches to the Auth Mode's Adapter, or the fallback.
func (registry *ProbeAdapterRegistry) Probe(ctx context.Context, command ports.ProbeCommand) (ports.ProbeOutcome, error) {
	if registry != nil {
		if adapter, ok := registry.byMode[command.AuthMode]; ok && adapter != nil {
			return adapter.Probe(ctx, command)
		}
		if registry.fallback != nil {
			return registry.fallback.Probe(ctx, command)
		}
	}
	return ports.ProbeOutcome{}, ports.ErrDependencyUnavailable
}

// CapabilityAdapterRegistry dispatches capability observation by Auth Mode.
type CapabilityAdapterRegistry struct {
	fallback ports.CapabilityAdapter
	byMode   map[domain.AuthMode]ports.CapabilityAdapter
}

// NewCapabilityAdapterRegistry builds a registry over a fail-closed fallback.
func NewCapabilityAdapterRegistry(fallback ports.CapabilityAdapter, byMode map[domain.AuthMode]ports.CapabilityAdapter) *CapabilityAdapterRegistry {
	return &CapabilityAdapterRegistry{fallback: fallback, byMode: cloneCapabilityAdapters(byMode)}
}

// Observe dispatches to the Auth Mode's Adapter, or the fallback.
func (registry *CapabilityAdapterRegistry) Observe(ctx context.Context, command ports.CapabilityObservationCommand) (ports.CapabilityObservation, error) {
	if registry != nil {
		if adapter, ok := registry.byMode[command.AuthMode]; ok && adapter != nil {
			return adapter.Observe(ctx, command)
		}
		if registry.fallback != nil {
			return registry.fallback.Observe(ctx, command)
		}
	}
	return ports.CapabilityObservation{}, ports.ErrDependencyUnavailable
}

// The clone helpers copy the caller's map so a later mutation of the argument
// cannot silently add or remove an Auth Mode from a live registry.

func cloneChatAdapters(source map[domain.AuthMode]ports.ChatAdapter) map[domain.AuthMode]ports.ChatAdapter {
	if len(source) == 0 {
		return nil
	}
	cloned := make(map[domain.AuthMode]ports.ChatAdapter, len(source))
	for mode, adapter := range source {
		cloned[mode] = adapter
	}
	return cloned
}

func cloneChatStreamAdapters(source map[domain.AuthMode]ports.ChatStreamAdapter) map[domain.AuthMode]ports.ChatStreamAdapter {
	if len(source) == 0 {
		return nil
	}
	cloned := make(map[domain.AuthMode]ports.ChatStreamAdapter, len(source))
	for mode, adapter := range source {
		cloned[mode] = adapter
	}
	return cloned
}

func cloneProbeAdapters(source map[domain.AuthMode]ports.ProbeAdapter) map[domain.AuthMode]ports.ProbeAdapter {
	if len(source) == 0 {
		return nil
	}
	cloned := make(map[domain.AuthMode]ports.ProbeAdapter, len(source))
	for mode, adapter := range source {
		cloned[mode] = adapter
	}
	return cloned
}

func cloneCapabilityAdapters(source map[domain.AuthMode]ports.CapabilityAdapter) map[domain.AuthMode]ports.CapabilityAdapter {
	if len(source) == 0 {
		return nil
	}
	cloned := make(map[domain.AuthMode]ports.CapabilityAdapter, len(source))
	for mode, adapter := range source {
		cloned[mode] = adapter
	}
	return cloned
}

var (
	_ ports.ChatAdapter       = (*ChatAdapterRegistry)(nil)
	_ ports.ChatStreamAdapter = (*ChatStreamAdapterRegistry)(nil)
	_ ports.ProbeAdapter      = (*ProbeAdapterRegistry)(nil)
	_ ports.CapabilityAdapter = (*CapabilityAdapterRegistry)(nil)
)
