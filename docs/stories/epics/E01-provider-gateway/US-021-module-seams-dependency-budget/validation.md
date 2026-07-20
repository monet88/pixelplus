# Validation — US-021 Pure-Go Module Seams and Dependency Budget

## Proof Strategy

This story is specification-only. Prove that the accepted decision names every
required seam, gives the dependency graph and budget a concrete rationale, and
defines a future test boundary through real HTTP composition. Prove that no
Gateway runtime artifact or public contract mutation was introduced.

Executable runtime proof is intentionally deferred until a follow-up story
creates `apps/gateway`. At that point the composition suite must use the same
constructor as production, controlled implementations at the listed ports,
and a public `httptest` surface.

## Test Plan

| Layer | Cases |
| --- | --- |
| Unit | Not applicable; no Go implementation exists. Future unit tests cover pure domain invariants through exported behavior. |
| Integration | Decision/story cross-check: all seven requested boundaries, inward dependency direction, approved dependency slots, and logical atomic port guarantees are present. |
| E2E | Deferred to the Gateway implementation story; future cases use public HTTP + real composition for ownership, replay, Vault, Adapter, JobRuntime, and output-placement behavior. |
| Platform | Not applicable; no process, deployment, or Photoshop Plugin code changes. |
| Performance | Deferred; persistence driver and queue choices require implementation-specific benchmark evidence. |
| Logs/Audit | Static contract check requires audit-before-decrypt, typed secret-free telemetry, fail-closed audit dependency behavior, one canonical JSON request log per request with `timestamp`, `level`, `request_id`, `user_id` when known, `action`, `duration_ms`, `status_code`, and `message`, plus separate application-log and product-audit projections. |
| Repository | `git diff --check`, retained OpenAPI/policy validators, story verification, durable decision verification, and independent code review. |

## Fixtures

- Issue #21 acceptance criteria and prerequisite decisions #15, #16, #17,
  and #20.
- The package tree and dependency graph in decision 0009.
- Controlled port observations: Adapter calls, Vault use/decrypt attempts,
  durable writes, JobRuntime enqueues, Clock/ID values, and audit outcomes.
- Future public-HTTP scenarios for foreign ownership, idempotency no-steal,
  stale capability/health, committed/unknown Render attempts, output delivery
  retry, and secret-free projections.

## Commands

```text
git diff --check
node scripts/validate-public-api-contract.mjs
node scripts/test-public-api-contract-validator.mjs
node scripts/validate-openapi-contract.mjs contracts/openapi/pixelplus-public-api-v0alpha.yaml
node scripts/prototype-management-contract.mjs
scripts/bin/harness-cli.exe story verify US-021
scripts/bin/harness-cli.exe decision verify 0009-pure-go-module-seams-and-dependency-budget
```

No `go test` or Go typecheck command exists yet because this story must not
create the Gateway module. These commands become required in the follow-up
implementation story.

## Acceptance Evidence

- The decision record names the seven issue boundaries plus composition and
  contract-test packages.
- The dependency graph has no inward edge from domain/application to transport,
  Provider, concrete infrastructure, queue, or configuration.
- The budget is standard-library-first with one bounded cryptography slot, one
  optional cgo-free persistence-driver slot, and at most one direct queue/
  runtime client outside the application when durable delivery requires it;
  all other external groups are forbidden by default and every nonzero slot
  requires implementation-specific rationale.
- The test seam uses real HTTP composition and controlled ports, with explicit
  no-private-function/no-concrete-schema/no-goroutine-layout rules.
- The observability contract includes the canonical request-log fields and
  keeps operational application logs separate from product/security audit
  records.
- Runtime implementation and contract mutation remain out of scope.
