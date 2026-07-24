package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// RenderOperation names the immutable image operation bound to one Render Job.
type RenderOperation string

// Frozen image operation vocabulary (OpenAPI / #14 lifecycle).
const (
	RenderOpImageGeneration RenderOperation = "image_generation"
	RenderOpImageEdit       RenderOperation = "image_edit"
	RenderOpInpaint         RenderOperation = "inpaint"
)

// Valid reports whether the operation is one of the three locked image ops.
func (operation RenderOperation) Valid() bool {
	switch operation {
	case RenderOpImageGeneration, RenderOpImageEdit, RenderOpInpaint:
		return true
	default:
		return false
	}
}

// CapabilityOperation maps the render operation onto the capability vocabulary.
func (operation RenderOperation) CapabilityOperation() CapabilityOperation {
	switch operation {
	case RenderOpImageGeneration:
		return CapabilityOpImageGeneration
	case RenderOpImageEdit:
		return CapabilityOpImageEdit
	case RenderOpInpaint:
		return CapabilityOpInpaint
	default:
		return ""
	}
}

// RequiredScope returns the Client API Key scope required to create this op.
func (operation RenderOperation) RequiredScope() Scope {
	switch operation {
	case RenderOpImageGeneration:
		return ScopeImagesGenerate
	case RenderOpImageEdit, RenderOpInpaint:
		return ScopeImagesEdit
	default:
		return ""
	}
}

// JobLifecycleState is the publicly observable Render Job lifecycle.
type JobLifecycleState string

// Exactly six lifecycle states (#14 §4.1).
const (
	JobQueued          JobLifecycleState = "queued"
	JobRunning         JobLifecycleState = "running"
	JobCancelRequested JobLifecycleState = "cancel_requested"
	JobCanceled        JobLifecycleState = "canceled"
	JobFailed          JobLifecycleState = "failed"
	JobCompleted       JobLifecycleState = "completed"
)

// Terminal reports whether the lifecycle state is immutable after publication.
func (state JobLifecycleState) Terminal() bool {
	switch state {
	case JobCanceled, JobFailed, JobCompleted:
		return true
	default:
		return false
	}
}

// Valid reports whether the lifecycle value is in the frozen enum.
func (state JobLifecycleState) Valid() bool {
	switch state {
	case JobQueued, JobRunning, JobCancelRequested, JobCanceled, JobFailed, JobCompleted:
		return true
	default:
		return false
	}
}

// ExecutionPhase is the durable sub-phase while the job is non-terminal.
type ExecutionPhase string

// Frozen execution phases (#14 §3.1).
const (
	PhasePreflight       ExecutionPhase = "preflight"
	PhaseUpstream        ExecutionPhase = "upstream"
	PhaseCapturingResult ExecutionPhase = "capturing_result"
	PhasePlacingOutput   ExecutionPhase = "placing_output"
)

// Valid reports whether the phase is in the frozen enum.
func (phase ExecutionPhase) Valid() bool {
	switch phase {
	case PhasePreflight, PhaseUpstream, PhaseCapturingResult, PhasePlacingOutput:
		return true
	default:
		return false
	}
}

// CommitStatus is the durable attempt commit certainty (#14 §6.2).
type CommitStatus string

// Attempt commit statuses. unknown is fail-closed and never treated as not_committed.
const (
	CommitNotStarted   CommitStatus = "not_started"
	CommitNotCommitted CommitStatus = "not_committed"
	CommitCommitted    CommitStatus = "committed"
	CommitUnknown      CommitStatus = "unknown"
)

// ForbidsReplacement reports whether a new upstream render is forbidden.
func (status CommitStatus) ForbidsReplacement() bool {
	return status == CommitCommitted || status == CommitUnknown
}

// Valid reports whether the commit status is in the frozen enum.
func (status CommitStatus) Valid() bool {
	switch status {
	case CommitNotStarted, CommitNotCommitted, CommitCommitted, CommitUnknown:
		return true
	default:
		return false
	}
}

// ProgressSource labels how progress was obtained (#14 §7.1).
type ProgressSource string

// Progress sources. The Gateway never invents pixel/token precision.
const (
	ProgressReported  ProgressSource = "reported"
	ProgressEstimated ProgressSource = "estimated"
	ProgressUnknown   ProgressSource = "unknown"
)

// Valid reports whether the progress source is in the frozen enum.
func (source ProgressSource) Valid() bool {
	switch source {
	case ProgressReported, ProgressEstimated, ProgressUnknown:
		return true
	default:
		return false
	}
}

// OutputDeliveryState is per-entry delivery independent of job lifecycle.
type OutputDeliveryState string

// Frozen output delivery states (#14 §8.2).
const (
	OutputPending   OutputDeliveryState = "pending"
	OutputAvailable OutputDeliveryState = "available"
	OutputExpired   OutputDeliveryState = "expired"
	OutputFailed    OutputDeliveryState = "failed"
)

// Valid reports whether the delivery state is in the frozen enum.
func (state OutputDeliveryState) Valid() bool {
	switch state {
	case OutputPending, OutputAvailable, OutputExpired, OutputFailed:
		return true
	default:
		return false
	}
}

// FencingToken is a monotonically increasing worker lease generation.
type FencingToken int64

// OutputEntryID is the stable per-job output entry identity.
type OutputEntryID string

// ResultManifestID is the immutable captured-result identity.
type ResultManifestID string

// AttemptID is the immutable upstream attempt identity.
type AttemptID string

// JobProgress is the honest progress projection (#14 §7.1).
type JobProgress struct {
	Source ProgressSource
	// Value is optional estimated progress in [0,100]. Negative means unknown.
	Value     int
	UpdatedAt Timestamp
}

// OutputEntry is one ordered logical generated image in the result manifest.
type OutputEntry struct {
	ID            OutputEntryID
	Position      int
	DeliveryState OutputDeliveryState
	AssetID       AssetID
	ContentType   string
	ByteSize      int64
	Checksum      string
	// PlacementFailureClass is a safe class when delivery cannot proceed.
	PlacementFailureClass string
}

// ResultManifest is the immutable durable description of Provider outputs.
type ResultManifest struct {
	ID        ResultManifestID
	AttemptID AttemptID
	Entries   []OutputEntry
	// StagingChecksum is a non-secret digest of staged result material.
	StagingChecksum string
	CapturedAt      Timestamp
}

// UpstreamAttempt is the single attempt ledger for one Render Job.
type UpstreamAttempt struct {
	ID                AttemptID
	ProviderAccountID ProviderAccountID
	CredentialVersion int
	CommitStatus      CommitStatus
	PayloadSent       bool
	ResponseCaptured  bool
	Sequence          int
	CreatedAt         Timestamp
	UpdatedAt         Timestamp
}

// RenderJob is the Tenant-owned durable unit of one image request (#14 §3).
// Prompt plaintext is never stored here (ADR 0009 TenantConfidentialStore /
// confidential port). Only a non-secret PromptDigest may appear on the job
// metadata row for fingerprint binding and audit-safe correlation.
type RenderJob struct {
	TenantID           TenantID
	JobID              Identifier
	ClientAPIKeyID     ClientAPIKeyID
	IdempotencyKey     string
	RequestFingerprint Fingerprint
	Operation          RenderOperation
	Model              string
	// PromptDigest is the non-secret SHA-256 hex digest of the create-time
	// prompt. It is not reversible and is safe on status projections that omit
	// it; confidential material lives only in the protected confidential port.
	PromptDigest       string
	InputAssetIDs      []AssetID
	MaskAssetID        AssetID
	ProviderAccountID  ProviderAccountID
	CredentialVersion  int
	Lifecycle          JobLifecycleState
	ExecutionPhase     ExecutionPhase
	StateRevision      int64
	Progress           JobProgress
	Attempt            UpstreamAttempt
	Manifest           ResultManifest
	OutputEntries      []OutputEntry
	WorkerFencingToken FencingToken
	WorkerID           Identifier
	LeaseHeld          bool
	// LeaseExpiresAt is the worker fence expiry. After expiry, a new worker may
	// reclaim only when CommitStatus is not_started and PayloadSent is false
	// (#14 §6.4). Expiry never authorizes a second generation after payload.
	LeaseExpiresAt Timestamp
	// HeartbeatAt is the last durable worker lease renewal under the current fence.
	// Long Adapter calls renew via RenewWorkerLease so expiry recovery does not
	// steal an active healthy worker (#14 §6.4).
	HeartbeatAt       Timestamp
	CancelRequestedAt Timestamp
	CancelRequestedBy ClientAPIKeyID
	FailureStage      FailureStage
	FailureClass      ErrorCode
	CommitStatus      CommitStatus
	// QueuePublished is true after the SafeJobReference was accepted by a queue
	// at least once. It is a historical acceptance marker, not proof that a
	// process-local pending item survived restart. Startup recovery re-arms every
	// nonterminal job's stable ref even when this flag is true (#14 §3.3).
	QueuePublished bool
	// AdmissionSettled is true after create-time occupancy Reconcile was
	// durably recorded. Reconcile itself is keyed-idempotent; the marker stops
	// unnecessary work and proves settlement for operators (#8 §7.4).
	AdmissionSettled bool
	// PromptPurged is true after confidential prompt material was deleted for
	// this job. Terminal visibility may precede purge; redelivery retries purge
	// without Provider render when this remains false (ADR 0009).
	PromptPurged bool
	// ClaimedAudited is true after the worker claimed audit was successfully
	// recorded. Terminal/redelivery paths retry until marked (no Provider re-render).
	ClaimedAudited bool
	// OutputPlacedAudited is true after the output-placed audit was recorded for
	// durable placement. Placement may succeed before audit; redelivery retries.
	OutputPlacedAudited bool
	// TerminalAudited is true after completed/failed/canceled audit was recorded.
	// Transition may succeed before audit; redelivery retries without re-render.
	TerminalAudited bool
	// StagingPurgePending is true when placement committed but staging Delete has
	// not yet succeeded. Redelivery retries purge only (stable Asset placement).
	StagingPurgePending bool
	CreatedAt           Timestamp
	UpdatedAt           Timestamp
	TerminalAt          Timestamp
}

// JobRef returns the durable ownership identity shared with workers.
func (job RenderJob) JobRef() JobRef {
	return JobRef{TenantID: Identifier(job.TenantID), JobID: job.JobID}
}

// PlacementKey is the stable output placement identity (#14 §8.3).
type PlacementKey struct {
	TenantID      TenantID
	JobID         Identifier
	OutputEntryID OutputEntryID
}

// String returns a non-secret stable key form for maps/logs.
func (key PlacementKey) String() string {
	return string(key.TenantID) + "/" + string(key.JobID) + "/" + string(key.OutputEntryID)
}

// ErrInvalidLifecycleTransition reports a forbidden state machine edge.
var ErrInvalidLifecycleTransition = errors.New("invalid render job lifecycle transition")

// ErrStaleFence reports a mutation carrying a superseded fencing token.
var ErrStaleFence = errors.New("stale render job fencing token")

// ErrJobNotClaimable reports that claim lost the atomic condition.
var ErrJobNotClaimable = errors.New("render job is not claimable")

// CanTransition reports whether from→to is an allowed lifecycle edge (§4.2).
//
// Spec edges only:
//
//	queued → running | canceled
//	running → running | cancel_requested | completed | failed
//	cancel_requested → canceled | failed
//
// running → canceled is forbidden (must first persist cancel_requested).
// cancel_requested → completed is forbidden (completion races from running
// before the cancel CAS; once cancel wins, only canceled/failed remain).
func CanTransition(from, to JobLifecycleState) bool {
	if from == to && from.Terminal() {
		return true // idempotent terminal no-op
	}
	switch from {
	case JobQueued:
		return to == JobRunning || to == JobCanceled
	case JobRunning:
		return to == JobRunning || to == JobCancelRequested || to == JobCompleted || to == JobFailed
	case JobCancelRequested:
		return to == JobCanceled || to == JobFailed
	case JobCanceled, JobFailed, JobCompleted:
		return false
	default:
		return false
	}
}

// NewQueuedRenderJob builds a newly admitted queued job with immutable inputs.
// promptDigest is the non-secret digest of the create-time prompt; callers must
// never pass raw prompt plaintext into this constructor.
func NewQueuedRenderJob(
	jobID Identifier,
	tenantID TenantID,
	keyID ClientAPIKeyID,
	operation RenderOperation,
	model string,
	promptDigest string,
	inputs []AssetID,
	mask AssetID,
	accountID ProviderAccountID,
	credentialVersion int,
	fingerprint Fingerprint,
	idempotencyKey string,
	now Timestamp,
) RenderJob {
	return RenderJob{
		TenantID:           tenantID,
		JobID:              jobID,
		ClientAPIKeyID:     keyID,
		IdempotencyKey:     idempotencyKey,
		RequestFingerprint: fingerprint,
		Operation:          operation,
		Model:              model,
		PromptDigest:       promptDigest,
		InputAssetIDs:      append([]AssetID(nil), inputs...),
		MaskAssetID:        mask,
		ProviderAccountID:  accountID,
		CredentialVersion:  credentialVersion,
		Lifecycle:          JobQueued,
		ExecutionPhase:     PhasePreflight,
		StateRevision:      1,
		Progress: JobProgress{
			Source:    ProgressUnknown,
			Value:     -1,
			UpdatedAt: now,
		},
		CommitStatus: CommitNotStarted,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

// PromptDigest and create fingerprints are produced by ports.RenderDigester
// (keyed HMAC) in infrastructure. Domain no longer exposes unkeyed SHA-256 of
// prompt material — that would be a dictionary/correlation oracle (#54 P1-9).

// NewOutputEntryID builds a stable entry id from job and position.
func NewOutputEntryID(jobID Identifier, position int) OutputEntryID {
	return OutputEntryID(fmt.Sprintf("%s_out_%d", jobID, position))
}

// NewAttemptID builds a stable attempt id for a job sequence.
func NewAttemptID(jobID Identifier, sequence int) AttemptID {
	return AttemptID(string(jobID) + "_attempt_" + strconv.Itoa(sequence))
}

// NewResultManifestID builds a stable manifest id from attempt identity.
func NewResultManifestID(attemptID AttemptID) ResultManifestID {
	return ResultManifestID(string(attemptID) + "_manifest")
}

// StagingChecksum digests non-secret staged bytes for placement identity.
func StagingChecksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// StableOutputAssetID derives a deterministic Asset id from the placement key
// so placement retries claim at most one Asset/reservation (#14 §8.3, #13).
func StableOutputAssetID(tenant TenantID, jobID Identifier, entryID OutputEntryID) AssetID {
	const separator = "\x1f"
	h := sha256.New()
	_, _ = h.Write([]byte(tenant))
	_, _ = h.Write([]byte(separator))
	_, _ = h.Write([]byte(jobID))
	_, _ = h.Write([]byte(separator))
	_, _ = h.Write([]byte(entryID))
	return AssetID("asset_" + hex.EncodeToString(h.Sum(nil))[:32])
}

// DefaultOutputContentType is the MVP generated image media type.
const DefaultOutputContentType = ContentTypePNG

// RenderOutcomeClass classifies a controlled Provider render result.
// Storage-cap is not a Provider outcome; it is an output placement/delivery
// failure class handled after capture (#14 §8.3).
type RenderOutcomeClass string

// Controlled Provider render outcome classes for the Adapter port.
const (
	RenderOutcomeSuccess      RenderOutcomeClass = "success"
	RenderOutcomeNotCommitted RenderOutcomeClass = "not_committed"
	RenderOutcomeCommitted    RenderOutcomeClass = "committed"
	RenderOutcomeUnknown      RenderOutcomeClass = "unknown"
)

// RenderInvocation is the safe, non-secret render request the Adapter receives.
// Prompt and Asset bytes are not carried here; bytes are captured only through
// the protected staging sink (ADR 0009 RenderStagingStore).
type RenderInvocation struct {
	TenantID          TenantID
	JobID             Identifier
	AttemptID         AttemptID
	Operation         RenderOperation
	Model             string
	ProviderAccountID ProviderAccountID
	CredentialVersion int
}

// RenderOutcome is the safe classification returned to application code.
// It never carries output bytes — only commit certainty and the immutable
// result manifest metadata already staged under RenderStagingStore.
type RenderOutcome struct {
	Class    RenderOutcomeClass
	Commit   CommitStatus
	Manifest ResultManifest
}

// NowTimestamp is a convenience for tests and pure helpers.
func NowTimestamp(now time.Time) Timestamp {
	return NewTimestamp(now)
}
