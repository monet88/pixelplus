package httptransport

import (
	"net/http"

	"github.com/monet88/pixelplus/apps/gateway/internal/application"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// assetWire mirrors the frozen Public API v1 Asset schema exactly. Required
// fields are always present; width, height, expires_at, and source_job_id are
// optional and omitted when unset. source_job_id is only populated for a
// `generated` Asset, which never reaches the upload/read surface with a value
// here. additionalProperties is false on the wire, so no extra field is emitted
// and tenant_id stays server-side authority (#13 section 3.1).
type assetWire struct {
	AssetID        string `json:"asset_id"`
	Kind           string `json:"kind"`
	ContentType    string `json:"content_type"`
	ByteSize       int64  `json:"byte_size"`
	Width          int    `json:"width,omitempty"`
	Height         int    `json:"height,omitempty"`
	Checksum       string `json:"checksum"`
	Origin         string `json:"origin"`
	CreatedAt      string `json:"created_at"`
	ExpiresAt      string `json:"expires_at,omitempty"`
	SourceJobID    string `json:"source_job_id,omitempty"`
	RetentionClass string `json:"retention_class"`
}

func toAssetWire(asset domain.Asset) assetWire {
	return assetWire{
		AssetID:        string(asset.ID),
		Kind:           string(asset.Kind),
		ContentType:    asset.ContentType,
		ByteSize:       asset.ByteSize,
		Width:          asset.Width,
		Height:         asset.Height,
		Checksum:       asset.Checksum,
		Origin:         string(asset.Origin),
		CreatedAt:      timestampString(asset.CreatedAt),
		ExpiresAt:      timestampString(asset.ExpiresAt),
		SourceJobID:    string(asset.SourceJobID),
		RetentionClass: string(asset.RetentionClass),
	}
}

func writeAsset(writer http.ResponseWriter, statusCode int, asset domain.Asset) {
	writeJSON(writer, statusCode, toAssetWire(asset))
}

// writeAssetContent streams the immutable Asset bytes with the canonical media
// type resolved by the store. It never sets a JSON content type and uses
// no-store caching so a retrieval does not persist bytes in a shared cache.
func writeAssetContent(writer http.ResponseWriter, result application.AssetContentResult) {
	contentType := result.Content.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", contentType)
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(result.Content.Data)
}
