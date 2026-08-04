package ports

import (
	"context"
	"errors"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// Typed Chat port errors.
var (
	// ErrChatAdapterUnavailable fails closed when no controlled Provider chat
	// surface is configured or the Adapter is structurally incomplete.
	ErrChatAdapterUnavailable = errors.New("chat adapter unavailable")
	// ErrChatDigesterUnavailable is returned when the digester cannot mint a
	// durable fingerprint (missing/weak key, fail-closed composition). Create
	// must fail closed with dependency_unavailable before replay/admission
	// side effects (chat lifecycle mirror of render #54).
	ErrChatDigesterUnavailable = errors.New("chat digester unavailable")
	// ErrChatStreamAdapterUnavailable fails closed when no controlled Provider
	// streaming surface is configured. A streaming request MUST NOT silently
	// degrade to the non-streaming Adapter (chat lifecycle §3.2 rule 2).
	ErrChatStreamAdapterUnavailable = errors.New("chat stream adapter unavailable")
	// ErrChatStreamLeaseHeld is returned when the hard chat_stream lease for the
	// account is already held by another in-flight stream of the same Tenant.
	ErrChatStreamLeaseHeld = errors.New("chat stream lease already held")
)

// ChatReplayDecision is the result of an atomic idempotency claim. On
// ReplayTerminal, TerminalResult carries the prior canonical completion so the
// original result is replayed without a new Adapter call.
type ChatReplayDecision struct {
	Outcome        ReplayOutcome
	TerminalResult domain.ChatCompletion
}

// ChatReplayResult is the terminal projection recorded once an owning request
// completes its canonical completion, so later matching replays are stable.
type ChatReplayResult struct {
	Completion domain.ChatCompletion
}

// ChatReplayStore performs the atomic idempotency claim for chat surfaces. It
// enforces the same no-steal rule and one-accepted-owner semantics as the other
// ReplayStore boundaries (#20): a terminal replay returns the original result
// without a new Adapter call; conflict/uncertainty never steal execution.
type ChatReplayStore interface {
	Claim(context.Context, domain.ReplayIdentity) (ChatReplayDecision, error)
	Complete(context.Context, domain.ReplayIdentity, ChatReplayResult) error
	Abandon(context.Context, domain.ReplayIdentity) error
}

// ChatDigester produces opaque, keyed digests for chat create-time fingerprint
// binding. The fingerprint covers the operation identity, model, ordered
// canonical messages (name included), and every remaining accepted request
// field in options — generation tuning and x_pixelplus routing inputs alike —
// so a same-key request differing in any contracted field conflicts instead of
// replaying (idempotency policy §5.2, canonical-errors §7.1). The key never
// leaves the digester implementation. Unkeyed SHA-256 of the messages MUST NOT
// equal these digests (dictionary/oracle ban, mirror of RenderDigester).
// Method errors keep product paths fail-closed.
type ChatDigester interface {
	CreateFingerprint(operation domain.ChatOperation, model string, messages []domain.ChatMessage, options domain.ChatRequestOptions) (domain.Fingerprint, error)
}

// ChatAffinityStore keeps the soft conversation→account preference (routing
// spec §5.1, chat lifecycle §5.2). It is a preference record only, never a
// routing authority: selection still requires candidate-set membership
// (C0–C5), so a stale or foreign preference can never widen execution, and a
// lost preference simply falls through to P4 policy selection.
type ChatAffinityStore interface {
	// Preferred returns the account that last successfully served the scoped
	// conversation; ok=false when no preference is recorded.
	Preferred(context.Context, domain.ChatAffinityScope) (domain.ProviderAccountID, bool, error)
	// Record stores the account that just served the scoped conversation.
	Record(context.Context, domain.ChatAffinityScope, domain.ProviderAccountID) error
}

// ChatCommand is the safe Adapter invocation after authorization. It carries
// only safe identities and the canonical messages; credential material is never
// a durable field and is injected via CredentialInjection.
type ChatCommand struct {
	Principal   domain.SecurityPrincipal
	AccountID   domain.ProviderAccountID
	AuthMode    domain.AuthMode
	Version     int
	Operation   domain.ChatOperation
	Model       string
	Messages    []domain.ChatMessage
	ExecutionID domain.Identifier
}

// ChatCredentialAuthorizer is the vault-owned capability for chat execution.
// Authorize validates the credential identity and, only on success, invokes fn
// with a callback-scoped CredentialInjection. It never returns plaintext to
// callers outside fn. Absent credential / auth mismatch / fail-closed state
// must return without calling fn so the Adapter is never entered.
type ChatCredentialAuthorizer interface {
	Authorize(context.Context, CredentialValidation, func(CredentialInjection) error) error
}

// ChatAdapter runs one controlled non-streaming chat completion attempt after
// the authorized boundary has resolved secrets. Credential material is injected
// via the protected Use-scoped value. ChatCommand never carries credential or
// Provider framing. The Adapter is structurally incomplete without
// CredentialInjection (cannot succeed after Validate alone).
type ChatAdapter interface {
	Run(context.Context, ChatCommand, CredentialInjection) (domain.ChatOutcome, error)
}

// ChatSendBoundary is the same protected send surface as the render
// PayloadSendBoundary: it records the durable fact that Provider payload
// transmission is beginning for a synchronous chat call. It is invoked only at
// the protected send surface (immediately before ChatAdapter.Run), never before
// preflight/authorization. It is a type alias to the render boundary so a single
// send-boundary contract and its guarantees stay consistent across chat and
// render (render PayloadSendBoundary #14 §6.2, decision 0012).
type ChatSendBoundary = PayloadSendBoundary

// AuthorizedChatRequest is the application-facing request for one upstream
// non-streaming chat completion. It carries only safe identities so the
// authorized port resolves Vault credential internally and never returns
// plaintext to application.
type AuthorizedChatRequest struct {
	Principal domain.SecurityPrincipal
	AccountID domain.ProviderAccountID
	AuthMode  domain.AuthMode
	Version   int
	Operation domain.ChatOperation
	Model     string
	Messages  []domain.ChatMessage
	// RequestID is the boundary request correlation id for the audit
	// projection; it is distinct from ExecutionID (one request may walk more
	// than one account attempt under the same execution).
	RequestID    domain.Identifier
	ExecutionID  domain.Identifier
	SendBoundary ChatSendBoundary
}

// AuthorizedChat is the protected execution boundary for one non-streaming chat
// completion. Implementations resolve credential presence (Vault), mark the
// send boundary immediately before Adapter entry, and run ChatAdapter.Run with
// a Use-scoped CredentialInjection. Application receives only the safe
// ChatOutcome (ADR 0009 protected-boundary mirror).
type AuthorizedChat interface {
	Chat(context.Context, AuthorizedChatRequest) (domain.ChatOutcome, error)
}

// ChatAuditAction names a chat product/security audit event.
type ChatAuditAction string

// Audit actions emitted by the chat spine.
const (
	AuditChatCompleted       ChatAuditAction = "chat_completion.completed"
	AuditChatReplayed        ChatAuditAction = "chat_completion.replayed"
	AuditChatProtectedAccess ChatAuditAction = "chat_completion.protected_access"
)

// ChatAuditEvent is a secret-free chat audit projection. It never carries
// prompt, credential material, raw Provider payloads, or foreign ids.
type ChatAuditEvent struct {
	Action            ChatAuditAction
	TenantID          domain.TenantID
	ClientAPIKeyID    domain.ClientAPIKeyID
	ProviderAccountID domain.ProviderAccountID
	RequestID         domain.Identifier
	ExecutionID       domain.Identifier
	Outcome           string
}

// ChatAuditRecorder writes the secret-free chat audit projection. A failing
// recorder is a typed dependency outcome for the application to classify
// (P1-B audit-before-allow must not be skipped).
type ChatAuditRecorder interface {
	Record(context.Context, ChatAuditEvent) error
}
