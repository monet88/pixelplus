# Client API Key Lifecycle and Admission Controls

- Status: Accepted for specification (issue #8)
- Date: 2026-07-14
- Parent: [#1](https://github.com/monet88/pixelplus/issues/1)
- Issue: [#8](https://github.com/monet88/pixelplus/issues/8)
- Vocabulary source: `CONTEXT.md`
- Related ownership invariants: `docs/spec/tenant-ownership-authorization-invariants.md` (#6)
- Related risk envelope: `docs/spec/auth-mode-risk-envelope-and-kill-criteria.md` (#7)

## 1. Scope and non-goals

### 1.1 Scope

This specification locks how a Tenant **issues and controls Client API Keys** and how the Public API **admits or rejects** a request **before** execution work is accepted.

It covers:

1. Client API Key material format and one-time display
2. Hashing and durable storage semantics
3. Authentication that produces a Security Principal
4. Scope / least-privilege narrowing inside a Tenant
5. Rotate and revoke lifecycle, including immediate revocation at the Public API boundary
6. Admission controls: rate limit, concurrency, quota, request-size limit
7. Abuse controls for unauthenticated and authenticated misuse
8. The hard boundary between **admission rejection** and **execution / runtime failure**

It codifies parent #1 user stories 1, 8–9, 20, 61 and the testing decision that *client key revocation, request limits and abuse controls have effect at the Public API boundary*.

### 1.2 Non-goals

This document does **not**:

- Implement Gateway, storage, or management UI code.
- Design Provider Account connection journeys (#9).
- Design Capability Snapshot schema (#10), routing algorithms (#11), chat stream internals (#12), asset retention (#13), Render Job state machine (#14), vault cryptography for Provider Credential (#15), full canonical error **code names** (#16), or operator break-glass (#17).
- Freeze exact OpenAPI path strings and response schemas (#18 / #20).
- Design commercial metering, invoices, or payment capture (parent #1 Out of Scope). Product **anti-abuse quota** here is not billing.
- Design the full Tenant bootstrap / first-admin identity product (only the rules keys must obey once a Tenant exists).
- Authorize open/anonymous Public API inference.

Downstream issues **MUST** preserve every decision here. They may tighten limits or add scopes; they MUST NOT weaken hashing, revocation, Tenant isolation, or the admission-before-execution boundary.

### 1.3 Normative language

- **MUST / MUST NOT / REQUIRED**: product/security policy. Violation is a defect.
- **SHALL**: same force as MUST for observable Public API behavior.
- **SHOULD**: strongly preferred default; deviation needs an operator-recorded exception.
- **MAY**: optional surface that cannot weaken MUST rules.

### 1.4 Relationship to #6

| Topic | #6 already locked | This document adds |
|---|---|---|
| Ownership of Client API Key | Exactly one Tenant; immutable `tenant_id` | How key material is issued, stored, rotated, revoked |
| Security Principal | `(tenant_id, client_api_key_id)` from one key | Exact authn pipeline and failure classes for material |
| Revoke | Removes ability to form a principal | Propagation budget, cache bound, residual in-flight window |
| Scope | May only narrow inside Tenant | Concrete scope dimensions and default grant |
| Quota / admission counters | Tenant-scoped; optional same-Tenant subdivisions | Limit hierarchy, check order, debit rules, defaults |
| Foreign ids | 404-class non-enumeration | Unchanged; management APIs obey the same rules |

---

## 2. Glossary extensions (normative use)

| Term | Meaning in this document |
|---|---|
| **Client API Key material** | The full bearer secret string presented by a client. Shown **once** at create/rotate. Never returned again by list/get. |
| **`client_api_key_id`** | Stable non-secret identifier of the key record. Safe for logs, metrics, audit, and management list responses. |
| **Key public locator** | The non-secret segment embedded in material used for indexed lookup (see §3.1). Maps 1:1 to `client_api_key_id` (they MAY be identical strings). |
| **Secret segment** | High-entropy random bytes encoded in the material. Never logged. |
| **Secret hash** | Durable verifier stored for the secret segment. Not reversible to the secret. |
| **Admission** | The ordered checks after transport accept and before the request is accepted for execution (Adapter call, durable job enqueue, or other side-effecting work). |
| **Admission rejection** | A failure that prevents acceptance for execution. No execution side effects beyond counters defined in §7. |
| **Execution / runtime failure** | A failure after the request was admitted. May involve Provider, worker, stream, or job lifecycle (#12, #14, #16, #17). |
| **RevocationPropagationBudget** | Maximum wall-clock age of a positive “this key is active” auth decision cache. Default: **5 seconds** (constant `R-REVOKE-PROP`). |
| **Limit hierarchy** | `effective = min(platform_cap, tenant_limit, key_limit_if_set)` for each limit dimension. |

---

## 3. Material format, hashing, storage

### 3.1 Material format

Client API Key material MUST be a single bearer string, presented as:

```http
Authorization: Bearer <client_api_key_material>
```

Normative structure:

```text
sk-pxp_<public_locator>_<secret>
```

| Segment | Rule |
|---|---|
| `sk-pxp_` | Fixed product prefix. Distinguishes PixelPlus Client API Keys from arbitrary tokens. |
| `<public_locator>` | Non-secret, URL-safe, high-entropy enough to be unguessable as an id (minimum 96 bits entropy recommended). Used only for indexed load of the key row. |
| `<secret>` | CSPRNG secret, minimum **256 bits** entropy before encoding. |

Cause → effect:

1. Client sends only the full material.
2. Gateway parses prefix + public locator; malformed prefix/shape → **401** (no principal).
3. Gateway loads the key row by public locator (or by `client_api_key_id` if identical).
4. Gateway verifies the secret segment against the stored secret hash with a **constant-time** compare of the derived verifier.
5. Only on success does a Security Principal exist.

**MUST NOT:**

- Accept material without the product prefix as a PixelPlus Client API Key.
- Authenticate on public locator alone.
- Put full material, secret segment, or secret hash into Public API success bodies after the one-time create/rotate response, logs, metrics labels, or support transcripts.

Query-string or body transport of the material is **MUST NOT** for Public API inference endpoints. Management tools that accidentally log `Authorization` headers MUST redact.

### 3.2 Hashing algorithm (locked default)

API key secrets are high-entropy. Offline brute-force of a 256-bit secret is not a realistic threat; online auth latency and constant-time verify are.

**Locked default (`H-KEY-HMAC`):**

- `secret_hash = HMAC-SHA-256(server_pepper, secret_segment_bytes)`
- `server_pepper` is a platform secret held in the same class of secret store as vault master material (#15 expands KMS/HSM). Pepper is **not** Tenant-specific in MVP.
- Verify: recompute HMAC and compare in constant time.
- Store alongside `hash_version` so pepper/algorithm rotation is possible.

**Pepper compromise impact:** with a **256-bit CSPRNG secret**, offline brute-force recovery of the secret from `secret_hash` remains computationally infeasible even if both the hash table and pepper leak. Pepper is still defense-in-depth: it prevents an attacker who only obtains hashes (no pepper) from running offline HMAC checks against guessed or recycled low-entropy material, supports hash-version rotation, and keeps verify tied to platform secret custody. Mitigation on suspected pepper leak: rotate pepper (`hash_version`), dual-pepper verify window, hash-table access control, and operator revoke-all runbook (not coded here).

**Rejected for Client API Key secrets:** password-style interactive KDF as the *only* verifier when it would make legitimate RPM targets impractical. Implementations MAY add a memory-hard KDF **in addition** only if they still meet admission latency budgets; they MUST NOT replace constant-time verify with early-exit compares.

**MUST NOT** store:

- Plaintext secret segment after the create/rotate response is generated
- Reversible encryption of the secret “so we can show it again”
- Truncated hashes that allow forgery

### 3.3 Durable key record (logical fields)

| Field | Required | Notes |
|---|---|---|
| `tenant_id` | yes | Immutable (#6) |
| `client_api_key_id` | yes | Stable id; safe to expose |
| `public_locator` | yes | Indexed lookup; unique platform-wide |
| `secret_hash` | yes while active | Cleared or rotated on rotate |
| `hash_version` | yes | Algorithm/pepper generation |
| `status` | yes | `active` \| `revoked` |
| `scopes` | yes | See §5; empty set is invalid — use explicit default grant |
| `model_allowlist` | no | Empty / null = all models otherwise allowed for Tenant |
| `provider_account_allowlist` | no | Empty / null = all same-Tenant accounts allowed by routing |
| `limit_overrides` | no | Optional per-key ceilings (§7.2) |
| `label` | no | Tenant-visible name |
| `created_at` | yes | |
| `revoked_at` | when revoked | |
| `last_used_at` | optional | Update MUST NOT block auth hot path correctness if write fails |

No field may reassign `tenant_id`.

### 3.4 One-time display

| Event | Material returned? | Metadata returned? |
|---|---|---|
| Create success | **Yes, exactly once** in that response | Yes |
| Rotate success | **Yes, new material exactly once** | Yes |
| List keys | **No** | Yes (id, label, status, scopes, created_at, revoked_at, last_used_at, limit summary) |
| Get key | **No** | Yes |
| Authenticate | **No** (request already holds material) | N/A |

If the create/rotate response is lost by the client, the Tenant MUST rotate or create a new key; Gateway MUST NOT re-display historical material.

---

## 4. Lifecycle operations

### 4.1 States

```text
                 create
                   │
                   ▼
               ┌────────┐
               │ active │◄── rotate (same id, new secret; still active)
               └───┬────┘
                   │ revoke
                   ▼
               ┌─────────┐
               │ revoked │  (terminal for authentication)
               └─────────┘
```

- **`active`**: material may authenticate; subject to scope and admission.
- **`revoked`**: material MUST NOT authenticate. Metadata MAY remain for audit until hard-delete under data lifecycle (#15).

There is **no** `suspended` state in MVP. Temporary disable = revoke (and create a replacement) or rely on Tenant-level kill / limit zeroing.

Hard-delete of a revoked row is allowed later for retention; auth behavior equals unknown key → **401**.

### 4.2 Create

**Who:** a Tenant management principal authorized for `keys.manage` (see §5.3 and §4.7).

**Effect:**

1. Stamp immutable `tenant_id` of the managing Tenant only.
2. Generate `client_api_key_id`, `public_locator`, secret segment.
3. Persist hash and metadata with `status=active`.
4. Return one-time material + metadata.
5. Emit audit event: `client_api_key.created` with `tenant_id`, `client_api_key_id`, actor, scopes — **without** secret.

**MUST NOT** create a key under another Tenant’s id, even if the caller “knows” that id.

### 4.3 Authenticate (Public API)

Ordered pipeline (normative):

1. **Extract** bearer material. Missing/malformed header → **401**.
2. **Parse** product prefix and public locator. Failure → **401**.
3. **Load** key record by public locator.
4. If no row **or** `status != active` **or** hash verify fails → **401**.  
   These three cases MUST be **observably indistinguishable** to the client (same status class and non-differentiating body shape). Timing SHOULD be mitigated toward constant work for known locators; unknown locator SHOULD use a dummy verify.
5. Build Security Principal `(tenant_id, client_api_key_id)`.
6. Continue to **scope** then **admission** (§6–§7).

Client-supplied `tenant_id` headers/body fields MUST be ignored for principal formation (#6 I-PRINCIPAL).

### 4.4 Rotate

**Definition (locked):** rotate replaces the secret segment of an **existing** `client_api_key_id` while keeping the same Tenant ownership, scopes (unless also updated), and id for audit continuity.

**Rules:**

1. Only the owning Tenant’s `keys.manage` principal may rotate.
2. Generate new secret; store new hash; **immediately** invalidate previous secret material (no dual-valid grace window in MVP).
3. Return new material **once**.
4. Old material MUST fail auth as **401** within `RevocationPropagationBudget` (same bound as revoke).
5. Audit: `client_api_key.rotated` with id + actor; no secrets.

**Why no grace window:** parent #1 requires immediate revoke semantics for leaked credentials. A dual-valid rotate window re-introduces leak dwell time. If product later needs grace, that is an explicit reopen (`D-ROTATE-GRACE`), not a silent default.

Alternative product flow “create new key + revoke old” remains valid and is composition of §4.2 + §4.5; it yields a **new** `client_api_key_id`.

### 4.5 Revoke

**Definition:** set `status=revoked`, set `revoked_at`, prevent any new Security Principal from that material.

**Rules:**

1. Only owning Tenant `keys.manage` (or platform emergency path outside MVP Public API).
2. Revoke is idempotent: revoking an already-revoked key is success without re-enabling.
3. After successful revoke acknowledgement, **new** Public API requests using that material MUST receive **401** within **`RevocationPropagationBudget` (`R-REVOKE-PROP` = 5s)**.
4. Positive auth caches (“key active / principal X”) MUST NOT outlive `R-REVOKE-PROP`. Unbounded TTL caches of active principals are a **security defect**.
5. Source of truth is the durable key record (or a revocation log / version vector derived from it). Replica lag MUST be designed so the observable budget is still met at the Public API edge, or the edge MUST fail closed when revocation state is unavailable (§7.6).

**In-flight residual window (honest bound):**

- Revoke does **not** by itself prove that already-admitted work has finished.
- Implementations **MUST attempt** to cancel cancelable in-flight work (chat streams, cancelable jobs) whose admitting `client_api_key_id` was revoked, releasing concurrency slots on cancel completion (#12 / #14 refine protocols and best-effort limits).
- Non-cancelable residual (for example an upstream render that cannot be aborted) MAY continue until natural terminal state; it MUST NOT mint a new principal or new A6 accept for the revoked material.
- Security claim of this issue: **no new admission** after budget; cancel-on-revoke is mandatory attempt, not a silent best-effort footnote.

### 4.6 List / get / update metadata

| Operation | Secret | Cross-Tenant id |
|---|---|---|
| List | never | only own Tenant’s keys |
| Get by id | never | foreign/unknown id → **404-class** (#6) |
| Update label / scopes / limit_overrides | never | foreign → **404-class**; same-Tenant insufficient scope → **403-class** |

Scope updates MUST only **narrow or restate** rights inside the Tenant. They MUST NOT grant cross-Tenant rights or platform admin rights.

### 4.7 Management authentication surface

MVP locks behavior, not full identity product design:

1. **Inference Public API** (OpenAI-compatible chat/images and related runtime): authenticated **only** by Client API Key material.
2. **Key management API** (create/list/rotate/revoke/update): requires a principal with `keys.manage` for that Tenant.
3. A Client API Key **MAY** be granted `keys.manage` (break-glass style automation). Default create grant **MUST NOT** include `keys.manage` unless explicitly requested (§5.2).
4. Bootstrap of the first management capability for a new Tenant is **platform provisioning** (out of band). Once any `keys.manage` principal exists, ordinary lifecycle applies.
5. Management APIs are still Tenant-scoped and subject to #6 non-enumeration.

---

## 5. Scope model (least privilege)

### 5.1 Dimensions

Scopes only **narrow** actions inside the owning Tenant (#6).

| Dimension | Type | Omitted / null means | Explicit empty set `[]` means |
|---|---|---|---|
| **operations** | set of operation ids | Invalid at rest; create MUST write an explicit set (default grant below) | Invalid at rest — a key MUST retain at least one operation or be revoked |
| **model_allowlist** | set of model ids | All models otherwise visible via Tenant Capability Snapshots | **Deny all** model-bearing operations (**403-class**), even if `chat.completions` / `images.*` remain in operations |
| **provider_account_allowlist** | set of same-Tenant Provider Account ids | All accounts eligible under Routing Policy | **Deny all** explicit affinity and implicit routing candidates for this key (**403-class** for same-Tenant policy deny; foreign ids still **404-class**) |

### 5.2 Operation ids (MVP vocabulary)

Stable ids for authorization checks (OpenAPI may map paths later in #18/#20):

| Operation id | Allows |
|---|---|
| `chat.completions` | Create chat completions (stream and non-stream) |
| `images.generate` | Image generation job create |
| `images.edit` | Edit / inpaint job create |
| `assets.read` | Read/list/download own Assets |
| `assets.write` | Upload/delete own Assets |
| `accounts.read` | List/get own Provider Accounts and safe health |
| `accounts.manage` | Connect/disable/reauth/delete own Provider Accounts (#9) |
| `capabilities.read` | Read Capability Snapshot / model list for own Tenant |
| `jobs.read` | Poll/list own Render Jobs / outputs metadata |
| `jobs.manage` | Cancel own jobs (create covered by images.*) |
| `keys.manage` | Create/list/rotate/revoke/update Client API Keys |
| `routing.read` | Read own Routing Policy |
| `routing.manage` | Update own Routing Policy (#11) |

### 5.3 Default grant on create

Unless the create request specifies a narrower set, new keys receive:

```text
chat.completions
images.generate
images.edit
assets.read
assets.write
accounts.read
capabilities.read
jobs.read
jobs.manage
routing.read
```

**Excluded by default:** `keys.manage`, `accounts.manage`, `routing.manage`.

Cause → effect: a leaked default inference key cannot mint new keys or reconnect Provider Accounts without a second compromise of a management principal.

### 5.4 Evaluation

After authn, for the target operation:

1. If operation id ∉ key.operations → **403-class** (same-Tenant forbidden).
2. **model_allowlist:**
   - omitted/null → no extra model filter beyond Capability Snapshot;
   - explicit `[]` → deny every model-bearing request (**403-class**);
   - non-empty → requested model MUST be ∈ list, else **403-class**.
3. **provider_account_allowlist:**
   - omitted/null → all same-Tenant accounts allowed by Routing Policy;
   - explicit `[]` → deny account selection for this key (**403-class**);
   - non-empty → explicit affinity account MUST be ∈ list, else **403-class** (same-Tenant policy deny, not existence oracle).
   If the account id is foreign/unknown to the Tenant, #6 **404-class** still wins (ownership check before or combined so foreign ids do not become 403 oracles).
4. Scopes never override ownership: a scope cannot authorize Tenant B resources.

### 5.5 Worked example — scope vs ownership

1. Tenant A key has `chat.completions` only.
2. Caller tries `images.generate` → **403-class** (owned Tenant, insufficient scope).
3. Same key tries chat with `provider_account_id` of Tenant B → **404-class** (non-enumeration), zero Adapter calls.
4. Same key tries chat with A’s account not on allowlist (allowlist non-empty) → **403-class**.

---

## 6. Admission pipeline (order is normative)

After a Security Principal is established, Gateway MUST evaluate admission in this order before accepting execution:

| Step | Check | Failure class | Execution side effects |
|---|---|---|---|
| A0 | Authn (principal) | **401** | None |
| A1 | Scope / allowlists / ownership on named ids | **403-class** same-Tenant policy deny; **404-class** foreign/unknown Tenant-scoped ids (#6) | None |
| A2 | Request-size limits | **413-class** for every size violation (known `Content-Length` over max, or buffered bytes over max on chunked/unknown length). Framing/syntax errors that are **not** size violations remain **400** and are not size outcomes. | Partial body discard only; authenticated size rejects SHOULD count toward Tenant RPM (§7.3) |
| A3 | Rate limit (RPM / burst) | **429-class** `rate_limit` | Counts toward rate window (§7.3) |
| A4 | Concurrency limit | **429-class** `concurrency_limit` | No slot held |
| A5 | Quota (anti-abuse units) | **429-class** `quota_exhausted` | No daily quota debit for reject (§7.5) |
| A6 | Accept for execution | — | Concurrency slot acquired; quota reservation rules apply |

**MUST NOT** call Provider Adapters, decrypt Provider Credentials, or enqueue durable Render Jobs before A6 success.

Unsupported capability denials that require Capability Snapshot inspection occur **after** A0–A5 when they need Tenant data, and still **before** Adapter invocation (parent #1). They are authorization/capability outcomes (#10), not rate limits; they MUST NOT be labeled as execution/runtime Provider failures.

---

## 7. Rate, concurrency, quota, request-size

### 7.1 Hierarchy and isolation

For each dimension:

```text
effective_limit(principal) =
  min(platform_cap, tenant_limit, key_limit_override_if_set)
```

- Counters are owned by `principal.tenant_id` and MAY subdivide by `client_api_key_id` (#6 I-QUOTA-SCOPE).
- Exhaustion for Tenant A MUST NOT alter Tenant B’s remaining capacity.
- Key overrides can only **lower** effective limits relative to Tenant/platform, never raise above Tenant ceiling.

### 7.2 Default product numbers (`D-NUMERIC-TUNE`)

These are **product-chosen conservative defaults**, reopenable without rewriting lifecycle semantics. Names are stable for tests and runbooks.

| Constant | Default | Dimension |
|---|---|---|
| `L-TENANT-RPM` | 60 requests / minute | Rate |
| `L-TENANT-BURST` | 20 | Rate burst token bucket |
| `L-TENANT-CHAT-CONCURRENCY` | 5 in-flight chat requests | Concurrency (Tenant ceiling) |
| `L-TENANT-JOB-CONCURRENCY` | 3 non-terminal active Render Jobs | Concurrency (Tenant ceiling) |
| `L-KEY-CHAT-CONCURRENCY` | inherit Tenant; if unset, effective key share still counts under Tenant ceiling | Concurrency (per-key subdivision; default partition by `client_api_key_id` for slot accounting) |
| `L-TENANT-REQ-DAY` | 10_000 requests / UTC day | Quota |
| `L-TENANT-CHAT-TOKEN-DAY` | 2_000_000 estimated tokens / UTC day | Quota |
| `L-TENANT-IMAGE-JOB-DAY` | 200 job creates / UTC day | Quota |
| `L-CHAT-MAX-TOKENS-PER-REQ` | 8_192 | Quota reservation cap per chat request |
| `L-JSON-BODY-MAX` | 2 MiB | Request-size |
| `L-ASSET-UPLOAD-MAX` | 20 MiB | Request-size |
| `R-REVOKE-PROP` | 5 seconds | Revocation cache bound |
| `L-AUTH-FAIL-IP-RPM` | 60 failed auth / minute / IP | Abuse |
| `L-AUTH-FAIL-IP-LOCATOR-RPM` | 20 failed auth / minute / `(source_ip, public_locator)` | Abuse (primary locator throttle) |

Per-key overrides default to **inherit** (no extra tightening). Platform caps MAY be equal to these defaults in single-tenant lab deploys.

### 7.3 Rate limit semantics

- Counts **requests that passed A0 (authenticated)** and reached the rate checker, including those later rejected for concurrency/quota/**size** (including authenticated **413-class** size rejects). Unauthenticated failures use abuse counters (§8), not Tenant RPM.
- If size is enforced before the rate counter is touched in the implementation hot path, the implementation MUST still attribute authenticated size rejects to the Tenant/key RPM window (or an equivalent authenticated abuse counter with the same effective cap).
- Algorithm: token bucket or sliding window is an implementation choice; observable MUST include a retryable **429-class** with stable error class `rate_limit` (#16 names the code string).
- OpenAI-compatible rate headers SHOULD be emitted when practical: limit, remaining, reset.
- Streaming chat: counts as **one** request at admission, not per SSE event.

### 7.4 Concurrency semantics

Two independent counter families (Tenant ceiling + per-key subdivision):

| Counter | Acquired | Released |
|---|---|---|
| Chat in-flight | A6 accept of chat completion | Accounting terminal: upstream completed or abort was confirmed; a client terminal/disconnect/cancel alone does not release occupancy while upstream survives (#12 §6.5) |
| Active Render Jobs | A6 accept of job create | Job reaches terminal state (completed / failed / canceled) per #14 |

Rules:

1. **Tenant ceiling** remains `L-TENANT-CHAT-CONCURRENCY` / `L-TENANT-JOB-CONCURRENCY`.
2. Implementations **MUST** account in-flight chat (and SHOULD account jobs) **per `client_api_key_id`** as well as under the Tenant ceiling so one leaked key cannot alone pin the entire Tenant chat concurrency budget by default. Effective admit requires both key subdivision and Tenant ceiling to have capacity (`effective` still respects §7.1 hierarchy).
3. A non-cancelable chat that has reached its client terminal but still runs upstream remains occupied under both the original Tenant ceiling and originating `client_api_key_id` subdivision until #12 accounting terminal. Moving its bookkeeping to a bounded residual state does not create a second concurrency pool or free capacity for another A6 accept.
4. Reject at A4 → **429-class** `concurrency_limit` (distinct class from `rate_limit` so clients can back off differently).
5. On revoke, cancel-on-revoke (§4.5) releases chat occupancy only when abort/completion confirms upstream stopped; a surviving non-cancelable execution follows #12 residual accounting without minting replacement capacity.

### 7.5 Quota semantics (anti-abuse, not billing)

MVP quota units:

1. **Requests / day** — +1 at A6 accept.
2. **Estimated chat tokens / day** — reserve at A6 using:

   ```text
   reserve = input_tokens_estimate + min(requested_max_tokens_or_product_default, L-CHAT-MAX-TOKENS-PER-REQ)
   ```

   - If the client supplies `max_tokens` (or equivalent) **above** `L-CHAT-MAX-TOKENS-PER-REQ`, Gateway MUST reject before reserve with **400-class** (invalid request parameter) or clamp only when product policy explicitly documents clamp; default is **reject**.
   - Reconcile after completion when actual usage known; never leave unbounded under-admission if counters are unavailable (§7.6).
3. **Image job creates / day** — +1 at A6 accept of job create. Output-placement retries that do **not** create a new upstream render MUST NOT consume another image-job quota unit (#14).

**Admission rejection (A1–A5)** MUST NOT debit daily request/token/job quota.

**Durable asset storage / count caps** (total bytes or object count retained for a Tenant) are **not** numbered here; #13 MUST define them. Until #13 lands, operators MUST NOT treat per-request `L-ASSET-UPLOAD-MAX` as a substitute for Tenant storage anti-abuse.

Provider-side quota/rate signals are **execution** outcomes: they update Provider Account health/cooldown (#17) and map to canonical errors (#16). They do **not** replace Client API Key admission controls and MUST NOT debit another Tenant.

### 7.6 Fail-closed when limit state is unavailable

If rate, concurrency, quota, or revocation state backends are unavailable:

- Public API edge MUST **fail closed** for new admissions (prefer **503-class** with retry semantics distinct from Tenant `quota_exhausted`), **or** use a previously consistent snapshot only if it still respects `R-REVOKE-PROP` for authz of revoked keys.
- MUST NOT “allow all” when counters are down.
- MUST NOT borrow another Tenant’s counter store.

### 7.7 Request-size semantics

**Canonical size-violation outcome:** **413-class** for every request rejected because it exceeds `L-JSON-BODY-MAX`, `L-ASSET-UPLOAD-MAX`, or a stricter documented endpoint max.

- If `Content-Length` is present and exceeds the applicable max → reject with **413-class** without reading the full body.
- If length is unknown (chunked), abort when buffered bytes exceed max → **413-class** (same class as Content-Length path; clients MUST NOT need to distinguish transport encoding).
- Malformed framing, invalid multipart boundaries, or JSON syntax errors that occur **without** exceeding the size max are **400** and are **not** counted as size violations for this AC.
- JSON inference bodies use `L-JSON-BODY-MAX`.
- Asset upload endpoints use `L-ASSET-UPLOAD-MAX` (and any stricter image dimension checks from #13 remain additional).
- Size limits apply **per request**; they are not a substitute for RPM.

---

## 8. Abuse controls

### 8.1 No open proxy

- Unauthenticated inference is **MUST NOT**.
- Valid Client API Key is required for all Tenant-owned resource operations on the Public API.

### 8.2 Failed authentication throttling

| Signal | Control |
|---|---|
| Failures per source IP | `L-AUTH-FAIL-IP-RPM` → **429-class** or temporary soft-block without confirming whether a locator exists |
| Failures per `(source_ip, public_locator)` | `L-AUTH-FAIL-IP-LOCATOR-RPM` → same class; **primary** locator-aware throttle |
| Failures per `public_locator` alone (any IP) | MUST NOT hard-lockout a locator platform-wide solely from distributed wrong-secret attempts. Implementations MAY apply mild global slowdown/telemetry for a locator under extreme volume, but a client presenting the **correct** secret from a non-abusive IP MUST still authenticate subject only to IP and `(IP, locator)` budgets |
| Valid auth + repeated A1/A2 violations | MAY auto-revoke or disable key after operator-configured threshold; emit audit `client_api_key.abuse_revoked` |

Cause → effect: an attacker who scraped `public_locator` from logs can burn fail-auth attempts from many IPs; without the rule above they could deny the legitimate key owner. Binding the tight locator budget to **`(source_ip, public_locator)`** stops that cross-IP lockout while still rate-limiting single-IP stuffing.

Responses MUST NOT become an oracle for “this locator exists but secret is wrong” vs “unknown” beyond what §4.3 already allows; throttle messages stay generic.

### 8.3 Cross-Tenant and replay

- Follow #6 attack matrix (A1–A14). Revoked keys cannot read any Tenant’s resources (**401**).
- Idempotency records remain partitioned by Tenant and key scope (#6 §3 Idempotency Record); #20 refines HTTP idempotency headers.

### 8.4 Logging redaction

MUST NOT log: full material, secret segment, secret hash, `Authorization` raw value.

MAY log: `tenant_id`, `client_api_key_id`, public_locator, decision (`auth_ok`, `auth_fail`, `rate_limited`, …), request id.

---

## 9. Admission rejection vs execution / runtime failure

### 9.1 Decision rule

```text
if failure occurs before A6 accept for execution:
    classification = admission_rejection
else:
    classification = execution_runtime_failure
```

### 9.2 Matrix

| Condition | Phase | HTTP-oriented class | Debit rate? | Hold concurrency? | Debit daily quota? | Adapter / vault decrypt? |
|---|---|---|---|---|---|---|
| Missing/invalid/revoked key | A0 | **401** | abuse counters only | no | no | no |
| Insufficient scope / allowlist (same Tenant) | A1 | **403-class** | optional authz metric | no | no | no |
| Foreign/unknown Tenant-scoped id | A1 | **404-class** | optional authz metric | no | no | no |
| Body/asset too large (size max exceeded) | A2 | **413-class** only | SHOULD count Tenant RPM | no | no | no |
| Tenant/key RPM exceeded | A3 | **429-class** `rate_limit` | counts in window | no | no | no |
| Concurrency exceeded | A4 | **429-class** `concurrency_limit` | may already have counted RPM | no | no | no |
| Daily quota exhausted | A5 | **429-class** `quota_exhausted` | may already have counted RPM | no | no | no |
| Capability unsupported (pre-exec) | post-A5 capability gate | **4xx** capability class (#10/#16) | per rate rules if admitted past A3 | no if rejected before A6 | no | no |
| Provider rate / quota / challenge / auth expiry / timeout / protocol drift | execution | canonical runtime errors (#16/#17) | already counted at A6 | yes until release | reservation/reconcile per §7.5 | yes, same-Tenant only |
| Worker crash after job admitted | execution | job failure states (#14) | already counted | until terminal | job unit already counted | possible |

### 9.3 Client guidance (observable)

- **401**: fix or rotate key; do not retry blindly with same material after revoke.
- **403-class**: adjust scope or use a key with broader grant; not a signal to switch Tenant.
- **413-class**: reduce payload.
- **429-class `rate_limit`**: respect reset; exponential backoff.
- **429-class `concurrency_limit`**: wait for in-flight completion; lowering parallelism.
- **429-class `quota_exhausted`**: wait for quota period reset or raise Tenant limits; not the same as Provider quota.
- Runtime Provider errors: follow #16 remediation classes; may require reauth of Provider Account (#9), not a new Client API Key.

### 9.4 Worked examples

#### Example A — Immediate revoke

1. Attacker steals material `sk-pxp_loc_secret`.
2. Tenant owner revokes key at t=0; API returns success.
3. At t=3s (< `R-REVOKE-PROP`), edge has refreshed revocation → attacker’s next chat request gets **401**.
4. No new Adapter call is made for that attacker request.
5. A chat stream admitted at t=−1s MUST have cancel attempted; if still non-cancelable upstream, it MAY finish but MUST NOT mint a new principal or new A6 accept after revoke.

#### Example B — Rate vs Provider rate

1. Tenant RPM effective = 60. Client sends request 61 inside the window → **429-class `rate_limit`** at A3; Provider never called.
2. Later, under RPM, Provider returns upstream rate limit → execution failure mapped by #16; Provider Account cooldown may update (#17); Client API Key still valid.

#### Example C — Quota isolation

1. Tenant A exhausts `L-TENANT-IMAGE-JOB-DAY`.
2. Tenant B’s job create still admits if B’s counters allow.
3. A’s rejection is `quota_exhausted`, not a global platform outage signal.

#### Example D — Scope narrow

1. Key has only `assets.read`.
2. Upload asset → **403-class** at A1; upload bytes MUST NOT be stored as an Asset.

---

## 10. Security impact summary

| Defect | Impact |
|---|---|
| Store plaintext Client API Key material | Database leak = full Tenant impersonation |
| Unbounded positive auth cache | Revoke ineffective; stolen keys continue |
| Dual-valid rotate grace without policy | Extended leak window |
| Scope grants cross-Tenant | Breaks BYOA / #6 |
| Admission after Adapter call | Wasted Provider quota; harder abuse control |
| Shared counters across Tenants | Cross-Tenant DoS / capacity theft |
| Allow-all when limit store down | Open flood / cost amplification |
| Log Authorization header | Secret sprawl into ops systems |
| 403 on foreign resource id | Existence oracle (#6) |
| Default `keys.manage` on inference keys | Lateral movement after single key leak |

---

## 11. Test obligations

Exact harness arrives with contract prototypes (#18–#20). Required observable cases for this issue:

### 11.1 Lifecycle

1. Create returns material once; subsequent get/list never return material.
2. Authenticate with returned material → principal Tenant matches owner.
3. Rotate → old material **401** within `R-REVOKE-PROP`; new material works; `client_api_key_id` stable.
4. Revoke → material **401** within `R-REVOKE-PROP`; idempotent second revoke.
5. Unknown / wrong secret / revoked → indistinguishably **401**.

### 11.2 Scope and ownership

6. Missing operation scope → **403-class**; no Adapter call.
7. Foreign Provider Account id → **404-class**; no Adapter call; no vault decrypt.
8. Default create grant excludes `keys.manage`.
9. Key with `keys.manage` can revoke sibling keys of same Tenant only.

### 11.3 Admission

10. Exceed RPM → **429-class `rate_limit`**; Adapter calls = 0.
11. Exceed chat concurrency → **429-class `concurrency_limit`**; Adapter calls = 0, including when client-terminal residual executions still occupy the Tenant and originating-key counters until accounting terminal (#12).
12. Exceed image job day quota → **429-class `quota_exhausted`**; no job row accepted.
13. Oversized JSON body → **413-class**; authenticated size rejects count toward Tenant RPM.
14. Oversized asset upload → **413-class**.
15. Tenant A exhaustion does not change Tenant B remaining counters.
16. Admission rejects do not debit daily quota units.
17. Limit backend outage → fail closed (no allow-all).
18. `max_tokens` above `L-CHAT-MAX-TOKENS-PER-REQ` rejected before reserve; reserve uses the min cap.
19. Explicit empty `model_allowlist` denies model-bearing ops (**403-class**); omitted allowlist does not.
20. One active key at chat concurrency cap does not by itself block a second key of the same Tenant from admitting while Tenant ceiling still has capacity (per-key subdivision).

### 11.4 Abuse and redaction

21. Failed auth flood from one IP hits `L-AUTH-FAIL-IP-RPM` / `L-AUTH-FAIL-IP-LOCATOR-RPM` without confirming key existence beyond §4.3.
22. Distributed wrong-secret attempts against a scraped `public_locator` from many IPs do **not** hard-lockout a correct secret from a clean IP.
23. Logs/metrics for the above cases contain `client_api_key_id` or public_locator at most, never full material.
24. Revoked key cannot read own or foreign resources (**401** only).
25. Revoke triggers cancel attempt for cancelable in-flight chat; slots release on cancel completion.

### 11.5 Phase boundary

26. Injected Provider failure only occurs on tests that passed A6; admission-only tests assert zero Adapter invocations and zero Provider Credential decrypts.

---

## 12. Core invariants (normative checklist)

1. **I-KEY-TENANT** — Every Client API Key has exactly one immutable `tenant_id`.
2. **I-KEY-MATERIAL-ONCE** — Full material is displayed at most once per create/rotate event and never stored in plaintext.
3. **I-KEY-HASH** — Only a non-reversible secret hash (HMAC-SHA-256 with server pepper by default) is stored for verify; compare is constant-time.
4. **I-KEY-PREFIX** — Material uses `sk-pxp_` product prefix and bearer transport only for Public API.
5. **I-PRINCIPAL-FROM-KEY** — Authenticated Public API principal is solely `(tenant_id, client_api_key_id)` from verified active material.
6. **I-AUTH-FAIL-401** — Missing, malformed, unknown, wrong-secret, and revoked material all yield **401** without Tenant resource access.
7. **I-REVOKE-BOUNDED-CACHE** — Positive auth decisions MUST NOT outlive `R-REVOKE-PROP` (5s default); unbounded active-key cache is forbidden.
8. **I-ROTATE-IMMEDIATE** — Rotate invalidates previous secret immediately (no MVP dual-valid grace).
9. **I-SCOPE-NARROW** — Scopes only narrow same-Tenant actions; default grant excludes `keys.manage`; explicit empty model/account allowlists mean deny-all for that dimension.
10. **I-ADMIT-ORDER** — Authn → scope → size → rate → concurrency → quota → accept; no Adapter/job side effects before accept.
11. **I-LIMIT-HIERARCHY** — `effective = min(platform, tenant, key_override?)`; key cannot exceed Tenant ceiling.
12. **I-LIMIT-ISOLATION** — Rate/concurrency/quota counters never shared across Tenants; chat concurrency is also subdivided by `client_api_key_id` under the Tenant ceiling, and client-terminal residual work retains both occupancies until #12 accounting terminal.
13. **I-ADMIT-VS-EXEC** — Admission rejections are distinct from Provider/execution failures; clients can tell `rate_limit` / `concurrency_limit` / `quota_exhausted` classes apart from runtime Provider errors.
14. **I-NO-OPEN-PROXY** — No unauthenticated inference; abuse throttles apply to auth failures without global locator lockout from foreign IPs.
15. **I-REDACT-KEY** — Full material and secret hash never appear in logs, metrics labels, or non-one-time API responses.
16. **I-FAIL-CLOSED-LIMITS** — Unavailable revocation or limit state does not fail open.
17. **I-RESERVE-CAP** — Chat token reservation at admission is capped by `L-CHAT-MAX-TOKENS-PER-REQ`.
18. **I-CANCEL-ON-REVOKE** — Revoke MUST attempt cancel of cancelable in-flight work for that key and release concurrency only when cancel completion confirms upstream stopped; surviving work remains accounted under #12.

---

## 13. Open follow-ups (explicitly deferred)

| Topic | Issue | Constraint retained here |
|---|---|---|
| Exact OpenAPI paths/schemas for key management + error code strings | #16, #18, #20 | Status classes and class names above |
| Provider Account connection UX | #9 | `accounts.*` scopes only |
| Capability gate details | #10 | Pre-Adapter; not a rate limit |
| Routing policy shape | #11 | `routing.*` scopes; allowlists still same-Tenant |
| Chat cancel protocol details after revoke | #12 | MUST attempt cancel; residual non-cancelable window documented |
| Asset retention, dimension validation, **Tenant durable storage/count caps** | #13 | Per-request upload max here; total storage anti-abuse **required** in #13 |
| Job terminal states & output retry quota | #14 | Job concurrency + job/day units |
| Pepper/KMS storage mechanics | #15 | Pepper secrecy class; HMAC default; 256-bit offline-crack infeasibility |
| Provider health / cooldown | #17 | Execution phase only |
| Commercial billing | parent Out of Scope | Anti-abuse quota ≠ invoice |
| Rotate grace window | reopen `D-ROTATE-GRACE` | Immediate invalidate is MVP |
| Numeric retune | reopen `D-NUMERIC-TUNE` | Semantics unchanged |

---

## 14. ADR decision

No new ADR. Client API Key ownership and principal formation were product-locked in parent #1 and expanded by #6. This document is the durable normative expansion under `docs/spec/` for lifecycle and admission controls. An ADR **would** be warranted if product later introduced:

- dual-valid rotate grace as default, or
- shared global API keys across Tenants (forbidden by current product), or
- fail-open admission under limit-store outage.

---

## 15. Acceptance criteria traceability

| AC (issue #8) | Where satisfied |
|---|---|
| Create, one-time display, authenticate, rotate, scope and revoke have clear observable behavior | §3.4, §4, §5, §11.1–§11.2, §12 |
| Revocation takes effect at Public API boundary and does not depend on unbounded cache | §4.5, `R-REVOKE-PROP`, I-REVOKE-BOUNDED-CACHE, §11.1.4, §9.4 Example A |
| Rate, concurrency, quota and request-size violations have canonical outcomes per Tenant and key | §6, §7, §9.2, §11.3 |
| Boundary between admission rejection and execution/runtime failure is locked | §1.1, §6, §9, I-ADMIT-VS-EXEC, §11.5 |

---

## 16. Constants index

| Id | Value / meaning |
|---|---|
| `H-KEY-HMAC` | HMAC-SHA-256(server_pepper, secret) default verify |
| `R-REVOKE-PROP` | 5s max positive auth cache / revocation propagation budget |
| `L-TENANT-RPM` | 60 / min |
| `L-TENANT-BURST` | 20 |
| `L-TENANT-CHAT-CONCURRENCY` | 5 Tenant ceiling |
| `L-TENANT-JOB-CONCURRENCY` | 3 Tenant ceiling |
| `L-KEY-CHAT-CONCURRENCY` | Per-key chat subdivision under Tenant ceiling (default partition by key) |
| `L-TENANT-REQ-DAY` | 10_000 / UTC day |
| `L-TENANT-CHAT-TOKEN-DAY` | 2_000_000 est. tokens / UTC day |
| `L-TENANT-IMAGE-JOB-DAY` | 200 / UTC day |
| `L-CHAT-MAX-TOKENS-PER-REQ` | 8_192 reservation/request cap |
| `L-JSON-BODY-MAX` | 2 MiB |
| `L-ASSET-UPLOAD-MAX` | 20 MiB |
| `L-AUTH-FAIL-IP-RPM` | 60 / min / IP |
| `L-AUTH-FAIL-IP-LOCATOR-RPM` | 20 / min / `(IP, locator)` |
| `D-NUMERIC-TUNE` | Reopen id for changing numeric defaults without semantic rewrite |
| `D-ROTATE-GRACE` | Reopen id if dual-valid rotate is ever desired |
