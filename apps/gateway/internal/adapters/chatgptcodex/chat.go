package chatgptcodex

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// codexOAuthClientID is the Codex CLI OAuth client_id reference-learned from
// `.ref/CLIProxyAPI` (capability evidence §3.2 "Obtain (browser OAuth)"). It is
// a public client identifier, not a secret, and is the client the refresh grant
// MUST use — using the Web Access platform client here would break refresh
// (evidence §7 "Refresh client_id mismatch").
const codexOAuthClientID = "app_EMoamEEZ73f0CkXaXp7hrann"

// codexBundle is the OAuth credential set parsed inside the
// CredentialInjection callback. It never leaves the callback: the rotated
// access_token is used only to re-send the same exchange and is then discarded.
type codexBundle struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"`
}

// parseCodexBundle decodes the secret material the Vault injected. A malformed
// bundle is protocol drift rather than an auth failure: the Vault validated the
// shape at storage time, so an undecodable bundle here means the credential
// class is wrong for this Auth Mode, not that the credential is expired.
func parseCodexBundle(secretMaterial string) (codexBundle, error) {
	var bundle codexBundle
	if err := json.Unmarshal([]byte(secretMaterial), &bundle); err != nil {
		return codexBundle{}, errProtocolDrift
	}
	if bundle.AccessToken == "" {
		return codexBundle{}, errProtocolDrift
	}
	return bundle, nil
}

// responsesBody builds the upstream Codex Responses payload for one turn. Codex
// uses a Responses-style body with an `input` array (not `messages`), matching
// the reference executor (capability evidence §2.2 "posts to
// /backend-api/codex/responses"). Messages are already canonical and
// Provider-independent when they arrive, so this is pure framing: no prompt is
// inspected, rewritten, or retained.
func responsesBody(model string, messages []domain.ChatMessage, stream bool) (string, error) {
	type input struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	payload := struct {
		Model  string  `json:"model"`
		Input  []input `json:"input"`
		Stream bool    `json:"stream"`
	}{Model: model, Stream: stream, Input: make([]input, 0, len(messages))}
	for _, message := range messages {
		payload.Input = append(payload.Input, input{Role: string(message.Role), Content: message.Content})
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// turnResult accumulates what one SSE turn produced.
//
// text is a plain string rather than a strings.Builder because a turnResult is
// copied by value on every return path and again by the caller. Copying a
// non-zero Builder and then writing to the copy panics ("illegal use of non-zero
// Builder copied by value") and go vet does not catch it, so the accumulating
// Builder stays local to consumeStream and only its finished string is stored
// here.
type turnResult struct {
	text         string
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
	// The Builder is local: turnResult is copied by value on every return below,
	// and copying a non-zero Builder then writing to it panics. snapshot folds the
	// accumulated text into the returned value so no caller ever holds a Builder.
	var text strings.Builder
	snapshot := func() turnResult {
		result.text = text.String()
		return result
	}
	defer func() { _ = stream.Close() }()

	for {
		// Cancellation is cooperative and checked between payloads: the caller's
		// context is the cancel/disconnect signal (chat lifecycle §6.2/§6.3), and
		// stopping here is the only abort this surface can honestly perform.
		if err := ctx.Err(); err != nil {
			return snapshot(), err
		}
		payload, ok, err := stream.Next()
		if err != nil {
			return snapshot(), err
		}
		if !ok {
			return snapshot(), nil
		}
		for _, event := range decodeStreamPayload(payload) {
			switch event.kind {
			case eventDone:
				result.sawDone = true
				return snapshot(), nil
			case eventDelta:
				if event.text == "" {
					continue
				}
				result.sawContent = true
				text.WriteString(event.text)
				if deliver != nil {
					if err := deliver(event.text); err != nil {
						return snapshot(), err
					}
				}
			case eventFinished:
				result.finished = true
			case eventBlocked:
				result.blocked = true
			case eventImage:
				result.imagePointer = event.pointer
			case eventQuota:
				// The in-stream reset hint is deliberately NOT carried on the chat
				// outcome, and that is the spec'd shape rather than a gap. Canonical
				// errors §5.1 assigns the numeric duration to #17, and the health
				// spec §17.4 defines the client-visible value as a ceiling computed
				// from the latest retry_not_before among matching waitable gates —
				// NOT the raw Provider number. Health spec §17.8 forbids forwarding
				// a raw Provider timestamp directly, and §7.4/§7.6 require an
				// implausible hint to become a malformed-hint observation rather
				// than a wait the Tenant is told to honor.
				//
				// So a retry-after field on ChatOutcome would be the wrong carrier:
				// it would route resets_in_seconds around the plausibility and
				// backoff rules that make the number trustworthy. The right path is
				// a CooldownObservation into the health store, which is how the
				// probe surface already feeds its hint
				// (ports.ProbeOutcome.RetryAfterSeconds → providerRetryNotBefore).
				// Chat-surface quota does not yet feed that path on EITHER Adapter
				// (this one or the T18 web Adapter), which is a #17 follow-up and
				// not something a field here would fix.
				return snapshot(), errQuota
			case eventRateLimited:
				return snapshot(), errRateLimited
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
func (result turnResult) producedNothing() bool {
	return !result.sawContent && !result.finished && !result.blocked && result.imagePointer == ""
}

// driftedWithoutEvidence reports a drift on a turn that produced nothing this
// surface could observe: no content delta, no image pointer.
//
// This stays AUTHORITATIVELY not-committed on purpose. The spine's fallback walk
// depends on an authoritative no-commit to be allowed to re-attempt on another
// account; if every unparseable payload became UNKNOWN, fallback would be
// disabled for the whole Codex surface and a single Provider-side protocol
// change would strand every turn instead of failing over. Nothing left the
// Gateway and nothing proves the upstream generated, so re-attempting cannot
// double-bill.
func (result turnResult) driftedWithoutEvidence() bool {
	return result.drifted && !result.sawContent && result.imagePointer == ""
}

// driftedAfterEvidence reports a drift on a turn that HAD already produced
// something: at least one content delta, or a confirmed image pointer. The
// upstream demonstrably generated (and may have billed), so the honest answer is
// UNKNOWN: the success is withheld and so is permission to re-generate. See the
// T18 chatgptweb.turnResult.driftedAfterEvidence for the full rationale.
func (result turnResult) driftedAfterEvidence() bool {
	return result.drifted && (result.sawContent || result.imagePointer != "")
}

// truncated reports a turn whose body ended prematurely: content arrived but the
// stream carried neither a finish marker nor the `[DONE]` terminator.
func (result turnResult) truncated() bool {
	return result.sawContent && !result.finished && !result.blocked && !result.sawDone
}

// undeliverableImage reports a turn that produced an image asset this surface
// cannot carry — whether or not it also produced text. This Adapter serves the
// CHAT surface, and the canonical chat vocabulary has no carrier for an asset,
// so the turn is UNKNOWN: something was committed upstream, this surface cannot
// deliver it, and no replacement attempt is authorized. See the T18
// chatgptweb.turnResult.undeliverableImage for the full rationale.
func (result turnResult) undeliverableImage() bool {
	return result.imagePointer != ""
}

// Run executes one non-streaming chat completion.
//
// The Codex Responses surface streams natively; a non-stream response is a
// client aggregation over the same SSE responses stream, which keeps one
// protocol decoder for both surfaces (evidence §2.2 "chat streaming").
func (adapter *Adapter) Run(ctx context.Context, command ports.ChatCommand, credential ports.CredentialInjection) (domain.ChatOutcome, error) {
	if command.AuthMode != domain.AuthModeChatGPTCodexOAuth {
		return domain.ChatOutcome{}, ports.ErrChatAdapterUnavailable
	}
	if credential == nil {
		// Structurally incomplete: the Adapter cannot succeed on validation alone.
		return domain.ChatOutcome{}, ports.ErrChatAdapterUnavailable
	}

	var result turnResult
	err := adapter.withResponses(ctx, credential, command.Model, command.Messages, func(stream Stream) error {
		consumed, err := consumeStream(ctx, stream, nil)
		result = consumed
		return err
	})
	if err != nil {
		return chatFailureOutcome(ctx, err, result), nil
	}
	if result.driftedWithoutEvidence() || result.producedNothing() {
		// Nothing was delivered and nothing proved a generation happened, so
		// claiming a completion would invent one. No content left the Gateway, so
		// this is authoritatively not committed and the spine may re-attempt.
		return domain.ChatOutcome{
			Class:        domain.ChatOutcomeNotCommitted,
			Commit:       domain.CommitNotCommitted,
			FailureClass: domain.ErrCodeUpstreamProtocolDrift,
		}, nil
	}

	if result.driftedAfterEvidence() {
		return domain.ChatOutcome{
			Class:        domain.ChatOutcomeUnknown,
			Commit:       domain.CommitUnknown,
			FailureClass: domain.ErrCodeExecutionPossiblyCommitted,
		}, nil
	}

	if result.undeliverableImage() {
		return domain.ChatOutcome{
			Class:        domain.ChatOutcomeUnknown,
			Commit:       domain.CommitUnknown,
			FailureClass: domain.ErrCodeExecutionPossiblyCommitted,
		}, nil
	}

	if result.truncated() {
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
				Message:     domain.ChatMessage{Role: domain.ChatRoleAssistant, Content: result.text},
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
	if command.AuthMode != domain.AuthModeChatGPTCodexOAuth {
		return domain.ChatStreamOutcome{}, ports.ErrChatStreamAdapterUnavailable
	}
	if credential == nil || sink == nil {
		return domain.ChatStreamOutcome{}, ports.ErrChatStreamAdapterUnavailable
	}

	var result turnResult
	err := adapter.withResponses(ctx, credential, command.Model, command.Messages, func(stream Stream) error {
		consumed, err := consumeStream(ctx, stream, func(text string) error {
			return sink.Delta(domain.ChatDelta{Content: text})
		})
		result = consumed
		return err
	})
	if err != nil {
		return streamFailureOutcome(ctx, err, result), nil
	}
	if result.driftedWithoutEvidence() || result.producedNothing() {
		return domain.ChatStreamOutcome{
			Class:        domain.ChatOutcomeNotCommitted,
			Commit:       domain.CommitNotCommitted,
			FailureClass: domain.ErrCodeUpstreamProtocolDrift,
			Usage:        domain.ChatUsage{},
		}, nil
	}

	if result.driftedAfterEvidence() {
		return domain.ChatStreamOutcome{
			Class:        domain.ChatOutcomeUnknown,
			Commit:       domain.CommitUnknown,
			FailureClass: domain.ErrCodeExecutionPossiblyCommitted,
		}, nil
	}

	if result.undeliverableImage() {
		return domain.ChatStreamOutcome{
			Class:        domain.ChatOutcomeUnknown,
			Commit:       domain.CommitUnknown,
			FailureClass: domain.ErrCodeExecutionPossiblyCommitted,
		}, nil
	}

	if result.truncated() {
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

// withResponses opens the Codex Responses stream inside the credential callback
// and hands it to consume. On a 401 it asks the credential boundary to own one
// rotation and re-sends the SAME exchange once — the documented Codex "on 401
// refresh-and-retry" behavior (evidence §3.2) — which is distinct from the
// spine's full-operation fallback re-attempt on another account (#62 AC4).
//
// The rotation is delegated rather than performed here: the Provider invalidates
// the previous refresh material on a successful grant, so only the boundary that
// can persist the rotated set, advance credential_version, dedupe concurrent
// rotations, and audit may own it (ports.CredentialRotation). An injection that
// does not offer that capability makes the 401 terminal for this attempt.
//
// Everything that needs the secret happens inside CredentialInjection.Use, so
// credential material never lands in a field, a log, or a returned value
// (OP-G3, ADR 0009 protected boundary). The Codex surface carries no sentinel,
// proof-of-work, or Turnstile pre-flight (evidence §6: all unsupported on the
// Codex path), so there is no challenge gate to run before the conversation is
// opened.
func (adapter *Adapter) withResponses(
	ctx context.Context,
	credential ports.CredentialInjection,
	model string,
	messages []domain.ChatMessage,
	consume func(Stream) error,
) error {
	body, err := responsesBody(model, messages, true)
	if err != nil {
		return errProtocolDrift
	}

	return credential.Use(func(secretMaterial string) error {
		bundle, err := parseCodexBundle(secretMaterial)
		if err != nil {
			return err
		}
		headers := codexHeaders(bundle)

		opened, err := adapter.exchange(ctx, Request{
			Method:  http.MethodPost,
			Path:    PathCodexResponses,
			Body:    body,
			Headers: headers,
			Stream:  true,
		})
		if err != nil {
			return err
		}
		class := classifyStatus(opened.Status)
		// On an auth-class failure, attempt one in-boundary refresh and re-send
		// the SAME exchange. The refresh is a credential operation, not a
		// generation: nothing was produced, so a failed refresh is
		// authoritatively not-committed and the spine may re-attempt elsewhere.
		// The rotated access_token is used only for this re-send and discarded
		// (AC2, OP-G3).
		if class == signalAuthFailed && bundle.RefreshToken != "" {
			rotator, rotatable := credential.(ports.CredentialRotation)
			if !rotatable {
				// No boundary owns rotation here, so there is nothing this Adapter
				// may safely do: performing the grant itself would rotate the
				// Provider's refresh material while leaving the Vault holding the
				// now-dead previous set, so the NEXT refresh would fail and strand
				// the account. Reporting the auth failure loses this live session
				// but keeps stored credential state truthful.
				return errAuthFailed
			}
			rotateErr := rotator.Rotate(ctx,
				func() (string, error) { return adapter.rotateCredential(ctx, bundle.RefreshToken) },
				func(rotated string) error {
					// The boundary has persisted and versioned the rotated set, so
					// re-sending on it is safe: the material in play is the material
					// the Vault holds.
					rotatedBundle, parseErr := parseCodexBundle(rotated)
					if parseErr != nil {
						return parseErr
					}
					bundle = rotatedBundle
					headers = codexHeaders(bundle)
					opened, err = adapter.exchange(ctx, Request{
						Method:  http.MethodPost,
						Path:    PathCodexResponses,
						Body:    body,
						Headers: headers,
						Stream:  true,
					})
					if err != nil {
						return err
					}
					class = classifyStatus(opened.Status)
					return nil
				})
			switch {
			case rotateErr == nil:
			case errors.Is(rotateErr, ports.ErrCredentialRotationUnsupported):
				// The injection advertised rotation but its Vault has no rotation
				// store. Same reasoning as the missing-capability path above: the
				// responses exchange was never re-sent.
				return errAuthFailed
			default:
				// Rotate surfaces both a refused grant and the re-sent exchange's
				// own error. Returning it preserves the send-boundary distinction
				// classifyFailure needs: a refused grant proves the retry never
				// left, while a transport error from the re-send does not.
				return rotateErr
			}
		}
		switch class {
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
			// A 200 with no stream is a transport that cannot stream — fail
			// closed rather than buffering silently and mistaking it for an
			// empty generation. Unlike a nil transport (never transmitted), a
			// 200 proves the payload DID reach the Provider, so this is a
			// post-send-onset failure and commit certainty is forfeited.
			return errNonStreamingResponse
		}
		return consume(opened.Stream)
	})
}

// rotateCredential performs one Provider-side OAuth refresh grant and returns
// the COMPLETE rotated credential set for the boundary to persist. It is the
// `exchange` half of ports.CredentialRotation: it talks to the Provider and
// nothing else — it does not persist, version, dedupe, or audit, because those
// are the boundary's to own (F2/F10).
//
// The returned document carries the rotated refresh_token, not just the rotated
// access_token. That is the whole point: the Provider invalidates the previous
// refresh material on a successful grant, so a caller that persisted only the
// access token would leave the Vault holding a dead refresh token.
//
// Nothing here is retained: the material is produced inside the credential
// boundary's callback and handed straight back to it (AC2, OP-G3).
//
// A refresh_token_reused / revoked response is an auth-class failure: the
// account must reauthenticate, and this grant MUST NOT loop.
func (adapter *Adapter) rotateCredential(ctx context.Context, refreshToken string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", codexOAuthClientID)
	form.Set("refresh_token", refreshToken)
	refreshed, err := adapter.exchange(ctx, Request{
		Method:  http.MethodPost,
		Path:    PathOAuthToken,
		Body:    form.Encode(),
		Headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
	})
	if err != nil {
		return "", err
	}
	switch classifyStatus(refreshed.Status) {
	case signalOK:
	default:
		// A 400/401 on the refresh endpoint is a reused or revoked refresh_token.
		// Treat it as auth-failed so the account moves to reauth_required rather
		// than retrying the refresh loop.
		return "", errRefreshFailed
	}
	rotated, err := parseCodexBundle(refreshed.Body)
	if err != nil {
		return "", errRefreshFailed
	}
	if rotated.RefreshToken == "" {
		// A grant that rotated the access token but returned no refresh material
		// leaves the next rotation with nothing to spend. Refusing here is honest:
		// the boundary would otherwise persist a set it cannot rotate again.
		return "", errRefreshFailed
	}
	return refreshed.Body, nil
}

// codexHeaders builds the protocol framing headers for one exchange from the
// OAuth bundle. The account_id header ties the request to the exact account the
// spine selected (evidence §3.2 lifecycle example step 3), and the Bearer token
// is the access_token. Both are secret-class and live only inside the
// CredentialInjection callback.
func codexHeaders(bundle codexBundle) map[string]string {
	headers := map[string]string{
		"Authorization": "Bearer " + bundle.AccessToken,
		"Content-Type":  "application/json",
	}
	if bundle.AccountID != "" {
		headers["Chatgpt-Account-Id"] = bundle.AccountID
	}
	return headers
}

// Adapter-internal classification errors. They carry a class and nothing else:
// no provider body, no header, no credential material (OP-G3).
var (
	errAuthFailed    = errors.New("chatgpt codex auth failed")
	errChallenged    = errors.New("chatgpt codex challenge required")
	errRateLimited   = errors.New("chatgpt codex rate limited")
	errQuota         = errors.New("chatgpt codex quota exhausted")
	errUnavailable   = errors.New("chatgpt codex upstream unavailable")
	errProtocolDrift = errors.New("chatgpt codex protocol drift")
	errRefreshFailed = errors.New("chatgpt codex refresh failed")
	// errNonStreamingResponse reports a 200 that carried no SSE stream. The
	// request demonstrably reached the Provider (the 200 was answered), so the
	// attempt is possibly committed and MUST NOT be reported authoritatively
	// not-committed (chat/stream lifecycle §7.2 rule 3). It is distinct from
	// ErrTransportUnavailable, which only ever means a nil transport never
	// transmitted anything.
	errNonStreamingResponse = errors.New("chatgpt codex non-streaming response")
)

// canonicalFailureClass maps an internal classification onto a canonical error
// code. Commit certainty is decided separately by the caller, because whether
// content already reached the client is not a property of the failure class.
func canonicalFailureClass(err error) domain.ErrorCode {
	switch {
	case errors.Is(err, errAuthFailed), errors.Is(err, errRefreshFailed):
		return domain.ErrCodeProviderAuthExpired
	case errors.Is(err, errChallenged):
		return domain.ErrCodeProviderChallenged
	case errors.Is(err, errRateLimited):
		return domain.ErrCodeProviderRateLimited
	case errors.Is(err, errQuota):
		return domain.ErrCodeProviderQuotaExhausted
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
//   - Context cancellation: the upstream may still be generating. The Codex
//     surface has no documented cooperative cancel (§2.2 marks cancel/abort
//     `conditionally supported` with upstream cooperative cancel unverified),
//     so there is nothing to prove a stop with.
//   - Content already arrived from the upstream before the break. `Run` buffers,
//     so the caller saw nothing — but the upstream demonstrably produced a
//     generation and may have committed and billed it.
//
// failureDecision is the commit-certainty and failure-class decision shared by
// the non-streaming and streaming failure classifiers. The two surfaces differ
// only in the outcome struct they project onto; the commit question — the
// proof-of-non-commit boundary in the chat/stream lifecycle §7.2 — is identical.
type failureDecision struct {
	class        domain.ChatOutcomeClass
	commit       domain.CommitStatus
	failureClass domain.ErrorCode
}

func unknownDecision() failureDecision {
	return failureDecision{
		class:        domain.ChatOutcomeUnknown,
		commit:       domain.CommitUnknown,
		failureClass: domain.ErrCodeExecutionPossiblyCommitted,
	}
}

// classifyFailure maps a transport/stream failure onto the canonical commit
// decision. It is the single owner of the §7.2 proof-of-non-commit boundary, so
// the non-streaming and streaming surfaces cannot drift apart.
//
// Authoritative not-committed is claimed only when the failure PROVES the
// Provider never accepted a generation:
//
//   - the request never crossed the send boundary (a nil transport, or a body /
//     bundle parse failure before the exchange);
//   - the Provider answered with an explicit refusal that itself proves no
//     generation was created or billed (a 401 auth failure, a 403 challenge, a
//     usage_limit / rate refusal, or a refused refresh).
//
// Anything that crossed the send boundary without such proof — a raw transport
// egress error, a stream interrupt once the upstream accepted, a 200 that
// carried no stream, or an ambiguous 5xx — is possibly committed and MUST NOT
// be reported not-committed: the spine's fallback walk treats NotCommitted as
// authoritative no-commit proof, so doing so could re-attempt on another account
// and bill a second generation (#62 AC4, §7.2 rule 3).
func classifyFailure(ctx context.Context, err error, result turnResult) failureDecision {
	// A clean quota or rate refusal is authoritative no-generation ONLY while
	// nothing reached the client. Once content arrived or the context died, the
	// upstream demonstrably began work and certainty is forfeited.
	switch {
	case errors.Is(err, errQuota):
		if ctx.Err() != nil || result.sawContent {
			return unknownDecision()
		}
		return failureDecision{
			class:        domain.ChatOutcomeNotCommitted,
			commit:       domain.CommitNotCommitted,
			failureClass: domain.ErrCodeProviderQuotaExhausted,
		}
	case errors.Is(err, errRateLimited):
		if ctx.Err() != nil || result.sawContent {
			return unknownDecision()
		}
		return failureDecision{
			class:        domain.ChatOutcomeNotCommitted,
			commit:       domain.CommitNotCommitted,
			failureClass: domain.ErrCodeProviderRateLimited,
		}
	}

	// Context cancellation or content already observed forfeits certainty
	// (§6.2). The Codex surface has no documented cooperative cancel (§2.2 marks
	// cancel/abort conditionally supported with upstream cooperative cancel
	// unverified), so there is nothing to prove a stop with.
	if ctx.Err() != nil || result.sawContent {
		return unknownDecision()
	}

	// Authoritative no-commit proof, as defined above.
	switch {
	case errors.Is(err, ErrTransportUnavailable), // nil transport: never transmitted
		errors.Is(err, errProtocolDrift), // body/bundle failure before exchange
		errors.Is(err, errAuthFailed),    // 401: not authorized, no generation
		errors.Is(err, errChallenged),    // 403: refused before generation
		errors.Is(err, errRefreshFailed): // refresh refused, responses never re-sent
		return failureDecision{
			class:        domain.ChatOutcomeNotCommitted,
			commit:       domain.CommitNotCommitted,
			failureClass: canonicalFailureClass(err),
		}
	}

	// Everything else — a raw transport egress error, a post-acceptance stream
	// interrupt, errUnavailable from an ambiguous 5xx, or errNonStreamingResponse —
	// crossed the send boundary without authoritative proof of non-commit.
	return unknownDecision()
}

// chatFailureOutcome projects a failure onto the non-streaming outcome.
func chatFailureOutcome(ctx context.Context, err error, result turnResult) domain.ChatOutcome {
	decision := classifyFailure(ctx, err, result)
	return domain.ChatOutcome{
		Class:        decision.class,
		Commit:       decision.commit,
		FailureClass: decision.failureClass,
	}
}

// streamFailureOutcome projects a failure onto the streaming outcome.
//
// Once any delta reached the sink the turn is committed as far as the client is
// concerned, so commit certainty is UNKNOWN rather than not-committed — a
// fallback re-attempt would deliver a second, contradictory answer on a stream
// the client already partly consumed. UpstreamAbortAttempted stays false and
// UpstreamStopConfirmed stays false on every path: closing the local stream is
// not an upstream abort, and §6.2 rules 3-4 forbid claiming either without
// proof. The evidence marks Codex cancel/abort `conditionally supported` with
// upstream cooperative cancel unverified (§2.2, §10.1), so this Adapter has
// nothing to prove with.
func streamFailureOutcome(ctx context.Context, err error, result turnResult) domain.ChatStreamOutcome {
	decision := classifyFailure(ctx, err, result)
	return domain.ChatStreamOutcome{
		Class:        decision.class,
		Commit:       decision.commit,
		FailureClass: decision.failureClass,
	}
}

var (
	_ ports.ChatAdapter       = (*Adapter)(nil)
	_ ports.ChatStreamAdapter = (*Adapter)(nil)
)
