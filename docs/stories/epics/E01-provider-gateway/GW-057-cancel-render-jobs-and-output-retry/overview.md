# Overview

## Current Behavior

Gateway already exposes tenant-scoped Render Job cancel, terminal job retrieval, and manifest-backed output retry routes. Existing proof covered queued cancellation, running `cancel_requested`, post-payload recovery, and one output retry, but not foreign mutation parity or repeated retry admission identity.

## Target Behavior

Tenant owners can cancel a Render Job idempotently: queued jobs become `canceled`; running jobs first expose `cancel_requested`; residual/recovery work remains conservative and never creates a replacement render. Output retry reuses the captured manifest and stable placement identity, performs no new image render admission, and never calls the Render Adapter again. Foreign cancel and output IDs are indistinguishable from unknown identifiers.

## Affected Users

- Tenant clients managing their Render Jobs and generated output delivery.
- Operators reconciling canceled, residual, or output-placement work.

## Affected Product Docs

- `docs/spec/durable-render-job-and-output-retry-lifecycle.md`
- `docs/spec/canonical-errors-and-retry-ownership.md`

## Non-Goals

- Provider-specific upstream abort implementation.
- Re-rendering an already completed, canceled, failed, or uncertain job.
- Changing public route shapes or durable storage format.
