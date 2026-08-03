package contracttest_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"sync"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// countingChatAdapter is a controlled ChatAdapter fake that records every run
// (account visited) and returns a scripted ChatOutcome per call (the last
// entry repeats). It also appends "adapter.run" to the shared spineLog so tests
// can prove the full gate order through real composition.
type countingChatAdapter struct {
	log      *spineLog
	mu       sync.Mutex
	calls    int
	accounts []domain.ProviderAccountID
	script   []domain.ChatOutcome
}

func newCountingChatAdapter(log *spineLog) *countingChatAdapter {
	return &countingChatAdapter{log: log}
}

// Script sets the ordered outcomes; the last entry repeats for extra calls.
func (adapter *countingChatAdapter) Script(outcomes ...domain.ChatOutcome) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	adapter.script = append([]domain.ChatOutcome(nil), outcomes...)
}

func (adapter *countingChatAdapter) Run(
	_ context.Context,
	command ports.ChatCommand,
	_ ports.CredentialInjection,
) (domain.ChatOutcome, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	adapter.calls++
	adapter.accounts = append(adapter.accounts, command.AccountID)
	if adapter.log != nil {
		adapter.log.add("adapter.run")
	}
	i := adapter.calls - 1
	var outcome domain.ChatOutcome
	switch {
	case i < len(adapter.script):
		outcome = adapter.script[i]
	case len(adapter.script) > 0:
		outcome = adapter.script[len(adapter.script)-1]
	default:
		outcome = chatSuccess(command.AccountID, command.ExecutionID, "", command.Model)
	}
	return outcome, nil
}

func (adapter *countingChatAdapter) CallCount() int {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	return adapter.calls
}

func (adapter *countingChatAdapter) Accounts() []domain.ProviderAccountID {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	return append([]domain.ProviderAccountID(nil), adapter.accounts...)
}

// chatSuccess builds a committed canonical completion for the given account.
func chatSuccess(account domain.ProviderAccountID, executionID domain.Identifier, requestID domain.Identifier, model string) domain.ChatOutcome {
	return domain.ChatOutcome{
		Class:  domain.ChatOutcomeCommitted,
		Commit: domain.CommitCommitted,
		Completion: domain.ChatCompletion{
			ID:                executionID,
			Object:            "chat.completion",
			Created:           domain.NewTimestamp(spineFixtureTime),
			Model:             model,
			ProviderAccountID: account,
			RequestID:         requestID,
			ExecutionID:       executionID,
			Choices: []domain.ChatChoice{{
				Index:       0,
				Message:     domain.ChatMessage{Role: domain.ChatRoleAssistant, Content: "Hello!"},
				FinishClass: domain.FinishStop,
			}},
			Usage: domain.ChatUsage{PromptTokens: 5, CompletionTokens: 3},
		},
	}
}

// notCommittedOutcome is an authoritative no-commit proof outcome.
func notCommittedOutcome(class domain.ErrorCode) domain.ChatOutcome {
	return domain.ChatOutcome{
		Class:        domain.ChatOutcomeNotCommitted,
		Commit:       domain.CommitNotCommitted,
		FailureClass: class,
	}
}

// unknownOutcome is a fail-closed commit-unknown outcome.
func unknownOutcome() domain.ChatOutcome {
	return domain.ChatOutcome{
		Class:  domain.ChatOutcomeUnknown,
		Commit: domain.CommitUnknown,
	}
}

// chatCapabilitySnapshot builds a same-Tenant snapshot that offers the `chat`
// operation and the given model for injection into a stubCapabilityStore.
func chatCapabilitySnapshot(accountID domain.ProviderAccountID, mode domain.AuthMode, version int, model string) domain.CapabilitySnapshot {
	snapshot := sampleObservationSnapshot(accountID, mode, version, spineFixtureTime)
	snapshot.Operations[domain.CapabilityOpChat] = domain.CapabilityFact{
		Status:        domain.CapabilityVerified,
		Offerable:     true,
		EvidenceClass: domain.EvidenceLiveProbe,
		ProbeSurface:  "/backend-api/chat",
	}
	snapshot.Models = []domain.ModelCapability{{
		ModelSlug: model,
		Operations: map[domain.CapabilityOperation]domain.CapabilityStatus{
			domain.CapabilityOpChat: domain.CapabilityVerified,
		},
		SurfaceBinding: "/backend-api/chat",
		ObservedAt:     domain.NewTimestamp(spineFixtureTime),
	}}
	return snapshot.WithDerivedFreshness(spineFixtureTime)
}

// stubChatDigester is a controlled, deterministic chat digester that logs
// "digest" to the shared spineLog and returns a stable fingerprint per input so
// identical requests replay deterministically.
type stubChatDigester struct {
	log *spineLog
}

func newStubChatDigester(log *spineLog) *stubChatDigester {
	return &stubChatDigester{log: log}
}

func (d *stubChatDigester) CreateFingerprint(operation domain.ChatOperation, model string, messages []domain.ChatMessage) (domain.Fingerprint, error) {
	if d.log != nil {
		d.log.add("digest")
	}
	h := sha256.New()
	_, _ = io.WriteString(h, string(operation))
	_, _ = h.Write([]byte{0})
	_, _ = io.WriteString(h, model)
	for _, m := range messages {
		_, _ = h.Write([]byte{0})
		_, _ = io.WriteString(h, string(m.Role))
		_, _ = io.WriteString(h, string(m.Content))
	}
	return domain.Fingerprint("fp_" + hex.EncodeToString(h.Sum(nil))), nil
}

// recordingChatReplay is a controlled chat replay store that mirrors the
// no-steal memory semantics and logs claim/complete/abandon to the spineLog so
// tests can prove replay ordering through real composition.
type recordingChatReplay struct {
	log     *spineLog
	mu      sync.Mutex
	records map[domain.ReplayScope]*chatReplayRecord
}

func newRecordingChatReplay(log *spineLog) *recordingChatReplay {
	return &recordingChatReplay{log: log, records: make(map[domain.ReplayScope]*chatReplayRecord)}
}

type chatReplayRecord struct {
	fingerprint domain.Fingerprint
	terminal    bool
	completion  domain.ChatCompletion
}

func (store *recordingChatReplay) Claim(_ context.Context, identity domain.ReplayIdentity) (ports.ChatReplayDecision, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.log.add("replay.claim")
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

func (store *recordingChatReplay) Complete(_ context.Context, identity domain.ReplayIdentity, result ports.ChatReplayResult) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.log.add("replay.complete")
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

func (store *recordingChatReplay) Abandon(_ context.Context, identity domain.ReplayIdentity) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.log.add("replay.abandon")
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

// chatRoutingPolicy is a deterministic routing policy over the given selection
// order with optional fallback chain.
func chatRoutingPolicy(selectionOrder []domain.ProviderAccountID, fallbackChain []domain.ProviderAccountID) domain.RoutingPolicy {
	return domain.RoutingPolicy{
		CandidateAccounts: selectionOrder,
		SelectionOrder:    selectionOrder,
		FallbackEnabled:   len(fallbackChain) > 0,
		FallbackChain:     fallbackChain,
		FallbackAuthModes: []domain.AuthMode{},
		Affinity:          domain.AffinityPolicy{Enabled: false},
		LeasePolicy:       domain.LeasePolicy{Enabled: false, EligibleUnits: []domain.LeaseUnit{}},
		UpdatedAt:         domain.SystemDefaultUpdatedAt,
		UpdatedBy:         domain.SystemDefaultUpdatedBy,
	}
}
