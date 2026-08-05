package chatgptweb

import (
	"encoding/json"
	"strings"
)

// streamEventKind classifies one decoded SSE payload.
type streamEventKind int

const (
	// eventIgnored is a payload that carries no canonical meaning: the `"v1"`
	// protocol marker, message markers, title generation, and server metadata.
	// Evidence: upstream-sse-conversation.md "marker 和 title 事件" / "metadata 事件".
	eventIgnored streamEventKind = iota
	// eventDelta carries assistant content to append.
	eventDelta
	// eventFinished marks the turn complete (message.status finished_successfully
	// or message.end_turn true).
	eventFinished
	// eventBlocked marks a moderation block for this turn.
	eventBlocked
	// eventImage carries a confirmed image-tool output pointer.
	eventImage
	// eventDone is the `[DONE]` terminator.
	eventDone
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
	// conversationID is carried when the payload disclosed one.
	conversationID string
}

// decodeStreamPayload translates one raw SSE `data:` payload into a canonical
// event. It is the single place ChatGPT-specific JSON-patch SSE becomes
// Provider-independent, so every protocol assumption in this package is
// readable in one function.
//
// Translation table (upstream-sse-conversation.md):
//
//	"v1"                                          -> ignored protocol marker
//	[DONE]                                        -> end of stream
//	{"p":"/message/content/parts/0","o":"append"} -> content delta
//	{"v":"..."} with a bare string                -> path-elided content delta
//	{"o":"patch","v":[...]}                       -> each element applied in order
//	/message/status = finished_successfully       -> turn finished
//	{"type":"moderation",...blocked:true}         -> turn blocked
//	tool message + async_task_type=image_gen      -> image output pointer
//	anything unparseable                          -> drift
func decodeStreamPayload(payload string) []streamEvent {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return nil
	}
	if trimmed == "[DONE]" {
		return []streamEvent{{kind: eventDone}}
	}
	// The bare `"v1"` protocol version marker arrives as a quoted JSON string.
	if trimmed == `"v1"` {
		return []streamEvent{{kind: eventIgnored}}
	}

	var raw map[string]any
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		// A quoted JSON string that is not the version marker is a short text
		// patch (evidence: "JSON string | 短文本 patch 或协议标记").
		var text string
		if json.Unmarshal([]byte(trimmed), &text) == nil {
			return []streamEvent{{kind: eventDelta, text: text}}
		}
		return []streamEvent{{kind: eventDrift}}
	}
	return decodeObject(raw)
}

// decodeObject translates one decoded JSON object. It is separate from
// decodeStreamPayload so a batch patch can recurse into its elements without
// re-serializing them.
func decodeObject(raw map[string]any) []streamEvent {
	conversationID, _ := raw["conversation_id"].(string)

	// Typed events first: they are self-describing and never patches.
	if eventType, ok := raw["type"].(string); ok {
		switch eventType {
		case "moderation":
			if moderationBlocked(raw) {
				return []streamEvent{{kind: eventBlocked, conversationID: conversationID}}
			}
			return []streamEvent{{kind: eventIgnored, conversationID: conversationID}}
		case "resume_conversation_token", "message_marker", "title_generation",
			"server_ste_metadata", "input_message":
			// Non-content events. The resume token in particular MUST NOT be
			// exposed downstream (evidence: "token 不应该暴露给下游用户").
			return []streamEvent{{kind: eventIgnored, conversationID: conversationID}}
		default:
			// A typed event this Adapter has never seen is drift, NOT something to
			// ignore. The Provider added a self-describing event we cannot
			// interpret, which is precisely the KS-5-relevant observation (evidence
			// §7). Ignoring it would let a moved protocol return an empty
			// generation that still classified as committed.
			return []streamEvent{{kind: eventDrift, conversationID: conversationID}}
		}
	}

	operation, _ := raw["o"].(string)
	path, _ := raw["p"].(string)

	// Batch patch: apply every element in array order (evidence: "o == patch 且 v
	// 是数组 | 批量 patch，需要按数组顺序处理").
	if operation == "patch" {
		elements, ok := raw["v"].([]any)
		if !ok {
			return []streamEvent{{kind: eventDrift}}
		}
		var events []streamEvent
		for _, element := range elements {
			object, ok := element.(map[string]any)
			if !ok {
				events = append(events, streamEvent{kind: eventDrift})
				continue
			}
			events = append(events, decodeObject(object)...)
		}
		return events
	}

	// Terminal status / end-of-turn replacements.
	if operation == "replace" {
		switch path {
		case "/message/status":
			if status, _ := raw["v"].(string); status == "finished_successfully" {
				return []streamEvent{{kind: eventFinished, conversationID: conversationID}}
			}
			return []streamEvent{{kind: eventIgnored, conversationID: conversationID}}
		case "/message/end_turn":
			if ended, _ := raw["v"].(bool); ended {
				return []streamEvent{{kind: eventFinished, conversationID: conversationID}}
			}
			return []streamEvent{{kind: eventIgnored, conversationID: conversationID}}
		}
	}

	// Content append on the canonical text part.
	if path == "/message/content/parts/0" && (operation == "append" || operation == "") {
		if text, ok := raw["v"].(string); ok {
			return []streamEvent{{kind: eventDelta, text: text, conversationID: conversationID}}
		}
		return []streamEvent{{kind: eventDrift}}
	}

	// A full message value: either an image-tool output or a message shell.
	if message, ok := messageFrom(raw["v"]); ok {
		if pointer, ok := imageOutputPointer(message); ok {
			return []streamEvent{{kind: eventImage, pointer: pointer, conversationID: conversationID}}
		}
		if messageFinished(message) {
			return []streamEvent{{kind: eventFinished, conversationID: conversationID}}
		}
		return []streamEvent{{kind: eventIgnored, conversationID: conversationID}}
	}

	// Path-elided text delta: only `v` present and it is a string (evidence: "只有
	// v 且 v 是字符串 | 可能是省略路径的文本增量").
	if text, ok := raw["v"].(string); ok && path == "" {
		return []streamEvent{{kind: eventDelta, text: text, conversationID: conversationID}}
	}

	if conversationID != "" {
		return []streamEvent{{kind: eventIgnored, conversationID: conversationID}}
	}
	return []streamEvent{{kind: eventDrift}}
}

// moderationBlocked reports a moderation event that blocked this turn.
func moderationBlocked(raw map[string]any) bool {
	response, ok := raw["moderation_response"].(map[string]any)
	if !ok {
		return false
	}
	blocked, _ := response["blocked"].(bool)
	return blocked
}

// messageFrom extracts the `message` object from a patch value, accepting both
// `{"v":{"message":{...}}}` and a bare `{"v":{...message fields...}}`.
func messageFrom(value any) (map[string]any, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	if message, ok := object["message"].(map[string]any); ok {
		return message, true
	}
	if _, hasAuthor := object["author"]; hasAuthor {
		return object, true
	}
	return nil, false
}

// messageFinished reports a message whose own status ends the turn.
func messageFinished(message map[string]any) bool {
	if ended, _ := message["end_turn"].(bool); ended {
		return true
	}
	status, _ := message["status"].(string)
	return status == "finished_successfully"
}

// imageOutputPointer returns the asset pointer of a confirmed image-tool output.
//
// All three conditions are required (evidence: "只有同时满足以下条件的图片指针，才应该
// 视为输出结果"): the message role is `tool`, the metadata async_task_type is
// `image_gen`, and the pointer scheme is resolvable. A `sediment://` pointer on
// a user message is an INPUT attachment and must never be returned as output —
// the evidence is explicit that string matching alone is not sufficient ("不要只凭
// 字符串里出现 file_ 或 sediment:// 就判定为输出图").
func imageOutputPointer(message map[string]any) (string, bool) {
	author, _ := message["author"].(map[string]any)
	if role, _ := author["role"].(string); role != "tool" {
		return "", false
	}
	metadata, _ := message["metadata"].(map[string]any)
	if taskType, _ := metadata["async_task_type"].(string); taskType != "image_gen" {
		return "", false
	}
	content, _ := message["content"].(map[string]any)
	parts, _ := content["parts"].([]any)
	for _, part := range parts {
		object, ok := part.(map[string]any)
		if !ok {
			continue
		}
		pointer, _ := object["asset_pointer"].(string)
		if strings.HasPrefix(pointer, "file-service://") || strings.HasPrefix(pointer, "sediment://") {
			return pointer, true
		}
	}
	return "", false
}
