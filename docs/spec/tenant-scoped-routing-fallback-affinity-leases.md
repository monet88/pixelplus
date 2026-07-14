# Tenant-Scoped Routing, Fallback, Affinity, and Account Leases

- Status: Accepted for specification (issue #11)
- Date: 2026-07-14
- Parent: [#1](https://github.com/monet88/pixelplus/issues/1)
- Issue: [#11](https://github.com/monet88/pixelplus/issues/11)
- Vocabulary source: `CONTEXT.md`
- Related ownership invariants: `docs/spec/tenant-ownership-authorization-invariants.md` (#6)
- Related risk envelope: `docs/spec/auth-mode-risk-envelope-and-kill-criteria.md` (#7)
- Related Client API Key / admission: `docs/spec/client-api-key-lifecycle-and-admission-controls.md` (#8)
- Related connection / credential lifecycle: `docs/spec/provider-account-connection-and-credential-lifecycle.md` (#9)
- Related Capability Snapshot / model availability: `docs/spec/capability-snapshot-and-model-availability-semantics.md` (#10)

## 1. Scope and non-goals

### 1.1 Scope

This specification locks **how the Gateway selects a Provider Account for a request inside one Tenant** — the candidate set, the precedence among explicit selection / lease / affinity / policy routing, when a **fallback** to a second account is allowed, and the cases where fallback is **absolutely forbidden** with their failure semantics.

It codifies parent #1 routing/fallback/affinity decisions and consumes the ownership boundary (#6), risk envelope (#7), admission pipeline (#8), usability gate (#9 `I-USABLE-GATE`), and capability gate (#10).

This is **specification work**; it does **not** implement the Gateway.

It covers:

1. The **candidate set** construction rule and its Tenant boundary.
2. The **selection precedence**: explicit account selection → lease → affinity → policy routing → fallback.
3. **Account leases** and **affinity**: what they pin, their lifetime class, and their precedence.
4. **Fallback**: when it MAY run, which accounts/Auth Modes it MAY move between, and the capability match it MUST satisfy.
5. The **absolute no-fallback set** and its observable failure semantics.
6. **Routing Policy** logical fields (not JSON schema) and their evaluation.

### 1.2 Non-goals

This document does **not**:

- Implement Gateway, Adapter, routing engine, lease store, or UI code.
- Redefine **ownership / non-enumeration** (#6), **risk status** (#7), **admission controls** (#8), the **usability gate** `I-USABLE-GATE` (#9 §5.1), or **capability enforcement** (#10 §9). It **consumes** them.
- Own **capability taxonomy or freshness** (#10) — routing reads the offerable set, it does not compute it.
- Freeze **numeric** lease TTLs, affinity windows, cooldown timers, retry budgets, or per-account backoff — those are #17 (this document locks named **classes** and the re-selection obligation).
- Freeze **JSON schema, field names, or OpenAPI paths** for Routing Policy — those are #18 / #20. This document locks **logical fields and semantics**.
- Define **canonical error code strings** — #16 owns those; this document locks status/remediation **classes**.
- Authorize **multi-account load balancing / pooling inside one Tenant** — that remains deferred (#7 `D-MULTI-ACCT`); this document locks only candidate/precedence/lease/fallback semantics, not load-spreading across many accounts.
- Design the **chat stream** (#12), **asset/image op execution** (#13/#14), **vault crypto** (#15), or **operator health UI** (#17) beyond the routing eligibility they feed.

Downstream issues **MUST** preserve every decision here. They may add fields, tighten policy, or add UX. They **MUST NOT**:

- Bring a Provider Account outside `principal.tenant_id` into any routing step (#6).
- Fall back silently across Auth Modes the Tenant did not explicitly enable for fallback (#7 §6.3, #9 §8.8).
- Route to a non-usable (#9 §5.1) or capability-unsatisfied (#10 §9) account.
- Route into a `prohibited` Auth Mode, or an `experimental` mode outside a lab profile, or a `gated` mode without flag+ack (#7).
- Let a lease or affinity outlive the usability/capability of the account it pins.

### 1.3 Normative language

- **MUST / MUST NOT / REQUIRED**: product/security policy. Violation is a defect.
- **SHALL**: same force as MUST for observable Public API / management API behavior.
- **SHOULD**: strongly preferred default; deviation needs an operator-recorded exception.
- **MAY**: optional surface that cannot weaken MUST rules.

### 1.4 Relationship to prior issues

| Topic | Already locked | This document adds |
|---|---|---|
| Ownership / candidate scope | Candidate set ⊆ Tenant accounts; no cross-Tenant fallback; confused-deputy rules (#6 §4.2/§5.3, `I-ROUTE-SCOPE`) | Concrete candidate-set construction and precedence steps |
| Risk status gating | `allowed`/`gated`/`experimental`/`prohibited`; no silent cross-mode fallback (#7 §6.3) | How each status filters candidates and bounds fallback |
| Admission ordering | Authn→scope→size→rate→concurrency→quota→accept; allowlists narrow same-Tenant (#8) | Routing runs after A6 accept; how `provider_account_allowlist` bounds candidates |
| Usability gate | `I-USABLE-GATE` §5.1 items 1–7 (#9) | Only usable accounts enter the candidate set; lease/affinity cannot bypass it |
| Capability gate | Offerable = fresh + offerable-status + usable + risk-permitted (#10 §5.3, §9) | Fallback target MUST satisfy capability for the requested op+model |
| Health classes | 9 tokens; `quota_exhausted`/`rate_limited`/`protocol_drift`… (#9 §6) | Which health degrades a candidate vs triggers fallback vs forbids it |

### 1.5 Decision unit

**One routing decision = one request (one Security Principal, one requested operation+model) resolved to at most one Provider Account inside `principal.tenant_id`, or a fail-closed rejection.**

Cause → effect:

1. A chat request from Tenant A with no explicit account and a policy naming two ChatGPT Codex OAuth accounts resolves to **exactly one** account per the precedence in §4 — never a blend, never a Tenant-B account.
2. If the resolved account becomes non-usable mid-selection, fallback (§6) may pick a second **same-Tenant** account **only if** policy allows and capability matches; otherwise the request fails closed (§7) — it does **not** silently widen scope.
3. A request pinned by an active **lease** (§5) to account `X` stays on `X` until the lease releases or `X` becomes non-usable; the lease never authorizes a different Auth Mode or a foreign account.

---

## 2. Glossary extensions (normative use)

| Term | Meaning in this document |
|---|---|
| **Routing decision** | The Gateway's request-time resolution of a usable, capability-satisfied Provider Account (or fail-closed rejection) for one request inside one Tenant. |
| **Candidate set** | The set of the acting Tenant's Provider Accounts eligible for a request after ownership, key allowlist, usability (#9), risk (#7), and capability (#10) filters. Never contains a foreign or non-usable account. §3. |
| **Explicit account selection** | The client naming a `provider_account_id` for the request (explicit affinity in #6 §4.2). A **pin/filter**, not a capability grant; it can only select within the candidate set. §4.2. |
| **Routing Policy** | Tenant-declared configuration to select, order, and permit fallback among that Tenant's own accounts (`CONTEXT.md`). Its candidate list MUST be a subset of the Tenant's accounts (#6 §3.2). §8. |
| **Affinity** | A soft preference to reuse the same Provider Account across related requests of a Tenant (e.g. a multi-turn conversation) within an **affinity window class**. Does not survive non-usability. §5.1. |
| **Account lease** | A stronger, explicit hold binding a unit of work (e.g. a multi-step Render Job or a streaming session) to exactly one Provider Account for a **lease TTL class**, so mid-work the Gateway does not switch accounts under it. §5.2. |
| **Fallback** | Selecting a **second** candidate account after a first choice is unavailable/failed, only within the Tenant-permitted set and with matching capability. §6. |
| **No-fallback case** | A condition under which the Gateway MUST NOT try another account and MUST fail closed with a stable class. §7. |
| **Transient vs durable unavailability** | Transient = short backoff class (`rate_limited`, some `degraded`); durable = usability/capability kill (#9 §5.1 items 1–5 fail, `unsupported`, `stale`/`invalid`). Only transient (or Tenant-policy-permitted entitlement) unavailability may trigger fallback; durable kills of the *request's* only pinned account fail closed. §6.4. |

---

## 3. Candidate set construction (Tenant boundary first)

### 3.1 Construction order (normative)

For a request with Security Principal `(tenant_id, client_api_key_id)`, requested operation `op`, and model `m`, the candidate set is built by applying **all** filters. Ownership is first and non-negotiable:

| Step | Filter | Removes an account when | Source |
|---|---|---|---|
| C0 | **Ownership** | `account.tenant_id != principal.tenant_id` | #6 `I-ROUTE-SCOPE`, §4.2 |
| C1 | **Key allowlist** | `provider_account_allowlist` non-empty and account ∉ list; or explicit `[]` (deny-all) | #8 §5.1/§5.4 |
| C2 | **Usability** | account not usable per `I-USABLE-GATE` #9 §5.1 items 1–5 (durable account gates; item 6 is C1, item 7 is C4) | #9 §5.1 |
| C3 | **Risk** | Auth Mode `prohibited`; `experimental` outside lab; `gated` without flag+ack | #7 §2, §7 |
| C4 | **Capability** | snapshot does not affirm `op`+`m` as offerable (fresh + offerable-status) | #10 §5.3, §9 |
| C5 | **Health (routable)** | operational health is a hard-block (`auth_expired`/`provider_banned`/`challenged`) or op-affecting `quota_exhausted` for `op` | #9 §6, #10 §8.2 |

An account survives to the candidate set only if it passes C0–C5. The candidate set MAY be empty → the request fails closed (§7).

### 3.2 Ownership can never be widened

- No later step (explicit selection, affinity, lease, policy, fallback) may **add** an account that failed C0. A forged or foreign `provider_account_id` yields **404-class** non-enumeration (#6 §5.2 A1/A14), never a 403 that confirms existence, and never a fallback candidate.
- A decrypted credential in process memory for another Tenant's in-flight work MUST NOT become a candidate (#6 §5.3 rule 5, confused deputy).

### 3.3 Candidate set is request-time, not durable

- The candidate set is recomputed per request from current usability/capability/health. It is **not** a stored routing authority that can outlive its inputs (parallels #10 §5.3 "offerable is derived").
- A durable #9 §5.1 items 1–5 failure removes the account from every candidate set immediately, even inside a lease (§5.4) and even if a Capability Snapshot's TTL has not elapsed (#9 `I-SNAPSHOT-NONUSE`, #10 §8.3).

---

## 4. Selection precedence (explicit → lease → affinity → policy → fallback)

### 4.1 Precedence ladder (normative)

Given the candidate set from §3, the Gateway resolves the account in this strict order. Each rung operates **only** on the candidate set (C0–C5 already applied); no rung may reach outside it.

| Rung | Mechanism | Effect |
|---|---|---|
| **P0** | **Candidate gate** (§3) | If candidate set is empty → fail closed (§7). Nothing below can revive an empty set. |
| **P1** | **Explicit account selection** | If the client named `provider_account_id = X`: X MUST be in the candidate set. If yes → resolve to X (no other account considered; fallback OFF unless §4.2 rule 4). If X is same-Tenant but not in the candidate set → fail closed with the specific class (§7.2). If X is foreign/unknown → **404-class**. |
| **P2** | **Active lease** | If an active lease (§5.2) binds this unit of work to account `L`: if `L` is still in the candidate set → resolve to `L`. If `L` left the candidate set → lease is void for new work (§5.4); the request MUST NOT fall through to P3 (affinity) or P4 (fresh policy selection) — those would silently pick a different account and bypass the fail-closed fallback opt-in (`I-ROUTE-FALLBACK-OPTIN`). Instead go directly to P5: trigger fallback if §6 permits, else fail closed (§7). |
| **P3** | **Affinity** | If affinity (§5.1) prefers account `A` (from a prior related request) and `A` is in the candidate set → resolve to `A`. Affinity is a preference, not a hard pin; if `A` is absent, continue. |
| **P4** | **Policy routing** | Apply Routing Policy ordering/selection (§8) over the remaining candidate set to pick the primary account. |
| **P5** | **Fallback** | Only if the chosen account (from P1–P4) becomes unavailable in a fallback-eligible way (§6) AND policy permits fallback → pick the next permitted candidate. Otherwise fail closed (§7). |

### 4.2 Explicit selection is the strongest pin

Cause → effect:

1. Explicit selection **pins** the request to one same-Tenant account. It is a filter down to one, never a widening.
2. Because it pins to one account, **fallback is OFF by default** for an explicitly selected request (§7.1 case NF-PIN): the client asked for *that* account; silently using another would violate least surprise and the confused-deputy posture (#6 §5.3 rule 1).
3. **Exception (rule 4):** fallback from an explicitly selected account is allowed **only** if the Tenant's Routing Policy explicitly declares an ordered fallback chain **and** the request opts into it (an explicit "allow fallback" flag on the request or policy). Even then, every fallback target obeys §6 (same-Tenant, permitted Auth Mode, capability match).
4. Explicit selection never overrides C0–C5: naming a non-usable or capability-unsupported account fails closed (§7.2), it does not force execution.

### 4.3 Lease outranks affinity outranks policy

- A **lease** (P2) is a hard hold for the duration of a unit of work; it outranks affinity and policy so a mid-flight multi-step job or stream does not hop accounts (§5.2).
- **Affinity** (P3) is a soft preference; it outranks fresh policy selection so related turns tend to reuse an account, but it yields the moment the account leaves the candidate set.
- **Policy** (P4) is the default selector when no stronger pin applies.

### 4.4 Determinism

For a given candidate set and policy, P1–P4 MUST be deterministic (same inputs → same primary account). Fallback order (P5) MUST also be deterministic per policy (an ordered chain, not a random pick), so behavior is testable (§11) and diagnosable. Multi-account load spreading (non-deterministic by design) is **not** authorized here (#7 `D-MULTI-ACCT`).

---

## 5. Affinity and account leases

### 5.1 Affinity (soft preference)

| Field | Meaning |
|---|---|
| **Affinity key** | The relation over which reuse is preferred (e.g. conversation id, session id, idempotency scope). Same-Tenant only. |
| **Preferred account** | The `provider_account_id` last successfully used for that affinity key. |
| **Affinity window class** | Named freshness budget after which affinity expires and policy re-selects. Numeric value is #17-tunable (`AFFINITY-WINDOW-CLASS`). |

Rules:

1. Affinity is a **preference at P3**, never a durable authority. It MUST NOT keep a non-usable/capability-unsatisfied account (C2/C4 fail removes it).
2. Affinity MUST NOT cross Auth Modes: reuse means the *same account*, not "another account of the same human" (#9 §8.8 rule 1).
3. Affinity expiring or its account leaving the candidate set falls through to P4 policy, not to a foreign account.

### 5.2 Account lease (hard hold)

| Field | Meaning |
|---|---|
| **Lease holder** | The unit of work holding the lease (e.g. a Render Job id, a streaming session id). Same-Tenant. |
| **Leased account** | The single `provider_account_id` bound for the work's duration. |
| **Lease TTL class** | Named maximum hold; numeric value #17-tunable (`LEASE-TTL-CLASS`). Renewable while the work is active and the account stays usable. |
| **Lease state** | `held` / `released` / `void`. |

Rules:

1. A lease binds a multi-step or long-running unit of work to **exactly one** same-Tenant account so intermediate steps do not switch accounts (e.g. an inpaint job that references prior outputs, or a chat stream whose continuity depends on one session). This is the routing counterpart of #9's single-account credential binding.
2. A lease is acquired **from the candidate set** (§3) at the start of the work under the same precedence (P1–P4). It cannot be acquired on a non-usable or foreign account.
3. A lease outranks affinity and policy (P2) but **never** outranks C0–C5: it cannot hold a non-usable account (§5.4).
4. Leases are **per unit of work**, not a Tenant-wide reservation, and MUST NOT be used to implement multi-account load balancing (deferred, #7 `D-MULTI-ACCT`).
5. Concurrency accounting for leased work still obeys #8 §7.4 (a lease does not create extra concurrency budget).

### 5.3 Lease vs affinity (concrete)

| | Affinity | Lease |
|---|---|---|
| Strength | Soft preference (P3) | Hard hold (P2) |
| Scope | Related requests (conversation) | One unit of work (job/stream) |
| Expiry | `AFFINITY-WINDOW-CLASS` or account leaves candidate set | `LEASE-TTL-CLASS`, work terminal, or account non-usable |
| On account loss | Fall through to policy | Void; fallback only if §6 permits, else fail closed |
| Cross Auth Mode | Never | Never |

### 5.4 Lease/affinity release and void (durable-gate precedence)

- When durable `I-USABLE-GATE` §5.1 items 1–5 fail for the leased/affine account (`disabled`/`revoked`/`deleted`/`reauth_required`/hard-block health/vault-revoke), the lease is **void** and affinity is dropped **immediately** for new work, overriding any TTL (#9 `I-SNAPSHOT-NONUSE`, #10 §8.3). In-flight upstream work that cannot be aborted MAY finish on the old account (mirrors #8 §4.5 in-flight residual; #9 §4.10 rule 2), but **no new step** admits on a voided lease.
- Request-time item 6 (key scope/allowlist) or item 7 (capability) failing for one principal MUST NOT void a lease held for other principals or system paths (#9 §5.1 item 7, #10 §8.3).
- A capability turning `stale`/`invalid` (#10 §6) makes the leased account non-offerable for the affected op; new steps needing that op fail closed or await re-probe, but the lease itself is not void unless a durable gate also failed.

---

## 6. Fallback (same-Tenant, permitted, capability-matched)

### 6.1 When fallback MAY run

Fallback (P5) is considered **only** when **all** hold:

1. The primary account (from P1–P4) became **unavailable in a fallback-eligible way** (§6.4), **and**
2. The Tenant's Routing Policy **explicitly declares** an ordered fallback chain (fallback is **opt-in**, fail-closed by default — no silent auto-failover; §6.5), **and**
3. For an **explicitly selected** request, §4.2 rule 4 opt-in also holds, **and**
4. At least one **next** account remains in the candidate set (§3) — i.e. it already passed C0–C5 for this exact `op`+`m`, **and**
5. If the primary was already attempted upstream, the owning operation's retry contract authorizes another attempt. For non-idempotent chat, #12 requires authoritative proof that the prior Provider attempt did not accept/create a generation; status/error class alone is insufficient.

### 6.2 What fallback MAY move between

- **Only same-Tenant accounts** (#6 `I-ROUTE-SCOPE`, `I-NO-SILENT-CROSS`). A foreign id is never a fallback target (§3.2).
- **Only Auth Modes the Tenant policy explicitly lists for fallback** and that are product-enabled (#7). Cross-Auth-Mode fallback (e.g. Codex OAuth → ChatGPT Web) is allowed **only** if the policy names both modes and both are enabled; it is **never** silent (#7 §6.3, #9 §8.8 rule 3).
- **Never** into a `prohibited` mode (e.g. Grok Web SSO), an `experimental` mode outside a lab profile, or a `gated` mode without flag+ack (#7 §6.1).

### 6.3 Capability match on the fallback target (AC)

A fallback target MUST satisfy capability for the **exact requested `op`+`m`** (#10 §9):

1. The target's Capability Snapshot MUST affirm `op` as offerable (`verified`/`conditionally_supported`, `fresh`) — an `unsupported`/`unverified`/`stale`/`invalid` target is **not** a candidate (already excluded at C4).
2. If the request names model `m`, the target MUST have observed `m` offerable for `op` (#10 §5.3). If `m` is not offerable on the target, that target is skipped; the Gateway MUST NOT silently substitute a different model.
3. **Inpaint specifically:** a masked (`inpaint`) request MUST NOT fall back to an account/Auth Mode where `inpaint` is `unsupported` (all Gemini/Grok modes, #10 §4.3), and MUST NOT be silently downgraded to `image_edit` (#10 §9.1, parent #1 mask fidelity).
4. **Streaming specifically:** a request requiring real `chat_streaming` MUST NOT fall back to a mode whose streaming is `synthetic` (Gemini Web Cookie, #10 §3.1/§4.3) without the client accepting synthetic streaming; the snapshot's streaming class is authoritative (#10 §3.1).

### 6.4 Fallback-eligible vs fail-closed unavailability

| Primary account became… | Source | Fallback eligible? |
|---|---|---|
| `rate_limited` known before this request sends an upstream payload (for example, active cooldown) | #9 §6 | **Yes**, if policy permits — short backoff, try next permitted candidate |
| `rate_limited` returned during an upstream attempt | #9 §6 + owning operation retry contract | **Only when the operation permits re-attempt**; non-idempotent chat additionally requires #12 authoritative proof of non-commit. HTTP/error class alone is insufficient |
| `degraded` (partial, non-auth) | #9 §6 | **Yes** with caution before an attempt, if policy permits and capability still offerable; after an attempt, the owning operation's retry safety also applies |
| `quota_exhausted` known before this request sends an upstream payload (for example, current entitlement/reset state) | #9 §6, #10 §8.1 | **Policy-dependent**: MAY fall back to another permitted account; the exhausted account is non-offerable for the op until reset (#10 §8.2) — this is entitlement drift, not reauth |
| `quota_exhausted` returned during an upstream attempt | #9 §6, #10 §8.1 + owning operation retry contract | **Only when the operation permits re-attempt**; non-idempotent chat additionally requires #12 authoritative proof of non-commit |
| `auth_expired` / `challenged` / `provider_banned` (hard-block) | #9 §6 | Account leaves candidate set (C5); fallback to **other** permitted candidates MAY run, but the request never fails *open* on the dead account |
| Durable #9 §5.1 items 1–5 fail (disabled/revoked/deleted/reauth_required) | #9 §5.1 | Account leaves candidate set; fallback to other permitted candidates only |
| `protocol_drift` → capability `invalid` | #9 §6, #10 §8.1 | Affected op non-offerable on that account (C4); fallback to a capability-offerable permitted candidate only |
| **Explicit single-account pin, no policy fallback opt-in** | §4.2 | **No** — fail closed (NF-PIN) |
| **No permitted next candidate** | §3 | **No** — fail closed (§7) |

### 6.5 No silent fallback (fail-closed default)

- If no Routing Policy fallback chain is declared, the Gateway MUST NOT invent one. A single-account Tenant, or a Tenant that did not opt into fallback, gets a fail-closed rejection when its primary is unavailable (§7), never a surprise account switch (#6 `I-NO-SILENT-CROSS`, #7 §6.3).
- A health/error token describes availability; it does not by itself prove retry safety. Once an upstream attempt may have committed, fallback MUST fail closed unless the owning operation contract authorizes re-attempt. In particular, chat consumes #12 proof-of-non-commit regardless of `rate_limited`/`quota_exhausted` naming.
- Circuit-breaking / health degradation MUST NOT replace a Tenant's policy with another Tenant's accounts or a shared pool (#6 §6, #7 OP-G7).

### 6.6 Fallback bound and loop safety

- Fallback walks the policy's ordered chain **once** per request (each target tried at most once); it MUST terminate at the end of the chain with a fail-closed rejection (§7). No infinite retry across accounts.
- Per-account transient backoff/cooldown numbers are #17; this document locks that fallback is **bounded and ordered**, not that it retries indefinitely.
- Fallback attempts still respect #8 admission: fallback does not re-open a new A0–A5 admission; it re-selects an account after A6 accept, and MUST NOT exceed the request's concurrency/quota reservation.

---

## 7. Absolute no-fallback cases and failure semantics (AC)

### 7.1 No-fallback set (normative)

The Gateway MUST NOT attempt any other account (MUST fail closed) in these cases:

| Id | Case | Why |
|---|---|---|
| **NF-XTENANT** | The only alternative is an account outside `principal.tenant_id` | #6 `I-ROUTE-SCOPE`, `I-NO-SILENT-CROSS`; cross-Tenant fallback is a confused-deputy compromise |
| **NF-PIN** | Request explicitly selected one account and did not opt into policy fallback (§4.2) | Client pinned that account; silent switch violates intent and #6 §5.3 rule 1 |
| **NF-NOPOLICY** | No Routing Policy fallback chain declared (§6.5) | Fail-closed default; no silent auto-failover |
| **NF-XMODE** | The only alternative is a different Auth Mode not explicitly listed for fallback by policy | #7 §6.3, #9 §8.8 rule 3; no silent cross-mode |
| **NF-PROHIBITED** | The only alternative is a `prohibited` mode (e.g. Grok Web SSO), or `experimental` outside lab, or `gated` without flag+ack | #7 §2, §6.1 |
| **NF-CAP-UNSUPPORTED** | The requested `op` is `unsupported`/`unverified` on every remaining candidate (e.g. inpaint on a Gemini/Grok-only Tenant) | #10 §4.1, §9; MUST NOT downgrade inpaint→edit |
| **NF-MODEL** | The requested model `m` is not offerable on any remaining candidate | #10 §5.3; MUST NOT substitute a different model silently |
| **NF-STALE** | Every remaining candidate's snapshot is `stale`/`invalid` for the op | #10 §6.1 fail-closed; re-probe first |
| **NF-EMPTY** | Candidate set is empty after C0–C5 | Nothing usable/permitted to route to |

### 7.2 Observable failure semantics

Consistent with #6 §7, #8 §9, #9 §5.2, #10 §10. Exact error code strings are #16; **status classes and remediation classes** are locked here:

| Case | HTTP-oriented class | Remediation class | Side effects |
|---|---|---|---|
| NF-XTENANT / foreign id in selection or forged fallback | **404-class** (non-enumerating) | n/a (unknown id) | zero Adapter call; zero vault decrypt for the foreign account |
| NF-PIN / NF-NOPOLICY (own account unavailable, no fallback) | **403-class** (same-Tenant policy) or the primary's own runtime class surfaced | per primary cause: `reauthenticate` / `wait_provider_cooldown` / `enable_account` (#9 §7) | no fallback Adapter call |
| NF-XMODE / NF-PROHIBITED | **403-class** / fail-closed | `auth_mode_unavailable` (#9 §7, #10 §10) | no execution on the forbidden mode |
| NF-CAP-UNSUPPORTED | **4xx** capability class | `capability_unsupported` (#10 §10) | reject **before** upstream; Adapter executions = 0 |
| NF-MODEL | **4xx** capability class | `model_unavailable` (#10 §10) | reject before upstream |
| NF-STALE | **4xx** capability class | `snapshot_stale` (#10 §10) + re-probe path | reject before upstream; trigger re-probe |
| NF-EMPTY | **403-class** / fail-closed (or **404-class** if the only named id was foreign) | most-actionable of the above (e.g. `reauthenticate`, `capability_unsupported`) | zero Adapter call |

### 7.3 Fail-closed, never fail-open

- In every no-fallback case the Gateway MUST fail closed: no Adapter invocation on a forbidden/foreign/non-usable account, no vault decrypt, no cross-Tenant borrow (#6 `I-FAIL-CLOSED`, #8 §7.6).
- The failure MUST NOT leak whether another Tenant holds a usable account for that op (#6 §5.1 non-enumeration).
- A routing failure is **not** a Client API Key admission failure (#8) and **not** a raw Provider runtime error unless it is surfaced from the primary account's own execution; it is a routing/capability outcome and MUST be classifiable as such (#10 §9.2 spirit).

---

## 8. Routing Policy (logical fields and evaluation)

### 8.1 Logical fields (schema is #18/#20)

| Field | Required | Notes |
|---|---|---|
| `tenant_id` | yes | Immutable; policy is Tenant-owned (#6 §3) |
| `candidate_accounts` | yes | Ordered list of same-Tenant `provider_account_id`; every id MUST share `tenant_id` (#6 §3.2) or the write fails closed (§8.3) |
| `selection_order` | yes | Deterministic primary-selection order over the candidate list (§4.4) |
| `fallback_enabled` | yes | Boolean; default **false** (fail-closed, §6.5) |
| `fallback_chain` | when `fallback_enabled` | Ordered subset of `candidate_accounts` tried in order (§6.6) |
| `fallback_auth_modes` | when cross-mode fallback intended | Explicit list of Auth Modes permitted for fallback; absence forbids cross-mode (§6.2, NF-XMODE) |
| `affinity_enabled` | no | Boolean; enables §5.1 preference |
| `lease_policy` | no | Whether/which units of work acquire leases (§5.2); numbers #17 |
| `updated_at` / `updated_by` | yes | Audit; requires `routing.manage` scope (#8 §5.2) |

### 8.2 Evaluation (normative)

1. Read the policy for `principal.tenant_id` only (foreign policy id → 404-class; #6).
2. Intersect `candidate_accounts` with the live candidate set (§3): any policy-listed account that fails C0–C5 is skipped this request (policy listing does not override usability/capability/risk).
3. Apply the precedence ladder (§4) using `selection_order` at P4 and `fallback_chain` at P5.
4. If `fallback_enabled=false`, P5 is disabled → unavailable primary fails closed (NF-NOPOLICY).

### 8.3 Policy write safety (consumes #6)

- A policy write listing a foreign account id MUST fail closed and MUST NOT persist a cross-Tenant reference (#6 §5.2 A5, §3.2). Foreign ids yield non-enumerating denial; the policy is rejected without confirming foreign existence.
- Policy read/write requires `routing.read` / `routing.manage` (#8 §5.2); default inference keys lack `routing.manage` (#8 §5.3), so a leaked inference key cannot rewrite routing to exfiltrate via a different account.

### 8.4 Authorization surface (#8 scope mapping)

| Operation | Minimum scope |
|---|---|
| Read own Routing Policy | `routing.read` |
| Update own Routing Policy (candidates, order, fallback, affinity, lease policy) | `routing.manage` |
| Name an account in an inference request (explicit selection) | inference scope + `provider_account_allowlist` permits (#8 §5.4) |

---

## 9. Ownership, confused deputy, and non-enumeration in routing

All routing paths obey #6:

1. **Candidate set ⊆ Tenant accounts** (C0). Foreign ids never enter any rung (§3.2, `I-ROUTE-SCOPE`).
2. **Account name is a same-Tenant selector, not a global grant** (#6 §5.3 rule 1): explicit selection, affinity, lease, and fallback chain entries are all interpreted only inside `principal.tenant_id`.
3. **Credential decrypt is gated by `(tenant_id, provider_account_id)`** already authorized for the resolved account (#6 §5.3 rule 3); a routing decision does not itself authorize decrypt of any other account.
4. **No ambient authority** (#6 §5.3 rule 5): another Tenant's in-flight decrypted credential is never a fallback candidate.
5. **Non-enumeration** (#6 §5.1): a foreign id anywhere in a request or policy yields 404-class; routing failures never confirm foreign account existence or capability.
6. **Workers** route only with the resource's `tenant_id` (#6 §2.4); a Render Job's lease/affinity is resolved inside the job's Tenant only.

---

## 10. Security impact summary

| Defect | Impact |
|---|---|
| Cross-Tenant account enters candidate set / fallback | Confused deputy; Gateway becomes cross-Tenant proxy; foreign quota/credential abuse (#6) |
| Silent cross-Auth-Mode fallback | Hits `experimental`/`prohibited` surface; ToS/ban blast (#7 §6.3) |
| Fallback to `prohibited` mode (Grok Web SSO) | Direct AUP collision (#7 §5.5) |
| Route to non-usable account | Dead/invalid execution, wasted quota, false success (#9) |
| Fallback without capability match | Upstream failure, or inpaint silently degraded to edit (mask fidelity loss) (#10) |
| Post-attempt fallback based only on `rate_limited`/`quota_exhausted` token | Duplicate non-idempotent generation/quota because the primary may already have committed (#12) |
| Lease/affinity outliving usability | Routing to a revoked/disabled account after kill (#9 `I-SNAPSHOT-NONUSE`) |
| Silent auto-failover with no policy | Surprise account switch; client loses control; leaked-key lateral use |
| Fail-open on empty candidate set | Open proxy / cross-Tenant borrow (#6, #8 §7.6) |
| Explicit-pin ignored by fallback | Violates client intent; confused-deputy posture |
| Enumerating 403 on foreign fallback id | Existence oracle / tenant graph mapping (#6 §5.1) |

---

## 11. Test obligations

Exact harness arrives with contract prototypes (#18–#20). Required observable cases for this issue:

### 11.1 Candidate scope (AC1)

1. A request from Tenant A never yields a Tenant-B account in any rung; a forged B `provider_account_id` in selection, affinity, lease, or `fallback_chain` → 404-class, zero Adapter call, zero B credential decrypt.
2. A policy write listing a foreign account id fails closed without persisting a cross-Tenant reference.
3. `provider_account_allowlist` non-empty excludes non-listed same-Tenant accounts from the candidate set; explicit `[]` denies all (403-class).
4. Non-usable (`I-USABLE-GATE` fail), `prohibited`/non-lab-experimental/gated-without-ack, and capability-unsupported accounts are absent from the candidate set.

### 11.2 Precedence (AC2)

5. Explicit selection of a usable, capability-satisfied same-Tenant account resolves to exactly that account and does not fall back by default.
6. Explicit selection of a same-Tenant account that fails C2/C4 → fail closed with the specific class (not a silent switch).
7. An active lease pins the unit of work to one account across steps; affinity prefers the prior account; policy selects when neither pins; order is deterministic for fixed inputs.
8. Lease outranks affinity outranks policy (P2 > P3 > P4).

### 11.3 Fallback permitted (AC3)

9. With `fallback_enabled=true` and an ordered chain, a primary known `rate_limited` before the current request's upstream payload falls back to the next permitted same-Tenant candidate that is capability-offerable for the exact op+model; a `rate_limited` response after an attempt also requires the owning operation's re-attempt authorization (for chat, #12 authoritative proof of non-commit).
10. Cross-Auth-Mode fallback occurs **only** when policy lists both modes and both are enabled; otherwise NF-XMODE fail-closed.
11. A fallback target must have model `m` offerable; a target missing `m` is skipped, never silently substituted.
12. Inpaint request never falls back to a mode where inpaint is `unsupported` and is never downgraded to `image_edit`.

### 11.4 No fallback (AC4)

13. NF-PIN: explicitly selected single account unavailable, no opt-in → fail closed, no other account tried.
14. NF-NOPOLICY: no fallback chain declared → fail closed on primary unavailability; no silent switch.
15. NF-XTENANT: only alternative is foreign → 404-class; zero cross-Tenant execution.
16. NF-PROHIBITED: only alternative is Grok Web SSO / non-lab experimental / gated-without-ack → fail closed `auth_mode_unavailable`.
17. NF-CAP-UNSUPPORTED / NF-MODEL / NF-STALE: reject before upstream with `capability_unsupported` / `model_unavailable` / `snapshot_stale`; Adapter executions = 0.
18. NF-EMPTY: empty candidate set → fail closed; failure does not reveal whether another Tenant has a usable account.
19. NF-REATTEMPT: a chat primary returns `rate_limited`/`quota_exhausted` after payload may have been accepted but the Adapter cannot prove non-commit → fail closed with zero fallback Adapter calls; the status token alone never authorizes a duplicate generation.

### 11.5 Lifecycle interaction

20. Durable #9 §5.1 items 1–5 failure on a leased/affine account voids the lease / drops affinity for new work immediately, even within lease TTL; in-flight non-cancelable work MAY finish on the old account.
21. Request-time key-scope failure for one principal does not void a lease held by other principals or system paths.
22. Fallback walks the chain once and terminates in a fail-closed rejection; no infinite cross-account retry; no extra concurrency/quota beyond the request's reservation (#8).

### 11.6 Scope and ownership

23. `routing.manage` required to change policy; default inference key cannot (#8). `routing.read` required to read policy.
24. Routing failures are classifiable as routing/capability outcomes, distinct from #8 admission rejections and from raw Provider runtime errors.

---

## 12. Core invariants (normative checklist)

1. **I-ROUTE-TENANT** — Every candidate, at every rung (explicit, affinity, lease, policy, fallback), is a Provider Account of `principal.tenant_id`; a foreign id yields 404-class and never routes (#6 `I-ROUTE-SCOPE`).
2. **I-ROUTE-CANDIDATE-GATE** — The candidate set is C0–C5 (ownership → key allowlist → usability → risk → capability → routable health); no rung may add an account that failed any filter.
3. **I-ROUTE-PRECEDENCE** — Resolution order is P0 candidate gate → P1 explicit → P2 lease → P3 affinity → P4 policy → P5 fallback; lower rungs cannot revive an empty candidate set.
4. **I-ROUTE-EXPLICIT-PIN** — Explicit account selection pins to one same-Tenant account with fallback OFF unless policy declares a chain and the request opts in; it never overrides C0–C5.
5. **I-ROUTE-LEASE-SAME-ACCOUNT** — A lease binds one unit of work to exactly one same-Tenant account for its duration; it outranks affinity/policy but never usability/capability, and is void immediately on durable #9 §5.1 items 1–5 failure.
6. **I-ROUTE-AFFINITY-SOFT** — Affinity is a preference that yields the moment its account leaves the candidate set; it never crosses Auth Modes or Tenants.
7. **I-ROUTE-FALLBACK-OPTIN** — Fallback runs only when Tenant policy explicitly declares an ordered chain (fail-closed default), and a post-attempt account switch also satisfies the owning operation's retry-safety contract; chat requires #12 authoritative proof of non-commit. No silent or status-only auto-failover (#6 `I-NO-SILENT-CROSS`, #7 §6.3).
8. **I-ROUTE-FALLBACK-SAMETENANT** — Fallback moves only among same-Tenant accounts; never cross-Tenant.
9. **I-ROUTE-FALLBACK-MODE** — Cross-Auth-Mode fallback only when policy lists both modes and both are product-enabled; never into `prohibited`/non-lab-`experimental`/`gated`-without-ack (#7).
10. **I-ROUTE-FALLBACK-CAPABILITY** — A fallback target MUST affirm the exact requested op+model as offerable (#10); inpaint never degrades to edit; real streaming never silently becomes synthetic.
11. **I-ROUTE-NO-FALLBACK-SET** — The NF-* set (§7.1) MUST fail closed with a stable status/remediation class; never fail open, never enumerate foreign accounts.
12. **I-ROUTE-DETERMINISTIC** — P1–P5 are deterministic for fixed candidate set + policy; multi-account load spreading is not authorized here (#7 `D-MULTI-ACCT`).
13. **I-ROUTE-POLICY-SCOPE** — Routing Policy is Tenant-owned; candidates ⊆ Tenant accounts; write with foreign ids fails closed; read/write require `routing.read`/`routing.manage` (#6, #8).
14. **I-ROUTE-REQUEST-TIME** — The candidate set and resolution are recomputed per request from current usability/capability/health; no stored routing authority outlives its inputs.
15. **I-ROUTE-FAIL-CLOSED** — On any no-fallback case or backend unavailability, the Gateway fails closed (no Adapter call, no vault decrypt, no cross-Tenant borrow) (#6 `I-FAIL-CLOSED`, #8 §7.6).

---

## 13. Open follow-ups (explicitly deferred)

| Topic | Issue | Constraint retained here |
|---|---|---|
| Numeric lease TTL, affinity window, cooldown, retry/backoff budgets | #17 | Named classes (`LEASE-TTL-CLASS`, `AFFINITY-WINDOW-CLASS`) + bounded-ordered fallback locked here; #17 tunes numbers, not the fail-closed/no-silent rules |
| Routing Policy JSON schema, field names, OpenAPI paths | #18 / #20 | Logical fields + evaluation locked here |
| Canonical error code strings / problem+json | #16 | Status/remediation classes locked here |
| Chat stream continuity consuming leases | #12 | Lease/streaming-class semantics locked here |
| Render Job account affinity/lease across steps | #14 | Job → single-account lease semantics locked here (#6 §3.2 job affinity) |
| Capability offerable computation | #10 | Routing consumes offerable; does not compute it |
| Multi-account load balancing / pooling inside one Tenant | reopen #7 `D-MULTI-ACCT` | Not authorized; only deterministic single-account precedence here |
| Smart auto-failover as default (no explicit policy) | reopen `D-ROUTE-AUTOFALLBACK` | MVP: fallback opt-in, fail-closed |
| Cross-mode fallback default posture | reopen `D-ROUTE-XMODE` | MVP: explicit policy list only |

---

## 14. ADR decision

No new ADR. Tenant-scoped routing and no-cross-Tenant fallback were product-locked in parent #1 and #6 (`I-ROUTE-SCOPE`, `I-NO-SILENT-CROSS`); no-silent-cross-mode in #7 §6.3 and #9 §8.8. This document is the durable normative expansion under `docs/spec/` for candidate construction, selection precedence, affinity/lease, and fallback.

An ADR **would** be warranted if the product later introduced:

- silent cross-Tenant or shared-pool routing (forbidden),
- default auto-failover without explicit Tenant policy (deferred `D-ROUTE-AUTOFALLBACK`),
- multi-account load balancing that shards a Tenant's load across many consumer accounts (deferred #7 `D-MULTI-ACCT`),
- or fallback that ignores the capability gate (#10) or usability gate (#9).

---

## 15. Constants and reopen ids

| Id | Meaning |
|---|---|
| `LEASE-TTL-CLASS` | Named max hold for an account lease (§5.2); numeric #17-tunable |
| `AFFINITY-WINDOW-CLASS` | Named freshness budget for affinity reuse (§5.1); numeric #17-tunable |
| `NF-XTENANT` / `NF-PIN` / `NF-NOPOLICY` / `NF-XMODE` / `NF-PROHIBITED` / `NF-CAP-UNSUPPORTED` / `NF-MODEL` / `NF-STALE` / `NF-EMPTY` | Absolute no-fallback cases (§7.1) |
| `D-ROUTE-AUTOFALLBACK` | Reopen if default (no-policy) auto-failover is desired |
| `D-ROUTE-XMODE` | Reopen for a non-explicit cross-mode fallback posture |
| `I-USABLE-GATE` | Owned by #9 §5.1; routing consumes it (candidate C2) |
| `I-ROUTE-SCOPE` / `I-NO-SILENT-CROSS` / `I-FAIL-CLOSED` | Owned by #6; reaffirmed here |
| Capability status / offerable / freshness | Owned by #10; routing consumes offerable (C4) |
| Risk statuses / kill signals | Owned by #7 (`allowed`/`gated`/`experimental`/`prohibited`; `D-MULTI-ACCT`) |

---

## 16. Acceptance criteria traceability

| AC (issue #11) | Where satisfied |
|---|---|
| Candidate selection cannot bring an out-of-Tenant account into any step | §3 (C0), §3.2, §9, §11.1, `I-ROUTE-TENANT`, `I-ROUTE-CANDIDATE-GATE` |
| Explicit account selection, policy routing, affinity and lease have clear precedence | §4 (P0–P5), §5, §11.2, `I-ROUTE-PRECEDENCE`, `I-ROUTE-EXPLICIT-PIN`, `I-ROUTE-LEASE-SAME-ACCOUNT`, `I-ROUTE-AFFINITY-SOFT` |
| Fallback only between Tenant-allowed accounts/Auth Modes with matching capability | §6, §11.3, `I-ROUTE-FALLBACK-OPTIN`, `-SAMETENANT`, `-MODE`, `-CAPABILITY` |
| Absolute no-fallback cases listed with failure semantics | §7, §11.4, `I-ROUTE-NO-FALLBACK-SET`, `I-ROUTE-FAIL-CLOSED` |

---

## 17. Document control

| Field | Value |
|---|---|
| Status | Accepted for specification (issue #11) |
| Check date of evidence inputs | 2026-07-14 |
| Supersedes | n/a (initial tenant-scoped routing / fallback / affinity / lease lock) |
| Next review | On #7 status/`D-MULTI-ACCT` changes, #9 usability-gate changes, #10 capability/freshness changes, or #17 numeric tuning |
| Authors | Spec decision agent for issue #11 |
