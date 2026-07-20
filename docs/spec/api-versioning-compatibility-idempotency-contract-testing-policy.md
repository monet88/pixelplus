# API Versioning, Compatibility, Idempotency, and Contract Testing Policy

- Status: Accepted stable Public API policy (issue #20)
- Date: 2026-07-19
- Parent: [#1](https://github.com/monet88/pixelplus/issues/1)
- Issue: [#20](https://github.com/monet88/pixelplus/issues/20)
- Base commit: `f8213c45680d12ab38454a15bfc8475c9a2ef9d9`
- Vocabulary source: `CONTEXT.md`
- Inference prototype evidence: `docs/spec/openai-compatible-inference-contract.md` (#18)
- Management prototype evidence: `docs/spec/provider-account-and-capability-management-contract.md` (#19)
- Canonical errors and retry ownership: `docs/spec/canonical-errors-and-retry-ownership.md` (#16)
- Chat execution lifecycle: `docs/spec/chat-execution-and-streaming-lifecycle.md` (#12)
- Durable Render Job lifecycle: `docs/spec/durable-render-job-and-output-retry-lifecycle.md` (#14)
- Stable OpenAPI artifact: `contracts/openapi/pixelplus-public-api-v1.yaml`
- Stable validator: `node scripts/validate-public-api-contract.mjs`
- Validator mutation suite: `node scripts/test-public-api-contract-validator.mjs`

## 1. Scope and authority

### 1.1 Scope

This specification publishes one stable Public API package by consolidating the accepted inference tracer from #18 and management tracer from #19. It locks:

1. The OpenAPI and URL version carried by the stable package.
2. Which changes are backward-compatible within `/v1` and which require a new major.
3. The deprecation notice, support, and removal policy.
4. HTTP request replay semantics for chat, Asset creation, Render Job creation, Provider Account and credential journeys, resource reads, and output delivery retry.
5. The public seam and composition requirements for future runtime contract tests.

The stable artifact is OpenAPI `3.1.1`, uses JSON Schema Draft 2020-12, has `info.version=1.0.0`, and exposes every relative path under the single server base `/v1`.

### 1.2 Decision separation

| Class | Content | Authority |
|---|---|---|
| Inherited domain behavior | Tenant ownership/non-enumeration, Client API Key authority, Provider Account lifecycle, Capability Snapshot, routing, chat execution, Render Job/Asset lifecycle, canonical errors, commit certainty, retry ownership | #6–#17 normative specs |
| Stable wire and policy decisions | Unified paths/components, `/v1`, `1.0.0`, compatibility/deprecation rules, `Idempotency-Key` matrix, replay retention, stable contract-test seam | this specification + `pixelplus-public-api-v1.yaml` |
| Retained evidence | Separate `0.0.0-prototype` inference and management artifacts and their prototype validators/scenarios | #18 and #19 |
| Deferred implementation | Concrete Gateway modules, Go interfaces/package names, composition root, runtime HTTP conformance runner | #21 and subsequent implementation tickets |

If a prototype description conflicts with this stable policy, this specification and `pixelplus-public-api-v1.yaml` win for Public API representation and policy. The domain specs continue to own operation behavior.

### 1.3 Non-goals

This issue does not:

- Implement a Gateway, HTTP server, Adapter, Credential Vault, database, queue, or worker.
- Choose concrete Go interfaces or package layout before #21.
- Change the domain state machines accepted in #6–#17.
- Claim `Idempotency-Key` is a finalized IETF standard.
- Add a general-purpose YAML parser to the contract validator; the stable artifact remains JSON-compatible YAML. Structural OpenAPI validation uses the pinned Redocly CLI dependency.
- Turn output retrieval or output delivery retry into permission to execute a Provider operation again.

### 1.4 Normative language

- **MUST / MUST NOT / REQUIRED**: observable product or security policy; violation is a defect.
- **SHOULD**: strongly preferred unless a documented constraint requires otherwise.
- **MAY**: optional behavior that cannot weaken a MUST rule.

---

## 2. Stable package

### 2.1 Version identity

| Field | Stable value |
|---|---|
| OpenAPI Specification | `3.1.1` |
| JSON Schema dialect | `https://json-schema.org/draft/2020-12/schema` |
| Server base | `/v1` |
| API semantic version | `1.0.0` |
| Artifact status | `stable` |
| Encoding | JSON-compatible YAML; JSON is a YAML 1.2 subset |
| Authentication | Shared HTTP bearer `ClientApiKey` |

The URL major and semantic major describe different layers but MUST remain aligned:

- `/v1` is the routing and client-compatibility boundary.
- `info.version=1.0.0` identifies the first stable contract document release.
- A backward-compatible clarification or addition can increase MINOR or PATCH while remaining on `/v1`; the validator requires valid SemVer, equality with `x-pixelplus-api-lifecycle.semantic_version`, and semantic-major alignment with `/v1`.
- The frozen `contracts/openapi/baselines/pixelplus-public-api-v1.0.0.yaml` artifact is the compatibility oracle: a MINOR/PATCH candidate must preserve its operations, authorization scopes, parameter/idempotency requiredness, response statuses/media types, required response fields, and closed enums. Pull-request validation MUST load this blob from the full immutable PR base commit SHA via `PIXELPLUS_PUBLIC_API_BASELINE_REF`; mutable refs are rejected and CI fails closed when no immutable source is available. After public release, protected tag `pixelplus-public-api-v1.0.0` is the oracle and the validator rejects worktree baseline drift. The worktree file is only a non-CI local pre-release fallback before that tag exists.
- An incompatible Public API change requires a new URL major and semantic MAJOR, for example `/v2` and `2.0.0`.

OpenAPI's own `openapi: 3.1.1` field identifies the OpenAPI Specification version and is independent of `info.version`.

### 2.2 Unified surface

The stable package contains both accepted surfaces:

- Inference: models, chat completion/cancel, Assets, image generation/edit/inpaint, Render Job poll/cancel/output retry.
- Management: Provider Account lifecycle, direct credential intake, OAuth authorization, probe, reauthentication, controls, Capability Snapshot, and Routing Policy.

The #18 and #19 prototype artifacts remain historical evidence. They are not alternative stable client contracts.

Every stable operation declares its Client API Key authorization requirement with exactly one of `x-required-scopes` or `x-required-scope-any-of`. The validator owns one descriptor matrix that binds path, method, `operationId`, idempotency class/header, and scope requirement. Inference mappings follow #8: models use `capabilities.read`; chat create/cancel use `chat.completions`; Assets use `assets.read`/`assets.write`; image creates use `images.generate`/`images.edit`; Render Job read/cancel/output retry use `jobs.read`/`jobs.manage`. Management mappings preserve the #19 matrix, including Capability Snapshot accepting either `accounts.read` or `capabilities.read`.

### 2.3 Shared components

The stable artifact uses one:

- `ClientApiKey` security scheme.
- `CanonicalError` schema.
- Closed `Remediation` vocabulary containing the union needed by inference and management.
- Shared set of non-enumerating and secret-free canonical error examples.
- Optional and required reusable `Idempotency-Key` parameter definitions.
- Reusable `Deprecation`, `Sunset`, and `Link` response-header definitions.

`CanonicalError.code`, `operation`, `retry_after_class`, and `safe_context` are declared extension points. Closed enums such as `category`, `status_class`, `retryability`, `commit_status`, `idempotency_state`, and `Remediation` are not extension points.

---

## 3. Compatibility policy

### 3.1 Backward compatibility within `/v1`

A `/v1` change is compatible only when an existing conforming client can continue making the same requests and interpreting the same required behavior without code changes.

Compatible examples:

| Change | Why it is compatible |
|---|---|
| Add a new endpoint | Existing endpoint requests and responses do not change. |
| Add an optional request field/header | Existing requests remain valid and preserve behavior. |
| Clarify prose without changing behavior | Wire values, status, authorization, side effects, and retry semantics stay the same. |
| Add a field at a declared open response extension point | Clients were explicitly required to tolerate unseen values/fields there. |
| Add a canonical error example | The example demonstrates existing schema and semantics; it does not create a new required outcome. |

A change is incompatible and MUST use a new major when it can invalidate an existing request, change existing authorization/side effects, or make a previously exhaustive client interpretation incomplete.

Incompatible examples:

| Change | Cause → effect |
|---|---|
| Rename or remove `/chat/completions` | Existing clients receive 404 or call a different operation. |
| Make an optional input required | A request that passed yesterday now fails validation. |
| Add `maxLength`, `minimum`, `maxItems`, or another narrowing constraint where none existed | A previously valid request can now fail schema validation. |
| Add an optional field to a closed response object | An exhaustive generated client can no longer treat the documented object shape as complete. |
| Change Client API Key scope or authentication | The same credential/request pair receives a different authorization result. |
| Change `202` Render Job creation to synchronous output | Polling, cancellation, accounting, and durability behavior all change. |
| Add a value to closed `Remediation` | Generated clients with exhaustive enum handling can fail or mis-handle the response. |
| Make chat `Idempotency-Key` required | Existing OpenAI-compatible callers without the header begin failing. |
| Make image creation `Idempotency-Key` optional | The duplicate-billing/resource-safety guarantee is weakened. |
| Reinterpret `commit_status=unknown` as safe to retry | One accepted request can cause more than one committed Provider side effect. |

### 3.2 Response evolution

`additionalProperties` and enum rules in the OpenAPI schema are normative:

- A closed object MUST NOT gain arbitrary response fields within `/v1` unless the field is at a declared extension point or the object was explicitly documented as open. The compatibility comparator enforces this on response schemas while continuing to allow new optional request fields.
- A closed enum MUST NOT gain or lose values within `/v1`.
- Clients MUST tolerate new `CanonicalError.code`, `operation`, `retry_after_class`, and bounded `safe_context` entries because those are declared open tokens/metadata.
- Extension points never allow secrets, `tenant_id`, prompt/content, raw Provider payload, ciphertext, cookies, tokens, or foreign identifiers.

### 3.3 Security and status compatibility

Within `/v1`, PixelPlus MUST NOT silently change:

- Authentication scheme or required scope.
- Same-Tenant ownership and non-enumeration behavior.
- The order that rejects a foreign/unknown resource before vault decrypt or Adapter call.
- HTTP success status classes or durable-resource creation semantics.
- Canonical retryability, remediation, commit certainty, or idempotency meaning.
- Whether an operation requires, permits, or does not use `Idempotency-Key`.

---

## 4. Deprecation and removal

### 4.1 Notice and support window

A deprecated `/v1` behavior MUST:

1. Have a generally available successor before removal.
2. Receive at least **180 days** of public notice before the old behavior becomes unavailable.
3. Continue preserving its documented behavior during the support window; announcing deprecation does not itself change behavior.
4. Return RFC 9745 `Deprecation` when the response resource is deprecated.
5. Return a `Link` with `rel="deprecation"` to migration policy/instructions.
6. Return RFC 8594 `Sunset` when an unavailability date is known; the Sunset date MUST NOT be earlier than the Deprecation date.
7. Be removed only in a new Public API major.

Illustrative headers:

```http
Deprecation: @1798761600
Sunset: Thu, 01 Jul 2027 00:00:00 GMT
Link: <https://docs.pixelplus.example/migrations/v1>; rel="deprecation"; type="text/html"
```

The exact dates and documentation URL are release data, not constants in this issue.

### 4.2 Removal gate

Removal is permitted only when all conditions are true:

- Notice was published at least 180 days before removal.
- The successor has been generally available through the support window.
- Migration instructions identify request, response, error, authorization, and idempotency differences.
- Removal ships on a new major such as `/v2`; `/v1` is not silently repurposed.
- Contract tests cover both the supported old behavior and the successor until the old support window ends.

The lifecycle extension machine-locks this gate with `migration_instructions_required=true`, an exact migration dimension set (`request`, `response`, `error`, `authorization_scope`, `idempotency`), and `parallel_old_and_successor_contract_tests_until_support_window_ends=true`.

### 4.3 Security or compliance emergencies

An emergency MAY make an operation fail closed through already documented policy gates and canonical errors, for example disabling a compromised Auth Mode or quarantining a Provider Account. It does not authorize:

- Returning secret material.
- Reusing `/v1` fields with a new meaning.
- Treating a dangerous retry as safe.
- Silently deleting the `/v1` wire contract.

If permanent removal is required, PixelPlus still publishes the migration/removal record and moves the incompatible contract to a new major.

---

## 5. Idempotency policy

### 5.1 Four separate concepts

The following concepts MUST NOT be collapsed:

| Concept | Identity and owner | Repeat behavior |
|---|---|---|
| HTTP request replay | Authenticated Tenant + Client API Key + `Idempotency-Key`, bound to an operation fingerprint | Same fingerprint returns/recovers the original operation; different fingerprint conflicts. |
| Chat execution | One accepted chat execution; chat execution layer is the sole full-execution retry owner | Middleware, Adapter, routing, queue, or worker redelivery cannot create an extra chat execution. |
| Render Job creation/execution | One accepted image request creates one durable Render Job; Render Job execution layer owns full image retry | Queue redelivery references the same job/attempt ledger and is not permission to render again. |
| Output retrieval/delivery | Existing job manifest, output entry, placement key, and Asset identities | Reads and delivery retries reuse existing output identity; they never create another Render Job or Provider execution. |

### 5.2 Scope and fingerprint

An idempotency record is scoped by:

```text
(authenticated Tenant, Client API Key, Idempotency-Key)
```

The fingerprint MUST include:

- Operation identity, so the same key on two endpoints conflicts rather than opening two scopes.
- Normalized path/query inputs.
- Every request input that can change the side effect or resulting durable resource.

For direct credential submission and direct reauthentication, the fingerprint stores only a non-reversible keyed digest of secret-bearing input. Raw Provider Credential material, token, cookie, ciphertext, or replayable secret MUST NOT enter the idempotency record, logs, errors, snapshots, or examples. The static gate scans request, response, component, and schema examples before schema validation.

### 5.3 Retention

The HTTP replay record is retained for **24 hours** from initial claim.

- A durable Chat execution, Asset, Render Job, Provider Account, or OAuth authorization can outlive the replay record.
- During retention, matching replay resolves to the original operation/resource state.
- After expiry, the same key is a new request; clients requiring durable lookup use the resource identifier, not the expired key.
- Cleanup MUST NOT erase evidence still required to prevent a second execution while commit state is `committed` or `unknown`.

### 5.4 Header matrix

| Operation class | Operations | `Idempotency-Key` |
|---|---|---|
| Chat execution | `POST /chat/completions` | Optional, preserving bounded OpenAI compatibility. When supplied, all replay rules apply. |
| Asset create | `POST /assets` | Required. |
| Render Job create | `POST /images/generations`, `/images/edits`, `/images/inpaints` | Required. |
| Provider Account create | `POST /provider-accounts` | Required. |
| Direct secret ingress | `POST .../credentials`, `POST .../reauthentication` | Required; fingerprint retains keyed digest only. |
| OAuth authorization start | `POST .../oauth-authorizations` | Required. |
| Resource-state commands | chat/job cancel, Provider Account delete/probe/disable/enable, Routing Policy replace | Header not required; repeated command must preserve the resource-state/product idempotency contract and cannot duplicate external work. |
| Resource/catalog retrieval | `GET /models`, Provider Account list/detail, OAuth authorization status, Capability Snapshot, Routing Policy | Not applicable; repeat reads existing projection/lifecycle state without Provider or job execution. |
| Output retrieval | Asset metadata/content and Render Job status GETs | Not applicable; repeat reads the existing durable output/resource identity. |
| Output delivery retry | `POST .../outputs/{output_entry_id}/retry` | Header not required; stable job/output/placement identity supplies deduplication. |

Header requiredness is itself part of `/v1` compatibility. Changing a row requires a new major.

### 5.5 Claim and replay outcomes

For a claimed key:

1. **First accepted request** atomically binds scope + key to the fingerprint before a non-idempotent Adapter call or durable create side effect.
2. **Concurrent matching request** does not become a second executor. It may receive `idempotency_in_progress` or bounded recovery for the same claim.
3. **Terminal matching request** returns the prior safe response/status/resource references. It creates no new admission reservation, Provider attempt, Render Job, Asset placement, OAuth exchange, vault write, or quota debit.
4. **Different fingerprint** returns `idempotency_conflict`. The original record and operation remain unchanged.
5. **Lost/uncertain owner** returns or exposes `idempotency_uncertain` / `execution_possibly_committed` as appropriate. Recovery may reconcile the same attempt but MUST NOT steal the claim into a new execution.
6. **`committed` or `unknown` Provider attempt** forbids replacement execution. A deliberate client submission with a new key is a new operation, not an automatic retry of the old one.

### 5.6 Concrete cause → effect examples

#### Example A — duplicate image generation

1. Client sends `POST /images/generations` with key `img-42`.
2. Gateway claims the scoped key and creates `job_123`.
3. A timeout hides the `202` response from the client.
4. Client repeats the same request and key.
5. Gateway returns/reconciles `job_123`.
6. Adapter call count, Render Job count, and image quota debit do not increase.

#### Example B — same key, changed prompt

1. `img-42` is bound to prompt `red cube`.
2. Client repeats `img-42` with prompt `blue cube`.
3. Fingerprints differ.
4. Gateway returns `idempotency_conflict`.
5. `job_123` and its original prompt-bound execution remain unchanged; no `blue cube` job is created.

#### Example C — chat compatibility

1. A client omits `Idempotency-Key` on `POST /chat/completions`.
2. The request remains valid for OpenAI-compatible calling behavior.
3. Repeating it is a new chat request because no replay identity was supplied.
4. If the client supplies a key, matching replay has one executor and terminal replay returns the original safe result.

#### Example D — output retrieval and delivery retry

1. `job_123` completed with immutable manifest entry `out_1`.
2. Repeated `GET /render-jobs/job_123` or Asset content retrieval reads the same durable state.
3. `POST /render-jobs/job_123/outputs/out_1/retry` retries placement using the existing manifest and placement identity.
4. No new render, Provider attempt, or Render Job is created.

#### Example E — uncertain commit

1. A chat or image payload may have reached the Provider.
2. The acknowledgement/result is lost, so commit status is `unknown`.
3. Worker redelivery or HTTP replay may reconcile the same operation.
4. Neither may select another account or send a replacement Provider request.
5. The public result remains uncertain until reconciled or terminated according to #12/#14/#16.

---

## 6. Runtime contract-testing policy

### 6.1 Public seam

Runtime conformance MUST enter through the same public HTTP surface used by clients:

```text
HTTP request
  → real Gateway routing/composition
  → domain/application behavior
  → controlled implementation at an approved port
  → HTTP response + observable side-effect counts
```

A test that calls a handler stub or private function directly does not prove composition, authentication, request decoding, policy order, response encoding, or shared inference/management behavior and therefore is not a Public API contract test.

### 6.2 Real composition and controlled implementations

The suite MUST use the real Gateway composition root once it exists. It MAY replace external/nondeterministic implementations only at these conceptual ports:

- Adapter.
- Credential Vault.
- Persistence.
- Job runtime/queue execution.
- Clock.
- ID generator.

The controlled implementation must preserve the port contract and expose deterministic observations such as call count and safe request identity. It is not allowed to bypass the Gateway decision path.

Concrete Go interfaces, packages, constructors, and dependency direction are intentionally deferred to #21. This policy names conceptual boundaries without pre-empting that architecture ticket.

### 6.3 Forbidden test seams

The runtime suite MUST NOT assert behavior through:

- Handler-only stubs that skip real composition.
- Private functions.
- Concrete database table/schema layout.
- Goroutine count or scheduling layout.
- Adapter internals unrelated to the Public API contract.
- Secret-bearing logs or snapshots.

Those may have lower-level tests, but they are not substitutes for the public composition suite.

### 6.4 Required observation set

Each scenario observes the HTTP result and the relevant controlled-port side effects:

| Scenario | HTTP observation | Required side-effect observation |
|---|---|---|
| Matching idempotent replay | Same safe terminal status/body/resource identity, or bounded in-progress result | Zero additional Adapter calls, creates, vault writes, enqueues, placements, or quota debits. |
| Fingerprint conflict | `409` canonical `idempotency_conflict` | Original operation unchanged; zero new external work. |
| Uncertain claim | Canonical uncertain/possibly-committed outcome | No claim stealing, account fallback, or replacement execution. |
| Foreign/unknown resource | Indistinguishable `404 resource_not_found` | Zero vault decrypts and zero Adapter calls. |
| Direct credential intake | Accepted management response never echoes material | Exactly the approved vault write path; no plaintext in response/error/log/snapshot. |
| Image replay | Same `job_id` | One Render Job and at most one committed/uncertain Provider attempt. |
| Output retrieval/retry | Same manifest/output/Asset identity | Zero renders; placement deduplicates on stable identity. |
| Deprecated behavior in support window | Correct `Deprecation`/`Link`, and `Sunset` when scheduled | Existing behavior remains available until its documented boundary. |

### 6.5 Concrete composition examples

#### Same-Tenant replay

1. Send a real HTTP image-create request through the Gateway.
2. Controlled persistence records the idempotency claim and one job.
3. Controlled Adapter records one execution call.
4. Repeat the same HTTP request/key.
5. Assert the same `job_id`, one persisted job, and Adapter call count still equal to one.

#### Foreign Provider Account

1. Authenticate as Tenant A.
2. Call a management endpoint using Tenant B's Provider Account id.
3. Assert the same `resource_not_found` shape used for an unknown id.
4. Assert controlled vault decrypt count is zero.
5. Assert controlled Adapter call count is zero.

This cause → effect sequence proves the security order through the public seam; a handler unit test that only returns 404 does not.

### 6.6 Current executable evidence

Because no Gateway composition root exists in #20, current executable checks are representation/policy checks:

- Redocly CLI runs first with structural OpenAPI rules plus the PixelPlus non-empty Responses Object rule.
- `scripts/validate-public-api-contract.mjs` validates the unified stable artifact, refs, operations, exact scope/idempotency matrices, secret boundaries, examples, lifecycle/removal gates, the frozen v1.0.0 compatibility baseline, and composition policy declarations.
- `scripts/test-public-api-contract-validator.mjs` mutates the public artifact and proves the validator rejects structural, compatibility, authorization, idempotency, and policy drift.
- The #18 and #19 prototype validators/scenarios remain executable evidence for inherited inference and management representation.

A future Gateway implementation MUST add the runtime suite described above; schema-only validation does not satisfy that future requirement.

---

## 7. Security invariants

1. Tenant authority comes only from the authenticated Client API Key Security Principal; no request accepts client-supplied `tenant_id`.
2. Unknown, foreign, and deleted Tenant-scoped identifiers remain indistinguishable `resource_not_found` where defined by the domain specs.
3. Ownership rejection happens before Credential Vault decrypt, Adapter call, queue enqueue, or other protected side effect.
4. Provider Credential material is accepted only by the approved direct credential and direct reauthentication request boundaries.
5. Raw Provider Credential, token, cookie, ciphertext, prompt/content, Provider payload, or foreign id never appears in response, canonical error, example, log, health projection, Capability Snapshot, Routing Policy, or idempotency record.
6. Secret-bearing idempotency fingerprints use a non-reversible keyed digest.
7. Replay/retry signals never bypass ownership, lifecycle, capability, risk, health, vault, commit, or accounting gates.

---

## 8. Validation and acceptance mapping

Run from repository root:

```bash
npx redocly lint contracts/openapi/pixelplus-public-api-v1.yaml --config redocly.yaml
node scripts/validate-public-api-contract.mjs
node scripts/test-public-api-contract-validator.mjs
node scripts/validate-openapi-contract.mjs contracts/openapi/pixelplus-public-api-v0alpha.yaml
node scripts/prototype-management-contract.mjs
```

| Acceptance criterion | Evidence |
|---|---|
| Versioning and backward-compatible change rules are consistent across inference and management | SemVer/URL-major alignment plus the frozen v1.0.0 baseline comparator across operations, schemas, statuses, scopes, and idempotency requiredness. |
| Deprecation defines notice, support window, and removal | §4; minimum 180 days; RFC headers; successor/general-availability, migration-instruction, dual-suite, and new-major removal gates. |
| Authorization remains stable for every operation | One descriptor matrix and exact `x-required-scopes` / `x-required-scope-any-of` checks for all 26 operations. |
| Idempotency distinguishes HTTP replay, chat, Render Job creation, and output retrieval | §5 concept table, operation matrix, exact fingerprint/replay/retry-owner values, and mutation evidence. |
| OpenAPI structure is machine-valid | Pinned Redocly CLI structural gate plus the non-empty Responses Object rule runs before PixelPlus-specific validation. |
| Contract testing uses real Gateway composition with controlled implementations at locked ports | §6 and `x-pixelplus-contract-testing`; exact six-port allowlist and mutation suite reject handler-stub composition or missing protected-side-effect observations. |

---

## 9. Sources

Primary external sources used for the standards-facing parts of this policy:

- OpenAPI Specification 3.1.1: <https://spec.openapis.org/oas/v3.1.1>
- Semantic Versioning 2.0.0: <https://semver.org/spec/v2.0.0.html>
- RFC 9745, The Deprecation HTTP Response Header Field: <https://www.rfc-editor.org/rfc/rfc9745.html>
- RFC 8594, The Sunset HTTP Header Field: <https://www.rfc-editor.org/rfc/rfc8594.html>
- Expired IETF HTTPAPI `Idempotency-Key` Internet-Draft: <https://datatracker.ietf.org/doc/draft-ietf-httpapi-idempotency-key-header/>

The IETF draft informs header naming and common replay vocabulary but is not a finalized standard. PixelPlus idempotency scope, retention, requiredness, fingerprint, secret handling, and retry ownership are product contract decisions grounded in the local #12, #14, and #16 domain specs.
