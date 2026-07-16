# Canonical Errors and Retry Ownership

- Status: Accepted for specification (issue #16)
- Date: 2026-07-15
- Parent: [#1](https://github.com/monet88/pixelplus/issues/1)
- Issue: [#16](https://github.com/monet88/pixelplus/issues/16)
- Vocabulary source: `CONTEXT.md`
- Related ownership invariants: `docs/spec/tenant-ownership-authorization-invariants.md` (#6)
- Related risk envelope: `docs/spec/auth-mode-risk-envelope-and-kill-criteria.md` (#7)
- Related Client API Key / admission: `docs/spec/client-api-key-lifecycle-and-admission-controls.md` (#8)
- Related connection / credential lifecycle: `docs/spec/provider-account-connection-and-credential-lifecycle.md` (#9)
- Related Capability Snapshot / model availability: `docs/spec/capability-snapshot-and-model-availability-semantics.md` (#10)
- Related tenant-scoped routing / fallback / affinity / lease: `docs/spec/tenant-scoped-routing-fallback-affinity-leases.md` (#11)
- Related chat execution / streaming: `docs/spec/chat-execution-and-streaming-lifecycle.md` (#12)
- Related Asset exchange / authorization / retention: `docs/spec/asset-exchange-authorization-and-retention-lifecycle.md` (#13)
- Related durable Render Job / output retry: `docs/spec/durable-render-job-and-output-retry-lifecycle.md` (#14)
- Related vault and sensitive-data lifecycle: `docs/spec/credential-vault-and-sensitive-data-lifecycle.md` (#15)

## 1. Scope and non-goals

### 1.1 Scope

This specification locks the **canonical error vocabulary, safe diagnostic contract, retryability semantics, and retry ownership** for the PixelPlus Gateway. It creates one Provider-independent failure language across Public API admission, account/routing gates, chat execution, image Render Jobs, output delivery, credential lifecycle, workers, and Provider Adapters.

It codifies parent #1's requirement that a client can distinguish authentication expiry, quota, rate limit, challenge, timeout, cancellation, upstream rejection, and protocol drift without seeing Adapter internals or secret material. It consumes the lifecycle and status decisions already accepted in #6–#15; it does not replace them.

It covers:

1. Stable semantic error codes and their HTTP-oriented status classes.
2. The logical error/problem envelope and the safe diagnostic context that may accompany it.
3. The distinction between Public API authentication/admission failures and Provider/runtime failures.
4. Retryability classes, remediation classes, commit certainty, and the proof required for a safe re-attempt.
5. Exactly one retry owner for each non-idempotent operation and the prohibition on independent retry multiplication across HTTP, execution, Adapter, queue, routing, and worker layers.
6. Chat idempotency conflict/in-progress/uncertain semantics.
7. The separation between image render retry and output retrieval/staging/placement retry.
8. Redaction, correlation, audit, non-enumeration, and observable conformance obligations.

This is **specification work**. It does not implement the Gateway, transport, Adapter, queue, worker, storage, vault, or Public API code.

### 1.2 Non-goals

This document does **not**:

- Redefine Tenant ownership, Security Principal formation, or foreign-resource non-enumeration (#6).
- Redefine Client API Key hashing, scope, admission ordering, limit numbers, or revocation propagation (#8).
- Redefine Provider Account lifecycle, operational health vocabulary, Auth Mode risk gates, or remediation moments (#7/#9).
- Redefine Capability Snapshot taxonomy, freshness, or offerable computation (#10).
- Redefine routing candidate construction, affinity, leases, fallback eligibility, or no-fallback conditions (#11).
- Redefine chat stream ordering, cancellation, residual accounting, or authoritative proof-of-non-commit (#12).
- Redefine Asset validation, retention, expiry, tombstones, or storage reservations (#13).
- Redefine Render Job state transitions, worker fencing, attempt commit states, result manifests, or output placement keys (#14).
- Redefine vault cryptography, purpose-bound decrypt, retention holds, or sensitive-data redaction requirements (#15).
- Freeze numeric retry budgets, cooldowns, timeout values, drain windows, backoff durations, retry-after durations, or retention TTLs. Those are #17-tunable.
- Freeze JSON field names, RFC problem+json details, HTTP paths, headers, SSE event encoding, or OpenAPI schema. Those are #18/#20. The semantic HTTP-oriented status classes already fixed by #6–#15 remain normative; #18/#20 encode them without collapsing distinctions.
- Expose retry-owner implementation details, Adapter names, queue topology, worker identities, stack traces, or raw Provider responses to a client.
- Make a client-visible retryability signal an authorization to repeat an uncertain or committed Provider operation.

Downstream work **MUST** preserve every decision here. It may choose equivalent wire encodings and stricter limits, but it MUST NOT:

- Reuse a stable error code for a different meaning.
- Collapse admission `rate_limit`/`quota_exhausted` into Provider runtime `provider_rate_limited`/`provider_quota_exhausted`.
- Treat `unknown` commit status as `not_committed`.
- Allow more than one layer to retry the same non-idempotent operation independently.
- Put Provider Credential material, Client API Key material, prompt/image content, temporary bearer handles, raw upstream payloads, or foreign-resource existence into an error or diagnostic projection.
- Use output-delivery retry to reopen image generation/edit/inpaint or consume another image-generation quota unit.

### 1.3 Normative language

- **MUST / MUST NOT / REQUIRED**: product and security policy. Violation is a defect.
- **SHALL**: same force as MUST for observable Public API behavior.
- **SHOULD**: strongly preferred default; deviation needs an operator-recorded exception.
- **MAY**: optional behavior that cannot weaken a MUST rule.

### 1.4 Relationship to prior issues

| Topic | Already locked | This document adds |
|---|---|---|
| Ownership / non-enumeration | Invalid authentication and foreign/unknown resource outcomes retain the status-class distinctions already fixed by #6; owned-but-forbidden remains distinct | Stable codes, safe context, and equality rules for those outcomes |
| Admission | A0–A6 order; `rate_limit`, `concurrency_limit`, `quota_exhausted`; no execution before A6 (#8) | Canonical code ownership and separation from runtime Provider errors |
| Account health / remediation | Health tokens and account remediation classes (#9) | Mapping from health/failure signals to Provider-independent codes and retryability |
| Capability | `capability_unsupported`, `capability_unverified`, `snapshot_stale`, `model_unavailable` (#10) | Stable code contract and pre-upstream diagnostic rules |
| Routing | Candidate gate, fallback opt-in, no-fallback set, same-Tenant and capability constraints (#11) | Canonical routing/fallback failure codes and shared retry chain |
| Chat | Single Gateway execution retry boundary, idempotency claim, proof-of-non-commit, terminal semantics (#12) | Cross-operation retry-owner rule and idempotency error codes |
| Asset / Render Job | Validation, expiry, storage cap, attempt commit states, output-only retry (#13/#14) | Distinct render versus delivery errors and retry ownership |
| Sensitive data | Redaction, audit-before-allow, fail-closed vault/dependency semantics (#15) | Error-context allowlist, correlation contract, and public normalization rule |
| Numeric tuning | Deferred named classes and values (#7/#9/#10/#14/#15) | No numeric retry/cooldown/TTL values; only stable class names |
| Wire contract | Deferred JSON/OpenAPI/SSE details (#18/#20) | Semantic code and field meanings that wire contracts MUST encode |

### 1.5 Decision unit

**One canonical error = one safe, Provider-independent explanation of one failed or terminal decision for one request/job/operation, correlated by server-owned identifiers, with exactly one semantic code, one retryability class, and one remediation class.**

Cause → effect:

1. A Tenant A chat request reaches A6 and the Provider returns a rate limit before accepting the payload.
2. The Gateway records the Provider runtime cause as `provider_rate_limited`, not admission `rate_limit`.
3. If the Adapter proves `not_committed`, the Gateway execution owner may consume the bounded retry/fallback chain; the client receives safe retry guidance without seeing Adapter details.
4. If the commit is `unknown`, the same root cause remains observable only through safe runtime metadata, but automatic retry and fallback are forbidden.

---

## 2. Glossary extensions

| Term | Meaning in this document |
|---|---|
| **Canonical error** | Provider-independent semantic failure or terminal outcome represented by one stable `code`, status class, retryability class, remediation class, and safe context. |
| **Stable error code** | Snake-case semantic token whose meaning is append-only within a contract version; a retired code is never silently reused for another condition. |
| **Error category** | High-level boundary such as `authentication`, `authorization`, `admission`, `validation`, `routing`, `capability`, `execution`, `delivery`, `dependency`, or `internal`. |
| **Failure stage** | Safe logical phase where the failure was decided: `authentication`, `authorization`, `admission`, `request_validation`, `routing`, `capability`, `credential`, `asset`, `execution`, `upstream_preflight`, `upstream_execution`, `upstream_capture`, `cancellation`, `recovery`, `output_delivery`, `dependency`, or `internal`. |
| **Retryability class** | Client-safe and operation-aware guidance describing whether an outcome may be retried, replayed idempotently, retried only by an internal owner, or requires a deliberate new request/operator action. |
| **Remediation class** | Stable safe guidance category telling the Tenant or client what action can resolve the outcome. Existing account/capability classes from #9/#10 remain authoritative where they apply. |
| **Retry owner** | The single internal layer authorized to re-attempt one operation class. It is an enforcement boundary, not a client-visible implementation detail. |
| **Commit status** | Durable fact about whether a non-idempotent Provider operation may have been accepted: `not_started`, `not_committed`, `committed`, or `unknown`. |
| **Authoritative proof of non-commit** | Evidence that no Provider payload was transmitted, or an Adapter/Provider contract explicitly proves that no operation was accepted or created. A status code, timeout, reset, missing response, or missing delta alone is not proof after payload transmission. |
| **Retry chain** | One bounded, ordered sequence of safe re-attempts and permitted routing fallbacks owned by one operation layer. Each candidate/attempt is recorded and consumed once. |
| **Idempotent replay** | Returning or waiting for the result of an existing scoped operation without creating another Provider side effect. |
| **Safe context** | Redacted metadata permitted in a canonical error, audit event, log, metric, trace, or support view without exposing secrets, content, raw upstream details, or foreign existence. |
| **Request identifier** | Server-generated `request_id` correlating the Public API interaction and its safe diagnostics. It is not a Client API Key, idempotency key, Tenant selector, or authorization grant. |
| **Correlation identifier** | Server-generated `correlation_id` linking safe records across a workflow such as chat, Render Job, output delivery, or recovery. It never contains prompt, Asset, credential, or bearer material. |
| **Status class** | Bounded semantic outcome family used by this taxonomy (for example `unauthorized`, `not_found`, `forbidden`, `rate_limit`, `provider_rate`, `conflict`, or `internal`). #6–#15 fix the required boundary distinction; #18/#20 encode the class into concrete HTTP/problem/SSE details without changing its meaning. |
| **Output delivery retry** | Retrieval, staging, storage reservation, or output Asset placement retry from an immutable Render Job result manifest. It is never a new generation/edit/inpaint attempt. |

### 2.1 Stable code rules

1. Codes are lower snake-case semantic tokens. They MUST NOT contain Tenant ids, account ids, Provider raw messages, timestamps, secrets, stack traces, or dynamic values.
2. A code's meaning is stable within the Public API contract version. Adding a new code is preferred over changing the meaning of an existing code.
3. Wire-level aliases, localized titles, and human-readable detail MAY vary by contract version or locale, but the semantic `code`, retryability, remediation, and non-enumeration behavior MUST remain compatible.
4. A code MAY carry a safe `cause_class` or `failure_stage` when one code covers multiple lifecycle-owned causes. The cause value MUST come from a bounded enum and MUST NOT become an existence oracle.
5. Internal raw exceptions are inputs to normalization, never canonical codes. The normalized error is the only value allowed across the Public API boundary.

---

## 3. Canonical error envelope and safe diagnostic contract

### 3.1 Logical fields

The wire contract MAY rename or encode these fields differently, but every emitted canonical error or terminal failure MUST have the following logical information when the transport permits:

| Logical field | Required | Rule |
|---|---|---|
| `code` | yes | One stable token from §4; never a raw Provider or Adapter error string. |
| `category` | yes | Canonical boundary category. |
| `status_class` | yes | HTTP-oriented outcome class or stream/job terminal equivalent. Exact JSON/status field is #18/#20. |
| `retryability` | yes | One token from §5.1, derived with commit/idempotency state where applicable. |
| `remediation` | yes | One safe token from §5.2; existing #9/#10 vocabulary is used for account/capability outcomes. |
| `request_id` | yes for a Public API request | Server-generated correlation id. A client-supplied value MAY be separately accepted as a bounded trace hint, but MUST NOT replace the server id or authorize access. |
| `correlation_id` | when a workflow spans records | Server-generated safe workflow id for chat, Render Job, output delivery, recovery, or lifecycle work. |
| `operation` | when known | Canonical operation such as `chat`, `chat_streaming`, `image_generation`, `image_edit`, `inpaint`, `asset_retrieve`, or lifecycle action. |
| `failure_stage` | yes when known | One bounded stage from the glossary. Unknown stage is `internal`, never a raw internal path. |
| `retry_after_class` | when retryability is `retry_after` | Named wait reason only; numeric duration belongs to #17 and wire headers to #18/#20. |
| `commit_status` | when an upstream attempt exists | One of `not_started`, `not_committed`, `committed`, `unknown`; public exposure is limited to safe operation semantics. |
| `idempotency_state` | when idempotency applies | `not_used`, `in_progress`, `terminal`, `conflict`, or `uncertain`; no raw idempotency key value in diagnostics. |
| `resource_reference` | when safe and already authorized | Stable same-Tenant local id such as `job_id`, `asset_id`, or `provider_account_id`; omitted for foreign/unknown identifiers. |
| `safe_context` | optional | Bounded enum values, safe booleans, version numbers, knownness, size class, and timestamps allowed by #15. |

A successful non-streaming response or streaming terminal event MAY carry equivalent terminal metadata. It MUST NOT turn a successful canonical result into an error merely because an upstream Provider used a different framing or end marker.

### 3.2 Safe context allowlist

The following values MAY be included when the caller is authorized to see the owning resource and the owning lifecycle permits it:

- `request_id`, `correlation_id`, `operation`, `failure_stage`, `retryability`, `remediation`, and named `retry_after_class`;
- same-Tenant `provider_account_id`, `job_id`, `asset_id`, `result_manifest_id`, `output_entry_id`, or `client_api_key_id` when already visible on that path;
- safe lifecycle state, operational health class, capability status, model slug already observed for that Tenant, `credential_version`, `crypto_key_version`, `hash_version`, and safe timestamps;
- bounded commit state and idempotency state;
- safe booleans such as `upstream_abort_attempted`, `upstream_stop_confirmed`, `output_delivery_pending`, or `audit_intent_recorded`;
- bounded validation facts such as `size_limit_class`, `media_type_class`, `dimension_class`, or `mask_relationship_class` without raw content.

The following MUST NOT appear in a canonical Public API error, stream event, job status, ordinary audit event, log, metric label/value, trace attribute, support transcript, queue payload, or contract fixture:

- Provider Credential material, Client API Key material, secret hashes, server pepper, cookies, OAuth codes/tokens, authorization headers, or raw bearer handles;
- prompt text, assistant content not already part of an authorized response, Asset bytes, raw multipart fields, image pixels, masks, or content-bearing request payloads;
- raw Provider response bodies, raw upstream URLs, query parameters carrying bearer material, Adapter exception strings, stack traces, queue messages, or worker identities;
- envelope blobs, wrapped DEKs, ciphertext, temporary Provider retrieval URLs, or high-cardinality content-derived values that act as correlation oracles;
- foreign Tenant ids, foreign resource ids, candidate counts, account labels, dimensions, tombstone state, or any detail that distinguishes a foreign resource from an unknown resource;
- a client-supplied `tenant_id` or untrusted diagnostic field treated as server authority.

### 3.3 Correlation and audit

1. The Gateway creates a server-owned `request_id` at the Public API boundary and carries it through admission, routing, execution, terminal, accounting, and safe audit/telemetry records when the operation has those phases.
2. A long-running or durable operation MAY receive a separate `correlation_id` and `job_id`; those ids link records but do not replace Tenant/resource authorization.
3. A retry or recovery keeps the original `correlation_id` and records a new internal attempt identity where the owning lifecycle requires it. It MUST NOT create a new client request identity merely because a worker or queue redelivers a reference.
4. Audit records include safe actor, Tenant, resource, purpose, code, stage, retryability, commit/idempotency state, outcome, and request/correlation ids as permitted by #15. They never store raw content or secret material.
5. Telemetry MAY aggregate by stable safe code, stage, operation, Auth Mode, and redacted local identifiers according to privacy policy. It MUST NOT use prompt, Asset, credential, or bearer values as labels.
6. A request identifier is a correlation aid only. Possession of it MUST NOT authorize replay, decrypt, retrieval, routing, cancellation, or access to another Tenant's records.

### 3.4 Normalization boundary

The normalization boundary is immediately after an internal dependency, Adapter, worker, or lifecycle operation returns an outcome and before that outcome is serialized, logged, traced, audited, queued, or exposed to another layer.

1. The normalizer maps known internal/provider outcomes to one canonical code and bounded safe context.
2. Unknown or malformed internal outcomes map to `internal_error` or `integrity_failure` according to whether the invariant or stored record was compromised; raw details remain outside ordinary diagnostics.
3. A redaction failure or unknown sensitive field is a fail-closed error at the safe boundary. The system MUST drop/block the diagnostic event rather than emit the value temporarily for debugging.
4. A Provider status token is not sufficient to decide retryability. The normalizer combines root cause, operation, phase, commit status, idempotency state, and the owning operation's retry contract.
5. An error from one layer MUST NOT be wrapped repeatedly into multiple client-visible codes for the same terminal outcome. Internal layers may retain cause chains in an authorized incident record, but the Public API sees one canonical semantic result.

---

## 4. Canonical error taxonomy

The tables below define stable semantic codes. `status_class` is an HTTP-oriented class or logical terminal class; #18/#20 choose the exact wire field names and any compatible status detail without changing the semantic distinction.

### 4.1 Authentication, ownership, authorization, and request validation

| Code | Stage / status class | Meaning | Base retryability | Remediation |
|---|---|---|---|---|
| `authentication_failed` | `authentication` / authentication status class | Missing, malformed, unknown, wrong-secret, rotated, or revoked Client API Key; all cases are intentionally indistinguishable. | `not_retryable` | `authenticate` |
| `resource_not_found` | `authorization` / not-found status class | Unknown or foreign Tenant-scoped Provider Account, Asset, Render Job, Capability Snapshot, Routing Policy, or idempotency resource. | `not_retryable` | `none` |
| `forbidden` | `authorization` / forbidden status class | Same-Tenant resource or action exists but scope, allowlist, risk acknowledgement, or policy denies it. | `not_retryable` | `request_permission` |
| `invalid_request` | `request_validation` / invalid-request status class | Malformed syntax, invalid parameter, invalid cross-field combination, or unsupported request shape not caused by raw size. | `not_retryable` | `fix_request` |
| `request_too_large` | `admission` / request-size status class | Raw request or upload exceeds the applicable per-request size limit. | `not_retryable` | `reduce_payload` |

Foreign and unknown identifiers MUST use exactly the same observable `resource_not_found` semantics. The error MUST omit `resource_reference`, foreign metadata, and any diagnostic that reveals whether the id exists elsewhere.

### 4.2 Admission, capability, account, and routing

| Code | Stage / status class | Meaning | Base retryability | Remediation |
|---|---|---|---|---|
| `rate_limit` | `admission` A3 / rate-limit status class | Tenant/key Public API RPM or burst limit exceeded before A6. | `retry_after` with `retry_after_class=admission_rate_window` | `wait_admission` |
| `concurrency_limit` | `admission` A4 / concurrency-limit status class | Tenant or originating Client API Key execution occupancy is full before A6. | `retry_after` with `retry_after_class=concurrency_release` | `wait_admission` |
| `quota_exhausted` | `admission` A5 / quota status class | Client API Key/Tenant anti-abuse daily quota is exhausted before A6. | `retry_after` with `retry_after_class=admission_quota_reset` | `wait_admission` |
| `account_not_usable` | `routing` / account-policy status class | A same-Tenant Provider Account fails #9 `I-USABLE-GATE`, including disabled, revoked, reauth-required, hard-block health, or unavailable current credential version. | `not_retryable` for the same account; fallback only under #11 and the owning operation's retry contract | `account_remediation` |
| `risk_ack_required` | `routing` / Auth-Mode-policy status class | A gated Auth Mode lacks the required current Tenant risk acknowledgement. | `operator_action_required` | `ack_risk` |
| `auth_mode_unavailable` | `routing` / Auth-Mode-policy status class | Auth Mode is prohibited, killed, flag-off, or non-lab experimental. | `operator_action_required` | `auth_mode_unavailable` |
| `capability_unsupported` | `capability` / capability status class | Requested operation is unsupported for the selected account/mode/model. Includes masked `inpaint` where the snapshot says unsupported. | `not_retryable` for the same capability facts | `capability_unsupported` |
| `capability_unverified` | `capability` / capability status class | Requested operation has not been verified for the account/model. | `retry_after` with `retry_after_class=capability_reprobe` | `capability_unverified` |
| `snapshot_stale` | `capability` / capability status class | Snapshot freshness is stale or invalid and cannot authorize the operation. | `retry_after` with `retry_after_class=capability_reprobe` | `snapshot_stale` |
| `model_unavailable` | `capability` / capability status class | Requested model was not observed or is not offerable for the requested operation. | `not_retryable` for that model; a new model request may be deliberate | `model_unavailable` |
| `routing_no_candidate` | `routing` / routing status class | No same-Tenant candidate remains after ownership, allowlist, usability, risk, capability, and health filters. | `not_retryable` | `routing_remediation` |
| `routing_fallback_not_allowed` | `routing` / routing status class | Primary became unavailable but no policy, explicit pin, cross-mode permission, capability match, or proof-of-non-commit permits a second account. | `not_retryable` for the current operation attempt | `routing_remediation` |

The safe `cause_class` for routing errors MAY be one of `explicit_pin`, `no_policy`, `cross_mode`, `prohibited_mode`, `capability`, `model`, `stale_snapshot`, `empty_candidate_set`, or `post_attempt_no_commit_proof`. It MUST NOT include candidate counts, account lists, foreign ids, or policy internals.

Admission `rate_limit` and `quota_exhausted` are never Provider runtime outcomes. A Provider response after A6 uses the distinct codes in §4.5, even when the human-readable guidance is also “wait”.

### 4.3 Asset validation, expiry, and capacity

| Code | Stage / status class | Meaning | Base retryability | Remediation |
|---|---|---|---|---|
| `unsupported_format` | `request_validation` / validation status class | Declared or actual media type is outside the supported image format set. | `not_retryable` | `fix_request` |
| `invalid_image` | `request_validation` / validation status class | Bytes cannot be decoded as an image, or declared media type does not match actual content. | `not_retryable` | `fix_request` |
| `invalid_dimensions` | `request_validation` / validation status class | Decoded pixel dimensions violate the canonical bounds. | `not_retryable` | `fix_request` |
| `invalid_mask` | `request_validation` / validation status class | Mask role, channel, or encoding is invalid. | `not_retryable` | `fix_request` |
| `mask_dimension_mismatch` | `request_validation` / validation status class | Mask dimensions do not match the referenced input image under #13. | `not_retryable` | `fix_request` |
| `asset_gone` | `output_delivery` or `asset` retrieval / gone status class for authorized own tombstone | Same-Tenant Asset was deleted or expired and is no longer retrievable. | `not_retryable` | `asset_lifecycle` |
| `storage_cap_exceeded` | `output_delivery` or `asset` write / capacity status class distinct from request-size and admission-quota classes | Atomic Tenant durable Asset bytes/count reservation would exceed the cap. | `retry_after` with `retry_after_class=asset_capacity_release` | `delete_assets_or_wait_expiry` |

`asset_gone` is allowed only after a same-Tenant lookup has already established that the caller may see the owning lifecycle state. A foreign or unknown `asset_id`, including a foreign tombstone, always maps to `resource_not_found`. `storage_cap_exceeded` never consumes another image-job quota unit and never triggers a new render.

### 4.4 Idempotency and operation identity

| Code | Stage / status class | Meaning | Base retryability | Remediation |
|---|---|---|---|---|
| `idempotency_conflict` | `authorization` / conflict status class | Same scoped idempotency key is bound to a different request fingerprint. | `not_retryable` for that key | `fix_request` |
| `idempotency_in_progress` | `execution` / conflict status class | A matching request is already claimed or is recovering; this request is not the executor. | `idempotent_replay` or `retry_after` with `retry_after_class=idempotency_recovery`; never a new upstream attempt | `retry_same_idempotency_key` |
| `idempotency_uncertain` | `recovery` / conflict status class | The owner or durable claim was lost while commit certainty is unavailable. | `operator_action_required` until the record resolves; no claim stealing | `contact_operator` |
| `execution_canceled` | `cancellation` / operation terminal class | Client/system cancellation won or the operation was stopped/discarded according to #12/#14. | `not_retryable` for the canceled attempt | `none`; a deliberate new request is a new operation |
| `execution_possibly_committed` | `upstream_execution` or `recovery` / uncertainty-class | Payload may have reached the Provider, but result or acknowledgement cannot establish non-commit. | `new_request_only`; no automatic retry or fallback | `submit_new_request` only as a deliberate client decision, with idempotency guidance |

A matching terminal idempotency replay normally returns the prior safe success or canonical failure instead of emitting `idempotency_in_progress`. It MUST not create another admission reservation, Provider attempt, Render Job, output Asset, or quota debit.

### 4.5 Provider and upstream runtime

| Code | Stage / status class | Meaning | Base retryability | Remediation |
|---|---|---|---|---|
| `provider_rate_limited` | `upstream_execution` / Provider-rate status class | Provider returned a rate-limit/cooldown signal after A6. It is distinct from Public API `rate_limit`. | `safe_internal_retry` before an allowed attempt; `retry_after` with `retry_after_class=provider_cooldown` when waiting is required; `new_request_only` after uncertain/committed execution | `wait_provider_cooldown` |
| `provider_quota_exhausted` | `upstream_execution` / Provider-quota status class | Provider entitlement/quota was exhausted after A6. It is distinct from Public API `quota_exhausted`. | `safe_internal_retry` before an allowed attempt; `retry_after` with `retry_after_class=provider_cooldown` when waiting is required; `new_request_only` after uncertain/committed execution | `wait_provider_cooldown` |
| `provider_auth_expired` | `credential` or `upstream_execution` / upstream-auth status class | Provider rejected the credential as expired, invalid, or no longer authorized after the request passed Public API authentication. | `operator_action_required` | `reauthenticate` |
| `provider_challenged` | `upstream_execution` / upstream-challenge status class | Provider returned a challenge/bot-interstitial class. No productized challenge solver is implied. | `operator_action_required` | `contact_operator` |
| `provider_banned` | `upstream_execution` or `recovery` / upstream-account-policy status class | Provider permanently banned or revoked the Provider Account. | `operator_action_required` | `contact_operator` |
| `provider_rejected` | `upstream_execution` / upstream-rejection status class | Provider refused the operation, request, plan, entitlement, or policy without a safe canonical success. | `new_request_only` | `provider_remediation` |
| `upstream_timeout` | `upstream_preflight` or `upstream_execution` / upstream-timeout status class | Gateway timeout class elapsed before a definitive Provider outcome. | `safe_internal_retry` before payload or with authoritative `not_committed`; otherwise `new_request_only` | `execution_recovery` |
| `upstream_unavailable` | `upstream_preflight` or `upstream_execution` / upstream-unavailable status class | Provider connection/dependency unavailable. | `safe_internal_retry` before payload or with authoritative `not_committed`; otherwise `new_request_only` | `execution_recovery` |
| `upstream_protocol_drift` | `upstream_execution` or `capability` / upstream-protocol status class | Provider response/schema/event/model surface no longer matches the Adapter contract. | `operator_action_required` | `contact_operator` |

The taxonomy's conditional rows are deterministic mappings, not simultaneous output values: the normalizer evaluates stage, commit status, idempotency state, and policy, then emits one final `retryability` and one final `remediation`. For example, `provider_rate_limited` with `commit_status=not_committed` emits `safe_internal_retry`; a pre-attempt cooldown path emits `retry_after` plus `retry_after_class=provider_cooldown`; an uncertain/committed attempt emits `new_request_only`.

### 4.6 Dependency, integrity, and internal failures

| Code | Stage / status class | Meaning | Base retryability | Remediation |
|---|---|---|---|---|
| `dependency_unavailable` | `dependency` / dependency status class | Required admission, routing, state, queue, or non-sensitive dependency is unavailable. | `retry_after` with `retry_after_class=dependency_recovery` for an idempotent dependency read; never a full accepted-operation replay by transport | `execution_recovery` |
| `sensitive_data_unavailable` | `credential` / protected-data status class | Vault, key service, audit-before-allow, revocation, retention, or binding state cannot authorize protected data. | `retry_after` with `retry_after_class=dependency_recovery` only for the owning lifecycle/execution path after dependency recovery; fail closed meanwhile | `contact_operator` |
| `integrity_failure` | `recovery` or `internal` / integrity status class | Binding, fencing, envelope, attempt ledger, result manifest, or idempotency invariant failed. | `operator_action_required` | `contact_operator` |
| `internal_error` | `internal` / internal status class | No safer specific canonical classification is available. | `operator_action_required` | `contact_operator` |

When a dependency failure occurs before A6, no execution side effect exists and the admission/dependency owner may retry its own idempotent dependency read within its bounded policy. When it occurs after A6, the owning operation must apply its commit boundary and accounting rules; an HTTP transport retry is never a substitute for operation recovery.

### 4.7 Root cause versus terminal classification

A canonical error has a root semantic code and may also have a terminal outcome. The root code MUST NOT be rewritten merely because the operation reaches a terminal state:

- `provider_rate_limited` with `commit_status=not_committed` may be internally retried by the chat or Render Job execution owner.
- `provider_rate_limited` with `commit_status=unknown` keeps the single root code `provider_rate_limited`, sets `retryability=new_request_only`, and carries `commit_status=unknown`; it MUST NOT emit `execution_possibly_committed` as a second code. `execution_possibly_committed` is reserved for operations whose canonical terminal contract selects that code instead of a more specific root code.
- `upstream_timeout` before payload may authorize a safe internal retry; the same timeout after payload without proof is not safe to retry.
- `execution_canceled` records the terminal cancellation decision even if the underlying root cause was a timeout or account revoke; accounting and residual state still follow #12/#14.
- `storage_cap_exceeded` applies to output placement without changing a completed Render Job into a failed render or reopening image quota.

---

## 5. Retryability, remediation, and commit certainty

### 5.1 Retryability classes

These are semantic classes, not numeric budgets:

| Class | Meaning | Client implication | Internal implication |
|---|---|---|---|
| `not_retryable` | Repeating the same input, identity, or operation cannot resolve the condition safely. | Do not retry the same request; fix input/scope or stop. | No operation retry. |
| `retry_after` | A bounded wait or dependency recovery may change the outcome without changing the operation identity. | Retry only after the named wait class; exact timing is #17/#18/#20. | Only the owning layer may consume its bounded retry chain. |
| `safe_internal_retry` | The operation has not committed or has authoritative non-commit proof. | Client should not replay the full non-idempotent request merely because internal retry is possible. | Sole retry owner may re-attempt within the operation's chain. |
| `idempotent_replay` | The same scoped operation may be queried or replayed without a new Provider side effect. | Retry with the same scoped idempotency identity; expect the existing result/state. | Claim ownership is not transferred into a second execution. |
| `new_request_only` | The previous operation may have committed or has an uncertain outcome. | A deliberate new request is allowed only as a new client decision; idempotency key use is recommended when the desired behavior is replay rather than a new generation. | No automatic retry, fallback, or claim stealing. |
| `operator_action_required` | A policy, security, integrity, account, or protocol condition requires remediation. | Do not hammer or blind-retry. | Lifecycle/operator recovery must resolve the condition before execution resumes. |

`retryability` is computed from the operation's commit/idempotency state. It is not determined by a Provider status string alone.

### 5.2 Remediation classes

The canonical remediation vocabulary includes the existing classes from #9/#10 plus cross-cutting classes required by #8 and this document:

| Remediation class | Meaning |
|---|---|
| `authenticate` | Present a valid Client API Key through the supported bearer path. |
| `replace_client_api_key` | Obtain or rotate a Client API Key; never attempt to recover historical plaintext. |
| `request_permission` | Use a same-Tenant key/scope/policy that authorizes the requested action. |
| `fix_request` | Correct request syntax, parameters, content, media, mask, model, or idempotency fingerprint. |
| `reduce_payload` | Reduce body/upload size below the applicable limit. |
| `wait_admission` | Wait for Public API RPM, concurrency, or anti-abuse quota availability. |
| `submit_credential` | Supply valid Provider Credential material for a new connection. |
| `complete_oauth` | Complete the authorized OAuth browser/device flow. |
| `reauthenticate` | Replace or refresh an expired/invalid Provider Credential and satisfy #9 validation/probe. |
| `ack_risk` | Complete the required Auth Mode risk acknowledgement. |
| `enable_account` | Enable a disabled same-Tenant Provider Account and satisfy any required probe. |
| `wait_provider_cooldown` | Wait for Provider rate/quota/cooldown recovery; exact timing is #17. |
| `account_remediation` | Follow the account's bounded safe cause: reauthenticate, enable, wait for Provider cooldown, or contact the operator. The emitted remediation remains this single class; the detailed cause is bounded `cause_class` metadata. |
| `provider_remediation` | Follow the bounded Provider-safe cause such as a request correction or operator review; raw Provider instructions never cross the normalization boundary. |
| `routing_remediation` | Resolve the Tenant's routing policy or the operation's safe fallback condition; the emitted remediation remains this single class and never reveals candidate internals. |
| `capability_unsupported` | Choose an operation supported by the account/mode. |
| `capability_unverified` | Wait for or trigger the authorized capability probe. |
| `snapshot_stale` | Re-probe before using stale/invalid capability facts. |
| `model_unavailable` | Choose an observed offerable model for the requested operation. |
| `asset_lifecycle` | The owning Asset was expired or deleted; the code may expose the bounded lifecycle reason only on an authorized same-Tenant path. |
| `delete_assets_or_wait_expiry` | Free Tenant Asset storage or wait for lifecycle expiry. |
| `execution_recovery` | Allow the owning execution/recovery path to resolve a transient dependency or uncertain upstream state; client action follows the emitted retryability and commit status. |
| `retry_same_idempotency_key` | Query/replay the existing operation without creating a new side effect. |
| `submit_new_request` | Make a deliberate new client request after understanding that the prior operation may have committed. |
| `contact_operator` | Require safe operator/provider/protocol/security remediation; no challenge solver is implied. |
| `auth_mode_unavailable` | Select a different product-enabled Auth Mode or await an explicit operator gate. |
| `delete_and_recreate` | Delete an irrecoverable account/mode binding and create a new account explicitly. |
| `none` | No client remediation is required or permitted for this terminal outcome. |

A code has one fixed remediation class. Cause-specific guidance belongs in bounded `cause_class` metadata or in a distinct stable code; it MUST NOT change the remediation token attached to the same code.

### 5.3 Commit certainty

For any non-idempotent Provider operation, the Gateway MUST record and honor one of these states:

| Commit status | Meaning | Automatic new upstream attempt? | Client retryability |
|---|---|---|---|
| `not_started` | No Provider payload transmission began. | MAY start the same attempt after a fenced recovery or safe preflight retry. | Usually `safe_internal_retry` or `retry_after`. |
| `not_committed` | Authoritative proof shows that the Provider did not accept/create the operation. | MAY re-attempt or fallback if the owning operation, Tenant policy, and retry chain permit it. | Usually `safe_internal_retry`; client need not duplicate the request. |
| `committed` | Provider accepted the operation or returned a result tied to the attempt. | MUST NOT start replacement generation/edit/inpaint. Recover, capture, drain, or deliver the same result. | `new_request_only` for a new operation, or `idempotent_replay` for the same identity. |
| `unknown` | Payload may have been accepted but acknowledgement/result is unavailable or inconsistent. | MUST NOT re-render or fallback. Same-attempt status lookup/drain is allowed only when Provider support and lifecycle policy permit it. | `new_request_only` with explicit uncertainty; no blind retry. |

The following are **not** proof of `not_committed` after payload transmission:

- a Provider or transport status token by itself;
- timeout, connection reset, DNS/TCP/TLS failure after payload transmission began;
- missing response body, missing stream delta, absent client progress, or a worker/process crash;
- an Adapter exception that does not carry an authoritative Provider non-acceptance guarantee;
- a routing or health token such as `rate_limited`, `quota_exhausted`, `degraded`, or `protocol_drift` by itself.

A status code can be part of authoritative proof only when the Adapter's documented contract establishes that the Provider could not have accepted the operation. The status string alone is never sufficient.

### 5.4 Retry and fallback share one chain

1. Routing fallback is a re-attempt for the same logical operation. It consumes the same bounded retry chain as a Gateway execution retry.
2. A fallback target MUST satisfy #11 same-Tenant, risk, capability, model, usability, and policy gates.
3. After an upstream attempt, fallback additionally requires the owning operation's authoritative `not_committed` proof. Chat uses #12's proof contract; Render Jobs use #14's attempt ledger.
4. A retry owner MUST record each attempt/reason/proof and walk an ordered bounded chain once. No layer may reset the chain merely by changing accounts, transports, queues, or workers.
5. If the chain is exhausted, the Gateway emits one canonical terminal failure and stops automatic retries. It does not recursively retry the canonical error.
6. A client may submit a deliberate new request after a terminal `new_request_only` outcome, but that request is outside the prior retry chain and may consume new admission/quota according to #8.

---

## 6. Retry ownership and layer boundaries

### 6.1 Ownership matrix

| Operation / action | Sole retry owner | Permitted retry/recovery | Prohibited independent retry |
|---|---|---|---|
| Public API authentication and admission dependency reads | Admission/control layer | Retry idempotent state reads within its dependency policy; fail closed when state is unavailable | Re-running an accepted operation, Adapter retry, queue redelivery as execution permission |
| Chat generation (`chat`, `chat_streaming`) | Gateway chat execution layer (#12 X4) | Retry only `not_started` or authoritatively `not_committed` attempts; consume routing fallback in the same chain; preserve idempotency claim | Client automatic POST replay, HTTP transport middleware, Adapter full-generation retry, routing-layer retry, queue/worker duplicate execution |
| Chat cancellation/disconnect/timeout reconciliation | Chat execution/accounting lifecycle | Abort, residual tracking, drain, and conservative accounting for the same attempt | Reopening a new generation because the client terminal was canceled or timed out |
| Render generation/edit/inpaint | Gateway Render Job execution layer (#14) | Retry only `not_started` or authoritatively `not_committed` attempt under the durable attempt ledger and one bounded chain | HTTP transport, queue redelivery, stale worker, Provider Account lease, Adapter, output-delivery worker, or client replay as an automatic render |
| Render recovery after `committed`/`unknown` | Render Job recovery owner | Inspect/drain/status lookup/capture the same `attempt_id` where supported; finalize state conservatively | Replacement generation/edit/inpaint or silent account hop |
| Result retrieval and staging | Output-delivery worker (#14) | Fetch the existing result handle/manifest, persist bytes by stable result identity, retry same-attempt retrieval | New Render Job, new generation, new image quota reservation |
| Output Asset reservation and placement | Output-delivery worker with #13 storage protocol | Retry reservation/placement by `(tenant_id, job_id, output_entry_id)` placement key; settle reservation idempotently | New generation, duplicate Asset, or quota reopening |
| Provider Credential refresh/probe | Account lifecycle worker or explicitly authorized lifecycle operation (#9) | Same-account, same-Tenant, singleflight refresh/probe with versioned lifecycle transitions | Chat/render Adapter silently refreshing through a separate retry loop, cross-mode substitution, cross-Tenant credential use |
| Account revoke/delete/disable/expiry | Owning lifecycle state machine | Idempotent state transition, cancellation attempt, purge/reconciliation replay | Transport retry that reverses state, worker retry that re-enables use, execution retry on a revoked account |
| Idempotency claim/replay | Operation owner plus scoped idempotency record | Wait, return existing terminal state, or recover the same claim under fencing | Claim stealing into a second uncertain Provider execution |
| Routing selection/fallback | Owning operation execution layer; routing is a selector, not a retry owner | Select a permitted next candidate only when the operation's retry contract authorizes it | Routing/health/lease code independently retrying a non-idempotent operation |
| Queue delivery / worker lease recovery | Durable operation owner | Reclaim/fence the same job or attempt reference; recover bookkeeping | Treating at-least-once reference delivery as at-least-once Provider permission |
| Provider health/cooldown/kill controls | Health/operator control plane | Mark health, cooldown, or Auth Mode availability and notify the owning execution/lifecycle owner | Sending another operation, bypassing the retry chain, or selecting a foreign/shared account |

### 6.2 Layer rules

1. **Client layer:** A client MAY retry a safe read or use the same scoped idempotency identity for replay when the canonical error says `idempotent_replay`. A client MUST NOT automatically replay a non-idempotent `POST` after `new_request_only` merely because the network response was absent.
2. **HTTP/Public API transport:** Transport may retry connection housekeeping or safe reads according to its implementation, but it MUST NOT retry an accepted chat/render operation whose payload may have reached a Provider. Transport retry MUST NOT create a second admission, job, attempt, or quota reservation.
3. **Admission layer:** Admission may retry idempotent counter/state reads under its own bounded dependency policy. Once A6 succeeds, admission does not own the Provider operation and MUST NOT re-run it.
4. **Gateway execution layer:** The operation-specific execution layer is the only owner of a full chat or render re-attempt. It combines root error, commit state, idempotency state, routing policy, and operation retry budget.
5. **Adapter layer:** An Adapter normalizes Provider outcomes and records commit evidence. It MUST NOT independently re-run a full non-idempotent generation/edit/inpaint/chat operation. Same-attempt status lookup or transport continuation is permitted only when the Provider contract guarantees it does not create a second side effect.
6. **Queue/worker layer:** Queue redelivery and worker lease recovery deliver or reclaim references. They do not grant permission for a new Provider operation. Fencing rejects stale mutations and prevents concurrent attempts.
7. **Routing/lease/health layer:** Routing selects candidates and health marks availability. These layers do not own an independent retry budget and cannot bypass operation-specific proof-of-non-commit.
8. **Output-delivery worker:** Output delivery can retry retrieval, staging, storage reservation, and placement from the immutable result manifest. It MUST never call image generation/edit/inpaint as a delivery fallback.
9. **Lifecycle worker:** Credential refresh/probe and revoke/delete/expiry reconciliation own only their lifecycle operation. They cannot silently become an inference retry path.
10. **Operator control plane:** Kill switches and cooldowns block or defer future work. They do not authorize a retry, reset a commit ledger, or treat a banned/challenged account as usable.

### 6.3 Non-idempotent operation invariant

For one accepted/idempotently identified chat request or Render Job:

- at most one layer may initiate a new upstream operation;
- the operation owner may initiate at most the bounded attempts authorized by `not_started`/`not_committed` proof;
- `committed` and `unknown` stop automatic replacement execution;
- a change of account, Auth Mode, queue delivery, worker, or transport does not create a new retry budget;
- output delivery never reopens the render operation;
- a deliberate client request without the prior idempotency identity is a new operation, not an internal retry.

This is the cross-operation form of #12 `I-CHAT-RETRY-BOUNDARY` and #14 `I-RENDER-RETRY-BOUNDARY`.

---

## 7. Idempotency and replay semantics

### 7.1 Scoped identity

Idempotency records remain scoped by #6 to `(tenant_id, client_api_key_id-or-scope, idempotency_key)` and a canonical request fingerprint. #16 owns the outcome codes; #18/#20 own header/path/schema encoding and #17/#20 own numeric retention/compatibility details as applicable.

The fingerprint MUST cover the operation identity and all inputs that could change the Provider side effect, including model, prompt/request content or its authorized digest, Asset references, mask/reference options, and relevant policy inputs. A fingerprint is a comparison guard, not permission to recover the original prompt or Asset bytes.

### 7.2 Matching key and fingerprint

1. The first accepted request atomically claims the idempotency identity before a non-idempotent Adapter call.
2. A concurrent matching request MUST NOT call the Adapter. It may wait within a bounded `idempotency_recovery` class or receive `idempotency_in_progress`.
3. A later matching request after a terminal result receives the prior safe response/status/job/manifest references. It MUST NOT create a new admission reservation, Render Job, Provider attempt, output Asset, or quota debit.
4. A matching replay after `committed` or `unknown` may retrieve/reconcile the existing operation, but it MUST NOT turn uncertainty into permission for a second execution.
5. Cross-Tenant or cross-key replay cannot read another Tenant's record or result; it resolves in that principal's own scoped key space and obeys #6 non-enumeration.

### 7.3 Fingerprint conflict

A request using the same scope and idempotency key with a different fingerprint returns `idempotency_conflict`:

- the original record and operation remain unchanged;
- no new Provider call, queue enqueue, job, asset, reservation, or retry chain is created;
- the new request cannot overwrite, merge with, or steal the original record;
- safe context may include the request id and conflict state, but never either raw payload or fingerprint value.

### 7.4 Uncertain owner and no-steal rule

If the process/worker owning the idempotency claim disappears while commit certainty is unavailable:

1. The record becomes `uncertain` or remains an explicitly recoverable `in_progress` state according to the owning operation.
2. A new worker may recover the same claim only with durable fencing and the operation's attempt ledger.
3. Recovery may inspect, drain, capture, or settle the same attempt. It MUST NOT steal the record into a new generation merely because the old worker is gone.
4. Public status uses `idempotency_uncertain` or `execution_possibly_committed` with `retryability=operator_action_required` or `new_request_only` as appropriate.
5. The idempotency record, request/correlation identifiers, and commit state remain correlated until terminal reconciliation. Cleanup cannot erase the evidence needed to prevent a duplicate side effect.

### 7.5 Output replay

A terminal Render Job replay returns the stable job/result-manifest/output-entry state. An output-delivery retry is keyed by the existing manifest and placement identity. It never creates a new image-generation attempt, image quota unit, or output Asset identity.

---

## 8. Failure normalization, redaction, and non-enumeration

### 8.1 Admission versus execution/runtime

The canonical phase boundary is:

```text
if the failure occurs before A6 accept:
    category = admission / authorization / validation / capability
    no Provider Credential decrypt, Adapter call, or accepted job side effect
else:
    category = execution / delivery / recovery / accounting
    apply the owning operation's commit, cancellation, quota, and retry contract
```

| Condition | Canonical code | Phase | Adapter/vault side effect |
|---|---|---|---|
| Missing/invalid/revoked Client API Key | `authentication_failed` | A0 | none |
| Same-Tenant scope/policy denial | `forbidden` | A1 | none |
| Foreign/unknown Tenant-scoped id | `resource_not_found` | A1 or resource lookup | no foreign read/decrypt |
| Per-request size exceeded | `request_too_large` | A2 | none |
| Public API RPM exceeded | `rate_limit` | A3 | none |
| Public API concurrency exceeded | `concurrency_limit` | A4 | none |
| Public API anti-abuse quota exhausted | `quota_exhausted` | A5 | none |
| Unsupported/stale capability | `capability_unsupported` / `capability_unverified` / `snapshot_stale` | post-A5, pre-upstream | none |
| Provider rate/quota after A6 | `provider_rate_limited` / `provider_quota_exhausted` | runtime | credential/Adapter path may already have run |
| Provider auth/challenge/rejection/drift | Provider runtime codes in §4.5 | runtime | credential/Adapter path may already have run |
| Timeout/reset/missing response after payload | `upstream_timeout` or `upstream_unavailable` plus `commit_status=unknown` | runtime/recovery | no automatic replacement execution |
| Output storage cap after render | `storage_cap_exceeded` | output delivery | no new render; existing job remains governed by #14 |

A capability or routing rejection that is known before A6 MUST NOT be mislabeled as a Provider runtime failure, even if a Provider account would have been selected later.

### 8.2 Provider health mapping

Provider Account health remains the #9 vocabulary. Canonical errors map it without changing ownership:

| #9 health | Canonical runtime/preflight mapping | Default account effect |
|---|---|---|
| `healthy` | no error | remains routable subject to other gates |
| `degraded` | `upstream_unavailable`, `provider_rejected`, or another operation-specific code | may remain routable under #11/#17 policy |
| `auth_expired` | pre-upstream `account_not_usable`; post-A6 `provider_auth_expired` | reauth path; non-routable when #9 gate fails |
| `challenged` | `provider_challenged` | non-routable; no challenge solver |
| `quota_exhausted` | pre-A6 account gate or post-A6 `provider_quota_exhausted` | cooldown/entitlement handling; never Client API Key quota |
| `rate_limited` | pre-A6 routing/cooldown or post-A6 `provider_rate_limited` | bounded cooldown; no reauth by default |
| `protocol_drift` | `upstream_protocol_drift` | capability invalidation/operator path |
| `provider_banned` | `provider_banned` | non-routable; operator/reauth policy |
| `unknown` | `account_not_usable`, `snapshot_stale`, or dependency/integrity code according to stage | fail closed where the owning gate requires certainty |

### 8.3 Redaction order

1. Capture the raw internal result only inside the authorized boundary that needs to classify it. Raw Provider bodies and secret-bearing headers MAY be retained only in a separately authorized encrypted incident record under #15, never in ordinary error data.
2. Determine the semantic code, stage, operation, commit/idempotency state, and remediation from bounded facts.
3. Redact before structured logging, tracing, metrics extraction, audit serialization, queue publication, error wrapping, or Public API serialization.
4. Emit the canonical safe envelope and safe audit/telemetry projection.
5. If the redaction decision is unavailable or ambiguous, fail closed at the safe boundary and do not emit the raw value.

### 8.4 Non-enumeration

- Foreign and unknown Provider Account, Asset, Render Job, Capability Snapshot, Routing Policy, and idempotency identifiers use `resource_not_found` with the same status/body shape and no `resource_reference`.
- Same-Tenant insufficient scope or policy uses `forbidden`; it must not be used to confirm a foreign resource.
- Same-Tenant expired/deleted Asset may use `asset_gone` only when the lookup already established ownership and the tombstone policy permits the refinement.
- Canonical errors MUST NOT contain foreign account candidate lists, policy chains, capability snapshots, dimensions, tombstone state, or cross-Tenant correlation identifiers.
- Error timing, retry-after hints, and safe context MUST NOT become a foreign-resource existence oracle.

---

## 9. Security and operational impact

| Defect | Impact |
|---|---|
| Admission `rate_limit` conflated with Provider `provider_rate_limited` | Clients retry the wrong boundary; Provider health and Client API Key abuse controls become coupled incorrectly |
| Admission `quota_exhausted` conflated with Provider quota | Tenant may rotate the wrong credential or believe another account can bypass Public API anti-abuse controls |
| Timeout/status treated as non-commit proof | Duplicate chat/render generation, duplicate Provider quota, and account-ban/reputation damage |
| HTTP, Adapter, queue, and worker each retry independently | Retry storm, at-least-once non-idempotent execution, multiplied quota and side effects |
| Output delivery retry re-renders | Duplicate image generation after a successful render and unexpected quota/cost |
| Idempotency claim stolen after worker loss | Concurrent duplicate generation or replay of an uncertain operation |
| Raw Provider error/body leaked | Credential, prompt, temporary bearer URL, or sensitive account metadata exfiltration |
| Foreign id produces distinct error | Tenant/resource existence oracle and graph mapping |
| Retry owner exposed as Adapter detail | Leaks implementation topology and encourages clients to couple to internals |
| Error omits request/correlation identity | Incidents cannot correlate client response, audit, worker, and Provider health records safely |
| Provider protocol drift retried blindly | Repeated malformed calls, capability lies, and rapid Auth Mode kill signals |
| Output storage cap treated as render failure | Incorrect job terminal state, duplicate image quota, and broken delivery recovery |
| Canceled residual treated as stopped | Premature occupancy/quota release and replacement capacity amplification |

---

## 10. Test obligations

Exact HTTP/OpenAPI/SSE harness arrives with #18–#20. These are required observable conformance cases for #16; tests MUST assert public behavior and side-effect counters rather than private package layout.

### 10.1 Taxonomy and envelope

1. Every stable code has exactly one semantic meaning, category, status class, default retryability, remediation class, and safe-context rule.
2. Every emitted Public API error has a server-owned `request_id` when transport permits it; long-running workflows preserve a safe `correlation_id`.
3. Canonical errors contain no raw Provider body/header, secret, credential, prompt, Asset bytes, ciphertext, bearer URL, stack trace, queue message, or foreign-resource detail.
4. An unknown internal exception normalizes to `internal_error` or `integrity_failure`, never to a raw exception string.
5. `finish_class` and terminal outcome remain Provider-independent; Provider-specific stream markers do not become error codes.

### 10.2 Admission, ownership, and capability

6. Missing, malformed, unknown, wrong-secret, and revoked Client API Key material all produce indistinguishable `authentication_failed` behavior in the unauthorized status class.
7. Foreign and unknown Tenant-scoped identifiers produce identical `resource_not_found` behavior in the not-found status class, zero foreign read/decrypt, and no foreign metadata.
8. Same-Tenant scope/policy denial produces `forbidden` behavior in the forbidden status class and never an existence-confirming foreign response.
9. Public API RPM, concurrency, and anti-abuse quota produce `rate_limit`, `concurrency_limit`, and `quota_exhausted` respectively, with zero Adapter calls and no accepted job side effect.
10. Unsupported/unverified/stale capability produces the corresponding capability code before upstream; masked `inpaint` is never silently downgraded to `image_edit`.
11. Auth Mode kill/prohibited/gated-without-ack and non-usable account states fail closed with account/risk codes and safe remediation, without vault decrypt or Adapter execution.
12. Same-Tenant expired/deleted Asset may produce `asset_gone`; foreign/unknown Asset remains `resource_not_found`.
13. Storage-cap exhaustion produces `storage_cap_exceeded`, distinct from `request_too_large` and admission `quota_exhausted`, without reopening image quota.

### 10.3 Provider/runtime distinction

14. A Provider rate limit after A6 produces `provider_rate_limited`, not `rate_limit`; a Provider quota signal produces `provider_quota_exhausted`, not admission `quota_exhausted`.
15. Provider auth expiry, challenge, ban, rejection, timeout, unavailability, and protocol drift map to distinct canonical codes and safe remediation classes.
16. A Provider `rate_limited` or `quota_exhausted` token permits fallback only when the owning operation proves `not_committed` and #11 policy allows it.
17. A timeout, reset, missing response, missing delta, or HTTP status after payload transmission produces `commit_status=unknown` unless the Adapter has authoritative non-commit proof.
18. Auth expiry/challenge/protocol drift errors never include raw Provider HTML, challenge content, headers, URL query parameters, or secret material.

### 10.4 Retry ownership and duplicate prevention

19. Chat has exactly one full-operation retry owner: the Gateway chat execution layer. Adapter, HTTP transport, queue, routing, and client automatic replay cannot create a second generation.
20. Render generation/edit/inpaint has exactly one retry owner: the Gateway Render Job execution layer, with one durable attempt ledger and bounded chain.
21. `committed` or `unknown` Render attempts never start a replacement generation; recovery may only inspect/drain/capture the same attempt.
22. Output retrieval/staging/reservation/placement retries use the existing result manifest and placement key, invoke zero generation/edit/inpaint calls, and create at most one output Asset.
23. Queue redelivery and worker fencing recover a job/attempt reference but never grant a second Provider execution.
24. Credential refresh/probe retry is single-account and lifecycle-owned; it cannot silently become an inference retry or cross-Auth-Mode fallback.
25. Retry chain state is preserved across account selection, queue redelivery, worker recovery, and transport interruption; no layer resets or multiplies the budget.
26. A canonical terminal error is emitted once for a client operation; accounting/delivery reconciliation does not emit a second client terminal or restart execution.

### 10.5 Idempotency and diagnostics

27. Matching scoped idempotency key/fingerprint has one executor; concurrent duplicates receive `idempotency_in_progress` or existing terminal replay and make zero new Provider calls.
28. Same scoped key with a different fingerprint produces `idempotency_conflict` and leaves the original operation unchanged.
29. An uncertain claim produces `idempotency_uncertain` or `execution_possibly_committed`; no worker steals it into a second automatic execution.
30. Terminal replay returns stable job/manifest/output references without new admission, generation, placement, or quota side effects.
31. `request_id` and `correlation_id` correlate safe Public API, audit, worker, and health records, but neither id authorizes resource access or content recovery.
32. Diagnostic injection of a token, cookie, prompt, image, temporary Provider URL, ciphertext, or foreign id is removed or blocks the diagnostic event before logging/serialization.

---

## 11. Core invariants

1. **I-ERROR-CANONICAL** — Every emitted failure or terminal error has one stable semantic code, category, status class, retryability class, remediation class, and safe diagnostic context; code meanings are never silently reused.
2. **I-ERROR-PROVIDER-INDEPENDENT** — Public errors and terminal events do not expose Provider-specific framing, raw bodies, Adapter exception shapes, or internal topology.
3. **I-ERROR-ADMISSION-RUNTIME-DISTINCT** — Public API admission `rate_limit`, `concurrency_limit`, and `quota_exhausted` remain distinct from Provider runtime `provider_rate_limited` and `provider_quota_exhausted`; pre-A6 failures never masquerade as execution failures.
4. **I-ERROR-NON-ENUM** — Foreign and unknown Tenant-scoped identifiers have identical `resource_not_found` behavior with no foreign metadata, while authorized same-Tenant Asset tombstone refinement is bounded by #13.
5. **I-ERROR-REDACT** — Secrets, prompt/image content, raw Provider payloads, bearer handles, envelope blobs, stack traces, and foreign existence never appear in Public API errors, logs, metrics, traces, audit, support, queues, or fixtures.
6. **I-DIAGNOSTIC-CORRELATABLE-SAFE** — Server-owned request/correlation identifiers and bounded local resource ids correlate incidents without becoming authorization, content-recovery, or foreign-existence capabilities.
7. **I-RETRY-SINGLE-OWNER** — Each non-idempotent operation class has exactly one full-operation retry owner; HTTP, execution, Adapter, queue, routing, and worker layers cannot independently retry it.
8. **I-RETRY-PROOF-OF-NON-COMMIT** — Automatic re-attempt/fallback requires `not_started` or authoritative `not_committed` proof; timeout, reset, missing response/delta, or status token alone is insufficient after payload transmission.
9. **I-RETRY-FALLBACK-SHARED-CHAIN** — Routing fallback is a re-attempt inside the owning operation's one bounded retry chain; account changes never create a second retry budget.
10. **I-RETRY-NO-POST-COMMIT-REEXEC** — `committed` and `unknown` Provider attempts never launch replacement chat or image generation/edit/inpaint; recovery is same-attempt only.
11. **I-OUTPUT-RETRY-NO-RERENDER** — Retrieval, staging, storage reservation, and output placement retry from the immutable result manifest/placement key and never reopen image generation or image-job quota.
12. **I-RENDER-DELIVERY-DISTINCT** — Render completion and output delivery failure/capacity are separate outcomes; `storage_cap_exceeded` or pending placement does not make a completed render non-existent or trigger a new render.
13. **I-IDEMPOTENCY-NO-STEAL** — Matching scoped duplicates replay or wait; fingerprint conflicts do not mutate the first operation; uncertain claims are not stolen into a second automatic execution.
14. **I-IDEMPOTENCY-NO-DUPLICATE** — One accepted/idempotently identified chat request or Render Job produces at most one committed upstream side effect, subject to the owning operation's proof and bounded chain.
15. **I-RETRYABILITY-NOT-AUTHORITY** — A client-visible `retry_after`, `safe_internal_retry`, or `new_request_only` signal never authorizes a layer to bypass ownership, lifecycle, capability, vault, routing, commit, or accounting gates.
16. **I-RETRY-ACCOUNTING-BOUNDED** — Cancellation, timeout, disconnect, and recovery preserve #8/#12/#14 Tenant/key occupancy and conservative accounting until the owning accounting terminal; client terminal does not mint replacement capacity.
17. **I-CREDENTIAL-ERROR-FAIL-CLOSED** — Vault, audit, revocation, retention, binding, or protected-data dependency uncertainty produces a safe dependency/integrity code and never releases plaintext or starts Provider execution.
18. **I-ERROR-HEALTH-ORTHOGONAL** — Provider health/cooldown/kill controls classify and gate future work but never independently retry an operation or override Auth Mode/routing/capability policy.

---

## 12. Open follow-ups (explicitly deferred)

| Topic | Issue / owner | Constraint retained here |
|---|---|---|
| Numeric retry budgets, cooldowns, backoff, timeout, drain, residual, retry-after, and recovery windows | #17 | Stable retryability and named `retry_after_class` are fixed; values remain tunable without weakening commit or ownership rules |
| Provider Account health, cooldown, and operator control surfaces | #17 | Health-to-code mapping and no-independent-retry rule are fixed; thresholds and operator UI remain #17 |
| JSON/problem+json field names, HTTP paths, status details, headers, SSE terminal encoding | #18/#20 | Semantic codes, classes, redaction, and terminal meaning are fixed; wire shape remains downstream work |
| API versioning, compatibility aliases, idempotency header, record TTL, and contract fixtures | #20 | Stable code meanings and no-steal behavior are fixed; transport/version policy remains #20 |
| Chat stream event schema and `finish_class` wire representation | #18/#20 | Logical terminal and finish classes are consumed from #12 and canonically named here; encoding remains downstream |
| Render Job status/error/output-entry schema | #18/#20 | Failure stage, commit certainty, delivery separation, and output-only retry are fixed; schema remains #14/#18/#20 |
| Adapter-specific status lookup and non-commit proof | Provider Adapter implementation issues | Adapter may strengthen proof but cannot weaken the shared boundary or retry owner |
| Incident-record storage for authorized raw upstream evidence | #15 / security architecture | Ordinary errors remain redacted; any incident evidence is separately authorized, encrypted, retained, and never public |
| Multi-operation batch retry semantics | Future contract decision | Each contained operation must retain its own retry owner and commit boundary; no batch wrapper may multiply retries |
| Client-requested resumable chat/render operations | Reopen `D-CHAT-RESUME` / `D-RENDER-RESUME` | Current default is no implicit resume; same-operation recovery remains server-owned and fail-closed |

---

## 13. ADR decision

No new ADR is filed for the MVP. This document consolidates and names already accepted ownership, lifecycle, redaction, and commit-boundary decisions from #6–#15; it does not change the product topology or introduce a new persistence/runtime architecture.

An ADR would be warranted if the product later introduced:

- default at-least-once retry of a possibly committed non-idempotent Provider operation;
- multiple independent retry owners for one operation;
- cross-Tenant/shared-pool fallback or error diagnostics that reveal foreign resources;
- public raw Provider/Adapter error passthrough;
- output delivery that recreates a Render Job implicitly;
- or legal/compliance requirements that require retaining decryptable Provider Credential material in ordinary error/incident paths.

---

## 14. Constants and reopen ids

| Id | Meaning |
|---|---|
| `authentication_failed` / `resource_not_found` / `forbidden` | Stable authentication, non-enumerating ownership, and same-Tenant authorization codes |
| `rate_limit` / `concurrency_limit` / `quota_exhausted` | Public API admission codes owned by #8 and distinguished from Provider runtime codes |
| `provider_rate_limited` / `provider_quota_exhausted` | Provider runtime rate/quota codes after A6 |
| `provider_auth_expired` / `provider_challenged` / `provider_banned` / `provider_rejected` | Provider account/runtime failure codes |
| `upstream_timeout` / `upstream_unavailable` / `upstream_protocol_drift` | Upstream runtime and protocol codes |
| `capability_unsupported` / `capability_unverified` / `snapshot_stale` / `model_unavailable` | Capability and model availability codes owned by #10 and named canonically here |
| `unsupported_format` / `invalid_image` / `invalid_dimensions` / `invalid_mask` / `mask_dimension_mismatch` | Asset validation codes owned by #13 and named canonically here |
| `asset_gone` / `storage_cap_exceeded` | Asset lifecycle and durable storage capacity codes |
| `idempotency_conflict` / `idempotency_in_progress` / `idempotency_uncertain` | Scoped idempotency outcome codes |
| `execution_canceled` / `execution_possibly_committed` | Operation terminal/certainty codes |
| `dependency_unavailable` / `sensitive_data_unavailable` / `integrity_failure` / `internal_error` | Dependency, protected-data, integrity, and fallback internal codes |
| `not_retryable` / `retry_after` / `safe_internal_retry` / `idempotent_replay` / `new_request_only` / `operator_action_required` | Stable retryability classes |
| `admission_rate_window` / `concurrency_release` / `admission_quota_reset` / `provider_cooldown` / `capability_reprobe` / `asset_capacity_release` / `idempotency_recovery` / `dependency_recovery` | Named wait classes; numeric values belong to #17 |
| `request_id` / `correlation_id` | Server-owned safe diagnostic identifiers |
| `not_started` / `not_committed` / `committed` / `unknown` | Shared commit certainty states consumed from #12/#14 |
| `I-ERROR-*` / `I-RETRY-*` / `I-IDEMPOTENCY-*` / `I-OUTPUT-*` | Invariants in §11 |
| `D-ERROR-WIRE` | Reopen if wire contract needs semantic changes rather than encoding changes |
| `D-RETRY-AT-LEAST-ONCE` | Reopen only for an explicit product decision to permit uncertain/committed automatic re-execution; default remains forbidden |

---

## 15. Acceptance criteria traceability

| AC (issue #16) | Where satisfied |
|---|---|
| Each error class has a stable code, safe context, retryability, and remediation class | §2.1, §3, §4, §5.1–§5.2, §10.1, `I-ERROR-CANONICAL`, `I-DIAGNOSTIC-CORRELATABLE-SAFE` |
| HTTP, execution, Adapter, and worker layers cannot retry the same non-idempotent operation concurrently | §5.4, §6, §6.2–§6.3, §10.4, `I-RETRY-SINGLE-OWNER`, `I-RETRY-PROOF-OF-NON-COMMIT`, `I-IDEMPOTENCY-NO-DUPLICATE` |
| Image render retry and output-delivery retry are distinct and cannot multiply quota | §4.3, §4.7, §6.1, §7.5, §10.4, `I-OUTPUT-RETRY-NO-RERENDER`, `I-RENDER-DELIVERY-DISTINCT` |
| Request identifiers and diagnostics correlate incidents without exposing sensitive data | §3.1–§3.4, §8.3–§8.4, §10.1/§10.5, `I-ERROR-REDACT`, `I-DIAGNOSTIC-CORRELATABLE-SAFE` |

---

## 16. Document control

| Field | Value |
|---|---|
| Status | Accepted for specification (issue #16) |
| Check date of evidence inputs | 2026-07-15 |
| Supersedes | n/a (initial canonical error and retry-ownership lock) |
| Next review | On #8 admission changes, #9 health/remediation changes, #10 capability changes, #11 fallback changes, #12 chat retry/idempotency changes, #13 Asset delivery changes, #14 Render/output retry changes, #15 redaction/vault changes, #17 numeric tuning, or #18/#20 contract versioning |
| Authors | Spec decision agent for issue #16 |
