package contracttest_test

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// This file locks the PNG-only mask ingest rule (#121) through the public HTTP
// surface over real production composition. It shares the harness, upload
// helper, and image fixtures of the asset exchange tests in this package, so
// the seam under test is exactly the one a client reaches.

// webpBytes builds a minimal RIFF/WEBP VP8L header of the given pixel
// dimensions. The Go standard library ships no WebP encoder and the zero
// third-party dependency budget (ADR 0009) forbids adding one, so the fixture is
// assembled by hand. Validation reads only the canvas dimensions out of the
// header, so a header-only payload is enough to exercise both the accepted
// kind=input path and the refused kind=mask path.
func webpBytes(t *testing.T, width, height int) []byte {
	t.Helper()

	if width < 1 || height < 1 || width > 1<<14 || height > 1<<14 {
		t.Fatalf("webpBytes(%d, %d): dimensions outside the 14-bit VP8L range", width, height)
	}

	payload := make([]byte, 0, 25)
	payload = append(payload, "RIFF"...)
	payload = append(payload, 0, 0, 0, 0) // file size, patched below
	payload = append(payload, "WEBP"...)
	payload = append(payload, "VP8L"...)
	payload = append(payload, 0, 0, 0, 0) // chunk size, patched below
	payload = append(payload, 0x2f)       // VP8L signature byte

	// 14-bit (width-1) then 14-bit (height-1), packed little-endian.
	packed := uint32(width-1) | uint32(height-1)<<14
	payload = binary.LittleEndian.AppendUint32(payload, packed)

	binary.LittleEndian.PutUint32(payload[4:8], uint32(len(payload)-8))
	binary.LittleEndian.PutUint32(payload[16:20], uint32(len(payload)-20))
	return payload
}

// AC (#121): a kind=mask upload whose bytes are JPEG or WebP is refused with
// invalid_mask at ingest, before any Render Job can reference it. The canonical
// Mask Convention requires opaque PNG (ADR 0003), and a JPEG mask is the quiet
// version of the failure: it decodes, it carries the right dimensions, and the
// compression ringing around every mask edge becomes a band of mid-grey that a
// Provider surface reads as a partially edited region. Nothing errors
// downstream, so the boundary is the only place this can be caught.
//
// Two of the cases declare image/png and use a .png filename while carrying
// JPEG or WebP bytes. Refusal follows the sniffed content, so those are refused
// too: a client cannot talk its way past the gate with a header or a file name.
func TestCreateMaskAssetRejectsNonPNGBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		partType string
		fileName string
		content  func(*testing.T) []byte
	}{
		{
			name:     "jpeg declared as jpeg",
			partType: "image/jpeg",
			fileName: "selection-mask.jpg",
			content:  func(t *testing.T) []byte { return jpegBytes(t, 64, 64) },
		},
		{
			name:     "jpeg declared as png with png filename",
			partType: "image/png",
			fileName: "selection-mask.png",
			content:  func(t *testing.T) []byte { return jpegBytes(t, 64, 64) },
		},
		{
			name:     "webp declared as webp",
			partType: "image/webp",
			fileName: "selection-mask.webp",
			content:  func(t *testing.T) []byte { return webpBytes(t, 64, 64) },
		},
		{
			name:     "webp declared as png",
			partType: "image/png",
			fileName: "selection-mask.png",
			content:  func(t *testing.T) []byte { return webpBytes(t, 64, 64) },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			harness := newAssetHarness(t, assetHarnessConfig{})

			response, payload := harness.upload(t, uploadSpec{
				bearer:   assetWriteKey,
				idemKey:  "idem-mask-" + test.name,
				kind:     "mask",
				partType: test.partType,
				fileName: test.fileName,
				content:  test.content(t),
			})

			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%s)", response.StatusCode, payload)
			}
			body := decodeAssetError(t, payload)
			if body["code"] != "invalid_mask" {
				t.Fatalf("code = %v, want invalid_mask (body=%s)", body["code"], payload)
			}
			// The refusal lands at the Asset boundary, which is what keeps it
			// distinguishable from the request_validation mask outcomes raised
			// later, when a Render Job relates a mask to an input.
			if body["failure_stage"] != "asset" {
				t.Fatalf("failure_stage = %v, want asset (body=%s)", body["failure_stage"], payload)
			}

			// Nothing durable was created, so no Render Job can reference the
			// refused mask.
			if audits := harness.audit.snapshot(); len(audits) != 0 {
				t.Fatalf("audit events = %d, want 0 (a refused mask must not be created)", len(audits))
			}
		})
	}
}

// AC (#121): a canonical PNG mask still uploads. Without this companion case a
// gate that refused every mask would pass the refusal tests above.
func TestCreateMaskAssetAcceptsPNG(t *testing.T) {
	t.Parallel()

	harness := newAssetHarness(t, assetHarnessConfig{})
	response, payload := harness.upload(t, uploadSpec{
		bearer:   assetWriteKey,
		idemKey:  "idem-mask-png",
		kind:     "mask",
		partType: "image/png",
		fileName: "selection-mask.png",
		content:  pngBytes(t, 64, 64),
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", response.StatusCode, payload)
	}
	asset := decodeAsset(t, payload)
	if asset["kind"] != "mask" {
		t.Fatalf("kind = %v, want mask", asset["kind"])
	}
	if asset["content_type"] != "image/png" {
		t.Fatalf("content_type = %v, want image/png", asset["content_type"])
	}
}

// AC (#121): kind=input keeps accepting PNG, JPEG, and WebP with no change in
// behaviour. The asymmetry between the two kinds is deliberate: a mask is
// Plugin-generated from a selection so the Plugin picks the format, while an
// input image originates in the user's document and arrives in whatever that
// document holds (ADR 0003, CONTEXT.md "Mask Convention").
func TestCreateInputAssetStillAcceptsPNGJPEGAndWebP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		partType string
		wantType string
		content  func(*testing.T) []byte
	}{
		{
			name:     "png",
			partType: "image/png",
			wantType: "image/png",
			content:  func(t *testing.T) []byte { return pngBytes(t, 48, 48) },
		},
		{
			name:     "jpeg",
			partType: "image/jpeg",
			wantType: "image/jpeg",
			content:  func(t *testing.T) []byte { return jpegBytes(t, 48, 48) },
		},
		{
			name:     "webp",
			partType: "image/webp",
			wantType: "image/webp",
			content:  func(t *testing.T) []byte { return webpBytes(t, 48, 48) },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			harness := newAssetHarness(t, assetHarnessConfig{})

			response, payload := harness.upload(t, uploadSpec{
				bearer:   assetWriteKey,
				idemKey:  "idem-input-" + test.name,
				kind:     "input",
				partType: test.partType,
				content:  test.content(t),
			})
			if response.StatusCode != http.StatusCreated {
				t.Fatalf("status = %d, want 201 (body=%s)", response.StatusCode, payload)
			}
			asset := decodeAsset(t, payload)
			if asset["kind"] != "input" {
				t.Fatalf("kind = %v, want input", asset["kind"])
			}
			if asset["content_type"] != test.wantType {
				t.Fatalf("content_type = %v, want %s", asset["content_type"], test.wantType)
			}
		})
	}
}

// AC (#121): the mask-format refusal is distinguishable from a generic invalid
// asset, so a client can tell the designer what to actually fix. Three different
// mistakes on the mask surface keep three different codes:
//
//   - mask bytes are a lossy format   -> invalid_mask       ("re-export as PNG")
//   - bytes are not a decodable image -> invalid_image      ("this file is broken")
//   - pixels out of bounds            -> invalid_dimensions ("too large")
//
// Collapsing any pair would leave the panel unable to say anything more useful
// than "the mask was rejected". The dimension-mismatch outcome
// (mask_dimension_mismatch) is a different surface: it needs an input to relate
// the mask to, so it stays locked in the Render Job contract tests. The
// assertion below additionally proves the codes are pairwise distinct rather
// than merely each matching an expectation.
//
// unsupported_format is deliberately absent from this set. On the mask path the
// format gate resolves first and every non-PNG payload is invalid_mask, which is
// the more specific and more actionable answer; unsupported_format remains a
// distinct outcome on the kind=input path, where TestCreateAssetContentValidation
// IsDistinct locks it. That asymmetry is the point of the two kinds having
// different format rules rather than one relaxed shared policy.
func TestMaskFormatRefusalIsDistinctFromOtherAssetFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		partType string
		content  func(*testing.T) []byte
		wantCode string
	}{
		{
			name:     "jpeg mask is invalid_mask",
			partType: "image/jpeg",
			content:  func(t *testing.T) []byte { return jpegBytes(t, 32, 32) },
			wantCode: "invalid_mask",
		},
		{
			name:     "undecodable mask is invalid_image",
			partType: "image/png",
			content:  func(*testing.T) []byte { return []byte("\x89PNG\r\n\x1a\n truncated garbage") },
			wantCode: "invalid_image",
		},
		{
			name:     "oversize mask stays invalid_dimensions",
			partType: "image/png",
			content:  func(t *testing.T) []byte { return pngBytes(t, domain.AssetMaxDimension+1, 1) },
			wantCode: "invalid_dimensions",
		},
	}

	seen := make(map[string]string, len(tests))
	for _, test := range tests {
		harness := newAssetHarness(t, assetHarnessConfig{})
		response, payload := harness.upload(t, uploadSpec{
			bearer:   assetWriteKey,
			idemKey:  "idem-distinct-" + test.name,
			kind:     "mask",
			partType: test.partType,
			content:  test.content(t),
		})
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400 (body=%s)", test.name, response.StatusCode, payload)
		}
		got, _ := decodeAssetError(t, payload)["code"].(string)
		if got != test.wantCode {
			t.Fatalf("%s: code = %v, want %s (body=%s)", test.name, got, test.wantCode, payload)
		}
		if previous, clash := seen[got]; clash {
			t.Fatalf("code %q is produced by both %q and %q: the outcomes are not distinguishable",
				got, previous, test.name)
		}
		seen[got] = test.name
	}
}

// AC (#121): a GIF uploaded as a mask is refused as invalid_mask, not as
// unsupported_format. This is the one case where the mask path deliberately
// answers differently from the input path: the mask format gate resolves on
// sniffed bytes before the declared-type switch is reached, so the client is
// told the mask-specific fact ("masks must be PNG") rather than the generic one
// ("we do not accept this declared type"). Locking it separately keeps the
// pairwise-distinctness assertion above honest while still recording the
// intended answer for a non-PNG declared type.
func TestNonPNGDeclaredTypeOnMaskPathIsInvalidMask(t *testing.T) {
	t.Parallel()

	harness := newAssetHarness(t, assetHarnessConfig{})
	response, payload := harness.upload(t, uploadSpec{
		bearer:   assetWriteKey,
		idemKey:  "idem-mask-gif",
		kind:     "mask",
		partType: "image/gif",
		content:  []byte("GIF89a not really"),
	})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", response.StatusCode, payload)
	}
	body := decodeAssetError(t, payload)
	if body["code"] != "invalid_mask" {
		t.Fatalf("code = %v, want invalid_mask (body=%s)", body["code"], payload)
	}
	if body["failure_stage"] != "asset" {
		t.Fatalf("failure_stage = %v, want asset (body=%s)", body["failure_stage"], payload)
	}
}

// translucentPNGBytes encodes an RGBA PNG whose first pixel is not fully
// opaque. It is valid PNG with correct dimensions and format — exactly the
// shape that slips through a format-only gate and still reads as a partial
// edit region on a Provider surface (#98 review P2, ADR 0003 "opaque PNG").
func translucentPNGBytes(t *testing.T, width, height int) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{R: 10, G: 20, B: 30, A: 128})
	buffer := &bytes.Buffer{}
	if err := png.Encode(buffer, img); err != nil {
		t.Fatalf("encode translucent png: %v", err)
	}
	return buffer.Bytes()
}

// TestCreateMaskAssetRejectsTranslucentPNG locks the "opaque" half of "opaque
// PNG" through the public HTTP seam: a mask that is genuinely PNG but carries a
// translucent pixel is refused with invalid_mask, because a Provider surface
// would read the alpha channel as the edit region and produce a partial edit
// nobody asked for (#98 review P2, ADR 0003).
func TestCreateMaskAssetRejectsTranslucentPNG(t *testing.T) {
	t.Parallel()

	harness := newAssetHarness(t, assetHarnessConfig{})
	response, payload := harness.upload(t, uploadSpec{
		bearer:   assetWriteKey,
		idemKey:  "idem-mask-translucent",
		kind:     "mask",
		partType: "image/png",
		fileName: "selection-mask.png",
		content:  translucentPNGBytes(t, 64, 64),
	})
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", response.StatusCode, payload)
	}
	body := decodeAssetError(t, payload)
	if body["code"] != "invalid_mask" {
		t.Fatalf("code = %v, want invalid_mask (body=%s)", body["code"], payload)
	}
}
