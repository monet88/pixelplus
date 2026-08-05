package contracttest_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// streamStep is one scripted Adapter action. Exactly one field is meaningful per
// step, so a script reads as the literal event sequence the Provider produces.
type streamStep struct {
	// delta emits one canonical content fragment when non-empty.
	delta string
	// heartbeat emits one keepalive when true.
	heartbeat bool
	// attemptTerminal ends the Adapter attempt with this outcome when set.
	outcome *domain.ChatStreamOutcome
	// transportError makes the attempt return a transport error.
	transportError error
	// rogueAfter makes the Adapter leak a goroutine that keeps writing content
	// to the sink after the attempt returned. This models real Adapter drift (a
	// Provider client whose reader goroutine outlives the call) and proves the
	// Gateway refuses post-terminal writes structurally rather than trusting the
	// Adapter to stop. The goroutine waits for release before writing, so the
	// test can position the writes strictly after the terminal event.
	rogueAfter *rogueWriter
	// blockOn holds the Adapter mid-stream until the test releases it, so a test
	// can disconnect the client at a known point in the event sequence.
	blockOn *deliveryGate
	// cancelGate holds the Adapter until its CONTEXT is canceled. It proves the
	// cancel/disconnect signal actually reached the Adapter (AC1 / §6.2, §6.3),
	// which is impossible to infer from a mock that ignores its context.
	cancelGate *contextCancelGate
}

// contextCancelGate blocks the Adapter until the request execution context is
// canceled and records that it observed the cancellation. It models a real
// Adapter body reader that returns promptly when the Gateway signals abort.
type contextCancelGate struct {
	entered  chan struct{}
	canceled chan struct{}
	once     sync.Once
}

func newContextCancelGate() *contextCancelGate {
	return &contextCancelGate{
		entered:  make(chan struct{}),
		canceled: make(chan struct{}),
	}
}

// wait blocks until ctx is canceled. It closes entered when it starts blocking
// so a test can wait for the Adapter to be in-flight before issuing the cancel.
func (gate *contextCancelGate) wait(ctx context.Context) {
	gate.once.Do(func() { close(gate.entered) })
	select {
	case <-ctx.Done():
		close(gate.canceled)
	}
}

// Canceled returns a channel closed after the Adapter observed ctx cancellation.
func (gate *contextCancelGate) Canceled() <-chan struct{} {
	return gate.canceled
}

// rogueWriter coordinates an Adapter goroutine that attempts sink writes after
// its attempt returned.
type rogueWriter struct {
	// release is closed by the test once the client stream has fully completed.
	release chan struct{}
	// done is closed once the rogue write attempts finished.
	done chan struct{}

	mu      sync.Mutex
	results []error
}

func newRogueWriter() *rogueWriter {
	return &rogueWriter{release: make(chan struct{}), done: make(chan struct{})}
}

// Release lets the leaked goroutine attempt its post-terminal writes.
func (rogue *rogueWriter) Release() { close(rogue.release) }

// Wait blocks until the rogue write attempts completed.
func (rogue *rogueWriter) Wait(t *testing.T) {
	t.Helper()
	select {
	case <-rogue.done:
	case <-time.After(5 * time.Second):
		t.Fatalf("rogue adapter goroutine did not finish")
	}
}

// Results returns the errors the sink returned for each rogue write.
func (rogue *rogueWriter) Results() []error {
	rogue.mu.Lock()
	defer rogue.mu.Unlock()
	return append([]error(nil), rogue.results...)
}

// scriptedChatStreamAdapter is a controlled ChatStreamAdapter that replays a
// scripted event sequence per attempt and records the accounts it visited. It
// writes exclusively through domain.ChatSink, which is the point: the fake has
// no way to emit open/terminal events even if a test asked it to.
type scriptedChatStreamAdapter struct {
	log *spineLog

	mu       sync.Mutex
	calls    int
	accounts []domain.ProviderAccountID
	commands []ports.ChatStreamCommand
	// scripts[i] drives attempt i; the last entry repeats.
	scripts [][]streamStep
	// sinkErrors records errors the sink returned to the Adapter, which proves
	// the Gateway refuses post-terminal or out-of-order writes.
	sinkErrors []error
}

func newScriptedChatStreamAdapter(log *spineLog) *scriptedChatStreamAdapter {
	return &scriptedChatStreamAdapter{log: log}
}

// Script sets the per-attempt step sequences.
func (adapter *scriptedChatStreamAdapter) Script(scripts ...[]streamStep) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	adapter.scripts = append([][]streamStep(nil), scripts...)
}

func (adapter *scriptedChatStreamAdapter) Stream(
	ctx context.Context,
	command ports.ChatStreamCommand,
	_ ports.CredentialInjection,
	sink domain.ChatSink,
) (domain.ChatStreamOutcome, error) {
	adapter.mu.Lock()
	adapter.calls++
	index := adapter.calls - 1
	adapter.accounts = append(adapter.accounts, command.AccountID)
	adapter.commands = append(adapter.commands, command)
	var script []streamStep
	switch {
	case index < len(adapter.scripts):
		script = adapter.scripts[index]
	case len(adapter.scripts) > 0:
		script = adapter.scripts[len(adapter.scripts)-1]
	}
	if adapter.log != nil {
		adapter.log.add("stream.adapter.run")
	}
	adapter.mu.Unlock()

	for _, step := range script {
		switch {
		case step.blockOn != nil:
			step.blockOn.wait()
		case step.cancelGate != nil:
			step.cancelGate.wait(ctx)
		case step.rogueAfter != nil:
			// Leak a goroutine that keeps writing after this attempt returns.
			rogue := step.rogueAfter
			go func() {
				defer close(rogue.done)
				<-rogue.release
				deltaErr := sink.Delta(domain.ChatDelta{Index: 0, Content: "POST-TERMINAL LEAK"})
				heartbeatErr := sink.Heartbeat()
				rogue.mu.Lock()
				rogue.results = append(rogue.results, deltaErr, heartbeatErr)
				rogue.mu.Unlock()
			}()
		case step.transportError != nil:
			return domain.ChatStreamOutcome{}, step.transportError
		case step.outcome != nil:
			return *step.outcome, nil
		case step.heartbeat:
			adapter.recordSinkResult(sink.Heartbeat())
		case step.delta != "":
			adapter.recordSinkResult(sink.Delta(domain.ChatDelta{Index: 0, Content: step.delta}))
		}
	}
	return streamCommitted(domain.FinishStop, domain.ChatUsage{PromptTokens: 5, CompletionTokens: 3}), nil
}

func (adapter *scriptedChatStreamAdapter) recordSinkResult(err error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	adapter.sinkErrors = append(adapter.sinkErrors, err)
}

func (adapter *scriptedChatStreamAdapter) CallCount() int {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	return adapter.calls
}

func (adapter *scriptedChatStreamAdapter) Accounts() []domain.ProviderAccountID {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	return append([]domain.ProviderAccountID(nil), adapter.accounts...)
}

func (adapter *scriptedChatStreamAdapter) Commands() []ports.ChatStreamCommand {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	return append([]ports.ChatStreamCommand(nil), adapter.commands...)
}

// SinkRejections counts sink writes the Gateway refused.
func (adapter *scriptedChatStreamAdapter) SinkRejections() int {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	rejected := 0
	for _, err := range adapter.sinkErrors {
		if err != nil {
			rejected++
		}
	}
	return rejected
}

// streamCommitted is a committed streaming outcome with an authoritative commit
// proof and the given finish class.
func streamCommitted(finish domain.FinishClass, usage domain.ChatUsage) domain.ChatStreamOutcome {
	return domain.ChatStreamOutcome{
		Class:       domain.ChatOutcomeCommitted,
		Commit:      domain.CommitCommitted,
		FinishClass: finish,
		Usage:       usage,
	}
}

// streamNotCommitted is an authoritative no-commit streaming outcome.
func streamNotCommitted(class domain.ErrorCode) domain.ChatStreamOutcome {
	return domain.ChatStreamOutcome{
		Class:        domain.ChatOutcomeNotCommitted,
		Commit:       domain.CommitNotCommitted,
		FailureClass: class,
	}
}

// streamUnknown is a fail-closed commit-unknown streaming outcome.
func streamUnknown() domain.ChatStreamOutcome {
	return domain.ChatStreamOutcome{
		Class:  domain.ChatOutcomeUnknown,
		Commit: domain.CommitUnknown,
	}
}

// streamCanceledWithAbort is a committed `canceled` outcome from a CANCELABLE
// Adapter: the abort was genuinely attempted, but the Adapter could not confirm
// the upstream stopped.
func streamCanceledWithAbort(usage domain.ChatUsage) domain.ChatStreamOutcome {
	outcome := streamCommitted(domain.FinishCanceled, usage)
	outcome.UpstreamAbortAttempted = true
	return outcome
}

// streamCanceledNonCancelable is a committed `canceled` outcome from a
// NON-CANCELABLE Adapter: no abort was attempted, so the Gateway must not claim
// one (chat lifecycle §6.2 rule 4).
func streamCanceledNonCancelable(usage domain.ChatUsage) domain.ChatStreamOutcome {
	return streamCommitted(domain.FinishCanceled, usage)
}

// streamCanceledConfirmedStop is a committed `canceled` outcome where the
// Adapter PROVED the upstream stopped. That collapses X5 onto X6 (§6.5 rule 1),
// so settlement debits the observed usage immediately instead of entering the
// residual drain path.
func streamCanceledConfirmedStop(usage domain.ChatUsage) domain.ChatStreamOutcome {
	outcome := streamCommitted(domain.FinishCanceled, usage)
	outcome.UpstreamStopConfirmed = true
	return outcome
}

// recordingStreamLeases wraps the hard lease store and records acquire/release
// order so tests can prove the lease is held for the stream's duration, and
// whether a release ever received an already-canceled context (review finding 1:
// release must survive the client, so it must run on settleCtx, not the request
// context).
type recordingStreamLeases struct {
	log   *spineLog
	inner ports.ChatStreamLeaseStore

	mu       sync.Mutex
	acquired []ports.ChatStreamLease
	released []ports.ChatStreamLease
	// sawCanceledRelease is set when Release is called with a canceled context —
	// a real resilient store would reject the write and leak the account binding.
	sawCanceled atomic.Bool
}

func newRecordingStreamLeases(log *spineLog, inner ports.ChatStreamLeaseStore) *recordingStreamLeases {
	return &recordingStreamLeases{log: log, inner: inner}
}

func (store *recordingStreamLeases) Acquire(ctx context.Context, lease ports.ChatStreamLease) error {
	if err := store.inner.Acquire(ctx, lease); err != nil {
		return err
	}
	store.mu.Lock()
	store.acquired = append(store.acquired, lease)
	store.mu.Unlock()
	if store.log != nil {
		store.log.add("lease.acquire")
	}
	return nil
}

func (store *recordingStreamLeases) Holder(ctx context.Context, tenant domain.TenantID, account domain.ProviderAccountID) (domain.Identifier, bool, error) {
	return store.inner.Holder(ctx, tenant, account)
}

func (store *recordingStreamLeases) Release(ctx context.Context, lease ports.ChatStreamLease) error {
	if ctx.Err() != nil {
		store.sawCanceled.Store(true)
	}
	if err := store.inner.Release(ctx, lease); err != nil {
		return err
	}
	store.mu.Lock()
	store.released = append(store.released, lease)
	store.mu.Unlock()
	if store.log != nil {
		store.log.add("lease.release")
	}
	return nil
}

func (store *recordingStreamLeases) sawCanceledRelease() bool {
	return store.sawCanceled.Load()
}

func (store *recordingStreamLeases) Acquisitions() []ports.ChatStreamLease {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]ports.ChatStreamLease(nil), store.acquired...)
}

func (store *recordingStreamLeases) Releases() []ports.ChatStreamLease {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]ports.ChatStreamLease(nil), store.released...)
}

// sseEvent is one parsed canonical stream event.
type sseEvent struct {
	Type        string           `json:"type"`
	ID          string           `json:"id"`
	Model       string           `json:"model"`
	Created     int64            `json:"created"`
	TS          int64            `json:"ts"`
	FinishClass string           `json:"finish_class"`
	Choices     []sseDeltaChoice `json:"choices"`
	Usage       *sseUsage        `json:"usage"`
	Error       map[string]any   `json:"error"`
	Xpixelplus  *sseSafeMetadata `json:"x_pixelplus"`
	// raw retains the exact frame so tests can assert nothing extra leaked.
	raw string
}

type sseDeltaChoice struct {
	Index int `json:"index"`
	Delta struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"delta"`
}

type sseUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type sseSafeMetadata struct {
	RequestID              string `json:"request_id"`
	ExecutionID            string `json:"execution_id"`
	ProviderAccountID      string `json:"provider_account_id"`
	FinishClass            string `json:"finish_class"`
	StreamingClass         string `json:"streaming_class"`
	UpstreamAbortAttempted *bool  `json:"upstream_abort_attempted"`
	UpstreamStopConfirmed  *bool  `json:"upstream_stop_confirmed"`
}

// parseSSE decodes an SSE body into ordered canonical events. It fails the test
// on any frame that is not a `data:` JSON event, which is how a leaked Provider
// sentinel (for example OpenAI's `[DONE]`) would be caught.
func parseSSE(t *testing.T, body []byte) []sseEvent {
	t.Helper()

	var events []sseEvent
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			continue
		}
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			t.Fatalf("non-canonical SSE frame %q in stream body:\n%s", line, body)
		}
		if payload == "[DONE]" {
			t.Fatalf("Provider-specific [DONE] sentinel leaked into the canonical stream:\n%s", body)
		}
		var event sseEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			t.Fatalf("decode SSE event %q: %v", payload, err)
		}
		event.raw = payload
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		t.Fatalf("scan SSE body: %v", err)
	}
	return events
}

// eventTypes projects the ordered event type sequence.
func eventTypes(events []sseEvent) []string {
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}

// terminalEvents returns every terminal event in the stream. A canonical stream
// has exactly one.
func terminalEvents(events []sseEvent) []sseEvent {
	var terminals []sseEvent
	for _, event := range events {
		switch event.Type {
		case "completed", "failed", "canceled":
			terminals = append(terminals, event)
		}
	}
	return terminals
}

// joinDeltas concatenates delta content in arrival order, which must reconstruct
// the assistant message.
func joinDeltas(events []sseEvent) string {
	var builder strings.Builder
	for _, event := range events {
		if event.Type != "delta" {
			continue
		}
		for _, choice := range event.Choices {
			builder.WriteString(choice.Delta.Content)
		}
	}
	return builder.String()
}
