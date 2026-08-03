package persistence

import (
	"context"
	"sync"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// chatReplayRecord is one scoped chat idempotency claim.
type chatReplayRecord struct {
	fingerprint domain.Fingerprint
	terminal    bool
	completion  domain.ChatCompletion
}

// MemoryChatReplayStore is a process-local controlled chat replay store
// (fixtures / AllowInMemory). It enforces the same no-steal rule and
// one-accepted-owner semantics as the render/account replay stores.
type MemoryChatReplayStore struct {
	mu      sync.Mutex
	records map[domain.ReplayScope]*chatReplayRecord
}

// NewMemoryChatReplayStore builds an empty process-local chat replay store.
func NewMemoryChatReplayStore() *MemoryChatReplayStore {
	return &MemoryChatReplayStore{records: make(map[domain.ReplayScope]*chatReplayRecord)}
}

// Restore is a no-op for process-local memory (already in-memory). Durable
// file-backed chat replay is deferred; this seam satisfies the public proof.
func (*MemoryChatReplayStore) Restore(context.Context) error { return nil }

// Claim atomically binds the scope+key to the fingerprint or resolves a repeat.
func (store *MemoryChatReplayStore) Claim(_ context.Context, identity domain.ReplayIdentity) (ports.ChatReplayDecision, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	existing, ok := store.records[identity.Scope]
	if !ok {
		store.records[identity.Scope] = &chatReplayRecord{fingerprint: identity.Fingerprint}
		return ports.ChatReplayDecision{Outcome: ports.ReplayClaimed}, nil
	}
	if existing.fingerprint != identity.Fingerprint {
		return ports.ChatReplayDecision{Outcome: ports.ReplayConflict}, nil
	}
	if existing.terminal {
		return ports.ChatReplayDecision{Outcome: ports.ReplayTerminal, TerminalResult: existing.completion}, nil
	}
	return ports.ChatReplayDecision{Outcome: ports.ReplayInProgress}, nil
}

// Complete records the terminal completion so later matching replays are stable.
func (store *MemoryChatReplayStore) Complete(_ context.Context, identity domain.ReplayIdentity, result ports.ChatReplayResult) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	record, ok := store.records[identity.Scope]
	if !ok || record.fingerprint != identity.Fingerprint || result.Completion.ExecutionID == "" {
		return ports.ErrDependencyUnavailable
	}
	if record.terminal {
		return nil
	}
	record.terminal = true
	record.completion = result.Completion
	return nil
}

// Abandon clears an in-progress claim still owned by this request (no-steal).
func (store *MemoryChatReplayStore) Abandon(_ context.Context, identity domain.ReplayIdentity) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	record, ok := store.records[identity.Scope]
	if !ok {
		return nil
	}
	if record.terminal || record.fingerprint != identity.Fingerprint {
		return nil
	}
	delete(store.records, identity.Scope)
	return nil
}

var (
	_ ports.ChatReplayStore = (*MemoryChatReplayStore)(nil)
	_ ports.Restorer        = (*MemoryChatReplayStore)(nil)
)
