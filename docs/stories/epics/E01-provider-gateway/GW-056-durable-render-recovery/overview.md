# Overview

## Current Behavior

Issue #56 introduced file-backed Render Job and idempotency replay ledgers, but review found six crash-recovery gaps: marker locks can survive process death, every operation rereads the entire ledger, restore accepts impossible cross-field state, a crash can leave an executable job paired with an in-progress replay, and existing fencing/restart tests do not prove the dangerous mutation and post-payload seams.

## Target Behavior

Both file stores use OS-owned advisory locks, apply only newly appended JSONL rows after initial restore, and fail closed on truncation/replacement or invalid durable state. Startup reconciles durable jobs into matching replay terminal records before queue publication recovery. A fresh runtime recovering a non-terminal post-payload job never invokes the Provider again.

## Affected Users

- Tenant clients retrying image creation after Gateway process loss.
- Operators restarting the Gateway after abrupt termination.
- Workers recovering possibly committed Render attempts.

## Affected Product Docs

- `CONTEXT.md`
- `docs/spec/durable-render-job-and-output-retry-lifecycle.md`
- `docs/spec/canonical-errors-and-retry-ownership.md`
- `docs/decisions/0009-pure-go-module-seams-and-dependency-budget.md`

## Non-Goals

- Changing the stable Public API or Render Job lifecycle.
- Adding a database, queue, Provider SDK, or third-party lock dependency.
- Retrying a committed or uncertain Provider render.
