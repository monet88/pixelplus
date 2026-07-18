# Exec Plan — US-017 Health Spec Review Fixes

## Goal

Resolve all confirmed PR #37 review findings so Provider Account Health has one canonical vocabulary, deterministic recovery transitions, and field-agnostic retry timing semantics.

## Scope

In scope:

- Make #17 Health State + Health Reason canonical across dependent specifications.
- Define total legacy #9-token normalization for compatibility.
- Update routing, capability, execution, render, and canonical-error references that consume the old vocabulary.
- Define deterministic `recovery_probe_failed` transitions.
- Remove the premature `retry_after_seconds` wire-field commitment.

Out of scope:

- Gateway runtime code, persistence schema, OpenAPI paths/fields, headers, or UI.
- Changing Tenant routing authority, retry ownership, lifecycle authority, or Auth Mode risk policy.

## Risk Classification

Risk flags:

- Authorization.
- Audit/security.
- External systems.
- Public contracts.
- Existing behavior.
- Multi-domain.

Hard gates:

- External Provider behavior and security-sensitive account controls.

## Work Phases

1. Record high-risk intake and story proof expectations.
2. Add canonical compatibility mapping and update dependent specs.
3. Lock deterministic recovery-probe failure behavior.
4. Make retry timing logical and field-agnostic.
5. Run consistency, diff, and independent review checks.
6. Record Harness evidence and implementation note.

## Stop Conditions

Pause for human confirmation if:

- A fix would change Tenant routing/fallback authority rather than clarify it.
- A fix would weaken lifecycle, risk, capability, audit, or retry-owner gates.
- Exact HTTP/JSON encoding must be chosen before #18/#20.
