# Validation - US-022 Implementation-Ready Provider Gateway Specification

## Proof Strategy

Prove the implementation handoff as a repository-level specification package,
not as a Gateway runtime. The machine-readable manifest is the seam: it must
resolve all declared authority and retain the exact required Auth Mode,
capability-operation, decision, implementation-slice, and deferral coverage.

Mutation tests establish that the validator is sensitive to the failure modes
the ticket cares about. Retained Public API/prototype validators establish that
the assembly did not weaken or silently replace existing contract artifacts.
No Go typecheck or runtime test exists because issue #22 must not create the Go
module.

## Test Plan

| Layer | Cases |
| --- | --- |
| Unit | Node test-runner mutations: missing authority, invalid capability token, missing required mode/operation, undeclared evidence, incomplete decision, missing required decision/slice, incomplete deferred item, and same gate/implementation issue. |
| Integration | Validate the real manifest and human specification, all declared files, the exact six-by-five capability matrix, 14 decisions, seven vertical slices, and deferred register. |
| E2E | Retained stable OpenAPI, compatibility, JSON Schema example, prototype inference, and prototype management validators. Future Gateway E2E is explicitly deferred to #42. |
| Platform | Not applicable; no process, deployment, or Photoshop Plugin code. |
| Performance | Not applicable for static specification validation. Driver/queue benchmarks are deferred with explicit triggers. |
| Logs/Audit | Static review confirms the future runtime's audit-before-protected-access and secret-free observability requirements remain linked and unchanged. |
| Repository | `git diff --check`, Harness story verification/completion, final diff review, and commit. |

## Fixtures

- Temporary specification package created per Node test.
- Canonical capability vocabulary: `verified`, `conditionally_supported`,
  `unsupported`, `unverified`.
- Required Auth Modes: ChatGPT Web, ChatGPT Codex OAuth, Gemini Web Cookie,
  Gemini Antigravity OAuth, Grok Web SSO, and Grok xAI OAuth.
- Required operations: chat, chat streaming, image generation, image edit, and
  inpaint.
- Stable repository manifest and implementation-ready specification.

## Commands

```text
node --test scripts/test-provider-gateway-implementation-spec-validator.mjs
node scripts/validate-provider-gateway-implementation-spec.mjs
node scripts/validate-public-api-contract.mjs
node scripts/test-public-api-contract-validator.mjs
node scripts/validate-openapi-contract.mjs contracts/openapi/pixelplus-public-api-v0alpha.yaml
node scripts/prototype-management-contract.mjs
git diff --check
scripts/bin/harness-cli.exe story verify US-022
```

## Acceptance Evidence

Recorded on 2026-07-20 against the issue #22 working tree based on `63d6454`:

| Command | Result |
| --- | --- |
| `node --test scripts/test-provider-gateway-implementation-spec-validator.mjs` | pass: 13 mutations cover complete package, missing/escaping authority, exact capability vocabulary/matrix/risk, undeclared evidence/authority, decision dimensions/IDs, slice IDs/dependencies/cycles, deferred requirements, and separate implementation issue |
| `node scripts/validate-provider-gateway-implementation-spec.mjs` | pass: issue #22, implementation #42, 30 capability claims, 14 decisions, seven slices, 30 deferred items, 26 authority files |
| `node scripts/validate-public-api-contract.mjs` | pass: stable contract has 26 operations and 205 Draft 2020-12 examples; worktree pre-release baseline |
| `node scripts/test-public-api-contract-validator.mjs` | pass: stable Public API validator mutation suite |
| `node scripts/validate-openapi-contract.mjs contracts/openapi/pixelplus-public-api-v0alpha.yaml` | pass: retained inference prototype, 12 operations, 29 schemas, 61 validated examples |
| `node scripts/prototype-management-contract.mjs` | pass: management prototype and 140 deterministic actions |
| `node --check scripts/validate-provider-gateway-implementation-spec.mjs` | pass |
| `node --check scripts/test-provider-gateway-implementation-spec-validator.mjs` | pass |
| `git diff --check` | pass |

Implementation handoff evidence:

- GitHub issue #42, `[Implementation] Build the Pure-Go Provider Gateway`,
  exists as an open enhancement blocked by #22. It is not assigned and does
  not carry `ready-for-agent`.
- No `apps/gateway/go.mod`, Gateway runtime package, persistence schema,
  Provider adapter, Vault, worker, or deployment artifact was created.
- The manifest requires all six Auth Modes, five primary operation claims,
  the locked risk state for each mode, 14 decision domains, seven vertical
  implementation slices, and every deferred item's reason/dependency/reopen
  trigger.
- The stable `/v1` artifact remains the only client wire authority; the
  assembly points to rather than copies/replaces normative domain sources.

Harness story verification/completion, independent review, and the final
commit are recorded after this evidence block is written.
