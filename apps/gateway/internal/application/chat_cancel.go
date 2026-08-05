package application

import (
	"context"
	"sync"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// CancelChatExecutionCommand is the typed cancel request for an in-flight chat
// execution (chat lifecycle §6.2).
type CancelChatExecutionCommand struct {
	RequestID            domain.Identifier
	PresentedKeyMaterial string
	ExecutionID          domain.Identifier
}

// ChatCancelResult is the honest acknowledgement returned to the client. It
// never claims an upstream stop was confirmed unless the Adapter proved it
// (OpenAPI ChatCancelResponse, chat lifecycle §6.2 rule 3).
type ChatCancelResult struct {
	ExecutionID            domain.Identifier
	CancelState            ChatCancelState
	UpstreamAbortAttempted bool
	UpstreamStopConfirmed  bool
	RequestID              domain.Identifier
}

// ChatCancelState is the typed acknowledgement state of an explicit cancel
// (chat lifecycle §6.2). Using a named type keeps the two states from being
// conflated with arbitrary strings at call sites, matching how the codebase
// models ChatStreamEventType and FinishClass.
type ChatCancelState string

const (
	// ChatCancelRequested reports that the running execution was signaled; it is
	// never proof the upstream stopped (§6.2 rule 3).
	ChatCancelRequested ChatCancelState = "cancel_requested"
	// ChatCanceled reports the execution was already terminal, so the cancel was
	// an idempotent no-op (§6.2 rule 5).
	ChatCanceled ChatCancelState = "canceled"
)

// chatCancelRetention is how long a terminal execution stays resolvable for an
// idempotent no-op cancel before the registry evicts it. The bound keeps the
// process-local registry from growing without limit while still honoring the
// post-terminal idempotent-cancel window (§6.2 rule 5).
const chatCancelRetention = 60 * time.Second

// chatExecutionRegistry tracks in-flight chat executions so an explicit cancel
// (§6.2) or disconnect (§6.3) can resolve an execution_id to its running
// execution and signal it.
//
// It is process-local state about in-flight work, not durable business state: a
// cancel can only target a live execution in this process. An unknown or
// already-terminal execution_id is indistinguishable from a foreign one, so the
// cancel route returns the same 404 resource_not_found for all three (#6
// I-ERROR-NON-ENUM, chat lifecycle §8).
type chatExecutionRegistry struct {
	mu sync.Mutex
	// entries maps execution_id -> handle. The handle carries the cancel
	// function and terminal state.
	entries map[domain.Identifier]*chatExecutionHandle
	clock   ports.Clock
}

func newChatExecutionRegistry(clock ports.Clock) *chatExecutionRegistry {
	return &chatExecutionRegistry{
		entries: make(map[domain.Identifier]*chatExecutionHandle),
		clock:   clock,
	}
}

// chatExecutionHandle is one in-flight execution's cancel handle.
type chatExecutionHandle struct {
	tenantID       domain.TenantID
	keyID          domain.ClientAPIKeyID
	cancel         context.CancelFunc
	mu             sync.Mutex
	terminal       bool
	terminalAt     time.Time
	stopConfirmed  bool
	abortAttempted bool
}

// register records an in-flight execution and returns a cancel context plus the
// cancel function. The caller passes the context to the Adapter so a cancel
// signal reaches it as context cancellation.
func (registry *chatExecutionRegistry) register(
	tenantID domain.TenantID,
	keyID domain.ClientAPIKeyID,
	executionID domain.Identifier,
	cancel context.CancelFunc,
) {
	registry.reap(registry.now())
	handle := &chatExecutionHandle{
		tenantID: tenantID,
		keyID:    keyID,
		cancel:   cancel,
	}
	registry.mu.Lock()
	registry.entries[executionID] = handle
	registry.mu.Unlock()
}

// markTerminal records the terminal state of an execution so a later cancel is
// an idempotent no-op (§6.2 rule 5).
func (registry *chatExecutionRegistry) markTerminal(
	executionID domain.Identifier,
	abortAttempted bool,
	stopConfirmed bool,
) {
	registry.mu.Lock()
	handle, ok := registry.entries[executionID]
	registry.mu.Unlock()
	if !ok {
		return
	}
	handle.mu.Lock()
	handle.terminal = true
	handle.terminalAt = registry.now()
	handle.abortAttempted = abortAttempted
	handle.stopConfirmed = stopConfirmed
	handle.mu.Unlock()
}

// unregister removes an entry once its retention window has passed. A later
// cancel for this execution_id is a 404 (non-enumerating).
func (registry *chatExecutionRegistry) unregister(executionID domain.Identifier) {
	registry.mu.Lock()
	delete(registry.entries, executionID)
	registry.mu.Unlock()
}

// reap evicts terminal entries whose retention window has expired. It runs on
// register and cancel, so the registry stays bounded by chatCancelRetention
// without a background goroutine: under continuous load the map only ever
// holds executions from the last retention window.
func (registry *chatExecutionRegistry) reap(now time.Time) {
	var expired []domain.Identifier
	registry.mu.Lock()
	for id, handle := range registry.entries {
		handle.mu.Lock()
		expiredEntry := handle.terminal && now.Sub(handle.terminalAt) >= chatCancelRetention
		handle.mu.Unlock()
		if expiredEntry {
			expired = append(expired, id)
		}
	}
	registry.mu.Unlock()
	for _, id := range expired {
		registry.unregister(id)
	}
}

func (registry *chatExecutionRegistry) now() time.Time {
	if registry.clock != nil {
		return registry.clock.Now()
	}
	return time.Now()
}

// cancel signals a running execution if it is still in-flight. It reports the
// honest acknowledgement: cancel_requested when the execution was running and
// the cancel signal was sent, canceled when it was already terminal. A foreign
// or unknown execution_id returns ok=false so the caller emits 404.
//
// The same-Tenant check is structural: the handle's tenantID must match the
// authenticated principal's, so a Tenant-B cancel can never signal Tenant-A's
// execution (§6.5 rule 5, I-CHAT-OWNERSHIP). A mismatch is reported as
// ok=false (non-enumerating 404), not a forbidden.
func (registry *chatExecutionRegistry) cancel(
	tenantID domain.TenantID,
	executionID domain.Identifier,
) (cancelState ChatCancelState, abortAttempted, stopConfirmed, ok bool) {
	registry.reap(registry.now())
	registry.mu.Lock()
	handle, exists := registry.entries[executionID]
	registry.mu.Unlock()
	if !exists {
		return "", false, false, false
	}
	// Same-Tenant ownership: a foreign execution is indistinguishable from
	// unknown (non-enumeration, #6 §5.1).
	if handle.tenantID != tenantID {
		return "", false, false, false
	}
	handle.mu.Lock()
	if handle.terminal {
		// Idempotent: a second cancel on a terminal execution is a success
		// no-op, not an error (§6.2 rule 5).
		abort := handle.abortAttempted
		stop := handle.stopConfirmed
		handle.mu.Unlock()
		return ChatCanceled, abort, stop, true
	}
	handle.mu.Unlock()

	// Signal the running execution. The cancel function is idempotent
	// (context.CancelFunc), so a second cancel is a safe no-op.
	handle.cancel()
	// The Gateway attempted to abort by signaling; whether upstream actually
	// stopped is NOT confirmed by this acknowledgement (§6.2 rule 3).
	return ChatCancelRequested, true, false, true
}

// CancelChatExecution handles an explicit same-Tenant cancel request. It
// authenticates, checks the chat.completions scope, resolves the execution,
// signals it when possible, and returns an honest acknowledgement.
//
// It does NOT release occupancy or settle quota: the accounting terminal (X6)
// is owned by the streaming spine that launched the execution (§6.5 rule 1).
// The cancel route emits no second client terminal — the stream's own terminal
// event is the client outcome, and the cancel response is a separate
// acknowledgement (§6.2 rule 5, I-CHAT-CANON-TERMINAL).
func (service *ChatService) CancelChatExecution(ctx context.Context, command CancelChatExecutionCommand) (ChatCancelResult, error) {
	sc := spineContext{
		operation: domain.OperationChatCompletionStreaming,
		requestID: service.resolveRequestID(command.RequestID),
		start:     service.clock.Now(),
	}

	// A0 authenticate.
	principal, canonical, ok := service.authenticate(ctx, ports.PresentedClientAPIKey{Material: command.PresentedKeyMaterial})
	if !ok {
		return ChatCancelResult{}, service.fail(ctx, sc, canonical)
	}
	sc.keyID = principal.ClientAPIKeyID

	// A1 scope: cancel requires the same chat.completions scope as the stream.
	if !principal.Scopes.Has(domain.ChatOpCompletionStreaming.RequiredScope()) {
		return ChatCancelResult{}, service.fail(ctx, sc, domain.NewForbidden())
	}

	if command.ExecutionID == "" {
		return ChatCancelResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}

	// Resolve the in-flight execution under same-Tenant ownership. Unknown,
	// foreign, and already-unregistered executions all share the same 404
	// resource_not_found shape (non-enumeration, §8, I-CHAT-OWNERSHIP).
	cancelState, abortAttempted, stopConfirmed, found := service.executions.cancel(principal.TenantID, command.ExecutionID)
	if !found {
		service.recordTelemetry(ctx, sc.operation, domain.ErrCodeResourceNotFound, 404)
		service.recordRequestLog(ctx, sc.requestID, principal.ClientAPIKeyID, "cancel_chat_execution", 404, string(domain.ErrCodeResourceNotFound), sc.start)
		return ChatCancelResult{}, domain.NewResourceNotFound().WithRequestID(sc.requestID)
	}

	_ = service.chatAudit(ctx, sc, principal, "", command.ExecutionID, string(ports.AuditChatCanceled)+":"+string(cancelState))
	service.recordTelemetry(ctx, sc.operation, "", 200)
	service.recordRequestLog(ctx, sc.requestID, principal.ClientAPIKeyID, "cancel_chat_execution", 200, "ok", sc.start)
	return ChatCancelResult{
		ExecutionID:            command.ExecutionID,
		CancelState:            cancelState,
		UpstreamAbortAttempted: abortAttempted,
		UpstreamStopConfirmed:  stopConfirmed,
		RequestID:              sc.requestID,
	}, nil
}
