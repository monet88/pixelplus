package chatgptweb

import (
	"context"
	"errors"
)

// Upstream paths observed on the ChatGPT Web backend (capability evidence §2.1,
// §3.1). They are named here so translation code and fixtures agree on one
// spelling and a drift shows up as a compile-time change rather than a typo.
const (
	// PathMe proves the session identity during credential preparation.
	PathMe = "/backend-api/me"
	// PathConversationInit carries default model slug and limits_progress, which
	// is where the image_gen quota and its reset live.
	PathConversationInit = "/backend-api/conversation/init"
	// PathChatRequirements is the sentinel pre-flight that declares whether a
	// challenge (Arkose / proof-of-work / Turnstile) is required.
	PathChatRequirements = "/backend-api/sentinel/chat-requirements"
	// PathConversation is the authenticated SSE conversation endpoint.
	PathConversation = "/backend-api/conversation"
	// PathModels lists the session-dependent model slugs.
	PathModels = "/backend-api/models"
)

// ErrTransportUnavailable reports that the Adapter was composed without a
// Transport. It is the Adapter's fail-closed state: the lab profile may be on
// and the Adapter registered, and it still cannot reach a Provider until an
// operator deliberately supplies transport.
var ErrTransportUnavailable = errors.New("chatgpt web transport unavailable")

// Request is one upstream exchange. Headers carries only protocol framing;
// credential material is added inside the CredentialInjection callback at the
// call site and is never stored on a Request that outlives the callback.
type Request struct {
	Method  string
	Path    string
	Body    string
	Headers map[string]string
	// Stream requests an SSE response body. A Transport that cannot stream must
	// return a Response with a nil Stream rather than buffering silently, so the
	// caller classifies it as unavailable instead of mistaking it for an empty
	// generation.
	Stream bool
}

// Response is one upstream reply. Body carries a non-streaming payload; Stream
// carries an SSE body and is nil for non-streaming exchanges.
type Response struct {
	Status int
	Body   string
	Stream Stream
	// RetryAfterSeconds is the validated relative Retry-After hint in seconds,
	// or zero when the upstream proved no safe hint. It is a normalized number,
	// never a raw header value.
	RetryAfterSeconds int
}

// Stream yields SSE `data:` payloads one at a time so streaming translation is
// incremental rather than buffered. The bool reports whether a payload was
// produced; false means the stream ended.
type Stream interface {
	Next() (string, bool, error)
	Close() error
}

// Transport performs one upstream exchange. It is the seam that keeps this
// package protocol-only: fixtures implement it over sanitized payloads, and a
// real HTTP client is a separate concern (this story ships no egress).
//
// A Transport implementation MUST NOT retry a full operation. Retry ownership
// belongs to the spine, which is the only layer that can honor the
// authoritative-no-commit rule before re-attempting on another account.
type Transport interface {
	Exchange(ctx context.Context, request Request) (Response, error)
}
