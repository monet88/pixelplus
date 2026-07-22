// Package application owns Gateway use cases and retry ownership policy.
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

// Operation tokens for the Asset exchange spine. They match the stable
// operation ids consumed by telemetry and admission (#8 section 5.2).
const (
	operationCreateAsset     domain.OperationToken = "create_asset"
	operationGetAsset        domain.OperationToken = "get_asset"
	operationGetAssetContent domain.OperationToken = "get_asset_content"
)

// CreateAssetCommand is the typed upload request. Content is the raw uploaded
// bytes the transport extracted from the multipart file part; DeclaredType is
// the client-declared media type of that part. The application authenticates
// the presented key and derives Tenant authority server-side; client-supplied
// Tenant identity is never part of this command (#6 section 2.2, #13 3.2).
type CreateAssetCommand struct {
	PresentedKeyMaterial string
	Kind                 domain.AssetKind
	DeclaredType         string
	Content              []byte
	IdempotencyKey       string
	// RequestID is the server-owned correlation id created at the transport
	// boundary. The application never trusts a client-supplied value.
	RequestID domain.Identifier
	// OversizeBody reports that the transport observed an upload over the
	// L-ASSET-UPLOAD-MAX ceiling (admission step A2). It is carried as a flag so
	// the normative order A0 auth -> A1 scope -> A2 size holds in one place and
	// an unauthenticated oversize upload still fails as authentication_failed
	// (#8 section 6, #13 section 4.2.1).
	OversizeBody bool
	// MalformedBody reports that the transport could not parse the multipart
	// upload into a kind + file. It is a request_validation outcome evaluated
	// after A0-A2 so a malformed body from an unauthenticated caller never leaks
	// a distinguishable 400 before 401.
	MalformedBody bool
}

// GetAssetQuery is the typed read-one metadata request.
type GetAssetQuery struct {
	PresentedKeyMaterial string
	AssetID              domain.AssetID
	RequestID            domain.Identifier
}

// GetAssetContentQuery is the typed content-download request.
type GetAssetContentQuery struct {
	PresentedKeyMaterial string
	AssetID              domain.AssetID
	RequestID            domain.Identifier
}

// AssetResult carries one safe Asset metadata projection plus the server-owned
// request id.
type AssetResult struct {
	Asset     domain.Asset
	RequestID domain.Identifier
}

// AssetContentResult carries the same-Tenant Asset content plus the server-owned
// request id. The Asset metadata is included so the transport can set the
// canonical media type without re-reading the store.
type AssetContentResult struct {
	Content   ports.AssetContent
	RequestID domain.Identifier
}

// AssetService runs the protected Public API request spine for the immutable
// Asset exchange surface. It owns the normative gate order: authenticate (A0),
// scope and same-Tenant ownership on named ids (A1), request-size (A2) and
// request/content validation, then a replay ownership claim, then admission
// (A3-A5), then an atomic storage reservation, before any durable side effect
// (#20 section 5.5, #13 sections 4-6). Request-size and multipart-parse
// outcomes are observed at the transport boundary but carried as flags so this
// single normative order is enforced here.
type AssetService struct {
	principal  ports.PrincipalStore
	admission  ports.AdmissionStore
	replay     ports.AssetReplayStore
	metadata   ports.AssetMetadataStore
	content    ports.AssetContentStore
	audit      ports.AssetAuditRecorder
	telemetry  ports.TelemetryRecorder
	requestLog ports.RequestLogRecorder
	clock      ports.Clock
	ids        ports.IDGenerator
}

// AssetDependencies bundles the controlled ports this slice owns.
type AssetDependencies struct {
	Principal  ports.PrincipalStore
	Admission  ports.AdmissionStore
	Replay     ports.AssetReplayStore
	Metadata   ports.AssetMetadataStore
	Content    ports.AssetContentStore
	Audit      ports.AssetAuditRecorder
	Telemetry  ports.TelemetryRecorder
	RequestLog ports.RequestLogRecorder
	Clock      ports.Clock
	IDs        ports.IDGenerator
}

// NewAssetService validates and wires the Asset exchange spine dependencies.
func NewAssetService(dependencies AssetDependencies) (*AssetService, error) {
	switch {
	case dependencies.Principal == nil:
		return nil, errors.New("application: principal store is required")
	case dependencies.Admission == nil:
		return nil, errors.New("application: admission store is required")
	case dependencies.Replay == nil:
		return nil, errors.New("application: asset replay store is required")
	case dependencies.Metadata == nil:
		return nil, errors.New("application: asset metadata store is required")
	case dependencies.Content == nil:
		return nil, errors.New("application: asset content store is required")
	case dependencies.Audit == nil:
		return nil, errors.New("application: asset audit recorder is required")
	case dependencies.Telemetry == nil:
		return nil, errors.New("application: telemetry recorder is required")
	case dependencies.RequestLog == nil:
		return nil, errors.New("application: request log recorder is required")
	case dependencies.Clock == nil:
		return nil, errors.New("application: clock is required")
	case dependencies.IDs == nil:
		return nil, errors.New("application: ID generator is required")
	}
	return &AssetService{
		principal:  dependencies.Principal,
		admission:  dependencies.Admission,
		replay:     dependencies.Replay,
		metadata:   dependencies.Metadata,
		content:    dependencies.Content,
		audit:      dependencies.Audit,
		telemetry:  dependencies.Telemetry,
		requestLog: dependencies.RequestLog,
		clock:      dependencies.Clock,
		ids:        dependencies.IDs,
	}, nil
}

// CreateAsset runs the full protected spine and stores exactly one immutable
// Asset when this request wins the replay claim, after canonical validation and
// an atomic committed-plus-reserved storage reservation.
func (service *AssetService) CreateAsset(ctx context.Context, command CreateAssetCommand) (AssetResult, error) {
	sc := spineContext{operation: operationCreateAsset, requestID: service.resolveRequestID(command.RequestID), start: service.clock.Now()}

	principal, canonical, ok := service.authenticate(ctx, ports.PresentedClientAPIKey{Material: command.PresentedKeyMaterial})
	if !ok {
		return AssetResult{}, service.fail(ctx, sc, canonical)
	}
	sc.keyID = principal.ClientAPIKeyID

	// A1: scope. Upload requires assets.write.
	if !principal.Scopes.Has(domain.ScopeAssetsWrite) {
		return AssetResult{}, service.fail(ctx, sc, domain.NewForbidden())
	}

	// A2: request-size. An oversize upload is rejected only after authentication
	// and scope so the normative order A0 -> A1 -> A2 holds (#8 section 6).
	if command.OversizeBody {
		return AssetResult{}, service.fail(ctx, sc, domain.NewRequestTooLarge())
	}

	// Request validation (post-A2): multipart parse, required Idempotency-Key,
	// key length, and uploadable kind. These run before any content decode,
	// reservation, or replay claim.
	if command.MalformedBody {
		return AssetResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}
	if command.IdempotencyKey == "" {
		return AssetResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}
	if utf8.RuneCountInString(command.IdempotencyKey) > maxIdempotencyKeyLength {
		return AssetResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}
	if !command.Kind.UploadKind() {
		return AssetResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}

	// Canonical content validation (#13 section 4): format, decodability, and
	// pixel dimensions produce distinct outcomes before any Asset is stored. A
	// bad upload leaves no durable Asset and never reaches the replay claim,
	// admission, or a storage reservation.
	facts, err := domain.InspectImageContent(command.DeclaredType, command.Content)
	if err != nil {
		return AssetResult{}, service.fail(ctx, sc, contentCanonical(err))
	}

	byteSize := int64(len(command.Content))
	checksum := assetChecksum(command.Content)

	// Replay ownership claim BEFORE admission and reservation so a
	// terminal/in-progress/conflict replay never debits admission or reserves
	// storage (#20 section 5.5). Only a fresh claim proceeds; a fresh claim
	// rejected by a later gate is abandoned so a retry can re-claim.
	identity := domain.ReplayIdentity{
		Scope: domain.ReplayScope{
			TenantID:       principal.TenantID,
			ClientAPIKeyID: principal.ClientAPIKeyID,
			Key:            command.IdempotencyKey,
		},
		Fingerprint: domain.NewCreateAssetFingerprint(command.Kind, checksum, byteSize),
	}
	decision, err := service.replay.Claim(ctx, identity)
	if err != nil {
		return AssetResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}

	switch decision.Outcome {
	case ports.ReplayClaimed:
		// Sole executor: fall through to admission, reservation, then store.
	case ports.ReplayTerminal:
		// A matching terminal replay returns the original Asset without a new
		// durable side effect, so it emits no second asset.created audit event.
		asset := decision.TerminalAsset
		service.recordTelemetry(ctx, sc.operation, "", 201)
		service.recordRequestLog(ctx, sc.requestID, principal.ClientAPIKeyID, string(sc.operation), 201, "ok", sc.start)
		return AssetResult{Asset: asset, RequestID: sc.requestID}, nil
	case ports.ReplayInProgress:
		return AssetResult{}, service.fail(ctx, sc, domain.NewIdempotencyInProgress())
	case ports.ReplayConflict:
		return AssetResult{}, service.fail(ctx, sc, domain.NewIdempotencyConflict())
	case ports.ReplayUncertain:
		return AssetResult{}, service.fail(ctx, sc, domain.NewIdempotencyUncertain())
	default:
		return AssetResult{}, service.fail(ctx, sc, domain.NewInternalError())
	}

	// A3-A5: rate, concurrency, quota admission in normative order. A rejection
	// abandons the fresh replay claim so this request debited nothing durable.
	reservation, canonical, ok := service.admit(ctx, principal, operationCreateAsset)
	if !ok {
		service.abandon(ctx, identity)
		return AssetResult{}, service.fail(ctx, sc, canonical)
	}

	// Atomic committed-plus-reserved storage reservation before any durable
	// Asset (#13 section 6.1). A cap overrun fails closed with the distinct
	// storage_cap_exceeded outcome; admission is released and the fresh claim
	// abandoned so a later retry can re-claim without a double debit.
	hold := ports.AssetReservation{TenantID: principal.TenantID, Bytes: byteSize}
	if err := service.metadata.Reserve(ctx, hold); err != nil {
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		return AssetResult{}, service.fail(ctx, sc, service.reservationCanonical(err))
	}

	assetID, err := service.newAssetID()
	if err != nil {
		service.releaseStorage(ctx, hold)
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		return AssetResult{}, service.fail(ctx, sc, domain.NewInternalError())
	}
	asset := domain.NewUploadedAsset(assetID, principal.TenantID, command.Kind, facts, byteSize, checksum, domain.NewTimestamp(sc.start))

	// Store content first so a committed metadata row never references missing
	// bytes; a content failure releases the reservation and abandons the claim.
	if err := service.content.Put(ctx, assetID, command.Content); err != nil {
		service.releaseStorage(ctx, hold)
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		return AssetResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}

	persisted, err := service.metadata.Commit(ctx, ports.AssetCreation{Principal: principal, Asset: asset, Reservation: hold})
	if err != nil {
		service.releaseStorage(ctx, hold)
		service.release(ctx, reservation)
		service.abandon(ctx, identity)
		return AssetResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}

	if err := service.replay.Complete(ctx, identity, ports.AssetReplayResult{Asset: persisted}); err != nil {
		// The Asset is already durably committed, so the create side effect
		// happened but its terminal replay record is uncertain. Return
		// idempotency_uncertain (no-steal) and do NOT abandon the claim:
		// abandoning would let a retry create a duplicate Asset for the same
		// scoped key (#20 section 5.5). The admission reservation is released.
		service.release(ctx, reservation)
		return AssetResult{}, service.fail(ctx, sc, domain.NewIdempotencyUncertain())
	}

	service.release(ctx, reservation)
	service.observeSuccess(ctx, sc, ports.AuditAssetCreated, principal, persisted.ID, 201)
	return AssetResult{Asset: persisted, RequestID: sc.requestID}, nil
}

// GetAsset reads one owning-Tenant Asset's metadata. Foreign, unknown, expired,
// and deleted identifiers share the same non-enumerating resource_not_found
// outcome before any admission or protected access (#13 sections 4.5, 5.5, 8).
func (service *AssetService) GetAsset(ctx context.Context, query GetAssetQuery) (AssetResult, error) {
	sc := spineContext{operation: operationGetAsset, requestID: service.resolveRequestID(query.RequestID), start: service.clock.Now()}

	principal, canonical, ok := service.authenticate(ctx, ports.PresentedClientAPIKey{Material: query.PresentedKeyMaterial})
	if !ok {
		return AssetResult{}, service.fail(ctx, sc, canonical)
	}
	sc.keyID = principal.ClientAPIKeyID

	if !principal.Scopes.Has(domain.ScopeAssetsRead) {
		return AssetResult{}, service.fail(ctx, sc, domain.NewForbidden())
	}

	asset, err := service.metadata.Visible(ctx, principal, query.AssetID)
	if err != nil {
		return AssetResult{}, service.fail(ctx, sc, service.visibilityCanonical(err))
	}

	reservation, canonical, ok := service.admit(ctx, principal, operationGetAsset)
	if !ok {
		return AssetResult{}, service.fail(ctx, sc, canonical)
	}
	service.release(ctx, reservation)

	service.observeSuccess(ctx, sc, ports.AuditAssetRead, principal, asset.ID, 200)
	return AssetResult{Asset: asset, RequestID: sc.requestID}, nil
}

// GetAssetContent returns the bytes of one owning-Tenant, still-retrievable
// Asset. A foreign, unknown, expired, or deleted id yields the same
// non-enumerating resource_not_found and serves no bytes (#13 sections 5.3-5.5,
// vault spec 5.4.2: a foreign/unknown asset_id never reaches content).
func (service *AssetService) GetAssetContent(ctx context.Context, query GetAssetContentQuery) (AssetContentResult, error) {
	sc := spineContext{operation: operationGetAssetContent, requestID: service.resolveRequestID(query.RequestID), start: service.clock.Now()}

	principal, canonical, ok := service.authenticate(ctx, ports.PresentedClientAPIKey{Material: query.PresentedKeyMaterial})
	if !ok {
		return AssetContentResult{}, service.fail(ctx, sc, canonical)
	}
	sc.keyID = principal.ClientAPIKeyID

	if !principal.Scopes.Has(domain.ScopeAssetsRead) {
		return AssetContentResult{}, service.fail(ctx, sc, domain.NewForbidden())
	}

	// Ownership + lifecycle (expiry/deletion) resolve in the metadata store
	// authority before any content access, so an expired/deleted/foreign id
	// serves no bytes and never reaches the content store.
	asset, err := service.metadata.Visible(ctx, principal, query.AssetID)
	if err != nil {
		return AssetContentResult{}, service.fail(ctx, sc, service.visibilityCanonical(err))
	}

	reservation, canonical, ok := service.admit(ctx, principal, operationGetAssetContent)
	if !ok {
		return AssetContentResult{}, service.fail(ctx, sc, canonical)
	}

	content, err := service.content.Fetch(ctx, principal, asset.ID)
	if err != nil {
		service.release(ctx, reservation)
		return AssetContentResult{}, service.fail(ctx, sc, service.visibilityCanonical(err))
	}
	service.release(ctx, reservation)

	service.observeSuccess(ctx, sc, ports.AuditAssetContentRetrieved, principal, asset.ID, 200)
	return AssetContentResult{Content: content, RequestID: sc.requestID}, nil
}

// authenticate resolves the Security Principal. All authentication failures map
// to one indistinguishable canonical outcome.
func (service *AssetService) authenticate(ctx context.Context, key ports.PresentedClientAPIKey) (domain.SecurityPrincipal, domain.CanonicalError, bool) {
	principal, err := service.principal.Authenticate(ctx, key)
	if err != nil {
		return domain.SecurityPrincipal{}, domain.NewAuthenticationFailed(), false
	}
	if !principal.Valid() {
		return domain.SecurityPrincipal{}, domain.NewAuthenticationFailed(), false
	}
	return principal, domain.CanonicalError{}, true
}

// admit runs the A3-A5 admission gates and maps the failing stage to its
// canonical code without inventing a new mapping.
func (service *AssetService) admit(ctx context.Context, principal domain.SecurityPrincipal, operation domain.OperationToken) (ports.AdmissionReservation, domain.CanonicalError, bool) {
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

// release settles an admission reservation, ignoring a nil reservation.
func (service *AssetService) release(ctx context.Context, reservation ports.AdmissionReservation) {
	_ = service.admission.Reconcile(ctx, reservation)
}

// releaseStorage releases an un-committed storage reservation exactly once.
func (service *AssetService) releaseStorage(ctx context.Context, hold ports.AssetReservation) {
	_ = service.metadata.Release(ctx, hold)
}

// abandon releases a fresh replay claim this request will not carry to a
// durable side effect because a later same-request gate rejected it.
func (service *AssetService) abandon(ctx context.Context, identity domain.ReplayIdentity) {
	_ = service.replay.Abandon(ctx, identity)
}

// visibilityCanonical maps an Asset visibility failure to the non-enumerating
// resource_not_found outcome, or a fail-closed dependency error.
func (service *AssetService) visibilityCanonical(err error) domain.CanonicalError {
	if errors.Is(err, ports.ErrAssetNotVisible) {
		return domain.NewResourceNotFound()
	}
	return service.dependencyCanonical(err)
}

// reservationCanonical maps a storage reservation failure to the distinct
// storage_cap_exceeded outcome, or a fail-closed dependency error.
func (service *AssetService) reservationCanonical(err error) domain.CanonicalError {
	if errors.Is(err, ports.ErrStorageCapExceeded) {
		return domain.NewStorageCapExceeded()
	}
	return service.dependencyCanonical(err)
}

// dependencyCanonical maps an infrastructure failure to a fail-closed dependency
// code, never leaking the underlying cause.
func (service *AssetService) dependencyCanonical(err error) domain.CanonicalError {
	if errors.Is(err, ports.ErrDependencyUnavailable) {
		return domain.NewDependencyUnavailable()
	}
	return domain.NewInternalError()
}

// contentCanonical maps a canonical content-validation failure to its distinct
// canonical error. Each validation outcome is a separate class so a raw-size,
// format, decode, and dimension failure are never relabeled as one another
// (#13 section 4.4, I-ASSET-SIZE-DISTINCT).
func contentCanonical(err error) domain.CanonicalError {
	switch {
	case errors.Is(err, domain.ErrUnsupportedFormat):
		return domain.NewUnsupportedFormat()
	case errors.Is(err, domain.ErrInvalidDimensions):
		return domain.NewInvalidDimensions()
	case errors.Is(err, domain.ErrInvalidImage):
		return domain.NewInvalidImage()
	default:
		return domain.NewInvalidImage()
	}
}

// fail records safe telemetry and the audit-safe request log, then returns the
// canonical error carrying the server-owned request id.
func (service *AssetService) fail(ctx context.Context, sc spineContext, canonical domain.CanonicalError) domain.CanonicalError {
	canonical = canonical.WithRequestID(sc.requestID)
	statusCode := canonical.HTTPStatus()
	service.recordTelemetry(ctx, sc.operation, canonical.Code, statusCode)
	service.recordRequestLog(ctx, sc.requestID, sc.keyID, string(sc.operation), statusCode, string(canonical.Code), sc.start)
	return canonical
}

// observeSuccess records the audit, telemetry, and request-log projections for
// a successful Asset operation.
func (service *AssetService) observeSuccess(ctx context.Context, sc spineContext, action ports.AssetAuditAction, principal domain.SecurityPrincipal, assetID domain.AssetID, statusCode int) {
	_ = service.audit.Record(ctx, ports.AssetAuditEvent{
		Action:         action,
		TenantID:       principal.TenantID,
		ClientAPIKeyID: principal.ClientAPIKeyID,
		AssetID:        assetID,
		RequestID:      sc.requestID,
		Outcome:        "success",
	})
	service.recordTelemetry(ctx, sc.operation, "", statusCode)
	service.recordRequestLog(ctx, sc.requestID, principal.ClientAPIKeyID, string(sc.operation), statusCode, "ok", sc.start)
}

func (service *AssetService) recordTelemetry(ctx context.Context, operation domain.OperationToken, code domain.ErrorCode, statusCode int) {
	_ = service.telemetry.Record(ctx, ports.TelemetryEvent{
		Operation:  operation,
		Code:       code,
		StatusCode: statusCode,
	})
}

func (service *AssetService) recordRequestLog(ctx context.Context, requestID domain.Identifier, keyID domain.ClientAPIKeyID, action string, statusCode int, message string, start time.Time) {
	_ = service.requestLog.Record(ctx, ports.RequestLog{
		RequestID:  requestID,
		UserID:     keyID,
		Action:     action,
		DurationMS: service.durationMS(start),
		StatusCode: statusCode,
		Message:    message,
	})
}

func (service *AssetService) durationMS(start time.Time) int64 {
	elapsed := service.clock.Now().Sub(start)
	if elapsed < 0 {
		return 0
	}
	return elapsed.Milliseconds()
}

// resolveRequestID keeps the server-owned request id created at the transport
// boundary, or mints one when the transport could not. The id is never taken
// from client input.
func (service *AssetService) resolveRequestID(boundaryID domain.Identifier) domain.Identifier {
	if boundaryID != "" {
		return boundaryID
	}
	id, err := service.ids.New(domain.IdentifierKindRequest)
	if err != nil {
		return ""
	}
	return id
}

func (service *AssetService) newAssetID() (domain.AssetID, error) {
	id, err := service.ids.New(domain.IdentifierKindAsset)
	if err != nil {
		return "", err
	}
	return domain.AssetID(id), nil
}

// assetChecksum computes the non-secret content digest recorded on the Asset
// for integrity and dedupe. The prefix keeps the algorithm explicit on the
// wire (#13 section 3.1: checksum is a non-secret content digest).
func assetChecksum(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}
