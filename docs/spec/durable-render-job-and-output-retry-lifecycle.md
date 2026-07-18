# Durable Render Job and Output-Retry Lifecycle

- Status: Accepted for specification (issue #14)
- Date: 2026-07-15
- Parent: [#1](https://github.com/monet88/pixelplus/issues/1)
- Issue: [#14](https://github.com/monet88/pixelplus/issues/14)
- Vocabulary source: `CONTEXT.md`
- Related ownership invariants: `docs/spec/tenant-ownership-authorization-invariants.md` (#6)
- Related risk envelope: `docs/spec/auth-mode-risk-envelope-and-kill-criteria.md` (#7)
- Related Client API Key / admission: `docs/spec/client-api-key-lifecycle-and-admission-controls.md` (#8)
- Related vault and sensitive-data lifecycle: `docs/spec/credential-vault-and-sensitive-data-lifecycle.md` (#15)
- Related connection / credential lifecycle: `docs/spec/provider-account-connection-and-credential-lifecycle.md` (#9)
- Related Capability Snapshot / model availability: `docs/spec/capability-snapshot-and-model-availability-semantics.md` (#10)
- Related tenant-scoped routing / fallback / affinity / lease: `docs/spec/tenant-scoped-routing-fallback-affinity-leases.md` (#11)
- Related chat execution / streaming: `docs/spec/chat-execution-and-streaming-lifecycle.md` (#12)
- Related Asset exchange / authorization / retention: `docs/spec/asset-exchange-authorization-and-retention-lifecycle.md` (#13)

## 1. Scope and non-goals

### 1.1 Scope

This specification locks the **durable lifecycle of one image Render Job** for `image_generation`, `image_edit`, or `inpaint`: creation, queueing, worker claim, upstream execution, progress, cancellation, process recovery, terminal outcome, and delivery of generated output Assets.

It codifies parent #1 user stories 39–43 and consumes the ownership boundary (#6), admission and quota reservation (#8), account usability (#9), capability and mask semantics (#10), tenant-scoped routing and account leases (#11), and Asset validation/retention/storage reservation (#13).

This is **specification work**; it does **not** implement the Gateway, worker, Adapter, queue, object store, or Public API transport.

It covers:

1. The Render Job logical object and the externally observable lifecycle states: `queued`, `running`, `cancel_requested`, `canceled`, `failed`, and `completed`.
2. Atomic creation, scoped idempotency, worker lease/fencing, and recovery after worker or process loss.
3. The single upstream-render attempt boundary and the proof required before any safe pre-commit retry or fallback.
4. Progress and cancellation behavior that remains truthful when the Provider is slow or non-cancelable.
5. The durable result manifest and the separation between upstream rendering and output retrieval, persistence, and Asset placement.
6. Output-placement retry rules that never create another generation/edit/inpaint execution.
7. Public API behavior, ownership, terminal semantics, accounting, and observable test obligations.

### 1.2 Non-goals

This document does **not**:

- Implement Gateway, queue, worker, Adapter, object-store, database, or UI code.
- Redefine ownership/non-enumeration (#6), admission/rate/concurrency/quota (#8), account usability (#9), capability or mask taxonomy (#10), routing/fallback/affinity/lease precedence (#11), chat execution (#12), or Asset validation/retention/storage caps (#13). It consumes them.
- Freeze JSON schema, OpenAPI paths, multipart format, SSE fields, or HTTP idempotency headers — those are #18/#20. This document locks logical fields and observable semantics.
- Define canonical error-code strings — #16 owns exact strings. This document locks failure stage, commit certainty, retryability, and remediation classes.
- Freeze numeric lease, timeout, heartbeat, retry, residual, staging-retention, or cleanup values — #17 tunes named classes and limits.
- Make the Provider generation operation idempotent. Gateway-side durable claims and attempt records prevent duplicate execution; an upstream Provider may still be non-idempotent.
- Provide resumable client upload or resumable output download. Those are separate transport/storage decisions.

Downstream issues **MUST** preserve every decision here. They may add fields, tighten limits, or add UX. They **MUST NOT**:

- Enqueue a job that references an Asset outside `principal.tenant_id` or decrypt a foreign Tenant's credential.
- Re-run generation/edit/inpaint after the upstream attempt is committed or its commit status is uncertain.
- Treat a stale worker lease as permission to mutate a job or launch a second upstream attempt.
- Report `completed` as if an output Asset were downloadable when the output manifest or Asset placement is unavailable; output readiness is separately observable.
- Treat a client-visible cancel acknowledgement as proof that upstream work stopped.
- Release execution occupancy or quota before the accounting terminal for surviving upstream work.
- Use output retrieval, staging persistence, or Asset-placement retry as a reason to create a new Render Job or consume another image-generation reservation.

### 1.3 Normative language

- **MUST / MUST NOT / REQUIRED**: product/security policy. Violation is a defect.
- **SHALL**: same force as MUST for observable Public API behavior.
- **SHOULD**: strongly preferred default; deviation needs an operator-recorded exception.
- **MAY**: optional surface that cannot weaken MUST rules.

### 1.4 Relationship to prior issues

| Topic | Already locked | This document adds |
|---|---|---|
| Ownership and non-enumeration | Job/worker/Asset paths are same-Tenant and foreign references fail closed (#6) | Durable job ownership, worker scope, and output inheritance |
| Admission and accounting | Admission is scope → size → rate → concurrency → quota → accept; enqueue is a post-accept side effect (#8) | Job reservation lifetime, one job per accepted request, and terminal reconciliation |
| Usability and capability | Account must pass `I-USABLE-GATE`; image operation/model must be offerable and fresh (#9/#10) | Request-time preflight before queue/upstream and lease behavior during a job |
| Routing and leases | Candidate set, P0–P5 precedence, fallback opt-in, and `LEASE-TTL-CLASS` (#11) | A Render Job acquires one hard account lease and never silently hops accounts mid-attempt |
| Asset exchange | Inputs/masks are immutable and same-Tenant; output placement uses atomic storage reservation (#13) | Captured output manifest, placement idempotency, and output retry boundary |
| Retry ownership | Chat owns its own proof-of-non-commit contract (#12) | Image-job-specific attempt ledger: no new render after commit or uncertainty |

### 1.5 Decision unit

**One accepted image request = one durable Render Job owned by one Tenant, with one scoped idempotency identity when supplied, at most one committed upstream generation/edit/inpaint attempt, one terminal lifecycle outcome, and zero or more output Assets derived from its immutable result manifest.**

Cause → effect:

1. A valid same-Tenant image request is admitted once, then creates one `queued` job. A worker retry does not create another job or another admission reservation.
2. A worker claims the job and records one upstream attempt before sending payload. A second worker cannot claim the same execution because the lease and attempt claim are atomic and fenced.
3. If payload transmission may have reached the Provider, the attempt is possibly committed. Restart recovery may inspect or drain that attempt, but MUST NOT launch another generation merely because the response is missing.
4. Once the Provider result is durably captured as a result manifest, the job is `completed`. Retrieving bytes, persisting staging data, reserving Asset storage, or placing output Assets may retry from that manifest and never re-enter upstream render.

---

## 2. Glossary extensions (normative use)

| Term | Meaning in this document |
|---|---|
| **Render Job** | Durable Tenant-owned unit of one `image_generation`, `image_edit`, or `inpaint` request. |
| **Job lifecycle state** | Publicly observable state: `queued`, `running`, `cancel_requested`, `canceled`, `failed`, or `completed`. |
| **Execution phase** | Durable sub-phase inside `running`: `preflight`, `upstream`, `capturing_result`, or `placing_output`. It does not replace the lifecycle state. |
| **Result manifest** | Immutable durable description of Provider output(s), including ordered output entries, checksums/size/type when known, source attempt, and staging references. It is the source for all post-render output work. |
| **Output entry** | One logical generated image in the result manifest. A job MAY produce multiple entries; each becomes at most one output Asset. |
| **Output delivery state** | Per-entry state independent of job lifecycle: `pending`, `available`, `expired`, or `failed`. A `completed` job can have `pending` delivery while placement retries run. |
| **Upstream attempt** | One recorded Gateway-to-Provider render attempt with an immutable `attempt_id`, operation/model/account binding, commit status, and retry history. |
| **Commit boundary** | The point after which the Provider may have accepted the generation payload. Before it, a retry MAY be safe only with authoritative non-commit proof; after it, the attempt is committed or uncertain. |
| **Commit status** | Durable attempt fact: `not_started`, `not_committed`, `committed`, or `unknown`. `unknown` is fail-closed and is never treated as `not_committed`. |
| **Worker lease** | Fenced, expiring ownership of job mutation and execution by one worker. It is distinct from the #11 Provider Account lease, though both bind the same job. |
| **Fencing token** | Monotonically increasing lease generation that prevents a stale worker from committing state after lease loss or recovery takeover. |
| **Residual execution** | Bounded accounting/drain tracking for upstream work that survives client cancellation or worker request cancellation. It retains the original Tenant and `client_api_key_id` occupancy until accounting terminal; it is not replacement execution capacity. |
| **Output placement** | Creating or associating a #13 `output` Asset from one result-manifest entry using the Tenant-scoped storage reservation protocol. |
| **Placement key** | Stable `(tenant_id, job_id, output_entry_id)` identity used to make output Asset creation idempotent. |
| **Recovery** | Restart/process-loss handling that resumes a safe pre-commit or post-result phase, or fails closed when commit certainty is unavailable. Recovery never means blind re-render. |
| **Terminal lifecycle state** | `canceled`, `failed`, or `completed`; immutable after publication. Delivery metadata may continue changing under the rules in §8, but lifecycle state never regresses or changes terminal kind. |

---

## 3. Render Job object and ownership

### 3.1 Logical fields

| Field | Required | Notes |
|---|---|---|
| `tenant_id` | yes | Immutable; stamped from the authenticated Security Principal (#6). |
| `job_id` | yes | Stable, unguessable identifier; scoped by owning Tenant for reads. |
| `client_api_key_id` | yes | Originating key for concurrency/quota attribution; never transferable to another key. |
| `idempotency_scope` / `idempotency_key` | when supplied | Scoped as #6 requires; exact header/wire shape is #18/#20. |
| `request_fingerprint` | yes when idempotency is used | Canonical hash of operation, model, prompt/input references, mask/reference options, and relevant policy inputs. |
| `operation` | yes | `image_generation` \| `image_edit` \| `inpaint`; immutable. |
| `model` | requested | Observed model slug or explicit model selection; no silent model substitution (#10/#11). |
| `input_asset_ids` | as required | Same-Tenant immutable input references; role and mask relation validated by #13 before queue. |
| `provider_account_id` | after routing | The one #11-selected account bound to this job's Provider Account lease. |
| `provider_credential_version` | after preflight | Version used by the attempt; credential material is never stored in the job projection. |
| `lifecycle_state` | yes | One of the six states in §4. |
| `execution_phase` | while non-terminal | `preflight` \| `upstream` \| `capturing_result` \| `placing_output`. |
| `state_revision` | yes | Monotonic durable revision for observable reads/events. |
| `progress` | yes | Logical phase, value/knownness, source, and `updated_at`; exact JSON is #18/#20. |
| `attempt_id` | after claim | Stable upstream attempt identity; one committed/uncertain attempt maximum. |
| `result_manifest_id` | after capture | Immutable source for output retrieval and placement; same Tenant. |
| `output_entries` | after capture | Ordered entry status and output Asset references; no output bytes in the job row/API status. |
| `cancel_requested_at` / `cancel_requested_by` | when requested | Audit and observable cancellation intent. |
| `failure_stage` / `failure_class` | when failed | Preflight, upstream, capture, recovery, or output-delivery context; exact error string #16. |
| `commit_status` | after attempt starts | Durable summary of whether upstream may have accepted work. |
| `created_at` / `updated_at` / terminal timestamp | yes | Monotonic lifecycle timestamps; server-owned. |

### 3.2 Ownership and input rules

1. `tenant_id` is immutable and equals `principal.tenant_id` at creation. There is no cross-Tenant transfer or attach operation.
2. Every input and mask reference is resolved inside the same Tenant before the job is persisted or queued. A foreign/unknown Asset id produces the #6/#13 404-class non-enumerating outcome and zero upstream call.
3. `operation` and input roles are validated against #10 capability and #13 mask semantics before queue. An `inpaint` mask MUST NOT be dropped to turn the request into `image_edit`.
4. The selected Provider Account and its credential version are same-Tenant and pass #9/#10/#11 gates before the first upstream payload. The job's account lease does not authorize any other account.
5. Output Assets inherit the job's `tenant_id` and `source_job_id`; a worker never places output under another Tenant even if its process currently holds another Tenant's decrypted credential.
6. Job reads, progress, cancel, output metadata, and output retrieval references obey #6 non-enumeration. A foreign/unknown `job_id` is not distinguished by a 403 or by returned lifecycle metadata.

### 3.3 Admission and durable creation

1. The Public API completes #8 admission before creating a durable job. Admission rejects do not create a `queued` job, consume image execution quota, or reserve output storage.
2. Job creation and the initial execution/quota reservation are atomic from the client's perspective: either the request receives a durable `job_id` in `queued`, or no executable job exists.
3. A client retry with a matching scoped idempotency key and fingerprint returns the existing job identity/status and does not enqueue a second job or reserve a second execution slot.
4. A same-scoped key with a different fingerprint returns an idempotency-conflict class and does not mutate the first job.
5. If the queue backend is unavailable after admission but before durable creation, the request fails without claiming that a job exists. If durable creation succeeded, later queue publication/recovery MUST discover the `queued` job without requiring a second client submission.

---

## 4. Lifecycle states and transitions (AC1)

### 4.1 State meanings

| State | Meaning | Terminal? |
|---|---|---|
| `queued` | Durable job accepted; no worker currently owns execution; no upstream payload has been sent. | no |
| `running` | A live or recoverable worker owns the job lease and is performing preflight, upstream execution, result capture, or output placement. | no |
| `cancel_requested` | Cancellation intent was durably recorded; worker is attempting pre-upstream cancellation, upstream abort, drain, or safe discard. | no |
| `canceled` | No future upstream execution will be launched for this job and the client-visible outcome is cancellation. Accounting has reached its terminal or conservative settlement point. | yes |
| `failed` | No future automatic upstream execution will be launched; the job has an error outcome with commit certainty and retry/remediation class. | yes |
| `completed` | The Provider result is durably captured in an immutable result manifest; no further generation/edit/inpaint is needed or allowed for this job. | yes |

`completed` means **render completion**, not necessarily that every output Asset is currently downloadable. Output delivery is exposed separately via `output_entries`/`output_delivery_state`; a pending placement is retried from the result manifest under §8.

### 4.2 Allowed transitions

| From | Trigger | To | Required effect |
|---|---|---|---|
| none | admitted create | `queued` | Persist ownership, input snapshot/references, idempotency record, and reservation atomically. |
| `queued` | atomic worker claim | `running` | Create/renew worker lease and Provider Account lease; increment `state_revision`. |
| `queued` | cancel before claim | `canceled` | Mark no upstream attempt, release execution reservation exactly once, no worker call. |
| `running` | safe pre-commit internal retry | `running` | Keep one job/attempt ledger, increment retry metadata, prove `not_committed`; never overlap workers. |
| `running` | client/system cancel | `cancel_requested` | Persist cancellation intent; attempt abort or prevent payload transmission. |
| `running` | durable result manifest captured | `completed` | Freeze render result, publish output delivery states, release execution lease, settle accounting. |
| `running` | terminal pre-commit or post-commit failure | `failed` | Persist failure stage/class and commit status; no automatic re-render after terminalization. |
| `cancel_requested` | cancellation confirmed/no future execution | `canceled` | Settle accounting; discard or retain staged bytes only under explicit retention policy; release leases once. |
| `cancel_requested` | unable to establish safe terminal after bounded recovery | `failed` | Mark cancellation/recovery failure and `commit_status=unknown` when applicable; no re-render. |
| terminal | repeated cancel/status/output retry | same terminal state | Idempotent read/no-op or delivery-only action; lifecycle state never changes. |

No other lifecycle transition is valid. In particular, `failed`/`canceled` MUST NOT return to `queued` or `running`, and `completed` MUST NOT return to execution.

### 4.3 Terminal semantics and races

1. `state_revision` is monotonic. A stale worker or delayed queue message MUST NOT publish a lower revision or a second terminal transition.
2. Cancellation is linearized by an atomic compare-and-set against the current state/attempt phase:
   - In `queued`, cancellation wins before any claim and transitions directly to `canceled`.
   - Before the commit boundary, cancellation prevents payload transmission where possible; a confirmed no-commit outcome transitions to `canceled`.
   - After the commit boundary, cancellation cannot undo Provider work. The Gateway attempts abort/drain, suppresses new generation, and eventually transitions to `canceled` if the result is discarded or to `failed` if bounded recovery cannot establish a safe terminal/accounting outcome.
   - After `completed`, `failed`, or `canceled`, cancel is a success no-op and does not delete or alter an already-published output.
3. If a Provider result races with cancellation, the durable ordering of `cancel_requested` versus result capture decides the public lifecycle: a result captured before the cancellation CAS may complete normally; a cancellation CAS that wins before capture suppresses delivery and does not turn the job into `completed`.
4. A job never emits a second lifecycle terminal. Accounting reconciliation, Asset expiry, placement retry, or cleanup events are not lifecycle transitions.
5. A job may expose an operator-visible accounting fault after a terminal state, but it MUST retain conservative debit/occupancy rather than refunding unknown Provider usage as zero.

---

## 5. Worker claim, Provider Account lease, and fencing (AC2)

### 5.1 Atomic claim

1. Only one worker may own a live execution lease for a job at a time. Claim is an atomic conditional operation over `(job_id, lifecycle_state, lease_generation)`.
2. Claim requires `queued` or an explicitly recoverable `running` phase and rechecks same-Tenant ownership, current #9 usability, #10 capability/model offerability, #11 candidate/lease rules, and input Asset availability before payload transmission.
3. A worker that loses its lease MUST stop all state mutation and MUST NOT send a new upstream payload. It MAY finish a non-cancelable network drain only under bounded residual accounting, with no further job claim authority.
4. The lease record contains a worker identity, fencing token, acquisition/heartbeat timestamps, expiry class, and last durable phase. Numeric TTL/heartbeat values belong to #17.
5. Lease renewal is conditional on the same fencing token and non-terminal job state. A renewal after another worker has fenced it out is rejected.

### 5.2 Relationship to the #11 Provider Account lease

1. A Render Job acquires at most one #11 hard Provider Account lease for its execution. The account does not change between preflight, upstream attempt, result capture, and output placement.
2. The worker lease controls process ownership; the Provider Account lease controls routing/account continuity. Neither lease permits cross-Tenant access.
3. A durable #9 usability failure voids the Provider Account lease for new work immediately. An in-flight upstream operation follows the cancel/residual rules below; recovery MUST NOT silently select a different account after a possibly committed attempt.
4. A pre-payload account loss MAY trigger #11 fallback only if the Tenant policy permits it, the exact operation/model is capability-offerable, and the attempt ledger proves `not_committed`. Once payload transmission begins without proof of non-commit, fallback is forbidden.
5. Lease expiry does not imply safe re-render. Recovery first inspects the attempt ledger and commit boundary; it may reclaim bookkeeping, not blindly repeat the Provider operation.

### 5.3 Fencing and stale-worker safety

- Every mutation after claim carries the current fencing token. The store rejects stale-token updates, including terminal transitions, progress, cancellation completion, output placement acknowledgements, and lease renewals.
- A worker crash after sending a payload leaves a durable attempt record that the next worker must honor. It cannot create a fresh attempt solely because the old worker disappeared.
- Queue redelivery is at-least-once delivery of a **job reference**, not permission for at-least-once upstream rendering. The durable claim/attempt ledger converts it to at-most-one committed generation.

---

## 6. Upstream attempt boundary and retry/recovery (AC2, AC3)

### 6.1 Attempt record

Before the first Provider call, the Gateway creates an immutable `attempt_id` record containing:

- `tenant_id`, `job_id`, operation, requested model, selected Provider Account and credential version;
- the job/request fingerprint and input Asset checksums or immutable references;
- attempt sequence, Adapter surface, Provider request/operation locator when available;
- `commit_status`, payload-send marker, response/capture markers, and timestamps;
- retry reason and proof class for any safe pre-commit re-attempt.

The record is scoped to the job's Tenant and cannot be reused by another job or client API key.

### 6.2 Commit boundary

The Gateway MUST distinguish these facts:

| Durable fact | Meaning | Automatic new upstream attempt? |
|---|---|---|
| `not_started` | No payload transmission began. | MAY start the same single attempt after lease recovery. |
| `not_committed` | Adapter has authoritative proof that no generation was accepted, including no payload bytes sent or a Provider-specific no-accept guarantee. | MAY retry/fallback within the bounded job retry policy. |
| `committed` | Provider accepted the generation or returned a result tied to the attempt. | MUST NOT re-render; recover/drain/capture the existing result. |
| `unknown` | Payload may have been accepted but result/ack is unavailable. | MUST NOT re-render; fail closed or use a Provider-supported status lookup for this attempt only. |

An HTTP status, timeout, connection reset, missing response, absent output, or lack of client-visible progress is **not** proof of `not_committed` after payload transmission.

### 6.3 Single retry owner and bounded chain

1. The Gateway Render Job execution layer is the only layer allowed to re-attempt a safe pre-commit image operation. Transport, queue, Adapter, and Provider Account lease layers MUST NOT each retry the same operation independently.
2. All safe retries and #11 fallbacks consume one bounded retry budget/chain for this job. Each candidate attempt is ordered and recorded; no loop or unbounded account walk is allowed.
3. A retry is permitted only while the current attempt is `not_started` or `not_committed` and the job is not terminal/cancel-requested. A matching fingerprint does not make a committed Provider operation idempotent.
4. If the job reaches `committed` or `unknown`, the Gateway records the outcome and stops automatic re-render. The client may submit a deliberate new request; it is not an internal retry.
5. A preflight failure before attempt creation fails/retries without an upstream call. A failure after attempt creation is classified using the attempt's commit status, not merely the exception type.

### 6.4 Worker/process recovery matrix

| Loss point | Recovery action | Forbidden action |
|---|---|---|
| Before durable job claim | Re-deliver `queued`; one worker claims atomically. | Creating a second job from the queue message. |
| After claim, before payload | Reclaim expired lease; resume the same attempt if `not_started`. | Sending through both old and new workers. |
| During payload/after payload, before response | Mark/retain `unknown` unless the Adapter can prove `not_committed`; query/drain the same Provider operation if supported. | Starting a second generation because the old worker timed out or died. |
| Response received, result not yet captured | Reconcile the durable response/handle and capture it under the same `attempt_id`; if bytes are unavailable, fail closed without re-render. | Re-running generation to obtain a replacement output. |
| Result manifest captured, job transition not acknowledged | Recovery finalizes `completed` with the existing manifest using a fenced compare-and-set. | Calling the Provider again. |
| Staging or output placement reservation in progress | Reconcile reservation and retry capture/placement by `result_manifest_id`/placement key. | Reopening image quota or creating another output generation. |
| Worker lost after client cancel | Continue bounded drain/residual accounting under the original Tenant/key; resolve `canceled` or `failed` conservatively. | Freeing capacity while upstream may still run. |

If the durable attempt ledger itself is unavailable or inconsistent, recovery fails closed: no new upstream payload is sent, the job is operator-visible as recovery/accounting failure, and any unknown usage is settled conservatively.

### 6.5 Attempt outcome classes

| Outcome | Job behavior | Client/remediation meaning |
|---|---|---|
| Proven pre-commit transient | Bounded retry/fallback may continue; if exhausted, `failed`. | Safe retry is Gateway-owned; exact error strings #16. |
| Provider accepted and returned output | Capture manifest, then `completed`. | Output delivery may still be `pending`; retry placement only. |
| Provider accepted, output retrieval failed | Keep the attempt committed; retry retrieval from the same Provider handle when supported. If no safe retrieval remains, `failed` with output-recovery class. | Never re-render automatically. |
| Provider response/commit uncertain | `failed` after bounded recovery or explicit operator resolution; `commit_status=unknown`. | New client request may retry, but Gateway does not assume non-commit. |
| Capability/usability/ownership failure before upstream | No attempt or `not_started` attempt; `failed`/request rejection per owning spec. | No Provider execution; remediate account, capability, or input. |

---

## 7. Progress and cancellation (AC1, AC4)

### 7.1 Progress contract

1. Public API status reads and any optional event surface expose the current lifecycle state, execution phase, monotonic `state_revision`, progress source, and `updated_at`. Exact field names are #18/#20.
2. Progress MUST distinguish `reported` Provider progress from `estimated` Gateway phase progress and `unknown`; the Gateway MUST NOT invent token/pixel precision it cannot observe.
3. A numeric progress value, when exposed, is bounded to the canonical range and MUST NOT regress for the same `state_revision` stream. A phase transition may advance the phase while the value becomes `unknown`; it must not fabricate a false percentage.
4. `queued` reports queue/waiting semantics, not Provider progress. `running` reports at least one of `preflight`, `upstream`, `capturing_result`, or `placing_output`. `cancel_requested` reports cancellation/drain status, not successful cancellation.
5. After a terminal lifecycle state, no later progress event may claim active upstream execution or move the lifecycle back to `running`. Delivery updates may change output-entry status only.
6. Progress metadata MUST NOT include credentials, prompt plaintext unless the owning API explicitly permits it, Asset bytes, foreign ids, or Provider-specific raw event payloads.

### 7.2 Cancellation API behavior

1. A cancel request is authorized and resolved inside the owning Tenant. Unknown/foreign `job_id` is non-enumerating 404-class; same-Tenant cancel requires the relevant inference/job scope defined by #8.
2. Cancel on `queued` atomically transitions to `canceled` without worker or Provider execution.
3. Cancel on `running` atomically records `cancel_requested` and returns the durable state. The response MUST NOT claim upstream stopped until a worker/Adapter confirms it.
4. The worker MUST attempt Provider abort when the Auth Mode/Adapter supports it. If abort succeeds before a committed result, transition to `canceled`; if the Provider is non-cancelable, stop client-facing delivery and drain/track under §7.3.
5. Cancel on `cancel_requested`, `canceled`, `failed`, or `completed` is idempotent. It does not emit a second terminal, delete a completed output, or create another attempt.
6. If a cancel request races with a safe pre-commit retry, the cancellation CAS wins or loses atomically. A worker that observes `cancel_requested` MUST NOT begin another attempt.

### 7.3 Accounting and residual work

- The image-job execution reservation and concurrency occupancy created at #8 admission remain held until the accounting terminal: upstream stopped/settled and final known or conservatively settled usage is recorded.
- If upstream survives client/system cancellation or worker request cancellation, the Gateway retains the original Tenant and originating `client_api_key_id` attribution. It MAY move bookkeeping to a bounded same-Tenant residual state; if the residual cap is full, the original job state remains held. No path frees capacity before upstream stops or conservative settlement completes.
- A drain timeout does not prove zero usage and does not authorize an optimistic refund. Missing final usage is retained/debited conservatively and emits an operator-visible accounting fault, following #8/#12 principles.
- Residual tracking is not a second execution pool and does not permit another worker to render the same job. A recovered worker may drain/capture the same attempt, not generate a replacement.

---

## 8. Result manifest and output delivery retry (AC3, AC4)

### 8.1 Capture-before-complete rule

1. The Gateway MUST durably capture the Provider result or a Provider-supported immutable retrieval handle into a `result_manifest` before transitioning the job to `completed`.
2. The manifest is immutable and Tenant-scoped. It contains an ordered entry per logical output, the originating `attempt_id`, content type/size/checksum when known, retrieval/staging reference, and output delivery state.
3. If the Provider returns a temporary URL/handle, the Gateway MUST persist the handle and its expiry/retrieval class before completion. A later fetch is a retrieval step for the same committed result, not a new render.
4. If the result cannot be captured or safely referenced, the job MUST NOT claim `completed`; it becomes `failed` with commit status preserved. Recovery may retry capture/retrieval from the same attempt only when that operation is safe.
5. Once `completed`, the job's attempt and result manifest are immutable. Output delivery may continue independently, but no lifecycle path can call the Adapter's generation/edit/inpaint operation again.

### 8.2 Output delivery state and Public API behavior

Each output entry reports:

- stable `output_entry_id` and ordered position;
- `output_delivery_state`: `pending`, `available`, `expired`, or `failed`;
- `asset_id` only when a same-Tenant #13 output Asset exists;
- safe checksum/type/size metadata when known;
- delivery failure/remediation class when placement cannot proceed.

A `completed` job with `pending` output entries is a truthful render-complete result whose Asset delivery is not yet ready. The Public API MUST NOT return an `asset_id` before the Asset placement commit succeeds and MUST NOT return downloadable bytes from a failed/expired entry.

### 8.3 Idempotent output retrieval, persistence, and placement

1. **Retrieve:** A retry fetches the existing Provider result handle or staging object identified by `result_manifest_id`/entry id. It MUST NOT invoke generation/edit/inpaint. If the handle is expired or unavailable, the entry becomes `failed`/`expired` according to #13/#16; a new render is not implicit.
2. **Persist:** Saving bytes to durable staging is keyed by `(tenant_id, result_manifest_id, output_entry_id, checksum)` or an equivalent stable identity. Repeating a successful save returns the existing staging object; a checksum mismatch fails closed and never overwrites the captured result.
3. **Place:** Asset creation uses #13's atomic Tenant storage reservation and a stable `placement_key = (tenant_id, job_id, output_entry_id)`. Repeated placement returns the existing output Asset or safely resumes the same reservation; it cannot create duplicate Asset objects or double-count bytes.
4. **Storage-cap failure:** If #13 storage reservation is unavailable or exceeds the Tenant cap, the job remains `completed` and the entry remains `pending`/`failed` with `storage_cap_exceeded`; a later delivery retry may run after the Tenant frees capacity. It never consumes another image-job quota or re-renders.
5. **Partial delivery:** Entries are independent. One placed output may be `available` while another remains `pending` or `failed`; retries operate on the affected entry only and preserve ordering metadata.
6. **Expiry/deletion:** Once an output Asset expires or is deleted under #13, retrieval follows #13's `gone`/not-found semantics. The output entry becomes `expired`, and MVP MUST NOT re-place or recreate an Asset from that entry. Any future recreation requires the explicit `D-RENDER-OUTPUT-RECREATE` decision to define a new identity and client-visible lifecycle; it is never an implicit new Provider render.
7. **Terminal job replay:** Repeating a create/status/output request with the same scoped idempotency identity returns the same job/manifest/Asset references. It never adds a generation, output Asset, quota reservation, or placement side effect beyond the idempotent operation.

### 8.4 Output delivery retry ownership

The Gateway's output-delivery worker owns retries of retrieval, staging, reservation, and placement. Queue/transport/client layers MUST NOT wrap an output-placement retry in a new image-generation request. Each delivery action has its own bounded retry budget and durable attempt/result markers; exhausted delivery retries remain observable and operator-remediable without changing the render attempt.

---

## 9. Ownership, authorization, and redaction

All Render Job paths consume #6, #8, #11, and #13:

1. Job lookup, idempotency replay, cancel, progress, result manifest, and output references resolve by `(principal.tenant_id, resource_id)`. Foreign and unknown ids are indistinguishable 404-class outcomes.
2. A worker/system path carries the job's `tenant_id` explicitly. It may read only same-Tenant input Assets, Provider Account metadata/credential version, staging objects, and output reservations.
3. A Provider Account lease is a same-Tenant routing binding, not ambient authority. A decrypted credential for another Tenant or job cannot be selected for this job.
4. Logs, metrics, traces, and Public API projections MUST redact credential material, Client API Key material, prompt/image bytes, temporary Provider tokens/URLs, and foreign-resource existence. Use stable local ids and safe classes instead.
5. Output Asset `source_job_id` is same-Tenant provenance. It MUST NOT become a cross-Tenant lookup oracle.
6. Job cancellation, output placement, and cleanup are authorized against the originating Tenant and key scope. A worker cannot move quota/occupancy or storage reservation to another Tenant/key.

---

## 10. Security and failure impact summary

| Defect | Impact |
|---|---|
| Duplicate worker claim or stale-worker mutation | Concurrent duplicate generation, corrupt terminal state, quota/accounting race |
| Blind re-render after payload timeout/restart | Duplicate Provider billing and image side effects |
| Treating `unknown` as `not_committed` | At-least-once upstream generation under crash/retry |
| Output placement retry creates a new render | Duplicate image generation and unexpected cost after a successful job |
| Output placement lacks idempotent key | Duplicate Assets, storage-cap overrun, ambiguous client references |
| Marking `completed` before durable result capture | Irrecoverable output loss or false success after process failure |
| Progress claims precise Provider state without evidence | Client misrepresentation and unsafe cancellation/retry decisions |
| Freeing concurrency/quota while residual upstream runs | Tenant quota bypass and self-inflicted resource exhaustion |
| Cross-Tenant job/Asset/credential access | Confused deputy and confidential image leakage |
| Cancel mutates terminal completed output | Destructive surprise and broken idempotent replay |
| Queue redelivery treated as render permission | At-least-once queue becomes at-least-once Provider execution |

---

## 11. Test obligations

Exact harness arrives with contract prototypes (#18–#20). Required observable cases for this issue:

### 11.1 State machine and idempotency (AC1)

1. An admitted valid request creates exactly one `queued` job with immutable Tenant/input/operation/model identity.
2. A queued cancel transitions directly to `canceled`, releases the reservation once, and makes zero worker/Provider calls.
3. An atomic worker claim allows one worker to enter `running`; a concurrent claimant is rejected or observes the existing lease and cannot call upstream.
4. A job moves only through the allowed transitions; terminal states never regress or change terminal kind.
5. Cancel of `running` exposes `cancel_requested` before stop confirmation and does not falsely report `canceled` immediately.
6. Cancel is idempotent in every state; cancel after `completed` does not remove or alter output.
7. Matching scoped idempotency replay returns the same job and performs zero new admission, queue, generation, or placement side effects.
8. A fingerprint mismatch on the same scoped idempotency key returns conflict and leaves the first job unchanged.

### 11.2 Lease, fencing, and recovery (AC2)

9. A stale worker cannot renew, update progress, transition state, or place output after a fencing takeover.
10. Worker loss before payload resumes the same `not_started` attempt without a duplicate Provider call from concurrent workers.
11. Worker loss after payload with no response records `unknown` or performs an authoritative same-attempt status lookup; it never launches a replacement render.
12. A Provider response captured before worker loss is recovered into the same result manifest and `completed` without another Adapter generation call.
13. Durable attempt/lease-store unavailability fails closed and does not permit a new upstream payload.
14. A Render Job retains one same-Tenant Provider Account lease across claim, retry-safe preflight, upstream, capture, and placement; no silent mid-attempt account hop occurs.

### 11.3 Retry, cancellation, and accounting (AC2/AC3)

15. A pre-payload transient with authoritative `not_committed` proof may retry within one bounded chain.
16. A timeout, reset, missing response, or missing delta after payload is not treated as proof of non-commit and produces no automatic second generation.
17. Cancel before commit prevents a new payload; cancel after commit attempts abort/drain and retains original Tenant/key occupancy until accounting terminal.
18. A non-cancelable upstream cannot free concurrency or refund unknown usage at client cancel; residual capacity is bounded and same-Tenant.
19. Missing final usage settles conservatively and emits an accounting fault rather than refunding zero.
20. Retry/fallback after a post-A6 `provider_rate_limited` or `provider_quota_exhausted` outcome requires the image attempt's authoritative non-commit proof and Tenant policy; error/status class alone is insufficient. Persisted health uses canonical #17 `cooling_down/provider_rate_limited` or `cooling_down/provider_quota_exhausted`.

### 11.4 Progress and output delivery (AC4)

21. Status/progress exposes `queued`/`running` phase and monotonic revision; stale updates cannot regress a terminal job.
22. Estimated/unknown progress is labeled honestly; the Gateway does not expose fabricated pixel/token precision.
23. A successful Provider result is durably captured before `completed`; a crash between capture and state acknowledgement recovers the same manifest.
24. A completed job with pending output placement reports render completion plus pending delivery, not a fake `asset_id` or a second generation.
25. Retrying output retrieval, staging persistence, or Asset placement after `completed` performs zero generation/edit/inpaint calls and reuses the same manifest/placement key.
26. Repeated placement creates at most one same-Tenant output Asset and releases/reserves storage exactly once.
27. Storage-cap exhaustion leaves the job completed and delivery retryable/observable without reopening image quota or upstream execution.
28. Partial multi-output delivery updates only affected entries; available output Assets remain stable across retries.
29. Expired/deleted output follows #13 retrieval semantics; output retry never bypasses retention or ownership policy.

### 11.5 Ownership and redaction

30. A foreign input/mask/job/output id yields non-enumerating 404-class behavior, zero cross-Tenant read, and zero Provider call.
31. Worker output placement stamps the job Tenant and source job id; Tenant B cannot retrieve or list Tenant A's output.
32. Public status/log/metric projections contain no credential, Client API Key, temporary token/URL, prompt/image bytes, or foreign-resource existence.

---

## 12. Core invariants (normative checklist)

1. **I-RENDER-ONE-JOB** — One accepted image request creates at most one durable Render Job per scoped idempotency identity; matching replay never enqueues or executes another job.
2. **I-RENDER-STATE-MACHINE** — Lifecycle states are exactly `queued`, `running`, `cancel_requested`, `canceled`, `failed`, and `completed`; only §4.2 transitions are valid; terminal state is immutable.
3. **I-RENDER-ATOMIC-CLAIM** — Only one live worker lease/fencing token can own a job mutation/execution at a time; queue redelivery never grants a second upstream execution.
4. **I-RENDER-FENCED-WORKER** — Every worker mutation carries the current fencing token; stale workers cannot update state, progress, terminal outcome, or placement.
5. **I-RENDER-SAME-TENANT** — Job, input/mask Asset, Provider Account/credential version, worker authorization, result manifest, staging, and output Asset remain inside the job Tenant; foreign ids are non-enumerating.
6. **I-RENDER-ACCOUNT-LEASE** — A Render Job binds to at most one same-Tenant Provider Account lease for its execution; no silent account hop after a possibly committed attempt.
7. **I-RENDER-CAP-BEFORE-UPSTREAM** — Ownership, input/mask validation, usability, risk, capability/model, and routing gates pass before the first Provider payload; inpaint is never silently downgraded.
8. **I-RENDER-COMMIT-TRUTH** — `unknown` commit status is fail-closed and never treated as `not_committed`; missing response/timeout/reset/absence of progress is not non-commit proof after payload.
9. **I-RENDER-RETRY-BOUNDARY** — Only the Gateway job execution layer may retry a bounded `not_started`/authoritatively `not_committed` attempt; transport/queue/Adapter layers cannot multiply retries.
10. **I-RENDER-NO-DUPLICATE-UPSTREAM** — Once an attempt is `committed` or `unknown`, recovery, fallback, cancellation, and output retry MUST NOT launch another generation/edit/inpaint.
11. **I-RENDER-CANCEL-HONEST** — Cancel is durable and idempotent; `cancel_requested` is not proof of stopped upstream; terminal cancellation follows confirmed stop/discard or conservative failure.
12. **I-RENDER-ACCOUNTING-BOUNDED** — Surviving upstream retains original Tenant/key occupancy and quota reservation through accounting terminal; cleanup timeout never authorizes optimistic refund or replacement capacity.
13. **I-RENDER-PROGRESS-HONEST** — Public progress identifies lifecycle/phase and reported vs estimated vs unknown evidence; revisions are monotonic and no active progress follows terminal state.
14. **I-RENDER-CAPTURE-BEFORE-COMPLETE** — `completed` is published only after an immutable same-Tenant result manifest is durable; recovery finalizes that manifest without re-render.
15. **I-RENDER-OUTPUT-RETRY-ONLY** — Retrieval, staging persistence, storage reservation, and Asset placement retry from the result manifest/placement key only; they never reopen execution or image quota.
16. **I-RENDER-PLACEMENT-IDEMPOTENT** — Each output entry maps to at most one same-Tenant output Asset via a stable placement key; repeated placement cannot double-count storage.
17. **I-RENDER-DELIVERY-SEPARATE** — Render lifecycle completion and output Asset availability are separately observable; pending/failed delivery never masquerades as a ready Asset.
18. **I-RENDER-TERMINAL-REPLAY** — Replaying a terminal job returns the prior result/status and stable output references; it emits no second terminal or generation side effect.

---

## 13. Open follow-ups (explicitly deferred)

| Topic | Issue | Constraint retained here |
|---|---|---|
| Numeric queue/worker lease TTL, heartbeat, timeout, drain, residual, retry, staging-retention, and cleanup limits | #17 | Named bounded classes and fail-closed behavior are locked; #17 tunes values, not no-duplicate or no-optimistic-refund rules |
| Canonical error strings, problem+json, retry-after and remediation fields | #16 | Failure stage, commit certainty, retryability, and remediation classes are locked |
| JSON schema, OpenAPI paths, idempotency header, status polling/events, cancel endpoint | #18/#20 | Logical fields, state/event semantics, and scoped idempotency behavior are locked |
| Adapter-specific output handle/status lookup contracts | Provider research / adapter issues | Same-attempt lookup MAY recover a committed result; no Provider contract is invented here |
| Vault encryption, staging cryptographic deletion, legal hold | #15 | Same-Tenant scope/redaction and retention moments are locked; crypto details remain #15 |
| Multi-output response schema and ordering wire details | #18/#20 | Ordered manifest entries and one placement key per entry are locked |
| Resumable client upload/download | reopen `D-RENDER-RESUME` | MVP recovery is server-side durable attempt/result recovery, not client transport resume |
| Provider-native idempotency keys | reopen `D-RENDER-UPSTREAM-IDEMPOTENCY` | Gateway prevents duplicates without assuming Provider idempotency; adapter-specific support may strengthen recovery |
| Automatic output recreation after Asset expiry | reopen `D-RENDER-OUTPUT-RECREATE` | MVP never re-renders implicitly; recreation, if later allowed, must be an explicit new client decision |

---

## 14. ADR decision

No new ADR. Durable ownership, fail-closed execution, and no duplicate side effects were product-locked in parent #1 and #6; account continuity and fallback are locked in #11; Asset immutability and storage reservation are locked in #13. This document is the durable normative expansion under `docs/spec/` for Render Job state, worker recovery, the image attempt commit boundary, cancellation/progress, and output-only retry.

An ADR **would** be warranted if the product later introduced:

- at-least-once automatic re-render after an uncertain Provider commit (forbidden by `I-RENDER-NO-DUPLICATE-UPSTREAM`),
- output placement that silently re-runs generation (forbidden by `I-RENDER-OUTPUT-RETRY-ONLY`),
- default cross-Tenant/shared-pool workers or account hopping mid-job (forbidden by #6/#11),
- or implicit output recreation after retention expiry (deferred `D-RENDER-OUTPUT-RECREATE`).

---

## 15. Constants and reopen ids

| Id | Meaning |
|---|---|
| `RENDER-WORKER-LEASE-CLASS` | Expiring/fenced worker ownership of a job; numeric #17 |
| `LEASE-TTL-CLASS` | Owned by #11; Provider Account lease consumed by the job |
| `RENDER-RETRY-BUDGET-CLASS` | Bounded safe pre-commit retry/fallback chain; numeric #17 |
| `RENDER-DRAIN-TIMEOUT-CLASS` | Bounded cancel/recovery drain period; numeric #17 |
| `L-TENANT-RENDER-RESIDUAL` | Same-Tenant residual tracking cap; no replacement execution capacity; numeric #17 |
| `RENDER-STAGING-RETENTION-CLASS` | Retention for captured result bytes/handles until delivery resolves; numeric #17/#15 |
| `RENDER-OUTPUT-DELIVERY-RETRY-CLASS` | Bounded retrieve/persist/place retry policy; numeric #17 |
| `RENDER-OUTPUT-PLACEMENT-KEY` | Stable `(tenant_id, job_id, output_entry_id)` idempotency identity |
| `I-RENDER-*` | Invariants in §12 |
| `D-RENDER-RESUME` | Reopen for client transport resume |
| `D-RENDER-UPSTREAM-IDEMPOTENCY` | Reopen for Provider-native idempotency integration |
| `D-RENDER-OUTPUT-RECREATE` | Reopen for explicit post-expiry output recreation |
| `I-USABLE-GATE` / capability offerable / `LEASE-TTL-CLASS` | Owned by #9/#10/#11; consumed here |
| `RETAIN-OUTPUT` / `RETAIN-EPHEMERAL` / `ASSET-TOMBSTONE-TTL-CLASS` | Owned by #13; output Asset/staging lifecycle consumes them |

---

## 16. Acceptance criteria traceability

| AC (issue #14) | Where satisfied |
|---|---|
| Queued, running, cancel_requested, canceled, failed, completed have clear transitions and terminal semantics | §4, §11.1, `I-RENDER-STATE-MACHINE`, `I-RENDER-TERMINAL-REPLAY` |
| Idempotency and lease/recovery rules prevent duplicate upstream render after restart/concurrent claim | §3.3, §5, §6, §11.1–§11.2, `I-RENDER-ONE-JOB`, `I-RENDER-ATOMIC-CLAIM`, `I-RENDER-FENCED-WORKER`, `I-RENDER-NO-DUPLICATE-UPSTREAM` |
| Retry retrieving, saving, or placing output after completion never reruns generation/edit/inpaint | §8, §11.4, `I-RENDER-CAPTURE-BEFORE-COMPLETE`, `I-RENDER-OUTPUT-RETRY-ONLY`, `I-RENDER-PLACEMENT-IDEMPOTENT`, `I-RENDER-DELIVERY-SEPARATE` |
| Progress, cancellation, and output Asset have consistent observable Public API behavior | §7–§9, §11.3–§11.5, `I-RENDER-CANCEL-HONEST`, `I-RENDER-PROGRESS-HONEST`, `I-RENDER-SAME-TENANT` |

---

## 17. Document control

| Field | Value |
|---|---|
| Status | Accepted for specification (issue #14) |
| Check date of evidence inputs | 2026-07-15 |
| Supersedes | n/a (initial durable Render Job / output-retry lifecycle lock) |
| Next review | On #8 accounting changes, #9 account-loss changes, #10 image capability changes, #11 lease/fallback changes, #13 Asset storage/retention changes, #16 error ownership, or #17 numeric tuning |
| Authors | Spec decision agent for issue #14 |
