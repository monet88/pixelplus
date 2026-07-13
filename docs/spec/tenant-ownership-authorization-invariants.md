# Tenant Ownership and Authorization Invariants

- Status: Accepted for specification (issue #6)
- Date: 2026-07-14
- Parent: [#1](https://github.com/monet88/pixelplus/issues/1)
- Issue: [#6](https://github.com/monet88/pixelplus/issues/6)
- Vocabulary source: `CONTEXT.md`

## 1. Scope and non-goals

### 1.1 Scope

This specification locks **Tenant ownership** and **authorization invariants** that every Public API request, background worker, vault access path, routing decision, and operator tool path must obey for:

- Tenant
- Client API Key
- Provider Account
- Provider Credential
- Asset
- Render Job
- Capability Snapshot
- Routing Policy objects
- Quota counters and admission counters

It codifies parent #1 locked decisions and user stories 1–4 and 45, plus the cross-Tenant testing obligations already stated in parent #1.

### 1.2 Non-goals

This document does **not**:

- Design Client API Key lifecycle, hashing, scopes, rotation, or abuse control numbers (#8).
- Design Provider Account connection journeys or credential submission UX (#9).
- Design Capability Snapshot schema/TTL beyond ownership (#10).
- Design full routing/fallback/lease policy beyond Tenant-scoped candidate sets (#11).
- Design Asset retention/deletion policy beyond ownership (#13).
- Design Render Job state machine beyond ownership (#14).
- Design vault cryptography, KMS, or envelope details (#15).
- Design canonical error taxonomy beyond ownership-related failure semantics (#16).
- Design operator health/cooldown UI beyond “no silent cross-Tenant” (#17).
- Implement Gateway code.

Where a later issue needs a concrete number, schema field, or UX step, it must still preserve every invariant in this document.

### 1.3 Normative language

- **MUST / MUST NOT / REQUIRED**: absolute invariants. Violation is a security defect.
- **SHALL**: same force as MUST for observable Public API behavior.
- **MAY**: optional product surface that still cannot weaken MUST rules.

---

## 2. Principals

### 2.1 Tenant

**Tenant** is the ownership boundary. Every durable resource listed in §3 is owned by exactly one Tenant, except platform-global configuration that never references Tenant-owned secrets or data (for example Adapter kill-switch flags that do not carry Provider Credential material).

A Tenant does not share:

- Client API Keys
- Provider Accounts
- Provider Credentials
- Assets
- Render Jobs
- Capability Snapshots
- Routing Policy objects
- Quota or admission counters

with any other Tenant. Shared Provider Account pools are out of product scope (parent #1).

### 2.2 Security Principal of a request

For every authenticated Public API request:

1. The request presents exactly one Client API Key material.
2. Gateway authenticates that material and resolves exactly one **Security Principal**.
3. The Security Principal is the pair:
   - `tenant_id` of the owning Tenant
   - `client_api_key_id` of the authenticated Client API Key
4. All subsequent authorization decisions for that request use this Security Principal. No other Tenant identity may be substituted from request headers, body fields, path segments, or worker metadata.

Unauthenticated requests have no Security Principal and MUST NOT access Tenant-owned resources.

### 2.3 Client API Key as request principal

- A Client API Key belongs to exactly one Tenant at creation time and for its entire lifetime.
- A Client API Key never changes Tenant ownership.
- Revocation immediately removes the key’s ability to form a Security Principal.
- Scope/least-privilege restrictions (if introduced by #8) can only **narrow** actions inside the owning Tenant. They MUST NOT grant cross-Tenant rights.

### 2.4 Background workers and internal actors

Workers that execute chat, Render Jobs, probes, credential refresh, or asset placement act **on behalf of** the Tenant recorded on the durable resource they process.

- A worker claim on a Render Job owned by Tenant A MUST load only Tenant A’s Provider Account, Provider Credential, Asset, Routing Policy, and quota counters.
- Worker identity is not a Public API principal and MUST NOT be usable to bypass Tenant ownership.

### 2.5 Future operators (MVP stance)

Human operators, support tooling, and admin APIs are **not** a Public API principal in MVP.

Until an explicit break-glass design is accepted in a later issue:

- Operator tooling MUST NOT silently read, decrypt, route through, or mutate resources of an arbitrary Tenant through ordinary product paths.
- Any future break-glass capability MUST be separately designed with audit, dual control, and non-default off posture. It is **out of scope and denied by default** here.

Support may receive **safe diagnostic context** that the owning Tenant already could observe (request id, canonical error class, own account health summary). Support MUST NOT receive Provider Credential plaintext, Client API Key material, or another Tenant’s resource existence confirmation.

---

## 3. Resource ownership table

| Resource | Canonical owner | Cardinality / attachment | Who may create | Who may read metadata | Who may use / execute | Who may mutate / delete | Notes |
|---|---|---|---|---|---|---|---|
| **Tenant** | Itself (platform-provisioned) | 1 root boundary | Platform provisioning (out of Public API MVP detail) | Owning Tenant principals (own self); platform ops only via future break-glass | N/A as execution target | Platform-controlled; Tenant cannot reassign ownership of child resources to another Tenant | Ownership of children is immutable to another Tenant |
| **Client API Key** | Exactly one Tenant | N keys per Tenant | Owning Tenant (management surface to be defined in #8) | Owning Tenant (metadata only; secret material shown once at issue) | Authenticated caller presenting that key’s secret | Owning Tenant revoke/rotate/delete; never reassign Tenant | Secret material is not a durable “shared secret” across Tenants |
| **Provider Account** | Exactly one Tenant | N accounts per Tenant; each has one Auth Mode | Owning Tenant | Owning Tenant only | Only Security Principal of same Tenant, subject to key scope and account state | Owning Tenant disable/reauth/delete | External identity similarity across Tenants does **not** create shared ownership |
| **Provider Credential** | Exactly one Tenant, attached to exactly one Provider Account of that Tenant | 1 logical credential lifecycle per account (rotation replaces secret, not owner) | Created only through owning Tenant’s connection/reauth flows | **No** Public API read of secret; metadata status only to owning Tenant | Decrypt/use only for execution or probe of the attached Provider Account by same-Tenant paths | Rotate/revoke/delete only for owning Tenant / vault lifecycle (#15) | Never copied or referenced by another Tenant’s account |
| **Asset** | Exactly one Tenant | Input, mask, or output objects | Owning Tenant via upload or as Render Job output | Owning Tenant only | Referenced only by same-Tenant jobs/requests | Owning Tenant delete; retention job of same Tenant | Existence of foreign Asset ids MUST NOT be confirmed |
| **Render Job** | Exactly one Tenant | Durable image generation/edit/inpaint work unit | Owning Tenant via Public API | Owning Tenant only | Workers act only with that Tenant’s accounts/credentials/assets | Owning Tenant cancel; system recovery within same Tenant | Job record stores `tenant_id` immutably |
| **Capability Snapshot** | Exactly one Tenant via its Provider Account | Snapshot of one Provider Account at one time | System probe/activation for that account | Owning Tenant only | Routing/capability discovery only inside owning Tenant | Invalidated/refreshed for that account only | Not a global Provider catalog |
| **Routing Policy object** | Exactly one Tenant | Tenant-scoped policy documents / bindings | Owning Tenant | Owning Tenant | Applied only when selecting candidates inside that Tenant | Owning Tenant update/delete | Candidate set MUST be subset of Tenant’s Provider Accounts |
| **Quota / admission counter** | Exactly one Tenant (and optionally subdivided by Client API Key or Provider Account of same Tenant) | Counters never aggregate across Tenants | System on use | Owning Tenant aggregate views only | Charged only to owning Tenant resources | Reset/adjust only within Tenant policy (#8) | Exhaustion of Tenant A MUST NOT affect Tenant B |
| **Request / execution log metadata** | Exactly one Tenant of the Security Principal or job owner | Correlated by request id | System | Owning Tenant safe views; operators only safe classes | N/A | Retention per data lifecycle (#15) | MUST NOT store Provider Credential or raw Client API Key |

### 3.1 Immutability of ownership

Once created, `tenant_id` on Client API Key, Provider Account, Provider Credential, Asset, Render Job, Capability Snapshot, Routing Policy object, and quota counters is **immutable**. There is no “transfer ownership to another Tenant” operation in MVP. Migration products, if ever needed, are a new design that must still avoid dual ownership windows.

### 3.2 Attachment rules

- Provider Credential → Provider Account: same `tenant_id` REQUIRED.
- Capability Snapshot → Provider Account: same `tenant_id` REQUIRED.
- Render Job → optional explicit Provider Account affinity: account’s `tenant_id` MUST equal job’s `tenant_id`.
- Render Job → input/mask/output Assets: every referenced Asset’s `tenant_id` MUST equal job’s `tenant_id`.
- Routing Policy → listed Provider Account ids: every listed account MUST share the policy’s `tenant_id`.

Any write that would create a cross-Tenant attachment MUST fail before persistence.

---

## 4. Authorization rules (positive cases)

Authorization is evaluated after authentication establishes the Security Principal `(tenant_id, client_api_key_id)`.

### 4.1 Universal positive rule (BYOA)

A request or worker action may use resource `R` if and only if:

1. `R.tenant_id == principal.tenant_id` (or job owner’s `tenant_id` for workers), and
2. The action is allowed by Client API Key scope / account state / resource state (details in #8, #9, #11, #14), and
3. For Provider Credential use: the credential is attached to the Provider Account selected for that same-Tenant action.

This is the normative statement of BYOA from `CONTEXT.md` and parent #1.

### 4.2 Provider Account selection

Positive cases:

- **Implicit routing**: Gateway selects a Provider Account from the **candidate set** = Provider Accounts owned by `principal.tenant_id` that satisfy Routing Policy, health, Capability Snapshot, and quota rules of that Tenant only.
- **Explicit account affinity**: Client names a Provider Account id that belongs to `principal.tenant_id` and is allowed by policy/scope.

Both paths MUST reject any account outside the Tenant’s ownership set before Adapter invocation.

### 4.3 Asset use

Positive cases:

- Upload creates an Asset with `tenant_id = principal.tenant_id`.
- A Render Job may reference Assets only when each Asset’s `tenant_id` matches the job’s `tenant_id`.
- Download/list/status of an Asset succeeds only for a Security Principal of the owning Tenant (and key scope if applicable).

### 4.4 Render Job use

Positive cases:

- Create job stamps immutable `tenant_id = principal.tenant_id`.
- Poll, cancel, and fetch output succeed only for same-Tenant principals.
- Worker recovery reloads job by id **and** enforces stored `tenant_id` when loading related accounts, credentials, and assets.

### 4.5 Capability discovery

Positive cases:

- List models/operations returns only Capability Snapshot data for Provider Accounts owned by `principal.tenant_id`.
- If client names an account id, only that account’s snapshot is considered, and only if same Tenant.

### 4.6 Quota and admission

Positive cases:

- Rate limit, concurrency, and quota checks apply to counters owned by `principal.tenant_id` (and optional same-Tenant key/account subdivisions).
- Successful execution debits only those same-Tenant counters.
- Provider-side quota signals update health/cooldown of the **same** Provider Account only.

### 4.7 Vault access (ownership only)

Positive cases for #15 to refine cryptographically:

- Encrypt/store Provider Credential only under owning Tenant + account attachment.
- Decrypt only on a code path already authorized for that account’s Tenant.
- Delete/rotate only under owning Tenant lifecycle events.

---

## 5. Negative cases and attack scenarios

### 5.1 Design choice: non-enumeration over informative 403

**Decision (locked here):** For resource identifiers that are Tenant-scoped (Provider Account, Provider Credential references, Asset, Render Job, Capability Snapshot ids, Routing Policy ids), when the authenticated principal’s Tenant does **not** own the resource, Gateway MUST respond with the **same observable outcome as “not found in this principal’s universe”**.

- Preferred Public API pattern: **HTTP 404** with a canonical “not found” error class that does **not** distinguish “exists but other Tenant” from “never existed”.
- Gateway MUST NOT return **403** solely because the resource exists under another Tenant, when that would confirm existence.
- Timing and error bodies MUST NOT include fields that reveal foreign ownership (other `tenant_id`, foreign account labels, foreign capability lists).

**Justification:**

- Parent #1 user story 4 and testing decisions require non-enumeration.
- Resource ids are expected to be unguessable, but defense in depth still forbids existence oracles.
- A pure 403-on-foreign-id pattern teaches attackers which ids are live.

**When 401 is used instead:**

- Missing, malformed, expired, or revoked Client API Key → **401** (no authenticated principal). This is not an ownership oracle.

**When 403 is still valid:**

- Authenticated principal owns the resource (or the action target is not a foreign-id lookup) but lacks **scope/permission** for the action (for example read-only key attempting delete), or the action violates a same-Tenant policy (disabled account, denied operation).
- 403 MUST NOT be used as the sole signal that a foreign Tenant id was recognized.

**When 400 is valid:**

- Malformed id syntax, invalid cross-field combinations that do not require foreign existence checks, or request shape errors.
- If validation would require probing foreign existence, prefer the non-enumeration path after auth.

### 5.2 Attack scenarios and required responses

| # | Scenario | Required observable response | MUST NOT |
|---|---|---|---|
| A1 | Tenant A key uses Tenant B Provider Account id in explicit affinity | 404 not found (or equivalent non-enumerating denial); no Adapter call; no B credential decrypt | 403 that confirms B account exists; fallback to any account; leak B metadata |
| A2 | Tenant A key lists accounts and expects to see B | List contains only A’s accounts | Include B ids even as “redacted” |
| A3 | Tenant A key GETs Asset/Job id owned by B | 404 not found | 403; partial metadata; different error than unknown id |
| A4 | Tenant A creates job referencing B’s Asset as mask/input | Reject before queue; non-enumerating denial for foreign asset id (404-class) | Create job; copy asset; reveal B asset exists via distinct error |
| A5 | Tenant A Routing Policy lists B account id | Reject policy write; do not store cross-Tenant reference | Persist mixed candidate set |
| A6 | Worker for job A is handed credential handle for account B (bug/confused deputy) | Fail closed; no upstream call with B credential; security incident path | Silent use of B; “best effort” routing |
| A7 | Replay of A’s request id / idempotency key by B | B cannot read A’s prior response body or job; treat as B’s own key space | Return A’s cached result to B |
| A8 | Tenant A probes capability with B account id | 404-class denial | Return B’s Capability Snapshot |
| A9 | Quota exhaustion on A; attacker hopes to free quota by touching B | No effect on B counters; A still limited | Cross-Tenant debit/credit |
| A10 | Similar external identity (same Google/OpenAI login) connected under A and under B | Two independent Provider Accounts; no shared credential, quota, or health | Merge accounts across Tenants; reuse vault object |
| A11 | Operator dashboard “search account by id” without break-glass | Denied by default / out of scope | Silent cross-Tenant read |
| A12 | Log injection / support ticket with B’s account id while acting as A | Support tools only show A-visible data; B id yields non-existence in A’s view | Decrypt B credential for “debugging” |
| A13 | Disabled/revoked A key used after revoke | 401 | Late acceptance; act with stale principal |
| A14 | Explicit fallback chain that includes only A accounts plus a forged B id | B id ignored as non-candidate / policy invalid; never selected | Select B when A candidates fail |

### 5.3 Confused deputy and explicit account affinity

**Confused deputy** here means: Gateway (the deputy) holds many Tenants’ Provider Credentials and must not let Tenant A’s request cause use of Tenant B’s credential.

Hard rules:

1. **Account affinity is a same-Tenant name, not a global capability grant.** An account id in a request is only a selector inside `principal.tenant_id`.
2. **Routing candidate sets are computed only from the principal’s Tenant.** Foreign ids never enter the candidate set.
3. **Credential decrypt is gated by (tenant_id, provider_account_id)** already authorized for the current action, not by “account id string alone”.
4. **Fallback** (details in #11) may only choose another Provider Account of the **same Tenant**, and only when that Tenant’s Routing Policy allows. Silent fallback across Web vs OAuth accounts is already forbidden by product rules; this document additionally forbids any cross-Tenant fallback.
5. **No ambient authority:** presence of a decrypted credential in process memory for Tenant B’s in-flight job MUST NOT make it available to Tenant A’s concurrent request.

---

## 6. Operator tooling invariants

| Path | MVP rule |
|---|---|
| Public API | Strict Security Principal + ownership checks |
| Background workers | Act only with job/resource `tenant_id` |
| Metrics/logs | Labels may include `tenant_id` of the acting principal/resource only when policy allows; never Client API Key secret or Provider Credential |
| Support tooling | Read only what the Tenant could already see; no foreign existence oracle |
| Break-glass admin | **Denied by default**; not designed in this issue; any future design needs explicit ADR + audit |

Circuit-breaking and Provider health degradation (parent #1 user story 54) MUST NOT replace a Tenant’s Routing Policy with another Tenant’s accounts or with a shared pool.

---

## 7. Observable failure semantics matrix

| Condition | Authn result | Authz / ownership result | HTTP-oriented outcome | Side effects | Security impact if violated |
|---|---|---|---|---|---|
| No/invalid/revoked Client API Key | Fail | Not evaluated | **401** unauthenticated | No Tenant resource access | Impersonation / open proxy |
| Valid key, action allowed, resource owned | Pass | Allow | 2xx / normal protocol | Same-Tenant only | N/A |
| Valid key, resource id unknown **or** owned by other Tenant | Pass | Deny (non-enumerate) | **404**-class not found | No foreign read; no credential use | Existence oracle / data leak |
| Valid key, resource owned, insufficient scope or same-Tenant policy deny | Pass | Deny (authorized but forbidden) | **403**-class forbidden | No privileged side effect | Privilege escalation inside Tenant |
| Valid key, cross-Tenant attachment attempted on write | Pass | Deny | **404**-class for foreign ids and/or **400** for invalid policy document without confirming foreign live ids | No persistence of illegal graph | Persistent cross-Tenant link |
| Worker loads job then related foreign account (integrity bug) | N/A (internal) | Fail closed | Job fails safely; alert | No upstream call with wrong credential | Cross-Tenant execution / quota theft |
| Decrypt credential for non-owning path | N/A | Deny | Internal error mapped to safe public failure if request-scoped | No plaintext export | Secret disclosure |
| Quota check | Pass authn | Same-Tenant counters only | 429/ quota canonical error when **own** counters exhausted | Debit only own counters | Cross-Tenant DoS or free capacity |

Exact canonical error codes are deferred to #16, but the **status-class and non-enumeration behavior** above are normative now.

---

## 8. Core invariants (normative checklist)

1. **I-TENANT-BOUNDARY** — Tenant is the sole ownership boundary for Client API Key, Provider Account, Provider Credential, Asset, Render Job, Capability Snapshot, Routing Policy objects, and quota counters.
2. **I-SINGLE-OWNER** — Each of those resources has exactly one `tenant_id`, immutable after creation.
3. **I-PRINCIPAL** — Every authenticated Public API request resolves exactly one Security Principal `(tenant_id, client_api_key_id)` from one Client API Key; that principal cannot be overridden by client-supplied Tenant fields.
4. **I-BYOA** — Execution may use a Provider Account only when `account.tenant_id == principal.tenant_id` (workers: `== job.tenant_id`).
5. **I-CREDENTIAL-BIND** — A Provider Credential is usable only for its attached Provider Account and only on same-Tenant authorized paths; never shared or retargeted across Tenants.
6. **I-NO-SHARED-POOL** — No shared Provider Account pool exists between Tenants.
7. **I-ASSET-ISO** — Assets are readable, writable, and referenceable only within their owning Tenant.
8. **I-JOB-ISO** — Render Jobs are visible and controllable only within their owning Tenant; recovery cannot rebind a job to another Tenant’s accounts or assets.
9. **I-ROUTE-SCOPE** — Routing and fallback candidate sets are subsets of the acting Tenant’s Provider Accounts only.
10. **I-QUOTA-SCOPE** — Quota and admission counters are charged and enforced only within the acting Tenant (and optional same-Tenant subdivisions).
11. **I-SNAPSHOT-SCOPE** — Capability Snapshots are never served or applied outside their Provider Account’s Tenant.
12. **I-NON-ENUM** — Foreign Tenant resource identifiers yield non-enumerating not-found behavior, not existence-confirming forbidden responses.
13. **I-NO-SILENT-CROSS** — There is no silent cross-Tenant fallback, credential reuse, asset reuse, or quota borrowing.
14. **I-FAIL-CLOSED** — Ownership check failures prevent Adapter calls, credential decrypt, and durable illegal attachments.
15. **I-OPERATOR-DEFAULT-DENY** — Operator/break-glass cross-Tenant access is denied by default until a future explicit design.

---

## 9. Security impact summary

| Invariant failure | Impact |
|---|---|
| Cross-Tenant Provider Account use | Unauthorized consumption of another user’s external identity and Provider quota; possible ToS/ban blast to the wrong owner |
| Cross-Tenant Provider Credential decrypt/use | Secret disclosure and full account takeover at Provider |
| Cross-Tenant Asset read | Confidential image/mask/output leakage |
| Cross-Tenant Render Job read/cancel | Confidential prompt/parameters leakage; denial of service on another Tenant’s work |
| Existence oracle (403-on-foreign-id) | Facilitates targeted attacks and tenant graph mapping |
| Cross-Tenant routing/fallback | Confused deputy turns Gateway into a cross-Tenant proxy |
| Cross-Tenant quota debit/credit | Financial/DoS impact and unfair capacity theft |
| Mutable `tenant_id` | Ownership confusion windows and audit gaps |

---

## 10. Test obligations

Mapped to parent #1 Testing Decisions. These are **required** contract/security tests; exact harness arrives with contract prototypes (#18–#20).

### 10.1 Public contract suite (minimum)

1. Client API Key of Tenant A can create/list/view/probe/use only Provider Accounts of A.
2. Using B’s Provider Account id with A’s key → non-enumerating denial; zero Adapter invocations; zero vault decrypt for B.
3. Asset and Render Job ids of B are not readable, cancelable, or downloadable by A; response matches unknown id.
4. Job create that references B’s Asset fails without creating a durable cross-Tenant job.
5. Capability discovery for A never includes B’s models/operations or account ids.
6. Explicit affinity and fallback never select B for principal A.
7. Idempotency/replay keys of A are not readable as cached results by B.
8. Revoked A key cannot access A’s resources (401), and still cannot access B’s.

### 10.2 Security negative suite

- Cross-Tenant access matrix for account, credential metadata, asset, job, snapshot, routing policy.
- Confused-deputy: concurrent jobs for A and B never swap credentials.
- Policy write attempts that embed foreign account ids fail closed.
- Log/redaction checks: no Provider Credential or Client API Key secret in responses or standard logs.

### 10.3 Vault and job conformance (ownership slices)

- Vault: decrypt denied when caller tenant ≠ credential tenant (#15 expands crypto).
- Job worker: claim of job A cannot load account B even if id is injected in corrupted message; fail closed.

### 10.4 Observable assertions only

Tests assert HTTP status class, canonical error class, absence of foreign fields, durable state (no illegal row), and side-effect counters (Adapter calls = 0). Tests must not depend on private package layout.

---

## 11. Worked examples (cause → effect)

### Example 1 — Explicit affinity attack

1. Attacker authenticates as Tenant A.
2. Attacker sends chat request with `provider_account_id = acc_B`.
3. Gateway authenticates key → principal Tenant A.
4. Ownership check: `acc_B.tenant_id != A` **or** account not visible in A’s store query scoped by `tenant_id=A`.
5. Effect: **404-class** response; no Web/OAuth Adapter call; B’s quota untouched.

### Example 2 — Asset reference attack

1. Tenant A creates an inpaint job with `mask_asset_id` stolen from a log of Tenant B.
2. Gateway resolves assets with query `WHERE id=? AND tenant_id=A` (or equivalent enforce-after-load).
3. Effect: mask not found for A → job not queued → **404-class** on the foreign asset id path.

### Example 3 — Legitimate same-Tenant routing

1. Tenant A has two Provider Accounts (ChatGPT Web Access and ChatGPT Codex OAuth).
2. Request authenticates as A without explicit affinity.
3. Routing builds candidates only from A’s accounts per A’s Routing Policy and Capability Snapshots.
4. Effect: execution uses only A’s chosen account; never B; never a shared pool.

### Example 4 — Same external identity, two Tenants

1. User connects the same Google identity as Gemini Antigravity OAuth under Tenant A and later under Tenant B.
2. Gateway stores two Provider Accounts and two Provider Credentials.
3. Effect: A’s key cannot use B’s account id; health/quota remain separate; no vault aliasing across Tenants.

---

## 12. Open follow-ups (explicitly deferred)

| Topic | Issue | What this doc already constrains |
|---|---|---|
| Client API Key issue/hash/scope/RPM/concurrency/abuse numbers | #8 | Key belongs to one Tenant; revoke kills principal; scopes only narrow |
| Provider Account connection/reauth/probe journeys | #9 | Account and credential always same Tenant; no cross-Tenant link |
| Capability Snapshot schema/TTL | #10 | Snapshot owned via account’s Tenant; discovery scoped |
| Routing/fallback/lease algorithms | #11 | Candidate set ⊆ Tenant accounts; no cross-Tenant fallback |
| Asset retention/deletion details | #13 | Ownership + non-enumeration retained |
| Render Job state machine / output retry | #14 | Job tenant immutable; recovery same Tenant |
| Vault crypto/KMS/audit detail | #15 | Decrypt only same-Tenant authorized paths |
| Canonical error code names | #16 | 401 / 404-class non-enum / 403 same-Tenant forbidden classes fixed |
| Operator health UI / break-glass | #17 | Default deny cross-Tenant; no silent pool |

---

## 13. ADR decision

No new ADR is filed for this issue. Ownership and BYOA isolation were already product-locked in parent #1 and `CONTEXT.md`. This document is the durable normative expansion under `docs/spec/`. If a future break-glass operator model is accepted, that **would** warrant a separate ADR because it would change the default-deny posture in §2.5 and §6.

---

## 14. Acceptance criteria traceability

| AC | Where satisfied |
|---|---|
| Every resource and action has canonical owner or authorization rule | §3, §4, §8 |
| Provider Account, Provider Credential, Asset, Job, routing, quota cannot be used cross-Tenant | §3–§5, invariants I-BYOA, I-CREDENTIAL-BIND, I-ASSET-ISO, I-JOB-ISO, I-ROUTE-SCOPE, I-QUOTA-SCOPE, I-NO-SILENT-CROSS |
| Foreign Tenant identifiers follow safe non-enumeration | §5.1, §5.2, §7, I-NON-ENUM |
| Invariant violations state observable failure semantics and security impact | §5, §7, §9 |
