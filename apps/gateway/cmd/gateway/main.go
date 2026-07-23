package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/composition"
)

const (
	defaultAddress         = "127.0.0.1:8080"
	defaultStartupTimeout  = 10 * time.Second
	defaultShutdownTimeout = 10 * time.Second
)

const (
	serverReadHeaderTimeout = 5 * time.Second
	serverReadTimeout       = 10 * time.Second
	serverWriteTimeout      = 10 * time.Second
	serverIdleTimeout       = 60 * time.Second
)

type processConfig struct {
	address                  string
	startupTimeout           time.Duration
	shutdownTimeout          time.Duration
	providerAccountStorePath string
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Getenv); err != nil {
		slog.Error("gateway stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, getenv func(string) string) error {
	config, err := loadConfig(getenv)
	if err != nil {
		return err
	}
	if config.providerAccountStorePath == "" {
		return errors.New("PROVIDER_ACCOUNT_STORE_PATH is required")
	}

	runtime, err := composition.New(composition.Config{
		StartupTimeout:           config.startupTimeout,
		ProviderAccountStorePath: config.providerAccountStorePath,
	}, composition.ProductionDependencies())
	if err != nil {
		return fmt.Errorf("compose Gateway: %w", err)
	}
	return serve(ctx, config, runtime)
}

type processRuntime interface {
	Handler() http.Handler
	RunWorkers(context.Context) error
	Close(context.Context) error
}

func serve(ctx context.Context, config processConfig, runtime processRuntime) error {
	listener, err := net.Listen("tcp", config.address)
	if err != nil {
		closeContext, cancelClose := context.WithTimeout(context.Background(), config.shutdownTimeout)
		defer cancelClose()
		return errors.Join(fmt.Errorf("listen on %s: %w", config.address, err), runtime.Close(closeContext))
	}
	return serveListener(ctx, config, runtime, listener)
}

func serveListener(ctx context.Context, config processConfig, runtime processRuntime, listener net.Listener) error {
	// requestContext backs in-flight HTTP requests. It is intentionally not
	// derived from ctx so a shutdown signal does not cancel active requests
	// before server.Shutdown drains them within the grace period.
	requestContext, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()

	// workerContext tracks process lifecycle and is canceled as soon as
	// shutdown begins so background work stops promptly.
	workerContext, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()

	server := &http.Server{
		Handler:           runtime.Handler(),
		ReadHeaderTimeout: serverReadHeaderTimeout,
		ReadTimeout:       serverReadTimeout,
		WriteTimeout:      serverWriteTimeout,
		IdleTimeout:       serverIdleTimeout,
		BaseContext: func(net.Listener) context.Context {
			return requestContext
		},
	}
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.Serve(listener)
	}()

	workerErrors := make(chan error, 1)
	go func() {
		workerErrors <- runtime.RunWorkers(workerContext)
	}()

	var runError error
	select {
	case <-workerContext.Done():
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			runError = fmt.Errorf("serve HTTP: %w", err)
		}
	case err := <-workerErrors:
		if !errors.Is(err, context.Canceled) && !errors.Is(err, composition.ErrNotReady) {
			runError = fmt.Errorf("run workers: %w", err)
		}
		if errors.Is(err, composition.ErrNotReady) {
			select {
			case <-workerContext.Done():
			case err := <-serverErrors:
				if !errors.Is(err, http.ErrServerClosed) {
					runError = fmt.Errorf("serve HTTP: %w", err)
				}
			}
		}
	}
	cancelWorkers()

	// Drain in-flight HTTP requests before canceling their context so
	// well-behaved handlers can complete within the shutdown grace period.
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), config.shutdownTimeout)
	shutdownError := server.Shutdown(shutdownContext)
	cancelShutdown()
	cancelRequests()

	closeContext, cancelClose := context.WithTimeout(context.Background(), config.shutdownTimeout)
	closeError := runtime.Close(closeContext)
	cancelClose()

	return errors.Join(runError, shutdownError, closeError)
}

func loadConfig(getenv func(string) string) (processConfig, error) {
	config := processConfig{
		address:         valueOrDefault(getenv("PIXELPLUS_GATEWAY_ADDR"), defaultAddress),
		startupTimeout:  defaultStartupTimeout,
		shutdownTimeout: defaultShutdownTimeout,
	}

	var err error
	config.startupTimeout, err = durationOrDefault(getenv("PIXELPLUS_GATEWAY_STARTUP_TIMEOUT"), defaultStartupTimeout)
	if err != nil {
		return processConfig{}, fmt.Errorf("PIXELPLUS_GATEWAY_STARTUP_TIMEOUT: %w", err)
	}
	config.shutdownTimeout, err = durationOrDefault(getenv("PIXELPLUS_GATEWAY_SHUTDOWN_TIMEOUT"), defaultShutdownTimeout)
	if err != nil {
		return processConfig{}, fmt.Errorf("PIXELPLUS_GATEWAY_SHUTDOWN_TIMEOUT: %w", err)
	}
	config.providerAccountStorePath = getenv("PROVIDER_ACCOUNT_STORE_PATH")
	return config, nil
}

func valueOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func durationOrDefault(value string, fallback time.Duration) (time.Duration, error) {
	if value == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	if duration <= 0 {
		return 0, errors.New("must be positive")
	}
	return duration, nil
}
