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

This specification locks the **canonical chat execution lifecycle** — from an admitted Public API chat request to a terminal outcome — for both **non-streaming** and **streaming** responses, so a client observes **one Provider-independent contract** regardless of which Auth Mode or Provider Account served the request.

It codifies parent #1 user stories 28–36 and consumes the ownership boundary (#6), admission pipeline (#8), usability gate (#9 `I-USABLE-GATE`), capability gate (#10, including the **real-vs-synthetic streaming** distinction), and routing/affinity/lease precedence (#11).

This is **specification work**; it does **not** implement the Gateway, Adapter, or stream transport code.

It covers:

1. The **request-to-terminal lifecycle** phases and where chat sits relative to admission (#8 A6) and routing (#11).
2. **Non-streaming** canonical response shape (logical) and its single terminal outcome.
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
| Concurrency slot lifetime | Chat slot acquired at A6; released on terminal response fully sent, disconnect handled, cancel completed, or cancel-on-revoke completed (#8 §7.4) | The exact terminal events / disconnect / timeout that release the slot |
| Token quota reservation | Reserve at A6, reconcile after completion (#8 §7.5) | Reconcile on every terminal outcome incl. cancel/disconnect/timeout |
| Usability + capability gate | `I-USABLE-GATE` + capability reject-before-upstream (#9 §5.1, #10 §9) | Chat/`chat_streaming` capability checked before Adapter call; synthetic-streaming honesty |
| Routing / lease / affinity | Precedence P0–P5; lease binds a streaming session; affinity is soft (#11 §4–§5) | How a chat turn acquires affinity and a stream acquires a lease |
| Cancel-on-revoke | Revoke MUST attempt cancel of cancelable in-flight chat (#8 §4.5, `I-CANCEL-ON-REVOKE`) | The cancel protocol and its quota effect |

### 1.5 Decision unit

**One chat execution = one admitted Public API chat request (one Security Principal, one requested model, streaming or not) resolved to exactly one Provider Account (#11) and driven to exactly one terminal outcome, with at most one canonical terminal signal to the client.**

Cause → effect:

1. A non-streaming request that succeeds returns **one** canonical response with a terminal `finish` classification; a client never has to parse Provider-specific end markers.
2. A streaming request emits an **ordered** sequence of content events followed by **exactly one** terminal event (`completed` / `canceled` / `failed`); no content follows the terminal event, and no second terminal event is emitted.
3. A client that disconnects mid-stream causes a **defined** outcome (attempt cancel; release slot; reconcile quota) rather than a silently orphaned upstream generation billing forever.

---

## 2. Glossary extensions (normative use)

| Term | Meaning in this document |
|---|---|
| **Chat execution** | The post-admission (#8 A6) processing of one chat request against one routed Provider Account (#11) until a terminal outcome. |
| **Non-streaming response** | A single canonical response body delivered once, carrying the full assistant message and a terminal `finish_class`. |
| **Streaming response** | An ordered sequence of canonical **stream events** delivered incrementally, ending in exactly one terminal event. |
| **Stream event** | A canonical incremental unit (`open`, `delta`, `heartbeat`, terminal). Provider-specific framing is normalized into these before the client sees them (#10 §3.1 governs whether real or synthetic). |
| **Terminal outcome** | The single final classification of a chat execution: `completed`, `canceled`, `failed`, or `timed_out`. This is the **internal** outcome vocabulary for reconciliation/remediation; on the wire it maps onto the three terminal **events** (§4.3) — `timed_out` always emits the `failed` terminal event/finish class with a distinct timeout remediation class (§6.4), never a fourth wire signal. |
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
| **X1 Capability gate** | Resolve op (`chat` or `chat_streaming`) + model `m` against the routed-candidate snapshot; reject before upstream if `unsupported`/`unverified`/not-`fresh`/model-unavailable (#10 §9). Streaming request on a **synthetic-only** mode honored only per §4.4 | #10 (consumed) |
| **X2 Account resolution** | Resolve exactly one usable, capability-satisfied same-Tenant account via #11 precedence (P0–P5); acquire affinity/lease per §5 | #11 (consumed) |
| **X3 Credential decrypt** | Decrypt the resolved account's credential on the same-Tenant authorized path only (#6 §5.3 rule 3) | #6/#15 |
| **X4 Upstream execution** | Adapter calls the Provider; normalize response/stream into canonical shape (§4) | this doc |
| **X5 Terminal** | Deliver exactly one terminal outcome; release concurrency slot; reconcile token quota (§6.5) | this doc + #8 |

Cause → effect:

1. Capability gate (X1) runs **before** account credential decrypt (X3) where it can be known from the routed candidate's snapshot, so an `unsupported`/`stale` op wastes no vault decrypt or Adapter call (#10 §9, `I-CAP-REJECT-BEFORE-UPSTREAM`).
2. Because #11 already filtered the candidate set by capability (C4) and health (C5), X2 resolves an account already known offerable for `op`+`m`; X1 is the request-time reaffirmation on the resolved account (#10 §9.1 item 1).
3. A failure in X1–X3 is **pre-upstream** (no Adapter call, no quota debit beyond the released reservation); a failure in X4 is an **execution/runtime** failure (#8 §9.1) with its own terminal class.

### 3.2 Streaming vs non-streaming branch

- The client selects streaming per the OpenAI-compatible request (schema/field is #18/#20). The **operation token** differs: non-streaming → `chat`; streaming → `chat_streaming` (#10 §3.1). The capability gate (X1) MUST check the operation the client actually requested.
- A request for `chat_streaming` on an account whose snapshot classifies streaming as `unsupported`/`unverified` is rejected before upstream (X1); it MUST NOT be silently served as non-streaming (that would be a capability lie, parent #1 story 22).
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

| Terminal outcome | Streaming terminal event | Non-streaming `finish_class` | Concurrency slot | Token quota |
|---|---|---|---|---|
| Natural completion | `completed` | `stop` / `length` / `content_filter` | released | reconcile actual (§6.5) |
| Client/system cancel | `canceled` | `canceled` | released on cancel completion | reconcile partial (§6.5) |
| Runtime failure | `failed` | `failed` | released | reconcile partial/zero (§6.5) |
| Timeout | `failed` (+ timeout remediation, §6.4) | `failed` (+ timeout remediation) | released | reconcile partial (§6.5) |

---

## 5. Conversation affinity and account lease (AC3)

### 5.1 Consuming #11 precedence

Chat account resolution (X2) uses the #11 precedence ladder verbatim: **P0 candidate gate → P1 explicit selection → P2 lease → P3 affinity → P4 policy → P5 fallback** (#11 §4.1). This document does not re-derive it; it states how chat **populates** affinity and leases.

### 5.2 Conversation affinity (soft, P3)

1. A multi-turn conversation MAY set an **affinity key** (conversation id / session id; #11 §5.1) so related turns prefer the **same** account that last served the conversation, within the `AFFINITY-WINDOW-CLASS` (#11 §5.1, numeric #17).
2. Affinity is a **soft preference** (#11 §5.1 rule 1): if the preferred account left the candidate set (non-usable per #9, capability-unsatisfied per #10, or health-blocked), the turn falls through to P4 policy — never to a foreign account (#6) and never across Auth Modes (#11 §5.1 rule 2).
3. Affinity satisfies parent #1 story 34 ("multi-turn behavior không ngẫu nhiên chuyển account"): reuse is preferred but never at the cost of usability/capability.

### 5.3 Streaming session lease (hard, P2)

1. A **streaming session** MAY acquire an **account lease** (#11 §5.2): the lease binds the whole stream to **exactly one** account for its duration so the Gateway does not hop accounts mid-stream (#11 §5.2 rule 1 explicitly names a chat stream).
2. The lease is acquired from the candidate set under P1–P4 at stream start; it cannot be acquired on a non-usable/foreign account (#11 §5.2 rule 2).
3. If a durable #9 §5.1 items 1–5 gate fails for the leased account **mid-stream** (disable/revoke/delete/reauth_required/hard-block health/vault-revoke), the lease is **void for new work** immediately (#11 §5.4). The in-flight stream that cannot be aborted upstream MAY finish on the old account (#11 §5.4, #8 §4.5 residual), but **no new step/turn** admits on the voided lease; a new turn re-resolves via §5.1.
4. A lease does **not** create extra concurrency budget (#11 §5.2 rule 5, #8 §7.4): the stream still holds exactly one chat concurrency slot.

### 5.4 Fallback during chat

- Fallback (P5) is **opt-in, fail-closed** (#11 §6.5, `I-ROUTE-FALLBACK-OPTIN`): a chat request only fails over to a second account when Tenant policy declares an ordered chain, the target is same-Tenant, the Auth Mode is policy-permitted, and capability matches `op`+`m` (#11 §6). This satisfies parent #1 story 35 (no surprise account/Auth-Mode switch).
- **Mid-stream fallback is not silent re-emission.** Once `delta` content has been sent to the client on a stream, the Gateway MUST NOT transparently restart generation on a fallback account and concatenate a second attempt into the same stream (that would duplicate/garble output). If the leased account fails after content was emitted, the stream terminates `failed`; any retry is governed by the retry boundary (§7) as a **new** execution the client can choose to make.

---

## 6. Cancellation, disconnect, and timeout (AC2)

### 6.1 Principle: every non-natural termination has a defined execution + quota outcome

For each way a chat execution can end early, this section states **(a)** whether upstream execution continues or is aborted, and **(b)** the effect on concurrency and token quota. This satisfies parent #1 stories 31–32 and AC2.

### 6.2 Client-initiated cancellation

1. A client MAY cancel an in-flight chat (explicit cancel request, or by closing a stream it opened; §6.3 covers pure transport disconnect).
2. On cancel, the Gateway **MUST attempt to abort** the upstream execution when the Auth Mode/Adapter supports abort (cancelable). This mirrors #8 `I-CANCEL-ON-REVOKE` and parent #1 story 31 ("ngừng tiêu quota khi người dùng dừng").
3. **Cancelable upstream:** abort is attempted; the terminal outcome is `canceled`; the concurrency slot releases **on cancel completion** (#8 §7.4); token quota is reconciled to **actual tokens consumed so far** (§6.5).
4. **Non-cancelable upstream** (an already-committed generation the Provider will not abort): the Gateway stops streaming to the client and emits terminal `canceled`, but the upstream MAY run to its natural end. The Gateway MUST NOT claim the upstream was aborted when it was not — the honest statement is "client cancel observed; upstream residual may complete" (mirrors #8 §4.5 in-flight residual). Quota reconciles to whatever the upstream actually consumed (§6.5), which MAY exceed tokens streamed to the client.
5. Cancel is **idempotent**: a second cancel on an already-terminal execution is a success no-op, not an error.

### 6.3 Client disconnect

1. A pure transport disconnect (client closes the connection without an explicit cancel) is treated as an **implicit cancel** of that execution: the Gateway detects the closed transport and follows §6.2 (attempt abort if cancelable; release slot; reconcile quota).
2. Disconnect MUST NOT leave a concurrency slot pinned indefinitely: the slot releases when the Gateway observes the disconnect and completes its cancel attempt (bounded by a detection/timeout class, #17).
3. Disconnect of a **non-streaming** request behaves the same: if the client is gone before the single response is delivered, the Gateway attempts abort and reconciles quota; the response is simply undeliverable.
4. Disconnect does **not** by itself invalidate the account, lease, or affinity for other requests (#11 §5.4 request-time gates only); it terminates this one execution.

### 6.4 Timeout

1. The Gateway enforces named **timeout classes** (numeric #17) on chat execution — at minimum a first-token/response-start budget and an overall-execution budget. Exact names/values are #17; this document locks that timeouts are **bounded and observable**.
2. On timeout, the Gateway **MUST attempt to abort** the upstream (like cancel) and deliver a terminal outcome: streaming → terminal `failed` carrying a **timeout remediation class** (distinct from a generic failure so clients can back off/retry per §7); non-streaming → canonical error with the timeout class.
3. Timeout releases the concurrency slot and reconciles token quota to actual consumption (§6.5).
4. A timeout is a **runtime/execution** outcome (post-A6), not an admission `rate_limit`/`quota_exhausted` (#8 §9); it MUST be classifiable as such (#16).

### 6.5 Quota and concurrency reconciliation (normative)

Consumes #8 §7.4/§7.5:

1. **Concurrency slot** (acquired at A6) is released on **every** terminal outcome: `completed`, `canceled`, `failed`, `timed_out`, and on disconnect after the cancel attempt completes. A stuck upstream MUST NOT pin the slot past the abort/detection timeout class (#17).
2. **Token quota** reserved at A6 (`reserve = input_estimate + min(requested_max, L-CHAT-MAX-TOKENS-PER-REQ)`, #8 §7.5) is **reconciled** at terminal to actual usage:
   - Natural completion → reconcile to actual input+output tokens.
   - Cancel / disconnect / timeout / partial failure → reconcile to tokens **actually consumed** (which includes upstream residual for non-cancelable work, §6.2 rule 4). The Gateway MUST NOT silently over-refund a cancel that still cost upstream tokens, and MUST NOT keep the full reservation when far fewer tokens were used.
3. Reconciliation debits only same-Tenant counters (#6 `I-QUOTA-SCOPE`, #8 §7.1). A cancel/disconnect/timeout on Tenant A never affects Tenant B.
4. **Streaming counts as one request** at admission (#8 §7.3), not per event; reconciliation adjusts **tokens**, not the request count.

### 6.6 Cancel-on-revoke and account loss

- If the admitting **Client API Key is revoked** mid-execution, #8 `I-CANCEL-ON-REVOKE` applies: the Gateway MUST attempt cancel of the cancelable in-flight chat and release the slot on cancel completion (§6.2). No new admission is minted for the revoked material (#8 §4.5).
- If the **serving account** becomes non-usable mid-execution (#9 durable gate), §5.3 rule 3 applies: lease void for new work; in-flight non-cancelable stream MAY finish; a cancel attempt SHOULD still run where cancelable.

---

## 7. Chat retry boundary and idempotency (AC4)

### 7.1 The problem

Chat generation is **non-idempotent** at the Provider: re-sending the same prompt produces a new generation and consumes new quota. Multiple retry layers (client SDK, HTTP transport, Gateway execution, Adapter, #11 fallback) could each re-run the same execution and cause **duplicate billing / duplicate side effects** (parent #1 story 33, #1 required decision "retry ownership … không cho phép nhiều lớp retry cùng lặp một operation không-idempotent").

### 7.2 Single retry boundary (normative)

1. **Exactly one layer owns chat re-attempts: the Gateway execution layer.** The Adapter MUST NOT independently re-run a full chat generation on its own timer, and the HTTP transport layer MUST NOT auto-retry a chat `POST` that may have already reached upstream. Full canonical retry-ownership across all operations is #16; this document locks the chat rule.
2. A chat execution re-attempt is permitted **only** when the Gateway can prove the prior attempt did **not** commit an upstream generation — i.e. the failure occurred **before X4 upstream execution began** (pre-upstream: X1 capability, X2 routing, X3 decrypt, or a connection failure before the Provider accepted the request). These are safe to retry because no generation/side effect exists yet.
3. Once **X4 has begun** (the Provider may have started generating, especially once any `delta`/partial output exists), the execution is treated as **possibly-committed** and MUST NOT be automatically retried by the Gateway. The terminal outcome is `failed`; re-attempt is a **new client-initiated request** the client chooses to make, subject to §7.3 idempotency.
4. **Routing fallback (#11 P5) is not a content re-emission.** Fallback selects a different account for a **pre-upstream** or transient-unavailable primary (#11 §6.4); it does not re-run a generation that already produced client-visible content (§5.4). Fallback and retry are bounded and ordered (#11 §6.6): the chain is walked once, terminating in a fail-closed rejection.

### 7.3 Idempotency for accepted requests

1. Chat requests MAY carry an **idempotency key**; the record is scoped `(tenant_id, client_api_key_id-or-scope, idempotency_key)` (#6 §3 Idempotency Record). HTTP header shape is #20.
2. A **replay** of the same idempotency key by the same-Tenant principal with a **matching request fingerprint** MUST return the **prior outcome** (or its safe status), not launch a second upstream generation. This is the concrete defense against duplicate non-idempotent execution.
3. A replay by a **different Tenant** MUST NOT read the first Tenant's record or result (#6 §5.2 A7); it is treated as that Tenant's own key space.
4. Idempotency-record TTL/expiry is #16/#20; this document requires only that within the record's life, a matching replay does not duplicate execution.

### 7.4 Retryability signaling (client-facing)

Terminal failures carry a **retryability class** so a client retries correctly (parent #1 story 36; exact strings #16):

| Failure kind | Example source | Client guidance |
|---|---|---|
| Pre-upstream transient | X2/X3 transient, connect-before-upstream | Safe to retry (no generation committed) |
| Rate/quota (admission) | #8 A3/A5 | Back off; not a Provider error (#8 §9.3) |
| Provider rate / cooldown | #9 §6 `rate_limited`/`quota_exhausted` | Wait for reset hint; MAY fall back if policy permits (#11 §6.4) |
| Auth expiry / challenge | #9 hard-block health | `reauthenticate`; not a blind retry |
| Timeout | §6.4 | Retry as a **new** request; do not assume prior committed or not — idempotency key recommended |
| Possibly-committed runtime failure | X4 after start / partial deltas | **Not auto-retried**; client decides; idempotency key prevents duplicate if it does |

### 7.5 No duplicate-execution invariant

- The combination of §7.2 (single boundary, no retry once possibly-committed) and §7.3 (idempotency replay returns prior outcome) guarantees: **a single accepted chat request MUST NOT cause more than one committed upstream generation** unless the client explicitly issues a new request without an idempotency key. This is the AC4 guarantee.

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
| Over-refund cancel that still cost upstream tokens | Quota accounting bypass / cost abuse |
| Auto-retry a possibly-committed generation | Duplicate billing; duplicate side effects (parent #1 story 33) |
| Ignore idempotency replay | Duplicate non-idempotent execution |
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
5. `chat_streaming` on an `unsupported`/`unverified`-streaming account is rejected before upstream (X1); it is **not** silently served as non-streaming; Adapter executions = 0.

### 10.2 Cancellation, disconnect, timeout (AC2)

6. Client cancel of a cancelable execution → terminal `canceled`; upstream abort attempted; concurrency slot released on cancel completion; token quota reconciled to actual.
7. Cancel of a non-cancelable execution → terminal `canceled` to client; upstream residual honestly accounted; quota reconciled to real upstream consumption (may exceed streamed tokens).
8. Client disconnect mid-stream → implicit cancel; slot released within detection/timeout class; quota reconciled; no indefinite slot pin.
9. Timeout → terminal `failed` with a distinct timeout remediation class (not admission `rate_limit`/`quota_exhausted`); slot released; quota reconciled.
10. Cancel is idempotent; a second cancel on a terminal execution is a success no-op.
11. Cancel/disconnect/timeout on Tenant A does not change Tenant B counters.

### 10.3 Affinity and lease (AC3)

12. A multi-turn conversation prefers the prior account (affinity) within the window; when that account leaves the candidate set, the next turn falls through to policy, never to a foreign or cross-mode account.
13. A streaming session holds a lease pinning one account for the stream duration; the Gateway does not hop accounts mid-stream.
14. A durable #9 items 1–5 failure on the leased account voids the lease for new turns immediately; a non-cancelable in-flight stream MAY finish; a new turn re-resolves.
15. Fallback occurs only with an opt-in policy chain, same-Tenant, permitted Auth Mode, capability-matched; otherwise fail closed (#11 NF-*). Mid-stream content is never transparently re-emitted on a fallback account.

### 10.4 Retry boundary and idempotency (AC4)

16. A pre-upstream failure (X1–X3 / connect-before-upstream) MAY be retried by the Gateway execution layer; a possibly-committed failure (X4 started / partial deltas) is **not** auto-retried.
17. Only the Gateway execution layer re-attempts chat; Adapter/transport auto-retry of a possibly-committed generation is a conformance fail.
18. A replay of the same idempotency key (same Tenant, matching fingerprint) returns the prior outcome and launches **zero** additional upstream generations.
19. A cross-Tenant replay of an idempotency key does not read the first Tenant's result (#6 A7).
20. A single accepted request never produces more than one committed upstream generation absent an explicit new client request.

### 10.5 Ownership and scope

21. Foreign `provider_account_id` in explicit selection → 404-class; zero Adapter call; zero decrypt.
22. Chat requires an inference scope (`chat.completions`, #8 §5.2); a key lacking it → 403-class before upstream.
23. Safe execution metadata never includes credential material or foreign-Tenant existence.

---

## 11. Core invariants (normative checklist)

1. **I-CHAT-CANON-TERMINAL** — Every chat execution reaches exactly one terminal outcome (`completed`/`canceled`/`failed`, timeout→`failed`); non-streaming carries one `finish_class`, streaming emits exactly one terminal event with no content after it (AC1).
2. **I-CHAT-STREAM-ORDER** — Streaming events are `open` (once) → ordered `delta`* (+ `heartbeat`) → exactly one terminal event; no reordering that breaks reconstruction; no second terminal event.
3. **I-CHAT-PROVIDER-INDEPENDENT** — Canonical response/stream shape and terminal semantics never leak Provider-specific framing/end markers (parent #1 story 29/30).
4. **I-CHAT-STREAM-CLASS-HONEST** — Real vs synthetic streaming (#10 §3.1) is honored; a `chat_streaming` request is never silently downgraded to non-streaming, and synthetic is never advertised as real token latency.
5. **I-CHAT-CAP-BEFORE-UPSTREAM** — `chat`/`chat_streaming`+model is capability-checked on the resolved account before Adapter execution and vault decrypt (#10 §9); unsupported/unverified/stale/model-unavailable fails closed pre-upstream.
6. **I-CHAT-LEASE** — A streaming session binds to exactly one same-Tenant account via a #11 lease for its duration; the Gateway does not hop accounts mid-stream; the lease voids for new work on durable #9 items 1–5 failure (#11 §5.4).
7. **I-CHAT-AFFINITY** — Conversation affinity is a soft #11 preference that yields when its account leaves the candidate set; it never crosses Tenants or Auth Modes.
8. **I-CHAT-CANCEL** — Cancellation and client disconnect attempt upstream abort where cancelable, deliver terminal `canceled`, release the concurrency slot on cancel completion, and reconcile token quota to actual consumption (AC2); cancel is idempotent.
9. **I-CHAT-DISCONNECT-BOUNDED** — A disconnect never pins a concurrency slot indefinitely; the slot releases within a bounded detection/timeout class after the cancel attempt.
10. **I-CHAT-TIMEOUT** — Timeouts are bounded, observable, attempt upstream abort, terminate `failed` with a distinct timeout remediation class, release the slot, and reconcile quota; a timeout is a runtime outcome, not an admission reject (AC2).
11. **I-CHAT-QUOTA-RECONCILE** — The A6 token reservation is reconciled to actual consumption on every terminal outcome (incl. cancel/disconnect/timeout, including non-cancelable upstream residual); reconciliation is same-Tenant only (#6/#8).
12. **I-CHAT-RETRY-BOUNDARY** — Exactly one layer (Gateway execution) may re-attempt chat, and only when the prior attempt was pre-upstream; a possibly-committed execution is not auto-retried; Adapter/transport auto-retry of a committed generation is forbidden (AC4).
13. **I-CHAT-IDEMPOTENT** — A matching same-Tenant idempotency-key replay returns the prior outcome and launches zero additional generations; cross-Tenant replay cannot read the prior result (#6 A7).
14. **I-CHAT-NO-DUPLICATE-EXEC** — A single accepted chat request MUST NOT cause more than one committed upstream generation absent an explicit new client request (AC4).
15. **I-CHAT-OWNERSHIP** — Account resolution/affinity/lease/fallback stay within `principal.tenant_id`; foreign ids are 404-class; decrypt is gated by the resolved account; no ambient authority (#6, #11).

---

## 12. Open follow-ups (explicitly deferred)

| Topic | Issue | Constraint retained here |
|---|---|---|
| Numeric timeout classes, heartbeat interval, retry/backoff budgets, disconnect-detection window | #17 | Named classes + bounded/observable behavior locked here; #17 tunes numbers, not the fail-closed/terminal rules |
| Canonical error code strings / problem+json / finish_class strings | #16 | Terminal-outcome, finish, remediation, retryability **classes** locked here |
| SSE field names, JSON event schema, OpenAPI chat paths, HTTP idempotency header | #18 / #20 | Logical events, ordering, idempotency scope locked here |
| Full cross-operation retry ownership (chat + image + job) | #16 | Chat retry boundary + single-owner rule locked here |
| Image / Render Job execution lifecycle | #13 / #14 | Chat (sync/stream text) scope only; durable image jobs elsewhere |
| Idempotency-record TTL / expiry | #16 / #20 | Replay-returns-prior-outcome within record life locked here |
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
| Timeout classes (first-token, overall-execution), heartbeat interval, disconnect-detection window | Named here; numeric #17 |
| `finish_class` values (`stop`/`length`/`content_filter`/`canceled`/`failed`) | Logical here; strings #16/#18 |
| Terminal outcomes (`completed`/`canceled`/`failed`/`timed_out`) | Logical here; strings #16/#18 |
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
