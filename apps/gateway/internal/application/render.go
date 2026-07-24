package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// Operation tokens for the Render Job spine.
const (
	operationCreateImageGeneration domain.OperationToken = "create_image_generation"
	operationCreateImageEdit       domain.OperationToken = "create_image_edit"
	operationCreateImageInpaint    domain.OperationToken = "create_image_inpaint"
	operationGetRenderJob          domain.OperationToken = "get_render_job"
	operationCancelRenderJob       domain.OperationToken = "cancel_render_job"
	operationRetryRenderJobOutput  domain.OperationToken = "retry_render_job_output"
	operationExecuteRenderJob      domain.OperationToken = "execute_render_job"
)

// CreateImageGenerationCommand is the typed generation create request.
type CreateImageGenerationCommand struct {
	PresentedKeyMaterial string
	RequestID            domain.Identifier
	Model                string
	Prompt               string
	IdempotencyKey       string
	OversizeBody         bool
	MalformedBody        bool
}

// CreateImageEditCommand is the typed edit create request.
type CreateImageEditCommand struct {
	PresentedKeyMaterial string
	RequestID            domain.Identifier
	Model                string
	Prompt               string
	InputAssetID         domain.AssetID
	IdempotencyKey       string
	OversizeBody         bool
	MalformedBody        bool
}

// CreateImageInpaintCommand is the typed inpaint create request.
type CreateImageInpaintCommand struct {
	PresentedKeyMaterial string
	RequestID            domain.Identifier
	Model                string
	Prompt               string
	InputAssetID         domain.AssetID
	MaskAssetID          domain.AssetID
	IdempotencyKey       string
	OversizeBody         bool
	MalformedBody        bool
}

// GetRenderJobQuery is the typed job status read.
type GetRenderJobQuery struct {
	PresentedKeyMaterial string
	RequestID            domain.Identifier
	JobID                domain.Identifier
}

// CancelRenderJobCommand is the typed cancel request.
type CancelRenderJobCommand struct {
	PresentedKeyMaterial string
	RequestID            domain.Identifier
	JobID                domain.Identifier
}

// RetryRenderJobOutputCommand is the typed output-delivery retry request.
// re_render is always false on the Public API surface (#14 §8.4).
type RetryRenderJobOutputCommand struct {
	PresentedKeyMaterial string
	RequestID            domain.Identifier
	JobID                domain.Identifier
	OutputEntryID        domain.OutputEntryID
}

// RenderJobResult carries one safe job projection plus the server-owned request id.
type RenderJobResult struct {
	Job       domain.RenderJob
	RequestID domain.Identifier
}

// OutputDeliveryResult carries the job after an output-delivery action.
type OutputDeliveryResult struct {
	Job       domain.RenderJob
	Entry     domain.OutputEntry
	RequestID domain.Identifier
}

// RenderService runs the protected Public API Render Job spine and the exported
// JobExecutor worker seam. It owns gate order, routing C0–C5 / P0–P5, attempt
// commit certainty, fencing, and capture-before-complete (#14, #11, ADR 0009).
type RenderService struct {
	principal    ports.PrincipalStore
	admission    ports.AdmissionStore
	replay       ports.RenderReplayStore
	jobs         ports.RenderJobStore
	accounts     ports.AccountStore
	health       ports.HealthStore
	capabilities ports.CapabilityStore
	circuits     ports.CircuitStore
	routing      ports.RoutingPolicyStore
	assets       ports.AssetMetadataStore
	content      ports.AssetContentStore
	staging      ports.RenderStagingStore
	vault        ports.CredentialVault
	prompts      ports.RenderPromptStore
	authorized   ports.AuthorizedRender
	queue        ports.JobRuntime
	audit        ports.RenderAuditRecorder
	telemetry    ports.TelemetryRecorder
	requestLog   ports.RequestLogRecorder
	clock        ports.Clock
	ids          ports.IDGenerator
}

// RenderDependencies bundles the controlled ports this slice owns.
type RenderDependencies struct {
	Principal    ports.PrincipalStore
	Admission    ports.AdmissionStore
	Replay       ports.RenderReplayStore
	Jobs         ports.RenderJobStore
	Accounts     ports.AccountStore
	Health       ports.HealthStore
	Capabilities ports.CapabilityStore
	Circuits     ports.CircuitStore
	Routing      ports.RoutingPolicyStore
	Assets       ports.AssetMetadataStore
	Content      ports.AssetContentStore
	// Staging holds temporary Provider result bytes for capture/placement retry.
	// Required; production fail-closed default lives in composition.
	Staging ports.RenderStagingStore
	Vault   ports.CredentialVault
	// Prompts is the Put-only confidential port for create-time prompt intake.
	Prompts ports.RenderPromptStore
	// Authorized is the protected render boundary (Vault + confidential + Adapter).
	// Application never hands prompt/credential plaintext to an ordinary Adapter.
	Authorized ports.AuthorizedRender
	Queue      ports.JobRuntime
	Audit      ports.RenderAuditRecorder
	Telemetry  ports.TelemetryRecorder
	RequestLog ports.RequestLogRecorder
	Clock      ports.Clock
	IDs        ports.IDGenerator
}

// NewRenderService validates and wires the Render Job spine dependencies.
func NewRenderService(dependencies RenderDependencies) (*RenderService, error) {
	switch {
	case dependencies.Principal == nil:
		return nil, errors.New("application: principal store is required")
	case dependencies.Admission == nil:
		return nil, errors.New("application: admission store is required")
	case dependencies.Replay == nil:
		return nil, errors.New("application: render replay store is required")
	case dependencies.Jobs == nil:
		return nil, errors.New("application: render job store is required")
	case dependencies.Accounts == nil:
		return nil, errors.New("application: account store is required")
	case dependencies.Capabilities == nil:
		return nil, errors.New("application: capability store is required")
	case dependencies.Routing == nil:
		return nil, errors.New("application: routing policy store is required")
	case dependencies.Assets == nil:
		return nil, errors.New("application: asset metadata store is required")
	case dependencies.Content == nil:
		return nil, errors.New("application: asset content store is required")
	case dependencies.Staging == nil:
		return nil, errors.New("application: render staging store is required")
	case dependencies.Vault == nil:
		return nil, errors.New("application: credential vault is required")
	case dependencies.Prompts == nil:
		return nil, errors.New("application: render prompt store is required")
	case dependencies.Authorized == nil:
		return nil, errors.New("application: authorized render port is required")
	case dependencies.Queue == nil:
		return nil, errors.New("application: job runtime is required")
	case dependencies.Audit == nil:
		return nil, errors.New("application: render audit recorder is required")
	case dependencies.Telemetry == nil:
		return nil, errors.New("application: telemetry recorder is required")
	case dependencies.RequestLog == nil:
		return nil, errors.New("application: request log recorder is required")
	case dependencies.Clock == nil:
		return nil, errors.New("application: clock is required")
	case dependencies.IDs == nil:
		return nil, errors.New("application: ID generator is required")
	}
	return &RenderService{
		principal:    dependencies.Principal,
		admission:    dependencies.Admission,
		replay:       dependencies.Replay,
		jobs:         dependencies.Jobs,
		accounts:     dependencies.Accounts,
		health:       dependencies.Health,
		capabilities: dependencies.Capabilities,
		circuits:     dependencies.Circuits,
		routing:      dependencies.Routing,
		assets:       dependencies.Assets,
		content:      dependencies.Content,
		staging:      dependencies.Staging,
		vault:        dependencies.Vault,
		prompts:      dependencies.Prompts,
		authorized:   dependencies.Authorized,
		queue:        dependencies.Queue,
		audit:        dependencies.Audit,
		telemetry:    dependencies.Telemetry,
		requestLog:   dependencies.RequestLog,
		clock:        dependencies.Clock,
		ids:          dependencies.IDs,
	}, nil
}

// CreateImageGeneration creates one durable generation job after full gates.
func (service *RenderService) CreateImageGeneration(ctx context.Context, command CreateImageGenerationCommand) (RenderJobResult, error) {
	return service.create(ctx, createRequest{
		presented:      command.PresentedKeyMaterial,
		requestID:      command.RequestID,
		operation:      domain.RenderOpImageGeneration,
		opToken:        operationCreateImageGeneration,
		model:          command.Model,
		prompt:         command.Prompt,
		idempotencyKey: command.IdempotencyKey,
		oversize:       command.OversizeBody,
		malformed:      command.MalformedBody,
	})
}

// CreateImageEdit creates one durable edit job after full gates.
func (service *RenderService) CreateImageEdit(ctx context.Context, command CreateImageEditCommand) (RenderJobResult, error) {
	return service.create(ctx, createRequest{
		presented:      command.PresentedKeyMaterial,
		requestID:      command.RequestID,
		operation:      domain.RenderOpImageEdit,
		opToken:        operationCreateImageEdit,
		model:          command.Model,
		prompt:         command.Prompt,
		inputs:         []domain.AssetID{command.InputAssetID},
		idempotencyKey: command.IdempotencyKey,
		oversize:       command.OversizeBody,
		malformed:      command.MalformedBody,
	})
}

// CreateImageInpaint creates one durable inpaint job; never downgrades to edit.
func (service *RenderService) CreateImageInpaint(ctx context.Context, command CreateImageInpaintCommand) (RenderJobResult, error) {
	return service.create(ctx, createRequest{
		presented:      command.PresentedKeyMaterial,
		requestID:      command.RequestID,
		operation:      domain.RenderOpInpaint,
		opToken:        operationCreateImageInpaint,
		model:          command.Model,
		prompt:         command.Prompt,
		inputs:         []domain.AssetID{command.InputAssetID},
		mask:           command.MaskAssetID,
		idempotencyKey: command.IdempotencyKey,
		oversize:       command.OversizeBody,
		malformed:      command.MalformedBody,
	})
}

type createRequest struct {
	presented      string
	requestID      domain.Identifier
	operation      domain.RenderOperation
	opToken        domain.OperationToken
	model          string
	prompt         string
	inputs         []domain.AssetID
	mask           domain.AssetID
	idempotencyKey string
	oversize       bool
	malformed      bool
}

func (service *RenderService) create(ctx context.Context, command createRequest) (RenderJobResult, error) {
	sc := spineContext{operation: command.opToken, requestID: service.resolveRequestID(command.requestID), start: service.clock.Now()}

	principal, canonical, ok := service.authenticate(ctx, ports.PresentedClientAPIKey{Material: command.presented})
	if !ok {
		return RenderJobResult{}, service.fail(ctx, sc, canonical)
	}
	sc.keyID = principal.ClientAPIKeyID

	// A1: scope.
	if !principal.Scopes.Has(command.operation.RequiredScope()) {
		return RenderJobResult{}, service.fail(ctx, sc, domain.NewForbidden())
	}

	// A2: size.
	if command.oversize {
		return RenderJobResult{}, service.fail(ctx, sc, domain.NewRequestTooLarge())
	}
	if command.malformed {
		return RenderJobResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}
	if command.idempotencyKey == "" || utf8.RuneCountInString(command.idempotencyKey) > maxIdempotencyKeyLength {
		return RenderJobResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}
	if command.model == "" || command.prompt == "" || !command.operation.Valid() {
		return RenderJobResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}
	if command.operation == domain.RenderOpImageEdit || command.operation == domain.RenderOpInpaint {
		if len(command.inputs) == 0 || command.inputs[0] == "" {
			return RenderJobResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
		}
	}
	if command.operation == domain.RenderOpInpaint && command.mask == "" {
		return RenderJobResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}

	// Same-Tenant Asset visibility for inputs/mask before routing/Provider work.
	for _, assetID := range command.inputs {
		if assetID == "" {
			continue
		}
		asset, err := service.assets.Visible(ctx, principal, assetID)
		if err != nil {
			return RenderJobResult{}, service.fail(ctx, sc, service.assetVisibilityCanonical(err))
		}
		if asset.Kind != domain.AssetKindInput {
			return RenderJobResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
		}
	}
	if command.mask != "" {
		mask, err := service.assets.Visible(ctx, principal, command.mask)
		if err != nil {
			return RenderJobResult{}, service.fail(ctx, sc, service.assetVisibilityCanonical(err))
		}
		if mask.Kind != domain.AssetKindMask {
			return RenderJobResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
		}
	}

	fingerprint := domain.NewCreateRenderJobFingerprint(command.operation, command.model, command.prompt, command.inputs, command.mask)
	identity := domain.ReplayIdentity{
		Scope: domain.ReplayScope{
			TenantID:       principal.TenantID,
			ClientAPIKeyID: principal.ClientAPIKeyID,
			Key:            command.idempotencyKey,
		},
		Fingerprint: fingerprint,
	}
	decision, err := service.replay.Claim(ctx, identity)
	if err != nil {
		return RenderJobResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}
	switch decision.Outcome {
	case ports.ReplayClaimed:
		// sole owner continues below
	case ports.ReplayTerminal:
		// Matching replay: same job. If queue publication never succeeded,
		// recover by re-enqueueing without creating a replacement (#14 §3.3).
		job := decision.TerminalJob
		if !job.QueuePublished {
			published, err := service.ensureQueuePublished(ctx, job)
			if err != nil {
				// Job exists; return dependency failure so the client may retry the same key.
				return RenderJobResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
			}
			// Refresh terminal projection so later replays see QueuePublished.
			_ = service.replay.Complete(ctx, identity, ports.RenderReplayResult{Job: published})
			job = published
		}
		service.recordTelemetry(ctx, sc.operation, "", 202)
		service.recordRequestLog(ctx, sc.requestID, principal.ClientAPIKeyID, string(sc.operation), 202, "ok", sc.start)
		return RenderJobResult{Job: job, RequestID: sc.requestID}, nil
	case ports.ReplayInProgress:
		return RenderJobResult{}, service.fail(ctx, sc, domain.NewIdempotencyInProgress())
	case ports.ReplayConflict:
		return RenderJobResult{}, service.fail(ctx, sc, domain.NewIdempotencyConflict())
	case ports.ReplayUncertain:
		return RenderJobResult{}, service.fail(ctx, sc, domain.NewIdempotencyUncertain())
	default:
		return RenderJobResult{}, service.fail(ctx, sc, domain.NewInternalError())
	}

	// A3-A5 admission before routing/side effects.
	reservation, canonical, ok := service.admit(ctx, principal, command.opToken)
	if !ok {
		service.abandon(ctx, identity)
		return RenderJobResult{}, service.fail(ctx, sc, canonical)
	}

	account, canonical, ok := service.selectAccount(ctx, principal, command.operation, command.model, sc.start)
	if !ok {
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		return RenderJobResult{}, service.fail(ctx, sc, canonical)
	}

	// Vault presence gate: credential version must be authorized before enqueue.
	// Valid=false is account_not_usable (usability), not only a dependency error.
	validation, err := service.vault.Validate(ctx, ports.CredentialValidation{
		Principal: principal,
		AccountID: account.ID,
		AuthMode:  account.AuthMode,
		Version:   account.Credential.Version,
	})
	if err != nil {
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		if errors.Is(err, ports.ErrCredentialAbsent) {
			return RenderJobResult{}, service.fail(ctx, sc, domain.NewAccountNotUsable(domain.RemediationSubmitCredential))
		}
		return RenderJobResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}
	if !validation.Valid {
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		return RenderJobResult{}, service.fail(ctx, sc, domain.NewAccountNotUsable(domain.RemediationSubmitCredential))
	}

	jobID, err := service.ids.New(domain.IdentifierKindJob)
	if err != nil {
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		return RenderJobResult{}, service.fail(ctx, sc, domain.NewInternalError())
	}
	now := domain.NewTimestamp(sc.start)
	// Job metadata stores only a non-secret digest; plaintext goes to the
	// confidential port and is never retained on the durable job row (ADR 0009).
	promptDigest := domain.DigestPrompt(command.prompt)
	job := domain.NewQueuedRenderJob(
		jobID,
		principal.TenantID,
		principal.ClientAPIKeyID,
		command.operation,
		command.model,
		promptDigest,
		command.inputs,
		command.mask,
		account.ID,
		account.Credential.Version,
		fingerprint,
		command.idempotencyKey,
		now,
	)
	// Queue publication starts false; mark true only after Enqueue accepts.
	job.QueuePublished = false

	// Bind confidential prompt before the job becomes executable.
	if err := service.prompts.Put(ctx, ports.RenderPromptIntake{
		TenantID: principal.TenantID,
		JobID:    jobID,
		Material: command.prompt,
	}); err != nil {
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		return RenderJobResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}

	persisted, err := service.jobs.Create(ctx, ports.RenderJobCreation{Principal: principal, Job: job})
	if err != nil {
		service.purgePrompt(ctx, principal.TenantID, jobID)
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		return RenderJobResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}

	// Complete replay to terminal BEFORE enqueue so a later matching request
	// recovers this job rather than abandoning and creating a replacement.
	// If enqueue fails, matching retry re-attempts publication (#14 §3.3).
	if err := service.replay.Complete(ctx, identity, ports.RenderReplayResult{Job: persisted}); err != nil {
		// Job row may exist; keep occupancy until a later terminal path settles it.
		// Uncertain replay leaves the client to retry; do not free concurrency early.
		return RenderJobResult{}, service.fail(ctx, sc, domain.NewIdempotencyUncertain())
	}

	published, err := service.ensureQueuePublished(ctx, persisted)
	if err != nil {
		// Do NOT abandon and do NOT release admission: durable job + terminal
		// replay exist. Occupancy is held until job terminal (#8 §7.4 / #14 §7.3).
		// Client retry with the same key recovers the same job and re-enqueues.
		return RenderJobResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}
	// Refresh terminal with QueuePublished=true for pure matching replays.
	_ = service.replay.Complete(ctx, identity, ports.RenderReplayResult{Job: published})

	// Hold the create admission reservation through job terminal (complete /
	// fail / cancel). Releasing here would free concurrency while the job is
	// still executable (#8 Active Render Jobs / #14 residual occupancy).
	service.observeSuccess(ctx, sc, ports.AuditRenderJobCreated, principal, published, 202)
	return RenderJobResult{Job: published, RequestID: sc.requestID}, nil
}

// ensureQueuePublished enqueues the SafeJobReference once and marks the job
// published. Idempotent when already published.
func (service *RenderService) ensureQueuePublished(ctx context.Context, job domain.RenderJob) (domain.RenderJob, error) {
	if job.QueuePublished {
		return job, nil
	}
	if _, err := service.queue.Enqueue(ctx, ports.SafeJobReference{
		TenantID: domain.Identifier(job.TenantID),
		JobID:    job.JobID,
	}); err != nil {
		return job, err
	}
	return service.jobs.MarkQueuePublished(ctx, job.JobRef())
}

// RecoverUnpublishedQueues autonomously enqueues durable jobs that were created
// but never published (QueuePublished=false). Does not require a second client
// request (#14 §3.3 startup/background recovery).
func (service *RenderService) RecoverUnpublishedQueues(ctx context.Context) error {
	if service == nil || service.jobs == nil || service.queue == nil {
		return ports.ErrDependencyUnavailable
	}
	pending, err := service.jobs.ListUnpublishedQueue(ctx)
	if err != nil {
		return err
	}
	var first error
	for _, job := range pending {
		if _, err := service.ensureQueuePublished(ctx, job); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (service *RenderService) purgePrompt(ctx context.Context, tenant domain.TenantID, jobID domain.Identifier) {
	_ = service.prompts.Delete(ctx, ports.RenderPromptAccess{TenantID: tenant, JobID: jobID})
}

// fencedTerminal applies a fenced transition and purges confidential prompt when
// the resulting lifecycle is terminal (completed/failed/canceled).
func (service *RenderService) fencedTerminal(ctx context.Context, tenant domain.TenantID, transition ports.FencedTransition) (domain.RenderJob, error) {
	job, err := service.jobs.Transition(ctx, transition)
	if err != nil {
		return domain.RenderJob{}, err
	}
	if job.Lifecycle.Terminal() {
		service.purgePrompt(ctx, tenant, job.JobID)
	}
	return job, nil
}

// GetRenderJob reads one same-Tenant job status.
func (service *RenderService) GetRenderJob(ctx context.Context, query GetRenderJobQuery) (RenderJobResult, error) {
	sc := spineContext{operation: operationGetRenderJob, requestID: service.resolveRequestID(query.RequestID), start: service.clock.Now()}

	principal, canonical, ok := service.authenticate(ctx, ports.PresentedClientAPIKey{Material: query.PresentedKeyMaterial})
	if !ok {
		return RenderJobResult{}, service.fail(ctx, sc, canonical)
	}
	sc.keyID = principal.ClientAPIKeyID

	if !principal.Scopes.Has(domain.ScopeJobsRead) {
		return RenderJobResult{}, service.fail(ctx, sc, domain.NewForbidden())
	}
	if query.JobID == "" {
		return RenderJobResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}

	job, err := service.jobs.Visible(ctx, principal, query.JobID)
	if err != nil {
		return RenderJobResult{}, service.fail(ctx, sc, service.jobVisibilityCanonical(err))
	}

	reservation, canonical, ok := service.admit(ctx, principal, operationGetRenderJob)
	if !ok {
		return RenderJobResult{}, service.fail(ctx, sc, canonical)
	}
	service.release(ctx, reservation)

	service.observeSuccess(ctx, sc, ports.AuditRenderJobRead, principal, job, 200)
	return RenderJobResult{Job: job, RequestID: sc.requestID}, nil
}

// CancelRenderJob cancels a same-Tenant job without Provider work when queued.
func (service *RenderService) CancelRenderJob(ctx context.Context, command CancelRenderJobCommand) (RenderJobResult, error) {
	sc := spineContext{operation: operationCancelRenderJob, requestID: service.resolveRequestID(command.RequestID), start: service.clock.Now()}

	principal, canonical, ok := service.authenticate(ctx, ports.PresentedClientAPIKey{Material: command.PresentedKeyMaterial})
	if !ok {
		return RenderJobResult{}, service.fail(ctx, sc, canonical)
	}
	sc.keyID = principal.ClientAPIKeyID

	if !principal.Scopes.Has(domain.ScopeJobsManage) {
		return RenderJobResult{}, service.fail(ctx, sc, domain.NewForbidden())
	}
	if command.JobID == "" {
		return RenderJobResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}

	// Ownership before admission.
	if _, err := service.jobs.Visible(ctx, principal, command.JobID); err != nil {
		return RenderJobResult{}, service.fail(ctx, sc, service.jobVisibilityCanonical(err))
	}

	reservation, canonical, ok := service.admit(ctx, principal, operationCancelRenderJob)
	if !ok {
		return RenderJobResult{}, service.fail(ctx, sc, canonical)
	}
	defer service.release(ctx, reservation)

	job, err := service.jobs.Cancel(ctx, ports.CancelMutation{
		Principal:   principal,
		JobID:       command.JobID,
		RequestedBy: principal.ClientAPIKeyID,
		Now:         domain.NewTimestamp(sc.start),
	})
	if err != nil {
		return RenderJobResult{}, service.fail(ctx, sc, service.jobVisibilityCanonical(err))
	}

	// Purge confidential prompt on terminal cancel (queued→canceled).
	if job.Lifecycle == domain.JobCanceled {
		service.purgePrompt(ctx, principal.TenantID, job.JobID)
		// Create-time occupancy is released only at accounting terminal.
		service.releaseJobAdmission(ctx, job)
	}

	action := ports.AuditRenderJobCanceled
	service.observeSuccess(ctx, sc, action, principal, job, 200)
	return RenderJobResult{Job: job, RequestID: sc.requestID}, nil
}

// RetryRenderJobOutput retries placement only; never re-renders.
func (service *RenderService) RetryRenderJobOutput(ctx context.Context, command RetryRenderJobOutputCommand) (OutputDeliveryResult, error) {
	sc := spineContext{operation: operationRetryRenderJobOutput, requestID: service.resolveRequestID(command.RequestID), start: service.clock.Now()}

	principal, canonical, ok := service.authenticate(ctx, ports.PresentedClientAPIKey{Material: command.PresentedKeyMaterial})
	if !ok {
		return OutputDeliveryResult{}, service.fail(ctx, sc, canonical)
	}
	sc.keyID = principal.ClientAPIKeyID

	if !principal.Scopes.Has(domain.ScopeJobsManage) {
		return OutputDeliveryResult{}, service.fail(ctx, sc, domain.NewForbidden())
	}
	if command.JobID == "" || command.OutputEntryID == "" {
		return OutputDeliveryResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}

	job, err := service.jobs.Visible(ctx, principal, command.JobID)
	if err != nil {
		return OutputDeliveryResult{}, service.fail(ctx, sc, service.jobVisibilityCanonical(err))
	}
	if job.Lifecycle != domain.JobCompleted || job.Manifest.ID == "" {
		return OutputDeliveryResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}

	var entry domain.OutputEntry
	found := false
	for _, candidate := range job.OutputEntries {
		if candidate.ID == command.OutputEntryID {
			entry = candidate
			found = true
			break
		}
	}
	if !found {
		return OutputDeliveryResult{}, service.fail(ctx, sc, domain.NewResourceNotFound())
	}

	reservation, canonical, ok := service.admit(ctx, principal, operationRetryRenderJobOutput)
	if !ok {
		return OutputDeliveryResult{}, service.fail(ctx, sc, canonical)
	}
	defer service.release(ctx, reservation)

	// Already available: idempotent return, zero render/placement side effect.
	if entry.DeliveryState == domain.OutputAvailable && entry.AssetID != "" {
		service.observeSuccess(ctx, sc, ports.AuditRenderOutputRetry, principal, job, 200)
		return OutputDeliveryResult{Job: job, Entry: entry, RequestID: sc.requestID}, nil
	}

	// Placement-only recovery from immutable manifest staging checksum/content.
	// Controlled path: re-place from staged checksum identity without render.
	result, placeErr := service.placeFromManifest(ctx, principal, job, entry, 0)
	if placeErr.Code != "" {
		return OutputDeliveryResult{}, service.fail(ctx, sc, placeErr)
	}
	service.observeSuccess(ctx, sc, ports.AuditRenderOutputRetry, principal, result.Job, 200)
	return OutputDeliveryResult{Job: result.Job, Entry: result.Entry, RequestID: sc.requestID}, nil
}

// ExecuteJob is the exported worker seam. Queue redelivery is at-most-one render.
func (service *RenderService) ExecuteJob(ctx context.Context, ref domain.JobRef) error {
	if !ref.Valid() {
		return ports.ErrInvalidJobReference
	}

	workerID, err := service.ids.New(domain.IdentifierKindWorker)
	if err != nil {
		return err
	}

	now := service.nowTS()
	claim, err := service.jobs.ClaimWorker(ctx, ref, ports.WorkerLease{
		WorkerID:  workerID,
		Now:       now,
		ExpiresAt: domain.NewTimestamp(now.Time().Add(2 * time.Minute)),
	})
	if err != nil {
		// Concurrent claimant / terminal / cancel / post-payload lease expiry —
		// discard without Provider work (lease expiry ≠ re-render).
		if errors.Is(err, domain.ErrJobNotClaimable) || errors.Is(err, ports.ErrRenderJobNotVisible) || errors.Is(err, ports.ErrDependencyUnavailable) {
			return nil
		}
		return err
	}
	job := claim.Job
	fence := claim.FencingToken

	// Queued cancel may have won; re-load and exit without Provider.
	if job.Lifecycle == domain.JobCancelRequested || job.Lifecycle.Terminal() {
		if job.Lifecycle.Terminal() {
			service.releaseJobAdmission(ctx, job)
		}
		return nil
	}

	// Job→account continuity binding for this execution (not exclusive account mutex).
	if err := service.jobs.BindAccountLease(ctx, ref, fence, job.ProviderAccountID); err != nil {
		if err := service.persistTerminal(ctx, job, ports.FencedTransition{
			JobRef:       ref,
			FencingToken: fence,
			To:           domain.JobFailed,
			FailureStage: domain.StageRouting,
			FailureClass: domain.ErrCodeAccountNotUsable,
			CommitStatus: domain.CommitNotStarted,
			ClearLease:   true,
			Now:          service.nowTS(),
		}); err != nil {
			return err
		}
		return nil
	}

	// Worker principal for Vault/Asset visibility is synthetic same-Tenant.
	principal := domain.SecurityPrincipal{
		TenantID:       job.TenantID,
		ClientAPIKeyID: job.ClientAPIKeyID,
		Scopes:         domain.NewScopeSet(domain.ScopeJobsManage, domain.ScopeAssetsRead, domain.ScopeAssetsWrite),
	}

	// Recheck cancel before preflight/payload (honest cancel; no new attempt).
	current, err := service.jobs.Load(ctx, ref)
	if err != nil {
		return err
	}
	if current.Lifecycle == domain.JobCancelRequested {
		if err := service.persistTerminal(ctx, job, ports.FencedTransition{
			JobRef:       ref,
			FencingToken: fence,
			To:           domain.JobCanceled,
			CommitStatus: domain.CommitNotStarted,
			ClearLease:   true,
			Now:          service.nowTS(),
		}); err != nil {
			return err
		}
		return nil
	}

	// Pre-payload re-gates: Account / Health / Capability / model / Input Assets /
	// Vault Valid. Fail closed without Provider payload on reject (#14 §6.5).
	account, preflightClass, preflightOK := service.preflightExecute(ctx, principal, job)
	if !preflightOK {
		if err := service.persistTerminal(ctx, job, ports.FencedTransition{
			JobRef:       ref,
			FencingToken: fence,
			To:           domain.JobFailed,
			FailureStage: preflightFailureStage(preflightClass),
			FailureClass: preflightClass,
			CommitStatus: domain.CommitNotStarted,
			ClearLease:   true,
			Now:          service.nowTS(),
		}); err != nil {
			return err
		}
		return nil
	}

	// Attempt ledger before payload: CommitNotStarted + PayloadSent=false so a
	// crash before Adapter entry remains lease-recoverable (#14 §6.2–6.4).
	now = service.nowTS()
	attempt := domain.UpstreamAttempt{
		ID:                domain.NewAttemptID(job.JobID, 1),
		ProviderAccountID: job.ProviderAccountID,
		CredentialVersion: job.CredentialVersion,
		CommitStatus:      domain.CommitNotStarted,
		PayloadSent:       false,
		Sequence:          1,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if _, err := service.jobs.ObserveAttempt(ctx, ports.AttemptObservation{
		JobRef:       ref,
		FencingToken: fence,
		Attempt:      attempt,
		Phase:        domain.PhaseUpstream,
		CommitStatus: domain.CommitNotStarted,
		Progress: domain.JobProgress{
			Source:    domain.ProgressEstimated,
			Value:     10,
			UpdatedAt: now,
		},
		Now: now,
	}); err != nil {
		if errors.Is(err, domain.ErrStaleFence) {
			return nil
		}
		return err
	}

	// Payload send boundary lives inside AuthorizedRender immediately before
	// Adapter entry (SendBoundary). Application does not mark PayloadSent here.
	manifestID := domain.NewResultManifestID(attempt.ID)
	sendBoundary := &fencedPayloadSendBoundary{
		service: service,
		ref:     ref,
		fence:   fence,
		attempt: &attempt,
	}
	outcome, renderErr := service.authorized.Render(ctx, ports.AuthorizedRenderRequest{
		Principal: principal,
		JobRef:    ref,
		AccountID: job.ProviderAccountID,
		AuthMode:  account.AuthMode,
		Version:   job.CredentialVersion,
		Invocation: domain.RenderInvocation{
			TenantID:          job.TenantID,
			JobID:             job.JobID,
			AttemptID:         attempt.ID,
			Operation:         job.Operation,
			Model:             job.Model,
			ProviderAccountID: job.ProviderAccountID,
			CredentialVersion: job.CredentialVersion,
		},
		Capture: ports.RenderCapturePlan{
			TenantID:   job.TenantID,
			JobID:      job.JobID,
			AttemptID:  attempt.ID,
			ManifestID: manifestID,
		},
		SendBoundary:  sendBoundary,
		InputAssetIDs: append([]domain.AssetID(nil), job.InputAssetIDs...),
		MaskAssetID:   job.MaskAssetID,
	})

	if renderErr != nil {
		// Classification depends on whether the send boundary was crossed.
		// Pre-send failures stay not_started (recoverable); post-send uncertain
		// outcomes are unknown (no re-render).
		commit := domain.CommitNotStarted
		failureClass := domain.ErrCodeDependencyUnavailable
		failureStage := domain.StageDependency
		if attempt.PayloadSent {
			commit = domain.CommitUnknown
		}
		if errors.Is(renderErr, ports.ErrCredentialAbsent) {
			// Vault reject is pre-Adapter (AuthorizedRender validates first).
			if !attempt.PayloadSent {
				commit = domain.CommitNotStarted
			}
			failureClass = domain.ErrCodeAccountNotUsable
			failureStage = domain.StageRouting
		}
		attempt.CommitStatus = commit
		now = service.nowTS()
		if err := service.persistAttempt(ctx, ports.AttemptObservation{
			JobRef: ref, FencingToken: fence, Attempt: attempt,
			Phase: domain.PhaseUpstream, CommitStatus: commit, Now: now,
		}); err != nil {
			return err
		}
		if err := service.persistTerminal(ctx, job, ports.FencedTransition{
			JobRef: ref, FencingToken: fence, To: domain.JobFailed,
			FailureStage: failureStage, FailureClass: failureClass,
			CommitStatus: commit, ClearLease: true, Now: now,
		}); err != nil {
			return err
		}
		return nil
	}

	switch outcome.Class {
	case domain.RenderOutcomeNotCommitted:
		attempt.CommitStatus = domain.CommitNotCommitted
		if err := service.persistTerminal(ctx, job, ports.FencedTransition{
			JobRef: ref, FencingToken: fence, To: domain.JobFailed,
			FailureStage: domain.StageInternal, FailureClass: domain.ErrCodeInternal,
			CommitStatus: domain.CommitNotCommitted, ClearLease: true, Now: service.nowTS(),
		}); err != nil {
			return err
		}
		return nil
	case domain.RenderOutcomeUnknown:
		attempt.CommitStatus = domain.CommitUnknown
		if err := service.persistTerminal(ctx, job, ports.FencedTransition{
			JobRef: ref, FencingToken: fence, To: domain.JobFailed,
			FailureStage: domain.StageRecovery, FailureClass: domain.ErrCodeInternal,
			CommitStatus: domain.CommitUnknown, ClearLease: true, Now: service.nowTS(),
		}); err != nil {
			return err
		}
		return nil
	case domain.RenderOutcomeSuccess, domain.RenderOutcomeCommitted:
		// fall through — bytes already staged; only metadata remains
	default:
		attempt.CommitStatus = domain.CommitUnknown
		if err := service.persistTerminal(ctx, job, ports.FencedTransition{
			JobRef: ref, FencingToken: fence, To: domain.JobFailed,
			FailureStage: domain.StageInternal, FailureClass: domain.ErrCodeInternal,
			CommitStatus: domain.CommitUnknown, ClearLease: true, Now: service.nowTS(),
		}); err != nil {
			return err
		}
		return nil
	}

	// Cancel race after result: if cancel_requested won before capture, drain to canceled.
	// Honest: response after cancel mid-flight does not claim upstream aborted when the
	// Provider already committed; we stop client delivery and settle terminal.
	current, err = service.jobs.Load(ctx, ref)
	if err != nil {
		return err
	}
	if current.Lifecycle == domain.JobCancelRequested {
		if err := service.persistTerminal(ctx, job, ports.FencedTransition{
			JobRef: ref, FencingToken: fence, To: domain.JobCanceled,
			CommitStatus: domain.CommitCommitted, ClearLease: true, Now: service.nowTS(),
		}); err != nil {
			return err
		}
		return nil
	}

	manifest := outcome.Manifest
	if manifest.ID == "" || len(manifest.Entries) == 0 {
		if err := service.persistTerminal(ctx, job, ports.FencedTransition{
			JobRef: ref, FencingToken: fence, To: domain.JobFailed,
			FailureStage: domain.StageInternal, FailureClass: domain.ErrCodeInternal,
			CommitStatus: domain.CommitCommitted, ClearLease: true, Now: service.nowTS(),
		}); err != nil {
			return err
		}
		return nil
	}
	manifest.CapturedAt = service.nowTS()

	if _, err := service.jobs.CaptureManifest(ctx, ports.ManifestCapture{
		JobRef:       ref,
		FencingToken: fence,
		Manifest:     manifest,
		Phase:        domain.PhasePlacingOutput,
		Now:          service.nowTS(),
	}); err != nil {
		if errors.Is(err, domain.ErrStaleFence) {
			return nil
		}
		return err
	}

	// Application owns Asset Reserve/Commit/Put via stable placement ids;
	// job store only records the result. Storage-cap is placement/delivery only.
	jobAfter, err := service.jobs.Load(ctx, ref)
	if err != nil {
		return err
	}
	for _, entry := range jobAfter.OutputEntries {
		if placeErr := service.placeEntryFromStaging(ctx, principal, jobAfter, entry, fence); placeErr != nil {
			if errors.Is(placeErr, ports.ErrStorageCapExceeded) {
				if _, err := service.jobs.PlaceOutput(ctx, ports.PlacementRequest{
					JobRef: ref, FencingToken: fence, EntryID: entry.ID,
					DeliveryStateForced: domain.OutputPending,
					FailureClass:        string(domain.ErrCodeStorageCapExceeded),
					Now:                 service.nowTS(),
				}); err != nil {
					if errors.Is(err, domain.ErrStaleFence) {
						return nil
					}
					return err
				}
				continue
			}
			if err := service.persistTerminal(ctx, job, ports.FencedTransition{
				JobRef: ref, FencingToken: fence, To: domain.JobFailed,
				FailureStage: domain.StageAsset, FailureClass: domain.ErrCodeInternal,
				CommitStatus: domain.CommitCommitted, ClearLease: true, Now: service.nowTS(),
			}); err != nil {
				return err
			}
			return nil
		}
	}

	completeAt := service.nowTS()
	if err := service.persistTerminal(ctx, job, ports.FencedTransition{
		JobRef: ref, FencingToken: fence, To: domain.JobCompleted,
		CommitStatus: domain.CommitCommitted,
		Progress: domain.JobProgress{
			Source: domain.ProgressEstimated, Value: 100, UpdatedAt: completeAt,
		},
		ClearLease: true, Now: completeAt,
	}); err != nil {
		return err
	}
	return nil
}

// fencedPayloadSendBoundary records PayloadSent=true under the worker fence at
// the protected Adapter entry surface (not before AuthorizedRender begins).
type fencedPayloadSendBoundary struct {
	service *RenderService
	ref     domain.JobRef
	fence   domain.FencingToken
	attempt *domain.UpstreamAttempt
}

// MarkPayloadSent durably observes payload transmission beginning.
func (b *fencedPayloadSendBoundary) MarkPayloadSent(ctx context.Context) error {
	if b == nil || b.service == nil || b.attempt == nil {
		return ports.ErrDependencyUnavailable
	}
	now := b.service.nowTS()
	b.attempt.PayloadSent = true
	b.attempt.UpdatedAt = now
	// Commit remains not_started until Adapter returns an authoritative class;
	// PayloadSent alone blocks lease reclaim re-render (#14 §6.2).
	_, err := b.service.jobs.ObserveAttempt(ctx, ports.AttemptObservation{
		JobRef:       b.ref,
		FencingToken: b.fence,
		Attempt:      *b.attempt,
		Phase:        domain.PhaseUpstream,
		CommitStatus: b.attempt.CommitStatus,
		Now:          now,
	})
	if err != nil {
		b.attempt.PayloadSent = false
		return err
	}
	return nil
}

// persistAttempt records attempt observation; only ErrStaleFence is discarded.
func (service *RenderService) persistAttempt(ctx context.Context, observation ports.AttemptObservation) error {
	_, err := service.jobs.ObserveAttempt(ctx, observation)
	if err != nil && errors.Is(err, domain.ErrStaleFence) {
		return nil
	}
	return err
}

// persistTerminal applies a terminal transition; only ErrStaleFence is discarded.
// Other durable mutation errors return so redelivery can recover without a second render.
// On successful terminal, create-time admission occupancy is released once (#8 §7.4).
func (service *RenderService) persistTerminal(ctx context.Context, job domain.RenderJob, transition ports.FencedTransition) error {
	terminal, err := service.fencedTerminal(ctx, job.TenantID, transition)
	if err != nil {
		if errors.Is(err, domain.ErrStaleFence) {
			return nil
		}
		return err
	}
	if terminal.Lifecycle.Terminal() {
		service.releaseJobAdmission(ctx, terminal)
	}
	return nil
}

// preflightExecute re-gates Account/Health/Capability/model/Input Assets and
// Vault Valid before any payload. Returns the live account for AuthMode surface.
func (service *RenderService) preflightExecute(
	ctx context.Context,
	principal domain.SecurityPrincipal,
	job domain.RenderJob,
) (domain.ProviderAccount, domain.ErrorCode, bool) {
	account, err := service.accounts.Visible(ctx, principal, job.ProviderAccountID)
	if err != nil {
		return domain.ProviderAccount{}, domain.ErrCodeAccountNotUsable, false
	}
	if canonical, ok := service.candidateRejection(ctx, principal, account, job.Operation, job.Model, service.clock.Now()); !ok {
		if canonical.Code != "" {
			return domain.ProviderAccount{}, canonical.Code, false
		}
		return domain.ProviderAccount{}, domain.ErrCodeAccountNotUsable, false
	}
	// Input Assets (and mask) must remain same-Tenant visible and correct kind.
	for _, assetID := range job.InputAssetIDs {
		if assetID == "" {
			continue
		}
		asset, err := service.assets.Visible(ctx, principal, assetID)
		if err != nil {
			return domain.ProviderAccount{}, domain.ErrCodeResourceNotFound, false
		}
		if asset.Kind != domain.AssetKindInput {
			return domain.ProviderAccount{}, domain.ErrCodeInvalidRequest, false
		}
	}
	if job.MaskAssetID != "" {
		mask, err := service.assets.Visible(ctx, principal, job.MaskAssetID)
		if err != nil {
			return domain.ProviderAccount{}, domain.ErrCodeResourceNotFound, false
		}
		if mask.Kind != domain.AssetKindMask {
			return domain.ProviderAccount{}, domain.ErrCodeInvalidRequest, false
		}
	}
	validation, err := service.vault.Validate(ctx, ports.CredentialValidation{
		Principal: principal,
		AccountID: account.ID,
		AuthMode:  account.AuthMode,
		Version:   job.CredentialVersion,
	})
	if err != nil {
		if errors.Is(err, ports.ErrCredentialAbsent) {
			return domain.ProviderAccount{}, domain.ErrCodeAccountNotUsable, false
		}
		return domain.ProviderAccount{}, domain.ErrCodeDependencyUnavailable, false
	}
	if !validation.Valid {
		return domain.ProviderAccount{}, domain.ErrCodeAccountNotUsable, false
	}
	return account, "", true
}

// releaseJobAdmission settles the create-time occupancy for one job at terminal.
// Reconstructs the reservation from durable job identity (#8 Active Render Jobs).
func (service *RenderService) releaseJobAdmission(ctx context.Context, job domain.RenderJob) {
	op, ok := createAdmissionOperation(job.Operation)
	if !ok {
		return
	}
	service.release(ctx, ports.AdmissionReservation{
		Principal: domain.SecurityPrincipal{
			TenantID:       job.TenantID,
			ClientAPIKeyID: job.ClientAPIKeyID,
		},
		Operation: op,
	})
}

func createAdmissionOperation(op domain.RenderOperation) (domain.OperationToken, bool) {
	switch op {
	case domain.RenderOpImageGeneration:
		return operationCreateImageGeneration, true
	case domain.RenderOpImageEdit:
		return operationCreateImageEdit, true
	case domain.RenderOpInpaint:
		return operationCreateImageInpaint, true
	default:
		return "", false
	}
}

func preflightFailureStage(class domain.ErrorCode) domain.FailureStage {
	switch class {
	case domain.ErrCodeCapabilityUnsupported, domain.ErrCodeCapabilityUnverified, domain.ErrCodeSnapshotStale:
		return domain.StageCapability
	case domain.ErrCodeResourceNotFound, domain.ErrCodeInvalidRequest:
		return domain.StageAsset
	case domain.ErrCodeDependencyUnavailable:
		return domain.StageDependency
	default:
		return domain.StageRouting
	}
}

func (service *RenderService) nowTS() domain.Timestamp {
	return domain.NewTimestamp(service.clock.Now())
}

func (service *RenderService) stagingIdentity(job domain.RenderJob, entry domain.OutputEntry) ports.StagingIdentity {
	return ports.StagingIdentity{
		TenantID:   job.TenantID,
		JobID:      job.JobID,
		ManifestID: job.Manifest.ID,
		EntryID:    entry.ID,
		Checksum:   entry.Checksum,
	}
}

// placeEntryFromStaging Uses staged bytes then commits a permanent Asset and
// records placement on the job store. No package-global state.
func (service *RenderService) placeEntryFromStaging(
	ctx context.Context,
	principal domain.SecurityPrincipal,
	job domain.RenderJob,
	entry domain.OutputEntry,
	fence domain.FencingToken,
) error {
	var placeErr error
	err := service.staging.Use(ctx, ports.StagingAccess{
		Principal: principal,
		Identity:  service.stagingIdentity(job, entry),
	}, func(data []byte) error {
		placeErr = service.placeOutputBytes(ctx, principal, job, entry, data, fence)
		return placeErr
	})
	if err != nil {
		if errors.Is(err, ports.ErrStagingNotFound) || errors.Is(err, ports.ErrDependencyUnavailable) {
			return err
		}
		return err
	}
	return placeErr
}

func (service *RenderService) placeOutputBytes(
	ctx context.Context,
	principal domain.SecurityPrincipal,
	job domain.RenderJob,
	entry domain.OutputEntry,
	data []byte,
	fence domain.FencingToken,
) error {
	// Stable placement-derived Asset ID: retries claim at most one Asset.
	stableID := domain.StableOutputAssetID(job.TenantID, job.JobID, entry.ID)
	if existing, err := service.assets.Visible(ctx, principal, stableID); err == nil {
		_, err = service.jobs.PlaceOutput(ctx, ports.PlacementRequest{
			JobRef: job.JobRef(), FencingToken: fence, EntryID: entry.ID,
			Asset: existing, Now: service.nowTS(),
		})
		return err
	}

	byteSize := int64(len(data))
	sum := sha256.Sum256(data)
	checksum := hex.EncodeToString(sum[:])
	hold := ports.AssetReservation{TenantID: job.TenantID, Bytes: byteSize}
	if err := service.assets.Reserve(ctx, hold); err != nil {
		// Recovery: prior attempt may have committed the Asset after reserve.
		if existing, visErr := service.assets.Visible(ctx, principal, stableID); visErr == nil {
			_, err = service.jobs.PlaceOutput(ctx, ports.PlacementRequest{
				JobRef: job.JobRef(), FencingToken: fence, EntryID: entry.ID,
				Asset: existing, Now: service.nowTS(),
			})
			return err
		}
		return err
	}
	now := domain.NewTimestamp(service.clock.Now())
	asset := domain.Asset{
		ID:             stableID,
		TenantID:       job.TenantID,
		Kind:           domain.AssetKindOutput,
		ContentType:    entry.ContentType,
		ByteSize:       byteSize,
		Width:          1,
		Height:         1,
		Checksum:       checksum,
		Origin:         domain.AssetOriginGenerated,
		SourceJobID:    job.JobID,
		RetentionClass: domain.RetentionClassOutput,
		CreatedAt:      now,
		ExpiresAt:      domain.NewTimestamp(now.Time().Add(domain.RetentionWindow(domain.RetentionClassOutput))),
	}
	if err := service.content.Put(ctx, asset.ID, data); err != nil {
		_ = service.assets.Release(ctx, hold)
		// Content put failed: if metadata already exists from a prior attempt, recover.
		if existing, visErr := service.assets.Visible(ctx, principal, stableID); visErr == nil {
			_, err = service.jobs.PlaceOutput(ctx, ports.PlacementRequest{
				JobRef: job.JobRef(), FencingToken: fence, EntryID: entry.ID,
				Asset: existing, Now: service.nowTS(),
			})
			return err
		}
		return err
	}
	committed, err := service.assets.Commit(ctx, ports.AssetCreation{Principal: principal, Asset: asset, Reservation: hold})
	if err != nil {
		// Do not Release if Asset may already be committed under stable id.
		if existing, visErr := service.assets.Visible(ctx, principal, stableID); visErr == nil {
			_, placeErr := service.jobs.PlaceOutput(ctx, ports.PlacementRequest{
				JobRef: job.JobRef(), FencingToken: fence, EntryID: entry.ID,
				Asset: existing, Now: service.nowTS(),
			})
			return placeErr
		}
		_ = service.assets.Release(ctx, hold)
		return err
	}
	_, err = service.jobs.PlaceOutput(ctx, ports.PlacementRequest{
		JobRef:       job.JobRef(),
		FencingToken: fence,
		EntryID:      entry.ID,
		Asset:        committed,
		Now:          service.nowTS(),
	})
	// If PlaceOutput fails after Commit, return error for redelivery; next attempt
	// Visible(stableID) and records placement only — no second reservation.
	return err
}

func (service *RenderService) placeFromManifest(
	ctx context.Context,
	principal domain.SecurityPrincipal,
	job domain.RenderJob,
	entry domain.OutputEntry,
	fence domain.FencingToken,
) (ports.PlacementResult, domain.CanonicalError) {
	// Prefer existing placement key resume.
	if entry.AssetID != "" {
		result, err := service.jobs.PlaceOutput(ctx, ports.PlacementRequest{
			JobRef:       job.JobRef(),
			FencingToken: fence,
			EntryID:      entry.ID,
			Asset:        domain.Asset{ID: entry.AssetID, ContentType: entry.ContentType, ByteSize: entry.ByteSize, Checksum: entry.Checksum},
			Now:          service.nowTS(),
		})
		if err != nil {
			return ports.PlacementResult{}, service.dependencyCanonical(err)
		}
		return result, domain.CanonicalError{}
	}
	if err := service.placeEntryFromStaging(ctx, principal, job, entry, fence); err != nil {
		if errors.Is(err, ports.ErrStagingNotFound) || errors.Is(err, ports.ErrDependencyUnavailable) {
			result, placeErr := service.jobs.PlaceOutput(ctx, ports.PlacementRequest{
				JobRef:              job.JobRef(),
				FencingToken:        fence,
				EntryID:             entry.ID,
				DeliveryStateForced: domain.OutputFailed,
				FailureClass:        string(domain.ErrCodeInternal),
				Now:                 service.nowTS(),
			})
			if placeErr != nil {
				return ports.PlacementResult{}, service.dependencyCanonical(placeErr)
			}
			return result, domain.CanonicalError{}
		}
		if errors.Is(err, ports.ErrStorageCapExceeded) {
			result, placeErr := service.jobs.PlaceOutput(ctx, ports.PlacementRequest{
				JobRef:              job.JobRef(),
				FencingToken:        fence,
				EntryID:             entry.ID,
				DeliveryStateForced: domain.OutputPending,
				FailureClass:        string(domain.ErrCodeStorageCapExceeded),
				Now:                 service.nowTS(),
			})
			if placeErr != nil {
				return ports.PlacementResult{}, service.dependencyCanonical(placeErr)
			}
			return result, domain.CanonicalError{}
		}
		return ports.PlacementResult{}, service.dependencyCanonical(err)
	}
	updated, err := service.jobs.Load(ctx, job.JobRef())
	if err != nil {
		return ports.PlacementResult{}, service.dependencyCanonical(err)
	}
	for _, e := range updated.OutputEntries {
		if e.ID == entry.ID {
			return ports.PlacementResult{Job: updated, Entry: e, Created: true}, domain.CanonicalError{}
		}
	}
	return ports.PlacementResult{Job: updated}, domain.CanonicalError{}
}

// selectAccount applies C0–C5 candidate filters and P0–P5 precedence.
func (service *RenderService) selectAccount(
	ctx context.Context,
	principal domain.SecurityPrincipal,
	operation domain.RenderOperation,
	model string,
	now time.Time,
) (domain.ProviderAccount, domain.CanonicalError, bool) {
	policy, err := service.routing.Read(ctx, principal)
	if err != nil {
		if errors.Is(err, ports.ErrRoutingPolicyNotFound) {
			policy = domain.FailClosedDefaultRoutingPolicy()
		} else {
			return domain.ProviderAccount{}, service.dependencyCanonical(err), false
		}
	}

	// Build candidate set from policy selection order (then candidates).
	order := policy.SelectionOrder
	if len(order) == 0 {
		order = policy.CandidateAccounts
	}
	if len(order) == 0 {
		return domain.ProviderAccount{}, domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
	}

	var candidates []domain.ProviderAccount
	var lastCanonical domain.CanonicalError
	for _, id := range order {
		account, err := service.accounts.Visible(ctx, principal, id)
		if err != nil {
			// C0 ownership: foreign/unknown is skipped only when listed; a
			// policy referencing foreign fails closed non-enumerating at write
			// time. At route time treat as not candidate.
			continue
		}
		// C1 allowlist
		if !principal.AllowsProviderAccount(id) {
			continue
		}
		if canonical, ok := service.candidateRejection(ctx, principal, account, operation, model, now); !ok {
			lastCanonical = canonical
			continue
		}
		candidates = append(candidates, account)
	}
	if len(candidates) == 0 {
		if lastCanonical.Code != "" {
			return domain.ProviderAccount{}, lastCanonical, false
		}
		return domain.ProviderAccount{}, domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
	}
	// P4: first surviving policy-ordered candidate (deterministic).
	return candidates[0], domain.CanonicalError{}, true
}

func (service *RenderService) candidateRejection(
	ctx context.Context,
	principal domain.SecurityPrincipal,
	account domain.ProviderAccount,
	operation domain.RenderOperation,
	model string,
	now time.Time,
) (domain.CanonicalError, bool) {
	// C3 risk / C2 usability
	if account.AuthMode.Prohibited() || account.AuthMode.Experimental() {
		return domain.NewAuthModeUnavailable(), false
	}
	if account.AuthMode.RequiresRiskAck() && !account.RiskAcknowledged {
		return domain.NewAccountNotUsable(domain.RemediationAckRisk), false
	}
	if account.Lifecycle != domain.LifecycleActive {
		return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
	}
	if account.Health.SummaryState == domain.HealthUnknown {
		return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
	}
	if account.Controls.Drain == domain.DrainDraining || account.Controls.Quarantine == domain.QuarantineQuarantined {
		return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
	}
	if !account.Controls.AuthModeExecutionEnabled {
		return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
	}
	// C5 health: cooling/blocked on matching scopes
	for _, condition := range account.Health.Conditions {
		if condition.CredentialVersion != account.Credential.Version {
			continue
		}
		switch condition.State {
		case domain.HealthCoolingDown:
			return domain.NewProviderCooldownBlocked(0), false
		case domain.HealthBlocked, domain.HealthChallenged, domain.HealthExpired:
			return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
		case domain.HealthUnknown:
			if condition.Scope.Kind == domain.HealthScopeAccount {
				return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
			}
		}
	}

	// C4 capability
	snapshot, err := service.capabilities.Get(ctx, principal, account.ID)
	if err != nil {
		if errors.Is(err, ports.ErrCapabilitySnapshotNotFound) {
			return domain.NewCapabilityUnverified(), false
		}
		return service.dependencyCanonical(err), false
	}
	derived := snapshot.WithDerivedFreshness(now)
	switch derived.Freshness {
	case domain.SnapshotStale, domain.SnapshotInvalid:
		return domain.NewSnapshotStale(), false
	case domain.SnapshotFresh:
	default:
		return domain.NewCapabilityUnverified(), false
	}
	capOp := operation.CapabilityOperation()
	opFact, ok := derived.Operations[capOp]
	if !ok || !opFact.Status.Offerable() {
		if ok && opFact.Status == domain.CapabilityUnsupported {
			return domain.NewCapabilityUnsupported(), false
		}
		return domain.NewCapabilityUnverified(), false
	}
	// Model offerability when models are present.
	if model != "" && len(derived.Models) > 0 {
		found := false
		for _, m := range derived.Models {
			if m.ModelSlug != model {
				continue
			}
			if derived.IsOfferablePair(capOp, m, now) {
				found = true
				break
			}
		}
		if !found {
			return domain.NewCapabilityUnsupported(), false
		}
	}

	// Circuit gate when wired.
	if service.circuits != nil {
		circuit, err := service.circuits.SurfaceOpen(ctx, ports.CircuitSurface{
			Provider:  account.Provider,
			AuthMode:  account.AuthMode,
			Operation: capOp,
		})
		if err != nil {
			if errors.Is(err, ports.ErrCircuitUnavailable) {
				return domain.NewDependencyUnavailable(), false
			}
			return service.dependencyCanonical(err), false
		}
		if circuit.Open {
			return domain.NewProviderCooldownBlocked(0), false
		}
	}
	return domain.CanonicalError{}, true
}

func (service *RenderService) authenticate(ctx context.Context, key ports.PresentedClientAPIKey) (domain.SecurityPrincipal, domain.CanonicalError, bool) {
	principal, err := service.principal.Authenticate(ctx, key)
	if err != nil {
		return domain.SecurityPrincipal{}, domain.NewAuthenticationFailed(), false
	}
	if !principal.Valid() {
		return domain.SecurityPrincipal{}, domain.NewAuthenticationFailed(), false
	}
	return principal, domain.CanonicalError{}, true
}

func (service *RenderService) admit(ctx context.Context, principal domain.SecurityPrincipal, operation domain.OperationToken) (ports.AdmissionReservation, domain.CanonicalError, bool) {
	decision, reservation, err := service.admission.Admit(ctx, ports.AdmissionRequest{Principal: principal, Operation: operation})
	if err != nil {
		return ports.AdmissionReservation{}, service.dependencyCanonical(err), false
	}
	if decision.Admitted {
		return reservation, domain.CanonicalError{}, true
	}
	switch decision.Stage {
	case ports.AdmissionStageRateLimit:
		return ports.AdmissionReservation{}, domain.NewRateLimit(), false
	case ports.AdmissionStageConcurrency:
		return ports.AdmissionReservation{}, domain.NewConcurrencyLimit(), false
	case ports.AdmissionStageQuota:
		return ports.AdmissionReservation{}, domain.NewQuotaExhausted(), false
	default:
		return ports.AdmissionReservation{}, domain.NewInternalError(), false
	}
}

func (service *RenderService) release(ctx context.Context, reservation ports.AdmissionReservation) {
	_ = service.admission.Reconcile(ctx, reservation)
}

func (service *RenderService) abandon(ctx context.Context, identity domain.ReplayIdentity) {
	_ = service.replay.Abandon(ctx, identity)
}

func (service *RenderService) jobVisibilityCanonical(err error) domain.CanonicalError {
	if errors.Is(err, ports.ErrRenderJobNotVisible) {
		return domain.NewResourceNotFound()
	}
	return service.dependencyCanonical(err)
}

func (service *RenderService) assetVisibilityCanonical(err error) domain.CanonicalError {
	if errors.Is(err, ports.ErrAssetNotVisible) {
		return domain.NewResourceNotFound()
	}
	return service.dependencyCanonical(err)
}

func (service *RenderService) dependencyCanonical(err error) domain.CanonicalError {
	if errors.Is(err, ports.ErrDependencyUnavailable) || errors.Is(err, ports.ErrRenderAdapterUnavailable) {
		return domain.NewDependencyUnavailable()
	}
	return domain.NewInternalError()
}

func (service *RenderService) fail(ctx context.Context, sc spineContext, canonical domain.CanonicalError) domain.CanonicalError {
	canonical = canonical.WithRequestID(sc.requestID)
	statusCode := canonical.HTTPStatus()
	service.recordTelemetry(ctx, sc.operation, canonical.Code, statusCode)
	service.recordRequestLog(ctx, sc.requestID, sc.keyID, string(sc.operation), statusCode, string(canonical.Code), sc.start)
	return canonical
}

func (service *RenderService) observeSuccess(ctx context.Context, sc spineContext, action ports.RenderAuditAction, principal domain.SecurityPrincipal, job domain.RenderJob, statusCode int) {
	_ = service.audit.Record(ctx, ports.RenderAuditEvent{
		Action:         action,
		TenantID:       principal.TenantID,
		ClientAPIKeyID: principal.ClientAPIKeyID,
		JobID:          job.JobID,
		AccountID:      job.ProviderAccountID,
		RequestID:      sc.requestID,
		Outcome:        "success",
		Lifecycle:      job.Lifecycle,
	})
	service.recordTelemetry(ctx, sc.operation, "", statusCode)
	service.recordRequestLog(ctx, sc.requestID, principal.ClientAPIKeyID, string(sc.operation), statusCode, "ok", sc.start)
}

func (service *RenderService) recordTelemetry(ctx context.Context, operation domain.OperationToken, code domain.ErrorCode, statusCode int) {
	_ = service.telemetry.Record(ctx, ports.TelemetryEvent{
		Operation:  operation,
		Code:       code,
		StatusCode: statusCode,
	})
}

func (service *RenderService) recordRequestLog(ctx context.Context, requestID domain.Identifier, keyID domain.ClientAPIKeyID, action string, statusCode int, message string, start time.Time) {
	_ = service.requestLog.Record(ctx, ports.RequestLog{
		RequestID:  requestID,
		UserID:     keyID,
		Action:     action,
		DurationMS: service.durationMS(start),
		StatusCode: statusCode,
		Message:    message,
	})
}

func (service *RenderService) durationMS(start time.Time) int64 {
	return service.clock.Now().Sub(start).Milliseconds()
}

func (service *RenderService) resolveRequestID(boundaryID domain.Identifier) domain.Identifier {
	if boundaryID != "" {
		return boundaryID
	}
	id, err := service.ids.New(domain.IdentifierKindRequest)
	if err != nil {
		return domain.Identifier("request_unavailable")
	}
	return id
}

var _ JobExecutor = (*RenderService)(nil)
