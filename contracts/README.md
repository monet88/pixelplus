# Contracts

Public OpenAI-compatible HTTP contract giữa Gateway và các client.

## Non-final inference tracer (#18)

Issue #18 giữ một **prototype non-final** cho inference surface (models, chat stream/cancel, assets, image jobs, render-job poll/cancel/output-retry, canonical errors):

| Artifact | Path |
|---|---|
| Normative decisions | `docs/spec/openai-compatible-inference-contract.md` |
| OpenAPI 3.1.1 tracer (`info.version=0.0.0-prototype`) | `contracts/openapi/pixelplus-public-api-v0alpha.yaml` |
| OpenAPI directory notes | `contracts/openapi/README.md` |
| Representation validator | `scripts/validate-openapi-contract.mjs` |

Validate from repository root:

```bash
node scripts/validate-openapi-contract.mjs contracts/openapi/pixelplus-public-api-v0alpha.yaml
```

## Non-final management tracer (#19)

Issue #19 giữ một **prototype non-final riêng** cho Provider Account, direct/OAuth credential journey, probe/reauthentication, lifecycle controls, Capability Snapshot và Tenant Routing Policy:

| Artifact | Path |
|---|---|
| Normative decisions | `docs/spec/provider-account-and-capability-management-contract.md` |
| OpenAPI 3.1.1 tracer (`info.version=0.0.0-prototype`) | `contracts/openapi/pixelplus-management-api-v0alpha.yaml` |
| Representation validator | `scripts/validate-management-openapi-contract.mjs` |
| Deterministic cause→effect runner | `scripts/run-management-contract-scenarios.mjs` |
| One-command prototype gate | `scripts/prototype-management-contract.mjs` |

Validate OpenAPI và chạy deterministic management scenarios từ repository root:

```bash
node scripts/prototype-management-contract.mjs
```

Hai tracer là prototype evidence only. Các YAML artifact là JSON-compatible để Node parse không cần YAML dependency. Validation cần Python với `jsonschema` Draft 2020-12 đã có sẵn trong environment; repository không thêm package dependency. Đây không phải runtime Gateway test và không phải full external OpenAPI metaschema validation.

## Final unified contract (#20)

Final versioned Public API package — hợp nhất inference tracer #18 với management tracer #19 — được defer sang issue **#20**. Không xem `0.0.0-prototype` hoặc path/field hiện tại là stable release surface.
