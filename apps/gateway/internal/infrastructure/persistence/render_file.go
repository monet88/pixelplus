// Package persistence owns physical durable state and atomic transitions.
package persistence

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// renderJobLedgerEntry is one append-only durable Render Job row. Latest row
// per (tenant_id, job_id) wins on replay (#56 durable restart). RenderJob
// never carries prompt plaintext or credential material (ADR 0009), so the
// full struct is safe to persist verbatim.
type renderJobLedgerEntry struct {
	TenantID domain.TenantID  `json:"tenant_id"`
	Job      domain.RenderJob `json:"job"`
}

// FileRenderJobStore is a durable RenderJobStore backed by an append-only
// JSONL ledger protected by an OS advisory lock. The lock anchor may remain on
// disk; the open kernel lock/handle, not file existence, owns exclusion, so a
// crashed process cannot wedge future restarts. Operations synchronize the
// in-process MemoryRenderJobStore with durable rows before delegating the real
// mutation to that already-proven implementation, then append the resulting
// row before returning — so all fencing/attempt/manifest/placement rules stay
// defined in exactly one place.
type FileRenderJobStore struct {
	mu     sync.Mutex
	path   string
	lock   string
	mem    *MemoryRenderJobStore
	cursor ledgerCursor
}

// ledgerCursor tracks the already-validated append-only prefix held in memory.
// File identity detects replacement; a smaller size detects truncation. Either
// condition forces a full rebuild before state is exposed.
type ledgerCursor struct {
	info   os.FileInfo
	offset int64
	lineNo int
}

func (cursor *ledgerCursor) reset() {
	*cursor = ledgerCursor{}
}

func (cursor *ledgerCursor) canContinue(info os.FileInfo) bool {
	return cursor.info != nil && info.Size() >= cursor.offset && os.SameFile(cursor.info, info)
}

func (cursor *ledgerCursor) advance(info os.FileInfo, lines int) {
	cursor.info = info
	cursor.offset = info.Size()
	cursor.lineNo += lines
}

// NewFileRenderJobStore builds a file-backed durable Render Job store.
func NewFileRenderJobStore(path string) *FileRenderJobStore {
	return &FileRenderJobStore{
		path: path,
		lock: path + ".lock",
		mem:  NewMemoryRenderJobStore(),
	}
}

func (store *FileRenderJobStore) acquireLock() (func(), error) {
	dir := filepath.Dir(store.lock)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, err
		}
	}
	// OS advisory lock, not an O_EXCL marker file: the kernel releases it when
	// this process dies, so an abrupt crash cannot leave a lock that blocks every
	// future restart (#56 Standards P1-1).
	return acquireAdvisoryLock(store.lock, "render job store exclusive lock held")
}

// Restore loads persisted job rows. A missing file is empty state; null,
// corrupt, or inconsistent rows fail closed so readiness never opens over
// untrusted durability (health/cooldown spec §7.1-§7.2 posture, applied here).
func (store *FileRenderJobStore) Restore(context.Context) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	unlock, err := store.acquireLock()
	if err != nil {
		return err
	}
	defer unlock()
	return store.reloadLocked()
}

func requireCompleteJSONLRecord(file *os.File, info os.FileInfo) error {
	if info.Size() == 0 {
		return nil
	}
	var last [1]byte
	if _, err := file.ReadAt(last[:], info.Size()-1); err != nil {
		return err
	}
	if last[0] != '\n' {
		return fmt.Errorf("%w: ledger has an incomplete final record", ports.ErrDependencyUnavailable)
	}
	return nil
}

func (store *FileRenderJobStore) reloadLocked() error {
	file, err := os.Open(store.path)
	if err != nil {
		if os.IsNotExist(err) {
			store.mem = NewMemoryRenderJobStore()
			store.cursor.reset()
			return nil
		}
		return err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return err
	}
	if err := requireCompleteJSONLRecord(file, info); err != nil {
		return err
	}
	incremental := store.cursor.canContinue(info)
	if incremental && info.Size() == store.cursor.offset {
		return nil
	}
	startOffset := int64(0)
	baseLine := 0
	next := NewMemoryRenderJobStore()
	if incremental {
		startOffset = store.cursor.offset
		baseLine = store.cursor.lineNo
		next = store.mem
	}
	if _, err := file.Seek(startOffset, 0); err != nil {
		return err
	}

	var entries []renderJobLedgerEntry
	scanner := bufio.NewScanner(file)
	// Manifests/output entries can be moderately large; allow multi-MiB lines.
	scanner.Buffer(make([]byte, 64*1024), 8<<20)
	lineNo := baseLine
	for scanner.Scan() {
		lineNo++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		if string(line) == "null" {
			return fmt.Errorf("%w: render job ledger line %d: null record", ports.ErrDependencyUnavailable, lineNo)
		}
		var entry renderJobLedgerEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return fmt.Errorf("%w: render job ledger line %d: invalid json", ports.ErrDependencyUnavailable, lineNo)
		}
		if entry.TenantID == "" || entry.Job.JobID == "" {
			return fmt.Errorf("%w: render job ledger line %d: missing tenant or job id", ports.ErrDependencyUnavailable, lineNo)
		}
		if entry.Job.TenantID != entry.TenantID {
			return fmt.Errorf("%w: render job ledger line %d: tenant mismatch", ports.ErrDependencyUnavailable, lineNo)
		}
		// Valid JSON can still decode an impossible cross-field state (e.g. a queued
		// job that already sent a Provider payload). Seeding such a row would let
		// ClaimWorker take it as a fresh full claim and authorize a second Provider
		// generation after a crash. Fail closed so readiness never opens over an
		// unsound durable row (#56 Spec P1-2, ADR 0009).
		if err := entry.Job.ValidateDurable(); err != nil {
			return fmt.Errorf("%w: render job ledger line %d: %v", ports.ErrDependencyUnavailable, lineNo, err)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	// Parse and validate the entire new suffix before touching live memory. One bad
	// appended row therefore leaves both the prior memory state and cursor intact.
	for _, entry := range entries {
		next.seedJob(entry.Job)
	}
	store.mem = next
	store.cursor.info = info
	store.cursor.offset = info.Size()
	store.cursor.lineNo = lineNo
	return nil
}

func (store *FileRenderJobStore) appendJobLocked(job domain.RenderJob) (err error) {
	// The Memory store mutates before append. If any durable write step fails,
	// invalidate the cursor so the next operation rebuilds from the last durable
	// prefix instead of treating the unpersisted in-memory mutation as committed.
	defer func() {
		if err != nil {
			store.cursor.reset()
		}
	}()
	dir := filepath.Dir(store.path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	entry := renderJobLedgerEntry{TenantID: job.TenantID, Job: job}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(store.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	store.cursor.advance(info, 1)
	return nil
}

// withReload serializes one operation under the in-process mutex and the
// cross-process exclusive file lock, reloading the ledger first so every
// call observes the latest durable state before delegating to fn.
func (store *FileRenderJobStore) withReload(fn func() error) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	unlock, err := store.acquireLock()
	if err != nil {
		return err
	}
	defer unlock()
	if err := store.reloadLocked(); err != nil {
		return err
	}
	return fn()
}

// Create records one queued job for the owning Tenant.
func (store *FileRenderJobStore) Create(ctx context.Context, creation ports.RenderJobCreation) (domain.RenderJob, error) {
	var result domain.RenderJob
	err := store.withReload(func() error {
		job, err := store.mem.Create(ctx, creation)
		if err != nil {
			return err
		}
		result = job
		return store.appendJobLocked(job)
	})
	return result, err
}

// Visible returns a same-Tenant job or the non-enumerating not-visible error.
func (store *FileRenderJobStore) Visible(ctx context.Context, principal domain.SecurityPrincipal, jobID domain.Identifier) (domain.RenderJob, error) {
	var result domain.RenderJob
	err := store.withReload(func() error {
		var innerErr error
		result, innerErr = store.mem.Visible(ctx, principal, jobID)
		return innerErr
	})
	return result, err
}

// Load loads by JobRef for worker paths.
func (store *FileRenderJobStore) Load(ctx context.Context, ref domain.JobRef) (domain.RenderJob, error) {
	var result domain.RenderJob
	err := store.withReload(func() error {
		var innerErr error
		result, innerErr = store.mem.Load(ctx, ref)
		return innerErr
	})
	return result, err
}

// ClaimWorker atomically claims a queued (or recoverable running) job.
func (store *FileRenderJobStore) ClaimWorker(ctx context.Context, ref domain.JobRef, lease ports.WorkerLease) (ports.WorkerClaim, error) {
	var result ports.WorkerClaim
	err := store.withReload(func() error {
		claim, err := store.mem.ClaimWorker(ctx, ref, lease)
		if err != nil {
			return err
		}
		result = claim
		return store.appendJobLocked(claim.Job)
	})
	return result, err
}

// ObserveAttempt updates the attempt ledger under the current fence.
func (store *FileRenderJobStore) ObserveAttempt(ctx context.Context, observation ports.AttemptObservation) (domain.RenderJob, error) {
	var result domain.RenderJob
	err := store.withReload(func() error {
		job, err := store.mem.ObserveAttempt(ctx, observation)
		if err != nil {
			return err
		}
		result = job
		return store.appendJobLocked(job)
	})
	return result, err
}

// Transition applies a fenced lifecycle transition.
func (store *FileRenderJobStore) Transition(ctx context.Context, transition ports.FencedTransition) (domain.RenderJob, error) {
	var result domain.RenderJob
	err := store.withReload(func() error {
		job, err := store.mem.Transition(ctx, transition)
		if err != nil {
			return err
		}
		result = job
		return store.appendJobLocked(job)
	})
	return result, err
}

// CaptureManifest freezes the immutable result under the fence.
func (store *FileRenderJobStore) CaptureManifest(ctx context.Context, capture ports.ManifestCapture) (domain.RenderJob, error) {
	var result domain.RenderJob
	err := store.withReload(func() error {
		job, err := store.mem.CaptureManifest(ctx, capture)
		if err != nil {
			return err
		}
		result = job
		return store.appendJobLocked(job)
	})
	return result, err
}

// PlaceOutput records an already-committed Asset placement on the job entry.
func (store *FileRenderJobStore) PlaceOutput(ctx context.Context, request ports.PlacementRequest) (ports.PlacementResult, error) {
	var result ports.PlacementResult
	err := store.withReload(func() error {
		placed, err := store.mem.PlaceOutput(ctx, request)
		if err != nil {
			return err
		}
		result = placed
		return store.appendJobLocked(placed.Job)
	})
	return result, err
}

// Cancel applies client cancel rules atomically.
func (store *FileRenderJobStore) Cancel(ctx context.Context, mutation ports.CancelMutation) (domain.RenderJob, error) {
	var result domain.RenderJob
	err := store.withReload(func() error {
		job, err := store.mem.Cancel(ctx, mutation)
		if err != nil {
			return err
		}
		result = job
		return store.appendJobLocked(job)
	})
	return result, err
}

// BindAccountLease records the job→account continuity binding for this job.
func (store *FileRenderJobStore) BindAccountLease(ctx context.Context, ref domain.JobRef, token domain.FencingToken, accountID domain.ProviderAccountID) error {
	return store.withReload(func() error {
		if err := store.mem.BindAccountLease(ctx, ref, token, accountID); err != nil {
			return err
		}
		job, err := store.mem.Load(ctx, ref)
		if err != nil {
			return err
		}
		return store.appendJobLocked(job)
	})
}

// AccountLeaseHolder reports a non-terminal job bound to the account.
func (store *FileRenderJobStore) AccountLeaseHolder(ctx context.Context, tenant domain.TenantID, accountID domain.ProviderAccountID) (domain.Identifier, bool, error) {
	var id domain.Identifier
	var found bool
	err := store.withReload(func() error {
		var innerErr error
		id, found, innerErr = store.mem.AccountLeaseHolder(ctx, tenant, accountID)
		return innerErr
	})
	return id, found, err
}

// ReleaseAccountLease clears the worker fence hold for the job.
func (store *FileRenderJobStore) ReleaseAccountLease(ctx context.Context, ref domain.JobRef, token domain.FencingToken) error {
	return store.withReload(func() error {
		if err := store.mem.ReleaseAccountLease(ctx, ref, token); err != nil {
			return err
		}
		job, err := store.mem.Load(ctx, ref)
		if err != nil {
			return err
		}
		return store.appendJobLocked(job)
	})
}

// MarkQueuePublished records that the SafeJobReference was accepted by the queue.
func (store *FileRenderJobStore) MarkQueuePublished(ctx context.Context, ref domain.JobRef) (domain.RenderJob, error) {
	var result domain.RenderJob
	err := store.withReload(func() error {
		job, err := store.mem.MarkQueuePublished(ctx, ref)
		if err != nil {
			return err
		}
		result = job
		return store.appendJobLocked(job)
	})
	return result, err
}

// ListQueueRecoveryCandidates returns all durable non-terminal jobs.
func (store *FileRenderJobStore) ListQueueRecoveryCandidates(ctx context.Context) ([]domain.RenderJob, error) {
	var out []domain.RenderJob
	err := store.withReload(func() error {
		var innerErr error
		out, innerErr = store.mem.ListQueueRecoveryCandidates(ctx)
		return innerErr
	})
	return out, err
}

// MarkAdmissionSettled records that create-time occupancy Reconcile completed.
func (store *FileRenderJobStore) MarkAdmissionSettled(ctx context.Context, ref domain.JobRef) (domain.RenderJob, error) {
	var result domain.RenderJob
	err := store.withReload(func() error {
		job, err := store.mem.MarkAdmissionSettled(ctx, ref)
		if err != nil {
			return err
		}
		result = job
		return store.appendJobLocked(job)
	})
	return result, err
}

// MarkPromptPurged records that confidential prompt material was deleted.
func (store *FileRenderJobStore) MarkPromptPurged(ctx context.Context, ref domain.JobRef) (domain.RenderJob, error) {
	var result domain.RenderJob
	err := store.withReload(func() error {
		job, err := store.mem.MarkPromptPurged(ctx, ref)
		if err != nil {
			return err
		}
		result = job
		return store.appendJobLocked(job)
	})
	return result, err
}

// RenewWorkerLease extends LeaseExpiresAt and HeartbeatAt under the current fence.
func (store *FileRenderJobStore) RenewWorkerLease(ctx context.Context, ref domain.JobRef, token domain.FencingToken, lease ports.WorkerLease) (domain.RenderJob, error) {
	var result domain.RenderJob
	err := store.withReload(func() error {
		job, err := store.mem.RenewWorkerLease(ctx, ref, token, lease)
		if err != nil {
			return err
		}
		result = job
		return store.appendJobLocked(job)
	})
	return result, err
}

// MarkClaimedAudited records durable fulfillment of the claimed audit obligation.
func (store *FileRenderJobStore) MarkClaimedAudited(ctx context.Context, ref domain.JobRef) (domain.RenderJob, error) {
	var result domain.RenderJob
	err := store.withReload(func() error {
		job, err := store.mem.MarkClaimedAudited(ctx, ref)
		if err != nil {
			return err
		}
		result = job
		return store.appendJobLocked(job)
	})
	return result, err
}

// MarkOutputPlacedAudited records durable fulfillment of the output-placed audit.
func (store *FileRenderJobStore) MarkOutputPlacedAudited(ctx context.Context, ref domain.JobRef) (domain.RenderJob, error) {
	var result domain.RenderJob
	err := store.withReload(func() error {
		job, err := store.mem.MarkOutputPlacedAudited(ctx, ref)
		if err != nil {
			return err
		}
		result = job
		return store.appendJobLocked(job)
	})
	return result, err
}

// MarkTerminalAudited records durable fulfillment of the terminal lifecycle audit.
func (store *FileRenderJobStore) MarkTerminalAudited(ctx context.Context, ref domain.JobRef) (domain.RenderJob, error) {
	var result domain.RenderJob
	err := store.withReload(func() error {
		job, err := store.mem.MarkTerminalAudited(ctx, ref)
		if err != nil {
			return err
		}
		result = job
		return store.appendJobLocked(job)
	})
	return result, err
}

// MarkStagingPurgePending sets or clears the staging Delete debt after placement.
func (store *FileRenderJobStore) MarkStagingPurgePending(ctx context.Context, ref domain.JobRef, pending bool) (domain.RenderJob, error) {
	var result domain.RenderJob
	err := store.withReload(func() error {
		job, err := store.mem.MarkStagingPurgePending(ctx, ref, pending)
		if err != nil {
			return err
		}
		result = job
		return store.appendJobLocked(job)
	})
	return result, err
}

// renderReplayLedgerEntry is one append-only durable replay row. Abandoned
// rows are tombstones: on replay they remove a still-pending (non-terminal)
// record and are a no-op against an already-terminal record, mirroring
// MemoryRenderReplayStore.Abandon's no-steal semantics exactly.
type renderReplayLedgerEntry struct {
	Scope       domain.ReplayScope `json:"scope"`
	Fingerprint domain.Fingerprint `json:"fingerprint"`
	Terminal    bool               `json:"terminal"`
	Job         domain.RenderJob   `json:"job,omitempty"`
	Abandoned   bool               `json:"abandoned,omitempty"`
}

// FileRenderReplayStore is a durable RenderReplayStore backed by an
// append-only JSONL ledger protected by the same crash-safe OS advisory lock as
// FileRenderJobStore and delegating replay semantics to MemoryRenderReplayStore.
type FileRenderReplayStore struct {
	mu     sync.Mutex
	path   string
	lock   string
	mem    *MemoryRenderReplayStore
	cursor ledgerCursor
}

// NewFileRenderReplayStore builds a file-backed durable render replay store.
func NewFileRenderReplayStore(path string) *FileRenderReplayStore {
	return &FileRenderReplayStore{
		path: path,
		lock: path + ".lock",
		mem:  NewMemoryRenderReplayStore(),
	}
}

func (store *FileRenderReplayStore) acquireLock() (func(), error) {
	dir := filepath.Dir(store.lock)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, err
		}
	}
	// OS advisory lock (see FileRenderJobStore.acquireLock): released by the
	// kernel on process death so a crash never wedges every restart (#56 P1-1).
	return acquireAdvisoryLock(store.lock, "render replay store exclusive lock held")
}

// Restore loads persisted replay rows. A missing file is empty state; null or
// corrupt rows fail closed.
func (store *FileRenderReplayStore) Restore(context.Context) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	unlock, err := store.acquireLock()
	if err != nil {
		return err
	}
	defer unlock()
	return store.reloadLocked()
}

func (store *FileRenderReplayStore) reloadLocked() error {
	file, err := os.Open(store.path)
	if err != nil {
		if os.IsNotExist(err) {
			store.mem = NewMemoryRenderReplayStore()
			store.cursor.reset()
			return nil
		}
		return err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return err
	}
	if err := requireCompleteJSONLRecord(file, info); err != nil {
		return err
	}
	incremental := store.cursor.canContinue(info)
	if incremental && info.Size() == store.cursor.offset {
		return nil
	}
	startOffset := int64(0)
	baseLine := 0
	next := NewMemoryRenderReplayStore()
	if incremental {
		startOffset = store.cursor.offset
		baseLine = store.cursor.lineNo
		next = store.mem
	}
	if _, err := file.Seek(startOffset, 0); err != nil {
		return err
	}

	var entries []renderReplayLedgerEntry
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8<<20)
	lineNo := baseLine
	for scanner.Scan() {
		lineNo++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		if string(line) == "null" {
			return fmt.Errorf("%w: render replay ledger line %d: null record", ports.ErrDependencyUnavailable, lineNo)
		}
		var entry renderReplayLedgerEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return fmt.Errorf("%w: render replay ledger line %d: invalid json", ports.ErrDependencyUnavailable, lineNo)
		}
		if !entry.Scope.Valid() {
			return fmt.Errorf("%w: render replay ledger line %d: invalid scope", ports.ErrDependencyUnavailable, lineNo)
		}
		if entry.Terminal && entry.Job.JobID == "" {
			return fmt.Errorf("%w: render replay ledger line %d: terminal record missing job", ports.ErrDependencyUnavailable, lineNo)
		}
		// A carried replay job is an ownership capability. Bind every identity
		// dimension before seeding it: Tenant alone is insufficient because two API
		// keys in one Tenant must not cross-return each other's jobs.
		if entry.Job.JobID != "" && (entry.Job.TenantID != entry.Scope.TenantID ||
			entry.Job.ClientAPIKeyID != entry.Scope.ClientAPIKeyID ||
			entry.Job.IdempotencyKey != entry.Scope.Key ||
			entry.Job.RequestFingerprint != entry.Fingerprint) {
			return fmt.Errorf("%w: render replay ledger line %d: job replay identity mismatch", ports.ErrDependencyUnavailable, lineNo)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.Abandoned {
			next.removeRecord(entry.Scope, entry.Fingerprint)
			continue
		}
		next.seedRecord(entry.Scope, entry.Fingerprint, entry.Terminal, entry.Job)
	}
	store.mem = next
	store.cursor.info = info
	store.cursor.offset = info.Size()
	store.cursor.lineNo = lineNo
	return nil
}

func (store *FileRenderReplayStore) appendLocked(entry renderReplayLedgerEntry) (err error) {
	// Replay memory is also mutated before append; rebuild from disk after any
	// write failure so an unpersisted claim/completion/tombstone cannot leak into
	// a later operation in this process.
	defer func() {
		if err != nil {
			store.cursor.reset()
		}
	}()
	dir := filepath.Dir(store.path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(store.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	store.cursor.advance(info, 1)
	return nil
}

func (store *FileRenderReplayStore) withReload(fn func() error) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	unlock, err := store.acquireLock()
	if err != nil {
		return err
	}
	defer unlock()
	if err := store.reloadLocked(); err != nil {
		return err
	}
	return fn()
}

// Claim atomically binds the scope+key to the fingerprint or resolves a repeat.
// Only the ReplayClaimed outcome mutates state, so only that outcome appends
// a durable row (the pending record survives restart without a job yet).
func (store *FileRenderReplayStore) Claim(ctx context.Context, identity domain.ReplayIdentity) (ports.RenderReplayDecision, error) {
	var decision ports.RenderReplayDecision
	err := store.withReload(func() error {
		var innerErr error
		decision, innerErr = store.mem.Claim(ctx, identity)
		if innerErr != nil {
			return innerErr
		}
		if decision.Outcome != ports.ReplayClaimed {
			return nil
		}
		return store.appendLocked(renderReplayLedgerEntry{
			Scope:       identity.Scope,
			Fingerprint: identity.Fingerprint,
		})
	})
	return decision, err
}

// Complete records the terminal job so later matching replays are stable.
func (store *FileRenderReplayStore) Complete(ctx context.Context, identity domain.ReplayIdentity, result ports.RenderReplayResult) error {
	return store.withReload(func() error {
		if err := store.mem.Complete(ctx, identity, result); err != nil {
			return err
		}
		return store.appendLocked(renderReplayLedgerEntry{
			Scope:       identity.Scope,
			Fingerprint: identity.Fingerprint,
			Terminal:    true,
			Job:         result.Job,
		})
	})
}

// Abandon clears an in-progress claim still owned by this request. The
// tombstone row is appended only when this identity actually owned and
// removed the record — never on a missing/terminal/fingerprint-mismatched
// no-op — so replay's removeRecord (same ownership check) can never delete a
// still-legitimately-claimed record belonging to another owner (no-steal,
// canonical-errors-and-retry-ownership.md §7.4).
func (store *FileRenderReplayStore) Abandon(ctx context.Context, identity domain.ReplayIdentity) error {
	return store.withReload(func() error {
		owned := store.mem.ownsPendingRecord(identity)
		if err := store.mem.Abandon(ctx, identity); err != nil {
			return err
		}
		if !owned {
			return nil
		}
		return store.appendLocked(renderReplayLedgerEntry{
			Scope:       identity.Scope,
			Fingerprint: identity.Fingerprint,
			Abandoned:   true,
		})
	})
}

var (
	_ ports.RenderJobStore    = (*FileRenderJobStore)(nil)
	_ ports.RenderReplayStore = (*FileRenderReplayStore)(nil)
	_ ports.Restorer          = (*FileRenderJobStore)(nil)
	_ ports.Restorer          = (*FileRenderReplayStore)(nil)
)
