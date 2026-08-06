package contracttest_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// This file pins the render posture #62 F7 questioned. It does NOT implement the
// Codex render surface: decision 0014 deliberately defers ports.RenderAdapter,
// the gated render registry, and the render-candidate-gate relaxation to their own
// story, because those three must land together or the composition is incoherent.
// The image-edit mask work the issue's evidence comment describes
// (`input_image_mask.image_url`, `alpha_out = 255 - luminance_in` via an RGBA
// encoder, and the pixel-outside-mask check) belongs to that story too.
//
// What was missing — and is what this file adds — is proof of the posture 0014
// actually claims in the meantime. The ADR states two things that nothing tested:
//
//  1. A gated mode the operator did NOT enable is refused at the render candidate
//     gate, ahead of the Vault.
//  2. An ENABLED gated mode passes the candidate gate and then fails closed at
//     EXECUTION for lack of a ports.RenderAdapter — the "accept-then-fail"
//     posture. Left unproven, a future change could quietly turn that into a
//     completed job with fabricated output, which is far worse than a clean
//     failure.
//
// Until the render story lands, the honest completion evidence for T19's render
// surface is that it fails closed rather than that it works.

// The render surface must refuse a killed gated mode before the Vault. The
// zero-Vault assertion is load-bearing: a refusal after the decrypt would mean
// credential material was released for a mode the operator turned off
// (decision 0014 §5.2).
func TestGatedCodexRenderRefusedBeforeTheVaultWithoutTheOperatorFlag(t *testing.T) {
	t.Parallel()

	h := newRenderHarness(t, func(h *renderHarness) {
		h.gatedAuthModes = nil
		seedRoutableImageAccount(h, "pa_render_codex_noflag")
	})

	response, payload := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-render-codex-noflag",
		body:    `{"model":"gpt-image-1","prompt":"a red circle"}`,
	})
	if response.StatusCode == http.StatusAccepted {
		t.Fatalf("status = 202, want a refusal for a gated mode the operator did not enable (body=%s)", payload)
	}
	if code := decodeError(t, payload)["code"]; code != "auth_mode_unavailable" {
		t.Errorf("code = %v, want auth_mode_unavailable", code)
	}
	if valid := h.vault.validCalls.Load(); valid != 0 {
		t.Errorf("vault.Validate ran %d times, want 0 — the operator-flag gate must precede the render decrypt", valid)
	}
	if calls := h.renderCalls.Load(); calls != 0 {
		t.Errorf("render Adapter ran %d times, want 0", calls)
	}
}

// The accept-then-fail posture (decision 0014): an ENABLED gated Codex account
// passes the render candidate gate — the job IS accepted — and then execution
// fails closed because the deployment has no ports.RenderAdapter.
//
// Cause and effect this pins down: the Codex Adapter implements chat, stream,
// probe, and capability but NOT render. Composition therefore leaves the
// fail-closed render foundation in place, and the job must reach a terminal
// FAILURE. The assertion that matters is the negative one — the job must never
// reach `completed`, because a completed render job carries an output manifest,
// and minting one for a generation that never happened would hand a Tenant a
// fabricated asset.
func TestEnabledGatedCodexRenderIsAcceptedThenFailsClosedAtExecution(t *testing.T) {
	t.Parallel()

	h := newRenderHarness(t, func(h *renderHarness) {
		// The mode is enabled by the operator, and the deployment has no render
		// Adapter — the exact T19 shipping posture.
		h.gatedAuthModes = []domain.AuthMode{domain.AuthModeChatGPTCodexOAuth}
		h.omitRenderAdapter = true
		seedRoutableImageAccount(h, "pa_render_codex_enabled")
	})

	create, payload := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-render-codex-enabled",
		body:    `{"model":"gpt-image-1","prompt":"accept then fail"}`,
	})
	if create.StatusCode != http.StatusAccepted {
		t.Fatalf("create status = %d, want 202: an enabled gated mode must pass the candidate gate (body=%s)", create.StatusCode, payload)
	}
	var job map[string]any
	if err := json.Unmarshal(payload, &job); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	jobID, _ := job["job_id"].(string)
	if jobID == "" {
		t.Fatalf("job_id missing: %s", payload)
	}

	workerCtx, cancelWorkers := context.WithCancel(t.Context())
	defer cancelWorkers()
	workerResult := make(chan error, 1)
	go func() {
		workerResult <- h.fixture.Runtime().RunWorkers(workerCtx)
	}()
	select {
	case <-h.fixture.WorkersStarted():
	case <-time.After(2 * time.Second):
		t.Fatal("RunWorkers did not start (possible deadlock)")
	}

	deadline := time.Now().Add(3 * time.Second)
	var lastState string
	var lastBody []byte
	for {
		get, body := h.do(t, requestSpec{
			method: http.MethodGet,
			path:   "/v1/render-jobs/" + jobID,
			bearer: tenantAKey,
		})
		lastBody = body
		if get.StatusCode == http.StatusOK {
			var current map[string]any
			if err := json.Unmarshal(body, &current); err == nil {
				lastState, _ = current["lifecycle_state"].(string)
				// A completed job is the failure this test exists to catch: it would
				// mean output was manifested without a render Adapter.
				if lastState == "completed" {
					t.Fatalf("job reached completed with no render Adapter; output must never be fabricated (body=%s)", body)
				}
				if lastState == "failed" || lastState == "canceled" {
					break
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("job never reached a terminal failure (last state=%q, body=%s)", lastState, lastBody)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if lastState != "failed" {
		t.Fatalf("terminal state = %q, want failed", lastState)
	}
	// And no output was manifested on the way to that failure.
	var terminal map[string]any
	if err := json.Unmarshal(lastBody, &terminal); err != nil {
		t.Fatalf("decode terminal job: %v", err)
	}
	if entries, _ := terminal["output_entries"].([]any); len(entries) != 0 {
		t.Errorf("output_entries = %d, want 0 on a fail-closed execution", len(entries))
	}

	cancelWorkers()
	select {
	case err := <-workerResult:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("RunWorkers error = %v, want nil or context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunWorkers did not exit after cancel (deadlock)")
	}
}
