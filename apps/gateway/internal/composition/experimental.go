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
func (enabled experimentalAdapters) none() bool {
	return enabled.chatGPTWeb == nil
}

// chatAdapter returns fallback unchanged in production, or a registry that
// dispatches the enabled experimental modes over it.
func (enabled experimentalAdapters) chatAdapter(fallback ports.ChatAdapter) ports.ChatAdapter {
	if enabled.none() {
		return fallback
	}
	byMode := map[domain.AuthMode]ports.ChatAdapter{}
	if enabled.chatGPTWeb != nil {
		byMode[domain.AuthModeChatGPTWebAccess] = enabled.chatGPTWeb
	}
	return adapters.NewChatAdapterRegistry(fallback, byMode)
}

// chatStreamAdapter mirrors chatAdapter for the streaming surface.
func (enabled experimentalAdapters) chatStreamAdapter(fallback ports.ChatStreamAdapter) ports.ChatStreamAdapter {
	if enabled.none() {
		return fallback
	}
	byMode := map[domain.AuthMode]ports.ChatStreamAdapter{}
	if enabled.chatGPTWeb != nil {
		byMode[domain.AuthModeChatGPTWebAccess] = enabled.chatGPTWeb
	}
	return adapters.NewChatStreamAdapterRegistry(fallback, byMode)
}

// probeAdapter mirrors chatAdapter for credential probes.
func (enabled experimentalAdapters) probeAdapter(fallback ports.ProbeAdapter) ports.ProbeAdapter {
	if enabled.none() {
		return fallback
	}
	byMode := map[domain.AuthMode]ports.ProbeAdapter{}
	if enabled.chatGPTWeb != nil {
		byMode[domain.AuthModeChatGPTWebAccess] = enabled.chatGPTWeb
	}
	return adapters.NewProbeAdapterRegistry(fallback, byMode)
}

// capabilityAdapter mirrors chatAdapter for capability observation.
func (enabled experimentalAdapters) capabilityAdapter(fallback ports.CapabilityAdapter) ports.CapabilityAdapter {
	if enabled.none() {
		return fallback
	}
	byMode := map[domain.AuthMode]ports.CapabilityAdapter{}
	if enabled.chatGPTWeb != nil {
		byMode[domain.AuthModeChatGPTWebAccess] = enabled.chatGPTWeb
	}
	return adapters.NewCapabilityAdapterRegistry(fallback, byMode)
}
