# 0011 Provider Credential Reauthentication Cutover

Date: 2026-07-22

## Status

Accepted

## Context

Issue #48 adds replacement credentials to a Gateway slice that already exposes
first-connect submission and server-owned OAuth. The stable contract requires a
pending version to remain non-authorizing until its own validation and probe,
while the prior version remains available for authorized in-flight work.

## Decision

Keep one account-level replacement marker (`ActiveOAuthAuthorizationID`) as the
single-flight fence for direct and OAuth replacement. Track monotonic allocated,
pending, active, and origin lifecycle metadata inside the safe domain object;
only the existing public credential version projection is exposed. Add explicit
Vault revocation and account-store pending-version preconditions. Promotion is
performed only after pending validation, probe, and capability evidence succeed;
failed pending material is revoked and the origin lifecycle is restored.

## Alternatives Considered

1. Add a second direct/OAuth mutex. Rejected because two locks would permit a
   direct and OAuth replacement to race across different gates.
2. Treat the newly allocated version as current immediately. Rejected because
   routing and capability snapshots could authorize an unprobed credential.
3. Add a SQL transaction in this slice. Rejected because the accepted Pure-Go
   dependency budget and current composition use controlled ports without a
   durable schema.

## Consequences

Positive:

- Replacement remains same-account and same-mode.
- Failed versions are never reused, and stale writers cannot promote them.
- Public HTTP proof can observe safe version/lifecycle outcomes without secrets.

Tradeoffs:

- The current in-memory port boundary models cutover with idempotent Vault
  revocation plus account fencing; a later durable store must implement the same
  atomic transaction semantics.
- The in-memory flow persists the promoted account before revoking the prior
  Vault version. This makes the public projection durable before cleanup, at the
  cost of a temporary old-version residue if revocation is unavailable; the
  durable Vault adapter must reconcile that idempotently.

## Follow-Up

- #49 owns administrative enable/disable/delete behavior beyond preserving a
  disabled-origin replacement.
- Provider-specific Vault transactions and live adapters remain #61-#66.
