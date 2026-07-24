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
	RenderJobs     ports.RenderJobStore
	RenderReplay   ports.RenderReplayStore
	RenderAdapter  ports.RenderAdapter
	RenderStaging  ports.RenderStagingStore
	RenderAudit    ports.RenderAuditRecorder
	RenderDigester ports.RenderDigester
	// RenderDigestKey injects key material when RenderDigester is nil.
	RenderDigestKey []byte
	// AllowInMemoryRenderJobs overrides the fixture default (true). Set false
	// for production-like readiness/digester proofs.
	AllowInMemoryRenderJobs *bool

	// EnqueueFailTimes fails the first N Enqueue calls with EnqueueError (or
	// dependency unavailable) so contract tests prove create+publication recovery.
	EnqueueFailTimes int
	EnqueueError     error
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
	if options.EnqueueFailTimes > 0 {
		jobs.enqueueFailRemaining.Store(int32(options.EnqueueFailTimes))
		jobs.enqueueError = options.EnqueueError
	}

	clock := ports.Clock(&controlledClock{next: fixtureStartTime})
	if options.Clock != nil {
		clock = options.Clock
	}
	allowInMemory := true
	if options.AllowInMemoryRenderJobs != nil {
		allowInMemory = *options.AllowInMemoryRenderJobs
	}
	runtime, err := composition.New(composition.Config{
		// Controlled fixtures only: process-local job store is not production
		// durable state. Production composition leaves this false and fails closed.
		AllowInMemoryRenderJobs: allowInMemory,
	}, composition.Dependencies{
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

		RenderJobs:      options.RenderJobs,
		RenderReplay:    options.RenderReplay,
		RenderAdapter:   options.RenderAdapter,
		RenderStaging:   options.RenderStaging,
		RenderAudit:     options.RenderAudit,
		RenderDigester:  options.RenderDigester,
		RenderDigestKey: options.RenderDigestKey,
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

	// enqueueMu protects observation log + delivery backlog + publication set.
	// enqueueRefs is append-only history for "exactly one publication" proofs
	// (first logical accept only). pending is the delivery backlog for Run.
	enqueueMu   sync.Mutex
	enqueueRefs []ports.SafeJobReference
	pending     []ports.SafeJobReference
	published   map[string]struct{}
	// wakeup signals Run that pending grew (buffered 1; nonblocking Enqueue).
	wakeup chan struct{}
	// enqueueFailRemaining, when >0, fails the next Enqueue calls with
	// enqueueError (or ErrDependencyUnavailable) then decrements — used to
	// prove durable create + publication recovery (#14 §3.3).
	enqueueFailRemaining atomic.Int32
	enqueueError         error
}

func newControlledJobRuntime(events *eventLog, recoveryError error, closeGate <-chan struct{}) *controlledJobRuntime {
	return &controlledJobRuntime{
		events:        events,
		recoveryError: recoveryError,
		closeGate:     closeGate,
		started:       make(chan struct{}),
		done:          make(chan struct{}),
		wakeup:        make(chan struct{}, 1),
		published:     make(map[string]struct{}),
	}
}

func (runtime *controlledJobRuntime) Restore(context.Context) error {
	runtime.events.add("job_runtime.restore")
	return runtime.recoveryError
}

func controlledPublicationKey(reference ports.SafeJobReference) string {
	return string(reference.TenantID) + "/" + string(reference.JobID)
}

func (runtime *controlledJobRuntime) Enqueue(_ context.Context, reference ports.SafeJobReference) (ports.EnqueueReceipt, error) {
	if _, err := reference.JobRef(); err != nil {
		return ports.EnqueueReceipt{}, err
	}
	if remaining := runtime.enqueueFailRemaining.Load(); remaining > 0 {
		if runtime.enqueueFailRemaining.CompareAndSwap(remaining, remaining-1) {
			runtime.events.add("job_runtime.enqueue_fail")
			err := runtime.enqueueError
			if err == nil {
				err = ports.ErrDependencyUnavailable
			}
			return ports.EnqueueReceipt{}, err
		}
	}
	// Nonblocking + idempotent by SafeJobReference identity (matches jobs.Runtime).
	runtime.enqueueMu.Lock()
	if runtime.published == nil {
		runtime.published = make(map[string]struct{})
	}
	key := controlledPublicationKey(reference)
	if _, seen := runtime.published[key]; seen {
		if !runtime.hasPendingLocked(key) {
			runtime.pending = append(runtime.pending, reference)
			runtime.enqueueMu.Unlock()
			runtime.signalWakeup()
			return ports.EnqueueReceipt{Reference: reference}, nil
		}
		runtime.enqueueMu.Unlock()
		return ports.EnqueueReceipt{Reference: reference}, nil
	}
	runtime.published[key] = struct{}{}
	runtime.enqueueRefs = append(runtime.enqueueRefs, reference)
	runtime.pending = append(runtime.pending, reference)
	runtime.enqueueMu.Unlock()
	runtime.events.add("job_runtime.enqueue")
	runtime.signalWakeup()
	return ports.EnqueueReceipt{Reference: reference}, nil
}

func (runtime *controlledJobRuntime) hasPendingLocked(key string) bool {
	for _, ref := range runtime.pending {
		if controlledPublicationKey(ref) == key {
			return true
		}
	}
	return false
}

func (runtime *controlledJobRuntime) signalWakeup() {
	select {
	case runtime.wakeup <- struct{}{}:
	default:
	}
}

func (runtime *controlledJobRuntime) takePending() (ports.SafeJobReference, bool) {
	runtime.enqueueMu.Lock()
	defer runtime.enqueueMu.Unlock()
	if len(runtime.pending) == 0 {
		return ports.SafeJobReference{}, false
	}
	ref := runtime.pending[0]
	runtime.pending = runtime.pending[1:]
	return ref, true
}

func (runtime *controlledJobRuntime) requeueFront(reference ports.SafeJobReference) {
	runtime.enqueueMu.Lock()
	runtime.pending = append([]ports.SafeJobReference{reference}, runtime.pending...)
	runtime.enqueueMu.Unlock()
}

// EnqueuedReferences returns a copy of SafeJobReference values accepted by Enqueue.
// Only first logical accepts are recorded (idempotent re-Enqueue does not grow this).
func (runtime *controlledJobRuntime) EnqueuedReferences() []ports.SafeJobReference {
	runtime.enqueueMu.Lock()
	defer runtime.enqueueMu.Unlock()
	return append([]ports.SafeJobReference(nil), runtime.enqueueRefs...)
}

// Run delivers accepted SafeJobReference values to handler so
// composition.Runtime.RunWorkers can invoke the exported JobExecutor.
// Enqueue before or during Run is supported without deadlock. Handler errors
// requeue the same reference (at-least-once) and surface to the Run caller.
func (runtime *controlledJobRuntime) Run(ctx context.Context, handler ports.JobHandler) error {
	runtime.running.Store(true)
	runtime.events.add("job_runtime.run")
	runtime.startOnce.Do(func() {
		close(runtime.started)
	})
	defer func() {
		runtime.doneOnce.Do(func() {
			close(runtime.done)
		})
	}()

	if handler == nil {
		<-ctx.Done()
		runtime.events.add("job_runtime.canceled")
		return ctx.Err()
	}

	for {
		reference, ok := runtime.takePending()
		if !ok {
			select {
			case <-ctx.Done():
				runtime.events.add("job_runtime.canceled")
				return ctx.Err()
			case <-runtime.wakeup:
				continue
			}
		}
		if err := handler(ctx, reference); err != nil {
			runtime.requeueFront(reference)
			return err
		}
	}
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
