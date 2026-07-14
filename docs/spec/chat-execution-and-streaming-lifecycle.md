# Chat Execution and Streaming Lifecycle

- Status: Accepted for specification (issue #12)
- Date: 2026-07-14
- Parent: [#1](https://github.com/monet88/pixelplus/issues/1)
- Issue: [#12](https://github.com/monet88/pixelplus/issues/12)
- Vocabulary source: `CONTEXT.md`
- Related ownership invariants: `docs/spec/tenant-ownership-authorization-invariants.md` (#6)
- Related risk envelope: `docs/spec/auth-mode-risk-envelope-and-kill-criteria.md` (#7)
- Related Client API Key / admission: `docs/spec/client-api-key-lifecycle-and-admission-controls.md` (#8)
- Related connection / credential lifecycle: `docs/spec/provider-account-connection-and-credential-lifecycle.md` (#9)
- Related Capability Snapshot / model availability: `docs/spec/capability-snapshot-and-model-availability-semantics.md` (#10)
- Related tenant-scoped routing / fallback / affinity / lease: `docs/spec/tenant-scoped-routing-fallback-affinity-leases.md` (#11)

## 1. Scope and non-goals

### 1.1 Scope

This specification locks the **canonical chat execution lifecycle** — from an admitted Public API chat request through its client and accounting terminal moments — for both **non-streaming** and **streaming** responses, so a client observes **one Provider-independent contract** regardless of which Auth Mode or Provider Account served the request.

It codifies parent #1 user stories 28–36 and consumes the ownership boundary (#6), admission pipeline (#8), usability gate (#9 `I-USABLE-GATE`), capability gate (#10, including the **real-vs-synthetic streaming** distinction), and routing/affinity/lease precedence (#11).

This is **specification work**; it does **not** implement the Gateway, Adapter, or stream transport code.

It covers:

1. The **request-to-terminal lifecycle** phases and where chat sits relative to admission (#8 A6) and routing (#11).
2. **Non-streaming** canonical response shape (logical) and its single client-terminal outcome.
3. **Streaming** canonical event ordering, the terminal event set, and the guarantee that terminal semantics do not leak Provider-specific framing.
4. **Cancellation, client disconnect, and timeout**: whether upstream execution continues or is aborted, and the **quota/concurrency** consequence of each.
5. **Conversation affinity** and **account lease** for chat, consuming #11 precedence.
6. The **chat retry boundary**: which layer may retry, and the idempotency rule that prevents duplicate non-idempotent chat execution.

### 1.2 Non-goals

This document does **not**:

- Implement Gateway, Adapter, SSE/transport framing, or client SDK code.
- Redefine **ownership / non-enumeration** (#6), **admission controls / concurrency / quota reservation** (#8), the **usability gate** (#9 §5.1), **capability enforcement or streaming class** (#10), or **routing precedence / fallback / lease / affinity** (#11). It **consumes** them.
- Own the **capability taxonomy or the real-vs-synthetic streaming fact** (#10 §3.1) — chat execution reads the offerable/streaming class, it does not compute it.
- Freeze **numeric** timeout values, keepalive intervals, retry budgets, or backoff timers — those are #17 (this document locks named **classes** and the observable behavior).
- Freeze **JSON schema, SSE field names, OpenAPI paths, or HTTP idempotency headers** — those are #18 / #20. This document locks **logical events, ordering, and semantics**.
- Define **canonical error code strings** — #16 owns those; this document locks terminal-outcome and remediation **classes** and their retryability.
- Define the **image / Render Job** lifecycle (#13/#14). Chat here is the synchronous (or streamed) text path, not durable image jobs.
- Design **vault crypto** (#15) or **operator health/cooldown numbers** (#17) beyond the execution signals they consume/emit.

Downstream issues **MUST** preserve every decision here. They may add fields, tighten timeouts, or add UX. They **MUST NOT**:

- Leak Provider-specific terminal framing, event names, or error shapes into the canonical contract (parent #1 story 29/30).
- Continue billing quota after a terminal cancel is observed by the client without an honest in-flight-residual statement (§6).
- Blindly retry a non-idempotent chat execution across the same or a different account (§7).
- Switch the serving account mid-stream in violation of the #11 lease (§5).
- Emit a second terminal event on one stream, or emit content after a terminal event (§4).

### 1.3 Normative language

- **MUST / MUST NOT / REQUIRED**: product/security policy. Violation is a defect.
- **SHALL**: same force as MUST for observable Public API behavior.
- **SHOULD**: strongly preferred default; deviation needs an operator-recorded exception.
- **MAY**: optional surface that cannot weaken MUST rules.

### 1.4 Relationship to prior issues

| Topic | Already locked | This document adds |
|---|---|---|
| Admission vs execution | Authn→scope→size→rate→concurrency→quota→A6 accept; chat = one request at admission, not per SSE event (#8 §6, §7.3) | The execution phases **after** A6 and their terminal semantics |
| Concurrency slot lifetime | Chat occupancy acquired at A6 under both Tenant and key counters; surviving upstream work retains both until accounting terminal (#8 §7.4) | The exact client/accounting terminal events and residual tracking state |
| Token quota reservation | Reserve at A6, reconcile after completion (#8 §7.5) | Reconcile at accounting terminal for every outcome; retain reservation across non-cancelable residual work |
| Usability + capability gate | `I-USABLE-GATE` + capability reject-before-upstream (#9 §5.1, #10 §9) | Chat/`chat_streaming` capability checked before Adapter call; synthetic-streaming honesty |
| Routing / lease / affinity | Precedence P0–P5; lease binds a streaming session; affinity is soft (#11 §4–§5) | How a chat turn acquires affinity and a stream acquires a lease |
| Cancel-on-revoke | Revoke MUST attempt cancel of cancelable in-flight chat (#8 §4.5, `I-CANCEL-ON-REVOKE`) | The cancel protocol and its quota effect |

### 1.5 Decision unit

**One chat execution = one admitted Public API chat request (one Security Principal, one requested model, streaming or not) resolved to exactly one Provider Account (#11), driven to exactly one client-terminal outcome and one accounting terminal, with at most one canonical terminal signal to the client.**

Cause → effect:

1. A non-streaming request that succeeds returns **one** canonical response with a terminal `finish` classification; a client never has to parse Provider-specific end markers.
2. A streaming request emits an **ordered** sequence of content events followed by **exactly one** terminal event (`completed` / `canceled` / `failed`); no content follows the terminal event, and no second terminal event is emitted.
3. A client that disconnects mid-stream causes a **defined** outcome: attempt cancel, emit no more client events, and release concurrency only when upstream stops; surviving work may move to bounded residual tracking but retains its original Tenant+key occupancy until accounting reconciliation — never silently orphan upstream generation or mint replacement capacity.

---

## 2. Glossary extensions (normative use)

| Term | Meaning in this document |
|---|---|
| **Chat execution** | The post-admission (#8 A6) processing of one chat request against one routed Provider Account (#11) through its client and accounting terminal moments. |
| **Non-streaming response** | A single canonical response body delivered once, carrying the full assistant message and a terminal `finish_class`. |
| **Streaming response** | An ordered sequence of canonical **stream events** delivered incrementally, ending in exactly one terminal event. |
| **Stream event** | A canonical incremental unit (`open`, `delta`, `heartbeat`, terminal). Provider-specific framing is normalized into these before the client sees them (#10 §3.1 governs whether real or synthetic). |
| **Client terminal outcome** | The single final classification visible to the client: `completed`, `canceled`, `failed`, or `timed_out`. On the wire it maps onto the three terminal **events** (§4.3) — `timed_out` emits `failed` with a timeout remediation class (§6.4), never a fourth wire signal. |
| **Accounting terminal** | The point at which upstream work has stopped or naturally completed and final usage has been reconciled. Usually coincides with the client terminal; it occurs later when a non-cancelable upstream continues as residual work (§6.5). It emits no second client terminal event. |
| **Residual hold** | A bounded, same-Tenant tracking state for non-cancelable upstream work after the client terminal. It preserves quota reservation and the original Tenant+`client_api_key_id` concurrency occupancy until X6; it is not a second concurrency pool and does not mint replacement capacity (§6.5). |
| **Synthetic streaming** | A stream the Provider does not natively deliver token-by-token; the Adapter chunks a buffered full body (Gemini Web Cookie, #10 §3.1/§4.3). Canonically still ordered + terminal, but MUST NOT be advertised as real token latency. |
| **Cancellation** | A client- or system-initiated request to stop an in-flight chat execution before natural completion. |
| **Client disconnect** | The client transport closes (TCP/HTTP2 stream reset, browser close) before a terminal outcome is delivered. |
| **Timeout class** | A named maximum duration after which the Gateway terminates a phase of chat execution; numeric value is #17-tunable. |
| **Retry boundary** | The single layer permitted to re-attempt a failed chat execution, and the conditions under which a re-attempt is allowed at all (§7). |
| **Idempotency scope** | The `(tenant_id, client_api_key_id-or-scope, idempotency_key)` record (#6 §3 Idempotency Record) that de-duplicates an accepted chat request. |

---

## 3. Chat execution lifecycle (phases)

### 3.1 Phase order (normative)

A chat request that reached **A6 accept** (#8 §6) proceeds through these phases. Everything before A6 is admission (#8) and is out of scope here except as the entry precondition.

| Phase | What happens | Owner |
|---|---|---|
| **X0 Admitted** | A6 accept succeeded: concurrency slot held, token reservation made (#8 §7.4/§7.5) | #8 |
| **X1 Route and select** | Build the #11 candidate set for the requested `op`+model (including C0–C5 capability/health filters), apply P1–P5, resolve exactly one same-Tenant account, and acquire affinity/lease per §5 | #11 (consumed) |
| **X2 Selected-account gate** | Reaffirm the selected account's current usability and `op`+model Capability Snapshot immediately before credential access; reject if unsupported/unverified/not-fresh/model-unavailable. Synthetic-only streaming is governed by §4.4 | #9/#10 (consumed) |
| **X3 Credential decrypt** | Decrypt the selected account's credential on the same-Tenant authorized path only (#6 §5.3 rule 3) | #6/#15 |
| **X4 Upstream execution** | Atomically claim idempotency (§7.3), then Adapter calls the Provider and normalizes response/stream into canonical shape (§4) | this doc |
| **X5 Client terminal** | Deliver at most one client terminal outcome; release concurrency only if upstream stopped, otherwise move bookkeeping to residual tracking while retaining the original Tenant+key occupancy (§6.5) | this doc + #8 |
| **X6 Accounting terminal** | When upstream work is definitively over, reconcile final token usage and release the original Tenant+key concurrency occupancy plus any residual tracking state exactly once; normally coincides with X5 | this doc + #8 |

Cause → effect:

1. X1 can evaluate capability because it constructs candidates from each account's snapshot before selecting one; there is no check against an account that has not yet been resolved.
2. X2 reaffirms the chosen account immediately before decrypt, so a snapshot/usability change between candidate construction and execution fails closed with zero Adapter calls (#9 `I-USABLE-GATE`, #10 `I-CAP-REJECT-BEFORE-UPSTREAM`).
3. Failures in X1–X3 and an idempotency-claim loss before the Adapter call are **pre-upstream**: release this request's slot/reservation and make no Provider call. X4 failures are classified by proof of non-commit (§7.2), not merely by phase name.
4. X5 and X6 are the same transition unless a non-cancelable upstream survives the client terminal; in that case X5 moves bookkeeping to bounded residual tracking without freeing Tenant/key concurrency, and X6 performs final reconciliation and one-time release without emitting another client event (§6.5).

### 3.2 Streaming vs non-streaming branch

- The client selects streaming per the OpenAI-compatible request (schema/field is #18/#20). The **operation token** differs: non-streaming → `chat`; streaming → `chat_streaming` (#10 §3.1). The capability gate (X1) MUST check the operation the client actually requested.
- A request for `chat_streaming` for which X1 finds no eligible account, or whose selected account fails X2 reaffirmation, is rejected before upstream; it MUST NOT be silently served as non-streaming (that would be a capability lie, parent #1 story 22).
- A request for `chat_streaming` on a **synthetic-only** mode (Gemini Web Cookie, #10 §4.3) is governed by §4.4.

---

## 4. Canonical response and streaming event ordering (AC1)

### 4.1 Provider-independence principle

The client contract is **owned by PixelPlus**, not a passthrough of upstream (parent #1 locked decisions). Both response modes MUST present canonical shape and terminal semantics; Provider-specific end markers (OpenAI `[DONE]`, Gemini chunk boundaries, Grok event names) are **normalized away** before the client sees them.

### 4.2 Non-streaming canonical response

1. A successful non-streaming execution delivers **one** response carrying the assistant message content and a **`finish_class`** terminal classification.
2. `finish_class` canonical values (logical; exact strings #16/#18): `stop` (natural completion), `length` (max tokens/length bound reached), `content_filter` (upstream refused/filtered), `canceled` (terminated before completion), `failed` (runtime error). Every non-streaming success carries exactly one.
3. A non-streaming execution that fails in X4 returns a canonical **error** (not a partial success body) with a terminal-outcome class and retryability signal (§7.4, #16).
4. Safe execution metadata (which account/model served the request, request id) MUST be available per parent #1 story 26/27 without leaking credential material (#9 §9) or foreign-Tenant existence (#6).

### 4.3 Streaming canonical event ordering (normative)

A streaming execution MUST emit events in this order:

| Order | Event | Rule |
|---|---|---|
| 1 | **`open`** (once) | Signals stream start; MAY carry safe metadata (request id, resolved model). Exactly one, first. |
| 2..n | **`delta`** (zero or more) | Incremental content. Order is preserved; concatenation of deltas reconstructs the message. |
| interleaved | **`heartbeat`** (zero or more) | Keepalive with no content; MUST NOT carry assistant tokens; MAY appear between deltas to keep the transport open (interval is #17). |
| final | **exactly one terminal event** | One of `completed` / `canceled` / `failed`. Carries the terminal `finish_class`. |

Hard rules:

1. **Exactly one terminal event** per stream. After it, the Gateway MUST NOT emit any further `delta`, `heartbeat`, or a second terminal event.
2. **No content after terminal.** A `delta` MUST NOT follow the terminal event.
3. **Terminal always delivered when the transport is alive.** If execution ends for any reason while the client is still connected, the client receives a terminal event (not a silent hang) — `completed` on success, `canceled` on cancellation, `failed` on runtime error or timeout (§6.4 maps timeout onto `failed` with a timeout remediation class).
4. **Ordering is monotonic.** Deltas are delivered in generation order; the Gateway MUST NOT reorder or deduplicate-merge in a way that corrupts reconstruction.
5. **Partial-before-failure is explicit.** If deltas were emitted and then execution fails, the terminal event is `failed` and the client can tell the message is incomplete (the terminal class, not absence of `[DONE]`, is authoritative).

### 4.4 Synthetic streaming honesty

- On a **synthetic-only** streaming mode (Gemini Web Cookie, #10 §3.1), the Adapter buffers the full upstream body and chunks it into canonical `delta` events. The event **ordering and terminal semantics (§4.3) are identical**, so a client renders it the same way.
- The Gateway MUST NOT promise **token-level latency** it cannot deliver: capability discovery (#10) marks streaming synthetic, and #11 already forbids falling back a real-streaming requirement onto a synthetic mode without client acceptance (#11 §6.3 rule 4). Chat execution MUST honor that: it does not relabel synthetic as real.
- A synthetic stream still emits `open` → `delta`* → terminal, so AC1 canonical ordering holds regardless of real vs synthetic.

### 4.5 Terminal outcome ↔ finish class mapping

| Terminal outcome | Streaming terminal event | Non-streaming `finish_class` | Concurrency occupancy | Token quota |
|---|---|---|---|---|
| Natural completion | `completed` | `stop` / `length` / `content_filter` | released at X6 (normally X5) | reconcile actual (§6.5) |
| Client/system cancel | `canceled` | `canceled` | release if upstream stopped; otherwise retain Tenant+key occupancy through residual tracking to X6 (§6.5) | final at accounting terminal |
| Runtime failure | `failed` | `failed` | release if no upstream survives; otherwise retain Tenant+key occupancy to X6 | final at accounting terminal |
| Timeout | `failed` (+ timeout remediation, §6.4) | `failed` (+ timeout remediation) | release if upstream stopped; otherwise retain Tenant+key occupancy to X6 (§6.5) | final at accounting terminal |

---

## 5. Conversation affinity and account lease (AC3)

### 5.1 Consuming #11 precedence

Chat account resolution (X1) uses the #11 precedence ladder verbatim: **P0 candidate gate → P1 explicit selection → P2 lease → P3 affinity → P4 policy → P5 fallback** (#11 §4.1). This document does not re-derive it; it states how chat **populates** affinity and leases.

### 5.2 Conversation affinity (soft, P3)

1. A multi-turn conversation MAY set an **affinity key** (conversation id / session id; #11 §5.1) so related turns prefer the **same** account that last served the conversation, within the `AFFINITY-WINDOW-CLASS` (#11 §5.1, numeric #17).
2. Affinity is a **soft preference** (#11 §5.1 rule 1): if the preferred account left the candidate set (non-usable per #9, capability-unsatisfied per #10, or health-blocked), the turn falls through to P4 policy — never to a foreign account (#6) and never across Auth Modes (#11 §5.1 rule 2).
3. Affinity satisfies parent #1 story 34 ("multi-turn behavior không ngẫu nhiên chuyển account"): reuse is preferred but never at the cost of usability/capability.

### 5.3 Streaming session lease (hard, P2)

1. A **streaming session** MAY acquire an **account lease** (#11 §5.2): the lease binds the whole stream to **exactly one** account for its duration so the Gateway does not hop accounts mid-stream (#11 §5.2 rule 1 explicitly names a chat stream).
2. The lease is acquired from the candidate set under P1–P4 at stream start; it cannot be acquired on a non-usable/foreign account (#11 §5.2 rule 2).
3. If a durable #9 §5.1 items 1–5 gate fails for the leased account **mid-stream**, the lease is **void for new work** immediately (#11 §5.4) and **no new step/turn** admits on the voided lease; a new turn re-resolves via §5.1. The fate of the **in-flight** stream depends on the failure kind:
   - **Soft/administrative loss** (disable, reauth_required, hard-block health): the credential material is still physically usable, so an in-flight stream that cannot be aborted upstream MAY finish on the old account (#11 §5.4, #8 §4.5 residual).
   - **Hard credential/account loss** (delete, revoke, vault-revoke): the credential or account no longer exists or is inaccessible, so the stream cannot physically continue — it MUST terminate `failed` immediately (a cancel attempt SHOULD still run where cancelable; §6.6).
4. A lease does **not** create extra concurrency budget (#11 §5.2 rule 5, #8 §7.4): the stream still holds exactly one chat concurrency slot.

### 5.4 Fallback during chat

- Fallback (P5) is **opt-in, fail-closed** (#11 §6.5, `I-ROUTE-FALLBACK-OPTIN`): a chat request only fails over to a second account when Tenant policy declares an ordered chain, the target is same-Tenant, the Auth Mode is policy-permitted, and capability matches `op`+`m` (#11 §6). This satisfies parent #1 story 35 (no surprise account/Auth-Mode switch).
- **Fallback shares the proof-of-non-commit boundary in §7.2.** It may select another account only when the Gateway has authoritative evidence that the prior account did not accept a generation. A Provider `rate_limited`/`quota_exhausted` response is fallback-safe only when that Adapter's documented response semantics prove non-commit; an HTTP status alone is insufficient.
- **Mid-stream fallback is not silent re-emission.** Once an attempt is possibly committed — whether or not a `delta` reached the client — the Gateway MUST NOT restart generation on a fallback account. The stream terminates `failed`; any later attempt is a new client decision governed by §7.3.

---

## 6. Cancellation, disconnect, and timeout (AC2)

### 6.1 Principle: every non-natural termination has a defined execution + quota outcome

For each way a chat execution can end early, this section states **(a)** whether upstream execution continues or is aborted, and **(b)** the effect on concurrency and token quota. This satisfies parent #1 stories 31–32 and AC2.

### 6.2 Client-initiated cancellation

1. A client MAY cancel an in-flight chat (explicit cancel request, or by closing a stream it opened; §6.3 covers pure transport disconnect).
2. On cancel, the Gateway **MUST attempt to abort** the upstream execution when the Auth Mode/Adapter supports abort (cancelable). This mirrors #8 `I-CANCEL-ON-REVOKE` and parent #1 story 31 ("ngừng tiêu quota khi người dùng dừng").
3. **Cancelable upstream:** abort is attempted; the terminal outcome is `canceled`; the concurrency occupancy releases **only when cancel completion confirms upstream stopped** (#8 §7.4); token quota is reconciled to **actual tokens consumed so far** (§6.5).
4. **Non-cancelable upstream** (an already-committed generation the Provider will not abort): the Gateway stops client delivery and emits terminal `canceled`, but the upstream MAY run to its natural end. The Gateway MUST NOT claim it was aborted. It MUST drain the upstream under the bounded residual protocol (§6.5): retain the token reservation and the original Tenant+`client_api_key_id` concurrency occupancy, move bookkeeping to a same-Tenant residual hold only if residual tracking capacity is atomically available, and reconcile at the later accounting terminal. If transfer cannot acquire tracking capacity, the original request state remains held until drain completes; neither path frees capacity for another A6 accept.
5. Cancel is **idempotent**: a second cancel on an already-terminal execution is a success no-op, not an error.

### 6.3 Client disconnect

1. A pure transport disconnect (client closes the connection without an explicit cancel) is treated as an **implicit cancel** of that execution: the Gateway detects the closed transport and follows §6.2, including the same abort attempt and stopped-vs-residual tracking disposition while Tenant+key occupancy remains held for surviving upstream work.
2. Disconnect detection and the cancel attempt are bounded by timeout classes (#17), but a surviving upstream remains capacity- and quota-accounted under §6.5 until X6; a cleanup timeout MUST NOT turn it into untracked work.
3. Disconnect of a **non-streaming** request behaves the same: if the client is gone before the single response is delivered, the Gateway attempts abort and applies §6.5; the response is simply undeliverable.
4. Disconnect does **not** by itself invalidate the account, lease, or affinity for other requests (#11 §5.4 request-time gates only); it terminates this one execution.

### 6.4 Timeout

1. The Gateway enforces named **timeout classes** (numeric #17) on chat execution — at minimum a first-token/response-start budget and an overall-execution budget. Exact names/values are #17; this document locks that timeouts are **bounded and observable**.
2. On timeout, the Gateway **MUST attempt to abort** the upstream (like cancel) and deliver a terminal outcome: streaming → terminal `failed` carrying a **timeout remediation class** (distinct from a generic failure so clients can back off/retry per §7); non-streaming → canonical error with the timeout class.
3. Timeout applies the same tracking and accounting rules as cancel: release occupancy immediately only when upstream stopped; otherwise move to bounded residual tracking or retain the original request state while keeping Tenant+key occupancy until accounting terminal (§6.5).
4. A timeout is a **runtime/execution** outcome (post-A6), not an admission `rate_limit`/`quota_exhausted` (#8 §9); it MUST be classifiable as such (#16).

### 6.5 Quota and concurrency reconciliation (normative)

Consumes #8 §7.4/§7.5:

1. **Two terminal moments:** X5 is client-facing; X6 is accounting-facing. They coincide when upstream completed or abort was confirmed. A non-cancelable residual may pass X5 while remaining accounting-active until X6; X6 emits no client event.
2. **State transfer, not capacity release, at X5:** when upstream stopped, release the A6 Tenant and originating-`client_api_key_id` chat occupancy. When upstream continues, both occupancies remain held until X6. The Gateway MAY atomically move bookkeeping to a same-Tenant residual state bounded by `L-TENANT-CHAT-RESIDUAL`; that limit bounds how many occupied executions use residual tracking, not extra execution capacity. If residual tracking is full, retain the original request state. No path releases first or admits replacement work while the prior upstream survives.
3. **Quota reservation remains held** for residual work. Reconcile the A6 reservation at X6 to final actual input+output usage, including tokens consumed after X5. Never over-refund at client terminal. If final usage cannot be obtained after bounded drain/recovery, fail closed for anti-abuse accounting: retain the full reservation (or a platform-configured conservative debit no smaller than known usage) and emit an operator-visible accounting fault; never assume zero.
4. A drain/recovery deadline (#17) bounds resource cleanup, but reaching it does not authorize an optimistic quota refund. At X6 or bounded recovery settlement, release the original Tenant+key concurrency occupancy and residual tracking state exactly once.
5. Reconciliation and residual tracking are same-Tenant and remain charged to the originating `client_api_key_id` (#6 `I-QUOTA-SCOPE`, #8 §7.1/§7.4). Tenant A never consumes Tenant B's counters, and one key cannot shed occupancy onto another key by disconnecting.
6. **Streaming counts as one request** at admission (#8 §7.3), not per event; reconciliation adjusts tokens, not request count.

### 6.6 Cancel-on-revoke and account loss

- If the admitting **Client API Key is revoked** mid-execution, #8 `I-CANCEL-ON-REVOKE` applies: the Gateway MUST attempt cancel of the cancelable in-flight chat and release the slot on cancel completion (§6.2). No new admission is minted for the revoked material (#8 §4.5).
- If the **serving account** becomes non-usable mid-execution (#9 durable gate), §5.3 rule 3 applies: lease void for new work; the in-flight stream's fate depends on the failure kind — soft/administrative loss MAY finish on the old account, hard credential/account loss (delete/revoke/vault-revoke) MUST terminate `failed`; a cancel attempt SHOULD still run where cancelable.

---

## 7. Chat retry boundary and idempotency (AC4)

### 7.1 The problem

Chat generation is **non-idempotent** at the Provider: re-sending the same prompt produces a new generation and consumes new quota. Multiple retry layers (client SDK, HTTP transport, Gateway execution, Adapter, #11 fallback) could each re-run the same execution and cause **duplicate billing / duplicate side effects** (parent #1 story 33, #1 required decision "retry ownership … không cho phép nhiều lớp retry cùng lặp một operation không-idempotent").

### 7.2 Single retry boundary (normative)

1. **Exactly one layer owns chat re-attempts: the Gateway execution layer.** The Adapter MUST NOT independently re-run a full chat generation on its own timer, and the HTTP transport layer MUST NOT auto-retry a chat `POST` that may have already reached upstream. Full canonical retry-ownership across all operations is #16; this document locks the chat rule.
2. A re-attempt is permitted only when the Gateway has **authoritative proof of non-commit**: no request payload bytes were transmitted, or an Adapter-classified Provider response explicitly guarantees that no generation was accepted/created. DNS/TCP/TLS failures before payload are safe examples. An HTTP status, missing response, timeout, reset, or absence of client-visible deltas is not proof by itself.
3. Once payload transmission begins without such proof, the attempt is **possibly committed** and MUST NOT be automatically retried. The client receives `failed`; any later attempt is a new client-initiated request subject to §7.3.
4. **Routing fallback (#11 P5) is a re-attempt under the same rule.** A transient `rate_limited`/`quota_exhausted` primary may fall back only if Adapter semantics prove non-commit for that response. Otherwise fail closed. Retry and fallback share one bounded chain walked once; neither can bypass the other by changing layers or accounts.

### 7.3 Idempotency for accepted requests

1. Chat requests MAY carry an **idempotency key**; the record is scoped `(tenant_id, client_api_key_id-or-scope, idempotency_key)` (#6 §3 Idempotency Record). HTTP header shape is #20.
2. Before any X4 Adapter call, the Gateway MUST atomically claim that scope+key with the request fingerprint. The claim has at least `in_progress` and `terminal` states; exactly one claimant may own upstream execution.
3. A concurrent or later request with a matching fingerprint MUST NOT call upstream. If the claim is `in_progress`, it waits within a bounded class or receives a safe in-progress status; if `terminal`, it returns the prior outcome or safe status. A different fingerprint on the same scoped key returns a canonical idempotency-conflict class (#16/#20), never reuses or overwrites the first execution.
4. A request that loses the atomic claim releases its own A6 slot/reservation exactly once. Claim recovery after owner crash MUST preserve at-most-one committed generation: an uncertain/possibly-committed claim is not stolen for automatic re-execution.
5. A replay by a **different Tenant** MUST NOT read the first Tenant's record or result (#6 §5.2 A7); it is treated as that Tenant's own key space.
6. Idempotency-record TTL/expiry is #16/#20; within its life, sequential and concurrent duplicates cannot launch another generation.

### 7.4 Retryability signaling (client-facing)

Terminal failures carry a **retryability class** so a client retries correctly (parent #1 story 36; exact strings #16):

| Failure kind | Example source | Client guidance |
|---|---|---|
| Proven non-commit transient | X1–X3 or X4 with authoritative no-commit proof | Safe for Gateway retry/fallback |
| Rate/quota (admission) | #8 A3/A5 | Back off; not a Provider error (#8 §9.3) |
| Provider rate / cooldown | #9 §6 `rate_limited`/`quota_exhausted` | MAY fall back only with authoritative no-commit proof; otherwise wait/fail closed |
| Auth expiry / challenge | #9 hard-block health | `reauthenticate`; not a blind retry |
| Timeout | §6.4 | Retry as a **new** request; do not assume prior committed or not — idempotency key recommended |
| Possibly-committed runtime failure | X4 after start / partial deltas | **Not auto-retried**; client decides; idempotency key prevents duplicate if it does |

### 7.5 No duplicate-execution invariant

- The combination of §7.2 (one proof-of-non-commit boundary for retry and fallback) and §7.3 (atomic idempotency claim) guarantees: **one accepted/idempotently identified chat request cannot cause more than one committed upstream generation**, including under concurrent duplicates. A deliberate new request without the same idempotency key is a new execution.

---

## 8. Ownership, confused deputy, and non-enumeration in chat

All chat paths obey #6:

1. **Account resolution ⊆ Tenant accounts** (#11 C0): a chat request from Tenant A never routes, affines, or leases a Tenant-B account; a foreign `provider_account_id` in explicit selection → **404-class**, zero Adapter call, zero decrypt (#6 §5.2 A1, #11 §3.2).
2. **Credential decrypt is gated by the resolved `(tenant_id, provider_account_id)`** (#6 §5.3 rule 3); a stream lease does not authorize decrypt of any other account.
3. **No ambient authority** (#6 §5.3 rule 5): a concurrent Tenant-B stream's decrypted credential in memory is never usable for Tenant A's chat.
4. **Safe execution metadata** (which account/model served, request id) is disclosed only to the owning Tenant and never includes credential material (#9 §9) or foreign existence (#6 §5.1).
5. **Workers/system paths** driving a stream act only under the resource's `tenant_id` (#6 §2.4).

---

## 9. Security impact summary

| Defect | Impact |
|---|---|
| Leak Provider-specific terminal framing into the contract | Client couples to upstream; breaks Provider-independence (parent #1 story 29/30) |
| Emit content after terminal / two terminal events | Corrupt client rendering; ambiguous completion state |
| Silent account hop mid-stream | Continuity break; possible cross-mode capability mismatch (#11 lease) |
| No cancel on disconnect | Orphaned upstream generation bills quota forever; pinned concurrency slot (DoS on own Tenant budget) |
| Release slot/reservation while non-cancelable residual continues unbounded | Cancel amplification; quota bypass; background-work DoS |
| Over-refund cancel that still cost upstream tokens | Quota accounting bypass / cost abuse |
| Auto-retry a possibly-committed generation | Duplicate billing; duplicate side effects (parent #1 story 33) |
| Non-atomic idempotency claim/replay | Concurrent duplicate non-idempotent execution |
| Serve synthetic streaming as real | Latency/UX promises the mode cannot keep (#10 §3.1) |
| Cross-Tenant idempotency replay | Leak prior Tenant's chat result (#6 A7) |
| Cross-Tenant account in routing/affinity/lease | Confused deputy; foreign quota/credential abuse (#6, #11) |

---

## 10. Test obligations

Exact harness arrives with contract prototypes (#18–#20). Required observable cases for this issue:

### 10.1 Canonical response and ordering (AC1)

1. A non-streaming success returns one canonical response with exactly one `finish_class`; no Provider-specific end marker leaks.
2. A streaming success emits `open` → `delta`* → exactly one `completed`; concatenated deltas reconstruct the message; no content after terminal.
3. A streaming runtime failure after some deltas emits terminal `failed`; the client can tell the message is incomplete without a Provider marker.
4. Synthetic-streaming mode (Gemini Web Cookie) emits the same `open`→`delta`*→terminal ordering; capability marks it synthetic; no token-latency promise.
5. X1 excludes non-offerable accounts and X2 reaffirms the selected snapshot; unsupported/unverified streaming is rejected before upstream and never silently downgraded; Adapter executions = 0.

### 10.2 Cancellation, disconnect, timeout (AC2)

6. Client cancel of a cancelable execution → terminal `canceled`; upstream abort attempted; concurrency occupancy released only when cancel completion confirms upstream stopped; token quota reconciled to actual.
7. Cancel of a non-cancelable execution → one client terminal, optional atomic move to bounded residual tracking (or original request state retained), with the original Tenant+key occupancy and reservation still held until accounting terminal; X6 releases each exactly once.
8. Whether residual tracking has capacity or is exhausted, cancel/disconnect/timeout cannot free Tenant/key concurrency while upstream survives; a new A6 request is rejected when those retained occupancies keep a limit full.
9. Client disconnect mid-stream → implicit cancel with the same stopped-vs-residual accounting; no untracked work or indefinite unbounded drain.
10. Timeout → terminal `failed` with a distinct timeout remediation class and the same residual protocol when abort is unconfirmed.
11. Missing final Provider usage settles conservatively and emits an accounting fault; it never refunds as zero.
12. Cancel is idempotent; a second cancel on a client-terminal execution emits no second terminal and creates/releases no second hold.
13. Cancel/disconnect/timeout on Tenant A does not change Tenant B counters or residual capacity; the surviving execution remains charged to Tenant A and its originating key until X6.

### 10.3 Affinity and lease (AC3)

14. A multi-turn conversation prefers the prior account (affinity) within the window; when that account leaves the candidate set, the next turn falls through to policy, never to a foreign or cross-mode account.
15. A streaming session holds a lease pinning one account for the stream duration; the Gateway does not hop accounts mid-stream.
16. A durable #9 items 1–5 failure on the leased account voids the lease for new turns immediately; a non-cancelable in-flight stream MAY finish; a new turn re-resolves.
17. Fallback occurs only with an opt-in policy chain, same-Tenant, permitted Auth Mode, capability-matched, and proof of non-commit for any attempted primary; otherwise fail closed. Mid-stream or possibly-committed content is never re-emitted on another account.

### 10.4 Retry boundary and idempotency (AC4)

18. Gateway retry and #11 fallback both require authoritative proof of non-commit; payload transmission without such proof is possibly committed even if no delta was observed.
19. A Provider `rate_limited` response permits fallback only when Adapter semantics prove no generation was accepted; status code alone is insufficient.
20. Only the Gateway execution layer re-attempts chat; Adapter/transport retry of a possibly-committed generation is a conformance fail.
21. Two concurrent requests with the same scoped idempotency key and fingerprint yield one atomic claimant and exactly one upstream generation; the loser waits/gets in-progress and releases its own A6 resources.
22. Same key with a different fingerprint returns idempotency conflict and never overwrites/joins the first request.
23. Owner crash with an uncertain claim does not permit claim stealing into a second automatic execution.
24. A terminal replay returns the prior outcome with zero additional generations; cross-Tenant replay cannot read it (#6 A7).
25. A single accepted/idempotently identified request never produces more than one committed upstream generation.

### 10.5 Ownership and scope

26. Foreign `provider_account_id` in explicit selection → 404-class; zero Adapter call; zero decrypt.
27. Chat requires an inference scope (`chat.completions`, #8 §5.2); a key lacking it → 403-class before upstream.
28. Safe execution metadata never includes credential material or foreign-Tenant existence.

---

## 11. Core invariants (normative checklist)

1. **I-CHAT-CANON-TERMINAL** — Every chat execution reaches exactly one client-terminal outcome (`completed`/`canceled`/`failed`, timeout→`failed`) and one accounting terminal; non-streaming carries one `finish_class`, streaming emits exactly one terminal event with no content after it, and accounting emits no second client event (AC1).
2. **I-CHAT-STREAM-ORDER** — Streaming events are `open` (once) → ordered `delta`* (+ `heartbeat`) → exactly one terminal event; no reordering that breaks reconstruction; no second terminal event.
3. **I-CHAT-PROVIDER-INDEPENDENT** — Canonical response/stream shape and terminal semantics never leak Provider-specific framing/end markers (parent #1 story 29/30).
4. **I-CHAT-STREAM-CLASS-HONEST** — Real vs synthetic streaming (#10 §3.1) is honored; a `chat_streaming` request is never silently downgraded to non-streaming, and synthetic is never advertised as real token latency.
5. **I-CHAT-CAP-BEFORE-UPSTREAM** — X1 constructs capability-filtered candidates and selects an account; X2 reaffirms that selected account before Adapter execution and vault decrypt. Unsupported/unverified/stale/model-unavailable fails closed.
6. **I-CHAT-LEASE** — A streaming session binds to exactly one same-Tenant account via a #11 lease for its duration; the Gateway does not hop accounts mid-stream; the lease voids for new work on durable #9 items 1–5 failure (#11 §5.4).
7. **I-CHAT-AFFINITY** — Conversation affinity is a soft #11 preference that yields when its account leaves the candidate set; it never crosses Tenants or Auth Modes.
8. **I-CHAT-CANCEL** — Cancellation/disconnect attempts abort and emits one client terminal; if upstream survives, slot disposition and accounting follow the bounded residual protocol; cancel is idempotent.
9. **I-CHAT-RESIDUAL-BOUNDED** — Non-cancelable work may move atomically to same-Tenant residual tracking only within `L-TENANT-CHAT-RESIDUAL`, but always retains its original Tenant and `client_api_key_id` concurrency occupancy until X6; otherwise the original request state remains held. Residual tracking is not additional execution capacity, and client terminal never mints a replacement slot.
10. **I-CHAT-DISCONNECT-BOUNDED** — Disconnect detection and drain/recovery use bounded classes; cleanup timeout never authorizes an optimistic quota refund.
11. **I-CHAT-TIMEOUT** — Timeouts are observable, attempt abort, terminate client-facing `failed`, and use the same residual/accounting protocol when abort is unconfirmed.
12. **I-CHAT-QUOTA-RECONCILE** — The A6 reservation remains until accounting terminal and reconciles to final actual usage including residual; unavailable final usage settles conservatively, same-Tenant only.
13. **I-CHAT-RETRY-BOUNDARY** — Gateway retry and routing fallback share authoritative proof of non-commit; a possibly-committed attempt is never automatically re-run by any layer/account.
14. **I-CHAT-IDEMPOTENT** — An atomic scoped claim before upstream gives one executor; matching concurrent/sequential duplicates never call upstream, fingerprint mismatch conflicts, and cross-Tenant replay cannot read the result.
15. **I-CHAT-NO-DUPLICATE-EXEC** — A single accepted/idempotently identified chat request MUST NOT cause more than one committed upstream generation absent a deliberate new request.
16. **I-CHAT-OWNERSHIP** — Account resolution/affinity/lease/fallback stay within `principal.tenant_id`; foreign ids are 404-class; decrypt is gated by the resolved account; no ambient authority (#6, #11).

---

## 12. Open follow-ups (explicitly deferred)

| Topic | Issue | Constraint retained here |
|---|---|---|
| Numeric timeout classes, heartbeat interval, retry/backoff budgets, disconnect/drain windows, residual-tracking limit | #17 | Named classes + bounded/observable behavior locked here; #17 tunes tracking numbers, not retained Tenant/key occupancy, atomic state transfer, or conservative settlement |
| Canonical error code strings / problem+json / finish_class strings | #16 | Terminal-outcome, finish, remediation, retryability **classes** locked here |
| SSE field names, JSON event schema, OpenAPI chat paths, HTTP idempotency header | #18 / #20 | Logical events, ordering, idempotency scope locked here |
| Full cross-operation retry ownership (chat + image + job) | #16 | Chat retry boundary + single-owner rule locked here |
| Image / Render Job execution lifecycle | #13 / #14 | Chat (sync/stream text) scope only; durable image jobs elsewhere |
| Idempotency-record TTL / expiry and wire conflict/in-progress shape | #16 / #20 | Atomic claim, fingerprint conflict, uncertain-claim no-steal, and zero duplicate execution locked here |
| Tool calling / multi-modal chat inputs streaming | reopen `D-CHAT-TOOLS` | MVP: canonical text delta ordering; secondary ops `unverified` (#10 §3.2) |
| `stale`-grace or resumable streams after disconnect | reopen `D-CHAT-RESUME` | MVP: disconnect = implicit cancel, no resume |

---

## 13. ADR decision

No new ADR. Provider-independent Public API contract, OpenAI-compatible core, and no-silent-cross behavior were product-locked in parent #1, #6, #7, and #11. This document is the durable normative expansion under `docs/spec/` for chat execution phases, canonical terminal/streaming semantics, cancellation/disconnect/timeout, affinity/lease consumption, and the chat retry boundary.

An ADR **would** be warranted if the product later introduced:

- passthrough of Provider-specific streaming framing to clients (forbidden; Provider-independence),
- resumable streams / at-least-once chat retry as default (deferred `D-CHAT-RESUME`),
- multi-layer chat retry (forbidden; single boundary),
- or serving synthetic streaming as real token latency (forbidden; #10 orthogonality).

---

## 14. Constants and reopen ids

| Id | Meaning |
|---|---|
| `AFFINITY-WINDOW-CLASS` / `LEASE-TTL-CLASS` | Owned by #11 (§5); chat consumes them; numeric #17 |
| Timeout classes (first-token, overall-execution), heartbeat interval, disconnect/drain windows | Named here; numeric #17 |
| `L-TENANT-CHAT-RESIDUAL` | Same-Tenant cap on executions represented in residual tracking; it grants no concurrency beyond retained `L-TENANT-CHAT-CONCURRENCY` and originating-key occupancy; numeric #17 |
| `finish_class` values (`stop`/`length`/`content_filter`/`canceled`/`failed`) | Logical here; strings #16/#18 |
| Client terminal outcomes (`completed`/`canceled`/`failed`/`timed_out`) + accounting terminal | Logical here; strings #16/#18; accounting terminal is not a second wire event |
| `I-CANCEL-ON-REVOKE` | Owned by #8 §4.5; chat cancel consumes it |
| `I-USABLE-GATE` | Owned by #9 §5.1; chat consumes it (X1/X2) |
| Capability status / offerable / streaming class | Owned by #10; chat consumes offerable + streaming class |
| Routing precedence / lease / affinity / fallback | Owned by #11; chat populates and consumes |
| `D-CHAT-TOOLS` | Reopen for tool-calling / multi-modal streaming |
| `D-CHAT-RESUME` | Reopen for resumable streams / retry-after-disconnect |

---

## 15. Acceptance criteria traceability

| AC (issue #12) | Where satisfied |
|---|---|
| Non-streaming response and streaming event ordering have canonical terminal semantics | §4, §10.1, `I-CHAT-CANON-TERMINAL`, `I-CHAT-STREAM-ORDER`, `I-CHAT-PROVIDER-INDEPENDENT`, `I-CHAT-STREAM-CLASS-HONEST` |
| Cancellation, disconnect and timeout state whether execution continues or is canceled and quota impact | §6, §10.2, `I-CHAT-CANCEL`, `I-CHAT-DISCONNECT-BOUNDED`, `I-CHAT-TIMEOUT`, `I-CHAT-QUOTA-RECONCILE` |
| Conversation affinity and account lease maintained per routing policy | §5, §10.3, `I-CHAT-LEASE`, `I-CHAT-AFFINITY` |
| Chat retry boundary described enough to avoid duplicate non-idempotent execution | §7, §10.4, `I-CHAT-RETRY-BOUNDARY`, `I-CHAT-IDEMPOTENT`, `I-CHAT-NO-DUPLICATE-EXEC` |

---

## 16. Document control

| Field | Value |
|---|---|
| Status | Accepted for specification (issue #12) |
| Check date of evidence inputs | 2026-07-14 |
| Supersedes | n/a (initial chat execution / streaming lifecycle lock) |
| Next review | On #10 streaming-class changes, #11 lease/affinity changes, #8 concurrency/quota changes, #16 retry-ownership, or #17 numeric tuning |
| Authors | Spec decision agent for issue #12 |
