package vault

import (
	"context"
	"sync"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// FailClosedRenderCredentialAuthorizer never mints credential material.
// Adapter cannot be entered through Authorize.
type FailClosedRenderCredentialAuthorizer struct{}

// NewFailClosedRenderCredentialAuthorizer builds the production fail-closed authorizer.
func NewFailClosedRenderCredentialAuthorizer() *FailClosedRenderCredentialAuthorizer {
	return &FailClosedRenderCredentialAuthorizer{}
}

// Authorize fails closed without invoking fn.
func (*FailClosedRenderCredentialAuthorizer) Authorize(
	context.Context,
	ports.CredentialValidation,
	func(ports.CredentialInjection) error,
) error {
	return ports.ErrCredentialAbsent
}

// MemoryRenderCredentialAuthorizer is a controlled fixture authorizer. It holds
// fake credential bytes keyed by account/auth/version for tests only. Material
// is released only inside Authorize's callback and never returned to application.
type MemoryRenderCredentialAuthorizer struct {
	mu        sync.Mutex
	materials map[string]string
}

// NewMemoryRenderCredentialAuthorizer builds an empty fixture authorizer.
func NewMemoryRenderCredentialAuthorizer() *MemoryRenderCredentialAuthorizer {
	return &MemoryRenderCredentialAuthorizer{materials: make(map[string]string)}
}

func credKey(account domain.ProviderAccountID, mode domain.AuthMode, version int) string {
	return string(account) + "/" + string(mode) + "/" + itoa(version)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// Store binds fixture credential material for Authorize (test/fixture only).
func (a *MemoryRenderCredentialAuthorizer) Store(
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
func (a *MemoryRenderCredentialAuthorizer) Authorize(
	_ context.Context,
	validation ports.CredentialValidation,
	fn func(ports.CredentialInjection) error,
) error {
	if fn == nil {
		return ports.ErrRenderAdapterUnavailable
	}
	a.mu.Lock()
	material, ok := a.materials[credKey(validation.AccountID, validation.AuthMode, validation.Version)]
	a.mu.Unlock()
	if !ok || material == "" {
		return ports.ErrCredentialAbsent
	}
	return fn(credentialInjection{material: material})
}

type credentialInjection struct {
	material string
}

func (c credentialInjection) Use(fn func(secretMaterial string) error) error {
	if fn == nil || c.material == "" {
		return ports.ErrRenderAdapterUnavailable
	}
	return fn(c.material)
}

// PermissiveFixtureRenderCredentialAuthorizer mints opaque fixture credential
// material for any non-empty AccountID. Controlled fixtures / AllowInMemory
// only — never production. Material never leaves Authorize's callback.
type PermissiveFixtureRenderCredentialAuthorizer struct{}

// NewPermissiveFixtureRenderCredentialAuthorizer builds the fixture authorizer.
func NewPermissiveFixtureRenderCredentialAuthorizer() *PermissiveFixtureRenderCredentialAuthorizer {
	return &PermissiveFixtureRenderCredentialAuthorizer{}
}

// Authorize mints callback-scoped fixture material when AccountID is present.
func (*PermissiveFixtureRenderCredentialAuthorizer) Authorize(
	_ context.Context,
	validation ports.CredentialValidation,
	fn func(ports.CredentialInjection) error,
) error {
	if fn == nil || validation.AccountID == "" {
		return ports.ErrCredentialAbsent
	}
	return fn(credentialInjection{material: "fixture-credential-material"})
}

var (
	_ ports.RenderCredentialAuthorizer = (*FailClosedRenderCredentialAuthorizer)(nil)
	_ ports.RenderCredentialAuthorizer = (*MemoryRenderCredentialAuthorizer)(nil)
	_ ports.RenderCredentialAuthorizer = (*PermissiveFixtureRenderCredentialAuthorizer)(nil)
	_ ports.CredentialInjection        = credentialInjection{}
)
