# OpenAI-Compatible Inference Contract (Prototype)

- Status: Accepted for prototype evidence (issue #18)
- Date: 2026-07-18
- Parent: [#1](https://github.com/monet88/pixelplus/issues/1)
- Issue: [#18](https://github.com/monet88/pixelplus/issues/18)
- Base commit: `d1c2830`
- Vocabulary source: `CONTEXT.md`
- Related Client API Key / admission: `docs/spec/client-api-key-lifecycle-and-admission-controls.md` (#8)
- Related Capability Snapshot / model availability: `docs/spec/capability-snapshot-and-model-availability-semantics.md` (#10)
- Related routing / fallback / affinity: `docs/spec/tenant-scoped-routing-fallback-affinity-leases.md` (#11)
- Related chat execution / streaming: `docs/spec/chat-execution-and-streaming-lifecycle.md` (#12)
- Related Asset lifecycle: `docs/spec/asset-exchange-authorization-and-retention-lifecycle.md` (#13)
- Related durable Render Job / output retry: `docs/spec/durable-render-job-and-output-retry-lifecycle.md` (#14)
- Related canonical errors / retry ownership: `docs/spec/canonical-errors-and-retry-ownership.md` (#16)
- Related Provider Account Health: `docs/spec/provider-account-health-cooldown-and-operator-controls.md` (#17)
- OpenAPI artifact: `contracts/openapi/pixelplus-public-api-v0alpha.yaml` (`info.version=0.0.0-prototype`, `x-pixelplus-artifact-status: prototype`)
- Validator: `node scripts/validate-openapi-contract.mjs contracts/openapi/pixelplus-public-api-v0alpha.yaml`

## 1. Scope and non-goals

### 1.1 Scope

This document is the **retained non-final contract prototype** for the OpenAI-compatible **inference** Public API surface. It chooses concrete wire representation (paths, security scheme, logical request/response shapes, SSE event objects, canonical error envelope encoding, and examples) for:

1. Model / capability discovery
2. Chat non-streaming and streaming + cancel
3. Asset upload/metadata/content retrieval
4. Image generation / edit / inpaint as durable Render Jobs
5. Render Job poll / cancel / output-delivery retry
6. Canonical error examples that preserve #16 retryability, remediation, commit certainty, and redaction

It is **specification / prototype work**. It does **not** implement Gateway, server, database, worker, or Adapter code and does not add package dependencies.

### 1.2 Explicit separation of decisions

| Class | Content | Authority |
|---|---|---|
| **Locked inherited semantics** | Tenant ownership / non-enumeration, Client API Key + admission, Capability Snapshot, routing/fallback/leases, chat lifecycle, Asset lifecycle, Render Job + output-only retry, canonical errors + retry ownership, Provider Account Health | #6–#17 specs under `docs/spec/` |
| **#18 wire decisions** | HTTP path grouping, OpenAPI security scheme encoding, field names for request/response/SSE/error envelope, async 202 image job surface, chat cancel path, output retry path, example set, validator checks | this document + OpenAPI artifact |
| **Non-goals / deferred** | Final versioned Public API packaging, management contract, numeric limit values, full external OpenAPI metaschema CI, runtime HTTP conformance harness | #17 (numbers), #20 (unified versioned artifact), runtime tickets |

### 1.3 Non-goals

This prototype does **not**:

- Publish a final, versioned Public API or claim production stability.
- Implement Gateway/server/database/worker/Adapter code.
- Redefine domain semantics already locked by #6–#17.
- Freeze management-plane routes (account connect, vault ops, operator controls).
- Collapse admission errors with Provider runtime errors, or treat `commit_status=unknown` as safe retry.
- Expose `tenant_id`, credentials, prompt/content bytes, raw Provider payloads, or foreign-resource existence.

**#20** consolidates this inference tracer with the management contract into the final versioned Public API artifact.

### 1.4 Normative language

- **MUST / MUST NOT / REQUIRED**: product and security policy for the eventual Public API. Violation is a defect.
- **SHALL**: same force as MUST for observable Public API behavior.
- **SHOULD**: strongly preferred default.
- **MAY**: optional surface that cannot weaken a MUST rule.

Prototype status means the **representation choices** here are accepted evidence for #18; field renames that preserve semantics remain possible until #20 freezes the versioned contract.

---

## 2. Official compatibility baseline and PixelPlus extensions

### 2.1 Official OpenAI-compatible baseline paths

The official OpenAI API documents the following inference-relevant surfaces that PixelPlus treats as the compatibility baseline (relative to `https://api.openai.com/v1`):

| Official path | Role |
|---|---|
| `GET /v1/models` | List models |
| `POST /v1/chat/completions` | Chat completions (stream and non-stream) |
| `POST /v1/images/generations` | Image generation |
| `POST /v1/images/edits` | Image edit |

See Sources.

### 2.2 PixelPlus extensions on the same `/v1` surface

PixelPlus **adds** first-class surfaces that are not 1:1 passthroughs of OpenAI:

| PixelPlus path | Why |
|---|---|
| `POST /v1/images/inpaints` | Explicit masked inpaint; never silently degrade to edit (#10/#13/#14) |
| `POST /v1/assets`, `GET /v1/assets/{asset_id}`, `GET /v1/assets/{asset_id}/content` | Tenant-owned Asset exchange instead of raw multipart Provider payload passthrough (#13) |
| `GET /v1/render-jobs/{job_id}`, `POST .../cancel`, `POST .../outputs/{output_entry_id}/retry` | Durable Render Job lifecycle and output-delivery retry (#14) |
| `POST /v1/chat/executions/{execution_id}/cancel` | Explicit chat cancel with honest upstream-stop flags (#12) |
| `x_pixelplus` capability offers / safe execution metadata | Capability Snapshot and safe routing metadata (#10/#11/#12) |
| Canonical error envelope | Provider-independent codes, retryability, commit certainty (#16) |

Image generation/edit/inpaint in PixelPlus are **async durable jobs returning 202**, not long-lived single-connection Provider passthroughs.

Compatibility is therefore deliberately bounded: PixelPlus preserves the baseline path grouping and OpenAI-like core chat/model/image request fields, but its streaming terminal protocol and async image responses are **not byte-for-byte drop-in replacements** for clients that require OpenAI `[DONE]` chunks or synchronous image payloads. Those clients need PixelPlus-aware handling for `open`/terminal SSE events, Render Job polling, Asset retrieval, and cancellation.

---

## 3. Locked wire decisions

### 3.1 Server and path grouping

- OpenAPI `servers[0].url` is `/v1` (or an absolute URL ending in `/v1`).
- Path keys in the artifact are **relative** to that server (for example `/chat/completions`, not `/v1/chat/completions`).
- Operation descriptions document the full public route as `/v1/...`.

### 3.2 Operation surface (relative to `/v1`)

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/models` | Model list + owner-Tenant capability offers |
| `POST` | `/chat/completions` | Chat non-stream JSON or SSE stream |
| `POST` | `/chat/executions/{execution_id}/cancel` | Chat cancel acknowledgement |
| `POST` | `/assets` | Multipart upload `kind=input\|mask` + `file` |
| `GET` | `/assets/{asset_id}` | Same-Tenant asset metadata |
| `GET` | `/assets/{asset_id}/content` | Same-Tenant asset bytes |
| `POST` | `/images/generations` | Create durable generation job → 202 |
| `POST` | `/images/edits` | Create durable edit job → 202 |
| `POST` | `/images/inpaints` | Create durable inpaint job → 202 |
| `GET` | `/render-jobs/{job_id}` | Poll job state / outputs |
| `POST` | `/render-jobs/{job_id}/cancel` | Truthful job cancel |
| `POST` | `/render-jobs/{job_id}/outputs/{output_entry_id}/retry` | Output placement retry only |

### 3.3 Shared Client API Key scheme

Every operation uses the same security scheme:

- Name: `ClientApiKey`
- Type: HTTP bearer
- `bearerFormat`: `sk-pxp_<public_locator>_<secret>`

Security Principal (`tenant_id`, `client_api_key_id`) is derived server-side from the key. Clients **MUST NOT** supply `tenant_id` in request bodies or query.

### 3.4 Server-owned identifiers and safe metadata

Server-owned identifiers include `request_id`, `correlation_id`, `execution_id`, `job_id`, `asset_id`, `output_entry_id`, and same-Tenant `provider_account_id` when already authorized on that path.

Safe metadata may appear under `x_pixelplus` (chat/models) or job fields. It **MUST NOT** include credentials, raw Provider payloads, prompt/content bytes, or foreign ids.

### 3.5 Chat request / response

**Request (logical):**

- OpenAI-like: `model`, `messages`, optional `stream`, common generation knobs.
- Optional PixelPlus routing object `x_pixelplus`:
  - `provider_account_id` (same-Tenant pin; foreign → `resource_not_found`)
  - `allow_fallback` (hint; still subject to Tenant policy + proof-of-non-commit)
  - optional `conversation_id` (affinity key)
- Header `Idempotency-Key` for scoped replay.
- **No** `tenant_id`.

**Non-stream response (logical):**

- OpenAI-like: `id`, `object=chat.completion`, `created`, `model`, `choices`, `usage`.
- Plus `x_pixelplus` safe metadata (`request_id`, `execution_id`, `provider_account_id`, `finish_class`, …).
- No Provider raw shape.

### 3.6 Chat streaming SSE

SSE `text/event-stream` payload objects are a `oneOf`:

| Schema | `type` | Role |
|---|---|---|
| `ChatOpenEvent` | `open` | Exactly once, first |
| `ChatDeltaEvent` | `delta` | Zero or more; OpenAI-compatible `choices[].delta` |
| `ChatHeartbeatEvent` | `heartbeat` | Zero or more; no assistant tokens |
| `ChatCompletedEvent` | `completed` | Terminal success + `finish_class` |
| `ChatFailedEvent` | `failed` | Terminal failure + canonical error |
| `ChatCanceledEvent` | `canceled` | Terminal cancel |

**Normative sequence:**

```text
open -> delta* (heartbeats allowed interleaved) -> exactly one of {completed, failed, canceled}
```

Hard rules:

1. `open` MUST carry server-owned `request_id` and `execution_id`; the latter is the actionable handle for explicit cancel.
2. Exactly one terminal event per stream.
3. No post-terminal data (no further `delta` / `heartbeat` / second terminal).
4. No OpenAI `[DONE]` second sentinel; the terminal event is authoritative.
5. Client disconnect is **implicit cancel**.
6. Cancellation is **not** proof that upstream stopped.
7. `commit_status=unknown` forbids automatic fallback/retry (#12/#16).

### 3.7 Chat cancel response

`POST /v1/chat/executions/{execution_id}/cancel` returns an honest acknowledgement:

| Field | Rule |
|---|---|
| `cancel_state` | `cancel_requested` or `canceled` only when true |
| `upstream_abort_attempted` | boolean |
| `upstream_stop_confirmed` | boolean; **MUST** be false unless stop is confirmed |

Never claim stop without confirmation.

### 3.8 Models + capability offers

`GET /v1/models` returns OpenAI core fields (`id`, `object`, `created`, `owned_by`) plus one or more **currently offerable** owner-Tenant pairs:

```text
x_pixelplus.offers[] = {
  provider_account_id,   // owner-Tenant only
  operation,
  operation_status,      // verified|conditionally_supported only
  model_slug,            // observed slug, not static catalog invention
  offerable=true,
  streaming_class?,      // real|synthetic when relevant
  freshness=fresh,
  verified_at?
}
```

A client-facing model list is the set of offerable pairs from #10: stale/invalid snapshots and `unsupported`/`unverified` capabilities are excluded from the list, then rejected with their canonical errors if explicitly requested. Every model row therefore has at least one `x_pixelplus.offers[]` entry.

Never: Provider credential, raw evidence, foreign account data, or static Provider catalog.

### 3.9 Image operations as async durable jobs

| Operation | Required body | HTTP result |
|---|---|---|
| generation | `model`, `prompt` | `202` + `RenderJob` (`operation=image_generation` immutable) |
| edit | `model`, `prompt`, `input_asset_id` | `202` + `RenderJob` (`operation=image_edit`) |
| inpaint | `model`, `prompt`, `input_asset_id`, `mask_asset_id` | `202` + `RenderJob` (`operation=inpaint`) |

Rules:

1. Use JSON **Asset references**, not raw Provider payloads.
2. Client polls `GET /v1/render-jobs/{job_id}` and may cancel via `POST .../cancel`.
3. `completed` means render capture is durable; **output entries MAY remain `pending`**.
4. `POST .../outputs/{output_entry_id}/retry` targets one entry and **MUST NOT** re-render (`re_render=false` always).

### 3.10 RenderJob / Asset logical shapes

**RenderJob** exposes:

- `lifecycle_state`: `queued` \| `running` \| `cancel_requested` \| `canceled` \| `failed` \| `completed`
- `execution_phase` while non-terminal: `preflight` \| `upstream` \| `capturing_result` \| `placing_output`
- `progress.source`: `reported` \| `estimated` \| `unknown`
- monotonic `state_revision`
- `output_entries[]` with `delivery_state` `pending` \| `available` \| `expired` \| `failed`
- `asset_id` only when delivery is `available`
- truthful cancel fields (`upstream_abort_attempted`, `upstream_stop_confirmed`)

**Asset** upload: multipart `kind=input|mask` + `file`. The owning-Tenant metadata projection preserves the locked #13 fields: `asset_id`, `kind`, canonical `content_type`, `byte_size`, `checksum`, `origin`, `created_at`, and `retention_class`; decoded `width`/`height`, `expires_at`, and generated-output `source_job_id` appear when applicable. `tenant_id` remains server-side authority and is not accepted from the client. Metadata and content retrieval are same-Tenant only.

### 3.11 Canonical error envelope

Required fields:

| Field | Required |
|---|---|
| `code` | yes |
| `category` | yes |
| `status_class` | yes |
| `retryability` | yes |
| `remediation` | yes |
| `request_id` | yes |

Conditional: `correlation_id`, `operation`, `failure_stage`, `retry_after_class`, `retry_after_seconds` (finite logical projection only), `commit_status`, `idempotency_state`, `resource_reference`, `safe_context`.

**MUST NOT** expose: `tenant_id`, raw Provider data, credential material, prompt/content, foreign ids.

When emitted, `retry_after_seconds` is the ceiling in seconds to the latest matching finite `retry_not_before`, with a minimum of one second (#17). Numbers in examples are illustrative response-time projections, not frozen cooldown policy. Non-time gates (for example `commit_status=unknown` forcing `new_request_only`) **omit** the field.

### 3.12 Required error examples (minimum set)

The OpenAPI artifact includes reusable examples for these distinct boundaries (retryability / remediation / commit certainty / redaction preserved):

1. Admission `rate_limit`
2. Admission `concurrency_limit`
3. Admission `quota_exhausted`
4. `capability_unsupported`
5. `capability_unverified`
6. `snapshot_stale`
7. `account_not_usable`
8. Provider runtime `provider_rate_limited` with `commit_status=unknown` and `retryability=new_request_only` (no `retry_after_seconds`)
9. Provider runtime `provider_quota_exhausted`
10. `provider_auth_expired`
11. `provider_challenged`
12. `upstream_protocol_drift`
13. `execution_possibly_committed`
14. `resource_not_found` (no `resource_reference`)

Admission examples have `category=failure_stage=admission` and occur before A6; Provider rate/quota examples have `category=execution` and an `upstream_*` failure stage after admission. Sharing HTTP 429 does not collapse these canonical codes.

---

## 4. Cause → effect conformance matrix

These rows are prototype **conformance obligations**. Runtime harness arrives with implementation tickets; the OpenAPI + validator prove representation readiness.

| Cause | Observable effect |
|---|---|
| Client requests `chat_streaming` but Capability Snapshot marks `chat_streaming` unsupported / not offerable / not fresh | Gateway rejects with `capability_unsupported` or `snapshot_stale` / `capability_unverified` as appropriate **before** upstream; **Adapter calls = 0** |
| Post-attempt Provider rate limit with **unknown** commit certainty | Emit `provider_rate_limited` with `commit_status=unknown`, `retryability=new_request_only`; **no** automatic fallback/retry; omit finite `retry_after_seconds` when the non-time gate dominates |
| Successful Provider render then output Asset placement failure | Job may be `completed` with an output entry `pending`/`failed`; generation/edit/inpaint **calls = 1**; output retry uses placement key only and **never re-renders** |
| Cross-Tenant `job_id` / `asset_id` / `provider_account_id` / `execution_id` | Indistinguishable `resource_not_found`; **zero** foreign read/decrypt/Adapter call; no `resource_reference` |

Additional inherited cause→effect constraints (already normative in #6–#17, represented here):

- Explicit pin to foreign account → `resource_not_found`, zero Adapter call.
- Inpaint without mask or with unsupported inpaint capability → validation/capability failure, never silent downgrade to `image_edit`.
- Chat stream after terminal → no further events.
- Disconnect mid-stream → implicit cancel path with honest stop flags.

---

## 5. Artifacts and validation

| Artifact | Role |
|---|---|
| `docs/spec/openai-compatible-inference-contract.md` | This normative prototype decision record |
| `contracts/openapi/pixelplus-public-api-v0alpha.yaml` | OpenAPI 3.1.1 JSON-compatible YAML / JSON Schema 2020-12 non-final tracer (`x-pixelplus-artifact-status: prototype`) |
| `scripts/validate-openapi-contract.mjs` | Executable representation checks (Node entrypoint; Draft 2020-12 examples validated through required environment prerequisite Python + `jsonschema`) |

Run from repository root (Python with `jsonschema` Draft 2020-12 support is a required validation-environment prerequisite; no repository dependency is added):

```bash
node scripts/validate-openapi-contract.mjs contracts/openapi/pixelplus-public-api-v0alpha.yaml
```

The validator:

1. Parses JSON-compatible YAML (JSON is a YAML 1.2 subset) and checks OpenAPI `3.1.1` + core structure + top-level `x-pixelplus-artifact-status: prototype`
2. Recursively walks internal `$ref`s and fails external or cyclic refs
3. Validates component/schema/media-type examples with required-environment `jsonschema` Draft 2020-12
4. Checks required operations exist and use `ClientApiKey`
5. Checks request schemas do not expose `tenant_id`
6. Checks client model rows contain only fresh offerable pairs
7. Checks actionable stream-open identity, retry delay lower bound, conditional output `asset_id`, and locked Asset metadata fields
8. Checks admission/Provider error separation, required error example names/codes, and stream event/terminal set
9. Checks description corpus for sequence / no-post-terminal / cancel / unknown-commit / no-rerender invariants

**Gap (explicit):** this is **not** a full external OpenAPI metaschema validator and not a runtime HTTP/E2E test.

---

## 6. Relationship to prior issues

| Topic | Inherited lock | This prototype encodes |
|---|---|---|
| Ownership / non-enumeration (#6) | Foreign/unknown → 404-class | `resource_not_found` shape + examples |
| Client API Key (#8) | Bearer `sk-pxp_…` | `ClientApiKey` scheme on every op |
| Capability (#10) | offerable/fresh/streaming class | `/models` offers + pre-upstream error examples |
| Routing (#11) | pin/fallback rules | `x_pixelplus` fields only; no `tenant_id` |
| Chat (#12) | open→delta*→one terminal; cancel honesty | SSE schemas + cancel route |
| Asset (#13) | same-Tenant Asset exchange | `/assets*` routes |
| Render Job (#14) | durable job + output-only retry | image 202 + `/render-jobs*` |
| Canonical errors (#16) | envelope + codes | `CanonicalError` + examples |
| Health (#17) | health→error mapping | runtime codes only; no new health vocabulary |

---

## 7. Finality statement

This is **not** the final versioned Public API.

- `info.version` is `0.0.0-prototype`.
- Artifact paths live under `contracts/openapi/` as a **tracer** (`pixelplus-public-api-v0alpha.yaml`).
- **Issue #20** consolidates this inference contract with the management contract into the versioned Public API package.
- Downstream implementation MUST preserve the locked inherited semantics and the wire invariants above; cosmetic field renames before #20 require an explicit contract update.

---

## 8. Sources

Official OpenAI references used as the compatibility baseline:

1. OpenAI API base and models surface — `https://platform.openai.com/docs/api-reference`
2. Chat Completions — `https://platform.openai.com/docs/api-reference/chat`
3. Images generations — `https://developers.openai.com/api/reference/resources/images/methods/generate` (`POST /images/generations`)
4. Images edits — `https://developers.openai.com/api/reference/resources/images/methods/edit` (`POST /images/edits`)
5. OpenAPI definition listing supported batch endpoints including `/v1/chat/completions`, `/v1/images/generations`, `/v1/images/edits` — `https://platform.openai.com/docs/static/api-definition.yaml`

PixelPlus domain authority remains the accepted specs under `docs/spec/` for issues #6–#17; OpenAI URLs define compatibility baseline paths only, not Tenant ownership, retry, or commit-safety semantics.
