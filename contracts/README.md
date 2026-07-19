# Contracts

Public OpenAI-compatible HTTP contract giữa Gateway và các client.

## Stable unified Public API (#20)

Issue #20 hợp nhất inference tracer #18 và management tracer #19 thành một stable package duy nhất:

| Artifact | Path |
|---|---|
| Normative versioning, compatibility, deprecation, idempotency, and contract-testing policy | `docs/spec/api-versioning-compatibility-idempotency-contract-testing-policy.md` |
| Stable OpenAPI 3.1.1 contract (`/v1`, `info.version=1.0.0`) | `contracts/openapi/pixelplus-public-api-v1.yaml` |
| Stable representation/policy validator | `scripts/validate-public-api-contract.mjs` |
| Validator mutation suite | `scripts/test-public-api-contract-validator.mjs` |
| OpenAPI directory notes | `contracts/openapi/README.md` |

Validate stable contract từ repository root:

```bash
node scripts/validate-public-api-contract.mjs
node scripts/test-public-api-contract-validator.mjs
```

Artifact khóa chung 26 operation inference + management, `ClientApiKey`, canonical errors, compatibility/deprecation policy, operation-specific `Idempotency-Key`, secret/non-enumeration boundaries, và yêu cầu future runtime contract tests phải đi qua public HTTP surface + real Gateway composition. Issue #20 không triển khai Gateway; concrete ports/composition root vẫn thuộc #21.

## Retained non-final inference tracer (#18)

Issue #18 giữ một **prototype non-final** cho inference surface (models, chat stream/cancel, assets, image jobs, render-job poll/cancel/output-retry, canonical errors):

| Artifact | Path |
|---|---|
| Prototype decisions | `docs/spec/openai-compatible-inference-contract.md` |
| OpenAPI 3.1.1 tracer (`info.version=0.0.0-prototype`) | `contracts/openapi/pixelplus-public-api-v0alpha.yaml` |
| Representation validator | `scripts/validate-openapi-contract.mjs` |

Validate từ repository root:

```bash
node scripts/validate-openapi-contract.mjs contracts/openapi/pixelplus-public-api-v0alpha.yaml
```

## Retained non-final management tracer (#19)

Issue #19 giữ một **prototype non-final riêng** cho Provider Account, direct/OAuth credential journey, probe/reauthentication, lifecycle controls, Capability Snapshot và Tenant Routing Policy:

| Artifact | Path |
|---|---|
| Prototype decisions | `docs/spec/provider-account-and-capability-management-contract.md` |
| OpenAPI 3.1.1 tracer (`info.version=0.0.0-prototype`) | `contracts/openapi/pixelplus-management-api-v0alpha.yaml` |
| Representation validator | `scripts/validate-management-openapi-contract.mjs` |
| Deterministic cause→effect runner | `scripts/run-management-contract-scenarios.mjs` |
| One-command prototype gate | `scripts/prototype-management-contract.mjs` |

Validate OpenAPI và chạy deterministic management scenarios từ repository root:

```bash
node scripts/prototype-management-contract.mjs
```

Hai tracer `0.0.0-prototype` là historical evidence, không phải alternative stable client contracts. Các YAML artifact là JSON-compatible để Node parse không cần YAML dependency. Validation cần Python với `jsonschema` Draft 2020-12 đã có sẵn trong environment; repository không thêm package dependency. Đây chưa phải runtime Gateway conformance suite hoặc full external OpenAPI metaschema validation.
