# Gemini Auth Mode Capability Evidence

- Issue: [#4 — Verify Gemini Web Cookie and Gemini Antigravity OAuth](https://github.com/monet88/pixelplus/issues/4)
- Parent: [#1](https://github.com/monet88/pixelplus/issues/1)
- Wayfinder: `wf-0004-gemini-web-production-surface`
- Access date for external URLs: **2026-07-14**
- Scope: specification evidence only. No Gateway implementation. No risk-acceptance decisions (issue #7). No Tenant auth design (issue #6).

## 1. Purpose and status vocabulary

This note records **observable capability and credential-lifecycle evidence** for two independent Gemini Auth Modes:

| Auth Mode | Domain term (`CONTEXT.md`) | Upstream surface |
|---|---|---|
| **Gemini Web Cookie** | Web Access via consumer web cookies | `gemini.google.com` Bard/Gemini Web UI reverse surface |
| **Gemini Antigravity OAuth** | OAuth/CLI Access via Google OAuth | Cloud Code / Antigravity gateway `cloudcode-pa.googleapis.com` (`v1internal`) |

Parent #1 locks the rule that **Web Access and OAuth/CLI Access are independent execution surfaces even when they share one external Google identity**. Capability claims below are therefore **per Auth Mode**, never merged under the label “Gemini”.

Status values (only these four):

| Status | Meaning |
|---|---|
| `verified` | Observed in local reference code **and** consistent with official docs or an explicit, unit-tested protocol path that does not invent capability |
| `conditionally supported` | Reference implements a path, but success depends on entitlement, model, plan, region, prompt shape, or probe not yet run |
| `unsupported` | No first-class upstream or reference path for the capability as PixelPlus defines it |
| `unverified` | Plausible from product marketing or partial code, but not confirmed end-to-end for this Auth Mode |

**Evidence class labels used throughout:**

- **reference-learned** — behavior reconstructed from local `.ref/*` code, fixtures, or unit tests. Not a production promise.
- **upstream-verified** — behavior stated by official Google pages accessed 2026-07-14, or by a Google-owned product surface that documents the feature.
- **unverified live** — requires a live probe/prototype against a real Provider Account.

Reference repositories are **research seams**, not product guarantees (parent #1).

---

## 2. Surface separation (independent Provider Accounts)

| Dimension | Gemini Web Cookie | Gemini Antigravity OAuth |
|---|---|---|
| Product family | Gemini consumer app / Gemini Apps | Antigravity / Gemini Code Assist unified gateway (Cloud Code PA) |
| Credential | Web cookie jar (`__Secure-1PSID`, `__Secure-1PSIDTS`, plus derived session token `SNlM0e`/`at`) | Google OAuth `access_token` + `refresh_token` for Antigravity client |
| Host | `gemini.google.com`, `accounts.google.com`, `content-push.googleapis.com` | `oauth2.googleapis.com`, `cloudcode-pa.googleapis.com`, `daily-cloudcode-pa.googleapis.com` |
| Protocol shape | Browser form POST + nested Bard JSON (`f.req`, `at`) | Gemini-style JSON envelope `{ project, model, request: { contents… } }` over `v1internal` |
| Official API terms | Gemini Apps / consumer ToS and privacy hub | Distinct from public Gemini Developer API terms; OAuth client is IDE/Antigravity-class |
| Public Gemini API | **Not** this Auth Mode | **Not** the public `generativelanguage.googleapis.com` API-key surface |
| Quota / entitlement | Consumer plan (Free / AI Pro / Ultra) and Gemini app usage limits | Code Assist / Antigravity project tier, model list, and optional `GOOGLE_ONE_AI` credits |
| Local reference | `.ref/gemini-web-to-api` (ntthanh2603/gemini-web-to-api) | `.ref/CLIProxyAPI` Antigravity auth + executor + translators |

**Cause → effect example.** Same Google email can hold a healthy Gemini Web Cookie Provider Account and a healthy Gemini Antigravity OAuth Provider Account. Exhausting Gemini app image quota on Web does **not** automatically mark Antigravity chat models exhausted, and vice versa. Gateway must never silently fallback between these Auth Modes.

---

## 3. Capability matrix (required rows)

Statuses are **Auth Mode capabilities as PixelPlus may later probe**, not “does Google market image generation somewhere”.

### 3.1 Gemini Web Cookie

| Capability | Status | Evidence summary | Evidence class |
|---|---|---|---|
| chat non-streaming | `conditionally supported` | Reference `Client.GenerateContent` POSTs to `…/StreamGenerate` with `at` + `f.req`, waits for full body, parses text. Success depends on valid cookies, model availability scraped from `/app`, and absence of BardErrorInfo. | reference-learned |
| chat streaming | `conditionally supported` | Upstream endpoint name is `StreamGenerate`, but the Go client reads the **entire** HTTP body then optionally **simulates** SSE by chunking text (`GenerateContentStream` / OpenAI stream path). True token-by-token upstream streaming is **not** exposed by the reference adapter. Client-facing streaming is therefore synthetic. | reference-learned |
| image generation | `conditionally supported` | Consumer product markets Nano Banana image generation in the Gemini app. Reference implements OpenAI `/images/generations` by prompting chat generation and harvesting `googleusercontent.com` image URLs from the Bard payload. Success is plan/model/prompt dependent; not a dedicated image API. | reference-learned + upstream-verified (product marketing) |
| image edit | `conditionally supported` | Consumer product supports upload + edit in Gemini app. Reference can attach uploaded files and prompt for edits, then harvest output URLs. No dedicated mask/edit endpoint; quality and reliability unverified live. | reference-learned + upstream-verified (product marketing) |
| inpaint | `unsupported` | No mask field, no inpaint-specific request shape in reference or consumer docs reviewed. Photoshop-style masked inpaint is not a first-class Web Cookie operation. | reference-learned |
| model discovery | `conditionally supported` | Models scraped via regex from Gemini `/app` HTML (`gemini-*` / `gemini-advanced`). List is opportunistic, not an official catalog API; empty list when page layout drifts. | reference-learned |
| multi-turn continuity | `conditionally supported` | `GeminiChatSession` keeps `ConversationID` / choice metadata (`cid`, `rcid`) and resends them in subsequent `f.req` payloads. Continuity fails if metadata missing or session expired. | reference-learned |
| attachments / files / images input | `conditionally supported` | Multipart upload to `content-push.googleapis.com/upload` with `X-Tenant-Id: bard-storage` and `Push-ID`; file IDs injected into generate payload. Size/type limits and entitlement gates not live-probed. | reference-learned |
| cancel / abort | `conditionally supported` | HTTP request uses `context.Context`; cancel stops the client wait. No evidence of a durable server-side cancel API for an already-running Bard generation. | reference-learned |
| tool calling | `conditionally supported` | Reference bridges tools by prompt engineering (“tool bridge”) and parsing JSON from free text. Not native Gemini Web function-calling protocol. Reliability is model-behavior dependent. | reference-learned |

### 3.2 Gemini Antigravity OAuth

| Capability | Status | Evidence summary | Evidence class |
|---|---|---|---|
| chat non-streaming | `conditionally supported` | Executor POSTs `Bearer` token to `/v1internal:generateContent` with project + model envelope. Unit tests and pure-Go path exist; live entitlement still required. | reference-learned |
| chat streaming | `conditionally supported` | Executor POSTs `/v1internal:streamGenerateContent?alt=sse`, scans SSE lines, translates chunks. Tests cover interactions streaming path. Real-time stream is first-class (unlike Web Cookie simulation). | reference-learned |
| image generation | `unverified` | Translators can pass through `inlineData` image parts in model responses, but OpenAI images handlers in CLIProxyAPI route image tools to Codex/xAI/compat models — **not** Antigravity. Official public Gemini API documents image models; that is a **different surface**. Antigravity model catalog may include image-capable IDs for some accounts; not confirmed for this Auth Mode. | reference-learned + official API distinction |
| image edit | `unverified` | Same as generation: no dedicated Antigravity image-edit path in reference; conversational multimodal edit would require live model that accepts image inputs and returns images. | reference-learned |
| inpaint | `unsupported` | No mask/inpaint schema in Antigravity executor or translators. | reference-learned |
| model discovery | `conditionally supported` | `POST /v1internal:fetchAvailableModels` with project; utility `cmd/fetch_antigravity_models` and SDK hints (`webSearchModelIds`). Returned set is entitlement-specific; some internal IDs filtered. | reference-learned |
| multi-turn continuity | `conditionally supported` | Standard Gemini `contents[]` roles; reasoning/tool turns may require `thoughtSignature` / functionCall replay cache for some models. Continuity is request-history based, not Web `cid`. | reference-learned |
| attachments / files / images input | `conditionally supported` | Multimodal parts (`inlineData` / text) supported in Gemini-style request translation. Large file storage APIs of Gemini Developer API are **not** this surface. | reference-learned |
| cancel / abort | `conditionally supported` | Stream loop respects `ctx.Done()` and closes the body. No separate cancel RPC observed. | reference-learned |
| tool calling | `conditionally supported` | Native `functionCall` / `functionResponse` parts, toolConfig modes (`AUTO`, `VALIDATED` for Claude via Antigravity), schema sanitization, and reasoning-replay for tool rounds. Coverage is strong in unit tests; still entitlement/model dependent. | reference-learned |

---

## 4. Credential lifecycle

### 4.1 Gemini Web Cookie

#### Credential types (conceptual — never store real secrets in docs/fixtures)

| Artifact | Role | Notes |
|---|---|---|
| `__Secure-1PSID` | Long-lived Google session cookie | Primary identity proof for consumer Google properties |
| `__Secure-1PSIDTS` | Companion timestamp / rotation cookie | Often required; reference can attempt obtain via `RotateCookies` when missing |
| Derived `SNlM0e` / form field `at` | Short-lived Bard frontend session token | Scraped from `https://gemini.google.com/app` HTML |
| Ancillary cookies (`NID`, etc.) | Browser realism | Merged during init from google.com / gemini.google.com responses |
| Build/session labels (`bl`, `f.sid`, push ID) | Request fingerprint fields | Scraped from init page; attached on generate/upload |

#### Refresh / expiry (observed reference behavior)

1. On init: load cookies → optional `POST https://accounts.google.com/RotateCookies` → GET `/app` → extract `SNlM0e`.
2. Background ticker (default 30 minutes): rotate `__Secure-1PSIDTS`, then re-fetch session token.
3. Rotation may return HTTP 200 **without** a new `Set-Cookie` (cookie still valid) — not treated as failure.
4. Rotation HTTP 401/403 → cookies considered expired; client marked unhealthy; operator must re-export cookies from browser.
5. If `SNlM0e` missing and page contains sign-in language → authentication failed.

**Cause → effect.** Cookie jar valid but `at` stale → generate fails until `refreshSessionToken` succeeds. Cookie jar invalid → neither rotation nor session scrape recovers without human reauthentication.

#### Entitlement

- Consumer Gemini plan (Free / Google AI Pro / Ultra) and feature flags in the Web app determine available models and image tools.
- Reference model list is whatever IDs appear in the HTML of `/app` for **that** session; Advanced models may be absent on free accounts.
- Official product pages document image generation availability in the Gemini app and higher usage limits for paid plans (upstream-verified marketing; not a machine-readable entitlement API).

#### Quota

- No structured quota API in the Web reverse surface.
- Failures surface as HTTP non-200 or `BardErrorInfo` codes; reference maps these generically to “session expiration, rate limits, context window limits, or bot protection/CAPTCHA”.
- Gemini app usage limits (compute-based, refresh windows) are product-level, not exposed as headers to the adapter.

#### Challenge / bot detection

- Browser-like headers required (`User-Agent`, `Origin`, `Referer`, `X-Same-Domain`).
- Reference comments note default Go User-Agent is often blocked on rotation.
- `BardErrorInfo` may indicate CAPTCHA / bot protection; no automated CAPTCHA solver in reference.
- Protocol is reverse-engineered; Google can change HTML keys (`SNlM0e`, model regex) without notice → **protocol drift** risk.

#### Protocol drift

| Drift vector | Symptom | Failure class for later health mapping |
|---|---|---|
| Init HTML key rename | `SNlM0e not found` | auth / protocol_drift |
| Model list regex miss | empty models | degraded discovery |
| Generate payload layout change | parse failure / empty text | protocol_drift |
| Image URL host change | images not collected | capability regression |
| Cookie rotation endpoint change | unhealthy after refresh interval | auth |

### 4.2 Gemini Antigravity OAuth

#### Credential types (conceptual)

| Artifact | Role | Notes |
|---|---|---|
| OAuth `client_id` / `client_secret` | Installed-app Antigravity client constants in reference | Public desktop-app style client; still treat as product config, not end-user secret |
| Authorization code | One-time browser consent result | Local callback port (reference default `51121`) |
| `access_token` | Bearer for Cloud Code PA | Short-lived |
| `refresh_token` | Offline refresh (`access_type=offline`, `prompt=consent`) | Long-lived until revoked |
| `expires_in` / `expired` timestamp | Refresh scheduling | Reference refreshes ~5 minutes before expiry (`RefreshLead`) |
| `project_id` / `cloudaicompanionProject` | Required request field | From `loadCodeAssist` / `onboardUser` |
| Optional credits hint | `paidTier.availableCredits` for `GOOGLE_ONE_AI` | Used when credits retry enabled |

Scopes observed in reference:

- `https://www.googleapis.com/auth/cloud-platform`
- `https://www.googleapis.com/auth/userinfo.email`
- `https://www.googleapis.com/auth/userinfo.profile`
- `https://www.googleapis.com/auth/cclog`
- `https://www.googleapis.com/auth/experimentsandconfigs`

#### Refresh / expiry

1. Login: browser OAuth → code exchange at `https://oauth2.googleapis.com/token` → userinfo → `loadCodeAssist` (and onboard if needed) → persist tokens + project.
2. Before each request: if access token missing or within skew of expiry → single-flight refresh with `grant_type=refresh_token`.
3. Refresh failure (invalid_grant, 401) → account needs reauthentication; do not treat as transient model error.
4. Refresh uses Google token endpoint with Go default User-Agent (reference comment: matches real Antigravity).

#### Entitlement

- Account must be eligible for Antigravity / Code Assist family; official Google docs state Gemini Code Assist individuals / IDE extensions migrate to Antigravity (access date 2026-07-14).
- `loadCodeAssist` returns tier metadata (`allowedTiers`, `currentTier`, paid credits).
- `fetchAvailableModels` returns the model set **for that project/account**, including quota metadata in community reverse docs; reference filters some experimental IDs.
- Models may include Gemini and non-Gemini (e.g. Claude) IDs behind one Antigravity gateway — still one Auth Mode, not Gemini Web.

#### Quota

Reference classifies HTTP 429 / `RESOURCE_EXHAUSTED` into actionable kinds:

| Kind | Observed signal | Adapter behavior in reference |
|---|---|---|
| Instant retry same auth | `RATE_LIMIT_EXCEEDED` + short `retryAfter` | Wait and retry |
| Short cooldown switch auth | Medium `retryAfter` | Mark model/auth cooldown, return 429 to scheduler |
| Full quota exhausted | `QUOTA_EXHAUSTED` or keyword match | Hard fail; optional credits path |
| Soft rate limit | Unknown RESOURCE_EXHAUSTED | Soft retry |
| Credits exhausted | explicit credits balance reason | Permanently disable credits for auth until balance recovers |

Optional `enabledCreditTypes: ["GOOGLE_ONE_AI"]` injects paid credits when configured. Credits balance polled via `loadCodeAssist`.

#### Challenge / bot detection

- Not cookie/CAPTCHA based in the reference path.
- Failures are OAuth errors, HTTP 401/403, `SERVICE_DISABLED` for Cloud Code API, project missing, or model 404.
- User-Agent / Client-Metadata spoofing of Antigravity IDE is part of the reverse protocol; drift in required headers is a protocol risk, not a consumer CAPTCHA challenge.

#### Protocol drift

| Drift vector | Symptom | Failure class |
|---|---|---|
| OAuth client revoked / scope change | login or refresh fails | auth |
| Endpoint host preference change (daily vs prod) | intermittent 404/429 | transient / degraded |
| Request envelope field rename | 400 protocol errors | protocol_drift |
| Thought signature / tool replay rules change | 400 invalid signature | protocol_drift |
| Model ID renames | 404 model | capability snapshot invalidation |
| Credits schema change | wrong quota classification | health mis-map |

---

## 5. Failure modes (for later health / error mapping)

These are **observed classes**, not PixelPlus Public API codes (those belong to later issues).

### 5.1 Shared mapping sketch

| Upstream observation | Suggested future health class | Notes |
|---|---|---|
| Missing/invalid cookie or OAuth refresh failure | `auth_expired` | Reauthentication required |
| 401/403 on generate | `auth` or `challenge` | Web may be CAPTCHA; Antigravity more often pure auth |
| 429 / RESOURCE_EXHAUSTED short | `rate_limited` | Retry / cooldown |
| 429 / QUOTA_EXHAUSTED long | `quota_exhausted` | Account or model unusable until reset |
| BardErrorInfo / parse failure after HTML change | `protocol_drift` | Adapter update needed |
| Empty model list | `degraded` | Discovery failed; do not invent models |
| Context cancel | `canceled` | Client abort |
| 5xx / network | `transient` | Retry policy later |
| Model not in fetched/scraped set | `unsupported` for that snapshot | Reject before spend when possible |

### 5.2 Gemini Web Cookie specific

- `authentication failed: SNlM0e not found` / sign-in HTML → invalid cookies.
- Rotation status 401/403 → mark unhealthy; stop silent retry hammering.
- Generate status ≥500 with retries → transient; non-200 <500 often permanent for that request.
- `BardErrorInfo` codes → opaque; do not over-fit numeric codes without live catalog.
- Simulated streaming cancel only stops local chunk emission after full upstream completion if the client already finished `GenerateContent` — **important**: cancel during synthetic stream may still have consumed full upstream quota.

### 5.3 Gemini Antigravity OAuth specific

- Missing `project_id` → request build fails after failed `loadCodeAssist`.
- Model-level short cooldown stored per auth+model → scheduler should switch Provider Account only **within same Tenant and Auth Mode** (policy later).
- Credits permanently disabled state is local hint, not Google ban.
- Invalid thoughtSignature → clear reasoning replay cache and fail; not auth.
- `SERVICE_DISABLED` / Gemini for Google Cloud not enabled → entitlement, not retryable as transient network.

---

## 6. Official upstream context (not interchangeable)

Accessed **2026-07-14**:

| Source | Relevance |
|---|---|
| [Gemini app image generation overview](https://gemini.google/us/overview/image-generation/?hl=en-US) | Consumer Web markets image generation/editing (Nano Banana) in the Gemini app. Supports **Gemini Web Cookie** product capability claims at marketing level only. |
| [Gemini API image generation](https://ai.google.dev/gemini-api/docs/image-generation) | Documents developer API image generate/edit models. **Different surface** from both Auth Modes here; do not copy as Antigravity or Web Cookie evidence. |
| [Gemini API Additional Terms](https://ai.google.dev/gemini-api/terms) | Developer API is for professional/business API clients, not consumer use. Reinforces separation from Gemini Apps consumer surface. |
| [Gemini Code Assist setup / Antigravity migration note](https://developers.google.com/gemini-code-assist/docs/set-up-gemini) | Official direction toward Antigravity family for Code Assist individuals; grounds **Gemini Antigravity OAuth** as Google product direction, not a random third-party host. |
| Community reverse specs of `cloudcode-pa.googleapis.com` (e.g. opencode-antigravity-auth docs) | Corroborate endpoint names also present in CLIProxyAPI; still **reference-learned**, not Google primary docs. |

**Hard distinction for implementers:**

1. **Gemini Web Cookie** ≈ consumer Gemini Apps reverse engineering.
2. **Gemini Antigravity OAuth** ≈ IDE/CLI Cloud Code Assist / Antigravity gateway OAuth.
3. **Gemini Developer API** (API key / AI Studio) ≈ out of MVP Auth Mode set (parent #1 out of scope for official API adapters).

---

## 7. Local reference map

### 7.1 `.ref/gemini-web-to-api`

| Area | Path (representative) |
|---|---|
| Cookie store + init + rotate + generate | `internal/modules/providers/gemini_service.go` |
| Multi-turn session | `internal/modules/providers/gemini_chat_session.go` |
| File upload | `internal/modules/providers/gemini_upload.go` |
| Simulated stream + image generations façade | `internal/modules/gemini/gemini_service.go`, `internal/modules/openai/openai_service.go` |
| Endpoints | `EndpointInit`, `EndpointGenerate` (`StreamGenerate`), `EndpointRotateCookies`, `EndpointUpload` |

### 7.2 `.ref/CLIProxyAPI`

| Area | Path (representative) |
|---|---|
| OAuth constants / login | `internal/auth/antigravity/`, `sdk/auth/antigravity.go` |
| Executor generate/stream/refresh/quota | `internal/runtime/executor/antigravity_executor.go` |
| Model fetch utility | `cmd/fetch_antigravity_models/`, `sdk/cliproxy/antigravity_models.go` |
| Credits hint | `sdk/cliproxy/auth/antigravity_credits.go` |
| Translators (incl. inline image parts, tools) | `internal/translator/antigravity/**` |
| OpenAI images API (non-Antigravity routes) | `sdk/api/handlers/openai/openai_images_handlers.go` |

---

## 8. Probe / prototype backlog (explicit)

Nothing below is assumed true until run against a real Tenant-owned Provider Account.

### 8.1 Gemini Web Cookie probes

1. **Cookie bootstrap matrix** — PSID only vs PSID+PSIDTS; rotation success; time-to-unhealthy after forced logout.
2. **Chat non-stream smoke** — fixed prompt; measure latency; capture BardErrorInfo samples.
3. **True vs synthetic streaming** — confirm whether any live partial SSE frames arrive before completion; document cancel/quota interaction.
4. **Multi-turn continuity** — 5-turn chat with metadata restore after process restart.
5. **Attachment limits** — PNG/JPEG/WebP/PDF sizes; rejection codes.
6. **Image generation** — text-to-image prompts on Free vs paid plan; URL lifetime of `googleusercontent` outputs.
7. **Image edit** — upload source image + edit prompt; compare with pure generation.
8. **Inpaint negative test** — attempt mask-like prompt/file pair; expect no structured mask support.
9. **Model discovery stability** — scrape models over 7 days; detect HTML drift.
10. **Challenge induction** — high-frequency requests; record CAPTCHA/BardErrorInfo patterns (stop if account risk unacceptable — decision for #7).
11. **Tool bridge reliability** — force tool JSON; measure parse success rate (expect flaky).

### 8.2 Gemini Antigravity OAuth probes

1. **OAuth login + refresh** — full browser flow; refresh after forced access_token expiry; revoke and observe `invalid_grant`.
2. **Project onboarding** — new account `loadCodeAssist` / `onboardUser`; missing project failure mode.
3. **Chat non-stream + stream** — same prompt; verify SSE ordering and terminal chunk.
4. **Cancel mid-stream** — client cancel; observe server-side continuation if any.
5. **Model discovery** — `fetchAvailableModels` across free vs paid; record quota fields if present.
6. **Tool calling** — native functionCall round-trip; parallel tools; invalid schema rejection.
7. **Thought signature / reasoning replay** — multi-turn tool models that require signatures.
8. **Quota taxonomy** — induce 429; classify against reference decision tree; measure cooldown accuracy.
9. **Credits path** — account with/without `GOOGLE_ONE_AI` credits; exhaust and recover.
10. **Image generation/edit** — for each model ID that *might* be image-capable, attempt multimodal generate/edit; mark `verified` or `unsupported` per model. Until then keep Auth Mode row `unverified`.
11. **Inpaint negative test** — confirm no mask field acceptance.
12. **Host fallback** — prod vs daily hosts under partial outage.
13. **Header/User-Agent drift** — minimal header set that still works; document required Client-Metadata.

### 8.3 Cross-mode independence probes

1. Same Google identity: exhaust Web image quota; verify Antigravity chat still healthy (and reverse).
2. Revoke Antigravity OAuth grant; confirm Web Cookie account unaffected.
3. Export new Web cookies after password change; confirm Antigravity refresh_token behavior independently.

---

## 9. Compact summary for downstream issues

| Capability | Gemini Web Cookie | Gemini Antigravity OAuth |
|---|---|---|
| chat non-streaming | conditionally supported | conditionally supported |
| chat streaming | conditionally supported (synthetic) | conditionally supported (true SSE) |
| image generation | conditionally supported | unverified |
| image edit | conditionally supported | unverified |
| inpaint | unsupported | unsupported |
| model discovery | conditionally supported (HTML scrape) | conditionally supported (fetchAvailableModels) |
| multi-turn continuity | conditionally supported (cid metadata) | conditionally supported (contents history) |
| attachments input | conditionally supported | conditionally supported |
| cancel/abort | conditionally supported (HTTP ctx) | conditionally supported (stream ctx) |
| tool calling | conditionally supported (prompt bridge) | conditionally supported (native functionCall) |

**Credential lifecycle one-liners**

- **Web Cookie:** browser cookie jar → rotate PSIDTS → scrape `at` → generate; human re-export on 401/403.
- **Antigravity OAuth:** browser OAuth offline tokens → refresh access_token → load project → generate/stream; re-login on invalid_grant.

**Do not claim for either Auth Mode without probe:** durable server-side cancel, Photoshop-grade masked inpaint, stable official model catalog identical to Gemini Developer API, or silent cross-Auth-Mode fallback.

---

## 10. Acceptance criteria checklist (issue #4)

- [x] Matrix separates Gemini Web Cookie and Gemini Antigravity OAuth.
- [x] Chat, streaming, image generation, image edit, and inpaint have explicit evidence statuses.
- [x] Credential refresh, entitlement, quota, challenge, and protocol drift described from observed behavior.
- [x] Conditions needing further probe/prototype are explicit (§8).

## 11. Out of scope reminders

- Risk acceptance / kill criteria → issue #7.
- Tenant ownership / Client API Key design → issue #6.
- Gateway implementation, Public API shapes, health state machines → later issues.
- Official Gemini Developer API adapter → parent #1 out of scope for this MVP.
