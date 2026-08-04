# Exec Plan

## Goal

Deliver issue #59 (Gateway T16): an authenticated Tenant consumes canonical
chat streaming whose events stay Provider-independent and ordered `open` →
`delta`/`heartbeat`* → exactly one terminal, proven through the public HTTP
seam over real composition.

## Scope

In scope:

- `domain.ChatOpCompletionStreaming` operation token mapping to the
  `chat_streaming` capability operation and the `chat.completions` scope.
- `domain.ChatStreamEvent` canonical event vocabulary plus
  `domain.ChatStreamOrder`, the ordering state machine that makes a second
  terminal or post-terminal content structurally impossible.
- `domain.ChatSink` — delta/heartbeat-only Adapter sink (Adapters cannot emit
  `open` or terminals).
- `ports.ChatStreamAdapter`, `ports.AuthorizedChatStream`, and
  `ports.ChatStreamLeaseStore` (hard P2 `chat_stream` lease).
- `application.ChatService.StreamChat`: the full gate sequence, `open` before
  first delta, terminal mapping, delta-aware fallback boundary, affinity
  record, and exactly-once settlement.
- SSE transport: `text/event-stream` writer with flush-per-event, disconnect
  detection, and pre-stream canonical errors as real HTTP statuses.
- Composition wiring with a fail-closed production default streaming Adapter.
- Public-seam contract tests covering all six acceptance criteria.

Out of scope:

- Cancel route and bounded residual tracking (T17, #60).
- Real Provider streaming Adapters (T18–T23), numeric heartbeat/timeout
  budgets (#17), durable replay ledger (T25, #88), resumable streams.

## Risk Classification

Risk flags:

- Authorization (Tenant-scoped selection, lease, non-enumeration on the stream
  path).
- Audit/security (audit-before-allow on a new protected execution boundary).
- External systems (new Provider Adapter seam).
- Public contracts (new SSE response media type and event schemas).
- Existing behavior (`stream: true` changes from 400 to a served stream).
- Multi-domain (domain, ports, application, transport, composition).

Hard gates: Authorization, Audit/security, External provider behavior.

Lane: high-risk.

## Work Phases

1. Domain: streaming operation token, canonical event vocabulary, ordering
   state machine, delta-only sink. Unit tests for the state machine first.
2. Ports: streaming Adapter, authorized stream boundary, lease store, audit
   actions.
3. Application: `StreamChat` spine — gates, lease, `open`-before-delta,
   terminal mapping, delta-aware fallback, settle-once.
4. Transport: SSE writer bound to the ordering state machine; route branches on
   `stream`.
5. Composition: wire the streaming seam with fail-closed production defaults.
6. Contract tests at the public seam for AC1–AC6.
7. Full validation matrix (`go build`, `go vet`, `go test ./...`, `-race`,
   `golangci-lint`, `govulncheck`), then `/code-review`, then commit.
