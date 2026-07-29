# Validation

## Proof Strategy

Use persistence integration tests for ledger/lock behavior and fresh composition instances for process-restart behavior. Provider call counters must prove absence of replacement rendering, not merely infer it from terminal state.

## Test Plan

| Layer | Cases |
| --- | --- |
| Unit | Durable cross-field invariants; stale fenced bind leaves account and revision unchanged. |
| Integration | Stale lock anchor, concurrent lock exclusion/release, incremental tail application, truncate/replace handling, replay reconciliation/conflict. |
| E2E | Fresh composition recovers a non-terminal post-payload job with zero Provider calls and observable safe terminal/status outcome. |
| Platform | Windows tests/build plus Linux cross-compile/test of advisory lock implementation. |
| Performance | Instrumented/provable reload applies only appended rows after initial restore. |
| Logs/Audit | No prompt, credentials, content, or foreign-Tenant identity enters durable/error/status projections. |

## Fixtures

- Deterministic Tenant, Client API Key, Provider Account, clock, job IDs, and fingerprints.
- Two file-store instances sharing ledger paths.
- Controlled render adapter with call counter and payload-boundary crash behavior.

## Commands

```text
gofmt -l <changed-go-files>
go -C apps/gateway build ./...
go -C apps/gateway vet ./...
go -C apps/gateway test ./... -count=1
go -C apps/gateway test -race ./... -count=1
GOOS=linux GOARCH=amd64 go -C apps/gateway test ./internal/infrastructure/persistence -run <lock tests> -c
GitNexus detect_changes(scope=compare, base_ref=main)
```

## Acceptance Evidence

Pending implementation and fresh verification.
