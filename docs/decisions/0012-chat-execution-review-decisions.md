# 0012 Chat Execution Review Decisions

Date: 2026-08-03

## Status

Accepted

## Context

The T15 non-streaming chat review (#58) found that the chat spine's candidate
gates return different canonical codes than the render spine for the same
account conditions, that the two `candidateRejection` implementations are
near-duplicates, and that several smaller chat-spine choices existed only as
code comments:

- Risk acknowledgement: chat returns `risk_ack_required`; render returns
  `account_not_usable` (remediation `ack_risk`).
- Model miss: chat returns `model_unavailable`; render returns
  `capability_unsupported`.
- An Adapter `NotCommitted` outcome with an unrecognized/empty failure class
  mapped to `execution_possibly_committed`, discarding the authoritative
  no-commit proof: the single-owner fallback walk stopped and the replay
  claim was never abandoned, so a same-key retry stuck on
  `idempotency_in_progress`.
- The synchronous chat send boundary records no durable payload-sent marker.
- `ChatService`/`RenderService` held a `HealthStore` port they never call;
  execution gates read health from the `AccountStore` snapshot.
- The protected-access chat audit event aliased `RequestID` to the execution
  id because the authorized request carried no request correlation id.

## Decision

1. The candidate-gate code divergence is intentional and pinned, not drift.
   The chat spine serves the OpenAI-compatible surface and follows the #16
   §4.2/§4.4 canonical codes (`risk_ack_required`, `model_unavailable`). The
   render spine keeps `account_not_usable`/`capability_unsupported`; its wire
   behavior is frozen by decision 0008. Each spine keeps its own
   `candidateRejection`; no shared helper is introduced, because the
   gate-to-code mapping is part of each spine's independent wire contract.
   Contract tests pin each spine's codes.
2. An Adapter `NotCommitted` outcome with an unrecognized or empty failure
   class maps to the generic `provider_rejected`, never to
   `execution_possibly_committed`. The `NotCommitted` outcome is authoritative
   no-commit proof regardless of class; escalating it would block the
   single-owner fallback walk and leak the replay claim.
3. The non-streaming chat send boundary stays a no-op marker
   (`noopChatSendBoundary`): re-attempt and occupancy semantics are owned by
   the application execution layer's single-owner walk, so no durable
   cross-request send state is required for synchronous chat.
4. The execution spines do not depend on `HealthStore`; the dead port is
   removed from `ChatService`/`RenderService`. Health gating reads the
   `AccountStore` snapshot; the epoch `HealthStore` remains owned by the
   account/credential lifecycle services.
5. Chat settlement identity includes the originating `client_api_key_id`
   (`tenant/client_api_key/execution/chat_occupancy`) per chat spec §6.5.5.
6. `AuthorizedChatRequest` carries the boundary `RequestID` so the
   protected-access audit projection records the real request correlation id
   instead of aliasing the execution id.

## Alternatives Considered

1. Share one `candidateRejection` helper across spines. Rejected because it
   couples two wire contracts that are frozen independently; a render-frozen
   code would leak into the chat surface or vice versa.
2. Align render to the chat codes. Rejected because decision 0008 freezes the
   render wire behavior; changing render codes is a breaking public change.
3. Keep mapping unclassified not-committed to `execution_possibly_committed`.
   Rejected because it discards authoritative no-commit proof, blocks the
   fallback walk, and leaks the replay claim.
4. Record a durable payload-send marker for synchronous chat. Rejected for
   non-streaming chat: the single-owner walk owns re-attempts, so the marker
   would duplicate state without a consumer. Streaming (T16) revisits the
   send boundary.

## Consequences

Positive:

- The public error surface of each spine is pinned by this decision and by
  contract tests; future reviewers can distinguish intentional divergence
  from drift.
- Unclassified not-committed failures fall back and release their replay
  claim; clients can retry with the same Idempotency-Key.
- The chat audit projection carries the real request correlation id.
- The chat settlement identity matches the §6.5.5 same-Tenant, originating-key
  wording without changing exactly-once behavior.

Tradeoffs:

- Two near-identical gate implementations must be changed in tandem when
  account conditions evolve; this decision is the drift guard.
- `provider_rejected` is less specific than the true provider state when the
  Adapter omits a failure class; Adapters should still emit safe classes.

## Follow-Up

- Durable chat replay: production composition currently defaults to the
  process-local `MemoryChatReplayStore`, so `I-CHAT-NO-DUPLICATE-EXEC` holds
  only within one process. A restart drops in-flight claims and a same-key
  retry can re-execute and double-settle. The durable chat replay ledger
  (mirror of `FileRenderReplayStore`) is tracked in #88 (T25).
