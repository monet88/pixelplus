# Overview

## Current Behavior

The Gateway exposes Provider Account, capability, health, routing, and Asset
surfaces through the real Pure-Go composition. Image-generation Render Job
routes are not implemented, and the exported `JobExecutor` still fails closed
with `ErrJobExecutionUnavailable`.

## Target Behavior

An authenticated Tenant can create one idempotent durable image-generation,
edit, or inpaint Render Job through the stable Public API. The request passes
ownership, scope, request validation, replay, admission, routing, lifecycle,
risk, capability, health, Asset, and Vault gates before enqueue or Provider
work.

The exported worker claims the job with a fencing token, holds one same-Tenant
Provider Account lease, performs at most the allowed controlled upstream
attempt, durably captures an immutable result manifest, places the output Asset,
and only then publishes `completed`. Queue redelivery, stale workers, uncertain
commit state, cancellation, recovery, and output-delivery retry cannot create a
replacement render.

## Affected Users

- Tenant applications using the `/v1/images/*` and `/v1/render-jobs/*` API.
- Operators relying on durable job state, conservative recovery, and secret-free
  audit/telemetry.
- Gateway workers executing safe job references through `RunWorkers`.

## Affected Product Docs

- `CONTEXT.md`
- `docs/spec/durable-render-job-and-output-retry-lifecycle.md`
- `docs/spec/canonical-errors-and-retry-ownership.md`
- `docs/spec/tenant-scoped-routing-fallback-affinity-leases.md`
- `docs/spec/openai-compatible-inference-contract.md`
- `docs/decisions/0009-pure-go-module-seams-and-dependency-budget.md`
- `docs/decisions/0010-grok-xai-oauth-operation-surface-policy.md`

## Non-Goals

- Selecting a new database, queue service, Provider SDK, or third-party package.
- Changing the stable OpenAPI contract or the accepted Render Job state machine.
- Implementing chat execution, new Auth Modes, or cross-surface Provider fallback.
- Allowing output retry to recreate a Render Job or rerun generation/edit/inpaint.

