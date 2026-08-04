package ports

import (
	"context"
	"errors"

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

// ErrChatResidualCapacityFull reports that same-Tenant residual tracking has no
// free slot under `L-TENANT-CHAT-RESIDUAL`. It is NOT a failure of the client
// terminal: chat lifecycle §6.5 rule 2 says that when residual tracking is full
// the Gateway "retain[s] the original request state", so the spine keeps the
// original occupancy and reservation held instead of releasing anything.
var ErrChatResidualCapacityFull = errors.New("chat residual tracking capacity is full")

// ChatResidualHold identifies one same-Tenant residual bookkeeping entry for an
// execution whose upstream may still be running after the client terminal (X5).
//
// Every field is part of the ownership identity on purpose: §6.5 rule 5 requires
// residual tracking to stay same-Tenant and "remain charged to the originating
// `client_api_key_id`", so a hold can never migrate surviving work onto another
// Tenant or another key by disconnecting.
type ChatResidualHold struct {
	TenantID       domain.TenantID
	ClientAPIKeyID domain.ClientAPIKeyID
	ExecutionID    domain.Identifier
}

// ChatResidualStore bounds how many of a Tenant's occupied executions are
// represented in residual tracking (`L-TENANT-CHAT-RESIDUAL`, chat lifecycle
// §6.5 rule 2 and I-CHAT-RESIDUAL-BOUNDED). The numeric limit is #17; this port
// owns only the atomic bounded-acquire semantics.
//
// Acquire MUST be atomic and MUST return ErrChatResidualCapacityFull rather than
// exceeding the limit: residual tracking is bookkeeping for work that already
// holds concurrency, never extra execution capacity, so silently growing it
// would let cancel amplification hide surviving upstream generations.
//
// Release MUST be idempotent and MUST release exactly one logical hold per
// execution (§6.5 rule 4 "release ... residual tracking state exactly once").
type ChatResidualStore interface {
	// Acquire claims residual tracking for the execution, or reports
	// ErrChatResidualCapacityFull when the Tenant limit leaves no room.
	Acquire(context.Context, ChatResidualHold) error
	// Release clears the residual hold at the accounting terminal (X6).
	Release(context.Context, ChatResidualHold) error
}

// ChatResidualOutcome is the safe result of a bounded drain/recovery attempt on
// an execution whose upstream survived the client terminal.
//
// Usage is authoritative ONLY when UsageKnown is true. An unknown usage after a
// bounded drain is not zero: §6.5 rule 3 requires the Gateway to "retain the
// full reservation (or a platform-configured conservative debit no smaller than
// known usage) and emit an operator-visible accounting fault; never assume
// zero".
type ChatResidualOutcome struct {
	// UsageKnown reports whether Usage is the final actual Provider usage.
	UsageKnown bool
	// Usage is the final actual input+output usage including tokens consumed
	// after the client terminal.
	Usage domain.ChatUsage
	// StopConfirmed reports a CONFIRMED upstream stop observed during the drain.
	// It is an observation, never an inference from reaching the deadline.
	StopConfirmed bool
}

// ChatResidualDrainRequest is the safe drain instruction for one execution.
type ChatResidualDrainRequest struct {
	Hold ChatResidualHold
	// AccountID is the account that served the surviving execution, so a drain
	// implementation resolves the same same-Tenant binding the stream used.
	AccountID domain.ProviderAccountID
	// ObservedUsage is what the Adapter already reported at the client terminal,
	// so a drain that cannot learn more never settles BELOW known usage.
	ObservedUsage domain.ChatUsage
	// ObservedUsageKnown reports whether ObservedUsage is authoritative.
	ObservedUsageKnown bool
}

// ChatResidualDrain performs the bounded drain/recovery of a surviving upstream
// execution between X5 and X6 (chat lifecycle §6.5 rules 3-4).
//
// Drain MUST be bounded: reaching the deadline is a legitimate outcome reported
// as an unknown usage, which fails accounting closed. Reaching the deadline
// never authorizes an optimistic refund, and Drain MUST NOT start a replacement
// generation.
type ChatResidualDrain interface {
	Drain(context.Context, ChatResidualDrainRequest) (ChatResidualOutcome, error)
}

// Streaming audit actions emitted by the chat stream spine.
const (
	// AuditChatStreamOpened records that a stream was opened for execution.
	AuditChatStreamOpened ChatAuditAction = "chat_completion.stream_opened"
	// AuditChatStreamTerminal records the single client terminal outcome.
	AuditChatStreamTerminal ChatAuditAction = "chat_completion.stream_terminal"
	// AuditChatCanceled records an explicit same-Tenant cancel request and its
	// honest acknowledgement state (chat lifecycle §6.2).
	AuditChatCanceled ChatAuditAction = "chat_completion.canceled"
	// AuditChatResidual records the accounting terminal of a surviving upstream
	// execution: the bounded drain settled, or failed closed with an
	// operator-visible accounting fault (§6.5 rule 3).
	AuditChatResidual ChatAuditAction = "chat_completion.residual_settled"
)
