# Design

## Seams (agreed before tests)

Tests enter only through:

- `POST /v1/chat/completions` with `stream: true` (public SSE stream)
- `POST /v1/chat/executions/{execution_id}/cancel` (public cancel route)

over `httptest`-served production composition (`Runtime.Handler()`). No test
calls a private function, a handler stub, or `application.CancelChatExecution`
directly.

## In-flight execution registry

`chatExecutionRegistry` is a process-local, mutex-guarded map of
`execution_id` -> `chatExecutionHandle`. `StreamChat` registers the execution
before the Adapter call and marks it terminal after settlement. The cancel
route looks up by `execution_id` and checks same-Tenant ownership: a mismatch
returns the same 404 as unknown (non-enumeration).

The registry is intentionally NOT durable: a cancel can only target a live
execution in this process. After terminal, the handle stays in the registry as
terminal so a later cancel is an idempotent no-op (§6.2 rule 5), but only for a
bounded 60-second retention window. Expired terminal entries are reaped on each
register/cancel and removed via `unregister`, so the process-local map never
grows without limit while a client still has a reasonable window to re-cancel.

## Cancel signal: context cancellation

The cancel route calls the registered `context.CancelFunc`, which cancels the
child context. `StreamChat` passes that child context into `runStream` ->
`attemptStreamOnAccount` -> `authorizedStream.Stream` ->
`ChatStreamAdapter.Stream`, so the signal reaches the running Adapter (a review
fix: the child context was previously created and discarded). The Adapter sees
a canceled context and returns a `canceled` outcome. The cancel response is an
honest acknowledgement: `cancel_requested` with
`upstream_abort_attempted: true` and `upstream_stop_confirmed: false`.

Disconnect is the same signal: the request context cancels the child, which
cancels the Adapter context (§6.3 rule 1).

## X5/X6 settlement split

`settleStream` determines whether X5 and X6 coincide or split. `upstreamStopped`
is the single predicate that decides:

| Terminal | Upstream stopped? | Path |
|---|---|---|
| `completed` | Yes (natural) | X5 = X6: reconcile now |
| `canceled` + `UpstreamStopConfirmed` | Yes (confirmed) | X5 = X6: reconcile now |
| `canceled` without `UpstreamStopConfirmed` | Maybe | X5 != X6: hold + drain + settle |
| `failed` pre-upstream rejection | Yes (never reached) | X5 = X6: reconcile now |
| `failed` `upstream_timeout` | Maybe | X5 != X6: hold + drain + settle |
| `failed` `upstream_unavailable` | Maybe | X5 != X6: hold + drain + settle |
| `failed` `upstream_protocol_drift` | Maybe | X5 != X6: hold + drain + settle |
| `failed` `execution_possibly_committed` | Maybe | X5 != X6: hold + drain + settle |

Commit status is what decides a `failed` terminal, never `UpstreamAbortAttempted`:
that flag is only ever populated for `canceled` terminals, so testing it on a
`failed` one classifies every failure as stopped. A timeout in particular MUST
take the residual path — §6.4 rule 2 requires the Gateway to attempt an abort,
and §6.4 rule 3 applies the same tracking and accounting rules as cancel. An
Adapter-observed `UpstreamStopConfirmed` overrides the conservative default on
any terminal.

For the split path:

1. Try `residualStore.Acquire(hold)` where `hold` also carries the serving
   `AccountID` (AC3: residual work stays bound to the original account lease).
   If `ErrChatResidualCapacityFull`, retain the original request state
   (occupancy stays held; §6.5 rule 2).
2. Run `residualDrain.Drain(ctx, request)`. A nil drain returns unknown usage
   immediately, so settlement fails closed.
3. At X6: reconcile quota to final usage (or fail closed if unknown), release
   residual hold if acquired, release occupancy via `Reconcile`.

Because the drain is the only source of FINAL usage, the terminal's observed
usage is never trustworthy for settlement of a surviving upstream: when the drain
cannot confirm final usage, the spine RETAINS the full reservation (usage handed
to Reconcile is unknown, `Known=false`) and emits an operator-visible accounting
fault. This fail-closed choice never over-refunds the surviving upstream's unknown
remainder. The audit outcome distinguishes an accounting fault (FINAL usage
unknown, reservation retained) from a dependency fault (Reconcile itself failed),
so operators can triage them apart (review finding 5). It never assumes zero
(§6.5 rule 3).

For the pre-upstream rejection (`!opened`): settle immediately. There is no
stream to drain; the HTTP error is the client terminal and the accounting
terminal in one.

## Settlement context

Everything after the client terminal is accounting work, and accounting must
outlive the client. The request context is cancelled by a disconnect, so
settlement runs on `context.WithTimeout(context.WithoutCancel(ctx),
chatSettlementBudget)`:

- `WithoutCancel` keeps request-scoped values while detaching cancellation, so a
  disconnect cannot abort `Reconcile`. Running settlement on the request context
  handed an already-cancelled context to every ledger write, which failed and
  retained the Tenant+key occupancy forever — the untracked work §6.3 rule 2
  forbids.
- The timeout replaces the bound that cancellation used to provide: a detached
  context with no ceiling would let a hung drain pin a goroutine and its
  occupancy indefinitely. Reaching the budget yields unknown usage, which fails
  accounting closed (§6.5 rule 3).

The durable/observability writes on the same side of the client terminal —
`chatAudit`, `recordStreamTerminalState`, telemetry, request log — run on the
same detached context, so a disconnect loses neither the replay record nor the
audit trail.

`chatSettlementBudget` is the spine's conservative default until the #17
drain/recovery deadline lands.

## Accounting fault

When the drain returns unknown usage, `settleStream` returns a non-nil error.
`recordStreamTerminalState` records that outcome under the
`chat_completion.residual_settled` audit action with an `_accounting_fault`
suffix on the outcome label, so an operator filtering on the action finds it
(§6.5 rule 3). The reservation is NOT reconciled to zero; the admission store's
own fail-closed contract retains it.

Audit actions are passed to `chatAudit` as typed `ports.ChatAuditAction` values
rather than inferred from the outcome string. The outcome is a free-form label
assembled per call site, so deriving the action from its text made the audit
trail depend on string formatting in a distant file; a typed parameter lets the
compiler catch a mislabeled event.

## Remediation fix

`NewUpstreamTimeout` and `NewUpstreamUnavailable` previously carried
`RemediationNone`. The spec (§4.5) requires `execution_recovery` for both. This
is the distinct timeout remediation class §6.4 requires.

## Composition wiring

`ResidualStore` and `ResidualDrain` are optional dependencies on
`composition.Dependencies`. A nil store means no residual capacity is
available (original state retained). A nil drain means unknown usage
immediately (fail closed). The fixture `Options` gains the same two ports.

Neither port is wired in `cmd/gateway` yet, so production runs both nil. That is
the safe direction — retained occupancy plus a full reservation and an accounting
fault, never an over-refund — but it means residual capacity is unusable and
post-X5 tokens are not reconciled to actual usage until real implementations
land. The bounded store needs the #17 `L-TENANT-CHAT-RESIDUAL` numeric.
