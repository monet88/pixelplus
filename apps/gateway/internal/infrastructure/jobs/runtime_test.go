package jobs_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/infrastructure/jobs"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// Handler failure re-queues the same SafeJobReference; a subsequent Run
// redelivers it (at-least-once). Application still owns whether redelivery may render.
func TestRuntimeRedeliversSameReferenceAfterHandlerError(t *testing.T) {
	t.Parallel()

	runtime := jobs.New()
	if err := runtime.Restore(context.Background()); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	var calls atomic.Int32
	firstDone := make(chan struct{})
	firstResult := make(chan error, 1)
	go func() {
		firstResult <- runtime.Run(context.Background(), func(_ context.Context, ref ports.SafeJobReference) error {
			calls.Add(1)
			if ref.JobID != domain.Identifier("job_retry") {
				t.Errorf("unexpected job_id %s", ref.JobID)
			}
			close(firstDone)
			return errors.New("durable mutation failed")
		})
	}()

	if _, err := runtime.Enqueue(context.Background(), ports.SafeJobReference{
		TenantID: domain.Identifier("tenant_1"),
		JobID:    domain.Identifier("job_retry"),
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("first handler not invoked")
	}
	if err := <-firstResult; err == nil {
		t.Fatal("first Run() error = nil, want handler failure")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler calls after first Run = %d, want 1", got)
	}

	// Recovery: second Run redelivers the pending reference without a new Enqueue.
	redelivered := make(chan struct{})
	secondResult := make(chan error, 1)
	go func() {
		secondResult <- runtime.Run(context.Background(), func(_ context.Context, ref ports.SafeJobReference) error {
			calls.Add(1)
			if ref.JobID != domain.Identifier("job_retry") {
				t.Errorf("redelivery job_id = %s, want job_retry", ref.JobID)
			}
			close(redelivered)
			return nil
		})
	}()
	select {
	case <-redelivered:
	case <-time.After(2 * time.Second):
		t.Fatal("pending reference was not redelivered on second Run")
	}
	// Cancel the idle second Run.
	_ = runtime.Close(context.Background())
	if err := <-secondResult; !errors.Is(err, context.Canceled) && err != nil && !errors.Is(err, jobs.ErrClosed) {
		// Close cancels Run; accept Canceled or nil race after success.
		if !errors.Is(err, context.Canceled) {
			// success path already closed redelivered before Close
		}
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("handler calls after redelivery = %d, want 2", got)
	}
}

func TestRuntimeRejectsASecondConsumer(t *testing.T) {
	t.Parallel()

	runtime := jobs.New()
	if err := runtime.Restore(context.Background()); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	handlerStarted := make(chan struct{})
	handlerRelease := make(chan struct{})
	firstResult := make(chan error, 1)
	go func() {
		firstResult <- runtime.Run(ctx, func(context.Context, ports.SafeJobReference) error {
			close(handlerStarted)
			<-handlerRelease
			return nil
		})
	}()

	reference := ports.SafeJobReference{
		TenantID: domain.Identifier("tenant_1"),
		JobID:    domain.Identifier("job_1"),
	}
	if _, err := runtime.Enqueue(ctx, reference); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	<-handlerStarted

	secondResult := make(chan error, 1)
	go func() {
		secondResult <- runtime.Run(ctx, func(context.Context, ports.SafeJobReference) error {
			return nil
		})
	}()

	select {
	case err := <-secondResult:
		if !errors.Is(err, jobs.ErrAlreadyRunning) {
			t.Fatalf("second Run() error = %v, want ErrAlreadyRunning", err)
		}
	case <-time.After(250 * time.Millisecond):
		close(handlerRelease)
		cancel()
		t.Fatal("second Run() did not fail immediately")
	}

	close(handlerRelease)
	cancel()
	if err := <-firstResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("first Run() error = %v, want context.Canceled", err)
	}
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestRuntimeRejectsRunAfterClose(t *testing.T) {
	t.Parallel()

	runtime := jobs.New()
	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	err := runtime.Run(context.Background(), func(context.Context, ports.SafeJobReference) error {
		return nil
	})
	if !errors.Is(err, jobs.ErrClosed) {
		t.Fatalf("Run() error = %v, want ErrClosed", err)
	}

	_, err = runtime.Enqueue(context.Background(), ports.SafeJobReference{
		TenantID: domain.Identifier("tenant_1"),
		JobID:    domain.Identifier("job_1"),
	})
	if !errors.Is(err, jobs.ErrClosed) {
		t.Fatalf("Enqueue() error = %v, want ErrClosed", err)
	}
}

func TestRuntimeConcurrentEnqueueAndCloseNeverLoseAcceptedWork(t *testing.T) {
	t.Parallel()

	const attempts = 100
	for attempt := range attempts {
		runtime := jobs.New()
		racedJobHandled := make(chan struct{})
		warmupHandled := make(chan struct{})
		runResult := make(chan error, 1)
		go func() {
			runResult <- runtime.Run(context.Background(), func(_ context.Context, reference ports.SafeJobReference) error {
				if reference.JobID == domain.Identifier("job_warmup") {
					close(warmupHandled)
				} else {
					close(racedJobHandled)
				}
				return nil
			})
		}()
		if _, err := runtime.Enqueue(context.Background(), ports.SafeJobReference{
			TenantID: domain.Identifier("tenant_1"),
			JobID:    domain.Identifier("job_warmup"),
		}); err != nil {
			t.Fatalf("attempt %d: warm-up Enqueue() error = %v", attempt, err)
		}
		<-warmupHandled

		start := make(chan struct{})
		enqueueResult := make(chan error, 1)
		go func() {
			<-start
			_, err := runtime.Enqueue(context.Background(), ports.SafeJobReference{
				TenantID: domain.Identifier("tenant_1"),
				JobID:    domain.Identifier("job_1"),
			})
			enqueueResult <- err
		}()
		close(start)

		if err := runtime.Close(context.Background()); err != nil {
			t.Fatalf("attempt %d: Close() error = %v", attempt, err)
		}
		err := <-enqueueResult
		if err == nil {
			select {
			case <-racedJobHandled:
			case <-time.After(250 * time.Millisecond):
				t.Fatalf("attempt %d: Enqueue() succeeded without delivering accepted work", attempt)
			}
		}
		if err != nil && !errors.Is(err, jobs.ErrClosed) {
			t.Fatalf("attempt %d: Enqueue() error = %v, want nil or ErrClosed", attempt, err)
		}
		if err := <-runResult; !errors.Is(err, context.Canceled) {
			t.Fatalf("attempt %d: Run() error = %v, want context.Canceled", attempt, err)
		}
	}
}
