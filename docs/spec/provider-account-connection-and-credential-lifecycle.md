# Provider Account Connection and Provider Credential Lifecycle

- Status: Accepted for specification (issue #9)
- Date: 2026-07-14
- Parent: [#1](https://github.com/monet88/pixelplus/issues/1)
- Issue: [#9](https://github.com/monet88/pixelplus/issues/9)
- Vocabulary source: `CONTEXT.md`
- Related ownership invariants: `docs/spec/tenant-ownership-authorization-invariants.md` (#6)
- Related risk envelope: `docs/spec/auth-mode-risk-envelope-and-kill-criteria.md` (#7)
- Related Client API Key / admission: `docs/spec/client-api-key-lifecycle-and-admission-controls.md` (#8)
- Evidence inputs (research only; not acceptance by themselves):
  - `docs/spec/research/chatgpt-auth-mode-capability-evidence.md` (#3)
  - `docs/spec/research/gemini-auth-mode-capability-evidence.md` (#4)
  - `docs/spec/research/grok-auth-mode-capability-evidence.md` (#5)

## 1. Scope and non-goals

### 1.1 Scope

This specification locks the **Provider Account connection journey** and the **Provider Credential lifecycle** that produces **observable account states** and **consistent remediation guidance**.

It covers, for every initial Auth Mode:

1. Create (start connection)
2. Credential submission (material intake)
3. Validation (shape / cryptographic / issuer checks that do not require a full capability matrix)
4. Probe (live or fixture-authorized upstream check required before usability)
5. Activation (mark account usable for routing/execution under product policy)
6. Refresh (silent credential renewal where the Auth Mode supports it)
7. Reauthentication (human-mediated credential replacement on the same logical account)
8. Disable (Tenant or system temporary unusable without deleting the record)
9. Revoke (invalidate credential usability; stronger than disable)
10. Delete (Tenant-initiated removal of the account record and attached credential material)

It also locks:

- The separation of **Provider Account** (metadata + state) from **Provider Credential** (vaulted secret)
- **Usability gates** so an account never becomes usable when required validation or probe fails
- **Per-Auth-Mode credential classes and lifecycle differences** without mixing Web Access and OAuth/CLI Access
- **Redaction rules** so responses, logs, metrics, and operator metadata never leak Provider Credential material
- Minimum risk-driven connection UX obligations from #7 §6

It codifies parent #1 user stories 5–7, 11, 13–19, 55, 58–60 (connection / reauth / health surface / secret handling portions) and testing obligations for connection, reauthentication, disable, and delete.

### 1.2 Non-goals

This document does **not**:

- Implement Gateway, Adapter, vault cryptography, or UI code.
- Design Capability Snapshot schema/TTL beyond the activation and probe triggers that feed #10.
- Design Routing Policy algorithms, lease, or cooldown schedules beyond usability eligibility (#11, #17).
- Design vault KMS/envelope/rotation cryptography (#15) — this document only locks **when** credential material may be written, read, rotated, revoked, and deleted, and **what** must never leave the vault boundary.
- Design full canonical error **code strings** (#16) — it locks **status classes**, remediation classes, and non-enumeration.
- Design operator break-glass or multi-Tenant health dashboards (#17) beyond account-state visibility the owning Tenant may already see.
- Freeze exact OpenAPI paths and response JSON schemas (#18 / #20).
- Promote or demote Auth Mode risk status (#7 owns that).
- Approve Official API Adapters (parent #1 Out of Scope for MVP).
- Treat `.ref/*` reverse-engineering projects as legal permission or as automatic production capability.

Downstream issues **MUST** preserve every decision here. They may add fields, tighten probes, or add UX steps; they **MUST NOT**:

- Make an account usable without successful required validation + required probe
- Mix Web and OAuth/CLI credential lifecycles into one Provider Account
- Return or log Provider Credential material outside the vault decrypt boundary
- Weaken #6 ownership / non-enumeration
- Silently re-enable a `prohibited` Auth Mode or skip `gated` acknowledgements from #7

### 1.3 Normative language

- **MUST / MUST NOT / REQUIRED**: product/security policy. Violation is a defect.
- **SHALL**: same force as MUST for observable Public API / management API behavior.
- **SHOULD**: strongly preferred default; deviation needs an operator-recorded exception.
- **MAY**: optional surface that cannot weaken MUST rules.

### 1.4 Relationship to prior issues

| Topic | Already locked | This document adds |
|---|---|---|
| Ownership of Provider Account / Credential | Exactly one Tenant; immutable `tenant_id`; credential bound to one account (#6) | Connection journey, state machines, usability gates |
| Auth Mode risk status | `allowed` / `gated` / `experimental` / `prohibited` + kill/reopen (#7) | How status gates create/connect UX and activation |
| Client API Key scopes | `accounts.read`, `accounts.manage` (#8) | Which lifecycle ops require which scope |
| Credential vault crypto | Deferred to #15 | Write/read/delete **moments** and redaction |
| Capability Snapshot | Deferred to #10 | Activation requires a successful required probe that can mint/refresh a snapshot |
| Health / cooldown numbers | Deferred to #17 | Account **operational health** vocabulary and how it interacts with lifecycle states |

### 1.5 Decision unit

**One Provider Account = exactly one Auth Mode + exactly one Tenant + exactly one logical external identity attachment for that mode.**

Cause → effect:

1. One human may hold both ChatGPT Web Access material and ChatGPT Codex OAuth material.
2. Those are **two** Provider Accounts (two Auth Modes, two credential lifecycles, two Capability Snapshots).
3. Connection, refresh, reauth, disable, revoke, and delete never “upgrade” a Web account into an OAuth account in place.
4. Explicit conversion flows (for example research-only SSO→Build conversion observed in Grok references) MUST create or rebind only through an **explicit** connection action that targets the destination Auth Mode — never as silent fallback (#7 FG-2 / §6.3).

---

## 2. Glossary extensions (normative use)

| Term | Meaning in this document |
|---|---|
| **Provider Account** | Tenant-owned durable record representing a connection to one Provider via exactly one Auth Mode. Holds **non-secret** metadata and lifecycle/operational state. Does **not** hold plaintext Provider Credential material. |
| **`provider_account_id`** | Stable non-secret identifier of the Provider Account. Safe for logs, metrics, audit, list/get responses (owning Tenant only). |
| **Provider Credential** | Vaulted secret material that proves the gateway may act as that Provider Account. One logical credential lifecycle per account; rotation replaces material, not ownership (#6). |
| **`credential_handle`** | Opaque vault reference stored on the account (or equivalent). Safe to log as an id; **never** decryptable from the handle alone without vault authorization. |
| **Connection journey** | Ordered product flow from create → submit → validate → probe → activate (or fail). |
| **Usable** | Account may be selected for routing/execution (subject to Routing Policy, Capability Snapshot, health, kill switch, and key scope). See §5. |
| **Required validation** | Auth-Mode-defined checks that MUST succeed before a probe is allowed to mark progress toward usability (shape, required fields, issuer host allowlist, token parse without upstream). |
| **Required probe** | Auth-Mode-defined live check (or authorized lab fixture in tests) that MUST succeed before activation. Probe may mint/refresh a Capability Snapshot (#10). |
| **Silent refresh** | System-initiated credential renewal **without** Tenant re-entry of secrets, only when the Auth Mode credential class supports it (typically OAuth refresh_token). |
| **Reauthentication** | Human-mediated submission of replacement Provider Credential material for an **existing** `provider_account_id`. |
| **Lifecycle state** | Durable account state governing connection and credential ops (`draft`, `pending_validation`, `pending_probe`, `active`, `reauth_required`, `disabled`, `revoked`, `deleted`). |
| **Operational health** | Orthogonal runtime signal (`healthy`, `degraded`, `auth_expired`, `challenged`, `quota_exhausted`, `rate_limited`, `protocol_drift`, `provider_banned`, `unknown`). Does not replace lifecycle state; see §6. |
| **Remediation class** | Stable guidance category returned to the Tenant. Canonical tokens (this document): `submit_credential`, `complete_oauth`, `reauthenticate`, `ack_risk`, `enable_account`, `wait_provider_cooldown`, `contact_operator`, `auth_mode_unavailable`, `delete_and_recreate`. Exact Public API error **code strings** remain #16. |
| **Risk acknowledgement** | Explicit Tenant accept of residual ToS/ban/custody themes required by #7 for `gated` (and stronger lab warnings for `experimental`). |
| **Auth Mode kill / feature gate** | Platform-level block from #7 OP-G1 / KS-* / FG-* that forbids new connections and/or new executions for that Auth Mode. |

---

## 3. Objects and durable records

### 3.1 Provider Account logical fields

| Field | Required | Notes |
|---|---|---|
| `tenant_id` | yes | Immutable (#6) |
| `provider_account_id` | yes | Stable id; safe to expose to owning Tenant |
| `provider` | yes | `chatgpt` \| `gemini` \| `grok` (product family label; **not** the risk unit) |
| `auth_mode` | yes | Exactly one of the six initial Auth Modes; **immutable** after create |
| `lifecycle_state` | yes | See §4 |
| `operational_health` | yes | See §6; default `unknown` until first successful probe or classified failure |
| `label` | no | Tenant-visible name |
| `external_subject_hint` | no | Non-secret label only (email local-part hash, masked email, account_id public form). MUST NOT be raw cookie/token |
| `plan_type_hint` | no | Last observed non-secret plan/tier string from probe |
| `credential_handle` | when secret stored | Opaque vault pointer; absent in `draft` before first successful store |
| `credential_version` | when secret stored | Monotonic version for rotate/reauth audit |
| `credential_expires_at` | when known | Access-token or session expiry **hint** only; absence does not imply eternal validity |
| `refresh_supported` | yes | Boolean derived from Auth Mode + submitted credential class (§8) |
| `last_validated_at` | optional | Last successful required validation |
| `last_probed_at` | optional | Last required probe attempt completion |
| `last_activated_at` | optional | Last transition into `active` |
| `last_refresh_at` | optional | Last successful silent refresh |
| `last_reauth_at` | optional | Last successful reauthentication |
| `disabled_at` / `disable_reason` | when disabled | Tenant or system |
| `revoked_at` / `revoke_reason` | when revoked | |
| `deleted_at` | when soft-deleted pending hard purge | |
| `risk_ack_at` / `risk_ack_version` | when required | Gated/experimental acknowledgement record |
| `created_at` / `updated_at` | yes | |

**MUST NOT** store on the Provider Account row (or any Public API-visible projection):

- Raw access tokens, refresh tokens, cookies, SSO tokens, passwords, client secrets belonging to the user session
- Full browser cookie jars
- Authorization codes, device codes, PKCE verifiers after exchange completes
- Provider Credential ciphertext in a column that management list endpoints project by default (ciphertext lives in vault per #15)

### 3.2 Provider Credential logical fields (vault)

Logical contents (encrypted at rest; #15 owns crypto):

| Field | Required | Notes |
|---|---|---|
| `tenant_id` | yes | Same as account; immutable |
| `provider_account_id` | yes | Attachment; immutable |
| `auth_mode` | yes | Must match account |
| `credential_class` | yes | See §8 per Auth Mode |
| `material` | yes while present | Opaque encrypted blob of the class-specific secret set |
| `material_version` | yes | Matches account `credential_version` after successful write |
| `created_at` / `rotated_at` | yes | |
| `revoked_at` | when revoked | |

### 3.3 Separation rule (normative)

```text
Provider Account  =  identity + policy + observable state  (non-secret)
Provider Credential  =  secret proof attached 1:1 to that account  (vault)
```

Cause → effect:

1. List/get Provider Account returns state, health, labels, expiry **hints**.
2. It never returns material, even to `accounts.manage`.
3. Execution decrypts credential only after same-Tenant authorization for that `provider_account_id` (#6 I-CREDENTIAL-BIND).
4. Delete account implies vault delete of attached credential (ordering in §4.11).

---

## 4. Lifecycle state machine

### 4.1 States

```text
create (draft)
   │
   │ submit credential / start OAuth device|browser
   ▼
pending_validation ──fail──► draft | reauth_required* | revoked**
   │ success
   ▼
pending_probe ──fail──► reauth_required | disabled*** | draft****
   │ success
   ▼
 active ◄──────── silent refresh success (stays active)
   │  │
   │  └── silent refresh fail / upstream auth class ──► reauth_required
   │
   ├── Tenant disable ──► disabled ── Tenant enable (if credential still valid) ──► pending_probe or active*****
   ├── Tenant/system revoke credential ──► revoked
   ├── reauth submit ──► pending_validation → pending_probe → active
   └── Tenant delete ──► deleted (terminal for product use)

*  if account already had a prior usable generation and reauth is the remediation
** if Auth Mode kill / prohibited / policy revoke of credential class
*** e.g. operator/system disable after ban classification (see §6)
**** first-time connect only: failed probe may return to draft without durable secret if policy chooses not to retain failed material (default: retain encrypted material but state stays non-usable — §4.5)
***** enable of disabled account with untrusted/expired credential MUST re-probe; see §4.9
```

| State | Meaning | Usable for routing/execution? |
|---|---|---|
| **`draft`** | Account shell created; Auth Mode chosen; no successful validation yet or first connect abandoned | **No** |
| **`pending_validation`** | Credential material accepted into a controlled intake path; validation running or required | **No** |
| **`pending_probe`** | Validation succeeded; required probe not yet successful | **No** |
| **`active`** | Validation + required probe succeeded; not disabled/revoked/deleted; Auth Mode product-connectable | **Yes**, subject to operational health, kill switch, capability, routing, key scope |
| **`reauth_required`** | Credential known bad, expired without recoverable silent refresh, or refresh permanently failed | **No** |
| **`disabled`** | Explicit Tenant or system disable; credential may still exist | **No** |
| **`revoked`** | Credential invalidated for use; stronger than disable | **No** |
| **`deleted`** | Soft-deleted pending purge or hard-deleted; id MUST behave as unknown to Public API | **No** |

### 4.2 Global transition rules

1. **Activation preconditions (subset of I-USABLE-GATE):** Transition into `lifecycle_state=active` is allowed **only** after:
   - required validation success for the current `credential_version`, **and**
   - required probe success for that same `credential_version`, **and**
   - Auth Mode is product-connectable for this deployment/Tenant per #7 (not `prohibited`; `gated` needs flag+ack; `experimental` only in lab profile).
   Full **usable-for-routing/execution** conjunction is defined once in **§5.1 (`I-USABLE-GATE`)** and includes hard-block health, vault-revoked, and request-time scope/capability checks.
2. **No skip:** Create MUST NOT jump directly to `active` in a single write that bypasses validation+probe, even if the client sends a “trust me” flag.
3. **Auth Mode immutability:** `auth_mode` never changes. Wrong mode → delete and create a new account (or create a second account).
4. **Tenant immutability:** `tenant_id` never changes (#6).
5. **Idempotency:** Disable/revoke/delete/reauth-start operations are idempotent at the product level (second call does not re-enable secrets or flip state backwards unexpectedly).
6. **Fail closed on Auth Mode kill:** If OP-G1 / KS-* disables the Auth Mode, new creates fail closed. Existing accounts become **non-usable for execution immediately** (§5.1 item 2: Auth Mode not execution-enabled). Kill-switch reconciler MUST move formerly `active` accounts to durable `disabled` (operational kill / OP-G1) or `reauth_required` (KS-6 credential-class invalidation and similar auth-class kills) without waiting for the next Tenant action, so lifecycle state does not advertise a still-connected account after kill. Until that write lands, execution gates still fail closed on the Auth Mode flag alone.
7. **No silent cross-mode recovery:** Refresh/reauth stays on the same Auth Mode (#7 §6.3).

### 4.3 Create

**Who:** principal with `accounts.manage` for the Tenant (#8).

**Input (logical):**

- `auth_mode` (required)
- optional `label`
- optional connection parameters that are **non-secret** (e.g. prefer device-auth vs browser for OAuth)

**Effect:**

1. Reject if Auth Mode is `prohibited` in product composition → stable failure class `auth_mode_unavailable` (not an existence oracle for other Tenants).
2. Reject if Auth Mode is `gated` and operator feature flag is off → `auth_mode_unavailable` / not enabled.
3. Reject if Auth Mode is `experimental` and deployment is not a lab profile → `auth_mode_unavailable`.
4. Create row: `lifecycle_state=draft`, `operational_health=unknown`, no `credential_handle`.
5. Audit: `provider_account.created` with `tenant_id`, `provider_account_id`, `auth_mode`, actor — **no secrets**.

**MUST NOT** accept Provider Credential material on create if that would skip the validation record trail; preferred pattern is create shell then submit. Implementations MAY accept material in one management request **only if** the internal state machine still visits `pending_validation` and `pending_probe` and **does not** mark usable on store alone.

### 4.4 Credential submission

**Who:** `accounts.manage` for owning Tenant.

**Paths:**

| Path | Used by | Observable |
|---|---|---|
| **Direct material submit** | Web Access modes (token/cookie/SSO paste); optional OAuth token import for lab | Request carries secret once over TLS; response never echoes it |
| **OAuth browser / device start** | OAuth/CLI modes | Returns device code / verification URI or authorization redirect metadata; secrets appear only after token exchange into vault |
| **Reauth submit** | Any mode when `reauth_required` or Tenant-initiated rotate | Same as above but `provider_account_id` pre-exists |

**Rules:**

1. Foreign / unknown `provider_account_id` → **404-class** (#6).
2. Same-Tenant but insufficient scope → **403-class**.
3. Material is accepted into a short-lived intake buffer, then written to vault only after required validation **or** (for OAuth) after successful token exchange validation of issuer/endpoints.
4. Default after first successful vault write from a clean draft: `lifecycle_state=pending_probe` if validation already succeeded as part of submit; else `pending_validation`.
5. Submitting material **never** alone sets `active`.
6. Audit: `provider_credential.submitted` with account id + credential_version + class — **no material**.

**OAuth device/browser intermediate state:** While waiting for user approval, account remains non-usable (`draft` or `pending_validation`). Polling status MAY show `authorization_pending` as a **substatus** without inventing a separate durable lifecycle state.

### 4.5 Validation

**Purpose:** Catch malformed or incomplete credentials **before** spending upstream probe budget and before marking any success path toward usable.

**Required validation (normative minimum by access class):**

| Access class | Required validation examples |
|---|---|
| **Web Access** | Required fields present for the Auth Mode class; charset/size bounds; reject obviously truncated cookies/tokens; reject material that includes control characters after sanitization rules; **never** log the material while validating |
| **OAuth/CLI Access** | Issuer/token endpoint host allowlist; `grant_type` path consistency; presence of access token after exchange; refresh_token presence when `offline_access` was required; reject tokens bound to wrong client_id class when detectable |

**Outcomes:**

| Result | Next lifecycle state | Credential vault |
|---|---|---|
| Success | `pending_probe` | Material stored (or already stored from exchange) |
| Failure (first connect) | remain `draft` or return to `draft` | Prefer **not** retaining invalid material; if retained for support, still non-usable and redacted |
| Failure (reauth) | remain `reauth_required` (or prior non-active state) | Prior good version remains until a new version fully activates (see §4.8 dual-version rule) |

**MUST NOT** set `active` on validation success alone.

### 4.6 Probe

**Purpose:** Prove the credential can authenticate to the Auth Mode’s upstream surface enough to trust activation.

**Required probe (normative intent; exact endpoints are Auth-Mode Adapter details):**

| Auth Mode | Probe intent (from research; Adapter owns wire format) |
|---|---|
| ChatGPT Web Access | Authenticated identity/session check + conversation/init style entitlement touch as available |
| ChatGPT Codex OAuth | Authenticated Codex surface call with required account headers after token present |
| Gemini Web Cookie | Session init / session-token derivation success without sign-in HTML |
| Gemini Antigravity OAuth | Token valid + project/onboard load success path |
| Grok Web SSO | Product: **not connectable** (`prohibited`); probe path MUST NOT be exposed in product |
| Grok xAI OAuth | Token valid + authenticated call on the bound base-URL family recorded for the account |

**Probe rules:**

1. Probe runs only for owning Tenant paths (management or system job stamped with that `tenant_id`).
2. Probe **MAY** create/refresh a Capability Snapshot (#10 owns schema).
3. Probe **MUST** classify failures into operational health + remediation (§6–§7).
4. Probe success + usability gate (§4.2) → transition to `active`, set `last_activated_at`, `last_probed_at`, health typically `healthy` (or `degraded` if probe succeeded with partial capability — still may be usable if product defines minimum bar; MVP minimum bar = auth success + Auth Mode still connectable).
5. Probe failure → **MUST NOT** transition to `active`. Typical transitions:
   - auth class → `reauth_required` (+ health `auth_expired` or `challenged`)
   - transient → stay `pending_probe` with health `unknown`/`degraded` and retry policy (#17)
   - Auth Mode unavailable → non-usable + remediation `auth_mode_unavailable`

**Cost rule:** Required probes MUST be the **cheapest auth-proving path** the Adapter supports. They MUST NOT create billable image renders or long chat generations as part of activation (parent #1 story 19). If an Auth Mode has no free probe, product MUST document the residual cost and still keep probes minimal.

**Tenant-triggered re-probe:** Allowed with `accounts.manage` (or a narrower future scope). Re-probe of an `active` account updates health/snapshot; failure may move account to `reauth_required` or leave `active` with health `degraded` only when failure is **not** auth-class. Auth-class failure **MUST** clear usability (§5).

### 4.7 Activation

Activation is **not** a separate client verb in MVP; it is the successful completion of §4.5–§4.6 under §4.2.

Observable effects when activated:

1. `lifecycle_state=active`
2. Account may appear in routing candidate sets (#11) if operational health and kill switch allow
3. Capability Snapshot becomes readable to owning Tenant via `capabilities.read` / `accounts.read` (#10)
4. Audit: `provider_account.activated`

### 4.8 Silent refresh

**When allowed:** only if `refresh_supported=true` for the account (Auth Mode + credential class; §8).

**Who:** system workers acting for the account’s `tenant_id` (#6 §2.4), or inline refresh on auth-expiry during execution with singleflight per credential version.

**Rules:**

1. Refresh decrypts current material, performs Auth-Mode refresh grant, writes **new** material under a **new** `credential_version` (monotone +1; no silent same-version overwrite of rotated secret fields — keeps audit and dual-version cutover aligned with §4.9), updates `credential_expires_at` / `last_refresh_at`.
2. Refresh success from `active` **keeps** `lifecycle_state=active` (it is not a new activation ceremony and does not re-open draft/pending_*). Metadata and `credential_version` update; account remains subject to §5.1.
3. Refresh failure that is **permanent auth class** (invalid_grant, refresh_token_reused, revoked client, 401 without recovery) → `lifecycle_state=reauth_required`, health `auth_expired`, usable=false.
4. Refresh failure that is **transient** → keep lifecycle state, health `degraded`/`unknown`, backoff (#17); do not hammer (#7 KS-3 interaction).
5. Concurrent refresh MUST be singleflight per `(tenant_id, provider_account_id)` (research: Codex/xAI refresh_token reuse risk).
6. Refresh MUST NOT switch Auth Mode or Tenant.
7. Audit: `provider_credential.refreshed` without material.

**Web modes without refresh token:** `refresh_supported=false`. Cookie rotation helpers (e.g. Gemini PSIDTS rotation, ChatGPT optional refresh_token if present) are **Auth-Mode-specific**:

- If the Auth Mode defines an automatic companion-cookie/session re-derive that does **not** require human paste, it MAY count as silent refresh **only** for those companion fields; primary session invalidation still yields `reauth_required`.
- Grok Web SSO: no silent refresh path in product (and mode is prohibited).

### 4.9 Reauthentication

**Definition:** Tenant (or lab operator) supplies new primary Provider Credential material for an **existing** `provider_account_id`, keeping Auth Mode and Tenant.

**Triggers:**

- `lifecycle_state=reauth_required`
- `lifecycle_state=revoked` (explicit prior revoke; recovery is reauth with a new credential version, not “enable”)
- Tenant-initiated credential rotate while `active`/`disabled` (planned rotation)
- Operator instruction after challenge/ban recovery where credential was invalidated

**Flow:**

1. Authorize `accounts.manage` + ownership.
2. Enforce Auth Mode still connectable (#7); if `prohibited`/killed → fail closed.
3. For `gated`, prior risk ack may still be valid if `risk_ack_version` current; if risk themes version bumps, re-ack (`ack_risk`) REQUIRED before new material becomes **usable** (§5.1 / #7 §6.1 “stored as usable” = activation/usable, not merely encrypted vault write of intake material).
4. Accept material (direct or OAuth restart). Vault **MAY** write ciphertext during intake/`pending_*` for crash safety; that write alone MUST NOT satisfy §5.1.
5. Validate → probe → only then `active` (or back to `disabled` if Tenant wants to remain disabled: credential may update while state stays `disabled` until enable). From `revoked`, successful reauth activation is the only product path back to `active`.
6. **Dual-version rule (MVP locked):** During reauth, previous credential version remains the only decrypt source for in-flight executions until the new version reaches `active` (or until revoke). When new version activates, previous version is vault-revoked and MUST NOT decrypt for new work. No long dual-valid window for **new** admissions after activation of the new version (aligns with #8 rotate immediacy spirit for secrets).

**MUST NOT** create a second Provider Account solely because reauth occurred.

### 4.10 Disable

**Definition:** Mark account non-usable without deleting metadata or necessarily destroying vault material.

**Who:** owning Tenant `accounts.manage`, or system/policy (Auth Mode kill reconciler, ban classification, abuse).

**Rules:**

1. `lifecycle_state=disabled`; usable=false.
2. In-flight work: attempt cancel where cancelable (#12/#14); do not start **new** executions.
3. Enable (Tenant): only if credential still present and not revoked; MUST run required probe if `last_probed_at` is missing for current `credential_version` or health is `auth_expired`/`provider_banned`/`challenged` — fail closed to `reauth_required` on auth-class probe failure.
4. Disable is idempotent.
5. Audit: `provider_account.disabled` / `provider_account.enabled`.

### 4.11 Revoke (credential / account credential usability)

**Definition:** Invalidate Provider Credential so it MUST NOT be decrypted for execution or probe success paths. Stronger than disable.

**Who:** owning Tenant `accounts.manage`, system on provider-side invalidation, or vault lifecycle (#15).

**Rules:**

1. Vault marks credential revoked; decrypt for execution fails closed.
2. Account `lifecycle_state=revoked` (or `reauth_required` if product chooses to keep a softer Tenant-facing state — **MVP locks `revoked` for explicit revoke API**, and `reauth_required` for natural expiry/refresh-fail without explicit revoke).
3. Explicit revoke MUST NOT leave the account `active`.
4. New probes using revoked material MUST fail; activation forbidden until reauth stores a new version.
5. Audit: `provider_credential.revoked`.

**Difference disable vs revoke (concrete):**

| | Disable | Revoke |
|---|---|---|
| Intent | Pause use; maybe temporary | Invalidate secret trust |
| Vault material | May remain | Must not be used; prefer cryptographic/logical revoke |
| Typical restore | enable + maybe probe | reauth with new material |
| Example | Tenant pauses account for cost control | Token leaked; refresh_token_reused; Tenant clicks revoke |

### 4.12 Delete

**Definition:** Remove Provider Account from Tenant’s product universe.

**Who:** owning Tenant `accounts.manage`.

**Rules:**

1. Foreign id → **404-class**.
2. Set non-usable immediately; cancel-on-delete attempt for cancelable in-flight work using this account.
3. Vault delete/shred attached Provider Credential (ordering: stop new decrypt → revoke → delete material → delete or tombstone account). #15 refines cryptographic shred and retention legal hold.
4. After delete acknowledgement, get/list by that id for the Tenant behaves as **not found** (soft-delete tombstone may exist internally for audit but MUST NOT reappear in ordinary list).
5. Delete is terminal for product use of that `provider_account_id`. Recreate = new id.
6. Audit: `provider_account.deleted` without secrets.

### 4.13 Transition matrix (summary)

| From \ Event | submit OK | validate fail | probe OK | probe auth-fail | silent refresh success | silent refresh fail permanent | disable | enable | revoke | reauth activate | delete |
|---|---|---|---|---|---|---|---|---|---|---|---|
| draft | pending_validation / pending_probe | draft | — (must be pending_probe first) | — | — | — | disabled | — | revoked | — | deleted |
| pending_validation | — | draft / reauth_required | — | — | — | — | disabled | — | revoked | — | deleted |
| pending_probe | — | — | active* | reauth_required | — | — | disabled | — | revoked | — | deleted |
| active | reauth path → pending_validation | — | active (re-probe) | reauth_required | active (version++) | reauth_required | disabled | — | revoked | active | deleted |
| reauth_required | pending_validation | reauth_required | active* | reauth_required | — | — | disabled | — | revoked | active | deleted |
| disabled | optional reauth store | disabled | disabled or active on enable path | reauth_required | — | — | disabled | pending_probe / active* | revoked | disabled/active* | deleted |
| revoked | reauth → pending_validation | revoked | — | — | — | — | — | — | revoked | active* | deleted |
| deleted | — | — | — | — | — | — | — | — | — | — | deleted |

\* `active` only if §4.2 activation preconditions and full §5.1 `I-USABLE-GATE` can hold after the transition (including risk ack and Auth Mode connectable).

---

## 5. Usability rules

### 5.1 Definition of usable (`I-USABLE-GATE` — authoritative)

An account is **usable** for routing and new execution if and only if **all** hold:

1. `lifecycle_state == active`
2. Auth Mode is currently **execution-enabled** for the deployment (not killed; not `prohibited`; `gated`/`experimental` gates still satisfied, including required risk ack for gated modes)
3. Operational health is not in a hard-block set: MVP hard-block = `auth_expired`, `provider_banned`, `challenged` (until cleared by successful reauth/probe policy), and any #17 extension that marks non-routable
4. Current `credential_version` has passed required validation + required probe
5. Credential is not vault-revoked
6. Client API Key scope/allowlist allows the account when the caller is a Public API principal (#8) — N/A for pure system jobs that already act under the account’s `tenant_id`
7. Capability Snapshot requirements for the requested operation are satisfied when the action is capability-bearing (#10) — checked at request time; not a durable lifecycle field

Items 1–5 are **account-durable** gates. Items 6–7 are **request-time** gates that cannot make a non-`active` account usable.

### 5.2 Non-usable must fail before Adapter execution

If an account is selected explicitly but not usable:

- Same-Tenant policy deny → **403-class** with remediation class (e.g. `reauthenticate`, `enable_account`, `ack_risk`)
- Foreign/unknown id → **404-class** (#6)
- Auth Mode unavailable / prohibited → fail closed with `auth_mode_unavailable` (same-Tenant) without leaking whether another Tenant has such accounts

**MUST NOT** call upstream with a non-usable account “to see if it still works” as a substitute for the connection probe state machine, except for explicit Tenant-triggered re-probe or system health probes that still cannot mark usable without §5.1.

### 5.3 Concrete cause → effect examples

#### Example A — Validation failure never activates

1. Tenant submits ChatGPT Web Access material missing required token shape.
2. Validation fails → state stays `draft`.
3. List accounts shows non-active; routing candidate set excludes it.
4. Chat request with explicit affinity to that id → **403-class** (owned but not usable) or **404** if never listed — MVP: account exists so **403-class** `account_not_usable` with remediation `submit_credential`.

#### Example B — Probe failure never activates

1. Material validates locally; probe returns 401.
2. State → `reauth_required`, health `auth_expired`.
3. No Capability Snapshot is published as “verified usable”.
4. Account remains non-usable until reauth + probe success.

#### Example C — Refresh failure forces reauth

1. Codex OAuth account is `active`.
2. Silent refresh hits `refresh_token_reused`.
3. State → `reauth_required`; in-flight may fail auth; new admissions exclude account.
4. Tenant completes device login again → validate → probe → `active`.

#### Example D — Gated mode without ack

1. Deployment enables ChatGPT Codex OAuth flag.
2. Tenant starts connect without risk acknowledgement.
3. Credential MUST NOT become usable; activation blocked; remediation `ack_risk`.

#### Example E — Prohibited mode

1. Client tries to create Grok Web SSO account in product.
2. Create fails closed `auth_mode_unavailable`.
3. No vault write; no probe; no catalog entry.

---

## 6. Operational health (orthogonal to lifecycle)

### 6.1 Health vocabulary (MVP)

| Health | Meaning | Typical effect on usable `active` account |
|---|---|---|
| `healthy` | Recent success; no blocking signal | Routable |
| `degraded` | Partial issues (elevated errors, partial capability) | May remain routable with caution (#11) |
| `auth_expired` | Credential auth class failure | **Non-routable**; drive `reauth_required` |
| `challenged` | Bot/challenge interstitial class | **Non-routable** until cleared; no productized solver (#7) |
| `quota_exhausted` | Provider quota exhausted with known reset | Temporarily non-routable for affected ops (#11/#17) |
| `rate_limited` | Transient provider rate limit | Short backoff; not reauth |
| `protocol_drift` | Unexpected upstream protocol | Degrade/disable path; operator |
| `provider_banned` | Permanent ban / provider-revoked account signal | **Non-routable**; often disable + incident |
| `unknown` | No successful classification yet | Not sufficient alone for first-time usable |

### 6.2 Interaction rules

1. Health updates **MUST NOT** by themselves set `lifecycle_state=active`.
2. Auth-class health (`auth_expired`, many `challenged` cases, `provider_banned`) **MUST** clear usability and SHOULD transition lifecycle toward `reauth_required` or `disabled`.
3. Quota/rate health does **not** require reauth.
4. Exact cooldown timers and circuit breakers are #17; this document only locks the **classes** and that they cannot override #6/#7/# usability gates.

---

## 7. Remediation classes (Tenant-facing)

Returned as safe guidance (exact error code strings #16):

| Remediation class | When | Tenant action |
|---|---|---|
| `submit_credential` | draft / validation failed first connect | Submit correct material for Auth Mode |
| `complete_oauth` | device/browser flow pending | Complete provider consent |
| `reauthenticate` | `reauth_required` / auth_expired | Run reauth journey |
| `ack_risk` | gated/experimental ack missing or stale | Accept risk themes (#7 §6.2) |
| `enable_account` | disabled by Tenant | Enable + pass probe if required |
| `wait_provider_cooldown` | quota/rate health | Wait until reset hint |
| `contact_operator` | protocol_drift, auth mode killed, banned cluster | Operator / support safe diagnostics |
| `auth_mode_unavailable` | prohibited / flag off / non-lab experimental | Choose another Auth Mode |
| `delete_and_recreate` | Auth Mode mismatch / irrecoverable row | Delete account; create new with correct mode |

Responses carrying remediation MUST still obey redaction (§9).

---

## 8. Per-Auth-Mode credential classes and lifecycle differences

**Hard rule:** Web Access and OAuth/CLI Access lifecycles **MUST NOT** be mixed on one Provider Account. Tables below are product-normative **classes**, not guarantees of upstream stability (research remains `conditionally supported` unless noted).

### 8.1 Summary matrix

| Auth Mode | Risk status (#7) | Access class | Primary credential class | Silent refresh | Human reauth | Product connectable MVP |
|---|---|---|---|---|---|---|
| ChatGPT Web Access | experimental | Web | `chatgpt_web_access_token` (+ optional refresh/fingerprint) | Optional only if refresh_token present; no rt_token dependency | Re-paste / re-import token | Lab only |
| ChatGPT Codex OAuth | gated | OAuth/CLI | `chatgpt_codex_oauth_bundle` | Yes (`refresh_token`) | Browser or device OAuth | Flag + Tenant ack |
| Gemini Web Cookie | experimental | Web | `gemini_web_cookie_jar` (PSID/PSIDTS + derived session fields) | Companion cookie rotation MAY; primary cookie invalid → reauth | Re-export cookies | Lab only |
| Gemini Antigravity OAuth | gated | OAuth/CLI | `gemini_antigravity_oauth_bundle` | Yes | Browser OAuth restart | Flag + Tenant ack |
| Grok Web SSO | **prohibited** | Web | `grok_web_sso_token` (not productized) | No | N/A in product | **No** |
| Grok xAI OAuth | gated | OAuth/CLI | `grok_xai_oauth_bundle` | Yes | Device/browser OAuth | Flag + Tenant ack |

### 8.2 ChatGPT Web Access

| Topic | Lock |
|---|---|
| Credential class | Web access token material; optional refresh_token; optional non-secret fingerprint metadata stored as account hints only when required by Adapter |
| Submission | Direct material submit in lab connection UX |
| Validation | Token shape/presence; reject empty |
| Required probe | Authenticated web identity/session check + minimal entitlement touch |
| Refresh | If refresh_token present, OAuth token endpoint refresh with **Web-associated client id class** only — MUST NOT use Codex client_id (research: distinct client ids) |
| Reauth | New access token import; probe again |
| Challenge | Sentinel/PoW/Turnstile/CF may yield `challenged`; product MUST NOT ship challenge-solver as a feature (#7) |
| Usable bar | Lab profile + experimental enable + validation + probe |

### 8.3 ChatGPT Codex OAuth

| Topic | Lock |
|---|---|
| Credential class | OAuth bundle: access_token, refresh_token, optional id_token, account_id |
| Submission | Browser or device OAuth; API-key alternative is a **different credential class** and is **not** silently aliased as Codex OAuth without an explicit product decision (research notes official API-key login exists; MVP Auth Mode name remains Codex OAuth) |
| Validation | Issuer allowlist `auth.openai.com` family; token exchange success; account_id present when required by surface |
| Required probe | Authenticated Codex surface request with Bearer + account header requirements |
| Refresh | `grant_type=refresh_token`; singleflight; detect permanent reuse/revoke → `reauth_required` |
| Reauth | Full OAuth restart; do not fall back to Web Access |
| Gates | Operator flag + Tenant risk ack before usable |

### 8.4 Gemini Web Cookie

| Topic | Lock |
|---|---|
| Credential class | Primary Google session cookies (`__Secure-1PSID`, `__Secure-1PSIDTS`, …) as vault material; derived short-lived session fields MAY be re-scraped and stored as secondary vault fields or ephemeral worker cache with TTL |
| Submission | Controlled paste/import of cookie material in lab UX only |
| Validation | Required cookie names present; size bounds; sanitization |
| Required probe | Init session success (derived token present, not sign-in HTML) |
| Refresh | Companion rotation endpoints MAY run as silent maintenance; HTTP 401/403 on rotation → `reauth_required` |
| Reauth | Human re-export of cookies |
| Security note | Cookie custody ≈ broad Google session blast radius; experimental only |

### 8.5 Gemini Antigravity OAuth

| Topic | Lock |
|---|---|
| Credential class | Google OAuth access + refresh tokens + bound `project_id` / companion project metadata (project id is non-secret account field; tokens are vault) |
| Submission | Browser OAuth with offline access |
| Validation | Google token endpoint success; required scopes present as configured; project bind success path |
| Required probe | `loadCodeAssist` / equivalent onboard+project path success |
| Refresh | refresh_token grant; ~lead-time refresh allowed; invalid_grant → `reauth_required` |
| Reauth | OAuth restart; project metadata re-resolved |
| Gates | Flag + Tenant risk ack |
| MUST NOT | Treat as Gemini Web Cookie; MUST NOT use consumer cookie jar on Antigravity account |

### 8.6 Grok Web SSO

| Topic | Lock |
|---|---|
| Product status | **`prohibited`** (#7) |
| Connection UX | MUST NOT appear in product catalogs |
| API create | MUST fail closed |
| Credential lifecycle | Not offered; research-only notes remain in #5 for reopen history |
| Silent conversion to Build OAuth | Forbidden as automatic recovery; would be a **separate** explicit Grok xAI OAuth connection if ever allowed by product |

### 8.7 Grok xAI OAuth

| Topic | Lock |
|---|---|
| Credential class | OAuth access + refresh (+ optional id_token); record bound base-URL family as non-secret account metadata (`cli-chat-proxy` vs `api.x.ai` usage policy) |
| Submission | Device or browser OAuth against allowlisted `x.ai` issuer endpoints |
| Validation | OIDC/device/token host allowlist; token exchange success |
| Required probe | Authenticated call on the bound surface family |
| Refresh | refresh_token; singleflight; permanent unauthorized → `reauth_required` |
| Reauth | OAuth restart; MUST NOT use Web SSO |
| Gates | Flag + Tenant risk ack |

### 8.8 Cross-mode independence tests (normative intent)

1. Two accounts (Web + OAuth) for the “same” human under one Tenant remain two ids, two credentials, two healths, two snapshots.
2. Refresh code paths MUST use the Auth Mode’s client/issuer parameters; cross-wiring client_ids is a defect.
3. Routing MUST NOT failover Web↔OAuth unless Tenant policy **explicitly** lists both Auth Modes and both are product-enabled (#7/#11).
4. Deleting the OAuth account MUST NOT vault-delete the Web account credential.

---

## 9. Redaction and non-disclosure of Provider Credential

### 9.1 Never leave the vault boundary

**MUST NOT** appear in:

- Public API success or error bodies (including create/submit/reauth responses)
- Management list/get account responses
- Capability Snapshot payloads
- Logs, stdout, metrics labels, tracing attributes
- Support transcripts and operator UI fields beyond “secret present / version / expiry hint”
- Audit event payloads
- OpenAPI examples and fixtures (use placeholders only)
- Exception messages

### 9.2 What MAY be visible to the owning Tenant

- `provider_account_id`, `auth_mode`, `provider`, `label`
- `lifecycle_state`, `operational_health`, remediation class
- `credential_version`, `refresh_supported`, `credential_expires_at` (hint)
- Masked `external_subject_hint`, `plan_type_hint`
- `last_*_at` timestamps
- Risk ack status (accepted version / timestamp)
- Safe error diagnostics the Tenant already needs to remediate

### 9.3 Transport rules for submission

1. Secrets only via TLS management endpoints designed for credential intake (or OAuth redirect/device channels).
2. MUST NOT accept Provider Credential material as query-string parameters.
3. MUST NOT echo submitted material in responses or “debug” headers.
4. Intake buffers MUST be memory-short-lived; durable form is vault ciphertext only.

### 9.4 Operator metadata

Operators (MVP default deny cross-Tenant, #6) may see the same **safe** fields a Tenant sees for that Tenant under future break-glass — still **no** plaintext Provider Credential. OP-G3 from #7 is binding.

### 9.5 Test fixtures

Fixtures use synthetic secrets clearly fake; CI logs MUST apply the same redaction filters as production.

---

## 10. Authorization surface for connection APIs

### 10.1 Scope mapping (#8)

| Operation | Minimum scope |
|---|---|
| List/get own accounts, safe health | `accounts.read` |
| Create, submit, reauth, probe-now, disable, enable, revoke credential, delete | `accounts.manage` |
| Use account for inference | Inference scopes (`chat.*` / `images.*`) + usable account; not `accounts.manage` |

Default inference keys **exclude** `accounts.manage` (#8) so a leaked inference key cannot reconnect Provider Accounts.

### 10.2 Ownership and non-enumeration

All account ids obey #6:

- Foreign id → **404-class**, zero vault decrypt, zero Adapter call
- Same-Tenant insufficient scope → **403-class**
- Unauthenticated → **401**

### 10.3 Management vs inference

Connection journeys are **management** operations. They are not Client API Key admission A6 “execution accepts” for Provider inference, but they MAY:

- decrypt credentials
- call upstream probe endpoints
- write vault material

Those side effects are allowed **only** on authorized same-Tenant management paths and system jobs for that Tenant. They still MUST NOT run for `prohibited` Auth Modes in product composition.

---

## 11. Risk-status-driven connection UX (#7 feed)

| Status | Catalog | Ack | Activation |
|---|---|---|---|
| `allowed` | Self-serve | Standard security notices | validate+probe |
| `gated` | Self-serve only if operator flag on | **Required** residual-risk ack themes (#7 §6.2) before usable | validate+probe after ack |
| `experimental` | Lab consoles only | Strong research/ToS/ban warning | validate+probe; never ordinary production catalog |
| `prohibited` | Absent | N/A | Create fails closed |

Ack themes (normative content, not final copywriting):

1. Authorized to use the Provider Account
2. Provider may suspend for ToS/AUP
3. PixelPlus stores Provider Credential to act inside this Tenant only
4. Sibling Web/OAuth modes are separate and may be unavailable

---

## 12. System jobs related to lifecycle

| Job | Purpose | Constraints |
|---|---|---|
| Silent refresh worker | Renew OAuth/companion credentials | Same-Tenant; singleflight; no cross-mode |
| Probe / health worker | Optional background re-probe | Must not mark usable without §4.2; cost-minimal |
| Kill-switch reconciler | Apply #7 OP-G1/KS-* | Stop new connections/executions; durable flag |
| Delete purge worker | Hard-delete tombstones after retention | #15 retention; no secret resurrection |

Workers act only with the resource’s `tenant_id` (#6).

---

## 13. Security impact summary

| Defect | Impact |
|---|---|
| Activate without probe | Routing to dead/invalid accounts; wasted quota; false health |
| Activate without validation | Malformed secrets in vault; unpredictable Adapter failures |
| Mix Web and OAuth on one account | Wrong refresh client_id; wrong challenge handling; capability lies |
| Silent cross-mode fallback | Violates #7; may hit `prohibited`/`experimental` surfaces |
| Return credential in API/logs | Full Provider account takeover |
| Cross-Tenant account id oracle | Enumeration / targeted attacks (#6) |
| Refresh without singleflight | refresh_token reuse invalidation (Codex/xAI research) |
| Connect `prohibited` mode | Policy defect; AUP collision (Grok Web) |
| Skip gated ack | Uninformed residual risk acceptance |
| Leaked inference key with `accounts.manage` default | Lateral reconnect / secret overwrite — prevented by #8 defaults |

---

## 14. Test obligations

Exact harness arrives with contract prototypes (#18–#20). Required observable cases for this issue:

### 14.1 Journey and usability

1. Create → submit invalid material → validate fail → state not `active`; not in routing candidates.
2. Create → submit valid shape → probe auth-fail → not `active`; remediation `reauthenticate` or `submit_credential`.
3. Create → validate OK → probe OK → `active` and usable only if risk gates pass.
4. Explicit affinity to non-usable own account → **403-class** with remediation; Adapter executions = 0.
5. Foreign account id → **404-class**; vault decrypt = 0.

### 14.2 Refresh and reauth

6. OAuth account silent refresh success keeps `active`; version/expiry metadata updates; secrets not returned.
7. Permanent refresh failure → `reauth_required`; usable=false.
8. Reauth success on same `provider_account_id` returns to `active` only after probe; old credential version unusable for new work.
9. Reauth MUST NOT change `auth_mode` or `tenant_id`.

### 14.3 Disable, revoke, delete

10. Disable → usable=false; enable without credential/probe rules respected.
11. Revoke → decrypt denied; state not `active`; requires reauth to recover.
12. Delete → subsequent get is not-found; credential material not recoverable via API.

### 14.4 Auth Mode separation and risk gates

13. Product create Grok Web SSO → fail closed; no vault write.
14. Gated mode without operator flag or without ack → never usable.
15. Experimental mode absent from production catalog fixtures.
16. Two accounts Web+OAuth same Tenant remain independent on delete/refresh/fail.
17. Refresh path using wrong mode client parameters is a conformance fail (Adapter tests).

### 14.5 Redaction

18. Submit/reauth responses, list/get, logs, and audit events for the above cases contain **no** access tokens, refresh tokens, cookies, or SSO tokens.
19. Error bodies for auth failures do not include raw upstream secret material.

### 14.6 Scope

20. Key with only `accounts.read` cannot submit/disable/delete.
21. Key with default inference grant cannot manage accounts (#8).

---

## 15. Core invariants (normative checklist)

1. **I-ACCOUNT-TENANT** — Every Provider Account has exactly one immutable `tenant_id`.
2. **I-ACCOUNT-AUTHMODE** — Every Provider Account has exactly one immutable `auth_mode`.
3. **I-CREDENTIAL-BIND** — As locked in #6: a Provider Credential is usable only for its attached Provider Account and only on same-Tenant authorized paths; never shared or retargeted across Tenants. This document does not redefine that id.
4. **I-CREDENTIAL-AUTHMODE-BIND** — Provider Credential `auth_mode` MUST match its Provider Account `auth_mode`; material MUST NOT be retargeted across Auth Modes or accounts.
5. **I-USABLE-GATE** — Authoritative definition in §5.1: `active` + Auth Mode execution-enabled (incl. risk ack) + not hard-blocked health + validation+probe success for current credential version + not vault-revoked + request-time scope/allowlist (when applicable) + request-time capability (when capability-bearing).
6. **I-NO-ACTIVE-ON-FAIL** — Validation failure or required probe failure MUST NOT yield usable/active success.
7. **I-NO-WEB-OAUTH-MIX** — Web Access and OAuth/CLI Access lifecycles never share one Provider Account or silent recovery path.
8. **I-REFRESH-MODE-CORRECT** — Silent refresh uses only the Auth Mode’s issuer/client/credential class; singleflight per account; success bumps `credential_version` and stays `active` when already `active`.
9. **I-REAUTH-SAME-ID** — Reauth preserves `provider_account_id`, Tenant, and Auth Mode; valid from `reauth_required` and `revoked` (and planned rotate from `active`/`disabled`).
10. **I-DISABLE-NONUSE** — Disabled accounts are non-usable for new execution.
11. **I-REVOKE-NONUSE** — Revoked credentials cannot decrypt for execution; account not usable until new version activates via reauth.
12. **I-DELETE-TERMINAL** — Deleted accounts are not-found for product APIs; credentials shredded/deleted per vault policy.
13. **I-RISK-GATE** — `prohibited` unconnectable; `gated` needs flag+`ack_risk`; `experimental` lab-only.
14. **I-REDACT-CREDENTIAL** — Provider Credential material never appears in responses, logs, metrics, audit, or operator metadata.
15. **I-NON-ENUM-ACCOUNT** — Foreign Provider Account ids yield 404-class non-enumeration (#6).
16. **I-SCOPE-MANAGE** — Connection mutations require `accounts.manage`; default inference keys lack it.
17. **I-NO-CHALLENGE-SOLVER-PRODUCT** — Challenge health does not authorize shipping anti-bot bypass product features (#7).
18. **I-PROBE-MINIMAL** — Required probes are auth-proving and cost-minimal; not full billable renders.
19. **I-FAIL-CLOSED-KILL** — Auth Mode kill/feature gate stops new connections and executions for that mode; reconciler MUST move former `active` accounts to `disabled` or `reauth_required` per §4.2 rule 6.

---

## 16. Open follow-ups (explicitly deferred)

| Topic | Issue | Constraint retained here |
|---|---|---|
| Capability Snapshot schema/TTL/invalidation | #10 | Activation/re-probe may mint/refresh snapshots; cannot mark usable without probe |
| Routing candidate filters, leases, fallback | #11 | Only usable same-Tenant accounts; no silent cross-mode |
| Chat cancel on account disable/delete | #12 | MUST attempt cancel for cancelable work |
| Asset retention unrelated to account delete | #13 | Account delete does not redefine asset ownership rules |
| Render Job interaction with account loss mid-job | #14 | Job fails safely; no cross-Tenant recovery |
| Vault crypto, KMS, shred, retention holds | #15 | Write/read/delete moments + redaction here |
| Exact error code strings / problem+json | #16 | Classes + remediation vocabulary here |
| Cooldown timers, probe schedules, operator UI | #17 | Health classes + kill interaction here |
| OpenAPI paths for account management | #18 / #20 | Behavior and status classes here |
| Whether Codex API-key login is a first-class Auth Mode | reopen `D-CODEX-APIKEY-MODE` | Not silently merged into Codex OAuth |
| Dual-valid credential grace during reauth longer than cutover | reopen `D-REAUTH-GRACE` | MVP cutover on new version activation |
| Numeric probe rate limits per Tenant | reopen `D-PROBE-RATE` | Probes still cost-minimal and non-hammering |

---

## 17. ADR decision

No new ADR. Provider Account / Credential ownership and BYOA were product-locked in parent #1 and #6; risk status in #7. This document is the durable normative expansion under `docs/spec/` for connection journeys and credential lifecycle state machines.

An ADR **would** be warranted if product later introduced:

- shared Provider Account pools across Tenants (forbidden),
- automatic Web↔OAuth failover as default,
- storing Provider Credential plaintext in account tables,
- or promoting a `prohibited` Auth Mode without #7 reopen.

---

## 18. Acceptance criteria traceability

| AC (issue #9) | Where satisfied |
|---|---|
| Create, credential submission, validation, probe, activation, refresh, reauthentication, disable, revoke and delete all have clear transitions | §4 entire, §4.13 matrix, §14.1–§14.3, §15 |
| Account does not become usable when credential validation or required probe fails | §4.2 I-USABLE-GATE, §4.5–§4.6, §5, §5.3 A/B, §14.1, I-NO-ACTIVE-ON-FAIL |
| Differences among six Auth Modes recorded without mixing Web and OAuth/CLI lifecycles | §1.5, §8, §14.4, I-NO-WEB-OAUTH-MIX, I-REFRESH-MODE-CORRECT |
| Responses and operator metadata do not leak Provider Credential | §3.1–§3.3, §9, §14.5, I-REDACT-CREDENTIAL, #7 OP-G3 |

---

## 19. Constants and reopen ids

| Id | Meaning |
|---|---|
| `I-USABLE-GATE` | Authoritative usability conjunction in §5.1 (activation subset in §4.2) |
| `I-CREDENTIAL-AUTHMODE-BIND` | Credential cannot cross Auth Modes/accounts (§15) |
| `D-CODEX-APIKEY-MODE` | Reopen if API-key Codex login becomes its own Auth Mode |
| `D-REAUTH-GRACE` | Reopen if long dual-valid reauth window desired |
| `D-PROBE-RATE` | Reopen for numeric per-Tenant probe budgets |
| Risk statuses | Owned by #7 (`allowed`/`gated`/`experimental`/`prohibited`) |
| Kill/feature signals | Owned by #7 (`KS-*`, `FG-*`, `OP-G1`, reopen R0–R4) |

---

## 20. Document control

| Field | Value |
|---|---|
| Status | Accepted for specification (issue #9) |
| Check date of evidence inputs | 2026-07-14 |
| Supersedes | n/a (initial connection/credential lifecycle lock) |
| Next review | On #7 status changes, #15 vault design, or any Auth Mode credential-class break (KS-6) |
| Authors | Spec decision agent for issue #9 |
