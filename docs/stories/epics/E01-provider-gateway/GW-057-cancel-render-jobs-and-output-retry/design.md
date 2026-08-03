# Design

## Domain Model

`RenderJob` remains Tenant-owned, terminally immutable, and uses `cancel_requested` to distinguish cancellation intent from proven upstream stop. `ResultManifest` and `PlacementKey(tenant_id, job_id, output_entry_id)` remain the only source of output delivery recovery.

## Application Flow

1. The cancel HTTP route authenticates and authorizes `jobs.manage`, resolves the job within the caller Tenant, then calls the atomic job cancellation mutation.
2. A queued job transitions to `canceled`; a running job transitions to `cancel_requested`; terminal jobs are no-ops. Cleanup preserves conservative settlement semantics.
3. The output retry route resolves the same-Tenant job and entry, requires an immutable manifest, and calls placement recovery only. It cannot enter `ExecuteJob` or the Render Adapter.
4. The contract fake records admission operation tokens so proof distinguishes image generation from delivery retry.

## Interface Contract

- `POST /v1/render-jobs/{job_id}/cancel` returns a flat `RenderJobCancelResponse` acknowledgement (job_id, lifecycle_state, upstream_abort_attempted, upstream_stop_confirmed, state_revision, request_id) — not a full RenderJob projection.
- `POST /v1/render-jobs/{job_id}/outputs/{output_entry_id}/retry` returns `202 OutputRetryResponse` with `re_render=false`.
- Foreign and unknown job/output identifiers return the same `404 resource_not_found` behavior and contain no resource reference.

## Data Model

No schema change. The existing immutable manifest and `(tenant_id, job_id, output_entry_id)` placement key remain the idempotency identity for output placement.

## UI / Platform Impact

No UI or platform change.

## Observability

Existing cancel and output-retry audit actions remain the observable lifecycle events. Public projections exclude prompts, credentials, byte content, temporary URLs, and foreign-resource data.

## Alternatives Considered

1. Retry by submitting a new image request. Rejected: it could create duplicate Provider work and consumes image admission.
2. Return a distinct foreign-resource error. Rejected: it becomes an existence oracle.
