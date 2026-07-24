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

// Use injects prompt plaintext into fn without returning it as a value.
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

// Len reports retained prompt count (test observation only).
func (store *MemoryRenderPromptStore) Len() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return len(store.byKey)
}

type promptInjection struct {
	material string
}

func (p promptInjection) Use(fn func(plaintext string) error) error {
	if fn == nil || p.material == "" {
		return ports.ErrRenderAdapterUnavailable
	}
	return fn(p.material)
}

// stagingCaptureSink stages Provider output bytes into RenderStagingStore and
// accumulates safe OutputEntry metadata. Application never sees the bytes.
type stagingCaptureSink struct {
	ctx     context.Context
	staging ports.RenderStagingStore
	plan    ports.RenderCapturePlan
	entries []domain.OutputEntry
}

func (s *stagingCaptureSink) Accept(position int, contentType string, data []byte) error {
	if s.staging == nil || s.plan.TenantID == "" || s.plan.JobID == "" || s.plan.ManifestID == "" {
		return ports.ErrDependencyUnavailable
	}
	if len(data) == 0 {
		return ports.ErrDependencyUnavailable
	}
	if contentType == "" {
		contentType = domain.DefaultOutputContentType
	}
	checksum := domain.StagingChecksum(data)
	entryID := domain.NewOutputEntryID(s.plan.JobID, position)
	if err := s.staging.Put(s.ctx, ports.StagingPut{
		Identity: ports.StagingIdentity{
			TenantID:   s.plan.TenantID,
			JobID:      s.plan.JobID,
			ManifestID: s.plan.ManifestID,
			EntryID:    entryID,
			Checksum:   checksum,
		},
		ContentType: contentType,
		Data:        data,
	}); err != nil {
		return err
	}
	s.entries = append(s.entries, domain.OutputEntry{
		ID:            entryID,
		Position:      position,
		DeliveryState: domain.OutputPending,
		ContentType:   contentType,
		ByteSize:      int64(len(data)),
		Checksum:      checksum,
	})
	return nil
}

func (s *stagingCaptureSink) manifest() domain.ResultManifest {
	checksum := ""
	if len(s.entries) > 0 {
		checksum = s.entries[0].Checksum
	}
	return domain.ResultManifest{
		ID:              s.plan.ManifestID,
		AttemptID:       s.plan.AttemptID,
		Entries:         append([]domain.OutputEntry(nil), s.entries...),
		StagingChecksum: checksum,
	}
}

// AuthorizedRenderService is the protected render boundary.
//
// Limitation (honest): CredentialVault currently supports only Validate/Put/Revoke —
// credential plaintext is not injected into the Adapter. When Vault.Render(SecretMaterial)
// lands it must inject credentials inside the Vault without returning them to application.
type AuthorizedRenderService struct {
	prompts ports.RenderPromptStore
	vault   ports.CredentialVault
	adapter ports.RenderAdapter
	staging ports.RenderStagingStore
}

// NewAuthorizedRenderService wires the authorized render boundary.
func NewAuthorizedRenderService(
	prompts ports.RenderPromptStore,
	vault ports.CredentialVault,
	adapter ports.RenderAdapter,
	staging ports.RenderStagingStore,
) *AuthorizedRenderService {
	if vault == nil {
		vault = NewFailClosedCredentialVault()
	}
	return &AuthorizedRenderService{
		prompts: prompts,
		vault:   vault,
		adapter: adapter,
		staging: staging,
	}
}

// Render resolves Vault Validate + prompt Use, invokes the Adapter with a
// capture sink that stages bytes, and returns only safe manifest metadata.
func (service *AuthorizedRenderService) Render(ctx context.Context, request ports.AuthorizedRenderRequest) (domain.RenderOutcome, error) {
	if service.prompts == nil || service.adapter == nil || service.staging == nil {
		return domain.RenderOutcome{}, ports.ErrRenderAdapterUnavailable
	}
	validation, err := service.vault.Validate(ctx, ports.CredentialValidation{
		Principal: request.Principal,
		AccountID: request.AccountID,
		AuthMode:  request.AuthMode,
		Version:   request.Version,
	})
	if err != nil {
		return domain.RenderOutcome{}, err
	}
	// Valid=false is a usability reject, not only a dependency error (#15/#46).
	if !validation.Valid {
		return domain.RenderOutcome{}, ports.ErrCredentialAbsent
	}

	plan := request.Capture
	if plan.TenantID == "" {
		plan.TenantID = domain.TenantID(request.JobRef.TenantID)
	}
	if plan.JobID == "" {
		plan.JobID = request.JobRef.JobID
	}
	if plan.AttemptID == "" {
		plan.AttemptID = request.Invocation.AttemptID
	}
	if plan.ManifestID == "" && plan.AttemptID != "" {
		plan.ManifestID = domain.NewResultManifestID(plan.AttemptID)
	}

	sink := &stagingCaptureSink{ctx: ctx, staging: service.staging, plan: plan}
	var outcome domain.RenderOutcome
	var renderErr error
	access := ports.RenderPromptAccess{
		TenantID: domain.TenantID(request.JobRef.TenantID),
		JobID:    request.JobRef.JobID,
	}
	err = service.prompts.Use(ctx, access, func(plaintext string) error {
		injection := promptInjection{material: plaintext}
		outcome, renderErr = service.adapter.Render(ctx, ports.RenderCommand{
			Principal:  request.Principal,
			AccountID:  request.AccountID,
			AuthMode:   request.AuthMode,
			Version:    request.Version,
			Invocation: request.Invocation,
		}, injection, sink)
		return renderErr
	})
	if err != nil {
		return domain.RenderOutcome{}, err
	}
	// Attach safe staged metadata for successful/committed paths.
	if outcome.Class == domain.RenderOutcomeSuccess || outcome.Class == domain.RenderOutcomeCommitted {
		outcome.Manifest = sink.manifest()
		if outcome.Commit == "" {
			outcome.Commit = domain.CommitCommitted
		}
	}
	return outcome, nil
}

// FailClosedAuthorizedRender fails every render closed.
type FailClosedAuthorizedRender struct{}

// NewFailClosedAuthorizedRender builds the fail-closed authorized render port.
func NewFailClosedAuthorizedRender() *FailClosedAuthorizedRender {
	return &FailClosedAuthorizedRender{}
}

// Render fails closed.
func (*FailClosedAuthorizedRender) Render(context.Context, ports.AuthorizedRenderRequest) (domain.RenderOutcome, error) {
	return domain.RenderOutcome{}, ports.ErrRenderAdapterUnavailable
}

// FailClosedRenderPromptStore rejects confidential operations.
type FailClosedRenderPromptStore struct{}

// NewFailClosedRenderPromptStore builds the fail-closed prompt store.
func NewFailClosedRenderPromptStore() *FailClosedRenderPromptStore {
	return &FailClosedRenderPromptStore{}
}

func (*FailClosedRenderPromptStore) Put(context.Context, ports.RenderPromptIntake) error {
	return ports.ErrDependencyUnavailable
}
func (*FailClosedRenderPromptStore) Use(context.Context, ports.RenderPromptAccess, func(string) error) error {
	return ports.ErrRenderAdapterUnavailable
}
func (*FailClosedRenderPromptStore) Delete(context.Context, ports.RenderPromptAccess) error {
	return nil
}

var (
	_ ports.RenderPromptStore = (*MemoryRenderPromptStore)(nil)
	_ ports.RenderPromptStore = (*FailClosedRenderPromptStore)(nil)
	_ ports.AuthorizedRender  = (*AuthorizedRenderService)(nil)
	_ ports.AuthorizedRender  = (*FailClosedAuthorizedRender)(nil)
	_ ports.PromptInjection   = promptInjection{}
	_ ports.RenderCaptureSink = (*stagingCaptureSink)(nil)
)
