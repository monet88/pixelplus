# Validation

## Acceptance Criteria Traceability

| AC | Status | Evidence |
|---|---|---|
| 1. Explicit cancel same-Tenant, idempotent, signals running, not stop proof | PASS | `TestChatCancelIsIdempotent`, `TestChatCancelForeignTenantReturns404`, `TestChatCancelUnknownExecutionReturns404`, `TestChatCancelRequiresAuthentication` |
| 2. Disconnect = implicit cancel, timeout distinct cause | PARTIAL | Disconnect delivery-stop test exists (T16); timeout remediation `execution_recovery` fixed; timeout contract test deferred (needs timeout-enforcing Adapter) |
| 3. Residual transitions atomically under original Tenant/key | PASS | `settleStream` X5/X6 split; `ports.ChatResidualStore` with `ErrChatResidualCapacityFull`; `TestChatCancelNonCancelableSettlesOnceConservatively` proves exactly one settle with known usage |
| 4. Concurrency/quota release only at accounting terminal | PASS | `TestChatCancelNonCancelableSettlesOnceConservatively` proves `Reconcile` called exactly once |
| 5. Cancel/disconnect/timeout cannot create replacement after commit | PASS | Registry marks terminal; cancel is idempotent; spine never re-runs on terminal |
| 6. Client observes exactly one canonical terminal | PASS | `TestChatCancelIsIdempotent` asserts exactly one terminal; cancel response is separate (no second terminal) |

## Validation Matrix

| Check | Command | Result |
|---|---|---|
| Build | `go build ./...` | PASS |
| Vet | `go vet ./...` | PASS |
| Tests | `go test ./... -count=1` | PASS (all packages) |
| Race | `go test -race ./internal/contracttest/ -run TestChatCancel` | PASS |

## Deferred

- §10.2 item 8: retained-occupancy-rejects-new-A6 test (needs concurrency-limit
  enforcement in `stubAdmissionStore`; the infrastructure is present but the
  specific test is deferred).
- §10.2 item 9: disconnect mid-stream residual test (needs incremental SSE
  reading from a blocked stream).
- §10.2 item 10: timeout contract test (needs a timeout-enforcing Adapter).
- §10.2 item 11: missing-usage accounting-fault test (needs a drain fake that
  returns unknown usage with observable audit).
- `golangci-lint` and `govulncheck` (run before PR publication).
