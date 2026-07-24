# Validation

## Proof Strategy

The durable behavior is proven through the same boundaries production uses:

- Public HTTP requests enter `composition.Runtime.Handler()` through
  `contracttest.NewFixture`.
- Safe queue references enter `Runtime.RunWorkers` and the exported
  `application.JobExecutor`.
- Controlled ports expose only safe observations and side-effect counts.

Private handlers, direct use-case calls, concrete persistence queries, Provider
SDK shapes, and goroutine-layout assertions are not acceptance evidence.

## Test Plan

| Layer | Cases |
| --- | --- |
| Unit | State transitions, commit certainty, fencing, manifest/output invariants, fingerprint stability. |
| Integration | Public create/read/cancel/retry, replay/conflict/uncertainty, gate order, routing/lease, Vault/Adapter/Asset side-effect counts. |
| E2E | Public HTTP creation followed by exported worker execution and retrieval of completed job with durable output Asset. |
| Platform | Pure-Go build and architecture import policy on Windows. |
| Performance | Bounded candidate/retry chains; no performance benchmark required for the in-memory controlled slice. |
| Logs/Audit | Secret-free request log, audit, telemetry, queue projection, and job status. |

## Fixtures

- Two deterministic Tenants and Client API Keys with scoped permissions.
- Same-Tenant active Provider Account and a foreign account.
- Fresh offerable capability facts plus stale/unsupported variants.
- Controlled health/circuit/routing/Vault/render/job/Asset ports.
- Controlled clock and IDs.
- Render outcomes for success, authoritative pre-commit rejection, committed
  result, uncertain commit, multi-output, and placement-cap failure.

## Commands

Run from `apps/gateway` unless the command uses `go -C`:

```text
gofmt -w <changed-go-files>
go build ./...
go vet ./...
go test ./...
go test -race ./...
```

Harness verification command:

```text
go -C apps/gateway test ./...
```

Before commit:

```text
GitNexus detect_changes(scope=compare, base_ref=main)
```

## Acceptance Evidence

Standards P1-A/B/C security capability (credential authorizer, audit-before-allow,
recovery-before-ready) is implemented on `feature/issue-54-routed-render-jobs`
with focused package tests green. Remaining queue/E2E hardening (nonblocking
publication, `controlledJobRuntime` delivery, create→`RunWorkers`→GET completed)
is tracked as a separate local wave and is not mixed into the standards commit.

Proof commands (from `apps/gateway`):

```text
go test ./internal/composition/ ./internal/infrastructure/vault/ ./internal/application/ ./internal/infrastructure/persistence/ ./internal/contracttest/ -count=1
go test ./... -count=1
```

