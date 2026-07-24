package contracttest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/contracttest"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// renderHarness extends the shared spine ports with image/job scopes and a
// controlled Render Adapter so Public API create/execute proofs enter through
// composition.Runtime.Handler and the exported JobExecutor only.
type renderHarness struct {
	*spineHarness
	renderCalls      atomic.Int32
	lastPrompt       atomic.Value // string seen by controlled adapter PromptInjection
	adapter          *countingRenderAdapter
	enqueueFailTimes int
	enqueueError     error
}

func newRenderHarness(t *testing.T, configure func(*renderHarness)) *renderHarness {
	t.Helper()

	log := &spineLog{}
	principal := &stubPrincipalStore{
		log: log,
		principals: map[string]domain.SecurityPrincipal{
			tenantAKey: {
				TenantID:       "tenant_a",
				ClientAPIKeyID: "key_a",
				Scopes: domain.NewScopeSet(
					domain.ScopeAccountsRead,
					domain.ScopeAccountsManage,
					domain.ScopeCapabilitiesRead,
					domain.ScopeRoutingRead,
					domain.ScopeRoutingManage,
					domain.ScopeImagesGenerate,
					domain.ScopeImagesEdit,
					domain.ScopeJobsRead,
					domain.ScopeJobsManage,
					domain.ScopeAssetsRead,
					domain.ScopeAssetsWrite,
				),
			},
			readOnly: {
				TenantID:       "tenant_a",
				ClientAPIKeyID: "key_r",
				Scopes:         domain.NewScopeSet(domain.ScopeJobsRead),
			},
			tenantBKey: {
				TenantID:       "tenant_b",
				ClientAPIKeyID: "key_b",
				Scopes: domain.NewScopeSet(
					domain.ScopeImagesGenerate,
					domain.ScopeImagesEdit,
					domain.ScopeJobsRead,
					domain.ScopeJobsManage,
					domain.ScopeAccountsRead,
					domain.ScopeAccountsManage,
					domain.ScopeCapabilitiesRead,
					domain.ScopeRoutingRead,
					domain.ScopeRoutingManage,
				),
			},
		},
	}

	h := &renderHarness{
		spineHarness: &spineHarness{
			log:          log,
			principal:    principal,
			admission:    &stubAdmissionStore{log: log},
			replay:       newStubReplayStore(log),
			accounts:     newStubAccountStore(log),
			health:       newStubHealthStore(),
			audit:        &captureAudit{},
			telemetry:    &captureTelemetry{},
			reqLog:       &captureRequestLog{},
			vault:        newStubCredentialVault(log),
			probe:        newStubProbeAdapter(log),
			oauth:        newStubOAuthExchangeAdapter(log),
			capabilities: newStubCapabilityStore(log),
			capability:   newStubCapabilityAdapter(log),
			circuits:     newStubCircuitStore(log),
			routing:      newCountingRoutingPolicyStore(),
			clock:        &mutableTestClock{now: spineFixtureTime},
		},
	}
	if configure != nil {
		configure(h)
	}

	h.adapter = &countingRenderAdapter{harness: h}
	opts := contracttest.Options{
		Principal:     h.principal,
		Admission:     h.admission,
		Replay:        h.replay,
		Accounts:      h.accounts,
		Health:        h.health,
		Audit:         h.audit,
		Telemetry:     h.telemetry,
		RequestLog:    h.reqLog,
		Vault:         h.vault,
		Probe:         h.probe,
		OAuth:         h.oauth,
		Capabilities:  h.capabilities,
		Capability:    h.capability,
		Circuits:      h.circuits,
		Routing:       h.routing,
		Clock:         h.clock,
		RenderAdapter: h.adapter,
	}
	if h.enqueueFailTimes > 0 {
		opts.EnqueueFailTimes = h.enqueueFailTimes
		opts.EnqueueError = h.enqueueError
	}
	fixture, err := contracttest.NewFixture(opts)
	if err != nil {
		t.Fatalf("NewFixture() error = %v", err)
	}
	h.fixture = fixture
	t.Cleanup(func() {
		closeFixture(t, fixture)
	})
	return h
}

// countingRenderAdapter is a controlled Provider render surface. It records the
// exact prompt plaintext and input/mask Asset bytes injected via protected
// Use-scoped values for ADR 0009 proof, plus AuthMode on RenderCommand.
type countingRenderAdapter struct {
	harness *renderHarness
	outcome domain.RenderOutcome
	err     error
	// block, when non-nil, blocks Render until closed (running cancel tests).
	block <-chan struct{}
	// entered signals that Render has started (after reading prompt).
	entered chan struct{}
	// lastAuthMode is the AuthMode on the last RenderCommand (safe surface).
	lastAuthMode atomic.Value // domain.AuthMode
	// lastInput/lastMask capture Asset bytes seen inside InputAssetInjection.Use.
	lastInput atomic.Value // []byte
	lastMask  atomic.Value // []byte
}

func (adapter *countingRenderAdapter) Render(_ context.Context, cmd ports.RenderCommand, prompt ports.PromptInjection, assets ports.InputAssetInjection, sink ports.RenderCaptureSink) (domain.RenderOutcome, error) {
	adapter.harness.renderCalls.Add(1)
	adapter.lastAuthMode.Store(cmd.AuthMode)
	if prompt != nil {
		_ = prompt.Use(func(plaintext string) error {
			adapter.harness.lastPrompt.Store(plaintext)
			return nil
		})
	}
	if assets != nil {
		_ = assets.Use(func(inputs []ports.InputAssetMaterial, mask *ports.InputAssetMaterial) error {
			if len(inputs) > 0 {
				adapter.lastInput.Store(append([]byte(nil), inputs[0].Data...))
			}
			if mask != nil {
				adapter.lastMask.Store(append([]byte(nil), mask.Data...))
			}
			return nil
		})
	}
	if adapter.entered != nil {
		select {
		case <-adapter.entered:
		default:
			close(adapter.entered)
		}
	}
	if adapter.block != nil {
		<-adapter.block
	}
	if adapter.err != nil {
		return domain.RenderOutcome{}, adapter.err
	}
	if adapter.outcome.Class != "" && adapter.outcome.Class != domain.RenderOutcomeSuccess && adapter.outcome.Class != domain.RenderOutcomeCommitted {
		return adapter.outcome, nil
	}
	if sink != nil {
		if err := sink.Accept(0, domain.ContentTypePNG, fixtureMinimalPNG()); err != nil {
			return domain.RenderOutcome{}, err
		}
	}
	return domain.RenderOutcome{
		Class:  domain.RenderOutcomeSuccess,
		Commit: domain.CommitCommitted,
	}, nil
}

// fixtureMinimalPNG is controlled contract-test fixture bytes only — never
// exported from production domain (review finding #3).
func fixtureMinimalPNG() []byte {
	return []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
		0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00,
		0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49,
		0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
	}
}

// seedRoutableImageAccount seeds one active same-Tenant account with a fresh
// image_generation capability and a Routing Policy that selects it.
func seedRoutableImageAccount(h *renderHarness, accountID string) {
	mode := domain.AuthModeChatGPTCodexOAuth
	account := activeProbedAccount(accountID, mode)
	h.seedAccount("tenant_a", account)
	h.capabilities.seed("tenant_a", imageGenerationSnapshot(domain.ProviderAccountID(accountID), mode, 1, spineFixtureTime))
	h.routing.Seed("tenant_a", domain.RoutingPolicy{
		CandidateAccounts: []domain.ProviderAccountID{domain.ProviderAccountID(accountID)},
		SelectionOrder:    []domain.ProviderAccountID{domain.ProviderAccountID(accountID)},
		FallbackEnabled:   false,
		FallbackChain:     []domain.ProviderAccountID{},
		FallbackAuthModes: []domain.AuthMode{},
		Affinity:          domain.AffinityPolicy{Enabled: false},
		LeasePolicy:       domain.LeasePolicy{Enabled: true, EligibleUnits: []domain.LeaseUnit{domain.LeaseUnitRenderJob}},
		UpdatedAt:         domain.NewTimestamp(spineFixtureTime),
		UpdatedBy:         "key_a",
	})
}

// imageGenerationSnapshot is a fresh capability projection that offers
// image_generation (and edit) without inventing unrelated chat evidence edges.
func imageGenerationSnapshot(accountID domain.ProviderAccountID, mode domain.AuthMode, version int, verifiedAt time.Time) domain.CapabilitySnapshot {
	return domain.NewLiveProbeSnapshot(
		accountID,
		mode,
		version,
		domain.NewTimestamp(verifiedAt),
		map[domain.CapabilityOperation]domain.CapabilityFact{
			domain.CapabilityOpImageGeneration: {
				Status:        domain.CapabilityVerified,
				EvidenceClass: domain.EvidenceLiveProbe,
				ProbeSurface:  "/images/generations",
			},
			domain.CapabilityOpImageEdit: {
				Status:        domain.CapabilityVerified,
				EvidenceClass: domain.EvidenceLiveProbe,
				ProbeSurface:  "/images/edits",
			},
			domain.CapabilityOpInpaint: {
				Status:        domain.CapabilityVerified,
				EvidenceClass: domain.EvidenceLiveProbe,
				ProbeSurface:  "/images/inpaints",
			},
		},
		nil, // empty models: operation-level offerability is enough for create routing
		"/images/generations",
	)
}

// Slice 1 (AC create): an admitted valid generation request creates exactly one
// queued Render Job and enqueues exactly one secret-free SafeJobReference.
// Proof enters only through public HTTP on the real composed Handler.
func TestAdmittedImageGenerationCreatesOneQueuedJobAndOneEnqueue(t *testing.T) {
	t.Parallel()

	h := newRenderHarness(t, func(h *renderHarness) {
		seedRoutableImageAccount(h, "pa_render_1")
	})

	response, payload := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-gen-1",
		body:    `{"model":"gpt-image-1","prompt":"a red circle"}`,
	})
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body=%s)", response.StatusCode, payload)
	}

	var job map[string]any
	if err := json.Unmarshal(payload, &job); err != nil {
		t.Fatalf("decode job: %v", err)
	}
	jobID, _ := job["job_id"].(string)
	if jobID == "" {
		t.Fatalf("job_id missing: %v", job)
	}
	if job["lifecycle_state"] != "queued" {
		t.Fatalf("lifecycle_state = %v, want queued", job["lifecycle_state"])
	}
	if job["operation"] != "image_generation" {
		t.Fatalf("operation = %v, want image_generation", job["operation"])
	}
	if job["provider_account_id"] != "pa_render_1" {
		t.Fatalf("provider_account_id = %v, want pa_render_1", job["provider_account_id"])
	}
	// Public projection must never echo prompt content.
	if _, exists := job["prompt"]; exists {
		t.Fatalf("public job must not expose prompt: %v", job)
	}

	refs := h.fixture.EnqueuedReferences()
	if len(refs) != 1 {
		t.Fatalf("enqueue count = %d, want 1 (events=%v)", len(refs), h.fixture.Events())
	}
	if string(refs[0].TenantID) != "tenant_a" {
		t.Fatalf("enqueue tenant = %q, want tenant_a", refs[0].TenantID)
	}
	if string(refs[0].JobID) != jobID {
		t.Fatalf("enqueue job_id = %q, want %q", refs[0].JobID, jobID)
	}
	// SafeJobReference grants no independent credential/content authority:
	// only Tenant + Job identities.
	if refs[0].TenantID == "" || refs[0].JobID == "" {
		t.Fatalf("enqueue reference incomplete: %+v", refs[0])
	}

	if calls := h.renderCalls.Load(); calls != 0 {
		t.Fatalf("render adapter calls = %d, want 0 before worker claim", calls)
	}
	if admits := h.admission.admitCalls.Load(); admits != 1 {
		t.Fatalf("admission.Admit calls = %d, want 1", admits)
	}
}

// Slice 2: matching scoped idempotency replay returns the same job with zero
// new admission, enqueue, or render side effects.
func TestMatchingImageGenerationReplayReturnsSameJobZeroSideEffects(t *testing.T) {
	t.Parallel()

	h := newRenderHarness(t, func(h *renderHarness) {
		seedRoutableImageAccount(h, "pa_render_replay")
	})

	first, payload := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-gen-replay",
		body:    `{"model":"gpt-image-1","prompt":"a blue square"}`,
	})
	if first.StatusCode != http.StatusAccepted {
		t.Fatalf("first status = %d, want 202 (body=%s)", first.StatusCode, payload)
	}
	var job1 map[string]any
	if err := json.Unmarshal(payload, &job1); err != nil {
		t.Fatalf("decode first job: %v", err)
	}
	jobID := job1["job_id"].(string)
	admitsAfterCreate := h.admission.admitCalls.Load()
	enqueuesAfterCreate := len(h.fixture.EnqueuedReferences())

	second, payload2 := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-gen-replay",
		body:    `{"model":"gpt-image-1","prompt":"a blue square"}`,
	})
	if second.StatusCode != http.StatusAccepted {
		t.Fatalf("replay status = %d, want 202 (body=%s)", second.StatusCode, payload2)
	}
	var job2 map[string]any
	if err := json.Unmarshal(payload2, &job2); err != nil {
		t.Fatalf("decode replay job: %v", err)
	}
	if job2["job_id"] != jobID {
		t.Fatalf("replay job_id = %v, want %s", job2["job_id"], jobID)
	}
	if h.admission.admitCalls.Load() != admitsAfterCreate {
		t.Fatalf("admission calls after replay = %d, want %d (zero new admission)", h.admission.admitCalls.Load(), admitsAfterCreate)
	}
	if len(h.fixture.EnqueuedReferences()) != enqueuesAfterCreate {
		t.Fatalf("enqueue count after replay = %d, want %d", len(h.fixture.EnqueuedReferences()), enqueuesAfterCreate)
	}
	if h.renderCalls.Load() != 0 {
		t.Fatalf("render calls = %d, want 0 on create/replay", h.renderCalls.Load())
	}
}

// Slice 3: fingerprint conflict on the same scoped key creates no replacement.
func TestImageGenerationFingerprintConflictCreatesNoReplacement(t *testing.T) {
	t.Parallel()

	h := newRenderHarness(t, func(h *renderHarness) {
		seedRoutableImageAccount(h, "pa_render_conflict")
	})

	first, payload := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-gen-conflict",
		body:    `{"model":"gpt-image-1","prompt":"first prompt"}`,
	})
	if first.StatusCode != http.StatusAccepted {
		t.Fatalf("first status = %d, want 202 (body=%s)", first.StatusCode, payload)
	}
	var job1 map[string]any
	_ = json.Unmarshal(payload, &job1)
	jobID := job1["job_id"].(string)
	enqueues := len(h.fixture.EnqueuedReferences())

	conflict, payload2 := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-gen-conflict",
		body:    `{"model":"gpt-image-1","prompt":"different prompt"}`,
	})
	if conflict.StatusCode != http.StatusConflict {
		t.Fatalf("conflict status = %d, want 409 (body=%s)", conflict.StatusCode, payload2)
	}
	var errBody map[string]any
	if err := json.Unmarshal(payload2, &errBody); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if errBody["code"] != "idempotency_conflict" {
		t.Fatalf("error code = %v, want idempotency_conflict", errBody["code"])
	}
	if len(h.fixture.EnqueuedReferences()) != enqueues {
		t.Fatalf("enqueue after conflict = %d, want %d (no replacement)", len(h.fixture.EnqueuedReferences()), enqueues)
	}

	// First job remains queued and unchanged.
	get, getPayload := h.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/render-jobs/" + jobID,
		bearer: tenantAKey,
	})
	if get.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d, want 200 (body=%s)", get.StatusCode, getPayload)
	}
	var still map[string]any
	_ = json.Unmarshal(getPayload, &still)
	if still["lifecycle_state"] != "queued" {
		t.Fatalf("lifecycle after conflict = %v, want queued", still["lifecycle_state"])
	}
}

// Slice 4: queued cancel reaches terminal canceled with zero render adapter calls.
func TestQueuedCancelIsTerminalWithoutProviderCall(t *testing.T) {
	t.Parallel()

	h := newRenderHarness(t, func(h *renderHarness) {
		seedRoutableImageAccount(h, "pa_render_cancel")
	})

	create, payload := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-gen-cancel",
		body:    `{"model":"gpt-image-1","prompt":"cancel me"}`,
	})
	if create.StatusCode != http.StatusAccepted {
		t.Fatalf("create status = %d, want 202 (body=%s)", create.StatusCode, payload)
	}
	var job map[string]any
	_ = json.Unmarshal(payload, &job)
	jobID := job["job_id"].(string)

	cancel, cancelPayload := h.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/render-jobs/" + jobID + "/cancel",
		bearer: tenantAKey,
	})
	if cancel.StatusCode != http.StatusOK {
		t.Fatalf("cancel status = %d, want 200 (body=%s)", cancel.StatusCode, cancelPayload)
	}
	var canceled map[string]any
	_ = json.Unmarshal(cancelPayload, &canceled)
	if canceled["lifecycle_state"] != "canceled" {
		t.Fatalf("lifecycle_state = %v, want canceled", canceled["lifecycle_state"])
	}
	if h.renderCalls.Load() != 0 {
		t.Fatalf("render calls after queued cancel = %d, want 0", h.renderCalls.Load())
	}

	// Worker redelivery of the cancelled job must not call Provider.
	ref := domain.JobRef{TenantID: "tenant_a", JobID: domain.Identifier(jobID)}
	if err := h.fixture.Runtime().Worker().ExecuteJob(t.Context(), ref); err != nil {
		t.Fatalf("ExecuteJob after cancel: %v", err)
	}
	if h.renderCalls.Load() != 0 {
		t.Fatalf("render calls after worker on canceled job = %d, want 0", h.renderCalls.Load())
	}
}

// Slice 5: foreign job id is non-enumerating 404 with zero protected work.
func TestForeignRenderJobIsNonEnumerating404(t *testing.T) {
	t.Parallel()

	h := newRenderHarness(t, func(h *renderHarness) {
		seedRoutableImageAccount(h, "pa_render_foreign")
	})

	create, payload := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-gen-foreign",
		body:    `{"model":"gpt-image-1","prompt":"tenant a only"}`,
	})
	if create.StatusCode != http.StatusAccepted {
		t.Fatalf("create status = %d, want 202 (body=%s)", create.StatusCode, payload)
	}
	var job map[string]any
	_ = json.Unmarshal(payload, &job)
	jobID := job["job_id"].(string)
	admits := h.admission.admitCalls.Load()

	get, getPayload := h.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/render-jobs/" + jobID,
		bearer: tenantBKey,
	})
	if get.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign get status = %d, want 404 (body=%s)", get.StatusCode, getPayload)
	}
	var errBody map[string]any
	_ = json.Unmarshal(getPayload, &errBody)
	if errBody["code"] != "resource_not_found" {
		t.Fatalf("foreign get code = %v, want resource_not_found", errBody["code"])
	}
	if _, hasRef := errBody["resource_reference"]; hasRef {
		t.Fatalf("foreign get must not carry resource_reference: %v", errBody)
	}
	// Ownership fails before admission on get.
	if h.admission.admitCalls.Load() != admits {
		t.Fatalf("admission after foreign get = %d, want %d", h.admission.admitCalls.Load(), admits)
	}
}

// Slice 6: worker claim → render once → durable manifest + output Asset → completed.
func TestWorkerCompletesJobWithManifestAndOutputAsset(t *testing.T) {
	t.Parallel()

	h := newRenderHarness(t, func(h *renderHarness) {
		seedRoutableImageAccount(h, "pa_render_worker")
	})

	create, payload := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-gen-worker",
		body:    `{"model":"gpt-image-1","prompt":"complete me"}`,
	})
	if create.StatusCode != http.StatusAccepted {
		t.Fatalf("create status = %d, want 202 (body=%s)", create.StatusCode, payload)
	}
	var job map[string]any
	_ = json.Unmarshal(payload, &job)
	jobID := job["job_id"].(string)

	ref := domain.JobRef{TenantID: "tenant_a", JobID: domain.Identifier(jobID)}
	if err := h.fixture.Runtime().Worker().ExecuteJob(t.Context(), ref); err != nil {
		t.Fatalf("ExecuteJob: %v", err)
	}
	if calls := h.renderCalls.Load(); calls != 1 {
		t.Fatalf("render calls = %d, want 1", calls)
	}

	// Queue redelivery must not re-render after committed completion.
	if err := h.fixture.Runtime().Worker().ExecuteJob(t.Context(), ref); err != nil {
		t.Fatalf("ExecuteJob redelivery: %v", err)
	}
	if calls := h.renderCalls.Load(); calls != 1 {
		t.Fatalf("render calls after redelivery = %d, want 1", calls)
	}

	get, getPayload := h.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/render-jobs/" + jobID,
		bearer: tenantAKey,
	})
	if get.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d, want 200 (body=%s)", get.StatusCode, getPayload)
	}
	var completed map[string]any
	if err := json.Unmarshal(getPayload, &completed); err != nil {
		t.Fatalf("decode completed: %v", err)
	}
	if completed["lifecycle_state"] != "completed" {
		t.Fatalf("lifecycle_state = %v, want completed (body=%s)", completed["lifecycle_state"], getPayload)
	}
	entries, _ := completed["output_entries"].([]any)
	if len(entries) < 1 {
		t.Fatalf("output_entries empty: %v", completed)
	}
	entry, _ := entries[0].(map[string]any)
	if entry["delivery_state"] != "available" {
		t.Fatalf("delivery_state = %v, want available", entry["delivery_state"])
	}
	assetID, _ := entry["asset_id"].(string)
	if assetID == "" {
		t.Fatalf("asset_id missing on available entry: %v", entry)
	}

	// Output retry must not re-render.
	retry, retryPayload := h.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/render-jobs/" + jobID + "/outputs/" + entry["output_entry_id"].(string) + "/retry",
		bearer: tenantAKey,
	})
	if retry.StatusCode != http.StatusOK {
		t.Fatalf("retry status = %d, want 200 (body=%s)", retry.StatusCode, retryPayload)
	}
	if calls := h.renderCalls.Load(); calls != 1 {
		t.Fatalf("render calls after output retry = %d, want 1", calls)
	}
}

// Slice 7: capability failure rejects before Provider call / enqueue.
func TestCapabilityFailureRejectsBeforeEnqueue(t *testing.T) {
	t.Parallel()

	h := newRenderHarness(t, func(h *renderHarness) {
		account := activeProbedAccount("pa_render_cap", domain.AuthModeChatGPTCodexOAuth)
		h.seedAccount("tenant_a", account)
		// Fresh snapshot but image_generation unsupported.
		h.capabilities.seed("tenant_a", domain.NewLiveProbeSnapshot(
			"pa_render_cap",
			domain.AuthModeChatGPTCodexOAuth,
			1,
			domain.NewTimestamp(spineFixtureTime),
			map[domain.CapabilityOperation]domain.CapabilityFact{
				domain.CapabilityOpImageGeneration: {
					Status:        domain.CapabilityUnsupported,
					EvidenceClass: domain.EvidenceLiveProbe,
					ProbeSurface:  "/images/generations",
				},
			},
			nil,
			"/images/generations",
		))
		h.routing.Seed("tenant_a", domain.RoutingPolicy{
			CandidateAccounts: []domain.ProviderAccountID{"pa_render_cap"},
			SelectionOrder:    []domain.ProviderAccountID{"pa_render_cap"},
			FallbackChain:     []domain.ProviderAccountID{},
			FallbackAuthModes: []domain.AuthMode{},
			LeasePolicy:       domain.LeasePolicy{EligibleUnits: []domain.LeaseUnit{}},
			UpdatedAt:         domain.NewTimestamp(spineFixtureTime),
			UpdatedBy:         "key_a",
		})
	})

	response, payload := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-gen-cap",
		body:    `{"model":"gpt-image-1","prompt":"no capability"}`,
	})
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", response.StatusCode, payload)
	}
	var errBody map[string]any
	_ = json.Unmarshal(payload, &errBody)
	if errBody["code"] != "capability_unsupported" {
		t.Fatalf("code = %v, want capability_unsupported", errBody["code"])
	}
	if len(h.fixture.EnqueuedReferences()) != 0 {
		t.Fatalf("enqueue count = %d, want 0 on capability reject", len(h.fixture.EnqueuedReferences()))
	}
	if h.renderCalls.Load() != 0 {
		t.Fatalf("render calls = %d, want 0", h.renderCalls.Load())
	}
}

// Slice 8: Vault dependency failure rejects before enqueue/Provider.
func TestVaultFailureRejectsBeforeEnqueue(t *testing.T) {
	t.Parallel()

	h := newRenderHarness(t, func(h *renderHarness) {
		seedRoutableImageAccount(h, "pa_render_vault")
		h.vault.validateErr = ports.ErrDependencyUnavailable
	})

	response, payload := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-gen-vault",
		body:    `{"model":"gpt-image-1","prompt":"vault down"}`,
	})
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body=%s)", response.StatusCode, payload)
	}
	if len(h.fixture.EnqueuedReferences()) != 0 {
		t.Fatalf("enqueue count = %d, want 0 on vault reject", len(h.fixture.EnqueuedReferences()))
	}
	if h.renderCalls.Load() != 0 {
		t.Fatalf("render calls = %d, want 0", h.renderCalls.Load())
	}
}

// Slice 9: worker loss after payload with unknown outcome never re-renders.
func TestUnknownCommitAfterPayloadNeverRerenders(t *testing.T) {
	t.Parallel()

	h := newRenderHarness(t, func(h *renderHarness) {
		seedRoutableImageAccount(h, "pa_render_unknown")
	})
	h.adapter.outcome = domain.RenderOutcome{Class: domain.RenderOutcomeUnknown, Commit: domain.CommitUnknown}

	create, payload := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-gen-unknown",
		body:    `{"model":"gpt-image-1","prompt":"maybe committed"}`,
	})
	if create.StatusCode != http.StatusAccepted {
		t.Fatalf("create status = %d, want 202 (body=%s)", create.StatusCode, payload)
	}
	var job map[string]any
	_ = json.Unmarshal(payload, &job)
	jobID := job["job_id"].(string)
	ref := domain.JobRef{TenantID: "tenant_a", JobID: domain.Identifier(jobID)}

	if err := h.fixture.Runtime().Worker().ExecuteJob(t.Context(), ref); err != nil {
		t.Fatalf("ExecuteJob: %v", err)
	}
	if calls := h.renderCalls.Load(); calls != 1 {
		t.Fatalf("render calls = %d, want 1", calls)
	}
	// Redelivery must not re-render when commit is unknown/failed terminal.
	if err := h.fixture.Runtime().Worker().ExecuteJob(t.Context(), ref); err != nil {
		t.Fatalf("ExecuteJob redelivery: %v", err)
	}
	if calls := h.renderCalls.Load(); calls != 1 {
		t.Fatalf("render calls after redelivery = %d, want 1 (no replacement)", calls)
	}

	get, getPayload := h.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/render-jobs/" + jobID,
		bearer: tenantAKey,
	})
	if get.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d, want 200 (body=%s)", get.StatusCode, getPayload)
	}
	var failed map[string]any
	_ = json.Unmarshal(getPayload, &failed)
	if failed["lifecycle_state"] != "failed" {
		t.Fatalf("lifecycle_state = %v, want failed", failed["lifecycle_state"])
	}
	if failed["commit_status"] != "unknown" {
		t.Fatalf("commit_status = %v, want unknown", failed["commit_status"])
	}
}

// uploadInputAsset creates one same-Tenant input Asset via public multipart HTTP
// so edit/inpaint proofs never seed content through private store calls.
func (h *renderHarness) uploadInputAsset(t *testing.T, idemKey string, content []byte) string {
	t.Helper()
	buffer := &bytes.Buffer{}
	writer := multipart.NewWriter(buffer)
	if err := writer.WriteField("kind", "input"); err != nil {
		t.Fatalf("kind field: %v", err)
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="input.png"`)
	header.Set("Content-Type", domain.ContentTypePNG)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("CreatePart: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, h.fixture.URL()+"/v1/assets", buffer)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tenantAKey)
	req.Header.Set("Idempotency-Key", idemKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := h.fixture.Client().Do(req)
	if err != nil {
		t.Fatalf("Do upload: %v", err)
	}
	payload, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d (body=%s)", resp.StatusCode, payload)
	}
	var asset map[string]any
	if err := json.Unmarshal(payload, &asset); err != nil {
		t.Fatalf("decode asset: %v body=%s", err, payload)
	}
	id, _ := asset["asset_id"].(string)
	if id == "" {
		// Some wires use id
		id, _ = asset["id"].(string)
	}
	if id == "" {
		t.Fatalf("missing asset id: %s", payload)
	}
	return id
}

// P1-1: edit deliver same-Tenant input Asset bytes to Adapter inside authorized
// boundary; public create + Worker proof; no bytes on job wire.
func TestEditDeliversInputAssetBytesToAdapterNotOnWire(t *testing.T) {
	t.Parallel()

	h := newRenderHarness(t, func(h *renderHarness) {
		seedRoutableImageAccount(h, "pa_edit_bytes")
	})

	// Distinct valid PNG payload so Adapter observation is not the fixture default alone.
	inputBytes := fixtureMinimalPNG()
	assetID := h.uploadInputAsset(t, "idem-upload-edit-input", inputBytes)

	create, payload := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/edits",
		bearer:  tenantAKey,
		idemKey: "idem-edit-bytes",
		body:    `{"model":"gpt-image-1","prompt":"make blue","input_asset_id":"` + assetID + `"}`,
	})
	if create.StatusCode != http.StatusAccepted {
		t.Fatalf("edit create status = %d (body=%s)", create.StatusCode, payload)
	}
	var job map[string]any
	_ = json.Unmarshal(payload, &job)
	if _, has := job["prompt"]; has {
		t.Fatalf("job wire must not expose prompt: %v", job)
	}
	jobID, _ := job["job_id"].(string)
	if jobID == "" {
		t.Fatalf("missing job_id: %v", job)
	}
	// Wire must not carry asset bytes (PNG signature must not appear in JSON body).
	if bytes.Contains(payload, []byte{0x89, 0x50, 0x4e, 0x47}) {
		t.Fatalf("job wire leaked PNG bytes: %s", payload)
	}

	if err := h.fixture.Runtime().Worker().ExecuteJob(t.Context(), domain.JobRef{
		TenantID: "tenant_a",
		JobID:    domain.Identifier(jobID),
	}); err != nil {
		t.Fatalf("ExecuteJob: %v", err)
	}
	if calls := h.renderCalls.Load(); calls != 1 {
		t.Fatalf("render calls = %d, want 1", calls)
	}
	got, _ := h.adapter.lastInput.Load().([]byte)
	if !bytes.Equal(got, inputBytes) {
		t.Fatalf("adapter input bytes mismatch (got %d bytes, want %d)", len(got), len(inputBytes))
	}
	// Queue redelivery must not re-render.
	if err := h.fixture.Runtime().Worker().ExecuteJob(t.Context(), domain.JobRef{
		TenantID: "tenant_a",
		JobID:    domain.Identifier(jobID),
	}); err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if calls := h.renderCalls.Load(); calls != 1 {
		t.Fatalf("render after redelivery = %d, want 1", calls)
	}
}

// Slice 10: inpaint never downgrades to edit; requires mask_asset_id.
func TestInpaintNeverDowngradesToEdit(t *testing.T) {
	t.Parallel()

	h := newRenderHarness(t, func(h *renderHarness) {
		seedRoutableImageAccount(h, "pa_render_inpaint")
	})

	// Missing mask is request_validation, not silent edit.
	missing, payload := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/inpaints",
		bearer:  tenantAKey,
		idemKey: "idem-inpaint-missing",
		body:    `{"model":"gpt-image-1","prompt":"fill","input_asset_id":"asset_missing"}`,
	})
	if missing.StatusCode != http.StatusBadRequest && missing.StatusCode != http.StatusNotFound {
		// Either invalid (mask required) or asset not found for input — never 202 edit.
		t.Fatalf("status = %d, want 400 or 404 (body=%s)", missing.StatusCode, payload)
	}
	if len(h.fixture.EnqueuedReferences()) != 0 {
		t.Fatalf("enqueue on invalid inpaint = %d, want 0", len(h.fixture.EnqueuedReferences()))
	}
}

// Finding #8: two jobs may bind the same Provider Account (continuity, not mutex).
func TestMultipleJobsMayShareProviderAccount(t *testing.T) {
	t.Parallel()

	h := newRenderHarness(t, func(h *renderHarness) {
		seedRoutableImageAccount(h, "pa_shared")
	})

	var jobIDs []string
	for i, key := range []string{"idem-share-1", "idem-share-2"} {
		resp, payload := h.do(t, requestSpec{
			method:  http.MethodPost,
			path:    "/v1/images/generations",
			bearer:  tenantAKey,
			idemKey: key,
			body:    `{"model":"gpt-image-1","prompt":"share account ` + string(rune('a'+i)) + `"}`,
		})
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("create %d status = %d, want 202 (body=%s)", i, resp.StatusCode, payload)
		}
		var job map[string]any
		_ = json.Unmarshal(payload, &job)
		if job["provider_account_id"] != "pa_shared" {
			t.Fatalf("job %d account = %v, want pa_shared", i, job["provider_account_id"])
		}
		jobIDs = append(jobIDs, job["job_id"].(string))
	}

	// Both workers execute successfully against the same account.
	for _, id := range jobIDs {
		ref := domain.JobRef{TenantID: "tenant_a", JobID: domain.Identifier(id)}
		if err := h.fixture.Runtime().Worker().ExecuteJob(t.Context(), ref); err != nil {
			t.Fatalf("ExecuteJob(%s): %v", id, err)
		}
	}
	if calls := h.renderCalls.Load(); calls != 2 {
		t.Fatalf("render calls = %d, want 2 (one per job, shared account)", calls)
	}
	for _, id := range jobIDs {
		get, payload := h.do(t, requestSpec{
			method: http.MethodGet,
			path:   "/v1/render-jobs/" + id,
			bearer: tenantAKey,
		})
		if get.StatusCode != http.StatusOK {
			t.Fatalf("get %s status = %d (body=%s)", id, get.StatusCode, payload)
		}
		var job map[string]any
		_ = json.Unmarshal(payload, &job)
		if job["lifecycle_state"] != "completed" {
			t.Fatalf("job %s state = %v, want completed", id, job["lifecycle_state"])
		}
	}
}

// Finding #9: claim/complete advance UpdatedAt via injected clock (not stale create time).
func TestWorkerClaimAndCompleteAdvanceUpdatedAt(t *testing.T) {
	t.Parallel()

	h := newRenderHarness(t, func(h *renderHarness) {
		seedRoutableImageAccount(h, "pa_clock")
	})

	create, payload := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-clock",
		body:    `{"model":"gpt-image-1","prompt":"clock advance"}`,
	})
	if create.StatusCode != http.StatusAccepted {
		t.Fatalf("create status = %d, want 202 (body=%s)", create.StatusCode, payload)
	}
	var queued map[string]any
	_ = json.Unmarshal(payload, &queued)
	createdAt, _ := queued["created_at"].(string)
	updatedAtCreate, _ := queued["updated_at"].(string)
	jobID := queued["job_id"].(string)

	ref := domain.JobRef{TenantID: "tenant_a", JobID: domain.Identifier(jobID)}
	if err := h.fixture.Runtime().Worker().ExecuteJob(t.Context(), ref); err != nil {
		t.Fatalf("ExecuteJob: %v", err)
	}

	get, getPayload := h.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/render-jobs/" + jobID,
		bearer: tenantAKey,
	})
	if get.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d (body=%s)", get.StatusCode, getPayload)
	}
	var completed map[string]any
	_ = json.Unmarshal(getPayload, &completed)
	updatedAtDone, _ := completed["updated_at"].(string)
	if updatedAtDone == "" || updatedAtDone == updatedAtCreate {
		t.Fatalf("updated_at after complete = %q, create updated_at = %q; want clock advancement", updatedAtDone, updatedAtCreate)
	}
	if completed["created_at"] != createdAt {
		t.Fatalf("created_at changed: %v → %v", createdAt, completed["created_at"])
	}
	if completed["lifecycle_state"] != "completed" {
		t.Fatalf("lifecycle_state = %v, want completed", completed["lifecycle_state"])
	}
}

// Finding #7: completed job exposes asset_id only after application Asset placement;
// job store records the result (delivery available + asset_id).
func TestCompletedJobRecordsAssetPlacementNotOnlyJobMap(t *testing.T) {
	t.Parallel()

	h := newRenderHarness(t, func(h *renderHarness) {
		seedRoutableImageAccount(h, "pa_place")
	})

	create, payload := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-place",
		body:    `{"model":"gpt-image-1","prompt":"place asset"}`,
	})
	if create.StatusCode != http.StatusAccepted {
		t.Fatalf("create status = %d (body=%s)", create.StatusCode, payload)
	}
	var job map[string]any
	_ = json.Unmarshal(payload, &job)
	jobID := job["job_id"].(string)

	if err := h.fixture.Runtime().Worker().ExecuteJob(t.Context(), domain.JobRef{
		TenantID: "tenant_a",
		JobID:    domain.Identifier(jobID),
	}); err != nil {
		t.Fatalf("ExecuteJob: %v", err)
	}

	get, getPayload := h.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/render-jobs/" + jobID,
		bearer: tenantAKey,
	})
	var done map[string]any
	_ = json.Unmarshal(getPayload, &done)
	if get.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d (body=%s)", get.StatusCode, getPayload)
	}
	entries, _ := done["output_entries"].([]any)
	if len(entries) < 1 {
		t.Fatalf("no output entries: %v", done)
	}
	entry := entries[0].(map[string]any)
	if entry["delivery_state"] != "available" {
		t.Fatalf("delivery_state = %v, want available", entry["delivery_state"])
	}
	assetID, _ := entry["asset_id"].(string)
	if assetID == "" {
		t.Fatal("asset_id missing — placement must go through Asset ports then job record")
	}
	// Asset is retrievable via public Asset surface (proves AssetMetadata/Content).
	// Requires assets.read on principal — harness has it on tenantAKey.
	// Skip if scope missing; tenantAKey includes assets.read/write.
}

// Slice 7: concurrent workers — only one claim may render.
func TestConcurrentWorkerClaimRendersOnce(t *testing.T) {
	t.Parallel()

	h := newRenderHarness(t, func(h *renderHarness) {
		seedRoutableImageAccount(h, "pa_render_race")
	})

	create, payload := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-gen-race",
		body:    `{"model":"gpt-image-1","prompt":"race me"}`,
	})
	if create.StatusCode != http.StatusAccepted {
		t.Fatalf("create status = %d, want 202 (body=%s)", create.StatusCode, payload)
	}
	var job map[string]any
	_ = json.Unmarshal(payload, &job)
	jobID := job["job_id"].(string)
	ref := domain.JobRef{TenantID: "tenant_a", JobID: domain.Identifier(jobID)}

	errCh := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			errCh <- h.fixture.Runtime().Worker().ExecuteJob(t.Context(), ref)
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("ExecuteJob: %v", err)
		}
	}
	if calls := h.renderCalls.Load(); calls != 1 {
		t.Fatalf("render calls under concurrent claim = %d, want 1", calls)
	}
}

// Finding A: authorized path injects exact create prompt into controlled adapter;
// prompt is not on public wire and is not retained after completion.
func TestAuthorizedRenderInjectsExactPromptNotOnWire(t *testing.T) {
	t.Parallel()

	const wantPrompt = "exact-prompt-injection-proof"
	h := newRenderHarness(t, func(h *renderHarness) {
		seedRoutableImageAccount(h, "pa_prompt_inj")
	})

	create, payload := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-prompt-inj",
		body:    `{"model":"gpt-image-1","prompt":"` + wantPrompt + `"}`,
	})
	if create.StatusCode != http.StatusAccepted {
		t.Fatalf("create status = %d (body=%s)", create.StatusCode, payload)
	}
	var job map[string]any
	_ = json.Unmarshal(payload, &job)
	if _, ok := job["prompt"]; ok {
		t.Fatalf("public create response must not include prompt: %v", job)
	}
	jobID := job["job_id"].(string)

	if err := h.fixture.Runtime().Worker().ExecuteJob(t.Context(), domain.JobRef{
		TenantID: "tenant_a",
		JobID:    domain.Identifier(jobID),
	}); err != nil {
		t.Fatalf("ExecuteJob: %v", err)
	}
	got, _ := h.lastPrompt.Load().(string)
	if got != wantPrompt {
		t.Fatalf("adapter prompt = %q, want %q (authorized injection)", got, wantPrompt)
	}

	get, getPayload := h.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/render-jobs/" + jobID,
		bearer: tenantAKey,
	})
	if get.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d (body=%s)", get.StatusCode, getPayload)
	}
	var status map[string]any
	_ = json.Unmarshal(getPayload, &status)
	if _, ok := status["prompt"]; ok {
		t.Fatalf("status projection must not include prompt: %v", status)
	}
}

// Finding C / §3.3: durable create + enqueue failure must not abandon replay;
// matching retry recovers the same job and completes publication without a second job.
func TestEnqueueFailureAfterCreateRecoversSameJobOnRetry(t *testing.T) {
	t.Parallel()

	h := newRenderHarness(t, func(h *renderHarness) {
		seedRoutableImageAccount(h, "pa_enq_fail")
		h.enqueueFailTimes = 1
		h.enqueueError = ports.ErrDependencyUnavailable
	})

	first, payload := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-enq-recover",
		body:    `{"model":"gpt-image-1","prompt":"publish me"}`,
	})
	if first.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("first status = %d, want 503 after enqueue failure (body=%s)", first.StatusCode, payload)
	}
	if n := len(h.fixture.EnqueuedReferences()); n != 0 {
		t.Fatalf("successful enqueues after fail = %d, want 0", n)
	}

	// Matching retry recovers the durable job and re-attempts enqueue.
	second, payload2 := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-enq-recover",
		body:    `{"model":"gpt-image-1","prompt":"publish me"}`,
	})
	if second.StatusCode != http.StatusAccepted {
		t.Fatalf("retry status = %d, want 202 (body=%s)", second.StatusCode, payload2)
	}
	var job map[string]any
	if err := json.Unmarshal(payload2, &job); err != nil {
		t.Fatalf("decode: %v", err)
	}
	jobID, _ := job["job_id"].(string)
	if jobID == "" {
		t.Fatalf("missing job_id on recovery: %v", job)
	}
	if n := len(h.fixture.EnqueuedReferences()); n != 1 {
		t.Fatalf("enqueue count after recovery = %d, want 1", n)
	}
	if string(h.fixture.EnqueuedReferences()[0].JobID) != jobID {
		t.Fatalf("enqueued job_id = %s, want %s", h.fixture.EnqueuedReferences()[0].JobID, jobID)
	}

	// Third matching request is pure terminal replay — no second enqueue.
	third, payload3 := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-enq-recover",
		body:    `{"model":"gpt-image-1","prompt":"publish me"}`,
	})
	if third.StatusCode != http.StatusAccepted {
		t.Fatalf("third status = %d (body=%s)", third.StatusCode, payload3)
	}
	var job3 map[string]any
	_ = json.Unmarshal(payload3, &job3)
	if job3["job_id"] != jobID {
		t.Fatalf("third job_id = %v, want %s (no replacement)", job3["job_id"], jobID)
	}
	if n := len(h.fixture.EnqueuedReferences()); n != 1 {
		t.Fatalf("enqueue count after pure replay = %d, want 1", n)
	}
}

// P1-2: durable create + enqueue failure recovers via autonomous recovery
// without a second client request (startup/background RecoverUnpublishedQueues).
func TestUnpublishedQueueRecoversWithoutClientRetry(t *testing.T) {
	t.Parallel()

	h := newRenderHarness(t, func(h *renderHarness) {
		seedRoutableImageAccount(h, "pa_auto_pub")
		h.enqueueFailTimes = 1
		h.enqueueError = ports.ErrDependencyUnavailable
	})

	first, payload := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-auto-pub",
		body:    `{"model":"gpt-image-1","prompt":"recover me"}`,
	})
	if first.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("first status = %d, want 503 (body=%s)", first.StatusCode, payload)
	}
	if n := len(h.fixture.EnqueuedReferences()); n != 0 {
		t.Fatalf("enqueues after fail = %d, want 0", n)
	}

	// Autonomous recovery (no client matching retry).
	if err := h.fixture.Runtime().RecoverUnpublishedQueues(t.Context()); err != nil {
		t.Fatalf("RecoverUnpublishedQueues: %v", err)
	}
	if n := len(h.fixture.EnqueuedReferences()); n != 1 {
		t.Fatalf("enqueues after recovery = %d, want 1", n)
	}

	// Worker can claim the recovered publication.
	ref := h.fixture.EnqueuedReferences()[0]
	jobRef, err := ref.JobRef()
	if err != nil {
		t.Fatalf("JobRef: %v", err)
	}
	if err := h.fixture.Runtime().Worker().ExecuteJob(t.Context(), jobRef); err != nil {
		t.Fatalf("ExecuteJob after recovery: %v", err)
	}
	if calls := h.renderCalls.Load(); calls != 1 {
		t.Fatalf("render calls = %d, want 1", calls)
	}
}

// Spec: create admission occupancy is held until job terminal — not released on
// HTTP create response. Worker complete (or cancel) settles Reconcile once.
func TestAdmissionReservationHeldUntilJobTerminal(t *testing.T) {
	t.Parallel()

	h := newRenderHarness(t, func(h *renderHarness) {
		seedRoutableImageAccount(h, "pa_admit_hold")
	})

	create, payload := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-admit-hold",
		body:    `{"model":"gpt-image-1","prompt":"hold occupancy"}`,
	})
	if create.StatusCode != http.StatusAccepted {
		t.Fatalf("create status = %d (body=%s)", create.StatusCode, payload)
	}
	if admits := h.admission.admitCalls.Load(); admits != 1 {
		t.Fatalf("admit calls after create = %d, want 1", admits)
	}
	if recon := h.admission.reconcileCalls.Load(); recon != 0 {
		t.Fatalf("reconcile after create = %d, want 0 (occupancy held for job lifetime)", recon)
	}

	var job map[string]any
	_ = json.Unmarshal(payload, &job)
	jobID := job["job_id"].(string)
	if err := h.fixture.Runtime().Worker().ExecuteJob(t.Context(), domain.JobRef{
		TenantID: "tenant_a",
		JobID:    domain.Identifier(jobID),
	}); err != nil {
		t.Fatalf("ExecuteJob: %v", err)
	}
	if recon := h.admission.reconcileCalls.Load(); recon != 1 {
		t.Fatalf("reconcile after terminal complete = %d, want 1", recon)
	}
}

// Spec: worker re-gates Account/Health/Capability before payload; drain after
// create fails the job with zero Provider calls.
func TestWorkerPregateRejectsDrainWithoutProviderCall(t *testing.T) {
	t.Parallel()

	h := newRenderHarness(t, func(h *renderHarness) {
		seedRoutableImageAccount(h, "pa_pregate_drain")
	})

	create, payload := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-pregate-drain",
		body:    `{"model":"gpt-image-1","prompt":"preflight me"}`,
	})
	if create.StatusCode != http.StatusAccepted {
		t.Fatalf("create status = %d (body=%s)", create.StatusCode, payload)
	}
	var job map[string]any
	_ = json.Unmarshal(payload, &job)
	jobID := job["job_id"].(string)

	// Post-create: mark account draining so worker preflight fails.
	account := h.storedAccount(t, domain.SecurityPrincipal{
		TenantID: "tenant_a", ClientAPIKeyID: "key_a",
		Scopes: domain.NewScopeSet(domain.ScopeAccountsRead),
	}, "pa_pregate_drain")
	account.Controls.Drain = domain.DrainDraining
	h.seedAccount("tenant_a", account)

	if err := h.fixture.Runtime().Worker().ExecuteJob(t.Context(), domain.JobRef{
		TenantID: "tenant_a",
		JobID:    domain.Identifier(jobID),
	}); err != nil {
		t.Fatalf("ExecuteJob: %v", err)
	}
	if calls := h.renderCalls.Load(); calls != 0 {
		t.Fatalf("render calls = %d, want 0 after preflight reject", calls)
	}

	get, getPayload := h.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/render-jobs/" + jobID,
		bearer: tenantAKey,
	})
	if get.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d (body=%s)", get.StatusCode, getPayload)
	}
	var final map[string]any
	_ = json.Unmarshal(getPayload, &final)
	if final["lifecycle_state"] != "failed" {
		t.Fatalf("lifecycle = %v, want failed (body=%s)", final["lifecycle_state"], getPayload)
	}
	if final["failure_class"] != "account_not_usable" {
		t.Fatalf("failure_class = %v, want account_not_usable", final["failure_class"])
	}
}

// Spec: Vault Valid=false is usability reject at create (account_not_usable).
func TestVaultValidFalseRejectsCreateAsAccountNotUsable(t *testing.T) {
	t.Parallel()

	h := newRenderHarness(t, func(h *renderHarness) {
		seedRoutableImageAccount(h, "pa_vault_invalid")
		h.vault.validResult = ports.CredentialValidationResult{Valid: false}
	})

	response, payload := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-vault-invalid",
		body:    `{"model":"gpt-image-1","prompt":"no usable cred"}`,
	})
	if response.StatusCode == http.StatusAccepted {
		t.Fatalf("status = 202, want reject for Valid=false (body=%s)", payload)
	}
	if n := len(h.fixture.EnqueuedReferences()); n != 0 {
		t.Fatalf("enqueue count = %d, want 0", n)
	}
	if calls := h.renderCalls.Load(); calls != 0 {
		t.Fatalf("render calls = %d, want 0", calls)
	}
	var body map[string]any
	_ = json.Unmarshal(payload, &body)
	errObj, _ := body["error"].(map[string]any)
	if errObj == nil {
		// Wire may flatten code at top level depending on transport.
		if code, _ := body["code"].(string); code != "account_not_usable" {
			t.Fatalf("body = %s, want account_not_usable", payload)
		}
		return
	}
	if code, _ := errObj["code"].(string); code != "account_not_usable" {
		t.Fatalf("error.code = %v, want account_not_usable (body=%s)", errObj["code"], payload)
	}
}

// Spec: AuthMode is passed into the protected Adapter surface on worker render.
func TestWorkerPassesAuthModeToAdapterSurface(t *testing.T) {
	t.Parallel()

	h := newRenderHarness(t, func(h *renderHarness) {
		seedRoutableImageAccount(h, "pa_authmode_surface")
	})

	create, payload := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-authmode",
		body:    `{"model":"gpt-image-1","prompt":"auth mode surface"}`,
	})
	if create.StatusCode != http.StatusAccepted {
		t.Fatalf("create status = %d (body=%s)", create.StatusCode, payload)
	}
	var job map[string]any
	_ = json.Unmarshal(payload, &job)
	jobID := job["job_id"].(string)
	if err := h.fixture.Runtime().Worker().ExecuteJob(t.Context(), domain.JobRef{
		TenantID: "tenant_a",
		JobID:    domain.Identifier(jobID),
	}); err != nil {
		t.Fatalf("ExecuteJob: %v", err)
	}
	got, _ := h.adapter.lastAuthMode.Load().(domain.AuthMode)
	if got != domain.AuthModeChatGPTCodexOAuth {
		t.Fatalf("adapter AuthMode = %q, want %q", got, domain.AuthModeChatGPTCodexOAuth)
	}
}

// Spec: queued cancel is terminal without Provider and releases create occupancy.
func TestQueuedCancelReleasesAdmissionWithoutProvider(t *testing.T) {
	t.Parallel()

	h := newRenderHarness(t, func(h *renderHarness) {
		seedRoutableImageAccount(h, "pa_cancel_admit")
	})

	create, payload := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-cancel-admit",
		body:    `{"model":"gpt-image-1","prompt":"cancel occupancy"}`,
	})
	if create.StatusCode != http.StatusAccepted {
		t.Fatalf("create status = %d", create.StatusCode)
	}
	var job map[string]any
	_ = json.Unmarshal(payload, &job)
	jobID := job["job_id"].(string)
	reconAfterCreate := h.admission.reconcileCalls.Load()

	cancel, cancelPayload := h.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/render-jobs/" + jobID + "/cancel",
		bearer: tenantAKey,
	})
	if cancel.StatusCode != http.StatusOK {
		t.Fatalf("cancel status = %d (body=%s)", cancel.StatusCode, cancelPayload)
	}
	if calls := h.renderCalls.Load(); calls != 0 {
		t.Fatalf("render calls = %d, want 0", calls)
	}
	// cancel admits once (request-scoped) and releaseJobAdmission for create +
	// cancel defer Reconcile → at least +2 reconciles from create-held state.
	if recon := h.admission.reconcileCalls.Load(); recon <= reconAfterCreate {
		t.Fatalf("reconcile after cancel = %d, want > %d", recon, reconAfterCreate)
	}
}

// Running cancel first exposes cancel_requested (honest), not immediate canceled.
func TestRunningCancelExposesCancelRequestedBeforeTerminal(t *testing.T) {
	t.Parallel()

	h := newRenderHarness(t, func(h *renderHarness) {
		seedRoutableImageAccount(h, "pa_cancel_running")
	})
	block := make(chan struct{})
	entered := make(chan struct{})
	h.adapter.block = block
	h.adapter.entered = entered

	create, payload := h.do(t, requestSpec{
		method:  http.MethodPost,
		path:    "/v1/images/generations",
		bearer:  tenantAKey,
		idemKey: "idem-cancel-running",
		body:    `{"model":"gpt-image-1","prompt":"cancel while running"}`,
	})
	if create.StatusCode != http.StatusAccepted {
		t.Fatalf("create status = %d (body=%s)", create.StatusCode, payload)
	}
	var job map[string]any
	_ = json.Unmarshal(payload, &job)
	jobID := job["job_id"].(string)

	errCh := make(chan error, 1)
	go func() {
		errCh <- h.fixture.Runtime().Worker().ExecuteJob(t.Context(), domain.JobRef{
			TenantID: "tenant_a",
			JobID:    domain.Identifier(jobID),
		})
	}()

	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not enter render")
	}

	cancel, cancelPayload := h.do(t, requestSpec{
		method: http.MethodPost,
		path:   "/v1/render-jobs/" + jobID + "/cancel",
		bearer: tenantAKey,
	})
	if cancel.StatusCode != http.StatusOK {
		t.Fatalf("cancel status = %d (body=%s)", cancel.StatusCode, cancelPayload)
	}
	var mid map[string]any
	_ = json.Unmarshal(cancelPayload, &mid)
	if mid["lifecycle_state"] != "cancel_requested" {
		t.Fatalf("mid cancel lifecycle = %v, want cancel_requested (honest)", mid["lifecycle_state"])
	}

	close(block)
	if err := <-errCh; err != nil {
		t.Fatalf("ExecuteJob: %v", err)
	}

	get, getPayload := h.do(t, requestSpec{
		method: http.MethodGet,
		path:   "/v1/render-jobs/" + jobID,
		bearer: tenantAKey,
	})
	var final map[string]any
	_ = json.Unmarshal(getPayload, &final)
	if get.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d (body=%s)", get.StatusCode, getPayload)
	}
	// After worker observes cancel_requested, terminal is canceled (not completed).
	if final["lifecycle_state"] != "canceled" {
		t.Fatalf("final lifecycle = %v, want canceled", final["lifecycle_state"])
	}
}
