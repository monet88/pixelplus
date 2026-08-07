package domain_test

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// maskFormatPNG encodes an opaque grayscale PNG, the canonical mask shape.
func maskFormatPNG(t *testing.T) []byte {
	t.Helper()

	gray := image.NewGray(image.Rect(0, 0, 8, 8))
	gray.SetGray(0, 0, color.Gray{Y: 255})
	encoded := &bytes.Buffer{}
	if err := png.Encode(encoded, gray); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return encoded.Bytes()
}

// maskFormatJPEG encodes a decodable JPEG of the same dimensions. This is the
// dangerous shape #121 exists for: it decodes, its dimensions are right, and
// only its compression is wrong.
func maskFormatJPEG(t *testing.T) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	encoded := &bytes.Buffer{}
	if err := jpeg.Encode(encoded, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return encoded.Bytes()
}

// maskFormatWebP builds a minimal RIFF/WEBP lossy header. The Gateway reads
// WebP dimensions from the header rather than decoding, so a valid header is
// enough to reach the format decision under test.
func maskFormatWebP(width, height int) []byte {
	data := make([]byte, 30)
	copy(data[0:4], "RIFF")
	copy(data[8:12], "WEBP")
	copy(data[12:16], "VP8 ")
	data[23], data[24], data[25] = 0x9d, 0x01, 0x2a
	data[26] = byte(width & 0xff)
	data[27] = byte(width >> 8 & 0x3f)
	data[28] = byte(height & 0xff)
	data[29] = byte(height >> 8 & 0x3f)
	return data
}

// TestValidateMaskFormatAcceptsPNG asserts the canonical mask format passes.
func TestValidateMaskFormatAcceptsPNG(t *testing.T) {
	t.Parallel()

	if err := domain.ValidateMaskFormat(maskFormatPNG(t)); err != nil {
		t.Fatalf("ValidateMaskFormat(png) error = %v, want nil", err)
	}
}

// TestValidateMaskFormatRejectsLossyFormats asserts JPEG and WebP masks are
// refused. Both decode and both can carry the right dimensions, which is exactly
// why the check has to be on format rather than on decodability: a JPEG mask
// fails quietly downstream, turning every mask edge into a band of mid-grey that
// some surfaces treat as a partially-edited region (#121, ADR 0003).
func TestValidateMaskFormatRejectsLossyFormats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content []byte
	}{
		{name: "jpeg", content: maskFormatJPEG(t)},
		{name: "webp", content: maskFormatWebP(8, 8)},
		{name: "not an image", content: []byte("plain text")},
		{name: "empty", content: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := domain.ValidateMaskFormat(test.content)
			if !errors.Is(err, domain.ErrMaskFormatRejected) {
				t.Fatalf("ValidateMaskFormat(%s) error = %v, want ErrMaskFormatRejected", test.name, err)
			}
		})
	}
}

// TestValidateMaskFormatIgnoresDeclaredType asserts the decision is made on
// content alone. ValidateMaskFormat takes no declared type by design: a JPEG
// announced as image/png with a .png filename must still be refused, so the
// signature deliberately offers no place for a caller to pass a claim that could
// be trusted (#121).
func TestValidateMaskFormatIgnoresDeclaredType(t *testing.T) {
	t.Parallel()

	// The same JPEG bytes a client might label image/png.
	if err := domain.ValidateMaskFormat(maskFormatJPEG(t)); !errors.Is(err, domain.ErrMaskFormatRejected) {
		t.Fatalf("ValidateMaskFormat(jpeg) error = %v, want ErrMaskFormatRejected", err)
	}
	// And PNG bytes pass regardless of what a client would have declared.
	if err := domain.ValidateMaskFormat(maskFormatPNG(t)); err != nil {
		t.Fatalf("ValidateMaskFormat(png) error = %v, want nil", err)
	}
}

// TestMaskFormatRejectionIsItsOwnOutcome asserts the mask-format failure is not
// aliased onto the existing content-validation sentinels. If it collapsed into
// ErrUnsupportedFormat or ErrInvalidImage the application could not map it to
// invalid_mask, and a designer would be told "broken file" for a file that opens
// perfectly well (#121 distinguishability).
func TestMaskFormatRejectionIsItsOwnOutcome(t *testing.T) {
	t.Parallel()

	err := domain.ValidateMaskFormat(maskFormatJPEG(t))
	if errors.Is(err, domain.ErrUnsupportedFormat) {
		t.Fatal("mask format rejection must not alias ErrUnsupportedFormat")
	}
	if errors.Is(err, domain.ErrInvalidImage) {
		t.Fatal("mask format rejection must not alias ErrInvalidImage")
	}
	if errors.Is(err, domain.ErrMaskDimensionMismatch) {
		t.Fatal("mask format rejection must not alias ErrMaskDimensionMismatch")
	}
	if errors.Is(err, domain.ErrInvalidMask) {
		t.Fatal("mask format rejection must stay a distinct sentinel from ErrInvalidMask")
	}
}

// TestValidateMaskFormatDoesNotConstrainInputAssets is a guard on scope. The
// asymmetry between kinds is the point of #121: a mask is Plugin-generated so
// PNG can be required, while an input image comes from the user's document and
// must keep accepting JPEG and WebP. InspectImageContent is the kind-agnostic
// path, so it must still admit a JPEG that ValidateMaskFormat refuses.
func TestValidateMaskFormatDoesNotConstrainInputAssets(t *testing.T) {
	t.Parallel()

	jpegContent := maskFormatJPEG(t)
	if err := domain.ValidateMaskFormat(jpegContent); !errors.Is(err, domain.ErrMaskFormatRejected) {
		t.Fatalf("ValidateMaskFormat(jpeg) error = %v, want ErrMaskFormatRejected", err)
	}
	facts, err := domain.InspectImageContent(domain.ContentTypeJPEG, jpegContent)
	if err != nil {
		t.Fatalf("InspectImageContent(jpeg) error = %v, want nil (input assets keep JPEG)", err)
	}
	if facts.ContentType != domain.ContentTypeJPEG {
		t.Fatalf("InspectImageContent content type = %q, want %q", facts.ContentType, domain.ContentTypeJPEG)
	}
}
