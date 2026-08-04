package vault

import (
	"context"
	"sync"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// FailClosedChatCredentialAuthorizer never mints credential material. The Chat
// Adapter cannot be entered through Authorize. Production default.
type FailClosedChatCredentialAuthorizer struct{}

// NewFailClosedChatCredentialAuthorizer builds the production fail-closed authorizer.
func NewFailClosedChatCredentialAuthorizer() *FailClosedChatCredentialAuthorizer {
	return &FailClosedChatCredentialAuthorizer{}
}

// Authorize fails closed without invoking fn.
func (*FailClosedChatCredentialAuthorizer) Authorize(
	context.Context,
	ports.CredentialValidation,
	func(ports.CredentialInjection) error,
) error {
	return ports.ErrCredentialAbsent
}

// MemoryChatCredentialAuthorizer is a controlled fixture authorizer. It holds
// fake credential bytes keyed by account/auth/version for tests only. Material
// is released only inside Authorize's callback and never returned to application.
type MemoryChatCredentialAuthorizer struct {
	mu        sync.Mutex
	materials map[string]string
}

// NewMemoryChatCredentialAuthorizer builds an empty fixture authorizer.
func NewMemoryChatCredentialAuthorizer() *MemoryChatCredentialAuthorizer {
	return &MemoryChatCredentialAuthorizer{materials: make(map[string]string)}
}

// Store binds fixture credential material for Authorize (test/fixture only).
func (a *MemoryChatCredentialAuthorizer) Store(
	account domain.ProviderAccountID,
	mode domain.AuthMode,
	version int,
	material string,
) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.materials == nil {
		a.materials = make(map[string]string)
	}
	a.materials[credKey(account, mode, version)] = material
}

// Authorize mints a callback-scoped injection when the binding is present.
func (a *MemoryChatCredentialAuthorizer) Authorize(
	_ context.Context,
	validation ports.CredentialValidation,
	fn func(ports.CredentialInjection) error,
) error {
	if fn == nil {
		return ports.ErrChatAdapterUnavailable
	}
	a.mu.Lock()
	material, ok := a.materials[credKey(validation.AccountID, validation.AuthMode, validation.Version)]
	a.mu.Unlock()
	if !ok || material == "" {
		return ports.ErrCredentialAbsent
	}
	return fn(credentialInjection{material: material})
}

// PermissiveFixtureChatCredentialAuthorizer mints opaque fixture credential
// material for any non-empty AccountID. Controlled fixtures / AllowInMemory
// only — never production.
type PermissiveFixtureChatCredentialAuthorizer struct{}

// NewPermissiveFixtureChatCredentialAuthorizer builds the fixture authorizer.
func NewPermissiveFixtureChatCredentialAuthorizer() *PermissiveFixtureChatCredentialAuthorizer {
	return &PermissiveFixtureChatCredentialAuthorizer{}
}

// Authorize mints callback-scoped fixture material when AccountID is present.
func (*PermissiveFixtureChatCredentialAuthorizer) Authorize(
	_ context.Context,
	validation ports.CredentialValidation,
	fn func(ports.CredentialInjection) error,
) error {
	if fn == nil || validation.AccountID == "" {
		return ports.ErrCredentialAbsent
	}
	return fn(credentialInjection{material: "fixture-chat-credential-material"})
}

var (
	_ ports.ChatCredentialAuthorizer = (*FailClosedChatCredentialAuthorizer)(nil)
	_ ports.ChatCredentialAuthorizer = (*MemoryChatCredentialAuthorizer)(nil)
	_ ports.ChatCredentialAuthorizer = (*PermissiveFixtureChatCredentialAuthorizer)(nil)
)
