package chatgptcodex

import (
	"encoding/json"
	"strings"
)

// streamEventKind classifies one decoded Codex Responses SSE payload.
type streamEventKind int

const (
	// eventIgnored is a payload that carries no canonical meaning: protocol
	// markers, metadata, and response lifecycle events that do not produce
	// content.
	eventIgnored streamEventKind = iota
	// eventDelta carries assistant content to append (response.output_text.delta).
	eventDelta
	// eventFinished marks the turn complete (response.completed).
	eventFinished
	// eventBlocked marks a moderation / content-filter block for this turn.
	eventBlocked
	// eventImage carries a confirmed image_generation tool output pointer.
	eventImage
	// eventDone is the `[DONE]` terminator.
	eventDone
	// eventQuota is an in-stream usage_limit_reached error.
	eventQuota
	// eventRateLimited is an in-stream rate_limit_error.
	eventRateLimited
	// eventDrift is a payload this Adapter cannot parse. It is surfaced rather
	// than swallowed so protocol drift is observable (evidence §7 protocol drift
	// risks) instead of silently becoming an empty generation.
	eventDrift
)

// streamEvent is the canonical form of one decoded payload.
type streamEvent struct {
	kind streamEventKind
	// text is the content fragment for eventDelta.
	text string
	// pointer is the asset pointer for eventImage.
	pointer string
	// There is deliberately no reset-hint field for eventQuota. The numeric
	// retry-after duration is owned by #17, not by a chat outcome: the health
	// spec §17.4 computes the client-visible value from retry_not_before rather
	// than from the raw Provider number, and §17.8 forbids forwarding that number
	// directly. See the eventQuota case in consumeStream for the full reasoning
	// and the correct path (a CooldownObservation into the health store, as the
	// probe surface does via parseUsageLimit →
	// ports.ProbeOutcome.RetryAfterSeconds).
}

// decodeStreamPayload translates one raw SSE `data:` payload into a canonical
// event. It is the single place Codex-Responses SSE becomes
// Provider-independent, so every protocol assumption in this package is
// readable in one function.
//
// Translation table (CLIProxyAPI codex_executor.go Responses event handling):
//
//	[DONE]                                         -> end of stream
//	{"type":"response.output_text.delta","delta":..}-> content delta
//	{"type":"response.completed",...}              -> turn finished
//	{"type":"response.image_generation.completed",.}-> image output pointer
//	{"type":"response.content_filter","blocked":true} -> moderation block
//	{"type":"error","error":{"type":"usage_limit_reached",...}} -> quota
//	{"type":"error","error":{"type":"rate_limit_error"}} -> rate limited
//	anything unparseable                           -> drift
func decodeStreamPayload(payload string) []streamEvent {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return []streamEvent{{kind: eventIgnored}}
	}
	if trimmed == "[DONE]" {
		return []streamEvent{{kind: eventDone}}
	}

	// A Responses stream payload is one JSON object whose type names the event.
	var typed struct {
		Type   string          `json:"type"`
		Delta  string          `json:"delta"`
		Error  json.RawMessage `json:"error"`
		Image  string          `json:"image"`
		URL    string          `json:"url"`
		Output json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal([]byte(trimmed), &typed); err != nil {
		return []streamEvent{{kind: eventDrift}}
	}

	switch typed.Type {
	case "response.output_text.delta":
		return []streamEvent{{kind: eventDelta, text: typed.Delta}}
	case "response.completed", "response.done":
		return []streamEvent{{kind: eventFinished}}
	case "response.image_generation.completed", "response.image_generation.done":
		pointer := typed.Image
		if pointer == "" {
			pointer = typed.URL
		}
		if pointer == "" {
			// The image asset may be nested in an output item. If it is not a
			// simple field, the pointer is undecodable here and the turn is
			// drift rather than a fabricated empty image.
			return []streamEvent{{kind: eventDrift}}
		}
		return []streamEvent{{kind: eventImage, pointer: pointer}}
	case "response.content_filter", "response.moderation":
		var blocked struct {
			Blocked bool `json:"blocked"`
		}
		_ = json.Unmarshal(typed.Output, &blocked)
		if !blocked.Blocked {
			return []streamEvent{{kind: eventIgnored}}
		}
		return []streamEvent{{kind: eventBlocked}}
	case "error":
		return decodeResponsesError(typed.Error)
	default:
		// A recognized Responses lifecycle event that carries no content
		// (response.created, response.in_progress, response.output_item.added,
		// etc.) is ignored. An unrecognized type is drift only when it is not
		// one of the known lifecycle prefixes.
		if isResponsesLifecycleEvent(typed.Type) {
			return []streamEvent{{kind: eventIgnored}}
		}
		return []streamEvent{{kind: eventDrift}}
	}
}

// decodeResponsesError maps a Responses error body onto a quota / rate / drift
// event. Auth-class errors do not surface as in-stream events on a stream that
// already opened (the open exchange would have classified the 401 first), so an
// in-stream auth error is treated as drift rather than a second auth-failed
// path.
func decodeResponsesError(raw json.RawMessage) []streamEvent {
	if len(raw) == 0 {
		return []streamEvent{{kind: eventDrift}}
	}
	// Only the error TYPE is decoded. The upstream `message` is a Provider
	// string and must not travel further (OP-G3), and `resets_in_seconds` has no
	// carrier on the chat outcomes — see the streamEvent comment above.
	var payload struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return []streamEvent{{kind: eventDrift}}
	}
	switch payload.Type {
	case "usage_limit_reached":
		return []streamEvent{{kind: eventQuota}}
	case "rate_limit_error", "rate_limit_exceeded":
		return []streamEvent{{kind: eventRateLimited}}
	default:
		return []streamEvent{{kind: eventDrift}}
	}
}

// isResponsesLifecycleEvent reports whether a type is a known Responses
// lifecycle event that carries no canonical content. Ignoring these (rather
// than classifying them as drift) keeps a healthy stream from looking like
// protocol drift the moment the upstream adds a new lifecycle marker.
func isResponsesLifecycleEvent(eventType string) bool {
	switch eventType {
	case "response.created", "response.in_progress", "response.output_item.added",
		"response.output_item.done", "response.content_part.added",
		"response.content_part.done", "response.queued":
		return true
	default:
		return false
	}
}
