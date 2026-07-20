# Exec Plan - US-022 Implementation-Ready Provider Gateway Specification

## Goal

Assemble the accepted Provider Gateway evidence and contracts into a
validated implementation handoff that removes mandatory product, domain,
interface, security, and execution choices from future implementation
planning while preserving each source's authority.

## Scope

In scope:

- Source hierarchy and conflict-resolution rules.
- Six-Auth-Mode capability evidence ledger using the four canonical tokens.
- Cross-domain decision ledger with observable behavior, failure semantics,
  security impact, and dependencies.
- Dependency-ordered vertical implementation slices and their public proof
  seams.
- Deferred item register with reason, dependencies, and reopen trigger.
- Mechanical validator, mutation tests, story proof, and a separate blocked
  Gateway implementation issue.

Out of scope:

- Any Gateway runtime or infrastructure implementation.
- New product/domain/Public API/security/execution decisions.
- Production topology, migration, launch, billing, or Photoshop Plugin work.

## Risk Classification

Risk flags:

- Authorization and immutable Tenant ownership.
- Audit/security and Provider Credential custody.
- Public API compatibility and idempotency.
- Existing accepted behavior across multiple product domains.
- External Provider evidence and risk envelopes.
- Weak proof if assembly completeness is checked only by human reading.

Hard gates:

- The assembly cannot override its normative sources.
- Every capability claim uses `verified`, `conditionally_supported`,
  `unsupported`, or `unverified` and links declared evidence.
- Every locked decision states observable behavior, failure semantics,
  security impact, and dependencies.
- Every deferred item states why it is deferred, what it depends on, and what
  opens it.
- Runtime implementation belongs to a different issue and does not start in
  this story.

## Work Phases

1. Discovery: fetch issue #22 and dependencies; read Harness intake/context,
   the domain glossary, stable API policy, architecture decision, evidence
   summaries, and deferred sections.
2. Design: define source hierarchy, stable ledger IDs, implementation slices,
   and explicit implementation-choice versus product-decision boundaries.
3. Validation planning: select the repository specification-package seam and
   write mutations for missing authority, invalid vocabulary, incomplete
   decisions, incomplete required sets, and invalid deferrals.
4. Implementation: add the validator, manifest, human specification, story
   packet, entrypoint links, and separate implementation issue #42.
5. Verification: run the focused mutation tests, specification validator,
   retained contract validators, full repository test suite, whitespace check,
   Harness story proof, and independent review.
6. Harness update: record evidence, complete the story through fresh proof,
   and record the final trace.

## Stop Conditions

Pause for human confirmation if:

- two same-authority normative sources conflict;
- completing the gate would require a Public API, security, authorization,
  lifecycle, risk, retry, retention, or architecture change;
- a validation requirement must be weakened;
- issue #42 would need to start runtime implementation rather than remain a
  separate blocked handoff;
- an unresolved item lacks a safe default or reopen trigger and is therefore
  a current product decision rather than a deferral.
