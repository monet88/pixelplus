// Package jobs owns queue delivery and worker lifecycle plumbing.
package jobs

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// ErrClosed reports use after the foundation runtime starts closing.
var ErrClosed = errors.New("job runtime is closed")

// ErrAlreadyRunning enforces one consumer owner for this runtime instance.
var ErrAlreadyRunning = errors.New("job runtime is already running")

// Runtime is the standard-library foundation job runtime. It is intentionally
// process-local; durable delivery remains owned by the later job-runtime slice.
//
// Enqueue is nonblocking and idempotent by stable SafeJobReference identity:
// accept-then-MarkQueuePublished crash recovery may re-Enqueue the same ref
// without creating a second logical publication or unbounded wait on a consumer.
// Handler errors re-queue the same SafeJobReference for at-least-once redelivery
// (never a new job identity) and then surface the error to the Run caller.
type Runtime struct {
	done   chan struct{}
	wakeup chan struct{} // buffered 1; signals pending growth

	closing atomic.Bool

	mu      sync.Mutex
	closed  bool
	running bool
	cancel  context.CancelFunc
	runDone chan struct{}
	// pending holds SafeJobReferences awaiting delivery or redelivery.
	pending []ports.SafeJobReference
	// published records logical publication identities (tenant/job) for
	// idempotent Enqueue. Survives successful delivery so recovery re-arms
	// delivery without inventing a new identity.
	published map[string]struct{}
}

// New creates a process-local lifecycle with explicit cancellation.
func New() *Runtime {
	return &Runtime{
		done:      make(chan struct{}),
		wakeup:    make(chan struct{}, 1),
		published: make(map[string]struct{}),
	}
}

// Restore completes the foundation recovery phase. Durable queue recovery is
// added behind this approved port when a later slice selects its implementation.
func (runtime *Runtime) Restore(context.Context) error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.closed {
		return ErrClosed
	}
	return nil
}

func publicationKey(reference ports.SafeJobReference) string {
	return string(reference.TenantID) + "/" + string(reference.JobID)
}

// Enqueue transfers one safe reference without blocking on a Run consumer.
// The same SafeJobReference identity is accepted at most once as a logical
// publication; a later recovery Enqueue re-arms delivery when the ref is not
// already pending (at-least-once), never a replacement identity.
func (runtime *Runtime) Enqueue(ctx context.Context, reference ports.SafeJobReference) (ports.EnqueueReceipt, error) {
	if _, err := reference.JobRef(); err != nil {
		return ports.EnqueueReceipt{}, err
	}
	if runtime.closing.Load() {
		return ports.EnqueueReceipt{}, ErrClosed
	}
	select {
	case <-ctx.Done():
		return ports.EnqueueReceipt{}, ctx.Err()
	case <-runtime.done:
		return ports.EnqueueReceipt{}, ErrClosed
	default:
	}

	runtime.mu.Lock()
	if runtime.closed || runtime.closing.Load() {
		runtime.mu.Unlock()
		return ports.EnqueueReceipt{}, ErrClosed
	}
	if runtime.published == nil {
		runtime.published = make(map[string]struct{})
	}
	key := publicationKey(reference)
	if _, seen := runtime.published[key]; seen {
		// Idempotent accept: re-arm delivery if not already waiting.
		if !runtime.hasPendingLocked(key) {
			runtime.pending = append(runtime.pending, reference)
			runtime.mu.Unlock()
			runtime.signal()
			return ports.EnqueueReceipt{Reference: reference}, nil
		}
		runtime.mu.Unlock()
		return ports.EnqueueReceipt{Reference: reference}, nil
	}
	runtime.published[key] = struct{}{}
	runtime.pending = append(runtime.pending, reference)
	runtime.mu.Unlock()
	runtime.signal()
	return ports.EnqueueReceipt{Reference: reference}, nil
}

func (runtime *Runtime) hasPendingLocked(key string) bool {
	for _, ref := range runtime.pending {
		if publicationKey(ref) == key {
			return true
		}
	}
	return false
}

func (runtime *Runtime) signal() {
	select {
	case runtime.wakeup <- struct{}{}:
	default:
	}
}

// Run consumes safe references until the caller or runtime is canceled.
func (runtime *Runtime) Run(ctx context.Context, handler ports.JobHandler) error {
	runtime.mu.Lock()
	if runtime.closed {
		runtime.mu.Unlock()
		return ErrClosed
	}
	if runtime.running {
		runtime.mu.Unlock()
		return ErrAlreadyRunning
	}

	runContext, cancel := context.WithCancel(ctx)
	runDone := make(chan struct{})
	runtime.running = true
	runtime.cancel = cancel
	runtime.runDone = runDone
	runtime.mu.Unlock()

	defer func() {
		cancel()
		runtime.mu.Lock()
		runtime.running = false
		runtime.cancel = nil
		runtime.runDone = nil
		runtime.mu.Unlock()
		close(runDone)
	}()

	if handler == nil {
		<-runContext.Done()
		return runContext.Err()
	}

	for {
		reference, err := runtime.nextWork(runContext)
		if err != nil {
			return err
		}
		if err := handler(runContext, reference); err != nil {
			// At-least-once: keep the same safe reference for the next Run or
			// loop cycle. Application decides whether a redelivery may render.
			runtime.enqueuePending(reference)
			return err
		}
	}
}

// nextWork prefers redelivery backlog, then waits for wakeup/cancel.
func (runtime *Runtime) nextWork(ctx context.Context) (ports.SafeJobReference, error) {
	for {
		runtime.mu.Lock()
		if len(runtime.pending) > 0 {
			ref := runtime.pending[0]
			runtime.pending = runtime.pending[1:]
			runtime.mu.Unlock()
			return ref, nil
		}
		runtime.mu.Unlock()

		select {
		case <-ctx.Done():
			return ports.SafeJobReference{}, ctx.Err()
		case <-runtime.done:
			return ports.SafeJobReference{}, context.Canceled
		case <-runtime.wakeup:
			// drain and recheck pending
		}
	}
}

func (runtime *Runtime) enqueuePending(reference ports.SafeJobReference) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	// Prefer front so the failed reference is retried before newer work.
	runtime.pending = append([]ports.SafeJobReference{reference}, runtime.pending...)
}

// Close stops delivery and waits for active consumers without leaking a goroutine.
func (runtime *Runtime) Close(ctx context.Context) error {
	runtime.closing.Store(true)
	runtime.mu.Lock()
	if !runtime.closed {
		runtime.closed = true
		close(runtime.done)
	}
	cancel := runtime.cancel
	runDone := runtime.runDone
	runtime.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if runDone == nil {
		return nil
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-runDone:
		return nil
	}
}

var _ ports.JobRuntime = (*Runtime)(nil)
