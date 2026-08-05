# Validation

## Acceptance Criteria Traceability

| AC | Status | Evidence |
|---|---|---|
| 1. Explicit cancel same-Tenant, idempotent, signals running, not stop proof | PASS | `TestChatCancelSignalsRunningExecution` proves the cancel signal reaches the running Adapter context; `TestChatCancelIsIdempotent`, `TestChatCancelForeignTenantReturns404`, `TestChatCancelUnknownExecutionReturns404`, `TestChatCancelRequiresAuthentication` |
| 2. Disconnect = implicit cancel, timeout distinct cause | PASS | `TestChatStreamDisconnectSettlesImplicitCancel` proves disconnect settles the implicit cancel; `TestChatStreamDisconnectSettlesOnDetachedContext` proves settlement is not aborted by the disconnect and releases occupancy; `TestChatStreamTimeoutYieldsDistinctFailureClass` proves `upstream_timeout` + `execution_recovery`; `TestUpstreamStoppedClassification` proves a timeout routes to the residual path per §6.4 rule 3 |
| 3. Residual transitions atomically under original Tenant/key/account lease | PASS | `settleStream` X5/X6 split; `ChatResidualHold` carries `AccountID`; `ports.ChatResidualStore` with `ErrChatResidualCapacityFull`; `TestChatCancelNonCancelableSettlesOnceConservatively` proves exactly one settle and that, when the bounded drain cannot confirm FINAL usage, the reservation is RETAINED IN FULL (usage unknown, `Known=false`) with an operator-visible accounting fault (§6.5 rule 3); `TestChatStreamDisconnectSettlesOnDetachedContext` proves the hold is acquired and released exactly once, bound to the originating Tenant and serving account |
| 4. Concurrency/quota release only at accounting terminal | PASS | `TestChatCancelResidualExhaustedRetainsOccupancyAndRejectsNewA6` proves retained occupancy rejects a new A6 and releases exactly once at X6; `TestChatCancelNonCancelableSettlesOnceConservatively` proves `Reconcile` called exactly once; `TestChatStreamDisconnectSettlesOnDetachedContext` proves occupancy reaches zero after a disconnect rather than leaking |
| 5. Cancel/disconnect/timeout cannot create replacement after commit | PASS | Registry marks terminal; cancel is idempotent; spine never re-runs on terminal |
| 6. Client observes exactly one canonical terminal | PASS | `TestChatCancelIsIdempotent` asserts exactly one terminal; cancel response is separate (no second terminal) |

## Validation Matrix

| Check | Command | Result |
|---|---|---|
| Build | `go build ./...` | PASS |
| Vet | `go vet ./...` | PASS |
| Tests | `go test ./... -count=1` | PASS (all packages) |
| Race | `go test -race ./internal/contracttest/ ./internal/application/ -count=1` | PASS |

## Review-fix regressions

Findings from the PR #93 review, each pinned by a test that fails without the fix:

- **Settlement ran on the cancelled request context.** A disconnect cancelled the
  context that `settleStream` used, so `Reconcile` failed and the Tenant+key
  occupancy was retained forever (§6.3 rule 2, §6.5 rule 4). Fixed by detaching
  settlement with `context.WithoutCancel` under `chatSettlementBudget`. Pinned by
  `TestChatStreamDisconnectSettlesOnDetachedContext`, which asserts the drain and
  residual store never see a cancelled context and that occupancy returns to zero.
  The admission stubs (`stubAdmissionStore`, `limitAdmissionStore`) now return
  `ctx.Err()` on a cancelled context, as a datastore-backed store would; without
  that they reported success for writes production would have rejected.
- **`upstreamStopped` misclassified timeouts.** It keyed off
  `UpstreamAbortAttempted`, which is only populated for `canceled` terminals, so
  every `failed` terminal — including `upstream_timeout` — settled as if upstream
  were dead, skipping the residual protocol §6.4 rule 3 requires. Pinned by
  `TestUpstreamStoppedClassification` and
  `TestUpstreamStoppedIgnoresAbortFlagOnFailedTerminals`.
- **Audit actions were routed by string matching.** `chatAudit` now takes a typed
  `ports.ChatAuditAction`; the `strings.HasPrefix`/`HasSuffix` inference over
  manually concatenated outcome labels is gone.
- **A residual-store outage was treated as capacity exhaustion.** `settleStream`
  took the `ErrChatResidualCapacityFull` "retain the original request state" path
  for ANY `Acquire` error, so a store outage settled the reservation and closed
  the books as if the surviving upstream were tracked, when no residual hold
  existed at all. Only `errors.Is(err, ports.ErrChatResidualCapacityFull)` now
  takes that path; every other error surfaces a dependency fault. Pinned by
  `TestChatResidualStoreOutageSurfacesDependencyFault`.
- **A failed X6 reconciliation still released the residual hold.** The hold is the
  only remaining record of a surviving upstream, so releasing it while the
  reservation is unsettled handed that bounded slot to another execution
  (§6.5 rule 4 releases occupancy and residual tracking together, AT the
  accounting terminal). The hold is now retained unless `Reconcile` succeeded, and
  a failing `Release` is surfaced rather than swallowed. Pinned by
  `TestChatResidualHoldRetainedWhenReconcileFails`.
- **The P2 lease release ran on an already-cancelled context after all.** The
  previous fix pointed the release defer at `settleCtx`, but defers run LIFO and
  `settleCancel` is registered AFTER that defer, so it fired first: the release
  always saw a cancelled context, and a resilient lease store would have rejected
  the write and leaked the account binding until its TTL. The release now detaches
  its own context under `chatSettlementBudget`. Pinned by
  `TestChatStreamDisconnectReleasesLeaseOnDetachedContext`, which previously could
  not catch this: the harness default leaves `LeasePolicy` disabled, so no lease
  existed and the guard passed vacuously, and it synchronized on occupancy
  (zeroed by `Reconcile`, inside `settleStream`) rather than on the release. It
  now enables the lease policy and polls until the release has actually happened.

## Coverage notes

- §10.2 item 8 (retained occupancy rejects new A6): `TestChatCancelResidualExhaustedRetainsOccupancyAndRejectsNewA6`.
- §10.2 item 9 (disconnect implicit-cancel accounting): `TestChatStreamDisconnectSettlesImplicitCancel`.
- §10.2 item 10 (timeout distinct failure class): `TestChatStreamTimeoutYieldsDistinctFailureClass`.
- §10.2 item 11 (missing final usage -> retained reservation + accounting fault): `TestChatCancelNonCancelableSettlesOnceConservatively` asserts the residual accounting-fault audit action with the distinct `_accounting_fault` outcome (review finding 5), proving the drain-unable path retains the full reservation and fails closed rather than over-refunding the surviving upstream's unknown remainder.

## Deferred

- Numeric timeout classes, drain windows, `L-TENANT-CHAT-RESIDUAL` value (#17).
  `chatSettlementBudget` is a conservative spine default until those land.
- `ResidualStore` and `ResidualDrain` are not wired in `cmd/gateway`, so
  production runs both nil: residual capacity is unusable and post-X5 tokens are
  not reconciled to actual usage. The nil behavior is fail-closed (retained
  occupancy, full reservation, accounting fault) and never over-refunds.
- `golangci-lint` and `govulncheck` were not installed in this environment.
