package vault

import (
	"context"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// AuthorizedChatService is the protected non-streaming chat boundary (ADR 0009).
// Credential material is minted only via ChatCredentialAuthorizer (never
// Validate-only). Audit-before-allow records protected-access intent before any
// credential release. Plaintext never crosses into application. The application
// spine already runs the CredentialVault presence gate before this boundary, so
// this service only authorizes the stored credential for Adapter entry.
type AuthorizedChatService struct {
	authorizer ports.ChatCredentialAuthorizer
	adapter    ports.ChatAdapter
	audit      ports.ChatAuditRecorder
}

// NewAuthorizedChatService wires the authorized chat boundary.
func NewAuthorizedChatService(
	authorizer ports.ChatCredentialAuthorizer,
	adapter ports.ChatAdapter,
	audit ports.ChatAuditRecorder,
) *AuthorizedChatService {
	if authorizer == nil {
		authorizer = NewFailClosedChatCredentialAuthorizer()
	}
	return &AuthorizedChatService{
		authorizer: authorizer,
		adapter:    adapter,
		audit:      audit,
	}
}

// Chat resolves credential presence, audits protected access, authorizes the
// credential, marks the send boundary immediately before Adapter entry, and
// returns only the safe ChatOutcome.
func (service *AuthorizedChatService) Chat(ctx context.Context, request ports.AuthorizedChatRequest) (domain.ChatOutcome, error) {
	if service.adapter == nil || service.authorizer == nil {
		return domain.ChatOutcome{}, ports.ErrChatAdapterUnavailable
	}

	// Audit-before-allow (P1-B): intent must succeed before any secret release.
	// Missing audit fails closed — never skip protected-access record.
	if service.audit == nil {
		return domain.ChatOutcome{}, ports.ErrDependencyUnavailable
	}
	if err := service.audit.Record(ctx, ports.ChatAuditEvent{
		Action:            ports.AuditChatProtectedAccess,
		TenantID:          request.Principal.TenantID,
		ClientAPIKeyID:    request.Principal.ClientAPIKeyID,
		ProviderAccountID: request.AccountID,
		RequestID:         request.ExecutionID,
		ExecutionID:       request.ExecutionID,
		Outcome:           "intent",
	}); err != nil {
		return domain.ChatOutcome{}, err
	}

	validation := ports.CredentialValidation{
		Principal: request.Principal,
		AccountID: request.AccountID,
		AuthMode:  request.AuthMode,
		Version:   request.Version,
	}

	var outcome domain.ChatOutcome
	err := service.authorizer.Authorize(ctx, validation, func(cred ports.CredentialInjection) error {
		if request.SendBoundary != nil {
			if markErr := request.SendBoundary.MarkPayloadSent(ctx); markErr != nil {
				return markErr
			}
		}
		var runErr error
		outcome, runErr = service.adapter.Run(ctx, ports.ChatCommand{
			Principal:   request.Principal,
			AccountID:   request.AccountID,
			AuthMode:    request.AuthMode,
			Version:     request.Version,
			Operation:   request.Operation,
			Model:       request.Model,
			Messages:    append([]domain.ChatMessage(nil), request.Messages...),
			ExecutionID: request.ExecutionID,
		}, cred)
		return runErr
	})
	if err != nil {
		return domain.ChatOutcome{}, err
	}
	return outcome, nil
}

// FailClosedChatAdapter is the production default low-level chat Adapter. It
// fails every execution closed so no account can reach a Provider surface until
// a real chat Adapter lands.
type FailClosedChatAdapter struct{}

// NewFailClosedChatAdapter builds the fail-closed chat adapter.
func NewFailClosedChatAdapter() *FailClosedChatAdapter {
	return &FailClosedChatAdapter{}
}

// Run fails closed.
func (*FailClosedChatAdapter) Run(context.Context, ports.ChatCommand, ports.CredentialInjection) (domain.ChatOutcome, error) {
	return domain.ChatOutcome{}, ports.ErrChatAdapterUnavailable
}
