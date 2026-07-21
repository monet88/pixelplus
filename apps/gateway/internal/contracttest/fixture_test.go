package contracttest_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/composition"
	"github.com/monet88/pixelplus/apps/gateway/internal/contracttest"
)

type statusResponse struct {
	Status     string `json:"status"`
	RequestID  string `json:"request_id"`
	ObservedAt string `json:"observed_at"`
}

func TestFixtureExposesDeterministicHealthAndReadiness(t *testing.T) {
	t.Parallel()

	fixture, err := contracttest.NewFixture(contracttest.Options{})
	if err != nil {
		t.Fatalf("NewFixture() error = %v", err)
	}
	t.Cleanup(func() {
		closeFixture(t, fixture)
	})

	health := getStatus(t, fixture, "/healthz", http.StatusOK)
	if health != (statusResponse{
		Status:     "healthy",
		RequestID:  "request_0001",
		ObservedAt: "2026-07-21T00:00:00Z",
	}) {
		t.Fatalf("GET /healthz = %#v", health)
	}

	readiness := getStatus(t, fixture, "/readyz", http.StatusOK)
	if readiness != (statusResponse{
		Status:     "ready",
		RequestID:  "request_0002",
		ObservedAt: "2026-07-21T00:00:01Z",
	}) {
		t.Fatalf("GET /readyz = %#v", readiness)
	}
}

func TestFixtureReadinessFailsClosedWhenRecoveryIsUnavailable(t *testing.T) {
	t.Parallel()

	fixture, err := contracttest.NewFixture(contracttest.Options{
		RecoveryError: errors.New("controlled recovery failure"),
	})
	if err != nil {
		t.Fatalf("NewFixture() error = %v", err)
	}
	t.Cleanup(func() {
		closeFixture(t, fixture)
	})

	health := getStatus(t, fixture, "/healthz", http.StatusOK)
	if health.Status != "healthy" {
		t.Fatalf("GET /healthz status = %q, want healthy", health.Status)
	}

	readiness := getStatus(t, fixture, "/readyz", http.StatusServiceUnavailable)
	if readiness.Status != "not_ready" {
		t.Fatalf("GET /readyz status = %q, want not_ready", readiness.Status)
	}

	if err := fixture.Runtime().RunWorkers(context.Background()); !errors.Is(err, composition.ErrNotReady) {
		t.Fatalf("RunWorkers() error = %v, want ErrNotReady", err)
	}
}

func TestFixtureClosesHTTPWorkersAndResourcesInLifecycleOrder(t *testing.T) {
	t.Parallel()

	fixture, err := contracttest.NewFixture(contracttest.Options{})
	if err != nil {
		t.Fatalf("NewFixture() error = %v", err)
	}

	workerResult := make(chan error, 1)
	go func() {
		workerResult <- fixture.Runtime().RunWorkers(context.Background())
	}()

	select {
	case <-fixture.WorkersStarted():
	case <-time.After(2 * time.Second):
		t.Fatal("workers did not start")
	}

	closeFixture(t, fixture)

	select {
	case err := <-workerResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RunWorkers() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("workers did not stop")
	}

	events := fixture.Events()
	wantOrder := []string{
		"job_runtime.restore",
		"job_runtime.run",
		"http.shutdown",
		"job_runtime.canceled",
		"job_runtime.close",
	}
	if !slices.Equal(events, wantOrder) {
		t.Fatalf("lifecycle events = %v, want %v", events, wantOrder)
	}
}

func TestFixtureDoesNotExposeProductOperationsThroughReadiness(t *testing.T) {
	t.Parallel()

	fixture, err := contracttest.NewFixture(contracttest.Options{})
	if err != nil {
		t.Fatalf("NewFixture() error = %v", err)
	}
	t.Cleanup(func() {
		closeFixture(t, fixture)
	})

	getStatus(t, fixture, "/readyz", http.StatusOK)

	response, err := fixture.Client().Get(fixture.URL() + "/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /v1/models status = %d, want %d", response.StatusCode, http.StatusNotFound)
	}
}

func TestFixtureCloseRetriesAfterDeadline(t *testing.T) {
	t.Parallel()

	closeGate := make(chan struct{})
	fixture, err := contracttest.NewFixture(contracttest.Options{
		JobRuntimeCloseGate: closeGate,
	})
	if err != nil {
		t.Fatalf("NewFixture() error = %v", err)
	}

	workerResult := make(chan error, 1)
	go func() {
		workerResult <- fixture.Runtime().RunWorkers(context.Background())
	}()
	select {
	case <-fixture.WorkersStarted():
	case <-time.After(2 * time.Second):
		t.Fatal("workers did not start")
	}

	firstContext, cancelFirst := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancelFirst()
	if err := fixture.Close(firstContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first Fixture.Close() error = %v, want context.DeadlineExceeded", err)
	}
	close(closeGate)
	if err := fixture.Close(context.Background()); err != nil {
		t.Fatalf("second Fixture.Close() error = %v", err)
	}

	if err := <-workerResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("RunWorkers() error = %v, want context.Canceled", err)
	}
	wantOrder := []string{
		"job_runtime.restore",
		"job_runtime.run",
		"http.shutdown",
		"job_runtime.canceled",
		"job_runtime.close",
	}
	if events := fixture.Events(); !slices.Equal(events, wantOrder) {
		t.Fatalf("lifecycle events = %v, want %v", events, wantOrder)
	}
}

func getStatus(t *testing.T, fixture *contracttest.Fixture, path string, wantStatus int) statusResponse {
	t.Helper()

	response, err := fixture.Client().Get(fixture.URL() + path)
	if err != nil {
		t.Fatalf("GET %s error = %v", path, err)
	}
	defer response.Body.Close()

	if response.StatusCode != wantStatus {
		t.Fatalf("GET %s status = %d, want %d", path, response.StatusCode, wantStatus)
	}

	var body statusResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode GET %s response: %v", path, err)
	}
	return body
}

func closeFixture(t *testing.T, fixture *contracttest.Fixture) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := fixture.Close(ctx); err != nil {
		t.Fatalf("Fixture.Close() error = %v", err)
	}
}
