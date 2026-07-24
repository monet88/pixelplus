# Exec Plan

## Goal

Implement issue #54 as one independently verifiable Pure-Go vertical slice:
public HTTP creation/retrieval/cancellation/output retry plus exported worker
execution through controlled ports.

## Scope

In scope:

- Render Job domain values, state transitions, attempt certainty, fencing,
  immutable manifests, output delivery state, and stable placement identity.
- Logical Render Job, Provider render, account lease, staging/placement, replay,
  audit, telemetry, and request-log ports required by the slice.
- Application create/read/cancel/output-retry commands and the real
  `JobExecutor` implementation.
- Stable `/v1/images/*` and `/v1/render-jobs/*` transport routes.
- Additive composition wiring with fail-closed defaults.
- Contract tests through `Runtime.Handler()` and `RunWorkers`/`JobExecutor`.

Out of scope:

- Production Provider protocol implementation or live Provider acceptance.
- A new durable database or external queue dependency.
- Numeric lease/retry/staging tuning owned by #17.
- Changes to canonical error vocabulary owned by #16 beyond reusing existing
  constructors or adding already-frozen render error values when required.

## Risk Classification

Lane: `high_risk`.

Risk flags:

- Authorization and Tenant ownership.
- Durable job/attempt/manifest state.
- Audit, redaction, and sensitive Provider/Asset data.
- External Provider and queue behavior.
- Stable Public API contracts.
- Existing composition and contract-test behavior.
- Weak direct proof on the current foundation `JobExecutor`.

Hard gates:

- Authorization.
- Audit/security.
- External Provider behavior.

GitNexus pre-edit impact:

- `httptransport.NewHandler`: `HIGH`, 18 impacted symbols, 2 affected flows.
- `composition.New`: `HIGH`, 174 impacted symbols, 2 affected flows.
- `application.JobExecutor`: `MEDIUM`, 13 impacted symbols.
- `Runtime.RunWorkers`: `LOW` direct upstream impact, but remains part of the
  required public worker proof path.

## Work Phases

1. Add one failing public contract test for idempotent queued creation.
2. Add the minimum domain and port contracts to make that slice pass.
3. Add worker claim, lease, attempt, capture, placement, and completion slices.
4. Add cancellation, fencing/recovery, and output-only retry slices.
5. Add ownership, capability/health/Vault, replay, and redaction negative cases.
6. Run focused tests after each slice and the full build/vet/test/race matrix.
7. Run Standards + Spec review, fix findings, inspect GitNexus changes, and
   commit locally to the feature branch.

## Stop Conditions

Pause for human confirmation if:

- Implementation requires changing a locked API route or Render Job lifecycle.
- A new third-party dependency, database schema, or external queue is required.
- Provider-specific behavior lacks a fail-closed controlled-port representation.
- Validation would need to weaken Tenant isolation, fencing, commit certainty,
  output capture, or no-duplicate-render requirements.
