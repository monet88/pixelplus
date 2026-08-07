package domain

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
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
	// ErrInvalidMask reports a mask reference whose role/kind cannot be
	// interpreted as a region selector for a masked operation (#13 section 4.3).
	ErrInvalidMask = errors.New("mask asset role or encoding is invalid")
	// ErrMaskDimensionMismatch reports a mask whose pixel dimensions do not
	// match its target input image (#13 section 4.3).
	ErrMaskDimensionMismatch = errors.New("mask asset dimensions do not match the input asset")
	// ErrMaskFormatRejected reports a mask upload whose actual (sniffed) image
	// format is not PNG. The canonical Mask Convention requires masks to be
	// opaque PNG so mask edges are exact and never lossy (#98, #121, ADR 0003).
	ErrMaskFormatRejected = errors.New("mask asset format must be PNG")
	// ErrMaskOpacityRejected reports a mask upload that is PNG but not opaque:
	// some pixel carries alpha != 0xFF. The canonical Mask Convention requires
	// opaque PNG so mask edges are exact (#98, #121, ADR 0003); a translucent
	// mask would let a Provider surface read the alpha channel as the edit
	// region, the same silent failure as a lossy format.
	ErrMaskOpacityRejected = errors.New("mask asset must be opaque PNG (alpha = 255 on every pixel)")
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

// ValidateMaskRelationship checks that mask is a valid region selector for
// input on a masked image operation (#13 section 4.3). Callers must already
// have resolved both Assets as same-Tenant visible before calling this; it
// performs no I/O and never re-decodes content (dimensions are already
// recorded on the Asset at upload time).
func ValidateMaskRelationship(input, mask Asset) error {
	if mask.Kind != AssetKindMask {
		return ErrInvalidMask
	}
	if input.Width != mask.Width || input.Height != mask.Height {
		return ErrMaskDimensionMismatch
	}
	return nil
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

// ValidateMaskFormat checks that an uploaded mask Asset's content is an opaque
// PNG. The canonical Mask Convention requires opaque PNG: mask edges must be
// exact, and JPEG/WebP compression introduces mid-grey ringing around mask
// boundaries that some surfaces interpret as partial edit (#98, #121, ADR
// 0003). The check is driven by sniffed bytes, not by declared content type or
// filename, so a JPEG announced as image/png is still refused. "opaque" is
// enforced, not assumed: a PNG that decodes with an alpha channel is scanned
// and refused if any pixel has alpha != 0xFF, so a translucent mask cannot
// claim the canonical shape.
//
// A byte stream that sniffs as PNG but fails to decode is deliberately NOT
// refused here: it returns nil and lets InspectImageContent report it as
// invalid_image, keeping "wrong format" (invalid_mask) distinct from "broken
// image" (invalid_image) — the pairwise-distinctness contract of #121.
func ValidateMaskFormat(data []byte) error {
	actual := sniffImageType(data)
	if actual != ContentTypePNG {
		return ErrMaskFormatRejected
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		// Sniffs as PNG but is not decodable; leave it to InspectImageContent
		// to report invalid_image rather than claiming a format problem.
		return nil
	}
	if !maskIsOpaque(img) {
		return ErrMaskOpacityRejected
	}
	return nil
}

// maskIsOpaque reports whether every pixel of the decoded image is fully
// opaque. Images without an alpha channel (gray, gray16) are opaque by
// construction. Images with alpha (RGBA, NRGBA, and paletted images whose
// palette carries alpha) are scanned and refused on the first translucent
// pixel.
func maskIsOpaque(img image.Image) bool {
	bounds := img.Bounds()
	switch src := img.(type) {
	case *image.Gray, *image.Gray16:
		return true
	case *image.Paletted:
		for _, p := range src.Palette {
			_, _, _, a := p.RGBA()
			if a != 0xffff {
				return false
			}
		}
		return true
	default:
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				_, _, _, a := img.At(x, y).RGBA()
				if a != 0xffff {
					return false
				}
			}
		}
		return true
	}
}

// AlphaMaskFromCanonical converts a canonical mask into the alpha convention
// the ChatGPT-family image paths consume.
//
// The canonical Mask Convention (ADR 0003) is luminance, white = edit, opaque
// PNG. The ChatGPT family consumes the opposite: alpha 0 marks the editable
// region and non-transparent pixels are protected context. So the transform is
// alpha_out = 255 - luminance_in: canonical white (255) becomes alpha 0, the
// editable region; canonical black (0) becomes alpha 255, protected context.
// This is not only a bit inversion, it changes the image type from an opaque
// grayscale/palette PNG to an RGBA PNG, which is why this encodes through a
// fresh non-premultiplied image rather than rewriting a channel in place.
//
// The output is NRGBA, not RGBA, deliberately. Go's color.RGBA is
// alpha-premultiplied, so a light pixel that must become fully transparent has
// no valid premultiplied representation (R > A is not a legal RGBA color) and
// the value would not survive an encode/decode round trip. NRGBA stores the
// alpha channel independently, which is what the wire format needs.
//
// The transform is defined once here so a convention drift cannot silently fork
// the Adapters that share it. The decision to invert remains with each Provider
// Adapter (ADR 0003) — this package provides the pure byte transform that
// decision invokes.
func AlphaMaskFromCanonical(canonical []byte) ([]byte, error) {
	source, err := png.Decode(bytes.NewReader(canonical))
	if err != nil {
		return nil, ErrMaskNotPNG
	}

	bounds := source.Bounds()
	inverted := image.NewNRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			luminance := color.GrayModel.Convert(source.At(x, y)).(color.Gray).Y
			inverted.SetNRGBA(x, y, color.NRGBA{A: 255 - luminance})
		}
	}

	encoded := &bytes.Buffer{}
	if err := png.Encode(encoded, inverted); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

// ErrMaskNotPNG reports a mask that did not decode as PNG. Asset ingest already
// refuses a non-PNG mask (ValidateMaskFormat), so reaching this in an Adapter
// is a Gateway-internal inconsistency rather than a client mistake.
var ErrMaskNotPNG = errors.New("mask asset did not decode as PNG")
