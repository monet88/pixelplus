package composition

import (
	"context"
	"reflect"

	"github.com/monet88/pixelplus/apps/gateway/internal/adapters/chatgptcodex"
)

// GatedCodexExchange is one upstream Codex exchange, expressed WITHOUT naming the
// adapter package. It exists so a caller that may not import
// internal/adapters — contracttest, under the ADR 0009 dependency direction
// pinned by TestGatewayImportsRespectDependencyDirection — can still supply the
// gated Codex Adapter its egress and prove probe/chat/stream through the public
// HTTP seam.
//
// Cause and effect without it: contracttest cannot name chatgptcodex.Transport,
// so no fixture can give the gated Adapter egress, so the only Codex proofs
// reachable through the seam are the refusal ones. The Adapter's success paths
// then have unit coverage only, which is exactly the completion-evidence gap the
// review flagged (F6). Relaxing the import rule instead would let every future
// contracttest reach into adapter internals; this shim keeps the direction intact
// and mirrors only the transport's data shape.
//
// The fields mirror chatgptcodex.Request/Response one-for-one on purpose: this is
// a translation seam, not a place to add behavior. Headers carry protocol framing
// only, and credential material never appears here — it is added inside the
// Adapter's CredentialInjection callback (OP-G3).
type GatedCodexExchange struct {
	Method  string
	Path    string
	Body    string
	Headers map[string]string
	// Stream reports that the caller asked for an SSE body. A responder that
	// cannot stream must leave GatedCodexReply.Stream nil rather than buffering,
	// so the Adapter classifies it as unavailable instead of an empty generation.
	Stream bool
}

// GatedCodexReply is one upstream Codex reply in the same adapter-free vocabulary.
type GatedCodexReply struct {
	Status int
	Body   string
	// Stream carries an SSE body; nil for a non-streaming exchange.
	Stream GatedCodexStream
	// RetryAfterSeconds is the validated relative hint in seconds, or zero when
	// the upstream proved no safe hint. Never a raw header value.
	RetryAfterSeconds int
}

// GatedCodexStream yields SSE `data:` payloads one at a time, mirroring the
// adapter's stream seam so translation stays incremental rather than buffered.
type GatedCodexStream interface {
	Next() (string, bool, error)
	Close() error
}

// GatedCodexResponder answers gated Codex exchanges. A fixture implements it to
// grant the gated Adapter controlled egress; production supplies a real
// chatgptcodex.Transport directly and never uses this seam.
type GatedCodexResponder interface {
	Respond(ctx context.Context, exchange GatedCodexExchange) (GatedCodexReply, error)
}

// gatedCodexTransport adapts a GatedCodexResponder onto chatgptcodex.Transport.
// The translation is total and lossless in both directions, so a fixture cannot
// observe a request the Adapter did not make or hide one it did.
type gatedCodexTransport struct {
	responder GatedCodexResponder
}

func (transport gatedCodexTransport) Exchange(
	ctx context.Context,
	request chatgptcodex.Request,
) (chatgptcodex.Response, error) {
	reply, err := transport.responder.Respond(ctx, GatedCodexExchange{
		Method:  request.Method,
		Path:    request.Path,
		Body:    request.Body,
		Headers: request.Headers,
		Stream:  request.Stream,
	})
	if err != nil {
		return chatgptcodex.Response{}, err
	}
	// A nil reply.Stream stays nil here: assigning one interface value to another
	// preserves nil-ness (unlike wrapping a typed nil pointer), so the Adapter's
	// `opened.Stream == nil` fail-closed check still sees a streamless 200 for what
	// it is. TestGatedCodexChatFailsClosedOnATwoHundredWithoutAStreamThroughTheSeam
	// proves that end to end.
	return chatgptcodex.Response{
		Status:            reply.Status,
		Body:              reply.Body,
		Stream:            reply.Stream,
		RetryAfterSeconds: reply.RetryAfterSeconds,
	}, nil
}

// gatedCodexTransportFrom resolves the transport the gated profile should use.
// An explicit chatgptcodex.Transport wins; otherwise a supplied responder is
// wrapped. Both absent yields nil, which keeps the gated Adapter unregistered —
// enabling a mode is not granting egress (decision 0014).
//
// Both fields are checked with isNilInterface rather than a bare `!= nil`: a
// caller that stores a typed-nil concrete value (e.g. a nil *someTransport) in
// either interface-typed field would otherwise pass a `!= nil` guard — an
// interface holding a nil pointer is itself non-nil — and reach
// newGatedAdapters' registration guard, which would then register the Codex
// Adapter over a transport that panics on first use instead of failing closed
// before registration.
func gatedCodexTransportFrom(dependencies Dependencies) chatgptcodex.Transport {
	if !isNilInterface(dependencies.GatedChatGPTCodexTransport) {
		return dependencies.GatedChatGPTCodexTransport
	}
	if !isNilInterface(dependencies.GatedChatGPTCodexResponder) {
		return gatedCodexTransport{responder: dependencies.GatedChatGPTCodexResponder}
	}
	return nil
}

// isNilInterface reports whether an interface value is nil OR holds a
// concrete value that is itself nil (a typed nil, e.g. a nil pointer or nil
// map/slice/chan/func stored in the interface). A bare `value != nil` only
// catches the first case: an interface holding a typed nil concrete value
// compares non-nil in Go even though calling through it panics. reflect.Value
// is used only for the Kind check that distinguishes a nil-able concrete kind
// from one that cannot be nil (e.g. a struct value); it never inspects field
// contents, so this stays a structural nil check and nothing else.
func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Ptr, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func, reflect.Interface:
		return reflected.IsNil()
	default:
		return false
	}
}
