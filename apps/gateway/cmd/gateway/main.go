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

type processConfig struct {
	address         string
	startupTimeout  time.Duration
	shutdownTimeout time.Duration
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

	runtime, err := composition.New(composition.Config{
		StartupTimeout: config.startupTimeout,
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
	processContext, cancelProcess := context.WithCancel(ctx)
	defer cancelProcess()

	listener, err := net.Listen("tcp", config.address)
	if err != nil {
		closeContext, cancelClose := context.WithTimeout(context.Background(), config.shutdownTimeout)
		defer cancelClose()
		return errors.Join(fmt.Errorf("listen on %s: %w", config.address, err), runtime.Close(closeContext))
	}

	server := &http.Server{
		Handler:           runtime.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext: func(net.Listener) context.Context {
			return processContext
		},
	}
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.Serve(listener)
	}()

	workerErrors := make(chan error, 1)
	go func() {
		workerErrors <- runtime.RunWorkers(processContext)
	}()

	var runError error
	select {
	case <-processContext.Done():
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
			case <-processContext.Done():
			case err := <-serverErrors:
				if !errors.Is(err, http.ErrServerClosed) {
					runError = fmt.Errorf("serve HTTP: %w", err)
				}
			}
		}
	}
	cancelProcess()

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), config.shutdownTimeout)
	shutdownError := server.Shutdown(shutdownContext)
	cancelShutdown()

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
