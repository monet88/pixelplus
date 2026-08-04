# Design

## Seams (agreed before tests)

Tests enter **only** through `POST /v1/chat/completions` with `stream: true`
over `httptest`-served production composition (`Runtime.Handler()`), and assert
on the parsed SSE event sequence plus controlled-fake observations. No test
calls a private function, a handler stub, or `application.StreamChat` directly.

## Terminal ownership is structural, not conventional

The ordering invariant `I-CHAT-STREAM-ORDER` is enforced by construction rather
than by discipline, at two levels:

1. **`domain.ChatStreamOrder`** is a pure state machine
   (`awaiting_open → open → terminal`). Every write goes through it, and it
   rejects a second `open`, a `delta`/`heartbeat` before `open` or after the
   terminal, and a second terminal. It is the single source of truth for what
   "legal next event" means and is unit-testable without HTTP.
2. **`domain.ChatSink`** — the sink handed to the Provider Adapter — exposes
   *only* `Delta` and `Heartbeat`. The Adapter has no method with which to emit
   `open` or any terminal event. A drifting or hostile Adapter therefore
   *cannot* produce two terminals or content after the terminal; the Gateway
   owns both ends of the stream.

Cause → effect: because `open`/terminal are unreachable from the Adapter, and
because the SSE writer refuses out-of-state writes via the state machine, the
only way to break ordering is to change the state machine itself — which its
own tests pin.

## Phase order (chat lifecycle §3.1) and where the stream opens

`open` is written immediately **before** Adapter entry, not at HTTP 200:

| Phase | Failure surface |
|---|---|
| A0 authenticate → A1 scope → A2 validate/digest | HTTP canonical error (no stream) |
| replay claim → A3–A5 admission | HTTP canonical error (no stream) |
| X1 route/select → X2 reaffirm → lease → X3 vault | HTTP canonical error (no stream) |
| **`open` written, 200 + `text/event-stream` committed** | — |
| X4 Adapter execution | terminal `failed` / `canceled` event |
| X5/X6 client + accounting terminal | terminal event, then settle once |

This is required, not stylistic: an HTTP status cannot be revised after the SSE
body has begun, so a pre-upstream rejection (for example
`capability_unsupported` for `chat_streaming`) must surface as a real status
code. It also directly satisfies §3.2 rule 2 — a streaming request that fails
the streaming capability gate is *rejected*, never silently answered as
non-streaming.

## Streaming is a distinct operation, not a flag

`domain.ChatOpCompletionStreaming` is a second `ChatOperation` whose
`CapabilityOperation()` returns `CapabilityOpChatStreaming` (never
`CapabilityOpChat`). Consequence: an account whose snapshot verifies `chat` but
not `chat_streaming` is filtered out of the candidate set for a streaming
request and the request fails `capability_unverified` / `capability_unsupported`
before any Vault decrypt — it is never downgraded to a non-streaming body.
Health-condition scoping reuses the same operation token, so a condition scoped
to `chat_streaming` blocks streams while leaving non-streaming `chat` alone.

## Synthetic streaming honesty

The selected account's `CapabilityFact.StreamingClass` for `chat_streaming` is
projected verbatim into the `open` event's `x_pixelplus.streaming_class`
(`real` | `synthetic`). The Gateway never rewrites `synthetic` to `real`, and
the event ordering for a synthetic stream is identical (`open` → `delta`* →
terminal) so clients render both the same way.

## Hard lease (P2) and the fallback boundary

`ports.ChatStreamLeaseStore` records the hard `chat_stream` binding for the
stream's duration when Tenant policy enables leases for that unit. Fallback to a
second account is permitted only when **both** hold:

1. the Adapter returned authoritative no-commit proof (`not_committed`), and
2. **zero** `delta` events have been written to the client.

Rule 2 is not redundant with rule 1: if any delta already reached the client,
restarting generation on another account would splice two generations into one
canonical stream (`open → deltaA → deltaB`), corrupting reconstruction even if
the Adapter claimed non-commit. Commit-unknown fails closed to terminal
`failed` and never falls back (§7.2 rule 3, `I-CHAT-RETRY-BOUNDARY`).

## Accounting

Occupancy and reservation settle exactly once against the **original**
Tenant + `client_api_key_id` via the same `chatSettlementKey` scheme as T15,
after the client terminal is delivered. The accounting terminal emits no second
client event (§6.5 rule 1). Bounded residual capacity transfer for
non-cancelable upstream is T17 (#60) and is out of scope here.
