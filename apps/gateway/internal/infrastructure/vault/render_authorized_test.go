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

// stubStaging is a test-local staging store (vault may not import persistence).
type stubStaging struct {
	puts int
}

func (s *stubStaging) Put(_ context.Context, put ports.StagingPut) error {
	s.puts++
	if len(put.Data) == 0 {
		return ports.ErrDependencyUnavailable
	}
	return nil
}
func (s *stubStaging) Use(context.Context, ports.StagingAccess, func([]byte) error) error {
	return ports.ErrStagingNotFound
}

type capturePromptAdapter struct {
	seen atomic.Value
}

func (a *capturePromptAdapter) Render(_ context.Context, _ ports.RenderCommand, prompt ports.PromptInjection, _ ports.InputAssetInjection, sink ports.RenderCaptureSink) (domain.RenderOutcome, error) {
	if prompt != nil {
		_ = prompt.Use(func(p string) error {
			a.seen.Store(p)
			return nil
		})
	}
	if sink != nil {
		if err := sink.Accept(0, domain.ContentTypePNG, []byte{0x89, 0x50, 0x4e, 0x47}); err != nil {
			return domain.RenderOutcome{}, err
		}
	}
	return domain.RenderOutcome{
		Class:  domain.RenderOutcomeSuccess,
		Commit: domain.CommitCommitted,
	}, nil
}

type alwaysValidVault struct{}

func (alwaysValidVault) Put(context.Context, ports.CredentialIntake) error { return nil }
func (alwaysValidVault) Validate(context.Context, ports.CredentialValidation) (ports.CredentialValidationResult, error) {
	return ports.CredentialValidationResult{Valid: true}, nil
}
func (alwaysValidVault) Revoke(context.Context, ports.CredentialValidation) error { return nil }

// countingSendBoundary records when MarkPayloadSent is invoked.
type countingSendBoundary struct {
	calls atomic.Int32
	err   error
}

func (b *countingSendBoundary) MarkPayloadSent(context.Context) error {
	b.calls.Add(1)
	return b.err
}

// failBeforeAdapterAuthorized fails after Validate/prompt resolve would run but
// before SendBoundary when configured via missing prompt (prompt Use fails first).
// failAfterMark calls SendBoundary then fails the Adapter.
type markThenFailAdapter struct {
	fail error
}

func (a *markThenFailAdapter) Render(context.Context, ports.RenderCommand, ports.PromptInjection, ports.InputAssetInjection, ports.RenderCaptureSink) (domain.RenderOutcome, error) {
	return domain.RenderOutcome{}, a.fail
}

type stubContentStore struct {
	byID map[domain.AssetID]ports.AssetContent
}

func (s *stubContentStore) Put(context.Context, domain.AssetID, []byte) error { return nil }
func (s *stubContentStore) Fetch(_ context.Context, _ domain.SecurityPrincipal, id domain.AssetID) (ports.AssetContent, error) {
	if s == nil || s.byID == nil {
		return ports.AssetContent{}, ports.ErrAssetNotVisible
	}
	c, ok := s.byID[id]
	if !ok {
		return ports.AssetContent{}, ports.ErrAssetNotVisible
	}
	return c, nil
}

type captureAssetAdapter struct {
	input atomic.Value
	mask  atomic.Value
}

func (a *captureAssetAdapter) Render(_ context.Context, _ ports.RenderCommand, _ ports.PromptInjection, assets ports.InputAssetInjection, sink ports.RenderCaptureSink) (domain.RenderOutcome, error) {
	if assets != nil {
		_ = assets.Use(func(inputs []ports.InputAssetMaterial, mask *ports.InputAssetMaterial) error {
			if len(inputs) > 0 {
				a.input.Store(append([]byte(nil), inputs[0].Data...))
			}
			if mask != nil {
				a.mask.Store(append([]byte(nil), mask.Data...))
			}
			return nil
		})
	}
	if sink != nil {
		_ = sink.Accept(0, domain.ContentTypePNG, []byte{0x89, 0x50, 0x4e, 0x47})
	}
	return domain.RenderOutcome{Class: domain.RenderOutcomeSuccess, Commit: domain.CommitCommitted}, nil
}

func TestAuthorizedRenderInjectsInputAndMaskAssetBytes(t *testing.T) {
	t.Parallel()

	prompts := vaultpkg.NewMemoryRenderPromptStore()
	_ = prompts.Put(context.Background(), ports.RenderPromptIntake{
		TenantID: "tenant_a", JobID: "job_edit", Material: "edit-me",
	})
	content := &stubContentStore{byID: map[domain.AssetID]ports.AssetContent{
		"asset_in":  {ContentType: domain.ContentTypePNG, Data: []byte("INPUT-BYTES")},
		"asset_msk": {ContentType: domain.ContentTypePNG, Data: []byte("MASK-BYTES")},
	}}
	adapter := &captureAssetAdapter{}
	auth := vaultpkg.NewAuthorizedRenderService(prompts, alwaysValidVault{}, adapter, &stubStaging{}, content)
	_, err := auth.Render(context.Background(), ports.AuthorizedRenderRequest{
		Principal: domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "k"},
		JobRef:    domain.JobRef{TenantID: "tenant_a", JobID: "job_edit"},
		AccountID: "pa_1",
		Version:   1,
		Invocation: domain.RenderInvocation{
			TenantID: "tenant_a", JobID: "job_edit", AttemptID: "att_1",
			Operation: domain.RenderOpInpaint, Model: "m",
		},
		Capture: ports.RenderCapturePlan{
			TenantID: "tenant_a", JobID: "job_edit", AttemptID: "att_1", ManifestID: "man_1",
		},
		InputAssetIDs: []domain.AssetID{"asset_in"},
		MaskAssetID:   "asset_msk",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if string(adapter.input.Load().([]byte)) != "INPUT-BYTES" {
		t.Fatalf("input bytes = %v, want INPUT-BYTES", adapter.input.Load())
	}
	if string(adapter.mask.Load().([]byte)) != "MASK-BYTES" {
		t.Fatalf("mask bytes = %v, want MASK-BYTES", adapter.mask.Load())
	}
}

func TestPayloadSendBoundaryOnlyAtAdapterEntry(t *testing.T) {
	t.Parallel()

	prompts := vaultpkg.NewMemoryRenderPromptStore()
	staging := &stubStaging{}
	boundary := &countingSendBoundary{}
	// No prompt stored → Use fails before MarkPayloadSent.
	auth := vaultpkg.NewAuthorizedRenderService(prompts, alwaysValidVault{}, &capturePromptAdapter{}, staging, nil)
	_, err := auth.Render(context.Background(), ports.AuthorizedRenderRequest{
		Principal: domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "k"},
		JobRef:    domain.JobRef{TenantID: "tenant_a", JobID: "job_pre"},
		AccountID: "pa_1",
		Version:   1,
		Invocation: domain.RenderInvocation{
			TenantID: "tenant_a", JobID: "job_pre", AttemptID: "att_1", Model: "m",
		},
		Capture: ports.RenderCapturePlan{
			TenantID: "tenant_a", JobID: "job_pre", AttemptID: "att_1", ManifestID: "man_1",
		},
		SendBoundary: boundary,
	})
	if err == nil {
		t.Fatal("expected failure before Adapter entry (missing prompt)")
	}
	if boundary.calls.Load() != 0 {
		t.Fatalf("MarkPayloadSent calls = %d, want 0 before Adapter entry", boundary.calls.Load())
	}

	// With prompt present, boundary fires once immediately before Adapter.
	if err := prompts.Put(context.Background(), ports.RenderPromptIntake{
		TenantID: "tenant_a", JobID: "job_pre", Material: "p",
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	adapter := &markThenFailAdapter{fail: errors.New("adapter boom")}
	auth = vaultpkg.NewAuthorizedRenderService(prompts, alwaysValidVault{}, adapter, staging, nil)
	boundary2 := &countingSendBoundary{}
	_, err = auth.Render(context.Background(), ports.AuthorizedRenderRequest{
		Principal: domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "k"},
		JobRef:    domain.JobRef{TenantID: "tenant_a", JobID: "job_pre"},
		AccountID: "pa_1",
		Version:   1,
		Invocation: domain.RenderInvocation{
			TenantID: "tenant_a", JobID: "job_pre", AttemptID: "att_1", Model: "m",
		},
		Capture: ports.RenderCapturePlan{
			TenantID: "tenant_a", JobID: "job_pre", AttemptID: "att_1", ManifestID: "man_1",
		},
		SendBoundary: boundary2,
	})
	if err == nil {
		t.Fatal("expected adapter error")
	}
	if boundary2.calls.Load() != 1 {
		t.Fatalf("MarkPayloadSent calls = %d, want 1 at Adapter entry", boundary2.calls.Load())
	}
}

func TestAuthorizedRenderInjectsPromptViaUseNotCommand(t *testing.T) {
	t.Parallel()

	prompts := vaultpkg.NewMemoryRenderPromptStore()
	staging := &stubStaging{}
	adapter := &capturePromptAdapter{}
	auth := vaultpkg.NewAuthorizedRenderService(prompts, alwaysValidVault{}, adapter, staging, nil)

	if err := prompts.Put(context.Background(), ports.RenderPromptIntake{
		TenantID: "tenant_a",
		JobID:    "job_1",
		Material: "secret-prompt-text",
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	outcome, err := auth.Render(context.Background(), ports.AuthorizedRenderRequest{
		Principal: domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "k"},
		JobRef:    domain.JobRef{TenantID: "tenant_a", JobID: "job_1"},
		AccountID: "pa_1",
		Version:   1,
		Invocation: domain.RenderInvocation{
			TenantID:  "tenant_a",
			JobID:     "job_1",
			AttemptID: "att_1",
			Model:     "m",
		},
		Capture: ports.RenderCapturePlan{
			TenantID:   "tenant_a",
			JobID:      "job_1",
			AttemptID:  "att_1",
			ManifestID: "man_1",
		},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got, _ := adapter.seen.Load().(string)
	if got != "secret-prompt-text" {
		t.Fatalf("adapter saw %q, want secret-prompt-text", got)
	}
	if len(outcome.Manifest.Entries) != 1 {
		t.Fatalf("manifest entries = %d, want 1 (bytes staged, metadata only returned)", len(outcome.Manifest.Entries))
	}
	// Outcome must not expose raw output bytes (field removed from domain type).
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
