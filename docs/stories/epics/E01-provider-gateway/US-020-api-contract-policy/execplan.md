# Exec Plan — US-020 Public API Contract Policy

## Goal

Publish and prove one stable Public API contract that reconciles issues #18 and #19 and locks versioning, compatibility, deprecation, idempotency, security, and future real-composition contract-test policy without implementing Gateway runtime behavior.

## Scope

In scope:

- Merge the accepted inference and management operations into one stable `/v1` OpenAPI 3.1.1 artifact.
- Define semantic release and backward-compatibility rules for the stable major.
- Define deprecation notice, support window, successor, and removal gates.
- Define HTTP idempotency scope, fingerprints, replay outcomes, retention, per-operation requiredness, secret handling, and retry ownership.
- Define the future contract suite entrypoint, controlled test ports, forbidden seams, and observable side-effect assertions.
- Add executable stable-contract validation and black-box mutation tests.
- Keep the retained prototype artifacts and their documentation consistent with the stable package.

Out of scope:

- Gateway runtime, concrete composition root, Go interfaces, package layout, database schema, or job implementation.
- Runtime end-to-end contract execution before those seams exist.
- New dependencies or schema migrations.
- Publishing, pushing, or changing GitHub issue state.

## Risk Classification

Risk flags:

- Authorization.
- Audit/security.
- External systems.
- Public contracts.
- Existing behavior.
- Weak proof for runtime composition that does not exist yet.
- Multi-domain.

Hard gates:

- Authorization and Tenant ownership behavior.
- Audit/security and direct-secret ingress.
- External Provider retry behavior.

## Work Phases

1. Pin issue #20 and the base commit; inspect #18/#19 artifacts and dependent lifecycle specifications.
2. Record high-risk intake, story proof expectations, and the durable API-policy decision.
3. Add a failing public CLI validation seam for the absent stable contract.
4. Publish the unified stable artifact and normative policy, then make the focused validator green.
5. Add mutation cases for compatibility, deprecation, idempotency, secret handling, and composition-test drift.
6. Update `CONTEXT.md`, contract indexes, and retained prototype specifications.
7. Run stable and retained contract checks, independent Standards/Spec review, and final scope inspection.
8. Record Harness proof and a Detailed trace, then commit the verified specification work.

## Stop Conditions

Pause for human confirmation if:

- A change would implement runtime Gateway behavior or choose concrete #21 interfaces/package layout.
- A fix would weaken Tenant authority, non-enumeration, secret handling, retry ownership, or validation requirements.
- A database migration, dependency addition, artifact deletion, or source-of-truth replacement becomes necessary.
- Review reveals product intent that conflicts with issue #20 or an accepted prerequisite specification.
