package chatgptcodex

import (
	"context"
	"errors"
)

// Upstream paths observed on the ChatGPT Codex OAuth surface (capability
// evidence §2.2, §3.2). They are named here so translation code and fixtures
// agree on one spelling and a drift shows up as a compile-time change rather
// than a typo.
const (
	// PathCodexResponses is the authenticated Codex Responses execution
	// endpoint. Chat, stream, and image tool calls all post here; the
	// difference is the body and whether Stream is requested.
	PathCodexResponses = "/backend-api/codex/responses"
	// PathOAuthToken is the OAuth refresh endpoint. A refresh is performed
	// inside the CredentialInjection callback when a 401 surfaces, so the
	// rotated refresh_token never leaves the boundary.
	PathOAuthToken = "https://auth.openai.com/oauth/token"
	// PathMe proves the session identity during the credential probe. The
	// Codex surface shares the chatgpt.com host, so the same /backend-api/me
	// identity probe the Web surface uses proves the access_token is alive.
	PathMe = "/backend-api/me"
	// PathModels lists the session-dependent model slugs used for capability
	// observation. The evidence marks Codex model discovery
	// `conditionally_supported` (static catalog, drift-prone, no live /models
	// verified end-to-end: §2.2), so a fixture controls this exchange and a
	// real Transport MUST NOT ship before probe-time discovery is resolved.
	PathModels = "/backend-api/models"
)

// ErrTransportUnavailable reports that the Adapter was composed without a
// Transport. It is the Adapter's fail-closed state: the gated profile may be on
// and the Adapter registered, and it still cannot reach a Provider until an
// operator deliberately supplies transport.
var ErrTransportUnavailable = errors.New("chatgpt codex transport unavailable")

// Request is one upstream exchange. Headers carries only protocol framing;
// credential material (the access_token, refresh_token, and account_id) is added
// inside the CredentialInjection callback at the call site and is never stored on
// a Request that outlives the callback.
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
//
// UNRESOLVED — a Transport MUST NOT ship as a shared ambient client.
//
// Chat and Stream receive ports.CredentialInjection and build their headers
// inside the Use callback, so those surfaces are account-bound by construction.
// Probe and Observe are not: ports.ProbeCommand and
// ports.CapabilityObservationCommand carry Principal/AccountID/AuthMode/Version
// identifiers and no credential, because those ports predate this Adapter and
// were written for a spine that validates the stored version through the Vault
// first.
//
// Composition supplies ONE Transport for the whole deployment
// (Dependencies.GatedChatGPTCodexTransport, or the adapter-free
// Dependencies.GatedChatGPTCodexResponder that composition translates onto this
// seam for contract proofs), so nothing here binds an exchange to the account
// named in the command. Cause and effect if a real client shipped against this
// seam as written:
//
//   - A Transport holding no session sends a probe unauthenticated, the upstream
//     answers 401, and Probe faithfully reports Authenticated=false — moving a
//     perfectly good account to reauth_required.
//   - Worse, a Transport holding SOME session proves that session for every
//     account routed to it. Probe would report success for account B while
//     actually authenticating as account A, and Observe would mint account B a
//     Capability Snapshot describing A's entitlements.
//
// Neither is reachable in production: ProductionDependencies ships both egress
// fields nil (proved by composition.TestProductionDependenciesGrantNoGatedCodexEgress),
// so both methods fail closed with ErrTransportUnavailable before any exchange.
// The contract seam supplies egress only inside controlled fixtures whose
// responder answers one scripted account. That is what makes the gap latent
// rather than live. Binding the probe/observe surfaces to a per-account
// credential requires a ports change and is tracked separately —
// see the Follow-Up in docs/decisions/0014-gated-auth-mode-operator-feature-flag.md
// and docs/decisions/0013-experimental-lab-profile-and-capability-baseline.md.
// A real Transport MUST NOT be wired before that is resolved (#111).
type Transport interface {
	Exchange(ctx context.Context, request Request) (Response, error)
}
