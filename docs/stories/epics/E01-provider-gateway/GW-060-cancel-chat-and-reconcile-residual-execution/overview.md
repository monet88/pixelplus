# Overview

## Current Behavior

`POST /v1/chat/completions` serves both non-streaming and streaming chat (T15,
T16). The published contract also defines `POST
/v1/chat/executions/{execution_id}/cancel` (`cancelChatExecution`,
`ChatCancelResponse`), but no handler, application method, or wire type
exists for it. Cancellation is observed only through the stream terminal event
(`canceled`), and disconnect is tested for delivery-stop only.

The streaming spine settles occupancy and quota at exactly one point: it calls
`admission.Reconcile` once, unconditionally, regardless of whether the upstream
may still be running. There is no X5/X6 split (§6.5 rule 1): a non-cancelable
upstream whose client terminal is `canceled` releases concurrency immediately,
even though the upstream generation may keep consuming Provider quota. There is
no residual tracking port, no bounded drain, and no accounting-fault marker for
unavailable final usage.

## Target Behavior

- `POST /v1/chat/executions/{execution_id}/cancel` returns the honest
  `ChatCancelResponse`: `cancel_state` is `cancel_requested` when the execution
  is running and has been signaled, or `canceled` when it is already terminal
  (idempotent no-op, §6.2 rule 5). `upstream_abort_attempted` reports whether
  the Gateway attempted to signal the upstream. `upstream_stop_confirmed` is
  false unless the Adapter proved a stop (§6.2 rule 3).
- Unknown, foreign, and already-unregistered `execution_id` all return the same
  404 `resource_not_found` (non-enumeration, §8).
- The X5/X6 split: when the upstream stopped confirmed (completed, canceled +
  stop_confirmed, or authoritative non-commit), occupancy and quota settle
  immediately at X5. When the upstream may survive (canceled without
  stop_confirmed, or possibly_committed), the reservation is held, residual
  tracking is acquired atomically, a bounded drain runs, and settlement occurs
  at X6. If the drain returns unknown usage, settlement fails closed (retain
  full reservation, emit accounting fault; §6.5 rule 3).
- `upstream_timeout` and `upstream_unavailable` carry the `execution_recovery`
  remediation (§4.5), the distinct timeout class §6.4 requires.
- Cancel is same-Tenant: a Tenant B cancel of Tenant A's execution returns the
  same 404 as unknown (§6.5 rule 5, I-CHAT-OWNERSHIP).

## Affected Users

- Tenant clients canceling in-flight chat executions.
- Gateway operators relying on conservative accounting for surviving upstream.
- Reviewers verifying the X5/X6 split and bounded residual protocol.

## Affected Product Docs

- `docs/spec/chat-execution-and-streaming-lifecycle.md` (§6.2–§6.6, §10.2,
  §11)
- `docs/spec/canonical-errors-and-retry-ownership.md` (§4.5, §4.7, §5.2–§5.3)
- `contracts/openapi/pixelplus-public-api-v1.yaml` (`cancelChatExecution`,
  `ChatCancelResponse`)

## Non-Goals

- Numeric timeout classes, heartbeat interval, drain windows, and the numeric
  value of `L-TENANT-CHAT-RESIDUAL` (#17). Only named classes + bounded
  observable behavior belong here.
- Real Provider cancel/abort API integration (T18–T23).
- Durable replay ledger for cancel state (T25, #88).
- Resumable streams (`D-CHAT-RESUME`).
