# 0008 Stable Public API Contract Policy

Date: 2026-07-19

## Status

Accepted

## Context

Issues #18 and #19 produced separate inference and management prototype contracts. Issue #20 must turn those tracers into one stable client contract while preventing incompatible drift in authentication, Tenant ownership, errors, idempotency, retries, deprecation, and future validation. The Gateway and concrete composition ports do not exist yet, so this decision must lock behavior without implementing runtime architecture owned by issue #21.

## Decision

PixelPlus publishes `contracts/openapi/pixelplus-public-api-v1.yaml` as the only stable Public API contract. It uses OpenAPI 3.1.1, JSON Schema Draft 2020-12, URL major `/v1`, and semantic release `1.0.0`. The #18 and #19 `0.0.0-prototype` artifacts remain historical evidence, not competing stable contracts.

Within `/v1`, releases follow Semantic Versioning 2.0.0 and remain backward-compatible. Changes to authentication/scope, requiredness of idempotency, status semantics, closed enums, or other declared incompatible behavior require a new URL major and semantic MAJOR release. Declared open error extension points permit clients to receive previously unseen tokens without treating every new operation/error as a major change.

Deprecation preserves existing behavior and gives at least 180 days of notice. Notices use RFC 9745 `Deprecation`, RFC 8594 `Sunset`, and a migration `Link` with `rel="deprecation"`. Sunset cannot precede deprecation, a generally available successor is required, and removal occurs only in a new major.

HTTP idempotency is a PixelPlus contract using `Idempotency-Key`; it is not presented as a finalized IETF standard. Replay identity is scoped by authenticated Tenant, Client API Key, and key, with operation identity and all side-effect-changing inputs in the fingerprint. Records retain replay ownership for 24 hours. Matching replay returns the original operation without a new side effect; conflicts, in-progress ownership, and uncertain ownership never steal claims or create replacement executions. Secret-bearing fingerprints use only non-reversible keyed digests.

Chat execution and Render Job execution remain the sole full-execution retry owners for their domains. Resource/catalog retrieval and output retrieval read existing state without Provider or job execution; output delivery retries reuse the existing manifest/placement identity.

Future runtime contract tests must enter through the public HTTP surface and real Gateway composition. Controlled implementations may replace Adapter, Credential Vault, persistence, job runtime, clock, and ID generator behavior only at their ports. Handler stubs, private functions, concrete database schemas, and goroutine layouts are forbidden test seams. Exact interfaces, packages, and composition root remain issue #21 scope.

## Alternatives Considered

1. Publish separate stable inference and management contracts. Rejected because shared security, errors, lifecycle, and idempotency rules would have two sources of truth.
2. Allow breaking changes inside `/v1` with only a semantic version bump. Rejected because the route would no longer communicate client compatibility.
3. Treat every POST identically for idempotency. Rejected because chat replay, durable creation, resource-state commands, and retrieval have different side-effect and compatibility constraints.
4. Validate future behavior through handler stubs. Rejected because such tests can pass while real composition violates ownership ordering or duplicates side effects.
5. Implement the Gateway test harness now. Rejected because issue #20 is specification work and issue #21 owns concrete seams and composition.

## Consequences

Positive:

- Clients have one stable contract and a predictable major-version boundary.
- Duplicate billing/resource creation and secret-retention risks have explicit replay rules.
- Deprecation and removal are measurable rather than discretionary.
- Future contract tests must prove behavior through production-like composition while retaining deterministic external effects.

Tradeoffs:

- Required idempotency keys add client ceremony to costly create/secret-ingress operations.
- Closed-enum evolution is intentionally conservative and may require a new major.
- The current executable proof validates the contract representation, not runtime Gateway behavior.
- Python `jsonschema` remains an environment prerequisite without becoming a repository dependency.

## Follow-Up

- Issue #21 defines concrete module seams, Go interfaces, package layout, and composition root consistent with this policy.
- The Gateway implementation ticket builds the public HTTP conformance suite and controlled port implementations.
- Runtime proof must count Adapter, vault, persistence, job, and delivery side effects and verify ownership rejection before decrypt, Provider access, or job enqueue.
