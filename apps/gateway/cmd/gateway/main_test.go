package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
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
			case "PROVIDER_ACCOUNT_STORE_PATH":
				return t.TempDir() + "/accounts.json"
			default:
				return ""
			}
		})
	}()

	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(2 * time.Second)
	// Production composition without durable Render Job stores must keep /readyz
	// closed (503). Healthz still proves the process accepted traffic (#54 Standards).
	var readyzStatus int
	for {
		response, requestErr := client.Get("http://" + address + "/readyz")
		if requestErr == nil {
			readyzStatus = response.StatusCode
			response.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("production gateway did not start: %v", requestErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if readyzStatus != http.StatusServiceUnavailable {
		cancel()
		t.Fatalf("GET /readyz status = %d, want %d (production lacks durable render foundation)", readyzStatus, http.StatusServiceUnavailable)
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

func TestServeListenerCancelsWorkersWhenServerFails(t *testing.T) {
	serverError := errors.New("listener failed")
	release := make(chan struct{})
	listener := &erroringListener{release: release, acceptError: serverError}
	runtime := newBlockingProcessRuntime()

	serveResult := make(chan error, 1)
	go func() {
		serveResult <- serveListener(context.Background(), processConfig{
			shutdownTimeout: time.Second,
		}, runtime, listener)
	}()

	close(release)

	select {
	case <-runtime.workersCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("workers were not canceled after server failure")
	}

	if err := <-serveResult; !errors.Is(err, serverError) {
		t.Fatalf("serveListener() error = %v, want server failure", err)
	}
}

type erroringListener struct {
	release     chan struct{}
	acceptError error
}

func (listener *erroringListener) Accept() (net.Conn, error) {
	<-listener.release
	return nil, listener.acceptError
}

func (*erroringListener) Close() error {
	return nil
}

func (*erroringListener) Addr() net.Addr {
	return fakeAddr{}
}

type fakeAddr struct{}

func (fakeAddr) Network() string {
	return "tcp"
}

func (fakeAddr) String() string {
	return "127.0.0.1:0"
}

type blockingProcessRuntime struct {
	workersCanceled chan struct{}
}

func newBlockingProcessRuntime() *blockingProcessRuntime {
	return &blockingProcessRuntime{
		workersCanceled: make(chan struct{}),
	}
}

func (*blockingProcessRuntime) Handler() http.Handler {
	return http.NewServeMux()
}

func (runtime *blockingProcessRuntime) RunWorkers(ctx context.Context) error {
	<-ctx.Done()
	close(runtime.workersCanceled)
	return ctx.Err()
}

func (*blockingProcessRuntime) Close(context.Context) error {
	return nil
}

func TestServeListenerDrainsInFlightRequestOnShutdown(t *testing.T) {
	// Reserve then re-bind so the test owns a free address without racing
	// another process that might grab 127.0.0.1:0 between close and listen.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve test address: %v", err)
	}
	address := probe.Addr().String()
	if err := probe.Close(); err != nil {
		t.Fatalf("release test address: %v", err)
	}

	// Listen on the test goroutine before Serve or the client start. Calling
	// mustListen inside the serve goroutine races: the client can dial before
	// Listen returns and get connection refused (especially under -race).
	listener := mustListen(t, address)
	runtime := newDrainingProcessRuntime()
	serveResult := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		serveResult <- serveListener(ctx, processConfig{
			address:         address,
			shutdownTimeout: 2 * time.Second,
		}, runtime, listener)
	}()

	statusResult := make(chan int, 1)
	requestErr := make(chan error, 1)
	go func() {
		// Poll until Serve is accepting, matching TestRunStartsProductionCompositionAndShutsDown.
		// Only the successful in-flight GET is held for the drain assertion.
		client := &http.Client{Timeout: 3 * time.Second}
		deadline := time.Now().Add(2 * time.Second)
		var (
			response *http.Response
			getErr   error
		)
		for {
			response, getErr = client.Get("http://" + address + "/healthz")
			if getErr == nil {
				break
			}
			if time.Now().After(deadline) {
				requestErr <- getErr
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		defer response.Body.Close()
		statusResult <- response.StatusCode
	}()

	select {
	case <-runtime.requestStarted:
	case err := <-requestErr:
		t.Fatalf("request failed before start: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("request did not start")
	}

	// Begin shutdown while the request is still in flight.
	cancel()

	select {
	case status := <-statusResult:
		if status != http.StatusOK {
			t.Fatalf("in-flight request status = %d, want %d (request was canceled instead of drained)", status, http.StatusOK)
		}
	case err := <-requestErr:
		t.Fatalf("in-flight request was canceled during shutdown instead of draining: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("in-flight request neither completed nor failed")
	}

	if err := <-serveResult; err != nil {
		t.Fatalf("serveListener() error = %v", err)
	}
}

func mustListen(t *testing.T, address string) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("listen on %s: %v", address, err)
	}
	return listener
}

type drainingProcessRuntime struct {
	requestStarted chan struct{}
	startedOnce    sync.Once
}

func newDrainingProcessRuntime() *drainingProcessRuntime {
	return &drainingProcessRuntime{
		requestStarted: make(chan struct{}),
	}
}

func (runtime *drainingProcessRuntime) Handler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		runtime.startedOnce.Do(func() {
			close(runtime.requestStarted)
		})
		time.Sleep(200 * time.Millisecond)
		writer.WriteHeader(http.StatusOK)
	})
}

func (*drainingProcessRuntime) RunWorkers(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (*drainingProcessRuntime) Close(context.Context) error {
	return nil
}
