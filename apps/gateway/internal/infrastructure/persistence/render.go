package persistence

import (
	"context"
	"sync"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// MemoryRenderJobStore is a process-local, controlled Render Job store for
// fixtures and in-process proofs. It is NOT a durable production foundation:
// process restart loses state, so production composition must not default to
// this store (use UnavailableRenderJobStore or a future file-backed ledger).
//
// Responsibilities (#14 §§5-8):
//   - fenced worker claim and lifecycle CAS
//   - attempt/manifest metadata
//   - job→account continuity binding (not an exclusive account mutex)
//   - recording placement results already committed via Asset ports by the
//     application/output worker (this store never Reserve/Commit/Put Assets)
type MemoryRenderJobStore struct {
	mu sync.Mutex
	// byTenant[tenant][jobID] -> job
	byTenant map[domain.TenantID]map[domain.Identifier]domain.RenderJob
	// placementRecord[placementKey] -> asset id already recorded after application
	// Asset placement succeeded. Idempotent re-record only; not Asset storage.
	placementRecord map[string]domain.AssetID
	// nextFence is the process-local monotonic fencing source.
	nextFence domain.FencingToken
}

// NewMemoryRenderJobStore builds an empty process-local job store for controlled
// fixtures. Do not wire this as the silent production default.
func NewMemoryRenderJobStore() *MemoryRenderJobStore {
	return &MemoryRenderJobStore{
		byTenant:        make(map[domain.TenantID]map[domain.Identifier]domain.RenderJob),
		placementRecord: make(map[string]domain.AssetID),
	}
}

func (store *MemoryRenderJobStore) tenantJobs(tenant domain.TenantID) map[domain.Identifier]domain.RenderJob {
	jobs, ok := store.byTenant[tenant]
	if !ok {
		jobs = make(map[domain.Identifier]domain.RenderJob)
		store.byTenant[tenant] = jobs
	}
	return jobs
}

// Create records one queued job for the owning Tenant (process-local).
func (store *MemoryRenderJobStore) Create(_ context.Context, creation ports.RenderJobCreation) (domain.RenderJob, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	job := creation.Job
	job.TenantID = creation.Principal.TenantID
	jobs := store.tenantJobs(job.TenantID)
	if existing, ok := jobs[job.JobID]; ok {
		return existing, nil
	}
	jobs[job.JobID] = cloneJob(job)
	return cloneJob(job), nil
}

// Visible returns a same-Tenant job or the non-enumerating not-visible error.
func (store *MemoryRenderJobStore) Visible(_ context.Context, principal domain.SecurityPrincipal, jobID domain.Identifier) (domain.RenderJob, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	jobs, ok := store.byTenant[principal.TenantID]
	if !ok {
		return domain.RenderJob{}, ports.ErrRenderJobNotVisible
	}
	job, ok := jobs[jobID]
	if !ok {
		return domain.RenderJob{}, ports.ErrRenderJobNotVisible
	}
	return cloneJob(job), nil
}

// Load loads by JobRef for worker paths.
func (store *MemoryRenderJobStore) Load(_ context.Context, ref domain.JobRef) (domain.RenderJob, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	jobs, ok := store.byTenant[domain.TenantID(ref.TenantID)]
	if !ok {
		return domain.RenderJob{}, ports.ErrRenderJobNotVisible
	}
	job, ok := jobs[ref.JobID]
	if !ok {
		return domain.RenderJob{}, ports.ErrRenderJobNotVisible
	}
	return cloneJob(job), nil
}

// ClaimWorker atomically claims a queued (or recoverable running) job.
// lease.Now advances UpdatedAt and progress timestamps.
func (store *MemoryRenderJobStore) ClaimWorker(_ context.Context, ref domain.JobRef, lease ports.WorkerLease) (ports.WorkerClaim, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	job, err := store.loadLocked(ref)
	if err != nil {
		return ports.WorkerClaim{}, err
	}

	// Same worker redelivery with matching fence is idempotent ownership.
	if job.Lifecycle == domain.JobRunning && job.WorkerID == lease.WorkerID && job.LeaseHeld {
		return ports.WorkerClaim{Job: cloneJob(job), FencingToken: job.WorkerFencingToken, AlreadyOwned: true}, nil
	}

	switch job.Lifecycle {
	case domain.JobQueued:
		// take claim
	case domain.JobRunning:
		// Recoverable only when not_started and worker lease not held.
		if job.LeaseHeld || job.CommitStatus != domain.CommitNotStarted {
			return ports.WorkerClaim{}, domain.ErrJobNotClaimable
		}
	default:
		return ports.WorkerClaim{}, domain.ErrJobNotClaimable
	}

	now := lease.Now
	if now.IsZero() {
		// Fail closed on missing clock injection rather than inventing wall time.
		return ports.WorkerClaim{}, ports.ErrDependencyUnavailable
	}

	store.nextFence++
	job.Lifecycle = domain.JobRunning
	job.ExecutionPhase = domain.PhasePreflight
	job.WorkerFencingToken = store.nextFence
	job.WorkerID = lease.WorkerID
	job.LeaseHeld = true
	job.StateRevision++
	job.UpdatedAt = now
	job.Progress = domain.JobProgress{
		Source:    domain.ProgressEstimated,
		Value:     0,
		UpdatedAt: now,
	}
	store.saveLocked(job)
	return ports.WorkerClaim{Job: cloneJob(job), FencingToken: job.WorkerFencingToken}, nil
}

// ObserveAttempt updates the attempt ledger under the current fence.
func (store *MemoryRenderJobStore) ObserveAttempt(_ context.Context, observation ports.AttemptObservation) (domain.RenderJob, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	job, err := store.loadLocked(observation.JobRef)
	if err != nil {
		return domain.RenderJob{}, err
	}
	if err := store.requireFence(job, observation.FencingToken); err != nil {
		return domain.RenderJob{}, err
	}
	if job.Lifecycle.Terminal() {
		return domain.RenderJob{}, ports.ErrRenderJobConflict
	}
	job.Attempt = observation.Attempt
	if observation.Phase.Valid() {
		job.ExecutionPhase = observation.Phase
	}
	if observation.CommitStatus.Valid() {
		job.CommitStatus = observation.CommitStatus
	}
	if observation.Progress.Source.Valid() {
		job.Progress = observation.Progress
	}
	if !observation.Now.IsZero() {
		job.UpdatedAt = observation.Now
	}
	job.StateRevision++
	store.saveLocked(job)
	return cloneJob(job), nil
}

// Transition applies a fenced lifecycle transition.
func (store *MemoryRenderJobStore) Transition(_ context.Context, transition ports.FencedTransition) (domain.RenderJob, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	job, err := store.loadLocked(transition.JobRef)
	if err != nil {
		return domain.RenderJob{}, err
	}
	if err := store.requireFence(job, transition.FencingToken); err != nil {
		return domain.RenderJob{}, err
	}
	if len(transition.RequireStates) > 0 {
		ok := false
		for _, state := range transition.RequireStates {
			if job.Lifecycle == state {
				ok = true
				break
			}
		}
		if !ok {
			return domain.RenderJob{}, ports.ErrRenderJobConflict
		}
	}
	if job.Lifecycle != transition.To {
		if !domain.CanTransition(job.Lifecycle, transition.To) {
			return domain.RenderJob{}, domain.ErrInvalidLifecycleTransition
		}
		job.Lifecycle = transition.To
	}
	if transition.Phase.Valid() {
		job.ExecutionPhase = transition.Phase
	}
	if transition.Progress.Source.Valid() {
		job.Progress = transition.Progress
	}
	if transition.FailureClass != "" {
		job.FailureClass = transition.FailureClass
		job.FailureStage = transition.FailureStage
	}
	if transition.CommitStatus.Valid() {
		job.CommitStatus = transition.CommitStatus
	}
	if transition.ClearLease || transition.To.Terminal() {
		// Worker fence release only; account continuity binding is job-scoped
		// metadata (ProviderAccountID), not an exclusive account mutex.
		job.LeaseHeld = false
	}
	now := transition.Now
	if !now.IsZero() {
		job.UpdatedAt = now
		if transition.To.Terminal() {
			job.TerminalAt = now
			job.ExecutionPhase = ""
		}
	} else if transition.To.Terminal() {
		// Missing Now on terminal transition is a dependency/contract defect.
		return domain.RenderJob{}, ports.ErrDependencyUnavailable
	}
	job.StateRevision++
	store.saveLocked(job)
	return cloneJob(job), nil
}

// CaptureManifest freezes the immutable result under the fence.
func (store *MemoryRenderJobStore) CaptureManifest(_ context.Context, capture ports.ManifestCapture) (domain.RenderJob, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	job, err := store.loadLocked(capture.JobRef)
	if err != nil {
		return domain.RenderJob{}, err
	}
	if err := store.requireFence(job, capture.FencingToken); err != nil {
		return domain.RenderJob{}, err
	}
	if job.Manifest.ID != "" {
		if job.Manifest.ID != capture.Manifest.ID {
			return domain.RenderJob{}, ports.ErrRenderJobConflict
		}
		return cloneJob(job), nil
	}
	job.Manifest = cloneManifest(capture.Manifest)
	job.OutputEntries = cloneEntries(capture.Manifest.Entries)
	job.CommitStatus = domain.CommitCommitted
	job.Attempt.CommitStatus = domain.CommitCommitted
	job.Attempt.ResponseCaptured = true
	if capture.Phase.Valid() {
		job.ExecutionPhase = capture.Phase
	} else {
		job.ExecutionPhase = domain.PhasePlacingOutput
	}
	if !capture.Now.IsZero() {
		job.UpdatedAt = capture.Now
	}
	job.StateRevision++
	store.saveLocked(job)
	return cloneJob(job), nil
}

// PlaceOutput records an already-committed Asset placement on the job entry.
// It does NOT call AssetMetadataStore or AssetContentStore — application owns
// Reserve/Commit/Put, then records the result here under fence + placement key.
func (store *MemoryRenderJobStore) PlaceOutput(_ context.Context, request ports.PlacementRequest) (ports.PlacementResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	job, err := store.loadLocked(request.JobRef)
	if err != nil {
		return ports.PlacementResult{}, err
	}
	if request.FencingToken != 0 {
		if err := store.requireFence(job, request.FencingToken); err != nil {
			return ports.PlacementResult{}, err
		}
	} else if job.Lifecycle != domain.JobCompleted {
		return ports.PlacementResult{}, ports.ErrRenderJobConflict
	}

	key := domain.PlacementKey{
		TenantID:      job.TenantID,
		JobID:         job.JobID,
		OutputEntryID: request.EntryID,
	}.String()

	entryIndex := -1
	for i, entry := range job.OutputEntries {
		if entry.ID == request.EntryID {
			entryIndex = i
			break
		}
	}
	if entryIndex < 0 {
		return ports.PlacementResult{}, ports.ErrRenderJobNotVisible
	}

	if !request.Now.IsZero() {
		job.UpdatedAt = request.Now
	}

	if request.DeliveryStateForced != "" {
		job.OutputEntries[entryIndex].DeliveryState = request.DeliveryStateForced
		job.OutputEntries[entryIndex].PlacementFailureClass = request.FailureClass
		job.StateRevision++
		store.saveLocked(job)
		return ports.PlacementResult{Job: cloneJob(job), Entry: job.OutputEntries[entryIndex], Created: false}, nil
	}

	// Idempotent re-record of a placement already recorded for this key.
	if existingAsset, ok := store.placementRecord[key]; ok {
		job.OutputEntries[entryIndex].AssetID = existingAsset
		job.OutputEntries[entryIndex].DeliveryState = domain.OutputAvailable
		job.OutputEntries[entryIndex].PlacementFailureClass = ""
		if request.Asset.ContentType != "" {
			job.OutputEntries[entryIndex].ContentType = request.Asset.ContentType
			job.OutputEntries[entryIndex].ByteSize = request.Asset.ByteSize
			job.OutputEntries[entryIndex].Checksum = request.Asset.Checksum
		}
		job.StateRevision++
		store.saveLocked(job)
		return ports.PlacementResult{Job: cloneJob(job), Entry: job.OutputEntries[entryIndex], Created: false}, nil
	}

	// First record after application Asset ports committed the object.
	if request.Asset.ID == "" {
		return ports.PlacementResult{}, ports.ErrRenderJobConflict
	}
	store.placementRecord[key] = request.Asset.ID
	job.OutputEntries[entryIndex].AssetID = request.Asset.ID
	job.OutputEntries[entryIndex].DeliveryState = domain.OutputAvailable
	job.OutputEntries[entryIndex].ContentType = request.Asset.ContentType
	job.OutputEntries[entryIndex].ByteSize = request.Asset.ByteSize
	job.OutputEntries[entryIndex].Checksum = request.Asset.Checksum
	job.OutputEntries[entryIndex].PlacementFailureClass = ""
	job.StateRevision++
	store.saveLocked(job)
	return ports.PlacementResult{Job: cloneJob(job), Entry: job.OutputEntries[entryIndex], Created: true}, nil
}

// Cancel applies client cancel rules: queued→canceled, running→cancel_requested,
// terminal→idempotent no-op.
func (store *MemoryRenderJobStore) Cancel(_ context.Context, mutation ports.CancelMutation) (domain.RenderJob, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	jobs, ok := store.byTenant[mutation.Principal.TenantID]
	if !ok {
		return domain.RenderJob{}, ports.ErrRenderJobNotVisible
	}
	job, ok := jobs[mutation.JobID]
	if !ok {
		return domain.RenderJob{}, ports.ErrRenderJobNotVisible
	}

	if mutation.FencingToken != 0 {
		if err := store.requireFence(job, mutation.FencingToken); err != nil {
			return domain.RenderJob{}, err
		}
	}

	switch job.Lifecycle {
	case domain.JobQueued:
		job.Lifecycle = domain.JobCanceled
		job.CancelRequestedAt = mutation.Now
		job.CancelRequestedBy = mutation.RequestedBy
		job.TerminalAt = mutation.Now
		job.UpdatedAt = mutation.Now
		job.ExecutionPhase = ""
		job.StateRevision++
		job.LeaseHeld = false
		store.saveLocked(job)
		return cloneJob(job), nil
	case domain.JobRunning:
		job.Lifecycle = domain.JobCancelRequested
		job.CancelRequestedAt = mutation.Now
		job.CancelRequestedBy = mutation.RequestedBy
		job.UpdatedAt = mutation.Now
		job.StateRevision++
		store.saveLocked(job)
		return cloneJob(job), nil
	case domain.JobCancelRequested, domain.JobCanceled, domain.JobFailed, domain.JobCompleted:
		return cloneJob(job), nil
	default:
		return domain.RenderJob{}, ports.ErrRenderJobConflict
	}
}

// BindAccountLease records the job→account hard continuity binding for this
// job's execution. It is NOT an exclusive account-wide mutex: multiple jobs may
// bind the same account subject to admission/concurrency (#11 §5.2).
func (store *MemoryRenderJobStore) BindAccountLease(_ context.Context, ref domain.JobRef, token domain.FencingToken, accountID domain.ProviderAccountID) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	job, err := store.loadLocked(ref)
	if err != nil {
		return err
	}
	if err := store.requireFence(job, token); err != nil {
		return err
	}
	// Continuity: bind or reaffirm the job's selected account. Never reject
	// because another job uses the same account.
	if job.ProviderAccountID != "" && job.ProviderAccountID != accountID {
		// Silent account hop mid-job is forbidden; fail the bind.
		return ports.ErrRenderJobConflict
	}
	job.ProviderAccountID = accountID
	store.saveLocked(job)
	return nil
}

// AccountLeaseHolder reports whether this job has a continuity binding to the
// account (job-scoped). It does not mean exclusive ownership of the account.
func (store *MemoryRenderJobStore) AccountLeaseHolder(_ context.Context, tenant domain.TenantID, accountID domain.ProviderAccountID) (domain.Identifier, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	jobs, ok := store.byTenant[tenant]
	if !ok {
		return "", false, nil
	}
	// Return any non-terminal job currently bound to the account for diagnostics.
	// Multiple may exist; this is not an exclusion oracle.
	for id, job := range jobs {
		if job.ProviderAccountID == accountID && !job.Lifecycle.Terminal() {
			return id, true, nil
		}
	}
	return "", false, nil
}

// ReleaseAccountLease clears the worker fence hold for the job. Account
// continuity (ProviderAccountID) remains for audit; exclusivity is never held.
func (store *MemoryRenderJobStore) ReleaseAccountLease(_ context.Context, ref domain.JobRef, token domain.FencingToken) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	job, err := store.loadLocked(ref)
	if err != nil {
		return err
	}
	if token != 0 {
		if err := store.requireFence(job, token); err != nil {
			return err
		}
	}
	job.LeaseHeld = false
	store.saveLocked(job)
	return nil
}

func (store *MemoryRenderJobStore) loadLocked(ref domain.JobRef) (domain.RenderJob, error) {
	jobs, ok := store.byTenant[domain.TenantID(ref.TenantID)]
	if !ok {
		return domain.RenderJob{}, ports.ErrRenderJobNotVisible
	}
	job, ok := jobs[ref.JobID]
	if !ok {
		return domain.RenderJob{}, ports.ErrRenderJobNotVisible
	}
	return job, nil
}

func (store *MemoryRenderJobStore) saveLocked(job domain.RenderJob) {
	store.tenantJobs(job.TenantID)[job.JobID] = job
}

func (store *MemoryRenderJobStore) requireFence(job domain.RenderJob, token domain.FencingToken) error {
	if !job.LeaseHeld || job.WorkerFencingToken != token {
		return domain.ErrStaleFence
	}
	return nil
}

func cloneJob(job domain.RenderJob) domain.RenderJob {
	out := job
	if job.InputAssetIDs != nil {
		out.InputAssetIDs = append([]domain.AssetID(nil), job.InputAssetIDs...)
	}
	out.OutputEntries = cloneEntries(job.OutputEntries)
	out.Manifest = cloneManifest(job.Manifest)
	return out
}

func cloneEntries(entries []domain.OutputEntry) []domain.OutputEntry {
	if entries == nil {
		return nil
	}
	out := make([]domain.OutputEntry, len(entries))
	copy(out, entries)
	return out
}

func cloneManifest(manifest domain.ResultManifest) domain.ResultManifest {
	out := manifest
	out.Entries = cloneEntries(manifest.Entries)
	return out
}

// UnavailableRenderJobStore is the production fail-closed substitute when no
// durable Render Job store is configured. Every operation returns
// ErrDependencyUnavailable so product traffic cannot treat an empty process-
// local map as durable job state (review finding #6).
type UnavailableRenderJobStore struct{}

// NewUnavailableRenderJobStore builds the fail-closed job store.
func NewUnavailableRenderJobStore() *UnavailableRenderJobStore {
	return &UnavailableRenderJobStore{}
}

func (*UnavailableRenderJobStore) Create(context.Context, ports.RenderJobCreation) (domain.RenderJob, error) {
	return domain.RenderJob{}, ports.ErrDependencyUnavailable
}
func (*UnavailableRenderJobStore) Visible(context.Context, domain.SecurityPrincipal, domain.Identifier) (domain.RenderJob, error) {
	return domain.RenderJob{}, ports.ErrDependencyUnavailable
}
func (*UnavailableRenderJobStore) Load(context.Context, domain.JobRef) (domain.RenderJob, error) {
	return domain.RenderJob{}, ports.ErrDependencyUnavailable
}
func (*UnavailableRenderJobStore) ClaimWorker(context.Context, domain.JobRef, ports.WorkerLease) (ports.WorkerClaim, error) {
	return ports.WorkerClaim{}, ports.ErrDependencyUnavailable
}
func (*UnavailableRenderJobStore) ObserveAttempt(context.Context, ports.AttemptObservation) (domain.RenderJob, error) {
	return domain.RenderJob{}, ports.ErrDependencyUnavailable
}
func (*UnavailableRenderJobStore) Transition(context.Context, ports.FencedTransition) (domain.RenderJob, error) {
	return domain.RenderJob{}, ports.ErrDependencyUnavailable
}
func (*UnavailableRenderJobStore) CaptureManifest(context.Context, ports.ManifestCapture) (domain.RenderJob, error) {
	return domain.RenderJob{}, ports.ErrDependencyUnavailable
}
func (*UnavailableRenderJobStore) PlaceOutput(context.Context, ports.PlacementRequest) (ports.PlacementResult, error) {
	return ports.PlacementResult{}, ports.ErrDependencyUnavailable
}
func (*UnavailableRenderJobStore) Cancel(context.Context, ports.CancelMutation) (domain.RenderJob, error) {
	return domain.RenderJob{}, ports.ErrDependencyUnavailable
}
func (*UnavailableRenderJobStore) BindAccountLease(context.Context, domain.JobRef, domain.FencingToken, domain.ProviderAccountID) error {
	return ports.ErrDependencyUnavailable
}
func (*UnavailableRenderJobStore) AccountLeaseHolder(context.Context, domain.TenantID, domain.ProviderAccountID) (domain.Identifier, bool, error) {
	return "", false, ports.ErrDependencyUnavailable
}
func (*UnavailableRenderJobStore) ReleaseAccountLease(context.Context, domain.JobRef, domain.FencingToken) error {
	return ports.ErrDependencyUnavailable
}

// MemoryRenderReplayStore is the process-local create-idempotency store for
// image jobs (controlled/in-process; not restart-durable).
type MemoryRenderReplayStore struct {
	mu      sync.Mutex
	records map[domain.ReplayScope]*renderReplayRecord
}

type renderReplayRecord struct {
	fingerprint domain.Fingerprint
	terminal    bool
	job         domain.RenderJob
}

// NewMemoryRenderReplayStore builds an empty process-local render replay store.
func NewMemoryRenderReplayStore() *MemoryRenderReplayStore {
	return &MemoryRenderReplayStore{records: make(map[domain.ReplayScope]*renderReplayRecord)}
}

// Claim atomically binds the scope+key to the fingerprint or resolves a repeat.
func (store *MemoryRenderReplayStore) Claim(_ context.Context, identity domain.ReplayIdentity) (ports.RenderReplayDecision, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	existing, ok := store.records[identity.Scope]
	if !ok {
		store.records[identity.Scope] = &renderReplayRecord{fingerprint: identity.Fingerprint}
		return ports.RenderReplayDecision{Outcome: ports.ReplayClaimed}, nil
	}
	if existing.fingerprint != identity.Fingerprint {
		return ports.RenderReplayDecision{Outcome: ports.ReplayConflict}, nil
	}
	if existing.terminal {
		return ports.RenderReplayDecision{Outcome: ports.ReplayTerminal, TerminalJob: existing.job}, nil
	}
	return ports.RenderReplayDecision{Outcome: ports.ReplayInProgress}, nil
}

// Complete records the terminal job so later matching replays are stable.
func (store *MemoryRenderReplayStore) Complete(_ context.Context, identity domain.ReplayIdentity, result ports.RenderReplayResult) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	record, ok := store.records[identity.Scope]
	if !ok {
		record = &renderReplayRecord{fingerprint: identity.Fingerprint}
		store.records[identity.Scope] = record
	}
	record.terminal = true
	record.job = result.Job
	return nil
}

// Abandon clears an in-progress claim still owned by this request.
func (store *MemoryRenderReplayStore) Abandon(_ context.Context, identity domain.ReplayIdentity) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	record, ok := store.records[identity.Scope]
	if !ok {
		return nil
	}
	if record.terminal || record.fingerprint != identity.Fingerprint {
		return nil
	}
	delete(store.records, identity.Scope)
	return nil
}

var (
	_ ports.RenderJobStore    = (*MemoryRenderJobStore)(nil)
	_ ports.RenderJobStore    = (*UnavailableRenderJobStore)(nil)
	_ ports.RenderReplayStore = (*MemoryRenderReplayStore)(nil)
)
