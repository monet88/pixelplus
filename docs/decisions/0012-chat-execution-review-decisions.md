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
- `RenderService` held a `HealthStore` port it never calls; its execution gates
  read health from the `AccountStore` snapshot. `ChatService` does call the
  authoritative `HealthStore` (scoped conditions, ADR 0009).
- The protected-access chat audit event aliased `RequestID` to the execution
  id because the authorized request carried no request correlation id.

A second review round (PR #87, two-axis) added: the explicit pin failed
foreign/unknown ids with `routing_no_candidate` (409) instead of the locked
non-enumerating 404-class; the fallback walk never consulted
`fallback_auth_modes` (NF-XMODE); the request wire rejected contract-declared
`ChatCompletionRequest` fields as unknown; the documented `conversation_id`
affinity key was parsed then dropped; and this record's item 4 and Follow-Up
contradicted the shipped code.

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
4. Only `RenderService`'s dead `HealthStore` port is removed; render gates
   read the `AccountStore` snapshot. `ChatService` keeps and requires the
   authoritative `HealthStore` (ADR 0009): scoped health conditions and
   recovery permits are HealthStore authority, never the AccountStore copy,
   and missing health evidence fails closed.
5. Chat settlement identity includes the originating `client_api_key_id`
   (`tenant/client_api_key/execution/chat_occupancy`) per chat spec §6.5.5.
6. `AuthorizedChatRequest` carries the boundary `RequestID` so the
   protected-access audit projection records the real request correlation id
   instead of aliasing the execution id.
7. A foreign or unknown explicit pin (`x_pixelplus.provider_account_id`)
   resolves to `resource_not_found` (404-class, non-enumerating) before
   candidate construction, with zero Adapter calls and zero Vault decrypts
   (routing spec §4.1 P1, §3.2, §7.2 NF-XTENANT; chat spec §8 rule 1). A
   visible same-Tenant pin still passes C0–C5 and fails with the specific
   gate class, never 404.
8. Cross-Auth-Mode fallback is permitted only when the Tenant policy's
   `fallback_auth_modes` names BOTH the primary's and the target's mode
   (routing spec §6.2 sentence 2, §7.1 NF-XMODE); otherwise the walk fails
   closed on the primary's own outcome. Same-mode fallback moves between
   accounts, not modes, so it needs no listing.
9. The replay claim deliberately precedes admission (A3–A5). Canonical-errors
   §7.2.3 forbids a terminal replay from creating a new admission reservation
   or quota debit, which only claim-first guarantees; the claim loser holds
   no A6 slot, so chat spec §7.3.4's release-once premise is vacuously
   satisfied while the claim owner settles exactly once on every path. This
   mirrors the render spine order.
10. The request wire accepts every field the published `ChatCompletionRequest`
    / `ChatMessage` schemas declare. Generation tuning fields (`temperature`,
    `max_tokens`, `top_p`, `n`, `stop`, `user`, message `name`) are
    shape-validated at the boundary (parse-first) but not carried into the
    canonical command until real Provider Adapters consume them (T19–T23).
    Array `content` is canonicalized by concatenating text parts; a non-text
    part rejects `invalid_request` because silently dropping it would corrupt
    the prompt.
11. Conversation affinity (P3) is implemented as a policy-gated
    (`affinity.enabled`) soft preference over `conversation_id`, recorded on
    committed success and yielding whenever the preferred account leaves the
    candidate set. The store is process-local in-memory: affinity is a
    preference, never an authority (routing spec §5.1 rule 1), so process
    loss degrades to P4 policy selection — unlike the replay ledger it needs
    no fail-closed default. The durable store and the affinity window-class
    numeric remain deferred (#17 tunables; T25/#88 durable ledger).

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

- Durable chat replay: production composition wires the fail-closed
  `UnavailableChatReplayStore`; the process-local `MemoryChatReplayStore`
  serves only fixtures / explicit `AllowInMemoryChat` mode, so
  `I-CHAT-NO-DUPLICATE-EXEC` holds only within one process there. The durable
  chat replay ledger (mirror of `FileRenderReplayStore`) is tracked in #88
  (T25). The durable affinity store and window-class numeric follow the same
  ticket wave (item 11).
- Render has no fallback walk today, so `fallback_auth_modes` is unenforced
  there; if render gains fallback it must apply item 8's rule.
