package composition_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/composition"
	"github.com/monet88/pixelplus/apps/gateway/internal/infrastructure/jobs"
)

// ADR 0009: production without durable render recovery ports must not advertise
// execution readiness via /readyz.
func TestProductionMissingRenderDurabilityKeepsReadinessClosed(t *testing.T) {
	t.Parallel()

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

	if runtime.Ready() {
		t.Fatal("Ready() = true, want false when render durability is not configured")
	}
	if !runtime.Healthy() {
		t.Fatal("Healthy() = false, want true (process up; readiness closed)")
	}

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz status = %d, want 503", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["status"] != "not_ready" {
		t.Fatalf("/readyz body status = %v, want not_ready", body["status"])
	}
}

func TestControlledInMemoryRenderDurabilityAllowsReady(t *testing.T) {
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

	if !runtime.Ready() {
		t.Fatal("Ready() = false with AllowInMemoryRenderJobs, want true")
	}
}
