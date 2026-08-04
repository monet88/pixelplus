# Overview

## Current Behavior

`POST /v1/chat/completions` serves only the non-streaming branch (T15, #58).
The route rejects `stream: true` as `invalid_request`, so a Tenant that asks
for streaming gets a validation error instead of a canonical event stream. No
part of the Gateway emits SSE, no code path resolves the `chat_streaming`
operation token, and the hard streaming account lease (routing spec §5.2,
chat lifecycle §5.3) has no implementation despite `LeaseUnitChatStream`
already existing in the frozen lease vocabulary.

Concretely, today a client sending `{"model":"gpt-4o","stream":true,...}`
receives HTTP 400 `invalid_request` because `chat.go` treats a present
`stream: true` as malformed. Nothing distinguishes an account that supports
`chat` from one that supports `chat_streaming`, so capability could not be
honestly enforced for the streaming operation even if the route existed.

## Target Behavior

- A Tenant that sets `stream: true` receives `text/event-stream` whose event
  sequence is exactly `open` → `delta`* (heartbeats allowed interleaved) →
  exactly one `completed` / `failed` / `canceled` terminal. The `open` event
  carries the server-owned execution identity **before** any content delta.
- Concatenated `delta` content reconstructs the assistant message in
  generation order. Provider wire framing (OpenAI `[DONE]`, Gemini chunk
  boundaries, Grok event names) never reaches the client.
- After the terminal event no `delta`, `heartbeat`, or second terminal is ever
  written, and there is no `[DONE]` sentinel.
- The terminal `finish_class` and the safe usage/account metadata match the
  durable execution outcome: a stream that emitted deltas and then failed ends
  `failed` (not `completed`), and usage reflects what the Adapter observed.
- Streaming resolves the `chat_streaming` capability operation, never `chat`. A
  request whose selected account cannot stream is rejected before upstream and
  is never silently downgraded to a non-streaming response.
- Synthetic streaming (Gemini Web Cookie class) is disclosed as
  `streaming_class: "synthetic"` in the `open` event's safe metadata and never
  claims Provider-native token latency.
- The selected account lease is hard for the stream's duration: once the stream
  holds a lease the Gateway does not hop accounts, and a possibly-committed
  attempt is never replaced by a fallback account.

## Affected Users

- Tenant clients consuming streaming chat over the OpenAI-compatible contract.
- Tenant operators whose Routing Policy enables leases for `chat_stream`.
- Gateway operators relying on canonical terminal classes for observability.

## Affected Product Docs

- `docs/spec/chat-execution-and-streaming-lifecycle.md` (§3.2, §4.3–§4.5,
  §5.3–§5.4, §6.5, §7.2, §11)
- `docs/spec/tenant-scoped-routing-fallback-affinity-leases.md` (§4.1, §5.2,
  §5.4)
- `docs/spec/capability-snapshot-and-model-availability-semantics.md` (§3.1
  `chat_streaming`, §4.3 synthetic)
- `contracts/openapi/pixelplus-public-api-v1.yaml` (`ChatStreamEvent`,
  `ChatOpenEvent`, `ChatDeltaEvent`, `ChatHeartbeatEvent`,
  `ChatCompletedEvent`, `ChatFailedEvent`, `ChatCanceledEvent`,
  `ChatSafeMetadata`)

## Non-Goals

- The explicit cancel route `POST /v1/chat/executions/{id}/cancel` and the
  bounded residual-tracking protocol (T17, #60). This slice terminates a
  disconnected/canceled stream honestly but does not implement residual
  capacity transfer or the cancel endpoint.
- Real Provider streaming Adapters (T18–T23) and numeric timeout/heartbeat
  budgets (#17). Heartbeats are emitted when the Adapter yields them; the
  Gateway does not invent a timer schedule here.
- Durable chat replay ledger (T25, #88) and resumable streams
  (`D-CHAT-RESUME`).
