# Validation

## Acceptance Criteria Traceability

| AC | Status | Evidence |
|---|---|---|
| 1. Explicit cancel same-Tenant, idempotent, signals running, not stop proof | PASS | `TestChatCancelSignalsRunningExecution` proves the cancel signal reaches the running Adapter context; `TestChatCancelIsIdempotent`, `TestChatCancelForeignTenantReturns404`, `TestChatCancelUnknownExecutionReturns404`, `TestChatCancelRequiresAuthentication` |
| 2. Disconnect = implicit cancel, timeout distinct cause | PASS | `TestChatStreamDisconnectSettlesImplicitCancel` proves disconnect settles the implicit cancel; `TestChatStreamTimeoutYieldsDistinctFailureClass` proves `upstream_timeout` + `execution_recovery` |
| 3. Residual transitions atomically under original Tenant/key/account lease | PASS | `settleStream` X5/X6 split; `ChatResidualHold` carries `AccountID`; `ports.ChatResidualStore` with `ErrChatResidualCapacityFull`; `TestChatCancelNonCancelableSettlesOnceConservatively` proves exactly one settle with a known conservative floor |
| 4. Concurrency/quota release only at accounting terminal | PASS | `TestChatResidualExhaustedRetainsOccupancyAndRejectsNewA6` proves retained occupancy rejects a new A6 and releases exactly once at X6; `TestChatCancelNonCancelableSettlesOnceConservatively` proves `Reconcile` called exactly once |
| 5. Cancel/disconnect/timeout cannot create replacement after commit | PASS | Registry marks terminal; cancel is idempotent; spine never re-runs on terminal |
| 6. Client observes exactly one canonical terminal | PASS | `TestChatCancelIsIdempotent` asserts exactly one terminal; cancel response is separate (no second terminal) |

## Validation Matrix

| Check | Command | Result |
|---|---|---|
| Build | `go build ./...` | PASS |
| Vet | `go vet ./...` | PASS |
| Tests | `go test ./... -count=1` | PASS (all packages) |
| Race | `go test -race ./internal/contracttest/ -run TestChatCancel` | PASS |

## Coverage notes

- §10.2 item 8 (retained occupancy rejects new A6): `TestChatResidualExhaustedRetainsOccupancyAndRejectsNewA6`.
- §10.2 item 9 (disconnect implicit-cancel accounting): `TestChatStreamDisconnectSettlesImplicitCancel`.
- §10.2 item 10 (timeout distinct failure class): `TestChatStreamTimeoutYieldsDistinctFailureClass`.
- §10.2 item 11 (missing final usage -> conservative + accounting fault): `TestChatCancelNonCancelableSettlesOnceConservatively` asserts the residual accounting-fault audit action.

## Deferred

- Numeric timeout classes, drain windows, `L-TENANT-CHAT-RESIDUAL` value (#17).
- `golangci-lint` and `govulncheck` were not installed in this environment.
