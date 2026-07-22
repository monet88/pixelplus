// Package ports owns application-facing outbound Gateway contracts.
package ports

import (
	"context"
	"errors"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// Typed Asset port errors let the application map an infrastructure outcome to
// one canonical code without parsing driver strings (mirrors the request-spine
// error style in ports/request_spine.go).
var (
	// ErrAssetNotVisible reports that an Asset id is foreign, unknown, expired,
	// or deleted from the principal's perspective. It is the single
	// non-enumerating visibility failure for the Asset surface so a caller can
	// never distinguish "exists in another Tenant" from "never existed"
	// (#13 sections 4.5, 5.5, 8; I-ASSET-NON-ENUM).
	ErrAssetNotVisible = errors.New("asset not visible")
	// ErrStorageCapExceeded reports that an atomic storage reservation would
	// exceed the Tenant committed + reserved byte or object-count cap. It is
	// distinct from a per-request size violation and admission quota
	// (#13 section 6, I-ASSET-STORAGE-CAP).
	ErrStorageCapExceeded = errors.New("tenant storage cap exceeded")
)

// AssetReservation identifies an atomic committed-plus-reserved storage hold
// acquired before a durable Asset is created. It is converted to committed
// usage on commit or released exactly once on failure, and it never admits new
// storage by forgetting an uncertain reservation (#13 section 6.1).
type AssetReservation struct {
	TenantID domain.TenantID
	Bytes    int64
}

// AssetCreation is the typed command to persist a validated, immutable Asset
// for the owning Tenant. The application derives Tenant identity from the
// Security Principal; no client-supplied Tenant authority is trusted
// (#6 section 2.2, #13 section 3.2).
type AssetCreation struct {
	Principal   domain.SecurityPrincipal
	Asset       domain.Asset
	Reservation AssetReservation
}

// AssetMetadataStore owns durable Asset metadata, atomic Tenant storage
// reservation, same-Tenant non-enumerating visibility, and one-time accounting
// release on deletion or expiry. Reserve acquires a committed-plus-reserved
// hold atomically and MUST fail with ErrStorageCapExceeded rather than admit an
// overrun; Commit converts the hold to committed usage exactly once; Release
// settles an un-committed hold exactly once (#13 section 6.1). Visible returns
// ErrAssetNotVisible for foreign, unknown, expired, and deleted identifiers so
// the outcome is indistinguishable (#13 sections 4.5, 5.5).
type AssetMetadataStore interface {
	Reserve(context.Context, AssetReservation) error
	Commit(context.Context, AssetCreation) (domain.Asset, error)
	Release(context.Context, AssetReservation) error
	Visible(context.Context, domain.SecurityPrincipal, domain.AssetID) (domain.Asset, error)
}

// AssetContentStore owns immutable Asset content bytes. Put stores the bytes
// for a committed Asset; Fetch returns the bytes for a same-Tenant, still
// retrievable Asset. A foreign, unknown, expired, or deleted id never reaches
// content and returns ErrAssetNotVisible (#13 section 5.4 of the vault spec:
// Asset bytes are released only after ownership, scope, expiry, and deletion
// gates; a foreign/unknown asset_id never reaches content decryption).
type AssetContentStore interface {
	Put(context.Context, domain.AssetID, []byte) error
	Fetch(context.Context, domain.SecurityPrincipal, domain.AssetID) (AssetContent, error)
}

// AssetContent is the safe content projection returned to a same-Tenant
// retrieval. It carries only the bytes and their canonical media type.
type AssetContent struct {
	ContentType string
	Data        []byte
}

// AssetAuditAction names an Asset product/security audit event.
type AssetAuditAction string

// Audit actions emitted by the Asset exchange spine.
const (
	AuditAssetCreated          AssetAuditAction = "asset.created"
	AuditAssetRead             AssetAuditAction = "asset.read"
	AuditAssetContentRetrieved AssetAuditAction = "asset.content_retrieved"
)

// AssetAuditEvent is a secret-free product/security audit projection for the
// Asset surface. It carries safe actor, Tenant, resource, and outcome fields
// only and never Asset bytes or a foreign asset_id (#13 section 8.5,
// I-ASSET-REDACT).
type AssetAuditEvent struct {
	Action         AssetAuditAction
	TenantID       domain.TenantID
	ClientAPIKeyID domain.ClientAPIKeyID
	AssetID        domain.AssetID
	RequestID      domain.Identifier
	Outcome        string
}

// AssetAuditRecorder writes the secret-free Asset audit projection.
type AssetAuditRecorder interface {
	Record(context.Context, AssetAuditEvent) error
}

// AssetReplayDecision is the result of an atomic Asset idempotency claim.
// TerminalAsset carries the prior durable Asset when Outcome is ReplayTerminal
// so the original create result can be replayed without re-persisting or
// re-reserving storage (#20 section 5.5).
type AssetReplayDecision struct {
	Outcome       ReplayOutcome
	TerminalAsset domain.Asset
}

// AssetReplayResult is the terminal projection recorded once an owning upload
// completes its durable side effect, so later matching replays are stable.
type AssetReplayResult struct {
	Asset domain.Asset
}

// AssetReplayStore performs the atomic idempotency claim, fingerprint match,
// and terminal replay for the Asset upload surface. It enforces the no-steal
// rule and one accepted owner, mirroring the Provider Account ReplayStore
// (#20 section 5.5). Abandon releases a fresh claim that a later same-request
// gate rejected so a legitimate retry can re-claim without having reserved
// storage; it never removes a terminal record.
type AssetReplayStore interface {
	Claim(context.Context, domain.ReplayIdentity) (AssetReplayDecision, error)
	Complete(context.Context, domain.ReplayIdentity, AssetReplayResult) error
	Abandon(context.Context, domain.ReplayIdentity) error
}
