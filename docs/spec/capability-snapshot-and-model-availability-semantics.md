# Capability Snapshot and Model Availability Semantics

- Status: Accepted for specification (issue #10)
- Date: 2026-07-14
- Parent: [#1](https://github.com/monet88/pixelplus/issues/1)
- Issue: [#10](https://github.com/monet88/pixelplus/issues/10)
- Vocabulary source: `CONTEXT.md`
- Related ownership invariants: `docs/spec/tenant-ownership-authorization-invariants.md` (#6)
- Related risk envelope: `docs/spec/auth-mode-risk-envelope-and-kill-criteria.md` (#7)
- Related Client API Key / admission: `docs/spec/client-api-key-lifecycle-and-admission-controls.md` (#8)
- Related connection / credential lifecycle: `docs/spec/provider-account-connection-and-credential-lifecycle.md` (#9)
- Evidence inputs (research only; not acceptance by themselves):
  - `docs/spec/research/chatgpt-auth-mode-capability-evidence.md` (#3)
  - `docs/spec/research/gemini-auth-mode-capability-evidence.md` (#4)
  - `docs/spec/research/grok-auth-mode-capability-evidence.md` (#5)

## 1. Scope and non-goals

### 1.1 Scope

This specification locks the **canonical capability taxonomy** and the **Capability Snapshot** so that a client only ever sees operations and models that have been **verified for a specific Provider Account at a specific moment**, carrying enough freshness and provenance that routing (#11) and UI never infer capability from the Provider name, the Adapter, or a model identifier.

It covers:

1. The **operation taxonomy** distinguishing chat, chat streaming, image generation, image edit, and inpaint (plus a small extensible secondary-operation set).
2. The **capability status vocabulary** consumed from evidence #3–#5 and how it maps into a snapshot field.
3. The **Capability Snapshot record**: identity, capability status per operation, model availability, `verified_at`, freshness, and evidence/probe provenance.
4. **Freshness** (`fresh` / `stale` / `invalid`), TTL classes, and the difference between a soft re-verify signal and a hard non-authorizing kill.
5. **Invalidation and refresh triggers** for entitlement drift, credential change, and protocol drift.
6. **Enforcement**: unsupported or not-fresh capability is rejected **before** upstream execution where the gateway can know, wired into the #9 §5.1 `I-USABLE-GATE` item 7 request-time capability gate.

### 1.2 Non-goals

This document does **not**:

- Implement Gateway, Adapter, probe wire formats, or UI code.
- Redefine the **usability gate** (#9 §5.1 `I-USABLE-GATE`) or the **snapshot non-use rule** (#9 `I-SNAPSHOT-NONUSE`). It **consumes** them.
- Redefine **risk status** (`allowed` / `gated` / `experimental` / `prohibited`) or kill/feature-gate criteria (#7 owns `KS-*` / `FG-*` / `OP-G*` / `R0`–`R4`).
- Own the **capability-status meaning**; the tokens `verified` / `conditionally_supported` / `unsupported` / `unverified` are owned by evidence #3–#5. This document only locks how they are **recorded and enforced** in a snapshot.
- Freeze exact **TTL numbers**, probe schedules, or cooldown timers — those are #17 (this document locks named TTL **classes** and the re-verify obligation).
- Freeze the **JSON schema, field names, or OpenAPI paths** — those are #18 / #20. This document locks **logical fields and semantics**.
- Design **routing candidate selection, leases, or fallback** — #11 consumes usable + capability-satisfied accounts.
- Define **canonical error code strings** — #16 owns those; this document locks status/remediation **classes**.
- Design **vault crypto** — #15 owns that; snapshots never contain credential material (#9 §9).

Downstream issues **MUST** preserve every decision here. They may add fields, tighten probes, or add UX. They **MUST NOT**:

- Authorize a capability-bearing operation whose snapshot capability is `unsupported`, `unverified`, or not `fresh`.
- Advertise a model that was not observed for that Provider Account (no static-catalog invention).
- Let a Capability Snapshot promote or imply risk-status acceptance (#7 §2.2 / §7).
- Carry a snapshot across a `credential_version` bump without re-satisfying probe (#9 §4.8 / §4.9).
- Leak Provider Credential material into a snapshot payload (#9 §9.1).

### 1.3 Normative language

- **MUST / MUST NOT / REQUIRED**: product/security policy. Violation is a defect.
- **SHALL**: same force as MUST for observable Public API / management API behavior.
- **SHOULD**: strongly preferred default; deviation needs an operator-recorded exception.
- **MAY**: optional surface that cannot weaken MUST rules.

### 1.4 Relationship to prior issues

| Topic | Already locked | This document adds |
|---|---|---|
| Capability Snapshot ownership | Per-account, per-Tenant, minted by required probe, non-use on durable-gate failure (#9) | Schema fields, taxonomy, freshness, TTL classes, invalidation triggers |
| Usability gate | `I-USABLE-GATE` §5.1 incl. item 7 capability hook (#9) | What "capability satisfied for the requested operation" concretely means |
| Snapshot non-use | `I-SNAPSHOT-NONUSE` on durable §5.1 items 1–5 fail (#9) | TTL-driven `stale`/`invalid` in addition to durable-gate kill |
| Risk status | `allowed`/`gated`/`experimental`/`prohibited`; orthogonal to capability (#7) | Reaffirms orthogonality; snapshot never moves risk |
| Capability status meaning | `verified`/`conditionally_supported`/`unsupported`/`unverified` (#3–#5) | Records the token per operation + per model in a snapshot |
| Operational health | Canonical #17 Health State + Health Reason with scoped conditions | Which matching conditions invalidate vs merely degrade a snapshot |
| Model catalog | Observed slugs, not static (#3–#5) | Model-availability sub-record and observed-slug rule |

### 1.5 Decision unit

**One Capability Snapshot = exactly one Provider Account (one Tenant, one Auth Mode) at exactly one `credential_version`, at one verification moment.**

Cause → effect:

1. A ChatGPT Web Access account and a ChatGPT Codex OAuth account for the same human are **two** Provider Accounts (#9 §1.5) → **two** Capability Snapshots. Their image capabilities can diverge (web `image_gen` conversation vs Codex `image_generation` tool; evidence #3) and the snapshots MUST reflect that independently.
2. A silent refresh or reauth that bumps `credential_version` (#9 §4.8/§4.9) supersedes the snapshot's version binding. The new version is capability-satisfied only via a fresh probe **or** #9 §4.8 rule 3 inheritance; a superseded snapshot MUST NOT authorize work on the new version.
3. A Grok xAI OAuth account bound to `cli-chat-proxy.grok.com` vs `api.x.ai` (evidence #5) may expose different models; the snapshot MUST record the bound base-URL family so routing does not assume one from the other.

---

## 2. Glossary extensions (normative use)

| Term | Meaning in this document |
|---|---|
| **Capability Snapshot** | Tenant-owned, per-Provider-Account record of which **operations** and **models** were verified usable for that account at a `verified_at` moment and `credential_version`, with freshness and provenance. Not a static Provider/Adapter claim (`CONTEXT.md`). |
| **Operation** | A canonical capability-bearing action the Public API can request. Taxonomy in §3. |
| **Capability status** | Per-operation (and per-model) verification token consumed from #3–#5: `verified`, `conditionally_supported`, `unsupported`, `unverified`. §4. |
| **Model availability** | Per-model record within a snapshot: observed slug, per-operation capability status, entitlement/tier hint, and whether the model is currently offerable. §5. |
| **`verified_at`** | Wall-clock time the snapshot's capability facts were last confirmed by a successful probe or inherited satisfaction (#9 §4.8 rule 3). |
| **Freshness** | Derived lifecycle of a snapshot: `fresh`, `stale`, or `invalid`. §6. |
| **TTL class** | Named freshness budget per capability/provenance kind; numeric values are #17-tunable (like #7 threshold constants). §6.2. |
| **Provenance** | Evidence class + probe surface identifier proving how a capability fact was learned: `reference_learned`, `upstream_verified`, `live_probe`, or a hybrid. §7. |
| **Invalidation trigger** | An event that forces a snapshot from `fresh`/`stale` to `invalid` (or forces re-probe): `entitlement_drift`, `credential_change`, `protocol_drift`, `explicit_purge`, and durable-gate failure (#9 `I-SNAPSHOT-NONUSE`). §8. |
| **Capability-bearing operation** | A requested op whose admission requires the snapshot to affirm the operation+model (§9). Non-capability-bearing management ops (list/get account) do not consult it. |
| **Offerable** | A model/operation pair the gateway MAY expose to a client for selection right now: `fresh` + capability status in the offerable set + risk/usability gates pass. §5.3. |

---

## 3. Operation taxonomy (canonical)

### 3.1 Primary operations (AC-required)

Exactly these five are the first-class capability-bearing operations every snapshot MUST classify:

| Operation token | Meaning | Distinct because (evidence) |
|---|---|---|
| **`chat`** | Non-streaming chat/text completion | Baseline text op |
| **`chat_streaming`** | Incremental token/event streaming chat | Real vs **synthetic** streaming diverges by mode: Gemini Web Cookie simulates SSE by chunking a full body (#4), Gemini Antigravity is true SSE (#4). Routing MUST see this as a separate capability, not a flag |
| **`image_generation`** | Text-to-image generation | Web vs OAuth image surfaces diverge (#3 web `image_gen` conversation vs Codex `image_generation` tool); entitlement-gated |
| **`image_edit`** | Edit an existing image with a prompt (± reference images) | Distinct upstream path and entitlement from generation (#3/#5) |
| **`inpaint`** | Masked region edit (Photoshop-style mask semantics) | First-class in this product (parent #1); **`unsupported`** on all Gemini/Grok modes in evidence; offerable only on the two ChatGPT modes — client-composite mask on ChatGPT Web, stronger mask field on ChatGPT Codex (#3–#5). MUST be its own token so the gateway never silently degrades a mask request into a plain edit |

`chat_streaming` is a **separate operation**, not a modifier on `chat`, precisely so a snapshot can say "chat yes, real streaming no / synthetic only" without lying (#4 Gemini Web).

### 3.2 Secondary operations (extensible, non-AC)

Snapshots MAY additionally classify these where the Auth Mode exposes them; they are **not** required by #10 AC and downstream contract issues (#12–#14, #18–#20) refine them:

`model_listing`, `multi_turn_continuity`, `cancel_abort`, `tool_calling`, `file_input_attachment`.

Rules:

1. The primary five MUST always be present in a snapshot (value MAY be `unsupported`/`unverified`).
2. Secondary operations, when absent, are treated as `unverified` (not `unsupported`) — absence of a probe is not proof of non-support.
3. New operations MAY be added by later issues; adding one MUST NOT retroactively make older snapshots claim it — a snapshot without the field is `unverified` for it.

### 3.3 Operation ≠ model

An operation being `conditionally_supported` for an account does **not** imply every model supports it. Capability is recorded at **both** operation level (§4) and per-model level (§5). Enforcement (§9) requires **both** the operation and the specific requested model to be affirmed.

---

## 4. Capability status vocabulary (consumed from #3–#5)

### 4.1 Tokens

The snapshot records one of these per operation (and per model per operation). Meaning is owned by evidence #3–#5; this document only locks recording + enforcement.

**Legend ↔ field-token mapping (normative):** the evidence legends (#3–#5) write these as prose values `verified` / `conditionally supported` (space) / `unsupported` / `unverified`. This document records them as snake_case **field tokens** — `verified` / `conditionally_supported` / `unsupported` / `unverified` — to match the operation tokens (`chat_streaming`) and stay JSON-field-safe for #18/#20. The mapping is 1:1 and MUST NOT introduce a fifth value or change any legend meaning. `conditionally_supported` ≡ evidence `conditionally supported`.

| Token | Recording meaning | Offerable by default? |
|---|---|---|
| **`verified`** | Confirmed against an official/upstream surface or a unit-tested protocol path (#3–#5 legends) | Yes |
| **`conditionally_supported`** | Works via reference-learned path or under conditions (plan/tier/prompt) not fully guaranteed; the dominant real-world status across the corpus | Yes, subject to freshness + risk gates |
| **`unsupported`** | No path exists in reference or product for this account/mode (e.g. inpaint on Gemini/Grok) | **No** — hard non-offerable |
| **`unverified`** | Not yet probed / no evidence either way (e.g. Antigravity image gen, ChatGPT Web cancel) | **No** — MUST probe before offering |

**Offerable set = { `verified`, `conditionally_supported` }.** `unsupported` and `unverified` are **never** offerable and MUST be rejected before upstream execution for capability-bearing ops (§9). This is the concrete meaning of "unsupported … capability is rejected before upstream execution" (AC).

### 4.2 No status inflation

- A successful probe MAY move `unverified` → `verified`/`conditionally_supported` for the probed operation+model **only**. It MUST NOT bulk-promote other operations or models.
- The gateway MUST NOT record `verified` for a capability learned only from reference code; the strongest such status is `conditionally_supported` (#3–#5: "no row is product-`verified` without live probe").
- A snapshot capability status MUST NOT change the Auth Mode **risk** status (#7 §2.2: "Gateway MUST NOT promote risk status because a probe succeeded"; #7 §7 item 2).

### 4.3 Baseline capability facts from evidence (informative starting matrix)

This is the **evidence baseline** (#3–#5), not a runtime guarantee; every account still needs its own probe. Recorded here so downstream issues do not re-derive it. `cond` = `conditionally_supported`.

| Auth Mode | `chat` | `chat_streaming` | `image_generation` | `image_edit` | `inpaint` |
|---|---|---|---|---|---|
| ChatGPT Web Access | cond | cond | cond | cond | cond (client-composite mask) |
| ChatGPT Codex OAuth | cond | cond | cond | cond | cond (stronger mask field) |
| Gemini Web Cookie | cond | cond (**synthetic**) | cond | cond | **unsupported** |
| Gemini Antigravity OAuth | cond | cond (true SSE) | **unverified** | **unverified** | **unsupported** |
| Grok Web SSO | cond | cond | cond | cond | **unsupported** |
| Grok xAI OAuth | cond | cond | cond | cond | **unsupported** |

Notes locked by evidence:

1. No mode reaches product-`verified` end-to-end without a live account probe.
2. `inpaint` is `unsupported` everywhere except the two ChatGPT modes; the gateway MUST NOT synthesize inpaint on the four unsupported modes.
3. Gemini Antigravity image gen/edit are `unverified` — MUST be probed per candidate model before offering, never assumed from the mode.
4. Gemini Web Cookie streaming is synthetic; snapshots MUST mark it so #11/#12 do not promise token-level latency it cannot deliver.

---

## 5. Model availability

### 5.1 Observed slugs only

1. A snapshot's model list MUST be **observed** for that Provider Account (probe-time discovery: e.g. ChatGPT `/models`, Gemini scraped `/app` slugs or Antigravity `fetchAvailableModels`, Grok tier-filtered catalog + `/models`), never a static provider catalog copied in (#3–#5).
2. If discovery fails, the model list is **empty/degraded**, not invented — "do not invent models" (#4). An empty model list makes affected operations non-offerable until re-probe.
3. Model slugs are account/session/entitlement dependent; two accounts on the same Auth Mode MAY legitimately expose different models.

### 5.2 Per-model record (logical fields)

| Field | Required | Notes |
|---|---|---|
| `model_slug` | yes | Observed identifier as returned upstream (e.g. `gpt-image-2`, `codex-gpt-image-2`, `grok-imagine-image-quality`, `gemini-*`) |
| `operations` | yes | Map of operation token → capability status (§4) for this model |
| `entitlement_hint` | when known | Non-secret tier/plan gate (e.g. Grok `min_tier` Basic/Super/Heavy; Codex Plus/Team/Pro; Antigravity `currentTier`) |
| `surface_binding` | when relevant | Which upstream surface/base-URL family the model was observed on (e.g. Grok `cli-chat-proxy` vs `api.x.ai`; ChatGPT web vs codex image surface). Prevents cross-surface assumption (#3/#5) |
| `observed_at` | yes | When this model entry was last observed |
| `offerable` | derived | §5.3 |

### 5.3 Offerable computation

A model+operation pair is **offerable** to a client iff **all**:

1. Snapshot freshness is `fresh` (§6).
2. The per-model operation status is in the offerable set (`verified` / `conditionally_supported`) — §4.1.
3. The Provider Account is **usable** per #9 §5.1 `I-USABLE-GATE` (this document does not restate that conjunction).
4. The Auth Mode is risk-permitted for this deployment/Tenant (#7): not `prohibited`; `gated` needs flag+ack; `experimental` lab-only.
5. No matching `cooling_down/provider_quota_exhausted` condition or `entitlement_drift` blocks the model/operation (§8).

Offerable is **derived**, never a stored authority that can outlive its inputs. A client-facing model list (#18/#20) is the set of offerable pairs.

---

## 6. Freshness

### 6.1 Freshness states

| State | Meaning | Authorizes capability-bearing op? |
|---|---|---|
| **`fresh`** | `verified_at` within the applicable TTL class and no invalidation trigger fired | **Yes**, if §5.3 also holds |
| **`stale`** | TTL class elapsed; no hard invalidation, but facts are old | **No** (MVP fail-closed); SHOULD trigger background re-probe; MUST re-verify before authorizing |
| **`invalid`** | An invalidation trigger fired (§8) or a durable §9-linked gate failed (#9 `I-SNAPSHOT-NONUSE`) | **No**, unconditionally |

Cause → effect (the hard vs soft split):

1. A snapshot verified 2×TTL ago with no drift signal is `stale`: the gateway does not *trust* it for new work, but it is not evidence of breakage — the fix is a cheap re-probe, and the prior facts MAY seed the new probe.
2. A snapshot whose account was just `disabled`/`revoked` (#9), or whose canonical health became `degraded|blocked/protocol_drift`, is `invalid`: the facts are known-bad, re-probe is mandatory, and no grace applies.

MVP locks **`stale` = non-authorizing** for capability-bearing ops (fail-closed). A dual "grace window" where `stale` still authorizes low-risk ops is deferred (`D-SNAPSHOT-GRACE`).

### 6.2 TTL classes (named; numbers are #17-tunable)

Numeric minutes are **not** frozen here (like #7 threshold constants); implementations MUST cite these class ids, not invent parallel magic numbers:

| TTL class id | Applies to | Intent |
|---|---|---|
| `TTL-PROBE-LIVE` | Freshly live-probed `verified`/`conditionally_supported` facts | Longest budget |
| `TTL-INHERITED` | Facts carried by #9 §4.8 rule 3 refresh inheritance (no new probe) | Shorter than a fresh probe; SHOULD re-probe sooner |
| `TTL-DISCOVERY` | Model-list/model-availability observations | May differ from capability-status TTL; discovery drifts faster (HTML scrape, tier gates) |
| `TTL-DEGRADED` | Facts recorded while operational health was `degraded` | Shortest; re-verify aggressively |

Rules:

1. `verified_at` + TTL class → derived `fresh_until`. Past `fresh_until` with no trigger → `stale`.
2. Web-scrape-derived discovery (Gemini `SNlM0e`/regex, Grok Statsig-dependent catalog) SHOULD use a shorter `TTL-DISCOVERY` because those surfaces drift silently (#4/#5).
3. #17 owns the numbers and MAY retune them; it MUST NOT remove the re-verify obligation for `stale`/`invalid`.

### 6.3 Refresh triggers (move toward re-probe)

A background or inline re-probe SHOULD/MUST run when:

1. `fresh_until` elapsed (`stale`) — SHOULD (background); MUST before authorizing.
2. A `credential_version` bump occurred (#9 §4.8/§4.9) without inheritance — MUST re-probe before the new version authorizes work.
3. An invalidation trigger fired (§8) — MUST re-probe before returning to `fresh`.
4. Tenant-triggered re-probe (#9 §4.6 "Tenant-triggered re-probe") — allowed with `accounts.manage`.

All re-probes obey #9 `I-PROBE-MINIMAL` (cheapest auth/capability-proving path; no billable renders) and run only on same-Tenant authorized paths (#6).

---

## 7. Provenance

### 7.1 Evidence class per fact

Each capability fact (operation-level and model-level) carries a provenance so routing/UI never infer capability from a Provider or model name:

| Evidence class | Meaning | Max snapshot status it may justify |
|---|---|---|
| `reference_learned` | Learned from `.ref/*` reverse-engineering only | `conditionally_supported` |
| `upstream_verified` | Confirmed against official upstream product/docs surface | up to `verified` |
| `live_probe` | Confirmed by a live authenticated probe on **this** account | up to `verified` |
| hybrid (e.g. `reference_learned+upstream_verified`) | Mixed; record both | if hybrid **includes** `live_probe` on this account, the live-probe class governs (up to `verified` for the probed fact); otherwise governed by the weaker non-probe class for runtime trust |

Rules:
1. A fact whose only evidence class is `reference_learned` MUST NOT be recorded as `verified` (§4.2).
2. Live-probe on the actual account is what upgrades trust for that account — a hybrid that includes `live_probe` is not capped by any weaker co-recorded class for the probed fact.
3. Hybrid without `live_probe` (e.g. `reference_learned+upstream_verified`) is capped by the weaker class so docs/upstream confirmation alone cannot outrank a live account probe.

### 7.2 Probe surface identifier

A snapshot fact SHOULD record the probe surface it was confirmed against (e.g. `/backend-api/models`, `loadCodeAssist`, `/rest/rate-limits`, `GET /models`) plus, where dual surfaces exist, the bound family (§5.2 `surface_binding`). This lets #11/#18 diagnose stale/drift without re-reading evidence docs and prevents "probed api.x.ai, assumed cli-chat-proxy" mistakes (#5).

### 7.3 Redaction

Provenance and probe identifiers are **non-secret** metadata. A snapshot payload MUST NOT contain Provider Credential material (tokens, cookies, SSO material) — binding on #9 §9.1 (Capability Snapshot payloads are explicitly listed as MUST-NOT-leak) and #7 OP-G3.

---

## 8. Invalidation and refresh triggers

### 8.1 Trigger classes

| Trigger class | Fires when | Effect on snapshot |
|---|---|---|
| **`entitlement_drift`** | Plan/tier/quota change: image quota exhausted with reset (`chatgpt`), `QUOTA_EXHAUSTED`/`SERVICE_DISABLED` (`gemini`), tier downgrade / weekly-credit exhaustion (`grok`), free-plan image tool absence (`chatgpt`) | Affected operation/model → non-offerable; snapshot `stale`/`invalid` for those facts; re-probe to reclassify. Quota-with-reset is temporary (`cooling_down/provider_quota_exhausted`), not `reauth` |
| **`credential_change`** | `credential_version` bump via refresh/reauth (#9 §4.8/§4.9), or credential revoke | Snapshot bound to the old version is superseded; MUST NOT authorize new-version work without re-probe or #9 §4.8 rule 3 inheritance |
| **`protocol_drift`** | Upstream shape/schema change: SSE/patch schema, model-slug rename, HTML key rename (`SNlM0e`), Statsig signer drift, request-envelope rename (#3–#5 drift tables). Maps to canonical Health Reason `protocol_drift` with state `degraded` or qualified `blocked` under #17 | Affected capability/model facts → `invalid`; MUST re-probe; MAY degrade/block the operation according to #17 |
| **`explicit_purge`** | Operator/Tenant/vault explicit invalidation, or #9 durable-gate failure (`I-SNAPSHOT-NONUSE`) | Snapshot `invalid` immediately; non-authorizing regardless of TTL |
| **`ttl_expiry`** | `fresh_until` elapsed with no other trigger | Snapshot `stale`; re-probe path |

### 8.2 Health-condition mapping (which health invalidates vs degrades)

Consumes #17 canonical Health State + Health Reason; #10 does not add health tokens:

| Canonical matching health condition | Effect on snapshot capability |
|---|---|
| `healthy/*` | No effect |
| `degraded/*` except protocol drift | Facts recorded/kept under `TTL-DEGRADED`; SHOULD re-probe sooner; MAY keep offering `conditionally_supported` ops with caution (#11) |
| `expired/*`, `blocked/provider_account_banned`, `challenged/challenge_detected` | Durable hard-block (#9 §5.1 item 3) → account non-usable → snapshot **non-authorizing** (`I-SNAPSHOT-NONUSE`) |
| `cooling_down/provider_quota_exhausted` | `entitlement_drift` for affected ops; temporary; models non-offerable until authorized recovery after reset |
| `cooling_down/provider_rate_limited` | Matching scope is temporarily non-routable under #17; capability facts remain valid unless separate evidence invalidates them |
| `degraded/protocol_drift` or `blocked/protocol_drift` | `protocol_drift` trigger (§8.1) → affected facts `invalid`; blocked form is also non-routable |
| `unknown/initial_unprobed` | Not sufficient to offer; treat affected facts as needing (re)probe |

### 8.3 Immediate non-use precedence

When #9 `I-SNAPSHOT-NONUSE` applies (durable §5.1 items 1–5 fail — `disabled`/`revoked`/`deleted`/`reauth_required`/hard-block health/vault-revoke), the snapshot is **non-authorizing for new work immediately**, even if `fresh_until` has not elapsed. This overrides any freshness computation. Request-time item 6 (key scope/allowlist) alone MUST NOT invalidate a snapshot for other principals or system paths (#9 §5.1 item 7).

---

## 9. Enforcement (reject before upstream execution)

### 9.1 Capability gate wiring (#9 §5.1 item 7)

For a capability-bearing request naming operation `op` and model `m` on a usable account:

1. Resolve the account's current Capability Snapshot (bound to current `credential_version`).
2. If no snapshot exists, or snapshot is `stale`/`invalid` → **reject before upstream** (fail-closed), remediation drives a re-probe / retry-after path (§10). MUST NOT "call upstream to see if it works" as a substitute for the probe state machine (#9 §5.2).
3. If `op` is not present or its status is `unsupported`/`unverified` → **reject before upstream** with a stable capability class (e.g. `capability_unsupported`).
4. If model `m` is not in the observed model list, or its per-model `op` status is not offerable → **reject before upstream** (`model_unavailable` class).
5. Otherwise capability is satisfied; #9 §5.1 items 1–6 still all apply. This document is item 7 only.

`inpaint` specifically: a masked request on a mode where `inpaint` is `unsupported` (all Gemini/Grok modes, §4.3) MUST be rejected **before** upstream and MUST NOT be silently downgraded to `image_edit` (parent #1 mask fidelity; #3–#5).

### 9.2 Where enforcement lives

- Capability enforcement is a **request-time** gate (#9 §5.1 item 7), not a durable lifecycle field.
- It runs after Client API Key admission (#8) and account usability (#9 §5.1 items 1–6), before Adapter execution / Render Job enqueue.
- System jobs acting under the account's `tenant_id` are still bound by capability facts (a background job cannot use an `unsupported` op).

### 9.3 Ownership and non-enumeration

All snapshot reads obey #6:

- Foreign account id → **404-class**; zero snapshot disclosure, zero vault decrypt, zero Adapter call.
- Same-Tenant insufficient scope → **403-class**.
- Snapshot read requires `accounts.read` (or `capabilities.read` when #18 splits it); mutation of capability facts (force re-probe) requires `accounts.manage` (§11 / #8).

---

## 10. Remediation classes (Tenant-facing)

Capability failures return safe guidance (exact error code strings #16). These extend #9 §7 remediation vocabulary; they MUST obey redaction (§7.3):

| Remediation class | When | Tenant/UI action |
|---|---|---|
| `capability_unsupported` | operation `unsupported` for this Auth Mode/model | Choose a supported operation/model; e.g. no inpaint on Gemini/Grok |
| `capability_unverified` | operation `unverified` (not probed) | Wait for / trigger probe; do not assume support |
| `snapshot_stale` | snapshot past TTL | Re-probe (auto or `accounts.manage`); retry after |
| `model_unavailable` | model not observed / not offerable now | Pick an offerable model; may be entitlement/quota gated |
| `wait_provider_cooldown` | matching `cooling_down/provider_quota_exhausted` or `cooling_down/provider_rate_limited` condition (#17) | Wait until the finite safe recovery time; do not infer reauth |
| `reauthenticate` | capability blocked by durable hard-block/`reauth_required` (#9) | Run reauth journey (#9 §4.9) |
| `auth_mode_unavailable` | Auth Mode `prohibited`/flag-off/non-lab (#7/#9) | Choose another Auth Mode |

---

## 11. Authorization surface (#8 scope mapping)

| Operation | Minimum scope |
|---|---|
| Read own account Capability Snapshot / offerable models | `accounts.read` (or `capabilities.read` if #18 splits it) |
| Force re-probe / invalidate snapshot | `accounts.manage` |
| Use a model/operation for inference | Inference scopes (`chat.*` / `images.*`) + offerable capability; not `accounts.manage` |

Default inference keys **exclude** `accounts.manage` (#8) so a leaked inference key cannot force re-probes or mutate capability facts. Snapshot reads are Tenant-scoped (#6); operators get only the same safe fields under future break-glass (#7 OP-G3), never credential material.

---

## 12. System jobs related to snapshots

| Job | Purpose | Constraints |
|---|---|---|
| Capability probe/refresh worker | Mint/refresh snapshots; reclassify on drift | Same-Tenant (#6); `I-PROBE-MINIMAL` (#9); MUST NOT mark usable without #9 §5.1; MUST NOT promote risk (#7) |
| Discovery worker | Re-observe model lists | Observed slugs only; empty on failure, never invented (#4) |
| Invalidation reconciler | Apply §8 triggers, `I-SNAPSHOT-NONUSE` on durable-gate failure | Immediate non-use precedence (§8.3) |

Workers act only with the resource's `tenant_id` (#6 §2.4).

---

## 13. Security impact summary

| Defect | Impact |
|---|---|
| Offer an `unsupported`/`unverified` capability | Upstream failures, wasted quota, false client promises; inpaint silently degraded to edit (mask fidelity loss) |
| Authorize on a `stale`/`invalid` snapshot | Routing to a capability that no longer exists; drift-induced failures |
| Invent models from static catalog | Client selects a model the account cannot use (#3–#5 "do not invent models") |
| Carry snapshot across `credential_version` bump | Authorizing work on unverified entitlements after refresh/reauth |
| Snapshot promotes risk status | Violates #7 orthogonality; may expose `prohibited`/`experimental` surfaces |
| Leak credential material in snapshot payload | Full Provider account takeover (#9 §9) |
| Cross-Tenant snapshot read | Enumeration / capability oracle (#6) |
| Assume one Grok base-URL family from the other | Wrong model set; missed/failed calls (#5 dual base-URL) |
| Treat synthetic streaming as real | Latency/UX promises the mode cannot keep (#4 Gemini Web) |

---

## 14. Test obligations

Exact harness arrives with contract prototypes (#18–#20). Required observable cases for this issue:

### 14.1 Taxonomy and status

1. A snapshot for each Auth Mode classifies all five primary operations (`chat`, `chat_streaming`, `image_generation`, `image_edit`, `inpaint`).
2. `inpaint` on Gemini Web Cookie / Gemini Antigravity / Grok Web SSO / Grok xAI is `unsupported`; a masked request is rejected **before** upstream and not downgraded to `image_edit`.
3. Gemini Antigravity `image_generation`/`image_edit` default `unverified`; not offered until a per-model probe reclassifies.
4. Gemini Web Cookie `chat_streaming` is marked synthetic; a client cannot be promised token-level streaming.

### 14.2 Model availability

5. Model list is populated from observed slugs; a discovery failure yields an empty list and non-offerable affected ops, never invented models.
6. Two accounts (Web + OAuth) same Tenant, same human keep independent model lists and capability facts.
7. Grok xAI OAuth snapshot records the bound base-URL family; api.x.ai facts do not authorize cli-chat-proxy assumptions.

### 14.3 Freshness and invalidation

8. `verified_at` + TTL class → `fresh_until`; past it → `stale`; capability-bearing request on `stale` is rejected before upstream with `snapshot_stale` and triggers re-probe.
9. `credential_version` bump without #9 §4.8 inheritance → prior snapshot superseded; new-version work rejected until re-probe.
10. `degraded/protocol_drift` or `blocked/protocol_drift` → affected facts `invalid`; re-probe required.
11. `entitlement_drift` (quota exhausted / tier downgrade / `SERVICE_DISABLED`) → affected model/op non-offerable; temporary quota case restores after reset hint without reauth.
12. Durable §5.1 items 1–5 fail (`disabled`/`revoked`/`deleted`/`reauth_required`/hard-block) → snapshot non-authorizing immediately even within TTL (`I-SNAPSHOT-NONUSE`); request-time key-scope failure alone does not kill the snapshot for other principals.

### 14.4 Orthogonality and provenance

13. A successful probe does not change Auth Mode risk status (#7); a snapshot can exist in lab for an `experimental` mode without making it `allowed`.
14. A capability whose only evidence is `reference_learned` is never recorded `verified`.
15. Snapshot payloads, list/get, logs, and audit contain no credential material.

### 14.5 Enforcement and scope

16. `unsupported`/`unverified` op → reject before upstream (`capability_unsupported`/`capability_unverified`); Adapter executions = 0.
17. Foreign account id snapshot read → 404-class; disclosure = 0.
18. Key with only `accounts.read` cannot force re-probe/invalidate; default inference key cannot mutate capability facts (#8).

---

## 15. Core invariants (normative checklist)

1. **I-CAP-PER-ACCOUNT** — A Capability Snapshot belongs to exactly one Provider Account (one Tenant, one Auth Mode) and one `credential_version`; never shared or cross-Tenant (#6, #9 §1.5).
2. **I-CAP-OBSERVED-ONLY** — Model availability lists observed slugs only; discovery failure yields empty/degraded, never invented models (#3–#5).
3. **I-CAP-OP-TAXONOMY** — Every snapshot classifies the five primary operations; `chat_streaming` and `inpaint` are first-class, never flags or silent downgrades.
4. **I-CAP-STATUS-SOURCE** — Capability status tokens (`verified`/`conditionally_supported`/`unsupported`/`unverified`) are consumed from #3–#5; a reference-only fact is at most `conditionally_supported`.
5. **I-CAP-OFFERABLE** — Only `fresh` + offerable-status (`verified`/`conditionally_supported`) + usable (#9 §5.1) + risk-permitted (#7) model/op pairs are offerable.
6. **I-CAP-NO-STALE-USE** — `stale` and `invalid` snapshots MUST NOT authorize capability-bearing operations (MVP fail-closed; `stale` triggers re-probe).
7. **I-CAP-VERSION-BIND** — A snapshot bound to an old `credential_version` MUST NOT authorize work on a new version without re-probe or #9 §4.8 rule 3 inheritance.
8. **I-CAP-INVALIDATE** — `entitlement_drift`, `credential_change`, `protocol_drift`, `explicit_purge`, and #9 durable-gate failure move a snapshot to non-authorizing; `I-SNAPSHOT-NONUSE` takes immediate precedence over TTL (§8.3).
9. **I-CAP-REJECT-BEFORE-UPSTREAM** — Unsupported/unverified/not-fresh capability is rejected before Adapter execution; no "call upstream to check" substitute (#9 §5.2). This is #9 §5.1 item 7 only, not a second usability conjunction.
10. **I-CAP-RISK-ORTHOGONAL** — A snapshot never promotes or implies Auth Mode risk-status acceptance; a probe success does not move `gated`/`experimental`/`prohibited` (#7 §2.2/§7).
11. **I-CAP-PROVENANCE** — Every capability fact carries an evidence class and (SHOULD) a probe-surface identifier; routing/UI never infer capability from Provider or model name.
12. **I-CAP-REDACT** — Capability Snapshot payloads never contain Provider Credential material (#9 §9.1, #7 OP-G3).
13. **I-CAP-SCOPE** — Snapshot read requires `accounts.read`/`capabilities.read`; forcing re-probe/invalidation requires `accounts.manage`; default inference keys have neither (#8).
14. **I-CAP-PROBE-MINIMAL** — Capability probes are auth/capability-proving and cost-minimal; no billable renders (#9 `I-PROBE-MINIMAL`).
15. **I-CAP-SURFACE-BIND** — Where an Auth Mode has divergent surfaces (Grok cli-chat-proxy vs api.x.ai; ChatGPT web vs codex image), the snapshot records the bound surface per model; one family's facts do not authorize the other (#3/#5).

---

## 16. Open follow-ups (explicitly deferred)

| Topic | Issue | Constraint retained here |
|---|---|---|
| Exact TTL numeric values, probe schedules, cooldown timers | #17 | Named TTL classes + re-verify obligation locked here; #17 may retune numbers, not remove the obligation |
| Routing candidate filters, leases, fallback using offerable set | #11 | Only offerable + usable same-Tenant pairs; no cross-mode/cross-surface assumption |
| Chat/streaming execution consuming snapshot | #12 | Streaming class (real vs synthetic) locked here |
| Asset/image op execution consuming snapshot | #13/#14 | image gen/edit/inpaint taxonomy + `unsupported` inpaint locked here |
| Vault crypto; credential material handling | #15 | Snapshots never contain material (§7.3) |
| Canonical error code strings / problem+json | #16 | Capability status/remediation **classes** locked here |
| JSON schema, field names, OpenAPI paths, `capabilities.read` split | #18 / #20 | Logical fields + semantics locked here |
| `stale`-grace window for low-risk ops | reopen `D-SNAPSHOT-GRACE` | MVP: `stale` non-authorizing (fail-closed) |
| Per-Tenant probe/discovery rate budgets | reopen `D-PROBE-RATE` (shared with #9) | Probes cost-minimal and non-hammering |

---

## 17. ADR decision

No new ADR. Capability Snapshot ownership and semantics were introduced in `CONTEXT.md` and locked as #9's deferral to #10. This document is the durable normative expansion under `docs/spec/` for capability taxonomy, model availability, freshness, and invalidation.

An ADR **would** be warranted if the product later introduced:

- shared Capability Snapshots across Tenants (forbidden),
- static provider catalogs as authoritative capability (forbidden; observed-only),
- capability probes that promote risk status (forbidden; #7 orthogonality),
- or authorizing execution on `stale`/`invalid` snapshots by default.

---

## 18. Acceptance criteria traceability

| AC (issue #10) | Where satisfied |
|---|---|
| Taxonomy distinguishes chat, streaming, image generation, image edit and inpaint | §3.1, §4.3, §14.1, `I-CAP-OP-TAXONOMY` |
| Snapshot has capability status, model availability, verified-at, freshness and evidence/probe provenance | §2, §4, §5, §6, §7, §14.1–§14.2/§14.4 |
| TTL, invalidation and refresh triggers defined for entitlement, credential and protocol drift | §6.2, §6.3, §8, §14.3, `I-CAP-INVALIDATE` |
| Unsupported or stale capability rejected before upstream execution when possible | §4.1, §6.1, §9, §14.3/§14.5, `I-CAP-NO-STALE-USE`, `I-CAP-REJECT-BEFORE-UPSTREAM` |

---

## 19. Constants and reopen ids

| Id | Meaning |
|---|---|
| `TTL-PROBE-LIVE` / `TTL-INHERITED` / `TTL-DISCOVERY` / `TTL-DEGRADED` | Named freshness budgets (§6.2); numbers #17-tunable |
| Capability status | `verified` / `conditionally_supported` / `unsupported` / `unverified` (owned by #3–#5) |
| Trigger classes | `entitlement_drift` / `credential_change` / `protocol_drift` / `explicit_purge` / `ttl_expiry` (§8) |
| `I-SNAPSHOT-NONUSE` | Owned by #9; immediate non-use on durable §5.1 items 1–5 fail; item 6 alone is not a snapshot kill |
| `I-USABLE-GATE` | Owned by #9 §5.1; capability is item 7 only |
| `D-SNAPSHOT-GRACE` | Reopen if a `stale`-grace window for low-risk ops is desired |
| `D-PROBE-RATE` | Reopen for numeric per-Tenant probe/discovery budgets (shared with #9) |
| Risk statuses / kill signals | Owned by #7 (`allowed`/`gated`/`experimental`/`prohibited`; `KS-*`/`FG-*`/`OP-G*`/`R0`–`R4`) |

---

## 20. Document control

| Field | Value |
|---|---|
| Status | Accepted for specification (issue #10) |
| Check date of evidence inputs | 2026-07-14 |
| Supersedes | n/a (initial capability snapshot / model availability lock) |
| Next review | On #7 status changes, #9 credential-lifecycle changes, #17 TTL tuning, or any Auth Mode capability-class break (#3–#5 re-verification) |
| Authors | Spec decision agent for issue #10 |
