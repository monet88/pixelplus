# Asset Exchange, Authorization, and Retention Lifecycle

- Status: Accepted for specification (issue #13)
- Date: 2026-07-14
- Parent: [#1](https://github.com/monet88/pixelplus/issues/1)
- Issue: [#13](https://github.com/monet88/pixelplus/issues/13)
- Vocabulary source: `CONTEXT.md`
- Related ownership invariants: `docs/spec/tenant-ownership-authorization-invariants.md` (#6)
- Related risk envelope: `docs/spec/auth-mode-risk-envelope-and-kill-criteria.md` (#7)
- Related Client API Key / admission: `docs/spec/client-api-key-lifecycle-and-admission-controls.md` (#8)
- Related connection / credential lifecycle: `docs/spec/provider-account-connection-and-credential-lifecycle.md` (#9)
- Related Capability Snapshot / model availability: `docs/spec/capability-snapshot-and-model-availability-semantics.md` (#10)
- Related tenant-scoped routing / fallback / affinity / lease: `docs/spec/tenant-scoped-routing-fallback-affinity-leases.md` (#11)

## 1. Scope and non-goals

### 1.1 Scope

This specification locks the **Public API behavior for image Assets** — how a client **uploads or references** input image and mask data, how output Assets are **retrieved**, and how every Asset is **owned, validated, retained, and deleted** — so that Assets are validated early, accessible only inside the owning Tenant, and never leak the existence of another Tenant's Asset.

It codifies parent #1 user stories 37–38 and 44–46 (asset input/mask contract, early validation, output access, retention/deletion) and consumes the ownership boundary (#6 `I-ASSET-ISO`, non-enumeration), admission pipeline and per-request size limits (#8), and the capability taxonomy for `image_generation` / `image_edit` / `inpaint` including mask semantics (#10).

This is **specification work**; it does **not** implement the Gateway.

It covers:

1. The **Asset object**: kinds (`input` / `mask` / `output`), logical fields, ownership, and the create/reference/retrieve/delete surface.
2. **Canonical validation** of an input image, a mask, their format and size, and the **relationship** between an image and its mask (dimensions, channels) — with a canonical outcome for each failure.
3. **Tenant ownership enforcement** on create, reference, retrieve, and delete, including cross-Tenant reference in a job/request.
4. **Retention, expiry, and deletion**: when an output Asset stops being downloadable, and how deletion behaves.
5. **Non-enumeration**: a cross-Tenant (or unknown) Asset identifier yields a safe, non-existence-confirming failure.
6. **Tenant durable storage / object-count caps** (explicitly delegated to this issue by #8 §7.5).

### 1.2 Non-goals

This document does **not**:

- Implement Gateway, storage backend, image codec, or UI code.
- Redefine **ownership / non-enumeration** (#6), **admission controls / per-request size limits** (#8 §7.7), the **usability gate** (#9), **capability enforcement / inpaint-unsupported / mask taxonomy** (#10 §3/§9), or **routing** (#11). It **consumes** them.
- Own the **Render Job state machine, progress, cancellation, worker recovery, or output-placement retry** (#14). This document locks the **Asset** an image request consumes and produces; #14 locks the durable job that connects them.
- Own the **capability taxonomy** (#10). Whether `inpaint` is supported for an account is #10; this document locks the **mask/asset validation** that a supported `inpaint`/`image_edit` request must still pass.
- Freeze **numeric** retention windows, storage-cap numbers, or expiry timers as immutable — this document locks named **retention classes**, the **required** storage-cap dimension, and MVP defaults that #17 may retune without removing the obligation.
- Freeze **JSON schema, field names, OpenAPI paths, or multipart wire format** — those are #18 / #20. This document locks **logical fields and semantics**.
- Define **canonical error code strings** — #16 owns those; this document locks status/remediation **classes** and non-enumeration.
- Design **vault crypto** (#15). Assets are Tenant image data, not Provider Credential material; at-rest encryption/retention-hold crypto detail is #15, but the **ownership, redaction, and deletion moments** here are binding.

Downstream issues **MUST** preserve every decision here. They may add fields, tighten validation, or add UX. They **MUST NOT**:

- Read, write, or reference an Asset across `principal.tenant_id` (#6 `I-ASSET-ISO`).
- Return a distinguishing response for a foreign vs unknown Asset id (#6 `I-NON-ENUM`).
- Silently downgrade an `inpaint` request by dropping/ignoring the mask (#10 §9.1).
- Serve an output Asset after its retention window has expired or after deletion (§5).
- Treat per-request `L-ASSET-UPLOAD-MAX` (#8) as a substitute for the Tenant durable storage cap this issue defines (§6).

### 1.3 Normative language

- **MUST / MUST NOT / REQUIRED**: product/security policy. Violation is a defect.
- **SHALL**: same force as MUST for observable Public API behavior.
- **SHOULD**: strongly preferred default; deviation needs an operator-recorded exception.
- **MAY**: optional surface that cannot weaken MUST rules.

### 1.4 Relationship to prior issues

| Topic | Already locked | This document adds |
|---|---|---|
| Asset ownership | Exactly one Tenant; immutable `tenant_id`; foreign existence MUST NOT be confirmed (#6 §3, `I-ASSET-ISO`) | Asset kinds, fields, create/reference/retrieve/delete behavior |
| Non-enumeration | Foreign/unknown id → 404-class; reject job referencing foreign asset before queue (#6 §5.2 A3/A4) | The asset-specific validation + retrieval + delete paths that obey it |
| Per-request size | `L-ASSET-UPLOAD-MAX` (20 MiB) 413-class; framing errors 400 (#8 §7.7) | Content validation (format, dimensions, image↔mask relationship) beyond raw byte size |
| Durable storage caps | Explicitly **required of #13** (#8 §7.5) | The Tenant storage / object-count cap dimension + MVP defaults |
| Scope | `assets.read` (read/list/download), `assets.write` (upload/delete) (#8 §5.2) | Which asset op needs which scope |
| Capability / mask | `inpaint` first-class; `unsupported` on all Gemini/Grok modes; MUST NOT downgrade to `image_edit` (#10 §4.3, §9.1) | The mask **validation** an accepted inpaint/edit still must pass |

### 1.5 Decision unit

**One Asset = one immutable image data object (`input` / `mask` / `output`) owned by exactly one Tenant, referenced only by same-Tenant requests/jobs, retrievable only by a same-Tenant principal, and subject to a defined retention/deletion lifecycle.**

Cause → effect:

1. A client uploads an input image → an `input` Asset stamped `tenant_id = principal.tenant_id`; a mask uploaded for it is a separate `mask` Asset of the same Tenant.
2. A masked (`inpaint`) request references an input Asset id and a mask Asset id → the Gateway validates both belong to the Tenant and that the mask is dimensionally compatible **before** any upstream execution (#6 §11 Example 2, #10 §9.1).
3. A completed image request produces an `output` Asset the Tenant can download until its retention window expires or it is deleted; after that, retrieval returns a canonical "gone/not-found" outcome — never another Tenant's data, never a foreign-existence hint.

---

## 2. Glossary extensions (normative use)

| Term | Meaning in this document |
|---|---|
| **Asset** | Tenant-owned, immutable image data object with a kind (`input`/`mask`/`output`). Content bytes do not change after create; a new edit produces a **new** Asset (`CONTEXT.md`). |
| **Asset kind** | `input` (client-provided source image), `mask` (client-provided masked-region selector for inpaint/edit), `output` (image produced by a completed image request/Render Job). |
| **`asset_id`** | Stable, unguessable, non-secret identifier of an Asset (owning Tenant only). Safe for logs/audit within the Tenant; never confirms existence cross-Tenant (#6). |
| **Upload** | Client transfers new image/mask bytes to create an `input`/`mask` Asset. |
| **Reference** | A chat/image request or Render Job names an existing `asset_id` (input or mask) instead of re-uploading. |
| **Retrieve** | Client reads/lists/downloads an Asset (metadata and/or bytes). |
| **Validation outcome** | The canonical classification of an upload/reference check: `accepted`, or a specific rejection class (§4). |
| **Image↔mask relationship** | The compatibility constraints a mask must satisfy against its target input image (dimensions and, where required, channel/format) so a masked op is well-formed (§4.3). |
| **Retention class** | A named lifetime budget after which an output Asset is no longer downloadable; numeric value is #17-tunable (§5.2). |
| **Expiry** | The moment an Asset passes its retention window and becomes non-retrievable (canonical "gone"). |
| **Deletion** | Tenant- or system-initiated removal of an Asset before or at expiry; terminal for retrieval. |
| **Storage cap** | The Tenant durable total-bytes / object-count ceiling for retained Assets (§6), distinct from the per-request upload size limit (#8). |

---

## 3. Asset object and ownership

### 3.1 Asset logical fields

| Field | Required | Notes |
|---|---|---|
| `tenant_id` | yes | Immutable (#6 §3.1); stamped at create from the Security Principal |
| `asset_id` | yes | Stable, unguessable id; safe to expose to owning Tenant only |
| `kind` | yes | `input` \| `mask` \| `output`; immutable |
| `content_type` | yes | Canonical image media type (e.g. PNG, JPEG, WebP); validated (§4.1) |
| `byte_size` | yes | Actual stored size; bounded by `L-ASSET-UPLOAD-MAX` (#8) at intake and counted toward the storage cap (§6) |
| `width` / `height` | when decoded | Pixel dimensions; required for image↔mask relationship checks (§4.3) |
| `checksum` | yes | Content digest for integrity / dedupe (non-secret) |
| `origin` | yes | `uploaded` (client) or `generated` (Render Job output, #14) |
| `source_job_id` | when `origin=generated` | The Render Job that produced this `output` (same Tenant, #14); absent for uploads |
| `created_at` | yes | |
| `retention_class` | yes | Named retention budget (§5.2); derived from kind + product policy |
| `expires_at` | when applicable | Derived `created_at` + retention class; the moment retrieval stops (§5) |
| `deleted_at` | when deleted | Set on delete; retrieval behaves as not-found afterward (§5.4) |

**MUST NOT** store on the Asset row or any Public API projection:

- Provider Credential material or Client API Key material (#8/#9 redaction — Assets are image data, not secrets, but the row still MUST NOT become a secret sink).
- Another Tenant's `tenant_id` or any cross-Tenant reference (#6 §3.2).

### 3.2 Ownership rules (consumes #6)

1. Every Asset has exactly one immutable `tenant_id` (#6 §3.1, `I-SINGLE-OWNER`). There is no transfer-to-another-Tenant operation.
2. `input`/`mask` Assets are created with `tenant_id = principal.tenant_id` (#6 §4.3). `output` Assets inherit the `tenant_id` of the Render Job that produced them (#6 §2.4, #14).
3. An Asset is readable, listable, downloadable, referenceable, and deletable **only** by a Security Principal of the owning Tenant (subject to key scope, §7), or by a same-Tenant system/worker path acting under the resource `tenant_id` (#6 §2.4).
4. Any write that would attach an Asset across Tenants (e.g. a Render Job of Tenant A referencing an Asset of Tenant B as input/mask) MUST fail **before persistence / before queue** (#6 §3.2, §5.2 A4).

### 3.3 Immutability

- Asset content bytes are immutable after create. An `image_edit`/`inpaint` result is a **new** `output` Asset, not a mutation of the input (parent #1 mask fidelity; keeps the input reproducible and auditable).
- `tenant_id`, `asset_id`, and `kind` are immutable. Only lifecycle fields (`expires_at`, `deleted_at`) transition.

---

## 4. Canonical validation (AC1)

Validation runs at **upload** (content available) and at **reference** (id + relationship). Where a check needs Tenant data or the referenced Asset, it runs after admission (#8 A0–A5) and after ownership resolution, but always **before** upstream execution / Render Job enqueue (parent #1 story 38, #10 §9).

### 4.1 Format validation

1. `content_type` MUST be one of the canonical supported image media types (exact set is #18/#20; MVP intent: PNG, JPEG, WebP). An unsupported/mismatched type → rejection class `unsupported_format`.
2. The declared `content_type` MUST match the **actual decoded content** (magic-byte / decoder check); a mismatch (e.g. declared PNG, actual HTML/EXE) → `invalid_image` (defense against smuggling non-image payloads).
3. A file that cannot be decoded as a valid image → `invalid_image`.

### 4.2 Size validation

1. **Raw byte size** is bounded by `L-ASSET-UPLOAD-MAX` (#8 §7.2, 20 MiB) at intake; a violation is a **413-class** size outcome owned by #8 §7.7 (this document does not restate the class, it consumes it).
2. **Pixel dimensions** MUST fall within canonical min/max bounds (exact numbers #17/#18); an image whose width/height is outside bounds → `invalid_dimensions`. This is a **content** validation distinct from raw byte size (a 1 MiB file can still be dimensionally out of range).
3. Dimension validation is a canonical outcome the Gateway can return **before** upstream execution, satisfying "validate kích thước … trước execution khi có thể" (parent #1 story 38).

### 4.3 Image↔mask relationship validation (normative)

A masked operation (`inpaint`, or `image_edit` with a mask) references an input image Asset and a mask Asset. Before upstream execution the Gateway MUST validate:

1. Both `asset_id`s resolve to Assets of `principal.tenant_id` (else non-enumerating denial, §4.5 / #6).
2. The mask `kind == mask` and the input `kind == input` (or a prior `output` used as input); a role mismatch → `invalid_mask`.
3. **Dimensional compatibility:** the mask dimensions MUST match the input image dimensions per the canonical rule (default: exact `width`×`height` match; any product-permitted scaling rule is #18). A mismatch → `mask_dimension_mismatch`.
4. **Channel/encoding compatibility** where the operation requires it (e.g. mask expresses a single-channel selection): a mask that cannot be interpreted as a region selector → `invalid_mask`.
5. **Capability precedence (consumes #10):** if the routed account's snapshot classifies `inpaint` as `unsupported` (all Gemini/Grok modes, #10 §4.3), the request is rejected with `capability_unsupported` (#10 §10) **before** mask validation cost is spent on upstream, and the mask MUST NOT be silently dropped to degrade the request into a plain `image_edit` (#10 §9.1, `I-CAP-REJECT-BEFORE-UPSTREAM`). Capability rejection and mask-shape rejection are **distinct** canonical classes.

### 4.4 Validation outcome table (canonical)

| Outcome | When | HTTP-oriented class | Upstream/side effect |
|---|---|---|---|
| `accepted` | All checks pass | 2xx / proceed | Asset stored (upload) or reference resolved |
| `unsupported_format` | Media type not supported | 4xx validation | No upstream; no Asset stored for the bad upload |
| `invalid_image` | Undecodable / content≠declared type | 4xx validation | No upstream; bytes not retained as a usable Asset |
| `invalid_dimensions` | Pixel bounds violated | 4xx validation | No upstream |
| `invalid_mask` | Mask role/channel invalid | 4xx validation | No upstream |
| `mask_dimension_mismatch` | Mask≠input dimensions | 4xx validation | No upstream |
| `capability_unsupported` (inpaint) | Routed account `inpaint`=`unsupported` (#10) | 4xx capability (#10 §10) | No upstream; mask NOT dropped to edit |
| Raw size over max | Bytes > `L-ASSET-UPLOAD-MAX` | **413-class** (#8 §7.7) | Partial body discard (#8) |
| Foreign/unknown referenced id | Asset not owned / unknown | **404-class** (#6) | No upstream; no cross-Tenant read |

### 4.5 Validation and non-enumeration

- Validation that requires resolving a referenced `asset_id` MUST first apply ownership (`asset.tenant_id == principal.tenant_id`); a foreign/unknown id yields **404-class** non-enumeration (#6 §5.2 A3/A4) — the client cannot distinguish "exists in another Tenant" from "never existed", and no relationship/dimension detail of a foreign Asset is leaked.
- Validation error bodies MUST NOT include fields derived from a foreign Asset (its dimensions, checksum, or existence).

---

## 5. Retention, expiry, and deletion (AC3)

### 5.1 Principle: bounded, observable lifetime

Every Asset has a **defined** point after which it is no longer downloadable — either its **retention expiry** or an explicit **deletion**. A client can always tell when an output stops being retrievable (parent #1 story 46), and a retrieval after that point returns a canonical "gone/not-found" outcome, never stale-but-served data and never another Tenant's data.

### 5.2 Retention classes (named; numbers #17-tunable)

Numeric windows are **not** frozen here (like #7/#10 threshold constants); implementations MUST cite class ids, not invent parallel magic numbers:

| Retention class id | Applies to | Intent (MVP default; #17 tunes) |
|---|---|---|
| `RETAIN-OUTPUT` | `output` Assets | Long enough for the client/Plugin to download and place the result; MVP default **7 days** |
| `RETAIN-INPUT` | client-uploaded `input`/`mask` Assets | Retained while referencing work may still run + a grace; MVP default **24 hours** after last reference or upload |
| `RETAIN-EPHEMERAL` | internal intermediate/derived data not client-facing and **not** a client `kind` (§2) — Render Job intermediates whose model #14 owns | Shortest; deleted promptly after the producing job terminates |

Rules:

1. `created_at` (or last-reference for inputs) + retention class → derived `expires_at`. Past `expires_at`, the Asset is **not retrievable** (canonical `gone`).
2. `#17` owns the numbers and MAY retune them; it MUST NOT remove the "expired output is not downloadable" obligation or make retention unbounded by default.
3. An output's retention starts when the Render Job produces it (#14), not at request submission, so a long job does not eat the client's download window.

### 5.3 Expiry behavior

1. On or after `expires_at`, retrieve/download of the Asset returns a canonical **`gone`/not-found** outcome (410-class or 404-class per #16; MVP intent: distinguishable "expired" remediation where the Tenant owned it, but never a foreign-existence oracle — see §5.5).
2. Expiry MUST NOT serve partial or stale bytes; the storage backend SHOULD reclaim the bytes at/after expiry (reclamation timing is #15/#17, but retrievability stops **at** `expires_at` regardless of reclamation lag).
3. Listing own Assets MUST NOT include expired Assets as retrievable; an expired entry MAY appear as a tombstone status to the owning Tenant but MUST NOT be downloadable.

### 5.4 Deletion behavior

1. **Who:** owning Tenant principal with `assets.write` (#8 §5.2), or a same-Tenant retention/cleanup job.
2. A foreign/unknown `asset_id` on delete → **404-class** (#6); no cross-Tenant delete, no existence confirmation.
3. Delete sets `deleted_at`, makes the Asset immediately non-retrievable, and schedules byte reclamation (#15). Subsequent retrieve/list behaves as **not-found** for that id.
4. Delete is **idempotent**: deleting an already-deleted/expired Asset is a success no-op, not an error.
5. **Referential effect:** deleting an `input`/`mask` Asset that an in-flight Render Job still needs is governed by #14 (the job fails safely or uses an already-captured copy); this document requires only that a deleted Asset is not newly referenceable and that deletion does not cascade across Tenants.
6. Deleting an `output` Asset does not refund the image-job quota already consumed to produce it (#8 §7.5, #14): retention/deletion is a storage lifecycle, not an execution refund.

### 5.5 Retention/deletion and non-enumeration

- A retrieve of an expired/deleted **own** Asset MAY carry a Tenant-facing remediation hint (`asset_expired` / `asset_deleted`) because the Tenant owned it. A retrieve of a **foreign/unknown** id MUST return the plain non-enumerating not-found (#6) — the two paths MUST NOT be observably distinguishable in a way that reveals a foreign Asset ever existed.
- Expiry/deletion timing MUST NOT become a side channel for foreign existence (a foreign id is not-found immediately and identically regardless of any real Asset's lifecycle).

---

## 6. Tenant durable storage and object-count caps (AC — consumes #8 §7.5)

#8 §7.5 explicitly requires this issue to define Tenant durable storage anti-abuse; per-request `L-ASSET-UPLOAD-MAX` is **not** a substitute.

### 6.1 Cap dimensions (required)

| Cap id | Dimension | MVP default (`D-ASSET-CAP-TUNE`; #17 may retune) |
|---|---|---|
| `L-TENANT-ASSET-BYTES` | Total retained Asset bytes per Tenant | **5 GiB** |
| `L-TENANT-ASSET-COUNT` | Total retained non-expired Asset objects per Tenant | **10_000 objects** |

Rules:

1. Caps are **per Tenant** and isolated (#6 `I-QUOTA-SCOPE`): Tenant A hitting its storage cap MUST NOT affect Tenant B's capacity.
2. `byte_size` counts toward `L-TENANT-ASSET-BYTES` from successful store until **logical deletion** (`deleted_at` set, §5.4) or **expiry** (`expires_at` passed, §5.3) — cap headroom is reclaimed **immediately** at that lifecycle transition, not deferred to physical byte reclamation (which lags per #15/#17). Count toward `L-TENANT-ASSET-COUNT` likewise. This mirrors retrievability, which also stops **at** `expires_at`/`deleted_at` regardless of reclamation lag (§5.3 rule 2, §5.4 rule 3), so a Tenant that deletes Assets to free space is unblocked at once.
3. A new **upload** that would exceed either cap is rejected at intake with a canonical **storage-cap class** (`storage_cap_exceeded`, 4xx/insufficient-storage per #16), distinct from the per-request 413 size class (#8 §7.7) and from admission `quota_exhausted` (#8 §7.5). Remediation: delete Assets or wait for expiry reclamation.
4. `output` Asset creation by a Render Job is governed by the same caps; #14 defines whether an over-cap Tenant's job fails at output placement or the client must free space first — this document locks that output storage **counts** and is **capped**, not that it is exempt.
5. Caps use effective limit hierarchy consistent with #8 §7.1 (`min(platform, tenant, override?)`); numbers are `D-ASSET-CAP-TUNE`-reopenable without changing the cap semantics.

### 6.2 Cap vs per-request size vs admission quota (distinct)

| Control | Owner | Trigger | Class |
|---|---|---|---|
| Per-request upload size | #8 §7.7 | Single upload > `L-ASSET-UPLOAD-MAX` | **413-class** |
| Daily image-job/request quota | #8 §7.5 | Requests/jobs per day | **429-class** `quota_exhausted` |
| **Durable storage cap** | **this issue** | Total retained bytes/objects per Tenant | `storage_cap_exceeded` (this issue) |

These are independent: a Tenant can be under RPM and per-request size yet over its storage cap, and vice versa.

---

## 7. Authorization surface (#8 scope mapping)

| Operation | Minimum scope |
|---|---|
| Upload `input`/`mask` Asset | `assets.write` (#8 §5.2) |
| Reference an `asset_id` in a chat/image request or job | `assets.read` (read to resolve) + the relevant inference scope (`images.*`) |
| List / get metadata / download own Asset | `assets.read` |
| Delete own Asset | `assets.write` |

Rules:

1. Foreign/unknown `asset_id` → **404-class** (#6) regardless of scope (ownership check precedes/combines so a foreign id never becomes a 403 existence oracle, #8 §5.4).
2. Same-Tenant but insufficient scope → **403-class** (#8 §5.4): e.g. a key with only `assets.read` attempting upload/delete.
3. Default inference keys include `assets.read`/`assets.write` (#8 §5.3), so ordinary image clients can exchange Assets; they still cannot manage accounts/keys/routing (#8 excluded defaults).
4. System/worker paths act only under the resource `tenant_id` (#6 §2.4); a worker placing an `output` Asset stamps the job's Tenant, never another.

---

## 8. Ownership, confused deputy, and non-enumeration in asset exchange

All asset paths obey #6:

1. **Every Asset id resolves inside `principal.tenant_id` only** (#6 `I-ASSET-ISO`). Upload stamps the Tenant; reference/retrieve/delete resolve with the Tenant scope; a foreign id is **404-class** and never reveals existence (#6 §5.1, §5.2 A2/A3).
2. **A job/request referencing a foreign Asset fails before queue** (#6 §5.2 A4, §11 Example 2): no cross-Tenant copy, no distinct "that asset belongs to someone else" error.
3. **Output Assets never cross Tenants** (#6 §9): a completed job's output is readable only by the job's Tenant; cross-Tenant output read is `I-ASSET-ISO` violation with confidential-image-leak impact.
4. **No ambient authority** (#6 §5.3 rule 5): another Tenant's in-flight decoded image in memory is never a candidate for this Tenant's request.
5. **Redaction:** Asset bytes and metadata are Tenant data; logs/metrics MUST NOT embed Asset bytes, and MUST NOT log another Tenant's `asset_id` as existing (#6 §6, #8 §8.4 spirit).

---

## 9. Security impact summary

| Defect | Impact |
|---|---|
| Cross-Tenant Asset read/download | Confidential image/mask/output leakage (#6 §9) |
| 403-on-foreign-asset-id (existence oracle) | Enumeration / Tenant graph mapping (#6 §5.1) |
| Job references foreign Asset accepted | Confused deputy; cross-Tenant data pulled into a job (#6 A4) |
| Silent inpaint→edit downgrade (mask dropped) | Mask fidelity loss; wrong edit region; broken product promise (#10 §9.1) |
| Accept non-image content as image | Payload smuggling / decoder exploit surface |
| Serve output after expiry/deletion | Data retained past policy; privacy/retention violation |
| Unbounded storage per Tenant | Cost amplification / storage DoS (why #8 delegated the cap here) |
| Distinguishable expired-own vs foreign not-found | Foreign existence side channel (#6) |
| Mutable Asset content | Loss of reproducibility/audit; ambiguous edit provenance |
| Delete cascading across Tenants | Cross-Tenant data destruction |

---

## 10. Test obligations

Exact harness arrives with contract prototypes (#18–#20). Required observable cases for this issue:

### 10.1 Validation (AC1)

1. Unsupported media type → `unsupported_format`; no Asset stored; no upstream.
2. Declared-type≠actual-content (e.g. PNG header on non-image) → `invalid_image`; bytes not retained as usable.
3. Out-of-bounds pixel dimensions → `invalid_dimensions` before upstream, even when raw bytes are under `L-ASSET-UPLOAD-MAX`.
4. Mask with dimensions ≠ input image → `mask_dimension_mismatch`; distinct from `invalid_mask` (role/channel) and from raw-size 413.
5. `inpaint` request on an account where `inpaint`=`unsupported` (#10) → `capability_unsupported` before mask cost; mask is **not** dropped to `image_edit`.
6. Raw upload > `L-ASSET-UPLOAD-MAX` → 413-class (#8); malformed framing without size violation → 400 (#8), not a size outcome.

### 10.2 Ownership (AC2)

7. Upload stamps `tenant_id = principal.tenant_id`; another Tenant cannot retrieve/list/download it.
8. Reference of a foreign `asset_id` in a chat/image request or job → 404-class; job not queued; zero cross-Tenant read.
9. A same-Tenant job referencing a same-Tenant input+mask proceeds; a job referencing a foreign mask fails before queue (#6 A4) without creating a durable cross-Tenant job.
10. Output Asset of Tenant A is not downloadable by Tenant B (404-class), and A's list never shows B's Assets.
11. Delete of a foreign `asset_id` → 404-class; no cross-Tenant delete.

### 10.3 Retention, expiry, deletion (AC3)

12. An `output` Asset is downloadable within `RETAIN-OUTPUT`; after `expires_at`, retrieve returns canonical `gone`/not-found and serves no bytes.
13. Expired own Asset is absent from retrievable listing; no stale bytes served.
14. Delete makes the Asset immediately non-retrievable; subsequent get is not-found; delete is idempotent.
15. Deleting an output does not refund image-job quota (#8/#14).
16. Retention window start for output is job-produce time, not request-submit time.

### 10.4 Storage caps (AC — #8 §7.5)

17. Upload that would exceed `L-TENANT-ASSET-BYTES` or `L-TENANT-ASSET-COUNT` → `storage_cap_exceeded`, distinct from 413 and from `quota_exhausted`.
18. Tenant A over its storage cap does not reduce Tenant B's storage capacity.
19. Expiry/deletion reclaims cap headroom for the same Tenant.

### 10.5 Non-enumeration and scope (AC4)

20. Foreign vs unknown `asset_id` are observably indistinguishable (both 404-class); no relationship/dimension detail of a foreign Asset leaks.
21. Expired-own vs foreign not-found do not form a foreign-existence side channel.
22. Key with only `assets.read` cannot upload/delete (403-class); foreign id still 404-class regardless of scope.

---

## 11. Core invariants (normative checklist)

1. **I-ASSET-TENANT** — Every Asset has exactly one immutable `tenant_id`; created from the Security Principal (uploads) or the producing Render Job's Tenant (outputs); never transferred (#6 `I-SINGLE-OWNER`).
2. **I-ASSET-TENANT-ISO** — Assets are readable, listable, downloadable, referenceable, and deletable only within their owning Tenant (this document's asset-scoped restatement of #6 `I-ASSET-ISO`; #6 remains the owner); output never crosses Tenants.
3. **I-ASSET-NON-ENUM** — A foreign/unknown `asset_id` on reference/retrieve/delete yields 404-class non-enumeration; foreign vs unknown are indistinguishable; no foreign relationship/dimension/existence detail leaks (#6 `I-NON-ENUM`).
4. **I-ASSET-VALIDATE-EARLY** — Format, decodability, pixel dimensions, and image↔mask relationship are validated with canonical outcomes before upstream execution / job enqueue where knowable (parent #1 story 38).
5. **I-ASSET-MASK-FIDELITY** — A masked request is never silently downgraded by dropping the mask; on an `inpaint`-`unsupported` account it fails `capability_unsupported` (#10 §9.1), distinct from mask-shape validation classes.
6. **I-ASSET-IMMUTABLE** — Asset content bytes are immutable; an edit/inpaint produces a new `output` Asset, not a mutation of the input.
7. **I-ASSET-RETENTION-BOUNDED** — Every Asset has a bounded, named retention class; an output stops being downloadable at `expires_at`; retention is never unbounded by default (#17 tunes numbers, not the obligation).
8. **I-ASSET-EXPIRY-GONE** — After expiry or deletion, retrieve/download serves no bytes and returns a canonical gone/not-found; expired/deleted Assets are not retrievable in listings.
9. **I-ASSET-DELETE-IDEMPOTENT** — Delete is idempotent and same-Tenant only; a foreign id is 404-class; deletion never cascades across Tenants.
10. **I-ASSET-STORAGE-CAP** — Tenant durable storage is bounded by `L-TENANT-ASSET-BYTES` and `L-TENANT-ASSET-COUNT`; over-cap uploads/outputs get `storage_cap_exceeded`, isolated per Tenant, distinct from per-request 413 and admission `quota_exhausted` (fulfills #8 §7.5 delegation).
11. **I-ASSET-SIZE-DISTINCT** — Raw-byte size (413-class, #8), content validation (this issue), and storage cap (this issue) are distinct canonical outcomes; one is never relabeled as another.
12. **I-ASSET-SCOPE** — Upload/delete require `assets.write`; read/list/download/reference require `assets.read`; foreign ids are 404-class before any scope-based 403 (#8 §5.4).
13. **I-ASSET-REDACT** — Asset bytes never appear in logs/metrics; foreign `asset_id`s are never logged as existing; Asset rows never become a Provider Credential / Client API Key sink.
14. **I-ASSET-WORKER-SCOPE** — System/worker paths create/read/delete Assets only under the resource's `tenant_id` (#6 §2.4); output placement stamps the job's Tenant only.

---

## 12. Open follow-ups (explicitly deferred)

| Topic | Issue | Constraint retained here |
|---|---|---|
| Render Job state machine, progress, cancel, worker recovery, output-placement retry | #14 | Asset kinds + ownership + validation + retention locked here; #14 connects input→job→output without re-owning Asset rules |
| Exact supported media-type set, pixel min/max, mask scaling rule | #17 / #18 | Canonical validation **outcomes** + image↔mask relationship semantics locked here |
| Numeric retention windows, storage-cap numbers, reclamation timing | #17 | Named retention classes + required cap dimensions + MVP defaults locked here (`D-ASSET-CAP-TUNE`) |
| At-rest encryption, retention legal hold, cryptographic shred | #15 | Ownership, redaction, and deletion **moments** locked here |
| Canonical error code strings / 410-vs-404 for expiry / problem+json | #16 | Status/remediation **classes** + non-enumeration locked here |
| Multipart wire format, JSON schema, OpenAPI asset paths, HTTP caching headers | #18 / #20 | Logical fields + semantics locked here |
| Asset dedupe / content-addressed storage optimization | reopen `D-ASSET-DEDUPE` | MVP: `checksum` recorded; dedupe not required, must not break per-Tenant isolation |
| Resumable / chunked large uploads | reopen `D-ASSET-CHUNK` | MVP: single-request upload under `L-ASSET-UPLOAD-MAX` |

---

## 13. ADR decision

No new ADR. Asset ownership, Tenant isolation, and non-enumeration were product-locked in parent #1 and #6 (`I-ASSET-ISO`, `I-NON-ENUM`); inpaint/mask taxonomy in #10. This document is the durable normative expansion under `docs/spec/` for asset exchange, validation, retention, deletion, and Tenant storage caps.

An ADR **would** be warranted if the product later introduced:

- cross-Tenant asset sharing or a shared asset pool (forbidden; `I-ASSET-ISO`),
- unbounded default retention or no storage cap (forbidden; §5/§6),
- silent inpaint→edit downgrade by dropping masks (forbidden; #10),
- or mutable Asset content in place (forbidden; §3.3).

---

## 14. Constants and reopen ids

| Id | Meaning |
|---|---|
| `RETAIN-OUTPUT` / `RETAIN-INPUT` / `RETAIN-EPHEMERAL` | Named retention classes (§5.2); numbers #17-tunable |
| `L-TENANT-ASSET-BYTES` / `L-TENANT-ASSET-COUNT` | Tenant durable storage caps (§6); MVP 5 GiB / 10_000 objects |
| `storage_cap_exceeded` | Canonical over-storage-cap outcome (§6), distinct from 413 and `quota_exhausted` |
| Validation classes (`unsupported_format`/`invalid_image`/`invalid_dimensions`/`invalid_mask`/`mask_dimension_mismatch`) | Canonical validation outcomes (§4); strings #16/#18 |
| `L-ASSET-UPLOAD-MAX` | Owned by #8 §7.2 (20 MiB per-request); consumed here |
| `D-ASSET-CAP-TUNE` | Reopen id for retuning storage-cap numbers without changing cap semantics |
| `D-ASSET-DEDUPE` / `D-ASSET-CHUNK` | Reopen ids for dedupe and chunked upload |
| `I-ASSET-ISO` / `I-NON-ENUM` / `I-SINGLE-OWNER` / `I-QUOTA-SCOPE` | Owned by #6; referenced here (asset-scoped restatements are `I-ASSET-TENANT-ISO` §11.2, `I-ASSET-NON-ENUM` §11.3) |
| Capability status / `inpaint` unsupported / mask taxonomy | Owned by #10; asset validation consumes it |

---

## 15. Acceptance criteria traceability

| AC (issue #13) | Where satisfied |
|---|---|
| Input image, mask, format, size and their relationship have canonical validation outcomes | §4, §10.1, `I-ASSET-VALIDATE-EARLY`, `I-ASSET-MASK-FIDELITY`, `I-ASSET-SIZE-DISTINCT` |
| Create, reference, retrieve and delete enforce Tenant ownership | §3, §7, §8, §10.2, `I-ASSET-TENANT`, `I-ASSET-TENANT-ISO`, `I-ASSET-SCOPE`, `I-ASSET-WORKER-SCOPE` |
| Retention, expiry and deletion state when output stops being downloadable | §5, §6, §10.3–§10.4, `I-ASSET-RETENTION-BOUNDED`, `I-ASSET-EXPIRY-GONE`, `I-ASSET-DELETE-IDEMPOTENT`, `I-ASSET-STORAGE-CAP` |
| Cross-Tenant identifier returns safe non-enumerating failure | §4.5, §5.5, §8, §10.5, `I-ASSET-NON-ENUM` |

---

## 16. Document control

| Field | Value |
|---|---|
| Status | Accepted for specification (issue #13) |
| Check date of evidence inputs | 2026-07-14 |
| Supersedes | n/a (initial asset exchange / authorization / retention lock) |
| Next review | On #10 capability/mask changes, #14 Render Job lifecycle, #15 vault/retention crypto, #16 error strings, or #17 numeric tuning |
| Authors | Spec decision agent for issue #13 |
