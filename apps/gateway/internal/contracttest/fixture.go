// Package contracttest provides deterministic public-HTTP Gateway fixtures.
package contracttest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/composition"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

var fixtureStartTime = time.Date(2026, time.July, 21, 0, 0, 0, 0, time.UTC)

// Options controls only approved deterministic foundation ports.
type Options struct {
	RecoveryError       error
	JobRuntimeCloseGate <-chan struct{}

	// Provider Account request-spine ports (#45). A nil port keeps the
	// production foundation implementation composition substitutes by default;
	// a controlled fake proves the protected spine through real composition.
	Principal  ports.PrincipalStore
	Admission  ports.AdmissionStore
	Replay     ports.ReplayStore
	Accounts   ports.AccountStore
	Audit      ports.AuditRecorder
	Telemetry  ports.TelemetryRecorder
	RequestLog ports.RequestLogRecorder

	// Provider Credential Vault and Probe Adapter ports (#46). A nil port keeps
	// the production fail-closed foundation composition substitutes by default; a
	// controlled fake proves the credential-submit and probe spines through real
	// composition without releasing secret material.
	Vault ports.CredentialVault
	Probe ports.ProbeAdapter
}

// Fixture wraps the real Runtime in a public HTTP server.
type Fixture struct {
	runtime *composition.Runtime
	server  *httptest.Server
	events  *eventLog
	jobs    *controlledJobRuntime

	serverCloseOnce sync.Once
	closeMu         sync.Mutex
	closed          bool
}

// NewFixture builds the same composition.Runtime used by production.
func NewFixture(options Options) (*Fixture, error) {
	events := &eventLog{}
	jobs := newControlledJobRuntime(events, options.RecoveryError, options.JobRuntimeCloseGate)

	runtime, err := composition.New(composition.Config{}, composition.Dependencies{
		Runtime: jobs,
		Clock:   &controlledClock{next: fixtureStartTime},
		IDs:     &controlledIDs{},

		Principal:  options.Principal,
		Admission:  options.Admission,
		Replay:     options.Replay,
		Accounts:   options.Accounts,
		Audit:      options.Audit,
		Telemetry:  options.Telemetry,
		RequestLog: options.RequestLog,

		Vault: options.Vault,
		Probe: options.Probe,
	})
	if err != nil {
		return nil, err
	}

	server := httptest.NewServer(runtime.Handler())
	return &Fixture{
		runtime: runtime,
		server:  server,
		events:  events,
		jobs:    jobs,
	}, nil
}

// Runtime returns the exported composition lifecycle under test.
func (fixture *Fixture) Runtime() *composition.Runtime {
	return fixture.runtime
}

// Client returns the HTTP client connected to the real composed handler.
func (fixture *Fixture) Client() *http.Client {
	return fixture.server.Client()
}

// URL returns the public HTTP origin for this fixture.
func (fixture *Fixture) URL() string {
	return fixture.server.URL
}

// WorkersStarted closes after JobRuntime.Run begins.
func (fixture *Fixture) WorkersStarted() <-chan struct{} {
	return fixture.jobs.started
}

// Events returns a concurrency-safe copy of lifecycle observations.
func (fixture *Fixture) Events() []string {
	return fixture.events.values()
}

// Close shuts down HTTP first, then delegates reverse resource closure to Runtime.
func (fixture *Fixture) Close(ctx context.Context) error {
	fixture.serverCloseOnce.Do(func() {
		fixture.server.Close()
		fixture.events.add("http.shutdown")
	})

	fixture.closeMu.Lock()
	defer fixture.closeMu.Unlock()
	if fixture.closed {
		return nil
	}
	if err := fixture.runtime.Close(ctx); err != nil {
		return err
	}
	fixture.closed = true
	return nil
}

type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (log *eventLog) add(event string) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.events = append(log.events, event)
}

func (log *eventLog) values() []string {
	log.mu.Lock()
	defer log.mu.Unlock()
	return append([]string(nil), log.events...)
}

type controlledClock struct {
	mu   sync.Mutex
	next time.Time
}

func (clock *controlledClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	value := clock.next
	clock.next = clock.next.Add(time.Second)
	return value
}

type controlledIDs struct {
	next atomic.Uint64
}

func (ids *controlledIDs) New(kind domain.IdentifierKind) (domain.Identifier, error) {
	sequence := ids.next.Add(1)
	return domain.Identifier(fmt.Sprintf("%s_%04d", kind, sequence)), nil
}

type controlledJobRuntime struct {
	events        *eventLog
	recoveryError error
	started       chan struct{}
	done          chan struct{}
	running       atomic.Bool
	closeGate     <-chan struct{}

	startOnce sync.Once
	doneOnce  sync.Once
}

func newControlledJobRuntime(events *eventLog, recoveryError error, closeGate <-chan struct{}) *controlledJobRuntime {
	return &controlledJobRuntime{
		events:        events,
		recoveryError: recoveryError,
		closeGate:     closeGate,
		started:       make(chan struct{}),
		done:          make(chan struct{}),
	}
}

func (runtime *controlledJobRuntime) Restore(context.Context) error {
	runtime.events.add("job_runtime.restore")
	return runtime.recoveryError
}

func (runtime *controlledJobRuntime) Enqueue(_ context.Context, reference ports.SafeJobReference) (ports.EnqueueReceipt, error) {
	if _, err := reference.JobRef(); err != nil {
		return ports.EnqueueReceipt{}, err
	}
	return ports.EnqueueReceipt{Reference: reference}, nil
}

func (runtime *controlledJobRuntime) Run(ctx context.Context, _ ports.JobHandler) error {
	runtime.running.Store(true)
	runtime.events.add("job_runtime.run")
	runtime.startOnce.Do(func() {
		close(runtime.started)
	})

	<-ctx.Done()
	runtime.events.add("job_runtime.canceled")
	runtime.doneOnce.Do(func() {
		close(runtime.done)
	})
	return ctx.Err()
}

func (runtime *controlledJobRuntime) Close(ctx context.Context) error {
	if runtime.running.Load() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-runtime.done:
		}
	}
	if runtime.closeGate != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-runtime.closeGate:
		}
	}
	runtime.events.add("job_runtime.close")
	return nil
}

var (
	_ ports.Clock       = (*controlledClock)(nil)
	_ ports.IDGenerator = (*controlledIDs)(nil)
	_ ports.JobRuntime  = (*controlledJobRuntime)(nil)
)
