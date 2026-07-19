# Design — US-020 Public API Contract Policy

## Domain Model

The stable Public API package has two linked identities: URL major `v1` and semantic release `1.0.0`. A backward-compatible release may extend the existing major only under the declared compatibility rules; an incompatible change requires a new URL major and semantic MAJOR release.

Deprecation is a notice state, not a behavior change. Removal requires at least 180 days of notice, a generally available successor, a Sunset no earlier than Deprecation, and a new major.

An HTTP idempotency record is scoped by authenticated Tenant, Client API Key, and `Idempotency-Key`. Its fingerprint includes operation identity, normalized path/query, and every side-effect-changing input. It is distinct from chat execution identity, Render Job execution identity, output retrieval, and output placement/delivery identity.

## Application Flow

This story is specification-only. A future Gateway will authenticate the Client API Key, establish Tenant authority, validate the operation-specific idempotency requirement, reserve or load replay ownership, and only then cross vault, Adapter, persistence, or job-runtime ports. Matching terminal replay returns the original result; in-progress or uncertain ownership cannot trigger a second execution.

Future contract tests enter through the public HTTP interface and exercise real Gateway composition. Deterministic Adapter, Credential Vault, persistence, job runtime, clock, and ID generator implementations may replace external effects at their ports; handler stubs and private functions are not valid substitutes.

## Interface Contract

- Stable server base: `/v1`.
- Stable artifact: `contracts/openapi/pixelplus-public-api-v1.yaml`.
- Authentication authority: shared `ClientApiKey`; clients never supply `tenant_id`.
- Optional HTTP replay key: chat completion creation.
- Required HTTP replay key: asset upload, image/Render Job creation, Provider Account creation, credential intake, OAuth authorization start, and direct reauthentication.
- Retrieval operations do not use replay keys and must not create render or Provider execution side effects.
- Deprecation metadata uses RFC 9745 `Deprecation`, RFC 8594 `Sunset`, and a `Link` with `rel="deprecation"`.
- `CanonicalError` and `Remediation` are shared across inference and management, with declared open extension points where future tokens must not force a major release.

## Data Model

No concrete schema or migration is selected. The logical idempotency record must retain scope, key identity, request fingerprint, ownership state, and original terminal result/resource identity for 24 hours. Direct-secret fingerprints retain only a non-reversible keyed digest; raw credentials, tokens, cookies, or ciphertext are forbidden.

Durable resources and execution records outlive the HTTP replay record according to their own lifecycle specifications. Exact storage and interface choices remain issue #21 or later implementation scope.

## UI / Platform Impact

No UI, CLI client, deployment, or runtime platform implementation changes. The stable artifact is the client-facing specification package for future generated clients and Gateway work.

## Observability

Future conformance proof must observe public HTTP results and counts for Adapter calls, vault decrypts, persistence writes, job creation/execution, and output delivery. Foreign, unknown, or deleted ownership rejection must be non-enumerating and occur before vault decrypt or Adapter calls. Logs, errors, snapshots, examples, routing records, and idempotency records must remain secret-free.

## Alternatives Considered

1. Keep separate inference and management stable contracts — rejected because shared authentication, errors, compatibility, and idempotency could drift.
2. Version only through `info.version` without a URL major — rejected because incompatible client behavior needs a clear routing boundary.
3. Make `Idempotency-Key` required on every POST — rejected because bounded OpenAI chat compatibility and resource-state commands have different replay semantics.
4. Let handlers or HTTP middleware retry full executions — rejected because it creates duplicate billing/resource risk and conflicts with the locked chat and Render Job retry owners.
5. Require runtime composition tests in #20 — rejected because the Gateway and concrete ports do not exist and are explicitly issue #21 scope; #20 instead makes the future test architecture normative and machine-checked.
