package domain

// ChatOperation names the immutable chat operation bound to one request. The
// streaming and non-streaming operations are distinct values — not one value
// plus a flag — and both are distinct from image operations, so scope,
// admission, telemetry, and capability checks classify the right work.
type ChatOperation string

// Frozen chat operation vocabulary (OpenAPI / chat lifecycle #12 §3.2).
const (
	// ChatOpCompletion is the single-turn non-streaming chat completion operation.
	ChatOpCompletion ChatOperation = "chat_completion"
	// ChatOpCompletionStreaming is the streaming chat completion operation. It is
	// a separate operation rather than a modifier on ChatOpCompletion so an
	// account can honestly offer non-streaming chat while streaming stays
	// unsupported/synthetic (capability spec §3.1, chat lifecycle §3.2).
	ChatOpCompletionStreaming ChatOperation = "chat_completion_streaming"
)

// Valid reports whether the operation is in the frozen chat vocabulary.
func (operation ChatOperation) Valid() bool {
	switch operation {
	case ChatOpCompletion, ChatOpCompletionStreaming:
		return true
	default:
		return false
	}
}

// CapabilityOperation maps the chat operation onto the capability vocabulary.
// Streaming resolves `chat_streaming`, never `chat`: a snapshot that verifies
// non-streaming chat alone can therefore never satisfy a streaming request, so
// the Gateway cannot silently downgrade a stream to a non-streaming answer
// (chat lifecycle §3.2 rule 2, I-CHAT-STREAM-CLASS-HONEST).
func (operation ChatOperation) CapabilityOperation() CapabilityOperation {
	switch operation {
	case ChatOpCompletion:
		return CapabilityOpChat
	case ChatOpCompletionStreaming:
		return CapabilityOpChatStreaming
	default:
		return ""
	}
}

// RequiredScope returns the Client API Key scope required to run this operation.
func (operation ChatOperation) RequiredScope() Scope {
	switch operation {
	case ChatOpCompletion, ChatOpCompletionStreaming:
		return ScopeChatCompletions
	default:
		return ""
	}
}

// FinishClass is the canonical terminal classification of a non-streaming
// completion (chat lifecycle §4.2). Every non-streaming success carries exactly
// one; Provider end-marker/termination framing is normalized into it.
type FinishClass string

// Frozen finish classes (chat lifecycle §4.2).
const (
	FinishStop          FinishClass = "stop"           // natural completion
	FinishLength        FinishClass = "length"         // output bound reached
	FinishContentFilter FinishClass = "content_filter" // upstream refuse/filter
	FinishCanceled      FinishClass = "canceled"       // request canceled
	FinishFailed        FinishClass = "failed"         // execution failed
)

// Valid reports whether the finish class is in the frozen enum.
func (class FinishClass) Valid() bool {
	switch class {
	case FinishStop, FinishLength, FinishContentFilter, FinishCanceled, FinishFailed:
		return true
	default:
		return false
	}
}

// ChatRole is the canonical message role. It never reflects Provider-specific
// role tokens; the Adapter normalizes to this closed vocabulary.
type ChatRole string

// Frozen chat roles (OpenAI-compatible contract §3.3).
const (
	ChatRoleSystem    ChatRole = "system"
	ChatRoleUser      ChatRole = "user"
	ChatRoleAssistant ChatRole = "assistant"
)

// Valid reports whether the role is in the frozen enum.
func (role ChatRole) Valid() bool {
	switch role {
	case ChatRoleSystem, ChatRoleUser, ChatRoleAssistant:
		return true
	default:
		return false
	}
}

// ChatMessage is one canonical chat turn. Content is the non-secret canonical
// message text; it never carries credential material. Name is the optional
// contract-declared message label (ChatMessage schema): it is bound into the
// idempotency fingerprint but not consumed by the Adapter until real Provider
// Adapters land (T19–T23, decision 0012).
type ChatMessage struct {
	Role    ChatRole
	Content string
	Name    string
}

// ChatRequestOptions carries every accepted ChatCompletionRequest field beyond
// model and messages: the generation tuning fields (temperature, max_tokens,
// top_p, n, stop, user) and the x_pixelplus routing inputs
// (provider_account_id, allow_fallback, conversation_id). The tuning values
// are shape-validated at the transport boundary and bound into the idempotency
// fingerprint, so a same-key request differing in any accepted field conflicts
// instead of replaying (idempotency policy §5.2, canonical-errors §7.1); the
// Adapter does not consume them until real Provider Adapters land (T19–T23,
// decision 0012). A nil pointer / nil Stop marks an absent field, and the
// single-string stop form is canonicalized to a one-element list, so
// semantically equal requests fingerprint identically.
type ChatRequestOptions struct {
	Temperature       *float64
	MaxTokens         *int
	TopP              *float64
	N                 *int
	Stop              []string
	User              string
	ProviderAccountID ProviderAccountID
	AllowFallback     bool
	ConversationID    string
}

// Valid reports whether the message carries a known role and non-empty content.
func (message ChatMessage) Valid() bool {
	return message.Role.Valid() && message.Content != ""
}

// ChatUsage is the canonical token accounting projection. The Gateway never
// invents token precision; the Adapter reports these as observed.
type ChatUsage struct {
	PromptTokens     int
	CompletionTokens int
}

// ChatChoice is one canonical assistant choice in a completion.
type ChatChoice struct {
	Index       int
	Message     ChatMessage
	FinishClass FinishClass
}

// ChatCompletion is the canonical, Provider-independent non-streaming response
// (chat lifecycle §4.2, OpenAI-compatible contract §3.4/§3.6). It carries only
// stable safe metadata: server-owned ids, model, the single assistant message,
// and usage. It NEVER carries raw Provider payloads, Provider end-markers,
// credential material, prompt/content beyond the assistant message, or foreign
// ids.
type ChatCompletion struct {
	// ID is the server-owned execution identity of this completion.
	ID Identifier
	// Object is the canonical object discriminator ("chat.completion").
	Object string
	// Created is the server-owned completion instant.
	Created Timestamp
	// Model is the canonical model slug that served the completion.
	Model string
	// ProviderAccountID is the same-Tenant account that served it (safe metadata).
	ProviderAccountID ProviderAccountID
	// RequestID is the server-owned correlation id of the originating request.
	RequestID Identifier
	// ExecutionID is the server-owned execution identity (stable for replay).
	ExecutionID Identifier
	// Choices is the ordered canonical assistant choices.
	Choices []ChatChoice
	// Usage is the canonical token accounting projection.
	Usage ChatUsage
}

// ChatOutcomeClass classifies a controlled Provider chat result.
type ChatOutcomeClass string

// Controlled Provider chat outcome classes for the Adapter port.
const (
	ChatOutcomeCommitted    ChatOutcomeClass = "committed"
	ChatOutcomeNotCommitted ChatOutcomeClass = "not_committed"
	ChatOutcomeUnknown      ChatOutcomeClass = "unknown"
)

// ChatOutcome is the safe classification returned to application code. It never
// carries raw Provider payloads. CommitNotCommitted is the authoritative
// no-commit proof that authorizes a single fallback re-attempt; CommitUnknown
// fails closed and forbids replacement.
type ChatOutcome struct {
	Class ChatOutcomeClass
	// Commit reuses the render CommitStatus vocabulary (committed / not_committed
	// / unknown). not_committed is the authoritative no-commit proof.
	Commit CommitStatus
	// Completion is populated only on a committed success.
	Completion ChatCompletion
	// FailureClass is a safe canonical class when not committed / unknown (never
	// a raw provider string).
	FailureClass ErrorCode
}

// ChatAffinityScope is the same-Tenant scope of a conversation affinity
// preference (routing spec §5.1, chat lifecycle §5.2): Key is the client
// conversation_id. The preference never crosses Tenants, and because selection
// still requires candidate-set membership it can never widen execution.
type ChatAffinityScope struct {
	TenantID TenantID
	Key      string
}
