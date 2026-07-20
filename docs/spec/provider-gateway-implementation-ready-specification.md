# Provider Gateway Implementation-Ready Specification

Status: implementation ready

Gate issue: #22

Deferred implementation issue: #42

Machine-readable index:
`docs/spec/provider-gateway-implementation-ready-manifest.json`

## 1. Purpose

This document is the implementation handoff for the PixelPlus Pure-Go Provider
Gateway. It assembles the accepted evidence, domain decisions, stable Public
API, security boundaries, execution semantics, architecture seams, validation
obligations, and explicit deferrals into one navigable package.

This document does not replace the normative sources it indexes. Its job is to
remove implementation-planning ambiguity by saying which source owns each
decision, what observable result must exist, how failure remains safe, which
security boundary applies, and what proof seam the implementation must use.

Issue #22 remains specification-only. Runtime implementation belongs to issue
#42 and must be decomposed into vertical stories before code starts. Creating
#42 does not assign it, label it ready for an autonomous agent, create a Go
module, choose infrastructure, or begin Gateway implementation.

## Authority and conflict resolution

### Source hierarchy

The implementation must apply these sources by ownership, not by whichever
file is newest or most convenient:

1. `contracts/openapi/pixelplus-public-api-v1.yaml` owns the stable wire
   representation for all 26 Public API operations. Its frozen compatibility
   oracle is
   `contracts/openapi/baselines/pixelplus-public-api-v1.0.0.yaml`.
2. `docs/decisions/0008-stable-public-api-contract-policy.md` owns versioning,
   compatibility, deprecation, idempotency, and the public contract-test seam.
3. `docs/decisions/0009-pure-go-module-seams-and-dependency-budget.md` owns the
   Go package boundaries, dependency direction, port catalogue, composition
   root, and external-dependency budget.
4. `docs/decisions/0010-grok-xai-oauth-operation-surface-policy.md` owns the
   server-side operation-to-surface mapping for Grok xAI OAuth and forbids
   client overrides or automatic cross-surface fallback.
5. The normative specifications listed in the manifest own product behavior,
   domain invariants, authorization, security, lifecycle, execution, failure,
   retry, retention, health, and routing semantics in their named domains.
6. `CONTEXT.md` owns canonical domain vocabulary and points to each normative
   specification. It summarizes rules but does not override them.
7. The four research documents under `docs/spec/research/` own evidence and
   baseline capability confidence. Evidence never authorizes execution by
   itself; a current Provider Account snapshot, usability gate, risk gate, and
   routing decision are still required.
8. The inference and management prototype specifications and `v0alpha`
   OpenAPI artifacts are retained evidence. They do not compete with the
   stable `/v1` artifact.

When sources appear inconsistent, apply the source that owns the narrower
domain. Wire representation cannot redefine domain meaning, and a domain
specification cannot invent a wire field absent from the stable contract. If
two sources with the same ownership level genuinely conflict, stop that
implementation slice and create a decision; do not resolve it in code.

### Locked product boundary

The Gateway is a centralized SaaS, Pure-Go, BYOA service for six independent
Provider/Auth Mode surfaces:

- ChatGPT Web Access.
- ChatGPT Codex OAuth.
- Gemini Web Cookie.
- Gemini Antigravity OAuth.
- Grok Web SSO.
- Grok xAI OAuth.

Client API Keys authenticate Tenant software to PixelPlus. Provider
Credentials authorize a single Tenant-owned Provider Account to one Auth Mode.
Those credentials, identities, capability facts, risk decisions, quota, jobs,
assets, routing policies, and outputs are never shared across Tenants.

The Public API is the stable OpenAI-compatible core plus PixelPlus management,
Asset, and durable Render Job extensions under `/v1`. Chat and image work are
in scope. Video, shared cross-Tenant account pools, extra Provider families,
official OpenAI/Gemini/xAI API adapters, alternate public facades, the rewritten
Photoshop Plugin, migration, and billing remain outside the Gateway MVP unless
their deferred trigger is opened.

### Common protected-operation order

Every operation applies the relevant subset of this order. An outer layer may
parse and reject malformed input, but it may not move protected effects ahead
of the application-owned gates:

```text
parse unknown boundary input
  -> authenticate Client API Key
  -> derive Security Principal and Tenant server-side
  -> authorize operation scope
  -> enforce request-size and other admission gates
  -> atomically claim operation idempotency/replay ownership when applicable
  -> establish same-Tenant resource visibility/ownership
  -> apply lifecycle, administrative-control, risk, capability, health,
     surface-circuit, explicit-selection, lease, affinity, and routing gates
  -> record required audit intent before protected access
  -> authorize purpose-bound Vault/content use
  -> execute the single application-owned operation or enqueue its durable job
  -> normalize one canonical outcome with commit certainty
  -> persist/reconcile state and accounting
  -> emit secret-free audit, telemetry, and one canonical request log
```

Failure at a gate prevents every later protected effect. Foreign resource
existence, secret material, content, raw Provider details, internal retry
topology, and concrete infrastructure details are not safe diagnostics.

## Capability evidence ledger

The only capability field tokens are `verified`, `conditionally_supported`,
`unsupported`, and `unverified`. The evidence documents use the prose form
`conditionally supported`; the manifest records its canonical field token
`conditionally_supported`.

These rows are the implementation starting evidence, not an account guarantee.
Only a fresh, current-credential Capability Snapshot may authorize an
operation/model pair, and only when all ownership, lifecycle, risk, control,
health, routing, admission, and Vault gates also pass. `unsupported` and
`unverified` are never offerable. A reference-learned claim cannot be promoted
to `verified` without live evidence for that exact Auth Mode, account,
operation, and model.

| Auth Mode | Risk state | Chat | Streaming | Image generation | Image edit | Inpaint | Evidence |
| --- | --- | --- | --- | --- | --- | --- | --- |
| ChatGPT Web Access | `experimental` | `conditionally_supported` | `conditionally_supported` | `conditionally_supported` | `conditionally_supported` | `conditionally_supported` | `docs/spec/research/chatgpt-auth-mode-capability-evidence.md` |
| ChatGPT Codex OAuth | `gated` | `conditionally_supported` | `conditionally_supported` | `conditionally_supported` | `conditionally_supported` | `conditionally_supported` | `docs/spec/research/chatgpt-auth-mode-capability-evidence.md` |
| Gemini Web Cookie | `experimental` | `conditionally_supported` | `conditionally_supported` (synthetic) | `conditionally_supported` | `conditionally_supported` | `unsupported` | `docs/spec/research/gemini-auth-mode-capability-evidence.md` |
| Gemini Antigravity OAuth | `gated` | `conditionally_supported` | `conditionally_supported` | `unverified` | `unverified` | `unsupported` | `docs/spec/research/gemini-auth-mode-capability-evidence.md` |
| Grok Web SSO | `prohibited` | `conditionally_supported` | `conditionally_supported` | `conditionally_supported` | `conditionally_supported` | `unsupported` | `docs/spec/research/grok-auth-mode-capability-evidence.md` |
| Grok xAI OAuth | `gated` | `conditionally_supported` | `conditionally_supported` | `conditionally_supported` | `conditionally_supported` | `unsupported` | `docs/spec/research/grok-auth-mode-capability-evidence.md` |

Risk and capability are orthogonal. In particular, Grok Web SSO evidence does
not make the mode executable while its risk state is `prohibited`. Likewise,
a successful probe cannot promote an Auth Mode from `experimental` or `gated`
to `allowed`.

Streaming truth must be preserved. Gemini Web Cookie produces synthetic
client streaming after receiving an upstream body; it must not be represented
as real upstream token streaming. Provider-specific frames and terminal
markers are normalized to the canonical PixelPlus stream ordering.

## Decision ledger

The following table is the planning index. The manifest contains the same IDs
and full dependency list. Each implementation story must link the exact
normative sections it consumes rather than citing this table alone.

| Decision ID | Observable behavior | Failure semantics | Security impact | Authority |
| --- | --- | --- | --- | --- |
| `tenant_ownership_and_authorization` | One Client API Key derives one Tenant-bound Security Principal; every resource keeps immutable Tenant ownership. | Invalid auth is 401-class; missing same-Tenant scope is 403-class; foreign/unknown/deleted resources are indistinguishable 404-class before protected effects. | Blocks cross-Tenant access and confused-deputy execution. | `tenant-ownership-authorization-invariants.md` |
| `auth_mode_risk_envelope` | Each immutable Auth Mode has an independent risk state and kill/reopen contract. | Prohibited, killed, or unacknowledged gated modes reject before Vault or Adapter use. | Capability, health, or routing cannot override compliance and acceptable-use controls. | `auth-mode-risk-envelope-and-kill-criteria.md` |
| `client_api_key_and_admission` | One-time bearer keys use Tenant scopes; admission order is scope, size, rate, concurrency, quota, accept. | Admission failures remain distinct from Provider runtime failure and limit dependency outage fails closed. | Enforces least privilege, revoke propagation, non-secret storage, and per-Tenant anti-abuse isolation. | `client-api-key-lifecycle-and-admission-controls.md` |
| `provider_account_and_credential_lifecycle` | Accounts follow the accepted connection, validation, probe, activation, refresh, reauth, disable, revoke, and delete states with monotonic credential versions. | Any failed usability item rejects before Adapter execution and returns canonical remediation. | Prevents pending, stale, revoked, deleted, or cross-mode credentials from executing. | `provider-account-connection-and-credential-lifecycle.md` |
| `capability_snapshot_and_model_availability` | Per-account, per-model facts carry status, provenance, freshness, probe surface, and credential version. | Unsupported, unverified, stale, invalid, or unavailable facts reject before upstream. | Static or foreign evidence never grants capability. | `capability-snapshot-and-model-availability-semantics.md` |
| `tenant_routing_fallback_affinity_and_leases` | Selection precedence is explicit pin, lease, affinity, policy, declared fallback over same-Tenant candidates. | No policy, invalid candidate, explicit pin, or insufficient non-commit proof fails closed without silent fallback. | Prevents cross-Tenant pooling, cross-mode widening, and routing around gates. | `tenant-scoped-routing-fallback-affinity-leases.md` |
| `chat_execution_and_streaming` | Chat follows X1-X6; streams emit `open`, `delta`/heartbeat, then one terminal. | Cancel, disconnect, timeout, residual work, and uncertainty retain one execution and conservative accounting. | Prevents duplicate terminals/execution and keeps original Tenant/key occupancy on residual work. | `chat-execution-and-streaming-lifecycle.md` |
| `asset_exchange_and_retention` | Immutable Tenant-owned Assets validate decoded media, dimensions, image-mask relation, retention, deletion, and atomic capacity. | Invalid input fails before upstream; foreign/unknown are non-enumerating; expired/deleted content cannot be retrieved. | Prevents content disclosure, mask downgrade, cap races, and unbounded output retention. | `asset-exchange-authorization-and-retention-lifecycle.md` |
| `durable_render_jobs_and_output_retry` | Durable jobs use atomic creation, leases, fencing, attempt certainty, immutable manifests, and placement identities. | Committed/unknown attempts cannot be replaced; output retry never re-renders; cancellation settles conservatively. | Prevents duplicate spend, stale-worker commit, unsafe refund, and cross-Tenant recovery. | `durable-render-job-and-output-retry-lifecycle.md` |
| `credential_vault_and_sensitive_data` | Secrets and content use separate Tenant/resource/purpose-bound encrypted lifecycles and independent version families. | Auth, audit, binding, key, dependency, retention, or deletion failure denies decrypt/use. | Keeps plaintext outside application and ordinary records; prevents handle authority and cross-Tenant ciphertext reuse. | `credential-vault-and-sensitive-data-lifecycle.md` |
| `canonical_errors_idempotency_and_retry_ownership` | One Provider-independent error, commit certainty, remediation, and retry owner describe each terminal decision. | Conflicts/in-progress/uncertain claims never steal; outer layers cannot create a second full-operation retry chain. | Prevents duplicate side effects and secret/internal diagnostic leakage. | `canonical-errors-and-retry-ownership.md` |
| `health_cooldown_circuit_and_operator_controls` | Scoped health, cooldown, recovery permits, circuits, drain, quarantine, and disable remain separate state dimensions. | Stale success cannot clear newer failure; expiry grants bounded half-open proof; kill/quarantine dominate without rewriting health. | Prevents hammering, unsafe recovery, circuit poisoning, and control state becoming authorization. | `provider-account-health-cooldown-and-operator-controls.md` |
| `stable_public_api_and_compatibility` | One OpenAPI 3.1.1 `/v1` 1.0.0 artifact owns 26 operations, scopes, schemas, and idempotency classes. | Breaking v1 change fails validation and requires a major; removal follows deprecation notice and successor gates. | Prevents silent auth, scope, replay, error, and representation drift. | `api-versioning-compatibility-idempotency-contract-testing-policy.md`, decision 0008 |
| `pure_go_module_seams_and_dependency_budget` | A Pure-Go module uses the accepted domain/application/ports/outer-adapter/composition layout and production/test constructor. | Forbidden imports, generic policy shortcuts, private test seams, or unapproved dependencies block acceptance. | Keeps protocol, storage, queue, HTTP, and secret representations outside the canonical domain. | decision 0009 |
| `grok_xai_oauth_operation_surface_policy` | Grok xAI OAuth chat/streaming use `cli_chat_proxy`; image generation/edit use `api_x_ai`; inpaint stays unsupported; clients cannot choose the surface. | Activation chat proof cannot authorize image work; missing exact-surface proof rejects before execution and no alternate surface is attempted. | Prevents upstream widening, capability inflation, and silent fallback that changes credential, commit, or retry semantics. | decision 0010, Grok evidence, Provider Account lifecycle, Capability Snapshot semantics |

### Stable Public API operation groups

The implementation must expose every operation in the stable artifact through
`application.PublicGateway` and the same production composition:

- Models: list the currently offerable models and account offers for the
  authenticated Tenant.
- Chat: non-streaming creation, streaming on the same operation, and explicit
  execution cancel.
- Assets: create, metadata retrieval, and content retrieval.
- Images and Render Jobs: generation, edit, inpaint, job retrieval, cancel,
  and output-delivery retry.
- Provider Accounts: create/list/get/delete, direct credential submission,
  OAuth start/poll, probe, reauthentication, disable, and enable.
- Capabilities and Routing: account Capability Snapshot retrieval and Tenant
  singleton Routing Policy get/replace.

The OpenAPI operation descriptor matrix remains the source of exact path,
method, scope, idempotency header, request, response, and error encoding. No
generic CRUD handler or Provider-shaped route may substitute for these
operations.

### Canonical failure and retry rules

An implementation story must not invent an error merely because an upstream
uses a new message. The Adapter normalizes raw behavior to the accepted safe
taxonomy and records commit evidence. The application selects the terminal
canonical result. One operation has one full-execution retry owner:

- Chat application execution owns chat retry/fallback.
- Render Job execution owns Provider render attempt/recovery.
- Output delivery owns only retrieval/staging/placement from an immutable
  manifest and cannot reopen Provider execution.
- Safe resource reads may repeat without creating Provider or job work.
- Transport, queue redelivery, Adapter-local retry, and workers do not become
  additional retry owners.

`committed` or `unknown` never means safe to replace. A matching idempotency
replay returns the prior accepted result or in-progress state and does not
admit, execute, enqueue, debit, render, or place again.

### Sensitive-data and observability rules

The application never receives general plaintext Provider Credential bytes.
It submits an authorized access intent and capability Adapter to the Vault;
the Vault performs audit-before-decrypt and injects non-exportable secret
material only for that bounded call. Prompt/replay content, Asset bytes, and
Render staging use separate protected content ports.

The following never enter Public API output, canonical errors, ordinary audit,
request/application logs, metric fields, traces, queue payloads, snapshots,
routing state, health state, or contract fixtures:

- Client API Key or Provider Credential material.
- Ciphertext, wrapped keys, envelope metadata that acts as bearer material, or
  decrypted secret handles.
- Prompt, request/replay content, Asset bytes, or Render staging bytes.
- Raw Provider payloads, unsafe URLs, cookies, challenges, or protocol frames.
- Foreign-resource existence, concrete schema details, stack traces, worker
  identities, queue topology, or retry-owner internals.

Each HTTP request emits one canonical safe JSON request log with `timestamp`,
`level`, `request_id`, `user_id` when known, `action`, `duration_ms`,
`status_code`, and `message`. This operational log is separate from product and
security audit. A required audit record that cannot be accepted denies the
protected operation; logs or metrics cannot substitute for it.

## Planning closure ledger

Issue #22 acceptance criterion 3 is represented as five mandatory planning
domains. Every locked decision belongs to exactly one domain below, every
domain is present in the manifest completion gate, and the validator rejects a
missing domain, uncovered decision, duplicate assignment, or non-locked
disposition. This proves register coverage; independent review still judges
whether the decisions themselves are sufficient and the full handoff remains
fingerprinted after that review.

| Domain | Disposition | Decision IDs |
| --- | --- | --- |
| `product` | `locked` | `auth_mode_risk_envelope`, `capability_snapshot_and_model_availability` |
| `domain` | `locked` | `provider_account_and_credential_lifecycle`, `tenant_routing_fallback_affinity_and_leases`, `asset_exchange_and_retention`, `durable_render_jobs_and_output_retry`, `health_cooldown_circuit_and_operator_controls` |
| `interface` | `locked` | `stable_public_api_and_compatibility`, `pure_go_module_seams_and_dependency_budget` |
| `security` | `locked` | `tenant_ownership_and_authorization`, `client_api_key_and_admission`, `credential_vault_and_sensitive_data` |
| `execution` | `locked` | `chat_execution_and_streaming`, `canonical_errors_idempotency_and_retry_ownership`, `grok_xai_oauth_operation_surface_policy` |

## Implementation work breakdown

Issue #42 is the umbrella. Before runtime edits, create high-risk or normal
story packets for these dependency-ordered vertical slices. Each story must
name its public seam with the human before tests are written, follow red-green
cycles, and update proof through Harness.

### 1. Foundation and composition

Create `apps/gateway/go.mod`, the accepted package tree, the production
`cmd/gateway` entrypoint, `internal/composition.New`, `Runtime.Handler`,
`Runtime.Worker`, `Runtime.RunWorkers`, and `Runtime.Close`. Add parsed
non-secret config, deterministic Clock/ID ports, readiness lifecycle, and
compile-time architecture checks. Start with standard library only.

Proof seam: the real production composition constructor exposed through a
public HTTP server and exported worker lifecycle. A health/readiness smoke may
prove composition but cannot bypass or pre-decide product gates.

### 2. Principal, admission, audit, and replay

Implement HTTP boundary parsing, Client API Key authentication, Security
Principal derivation, Tenant visibility/non-enumeration, exact operation
scopes, ordered admission, canonical error serialization, required audit
intent, request logging, and atomic idempotency claim/replay. Controlled ports
must prove rejection occurs before later side effects.

Proof seam: public HTTP with controlled Principal, Admission, Replay, Audit,
Telemetry, RequestLog, Clock, and ID implementations.

### 3. Provider Account and Vault lifecycle

Implement account management journeys, immutable Auth Mode identity, direct
secret ingress, OAuth state, validation/probe transitions, version-fenced
reauthentication, enable/disable, revoke/delete, risk gates, protected Vault
use, retention, and no-secret projections. Use controlled capability Adapters;
do not begin real Provider protocols in this slice.

Proof seam: stable management HTTP routes through real composition and a Vault
that records safe authorize/use observations without exposing plaintext.

### 4. Capability, health, controls, and routing

Implement Capability Snapshot observation/freshness, model offers, account
health conditions, cooldown and half-open permits, surface circuits, drain,
quarantine, Tenant enable/disable interaction, candidate construction,
explicit pins, leases, affinity, Routing Policy, and fail-closed fallback.

Proof seam: public model/account/capability/routing routes plus controlled
Probe, Capability, and Recovery Adapter capabilities. Stale writes and
cross-Tenant observations must be independently rejected.

### 5. Asset and Render Job execution

Implement Asset create/read/content/deletion and atomic storage reservation,
then image-generation/edit/inpaint durable creation, JobRuntime reference-only
delivery, worker fencing, attempt certainty, cancellation, residual tracking,
immutable result manifests, output Asset placement, and output-only retry.

Proof seam: stable Asset/Render HTTP plus exported `JobExecutor` using the same
application policy, with controlled metadata/content/staging/job stores,
JobRuntime, Vault, Render Adapter, Clock, IDs, audit, and accounting.

### 6. Chat execution

Implement non-streaming and streaming chat, route/lease/affinity consumption,
open/delta/heartbeat/terminal ordering, synthetic-stream disclosure, cancel,
disconnect, timeout, residual tracking, conservative accounting, commit
certainty, idempotent replay, and one retry/fallback chain.

Proof seam: public HTTP and SSE through real composition with controlled Chat
Adapter, Vault, Replay, routing/health/capability, cancellation, and accounting
observations.

### 7. Provider adapters and full conformance

Add one Provider/Auth Mode adapter at a time behind its accepted risk gate and
capability evidence. Each adapter owns protocol translation only. It does not
select Tenants, persist application state, expose Provider payloads, or retry a
full non-idempotent operation. Live evidence can update an account snapshot but
cannot inflate another operation, model, account, mode, or risk state.

Proof seam: first controlled protocol fixtures, then explicitly authorized
live probes for the exact mode/account. Finish with all 26 stable operations
through production composition and the frozen compatibility validator.

### Implementation choices that do not reopen product semantics

The runtime team may make bounded local decisions when the corresponding
deferred trigger is reached:

- Concrete private type names and file splits inside the accepted packages.
- Physical schema, indexes, and transaction mechanics that satisfy every
  logical atomicity and retention contract without crossing ports.
- Standard-library implementation details and approved dependency-slot choices
  backed by the required benchmark/security/operational evidence.
- Numeric values within named tunable classes when the governing spec permits
  tuning and the selected value is documented and tested.
- Deployment plumbing after topology, SLO, canary, and launch decisions are
  explicitly opened.

Those choices may not alter Tenant authority, resource ownership, public wire
shape, authorization, risk status, capability meaning, lifecycle transitions,
secret boundaries, failure semantics, retry ownership, commit certainty,
accounting, retention meaning, or test seams.

### Locked Grok xAI OAuth surface policy

Grok xAI OAuth surface selection is not a client or implementation choice.
Decision 0010 fixes the server-owned operation mapping:

- `chat` and `chat_streaming` use `cli_chat_proxy`.
- `image_generation` and `image_edit` use `api_x_ai`.
- `inpaint` is unsupported.

Account activation uses a cost-minimal `cli_chat_proxy` chat probe. That proof
does not authorize image operations. Each image operation/model remains
non-offerable until a current-credential live probe proves it on `api_x_ai`.
Capability facts carry the exact surface binding, and Adapter, routing, retry,
or recovery code never attempts the alternate surface after a failure. The
stable Public API exposes no surface-selection field.

## Deferred item register

The full register, including reason, dependencies, and exact reopen trigger,
is normative in
`docs/spec/provider-gateway-implementation-ready-manifest.json`. Every deferred
item preserves the constraints in its originating specification. A deferred
technology choice is not permission to defer product correctness.

### Implementation and operations choices

| IDs | Deferred scope | Opens when |
| --- | --- | --- |
| `D-PERSISTENCE-DRIVER`, `D-VAULT-CRYPTO-VENDOR`, `D-JOB-RUNTIME` | Physical database, Vault primitive/vendor, and queue/runtime selection. | Immediately before that implementation, with the benchmark, security, migration, custody, delivery, and dependency-budget evidence named in the manifest. |
| `D-DEPLOYMENT-TOPOLOGY`, `D-SLO-CANARY-LAUNCH` | Single-region/distributed topology, SLOs, alerts, canary, and launch criteria. | After runtime conformance and before production admission. |
| `D-NUMERIC-TUNE`, `D-PROBE-RATE` | Tunable TTL, retention, cooldown, lease, timeout, quota, retry, and probe budgets. | When runtime/Provider measurements justify a value without weakening the named invariant. |
| `D-LEGACY-MIGRATION` | Legacy backend and Photoshop Plugin cutover. | After Gateway `/v1` conformance is green in the target deployment. |

### Optional behavior and contract changes

| IDs | Deferred scope | Opens when |
| --- | --- | --- |
| `D-SNAPSHOT-GRACE`, `D-ROTATE-GRACE`, `D-REAUTH-GRACE` | Grace periods that would weaken current fail-closed freshness or immediate cutover defaults. | A measured availability/client need exists and security proof preserves revoke, versioning, and non-use invariants. |
| `D-MULTI-ACCT`, `D-ROUTE-AUTOFALLBACK`, `D-ROUTE-XMODE` | Pool balancing or wider/default fallback behavior. | A new routing decision defines Tenant policy, disclosures, capability equality, and retry safety. |
| `D-CHAT-TOOLS`, `D-CHAT-RESUME`, `D-RENDER-RESUME` | Tool/multimodal chat and client-visible resume protocols. | A concrete client requirement and compatible public lifecycle/idempotency contract exist. |
| `D-RENDER-UPSTREAM-IDEMPOTENCY`, `D-RENDER-OUTPUT-RECREATE` | Provider idempotency strengthening or explicit recreation after output expiry. | Exact upstream evidence or a new admitted client operation defines the behavior without reusing an old side-effect identity. |
| `D-ASSET-CHUNK`, `D-ASSET-DEDUPE` | Resumable upload and storage dedupe. | Measured transfer/storage needs justify a design that retains Tenant, encryption, accounting, retention, and deletion isolation. |
| `D-CODEX-APIKEY-MODE`, `D-NEW-PROVIDERS-OFFICIAL-ADAPTERS` | New Auth Modes, Provider families, or official API adapters. | A new product/risk/capability decision defines the complete independent surface. |
| `D-ERROR-WIRE`, `D-RETRY-AT-LEAST-ONCE` | Semantic error-wire changes or accepting at-least-once non-idempotent execution. | A new compatibility/product decision proves current canonical semantics are insufficient and defines duplicate/accounting consequences. |

### Product, security, and compliance initiatives

| IDs | Deferred scope | Opens when |
| --- | --- | --- |
| `D-CHAT-HISTORY` | User-visible durable transcript product. | Consent, retrieval, deletion, retention, export, and Tenant administration are specified. |
| `D-BREAK-GLASS`, `D-INCIDENT-UPSTREAM-EVIDENCE` | Operator decrypt or raw incident-evidence retention. | A security ADR proves dual control, bounded purpose/time, encryption, retention, notification, and audit-before-access. |
| `D-TENANT-DELETE-EXPORT`, `D-JURISDICTION-RETENTION` | Full Tenant deletion/export and jurisdiction schedules. | Product or legal obligations identify deployment jurisdictions and complete data-class behavior. |
| `D-BATCH-RETRY` | Multi-operation batch API and retry semantics. | A new contract preserves per-operation idempotency, commit, accounting, and retry ownership. |
| `D-BILLING` | Commercial metering, credits, refunds, plans, and invoice-grade usage. | A separate product initiative distinguishes billing from anti-abuse quota and defines accounting authority. |

### Risk-envelope and security reopen decisions

| IDs | Deferred scope | Opens when |
| --- | --- | --- |
| `D-COUNSEL-AGENT`, `D-COUNSEL-RE`, `D-REGION` | Technical-agent theory, reverse-engineering enforceability, and regional launch restrictions. | Counsel evidence exists for the exact launch jurisdiction and risk-state change under consideration. |
| `D-OAI-TOKEN`, `D-ANTIGRAVITY-TERMS`, `D-GROK-ISSUER`, `D-XAI-COMPETE` | Provider-specific token custody, terms, issuer, and competitive-product gaps. | The primary source or counsel evidence named in the source risk specification is recorded before the affected risk-state promotion. |
| `D-COMM` | Commercial fee characterization under resale, lease, or time-sharing restrictions. | Product and counsel record the proposed pricing/marketing posture before claims or risk promotion. |
| `D-ASSET-CAP-TUNE` | Numeric Tenant Asset cap and reclamation tuning. | Storage evidence supports new values without changing atomic committed-plus-reserved semantics. |
| `D-VAULT-KEY-TOPOLOGY`, `D-LEGAL-HOLD-CREDENTIAL` | Logical key-topology change or a legal requirement to change the no-decryptable-credential-hold rule. | A security ADR proves equivalent Tenant binding, non-use, access, audit, revocation, and purge behavior. |

## Validation contract

### Static package proof for issue #22

The specification gate passes only when:

- every authority file exists;
- every authority file matches its validator-owned SHA-256 fingerprint;
- the stable implementation issue is distinct from gate issue #22;
- every capability claim uses the four-token vocabulary and links evidence;
- every capability claim matches the detailed Auth Mode capability matrix
  parsed from its declared evidence source;
- every decision states observable behavior, failure semantics, security
  impact, and dependencies;
- the product, domain, interface, security, and execution planning domains are
  all locked and cover every accepted decision exactly once;
- Provider-specific execution policies match the validator-owned contract;
- every implementation slice has dependencies, authority, and a public proof
  seam;
- every deferred item has a reason, dependency, and reopen trigger;
- all required sections exist in this document; and
- retained Public API and prototype validators remain green.

The executable proof is:

```text
node --test scripts/test-provider-gateway-implementation-spec-validator.mjs
node scripts/validate-provider-gateway-implementation-spec.mjs
```

### Runtime proof required by issue #42

Runtime completion cannot be inferred from this document. It requires tests
through the public HTTP handler returned by the production composition
constructor and, for workers, the exported `JobExecutor`/`RunWorkers` path.
Controlled implementations may replace only the approved ports. Assertions
must cover the wire result plus safe side-effect absence, identity, order, or
count at system boundaries.

At minimum, prove:

1. Foreign/unknown resources reject before decrypt, Adapter use, persistence
   mutation, enqueue, content access, or foreign existence disclosure.
2. Concurrent matching idempotency requests create one owner and one side
   effect; conflict, in-progress, and uncertainty never steal.
3. Scope, admission, lifecycle, control, risk, capability, health, circuit, and
   routing failures reject before the protected effect they govern.
4. Credential and content material never appears in any prohibited projection.
5. Chat emits one canonical terminal and retains honest cancel/residual/
   accounting behavior.
6. A Render attempt with committed or unknown certainty is never replaced;
   output retry reuses the manifest and placement identity without rendering.
7. Worker redelivery and stale fencing cannot multiply Provider work or commit
   stale state.
8. All 26 stable operations match exact scope, idempotency, response, error,
   compatibility, and deprecation policy through real composition.

Private functions, direct use-case tests as contract proof, mock handlers,
concrete schema queries, Provider SDK shapes, goroutine layout/count, and
generated OpenAPI examples alone are not valid substitutes.

## Completion gate

Issue #22 is complete when this package and its validation are green, #42
exists as a separate blocked implementation issue, Harness proof is recorded,
and independent review finds no missing acceptance item.

The gate conclusion is:

- Product semantics are locked by the normative specifications.
- Domain vocabulary and ownership are locked by `CONTEXT.md` and #6-#17.
- Public representation and compatibility are locked by the stable OpenAPI
  artifact, baseline, #20, and decision 0008.
- Security and sensitive-data boundaries are locked by #6-#9, #15-#17.
- Chat, Asset, Render Job, retry, idempotency, health, and routing execution
  semantics are locked by #10-#17 and #20.
- Pure-Go package seams, ports, composition, contract-test seam, and dependency
  budget are locked by #21 and decision 0009.
- Grok xAI OAuth operation-to-surface selection is locked by decision 0010 and
  cannot be recreated by issue #42.
- Remaining items are explicit bounded implementation/operations choices or
  separate future product decisions with reasons, dependencies, and reopen
  triggers. None requires an implementation agent to invent current Tenant,
  API, credential, security, or execution behavior.

Issue #42 remains unopened for execution until a human selects its first
vertical story. The Gateway runtime, database, Provider adapters, Vault,
workers, deployment, migration, and Photoshop Plugin have not been implemented
by this gate.
