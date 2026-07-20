# 0009 Pure-Go Gateway Module Seams and Dependency Budget

Date: 2026-07-20

## Status

Accepted

## Context

Issue #21 is the architecture decision that follows the accepted domain,
sensitive-data, error/retry, health, and Public API contract decisions in
#15, #16, #17, and #20. The repository has no Gateway runtime yet. The next
implementation must therefore have a concrete shape without allowing an HTTP
framework, Provider protocol, database schema, queue, or secret representation
to become the canonical domain model.

The future Gateway has several different failure and ownership boundaries:
Public API admission, Tenant authorization, Provider Account routing, Provider
execution, Credential Vault access, durable Asset and Render Job state, output
delivery, and worker recovery. The stable Public API contract also requires
future conformance tests to enter through real HTTP composition and replace
infrastructure only at controlled ports. A package layout that makes those
boundaries implicit would allow duplicate retry owners, cross-Tenant access,
secret leakage, or tests that pass while production composition is broken.

## Decision

PixelPlus will implement the Gateway as a Pure-Go module under
`apps/gateway`. Issue #21 locks the module seams and dependency rules below;
it does not create the runtime, concrete schema, queue, Provider Adapter, or
Vault implementation.

### Module and package layout

The implementation module is `apps/gateway` with the following package
responsibilities. Names are part of the decision so follow-up implementation
stories do not recreate the boundary from scratch.

```text
apps/gateway/
  go.mod
  cmd/gateway/
    main.go                         composition root for the production process
  internal/
    domain/                          canonical entities, values, invariants
    application/                     use cases and retry/ownership policy
    ports/                           application-owned inbound/outbound ports
    transport/http/                  Public API parsing, routing, serialization
    adapters/                        Provider and Auth Mode protocol translation
    infrastructure/
      vault/                         Credential Vault implementation
      persistence/                   durable state and atomic transitions
      jobs/                          queue/runtime implementation
      observability/                 logs, metrics, and audit delivery
    composition/                     dependency wiring and readiness lifecycle
    contracttest/                    public-HTTP composition test fixtures
```

`internal/` keeps the implementation replaceable while allowing contract tests
inside the Gateway module to exercise the real composition. The only process
entrypoint is `cmd/gateway`; it parses process configuration and calls
`composition`. It contains no business rules.

`domain` owns Provider-independent concepts such as Tenant and Security
Principal identity, Provider Account state, Capability Snapshot, Routing
Policy, Health State/Reason, Asset, Render Job, idempotency identity, commit
certainty, and canonical error values. Domain code may use the Go standard
library's pure value and error packages, but must not import HTTP, SQL, queue,
Provider, Vault, process environment, or third-party application packages.

`application` owns typed commands, queries, use cases, and the order of gates.
It derives Tenant authority from the authenticated Client API Key, enforces
ownership/scope/admission/capability/health/lifecycle rules, claims replay
ownership before side effects, and delegates exactly one retry chain to the
operation owner defined by #16. It never parses wire DTOs, constructs SQL,
knows Provider protocol fields, or handles a raw queue message.

`ports` is owned by the application boundary. Ports use canonical domain and
application types, not `net/http`, SQL rows, Provider payloads, queue
messages, or Vault ciphertext. Infrastructure and Adapter packages implement
these interfaces; the application never imports their concrete packages.

`transport/http` is an outer adapter. It parses unknown HTTP input into typed
application commands, maps canonical outcomes to the stable OpenAPI contract,
and performs no authorization, routing, retry, persistence, or secret work.

`adapters` contains Provider/Auth Mode protocol code. Each adapter translates
Provider-specific requests, responses, streams, challenges, and protocol
drift into canonical port outcomes. Provider SDK types and wire values stop in
this layer. Adapters do not select Tenants, retry a full non-idempotent
operation, write durable state, or expose Provider payloads to HTTP.

`infrastructure/vault` owns ciphertext, envelope/key versions, purpose-bound
decrypt, rotation, revocation, retention, purge, and redaction. The
application-facing Vault port exposes protected operations, not ciphertext or
handles as authority. Credential material is available only for the bounded
callback that invokes an authorized Adapter/probe operation.

`infrastructure/persistence` owns the physical store and transactions. It
implements logical transitions and consistency guarantees, not application
policy. No SQL row, ORM model, migration type, or driver error crosses
`ports`. A physical implementation may combine several logical stores in one
transaction, but the application depends on the logical port contracts.

`infrastructure/jobs` owns queue delivery, worker lease plumbing, and
at-least-once message handling. Queue redelivery may reclaim the same job or
attempt reference but never grants permission for a replacement Provider
execution. The Render Job application owner decides whether an attempt may
run, recover, or finalize.

`infrastructure/observability` receives typed, secret-free audit and telemetry
records. Audit-before-allow failures remain visible to the application as a
denial for protected operations; logging and metrics are never authorization
proof.

Application logs and product audit records are separate outputs. The request
log recorder emits exactly one canonical JSON log line per HTTP request with
`timestamp`, `level`, `request_id`, `user_id` when known, `action`,
`duration_ms`, `status_code`, and a safe `message`. The application log is an
operational projection; it is not an audit record and must not be used as an
authorization proof. `AuditRecorder` writes the product/security audit
projection with its own retention and availability semantics. Both recorders
reject or redact credentials, ciphertext/envelopes, prompt/content,
Provider payloads, foreign-resource existence, raw URLs, stack traces, and
worker internals before serialization.

### Dependency direction

The allowed compile-time direction is:

```text
cmd/gateway -> composition -> {transport, application, adapters,
                               infrastructure}

transport -> application -> {domain, ports}
adapters -> {domain, ports}
infrastructure/* -> {domain, ports}
contracttest -> composition -> the public HTTP handler
```

The reverse edges are forbidden. In particular:

- `domain` cannot import `application`, `ports`, transport, Provider, or
  infrastructure.
- `application` cannot import an Adapter, concrete Vault, persistence driver,
  queue client, HTTP package, or process configuration.
- `transport/http` cannot call a repository or Adapter directly.
- Provider protocol packages cannot define or replace canonical domain values.
- `composition` may wire concrete implementations but cannot add policy.
- `contracttest` may observe HTTP results and controlled port observations, but
  may not call private functions, handler internals, concrete schemas, or
  goroutine layouts.

Unknown data is parsed at an outer boundary before it enters an inner package:

```text
HTTP/provider/queue/env input
  -> boundary parser
  -> typed application/port input
  -> domain invariant and application gate
```

### Concrete port seams

The initial port set is intentionally small and capability-oriented. A future
implementation may split an interface when a consistency or ownership rule
requires it, but may not bypass the port with a concrete dependency.

| Port | Responsibility | Required observable guarantees |
| --- | --- | --- |
| `PublicGateway` | Typed Public API use cases for inference, Assets, Render Jobs, Provider Accounts, capabilities, and Routing Policy | HTTP composition reaches the same application policy used by all clients; no client-supplied Tenant authority |
| `JobExecutor` | Worker-facing execution of a durable Job reference | Same-Tenant fencing, attempt commit certainty, and #16 retry ownership are applied before Provider execution |
| `AdapterRegistry` capabilities | Resolve separate Chat, Render, Probe, Capability, and same-attempt Recovery ports for a Provider Account/Auth Mode | Provider protocol stays outside canonical domain; outcomes carry commit certainty and safe diagnostics |
| `CredentialVault` | Store, authorize, use, revoke, rotate, retain, and purge sensitive material | Purpose/resource/Tenant binding and audit-before-decrypt; plaintext is unavailable to logs, responses, traces, and ordinary records |
| `PrincipalStore` / `AdmissionStore` | Authenticate key material and read/claim scoped admission state | Tenant and Client API Key scope are derived server-side; unavailable dependencies fail closed |
| `ReplayStore` | Atomic idempotency claim, fingerprint match/conflict, in-progress and terminal replay | No-steal rule; one accepted owner; replay record lifecycle is independent from resource lifecycle |
| `AccountStore` / `CapabilityStore` / `HealthStore` / `RoutingPolicyStore` | Logical account, capability, health, cooldown, control, and routing transitions | Tenant scope, credential-version fencing, stale-write rejection, and atomic policy validation |
| `AssetStore` / `RenderJobStore` | Asset reservation/lifecycle and durable job/attempt/manifest state | Atomic Tenant accounting, worker fencing, immutable manifest, and output placement idempotency |
| `JobRuntime` | Enqueue/dequeue and worker lifecycle plumbing | Payloads contain safe references only; at-least-once delivery cannot multiply Provider side effects |
| `Clock` / `IDGenerator` | Time and identifier creation | All replay, expiry, lease, cooldown, request, job, attempt, and placement identities are controllable in tests |
| `AuditRecorder` / `TelemetryRecorder` | Secret-free audit and operational observation | Audit failure is a typed dependency outcome; no raw Provider, secret, content, or stack trace crosses the record |

The port interfaces must expose logical operations rather than generic
`Get/Put` methods or storage transactions. Examples include atomic replay
claim, Asset byte/count reservation, Render Job creation, worker claim with
fencing, same-attempt recovery, scoped health observation, and output
placement by stable placement key. A physical store may implement these with
SQL transactions or another mechanism without leaking that mechanism.

The `CredentialVault` port uses a purpose-bound operation such as
`WithCredential(ctx, accessIntent, use)` rather than returning a general
secret byte slice to the caller. The callback is scoped to the authorized
Adapter/probe action and its error is normalized before it reaches another
boundary. Prompt, Asset bytes, Render staging, and audit-safe records use the
same principle with data-class-specific intents.

The first interface catalogue is deliberately concrete even though the Go
module is deferred. Names below are the minimum shape; request and outcome
types are typed domain/application values and are not `map[string]any` or wire
DTOs:

```go
// internal/application
type PublicGateway interface {
	ModelsGateway
	ChatGateway
	AssetGateway
	RenderGateway
	ProviderAccountGateway
	RoutingPolicyGateway
}

type ModelsGateway interface {
	ListModels(context.Context, ModelsQuery) (ModelsResponse, error)
}

type ChatGateway interface {
	CreateChatCompletion(context.Context, ChatRequest) (ChatResponse, error)
	StreamChat(context.Context, ChatRequest, ChatSink) error
	CancelChatExecution(context.Context, CancelChatRequest) (CancelChatResponse, error)
}

type AssetGateway interface {
	CreateAsset(context.Context, CreateAssetRequest) (AssetResponse, error)
	GetAsset(context.Context, GetAssetRequest) (AssetResponse, error)
	GetAssetContent(context.Context, GetAssetContentRequest) (AssetContentResponse, error)
}

type RenderGateway interface {
	CreateImageGeneration(context.Context, ImageGenerationRequest) (RenderJobResponse, error)
	CreateImageEdit(context.Context, ImageEditRequest) (RenderJobResponse, error)
	CreateImageInpaint(context.Context, ImageInpaintRequest) (RenderJobResponse, error)
	GetRenderJob(context.Context, GetRenderJobRequest) (RenderJobResponse, error)
	CancelRenderJob(context.Context, CancelRenderJobRequest) (CancelRenderJobResponse, error)
	RetryRenderJobOutput(context.Context, RetryOutputRequest) (OutputDeliveryResponse, error)
}

type ProviderAccountGateway interface {
	CreateProviderAccount(context.Context, CreateProviderAccountRequest) (ProviderAccountResponse, error)
	ListProviderAccounts(context.Context, ListProviderAccountsRequest) (ProviderAccountsResponse, error)
	GetProviderAccount(context.Context, GetProviderAccountRequest) (ProviderAccountResponse, error)
	DeleteProviderAccount(context.Context, DeleteProviderAccountRequest) (DeleteProviderAccountResponse, error)
	SubmitProviderCredential(context.Context, SubmitCredentialRequest) (ProviderAccountResponse, error)
	StartOAuthAuthorization(context.Context, StartOAuthRequest) (OAuthAuthorizationResponse, error)
	GetOAuthAuthorization(context.Context, GetOAuthRequest) (OAuthAuthorizationResponse, error)
	ProbeProviderAccount(context.Context, ProbeAccountRequest) (ProviderAccountResponse, error)
	ReauthenticateProviderAccount(context.Context, ReauthenticateRequest) (ProviderAccountResponse, error)
	DisableProviderAccount(context.Context, DisableAccountRequest) (ProviderAccountResponse, error)
	EnableProviderAccount(context.Context, EnableAccountRequest) (ProviderAccountResponse, error)
	GetCapabilitySnapshot(context.Context, GetCapabilitySnapshotRequest) (CapabilitySnapshotResponse, error)
}

type RoutingPolicyGateway interface {
	GetRoutingPolicy(context.Context, GetRoutingPolicyRequest) (RoutingPolicyResponse, error)
	ReplaceRoutingPolicy(context.Context, ReplaceRoutingPolicyRequest) (RoutingPolicyResponse, error)
}

type JobExecutor interface {
	ExecuteJob(context.Context, JobReference) error
}

// internal/ports
type AdapterRegistry interface {
	ResolveChat(context.Context, domain.ProviderAccountRef) (ChatAdapter, error)
	ResolveRender(context.Context, domain.ProviderAccountRef) (RenderAdapter, error)
	ResolveProbe(context.Context, domain.ProviderAccountRef) (ProbeAdapter, error)
	ResolveCapabilities(context.Context, domain.ProviderAccountRef) (CapabilityAdapter, error)
	ResolveRecovery(context.Context, domain.ProviderAccountRef) (RecoveryAdapter, error)
}

type ChatAdapter interface {
	Chat(context.Context, ChatRequest) (ChatOutcome, error)
	StreamChat(context.Context, ChatRequest, StreamSink) error
}

type RenderAdapter interface {
	Render(context.Context, RenderRequest) (RenderOutcome, error)
}

type RecoveryAdapter interface {
	Recover(context.Context, RecoveryRequest) (RecoveryOutcome, error)
}

type ProbeAdapter interface {
	Probe(context.Context, ProbeRequest) (ProbeOutcome, error)
}

type CapabilityAdapter interface {
	ObserveCapabilities(context.Context, CapabilityRequest) (CapabilityOutcome, error)
}

type CredentialVault interface {
	Store(context.Context, StoreSecret) (domain.CredentialRef, error)
	WithCredential(context.Context, AccessIntent, func(SecretMaterial) error) error
	Revoke(context.Context, RevokeSecret) error
	Purge(context.Context, PurgeSecret) error
}

type ReplayStore interface {
	Claim(context.Context, domain.ReplayIdentity, domain.Fingerprint) (ReplayDecision, error)
	Complete(context.Context, domain.ReplayClaim, ReplayResult) error
}

type PrincipalStore interface {
	Authenticate(context.Context, PresentedClientAPIKey) (domain.SecurityPrincipal, error)
}

type AdmissionStore interface {
	Admit(context.Context, AdmissionRequest) (AdmissionDecision, error)
	Reconcile(context.Context, AdmissionReservation, AdmissionOutcome) error
}

type AccountStore interface {
	Visible(context.Context, domain.SecurityPrincipal, domain.ProviderAccountRef) (domain.ProviderAccount, error)
	Transition(context.Context, AccountTransition) (domain.ProviderAccount, error)
}

type CapabilityStore interface {
	ReadSnapshot(context.Context, domain.SecurityPrincipal, domain.ProviderAccountRef) (domain.CapabilitySnapshot, error)
	RecordObservation(context.Context, CapabilityObservation) error
}

type HealthStore interface {
	ReadCondition(context.Context, domain.ProviderAccountRef, domain.HealthScope) (domain.HealthCondition, error)
	Observe(context.Context, HealthObservation) error
}

type RoutingPolicyStore interface {
	Read(context.Context, domain.SecurityPrincipal) (domain.RoutingPolicy, error)
	Replace(context.Context, RoutingPolicyChange) (domain.RoutingPolicy, error)
}

type AssetStore interface {
	Visible(context.Context, domain.SecurityPrincipal, domain.AssetRef) (domain.Asset, error)
	Reserve(context.Context, AssetReservation) (ReservationDecision, error)
	Commit(context.Context, AssetReservation, AssetCommit) (domain.Asset, error)
	Release(context.Context, AssetReservation) error
}

type RenderJobStore interface {
	Create(context.Context, RenderJobCreation) (domain.RenderJob, error)
	ClaimWorker(context.Context, domain.JobRef, WorkerLease) (WorkerClaim, error)
	RecordAttempt(context.Context, AttemptObservation) error
	Finalize(context.Context, JobFinalization) error
	PlaceOutput(context.Context, PlacementRequest) (PlacementResult, error)
}

type JobRuntime interface {
	Enqueue(context.Context, SafeJobReference) (EnqueueReceipt, error)
	Run(context.Context, JobHandler) error
	Close(context.Context) error
}

type Clock interface { Now() time.Time }
type IDGenerator interface { New(domain.IdentifierKind) (domain.Identifier, error) }
type AuditRecorder interface { Record(context.Context, AuditEvent) error }
type TelemetryRecorder interface { Record(context.Context, TelemetryEvent) error }
type RequestLogRecorder interface { Record(context.Context, RequestLog) error }
```

The Public API inbound port consumes the complete stable operation descriptor
set; it must not reduce the contract to one generic CRUD path. The source of
truth for wire details remains the stable OpenAPI artifact and its validator
descriptor matrix from #20. The following mapping is the required application
operation coverage and idempotency class consumed by the future Go module:

| Operation | Stable route | Scope requirement | Idempotency class / header |
| --- | --- | --- | --- |
| `listModels` | `GET /models` | `capabilities.read` | `resource_retrieval` / not applicable |
| `createChatCompletion` | `POST /chat/completions` | `chat.completions` | `chat_execution` / optional |
| `cancelChatExecution` | `POST /chat/executions/{execution_id}/cancel` | `chat.completions` | `resource_state_commands` / not required |
| `createAsset` | `POST /assets` | `assets.write` | `durable_creation` / required |
| `getAsset` | `GET /assets/{asset_id}` | `assets.read` | `output_retrieval` / not applicable |
| `getAssetContent` | `GET /assets/{asset_id}/content` | `assets.read` | `output_retrieval` / not applicable |
| `createImageGeneration` | `POST /images/generations` | `images.generate` | `durable_creation` / required |
| `createImageEdit` | `POST /images/edits` | `images.edit` | `durable_creation` / required |
| `createImageInpaint` | `POST /images/inpaints` | `images.edit` | `durable_creation` / required |
| `getRenderJob` | `GET /render-jobs/{job_id}` | `jobs.read` | `output_retrieval` / not applicable |
| `cancelRenderJob` | `POST /render-jobs/{job_id}/cancel` | `jobs.manage` | `resource_state_commands` / not required |
| `retryRenderJobOutput` | `POST /render-jobs/{job_id}/outputs/{output_entry_id}/retry` | `jobs.manage` | `output_delivery_retry` / not required |
| `createProviderAccount` | `POST /provider-accounts` | `accounts.manage` | `durable_creation` / required |
| `listProviderAccounts` | `GET /provider-accounts` | `accounts.read` | `resource_retrieval` / not applicable |
| `getProviderAccount` | `GET /provider-accounts/{provider_account_id}` | `accounts.read` | `resource_retrieval` / not applicable |
| `deleteProviderAccount` | `DELETE /provider-accounts/{provider_account_id}` | `accounts.manage` | `resource_state_commands` / not required |
| `submitProviderCredential` | `POST /provider-accounts/{provider_account_id}/credentials` | `accounts.manage` | `durable_creation` / required |
| `startOAuthAuthorization` | `POST /provider-accounts/{provider_account_id}/oauth-authorizations` | `accounts.manage` | `durable_creation` / required |
| `getOAuthAuthorization` | `GET /provider-accounts/{provider_account_id}/oauth-authorizations/{authorization_id}` | `accounts.manage` | `resource_retrieval` / not applicable |
| `probeProviderAccount` | `POST /provider-accounts/{provider_account_id}/probe` | `accounts.manage` | `resource_state_commands` / not required |
| `reauthenticateProviderAccount` | `POST /provider-accounts/{provider_account_id}/reauthentication` | `accounts.manage` | `durable_creation` / required |
| `disableProviderAccount` | `POST /provider-accounts/{provider_account_id}/disable` | `accounts.manage` | `resource_state_commands` / not required |
| `enableProviderAccount` | `POST /provider-accounts/{provider_account_id}/enable` | `accounts.manage` | `resource_state_commands` / not required |
| `getCapabilitySnapshot` | `GET /provider-accounts/{provider_account_id}/capability-snapshot` | `accounts.read` or `capabilities.read` | `resource_retrieval` / not applicable |
| `getRoutingPolicy` | `GET /routing-policy` | `routing.read` | `resource_retrieval` / not applicable |
| `replaceRoutingPolicy` | `PUT /routing-policy` | `routing.manage` | `resource_state_commands` / not required |

The application may use separate typed methods internally for these operation
families, but every row must resolve through `PublicGateway` and the same
composition. A future contract test must be able to prove the row's scope,
header policy, replay result, and operation-specific retry owner through HTTP.

The shown methods are the minimum logical interface shape. Account,
capability, and health writes must carry revision/credential-version fencing;
`AssetStore.Reserve` must account for committed plus reserved bytes/count;
`RenderJobStore.Create` and `PlaceOutput` must be idempotent by their stable
identities; and `JobRuntime.Run` may redeliver only the safe reference to the
same `JobExecutor`. These interfaces are separate from `ReplayStore` because
replay ownership and resource lifecycle have different retention and recovery
semantics. The catalogue does not authorize a single universal `Store`
interface or permit physical transactions to cross the application boundary.

### Composition root and readiness

`internal/composition` is the only package allowed to assemble concrete
dependencies. Its constructor accepts a dependency bundle containing the
controlled ports and returns the Public HTTP handler plus the worker entrypoint
and lifecycle close function. Production wiring and test wiring use the same
constructor; tests replace only port implementations.

The constructor shape is:

```go
type Dependencies struct {
	Principal PrincipalStore
	Admission AdmissionStore
	Replay ReplayStore
	Accounts AccountStore
	Capabilities CapabilityStore
	Health HealthStore
	Routing RoutingPolicyStore
	Assets AssetStore
	Jobs RenderJobStore
	Adapters AdapterRegistry
	Vault CredentialVault
	Runtime JobRuntime
	Clock Clock
	IDs IDGenerator
	Audit AuditRecorder
	Telemetry TelemetryRecorder
	RequestLog RequestLogRecorder
}

func New(Config, Dependencies) (*Runtime, error)
func (Runtime) Handler() http.Handler
func (Runtime) Worker() application.JobExecutor
func (Runtime) Close(context.Context) error
```

`Config` contains already-parsed, non-secret runtime configuration. Environment
and file parsing belongs in `cmd/gateway`/composition and never in domain or
application. `Runtime` is the only returned object shared by the production
process and contract fixtures; the fixture uses `Handler()` with
`httptest.NewServer` and never reaches through to a private handler or use
case.

Composition startup must restore durable health/cooldown and replay/job
recovery state before advertising execution readiness. Shutdown propagates a
root context to HTTP, workers, Provider requests, and persistence, then closes
outer resources in reverse construction order. Readiness is not granted when a
required persistence, queue, key, or audit dependency cannot satisfy its
fail-closed contract.

### Contract-test seam

Future runtime contract tests create a real composition with deterministic
controlled ports, wrap `runtime.Handler()` in `httptest.NewServer`, and make
requests only through the stable Public API surface. Controlled implementations
record safe observations such as Adapter calls, Vault decrypt attempts,
durable writes, job enqueues, and output placements. Tests assert both the
wire result and the side-effect order/count.

The minimum conformance cases are:

1. Foreign or unknown Tenant/resource identifiers return non-enumerating
   results before Vault, Adapter, persistence mutation, or job enqueue.
2. Matching concurrent idempotency requests produce one claimant and one
   execution; conflicts and uncertain ownership never steal or replace it.
3. Unsupported/stale capability, health, lifecycle, scope, and admission
   gates reject before protected data access or upstream work.
4. A committed or uncertain Render attempt can recover the same attempt but
   cannot start a replacement generation.
5. Output placement retries reuse the immutable result manifest and placement
   key without reopening render execution or quota.
6. Secret-bearing input never appears in response, error, log, metric, audit,
   trace, queue payload, snapshot, or contract fixture.

Private functions, handler stubs, direct application calls, concrete schema
queries, mock HTTP handlers, and assertions about goroutine count/order are
not valid substitutes for these tests.

### Dependency budget

The default budget is zero third-party dependencies in `domain`, `application`,
`ports`, `transport/http`, `composition`, and contract fixtures. The standard
library is preferred for HTTP (`net/http`), JSON (`encoding/json`), context,
crypto primitives, errors, synchronization, time, logging (`log/slog`), and
testing (`testing`, `httptest`).

The following are the only approved implementation slots:

| Slot | Budget | Rationale and boundary |
| --- | --- | --- |
| Cryptography | One `golang.org/x/crypto` module only if the approved Vault primitive cannot be implemented safely with `crypto/*` | Vault implementation only; never imported by domain or transport; version and primitive must be recorded with the Vault implementation |
| Durable persistence | One cgo-free Pure-Go database driver, if needed, and no ORM/query builder | Persistence implementation only; `database/sql` or driver types stop at that boundary; selection requires benchmark and migration evidence |
| Provider protocol | Zero Provider SDKs by default | `net/http` and small adapter-local codecs keep protocol drift isolated and prevent SDK types from becoming domain API; an exception needs a new decision |
| Queue/runtime | Zero dependencies in the application; at most one direct cgo-free queue/runtime client in `infrastructure/jobs` when durable delivery requires it | `JobRuntime` remains the only application seam; the implementation story must document delivery, acknowledgement, recovery, operational ownership, and why the standard library is insufficient; no queue framework or second client |
| Web/DI/retry/telemetry | Zero frameworks, DI containers, generic retry libraries, and vendor telemetry SDKs in the first implementation | Ownership and lifecycle rules stay explicit in application code; operational integration uses typed recorder ports |

Every external module must be direct, pinned, license-compatible, cgo-free
where applicable, and justified by an operational requirement that the
standard library cannot meet. Transitive dependency growth is reviewed as
part of the implementation story. Adding a new slot, SDK, framework, ORM, or
retry owner requires a new decision rather than an incidental `go get`.

## Alternatives Considered

1. **Repository-first or framework-first architecture.** Rejected because it
   makes storage/HTTP concepts the center of the domain and encourages
   handlers or repositories to own retries and authorization.
2. **One generic `Provider` interface.** Rejected because chat, streaming,
   render, probe, and capability observation have different commit and retry
   contracts; capability-oriented ports keep those rules explicit.
3. **Return decrypted `[]byte` from the Vault.** Rejected because a general
   secret value is easy to log, retain, or pass across a Tenant or purpose
   boundary; a bounded authorized use operation limits exposure.
4. **Test handlers or use cases directly with mocks.** Rejected because the
   accepted #20 policy requires real HTTP composition and observable
   controlled ports to prove ownership ordering and side-effect counts.
5. **Adopt a web framework, ORM, DI container, queue client, and Provider SDK
   up front.** Rejected because no current runtime requirement justifies those
   dependencies and each would enlarge the canonical boundary.

## Consequences

Positive:

- The future Go implementation has one explicit dependency direction and a
  composition root shared by production and contract tests.
- Provider protocol, secret material, storage schema, queue delivery, and
  transport representation cannot silently become canonical domain APIs.
- Idempotency, retry ownership, Tenant isolation, and fail-closed Vault access
  have named ports and observable proof obligations.
- Standard-library-first defaults keep the module small and make dependency
  review meaningful.

Tradeoffs:

- The application and ports need more typed value objects than a generic CRUD
  layer.
- A physical persistence implementation must translate several logical atomic
  transitions instead of exposing one convenient ORM model.
- Contract fixtures need controlled fakes that count side effects, which is
  more work than unit-testing private handlers.
- The Vault callback boundary requires careful memory lifetime and redaction
  discipline in the implementation.

## Follow-Up

- Build `apps/gateway` and its first public HTTP composition in a follow-up
  implementation story; keep this decision as the architecture authority.
- Add the concrete Go interfaces and compile-time dependency checks before
  implementing Provider adapters or persistence schemas.
- Add the public-HTTP conformance suite with controlled Adapter, Vault,
  persistence, JobRuntime, Clock, and IDGenerator implementations.
- Select the Pure-Go persistence driver and any cryptographic module only with
  implementation-specific validation and a refreshed dependency rationale.
- Revisit this decision if a new Provider protocol, storage engine, queue
  contract, or observability SDK requires an additional dependency slot.
