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
type WorkerLease struct {
	WorkerID domain.Identifier
	Now      domain.Timestamp
}

// WorkerClaim is the fenced ownership grant returned by an atomic claim.
type WorkerClaim struct {
	Job          domain.RenderJob
	FencingToken domain.FencingToken
	AlreadyOwned bool
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

// RenderPromptStore is the purpose-bound confidential port for render prompts.
// Put accepts transient intake at create. Application never receives plaintext
// back; only AuthorizedRender resolves material inside its boundary.
type RenderPromptStore interface {
	Put(context.Context, RenderPromptIntake) error
}

// AuthorizedRenderRequest is the application-facing request for one upstream
// render. It carries only safe identities and the job reference so the
// authorized port can resolve Vault credential + confidential prompt material
// internally. Application never supplies prompt or credential plaintext.
type AuthorizedRenderRequest struct {
	Principal  domain.SecurityPrincipal
	JobRef     domain.JobRef
	AccountID  domain.ProviderAccountID
	AuthMode   domain.AuthMode
	Version    int
	Invocation domain.RenderInvocation
}

// AuthorizedRender is the protected execution boundary for one render attempt.
// Implementations resolve credential (Vault) and prompt (confidential store)
// inside the port and inject them into the Adapter without returning plaintext
// to application code (ADR 0009 CredentialVault.Render / confidential ports).
type AuthorizedRender interface {
	Render(context.Context, AuthorizedRenderRequest) (domain.RenderOutcome, error)
}

// RenderCommand is the safe Adapter invocation after authorization. It never
// carries prompt plaintext or credential material — those are injected only
// inside the AuthorizedRender / Vault boundary, not via this ordinary command.
type RenderCommand struct {
	Principal  domain.SecurityPrincipal
	AccountID  domain.ProviderAccountID
	AuthMode   domain.AuthMode
	Version    int
	Invocation domain.RenderInvocation
}

// RenderAdapter runs one controlled generation/edit/inpaint attempt after the
// authorized boundary has already resolved secrets. Fail-closed when no
// Provider surface is configured. Application code must not call this directly
// with confidential material; it uses AuthorizedRender.
type RenderAdapter interface {
	Render(context.Context, RenderCommand) (domain.RenderOutcome, error)
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
