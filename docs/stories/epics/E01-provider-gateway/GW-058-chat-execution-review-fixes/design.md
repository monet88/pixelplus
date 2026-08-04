# Design

## Domain Model

Selection keeps the #11 ladder P0 → P1 → P2 → P3 → P4 → P5. P1 explicit
selection gains the locked non-enumeration rule: a pin that is not visible to
the principal (foreign or unknown — indistinguishable via
`ErrAccountNotVisible`) resolves to `resource_not_found` (404-class) before
candidate construction; a visible same-Tenant pin still passes C0–C5 and fails
with the specific gate class. P3 is populated by a new
`domain.ChatAffinityScope` (`tenant_id` + `conversation_id`): a soft
preference record, never a routing authority.

## Application Flow

1. `selectAccount` reads policy, resolves pin visibility (404 on foreign /
   unknown), builds the C0–C5 candidate set, then applies P3: when the request
   carries `conversation_id` and `policy.Affinity.Enabled`, the preferred
   account wins while it is in the candidate set; otherwise P4 picks the first
   policy-ordered candidate. Affinity store misses and errors fall through to
   P4 (a preference can never widen the set).
2. `attemptAccounts` keeps the single ordered fallback walk but skips a
   fallback candidate whose Auth Mode differs from the primary's unless
   `policy.FallbackAuthModes` names BOTH modes (routing spec §6.2 sentence 2,
   NF-XMODE). Same-mode targets need no listing; `candidateRejection` already
   excludes prohibited/experimental modes.
3. On a committed success with `conversation_id` and affinity enabled, the
   serving account is recorded best-effort; a record failure only loses a
   preference, so the completed response is never failed by it.
4. Replay claim stays BEFORE admission: canonical-errors §7.2.3 forbids a
   terminal replay from creating a new admission reservation or quota debit,
   and the claim loser holds no A6 slot, so §7.3.4's release-once premise is
   vacuously satisfied. This mirrors the render spine order.

## Interface Contract

No OpenAPI change: the code is brought up to the published
`ChatCompletionRequest`. The wire parses `temperature`, `max_tokens` (≥1),
`top_p`, `n` (≥1), `stop` (string | string[]), `user`, message `name`, and
`content` as string | text-part array (parts concatenated; non-text parts
reject `invalid_request`). Generation-tuning values are validated at the
boundary but not carried into the canonical command until real Adapters land
(T19–T23). New inward port `ports.ChatAffinityStore`
(`Preferred`/`Record`); production composition wires the process-local
`MemoryChatAffinityStore` — safe because affinity is a preference, not an
authority, so process loss degrades to P4 (unlike the replay ledger, which
fails closed).

## Data Model

No schema or migration. The in-memory affinity map is keyed by
`(tenant_id, conversation_id)`; the affinity window-class numeric remains
#17-owned and the process-local store is bounded by process lifetime.

## UI / Platform Impact

None.
