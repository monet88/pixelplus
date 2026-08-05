package chatgptweb

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// conversationBody builds the upstream conversation payload for one turn.
// Messages are already canonical and Provider-independent when they arrive, so
// this is pure framing: no prompt is inspected, rewritten, or retained.
func conversationBody(model string, messages []domain.ChatMessage, stream bool) (string, error) {
	type part struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	payload := struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []part `json:"messages"`
	}{Model: model, Stream: stream, Messages: make([]part, 0, len(messages))}
	for _, message := range messages {
		payload.Messages = append(payload.Messages, part{Role: string(message.Role), Content: message.Content})
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// turnResult accumulates what one SSE turn produced.
type turnResult struct {
	text         strings.Builder
	finished     bool
	blocked      bool
	drifted      bool
	imagePointer string
	sawContent   bool
	// sawDone records the `[DONE]` terminator. Its ABSENCE is the truncation
	// signal: a real SSE body that ends without `[DONE]` ended prematurely, so a
	// turn with content but neither `[DONE]` nor a finish marker was cut off
	// mid-generation rather than completed.
	sawDone bool
}

// consumeStream reads an SSE body to its end, applying each decoded event.
// deliver is called for every content delta; a nil deliver aggregates silently,
// which is how the non-streaming path reuses the streaming decoder.
//
// A deliver error means the client is gone or the stream is already terminated,
// so consumption stops promptly rather than producing into a dead sink
// (ports.ChatStreamAdapter contract).
func consumeStream(ctx context.Context, stream Stream, deliver func(string) error) (turnResult, error) {
	var result turnResult
	if stream == nil {
		return result, ErrTransportUnavailable
	}
	defer func() { _ = stream.Close() }()

	for {
		// Cancellation is cooperative and checked between payloads: the caller's
		// context is the cancel/disconnect signal (chat lifecycle §6.2/§6.3), and
		// stopping here is the only abort this surface can honestly perform.
		if err := ctx.Err(); err != nil {
			return result, err
		}
		payload, ok, err := stream.Next()
		if err != nil {
			return result, err
		}
		if !ok {
			return result, nil
		}
		for _, event := range decodeStreamPayload(payload) {
			switch event.kind {
			case eventDone:
				result.sawDone = true
				return result, nil
			case eventDelta:
				if event.text == "" {
					continue
				}
				result.sawContent = true
				result.text.WriteString(event.text)
				if deliver != nil {
					if err := deliver(event.text); err != nil {
						return result, err
					}
				}
			case eventFinished:
				result.finished = true
			case eventBlocked:
				result.blocked = true
			case eventImage:
				result.imagePointer = event.pointer
			case eventDrift:
				result.drifted = true
			case eventIgnored:
			}
		}
	}
}

// finishClass maps an accumulated turn onto the canonical terminal class.
func (result turnResult) finishClass() domain.FinishClass {
	if result.blocked {
		return domain.FinishContentFilter
	}
	return domain.FinishStop
}

// producedNothing reports a turn that yielded no evidence of a generation at
// all: no content, no image output, no terminal marker, and no moderation
// block. That is not an empty answer — it means the transcript said nothing this
// Adapter recognized, so committing it would fabricate both an empty message and
// a `stop` finish class the Provider never sent.
//
// It is kept separate from `drifted` because the two have different causes: a
// drift is a payload we failed to parse, whereas this is a transcript of
// perfectly parseable non-content events. Both must refuse to commit, and both
// classify as protocol drift because a turn that produces nothing observable is
// a protocol expectation that no longer holds (evidence §7).
func (result turnResult) producedNothing() bool {
	return !result.sawContent && !result.finished && !result.blocked && result.imagePointer == ""
}

// truncated reports a turn whose body ended prematurely: content arrived but the
// stream carried neither a finish marker nor the `[DONE]` terminator.
//
// A completed ChatGPT Web body always ends with `[DONE]`, so its absence means
// the connection dropped mid-generation. Reporting `stop` for such a turn would
// tell the caller the model chose to end there, when in fact the answer is cut
// off — and the upstream may well have continued generating and billed the rest.
//
// `[DONE]` alone is enough to consider a turn complete: image turns legitimately
// terminate with `[DONE]` and no message-status marker, so requiring the marker
// would misclassify every image generation as truncated.
func (result turnResult) truncated() bool {
	return result.sawContent && !result.finished && !result.blocked && !result.sawDone
}

// Run executes one non-streaming chat completion.
//
// The Web surface has no separate non-streaming endpoint: a non-stream response
// is a client aggregation over the same SSE conversation (§2.1 "Non-stream is a
// client aggregation over SSE, not a separate upstream non-stream endpoint").
// So this method opens the stream and aggregates it, which keeps one protocol
// decoder for both surfaces.
func (adapter *Adapter) Run(ctx context.Context, command ports.ChatCommand, credential ports.CredentialInjection) (domain.ChatOutcome, error) {
	if command.AuthMode != domain.AuthModeChatGPTWebAccess {
		return domain.ChatOutcome{}, ports.ErrChatAdapterUnavailable
	}
	if credential == nil {
		// Structurally incomplete: the Adapter cannot succeed on validation alone.
		return domain.ChatOutcome{}, ports.ErrChatAdapterUnavailable
	}

	var result turnResult
	err := adapter.withConversation(ctx, credential, command.Model, command.Messages, func(stream Stream) error {
		consumed, err := consumeStream(ctx, stream, nil)
		result = consumed
		return err
	})
	if err != nil {
		return chatFailureOutcome(ctx, err, result), nil
	}
	if (result.drifted && !result.sawContent) || result.producedNothing() {
		// Nothing was delivered and nothing proved a generation happened, so
		// claiming a completion would invent one. No content left the Gateway, so
		// this is authoritatively not committed and the spine may re-attempt.
		return domain.ChatOutcome{
			Class:        domain.ChatOutcomeNotCommitted,
			Commit:       domain.CommitNotCommitted,
			FailureClass: domain.ErrCodeUpstreamProtocolDrift,
		}, nil
	}

	if result.truncated() {
		// Content arrived but the body ended without `[DONE]` or a finish marker.
		// The upstream may have kept generating and committed the full answer, so
		// certainty is UNKNOWN and no fallback re-attempt is authorized — returning
		// the partial text with a `stop` class would present a cut-off answer as a
		// complete one.
		return domain.ChatOutcome{
			Class:        domain.ChatOutcomeUnknown,
			Commit:       domain.CommitUnknown,
			FailureClass: domain.ErrCodeExecutionPossiblyCommitted,
		}, nil
	}

	return domain.ChatOutcome{
		Class:  domain.ChatOutcomeCommitted,
		Commit: domain.CommitCommitted,
		Completion: domain.ChatCompletion{
			Object: "chat.completion",
			Model:  command.Model,
			Choices: []domain.ChatChoice{{
				Index:       0,
				Message:     domain.ChatMessage{Role: domain.ChatRoleAssistant, Content: result.text.String()},
				FinishClass: result.finishClass(),
			}},
		},
	}, nil
}

// Stream executes one streaming chat completion, writing canonical deltas to
// the sink as they arrive.
//
// The Adapter cannot emit `open` or any terminal event — the sink exposes only
// Delta and Heartbeat — so canonical ordering and the exactly-one-terminal
// invariant cannot be broken from here (I-CHAT-STREAM-ORDER).
func (adapter *Adapter) Stream(
	ctx context.Context,
	command ports.ChatStreamCommand,
	credential ports.CredentialInjection,
	sink domain.ChatSink,
) (domain.ChatStreamOutcome, error) {
	if command.AuthMode != domain.AuthModeChatGPTWebAccess {
		return domain.ChatStreamOutcome{}, ports.ErrChatStreamAdapterUnavailable
	}
	if credential == nil || sink == nil {
		return domain.ChatStreamOutcome{}, ports.ErrChatStreamAdapterUnavailable
	}

	var result turnResult
	err := adapter.withConversation(ctx, credential, command.Model, command.Messages, func(stream Stream) error {
		consumed, err := consumeStream(ctx, stream, func(text string) error {
			return sink.Delta(domain.ChatDelta{Content: text})
		})
		result = consumed
		return err
	})
	if err != nil {
		return streamFailureOutcome(ctx, err, result), nil
	}
	if (result.drifted && !result.sawContent) || result.producedNothing() {
		// Same rule as the non-streaming path: a turn that produced nothing
		// observable must not commit. On the streaming surface no delta reached the
		// sink either, so the spine is free to re-attempt.
		return domain.ChatStreamOutcome{
			Class:        domain.ChatOutcomeNotCommitted,
			Commit:       domain.CommitNotCommitted,
			FailureClass: domain.ErrCodeUpstreamProtocolDrift,
			Usage:        domain.ChatUsage{},
		}, nil
	}

	if result.truncated() {
		// Deltas already reached the client, so the stream cannot be retried, and
		// the upstream may hold a longer committed generation. UNKNOWN routes this
		// onto the conservative residual-accounting path (§6.5).
		return domain.ChatStreamOutcome{
			Class:        domain.ChatOutcomeUnknown,
			Commit:       domain.CommitUnknown,
			FailureClass: domain.ErrCodeExecutionPossiblyCommitted,
		}, nil
	}

	return domain.ChatStreamOutcome{
		Class:       domain.ChatOutcomeCommitted,
		Commit:      domain.CommitCommitted,
		FinishClass: result.finishClass(),
	}, nil
}

// withConversation runs the sentinel pre-flight, opens the conversation stream
// inside the credential callback, and hands the stream to consume.
//
// Everything that needs the secret happens inside CredentialInjection.Use, so
// credential material never lands in a field, a log, or a returned value
// (OP-G3, ADR 0009 protected boundary).
func (adapter *Adapter) withConversation(
	ctx context.Context,
	credential ports.CredentialInjection,
	model string,
	messages []domain.ChatMessage,
	consume func(Stream) error,
) error {
	body, err := conversationBody(model, messages, true)
	if err != nil {
		return errProtocolDrift
	}

	return credential.Use(func(secretMaterial string) error {
		headers := map[string]string{
			"Authorization": "Bearer " + secretMaterial,
			"Content-Type":  "application/json",
		}

		// Sentinel pre-flight. A required challenge stops the turn here, BEFORE the
		// conversation is opened: this Adapter classifies challenges and never
		// solves them (OP-G6), and a refused turn that never reached generation is
		// authoritatively not committed.
		requirements, err := adapter.exchange(ctx, Request{
			Method:  "POST",
			Path:    PathChatRequirements,
			Body:    "{}",
			Headers: headers,
		})
		if err != nil {
			return err
		}
		switch classifyStatus(requirements.Status) {
		case signalOK:
			if challengeRequired(requirements.Body) {
				return errChallenged
			}
		case signalAuthFailed:
			return errAuthFailed
		case signalChallenged:
			return errChallenged
		case signalRateLimited:
			return errRateLimited
		default:
			return errUnavailable
		}

		opened, err := adapter.exchange(ctx, Request{
			Method:  "POST",
			Path:    PathConversation,
			Body:    body,
			Headers: headers,
			Stream:  true,
		})
		if err != nil {
			return err
		}
		switch classifyStatus(opened.Status) {
		case signalOK:
		case signalAuthFailed:
			return errAuthFailed
		case signalChallenged:
			return errChallenged
		case signalRateLimited:
			return errRateLimited
		default:
			return errUnavailable
		}
		if opened.Stream == nil {
			return ErrTransportUnavailable
		}
		return consume(opened.Stream)
	})
}

// Adapter-internal classification errors. They carry a class and nothing else:
// no provider body, no header, no credential material (OP-G3).
var (
	errAuthFailed    = errors.New("chatgpt web auth failed")
	errChallenged    = errors.New("chatgpt web challenge required")
	errRateLimited   = errors.New("chatgpt web rate limited")
	errUnavailable   = errors.New("chatgpt web upstream unavailable")
	errProtocolDrift = errors.New("chatgpt web protocol drift")
)

// canonicalFailureClass maps an internal classification onto a canonical error
// code. Commit certainty is decided separately by the caller, because whether
// content already reached the client is not a property of the failure class.
func canonicalFailureClass(err error) domain.ErrorCode {
	switch {
	case errors.Is(err, errAuthFailed):
		return domain.ErrCodeProviderAuthExpired
	case errors.Is(err, errChallenged):
		return domain.ErrCodeProviderChallenged
	case errors.Is(err, errRateLimited):
		return domain.ErrCodeProviderRateLimited
	case errors.Is(err, errProtocolDrift):
		return domain.ErrCodeUpstreamProtocolDrift
	case errors.Is(err, ErrTransportUnavailable):
		return domain.ErrCodeUpstreamUnavailable
	default:
		return domain.ErrCodeUpstreamUnavailable
	}
}

// chatFailureOutcome classifies a non-streaming failure.
//
// A failure BEFORE any content arrived is authoritatively not committed, so the
// spine's single fallback re-attempt stays authorized.
//
// Two cases forfeit that certainty, and both turn on what the UPSTREAM did
// rather than on what reached the client:
//
//   - Context cancellation: the upstream may still be generating. The Web
//     surface has no documented cooperative cancel (§2.1 marks cancel/abort
//     `unverified`), so there is nothing to prove a stop with.
//   - Content already arrived from the upstream before the break. `Run` buffers,
//     so the caller saw nothing — but the upstream demonstrably produced a
//     generation and may have committed and billed it. Claiming an authoritative
//     no-commit here would authorize a re-attempt that generates a second time
//     on the Provider (§6.2 authoritative-no-commit rule).
//
// The second case is why this takes the accumulated turn: the streaming path
// already applies the same rule through `result.sawContent`, and the commit
// question is identical on both surfaces even though the client exposure is not.
func chatFailureOutcome(ctx context.Context, err error, result turnResult) domain.ChatOutcome {
	if ctx.Err() != nil || result.sawContent {
		return domain.ChatOutcome{
			Class:        domain.ChatOutcomeUnknown,
			Commit:       domain.CommitUnknown,
			FailureClass: domain.ErrCodeExecutionPossiblyCommitted,
		}
	}
	return domain.ChatOutcome{
		Class:        domain.ChatOutcomeNotCommitted,
		Commit:       domain.CommitNotCommitted,
		FailureClass: canonicalFailureClass(err),
	}
}

// streamFailureOutcome classifies a streaming failure.
//
// Once any delta reached the sink the turn is committed as far as the client is
// concerned, so commit certainty is UNKNOWN rather than not-committed: a
// fallback re-attempt would deliver a second, contradictory answer on a stream
// the client already partly consumed.
//
// UpstreamAbortAttempted stays false and UpstreamStopConfirmed stays false on
// every path. Closing the local stream is not an upstream abort, and §6.2 rules
// 3-4 forbid claiming either without proof. The evidence marks Web cancel/abort
// `unverified` (§2.1, §10.1 gap 2), so this Adapter has nothing to prove with —
// which is exactly what routes a canceled Web stream onto the conservative
// residual-accounting path instead of an immediate release.
func streamFailureOutcome(ctx context.Context, err error, result turnResult) domain.ChatStreamOutcome {
	if ctx.Err() != nil || result.sawContent {
		return domain.ChatStreamOutcome{
			Class:        domain.ChatOutcomeUnknown,
			Commit:       domain.CommitUnknown,
			FailureClass: domain.ErrCodeExecutionPossiblyCommitted,
		}
	}
	return domain.ChatStreamOutcome{
		Class:        domain.ChatOutcomeNotCommitted,
		Commit:       domain.CommitNotCommitted,
		FailureClass: canonicalFailureClass(err),
	}
}

var (
	_ ports.ChatAdapter       = (*Adapter)(nil)
	_ ports.ChatStreamAdapter = (*Adapter)(nil)
)
