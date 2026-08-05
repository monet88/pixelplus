package application

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// StreamChatCommand is the typed streaming chat request. It mirrors
// CreateChatCompletionCommand because the client selects streaming through the
// same OpenAI-compatible body (`stream: true`); only the resolved operation
// token differs, and that difference is what makes capability, telemetry, and
// health scoping classify streaming work correctly.
type StreamChatCommand struct {
	RequestID            domain.Identifier
	PresentedKeyMaterial string
	IdempotencyKey       string
	Model                string
	Messages             []domain.ChatMessage
	Options              domain.ChatRequestOptions
	OversizeBody         bool
	MalformedBody        bool
}

// ChatStreamHandshake is the safe metadata the transport needs to write the
// canonical `open` event. It is produced only after every pre-upstream gate
// passed, so an `open` event never precedes a rejection.
type ChatStreamHandshake struct {
	RequestID         domain.Identifier
	ExecutionID       domain.Identifier
	ProviderAccountID domain.ProviderAccountID
	Model             string
	// StreamingClass discloses whether the serving mode streams natively
	// (`real`) or synthesizes chunks from a buffered body (`synthetic`). It is
	// copied verbatim from the selected account's Capability Snapshot; the
	// Gateway never relabels synthetic as real (chat lifecycle §4.4).
	StreamingClass domain.StreamingClass
}

// ChatStreamTerminal is the single client terminal outcome of a stream. Exactly
// one is delivered per accepted stream (I-CHAT-CANON-TERMINAL).
type ChatStreamTerminal struct {
	// Event is the canonical terminal event type (completed/failed/canceled).
	Event domain.ChatStreamEventType
	// FinishClass is the terminal classification carried by the event.
	FinishClass domain.FinishClass
	// Usage is the canonical token accounting for a completed stream.
	Usage domain.ChatUsage
	// Error is the canonical error carried by a `failed` terminal. It is the
	// zero value for completed/canceled terminals.
	Error domain.CanonicalError
	// UpstreamAbortAttempted reports whether an upstream abort was attempted on
	// cancellation. It is never used to claim the upstream actually stopped.
	UpstreamAbortAttempted bool
	// UpstreamStopConfirmed reports only confirmed upstream stops. Cancellation
	// alone is not proof upstream stopped (OpenAPI ChatCanceledEvent).
	UpstreamStopConfirmed bool
	// DeliveredContent is the concatenated assistant content the client actually
	// received. It never reaches the wire — the client already got it as deltas —
	// but the durable replay record must persist it, otherwise a matching replay
	// reconstructs an empty assistant message and silently loses the text.
	DeliveredContent string
}

// ChatStreamTransport is the transport-owned side of one streaming response.
// The application drives it, so the ordering contract lives in one place: Open
// is called exactly once before any content and Terminal exactly once at the
// end, and the transport refuses out-of-order writes through
// domain.ChatStreamOrder.
//
// Sink returns the delta/heartbeat-only sink. The Adapter therefore cannot reach
// Open or Terminal at all.
type ChatStreamTransport interface {
	// Open writes the single canonical open event carrying safe metadata.
	Open(ChatStreamHandshake) error
	// Sink returns the content sink for Adapter deltas and heartbeats.
	Sink() domain.ChatSink
	// Terminal writes the single canonical terminal event and ends the stream.
	Terminal(ChatStreamTerminal) error
	// DeltaCount reports how many canonical delta events already reached the
	// client. A non-zero count forbids restarting generation on a fallback
	// account: splicing a second generation onto a partially delivered stream
	// would corrupt reconstruction even under authoritative no-commit proof
	// (chat lifecycle §5.4 "mid-stream fallback is not silent re-emission").
	DeltaCount() int
}

// lazyStream defers the canonical `open` event until the stream actually has
// something to say: the first Adapter delta/heartbeat, or the terminal decision.
//
// Deferring is what keeps the HTTP contract honest. The status line is committed
// the moment `open` is written, so anything that fails before real upstream work
// — a fail-closed streaming Adapter, an absent credential, a gate that rejects —
// must be expressible as a status code rather than a 200 that immediately
// carries a failure event. Ordering is unaffected: `open` is still emitted
// exactly once and still precedes every delta, because every path to content
// runs through ensureOpen first.
//
// It is also the sink handed to the Adapter, so it must tolerate a drifting
// Adapter that writes from another goroutine and keeps writing after its
// attempt returned. Two properties make that safe:
//
//  1. mu serializes state, so a late write cannot race the spine's own
//     open/terminal decisions.
//  2. seal permanently closes the sink once the spine owns the outcome. After
//     sealing, a late write is refused WITHOUT touching the transport — which
//     matters because an http.ResponseWriter is invalid once its handler has
//     returned, so writing to it would panic in the server goroutine.
type lazyStream struct {
	transport ChatStreamTransport
	handshake ChatStreamHandshake

	mu      sync.Mutex
	opened  bool
	openErr error
	sealed  bool
	// delivered accumulates the canonical assistant content actually delivered to
	// the client. It is what the durable replay record must persist: without it a
	// matching replay reconstructs an EMPTY assistant message, so an idempotent
	// retry silently loses the text the first call already delivered.
	delivered strings.Builder
}

// ensureOpen writes the open event once, on first demand. Callers must hold mu.
func (stream *lazyStream) ensureOpen() error {
	if stream.sealed {
		return domain.ErrChatStreamTerminated
	}
	if stream.opened {
		return stream.openErr
	}
	stream.opened = true
	stream.openErr = stream.transport.Open(stream.handshake)
	return stream.openErr
}

// Opened reports whether the canonical open event was written, and therefore
// whether the client is committed to a stream rather than an HTTP status.
func (stream *lazyStream) Opened() bool {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.opened && stream.openErr == nil
}

// Delta opens the stream on first content, then delivers the fragment.
func (stream *lazyStream) Delta(delta domain.ChatDelta) error {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if err := stream.ensureOpen(); err != nil {
		return err
	}
	if err := stream.transport.Sink().Delta(delta); err != nil {
		return err
	}
	// Record only what the transport accepted, so the replay record can never
	// claim content the client did not receive.
	stream.delivered.WriteString(delta.Content)
	return nil
}

// Heartbeat opens the stream on first keepalive, then delivers it.
func (stream *lazyStream) Heartbeat() error {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if err := stream.ensureOpen(); err != nil {
		return err
	}
	return stream.transport.Sink().Heartbeat()
}

// terminal opens the stream if needed and writes the single terminal event, then
// seals the sink so nothing can follow the terminal.
func (stream *lazyStream) terminal(terminal ChatStreamTerminal) error {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if err := stream.ensureOpen(); err != nil {
		return err
	}
	err := stream.transport.Terminal(terminal)
	stream.sealed = true
	return err
}

// seal closes the sink without writing anything. It is used when the spine
// answers with a canonical HTTP status instead of a stream, so a late Adapter
// write can never open a stream on top of an already-sent error response.
func (stream *lazyStream) seal() {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	stream.sealed = true
}

// deltaCount reports delivered canonical deltas.
func (stream *lazyStream) deltaCount() int {
	return stream.transport.DeltaCount()
}

// deliveredContent is the concatenated assistant content the client received, for
// the durable replay record.
func (stream *lazyStream) deliveredContent() string {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return stream.delivered.String()
}

var _ domain.ChatSink = (*lazyStream)(nil)

// chatSettlementBudget bounds the accounting work that runs AFTER the client
// terminal (§6.5 rules 3-4). Because settlement is deliberately detached from
// the request context so a disconnect cannot abort it, it needs its own ceiling:
// without one, a hung residual drain would pin a goroutine and its retained
// occupancy indefinitely. Reaching the budget is a legitimate outcome — it
// yields unknown usage, which fails accounting closed rather than refunding
// (§6.5 rule 3). The exact drain/recovery deadline is #17; this is the spine's
// conservative default until that numeric lands.
const chatSettlementBudget = 30 * time.Second

// StreamChat runs the protected streaming chat spine. Phase order matches chat
// lifecycle §3.1 with one structural rule: every pre-upstream gate (A0-A5,
// X1-X3, lease) runs BEFORE the stream opens, so those rejections are returned
// as canonical errors the transport can still express as an HTTP status. Once
// the stream is open the status line is committed, so from that point on every
// outcome — including failure and cancellation — is delivered as exactly one
// canonical terminal event.
//
// The returned error is non-nil only for a pre-stream rejection. After the
// stream opens, StreamChat returns nil: the terminal event is the client-facing
// outcome, and no second client event is ever emitted (§6.5 rule 1).
func (service *ChatService) StreamChat(ctx context.Context, command StreamChatCommand, transport ChatStreamTransport) error {
	if transport == nil {
		return domain.NewInternalError()
	}
	sc := spineContext{
		operation: domain.OperationChatCompletionStreaming,
		requestID: service.resolveRequestID(command.RequestID),
		start:     service.clock.Now(),
	}

	// A0 authenticate.
	principal, canonical, ok := service.authenticate(ctx, ports.PresentedClientAPIKey{Material: command.PresentedKeyMaterial})
	if !ok {
		return service.fail(ctx, sc, canonical)
	}
	sc.keyID = principal.ClientAPIKeyID

	// A1 scope.
	if !principal.Scopes.Has(domain.ChatOpCompletionStreaming.RequiredScope()) {
		return service.fail(ctx, sc, domain.NewForbidden())
	}

	// A2 size/malformed/request validation (single normative order).
	if command.OversizeBody {
		return service.fail(ctx, sc, domain.NewRequestTooLarge())
	}
	if command.MalformedBody {
		return service.fail(ctx, sc, domain.NewInvalidRequest())
	}
	if command.Model == "" || len(command.Messages) == 0 {
		return service.fail(ctx, sc, domain.NewInvalidRequest())
	}
	for _, message := range command.Messages {
		if !message.Valid() {
			return service.fail(ctx, sc, domain.NewInvalidRequest())
		}
	}
	if command.IdempotencyKey == "" || utf8.RuneCountInString(command.IdempotencyKey) > maxIdempotencyKeyLength {
		return service.fail(ctx, sc, domain.NewInvalidRequest())
	}

	// Streaming must be composed before any side effect. A missing streaming
	// Adapter fails closed here rather than being served as a non-streaming
	// answer (chat lifecycle §3.2 rule 2).
	if service.authorizedStream == nil {
		return service.fail(ctx, sc, domain.NewDependencyUnavailable())
	}

	// Keyed digester before any replay/admission side effect. The streaming
	// operation is part of the fingerprint, so the same body streamed and
	// non-streamed under one idempotency key conflicts instead of replaying a
	// non-streaming completion into a stream.
	fingerprint, err := service.digester.CreateFingerprint(domain.ChatOpCompletionStreaming, command.Model, command.Messages, command.Options)
	if err != nil {
		return service.fail(ctx, sc, service.dependencyCanonical(err))
	}
	identity := domain.ReplayIdentity{
		Scope: domain.ReplayScope{
			TenantID:       principal.TenantID,
			ClientAPIKeyID: principal.ClientAPIKeyID,
			Key:            command.IdempotencyKey,
		},
		Fingerprint: fingerprint,
	}
	decision, err := service.replay.Claim(ctx, identity)
	if err != nil {
		return service.fail(ctx, sc, service.dependencyCanonical(err))
	}
	switch decision.Outcome {
	case ports.ReplayClaimed:
		// sole owner continues below
	case ports.ReplayTerminal:
		// A terminal replay owns a completed generation. Re-streaming stored
		// content would fabricate token-by-token delivery of an execution that
		// already finished, so the canonical replay is delivered as a single
		// delta plus the recorded terminal — honest about what happened while
		// keeping canonical ordering and making no new Adapter call.
		return service.streamReplay(ctx, sc, principal, transport, decision.TerminalResult)
	case ports.ReplayInProgress:
		return service.fail(ctx, sc, domain.NewIdempotencyInProgress())
	case ports.ReplayConflict:
		return service.fail(ctx, sc, domain.NewIdempotencyConflict())
	case ports.ReplayUncertain:
		return service.fail(ctx, sc, domain.NewIdempotencyUncertain())
	default:
		return service.fail(ctx, sc, domain.NewInternalError())
	}

	// A3-A5 admission before routing/side effects. Streaming counts as ONE
	// request at admission, not per event (§6.5 rule 6).
	reservation, canonical, ok := service.admit(ctx, principal, domain.OperationChatCompletionStreaming)
	if !ok {
		if abErr := service.abandon(ctx, identity); abErr != nil {
			return service.fail(ctx, sc, service.dependencyCanonical(abErr))
		}
		return service.fail(ctx, sc, canonical)
	}

	// X1 route/select under the STREAMING operation, so candidates are filtered
	// by chat_streaming capability rather than non-streaming chat.
	account, policy, canonical, ok := service.selectAccount(ctx, principal, domain.ChatOpCompletionStreaming, command.Model, sc.start, command.Options.ProviderAccountID, command.Options.ConversationID)
	if !ok {
		return service.failAfterRollback(ctx, sc, canonical, reservation, identity)
	}

	executionID, err := service.ids.New(domain.IdentifierKindExecution)
	if err != nil {
		return service.failAfterRollback(ctx, sc, domain.NewInternalError(), reservation, identity)
	}

	// P2 hard lease: bind the stream to exactly one same-Tenant account for its
	// duration before opening the stream, so no account hop can occur mid-stream.
	lease, canonical, ok := service.acquireStreamLease(ctx, principal, policy, account, executionID)
	if !ok {
		return service.failAfterRollback(ctx, sc, canonical, reservation, identity)
	}
	if lease != nil {
		defer func() {
			_ = service.streamLeases.Release(ctx, *lease)
		}()
	}

	execution := chatStreamExecution{
		sc:          sc,
		principal:   principal,
		policy:      policy,
		command:     command,
		executionID: executionID,
		transport:   transport,
		leased:      lease != nil,
	}

	// Register the in-flight execution so an explicit cancel (§6.2) or
	// disconnect (§6.3) can signal it. The cancel context is a child of the
	// request context: a client disconnect cancels the request context, which
	// cancels this child; an explicit cancel calls the registered CancelFunc.
	// The child context is what runStream -> attemptStreamOnAccount ->
	// authorizedStream.Stream hands to the Adapter, so a cancel signal reaches a
	// running execution and is never discarded.
	execCtx, execCancel := context.WithCancel(ctx)
	service.executions.register(principal.TenantID, principal.ClientAPIKeyID, executionID, execCancel)
	defer func() {
		execCancel()
	}()

	served, terminal, opened := service.runStream(execCtx, execution, account)

	// Everything after the client terminal is ACCOUNTING work, and accounting
	// must survive the client. A disconnect cancels the request context, so
	// running settlement on `ctx` would hand an already-canceled context to
	// Reconcile and leak the Tenant+key occupancy forever — exactly the
	// untracked work §6.3 rule 2 forbids. Detaching cancellation (while keeping
	// request-scoped values) and imposing our own bound is what makes X6
	// reachable on the disconnect path (§6.5 rule 4).
	settleCtx, settleCancel := context.WithTimeout(context.WithoutCancel(ctx), chatSettlementBudget)
	defer settleCancel()

	// X5/X6 settlement (§6.5). The terminal determines whether occupancy
	// releases immediately at X5 (upstream stopped confirmed or authoritative
	// non-commit) or is held for a bounded drain at X6 (upstream may survive).
	reservation.SettlementKey = chatSettlementKey(principal, executionID)

	// Record terminal state in the registry so a later explicit cancel is an
	// idempotent no-op (§6.2 rule 5).
	service.executions.markTerminal(executionID, terminal.UpstreamAbortAttempted, terminal.UpstreamStopConfirmed)

	if !opened {
		// Pre-upstream rejection: no stream was opened, so the client can still
		// receive a canonical HTTP error. Release the claim when non-commit is
		// authoritative; never release an uncertain claim (no steal).
		if terminal.Error.Code != domain.ErrCodeExecutionPossiblyCommitted {
			_ = service.abandon(settleCtx, identity)
		}
		// For a pre-upstream rejection there is no stream to drain: settle
		// immediately (X5 = X6). The reservation reconciles to whatever the
		// terminal carries (nothing for a failed/possibly-committed terminal,
		// which is the correct fail-closed behavior per §6.5 rule 3).
		if terminalCarriesUsage(terminal.Event) {
			reservation.Usage = admissionUsage(terminal.Usage)
		}
		if reconcileErr := service.admission.Reconcile(settleCtx, reservation); reconcileErr != nil {
			return service.fail(ctx, sc, service.dependencyCanonical(reconcileErr))
		}
		return service.fail(ctx, sc, terminal.Error)
	}

	// For an opened stream, X5 may differ from X6 when the upstream may survive
	// the client terminal (§6.5 rule 1). settleStream handles the coincide
	// (release now) and split (hold + drain + release at X6) paths.
	settleErr := service.settleStream(settleCtx, reservation, terminal, execution, served)

	// The stream was opened and its single terminal event has been delivered, so
	// the client outcome is already final. Record durable state and observability
	// without emitting any second client event (§6.5 rule 1).
	//
	// The open audit is recorded HERE, once, because only now is it true: the
	// stream really opened on `served`. Recording it before the Adapter ran would
	// claim `stream_opened` for a fail-closed Adapter that never opened anything,
	// and would emit one record per attempted account during a fallback walk.
	//
	// These are durable/observability writes on the same accounting side of the
	// client terminal, so they run on settleCtx too: a disconnect must not lose
	// the replay record or the audit trail.
	_ = service.chatAudit(settleCtx, sc, principal, served.ID, executionID, ports.AuditChatStreamOpened, "stream_opened")
	service.recordStreamTerminalState(settleCtx, execution, served, terminal, identity, settleErr)
	service.recordTelemetry(settleCtx, sc.operation, terminal.Error.Code, terminal.HTTPStatusHint())
	service.recordRequestLog(settleCtx, sc.requestID, principal.ClientAPIKeyID, string(sc.operation), terminal.HTTPStatusHint(), terminal.logMessage(), sc.start)
	return nil
}

// settleStream performs the X5/X6 settlement for one stream terminal. When X5
// and X6 coincide (upstream stopped confirmed or authoritative non-commit), it
// reconciles immediately. When they split (upstream may survive), it holds the
// reservation, optionally acquires residual tracking, runs a bounded drain,
// and reconciles at X6 (§6.5 rules 1-4).
//
// The returned error is non-nil only when settlement itself failed (a
// dependency outcome) or when final usage is unavailable after bounded drain
// (§6.5 rule 3 accounting fault); it is folded into audit/telemetry rather
// than emitted to the client, because the client terminal was already
// delivered.
func (service *ChatService) settleStream(
	ctx context.Context,
	reservation ports.AdmissionReservation,
	terminal ChatStreamTerminal,
	execution chatStreamExecution,
	served domain.ProviderAccount,
) error {
	if upstreamStopped(terminal) {
		// X5 = X6: release occupancy and settle quota now (§6.5 rule 1).
		if terminalCarriesUsage(terminal.Event) {
			reservation.Usage = admissionUsage(terminal.Usage)
		}
		return service.admission.Reconcile(ctx, reservation)
	}

	// X5 != X6: upstream may survive. Hold the reservation and try residual
	// tracking (§6.5 rule 2). The client terminal is already delivered; X6 emits
	// no second client event.
	hold := ports.ChatResidualHold{
		TenantID:       execution.principal.TenantID,
		ClientAPIKeyID: execution.principal.ClientAPIKeyID,
		ExecutionID:    execution.executionID,
		AccountID:      served.ID,
	}
	residualAcquired := false
	if service.residualStore != nil {
		if err := service.residualStore.Acquire(ctx, hold); err != nil {
			// Capacity full: retain the original request state. The spine keeps
			// the original occupancy and reservation held; no transfer occurs
			// (§6.5 rule 2 "If residual tracking is full, retain the original
			// request state"). Neither path frees capacity for another A6 accept.
			residualAcquired = false
		} else {
			residualAcquired = true
		}
	}

	// Bounded drain/recovery (§6.5 rule 3). The drain is the only source of
	// FINAL usage. The terminal's observed usage is at most a known conservative
	// floor; a nil drain returns unknown immediately, so settlement fails closed.
	finalUsage := terminal.Usage
	usageKnown := terminalCarriesUsage(terminal.Event)
	finalConfirmed := false
	if service.residualDrain != nil {
		outcome, err := service.residualDrain.Drain(ctx, ports.ChatResidualDrainRequest{
			Hold:               hold,
			AccountID:          served.ID,
			ObservedUsage:      terminal.Usage,
			ObservedUsageKnown: usageKnown,
		})
		if err == nil && outcome.UsageKnown {
			finalUsage = outcome.Usage
			usageKnown = true
			finalConfirmed = true
		}
		// A drain error or unknown usage leaves finalConfirmed false so
		// settlement emits an accounting fault below.
	}

	// X6: settle quota and release occupancy exactly once (§6.5 rule 4).
	if usageKnown {
		reservation.Usage = admissionUsage(finalUsage)
	}
	settleErr := service.admission.Reconcile(ctx, reservation)

	// Release the residual hold if it was acquired.
	if residualAcquired && service.residualStore != nil {
		_ = service.residualStore.Release(ctx, hold)
	}

	if settleErr != nil {
		return settleErr
	}

	// If the drain could not confirm FINAL usage, emit the accounting fault
	// marker so the audit record carries the conservative-settlement outcome
	// (§6.5 rule 3 "emit an operator-visible accounting fault"). The reservation
	// was already settled to the known floor above (or retained in full when no
	// floor existed), so the fault is recorded without optimistically refunding
	// the unknown remainder.
	if !finalConfirmed {
		return errors.New("chat residual accounting fault: final usage unavailable after bounded drain")
	}
	return nil
}

// upstreamStopped reports whether X5 and X6 coincide: the upstream is known to
// have stopped, so there is nothing left to drain and occupancy releases now
// (§6.5 rule 1).
//
// It is deliberately conservative. Only three terminals qualify:
//   - `completed`: the generation ended naturally.
//   - `canceled` WITH an Adapter-proved stop. Cancellation alone is never proof
//     (§6.2 rule 3), so a bare `canceled` falls through to the residual path.
//   - `failed` whose canonical error proves the upstream never committed.
//
// The commit status is what decides the `failed` case, never the abort flag:
// UpstreamAbortAttempted is only ever populated for `canceled` terminals, so
// testing it here would classify every `failed` as stopped. That is how an
// `upstream_timeout` — which §6.4 rule 2 says the Gateway MUST attempt to abort,
// and §6.4 rule 3 says follows the same residual rules as cancel — would have
// skipped the residual protocol entirely and settled as if upstream were dead.
func upstreamStopped(terminal ChatStreamTerminal) bool {
	switch terminal.Event {
	case domain.ChatStreamCompleted:
		return true
	case domain.ChatStreamCanceled:
		return terminal.UpstreamStopConfirmed
	case domain.ChatStreamFailed:
		return terminal.UpstreamStopConfirmed || authoritativeNonCommit(terminal.Error.Code)
	default:
		return false
	}
}

// authoritativeNonCommit reports whether a canonical error proves the upstream
// never started a billable generation, so no residual work can survive.
//
// A timeout or a transport loss is NOT proof: the request may well have reached
// the Provider and still be generating, which is precisely the surviving-upstream
// case §6.5 exists to account for. Only a rejection the Gateway observed BEFORE
// the upstream accepted work qualifies.
func authoritativeNonCommit(code domain.ErrorCode) bool {
	switch code {
	case domain.ErrCodeUpstreamTimeout,
		domain.ErrCodeUpstreamUnavailable,
		domain.ErrCodeUpstreamProtocolDrift,
		domain.ErrCodeExecutionPossiblyCommitted:
		return false
	default:
		return true
	}
}

// terminalCarriesUsage reports whether a terminal event carries authoritative
// observed usage from the Adapter (`completed`, `canceled`). It is the single
// place that "usage-bearing terminal" is defined so the X5/X6 settlement paths
// cannot drift apart (chat lifecycle §6.5).
func terminalCarriesUsage(event domain.ChatStreamEventType) bool {
	return event == domain.ChatStreamCompleted || event == domain.ChatStreamCanceled
}

// chatStreamExecution is the per-stream execution state shared by the attempt
// walk and the terminal bookkeeping.
type chatStreamExecution struct {
	sc          spineContext
	principal   domain.SecurityPrincipal
	policy      domain.RoutingPolicy
	command     StreamChatCommand
	executionID domain.Identifier
	transport   ChatStreamTransport
	leased      bool
}

// runStream performs the X2-X4 attempt walk and returns the account that served
// the stream plus the single terminal outcome.
//
// opened reports whether the client is committed to a stream. When it is false
// nothing reached the client, so the caller answers with a canonical HTTP status
// instead of a terminal event.
func (service *ChatService) runStream(
	ctx context.Context,
	execution chatStreamExecution,
	primary domain.ProviderAccount,
) (domain.ProviderAccount, ChatStreamTerminal, bool) {
	request := chatRequest{model: execution.command.Model, messages: execution.command.Messages}

	// A hard lease binds the stream to exactly one account, so a leased stream
	// never walks a fallback chain. Without a lease the walk is still bounded by
	// the proof-of-non-commit rule and the no-delta rule in classifyStreamOutcome.
	attempts := []domain.ProviderAccount{primary}
	if !execution.leased {
		attempts = service.attemptAccounts(ctx, execution.sc, execution.principal, primary, execution.policy, domain.ChatOpCompletionStreaming, request, execution.command.Options.AllowFallback)
	}

	served := primary
	lastTerminal := ChatStreamTerminal{
		Event:       domain.ChatStreamFailed,
		FinishClass: domain.FinishFailed,
		Error:       domain.NewInternalError().WithRequestID(execution.sc.requestID),
	}
	for index, account := range attempts {
		terminal, opened, canonical, settled := service.attemptStreamOnAccount(ctx, execution, account, request)
		served = account
		lastTerminal = terminal
		if settled {
			return account, terminal, opened
		}
		if opened {
			// Content already reached the client, so no other account may continue
			// this stream.
			return account, terminal, true
		}
		if canonical.Code == domain.ErrCodeExecutionPossiblyCommitted {
			// Commit unknown: fail closed, never fall back or replace.
			return account, terminal, false
		}
		if index == len(attempts)-1 {
			break
		}
	}
	return served, lastTerminal, false
}

// attemptStreamOnAccount runs the X2 reaffirmation, Vault gate, `open` event and
// one streaming Adapter attempt on a single account.
//
// settled reports that this attempt owns the final outcome (no further account
// may be tried). opened reports whether the client is already committed to a
// stream, which forbids answering with an HTTP status instead of a terminal
// event. On a settled+opened result the terminal event has already been written.
func (service *ChatService) attemptStreamOnAccount(
	ctx context.Context,
	execution chatStreamExecution,
	account domain.ProviderAccount,
	request chatRequest,
) (ChatStreamTerminal, bool, domain.CanonicalError, bool) {
	// X2 selected-account reaffirmation immediately before credential access,
	// under the streaming operation token.
	if canonical, ok := service.candidateRejection(ctx, execution.principal, account, domain.ChatOpCompletionStreaming, request.model, execution.sc.start); !ok {
		return ChatStreamTerminal{}, false, canonical, false
	}

	// X3 Vault presence gate before Adapter entry.
	validation, err := service.vault.Validate(ctx, ports.CredentialValidation{
		Principal: execution.principal,
		AccountID: account.ID,
		AuthMode:  account.AuthMode,
		Version:   account.Credential.Version,
	})
	if err != nil {
		if errors.Is(err, ports.ErrCredentialAbsent) {
			return ChatStreamTerminal{}, false, domain.NewAccountNotUsable(domain.RemediationSubmitCredential), false
		}
		return ChatStreamTerminal{}, false, service.dependencyCanonical(err), false
	}
	if !validation.Valid {
		return ChatStreamTerminal{}, false, domain.NewAccountNotUsable(domain.RemediationSubmitCredential), false
	}

	// Every gate passed. The stream opens with server-owned execution identity
	// on the FIRST canonical content or at the terminal decision, whichever comes
	// first, disclosing the honest streaming class. Deferring the open event this
	// way keeps a fail-closed Adapter a real HTTP status instead of a 200 that
	// immediately carries a failure event, while still guaranteeing `open`
	// precedes every delta.
	stream := &lazyStream{
		transport: execution.transport,
		handshake: ChatStreamHandshake{
			RequestID:         execution.sc.requestID,
			ExecutionID:       execution.executionID,
			ProviderAccountID: account.ID,
			Model:             request.model,
			StreamingClass:    service.streamingClass(ctx, execution.principal, account, execution.sc.start),
		},
	}
	// The send boundary is the only authoritative witness of whether Provider
	// payload transmission began for THIS attempt. A transport error alone cannot
	// distinguish "never left the Gateway" from "reached the Provider and the
	// generation may be running", and §7.2 rule 2 requires that distinction before
	// any re-attempt or fallback.
	sendBoundary := &observedChatSendBoundary{}
	outcome, err := service.authorizedStream.Stream(ctx, ports.AuthorizedChatStreamRequest{
		Principal:    execution.principal,
		AccountID:    account.ID,
		AuthMode:     account.AuthMode,
		Version:      account.Credential.Version,
		Operation:    domain.ChatOpCompletionStreaming,
		Model:        request.model,
		Messages:     request.messages,
		RequestID:    execution.sc.requestID,
		ExecutionID:  execution.executionID,
		SendBoundary: sendBoundary,
	}, stream)
	if err != nil {
		canonical := service.streamDependencyCanonical(err)
		if stream.deltaCount() > 0 {
			// Deltas already reached the client: the generation is possibly
			// committed and the stream must end `failed`, never be retried.
			possiblyCommitted := ChatStreamTerminal{
				Event:       domain.ChatStreamFailed,
				FinishClass: domain.FinishFailed,
				Error:       domain.NewExecutionPossiblyCommitted().WithRequestID(execution.sc.requestID),
			}
			_ = stream.terminal(possiblyCommitted)
			return possiblyCommitted, true, canonical, true
		}
		if sendBoundary.PayloadSent() {
			// Payload transmission began and the Adapter returned an unclassified
			// transport error, so this attempt is possibly committed even though no
			// delta reached the client: §7.2 rule 2 states an "HTTP status, missing
			// response, timeout, reset, or absence of client-visible deltas is not
			// proof by itself". Fail closed on this account — never fall back, which
			// §7.2 rule 4 binds to the same proof boundary — so one accepted request
			// can never cause a second committed generation (§7.5).
			//
			// Nothing reached the client, so the outcome is still expressible as a
			// canonical HTTP status rather than a stream terminal.
			uncertain := domain.NewExecutionPossiblyCommitted().WithRequestID(execution.sc.requestID)
			stream.seal()
			return ChatStreamTerminal{
				Event:       domain.ChatStreamFailed,
				FinishClass: domain.FinishFailed,
				Error:       uncertain,
			}, false, uncertain, false
		}
		// Nothing reached the client yet, so the failure is still expressible as a
		// canonical HTTP status, and the payload never left the Gateway, so
		// non-commit is authoritative (§7.2 rule 2 "no request payload bytes were
		// transmitted"): report it as not-opened and let the caller decide between a
		// fallback attempt and an HTTP rejection.
		stream.seal()
		return ChatStreamTerminal{Event: domain.ChatStreamFailed, FinishClass: domain.FinishFailed, Error: canonical}, false, canonical, false
	}

	terminal, opened, canonical, settled := service.classifyStreamOutcome(execution, stream, outcome)
	if opened {
		// Capture what the client actually received BEFORE the terminal seals the
		// sink, so the durable replay record can reconstruct the same content.
		terminal.DeliveredContent = stream.deliveredContent()
		// Deliver the single terminal event through the same lazy stream, so a
		// zero-delta stream still emits `open` before its terminal.
		if err := stream.terminal(terminal); err != nil {
			if !stream.Opened() {
				// The generation committed upstream but the client never saw a byte:
				// the open/terminal write failed. Reporting the zero-value canonical
				// error here would (a) hand `service.fail` an unclassified error and
				// (b) let the !opened branch abandon the replay claim for a generation
				// that ACTUALLY COMMITTED, which §7.3 rule 4 forbids ("an uncertain/
				// possibly-committed claim is not stolen for automatic re-execution").
				uncertain := domain.NewExecutionPossiblyCommitted().WithRequestID(execution.sc.requestID)
				terminal.Error = uncertain
				return terminal, false, uncertain, true
			}
			return terminal, true, canonical, true
		}
		return terminal, true, canonical, true
	}
	// The client will receive a canonical HTTP status (or another account will be
	// attempted), so seal this attempt's sink: a drifting Adapter goroutine must
	// never open a stream on top of an already-answered request.
	stream.seal()
	return terminal, false, canonical, settled
}

// classifyStreamOutcome maps the safe Adapter ChatStreamOutcome onto the single
// client terminal event, following the lifecycle §4.5 table: natural completion
// → `completed` with its finish class, cancellation → `canceled`, runtime
// failure and commit-uncertainty → `failed`.
//
// The `opened` result reports whether the client is already committed to a
// stream. Once content or an open event reached the client, the outcome MUST be
// delivered as a terminal event; if nothing reached the client, a not-committed
// failure can still be a canonical HTTP status or a fallback attempt.
func (service *ChatService) classifyStreamOutcome(
	execution chatStreamExecution,
	stream *lazyStream,
	outcome domain.ChatStreamOutcome,
) (ChatStreamTerminal, bool, domain.CanonicalError, bool) {
	deltas := stream.deltaCount()
	uncertain := func() (ChatStreamTerminal, bool, domain.CanonicalError, bool) {
		canonical := domain.NewExecutionPossiblyCommitted().WithRequestID(execution.sc.requestID)
		// Commit uncertainty always terminates the stream: the client must be told
		// the generation may have happened, and no layer may re-run it.
		return ChatStreamTerminal{
			Event:       domain.ChatStreamFailed,
			FinishClass: domain.FinishFailed,
			Error:       canonical,
		}, true, canonical, true
	}

	switch outcome.Class {
	case domain.ChatOutcomeCommitted:
		// A committed-class outcome without authoritative commit proof, or with an
		// unclassifiable finish, must never be presented as a natural completion.
		if outcome.Commit != domain.CommitCommitted || !outcome.FinishClass.Valid() {
			return uncertain()
		}
		terminal := ChatStreamTerminal{
			Event:       domain.TerminalEventForFinishClass(outcome.FinishClass),
			FinishClass: outcome.FinishClass,
			Usage:       outcome.Usage,
		}
		if terminal.Event == domain.ChatStreamCanceled {
			// Both bits are Adapter OBSERVATIONS, never inferences from the terminal
			// class. §6.2 rule 4 forbids claiming an abort that did not happen, so a
			// non-cancelable Adapter that reports nothing leaves abort-attempted
			// false; and cancellation is never proof of a stop (§6.2 rule 3), so
			// stop-confirmed holds only what the Adapter proved.
			terminal.UpstreamAbortAttempted = outcome.UpstreamAbortAttempted
			terminal.UpstreamStopConfirmed = outcome.UpstreamStopConfirmed
		}
		return terminal, true, domain.CanonicalError{}, true

	case domain.ChatOutcomeNotCommitted:
		canonical := service.notCommittedCanonical(outcome.FailureClass).WithRequestID(execution.sc.requestID)
		terminal := ChatStreamTerminal{
			Event:       domain.ChatStreamFailed,
			FinishClass: domain.FinishFailed,
			Error:       canonical,
		}
		if deltas > 0 {
			// Authoritative no-commit proof is NOT sufficient once content reached
			// the client: restarting generation elsewhere would splice two
			// generations into one canonical stream (§5.4). End it `failed`.
			return terminal, true, canonical, true
		}
		// Nothing reached the client, so the caller may still walk one more account
		// or answer with a canonical HTTP status.
		return terminal, stream.Opened(), canonical, false

	case domain.ChatOutcomeUnknown:
		return uncertain()

	default:
		internal := domain.NewInternalError().WithRequestID(execution.sc.requestID)
		return ChatStreamTerminal{
			Event:       domain.ChatStreamFailed,
			FinishClass: domain.FinishFailed,
			Error:       internal,
		}, true, internal, true
	}
}

// recordStreamTerminalState records replay, affinity, and audit state for a
// terminal that will be delivered as an event. A durable-state failure here
// cannot change the client outcome — the stream is already open and exactly one
// terminal is owed — so failures are folded into the audit/telemetry trail
// rather than converted into a second client event.
func (service *ChatService) recordStreamTerminalState(
	ctx context.Context,
	execution chatStreamExecution,
	account domain.ProviderAccount,
	terminal ChatStreamTerminal,
	identity domain.ReplayIdentity,
	settleErr error,
) {
	// `completed` and `canceled` are both COMMITTED generations: the Provider
	// accepted the work and consumed tokens. Both must record a durable terminal so
	// a retry under the same Idempotency-Key replays that outcome instead of
	// launching a second billed generation (§7.5 I-CHAT-NO-DUPLICATE-EXEC).
	//
	// Recording only `completed` used to leave a canceled stream's claim stuck
	// in_progress forever, so every retry received 409 idempotency_in_progress and
	// the work could never be replayed.
	if terminalCarriesUsage(terminal.Event) {
		completion := domain.ChatCompletion{
			ID:                execution.executionID,
			Object:            "chat.completion",
			Created:           service.nowTS(),
			Model:             execution.command.Model,
			ProviderAccountID: account.ID,
			RequestID:         execution.sc.requestID,
			ExecutionID:       execution.executionID,
			Choices: []domain.ChatChoice{{
				Index:       0,
				Message:     domain.ChatMessage{Role: domain.ChatRoleAssistant, Content: terminal.DeliveredContent},
				FinishClass: terminal.FinishClass,
			}},
			Usage: terminal.Usage,
		}
		_ = service.replay.Complete(ctx, identity, ports.ChatReplayResult{Completion: completion})

		// P3 affinity: remember the account that served this conversation. Soft
		// preference only, so a record failure just loses a hint.
		if execution.command.Options.ConversationID != "" && execution.policy.Affinity.Enabled {
			_ = service.affinity.Record(ctx, domain.ChatAffinityScope{
				TenantID: execution.principal.TenantID,
				Key:      execution.command.Options.ConversationID,
			}, account.ID)
		}
	} else if terminal.Error.Code != domain.ErrCodeExecutionPossiblyCommitted {
		// Authoritative non-commit: release the claim so a later deliberate retry
		// can re-claim. An uncertain claim is never released (no steal).
		_ = service.abandon(ctx, identity)
	}

	// A settlement failure is an ACCOUNTING outcome, not a client outcome, so it
	// gets its own audit action rather than being folded into the stream
	// terminal record. §6.5 rule 3 requires this fault to be operator-visible;
	// an operator filtering on `chat_completion.residual_settled` must find it.
	action := chatTerminalAuditAction(execution.sc.operation)
	outcome := string(terminal.Event)
	if settleErr != nil {
		action = ports.AuditChatResidual
		outcome = string(terminal.Event) + "_accounting_fault"
	}
	_ = service.chatAudit(ctx, execution.sc, execution.principal, account.ID, execution.executionID, action, outcome)
}

// streamReplay delivers a matching terminal replay through the canonical stream
// shape without a new Adapter call: open → one delta carrying the recorded
// assistant message → the recorded terminal. Ordering and the
// exactly-one-terminal rule hold exactly as for a live stream.
func (service *ChatService) streamReplay(
	ctx context.Context,
	sc spineContext,
	principal domain.SecurityPrincipal,
	transport ChatStreamTransport,
	completion domain.ChatCompletion,
) error {
	handshake := ChatStreamHandshake{
		RequestID:         sc.requestID,
		ExecutionID:       completion.ExecutionID,
		ProviderAccountID: completion.ProviderAccountID,
		Model:             completion.Model,
		// A replay is reconstructed from a durable record, never re-streamed from
		// the Provider, so it must disclose synthetic rather than inherit the
		// original attempt's class or leave the field ambiguous (§5.3: streaming
		// class is disclosed, never claimed).
		StreamingClass: domain.StreamingSynthetic,
	}
	if err := transport.Open(handshake); err != nil {
		return nil
	}

	finish := domain.FinishStop
	if len(completion.Choices) > 0 {
		if content := completion.Choices[0].Message.Content; content != "" {
			_ = transport.Sink().Delta(domain.ChatDelta{Index: 0, Content: content})
		}
		if completion.Choices[0].FinishClass.Valid() {
			finish = completion.Choices[0].FinishClass
		}
	}
	terminal := ChatStreamTerminal{
		Event:       domain.TerminalEventForFinishClass(finish),
		FinishClass: finish,
		Usage:       completion.Usage,
	}
	_ = service.chatAudit(ctx, sc, principal, completion.ProviderAccountID, completion.ExecutionID, ports.AuditChatReplayed, "replayed")
	_ = transport.Terminal(terminal)
	service.recordTelemetry(ctx, sc.operation, "", 200)
	service.recordRequestLog(ctx, sc.requestID, principal.ClientAPIKeyID, string(sc.operation), 200, "ok", sc.start)
	return nil
}

// acquireStreamLease acquires the hard P2 chat_stream lease when Tenant policy
// enables leases for that unit. lease is nil when the policy does not lease
// streams, in which case the stream still cannot hop accounts after any delta.
func (service *ChatService) acquireStreamLease(
	ctx context.Context,
	principal domain.SecurityPrincipal,
	policy domain.RoutingPolicy,
	account domain.ProviderAccount,
	executionID domain.Identifier,
) (*ports.ChatStreamLease, domain.CanonicalError, bool) {
	if service.streamLeases == nil || !policy.LeasePolicy.Enabled || !leaseUnitEnabled(policy.LeasePolicy.EligibleUnits, domain.LeaseUnitChatStream) {
		return nil, domain.CanonicalError{}, true
	}
	lease := ports.ChatStreamLease{TenantID: principal.TenantID, AccountID: account.ID, Holder: executionID}
	if err := service.streamLeases.Acquire(ctx, lease); err != nil {
		if errors.Is(err, ports.ErrChatStreamLeaseHeld) {
			// Another in-flight stream of this Tenant holds the account. A lease
			// grants no extra capacity, so this is a concurrency outcome, not a
			// silent hop onto a different account.
			return nil, domain.NewConcurrencyLimit(), false
		}
		return nil, service.dependencyCanonical(err), false
	}
	return &lease, domain.CanonicalError{}, true
}

// leaseUnitEnabled reports whether the policy names the unit as lease-eligible.
func leaseUnitEnabled(units []domain.LeaseUnit, unit domain.LeaseUnit) bool {
	for _, candidate := range units {
		if candidate == unit {
			return true
		}
	}
	return false
}

// streamingClass reads the honest streaming class for the selected account's
// chat_streaming capability fact. An absent/unknown class is reported as
// synthetic rather than real: the Gateway must never over-promise token-level
// latency it cannot prove (chat lifecycle §4.4, I-CHAT-STREAM-CLASS-HONEST).
func (service *ChatService) streamingClass(
	ctx context.Context,
	principal domain.SecurityPrincipal,
	account domain.ProviderAccount,
	now time.Time,
) domain.StreamingClass {
	snapshot, err := service.capabilities.Get(ctx, principal, account.ID)
	if err != nil {
		return domain.StreamingSynthetic
	}
	fact, ok := snapshot.WithDerivedFreshness(now).Operations[domain.CapabilityOpChatStreaming]
	if !ok || fact.StreamingClass != domain.StreamingReal {
		return domain.StreamingSynthetic
	}
	return domain.StreamingReal
}

// streamDependencyCanonical classifies a streaming Adapter transport error.
func (service *ChatService) streamDependencyCanonical(err error) domain.CanonicalError {
	if errors.Is(err, ports.ErrCredentialAbsent) {
		return domain.NewAccountNotUsable(domain.RemediationSubmitCredential)
	}
	if errors.Is(err, ports.ErrChatStreamAdapterUnavailable) {
		return domain.NewDependencyUnavailable()
	}
	return service.dependencyCanonical(err)
}

// HTTPStatusHint is the status code an equivalent non-streaming outcome would
// carry. The stream itself already returned 200 before the terminal was known,
// so this is used only for telemetry/request-log classification — never to
// rewrite the response status.
func (terminal ChatStreamTerminal) HTTPStatusHint() int {
	if terminal.Event == domain.ChatStreamCompleted {
		return 200
	}
	if terminal.Error.Code != "" {
		return terminal.Error.HTTPStatus()
	}
	return 200
}

// logMessage is the request-log message for the terminal outcome.
func (terminal ChatStreamTerminal) logMessage() string {
	if terminal.Error.Code != "" {
		return string(terminal.Error.Code)
	}
	return string(terminal.Event)
}

// observedChatSendBoundary records whether Provider payload transmission began
// for one streaming attempt. It is the streaming counterpart of the render
// fencedPayloadSendBoundary: the authorized boundary marks it immediately before
// Adapter entry, so a later transport error can be classified by proof of
// non-commit (§7.2 rule 2) instead of by phase name.
//
// Concurrency: the Adapter may mark the boundary from its own goroutine while
// the spine reads it after the call returned, so both sides are mutex-guarded.
type observedChatSendBoundary struct {
	mu   sync.Mutex
	sent bool
}

// MarkPayloadSent records that payload transmission is beginning.
func (boundary *observedChatSendBoundary) MarkPayloadSent(context.Context) error {
	boundary.mu.Lock()
	defer boundary.mu.Unlock()
	boundary.sent = true
	return nil
}

// PayloadSent reports whether payload transmission began for this attempt.
func (boundary *observedChatSendBoundary) PayloadSent() bool {
	boundary.mu.Lock()
	defer boundary.mu.Unlock()
	return boundary.sent
}

var _ ports.ChatSendBoundary = (*observedChatSendBoundary)(nil)
