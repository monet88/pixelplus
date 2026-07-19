# OpenAPI contracts

Non-final Public API tracers live here until issue **#20** publishes the unified versioned package.

## Inference tracer (#18)

| Field | Value |
|---|---|
| Artifact | `pixelplus-public-api-v0alpha.yaml` |
| OpenAPI | `3.1.1` |
| `info.version` | `0.0.0-prototype` |
| Status extension | `x-pixelplus-artifact-status: prototype` |
| Dialect | JSON Schema 2020-12 |
| Encoding | JSON-compatible YAML (JSON is a YAML 1.2 subset) so Node can parse without a YAML dependency |

Normative decisions: `docs/spec/openai-compatible-inference-contract.md`.

Validate from repository root:

```bash
node scripts/validate-openapi-contract.mjs contracts/openapi/pixelplus-public-api-v0alpha.yaml
```

This checks structure, security scheme coverage, internal `$ref`s, request redaction (`tenant_id` absent), required error/event examples, description invariants, and Draft 2020-12 example validation.

## Management tracer (#19)

| Field | Value |
|---|---|
| Artifact | `pixelplus-management-api-v0alpha.yaml` |
| OpenAPI | `3.1.1` |
| `info.version` | `0.0.0-prototype` |
| Status extension | `x-pixelplus-artifact-status: prototype` |
| Dialect | JSON Schema 2020-12 |
| Encoding | JSON-compatible YAML (JSON is a YAML 1.2 subset) so Node can parse without a YAML dependency |
| Server base | `/v1` |
| Surface | Provider Account lifecycle/credential/OAuth/probe/controls, Capability Snapshot reads, Tenant Routing Policy |

Normative decisions: `docs/spec/provider-account-and-capability-management-contract.md`.

Run representation validation and deterministic cause→effect scenarios from repository root:

```bash
node scripts/prototype-management-contract.mjs
```

The management validator checks required operations/scopes, Client API Key security, same-Tenant authority, direct-secret request boundaries, redacted examples, internal `$ref`s, Draft 2020-12 examples, lifecycle/enable/reauthentication probe semantics, five-operation Capability Snapshots, missing/stale read-versus-authorization behavior, routing fail-closed semantics, and no-decrypt descriptions. The scenario runner prints full relevant before/after state and explicit side-effect deltas for deterministic cause→effect actions.

Both validation flows require Python with the `jsonschema` package already available; the repository adds no package dependency. They are not full external OpenAPI metaschema validators and not runtime Gateway tests.

Issue **#20** owns consolidation, shared component/error naming, final idempotency policy, and the stable versioned package.
