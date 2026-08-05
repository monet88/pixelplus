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

`settleStream` determines whether X5 and X6 coincide or split:

| Terminal | Upstream stopped? | Path |
|---|---|---|
| `completed` | Yes (natural) | X5 = X6: reconcile now |
| `canceled` + `UpstreamStopConfirmed` | Yes (confirmed) | X5 = X6: reconcile now |
| `canceled` without `UpstreamStopConfirmed` | Maybe | X5 != X6: hold + drain + settle |
| `failed` non-commit | Yes (never reached) | X5 = X6: reconcile now |
| `failed` possibly_committed | Maybe | X5 != X6: hold + drain + settle |

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
usage is treated as a known conservative floor: when the drain cannot confirm
final usage, the spine settles to the observed floor (a debit no smaller than
known usage) and still emits an operator-visible accounting fault. When no
observed floor exists, it retains the full reservation. Either way it never
assumes zero (§6.5 rule 3).

For the pre-upstream rejection (`!opened`): settle immediately. There is no
stream to drain; the HTTP error is the client terminal and the accounting
terminal in one.

## Accounting fault

When the drain returns unknown usage, `settleStream` returns a non-nil error.
The caller folds it into the audit trail as an `_accounting_fault` suffix on
the terminal outcome, matching the existing pattern in
`recordStreamTerminalState`. The reservation is NOT reconciled to zero; the
admission store's own fail-closed contract retains it (§6.5 rule 3).

## Remediation fix

`NewUpstreamTimeout` and `NewUpstreamUnavailable` previously carried
`RemediationNone`. The spec (§4.5) requires `execution_recovery` for both. This
is the distinct timeout remediation class §6.4 requires.

## Composition wiring

`ResidualStore` and `ResidualDrain` are optional dependencies on
`composition.Dependencies`. A nil store means no residual capacity is
available (original state retained). A nil drain means unknown usage
immediately (fail closed). The fixture `Options` gains the same two ports.
