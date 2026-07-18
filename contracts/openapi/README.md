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

This checks structure, security scheme coverage, internal `$ref`s, request redaction (`tenant_id` absent), required error/event examples, description invariants, and Draft 2020-12 example validation. The command requires Python with the `jsonschema` package already available; the repository adds no package dependency. It is **not** a full external OpenAPI metaschema validator and **not** a runtime Gateway test.

Management-plane routes and the final versioned Public API package remain deferred to **#20**.
