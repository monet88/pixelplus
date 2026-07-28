package domain_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// Red-first invariants from domain/port review on #54.

func TestRenderJobDoesNotStorePromptPlaintext(t *testing.T) {
	t.Parallel()

	field, ok := reflect.TypeOf(domain.RenderJob{}).FieldByName("Prompt")
	if ok {
		t.Fatalf("RenderJob must not have a Prompt field (found %s); use PromptDigest / confidential reference only (ADR 0009 TenantConfidentialStore)", field.Name)
	}
	if _, ok := reflect.TypeOf(domain.RenderJob{}).FieldByName("PromptDigest"); !ok {
		t.Fatal("RenderJob must carry PromptDigest (non-secret digest), not prompt plaintext")
	}

	now := domain.NewTimestamp(time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC))
	prompt := "super secret prompt text that must never land on the job row"
	// Opaque digests are produced by ports.RenderDigester (keyed); domain only stores the result.
	digest := "keyed-opaque-digest-not-raw-sha256-of-prompt---------------"
	job := domain.NewQueuedRenderJob(
		"job_1",
		"tenant_a",
		"key_a",
		domain.RenderOpImageGeneration,
		"gpt-image-1",
		digest,
		nil,
		"",
		"pa_1",
		1,
		domain.Fingerprint("keyed-opaque-fingerprint-not-raw-concat-----------"),
		"idem-1",
		now,
	)
	if job.PromptDigest != digest {
		t.Fatalf("PromptDigest = %q, want %q", job.PromptDigest, digest)
	}
	if job.PromptDigest == prompt {
		t.Fatal("PromptDigest must not equal the raw prompt")
	}
	// Reflect all string fields: none may equal the raw prompt.
	v := reflect.ValueOf(job)
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if f.Kind() == reflect.String && f.String() == prompt {
			t.Fatalf("field %s stores prompt plaintext", v.Type().Field(i).Name)
		}
	}
}

func TestCanTransitionMatchesSpecSection42(t *testing.T) {
	t.Parallel()

	// Allowed edges from durable-render-job §4.2.
	allowed := []struct{ from, to domain.JobLifecycleState }{
		{domain.JobQueued, domain.JobRunning},
		{domain.JobQueued, domain.JobCanceled},
		{domain.JobRunning, domain.JobRunning},
		{domain.JobRunning, domain.JobCancelRequested},
		{domain.JobRunning, domain.JobCompleted},
		{domain.JobRunning, domain.JobFailed},
		{domain.JobCancelRequested, domain.JobCanceled},
		{domain.JobCancelRequested, domain.JobFailed},
	}
	for _, edge := range allowed {
		if !domain.CanTransition(edge.from, edge.to) {
			t.Fatalf("CanTransition(%s → %s) = false, want true per §4.2", edge.from, edge.to)
		}
	}

	// Forbidden: running must not jump straight to canceled (must pass cancel_requested).
	if domain.CanTransition(domain.JobRunning, domain.JobCanceled) {
		t.Fatal("CanTransition(running → canceled) must be false; cancel of running first persists cancel_requested")
	}
	// Forbidden: once cancel CAS won, result capture must not complete the job.
	if domain.CanTransition(domain.JobCancelRequested, domain.JobCompleted) {
		t.Fatal("CanTransition(cancel_requested → completed) must be false; completion races from running before cancel CAS")
	}
	// Terminal immutability.
	for _, terminal := range []domain.JobLifecycleState{domain.JobCanceled, domain.JobFailed, domain.JobCompleted} {
		for _, to := range []domain.JobLifecycleState{domain.JobQueued, domain.JobRunning, domain.JobCancelRequested, domain.JobCanceled, domain.JobFailed, domain.JobCompleted} {
			if terminal == to {
				// Same-terminal no-op may be allowed for idempotent reads; either way must not change kind.
				continue
			}
			if domain.CanTransition(terminal, to) {
				t.Fatalf("CanTransition(%s → %s) must be false (terminal immutable)", terminal, to)
			}
		}
	}
}

func TestProviderRenderOutcomeClassesAreClosedSet(t *testing.T) {
	t.Parallel()

	// Only these four Provider outcome classes are authorized. storage_cap_later
	// is placement/delivery, not a Provider render class — a rogue string must
	// not equal any authorized const.
	authorized := []domain.RenderOutcomeClass{
		domain.RenderOutcomeSuccess,
		domain.RenderOutcomeNotCommitted,
		domain.RenderOutcomeCommitted,
		domain.RenderOutcomeUnknown,
	}
	rogue := domain.RenderOutcomeClass("storage_cap_later")
	for _, class := range authorized {
		if class == "" {
			t.Fatal("empty outcome class")
		}
		if class == rogue {
			t.Fatalf("authorized set must not include storage_cap_later (got %q)", class)
		}
	}
}
