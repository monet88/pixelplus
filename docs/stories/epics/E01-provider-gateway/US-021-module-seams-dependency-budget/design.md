# Design — US-021 Pure-Go Module Seams and Dependency Budget

## Domain Model

The canonical domain remains Provider-independent. It includes Tenant and
Security Principal identity, Client API Key scope/admission state, Provider
Account lifecycle and Auth Mode identity, Credential version references,
Capability Snapshot, Routing Policy, Health State/Reason and cooldown scope,
Asset and retention state, Render Job/attempt/result manifest, idempotency
claim/replay identity, commit certainty, and canonical error/remediation
values.

`internal/domain` owns invariants and safe value objects. It does not own
HTTP status, SQL rows, Provider payloads, queue messages, ciphertext, or
process configuration. Provider IDs and Auth Mode IDs may be canonical
identifiers; Provider protocol schemas and authentication mechanics are not
domain types.

## Application Flow

`internal/application` owns typed commands, queries, and the operation-specific
retry boundary. The common protected-operation order is:

```text
authenticate Client API Key
  -> derive Security Principal/Tenant
  -> scope and request validation
  -> operation-specific idempotency claim/replay
  -> admission and durable ownership checks
  -> lifecycle + capability + health + routing policy gates
  -> audit intent before protected access
  -> Credential Vault purpose authorization/decrypt
  -> Adapter execution or JobRuntime enqueue
  -> canonicalize outcome, persist state, audit and emit safe telemetry
```

The exact path branches for reads, management transitions, chat streaming,
Render Job creation, worker recovery, and output delivery according to the
normative specs. The invariant is that transport, queue redelivery, Adapter,
and worker layers do not create an additional full-operation retry owner.

Inbound application ports are typed and operation-oriented:

- `PublicGateway` serves the stable Public API use cases and is the only
  application entrypoint used by HTTP contract tests.
- `JobExecutor` accepts a safe durable Job reference for worker execution and
  owns Render Job attempt/recovery decisions.

Outbound ports are grouped by ownership and consistency boundary:

- principal/admission (including usage/accounting reconcile), replay, account,
  capability, health, surface circuit, routing policy, Asset metadata, Asset
  content, Tenant-confidential content, Render staging, and Render Job stores;
- capability-oriented Provider Adapter operations for chat, streaming,
  render, probe, capability observation, and same-attempt recovery;
- purpose-bound Credential Vault authorize/use/rewrap/revoke/logical-delete/purge;
- JobRuntime enqueue/worker plumbing;
- controllable Clock and IDGenerator;
- AuditRecorder, TelemetryRecorder, and RequestLogRecorder.

Each port returns canonical outcomes and safe errors. No port returns a SQL
transaction, Provider SDK value, raw queue payload, ciphertext, stack trace,
or unbounded secret value.

## Interface Contract

The module boundary is:

```text
apps/gateway/
  cmd/gateway/main.go
  internal/domain
  internal/application
  internal/ports
  internal/transport/http
  internal/adapters
  internal/infrastructure/{vault,persistence,jobs,observability}
  internal/composition
  internal/contracttest
```

Dependency direction is inward:

```text
domain <- application <- transport/http
domain + ports <- adapters and infrastructure implementations
composition -> all concrete layers
contracttest -> composition -> public HTTP handler
```

The dependency graph forbids domain/application imports of transport,
Provider, concrete infrastructure, queue, or configuration packages. The
composition root is the sole place that names concrete implementations.

The persistence contract is logical rather than CRUD-shaped. It must expose
atomic operations for replay claim/no-steal, Tenant Asset reservation,
idempotent Render Job creation, worker fencing, attempt commit/recovery,
scoped health observations, stale-write rejection, and output placement by
stable placement key. Physical tables and transactions remain an
implementation concern.

The Vault contract is purpose/resource/Tenant-bound. It authorizes decrypt,
then injects non-exportable `SecretMaterial` into capability-specific Adapter
methods rather than returning a general secret byte slice or treating a
credential handle as authority. Secret material must not enter response,
error, log, metric, trace, audit, queue, snapshot, or contract-fixture
projections.

The JobRuntime contract transports only `domain.SafeJobReference` projections
of the single durable `domain.JobRef` identity and supports at-least-once
delivery. It does not own Provider retries. Composition exposes `RunWorkers`
to start the consumer against the same `JobExecutor` policy used by production
and HTTP fixtures that need durable execution.

## Data Model

No concrete schema or migration is selected. Logical persistence records must
preserve the invariants already locked by #6–#17 and #20:

- Tenant ownership and non-enumeration for every scoped resource;
- replay scope, fingerprint, claim state, and terminal result independent of
  resource lifetime;
- Asset `committed + reserved` bytes/count accounting with one-time release;
- Render Job lease/fencing, attempt commit certainty, immutable result
  manifest, and output placement identity;
- Provider Account credential-version and Health Condition fencing;
- safe audit projections without secret or content retention outside policy.

Stores may share a physical transaction, but the application sees logical
ports and cannot depend on table names, indexes, SQL isolation syntax, or
driver-specific errors.

## UI / Platform Impact

No UI, Photoshop Plugin, deployment, or runtime platform change is included.
The stable `/v1` contract remains the only client-facing authority. A future
production composition will expose an `http.Handler`; no framework-specific
router is selected here.

## Observability

Audit and telemetry are typed outer ports. Required observations include
request/correlation identifiers, safe operation/resource scope, gate outcome,
commit certainty, retry-owner outcome, and bounded side-effect counts. They
must exclude credentials, ciphertext/envelope material, prompts/content,
foreign existence, raw Provider responses/URLs, queue payloads, stack traces,
and worker internals.

The request-log port emits one canonical JSON line per HTTP request containing
`timestamp`, `level`, `request_id`, `user_id` when known, `action`,
`duration_ms`, `status_code`, and safe `message`. Application logs are
operational records; product/security audit records are a separate durable
projection with separate retention and failure semantics. Protected
operations require an audit intent before Vault decrypt or Provider execution.
If the audit dependency cannot accept the required record, the operation fails
closed. Ordinary logs and metrics cannot substitute for that authorization or
audit proof.

## Alternatives Considered

1. **HTTP handlers call repositories and adapters directly.** Rejected;
   policy ordering and retry ownership would be duplicated across routes.
2. **A single generic Provider interface and a single generic Store.**
   Rejected; chat, render, probe, health, replay, and output placement have
   different invariants and atomicity requirements.
3. **Use mocks or private application calls as the primary contract seam.**
   Rejected; #20 requires public HTTP + real composition with controlled ports.
4. **Return plaintext credential bytes from Vault.** Rejected; bounded use is
   safer and makes secret exposure observable.
5. **Lock a concrete SQL schema or queue in this decision.** Rejected; the
   boundary and atomic logical guarantees are required now, while technology
   and operational benchmarks belong to the implementation story.
