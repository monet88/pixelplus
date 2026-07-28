# Design

## Domain Model

- `RenderJob` is Tenant-owned and immutable in operation/model/input identity.
- Lifecycle is exactly `queued`, `running`, `cancel_requested`, `canceled`,
  `failed`, or `completed`; terminal kind never changes.
- A worker lease carries a monotonically increasing fencing token. Every worker
  mutation checks the current token.
- One attempt ledger records `not_started`, `not_committed`, `committed`, or
  `unknown`. `committed` and `unknown` forbid replacement rendering.
- A result manifest is immutable, ordered, and durable before completion.
- Each output entry has a stable placement identity and independent delivery
  state: `pending`, `available`, `expired`, or `failed`.

## Application Flow

Create:

1. Authenticate and derive the Security Principal server-side.
2. Enforce operation scope and same-Tenant visibility of named Assets/accounts.
3. Apply request-size and strict validation gates.
4. Claim scoped idempotency before admission; matching terminal replay returns
   the existing job, while conflict/uncertainty creates nothing.
5. Apply admission, current account usability/risk/capability/health, routing,
   and Vault authorization gates.
6. Atomically create one queued job/reservation and enqueue one
   `SafeJobReference` containing only Tenant and job identities.

Worker:

1. Atomically claim the job and receive a fencing token.
2. Recheck same-Tenant Asset/account/capability/health/Vault gates and acquire
   one hard `render_job` account lease.
3. Record the attempt before payload transmission.
4. Perform the controlled render once. Only authoritative `not_committed` proof
   permits a bounded internal retry/fallback.
5. Durably capture the immutable manifest, reserve/place the output Asset by
   stable placement key, then publish `completed` with the same fence.

Cancellation and recovery preserve commit truth: a queued cancel never reaches
the Provider; a running cancel first becomes `cancel_requested`; stale workers
cannot mutate; uncertain attempts fail closed; output retry never calls render.
If recovery sees `PayloadSent=true` but only empty/`not_started` commit evidence,
terminal cancel records `unknown`; it preserves authoritative `not_committed`,
`committed`, or already-`unknown` evidence.

## Interface Contract

- `POST /v1/images/generations`
- `POST /v1/images/edits`
- `POST /v1/images/inpaints`
- `GET /v1/render-jobs/{job_id}`
- `POST /v1/render-jobs/{job_id}/cancel`
- `POST /v1/render-jobs/{job_id}/outputs/{output_entry_id}/retry`

The transport parses unknown input and carries oversize/malformed observations
to the application so A0/A1 precede A2/validation. Public projections never
contain credentials, prompt or Asset bytes, temporary Provider URLs, or foreign
resource existence.

## Data Model

This story adds logical port contracts and controlled in-memory/fail-closed
implementations only. It does not select a physical database schema. Atomicity,
fencing, idempotency, immutable manifest, and placement guarantees are expressed
through the application-owned port methods so a future durable implementation
cannot weaken them.

Durable audit and cleanup obligations are represented on the job with markers
for claim, output placement, terminal audit, prompt purge, admission settlement,
and staging purge. Redelivery retries an owed obligation without reopening
Provider execution.

## UI / Platform Impact

No browser, mobile, Photoshop, or desktop UI change. The affected platform
surfaces are the Go HTTP server and worker process.

## Observability

- One canonical request log per HTTP request with safe operation/status fields.
- Product/security audit records for create, claim/execution outcome, cancel,
  completion, and output delivery without prompt/content/credential data.
- Telemetry labels use stable operation and canonical error classes only.
- Worker logs may identify safe Tenant-local job/account ids but never secret or
  content values.

## Alternatives Considered

1. Call application methods directly in tests. Rejected because issue #54 and
   ADR 0009 require real public composition proof.
2. Treat queue redelivery as render permission. Rejected because at-least-once
   delivery must not become at-least-once Provider execution.
3. Mark completed after Provider response and place Assets later. Rejected for
   this implementation slice because issue #54 acceptance requires the immutable
   manifest and output Asset placement to be durable before completion.
4. Let the Adapter retry full renders. Rejected because the Render Job execution
   layer is the sole retry owner.
5. Preserve `not_started` while cancel-recovering a post-payload attempt.
   Rejected because payload transmission means Provider commit may be unknown;
   recording `not_started` could authorize an unsafe replacement render.
