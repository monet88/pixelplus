package vault

import (
	"context"
	"sync"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// MemoryRenderPromptStore is a process-local controlled confidential prompt
// store for fixtures. It is NOT the production default (use
// FailClosedRenderPromptStore unless AllowInMemoryRenderJobs / explicit inject).
// Use injects material into a callback; Delete purges terminal/rollback paths.
type MemoryRenderPromptStore struct {
	mu    sync.Mutex
	byKey map[string]string
}

// NewMemoryRenderPromptStore builds an empty controlled prompt store.
func NewMemoryRenderPromptStore() *MemoryRenderPromptStore {
	return &MemoryRenderPromptStore{byKey: make(map[string]string)}
}

func promptKey(tenant domain.TenantID, jobID domain.Identifier) string {
	return string(tenant) + "/" + string(jobID)
}

// Put stores transient prompt material under the job identity.
func (store *MemoryRenderPromptStore) Put(_ context.Context, intake ports.RenderPromptIntake) error {
	if intake.TenantID == "" || intake.JobID == "" || intake.Material == "" {
		return ports.ErrDependencyUnavailable
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.byKey[promptKey(intake.TenantID, intake.JobID)] = intake.Material
	return nil
}

// Use injects a copy of prompt plaintext into fn. Material is not returned to
// the caller as a value and is not deleted by Use (Delete is explicit purge).
func (store *MemoryRenderPromptStore) Use(_ context.Context, access ports.RenderPromptAccess, fn func(plaintext string) error) error {
	if fn == nil || access.TenantID == "" || access.JobID == "" {
		return ports.ErrRenderAdapterUnavailable
	}
	store.mu.Lock()
	material, ok := store.byKey[promptKey(access.TenantID, access.JobID)]
	store.mu.Unlock()
	if !ok || material == "" {
		return ports.ErrRenderAdapterUnavailable
	}
	return fn(material)
}

// Delete purges confidential prompt material for the job (terminal/rollback).
func (store *MemoryRenderPromptStore) Delete(_ context.Context, access ports.RenderPromptAccess) error {
	if access.TenantID == "" || access.JobID == "" {
		return nil
	}
	store.mu.Lock()
	delete(store.byKey, promptKey(access.TenantID, access.JobID))
	store.mu.Unlock()
	return nil
}

// Len reports how many prompts are retained (test observation only).
func (store *MemoryRenderPromptStore) Len() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return len(store.byKey)
}

// promptInjection is a call-scoped PromptInjection built only inside this
// package. Application cannot obtain a populated injection from stored prompts.
type promptInjection struct {
	material string
}

// Use grants the Adapter one-shot access to prompt plaintext for this call.
func (p promptInjection) Use(fn func(plaintext string) error) error {
	if fn == nil || p.material == "" {
		return ports.ErrRenderAdapterUnavailable
	}
	return fn(p.material)
}

// AuthorizedRenderService is the protected render boundary. It validates the
// credential via Vault (presence only — CredentialVault does not release
// plaintext), Uses confidential prompt via bounded callback, and injects prompt
// into the Adapter through PromptInjection for this call only.
//
// Limitation (honest): production CredentialVault currently supports only
// Validate/Put/Revoke — no SecretMaterial injection into the Adapter. Credential
// plaintext remains Vault-owned and is not passed to RenderAdapter. When a real
// Vault.Render(SecretMaterial) lands, it must inject credentials inside the
// Vault boundary without returning them to application.
type AuthorizedRenderService struct {
	prompts ports.RenderPromptStore
	vault   ports.CredentialVault
	adapter ports.RenderAdapter
}

// NewAuthorizedRenderService wires the authorized render boundary.
// prompts and adapter must be non-nil; this constructor does not invent a
// Memory prompt store (composition owns fail-closed vs controlled selection).
func NewAuthorizedRenderService(prompts ports.RenderPromptStore, vault ports.CredentialVault, adapter ports.RenderAdapter) *AuthorizedRenderService {
	if vault == nil {
		vault = NewFailClosedCredentialVault()
	}
	return &AuthorizedRenderService{prompts: prompts, vault: vault, adapter: adapter}
}

// Render resolves Vault Validate + confidential prompt Use inside this boundary,
// then calls the Adapter with a call-scoped PromptInjection.
func (service *AuthorizedRenderService) Render(ctx context.Context, request ports.AuthorizedRenderRequest) (domain.RenderOutcome, error) {
	if service.prompts == nil || service.adapter == nil {
		return domain.RenderOutcome{}, ports.ErrRenderAdapterUnavailable
	}
	// Credential presence gate — no plaintext returned (Vault-owned only).
	if _, err := service.vault.Validate(ctx, ports.CredentialValidation{
		Principal: request.Principal,
		AccountID: request.AccountID,
		AuthMode:  request.AuthMode,
		Version:   request.Version,
	}); err != nil {
		return domain.RenderOutcome{}, err
	}

	var outcome domain.RenderOutcome
	var renderErr error
	access := ports.RenderPromptAccess{
		TenantID: domain.TenantID(request.JobRef.TenantID),
		JobID:    request.JobRef.JobID,
	}
	// Bounded Use: material never assigned to application-visible fields.
	err := service.prompts.Use(ctx, access, func(plaintext string) error {
		// Construct injection inside this package only for this call frame.
		injection := promptInjection{material: plaintext}
		outcome, renderErr = service.adapter.Render(ctx, ports.RenderCommand{
			Principal:  request.Principal,
			AccountID:  request.AccountID,
			AuthMode:   request.AuthMode,
			Version:    request.Version,
			Invocation: request.Invocation,
		}, injection)
		return renderErr
	})
	if err != nil {
		return domain.RenderOutcome{}, err
	}
	return outcome, renderErr
}

// FailClosedAuthorizedRender fails every render closed (no Adapter/Vault).
type FailClosedAuthorizedRender struct{}

// NewFailClosedAuthorizedRender builds the fail-closed authorized render port.
func NewFailClosedAuthorizedRender() *FailClosedAuthorizedRender {
	return &FailClosedAuthorizedRender{}
}

// Render fails closed.
func (*FailClosedAuthorizedRender) Render(context.Context, ports.AuthorizedRenderRequest) (domain.RenderOutcome, error) {
	return domain.RenderOutcome{}, ports.ErrRenderAdapterUnavailable
}

// FailClosedRenderPromptStore rejects every confidential operation so production
// without an explicit controlled store cannot silently retain prompts.
type FailClosedRenderPromptStore struct{}

// NewFailClosedRenderPromptStore builds the fail-closed prompt store.
func NewFailClosedRenderPromptStore() *FailClosedRenderPromptStore {
	return &FailClosedRenderPromptStore{}
}

// Put fails closed.
func (*FailClosedRenderPromptStore) Put(context.Context, ports.RenderPromptIntake) error {
	return ports.ErrDependencyUnavailable
}

// Use fails closed.
func (*FailClosedRenderPromptStore) Use(context.Context, ports.RenderPromptAccess, func(string) error) error {
	return ports.ErrRenderAdapterUnavailable
}

// Delete is a no-op success (nothing to purge).
func (*FailClosedRenderPromptStore) Delete(context.Context, ports.RenderPromptAccess) error {
	return nil
}

// emptyPromptInjection is used by fail-closed adapters that must satisfy the
// interface without receiving material.
type emptyPromptInjection struct{}

func (emptyPromptInjection) Use(func(string) error) error {
	return ports.ErrRenderAdapterUnavailable
}

var (
	_ ports.RenderPromptStore = (*MemoryRenderPromptStore)(nil)
	_ ports.RenderPromptStore = (*FailClosedRenderPromptStore)(nil)
	_ ports.AuthorizedRender  = (*AuthorizedRenderService)(nil)
	_ ports.AuthorizedRender  = (*FailClosedAuthorizedRender)(nil)
	_ ports.PromptInjection   = promptInjection{}
	_ ports.PromptInjection   = emptyPromptInjection{}
)
