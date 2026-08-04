# Overview

## Current Behavior

PR #87 implements the T15 non-streaming chat spine (#58), but two-axis review
(Standards + Spec) found contract and spec deviations: a foreign or unknown
`x_pixelplus.provider_account_id` pin fails closed with `routing_no_candidate`
(409) instead of the non-enumerating 404-class the routing spec locks; the
fallback walk never consults `fallback_auth_modes`, so cross-Auth-Mode fallback
can run without the policy naming both modes (NF-XMODE); the request wire
rejects contract-declared fields (`temperature`, `max_tokens`, `top_p`, `n`,
`stop`, `user`, message `name`, array `content`) as unknown; the documented
`conversation_id` affinity key is parsed then dropped; the explicit-pin (P1)
path has no proof-seam test; and decision record 0012 contradicts the shipped
code on HealthStore ownership and the production replay-store default.

## Target Behavior

- A foreign/unknown explicit pin returns `resource_not_found` (404-class,
  non-enumerating) with zero Adapter calls and zero Vault decrypts; a
  same-Tenant pinned but gated account keeps its specific gate class.
- Cross-Auth-Mode fallback runs only when the Tenant Routing Policy's
  `fallback_auth_modes` names BOTH the primary and the target mode; same-mode
  fallback needs no listing (routing spec §6.2, NF-XMODE).
- The request wire accepts every field the published `ChatCompletionRequest`
  schema declares and shape-validates their values; non-text content parts are
  rejected `invalid_request`.
- `conversation_id` drives a policy-gated (`affinity.enabled`) soft P3
  preference: prefer the account that last served the conversation while it
  remains in the candidate set, otherwise fall through to P4 policy — never
  cross-Tenant, never cross-Auth-Mode.
- Decision 0012 matches the code, including the deliberate claim-before-
  admission ordering justification.
- Presence-aware decoding rejects a present JSON null on the non-nullable
  fields (`stream`, numeric options) and null `stop` items as
  `invalid_request`; the idempotency fingerprint binds every accepted request
  field, so same-key requests differing in tuning or routing inputs conflict
  instead of replaying (round-2 review).

## Affected Users

- Tenant clients calling `POST /v1/chat/completions` (non-streaming).
- Tenant operators authoring Routing Policy fallback chains and affinity.
- Gateway operators relying on decision records as the drift guard.

## Affected Product Docs

- `docs/spec/tenant-scoped-routing-fallback-affinity-leases.md` (§3.2, §4.1,
  §5.1, §6.2, §7.1–§7.2)
- `docs/spec/chat-execution-and-streaming-lifecycle.md` (§3.1, §5.2, §8, §10.3)
- `docs/spec/canonical-errors-and-retry-ownership.md` (§5.4, §6, §7)
- `contracts/openapi/pixelplus-public-api-v1.yaml` (`ChatCompletionRequest`,
  `ChatMessage`, `ChatXPixelPlus`)
- `docs/decisions/0012-chat-execution-review-decisions.md`

## Non-Goals

- Streaming (T16), cancel/residual reconcile (T17), real Provider chat
  Adapters (T19–T23), durable chat replay ledger (T25/#88).
- Carrying generation-tuning fields into the Adapter port (deferred until
  real Adapters exist). They are carried into the canonical command only to
  bind the idempotency fingerprint (round-2 review).
- Render-spine fallback auth-mode gating (render has no fallback walk today;
  recorded as a separate discovery).
- PR thread replies, thread resolution, or merge.
