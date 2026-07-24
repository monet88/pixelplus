package ports

import (
	"context"
	"errors"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// Typed Render Job port errors.
var (
	// ErrRenderJobNotVisible is the single non-enumerating visibility failure
	// for foreign, unknown, and deleted job ids (#14 §9, #6).
	ErrRenderJobNotVisible = errors.New("render job not visible")
	// ErrRenderJobConflict reports a lost CAS/fence on a job mutation.
	ErrRenderJobConflict = errors.New("render job mutation conflict")
	// ErrRenderAdapterUnavailable fails closed when no controlled Provider
	// render surface is configured.
	ErrRenderAdapterUnavailable = errors.New("render adapter unavailable")
)

// RenderJobCreation is the typed command to persist one admitted queued job.
// Create is idempotent by (tenant, client key, fingerprint) when the store is
// asked to re-insert the same identity; application normally uses ReplayStore
// for Public API replay and Create for the winning owner only.
type RenderJobCreation struct {
	Principal domain.SecurityPrincipal
	Job       domain.RenderJob
}

// WorkerLease is the identity a worker presents when claiming a job.
// Now is the injected observation instant for claim timestamps (required).
// ExpiresAt, when non-zero, bounds the fence lifetime for expiry recovery
// (#14 §6.4). Zero means the store applies its foundation default.
type WorkerLease struct {
	WorkerID  domain.Identifier
	Now       domain.Timestamp
	ExpiresAt domain.Timestamp
}

// WorkerClaim is the fenced ownership grant returned by an atomic claim.
type WorkerClaim struct {
	Job          domain.RenderJob
	FencingToken domain.FencingToken
	AlreadyOwned bool
	// RecoveryOnly is true when the worker reclaimed an expired lease after
	// payload/manifest for drain/finalize only. Adapter generation is forbidden
	// (#14 §6.4 post-payload recovery).
	RecoveryOnly bool
}

// AttemptObservation records attempt ledger facts under the current fence.
type AttemptObservation struct {
	JobRef       domain.JobRef
	FencingToken domain.FencingToken
	Attempt      domain.UpstreamAttempt
	Phase        domain.ExecutionPhase
	CommitStatus domain.CommitStatus
	Progress     domain.JobProgress
	// Now advances job.UpdatedAt when non-zero.
	Now domain.Timestamp
}

// FencedTransition mutates lifecycle under the current fencing token.
type FencedTransition struct {
	JobRef       domain.JobRef
	FencingToken domain.FencingToken
	To           domain.JobLifecycleState
	Phase        domain.ExecutionPhase
	Progress     domain.JobProgress
	FailureStage domain.FailureStage
	FailureClass domain.ErrorCode
	CommitStatus domain.CommitStatus
	// RequireStates, when non-empty, requires current lifecycle ∈ set.
	RequireStates []domain.JobLifecycleState
	// ClearLease releases the worker fence on terminal transitions.
	ClearLease bool
	// Now is required for terminal transitions (TerminalAt / UpdatedAt).
	Now domain.Timestamp
}

// ManifestCapture freezes the immutable result manifest under the fence.
// Application captures Provider result first; the store only records metadata.
type ManifestCapture struct {
	JobRef       domain.JobRef
	FencingToken domain.FencingToken
	Manifest     domain.ResultManifest
	Phase        domain.ExecutionPhase
	Now          domain.Timestamp
}

// PlacementRequest records an already-committed Asset placement on the job.
// Application/output worker owns AssetMetadataStore.Reserve/Commit and
// AssetContentStore.Put; this request only carries the resulting Asset identity
// for fenced, idempotent job metadata update by placement key.
type PlacementRequest struct {
	JobRef       domain.JobRef
	FencingToken domain.FencingToken
	EntryID      domain.OutputEntryID
	// Asset is the already-committed same-Tenant output Asset projection.
	Asset domain.Asset
	// DeliveryStateForced, when non-empty, records delivery failure without an Asset
	// (e.g. storage_cap_exceeded after Asset reserve failed).
	DeliveryStateForced domain.OutputDeliveryState
	FailureClass        string
	Now                 domain.Timestamp
}

// PlacementResult is the idempotent placement outcome.
type PlacementResult struct {
	Job     domain.RenderJob
	Entry   domain.OutputEntry
	Created bool
}

// CancelMutation records cancel intent or terminal cancel under optional fence.
type CancelMutation struct {
	Principal domain.SecurityPrincipal
	JobID     domain.Identifier
	// FencingToken is zero for client cancel of queued/running (store CAS).
	// Worker cancel completion carries the fence.
	FencingToken domain.FencingToken
	RequestedBy  domain.ClientAPIKeyID
	Now          domain.Timestamp
}

// RenderJobStore owns durable job/attempt/manifest state with fencing.
// All mutations after claim require the current fencing token (#14 §5.3).
type RenderJobStore interface {
	Create(context.Context, RenderJobCreation) (domain.RenderJob, error)
	Visible(context.Context, domain.SecurityPrincipal, domain.Identifier) (domain.RenderJob, error)
	// Load loads by JobRef for worker paths (Tenant is explicit on the ref).
	Load(context.Context, domain.JobRef) (domain.RenderJob, error)
	ClaimWorker(context.Context, domain.JobRef, WorkerLease) (WorkerClaim, error)
	ObserveAttempt(context.Context, AttemptObservation) (domain.RenderJob, error)
	Transition(context.Context, FencedTransition) (domain.RenderJob, error)
	CaptureManifest(context.Context, ManifestCapture) (domain.RenderJob, error)
	PlaceOutput(context.Context, PlacementRequest) (PlacementResult, error)
	// Cancel applies client or worker cancellation rules atomically.
	Cancel(context.Context, CancelMutation) (domain.RenderJob, error)
	// BindAccountLease records the job→account continuity binding for this job.
	// It is not an exclusive account-wide mutex (#11 §5.2).
	BindAccountLease(context.Context, domain.JobRef, domain.FencingToken, domain.ProviderAccountID) error
	// AccountLeaseHolder reports a non-terminal job bound to the account for
	// diagnostics; multiple jobs may share an account.
	AccountLeaseHolder(context.Context, domain.TenantID, domain.ProviderAccountID) (domain.Identifier, bool, error)
	// ReleaseAccountLease clears the worker fence hold for the job.
	ReleaseAccountLease(context.Context, domain.JobRef, domain.FencingToken) error
	// MarkQueuePublished records that the SafeJobReference was accepted by the
	// queue for this job (durable create may precede publication; #14 §3.3).
	MarkQueuePublished(context.Context, domain.JobRef) (domain.RenderJob, error)
	// ListUnpublishedQueue returns durable non-terminal jobs whose SafeJobReference
	// was never accepted by the queue (QueuePublished=false). Used by autonomous
	// startup/background recovery without a second client request (#14 §3.3).
	ListUnpublishedQueue(context.Context) ([]domain.RenderJob, error)
	// MarkAdmissionSettled records that create-time occupancy Reconcile completed
	// for this job. Idempotent when already settled.
	MarkAdmissionSettled(context.Context, domain.JobRef) (domain.RenderJob, error)
	// MarkPromptPurged records that confidential prompt material was deleted.
	// Idempotent when already purged.
	MarkPromptPurged(context.Context, domain.JobRef) (domain.RenderJob, error)
	// RenewWorkerLease extends LeaseExpiresAt and HeartbeatAt under the current
	// fence for a long-running healthy worker. Stale fence / wrong worker fails.
	RenewWorkerLease(context.Context, domain.JobRef, domain.FencingToken, WorkerLease) (domain.RenderJob, error)
}

// RenderReplayDecision carries a terminal job for create replay.
type RenderReplayDecision struct {
	Outcome     ReplayOutcome
	TerminalJob domain.RenderJob
}

// RenderReplayResult is the terminal projection recorded after create.
type RenderReplayResult struct {
	Job domain.RenderJob
}

// RenderReplayStore performs atomic idempotency for image create surfaces.
type RenderReplayStore interface {
	Claim(context.Context, domain.ReplayIdentity) (RenderReplayDecision, error)
	Complete(context.Context, domain.ReplayIdentity, RenderReplayResult) error
	Abandon(context.Context, domain.ReplayIdentity) error
}

// StagingIdentity is the stable key for one staged Provider output entry.
// It is Tenant-scoped and binds manifest + entry + checksum so placement retry
// never reopens a different capture (#14 §8.3, ADR 0009 RenderStagingStore).
type StagingIdentity struct {
	TenantID   domain.TenantID
	JobID      domain.Identifier
	ManifestID domain.ResultManifestID
	EntryID    domain.OutputEntryID
	Checksum   string
}

// Valid reports whether the identity is usable for Put/Use.
func (id StagingIdentity) Valid() bool {
	return id.TenantID != "" && id.JobID != "" && id.ManifestID != "" && id.EntryID != "" && id.Checksum != ""
}

// StagingPut stores temporary result bytes under a stable identity. Permanent
// Asset objects do not live here — only capture staging for placement/retry.
// ExpiresAt, when non-zero, bounds retention for storage-cap placement retry;
// Use after expiry fails closed and clears the blob.
type StagingPut struct {
	Identity    StagingIdentity
	ContentType string
	Data        []byte
	// ExpiresAt bounds staging retention for placement-only recovery after
	// storage-cap failure. Zero means fixture/default unbounded until Delete.
	ExpiresAt domain.Timestamp
}

// StagingAccess authorizes purpose-bound use of staged bytes for placement.
// Application never receives plaintext as a return value; Use injects into a
// callback only. Now, when non-zero, is the observation instant for ExpiresAt.
type StagingAccess struct {
	Principal domain.SecurityPrincipal
	Identity  StagingIdentity
	Now       domain.Timestamp
}

// ErrStagingNotFound reports missing or non-visible staged material for the
// requested identity (same-Tenant non-enumeration at the application boundary).
var ErrStagingNotFound = errors.New("render staging material not found")

// ErrStagingExpired reports staged material past ExpiresAt; implementations
// clear the blob and return this so placement retry fails closed.
var ErrStagingExpired = errors.New("render staging material expired")

// RenderStagingStore owns temporary Provider result bytes for capture and
// placement retry. It is distinct from AssetContentStore (permanent Assets) and
// from job metadata. Application must inject this port — never use package-
// global maps (ADR 0009, #14 §8).
//
// Put is idempotent by StagingIdentity (same checksum). Use injects a copy of
// bytes into the callback and returns ErrStagingNotFound when the identity is
// unknown or Tenant does not match. Delete purges after successful placement.
// Storage-cap paths may set ExpiresAt; Use after expiry fails and clears.
type RenderStagingStore interface {
	Put(context.Context, StagingPut) error
	Use(context.Context, StagingAccess, func([]byte) error) error
	Delete(context.Context, StagingIdentity) error
}

// RenderPromptIntake is the transient create-time handoff of prompt material
// into the confidential store. Application forwards it once and never retains
// or logs it (ADR 0009 TenantConfidentialStore / I-FAIL-CLOSED-SENSITIVE).
type RenderPromptIntake struct {
	TenantID domain.TenantID
	JobID    domain.Identifier
	// Material is the raw prompt. It never enters RenderJob metadata, status
	// projections, audit, or ordinary Adapter commands.
	Material string
}

// RenderPromptAccess authorizes purpose-bound Use of stored prompt material.
type RenderPromptAccess struct {
	TenantID domain.TenantID
	JobID    domain.Identifier
}

// RenderPromptStore is the purpose-bound confidential port for render prompts.
// Put accepts transient intake at create. Delete purges on terminal/rollback.
// Use injects plaintext into a callback for the authorized infrastructure
// boundary only — application never receives plaintext as a return value.
type RenderPromptStore interface {
	Put(context.Context, RenderPromptIntake) error
	Use(context.Context, RenderPromptAccess, func(plaintext string) error) error
	Delete(context.Context, RenderPromptAccess) error
}

// RenderCapturePlan names the staging identities for one authorized render so
// Provider output bytes are written only into RenderStagingStore inside the
// protected boundary (ADR 0009).
type RenderCapturePlan struct {
	TenantID   domain.TenantID
	JobID      domain.Identifier
	AttemptID  domain.AttemptID
	ManifestID domain.ResultManifestID
}

// PayloadSendBoundary records the durable fact that Provider payload
// transmission is beginning. It is invoked only at the protected send surface
// (immediately before Adapter.Render), never before preflight/authorization
// so a crash prior to Adapter entry cannot falsely block lease recovery
// (#14 §6.2–6.4).
type PayloadSendBoundary interface {
	MarkPayloadSent(context.Context) error
}

// AuthorizedRenderRequest is the application-facing request for one upstream
// render. It carries only safe identities so the authorized port can resolve
// Vault credential, confidential prompt, input/mask Asset bytes, and staging
// capture internally. Application never supplies or receives prompt/credential/
// Asset/output plaintext. SendBoundary is marked only immediately before Adapter entry.
type AuthorizedRenderRequest struct {
	Principal    domain.SecurityPrincipal
	JobRef       domain.JobRef
	AccountID    domain.ProviderAccountID
	AuthMode     domain.AuthMode
	Version      int
	Invocation   domain.RenderInvocation
	Capture      RenderCapturePlan
	SendBoundary PayloadSendBoundary
	// InputAssetIDs and MaskAssetID are same-Tenant identities only; bytes are
	// resolved inside AuthorizedRender via AssetContentStore (ADR 0009).
	InputAssetIDs []domain.AssetID
	MaskAssetID   domain.AssetID
}

// AuthorizedRender is the protected execution boundary for one render attempt.
// Implementations resolve credential (Vault), prompt (confidential store), and
// stage Provider outputs into RenderStagingStore via a capture sink. Application
// receives only safe RenderOutcome metadata (ADR 0009).
type AuthorizedRender interface {
	Render(context.Context, AuthorizedRenderRequest) (domain.RenderOutcome, error)
}

// RenderCommand is the safe Adapter invocation after authorization. It never
// carries prompt plaintext or credential material as durable fields.
type RenderCommand struct {
	Principal  domain.SecurityPrincipal
	AccountID  domain.ProviderAccountID
	AuthMode   domain.AuthMode
	Version    int
	Invocation domain.RenderInvocation
}

// PromptInjection grants the Adapter a single-call, Use-scoped view of prompt
// plaintext constructed only inside AuthorizedRender.
type PromptInjection interface {
	Use(func(plaintext string) error) error
}

// InputAssetMaterial is one input or mask image presented to the Adapter only
// inside Use. Callers must not retain Data after Use returns.
type InputAssetMaterial struct {
	AssetID     domain.AssetID
	ContentType string
	Data        []byte
}

// InputAssetInjection grants the Adapter a single-call, Use-scoped view of
// same-Tenant input/mask Asset bytes resolved only inside AuthorizedRender.
// Generation may pass an empty injection (no inputs).
type InputAssetInjection interface {
	Use(func(inputs []InputAssetMaterial, mask *InputAssetMaterial) error) error
}

// RenderCaptureSink receives Provider output bytes only inside the authorized
// infrastructure boundary. Accept stages bytes and records safe entry metadata;
// application never sees the byte slices.
type RenderCaptureSink interface {
	Accept(position int, contentType string, data []byte) error
}

// CredentialInjection grants the Adapter a single-call, Use-scoped view of
// Provider credential material. Material is minted only inside
// RenderCredentialAuthorizer.Authorize and must never cross into application,
// domain, wire, logs, or durable job fields (ADR 0009).
type CredentialInjection interface {
	Use(func(secretMaterial string) error) error
}

// RenderCredentialAuthorizer is the vault-owned capability for render execution.
// Authorize validates the credential identity and, only on success, invokes fn
// with a callback-scoped CredentialInjection. It never returns plaintext to
// callers outside fn. Absent credential / auth mismatch / fail-closed state
// must return without calling fn so Adapter is never entered.
type RenderCredentialAuthorizer interface {
	Authorize(context.Context, CredentialValidation, func(CredentialInjection) error) error
}

// RenderAdapter runs one controlled generation/edit/inpaint attempt after the
// authorized boundary has resolved secrets. Prompt, input/mask Asset bytes, and
// credentials are injected via protected Use-scoped values; output bytes go only
// to RenderCaptureSink. RenderCommand never carries content bytes. Adapter is
// structurally incomplete without CredentialInjection (cannot succeed after
// Validate alone).
type RenderAdapter interface {
	Render(context.Context, RenderCommand, PromptInjection, InputAssetInjection, CredentialInjection, RenderCaptureSink) (domain.RenderOutcome, error)
}

// Restorer is an optional recovery contract for durable render persistence.
// Memory fixtures may no-op; Unavailable stores return dependency failure.
// composition.New runs Restore before Ready=true (#54 Standards P1-C).
type Restorer interface {
	Restore(context.Context) error
}

// ErrRenderDigesterUnavailable is returned when the digester cannot mint a
// durable fingerprint (missing/weak key, fail-closed composition). Create must
// fail closed with dependency_unavailable before replay/admission side effects.
var ErrRenderDigesterUnavailable = errors.New("render digester unavailable")

// RenderDigester produces opaque, keyed digests for create-time fingerprint and
// optional prompt binding. The key never leaves the digester implementation
// (composition/confidential infrastructure). Application receives only hex digests.
// Methods return errors so product paths cannot proceed with empty digests when
// the digester is fail-closed (not only /readyz).
// Unkeyed SHA-256 of the prompt MUST NOT equal these digests (dictionary oracle ban).
type RenderDigester interface {
	// DigestPrompt returns a keyed HMAC digest of prompt material.
	DigestPrompt(prompt string) (string, error)
	// CreateFingerprint binds operation, model, prompt, and asset ids under the key
	// using a typed structured payload (not delimiter concatenation).
	CreateFingerprint(operation domain.RenderOperation, model, prompt string, inputs []domain.AssetID, mask domain.AssetID) (domain.Fingerprint, error)
}

// RenderAuditAction names a Render Job product/security audit event.
type RenderAuditAction string

// Audit actions emitted by the Render Job spine.
const (
	AuditRenderJobCreated   RenderAuditAction = "render_job.created"
	AuditRenderJobClaimed   RenderAuditAction = "render_job.claimed"
	AuditRenderJobCompleted RenderAuditAction = "render_job.completed"
	AuditRenderJobFailed    RenderAuditAction = "render_job.failed"
	AuditRenderJobCanceled  RenderAuditAction = "render_job.canceled"
	AuditRenderJobRead      RenderAuditAction = "render_job.read"
	AuditRenderOutputRetry  RenderAuditAction = "render_job.output_retry"
	AuditRenderOutputPlaced RenderAuditAction = "render_job.output_placed"
	// AuditRenderProtectedAccess is recorded at the AuthorizedRender boundary
	// BEFORE credential authorization and BEFORE prompt/asset plaintext release.
	AuditRenderProtectedAccess RenderAuditAction = "render_job.protected_access"
)

// RenderAuditEvent is a secret-free product/security audit projection.
// It never carries prompt, image bytes, credentials, or temporary URLs.
type RenderAuditEvent struct {
	Action         RenderAuditAction
	TenantID       domain.TenantID
	ClientAPIKeyID domain.ClientAPIKeyID
	JobID          domain.Identifier
	AccountID      domain.ProviderAccountID
	RequestID      domain.Identifier
	Outcome        string
	Lifecycle      domain.JobLifecycleState
}

// RenderAuditRecorder writes the secret-free Render Job audit projection.
type RenderAuditRecorder interface {
	Record(context.Context, RenderAuditEvent) error
}
