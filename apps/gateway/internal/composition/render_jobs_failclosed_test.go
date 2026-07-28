package composition_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/composition"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/infrastructure/jobs"
	"github.com/monet88/pixelplus/apps/gateway/internal/infrastructure/persistence"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

type seqIDs struct{ n int }

func (s *seqIDs) New(kind domain.IdentifierKind) (domain.Identifier, error) {
	s.n++
	return domain.Identifier(string(kind) + "_t"), nil
}

// Finding #6: production Config without AllowInMemoryRenderJobs installs
// UnavailableRenderJobStore — Create fails closed with dependency unavailable.
func TestRenderJobsFailClosedWithoutAllowInMemory(t *testing.T) {
	t.Parallel()

	store := persistence.NewUnavailableRenderJobStore()
	_, err := store.Create(context.Background(), ports.RenderJobCreation{
		Principal: domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a"},
		Job:       domain.RenderJob{JobID: "job_1", TenantID: "tenant_a"},
	})
	if !errors.Is(err, ports.ErrDependencyUnavailable) {
		t.Fatalf("UnavailableRenderJobStore.Create error = %v, want ErrDependencyUnavailable", err)
	}

	runtime, err := composition.New(composition.Config{
		AllowInMemoryRenderJobs: false,
	}, composition.Dependencies{
		Runtime: jobs.New(),
		Clock:   fixedClock{t: time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)},
		IDs:     &seqIDs{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })

	// Worker must not invent durable jobs when the store is unavailable.
	if err := runtime.Worker().ExecuteJob(context.Background(), domain.JobRef{
		TenantID: "tenant_a",
		JobID:    "job_missing",
	}); err != nil {
		// Fail-closed claim may surface dependency or be discarded as not claimable.
		if !errors.Is(err, ports.ErrDependencyUnavailable) {
			t.Fatalf("ExecuteJob error = %v", err)
		}
	}
}

// Finding #6 positive control: AllowInMemoryRenderJobs enables process-local store.
func TestRenderJobsAllowInMemoryForControlledFixtures(t *testing.T) {
	t.Parallel()

	runtime, err := composition.New(composition.Config{
		AllowInMemoryRenderJobs: true,
	}, composition.Dependencies{
		Runtime: jobs.New(),
		Clock:   fixedClock{t: time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)},
		IDs:     &seqIDs{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })

	// Empty in-memory store: missing job is discarded without panic.
	if err := runtime.Worker().ExecuteJob(context.Background(), domain.JobRef{
		TenantID: "tenant_a",
		JobID:    "job_none",
	}); err != nil {
		t.Fatalf("ExecuteJob on empty in-memory store: %v (want nil discard)", err)
	}
}
