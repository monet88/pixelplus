package vault

import (
	"context"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// AuthorizedChatStreamService is the protected streaming chat boundary (ADR
// 0009), the streaming twin of AuthorizedChatService. Credential material is
// minted only via ChatCredentialAuthorizer (never Validate-only),
// audit-before-allow records protected-access intent before any credential
// release, and plaintext never crosses into application. The sink it forwards
// exposes only Delta/Heartbeat, so the Adapter cannot emit stream open or
// terminal events.
type AuthorizedChatStreamService struct {
	authorizer ports.ChatCredentialAuthorizer
	adapter    ports.ChatStreamAdapter
	audit      ports.ChatAuditRecorder
}

// NewAuthorizedChatStreamService wires the authorized streaming boundary.
func NewAuthorizedChatStreamService(
	authorizer ports.ChatCredentialAuthorizer,
	adapter ports.ChatStreamAdapter,
	audit ports.ChatAuditRecorder,
) *AuthorizedChatStreamService {
	if authorizer == nil {
		authorizer = NewFailClosedChatCredentialAuthorizer()
	}
	return &AuthorizedChatStreamService{
		authorizer: authorizer,
		adapter:    adapter,
		audit:      audit,
	}
}

// Stream audits protected access, authorizes the credential, marks the send
// boundary immediately before Adapter entry, and returns only the safe
// ChatStreamOutcome.
func (service *AuthorizedChatStreamService) Stream(
	ctx context.Context,
	request ports.AuthorizedChatStreamRequest,
	sink domain.ChatSink,
) (domain.ChatStreamOutcome, error) {
	if service.adapter == nil || service.authorizer == nil {
		return domain.ChatStreamOutcome{}, ports.ErrChatStreamAdapterUnavailable
	}
	if sink == nil {
		return domain.ChatStreamOutcome{}, ports.ErrChatStreamAdapterUnavailable
	}

	// Audit-before-allow (P1-B): intent must succeed before any secret release.
	if service.audit == nil {
		return domain.ChatStreamOutcome{}, ports.ErrDependencyUnavailable
	}
	if err := service.audit.Record(ctx, ports.ChatAuditEvent{
		Action:            ports.AuditChatProtectedAccess,
		TenantID:          request.Principal.TenantID,
		ClientAPIKeyID:    request.Principal.ClientAPIKeyID,
		ProviderAccountID: request.AccountID,
		RequestID:         request.RequestID,
		ExecutionID:       request.ExecutionID,
		Outcome:           "intent",
	}); err != nil {
		return domain.ChatStreamOutcome{}, err
	}

	validation := ports.CredentialValidation{
		Principal: request.Principal,
		AccountID: request.AccountID,
		AuthMode:  request.AuthMode,
		Version:   request.Version,
	}

	var outcome domain.ChatStreamOutcome
	err := service.authorizer.Authorize(ctx, validation, func(cred ports.CredentialInjection) error {
		if request.SendBoundary != nil {
			if markErr := request.SendBoundary.MarkPayloadSent(ctx); markErr != nil {
				return markErr
			}
		}
		var runErr error
		outcome, runErr = service.adapter.Stream(ctx, ports.ChatStreamCommand{
			Principal:   request.Principal,
			AccountID:   request.AccountID,
			AuthMode:    request.AuthMode,
			Version:     request.Version,
			Operation:   request.Operation,
			Model:       request.Model,
			Messages:    append([]domain.ChatMessage(nil), request.Messages...),
			ExecutionID: request.ExecutionID,
		}, cred, sink)
		return runErr
	})
	if err != nil {
		return domain.ChatStreamOutcome{}, err
	}
	return outcome, nil
}

// FailClosedChatStreamAdapter is the production default streaming Adapter. It
// fails every stream closed so no account reaches a Provider streaming surface
// until a real streaming Adapter lands (T18–T23). Failing closed here — rather
// than falling back to the non-streaming Adapter — is what keeps the Gateway
// from answering a streaming request with a non-streaming body.
type FailClosedChatStreamAdapter struct{}

// NewFailClosedChatStreamAdapter builds the fail-closed streaming adapter.
func NewFailClosedChatStreamAdapter() *FailClosedChatStreamAdapter {
	return &FailClosedChatStreamAdapter{}
}

// Stream fails closed without writing to the sink.
func (*FailClosedChatStreamAdapter) Stream(
	context.Context,
	ports.ChatStreamCommand,
	ports.CredentialInjection,
	domain.ChatSink,
) (domain.ChatStreamOutcome, error) {
	return domain.ChatStreamOutcome{}, ports.ErrChatStreamAdapterUnavailable
}

var (
	_ ports.AuthorizedChatStream = (*AuthorizedChatStreamService)(nil)
	_ ports.ChatStreamAdapter    = (*FailClosedChatStreamAdapter)(nil)
)
