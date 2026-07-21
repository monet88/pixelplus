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
type Runtime struct {
	queue chan enqueueRequest
	done  chan struct{}

	closing atomic.Bool

	mu      sync.Mutex
	closed  bool
	running bool
	cancel  context.CancelFunc
	runDone chan struct{}
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
		select {
		case <-runContext.Done():
			return runContext.Err()
		case <-runtime.done:
			return context.Canceled
		case request := <-runtime.queue:
			runtime.mu.Lock()
			closing := runtime.closed || runtime.closing.Load()
			runtime.mu.Unlock()
			if closing {
				request.accepted <- ErrClosed
				return context.Canceled
			}

			request.accepted <- nil
			if err := handler(runContext, request.reference); err != nil {
				return err
			}
		}
	}
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
