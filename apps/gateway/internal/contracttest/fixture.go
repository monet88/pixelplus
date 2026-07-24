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
	Principal ports.PrincipalStore
	Admission ports.AdmissionStore
	Replay    ports.ReplayStore
	Accounts  ports.AccountStore
	// Health is injected independently so public-proof fixtures can control
	// health durability without coupling to AccountStore lifecycle rows.
	Health ports.HealthStore
	// Clock overrides the fixture's controlled clock when non-nil (e.g. for
	// advancing past cooldown timers without reseeding durable health).
	Clock      ports.Clock
	Audit      ports.AuditRecorder
	Telemetry  ports.TelemetryRecorder
	RequestLog ports.RequestLogRecorder

	// Asset exchange request-spine ports (#53). A nil port keeps the production
	// foundation implementation composition substitutes by default; a controlled
	// fake proves the protected Asset spine through real composition.
	AssetReplay   ports.AssetReplayStore
	AssetMetadata ports.AssetMetadataStore
	AssetContent  ports.AssetContentStore
	AssetAudit    ports.AssetAuditRecorder

	// Provider Credential Vault and Probe Adapter ports (#46). A nil port keeps
	// the production fail-closed foundation composition substitutes by default; a
	// controlled fake proves the credential-submit and probe spines through real
	// composition without releasing secret material.
	Vault ports.CredentialVault
	Probe ports.ProbeAdapter
	// OAuth exchange adapter port (#47). A nil port keeps the production
	// fail-closed foundation; a controlled fake proves start/poll through real
	// composition without releasing exchange secrets on the wire.
	OAuth ports.OAuthExchangeAdapter

	// Capability Snapshot ports (#50). A nil port keeps the production fail-closed
	// foundation composition substitutes by default; a controlled fake proves
	// snapshot minting and model listing through real composition.
	Capabilities ports.CapabilityStore
	Capability   ports.CapabilityAdapter

	// Provider Surface Circuit gate (#51). A nil port keeps the foundation
	// closed-circuit store (every surface closed, nothing to block); a controlled
	// fake proves an open cross-Tenant surface blocks matching new work through
	// real composition without exposing the corroborating evidence.
	Circuits ports.CircuitStore

	// Routing Policy store (#52). A nil port keeps the foundation memory store;
	// a controlled fake proves atomic Replace mutation counts through real
	// composition without Vault/Adapter access.
	Routing ports.RoutingPolicyStore

	// Render Job ports (#54). A nil port keeps the foundation memory/fail-closed
	// implementations; controlled fakes prove create/claim/render/placement
	// through real composition and the exported JobExecutor.
	RenderJobs    ports.RenderJobStore
	RenderReplay  ports.RenderReplayStore
	RenderAdapter ports.RenderAdapter
	RenderAudit   ports.RenderAuditRecorder
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

	clock := ports.Clock(&controlledClock{next: fixtureStartTime})
	if options.Clock != nil {
		clock = options.Clock
	}
	runtime, err := composition.New(composition.Config{}, composition.Dependencies{
		Runtime: jobs,
		Clock:   clock,
		IDs:     &controlledIDs{},

		Principal:  options.Principal,
		Admission:  options.Admission,
		Replay:     options.Replay,
		Accounts:   options.Accounts,
		Health:     options.Health,
		Audit:      options.Audit,
		Telemetry:  options.Telemetry,
		RequestLog: options.RequestLog,

		AssetReplay:   options.AssetReplay,
		AssetMetadata: options.AssetMetadata,
		AssetContent:  options.AssetContent,
		AssetAudit:    options.AssetAudit,

		Vault: options.Vault,
		Probe: options.Probe,
		OAuth: options.OAuth,

		Capabilities: options.Capabilities,
		Capability:   options.Capability,

		Circuits: options.Circuits,
		Routing:  options.Routing,

		RenderJobs:    options.RenderJobs,
		RenderReplay:  options.RenderReplay,
		RenderAdapter: options.RenderAdapter,
		RenderAudit:   options.RenderAudit,
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

// EnqueuedReferences returns SafeJobReference values accepted by the fixture
// job runtime. Used by Render Job contract tests to prove one secret-free
// enqueue per admitted create without inspecting private application state.
func (fixture *Fixture) EnqueuedReferences() []ports.SafeJobReference {
	return fixture.jobs.EnqueuedReferences()
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

	// enqueueMu protects enqueueRefs for SafeJobReference observation in
	// Render Job contract tests (#54): admitted create must enqueue exactly
	// one secret-free reference.
	enqueueMu   sync.Mutex
	enqueueRefs []ports.SafeJobReference
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
	runtime.enqueueMu.Lock()
	runtime.enqueueRefs = append(runtime.enqueueRefs, reference)
	runtime.enqueueMu.Unlock()
	runtime.events.add("job_runtime.enqueue")
	return ports.EnqueueReceipt{Reference: reference}, nil
}

// EnqueuedReferences returns a copy of SafeJobReference values accepted by Enqueue.
func (runtime *controlledJobRuntime) EnqueuedReferences() []ports.SafeJobReference {
	runtime.enqueueMu.Lock()
	defer runtime.enqueueMu.Unlock()
	return append([]ports.SafeJobReference(nil), runtime.enqueueRefs...)
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
