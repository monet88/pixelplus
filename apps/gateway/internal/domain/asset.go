package domain

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image/jpeg"
	"image/png"
	"time"
)

// AssetID is the stable, unguessable, non-secret Asset identifier. It is safe
// to expose to the owning Tenant only and never confirms existence across
// Tenants (#13 section 2, I-ASSET-NON-ENUM).
type AssetID string

// AssetKind names the immutable role of an Asset. `output` is produced by a
// Render Job (#14); uploads create `input` or `mask` (#13 section 2).
type AssetKind string

// Asset kinds.
const (
	AssetKindInput  AssetKind = "input"
	AssetKindMask   AssetKind = "mask"
	AssetKindOutput AssetKind = "output"
)

// UploadKind reports whether the kind is a client-uploadable role. Only
// `input` and `mask` may be created through the upload surface; `output` is
// produced by a Render Job (#13 section 2, section 3.2).
func (kind AssetKind) UploadKind() bool {
	return kind == AssetKindInput || kind == AssetKindMask
}

// AssetOrigin records how an Asset came to exist.
type AssetOrigin string

// Asset origins.
const (
	AssetOriginUploaded  AssetOrigin = "uploaded"
	AssetOriginGenerated AssetOrigin = "generated"
)

// RetentionClass is a named lifetime budget after which an Asset is no longer
// downloadable. Numeric windows are #17-tunable; implementations cite the class
// id rather than inventing parallel magic numbers (#13 section 5.2).
type RetentionClass string

// Named retention classes (#13 section 5.2).
const (
	RetentionClassOutput    RetentionClass = "RETAIN-OUTPUT"
	RetentionClassInput     RetentionClass = "RETAIN-INPUT"
	RetentionClassEphemeral RetentionClass = "RETAIN-EPHEMERAL"
)

// MVP retention windows. D-ASSET-CAP-TUNE / #17 may retune the numbers, not the
// bounded-lifetime obligation (#13 section 5.2).
const (
	retentionWindowInput  = 24 * time.Hour
	retentionWindowOutput = 7 * 24 * time.Hour
)

// Asset content dimension bounds. Exact numbers are #17/#18 tunable; the
// canonical validation outcomes are locked (#13 section 4.2).
const (
	// AssetMinDimension is the smallest accepted pixel width/height.
	AssetMinDimension = 1
	// AssetMaxDimension is the largest accepted pixel width/height for the MVP.
	AssetMaxDimension = 8192
)

// Supported canonical image media types (MVP intent PNG/JPEG/WebP, #13 section
// 4.1). The exact set is #18/#20; this slice locks the validation outcomes.
const (
	ContentTypePNG  = "image/png"
	ContentTypeJPEG = "image/jpeg"
	ContentTypeWebP = "image/webp"
)

// Canonical content-validation failures. The application maps each to its
// canonical error so error-code strings stay owned by the error layer while the
// distinct validation outcomes are locked here (#13 section 4.4).
var (
	// ErrUnsupportedFormat reports a declared media type outside the supported
	// canonical set.
	ErrUnsupportedFormat = errors.New("asset content type is not supported")
	// ErrInvalidImage reports an undecodable image or a declared type that does
	// not match the actual decoded content (smuggling defense, #13 section 4.1).
	ErrInvalidImage = errors.New("asset content is not a decodable image of the declared type")
	// ErrInvalidDimensions reports pixel dimensions outside the canonical bounds.
	ErrInvalidDimensions = errors.New("asset pixel dimensions are out of bounds")
)

// ImageFacts is the canonical decoded description of an uploaded image. It is a
// content projection, never secret material.
type ImageFacts struct {
	ContentType string
	Width       int
	Height      int
}

// Asset is a Tenant-owned, immutable image data object. `tenant_id`,
// `asset_id`, and `kind` are immutable; content bytes never change after create
// (an edit produces a new `output`). Only lifecycle fields transition (#13
// section 3.3). The Tenant id stays server-side authority and never crosses the
// Public API wire (#6, #13 section 3.1).
type Asset struct {
	ID             AssetID
	TenantID       TenantID
	Kind           AssetKind
	ContentType    string
	ByteSize       int64
	Width          int
	Height         int
	Checksum       string
	Origin         AssetOrigin
	SourceJobID    Identifier
	RetentionClass RetentionClass
	CreatedAt      Timestamp
	ExpiresAt      Timestamp
	DeletedAt      Timestamp
	TombstoneUntil Timestamp
}

// NewUploadedAsset builds the immutable Asset stamped for the owning Tenant from
// a validated upload. Uploads use the RETAIN-INPUT class and are stamped
// `uploaded`; the derived expiry is created_at + the class window so the Asset
// has a bounded, downloadable lifetime (#13 sections 3.2, 5.1-5.2).
func NewUploadedAsset(id AssetID, tenant TenantID, kind AssetKind, facts ImageFacts, byteSize int64, checksum string, now Timestamp) Asset {
	class := RetentionClassInput
	return Asset{
		ID:             id,
		TenantID:       tenant,
		Kind:           kind,
		ContentType:    facts.ContentType,
		ByteSize:       byteSize,
		Width:          facts.Width,
		Height:         facts.Height,
		Checksum:       checksum,
		Origin:         AssetOriginUploaded,
		RetentionClass: class,
		CreatedAt:      now,
		ExpiresAt:      NewTimestamp(now.Time().Add(RetentionWindow(class))),
	}
}

// RetentionWindow returns the MVP lifetime budget for a retention class.
// Ephemeral data has no client-facing download window and reclaims promptly, so
// it carries the shortest (input) budget as a conservative bound here (#13
// section 5.2).
func RetentionWindow(class RetentionClass) time.Duration {
	switch class {
	case RetentionClassOutput:
		return retentionWindowOutput
	default:
		return retentionWindowInput
	}
}

// Retrievable reports whether the Asset can still be read/downloaded at now: it
// must not be deleted and must not have passed its expiry. After either moment
// retrieval serves no bytes and behaves as a canonical gone/not-found outcome
// indistinguishable from an unknown id (#13 sections 5.3-5.5, 8).
func (asset Asset) Retrievable(now time.Time) bool {
	if !asset.DeletedAt.IsZero() {
		return false
	}
	if asset.ExpiresAt.IsZero() {
		return true
	}
	return now.Before(asset.ExpiresAt.Time())
}

// InspectImageContent validates a declared media type against the actual
// decoded bytes and returns the canonical image facts. It rejects an
// unsupported type, an undecodable or type-mismatched payload (smuggling
// defense), and dimensions outside the canonical bounds with the matching
// distinct outcome (#13 sections 4.1-4.2). Decoding uses the Pure-Go standard
// library; WebP dimensions are read from the RIFF header because the standard
// library ships no WebP decoder and the zero third-party dependency budget
// forbids adding one (ADR 0009).
func InspectImageContent(declaredContentType string, data []byte) (ImageFacts, error) {
	switch declaredContentType {
	case ContentTypePNG, ContentTypeJPEG, ContentTypeWebP:
	default:
		return ImageFacts{}, ErrUnsupportedFormat
	}

	actual := sniffImageType(data)
	if actual == "" || actual != declaredContentType {
		return ImageFacts{}, ErrInvalidImage
	}

	width, height, ok := decodeDimensions(actual, data)
	if !ok {
		return ImageFacts{}, ErrInvalidImage
	}
	if width < AssetMinDimension || height < AssetMinDimension ||
		width > AssetMaxDimension || height > AssetMaxDimension {
		return ImageFacts{}, ErrInvalidDimensions
	}
	return ImageFacts{ContentType: actual, Width: width, Height: height}, nil
}

// sniffImageType returns the canonical media type implied by the magic bytes,
// or an empty string when the content matches no supported image format. It
// never trusts the declared type (#13 section 4.1.2).
func sniffImageType(data []byte) string {
	switch {
	case len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return ContentTypePNG
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return ContentTypeJPEG
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return ContentTypeWebP
	default:
		return ""
	}
}

// SniffImageType returns the canonical media type implied by the content magic
// bytes, or an empty string when the payload matches no supported image
// format. It is the exported projection the content store uses to label a
// stored Asset download without trusting a client-declared type (#13 section
// 4.1.2).
func SniffImageType(data []byte) string {
	return sniffImageType(data)
}

// decodeDimensions returns the pixel dimensions for the sniffed type, reporting
// false when the payload cannot be decoded as a well-formed image of that type.
func decodeDimensions(contentType string, data []byte) (int, int, bool) {
	switch contentType {
	case ContentTypePNG:
		config, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return 0, 0, false
		}
		return config.Width, config.Height, true
	case ContentTypeJPEG:
		config, err := jpeg.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return 0, 0, false
		}
		return config.Width, config.Height, true
	case ContentTypeWebP:
		return decodeWebPDimensions(data)
	default:
		return 0, 0, false
	}
}

// decodeWebPDimensions reads the canvas dimensions from a RIFF/WEBP header. It
// supports the simple lossy (VP8), simple lossless (VP8L), and extended (VP8X)
// containers, which is enough to validate format and dimensions without a full
// decoder (#13 section 4.2 dimension validation).
func decodeWebPDimensions(data []byte) (int, int, bool) {
	if len(data) < 16 {
		return 0, 0, false
	}
	switch string(data[12:16]) {
	case "VP8 ":
		// Lossy: after the 8-byte chunk header the frame tag (3 bytes) is
		// followed by the start code 0x9d 0x01 0x2a, then 14-bit width and
		// height, each little-endian in the low 14 bits of a 16-bit field.
		if len(data) < 30 {
			return 0, 0, false
		}
		if data[23] != 0x9d || data[24] != 0x01 || data[25] != 0x2a {
			return 0, 0, false
		}
		width := int(binary.LittleEndian.Uint16(data[26:28]) & 0x3fff)
		height := int(binary.LittleEndian.Uint16(data[28:30]) & 0x3fff)
		return width, height, width > 0 && height > 0
	case "VP8L":
		// Lossless: signature byte 0x2f then 14-bit (width-1) and (height-1)
		// packed little-endian across four bytes.
		if len(data) < 25 || data[20] != 0x2f {
			return 0, 0, false
		}
		bits := binary.LittleEndian.Uint32(data[21:25])
		width := int(bits&0x3fff) + 1
		height := int((bits>>14)&0x3fff) + 1
		return width, height, true
	case "VP8X":
		// Extended: 4 flag bytes then 24-bit (canvas width-1) and (height-1).
		if len(data) < 30 {
			return 0, 0, false
		}
		width := int(uint32(data[24])|uint32(data[25])<<8|uint32(data[26])<<16) + 1
		height := int(uint32(data[27])|uint32(data[28])<<8|uint32(data[29])<<16) + 1
		return width, height, true
	default:
		return 0, 0, false
	}
}
