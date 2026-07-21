// Package composition is the only Gateway package allowed to wire dependencies.
package composition

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/application"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
	httptransport "github.com/monet88/pixelplus/apps/gateway/internal/transport/http"
)

const defaultStartupTimeout = 5 * time.Second

// ErrNotReady blocks workers when required startup recovery did not complete.
var ErrNotReady = errors.New("gateway runtime is not ready")

// Config contains parsed, non-secret composition settings.
type Config struct {
	StartupTimeout time.Duration
}

// Dependencies contains the controlled foundation ports owned by this slice.
// Later vertical slices add their application-owned ports here.
type Dependencies struct {
	Runtime ports.JobRuntime
	Clock   ports.Clock
	IDs     ports.IDGenerator
}

// Runtime is the single composition result shared by production and fixtures.
type Runtime struct {
	handler http.Handler
	worker  application.JobExecutor
	jobs    ports.JobRuntime

	healthy atomic.Bool
	ready   atomic.Bool
	done    chan struct{}

	shutdownOnce sync.Once
	closeMu      sync.Mutex
	closed       bool
}

// New validates dependencies, restores required state, and builds one runtime.
// Recovery failures keep the operational HTTP surface live while readiness and
// worker execution fail closed.
func New(config Config, dependencies Dependencies) (*Runtime, error) {
	if dependencies.Runtime == nil {
		return nil, errors.New("composition: job runtime is required")
	}
	if dependencies.Clock == nil {
		return nil, errors.New("composition: clock is required")
	}
	if dependencies.IDs == nil {
		return nil, errors.New("composition: ID generator is required")
	}
	startupTimeout := config.StartupTimeout
	if startupTimeout == 0 {
		startupTimeout = defaultStartupTimeout
	}
	if startupTimeout < 0 {
		return nil, errors.New("composition: startup timeout must be positive")
	}

	runtime := &Runtime{
		worker: application.NewFoundationJobExecutor(),
		jobs:   dependencies.Runtime,
		done:   make(chan struct{}),
	}
	runtime.healthy.Store(true)

	startupContext, cancelStartup := context.WithTimeout(context.Background(), startupTimeout)
	defer cancelStartup()

	runtime.ready.Store(runtime.jobs.Restore(startupContext) == nil)
	runtime.handler = httptransport.NewStatusHandler(dependencies.Clock, dependencies.IDs, runtime)

	return runtime, nil
}

// Handler returns the real composed HTTP surface.
func (runtime *Runtime) Handler() http.Handler {
	return runtime.handler
}

// Worker returns the exported application worker seam.
func (runtime *Runtime) Worker() application.JobExecutor {
	return runtime.worker
}

// Healthy reports process lifecycle only and never authorizes product work.
func (runtime *Runtime) Healthy() bool {
	return runtime.healthy.Load()
}

// Ready reports whether required recovery succeeded and shutdown has not begun.
func (runtime *Runtime) Ready() bool {
	return runtime.ready.Load() && runtime.healthy.Load()
}

// RunWorkers connects safe queue references to the exported JobExecutor.
func (runtime *Runtime) RunWorkers(ctx context.Context) error {
	if !runtime.Ready() {
		return ErrNotReady
	}

	workerContext, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()

	bridgeDone := make(chan struct{})
	go func() {
		defer close(bridgeDone)
		select {
		case <-runtime.done:
			cancelWorkers()
		case <-workerContext.Done():
		}
	}()

	err := runtime.jobs.Run(workerContext, func(ctx context.Context, reference ports.SafeJobReference) error {
		job, err := reference.JobRef()
		if err != nil {
			return err
		}
		return runtime.worker.ExecuteJob(ctx, job)
	})
	cancelWorkers()
	<-bridgeDone
	return err
}

// Close cancels lifecycle work, then closes owned resources in reverse order.
func (runtime *Runtime) Close(ctx context.Context) error {
	runtime.shutdownOnce.Do(func() {
		runtime.ready.Store(false)
		runtime.healthy.Store(false)
		close(runtime.done)
	})

	runtime.closeMu.Lock()
	defer runtime.closeMu.Unlock()
	if runtime.closed {
		return nil
	}
	if err := runtime.jobs.Close(ctx); err != nil {
		return fmt.Errorf("close job runtime: %w", err)
	}
	runtime.closed = true
	return nil
}

var _ httptransport.Status = (*Runtime)(nil)
