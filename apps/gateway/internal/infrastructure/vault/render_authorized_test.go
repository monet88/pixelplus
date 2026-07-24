package vault_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	vaultpkg "github.com/monet88/pixelplus/apps/gateway/internal/infrastructure/vault"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

type capturePromptAdapter struct {
	seen atomic.Value
}

func (a *capturePromptAdapter) Render(_ context.Context, _ ports.RenderCommand, prompt ports.PromptInjection) (domain.RenderOutcome, error) {
	if prompt != nil {
		_ = prompt.Use(func(p string) error {
			a.seen.Store(p)
			return nil
		})
	}
	return domain.RenderOutcome{
		Class:   domain.RenderOutcomeSuccess,
		Commit:  domain.CommitCommitted,
		Outputs: [][]byte{{0x89, 0x50, 0x4e, 0x47}},
	}, nil
}

type alwaysValidVault struct{}

func (alwaysValidVault) Put(context.Context, ports.CredentialIntake) error { return nil }
func (alwaysValidVault) Validate(context.Context, ports.CredentialValidation) (ports.CredentialValidationResult, error) {
	return ports.CredentialValidationResult{Valid: true}, nil
}
func (alwaysValidVault) Revoke(context.Context, ports.CredentialValidation) error { return nil }

func TestAuthorizedRenderInjectsPromptViaUseNotCommand(t *testing.T) {
	t.Parallel()

	prompts := vaultpkg.NewMemoryRenderPromptStore()
	adapter := &capturePromptAdapter{}
	auth := vaultpkg.NewAuthorizedRenderService(prompts, alwaysValidVault{}, adapter)

	if err := prompts.Put(context.Background(), ports.RenderPromptIntake{
		TenantID: "tenant_a",
		JobID:    "job_1",
		Material: "secret-prompt-text",
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Command/request must not carry Prompt fields (compile-time via ports types).
	_, err := auth.Render(context.Background(), ports.AuthorizedRenderRequest{
		Principal: domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "k"},
		JobRef:    domain.JobRef{TenantID: "tenant_a", JobID: "job_1"},
		AccountID: "pa_1",
		Version:   1,
		Invocation: domain.RenderInvocation{
			TenantID: "tenant_a",
			JobID:    "job_1",
			Model:    "m",
		},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got, _ := adapter.seen.Load().(string)
	if got != "secret-prompt-text" {
		t.Fatalf("adapter saw %q, want secret-prompt-text", got)
	}
}

func TestMemoryPromptStoreDeletePurges(t *testing.T) {
	t.Parallel()

	prompts := vaultpkg.NewMemoryRenderPromptStore()
	_ = prompts.Put(context.Background(), ports.RenderPromptIntake{
		TenantID: "tenant_a", JobID: "job_1", Material: "x",
	})
	if prompts.Len() != 1 {
		t.Fatalf("Len = %d, want 1", prompts.Len())
	}
	_ = prompts.Delete(context.Background(), ports.RenderPromptAccess{TenantID: "tenant_a", JobID: "job_1"})
	if prompts.Len() != 0 {
		t.Fatalf("Len after Delete = %d, want 0", prompts.Len())
	}
}

func TestFailClosedPromptStoreRejectsPut(t *testing.T) {
	t.Parallel()

	store := vaultpkg.NewFailClosedRenderPromptStore()
	err := store.Put(context.Background(), ports.RenderPromptIntake{
		TenantID: "t", JobID: "j", Material: "p",
	})
	if !errors.Is(err, ports.ErrDependencyUnavailable) {
		t.Fatalf("Put error = %v, want ErrDependencyUnavailable", err)
	}
}
