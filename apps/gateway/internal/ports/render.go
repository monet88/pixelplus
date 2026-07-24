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
	// ErrAccountLeaseUnavailable reports that the hard render_job account lease
	// could not be acquired or is held by another job.
	ErrAccountLeaseUnavailable = errors.New("provider account render lease unavailable")
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
type WorkerLease struct {
	WorkerID domain.Identifier
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
	// ClearLease releases the worker lease on terminal transitions.
	ClearLease bool
}

// ManifestCapture durably freezes the immutable result manifest under the fence.
type ManifestCapture struct {
	JobRef       domain.JobRef
	FencingToken domain.FencingToken
	Manifest     domain.ResultManifest
	Phase        domain.ExecutionPhase
}

// PlacementRequest places one output entry by stable placement key.
type PlacementRequest struct {
	JobRef       domain.JobRef
	FencingToken domain.FencingToken
	EntryID      domain.OutputEntryID
	Asset        domain.Asset
	// Content bytes for the output Asset; empty when resuming an existing placement.
	Content []byte
	// DeliveryStateForced, when non-empty, sets delivery without placing (e.g. storage cap).
	DeliveryStateForced domain.OutputDeliveryState
	FailureClass        string
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
	// BindAccountLease records the hard same-Tenant account lease for the job.
	BindAccountLease(context.Context, domain.JobRef, domain.FencingToken, domain.ProviderAccountID) error
	// AccountLeaseHolder returns the job currently holding a render_job lease
	// on the account, if any.
	AccountLeaseHolder(context.Context, domain.TenantID, domain.ProviderAccountID) (domain.Identifier, bool, error)
	// ReleaseAccountLease clears the hard lease after terminal settlement.
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

// RenderCommand authorizes one controlled upstream render for a job attempt.
// It never carries plaintext credentials; the controlled Adapter proves
// execution counts without decrypting material (mirrors ProbeCommand).
type RenderCommand struct {
	Principal  domain.SecurityPrincipal
	AccountID  domain.ProviderAccountID
	AuthMode   domain.AuthMode
	Version    int
	Invocation domain.RenderInvocation
	// Prompt is transient application-owned content for controlled adapters
	// that synthesize outcomes; it never enters durable job status projections.
	Prompt string
}

// RenderAdapter runs one controlled generation/edit/inpaint attempt.
// Fail-closed when no Provider surface is configured.
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
