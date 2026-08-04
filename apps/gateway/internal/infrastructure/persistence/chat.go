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

// UnavailableChatReplayStore is the fail-closed chat replay store used in
// production until a durable chat replay ledger is configured (decision 0012).
// A process-local MemoryChatReplayStore silently loses idempotency claims on
// restart, so a client retry of the same Idempotency-Key could re-execute the
// Adapter and double-settle. Production therefore fails closed on ChatReplayStore
// rather than pretending a restart-losing in-memory store is authoritative.
// Every call returns ErrDependencyUnavailable so chat requests surface
// dependency_unavailable until a durable store is injected.
type UnavailableChatReplayStore struct{}

// NewUnavailableChatReplayStore builds the fail-closed chat replay store.
func NewUnavailableChatReplayStore() *UnavailableChatReplayStore {
	return &UnavailableChatReplayStore{}
}

func (*UnavailableChatReplayStore) Claim(context.Context, domain.ReplayIdentity) (ports.ChatReplayDecision, error) {
	return ports.ChatReplayDecision{}, ports.ErrDependencyUnavailable
}

func (*UnavailableChatReplayStore) Complete(context.Context, domain.ReplayIdentity, ports.ChatReplayResult) error {
	return ports.ErrDependencyUnavailable
}

func (*UnavailableChatReplayStore) Abandon(context.Context, domain.ReplayIdentity) error {
	return ports.ErrDependencyUnavailable
}

var _ ports.ChatReplayStore = (*UnavailableChatReplayStore)(nil)

// MemoryChatAffinityStore is the process-local conversation affinity store.
// Unlike the replay ledger, an affinity record is a soft P3 preference, never
// an authority (routing spec §5.1 rule 1): losing it on restart only degrades
// selection to P4 policy order, so production may run in-memory until a
// durable store lands (decision 0012). The affinity window-class numeric is
// #17-owned; the process-local map is bounded by process lifetime.
type MemoryChatAffinityStore struct {
	mu        sync.Mutex
	preferred map[domain.ChatAffinityScope]domain.ProviderAccountID
}

// NewMemoryChatAffinityStore builds an empty process-local affinity store.
func NewMemoryChatAffinityStore() *MemoryChatAffinityStore {
	return &MemoryChatAffinityStore{preferred: make(map[domain.ChatAffinityScope]domain.ProviderAccountID)}
}

// Preferred returns the recorded preference for the scope, if any.
func (store *MemoryChatAffinityStore) Preferred(_ context.Context, scope domain.ChatAffinityScope) (domain.ProviderAccountID, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	account, ok := store.preferred[scope]
	return account, ok, nil
}

// Record stores the account that just served the scoped conversation.
func (store *MemoryChatAffinityStore) Record(_ context.Context, scope domain.ChatAffinityScope, account domain.ProviderAccountID) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.preferred[scope] = account
	return nil
}

var _ ports.ChatAffinityStore = (*MemoryChatAffinityStore)(nil)

// chatStreamLeaseKey is the same-Tenant account identity of one hard lease.
type chatStreamLeaseKey struct {
	tenantID  domain.TenantID
	accountID domain.ProviderAccountID
}

// MemoryChatStreamLeaseStore is the process-local hard streaming lease store.
//
// A lease is intentionally process-scoped state about an in-flight stream, not
// durable business state: a stream cannot survive a restart, so losing the
// binding when the process dies is correct rather than lossy — the stream it
// guarded is gone too. What the store must guarantee is atomicity while the
// process lives, so two concurrent streams cannot both bind one account
// (routing spec §5.2 rule 1).
type MemoryChatStreamLeaseStore struct {
	mu     sync.Mutex
	leases map[chatStreamLeaseKey]domain.Identifier
}

// NewMemoryChatStreamLeaseStore builds an empty process-local lease store.
func NewMemoryChatStreamLeaseStore() *MemoryChatStreamLeaseStore {
	return &MemoryChatStreamLeaseStore{leases: make(map[chatStreamLeaseKey]domain.Identifier)}
}

// Acquire atomically binds the account to the holder. A different in-flight
// holder wins and this caller receives ErrChatStreamLeaseHeld; the same holder
// re-acquiring is an idempotent success.
func (store *MemoryChatStreamLeaseStore) Acquire(_ context.Context, lease ports.ChatStreamLease) error {
	if lease.Holder == "" || lease.AccountID == "" {
		return ports.ErrDependencyUnavailable
	}
	store.mu.Lock()
	defer store.mu.Unlock()

	key := chatStreamLeaseKey{tenantID: lease.TenantID, accountID: lease.AccountID}
	if holder, ok := store.leases[key]; ok && holder != lease.Holder {
		return ports.ErrChatStreamLeaseHeld
	}
	store.leases[key] = lease.Holder
	return nil
}

// Holder reports the execution currently holding the account's stream lease.
func (store *MemoryChatStreamLeaseStore) Holder(
	_ context.Context,
	tenantID domain.TenantID,
	accountID domain.ProviderAccountID,
) (domain.Identifier, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	holder, ok := store.leases[chatStreamLeaseKey{tenantID: tenantID, accountID: accountID}]
	return holder, ok, nil
}

// Release clears the binding when this holder owns it. Releasing a lease owned
// by another holder is a no-op, so a late cleanup can never revoke a live
// stream's binding.
func (store *MemoryChatStreamLeaseStore) Release(_ context.Context, lease ports.ChatStreamLease) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	key := chatStreamLeaseKey{tenantID: lease.TenantID, accountID: lease.AccountID}
	if holder, ok := store.leases[key]; ok && holder == lease.Holder {
		delete(store.leases, key)
	}
	return nil
}

var _ ports.ChatStreamLeaseStore = (*MemoryChatStreamLeaseStore)(nil)
