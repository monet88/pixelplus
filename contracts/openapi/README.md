# OpenAPI contracts

## Stable Public API (#20)

| Field | Value |
|---|---|
| Artifact | `pixelplus-public-api-v1.yaml` |
| Frozen baseline | `baselines/pixelplus-public-api-v1.0.0.yaml` |
| OpenAPI | `3.1.1` |
| `info.version` | `1.0.0` |
| Status extension | `x-pixelplus-artifact-status: stable` |
| Dialect | JSON Schema 2020-12 |
| Encoding | JSON-compatible YAML (JSON is a YAML 1.2 subset) so Node can parse without a YAML dependency |
| Server base | `/v1` |
| Surface | Unified inference, Assets/Render Jobs, Provider Account management, Capability Snapshot, Routing Policy |

Normative policy: `docs/spec/api-versioning-compatibility-idempotency-contract-testing-policy.md`.

Validate from repository root:

```bash
npm install
npx redocly lint contracts/openapi/pixelplus-public-api-v1.yaml --config redocly.yaml
node scripts/validate-public-api-contract.mjs
node scripts/test-public-api-contract-validator.mjs
```

The stable validator first runs pinned Redocly structural validation (`struct` plus the OAS non-empty Responses Object constraint), then checks the frozen v1.0.0 compatibility baseline, the 26 inherited operations, exact authorization scopes, shared Client API Key security, internal `$ref`s, Draft 2020-12 examples, no client `tenant_id`, approved direct-secret ingress only, secret-free request/response/component/schema examples, unified canonical error/remediation components, compatibility/deprecation rules, exact operation-specific idempotency/replay semantics, required `POST /assets` 403/413 outcomes, and future real-composition contract-test policy. The comparator permits optional request fields but rejects request-narrowing constraints and fields added to closed response objects. The mutation suite proves representative structural and semantic drift is rejected through the validator's public CLI seam.

For pull-request CI, set `PIXELPLUS_PUBLIC_API_BASELINE_REF` to the full 40-hex immutable pull-request base SHA before invoking the validator; the candidate remains the PR-head artifact while the baseline is read with `git show`. CI fails closed when neither that SHA nor the release tag is available, and a mutable name such as `HEAD` or a branch is rejected. After public release, create and protect tag `pixelplus-public-api-v1.0.0`; the validator then uses that tag and rejects semantic divergence of the checked-in v1 baseline. Before the tag exists, non-CI local validation falls back to the worktree baseline. `PIXELPLUS_PUBLIC_API_BASELINE` requires `PIXELPLUS_PUBLIC_API_ALLOW_TEST_BASELINE=1`, is reserved for isolated black-box tests, and cannot override a baseline ref.

The stable artifact is the only stable client contract. The two `0.0.0-prototype` artifacts remain historical evidence for the tracer decisions that produced it.

## Retained inference tracer (#18)

| Field | Value |
|---|---|
| Artifact | `pixelplus-public-api-v0alpha.yaml` |
| OpenAPI | `3.1.1` |
| `info.version` | `0.0.0-prototype` |
| Status extension | `x-pixelplus-artifact-status: prototype` |
| Dialect | JSON Schema 2020-12 |

Prototype decisions: `docs/spec/openai-compatible-inference-contract.md`.

```bash
node scripts/validate-openapi-contract.mjs contracts/openapi/pixelplus-public-api-v0alpha.yaml
```

This retained validator checks the original inference structure, security coverage, internal `$ref`s, request redaction, required error/event examples, description invariants, and Draft 2020-12 example validation.

## Retained management tracer (#19)

| Field | Value |
|---|---|
| Artifact | `pixelplus-management-api-v0alpha.yaml` |
| OpenAPI | `3.1.1` |
| `info.version` | `0.0.0-prototype` |
| Status extension | `x-pixelplus-artifact-status: prototype` |
| Dialect | JSON Schema 2020-12 |
| Server base | `/v1` |

Prototype decisions: `docs/spec/provider-account-and-capability-management-contract.md`.

```bash
node scripts/prototype-management-contract.mjs
```

The retained management validator and deterministic scenario runner preserve evidence for scopes, lifecycle, credential/OAuth boundaries, enable/reauthentication probe semantics, Capability Snapshots, Routing Policy, non-enumeration, and no-decrypt/no-Adapter side effects on rejected resources.

Stable validation requires installed npm development dependencies (including pinned Redocly CLI) and Python with `jsonschema`. Redocly validates the OpenAPI document structure; the PixelPlus validator adds semantic baseline and product-policy checks. Runtime Gateway composition tests become required when the composition root exists; exact interfaces and package layout remain #21 scope.
