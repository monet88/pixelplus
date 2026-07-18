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

This is prototype evidence only: path/security/schema/example/invariant checks. The YAML tracer is JSON-compatible so Node can parse it without a YAML dependency. The validation command requires Python with `jsonschema` Draft 2020-12 support already available in the environment; the repository adds no package dependency. It is **not** a runtime Gateway test and **not** a full external OpenAPI metaschema validator.

## Final unified contract (#20)

The final versioned Public API package — consolidating this inference tracer with the management contract — is deferred to issue **#20**. Do not treat `0.0.0-prototype` as a stable release surface.
