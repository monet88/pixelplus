# Overview — US-017 Health Spec Review Fixes

## Current Behavior

Issue #17 defines canonical Health State/Reason values, but #9, #10, #11, #12, #14, and #16 still consume older single-token health labels. The mismatch leaves routing/error mapping ambiguous. `recovery_probe_failed` also permits an unspecified prior-or-escalated state, and #17 names a JSON-like retry field while deferring wire encoding.

## Target Behavior

- Health State + Health Reason from #17 are the sole canonical persisted and routing vocabulary.
- Older #9 tokens are deterministic compatibility inputs/projections, never competing canonical states.
- Routing, capability, runtime error, and retry documents consume canonical pairs consistently.
- Generic recovery-probe failure has a deterministic state transition for every prior state.
- Retry timing remains a logical value; #18/#20 own field/header encoding.

## Affected Users

- Tenant administrators viewing account health/remediation.
- Gateway, Adapter, routing, capability, and lifecycle implementers.
- Platform/security operators reviewing health controls and audit outcomes.

## Affected Product Docs

- `CONTEXT.md`
- `docs/spec/provider-account-health-cooldown-and-operator-controls.md`
- `docs/spec/provider-account-connection-and-credential-lifecycle.md`
- `docs/spec/capability-snapshot-and-model-availability-semantics.md`
- `docs/spec/tenant-scoped-routing-fallback-affinity-leases.md`
- `docs/spec/chat-execution-and-streaming-lifecycle.md`
- `docs/spec/durable-render-job-and-output-retry-lifecycle.md`
- `docs/spec/canonical-errors-and-retry-ownership.md`

## Non-Goals

- Implementing any Gateway behavior.
- Freezing OpenAPI/JSON/header names.
- Reopening accepted routing, retry, lifecycle, or Auth Mode risk decisions.
