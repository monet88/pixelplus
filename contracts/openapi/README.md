# OpenAPI contracts

## Stable Public API (#20)

| Field | Value |
|---|---|
| Artifact | `pixelplus-public-api-v1.yaml` |
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
node scripts/validate-public-api-contract.mjs
node scripts/test-public-api-contract-validator.mjs
```

The stable validator checks the 26 inherited operations, shared Client API Key security, internal `$ref`s, Draft 2020-12 examples, no client `tenant_id`, approved direct-secret ingress only, secret-free responses/examples, unified canonical error/remediation components, compatibility/deprecation rules, operation-specific idempotency requiredness/replay semantics, and future real-composition contract-test policy. The mutation suite proves drift in those rules is rejected through the validator's public CLI seam.

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

All validation flows require Python with the `jsonschema` package already available; the repository adds no package dependency. They are not full external OpenAPI metaschema validators. Runtime Gateway composition tests become required when the composition root exists; exact interfaces and package layout remain #21 scope.
