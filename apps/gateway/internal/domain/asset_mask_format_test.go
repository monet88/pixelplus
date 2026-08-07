package domain_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// canonicalMaskLuminance is the fixed canonical mask every inversion test
// reads. It is luminance, opaque, and spans the interesting values: full white
// (edit), full black (keep), and two mid-greys that must not be rounded to an
// extreme. ADR 0003 requires each inversion to be locked by a fixed mask with a
// byte-compared output, so this fixture and the golden digest below move only
// when the convention itself is deliberately changed.
var canonicalMaskLuminance = [4][4]uint8{
	{255, 255, 0, 0},
	{255, 128, 64, 0},
	{0, 64, 128, 255},
	{0, 0, 255, 255},
}

// wantInvertedMaskHex is the golden encoding of the inverted fixture. It is a
// digest of intent, not of convenience: it changes only when the emitted mask
// bytes change, which per ADR 0003 means the convention decision is back in
// play and belongs in review rather than in a silent digest bump.
const wantInvertedMaskHex = "89504e470d0a1a0a0000000d4948445200000004000000040806000000a9f19e7e0000002a49444154789c62614080ff208219caa987d20a4c501907060686030c0c0c8c30a5ff61fa00010000ffff9e8c05a6007bdc930000000049454e44ae426082"

// canonicalMaskPNG encodes canonicalMaskLuminance as an opaque grayscale PNG,
// which is exactly what src/image/ emits for a canonical mask.
func canonicalMaskPNG(t *testing.T) []byte {
	t.Helper()

	gray := image.NewGray(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			gray.SetGray(x, y, color.Gray{Y: canonicalMaskLuminance[y][x]})
		}
	}
	encoded := &bytes.Buffer{}
	if err := png.Encode(encoded, gray); err != nil {
		t.Fatalf("encode canonical mask fixture: %v", err)
	}
	return encoded.Bytes()
}

// TestAlphaMaskFromCanonicalInvertsLuminanceToAlpha asserts the ChatGPT-family
// direction of ADR 0003: canonical white (luminance 255, "edit here") becomes
// alpha 0, the transparent brush area the image path treats as editable, and
// canonical black becomes alpha 255, protected context. A mid-grey inverts
// proportionally rather than snapping to an extreme, so a feathered mask edge
// stays feathered.
func TestAlphaMaskFromCanonicalInvertsLuminanceToAlpha(t *testing.T) {
	t.Parallel()

	output, err := domain.AlphaMaskFromCanonical(canonicalMaskPNG(t))
	if err != nil {
		t.Fatalf("AlphaMaskFromCanonical() error = %v, want nil", err)
	}

	decoded, err := png.Decode(bytes.NewReader(output))
	if err != nil {
		t.Fatalf("decode inverted mask: %v", err)
	}
	if bounds := decoded.Bounds(); bounds.Dx() != 4 || bounds.Dy() != 4 {
		t.Fatalf("inverted bounds = %v, want 4x4", bounds)
	}

	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			luminance := canonicalMaskLuminance[y][x]
			wantAlpha := uint32(255 - luminance)
			_, _, _, gotAlpha := decoded.At(x, y).RGBA()
			// RGBA() reports 16-bit channels; compare on the 8-bit scale.
			if gotAlpha>>8 != wantAlpha {
				t.Fatalf("at (%d,%d) canonical luminance %d -> alpha %d, want %d",
					x, y, luminance, gotAlpha>>8, wantAlpha)
			}
		}
	}
}

// TestAlphaMaskFromCanonicalSurvivesRoundTrip locks the alpha channel through
// an encode/decode cycle for a genuinely translucent result. The transform
// emits pixels as NRGBA with the inverted luminance in the alpha channel and
// RGB always zero (the canonical mask carries no colour); because RGB is zero,
// premultiplication by alpha is a no-op there, so this test cannot by itself
// tell NRGBA from premultiplied RGBA — the byte-stable golden digest and the
// NRGBA-construction assertion in TestAlphaMaskFromCanonicalOutputIsByteStable
// are what actually pin the representation. What this test does lock is that
// a mid-grey luminance (alpha 127, not the degenerate all-or-nothing 0/255)
// survives the encode/decode round trip, so a future change that clamps or
// rounds the alpha channel fails here (#98 review P3).
func TestAlphaMaskFromCanonicalSurvivesRoundTrip(t *testing.T) {
	t.Parallel()

	// A single mid-grey pixel: alpha_out = 255 - 128 = 127, a genuinely
	// translucent result that only NRGBA can carry losslessly.
	midGray := image.NewGray(image.Rect(0, 0, 1, 1))
	midGray.SetGray(0, 0, color.Gray{Y: 128})
	source := &bytes.Buffer{}
	if err := png.Encode(source, midGray); err != nil {
		t.Fatalf("encode mid-grey mask: %v", err)
	}

	output, err := domain.AlphaMaskFromCanonical(source.Bytes())
	if err != nil {
		t.Fatalf("AlphaMaskFromCanonical() error = %v, want nil", err)
	}
	decoded, err := png.Decode(bytes.NewReader(output))
	if err != nil {
		t.Fatalf("decode inverted mask: %v", err)
	}

	_, _, _, a := decoded.At(0, 0).RGBA()
	// RGBA() reports 16-bit channels; compare on the 8-bit scale.
	if a>>8 != 127 {
		t.Fatalf("mid-grey canonical mask -> alpha %d after round trip, want 127", a>>8)
	}
}

// TestAlphaMaskFromCanonicalOutputIsByteStable locks the encoded output.
func TestAlphaMaskFromCanonicalOutputIsByteStable(t *testing.T) {
	t.Parallel()

	first, err := domain.AlphaMaskFromCanonical(canonicalMaskPNG(t))
	if err != nil {
		t.Fatalf("AlphaMaskFromCanonical() error = %v, want nil", err)
	}
	second, err := domain.AlphaMaskFromCanonical(canonicalMaskPNG(t))
	if err != nil {
		t.Fatalf("AlphaMaskFromCanonical() second call error = %v, want nil", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("inversion is not deterministic")
	}
	if got := hex.EncodeToString(first); got != wantInvertedMaskHex {
		t.Fatalf("inverted mask bytes changed.\n got = %s\nwant = %s\n"+
			"If this change is intended, ADR 0003 requires the mask convention "+
			"decision to be revisited, not just this digest updated.", got, wantInvertedMaskHex)
	}
}

// TestAlphaMaskFromCanonicalRejectsNonPNG asserts the transform refuses bytes
// it cannot decode as PNG.
func TestAlphaMaskFromCanonicalRejectsNonPNG(t *testing.T) {
	t.Parallel()

	if _, err := domain.AlphaMaskFromCanonical([]byte("not a png")); !errors.Is(err, domain.ErrMaskNotPNG) {
		t.Fatalf("AlphaMaskFromCanonical(non-PNG) error = %v, want ErrMaskNotPNG", err)
	}
}

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

// maskFormatTranslucentPNG encodes an RGBA PNG whose single pixel is not fully
// opaque. It is valid PNG with correct dimensions — exactly the shape that
// slips through a format-only gate and still reads as a partial edit region.
func maskFormatTranslucentPNG(t *testing.T) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.SetRGBA(0, 0, color.RGBA{A: 128})
	encoded := &bytes.Buffer{}
	if err := png.Encode(encoded, img); err != nil {
		t.Fatalf("encode translucent png: %v", err)
	}
	return encoded.Bytes()
}

// TestValidateMaskFormatAcceptsPNG asserts the canonical mask format passes.
func TestValidateMaskFormatAcceptsPNG(t *testing.T) {
	t.Parallel()

	if err := domain.ValidateMaskFormat(maskFormatPNG(t)); err != nil {
		t.Fatalf("ValidateMaskFormat(png) error = %v, want nil", err)
	}
}

// TestValidateMaskFormatRejectsTranslucentPNG asserts that "opaque PNG" means
// what it says: a PNG that decodes but carries an alpha channel below 0xFF is
// refused even though its format and dimensions are fine (#98, ADR 0003). This
// is the review finding that the format gate alone would let a translucent mask
// claim the canonical shape.
func TestValidateMaskFormatRejectsTranslucentPNG(t *testing.T) {
	t.Parallel()

	err := domain.ValidateMaskFormat(maskFormatTranslucentPNG(t))
	if !errors.Is(err, domain.ErrMaskOpacityRejected) {
		t.Fatalf("ValidateMaskFormat(translucent png) error = %v, want ErrMaskOpacityRejected", err)
	}
}

// maskFormatPalettedWithUnusedTransparent builds an indexed PNG whose palette
// carries a transparent entry that NO pixel references. Every actual pixel is
// opaque; only the unused palette slot has alpha 0.
func maskFormatPalettedWithUnusedTransparent(t *testing.T) []byte {
	t.Helper()

	palette := []color.Color{
		color.RGBA{R: 255, G: 255, B: 255, A: 255}, // index 0: opaque white, used
		color.RGBA{A: 0}, // index 1: transparent, UNUSED
	}
	img := image.NewPaletted(image.Rect(0, 0, 8, 8), palette)
	for i := range img.Pix {
		img.Pix[i] = 0
	}
	encoded := &bytes.Buffer{}
	if err := png.Encode(encoded, img); err != nil {
		t.Fatalf("encode paletted png: %v", err)
	}
	return encoded.Bytes()
}

// TestValidateMaskFormatAcceptsPalettedWithUnusedTransparent asserts the opacity
// gate judges pixels actually referenced, not the whole palette. An indexed PNG
// whose palette has an unused transparent slot is fully opaque in practice and
// must be accepted — refusing it would reject a legitimate mask for a palette
// artifact the image never uses (#134 review P2).
func TestValidateMaskFormatAcceptsPalettedWithUnusedTransparent(t *testing.T) {
	t.Parallel()

	err := domain.ValidateMaskFormat(maskFormatPalettedWithUnusedTransparent(t))
	if err != nil {
		t.Fatalf("ValidateMaskFormat(paletted with unused transparent) error = %v, want nil", err)
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
