package composition

import (
	"github.com/monet88/pixelplus/apps/gateway/internal/adapters"
	"github.com/monet88/pixelplus/apps/gateway/internal/adapters/chatgptweb"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// experimentalAdapters is the set of Adapters this deployment's lab profile
// deliberately enabled. Every field is nil in ordinary production because the
// zero LabProfile enables nothing, so the registries below are never built and
// the experimental Auth Mode is absent from the composed object graph rather
// than merely rejected at runtime (risk envelope §6.1, §7 rule 1).
type experimentalAdapters struct {
	chatGPTWeb *chatgptweb.Adapter
}

// newExperimentalAdapters constructs the Adapter for each experimental Auth Mode
// the lab profile named. Construction is the registration decision: the
// composition root consults risk status through domain.LabProfile before it
// builds anything, which is what §7 rule 1 requires.
func newExperimentalAdapters(config Config, dependencies Dependencies) experimentalAdapters {
	profile := config.labProfile()

	var enabled experimentalAdapters
	if profile.AllowsExperimental(domain.AuthModeChatGPTWebAccess) {
		// A nil transport still yields a registered Adapter that fails closed on
		// every surface. Enabling the mode is not the same as granting egress:
		// an operator must deliberately supply transport as well.
		enabled.chatGPTWeb = chatgptweb.New(dependencies.ExperimentalChatGPTWebTransport)
	}
	return enabled
}

// none reports whether the lab profile enabled no Adapter at all, which is the
// ordinary production state.
//
// There is deliberately no renderAdapter helper beside the four below. This
// story's Adapter implements chat, stream, probe, and capability only, and the
// canonical chat surface has no carrier for an image asset. A ChatGPT Web image
// request is therefore refused on the render surface itself: renderCandidate
// keeps the mode out of the candidate set in EVERY composition, lab included,
// because no registry here can serve it. If a later story gives an experimental
// Adapter a real ports.RenderAdapter, it must add the matching renderAdapter
// helper here AND relax that render gate together; nothing else forces that, so
// the omission is recorded rather than implied.
func (enabled experimentalAdapters) none() bool {
	return enabled.chatGPTWeb == nil
}

// byMode collects the enabled Adapters keyed by Auth Mode, narrowed to whatever
// port the caller asked for.
//
// The four helpers below differ only in their port type, so the shape lives here
// once: an Adapter is registered for a mode when the profile enabled it and the
// concrete Adapter satisfies that port. The `ok` check is not ceremony — it is
// what makes an Adapter that does not implement a port absent from that port's
// registry rather than a nil entry that would panic on dispatch.
func experimentalByMode[T any](enabled experimentalAdapters) map[domain.AuthMode]T {
	byMode := map[domain.AuthMode]T{}
	if enabled.chatGPTWeb != nil {
		if port, ok := any(enabled.chatGPTWeb).(T); ok {
			byMode[domain.AuthModeChatGPTWebAccess] = port
		}
	}
	return byMode
}

// chatAdapter returns fallback unchanged in production, or a registry that
// dispatches the enabled experimental modes over it.
func (enabled experimentalAdapters) chatAdapter(fallback ports.ChatAdapter) ports.ChatAdapter {
	if enabled.none() {
		return fallback
	}
	return adapters.NewChatAdapterRegistry(fallback, experimentalByMode[ports.ChatAdapter](enabled))
}

// chatStreamAdapter mirrors chatAdapter for the streaming surface.
func (enabled experimentalAdapters) chatStreamAdapter(fallback ports.ChatStreamAdapter) ports.ChatStreamAdapter {
	if enabled.none() {
		return fallback
	}
	return adapters.NewChatStreamAdapterRegistry(fallback, experimentalByMode[ports.ChatStreamAdapter](enabled))
}

// probeAdapter mirrors chatAdapter for credential probes.
func (enabled experimentalAdapters) probeAdapter(fallback ports.ProbeAdapter) ports.ProbeAdapter {
	if enabled.none() {
		return fallback
	}
	return adapters.NewProbeAdapterRegistry(fallback, experimentalByMode[ports.ProbeAdapter](enabled))
}

// capabilityAdapter mirrors chatAdapter for capability observation.
func (enabled experimentalAdapters) capabilityAdapter(fallback ports.CapabilityAdapter) ports.CapabilityAdapter {
	if enabled.none() {
		return fallback
	}
	return adapters.NewCapabilityAdapterRegistry(fallback, experimentalByMode[ports.CapabilityAdapter](enabled))
}
