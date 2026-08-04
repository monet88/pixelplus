package contracttest_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/composition"
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

// fixtureChatDigestKey is the deterministic ≥32-byte fixture key mirroring the
// production HMACChatDigester fixture key and its minimum-strength bound.
// contracttest must not import infrastructure (ADR 0009), so the same value is
// re-declared here rather than referenced from the vault package.
const fixtureChatDigestKey = "pixelplus-fixture-chat-digest-key-v1"

// chatFingerprintMessage is the structured, delimiter-safe message encoding used
// by chat fingerprints so two distinct valid message arrays never collide
// (mirror of the production HMACChatDigester payload).
type chatFingerprintMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Name    string `json:"name,omitempty"`
}

// chatFingerprintOptions encodes every accepted request field beyond model and
// messages — generation tuning and x_pixelplus routing inputs — so a same-key
// request differing in any contracted field conflicts instead of replaying
// (mirror of the production HMACChatDigester options payload).
type chatFingerprintOptions struct {
	Temperature       *float64 `json:"temperature,omitempty"`
	MaxTokens         *int     `json:"max_tokens,omitempty"`
	TopP              *float64 `json:"top_p,omitempty"`
	N                 *int     `json:"n,omitempty"`
	Stop              []string `json:"stop,omitempty"`
	User              string   `json:"user,omitempty"`
	ProviderAccountID string   `json:"provider_account_id,omitempty"`
	AllowFallback     bool     `json:"allow_fallback,omitempty"`
	ConversationID    string   `json:"conversation_id,omitempty"`
}

// stubChatDigester is a controlled, deterministic chat digester that logs
// "digest" to the shared spineLog and returns a stable keyed fingerprint per
// input so identical requests replay deterministically. The fingerprint is
// HMAC-SHA256 under a fixture key — never a raw unkeyed SHA-256 of the messages
// (dictionary/oracle ban, ports.ChatDigester) — over a structured JSON encoding
// of operation + model + ordered messages + accepted request options,
// mirroring the production HMACChatDigester so replay coverage is faithful.
type stubChatDigester struct {
	log *spineLog
	key []byte
}

func newStubChatDigester(log *spineLog) *stubChatDigester {
	return &stubChatDigester{log: log, key: []byte(fixtureChatDigestKey)}
}

func (d *stubChatDigester) CreateFingerprint(operation domain.ChatOperation, model string, messages []domain.ChatMessage, options domain.ChatRequestOptions) (domain.Fingerprint, error) {
	if d.log != nil {
		d.log.add("digest")
	}
	if len(d.key) == 0 {
		d.key = []byte(fixtureChatDigestKey)
	}
	msgs := make([]chatFingerprintMessage, 0, len(messages))
	for _, m := range messages {
		msgs = append(msgs, chatFingerprintMessage{Role: string(m.Role), Content: m.Content, Name: m.Name})
	}
	payload, err := json.Marshal(struct {
		V         int                      `json:"v"`
		Operation string                   `json:"op"`
		Model     string                   `json:"model"`
		Messages  []chatFingerprintMessage `json:"messages"`
		Options   chatFingerprintOptions   `json:"options"`
	}{
		V:         2,
		Operation: string(operation),
		Model:     model,
		Messages:  msgs,
		Options: chatFingerprintOptions{
			Temperature:       options.Temperature,
			MaxTokens:         options.MaxTokens,
			TopP:              options.TopP,
			N:                 options.N,
			Stop:              options.Stop,
			User:              options.User,
			ProviderAccountID: string(options.ProviderAccountID),
			AllowFallback:     options.AllowFallback,
			ConversationID:    options.ConversationID,
		},
	})
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, d.key)
	_, _ = mac.Write([]byte("chat.create_fingerprint"))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write(payload)
	return domain.Fingerprint("fp_" + hex.EncodeToString(mac.Sum(nil))), nil
}

// AC: the fixture chat digester is keyed — its fingerprint must NEVER equal a
// raw unkeyed SHA-256 of the same messages (dictionary/oracle ban,
// ports.ChatDigester).
func TestStubChatDigesterKeyedFingerprint(t *testing.T) {
	d := newStubChatDigester(nil)
	messages := []domain.ChatMessage{{Role: domain.ChatRoleUser, Content: "hi"}}

	fp, err := d.CreateFingerprint(domain.ChatOpCompletion, chatModel, messages, domain.ChatRequestOptions{})
	if err != nil {
		t.Fatalf("CreateFingerprint() error = %v", err)
	}

	raw := sha256.New()
	for _, m := range messages {
		_, _ = raw.Write([]byte(string(m.Role) + string(m.Content)))
	}
	unkeyed := "fp_" + hex.EncodeToString(raw.Sum(nil))
	if string(fp) == unkeyed {
		t.Fatalf("keyed fingerprint %q equals an unkeyed SHA-256 of the messages; the digester is not keyed", fp)
	}
}

// AC: the structured encoding is delimiter-safe — two distinct valid message
// arrays must never collide into the same fingerprint (mirror of the production
// HMACChatDigester encoding; a naive role+content concatenation under a shared
// delimiter would collide on [{user "ab"},{user "c"}] vs [{user "a"},{user "bc"}]).
func TestStubChatDigesterNoMessageCollision(t *testing.T) {
	d := newStubChatDigester(nil)

	a, err := d.CreateFingerprint(domain.ChatOpCompletion, chatModel, []domain.ChatMessage{
		{Role: domain.ChatRoleUser, Content: "ab"},
		{Role: domain.ChatRoleUser, Content: "c"},
	}, domain.ChatRequestOptions{})
	if err != nil {
		t.Fatalf("CreateFingerprint(a) error = %v", err)
	}
	b, err := d.CreateFingerprint(domain.ChatOpCompletion, chatModel, []domain.ChatMessage{
		{Role: domain.ChatRoleUser, Content: "a"},
		{Role: domain.ChatRoleUser, Content: "bc"},
	}, domain.ChatRequestOptions{})
	if err != nil {
		t.Fatalf("CreateFingerprint(b) error = %v", err)
	}
	if a == b {
		t.Fatalf("distinct message arrays collided into one fingerprint %q", a)
	}
}

// AC: the fingerprint binds every accepted request field — identical messages
// with different generation tuning or routing inputs must never collide, so a
// same-key request differing in any contracted field conflicts instead of
// replaying (idempotency policy §5.2, canonical-errors §7.1).
func TestStubChatDigesterOptionsCovered(t *testing.T) {
	d := newStubChatDigester(nil)
	messages := []domain.ChatMessage{{Role: domain.ChatRoleUser, Content: "hi"}}

	base, err := d.CreateFingerprint(domain.ChatOpCompletion, chatModel, messages, domain.ChatRequestOptions{})
	if err != nil {
		t.Fatalf("CreateFingerprint(base) error = %v", err)
	}

	temperature := 0.7
	maxTokens := 64
	variations := map[string]domain.ChatRequestOptions{
		"temperature":         {Temperature: &temperature},
		"max_tokens":          {MaxTokens: &maxTokens},
		"stop":                {Stop: []string{"END"}},
		"user":                {User: "u_1"},
		"provider_account_id": {ProviderAccountID: "pa_chat"},
		"allow_fallback":      {AllowFallback: true},
		"conversation_id":     {ConversationID: "conv-1"},
	}
	for name, options := range variations {
		fp, err := d.CreateFingerprint(domain.ChatOpCompletion, chatModel, messages, options)
		if err != nil {
			t.Fatalf("CreateFingerprint(%s) error = %v", name, err)
		}
		if fp == base {
			t.Fatalf("%s: fingerprint %q equals the no-options fingerprint; the field is not bound", name, fp)
		}
	}

	// A message name difference must also change the fingerprint.
	named, err := d.CreateFingerprint(domain.ChatOpCompletion, chatModel, []domain.ChatMessage{
		{Role: domain.ChatRoleUser, Content: "hi", Name: "dao"},
	}, domain.ChatRequestOptions{})
	if err != nil {
		t.Fatalf("CreateFingerprint(named) error = %v", err)
	}
	if named == base {
		t.Fatalf("message name not bound into the fingerprint %q", named)
	}
}

// stubChatAuditRecorder records the secret-free chat audit projections emitted
// by the chat spine (audit-before-allow protected_access and terminal outcomes)
// and logs each Record so the gate-order proof can observe audit-before-allow
// through real composition without importing infrastructure.
type stubChatAuditRecorder struct {
	log    *spineLog
	mu     sync.Mutex
	events []ports.ChatAuditEvent
}

func newStubChatAuditRecorder(log *spineLog) *stubChatAuditRecorder {
	return &stubChatAuditRecorder{log: log}
}

func (r *stubChatAuditRecorder) Record(_ context.Context, event ports.ChatAuditEvent) error {
	if r.log != nil {
		r.log.add("chat.audit")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	return nil
}

func (r *stubChatAuditRecorder) snapshot() []ports.ChatAuditEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ports.ChatAuditEvent(nil), r.events...)
}

// recordingChatReplay wraps the composition-vended controlled chat replay
// store and logs claim/complete/abandon to the spineLog so tests can prove
// replay ordering through real composition. The no-steal state machine lives
// in the production memory store; ADR 0009 forbids contracttest from importing
// infrastructure directly, so the delegate comes from composition.
type recordingChatReplay struct {
	log   *spineLog
	inner ports.ChatReplayStore
}

func newRecordingChatReplay(log *spineLog) *recordingChatReplay {
	return &recordingChatReplay{log: log, inner: composition.NewControlledChatReplayStore()}
}

func (store *recordingChatReplay) Claim(ctx context.Context, identity domain.ReplayIdentity) (ports.ChatReplayDecision, error) {
	store.log.add("replay.claim")
	return store.inner.Claim(ctx, identity)
}

func (store *recordingChatReplay) Complete(ctx context.Context, identity domain.ReplayIdentity, result ports.ChatReplayResult) error {
	store.log.add("replay.complete")
	return store.inner.Complete(ctx, identity, result)
}

func (store *recordingChatReplay) Abandon(ctx context.Context, identity domain.ReplayIdentity) error {
	store.log.add("replay.abandon")
	return store.inner.Abandon(ctx, identity)
}

// chatRoutingPolicy is a deterministic routing policy over the given selection
// order with optional fallback chain. CandidateAccounts is built from BOTH the
// selection order and the fallback chain (deduplicated, order-preserving) so the
// policy is a valid persisted state — a real routing store rejects a fallback
// account that is missing from CandidateAccounts.
func chatRoutingPolicy(selectionOrder []domain.ProviderAccountID, fallbackChain []domain.ProviderAccountID) domain.RoutingPolicy {
	candidates := make([]domain.ProviderAccountID, 0, len(selectionOrder)+len(fallbackChain))
	seen := make(map[domain.ProviderAccountID]struct{}, len(selectionOrder)+len(fallbackChain))
	for _, id := range append(append([]domain.ProviderAccountID(nil), selectionOrder...), fallbackChain...) {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		candidates = append(candidates, id)
	}
	return domain.RoutingPolicy{
		CandidateAccounts: candidates,
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

// chatRoutingPolicyWithModes extends chatRoutingPolicy with an explicit
// fallback_auth_modes list so tests can prove cross-Auth-Mode fallback gating
// (routing spec §6.2, NF-XMODE).
func chatRoutingPolicyWithModes(selectionOrder []domain.ProviderAccountID, fallbackChain []domain.ProviderAccountID, modes []domain.AuthMode) domain.RoutingPolicy {
	policy := chatRoutingPolicy(selectionOrder, fallbackChain)
	policy.FallbackAuthModes = modes
	return policy
}
