# Validation — US-020 Public API Contract Policy

## Proof Strategy

Prove that the stable artifact contains the complete unified surface, preserves the accepted security and ownership boundaries, and rejects policy drift through its public validator CLI. Re-run retained #18/#19 validators to prove the historical tracer artifacts still validate. Runtime HTTP composition proof is required once the Gateway composition root exists, but is not executable in this specification-only story.

## Test Plan

| Layer | Cases |
| --- | --- |
| Unit | Not applicable; policy behavior is tested through the validator CLI rather than private functions. |
| Integration | Redocly OpenAPI structure, frozen v1.0.0 compatibility baseline, all 26 operations and exact authorization scopes, internal references, Draft 2020-12 examples, security, secret boundaries, lifecycle/removal gates, idempotency, and contract-test policy. |
| E2E | Black-box mutation suite invokes the validator as an external process and proves representative incompatible policy changes fail. Runtime Gateway HTTP conformance is deferred until the composition root exists. |
| Platform | Node CLI with pinned Redocly structural validation plus Python `jsonschema` example validation on the repository development environment. |
| Performance | Not applicable to specification work; no runtime hot path is implemented. |
| Logs/Audit | Static checks reject secret-bearing response schemas/examples and require ownership rejection before vault decrypt, Adapter calls, or job enqueue. |
| Repository | Retained prototype validators, scenario assertions, `git diff --check`, full changed-file scope review, and independent Standards/Spec review. |

## Fixtures

- `contracts/openapi/pixelplus-public-api-v1.yaml` as the stable source under test.
- `contracts/openapi/baselines/pixelplus-public-api-v1.0.0.yaml` as the semantic compatibility oracle, loaded from an immutable PR base ref or the public release tag; the worktree copy is only the pre-release fallback.
- Temporary cloned artifacts with one structural, authorization, compatibility, or policy mutation per black-box failure case.
- The retained inference and management prototype artifacts.
- Deterministic management contract scenarios and their expected assertion counts.

## Commands

```text
npx redocly lint contracts/openapi/pixelplus-public-api-v1.yaml --config redocly.yaml
node scripts/validate-public-api-contract.mjs
node scripts/test-public-api-contract-validator.mjs
node scripts/validate-openapi-contract.mjs contracts/openapi/pixelplus-public-api-v0alpha.yaml
node scripts/prototype-management-contract.mjs
git diff --check
scripts/bin/harness-cli.exe story verify US-020
```

## Acceptance Evidence

- Stable validator: 26 operations and 205 Draft 2020-12 examples passed.
- Stable validator black-box mutation suite passed all policy mutations.
- Retained inference prototype validator passed with 12 paths, 29 schemas, and 61 validated examples.
- Retained management prototype validator passed with 14 operations and 71 schema-validated examples.
- Deterministic management scenarios produced 140 PASS assertions and zero FAIL assertions.
- A delayed Spec-axis review identified two remaining gaps: six resource/catalog GET operations lacked an explicit idempotency class, and ownership/observation policy did not machine-lock job enqueue and the full side-effect observation set. Follow-up mutations reproduced both gaps; the stable extension, validator, policy, decision, and story design now classify all 26 operations and enforce those composition observations.
- Final independent review found and fixed: two falsely declared response extension points; missing Clock/ID-generator enforcement; operation-class/header-matrix drift; allow-by-default `Idempotency-Key` on unclassified operations; and broken `$ref` crashes that could hide accumulated policy failures. Focused re-reviews confirmed all findings closed.
- `harness-cli story verify US-020` and durable decision verification passed; `story complete US-020` reran fresh proof and marked the story implemented.
- A final contract-policy review found seven enforcement gaps: inference scope metadata, semantic baseline comparison, SemVer MINOR/PATCH support, complete idempotency fingerprints/replay values, closed controlled-port allowlist, complete removal gates, and structural OpenAPI validation. The implementation now uses one 26-row operation descriptor matrix, exact scope/idempotency/contract-test sets, a frozen v1.0.0 baseline, aligned SemVer checks, Redocly structural validation, and black-box mutations for every reproduced gap.
- Redocly's built-in `struct` rule did not reject an empty Responses Object, so the structural config includes the narrow `pixelplus/non-empty-responses` rule matching the official OAS schema's non-empty constraint. Both the golden artifact and `responses={}` mutation were exercised through the same public validator CLI.
- The descriptor matrix removes the previous operation identity data clump: path, method, operationId, scope, idempotency class, header policy, and approved secret boundary are now derived from one source, with partition mutations rejecting missing or overlapping classes.
- A final follow-up review closed five additional gaps: PR-head baseline co-editing, request-narrowing constraints added where none existed, optional fields added to closed response objects, missing `POST /assets` 403/413 outcomes, and unscanned request examples. The validator now sources baselines from a base ref/release tag, compares schemas by request/response direction, locks both Asset outcomes, and secret-scans every collected example through the public CLI mutation suite.
- `git diff --check` passed. Runtime Gateway composition proof remains intentionally deferred because issue #20 forbids implementing the Gateway; the stable artifact makes the required future composition and observations normative.
