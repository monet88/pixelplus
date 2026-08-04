# Exec Plan

## Goal

Resolve every actionable PR #87 review finding while preserving issue #58's
public HTTP proof seam and the Provider Gateway security boundaries.

## Scope

In scope:

- Foreign/unknown explicit pin → 404-class `resource_not_found` before any
  candidate work; same-Tenant pinned-but-gated keeps its specific class.
- Fallback walk gates cross-Auth-Mode targets on `fallback_auth_modes` naming
  both modes; fail closed otherwise (NF-XMODE).
- Request wire accepts all contract-declared `ChatCompletionRequest` /
  `ChatMessage` fields with shape validation; non-text content parts reject.
- Policy-gated P3 conversation affinity over `conversation_id` with an
  in-memory store (soft preference, never an authority).
- Proof-seam contract tests for the explicit pin (P1), foreign pin 404,
  cross-mode fallback gating, affinity prefer/yield, and wire acceptance.
- Decision 0012 corrected (HealthStore ownership, replay-store default) and
  extended with this round's rulings (claim-before-admission, cross-mode
  fallback, wire acceptance, affinity deferral).
- Dead code removed: unused error constructors/codes/stage, speculative
  `ChatCompletion.FirstIndex/Empty`, `ChatOutcomeSuccess`, dead validation
  branches, and the discarded chat digester usability probe.
- Unrelated GitNexus stat churn reverted from AGENTS.md / CLAUDE.md.

Out of scope:

- Streaming, cancellation, real Adapters, durable replay/affinity stores.
- Render-spine changes beyond what PR #87 already shipped.
- Public contract changes (the OpenAPI schema is already correct; the code is
  being brought up to it).

## Risk Classification

Risk flags:

- Authorization (Tenant scoping / non-enumeration on the pin path).
- Audit/security (fail-closed fallback gating).
- Public contracts (request wire acceptance, error class mapping).
- Existing behavior (test-covered fallback and selection change).
- Weak proof (P1 pin previously untested at the proof seam).

Hard gates:

- Authorization.
- Audit/security.

Lane: high-risk.

## Work Phases

1. Correct decision 0012 and revert unrelated AGENTS.md/CLAUDE.md churn.
2. Remove dead code (domain errors, chat domain helpers, composition probe).
3. Fix selection: foreign-pin 404 + cross-mode fallback gating.
4. Add policy-gated P3 affinity (domain scope, port, memory store, wiring).
5. Widen the request wire to the published contract shape.
6. Add/adjust proof-seam contract tests for all of the above.
7. Run targeted and full validation, race checks, and GitNexus review.
