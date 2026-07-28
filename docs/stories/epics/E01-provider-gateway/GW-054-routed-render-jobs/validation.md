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
gofmt -l <changed-go-files>
go build ./...
go vet ./...
go test ./... -count=1
go test -race ./... -count=1
$env:GOTOOLCHAIN='go1.25.12'; govulncheck ./...
```

Harness verification command:

```text
go -C apps/gateway test ./... -count=1
```

Before commit:

```text
GitNexus detect_changes(scope=compare, base_ref=main)
```

## Acceptance Evidence

Validated product head: `5a12b9c` on
`feature/issue-54-routed-render-jobs`.

| Proof | Result | Concrete evidence |
| --- | --- | --- |
| Format | pass | `gofmt -l` returned no changed Go files. |
| Build | pass | `go build ./...` from `apps/gateway`. |
| Vet | pass | `go vet ./...` from `apps/gateway`. |
| Unit/integration | pass | `go test ./... -count=1`; all Gateway packages passed. |
| Race | pass | `go test -race ./... -count=1`; all Gateway packages passed. |
| Public E2E | pass | Contract suite creates through `Runtime.Handler()`, executes through `Runtime.RunWorkers` / `JobExecutor`, then retrieves `completed` with durable output Asset placement. |
| Cancel recovery | pass | Pre-payload recovery stays `not_started`; post-payload recovery becomes `unknown`; both make zero Provider calls. |
| Vulnerability | pass | With `GOTOOLCHAIN=go1.25.12`, `govulncheck ./...` returned `No vulnerabilities found`. Host `go1.25.5` alone remains below the standard-library fixes required by the scanner. |
| Diff hygiene | pass | `git diff --check main...5a12b9c`. |
| Final Standards review | pass | Exact-commit review of `5a12b9c` reported no actionable finding. |

The implementation was delivered as issue-scoped local commits from
`1da40de` through `5a12b9c`. The final review-fix commit normalizes uncertain
post-payload cancel recovery without changing authoritative commit evidence.

## Residual Risks

- Production deliberately remains not-ready without injected durable Render
  persistence/queue, Vault credential authorizer, and Provider Adapter.
- No Provider status-lookup Adapter exists; post-payload crash/cancel recovery
  records `unknown` and forbids rerender instead of inventing abort certainty.
- No live Provider acceptance was run; controlled adapters prove Gateway
  orchestration and security boundaries, not a production Provider protocol.
- GitNexus has no PDG/taint layer for this checkout, so graph taint safety was
  not claimed; secret-boundary behavior is covered by explicit tests/reviews.
