package persistence

import (
	"context"
	"sync"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// MemoryRenderJobStore is the production foundation Render Job store. It owns
// durable job/attempt/manifest state, atomic worker claim with fencing, hard
// Provider Account lease binding, and idempotent output placement by stable
// placement key (#14 §§5-8, ADR 0009).
type MemoryRenderJobStore struct {
	mu sync.Mutex
	// byTenant[tenant][jobID] -> job
	byTenant map[domain.TenantID]map[domain.Identifier]domain.RenderJob
	// accountLeases[tenant][accountID] -> jobID holding render_job lease
	accountLeases map[domain.TenantID]map[domain.ProviderAccountID]domain.Identifier
	// placements[placementKey] -> asset id (idempotent placement)
	placements map[string]domain.AssetID
	// nextFence is the global monotonic fencing source for this process.
	nextFence domain.FencingToken
}

// NewMemoryRenderJobStore builds an empty foundation job store.
func NewMemoryRenderJobStore() *MemoryRenderJobStore {
	return &MemoryRenderJobStore{
		byTenant:      make(map[domain.TenantID]map[domain.Identifier]domain.RenderJob),
		accountLeases: make(map[domain.TenantID]map[domain.ProviderAccountID]domain.Identifier),
		placements:    make(map[string]domain.AssetID),
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

// Create persists one queued job for the owning Tenant.
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
		// Recoverable only when not_started and lease not held (worker loss before payload).
		if job.LeaseHeld || job.CommitStatus != domain.CommitNotStarted {
			return ports.WorkerClaim{}, domain.ErrJobNotClaimable
		}
	default:
		return ports.WorkerClaim{}, domain.ErrJobNotClaimable
	}

	store.nextFence++
	job.Lifecycle = domain.JobRunning
	job.ExecutionPhase = domain.PhasePreflight
	job.WorkerFencingToken = store.nextFence
	job.WorkerID = lease.WorkerID
	job.LeaseHeld = true
	job.StateRevision++
	job.Progress = domain.JobProgress{
		Source:    domain.ProgressEstimated,
		Value:     0,
		UpdatedAt: job.UpdatedAt,
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
		job.LeaseHeld = false
		store.clearAccountLeaseLocked(job)
	}
	if transition.To.Terminal() {
		job.TerminalAt = job.UpdatedAt
		job.ExecutionPhase = ""
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
		// Idempotent: same manifest identity is a no-op success.
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
	job.StateRevision++
	store.saveLocked(job)
	return cloneJob(job), nil
}

// PlaceOutput idempotently places one output entry by stable placement key.
func (store *MemoryRenderJobStore) PlaceOutput(_ context.Context, request ports.PlacementRequest) (ports.PlacementResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	job, err := store.loadLocked(request.JobRef)
	if err != nil {
		return ports.PlacementResult{}, err
	}
	// Placement may run with fence during worker execution, or fence 0 during
	// output-only retry after completed (no worker lease).
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

	if request.DeliveryStateForced != "" {
		job.OutputEntries[entryIndex].DeliveryState = request.DeliveryStateForced
		job.OutputEntries[entryIndex].PlacementFailureClass = request.FailureClass
		job.StateRevision++
		store.saveLocked(job)
		return ports.PlacementResult{Job: cloneJob(job), Entry: job.OutputEntries[entryIndex], Created: false}, nil
	}

	if existingAsset, ok := store.placements[key]; ok {
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

	store.placements[key] = request.Asset.ID
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
		store.clearAccountLeaseLocked(job)
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
		// Idempotent no-op.
		return cloneJob(job), nil
	default:
		return domain.RenderJob{}, ports.ErrRenderJobConflict
	}
}

// BindAccountLease records the hard same-Tenant render_job lease.
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
	tenant := domain.TenantID(ref.TenantID)
	leases, ok := store.accountLeases[tenant]
	if !ok {
		leases = make(map[domain.ProviderAccountID]domain.Identifier)
		store.accountLeases[tenant] = leases
	}
	if holder, taken := leases[accountID]; taken && holder != ref.JobID {
		return ports.ErrAccountLeaseUnavailable
	}
	leases[accountID] = ref.JobID
	job.ProviderAccountID = accountID
	job.LeaseHeld = true
	store.saveLocked(job)
	return nil
}

// AccountLeaseHolder returns the job holding a render_job lease, if any.
func (store *MemoryRenderJobStore) AccountLeaseHolder(_ context.Context, tenant domain.TenantID, accountID domain.ProviderAccountID) (domain.Identifier, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	leases, ok := store.accountLeases[tenant]
	if !ok {
		return "", false, nil
	}
	holder, ok := leases[accountID]
	return holder, ok, nil
}

// ReleaseAccountLease clears the hard lease after terminal settlement.
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
	store.clearAccountLeaseLocked(job)
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

func (store *MemoryRenderJobStore) clearAccountLeaseLocked(job domain.RenderJob) {
	leases, ok := store.accountLeases[job.TenantID]
	if !ok {
		return
	}
	if holder, taken := leases[job.ProviderAccountID]; taken && holder == job.JobID {
		delete(leases, job.ProviderAccountID)
	}
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

// MemoryRenderReplayStore is the foundation create-idempotency store for image jobs.
type MemoryRenderReplayStore struct {
	mu      sync.Mutex
	records map[domain.ReplayScope]*renderReplayRecord
}

type renderReplayRecord struct {
	fingerprint domain.Fingerprint
	terminal    bool
	job         domain.RenderJob
}

// NewMemoryRenderReplayStore builds an empty foundation render replay store.
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
	_ ports.RenderReplayStore = (*MemoryRenderReplayStore)(nil)
)
