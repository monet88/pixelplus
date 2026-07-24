# Exec Plan

## Goal

Resolve every actionable PR #81 review finding while preserving issue #51's
public HTTP proof seam and the Provider Gateway security boundaries.

## Scope

In scope:

- Restore the ADR 0009 `HealthStore` boundary and independent composition port.
- Enforce durable CAS and one recovery permit per condition revision.
- Make dependency-failure recovery bounded instead of permanently occupied.
- Preserve scoped Health Reason authority and emit normative transition audit.
- Reject unsafe restored state and make repeated durable writes Windows-safe.
- Persist Provider Account state outside container tmpfs.
- Remove Docker CI, Go toolchain, and generated metadata changes unrelated to #51.

Out of scope:

- Provider SDK or credential-custody changes.
- Database schemas, migrations, or third-party dependencies.
- Circuit correlation engines, absolute reset timestamps, and inference routing.
- PR thread replies, thread resolution, readiness changes, or merge.

## Risk Classification

Risk flags:

- Audit/security.
- External Provider behavior.
- Public contracts.
- Existing behavior.
- Weak proof.
- Multi-domain deployment and application changes.

Hard gates:

- Audit/security.
- External Provider behavior.

## Work Phases

1. Reproduce each review finding with a focused failing test.
2. Restore the HealthStore application and composition boundary.
3. Add durable CAS, validation, and restart behavior.
4. Correct recovery, reason, and audit transitions.
5. Correct container persistence and remove unrelated scope.
6. Run targeted and full validation, race checks, and GitNexus review.
7. Record Harness proof, create a follow-up commit, and push the PR branch.

## Stop Conditions

Pause for human confirmation if:

- A new dependency or migration becomes necessary.
- Provider credential custody would change.
- Public API shape must diverge from the accepted specifications.
- Validation requirements would need to be weakened.

## Implementation Note

- Decision not in plan: first-connect reserves only `LastAllocatedVersion` via
  AccountStore CAS before `Vault.Put`; lifecycle/current version advance only
  after Vault and HealthStore succeed.
- Tradeoff: failed intake attempts leave gaps in credential versions. This is
  intentional to satisfy ADR 0011's never-reuse rule without retaining material.
- Decision not in plan: a legacy whole-map account snapshot is rewritten to the
  JSONL ledger on its first mutation; subsequent writes remain append-only.
- Risk: HealthStore can conservatively advance before a lifecycle CAS conflict;
  retries converge the credential epoch and never relax lifecycle usability.
