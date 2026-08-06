# Overview

## Current Behavior

The `chatgpt_codex_oauth` Auth Mode is a first-class `domain.AuthMode` value:
`gated`, `SupportsRefresh`, `RequiresRiskAck`, `oauth_token_import` credential
class, and already covered by `CanonicalCapabilityBaseline`. But no Adapter
translates its protocol, and no operator feature-flag mechanism exists for
`gated` modes — `domain.LabProfile` is experimental-only by construction
(decision 0013). Today a Codex OAuth account can be **created** without an
operator flag (the create gate checks only `Prohibited` + `BlocksExperimental`),
and only the Tenant acknowledgement check stands between a stored credential and
use. The composition root never builds a Codex Adapter, so the mode dispatches to
the fail-closed foundation on every surface.

## Target Behavior

A Tenant whose operator enabled `chatgpt_codex_oauth` in the deployment's gated
profile AND who has explicitly acknowledged the residual risk can connect,
probe, chat, and stream through an independent Codex OAuth Adapter that:

- Translates the Codex Responses surface (`/backend-api/codex/responses`) and
  keeps OAuth refresh + protocol values inside the Adapter/Vault boundary.
- Classifies `usage_limit_reached`, `rate_limit_error`, auth-expired, and
  protocol-drift signals onto the existing health/probe vocabulary.
- Does not retry a full chat or stream operation; reports the
  application-required commit certainty (NotCommitted / Unknown / Committed)
  using the same ladder T18 established.
- Reports probe evidence bound to the exact Tenant, account, mode, credential
  version, operation, and model the command named.

When the operator flag OR the Tenant acknowledgement is absent, the create,
credential-use, capability-offer, and render-candidate gates reject before any
credential storage/use. A Codex account never silently falls back to ChatGPT Web
Access (FG-2 / §6.3).

## Affected Users

- Operator: enables the gated profile for a deployment (test composition today;
  environment surface deferred).
- Tenant: acknowledges residual risk at connection and uses Codex chat/stream.
- Reviewer: verifies the gated enablement and no-silent-fallback invariants
  through the public API seam.

## Affected Product Docs

- `docs/spec/auth-mode-risk-envelope-and-kill-criteria.md` §5.2, §6.2–6.3, §7
  (normative authority — no edit; consumed).
- `docs/spec/research/chatgpt-auth-mode-capability-evidence.md` §2.2, §3.2, §10.2
  (capability/credential evidence — no edit; consumed).
- `docs/decisions/0013-experimental-lab-profile-and-capability-baseline.md`
  (sibling decision; referenced).
- `docs/decisions/0014-gated-auth-mode-operator-feature-flag.md` (new — the
  gated enablement mechanism).

## Non-Goals

- A real `chatgptcodex.Transport` / live probe against a Codex account (blocked
  by #111 per-account credential binding; T19 ships the field nil, fixtures
  only).
- `ports.RenderAdapter` for Codex and the gated render registry (coherent
  second story; a Codex account whose operator flag is off is refused at the
  render candidate gate, while an operator-enabled Codex account passes that
  gate and fails closed at execution for lack of a `ports.RenderAdapter`).
- The operator environment surface in `cmd/gateway` (`GatedAuthModes` parsing;
  deferred, mirrors the 0013 follow-up for `ExperimentalLabAuthModes`).
- FG-5/KS-2 numeric challenge-rate thresholds and counters (#97).
- Multi-account-per-Tenant load balancing (deferred D-MULTI-ACCT).
- Promotion of `chatgpt_codex_oauth` toward `allowed` (blocked on D-OAI-TOKEN
  and D-COMM).
