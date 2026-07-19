# Validation — US-020 Public API Contract Policy

## Proof Strategy

Prove that the stable artifact contains the complete unified surface, preserves the accepted security and ownership boundaries, and rejects policy drift through its public validator CLI. Re-run retained #18/#19 validators to prove the historical tracer artifacts still validate. Runtime HTTP composition proof is required once the Gateway composition root exists, but is not executable in this specification-only story.

## Test Plan

| Layer | Cases |
| --- | --- |
| Unit | Not applicable; policy behavior is tested through the validator CLI rather than private functions. |
| Integration | Stable artifact structure, all 26 operations, internal references, Draft 2020-12 examples, security, secret boundaries, lifecycle, idempotency, and contract-test policy. |
| E2E | Black-box mutation suite invokes the validator as an external process and proves representative incompatible policy changes fail. Runtime Gateway HTTP conformance is deferred until the composition root exists. |
| Platform | Node CLI plus Python `jsonschema` validation on the repository development environment. |
| Performance | Not applicable to specification work; no runtime hot path is implemented. |
| Logs/Audit | Static checks reject secret-bearing response schemas/examples and require ownership rejection before vault decrypt, Adapter calls, or job enqueue. |
| Repository | Retained prototype validators, scenario assertions, `git diff --check`, full changed-file scope review, and independent Standards/Spec review. |

## Fixtures

- `contracts/openapi/pixelplus-public-api-v1.yaml` as the stable source under test.
- Temporary cloned artifacts with one policy mutation per black-box failure case.
- The retained inference and management prototype artifacts.
- Deterministic management contract scenarios and their expected assertion counts.

## Commands

```text
node scripts/validate-public-api-contract.mjs
node scripts/test-public-api-contract-validator.mjs
node scripts/validate-openapi-contract.mjs contracts/openapi/pixelplus-public-api-v0alpha.yaml
node scripts/prototype-management-contract.mjs
git diff --check
scripts/bin/harness-cli.exe story verify US-020
```

## Acceptance Evidence

- Stable validator: 26 operations and 202 Draft 2020-12 examples passed.
- Stable validator black-box mutation suite passed all policy mutations.
- Retained inference prototype validator passed with 12 paths, 29 schemas, and 61 validated examples.
- Retained management prototype validator passed with 14 operations and 71 schema-validated examples.
- Deterministic management scenarios produced 140 PASS assertions and zero FAIL assertions.
- A delayed Spec-axis review identified two remaining gaps: six resource/catalog GET operations lacked an explicit idempotency class, and ownership/observation policy did not machine-lock job enqueue and the full side-effect observation set. Follow-up mutations reproduced both gaps; the stable extension, validator, policy, decision, and story design now classify all 26 operations and enforce those composition observations.
- Final independent review found and fixed: two falsely declared response extension points; missing Clock/ID-generator enforcement; operation-class/header-matrix drift; allow-by-default `Idempotency-Key` on unclassified operations; and broken `$ref` crashes that could hide accumulated policy failures. Focused re-reviews confirmed all findings closed.
- `harness-cli story verify US-020` and durable decision verification passed; `story complete US-020` reran fresh proof and marked the story implemented.
- `git diff --check` passed. Runtime Gateway composition proof remains intentionally deferred because issue #20 forbids implementing the Gateway; the stable artifact makes the required future composition and observations normative.
