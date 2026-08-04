package ports

import (
	"context"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// ChatStreamCommand is the safe Adapter invocation for one streaming attempt
// after authorization. Like ChatCommand it carries only safe identities and the
// canonical messages; credential material is never a durable field and is
// injected via CredentialInjection.
type ChatStreamCommand struct {
	Principal   domain.SecurityPrincipal
	AccountID   domain.ProviderAccountID
	AuthMode    domain.AuthMode
	Version     int
	Operation   domain.ChatOperation
	Model       string
	Messages    []domain.ChatMessage
	ExecutionID domain.Identifier
}

// ChatStreamAdapter runs one controlled streaming chat attempt after the
// authorized boundary resolved secrets. It writes canonical content through the
// domain.ChatSink, which exposes only Delta and Heartbeat: the Adapter cannot
// emit the `open` event or any terminal event, so canonical ordering and the
// exactly-one-terminal invariant cannot be broken by Adapter drift
// (I-CHAT-STREAM-ORDER). The returned ChatStreamOutcome is the safe
// classification the Gateway maps onto the single terminal event.
//
// A sink error means the client is gone or the stream is already terminated;
// the Adapter MUST stop producing and return promptly.
type ChatStreamAdapter interface {
	Stream(context.Context, ChatStreamCommand, CredentialInjection, domain.ChatSink) (domain.ChatStreamOutcome, error)
}

// AuthorizedChatStreamRequest is the application-facing request for one upstream
// streaming attempt. It carries only safe identities so the authorized port
// resolves the Vault credential internally and never returns plaintext.
type AuthorizedChatStreamRequest struct {
	Principal domain.SecurityPrincipal
	AccountID domain.ProviderAccountID
	AuthMode  domain.AuthMode
	Version   int
	Operation domain.ChatOperation
	Model     string
	Messages  []domain.ChatMessage
	// RequestID is the boundary request correlation id for the audit projection;
	// it is distinct from ExecutionID (one request may walk more than one account
	// attempt under the same execution).
	RequestID    domain.Identifier
	ExecutionID  domain.Identifier
	SendBoundary ChatSendBoundary
}

// AuthorizedChatStream is the protected execution boundary for one streaming
// chat attempt. Implementations record audit-before-allow intent, resolve
// credential material, mark the send boundary immediately before Adapter entry,
// and run ChatStreamAdapter.Stream with a Use-scoped CredentialInjection.
// Application receives only the safe ChatStreamOutcome (ADR 0009 protected
// boundary mirror of AuthorizedChat).
type AuthorizedChatStream interface {
	Stream(context.Context, AuthorizedChatStreamRequest, domain.ChatSink) (domain.ChatStreamOutcome, error)
}

// ChatStreamLease is the hard P2 account hold for one streaming session
// (routing spec §5.2, chat lifecycle §5.3). Holder is the server-owned execution
// identity of the stream, so a lease is per unit of work and never a Tenant-wide
// reservation.
type ChatStreamLease struct {
	TenantID  domain.TenantID
	AccountID domain.ProviderAccountID
	Holder    domain.Identifier
}

// ChatStreamLeaseStore records the hard streaming-session account binding. It
// binds the whole stream to exactly one same-Tenant account so the Gateway does
// not hop accounts mid-stream.
//
// Acquire MUST be atomic: two concurrent streams that race for the same account
// cannot both hold the lease, and the loser receives ErrChatStreamLeaseHeld
// rather than silently sharing the binding. A lease grants no extra concurrency
// budget (routing spec §5.2 rule 5) — the stream still holds exactly one chat
// concurrency slot from admission.
type ChatStreamLeaseStore interface {
	// Acquire binds the account to the holder for the stream's duration.
	Acquire(context.Context, ChatStreamLease) error
	// Holder reports the execution currently holding the account's stream lease.
	Holder(context.Context, domain.TenantID, domain.ProviderAccountID) (domain.Identifier, bool, error)
	// Release clears the binding at the stream's terminal. It is idempotent so a
	// double release (client terminal plus cleanup) is a safe no-op.
	Release(context.Context, ChatStreamLease) error
}

// Streaming audit actions emitted by the chat stream spine.
const (
	// AuditChatStreamOpened records that a stream was opened for execution.
	AuditChatStreamOpened ChatAuditAction = "chat_completion.stream_opened"
	// AuditChatStreamTerminal records the single client terminal outcome.
	AuditChatStreamTerminal ChatAuditAction = "chat_completion.stream_terminal"
)
