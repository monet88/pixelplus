# Research: ChatGPT Web Access & ChatGPT Codex OAuth Capability Evidence

- **Issue:** [#3](https://github.com/monet88/pixelplus/issues/3)
- **Parent:** [#1](https://github.com/monet88/pixelplus/issues/1)
- **Auth Modes:** `ChatGPT Web Access`, `ChatGPT Codex OAuth`
- **Date:** 2026-07-14
- **Scope:** Specification evidence only. No Gateway implementation. No risk-acceptance decision (#7) and no compliance policy (#2) beyond technical challenge signals.
- **Vocabulary:** Uses `CONTEXT.md` terms (`Provider`, `Provider Account`, `Provider Credential`, `Auth Mode`, `Web Access`, `OAuth/CLI Access`, `Capability Snapshot`, `Web Adapter`).

## Status legend

Every capability claim uses exactly one of:

| Status | Meaning |
|---|---|
| `verified` | Confirmed against an official upstream product/docs surface (OpenAI Help Center / developers.openai.com Codex docs), not only a third-party reverse engineering repo. |
| `conditionally supported` | Present and implemented in local reference code, or official product docs describe the product feature, but live BYOA/Gateway probe against the exact Auth Mode surface is still missing or entitlement-gated. |
| `unsupported` | Reference code or official docs show the mode/path does not provide the capability, or explicitly blocks it. |
| `unverified` | Insufficient evidence from local references and public official docs. |

## Evidence classification rules

| Label | Meaning |
|---|---|
| **Reference-learned** | Behavior inferred from local research surfaces `.ref/chatgpt2api/` and/or `.ref/CLIProxyAPI/`. Not an automatic product promise. |
| **Upstream-verified** | Behavior confirmed from official OpenAI/ChatGPT/Codex documentation fetched 2026-07-14. |
| **Gap / live-probe needed** | Requires a controlled live probe or fixture capture against a real Provider Account. |

Parent #1 rule applied: reference repositories are research sources, not automatic production capability claims. Therefore most endpoint-level claims remain `conditionally supported` unless official docs independently confirm the product capability.

---

## 1. Surface separation

These two Auth Modes must remain independent Provider Account execution surfaces even when they share one external ChatGPT identity.

| Dimension | ChatGPT Web Access | ChatGPT Codex OAuth |
|---|---|---|
| Access class | Web Access | OAuth/CLI Access |
| Primary host | `https://chatgpt.com` | Auth: `https://auth.openai.com`; execution: `https://chatgpt.com/backend-api/codex` |
| Primary protocol | ChatGPT consumer web backend (`/backend-api/*`, SSE conversation protocol) | Codex Responses-style surface (`/backend-api/codex/responses`, optional websocket lite path) |
| Typical credential | Web `access_token` (+ optional `refresh_token`, fingerprint, proxy) | Codex OAuth bundle: `access_token`, `refresh_token`, `id_token`, `account_id`, `expired` |
| Adapter expectation | Web Adapter | OAuth/CLI Adapter |
| Quota domain | Consumer web entitlements / image_gen limits | Codex/ChatGPT-subscription or API-key usage domain |
| Challenge surface | Sentinel / PoW / Turnstile / Arkose / Cloudflare | Mostly auth expiry, usage limits, Cloudflare on image paths; no sentinel/PoW in Codex executor path |

**Cause → effect (concrete):**

1. Same human logs into ChatGPT Plus.
2. Web Access uses consumer conversation endpoints and web image_gen quota.
3. Codex OAuth uses `/backend-api/codex/responses` and a separate Codex entitlement/quota path.
4. Reference code explicitly treats web and codex as distinct model aliases (`gpt-image-2` vs `codex-gpt-image-2`) and separate account `source_type`s.
5. Therefore PixelPlus must model two Provider Accounts / Capability Snapshots, never one merged “ChatGPT account capability”.

---

## 2. Capability matrices

### 2.1 ChatGPT Web Access

| Capability | Status | Evidence class | Evidence | Observable behavior / failure | Notes for Gateway |
|---|---|---|---|---|---|
| chat (non-streaming) | `conditionally supported` | Reference-learned | `.ref/chatgpt2api/services/openai_backend_api.py` `stream_conversation()` posts to `/backend-api/conversation` (auth) or `/backend-anon/conversation` (anon); protocol layer can aggregate SSE into non-stream response | 401 → `InvalidAccessTokenError`; challenge failure aborts before conversation | Non-stream is a client aggregation over SSE, not a separate upstream non-stream endpoint in the reference |
| chat streaming | `conditionally supported` | Reference-learned | Same `stream_conversation()` with `stream=True`; docs in `.ref/chatgpt2api/docs/upstream-sse-conversation.md` | SSE payloads: `v1`, patch ops, `[DONE]`; missing `conversation_id` can require recovery via recent conversations list | Upstream protocol is ChatGPT-specific JSON-patch SSE, not OpenAI Chat Completions SSE |
| image generation | `conditionally supported` | Reference-learned + partial upstream product | Web path: `_stream_picture_conversation` → prepare + `/backend-api/f/conversation` with image_gen flow; official Help Center says ChatGPT Images available on tiers | Quota 0 / restore_at from `conversation/init.limits_progress[feature_name=image_gen]`; content policy errors | Product feature exists officially; web reverse path is reference-learned only |
| image edit | `conditionally supported` | Reference-learned + partial upstream product | Upload reference images via `/backend-api/files` then multimodal parts in image conversation; `/v1/images/edits` protocol wrapper | Edit requires auth token; upload + prepare + SSE generate | Official product supports edit in ChatGPT Images; web reverse path not live-probed here |
| inpaint / mask edit | `conditionally supported` | Reference-learned | `.ref/chatgpt2api/services/protocol/openai_v1_image_edit.py` composites mask alpha into source image before upload; no dedicated upstream mask field in web conversation path | Mask transparency = edit region, opaque = preserve | This is a client-side adaptation. Whether upstream accepts true mask semantics remains live-probe gap |
| model listing / discovery | `conditionally supported` | Reference-learned | `list_models()` → `/backend-api/models` or `/backend-anon/models`; protocol adds dynamic image aliases from account pool | Model list is account/session dependent | Capability Snapshot must store observed slugs, not static provider catalog |
| multi-turn / conversation continuity | `conditionally supported` | Reference-learned | SSE emits `conversation_id`; payload supports conversation fields; `delete_conversation`, list recent conversations, find-by-prompt recovery | Continuity depends on reusing conversation_id / history on same Provider Account | Affinity to one Provider Account is required for continuity |
| cancel / abort | `unverified` | Gap | No dedicated stop/cancel conversation API found in chatgpt2api backend client; only local stream close | Client disconnect stops reading SSE; upstream generation may continue | Live probe needed: does closing HTTP cancel image_gen/chat turn? |
| tool / function calling | `conditionally supported` | Reference-learned | SSE docs mention tool roles, `metadata.tool_invoked`, async `image_gen` tasks; not general OpenAI tools API | Tools are ChatGPT-native, not portable function-calling contract | Do not claim OpenAI tools parity for Web Access |
| file / image input attachment | `conditionally supported` | Reference-learned | `_upload_image` → POST `/backend-api/files`, PUT bytes, POST `.../uploaded`; multimodal parts use `file-service://{file_id}` | Upload failure blocks edit/generate-with-reference | Attachment is web file-service based |

### 2.2 ChatGPT Codex OAuth

| Capability | Status | Evidence class | Evidence | Observable behavior / failure | Notes for Gateway |
|---|---|---|---|---|---|
| chat (non-streaming) | `conditionally supported` | Reference-learned + partial upstream | CLIProxyAPI `CodexExecutor.Execute` posts to `https://chatgpt.com/backend-api/codex/responses`; official Codex auth docs confirm ChatGPT-subscription sign-in for Codex | 401 auth_unavailable; usage_limit_reached; previous_response_not_found | Official docs verify auth product surface; endpoint shape is reference-learned |
| chat streaming | `conditionally supported` | Reference-learned | `ExecuteStream` same `/responses` with `Accept: text/event-stream`; websocket executor for responses-lite models | Stream terminal errors include usage_limit and context errors | SSE/websocket translation is reference-learned |
| image generation | `conditionally supported` | Reference-learned | `codex_openai_images.go` builds Responses body with `tools:[{type:image_generation,action:generate}]` to `/backend-api/codex/responses`; chatgpt2api also has `iter_codex_image_response_events` | Free plan skips auto image tool injection (`isCodexFreePlanAuth`); Plus/Team/Pro gate in chatgpt2api model exposure | Codex image is entitlement-gated |
| image edit | `conditionally supported` | Reference-learned | Same Codex responses path with `action:edit` and input image data URLs | Requires paid plan path in references | Cross-checked in both refs |
| inpaint / mask edit | `conditionally supported` | Reference-learned | Codex edit path accepts `mask` / `input_image_mask.image_url` in JSON and multipart | Mask field mapped into image_generation tool | Stronger mask field support than Web Access reference path, still not live-probed |
| model listing / discovery | `conditionally supported` | Reference-learned | `internal/registry/models/codex_client_models.json` static client model catalog (e.g. GPT-5.6-Sol family metadata); no live `/models` fetch verified in this research pass | Catalog can drift from upstream availability | Capability Snapshot should prefer probe-time discovery over static JSON |
| multi-turn / conversation continuity | `conditionally supported` | Reference-learned | Responses continuity via session/prompt_cache/thread headers; `previous_response_id` is stripped in several paths and treated as error class when missing | Continuity is session/header and response-id sensitive; protocol drift risk high | Do not assume OpenAI Platform previous_response_id semantics unchanged |
| cancel / abort | `conditionally supported` | Reference-learned | Context cancel closes local stream/websocket; stream forwarder cancels on client disconnect | Local cancel is implemented; upstream cooperative cancel not officially documented | Mark cooperative upstream cancel as live-probe gap |
| tool / function calling | `conditionally supported` | Reference-learned | Executor handles `function_call` / `custom_tool_call` items and image_generation tool; parallel_tool_calls normalized | Codex agent tools ≠ Public API tools passthrough | Useful for adapter, not direct Public API promise |
| file / image input attachment | `conditionally supported` | Reference-learned | Image inputs as data URLs / multipart files into Responses input; input modalities include image in model catalog | Large payload / Cloudflare risk on image paths | Different from web file-service upload |

---

## 3. Credential lifecycle

### 3.1 ChatGPT Web Access

| Stage | Status | Evidence class | Store shape (no secrets) | Signals |
|---|---|---|---|---|
| Obtain | `conditionally supported` | Reference-learned | Import raw `access_token`; optional password re-login; optional platform OAuth import (`app_2SKx67EdpoN0G6j64rFvigXD`) producing access/refresh/id tokens | Import success only after token usable against `/backend-api/me` or user-info probe |
| Store shape | `conditionally supported` | Reference-learned | Account record fields observed: `access_token`, optional `refresh_token`, `email`, `user_id`, `type`/`plan`, `status`, `quota`, `restore_at`, `limits_progress`, `default_model_slug`, `source_type` (`web`/`codex`), fingerprint (`oai-device-id`, `oai-session-id`, UA), `proxy`, counters (`success`/`fail`/`invalid_count`), refresh error timestamps | Never expose tokens in Public API |
| Refresh | `conditionally supported` | Reference-learned | If `refresh_token` present: POST `https://auth.openai.com/oauth/token` with `grant_type=refresh_token`, `client_id=app_2SKx67EdpoN0G6j64rFvigXD` | JWT exp drives refresh need; failures recorded; `app_session_terminated` can trigger password re-login path |
| Revoke / expiry | `conditionally supported` | Reference-learned | HTTP 401 on backend paths → `InvalidAccessTokenError` / remove invalid token after thresholds | Refresh token reuse/termination errors; invalid_count / last_invalid_at |
| rt_token refresh | `unsupported` (in current reference feature set) | Reference-learned | feature-status marks `rt_token` refresh as not implemented | Do not depend on session_token/rt_token rotation in MVP without new research |

**Concrete lifecycle example:**

1. Tenant submits Web Access Provider Credential containing `access_token` (+ optional refresh/password/proxy).
2. Gateway probe calls `/backend-api/me` + `/backend-api/conversation/init` + accounts check.
3. Snapshot stores plan_type, image_gen remaining/reset, default model slug.
4. On near-expiry, refresh with refresh_token if present.
5. On 401 without recoverable refresh, mark Provider Account reauth-required / unusable.

### 3.2 ChatGPT Codex OAuth

| Stage | Status | Evidence class | Store shape (no secrets) | Signals |
|---|---|---|---|---|
| Obtain (browser OAuth) | `conditionally supported` | Reference-learned + upstream product | Official: `codex login` browser ChatGPT sign-in; reference: PKCE authorize at `https://auth.openai.com/oauth/authorize`, client_id `app_EMoamEEZ73f0CkXaXp7hrann`, redirect `http://localhost:1455/auth/callback`, scope `openid email profile offline_access` | Browser callback returns code → token exchange |
| Obtain (device auth) | `conditionally supported` | Reference-learned + upstream product | Official device-auth login; reference: `.../deviceauth/usercode`, poll `.../deviceauth/token`, verify URL `https://auth.openai.com/codex/device` | Device code + poll until authorization_code/code_verifier |
| Obtain (API key alternative) | `verified` (as official Codex login method) | Upstream-verified | Official docs: sign in with API key for usage-based access; billed on Platform, not ChatGPT plan credits | PixelPlus Auth Mode name is Codex OAuth; API-key path is related but not the same credential class as ChatGPT OAuth bundle |
| Store shape | `conditionally supported` | Reference-learned + upstream product | Official cache: `~/.codex/auth.json` or OS keyring. Reference storage: `id_token`, `access_token`, `refresh_token`, `account_id`, `email`, `type=codex`, `expired`/`last_refresh`, plan attributes | Store only encrypted in vault; metadata may keep email/account_id/plan_type/expiry |
| Refresh | `conditionally supported` | Reference-learned + upstream product | Official: Codex refreshes ChatGPT-managed session automatically; on 401 refresh-and-retry; CI guidance says persist refreshed auth.json and avoid concurrent multi-machine reuse. Reference: refresh_token grant to `https://auth.openai.com/oauth/token`, singleflight, retry; detect `refresh_token_reused` | Expiry timestamp; 401; refresh_token_reused |
| Revoke / logout | `conditionally supported` | Upstream-verified | Official logout clears cached credentials; reseed required if refresh fails | Provider Account becomes reauth-required |

**Concrete lifecycle example:**

1. Tenant starts Codex OAuth device or browser flow.
2. Gateway stores token bundle + account_id + plan_type metadata as Provider Credential / account attributes.
3. Execution uses `Authorization: Bearer <access_token>` and `Chatgpt-Account-Id: <account_id>` against `/backend-api/codex/responses`.
4. Near expiry or 401 → refresh with refresh_token, persist rotated tokens.
5. If refresh_token reused/revoked → disable account and require reauthentication.

---

## 4. Entitlement / plan gates

| Gate | Web Access | Codex OAuth | Status | Evidence |
|---|---|---|---|---|
| Plan type discovery | `/backend-api/accounts/check/...` → `plan_type`; user-info type free/plus/... | JWT/id_token claims + attributes `plan_type` | `conditionally supported` | chatgpt2api account check; CLIProxy free-plan checks |
| Image generation product availability | Official ChatGPT Images available across tiers with limits; thinking-images on Plus/Pro/Business | Official Codex is subscription or API-key based | Web product feature: `verified`; mode-specific reverse path: `conditionally supported` | help.openai.com Images in ChatGPT; developers.openai.com/codex/auth |
| Codex image gate | N/A for pure web path | References restrict Codex image exposure to Plus/Team/Pro; free plan skips image tool injection | `conditionally supported` | chatgpt2api models protocol; CLIProxy `isCodexFreePlanAuth` |
| Separate quotas for web vs codex image | Same external identity can have separate web and codex image quotas in reference design | Same | `conditionally supported` | chatgpt2api README/feature-status explicitly state separate quotas |
| API key vs ChatGPT subscription for Codex | N/A | Official: API key uses Platform billing; ChatGPT sign-in uses workspace/plan entitlements | `verified` | developers.openai.com/codex/auth |

---

## 5. Quota / rate-limit signals

| Signal | Auth Mode | Status | Evidence class | Observable fields / behavior |
|---|---|---|---|---|
| image_gen remaining + reset | Web Access | `conditionally supported` | Reference-learned | `conversation/init.limits_progress[]` where `feature_name=="image_gen"` → `remaining`, `reset_after`; account `status=限流` when quota 0 |
| usage_limit_reached | Codex OAuth | `conditionally supported` | Reference-learned | error.type `usage_limit_reached`; `resets_in_seconds` / `resets_at`; treated as cooldown-worthy quota exhaustion |
| rate_limit_error / rate_limit_exceeded | Codex OAuth | `conditionally supported` | Reference-learned | Transient per-minute style limit; reference intentionally retries rather than long cooldown |
| model capacity | Codex OAuth | `conditionally supported` | Reference-learned | message contains “selected model is at capacity” → mapped toward 429-class handling |
| HTTP 429 generic | Both | `unverified` as complete taxonomy | Gap | Need live corpus of web rate-limit bodies |

---

## 6. Challenge / PoW / captcha / sentinel signals

| Signal | Web Access | Codex OAuth | Status | Evidence |
|---|---|---|---|---|
| Sentinel chat-requirements prepare/finalize | Present (`/backend-api/sentinel/chat-requirements/...`) | Not used in Codex executor path | Web: `conditionally supported`; Codex: `unsupported` (for this path) | chatgpt2api `_get_chat_requirements` |
| Proof-of-work | Required when prepare says `proofofwork.required`; solved via `utils/pow.py` | Not observed | Web: `conditionally supported` | pow seed/difficulty → proof token |
| Turnstile | Required when prepare provides `turnstile.dx`; solved via `utils/turnstile.py` | Not observed | Web: `conditionally supported` | soft challenge |
| Arkose | prepare may set `arkose.required`; reference raises not implemented | Not observed | Web: `conditionally supported` as hard blocker if required | Current reference cannot complete Arkose |
| Cloudflare / bot block | WARP/FlareSolverr support; clearance refresh | Image path comments mention Cloudflare 1010 risk; UA sanitization | Both: `conditionally supported` as operational challenge class | Network/challenge health state |
| Official challenge contract | Not publicly specified as stable API | N/A | `unverified` as stable protocol | High protocol-drift / ban risk signal for #2/#7, not decided here |

---

## 7. Protocol drift risks

| Risk | Mode | Why it matters | Severity for Gateway design |
|---|---|---|---|
| Client version / build number headers | Web | `OAI-Client-Version`, `OAI-Client-Build-Number`, UA/sec-ch-ua fingerprints hard-coded in reference | High — silent upstream rejection/challenge |
| Sentinel SDK / PoW script URL | Web | Defaults to `https://chatgpt.com/backend-api/sentinel/sdk.js`; bootstrap scrapes scripts | High |
| Conversation SSE patch schema | Web | Custom `p/o/v` patch protocol, not OpenAI chat SSE | High for stream translator |
| Codex Responses experimental headers / originator | Codex | `Originator`, `Chatgpt-Account-Id`, beta/feature headers, responses-lite websocket metadata | High |
| Static model catalog drift | Codex | `codex_client_models.json` can lag real availability | Medium |
| previous_response_id semantics | Codex | Reference deletes/handles specially; error class `previous_response_not_found` | Medium for multi-turn |
| Dual image surfaces | Both | Web image_gen conversation vs Codex image_generation tool can diverge independently | High for Capability Snapshot and routing |
| Refresh client_id mismatch | Both | Web refresh uses platform client `app_2SKx67EdpoN0G6j64rFvigXD`; Codex uses `app_EMoamEEZ73f0CkXaXp7hrann` | High — wrong client_id breaks refresh |

---

## 8. Health failure modes observable by a Gateway

Recommended health classes (technical signals only; acceptance policy deferred to #7/#17):

| Health / failure class | Web Access signals | Codex OAuth signals | Suggested Gateway effect |
|---|---|---|---|
| auth_expired | 401 / InvalidAccessToken; refresh fail | 401 authentication_error; refresh_token_reused; expired bundle | Mark reauth-required; exclude from routing |
| quota_exhausted | image_gen remaining=0 + restore_at | usage_limit_reached + resets_in_seconds/resets_at | Cooldown until reset; capability temporarily unavailable |
| rate_limited_transient | unknown/partial | rate_limit_error / rate_limit_exceeded | Short backoff, retry ownership later in #16 |
| challenged | sentinel/turnstile/arkose/cloudflare failures | cloudflare/bot blocks on image | Mark challenged; require operator/user remediation |
| entitlement_missing | free plan without needed feature | free plan without image tool; non-Plus/Team/Pro for codex image aliases | Capability Snapshot false for gated ops |
| protocol_drift | unexpected SSE/sentinel schema, client version reject | unexpected responses event schema, header rejection | Degrade adapter; invalidate snapshots |
| upstream_capacity | unverified | model at capacity messages | Temporary exclude model or account |
| continuity_break | lost conversation_id | previous_response_not_found | Fail request without blind fallback across accounts |
| cancel_local_only | client closed stream | context cancel / websocket close | Local abort known; upstream stop unverified |

---

## 9. Evidence tables

### 9.1 Local reference sources

| Source path | Mode | What it supports | Accessed | Confidence |
|---|---|---|---|---|
| `.ref/chatgpt2api/services/openai_backend_api.py` | Web (+ some Codex image) | conversation SSE, sentinel, models, image prepare/upload/generate, codex/responses image events, user info/quota | 2026-07-14 | High for reference behavior; low for upstream stability |
| `.ref/chatgpt2api/services/account_service.py` | Web credential pool | account shape, refresh, invalidation, quota status, plan filters | 2026-07-14 | High reference |
| `.ref/chatgpt2api/services/oauth_login_service.py`, `services/openai_oauth.py` | Web-adjacent OAuth import | platform OAuth client/audience/redirect/scopes | 2026-07-14 | Medium |
| `.ref/chatgpt2api/utils/{pow,sentinel,turnstile}.py` | Web challenges | PoW/sentinel/turnstile solving | 2026-07-14 | High reference |
| `.ref/chatgpt2api/docs/upstream-sse-conversation.md` | Web streaming | SSE event taxonomy | 2026-07-14 | High reference |
| `.ref/chatgpt2api/docs/feature-status.en.md`, `README.md` | Both image paths | feature matrix, codex image plan gate, separate quotas | 2026-07-14 | Medium (project claims) |
| `.ref/chatgpt2api/services/protocol/openai_v1_image_edit.py` | Web mask adaptation | client-side mask composite | 2026-07-14 | High reference |
| `.ref/CLIProxyAPI/internal/auth/codex/*`, `sdk/auth/codex.go`, `sdk/auth/codex_device.go` | Codex OAuth | browser/device OAuth, refresh, storage | 2026-07-14 | High reference |
| `.ref/CLIProxyAPI/internal/runtime/executor/codex_executor.go` | Codex chat/tools | responses execute/stream, headers, errors, refresh, image tool injection | 2026-07-14 | High reference |
| `.ref/CLIProxyAPI/internal/runtime/executor/codex_openai_images.go` | Codex image | generate/edit/mask via responses | 2026-07-14 | High reference |
| `.ref/CLIProxyAPI/internal/runtime/executor/codex_websockets_executor.go` | Codex stream lite | websocket transport | 2026-07-14 | Medium-High |
| `.ref/CLIProxyAPI/internal/registry/models/codex_client_models.json` | Codex models | static model metadata | 2026-07-14 | Medium (static, drift-prone) |

### 9.2 Official / public upstream sources

| URL | Mode | What it supports | Accessed | Confidence |
|---|---|---|---|---|
| https://developers.openai.com/codex/auth | Codex OAuth | ChatGPT sign-in vs API key; credential cache; auto refresh; logout; enterprise access tokens | 2026-07-14 | High |
| https://developers.openai.com/codex/auth/ci-cd-auth | Codex OAuth | refresh-on-stale/401, auth.json persistence, no concurrent shared refresh token reuse | 2026-07-14 | High |
| https://help.openai.com/en/articles/11084440-images-in-chatgpt | Web product images | ChatGPT Images product exists; tier notes | 2026-07-14 | Medium (page partially cookie-gated in fetch; search snippet used) |
| https://developers.openai.com/api/docs/guides/image-generation | Official Image API (Platform) | gpt-image-2 generate/edit semantics on Platform API | 2026-07-14 | Medium for product family; **not** proof of Web/Codex reverse surfaces |
| https://openai.com/index/introducing-chatgpt-images-2-0/ | Product announcement | Images 2.0 product direction | 2026-07-14 | Medium |

No official public stable specification was found for ChatGPT consumer `/backend-api/conversation` or private reverse-engineered sentinel protocol. Those remain reference-learned.

---

## 10. Explicit gaps and prototype needs

### 10.1 Gaps (must not be papered over)

1. **No live probe in this ticket** against a real Plus/Team/Pro/Free Provider Account for either Auth Mode.
2. **Web cancel/abort** cooperative upstream behavior unverified.
3. **Web rate-limit body taxonomy** incomplete vs Codex usage_limit handling.
4. **Arkose-required accounts** unsupported by current reference path.
5. **True mask/inpaint fidelity** on Web Access is client composite only; Codex has mask field mapping but no live visual verification.
6. **Model discovery freshness** for Codex is static JSON in reference; live listing endpoint not verified here.
7. **Whether one external identity’s web token can call Codex endpoints and vice versa** not verified; references keep separate source types and client_ids, so treat as separate credentials.
8. **Official ToS/account-ban acceptability** intentionally out of scope (#2/#7).

### 10.2 Prototype / live-probe recommendations (for later issues, not implemented here)

Minimum fixture pack per Auth Mode:

1. **Credential bootstrap probe**
   - Web: me + conversation/init + accounts/check
   - Codex: OAuth device/browser once, then token refresh dry-run
2. **Chat stream fixture** one short prompt, capture sanitized SSE
3. **Image generate fixture** on Plus and Free if available
4. **Image edit + mask fixture**
5. **Quota exhaustion / 401 / challenge captures**
6. **Cancel mid-stream** observe whether upstream continues billing/generation
7. **Multi-turn continuity** same account vs forced account switch

These probes should feed Capability Snapshot schema (#10), connection lifecycle (#9), health (#17), and risk envelope (#7).

---

## 11. Compact conclusions for downstream issues

1. Treat **ChatGPT Web Access** and **ChatGPT Codex OAuth** as two Auth Modes, two credential lifecycles, two capability snapshots.
2. Most execution capabilities are **`conditionally supported`** from strong reference implementations, not **`verified`** end-to-end on upstream by this research pass.
3. Official docs **`verify`** Codex ChatGPT-login/API-key product auth model and refresh/caching expectations; they do **not** verify reverse web backend protocol details.
4. Image generation/edit are product-real on ChatGPT, but mode-specific reverse/Codex paths remain entitlement-gated and reference-learned.
5. Web Access carries sentinel/PoW/Turnstile/Arkose/Cloudflare challenge complexity; Codex path mainly carries OAuth refresh and usage-limit complexity.
6. No capability should be hardcoded as always-on for “ChatGPT”; only per-Provider-Account Capability Snapshot after probe.

---

## 12. Acceptance criteria checklist

- [x] Matrix separates ChatGPT Web Access and ChatGPT Codex OAuth
- [x] Each capability marked `verified` / `conditionally supported` / `unsupported` / `unverified`
- [x] Credential lifecycle, entitlement, quota, challenge, protocol drift documented with evidence or explicit gaps
- [x] Reference-learned behaviors distinguished from upstream-verified behaviors
