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
// Handler errors re-queue the same SafeJobReference for at-least-once redelivery
// (never a new job identity) and then surface the error to the Run caller.
type Runtime struct {
	queue chan enqueueRequest
	done  chan struct{}

	closing atomic.Bool

	mu      sync.Mutex
	closed  bool
	running bool
	cancel  context.CancelFunc
	runDone chan struct{}
	// pending holds SafeJobReferences awaiting redelivery after a handler
	// failure. Survives Run restarts on the same Runtime instance.
	pending []ports.SafeJobReference
}

type enqueueRequest struct {
	reference ports.SafeJobReference
	accepted  chan error
}

// New creates a process-local lifecycle with explicit cancellation.
func New() *Runtime {
	return &Runtime{
		queue: make(chan enqueueRequest),
		done:  make(chan struct{}),
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

// Enqueue transfers one safe reference or fails when the runtime is closing.
func (runtime *Runtime) Enqueue(ctx context.Context, reference ports.SafeJobReference) (ports.EnqueueReceipt, error) {
	if _, err := reference.JobRef(); err != nil {
		return ports.EnqueueReceipt{}, err
	}
	if runtime.closing.Load() {
		return ports.EnqueueReceipt{}, ErrClosed
	}
	request := enqueueRequest{
		reference: reference,
		accepted:  make(chan error, 1),
	}

	select {
	case <-ctx.Done():
		return ports.EnqueueReceipt{}, ctx.Err()
	case <-runtime.done:
		return ports.EnqueueReceipt{}, ErrClosed
	case runtime.queue <- request:
	}

	if err := <-request.accepted; err != nil {
		return ports.EnqueueReceipt{}, err
	}
	return ports.EnqueueReceipt{Reference: reference}, nil
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

	for {
		reference, accepted, fromPending, err := runtime.nextWork(runContext)
		if err != nil {
			return err
		}
		if accepted != nil {
			runtime.mu.Lock()
			closing := runtime.closed || runtime.closing.Load()
			runtime.mu.Unlock()
			if closing {
				accepted <- ErrClosed
				return context.Canceled
			}
			accepted <- nil
		}
		if err := handler(runContext, reference); err != nil {
			// At-least-once: keep the same safe reference for the next Run or
			// loop cycle. Application decides whether a redelivery may render.
			if !fromPending {
				runtime.enqueuePending(reference)
			} else {
				// Failed redelivery stays pending for a subsequent Run.
				runtime.enqueuePending(reference)
			}
			return err
		}
	}
}

// nextWork prefers redelivery backlog, then the inbound queue.
func (runtime *Runtime) nextWork(ctx context.Context) (ports.SafeJobReference, chan error, bool, error) {
	runtime.mu.Lock()
	if len(runtime.pending) > 0 {
		ref := runtime.pending[0]
		runtime.pending = runtime.pending[1:]
		runtime.mu.Unlock()
		return ref, nil, true, nil
	}
	runtime.mu.Unlock()

	select {
	case <-ctx.Done():
		return ports.SafeJobReference{}, nil, false, ctx.Err()
	case <-runtime.done:
		return ports.SafeJobReference{}, nil, false, context.Canceled
	case request := <-runtime.queue:
		return request.reference, request.accepted, false, nil
	}
}

func (runtime *Runtime) enqueuePending(reference ports.SafeJobReference) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.pending = append(runtime.pending, reference)
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
