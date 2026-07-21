# GW-044 Bootstrap Pure-Go Composition and Readiness

## Status

implemented

## Lane

normal

## Product Contract

The production Gateway process and deterministic contract fixture use the
same `composition.New` constructor and returned `Runtime`. Public operational
health and readiness are exercised over HTTP, workers use the exported
lifecycle, startup recovery fails closed, and owned resources close in reverse
construction order.

## Relevant Product Docs

- `docs/decisions/0009-pure-go-module-seams-and-dependency-budget.md`
- `docs/spec/api-versioning-compatibility-idempotency-contract-testing-policy.md` section 6
- `docs/spec/provider-gateway-implementation-ready-specification.md` foundation and composition slice

## Acceptance Criteria

- `apps/gateway` builds with the accepted package direction and a standard-library-only dependency budget.
- Production and contract fixtures call `composition.New` and use the same returned `Runtime`.
- Controlled Clock and ID ports make contract fixture observations deterministic.
- `GET /healthz` proves process liveness while `GET /readyz` fails closed until required recovery succeeds.
- `RunWorkers` observes root cancellation, HTTP shutdown is explicit, and owned resources close in reverse construction order.
- Health/readiness adds no authentication, routing, Provider, persistence schema, or product-operation shortcut.

## Design Notes

- Commands: `go run ./cmd/gateway` from `apps/gateway`.
- API: unversioned operational `GET /healthz` and `GET /readyz`; stable product operations remain under `/v1` and are out of scope.
- Domain rules: health means the process can serve operational probes; readiness additionally requires successful recovery and required dependency availability.
- Dependency direction: enforced by a standard-library architecture test over local Go imports.
- Public test seam: a real HTTP server around `Runtime.Handler()` plus `Runtime.RunWorkers` and fixture `Close`.

## Validation

| Layer | Expected proof |
| --- | --- |
| Unit | Config/dependency validation and architecture import rules. |
| Integration | Public health/readiness HTTP through real composition; deterministic ports; fail-closed recovery. |
| E2E | Not applicable; no browser/user workflow is introduced. |
| Platform | Production command builds; worker cancellation and HTTP/resource shutdown pass on Windows. |
| Release | `go vet`, full Go tests, race tests, dependency listing, and repository contract validators. |

## Harness Delta

Adds `GW-044` as the first Gateway runtime proof row with a reusable
`go -C apps/gateway test -race ./...` verification command.

## Evidence

- `go -C apps/gateway vet ./...`, `go -C apps/gateway test ./...`, and
  `go -C apps/gateway test -race ./...` passed.
- `CGO_ENABLED=0 go -C apps/gateway test ./...` passed, and `go list -deps`
  confirmed that the module dependency graph contains only the Go standard
  library plus local Gateway packages.
- Focused race regressions passed 20 times each for concurrent queue
  admission/shutdown, retryable resource close after a caller timeout, worker
  failure cancellation of active HTTP requests, and forbidden import rules.
- The contract fixture close retry regression passed 100 times with the race
  detector, proving that an initial caller deadline does not permanently cache
  failure after one-shot HTTP shutdown.
- The production command built to a temporary Windows executable. A live
  loopback smoke returned `200` from `/healthz` and `/readyz`, while
  `/v1/models` remained `404` so readiness did not bypass product gates.
- The implementation-ready Provider Gateway validator passed with 30
  capability claims, 15 decisions, five planning domains, seven slices, and 27
  authority files; its mutation suite passed all 30 cases.
- The stable Public API validator passed with 26 operations and 205 examples;
  its mutation suite also passed.
- `git diff --check` passed. No E2E proof applies because this story introduces
  no browser or end-user workflow.
