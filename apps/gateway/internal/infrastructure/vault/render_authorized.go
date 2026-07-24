package vault

import (
	"context"
	"sync"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// MemoryRenderPromptStore is the foundation confidential prompt store. It keeps
// process-local prompt material keyed by (tenant, job) and never exposes a
// Read API to application code. AuthorizedRenderService is the only consumer
// that may resolve material for Adapter injection (ADR 0009).
type MemoryRenderPromptStore struct {
	mu    sync.Mutex
	byKey map[string]string
}

// NewMemoryRenderPromptStore builds an empty confidential prompt store.
func NewMemoryRenderPromptStore() *MemoryRenderPromptStore {
	return &MemoryRenderPromptStore{byKey: make(map[string]string)}
}

func promptKey(tenant domain.TenantID, jobID domain.Identifier) string {
	return string(tenant) + "/" + string(jobID)
}

// Put stores transient prompt material under the job identity.
func (store *MemoryRenderPromptStore) Put(_ context.Context, intake ports.RenderPromptIntake) error {
	if intake.TenantID == "" || intake.JobID == "" {
		return ports.ErrDependencyUnavailable
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.byKey[promptKey(intake.TenantID, intake.JobID)] = intake.Material
	return nil
}

// take returns and keeps material for authorized use without exporting it on
// the ports surface. Empty means missing confidential binding.
func (store *MemoryRenderPromptStore) take(tenant domain.TenantID, jobID domain.Identifier) (string, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	material, ok := store.byKey[promptKey(tenant, jobID)]
	return material, ok && material != ""
}

// AuthorizedRenderService is the protected render boundary. It validates the
// credential via Vault, requires a confidential prompt binding for the job,
// and invokes the Adapter with a secret-free RenderCommand only — prompt and
// credential plaintext never return to application code.
type AuthorizedRenderService struct {
	prompts *MemoryRenderPromptStore
	vault   ports.CredentialVault
	adapter ports.RenderAdapter
}

// NewAuthorizedRenderService wires the authorized render boundary.
func NewAuthorizedRenderService(prompts *MemoryRenderPromptStore, vault ports.CredentialVault, adapter ports.RenderAdapter) *AuthorizedRenderService {
	if prompts == nil {
		prompts = NewMemoryRenderPromptStore()
	}
	if vault == nil {
		vault = NewFailClosedCredentialVault()
	}
	if adapter == nil {
		adapter = NewFailClosedRenderAdapter()
	}
	return &AuthorizedRenderService{prompts: prompts, vault: vault, adapter: adapter}
}

// PromptStore exposes the Put-only port for composition/application wiring.
func (service *AuthorizedRenderService) PromptStore() ports.RenderPromptStore {
	return service.prompts
}

// Render resolves Vault + confidential prompt inside this boundary, then calls
// the Adapter without handing plaintext to application.
func (service *AuthorizedRenderService) Render(ctx context.Context, request ports.AuthorizedRenderRequest) (domain.RenderOutcome, error) {
	// Credential presence gate — no plaintext returned.
	if _, err := service.vault.Validate(ctx, ports.CredentialValidation{
		Principal: request.Principal,
		AccountID: request.AccountID,
		AuthMode:  request.AuthMode,
		Version:   request.Version,
	}); err != nil {
		return domain.RenderOutcome{}, err
	}
	// Prompt must be bound confidentially for this job. Material is resolved
	// here and deliberately not passed on RenderCommand (ordinary port).
	// Controlled adapters synthesize outcomes without reading prompt; production
	// Adapters will receive injection via a future Vault.Render(SecretMaterial)
	// path without widening the application-facing command.
	if _, ok := service.prompts.take(domain.TenantID(request.JobRef.TenantID), request.JobRef.JobID); !ok {
		// Fail closed: no confidential binding means no authorized render.
		return domain.RenderOutcome{}, ports.ErrRenderAdapterUnavailable
	}
	return service.adapter.Render(ctx, ports.RenderCommand{
		Principal:  request.Principal,
		AccountID:  request.AccountID,
		AuthMode:   request.AuthMode,
		Version:    request.Version,
		Invocation: request.Invocation,
	})
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

// FailClosedRenderPromptStore rejects Put so production without wiring cannot
// silently accept confidential material with no durable boundary.
type FailClosedRenderPromptStore struct{}

// NewFailClosedRenderPromptStore builds the fail-closed prompt store.
func NewFailClosedRenderPromptStore() *FailClosedRenderPromptStore {
	return &FailClosedRenderPromptStore{}
}

// Put fails closed.
func (*FailClosedRenderPromptStore) Put(context.Context, ports.RenderPromptIntake) error {
	return ports.ErrDependencyUnavailable
}

var (
	_ ports.RenderPromptStore = (*MemoryRenderPromptStore)(nil)
	_ ports.RenderPromptStore = (*FailClosedRenderPromptStore)(nil)
	_ ports.AuthorizedRender  = (*AuthorizedRenderService)(nil)
	_ ports.AuthorizedRender  = (*FailClosedAuthorizedRender)(nil)
)
