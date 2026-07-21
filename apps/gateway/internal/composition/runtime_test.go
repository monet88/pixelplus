package composition_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/composition"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

func TestNewRejectsMissingRequiredDependencies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		dependencies composition.Dependencies
		wantError    string
	}{
		{
			name:      "job runtime",
			wantError: "job runtime is required",
		},
		{
			name: "clock",
			dependencies: composition.Dependencies{
				Runtime: inertJobRuntime{},
				IDs:     inertIDs{},
			},
			wantError: "clock is required",
		},
		{
			name: "ID generator",
			dependencies: composition.Dependencies{
				Runtime: inertJobRuntime{},
				Clock:   inertClock{},
			},
			wantError: "ID generator is required",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := composition.New(composition.Config{}, test.dependencies)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("New() error = %v, want containing %q", err, test.wantError)
			}
		})
	}
}

func TestNewRejectsNegativeStartupTimeout(t *testing.T) {
	t.Parallel()

	_, err := composition.New(composition.Config{
		StartupTimeout: -time.Second,
	}, composition.Dependencies{
		Runtime: inertJobRuntime{},
		Clock:   inertClock{},
		IDs:     inertIDs{},
	})
	if err == nil || !strings.Contains(err.Error(), "startup timeout must be positive") {
		t.Fatalf("New() error = %v", err)
	}
}

func TestRuntimeCloseRetriesOwnedResourceAfterTimeout(t *testing.T) {
	t.Parallel()

	jobs := &retryCloseJobRuntime{}
	runtime, err := composition.New(composition.Config{}, composition.Dependencies{
		Runtime: jobs,
		Clock:   inertClock{},
		IDs:     inertIDs{},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	firstContext, cancelFirst := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancelFirst()
	if err := runtime.Close(firstContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first Close() error = %v, want context.DeadlineExceeded", err)
	}
	if runtime.Healthy() || runtime.Ready() {
		t.Fatal("runtime remained healthy or ready after shutdown began")
	}

	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if calls := jobs.closeCalls.Load(); calls != 2 {
		t.Fatalf("JobRuntime.Close() calls = %d, want 2", calls)
	}
}

type inertClock struct{}

func (inertClock) Now() time.Time {
	return time.Time{}
}

type inertIDs struct{}

func (inertIDs) New(domain.IdentifierKind) (domain.Identifier, error) {
	return domain.Identifier("test"), nil
}

type inertJobRuntime struct{}

func (inertJobRuntime) Restore(context.Context) error {
	return nil
}

func (inertJobRuntime) Enqueue(_ context.Context, reference ports.SafeJobReference) (ports.EnqueueReceipt, error) {
	return ports.EnqueueReceipt{Reference: reference}, nil
}

func (inertJobRuntime) Run(ctx context.Context, _ ports.JobHandler) error {
	<-ctx.Done()
	return ctx.Err()
}

func (inertJobRuntime) Close(context.Context) error {
	return nil
}

type retryCloseJobRuntime struct {
	closeCalls atomic.Int32
}

func (*retryCloseJobRuntime) Restore(context.Context) error {
	return nil
}

func (*retryCloseJobRuntime) Enqueue(_ context.Context, reference ports.SafeJobReference) (ports.EnqueueReceipt, error) {
	return ports.EnqueueReceipt{Reference: reference}, nil
}

func (*retryCloseJobRuntime) Run(ctx context.Context, _ ports.JobHandler) error {
	<-ctx.Done()
	return ctx.Err()
}

func (runtime *retryCloseJobRuntime) Close(ctx context.Context) error {
	if runtime.closeCalls.Add(1) == 1 {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}
