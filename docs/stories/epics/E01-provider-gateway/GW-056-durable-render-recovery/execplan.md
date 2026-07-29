# Exec Plan

## Goal

Close all six issue #56 review findings without weakening Tenant isolation, commit truth, or the Pure-Go dependency budget.

## Scope

In scope:

- OS advisory locks for Render Job and replay ledgers.
- Incremental validated JSONL tail reload.
- Durable job/replay invariant validation.
- Startup replay reconciliation before queue recovery/readiness.
- Real post-payload restart recovery proof and stale-fence no-mutation proof.

Out of scope:

- Public API changes.
- New persistence or queue dependencies.
- Provider-specific status lookup.

## Risk Classification

Lane: `high_risk`.

Risk flags:

- Tenant-owned durable records.
- External Provider duplicate-side-effect prevention.
- Idempotency and public create behavior.
- Cross-platform process locking.
- Existing worker/composition behavior.
- Previously weak crash-point proof.

Hard gates:

- External Provider behavior.
- Audit/security and Tenant isolation.

## Work Phases

1. Replace crash-sticky marker locks and prove lock-anchor/release behavior.
2. Implement incremental tail replay with replacement/truncation handling.
3. Reconcile durable jobs to replay ownership during startup.
4. Rewrite restart and invariant regression tests.
5. Run full cross-platform, race, security, review, and change-impact gates.

## Stop Conditions

Pause if the fix requires changing a locked lifecycle/API contract, adding a dependency, weakening validation, or authorizing any replacement render after payload transmission.
