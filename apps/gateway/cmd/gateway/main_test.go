package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestRunStartsProductionCompositionAndShutsDown(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve test address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release test address: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() {
		runResult <- run(ctx, func(key string) string {
			switch key {
			case "PIXELPLUS_GATEWAY_ADDR":
				return address
			case "PIXELPLUS_GATEWAY_STARTUP_TIMEOUT", "PIXELPLUS_GATEWAY_SHUTDOWN_TIMEOUT":
				return "2s"
			default:
				return ""
			}
		})
	}()

	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(2 * time.Second)
	for {
		response, requestErr := client.Get("http://" + address + "/readyz")
		if requestErr == nil {
			response.Body.Close()
			if response.StatusCode != http.StatusOK {
				cancel()
				t.Fatalf("GET /readyz status = %d, want %d", response.StatusCode, http.StatusOK)
			}
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("production readiness did not start: %v", requestErr)
		}
		time.Sleep(10 * time.Millisecond)
	}

	response, err := client.Get("http://" + address + "/healthz")
	if err != nil {
		cancel()
		t.Fatalf("GET /healthz error = %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("GET /healthz status = %d, want %d", response.StatusCode, http.StatusOK)
	}

	cancel()
	select {
	case err := <-runResult:
		if err != nil {
			t.Fatalf("run() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run() did not shut down")
	}
}

func TestLoadConfigParsesNonSecretRuntimeSettings(t *testing.T) {
	t.Parallel()

	values := map[string]string{
		"PIXELPLUS_GATEWAY_ADDR":             "127.0.0.1:9090",
		"PIXELPLUS_GATEWAY_STARTUP_TIMEOUT":  "3s",
		"PIXELPLUS_GATEWAY_SHUTDOWN_TIMEOUT": "7s",
	}
	config, err := loadConfig(func(key string) string {
		return values[key]
	})
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	if config.address != "127.0.0.1:9090" {
		t.Fatalf("address = %q", config.address)
	}
	if config.startupTimeout != 3*time.Second {
		t.Fatalf("startup timeout = %s", config.startupTimeout)
	}
	if config.shutdownTimeout != 7*time.Second {
		t.Fatalf("shutdown timeout = %s", config.shutdownTimeout)
	}
}

func TestLoadConfigRejectsInvalidTimeout(t *testing.T) {
	t.Parallel()

	_, err := loadConfig(func(key string) string {
		if key == "PIXELPLUS_GATEWAY_STARTUP_TIMEOUT" {
			return "not-a-duration"
		}
		return ""
	})
	if err == nil {
		t.Fatal("loadConfig() error = nil")
	}
}

func TestServeCancelsActiveRequestWhenWorkersFail(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve test address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release test address: %v", err)
	}

	workerError := errors.New("worker failed")
	runtime := newFailingProcessRuntime(workerError)
	serveResult := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		serveResult <- serve(ctx, processConfig{
			address:         address,
			shutdownTimeout: time.Second,
		}, runtime)
	}()

	requestResult := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 2 * time.Second}
		deadline := time.Now().Add(time.Second)
		for {
			response, requestErr := client.Get("http://" + address + "/healthz")
			if requestErr == nil {
				response.Body.Close()
				requestResult <- nil
				return
			}
			if time.Now().After(deadline) {
				requestResult <- requestErr
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	select {
	case <-runtime.requestCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("active HTTP request context was not canceled")
	}
	if err := <-requestResult; err != nil {
		t.Fatalf("GET /healthz error = %v", err)
	}
	if err := <-serveResult; !errors.Is(err, workerError) {
		t.Fatalf("serve() error = %v, want worker failure", err)
	}
}

type failingProcessRuntime struct {
	workerError     error
	requestStarted  chan struct{}
	requestCanceled chan struct{}
}

func newFailingProcessRuntime(workerError error) *failingProcessRuntime {
	return &failingProcessRuntime{
		workerError:     workerError,
		requestStarted:  make(chan struct{}),
		requestCanceled: make(chan struct{}),
	}
}

func (runtime *failingProcessRuntime) Handler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		close(runtime.requestStarted)
		<-request.Context().Done()
		close(runtime.requestCanceled)
		writer.WriteHeader(http.StatusServiceUnavailable)
	})
}

func (runtime *failingProcessRuntime) RunWorkers(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-runtime.requestStarted:
		return runtime.workerError
	}
}

func (*failingProcessRuntime) Close(context.Context) error {
	return nil
}
