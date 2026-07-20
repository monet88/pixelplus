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

Recorded on 2026-07-20 against PR #41 review fixes on branch
`agent/issue-21-pure-go-gateway-seams`:

| Command | Result |
| --- | --- |
| `git diff --check` | pass (`exit 0`, no whitespace errors) |
| `node scripts/validate-public-api-contract.mjs` | pass (`PASS: stable Public API contract (26 operations, 205 Draft 2020-12 examples, baseline_source=worktree:pre-release)`) |
| `node scripts/validate-openapi-contract.mjs contracts/openapi/pixelplus-public-api-v0alpha.yaml` | pass (`EXIT=0`; OpenAPI 3.1.1 prototype validation) |
| `node scripts/prototype-management-contract.mjs` | pass (`PASS: 140 management contract actions validated.`) |
| `scripts/bin/harness-cli.exe story verify US-021` | pass (`Story US-021 verification: pass`) |
| `scripts/bin/harness-cli.exe decision verify 0009-pure-go-module-seams-and-dependency-budget` | pass (`Decision ... verification: pass`) |

Static review evidence after the PR #41 review fixes:

- Issue #21 seven boundaries remain named; ADR also locks `application`,
  `ports`, observability recorders (`AuditRecorder`, `TelemetryRecorder`,
  `RequestLogRecorder`), and `contracttest`.
- Dependency graph is now explicit: `transport -> {application, domain}`,
  `application -> {domain, ports}`, `ports -> domain`.
- Application owns Public API command/query/result types; adapters consume
  `domain.*Invocation` plus Vault-injected `SecretMaterial`.
- Vault catalogue includes `Authorize`, capability-specific use, `Rewrap`,
  `Revoke`, `LogicalDelete`, and `Purge`.
- Health/circuit catalogue includes principal fencing, recovery permits, and
  `SurfaceCircuitStore`; protected content has separate stores.
- Job identity is unified on `domain.JobRef` with `domain.SafeJobReference`
  queue projection and `Runtime.RunWorkers`.
- Runtime implementation and contract mutation remain out of scope.

Residual proof gap: `node scripts/test-public-api-contract-validator.mjs` was
started but hung under local process contention after spawning nested
`validate-public-api-contract.mjs` mutation workers; it was aborted. The base
contract validator itself still passes. Re-run the mutation suite in a clean
process environment before treating validator-regression coverage as green.
