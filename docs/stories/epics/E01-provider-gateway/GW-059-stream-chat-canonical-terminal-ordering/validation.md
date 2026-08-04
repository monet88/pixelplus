# Validation

## Proof seam

Every acceptance test issues a real `POST /v1/chat/completions` with
`stream: true` against production composition served over `httptest`, then
parses the `text/event-stream` body into an ordered event list. Adapter event
sequences, Clock, cancellation, lease, and accounting are controlled through
injected fakes. Private functions, handler stubs, direct use-case calls, and
goroutine-layout assertions are not accepted as evidence.

## Acceptance criteria → tests

| AC | Test |
|---|---|
| Every accepted stream emits one `open` with server-owned execution identity before content delta | `TestChatStreamEmitsOpenBeforeFirstDelta` |
| Zero or more canonical delta and allowed heartbeat events follow, without Provider wire events | `TestChatStreamDeltasReconstructMessageWithoutProviderFraming`, `TestChatStreamHeartbeatCarriesNoContent`, `TestChatStreamZeroDeltaCompletesCleanly` |
| Exactly one terminal ends the stream, with no data or second sentinel afterward | `TestChatStreamExactlyOneTerminalNoPostTerminalData`, `TestChatStreamRogueAdapterCannotWriteAfterTerminal`, `TestChatStreamFailureAfterDeltasIsFailedTerminal` |
| Finish class and safe usage/account metadata match the durable execution outcome | `TestChatStreamCompletedCarriesFinishClassAndUsage`, `TestChatStreamTerminalMatchesDurableOutcome` |
| — same AC, honesty direction: a non-cancelable upstream must not be reported as aborted | `TestChatStreamCanceledDoesNotClaimUnattemptedAbort` |
| Synthetic streaming is disclosed and does not claim Provider-native streaming | `TestChatStreamSyntheticClassDisclosed`, `TestChatStreamNeverDowngradesToNonStreaming` |
| The selected account lease remains hard and cannot be silently replaced after commit or uncertainty | `TestChatStreamLeaseBindsSingleAccount`, `TestChatStreamNoFallbackAfterDeltaEmitted`, `TestChatStreamNoFallbackWhenCommitUnknown`, `TestChatStreamTransportErrorNeverFallsBack` |

## Supporting invariant tests

- Domain state machine (`internal/domain/chat_stream_test.go`): delta before
  `open` and a second `open` are refused by
  `TestChatStreamOrderRequiresOpenBeforeContent`; a second terminal and
  post-terminal data are refused by
  `TestChatStreamOrderAdmitsExactlyOneTerminal`; a terminal on an unopened
  stream is refused by `TestChatStreamOrderTerminalRequiresOpenStream`;
  `TestTerminalEventForFinishClass` and
  `TestChatStreamEventTypeTerminalClassification` pin the finish-class → terminal
  mapping.
- `TestChatStreamCapabilityGateUsesStreamingOperation` (a `chat`-only account
  is not eligible for a stream; rejection is a real HTTP status, no partial
  stream).
- `TestChatStreamSettlesAccountingOnce` (one settlement key against the
  original Tenant + Client API Key).
- `TestChatStreamClientDisconnectStopsDelivery` and
  `TestChatStreamLateAdapterWriteAfterHandlerReturn` (client disconnect stops
  delivery, and a leaked Adapter goroutine cannot touch the `ResponseWriter`
  after the handler returned).
- `TestChatStreamTimestampsComeFromControlledClock` (every event timestamp comes
  from the injected Clock, never `time.Now()`).
- `TestChatStreamFallbackAcceptsStreamingOnlyTarget` and
  `TestChatStreamFallbackTargetMustSatisfyStreamingCapability`
  (`internal/contracttest/chat_stream_fallback_test.go`: pre-open fallback vets
  candidates against the streaming capability, not the non-streaming one).
- `TestChatStreamFailsClosedWithoutStreamingAdapter` (an uncomposed streaming
  Adapter fails closed as an HTTP status, never as a downgraded answer).
- `TestChatStreamAuditUsesStreamingActions` (the streaming spine emits
  `chat_completion.stream_opened` and `chat_completion.stream_terminal`, never
  the non-streaming `chat_completion.completed`).

## Review findings fixed in this slice

Each item was proved by a failing test first, then fixed:

1. **Transport error triggered fallback** (§7.2 rules 2 and 4, §7.5
   `I-CHAT-NO-DUPLICATE-EXEC`). An Adapter transport error with zero deltas was
   treated as authoritative non-commit, so the walk attempted a second account
   and the client received HTTP 200 carrying a *second* generation while the
   first may already have been billed. The attempt now carries an
   `observedChatSendBoundary`: once payload transmission began, the attempt is
   `possibly_committed` and fails closed on that account. Proof:
   `TestChatStreamTransportErrorNeverFallsBack`.
2. **`upstream_abort_attempted` was hardcoded true** (§6.2 rule 4 "The Gateway
   MUST NOT claim it was aborted"). The bit was derived from the terminal class
   rather than observed, so a non-cancelable upstream published `true` while the
   generation kept running. Both cancel bits are now Adapter observations on
   `domain.ChatStreamOutcome`. Proof:
   `TestChatStreamCanceledDoesNotClaimUnattemptedAbort`.
3. **Streaming audit used the non-streaming action.** `chatAudit` hardcoded
   `chat_completion.completed`, so the declared streaming audit actions were
   never emitted and a stream terminal was recorded as a synchronous completion.
   The action is now derived from the spine operation. Proof:
   `TestChatStreamAuditUsesStreamingActions`.

## Known limitations (not introduced by this slice)

- **Final usage is not reconciled into admission.** `ports.AdmissionReservation`
  carries no usage field, so `terminal.Usage` reaches the SSE event and the
  replay record but not the `AdmissionStore`. §6.5 rule 3 ("Reconcile the A6
  reservation at X6 to final actual input+output usage") is therefore only
  partially satisfied. This is shared with the T15 non-streaming spine and needs
  its own ticket.
- **The same transport-error/fallback gap exists in the T15 non-streaming
  spine** (`attemptOnAccount` passes `noopChatSendBoundary`, so `attemptWalk`
  cannot distinguish a pre-send failure from a possibly-committed one). Out of
  scope here; needs its own ticket.
- `golangci-lint run` reports 8 findings and `govulncheck ./...` reports 12
  stdlib findings (`go1.25.5`, fixed in `go1.25.7`). Both sets are identical on
  `main` and none touch this slice's files.

## Command matrix

Run from `apps/gateway`:

- `go build ./...`
- `go vet ./...`
- `gofmt -l .` (must print nothing)
- `go test ./...`
- `go test -race ./internal/contracttest/... ./internal/composition/... ./internal/domain/...`
- `golangci-lint run`
- `govulncheck ./...`

Run from the repository root:

- `python scripts/check-chat-stream-wire.py` (every SSE wire struct field is
  declared by the frozen `additionalProperties: false` schema, and every
  `required` schema field is present)

## Status

Recorded per run in the story trace; no criterion may be reported as met
without fresh command output in the same session.
