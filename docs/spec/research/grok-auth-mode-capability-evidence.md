# Grok Auth Mode Capability Evidence

- Issue: [#5 — Verify Grok Web SSO and Grok xAI OAuth](https://github.com/monet88/pixelplus/issues/5)
- Parent: [#1](https://github.com/monet88/pixelplus/issues/1)
- Access date for external URLs: **2026-07-14**
- Scope: specification evidence only. No Gateway implementation. No risk-acceptance decisions (issue #7). No Tenant auth design (issue #6).

## 1. Purpose and status vocabulary

This note records **observable capability and credential-lifecycle evidence** for two independent Grok Auth Modes:

| Auth Mode | Domain term (`CONTEXT.md`) | Upstream surface |
|---|---|---|
| **Grok Web SSO** | Web Access via Web SSO credential | Consumer web `grok.com` reverse surface (`/rest/app-chat/*`, Imagine WS, rate-limits) |
| **Grok xAI OAuth** | OAuth/CLI Access via xAI OAuth credential | Grok Build / CLI chat-proxy + official `api.x.ai` media paths |

Parent #1 locks the rule that **Web Access and OAuth/CLI Access are independent execution surfaces even when they share one external identity**. Capability claims below are therefore **per Auth Mode**, never merged under the label “Grok”.

Status values (only these four):

| Status | Meaning |
|---|---|
| `verified` | Observed in local reference code **and** consistent with official docs or an explicit, unit-tested protocol path that does not invent capability |
| `conditionally supported` | Reference implements a path, but success depends on entitlement, model, plan, region, prompt shape, signing health, or probe not yet run |
| `unsupported` | No first-class upstream or reference path for the capability as PixelPlus defines it |
| `unverified` | Plausible from product marketing or partial code, but not confirmed end-to-end for this Auth Mode |

**Evidence class labels used throughout:**

- **reference-learned** — behavior reconstructed from local `.ref/*` code, fixtures, or unit tests. Not a production promise.
- **upstream-verified** — behavior stated by official xAI pages accessed 2026-07-14, or by an xAI-owned product surface that documents the feature.
- **unverified live** — requires a live probe/prototype against a real Provider Account.

Reference repositories are **research seams**, not product guarantees (parent #1).

Local references:

- `.ref/grok2api/` — dual-provider pure Go gateway with **Grok Web SSO** (`provider/web`) and **Grok Build OAuth** (`provider/cli`).
- `.ref/CLIProxyAPI/` — pure Go multi-provider proxy with **xAI OAuth** (`internal/auth/xai`) and **xAI executor** (`internal/runtime/executor/xai_executor.go`).

Official sources (accessed 2026-07-14):

- https://docs.x.ai/developers/quickstart
- https://docs.x.ai/developers/rest-api-reference/inference/chat
- https://docs.x.ai/developers/rest-api-reference/inference/images
- https://docs.x.ai/developers/model-capabilities/imagine
- https://docs.x.ai/developers/model-capabilities/text/generate-text
- https://docs.x.ai/developers/tools/overview
- https://docs.x.ai/developers/rate-limits
- https://docs.x.ai/build/overview
- https://docs.x.ai/build/cli/reference
- https://x.ai/legal/terms-of-service
- https://x.ai/api

---

## 2. Surface separation (independent Provider Accounts)

| Dimension | Grok Web SSO | Grok xAI OAuth |
|---|---|---|
| Product family | Grok consumer web app | Grok Build / Grok CLI + xAI API family |
| Credential | Web SSO token stored as `sso` / `sso-rw` cookies (plus optional Cloudflare cookies on egress) | OAuth2 `access_token` + `refresh_token` for public Grok CLI client |
| Hosts | `https://grok.com` (default), `assets.grok.com`, `imagine-public.x.ai`, `imgen.x.ai` | `https://auth.x.ai` (OIDC), `https://cli-chat-proxy.grok.com/v1` (OAuth chat default), `https://api.x.ai/v1` (API-key / media / optional chat) |
| Protocol shape | Browser-like REST + NDJSON/JSON frames + Imagine WebSocket; Statsig request header | OpenAI-compatible Responses/Chat/Images over HTTPS with `Authorization: Bearer` |
| Official product terms | Consumer Grok / xAI consumer ToS | Enterprise/developer API terms for xAI APIs; Grok Build CLI is a separate product surface |
| Public xAI API key surface | **Not** this Auth Mode | Distinct from pure API-key BYOK; OAuth reuses Grok CLI client_id and often routes chat to CLI chat-proxy |
| Quota / entitlement | Web tier (`basic` / `super` / `heavy`) + per-mode rate-limit windows + weekly credits | Build/CLI free-window or paid billing; official API RPS/TPM per team when using `api.x.ai` |
| Local reference | `.ref/grok2api` `backend/internal/infra/provider/web` | `.ref/grok2api` `provider/cli` **and** `.ref/CLIProxyAPI` `auth/xai` + `xai_executor` |

**Cause → effect example.** Same xAI email can hold a healthy Grok Web SSO Provider Account and a healthy Grok xAI OAuth Provider Account. Exhausting Web Imagine quota does **not** automatically mark CLI chat models exhausted, and vice versa. Gateway must never silently fallback between these Auth Modes.

**Naming note.** Reference code names the OAuth side `grok_build` / provider `xai`. PixelPlus domain vocabulary uses **Grok xAI OAuth**. This document uses the domain term; file paths keep reference names.

---

## 3. Capability matrix (required rows)

Statuses are **Auth Mode capabilities as PixelPlus may later probe**, not “does Grok market image generation somewhere”.

### 3.1 Grok Web SSO

| Capability | Status | Evidence summary | Evidence class |
|---|---|---|---|
| chat non-streaming | `conditionally supported` | Web adapter POSTs to `/rest/app-chat/conversations/new` (or `…/{conversationId}/responses` for continuity), consumes upstream frames into OpenAI/Responses/Messages JSON. Success depends on valid SSO, egress UA/CF cookies, Statsig header health, and Web tier for the selected mode. | reference-learned |
| chat streaming | `conditionally supported` | Same open path; adapter streams deltas to client as SSE after parsing upstream frames. Not a public OpenAI SSE upstream; translation is gateway-owned. Anti-bot 403 can force Statsig invalidate + single retry. | reference-learned |
| image generation | `conditionally supported` | Catalog models: `grok-imagine-image` (protocol `imagine-lite` via chat path) and `grok-imagine-image-quality` (Imagine WebSocket `/ws/imagine/listen`). Tier gates: Basic for lite, Super for quality. Product marketing of Grok Imagine is separate from this reverse surface. | reference-learned + upstream-verified (Imagine product exists on official API, different surface) |
| image edit | `conditionally supported` | `EditImage` uploads file → creates media post → POSTs chat conversation with `modelName: imagine-image-edit` and `imageEditModel` config. Catalog: `grok-imagine-image-edit`, minimum Super. No official mask contract. | reference-learned |
| inpaint | `unsupported` | No mask field, no inpaint-specific request shape in Web adapter or catalog. Edit is prompt+reference image(s), not Photoshop-style masked inpaint. | reference-learned |
| model discovery | `conditionally supported` | **Static catalog filtered by Web tier**, not a live upstream model list API. `ListModels` returns catalog entries whose `MinimumTier` the account tier supports. Account-specific enablement beyond tier is **not** discovered from upstream. | reference-learned |
| multi-turn continuity | `conditionally supported` | Gateway persists `WebResponseState` (`conversationId` + upstream parent `responseId`) for 30 days; next turn uses `/rest/app-chat/conversations/{id}/responses` with `responseId`. Continuity is **gateway-owned state** bound to the same Provider Account; wrong account ID is rejected. | reference-learned |
| attachments | `conditionally supported` | Chat image attachments: up to 8 images, 64 MiB total; data URI or remote HTTPS URL (SSRF-filtered); upload via `/rest/app-chat/upload-file` returns `fileMetadataId` injected into payload. External downloads intentionally **do not** send SSO cookies. | reference-learned |
| cancel / abort | `conditionally supported` | Request context timeout wraps upstream body (`cancelBody`); client cancel closes body and cancels context. No durable server-side cancel RPC observed for an already-running Web generation. | reference-learned |
| tool calling | `conditionally supported` | **Gateway-owned prompt bridge**: tools serialized into prompt as `<tool_calls>` XML; stream sieve parses model text back into OpenAI/Anthropic tool calls. Not a native Grok Web function-calling protocol. Reliability is model-behavior dependent. | reference-learned |

### 3.2 Grok xAI OAuth

| Capability | Status | Evidence summary | Evidence class |
|---|---|---|---|
| chat non-streaming | `conditionally supported` | CLIProxyAPI and grok2api Build adapter POST Responses (and Chat via translation) with `Authorization: Bearer <access_token>`. OAuth default chat base is `https://cli-chat-proxy.grok.com/v1` (`using_api=false`); official `https://api.x.ai/v1` is used when `using_api=true` or API-key auth. Official docs document Responses/Chat with API keys — OAuth-to-chat-proxy is **reference-learned**. | reference-learned + upstream-verified (Responses API with API key) |
| chat streaming | `conditionally supported` | First-class SSE on `/responses` with `Accept: text/event-stream`. Executor scans SSE until `response.completed`; disconnect before completion surfaces as timeout-class error. Official docs support streaming Responses/Chat. | reference-learned + upstream-verified |
| image generation | `conditionally supported` | Executor routes OpenAI image handler to `POST {base}/images/generations` (default base `https://api.x.ai/v1`). Built-in models include `grok-imagine-image` and `grok-imagine-image-quality`. Official docs: Imagine generation with those models under API key. OAuth token acceptance on media endpoints is **unverified live**. | reference-learned + upstream-verified (API key path) |
| image edit | `conditionally supported` | `POST /images/edits` with image URL/file; official docs support multi-reference edit (up to 3). Reference implements path; OAuth entitlement for edit is plan-dependent. | reference-learned + upstream-verified (API key path) |
| inpaint | `unsupported` | Official Imagine edit is prompt + reference image(s), not a dedicated mask/inpaint schema. No mask field in CLIProxyAPI xAI image path. | reference-learned + upstream-verified (docs describe edit, not mask inpaint) |
| model discovery | `conditionally supported` | grok2api Build: `GET /models` with Bearer. CLIProxyAPI: static `models.json` xAI list + hard-coded Imagine builtins; dynamic registration possible via model updater. Official console/docs list models separately from OAuth-specific availability. Account-specific probe still required. | reference-learned + upstream-verified (model names exist in docs) |
| multi-turn continuity | `conditionally supported` | Official Responses API stores responses 30 days; client continues via previous response id (`store` default true). Composer models may require isolated `x-grok-conv-id`. Chat Completions is history-in-request. Live OAuth chat-proxy parity with full Responses store semantics is **unverified live**. | upstream-verified (API) + reference-learned (composer session header) |
| attachments | `conditionally supported` | Multimodal / file inputs follow OpenAI-compatible Responses and image edit payloads; Files API exists on official API. Exact OAuth chat-proxy attachment limits not live-probed. | reference-learned + upstream-verified (API) |
| cancel / abort | `conditionally supported` | Stream/request cancel via `context`; no separate cancel RPC in reference. Official deferred-completion exists for some chat paths; not mapped as durable cancel for OAuth executor. | reference-learned |
| tool calling | `conditionally supported` | Official Responses tools: function calling + built-in tools (`web_search`, `x_search`, `code_interpreter`, etc.). Reference normalizes tool types (custom→function, namespace expansion, automation_update schema workaround). Some tools hang or need sanitization — conditional. | reference-learned + upstream-verified |

---

## 4. Extended rows (lifecycle, signing, experiments, ownership)

### 4.1 Credential lifecycle

| Topic | Grok Web SSO | Grok xAI OAuth |
|---|---|---|
| Status | `conditionally supported` (import + probe path exists; auto-refresh **unsupported**) | `conditionally supported` (device OAuth + refresh path exists; live success plan-dependent) |
| Credential artifacts | SSO token (normalized from `sso=…` or raw); optional egress Cloudflare cookies (`cf_clearance`, `__cf_bm`, `_cfuvid`, `cf_chl_*`) | `access_token`, `refresh_token`, optional `id_token`; OIDC discovery endpoints; metadata `token_endpoint`, `base_url`, `last_refresh` |
| Connection | Import SSO JSON (`sso_token`/`token`) or plain-text lines; encrypt access token; **no refresh token** | Device authorization grant RFC 8628 against `auth.x.ai` with public client_id `b1a00492-073a-47ea-816f-4c329264a828` and scope `openid profile email offline_access grok-cli:access api:access` |
| Refresh | **Cannot auto-renew.** README: Web SSO invalid → account leaves pool until reauth. 401 → unauthorized. | `grant_type=refresh_token` + client_id; singleflight refresh; ~5 minute lead before expiry in CLIProxyAPI |
| Reauthentication | Operator re-exports SSO from browser; AuthStatus → reauth required | Device flow or re-login; 401 marks unauthorized and stops auto-refresh scheduling |
| Expiry signal | HTTP 401 on chat/quota; anti-bot 403 is **not** the same class as auth expiry | OAuth error / HTTP 401; free-tier exhaustion may cool down ~24h (reference heuristic) |

**Cause → effect (Web).** Tenant pastes an SSO token collected from browser cookies. Gateway stores encrypted token and probes quota/chat. Weeks later token is revoked in browser → every Web request 401 → Provider Account must leave usable pool; no refresh token can fix it.

**Cause → effect (OAuth).** Device flow yields access+refresh. Access expires hourly-class; refresh keeps account usable until refresh is revoked or user denies. This is a different lifecycle from Web SSO even for the same email.

### 4.2 Signing / request integrity requirements

| Topic | Grok Web SSO | Grok xAI OAuth |
|---|---|---|
| Status | `conditionally supported` (signing required for healthy Web REST path in reference) | `unsupported` as Statsig/browser integrity; uses Bearer + optional CLI identity headers |
| Mechanism | Header `x-statsig-id` on signed POSTs (chat open, rate-limits, JSON posts). Modes: `url` (default) or `manual`. | No Statsig. `Authorization: Bearer`. CLI chat-proxy adds `x-token-auth` / client version / UA when not `using_api`. |
| How `x-statsig-id` is obtained | Fetch page metaContent from `grok.com` with SSO cookies → POST method/path/meta to external signer (default `https://grok.wodf.de/sign`) → cache 1h (stale fallback). Valid ID = base64 of 70 raw bytes. | N/A |
| Ownership | **Upstream expects** a Statsig-style integrity header on browser REST. **Gateway owns** how to produce it (manual fixed value, remote signer service, or future self-hosted signer). Remote default signer is a **third-party dependency**, not an xAI official API. | Gateway owns CLI identity header constants that must track Grok CLI client version drift. |
| Failure mode | Missing/invalid signature → anti-bot / 403; reference invalidates cache once and retries; still failing → anti-bot error to client. | 401/403 are auth or plan failures, not Statsig. |

**Confidence:** High that Web path *attempts* Statsig signing in reference; **medium** that every production Web REST call still requires it (protocol can drift). **Unverified live** whether PixelPlus can operate without a third-party signer (manual mode or self-hosted).

### 4.3 Statsig / experiments / feature flags

| Topic | Grok Web SSO | Grok xAI OAuth |
|---|---|---|
| Status | `conditionally supported` (Statsig client-id header is first-class in reference) | `unverified` / effectively not present in OAuth executor |
| Evidence | `statsig.go`, config `StatsigMode` / `StatsigSignerURL` / `StatsigManualValue`; applied on chat, quota, postJSON | No Statsig code path in `xai_executor` |
| Implication | Capability Snapshot and health must treat “Statsig signer down” as a **gateway-owned dependency failure**, distinct from upstream Grok outage and from auth expiry. | N/A |

### 4.4 Entitlement / quota

| Topic | Grok Web SSO | Grok xAI OAuth |
|---|---|---|
| Status | `conditionally supported` | `conditionally supported` |
| Mechanism | Web tier enum Basic/Super/Heavy; `POST /rest/rate-limits` per mode (`fast`/`auto`/`expert`/`heavy`/imagine modes); Super/Heavy also weekly credits via gRPC-Web `GetGrokCreditsConfig`. Product codes include Chat=4, Imagine=5, Build=2, etc. | Build adapter: billing snapshots (`GetBilling` / credits format). CLIProxyAPI: free usage exhausted cooldown 24h heuristic; official API documents per-model RPS and TPM for API teams. |
| Model gates | Static catalog minimum tier (e.g. heavy chat needs Heavy; image quality needs Super). | Dynamic `/models` for Build; static registry + builtins for CLIProxyAPI; official model list for API key. |
| Independence | Web quota windows do not equal Build billing. | Build/CLI quota does not equal Web rate-limits. |

### 4.5 Challenge

| Topic | Grok Web SSO | Grok xAI OAuth |
|---|---|---|
| Status | `conditionally supported` (anti-bot path exists; no CAPTCHA solver) | `unverified` for browser challenges; OAuth is token-based |
| Evidence | `errWebAntiBot`, Cloudflare cookie sanitization on egress, User-Agent from lease, Origin/Referer/Sec-Fetch headers, Statsig retry | Device verify is human-in-the-loop at authorization time only |
| Failure semantics | Challenge ≠ quota ≠ auth expiry; must map to distinct Public API errors later (#16) | Authorization denied / expired device code during login only |

### 4.6 Protocol drift

| Drift vector | Auth Mode | Symptom | Suggested future failure class |
|---|---|---|---|
| Statsig metaContent / signer layout | Web SSO | cannot mint `x-statsig-id` | protocol_drift / gateway_dependency |
| Chat frame shape (`conversation`/`response` keys) | Web SSO | parse empty / stream hang | protocol_drift |
| Imagine WS message schema | Web SSO | image generation incomplete | protocol_drift |
| Catalog mode names / tier gates | Web SSO | false capability claims | capability_snapshot_stale |
| CLI chat-proxy client version headers | xAI OAuth | 4xx from proxy | protocol_drift |
| Responses event names / tool schemas | xAI OAuth | stream translation gaps | protocol_drift |
| OIDC discovery host change | xAI OAuth | login/refresh fail | auth |
| Official API field deprecations | xAI OAuth (API path) | request validation errors | protocol_drift |

### 4.7 Gateway-owned vs upstream-owned behaviors

| Behavior | Owner | Auth Mode | Notes |
|---|---|---|---|
| OpenAI Chat/Responses/Messages façade | Gateway | Both | Translation layers in both references |
| Web multi-turn state store (conversationId/parent) | Gateway | Web SSO | Upstream conversation ids exist; **binding and TTL are gateway policy** |
| Tool XML prompt bridge + stream sieve | Gateway | Web SSO | Not native Web tools |
| Statsig remote signer call | Gateway dependency | Web SSO | Upstream requires header; production of header is gateway/operator |
| Egress proxy + CF cookie pairing | Gateway | Web SSO | Same browser session realism |
| SSO import / encryption / reauth UX | Gateway | Web SSO | Upstream only sees cookies |
| Device OAuth + refresh scheduling | Gateway | xAI OAuth | Endpoints are upstream |
| CLI chat-proxy vs `api.x.ai` base URL choice | Gateway policy | xAI OAuth | OAuth default chat-proxy is reference policy, not PixelPlus decision yet |
| Image archive / public URL rewrite | Gateway | Web SSO | Saves to local media store |
| Capability Snapshot probe | Gateway | Both | Parent #1 invariant |
| Rate-limit windows / billing numbers | Upstream | Both | Gateway only stores snapshots |
| Model weights / generation | Upstream | Both | |
| Official API key issuance | Upstream (out of MVP official API adapter scope) | Distinct from both Auth Modes | Parent #1: no official API Adapter in MVP Web-to-API |

---

## 5. Credential lifecycle deep dive

### 5.1 Grok Web SSO

#### Credential types (conceptual — never store real secrets in docs/fixtures)

| Artifact | Role | Notes |
|---|---|---|
| SSO token | Primary Web session proof | Sent as `Cookie: sso=<token>; sso-rw=<token>` |
| Cloudflare cookies | Bot-mitigation session | Optional on egress lease; only `cf_clearance`, `__cf_bm`, `_cfuvid`, `cf_chl_*` retained |
| User-Agent | Browser realism | Bound to egress lease; should match CF cookie session |
| `x-statsig-id` | Request integrity | Not a stored credential; derived per method/path |
| `x-xai-request-id` | Request correlation | Generated UUID per request |

#### Connection journey (reference-learned)

1. Operator exports SSO from browser (JSON array or plain lines).
2. Adapter sanitizes token (strip `sso=` prefix, first segment before `;`, control chars).
3. Encrypt access token; `AuthType=sso`, `Provider=grok_web`; optional initial tier.
4. Sync quota (`/rest/rate-limits` and/or weekly credits) to refine tier.
5. Enable account only after successful probe (reference waits for first sync on import).

#### Refresh / expiry

- **No refresh token path** for pure Web SSO in grok2api.
- README states Web SSO cannot auto-renew; invalid credentials leave the pool.
- 401 → unauthorized; operator reauth required.

#### Optional SSO→Build conversion (out of primary lifecycle)

`sso_build.go` can exchange a live Web SSO session into Build OAuth tokens via device approve using Web cookies (`conversations:read/write` extra scopes). That produces a **different Auth Mode credential** (Build OAuth). PixelPlus must treat conversion as an explicit connection action, never silent fallback.

### 5.2 Grok xAI OAuth

#### Credential types

| Artifact | Role | Notes |
|---|---|---|
| `access_token` | Bearer for CLI chat-proxy and/or API | Short-lived |
| `refresh_token` | Silent renewal | Requires `offline_access` scope |
| `id_token` | Optional identity claims (email/sub) | JWT parse for label |
| OIDC discovery | Resolves device + token endpoints | `https://auth.x.ai/.well-known/openid-configuration`; hosts must be `x.ai` / `*.x.ai` |
| Public client_id | Grok CLI OAuth client | `b1a00492-073a-47ea-816f-4c329264a828` (no client secret in reference) |

#### Connection journey

1. Discover OIDC endpoints (or hardcode device/token URLs as grok2api does).
2. `POST device/code` with client_id + scope → user_code + verification URI.
3. User approves in browser (`grok login --device-auth` is the official CLI analogue — upstream-verified product command).
4. Poll token endpoint with device_code until access+refresh issued.
5. Persist tokens; schedule refresh before expiry.

Official Grok Build docs describe browser login and API-key alternative; device-auth flag is documented in CLI reference. The public client_id and device endpoints are **reference-learned / community-reused**, not published as a first-class developer OAuth product API for third-party SaaS. PixelPlus must not claim “official third-party OAuth app registration” without product confirmation.

#### Refresh / expiry

- `refresh_token` grant; singleflight per token in CLIProxyAPI.
- Refresh lead ~5 minutes.
- Unauthorized failure stops auto-refresh loop for that auth.

#### Dual base URL policy (critical)

| Path | Default base when OAuth | Notes |
|---|---|---|
| Chat / Responses HTTP | `https://cli-chat-proxy.grok.com/v1` | When `using_api` false (OAuth default) |
| Images / videos | `https://api.x.ai/v1` | Media always API host in executor |
| Explicit `using_api=true` chat | `https://api.x.ai/v1` | Behaves closer to official API key surface |

**Cause → effect.** A Capability Snapshot that only probes `api.x.ai` chat may miss CLI chat-proxy-only models or free-window behavior, and vice versa. PixelPlus must record which base URL family a Provider Account is bound to.

---

## 6. Signing, SSO, and Statsig findings (AC-critical)

### 6.1 SSO (Web)

1. Provider Credential is the **SSO token**, not a full browser cookie jar (though CF cookies may attach at egress).
2. Cookie construction is gateway-owned: always dual `sso` + `sso-rw`.
3. SSO is **not** interchangeable with OAuth access_token.
4. SSO has **no** automated refresh; reauthentication is human export.

### 6.2 Statsig / signing (Web)

1. Signed Web POSTs set `x-statsig-id`.
2. Default production path in reference depends on:
   - metaContent scraped from grok.com HTML with SSO, and
   - an HTTP signer service (default third-party URL).
3. Manual mode allows a fixed valid Statsig ID (70-byte base64) without remote signer — may be brittle if path-bound.
4. 403 anti-bot triggers invalidate + one retry — gateway-owned recovery policy.
5. **This is not OAuth request signing** and does not apply to Grok xAI OAuth.

### 6.3 OAuth integrity (xAI OAuth)

1. Integrity is Bearer token possession + TLS.
2. CLI chat-proxy may require extra client identity headers that track Grok CLI version constants in reference code — a drift surface.
3. No Statsig.

### 6.4 What is gateway-owned vs upstream-owned (summary)

| Requirement | Web SSO | xAI OAuth |
|---|---|---|
| Produce SSO cookies | Gateway | N/A |
| Produce Statsig ID | Gateway (+ optional external signer) | N/A |
| Pair UA/proxy/CF cookies | Gateway | Optional proxy only |
| Device login UX | N/A (unless SSO→Build conversion) | Gateway |
| Refresh tokens | Unsupported | Gateway scheduler |
| Translate OpenAI façade | Gateway | Gateway |
| Enforce no cross-Auth-Mode fallback | Gateway (product invariant) | Gateway |

---

## 7. Confidence and prototype gaps

### 7.1 High confidence (can inform contract shape, still not production promise)

- Two Auth Modes are independent (credential, host, quota, adapter).
- Web SSO cannot refresh; OAuth can.
- Web chat/image/edit reverse paths exist in grok2api with explicit endpoints.
- Web tool calling is synthetic (prompt bridge).
- Web model list is static catalog × tier, not live discovery.
- Web multi-turn continuity requires gateway state.
- OAuth device flow + refresh against `auth.x.ai` is implemented in two pure-Go references with the same client_id/scope.
- Official `api.x.ai` documents Responses, Chat, Images generations/edits, tools, rate limits for **API keys**.
- Masked inpaint is not a first-class capability on either Auth Mode.

### 7.2 Medium confidence

- Statsig still required on all critical Web REST calls in current production Grok web (inferred from reference retry logic; needs live probe).
- OAuth access_token accepted by `cli-chat-proxy` for full Responses feature set matching official API.
- OAuth access_token accepted by `api.x.ai` image endpoints without a separate API key.
- Image edit quality/limits for Web Super tier accounts.

### 7.3 Prototype gaps (required before Capability Snapshot can mark `verified` in product)

| Gap ID | Auth Mode | What to prototype | Why it blocks |
|---|---|---|---|
| P5-W1 | Web SSO | Live chat non-stream + stream with real SSO | Confirm frame parse + Statsig + anti-bot under current grok.com |
| P5-W2 | Web SSO | Statsig: manual vs self-hosted signer vs third-party | Decide gateway dependency budget (#21) and kill criteria (#7) |
| P5-W3 | Web SSO | Imagine lite + quality + edit on Basic vs Super | Tier gates and WS stability |
| P5-W4 | Web SSO | Multi-turn previous_response_id across process restart | State store durability requirements |
| P5-W5 | Web SSO | Attachment upload + remote URL SSRF cases | Security and size limits |
| P5-W6 | Web SSO | Tool bridge round-trip with parallel tools | Whether to advertise tools at all |
| P5-W7 | Web SSO | 401 vs 403 vs rate-limit classification | Error taxonomy input for #16 |
| P5-O1 | xAI OAuth | Device login + refresh on headless host | Connection journey |
| P5-O2 | xAI OAuth | Chat stream on `cli-chat-proxy` vs `api.x.ai` | Base URL binding in Capability Snapshot |
| P5-O3 | xAI OAuth | Image generation/edit with OAuth token only | Whether media needs API key |
| P5-O4 | xAI OAuth | Native tool calling + web_search | vs Web synthetic tools |
| P5-O5 | xAI OAuth | Free-window exhaustion and recovery | Health/cooldown input for #17 |
| P5-O6 | xAI OAuth | Model list from `/models` vs static registry drift | Discovery semantics for #10 |
| P5-X1 | Both | Same identity dual accounts isolation | Prove no silent fallback (with #6) |

### 7.4 Explicit non-claims

- This issue does **not** accept Web-to-API ToS or ban risk (#2/#7).
- This issue does **not** enable an Official xAI API-key Adapter (parent #1 out of scope for MVP).
- Reference catalogs and model IDs are not PixelPlus Public API model promises.
- Third-party Statsig signer availability/trust is not endorsed.

---

## 8. Compact matrix (copy-friendly)

| Capability | Grok Web SSO | Grok xAI OAuth |
|---|---|---|
| chat non-streaming | conditionally supported | conditionally supported |
| chat streaming | conditionally supported | conditionally supported |
| image generation | conditionally supported | conditionally supported |
| image edit | conditionally supported | conditionally supported |
| inpaint | unsupported | unsupported |
| model discovery | conditionally supported | conditionally supported |
| multi-turn continuity | conditionally supported | conditionally supported |
| attachments | conditionally supported | conditionally supported |
| cancel/abort | conditionally supported | conditionally supported |
| tool calling | conditionally supported | conditionally supported |
| credential lifecycle | conditionally supported (no auto-refresh) | conditionally supported (refreshable) |
| signing / request integrity | conditionally supported (Statsig `x-statsig-id`) | unsupported as Statsig; Bearer (+ CLI headers) |
| Statsig / experiments | conditionally supported | unverified / absent |
| entitlement / quota | conditionally supported | conditionally supported |
| challenge | conditionally supported (anti-bot/CF) | unverified (login human only) |
| protocol drift | unverified (high risk; web reverse) | unverified (medium risk; CLI headers + API evolution) |

No row is marked product-`verified` without live Provider Account probe. Strongest “reference + official docs” alignment is OAuth/API chat and images under API-key docs; Web remains reverse-engineered.

---

## 9. Implications for downstream issues (pointers only — no decisions)

- **#7 Risk envelope:** Web SSO carries Statsig third-party dependency, CF/anti-bot, and non-refreshable cookies. OAuth carries CLI client_id reuse and chat-proxy vs API dual surface. Risk acceptance is out of scope here.
- **#9 Credential lifecycle:** Model Web reauth-only vs OAuth refreshable as separate state machines.
- **#10 Capability Snapshot:** Probe per Auth Mode; include base URL family for OAuth; never merge Web catalog with Build `/models`.
- **#12 Chat lifecycle:** Web continuity needs durable response state; OAuth may use upstream response ids when on Responses API.
- **#14 Image jobs:** Web quality path is WS; lite/edit are HTTP; OAuth images are HTTP API. Inpaint remains unsupported.
- **#16 Errors:** Separate classes for auth expiry, anti-bot/challenge, Statsig dependency failure, quota, protocol drift.
- **#21 Dependencies:** Pure-Go Web adapter still needs a Statsig strategy (manual/self-host/third-party) if Web mode is enabled.

---

## 10. Evidence index (paths)

### Grok Web SSO — `.ref/grok2api`

- `backend/internal/infra/provider/web/adapter.go` — config, ListModels tier filter
- `backend/internal/infra/provider/web/headers.go` — SSO cookie + browser headers
- `backend/internal/infra/provider/web/statsig.go` — `x-statsig-id` signer client
- `backend/internal/infra/provider/web/chat.go` — openChat, stream, continuity, tools
- `backend/internal/infra/provider/web/image.go` — imagine lite/WS/edit/upload
- `backend/internal/infra/provider/web/attachments.go` — chat image upload limits
- `backend/internal/infra/provider/web/tools.go` — tool XML bridge
- `backend/internal/infra/provider/web/quota.go` — rate-limits + weekly credits
- `backend/internal/infra/provider/web/catalog.go` — static models and tier minima
- `backend/internal/infra/provider/web/import.go` — SSO import
- `backend/internal/infra/provider/web/sso_build.go` — optional SSO→Build conversion
- `backend/internal/infra/egress/manager.go` — `BuildSSOCookie`
- `backend/internal/domain/account/account.go` — Provider/AuthType/WebTier/Quota
- `README.md` — dual provider overview, non-refreshable SSO

### Grok xAI OAuth — `.ref/grok2api` + `.ref/CLIProxyAPI`

- `backend/internal/infra/provider/cli/oauth.go` — device + refresh
- `backend/internal/infra/provider/cli/adapter.go` — Responses forward, ListModels, billing
- `CLIProxyAPI/internal/auth/xai/types.go` — client_id, scope, base URLs
- `CLIProxyAPI/internal/auth/xai/xai.go` — discovery, device, poll, refresh
- `CLIProxyAPI/internal/runtime/executor/xai_executor.go` — chat/stream/images/tools/refresh
- `CLIProxyAPI/internal/registry/model_definitions.go` — Imagine builtins
- `CLIProxyAPI/internal/registry/models/models.json` — xAI model list
- `CLIProxyAPI/internal/cmd/xai_login.go` — login entrypoint

### Official

Listed in §1; all accessed **2026-07-14**.

---

## 11. Acceptance criteria checklist

| AC | Status |
|---|---|
| Matrix separates Grok Web SSO and Grok xAI OAuth | Met (§2, §3, §8) |
| Each capability uses only verified / conditionally supported / unsupported / unverified | Met |
| Signing, SSO, experiment/Statsig, gateway-owned behaviors identified with evidence | Met (§4, §6, §10) |
| Confidence and prototype gaps recorded for important conclusions | Met (§7) |

**Close recommendation:** AC complete for specification research. No product risk acceptance claimed. Live probes remain future work before any capability is promoted to contract-level `verified` in PixelPlus.
