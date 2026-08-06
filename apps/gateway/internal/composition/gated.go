package composition

import (
	"github.com/monet88/pixelplus/apps/gateway/internal/adapters"
	"github.com/monet88/pixelplus/apps/gateway/internal/adapters/chatgptcodex"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// gatedAdapters is the set of Adapters this deployment's gated profile
// deliberately enabled. Every field is nil in a deployment that did not opt in,
// because the zero GatedProfile enables nothing, so the registries below are
// never built and a gated Auth Mode is absent from the composed object graph
// rather than merely rejected at runtime (risk envelope §5.2, §6.1, §7 rule 1;
// decision 0014).
//
// It is the gated twin of experimentalAdapters. The two are separate so an
// operator cannot satisfy a gated mode's feature-flag gate by naming it in the
// experimental lab profile (which would skip the Tenant acknowledgement).
type gatedAdapters struct {
	chatGPTCodex *chatgptcodex.Adapter
}

// newGatedAdapters constructs the Adapter for each gated Auth Mode the profile
// named. Construction is the registration decision: the composition root
// consults risk status through domain.GatedProfile before it builds anything,
// which is what §7 rule 1 requires.
func newGatedAdapters(config Config, dependencies Dependencies) gatedAdapters {
	profile := config.gatedProfile()
	transport := gatedCodexTransportFrom(dependencies)

	var enabled gatedAdapters
	if profile.AllowsGated(domain.AuthModeChatGPTCodexOAuth) && transport != nil {
		// The Adapter is registered only when a transport is actually supplied.
		// Enabling the mode is not the same as granting egress: an operator must
		// deliberately supply transport too. With the profile on but no transport,
		// no gated registry is built, so the mode dispatches to the fail-closed
		// fallback — every surface stays closed without a Provider client
		// (decision 0014; the nil-transport fail-closed posture is proved by the
		// chatgptcodex.TestNilTransportFailsClosed package test).
		enabled.chatGPTCodex = chatgptcodex.New(transport)
	}
	return enabled
}

// none reports whether the gated profile enabled no Adapter at all, which is the
// state of a deployment that did not opt into any gated mode.
//
// There is deliberately no renderAdapter helper beside the four below, for the
// same reason 0013 recorded for experimental: this story's gated Adapter
// implements chat, stream, probe, and capability only, and the render candidate
// gate refuses a gated mode in EVERY composition so an enabled deployment never
// accepts a render job it cannot serve. A later story that gives a gated
// Adapter a real ports.RenderAdapter must add the matching renderAdapter helper
// here AND relax that render gate together; nothing else forces that, so the
// omission is recorded rather than implied.
func (enabled gatedAdapters) none() bool {
	return enabled.chatGPTCodex == nil
}

// gatedByMode collects the enabled Adapters keyed by Auth Mode, narrowed to
// whatever port the caller asked for. It mirrors experimentalByMode: an Adapter
// is registered for a mode when the profile enabled it and the concrete Adapter
// satisfies that port.
func gatedByMode[T any](enabled gatedAdapters) map[domain.AuthMode]T {
	byMode := map[domain.AuthMode]T{}
	if enabled.chatGPTCodex != nil {
		if port, ok := any(enabled.chatGPTCodex).(T); ok {
			byMode[domain.AuthModeChatGPTCodexOAuth] = port
		}
	}
	return byMode
}

// chatAdapter returns fallback unchanged when no gated mode is enabled, or a
// registry that dispatches the enabled gated modes over it. It is applied AFTER
// the experimental wrapper so a gated registry's fallback is the experimental
// registry (or the foundation), which keeps every Auth Mode on one dispatch
// chain.
func (enabled gatedAdapters) chatAdapter(fallback ports.ChatAdapter) ports.ChatAdapter {
	if enabled.none() {
		return fallback
	}
	return adapters.NewChatAdapterRegistry(fallback, gatedByMode[ports.ChatAdapter](enabled))
}

// chatStreamAdapter mirrors chatAdapter for the streaming surface.
func (enabled gatedAdapters) chatStreamAdapter(fallback ports.ChatStreamAdapter) ports.ChatStreamAdapter {
	if enabled.none() {
		return fallback
	}
	return adapters.NewChatStreamAdapterRegistry(fallback, gatedByMode[ports.ChatStreamAdapter](enabled))
}

// probeAdapter mirrors chatAdapter for credential probes.
func (enabled gatedAdapters) probeAdapter(fallback ports.ProbeAdapter) ports.ProbeAdapter {
	if enabled.none() {
		return fallback
	}
	return adapters.NewProbeAdapterRegistry(fallback, gatedByMode[ports.ProbeAdapter](enabled))
}

// capabilityAdapter mirrors chatAdapter for capability observation.
func (enabled gatedAdapters) capabilityAdapter(fallback ports.CapabilityAdapter) ports.CapabilityAdapter {
	if enabled.none() {
		return fallback
	}
	return adapters.NewCapabilityAdapterRegistry(fallback, gatedByMode[ports.CapabilityAdapter](enabled))
}
