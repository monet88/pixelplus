package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
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
	vault        ports.CredentialVault
	render       ports.RenderAdapter
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
	Vault        ports.CredentialVault
	Render       ports.RenderAdapter
	Queue        ports.JobRuntime
	Audit        ports.RenderAuditRecorder
	Telemetry    ports.TelemetryRecorder
	RequestLog   ports.RequestLogRecorder
	Clock        ports.Clock
	IDs          ports.IDGenerator
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
	case dependencies.Vault == nil:
		return nil, errors.New("application: credential vault is required")
	case dependencies.Render == nil:
		return nil, errors.New("application: render adapter is required")
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
		vault:        dependencies.Vault,
		render:       dependencies.Render,
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
	case ports.ReplayTerminal:
		service.recordTelemetry(ctx, sc.operation, "", 202)
		service.recordRequestLog(ctx, sc.requestID, principal.ClientAPIKeyID, string(sc.operation), 202, "ok", sc.start)
		return RenderJobResult{Job: decision.TerminalJob, RequestID: sc.requestID}, nil
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
	if _, err := service.vault.Validate(ctx, ports.CredentialValidation{
		Principal: principal,
		AccountID: account.ID,
		AuthMode:  account.AuthMode,
		Version:   account.Credential.Version,
	}); err != nil {
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		if errors.Is(err, ports.ErrCredentialAbsent) {
			return RenderJobResult{}, service.fail(ctx, sc, domain.NewAccountNotUsable(domain.RemediationSubmitCredential))
		}
		return RenderJobResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}

	jobID, err := service.ids.New(domain.IdentifierKindJob)
	if err != nil {
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		return RenderJobResult{}, service.fail(ctx, sc, domain.NewInternalError())
	}
	now := domain.NewTimestamp(sc.start)
	job := domain.NewQueuedRenderJob(
		jobID,
		principal.TenantID,
		principal.ClientAPIKeyID,
		command.operation,
		command.model,
		command.prompt,
		command.inputs,
		command.mask,
		account.ID,
		account.Credential.Version,
		fingerprint,
		command.idempotencyKey,
		now,
	)
	// Prompt is held only for worker execution via durable job row in-memory;
	// public wire projection never re-emits it (transport omits the field).

	persisted, err := service.jobs.Create(ctx, ports.RenderJobCreation{Principal: principal, Job: job})
	if err != nil {
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		return RenderJobResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}

	if _, err := service.queue.Enqueue(ctx, ports.SafeJobReference{
		TenantID: domain.Identifier(principal.TenantID),
		JobID:    persisted.JobID,
	}); err != nil {
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		return RenderJobResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}

	if err := service.replay.Complete(ctx, identity, ports.RenderReplayResult{Job: persisted}); err != nil {
		service.release(ctx, reservation)
		return RenderJobResult{}, service.fail(ctx, sc, domain.NewIdempotencyUncertain())
	}

	service.release(ctx, reservation)
	service.observeSuccess(ctx, sc, ports.AuditRenderJobCreated, principal, persisted, 202)
	return RenderJobResult{Job: persisted, RequestID: sc.requestID}, nil
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

	action := ports.AuditRenderJobCanceled
	if job.Lifecycle == domain.JobCancelRequested {
		action = ports.AuditRenderJobCanceled
	}
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

	claim, err := service.jobs.ClaimWorker(ctx, ref, ports.WorkerLease{WorkerID: workerID})
	if err != nil {
		// Concurrent claimant / terminal / cancel — discard without Provider work.
		if errors.Is(err, domain.ErrJobNotClaimable) || errors.Is(err, ports.ErrRenderJobNotVisible) {
			return nil
		}
		return err
	}
	job := claim.Job
	fence := claim.FencingToken

	// Queued cancel may have won; re-load and exit without Provider.
	if job.Lifecycle == domain.JobCancelRequested || job.Lifecycle.Terminal() {
		return nil
	}

	// Hard same-Tenant account lease for the entire execution.
	if err := service.jobs.BindAccountLease(ctx, ref, fence, job.ProviderAccountID); err != nil {
		_, _ = service.jobs.Transition(ctx, ports.FencedTransition{
			JobRef:       ref,
			FencingToken: fence,
			To:           domain.JobFailed,
			FailureStage: domain.StageRouting,
			FailureClass: domain.ErrCodeAccountNotUsable,
			CommitStatus: domain.CommitNotStarted,
			ClearLease:   true,
		})
		return nil
	}

	// Worker principal for Vault/Asset visibility is synthetic same-Tenant.
	principal := domain.SecurityPrincipal{
		TenantID:       job.TenantID,
		ClientAPIKeyID: job.ClientAPIKeyID,
		Scopes:         domain.NewScopeSet(domain.ScopeJobsManage, domain.ScopeAssetsRead, domain.ScopeAssetsWrite),
	}

	// Recheck cancel before payload.
	current, err := service.jobs.Load(ctx, ref)
	if err != nil {
		return err
	}
	if current.Lifecycle == domain.JobCancelRequested {
		_, _ = service.jobs.Transition(ctx, ports.FencedTransition{
			JobRef:       ref,
			FencingToken: fence,
			To:           domain.JobCanceled,
			CommitStatus: domain.CommitNotStarted,
			ClearLease:   true,
		})
		return nil
	}

	// Attempt ledger before payload.
	attempt := domain.UpstreamAttempt{
		ID:                domain.NewAttemptID(job.JobID, 1),
		ProviderAccountID: job.ProviderAccountID,
		CredentialVersion: job.CredentialVersion,
		CommitStatus:      domain.CommitNotStarted,
		Sequence:          1,
		CreatedAt:         domain.NewTimestamp(service.clock.Now()),
		UpdatedAt:         domain.NewTimestamp(service.clock.Now()),
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
			UpdatedAt: domain.NewTimestamp(service.clock.Now()),
		},
	}); err != nil {
		if errors.Is(err, domain.ErrStaleFence) {
			return nil
		}
		return err
	}

	// Vault authorize (presence) then Adapter render — no plaintext to application.
	if _, err := service.vault.Validate(ctx, ports.CredentialValidation{
		Principal: principal,
		AccountID: job.ProviderAccountID,
		Version:   job.CredentialVersion,
	}); err != nil {
		status := domain.CommitNotStarted
		_, _ = service.jobs.Transition(ctx, ports.FencedTransition{
			JobRef:       ref,
			FencingToken: fence,
			To:           domain.JobFailed,
			FailureStage: domain.StageDependency,
			FailureClass: domain.ErrCodeDependencyUnavailable,
			CommitStatus: status,
			ClearLease:   true,
		})
		return nil
	}

	// Mark not_committed immediately before payload send boundary.
	attempt.CommitStatus = domain.CommitNotCommitted
	attempt.PayloadSent = false
	attempt.UpdatedAt = domain.NewTimestamp(service.clock.Now())
	if _, err := service.jobs.ObserveAttempt(ctx, ports.AttemptObservation{
		JobRef:       ref,
		FencingToken: fence,
		Attempt:      attempt,
		Phase:        domain.PhaseUpstream,
		CommitStatus: domain.CommitNotCommitted,
	}); err != nil {
		if errors.Is(err, domain.ErrStaleFence) {
			return nil
		}
		return err
	}

	// Payload send boundary: once Adapter is invoked, missing response → unknown.
	attempt.PayloadSent = true
	outcome, renderErr := service.render.Render(ctx, ports.RenderCommand{
		Principal: principal,
		AccountID: job.ProviderAccountID,
		Version:   job.CredentialVersion,
		Prompt:    job.Prompt,
		Invocation: domain.RenderInvocation{
			TenantID:          job.TenantID,
			JobID:             job.JobID,
			AttemptID:         attempt.ID,
			Operation:         job.Operation,
			Model:             job.Model,
			ProviderAccountID: job.ProviderAccountID,
			CredentialVersion: job.CredentialVersion,
		},
	})

	if renderErr != nil {
		// After payload transmission, absence of authoritative non-commit is unknown.
		commit := domain.CommitUnknown
		if errors.Is(renderErr, ports.ErrRenderAdapterUnavailable) {
			// Fail-closed adapter before any Provider is not_committed only if
			// we treat unavailable adapter as no payload accepted — still
			// conservative: if PayloadSent, unknown.
			commit = domain.CommitUnknown
		}
		attempt.CommitStatus = commit
		_, _ = service.jobs.ObserveAttempt(ctx, ports.AttemptObservation{
			JobRef:       ref,
			FencingToken: fence,
			Attempt:      attempt,
			Phase:        domain.PhaseUpstream,
			CommitStatus: commit,
		})
		_, _ = service.jobs.Transition(ctx, ports.FencedTransition{
			JobRef:       ref,
			FencingToken: fence,
			To:           domain.JobFailed,
			FailureStage: domain.StageDependency,
			FailureClass: domain.ErrCodeDependencyUnavailable,
			CommitStatus: commit,
			ClearLease:   true,
		})
		return nil
	}

	switch outcome.Class {
	case domain.RenderOutcomeNotCommitted:
		// Authoritative pre-commit rejection: fail without replacement in this slice.
		attempt.CommitStatus = domain.CommitNotCommitted
		_, _ = service.jobs.Transition(ctx, ports.FencedTransition{
			JobRef:       ref,
			FencingToken: fence,
			To:           domain.JobFailed,
			FailureStage: domain.StageInternal,
			FailureClass: domain.ErrCodeInternal,
			CommitStatus: domain.CommitNotCommitted,
			ClearLease:   true,
		})
		return nil
	case domain.RenderOutcomeUnknown:
		attempt.CommitStatus = domain.CommitUnknown
		_, _ = service.jobs.Transition(ctx, ports.FencedTransition{
			JobRef:       ref,
			FencingToken: fence,
			To:           domain.JobFailed,
			FailureStage: domain.StageRecovery,
			FailureClass: domain.ErrCodeInternal,
			CommitStatus: domain.CommitUnknown,
			ClearLease:   true,
		})
		return nil
	case domain.RenderOutcomeSuccess, domain.RenderOutcomeCommitted, domain.RenderOutcomeStorageCapLater:
		// fall through to capture
	default:
		attempt.CommitStatus = domain.CommitUnknown
		_, _ = service.jobs.Transition(ctx, ports.FencedTransition{
			JobRef:       ref,
			FencingToken: fence,
			To:           domain.JobFailed,
			FailureStage: domain.StageInternal,
			FailureClass: domain.ErrCodeInternal,
			CommitStatus: domain.CommitUnknown,
			ClearLease:   true,
		})
		return nil
	}

	// Cancel race after result: if cancel_requested won before capture, drain to canceled.
	current, err = service.jobs.Load(ctx, ref)
	if err != nil {
		return err
	}
	if current.Lifecycle == domain.JobCancelRequested {
		// Spec: if result already in hand we still capture then decide; for
		// simplicity with CAS, cancel_requested before capture suppresses completed.
		_, _ = service.jobs.Transition(ctx, ports.FencedTransition{
			JobRef:       ref,
			FencingToken: fence,
			To:           domain.JobCanceled,
			CommitStatus: domain.CommitCommitted,
			ClearLease:   true,
		})
		return nil
	}

	outputs := outcome.Outputs
	if len(outputs) == 0 {
		outputs = [][]byte{domain.MinimalPNG()}
	}
	contentType := outcome.ContentType
	if contentType == "" {
		contentType = domain.DefaultOutputContentType
	}

	entries := make([]domain.OutputEntry, 0, len(outputs))
	for i, data := range outputs {
		checksum := domain.StagingChecksum(data)
		entries = append(entries, domain.OutputEntry{
			ID:            domain.NewOutputEntryID(job.JobID, i),
			Position:      i,
			DeliveryState: domain.OutputPending,
			ContentType:   contentType,
			ByteSize:      int64(len(data)),
			Checksum:      checksum,
		})
	}
	manifest := domain.ResultManifest{
		ID:              domain.NewResultManifestID(attempt.ID),
		AttemptID:       attempt.ID,
		Entries:         entries,
		StagingChecksum: entries[0].Checksum,
		CapturedAt:      domain.NewTimestamp(service.clock.Now()),
	}

	// Stage content in-memory via a side channel on the service path: store
	// staged bytes in a process-local map keyed by entry for placement.
	service.stageOutputs(job.JobID, outputs)

	if _, err := service.jobs.CaptureManifest(ctx, ports.ManifestCapture{
		JobRef:       ref,
		FencingToken: fence,
		Manifest:     manifest,
		Phase:        domain.PhasePlacingOutput,
	}); err != nil {
		if errors.Is(err, domain.ErrStaleFence) {
			return nil
		}
		return err
	}

	// Place each output Asset by stable placement key before completed.
	jobAfter, err := service.jobs.Load(ctx, ref)
	if err != nil {
		return err
	}
	for i, entry := range jobAfter.OutputEntries {
		data := outputs[i]
		if outcome.Class == domain.RenderOutcomeStorageCapLater {
			_, _ = service.jobs.PlaceOutput(ctx, ports.PlacementRequest{
				JobRef:              ref,
				FencingToken:        fence,
				EntryID:             entry.ID,
				DeliveryStateForced: domain.OutputPending,
				FailureClass:        string(domain.ErrCodeStorageCapExceeded),
			})
			continue
		}
		if placeErr := service.placeOutputBytes(ctx, principal, jobAfter, entry, data, fence); placeErr != nil {
			// Storage cap: remain completed with pending delivery later.
			if errors.Is(placeErr, ports.ErrStorageCapExceeded) {
				_, _ = service.jobs.PlaceOutput(ctx, ports.PlacementRequest{
					JobRef:              ref,
					FencingToken:        fence,
					EntryID:             entry.ID,
					DeliveryStateForced: domain.OutputPending,
					FailureClass:        string(domain.ErrCodeStorageCapExceeded),
				})
				continue
			}
			_, _ = service.jobs.Transition(ctx, ports.FencedTransition{
				JobRef:       ref,
				FencingToken: fence,
				To:           domain.JobFailed,
				FailureStage: domain.StageAsset,
				FailureClass: domain.ErrCodeInternal,
				CommitStatus: domain.CommitCommitted,
				ClearLease:   true,
			})
			return nil
		}
	}

	_, err = service.jobs.Transition(ctx, ports.FencedTransition{
		JobRef:       ref,
		FencingToken: fence,
		To:           domain.JobCompleted,
		CommitStatus: domain.CommitCommitted,
		Progress: domain.JobProgress{
			Source:    domain.ProgressEstimated,
			Value:     100,
			UpdatedAt: domain.NewTimestamp(service.clock.Now()),
		},
		ClearLease: true,
	})
	if err != nil && !errors.Is(err, domain.ErrStaleFence) {
		return err
	}
	return nil
}

// staged holds process-local staged output bytes for placement/retry in the
// foundation slice (no separate staging backend yet).
var stagedOutputs = struct {
	mu sync.Mutex
	m  map[domain.Identifier][][]byte
}{m: make(map[domain.Identifier][][]byte)}

func (service *RenderService) stageOutputs(jobID domain.Identifier, outputs [][]byte) {
	copied := make([][]byte, len(outputs))
	for i, data := range outputs {
		buf := make([]byte, len(data))
		copy(buf, data)
		copied[i] = buf
	}
	stagedOutputs.mu.Lock()
	stagedOutputs.m[jobID] = copied
	stagedOutputs.mu.Unlock()
}

func (service *RenderService) stagedFor(jobID domain.Identifier, index int) ([]byte, bool) {
	stagedOutputs.mu.Lock()
	defer stagedOutputs.mu.Unlock()
	outputs, ok := stagedOutputs.m[jobID]
	if !ok || index < 0 || index >= len(outputs) {
		return nil, false
	}
	out := make([]byte, len(outputs[index]))
	copy(out, outputs[index])
	return out, true
}

func (service *RenderService) placeOutputBytes(
	ctx context.Context,
	principal domain.SecurityPrincipal,
	job domain.RenderJob,
	entry domain.OutputEntry,
	data []byte,
	fence domain.FencingToken,
) error {
	byteSize := int64(len(data))
	sum := sha256.Sum256(data)
	checksum := hex.EncodeToString(sum[:])
	hold := ports.AssetReservation{TenantID: job.TenantID, Bytes: byteSize}
	if err := service.assets.Reserve(ctx, hold); err != nil {
		return err
	}
	assetID, err := service.ids.New(domain.IdentifierKindAsset)
	if err != nil {
		_ = service.assets.Release(ctx, hold)
		return err
	}
	now := domain.NewTimestamp(service.clock.Now())
	asset := domain.Asset{
		ID:             domain.AssetID(assetID),
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
		return err
	}
	if _, err := service.assets.Commit(ctx, ports.AssetCreation{Principal: principal, Asset: asset, Reservation: hold}); err != nil {
		_ = service.assets.Release(ctx, hold)
		return err
	}
	_, err = service.jobs.PlaceOutput(ctx, ports.PlacementRequest{
		JobRef:       job.JobRef(),
		FencingToken: fence,
		EntryID:      entry.ID,
		Asset:        asset,
		Content:      data,
	})
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
		})
		if err != nil {
			return ports.PlacementResult{}, service.dependencyCanonical(err)
		}
		return result, domain.CanonicalError{}
	}
	data, ok := service.stagedFor(job.JobID, entry.Position)
	if !ok {
		// No staged bytes available: mark failed delivery without re-render.
		result, err := service.jobs.PlaceOutput(ctx, ports.PlacementRequest{
			JobRef:              job.JobRef(),
			FencingToken:        fence,
			EntryID:             entry.ID,
			DeliveryStateForced: domain.OutputFailed,
			FailureClass:        string(domain.ErrCodeInternal),
		})
		if err != nil {
			return ports.PlacementResult{}, service.dependencyCanonical(err)
		}
		return result, domain.CanonicalError{}
	}
	if err := service.placeOutputBytes(ctx, principal, job, entry, data, fence); err != nil {
		if errors.Is(err, ports.ErrStorageCapExceeded) {
			result, placeErr := service.jobs.PlaceOutput(ctx, ports.PlacementRequest{
				JobRef:              job.JobRef(),
				FencingToken:        fence,
				EntryID:             entry.ID,
				DeliveryStateForced: domain.OutputPending,
				FailureClass:        string(domain.ErrCodeStorageCapExceeded),
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
