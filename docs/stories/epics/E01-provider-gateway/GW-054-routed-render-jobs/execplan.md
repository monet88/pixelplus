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

1. Completed: public idempotent queued creation through real composition.
2. Completed: domain and port contracts for jobs, replay, staging, audit,
   confidential prompt/Asset/credential injection, and safe queue references.
3. Completed: worker claim, lease heartbeat, attempt truth, capture, placement,
   terminal audit, cleanup, and completion through `Runtime.RunWorkers`.
4. Completed: cancellation, fencing/recovery, output-only retry, staging expiry,
   and no-rerender redelivery behavior.
5. Completed: ownership, capability/health/Vault, replay, audit-before-allow,
   readiness recovery, and redaction negative cases.
6. Completed: focused TDD cycles and full build/vet/test/race validation.
7. Completed: iterative Standards + Spec reviews and review-fix commits through
   `5a12b9c`; final Standards review reported no open findings.

## Delivery Checkpoint

- Base: `main@2c6edb3`.
- Product head: `5a12b9c`.
- Scope: local feature branch only; no push or pull request.
- GitNexus final product delta: 1,362 changed symbols, 52 affected processes,
  `CRITICAL` aggregate risk because the vertical slice crosses public HTTP,
  composition, worker, persistence, Vault, and Asset flows.

## Stop Conditions

Pause for human confirmation if:

- Implementation requires changing a locked API route or Render Job lifecycle.
- A new third-party dependency, database schema, or external queue is required.
- Provider-specific behavior lacks a fail-closed controlled-port representation.
- Validation would need to weaken Tenant isolation, fencing, commit certainty,
  output capture, or no-duplicate-render requirements.
