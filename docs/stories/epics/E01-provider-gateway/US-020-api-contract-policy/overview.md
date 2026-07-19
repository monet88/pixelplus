# Overview — US-020 Public API Contract Policy

## Current Behavior

Issues #18 and #19 produced separate `0.0.0-prototype` OpenAPI tracer artifacts for inference and management. They preserve useful decisions, but clients do not have one stable package that defines shared versioning, compatibility, deprecation, idempotency, security, and future contract-test behavior across both surfaces.

## Target Behavior

- `contracts/openapi/pixelplus-public-api-v1.yaml` is the only stable client contract and publishes the unified inference and management surface at `/v1` with semantic version `1.0.0`.
- Backward-compatible evolution, incompatible change classification, deprecation notice, support window, and removal gates are explicit and machine-checked.
- HTTP request replay is distinguished from chat execution retries, Render Job creation/execution, output retrieval, and output delivery retries.
- Future runtime conformance tests must enter through the public HTTP surface and real Gateway composition, with controlled implementations only at the locked conceptual ports.
- The #18 and #19 prototype artifacts remain retained historical evidence rather than competing stable contracts.

## Affected Users

- Client developers integrating inference, asset, Render Job, Provider Account, Capability Snapshot, and Routing Policy operations.
- Gateway, Adapter, vault, persistence, and job-runtime implementers.
- Tenant and platform operators relying on non-enumeration, retry ownership, and secret-handling guarantees.

## Affected Product Docs

- `CONTEXT.md`
- `contracts/README.md`
- `contracts/openapi/README.md`
- `contracts/openapi/pixelplus-public-api-v1.yaml`
- `docs/spec/api-versioning-compatibility-idempotency-contract-testing-policy.md`
- `docs/spec/openai-compatible-inference-contract.md`
- `docs/spec/provider-account-and-capability-management-contract.md`

## Non-Goals

- Implementing the Gateway, composition root, concrete Go interfaces, or package layout; issue #21 owns those seams.
- Implementing persistence, queues, workers, Provider calls, vault behavior, or runtime HTTP conformance tests.
- Deleting or rewriting the retained #18 and #19 prototype evidence.
- Claiming `Idempotency-Key` is a finalized IETF standard.
