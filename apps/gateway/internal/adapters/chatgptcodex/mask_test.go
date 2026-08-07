package chatgptcodex

import (
	"bytes"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// canonicalMaskFixture is the fixed canonical mask every inversion test reads.
// It is luminance, opaque, and spans the interesting values: full white (edit),
// full black (keep), and two mid-greys that must not be rounded to an extreme.
// ADR 0003 requires each inversion to be locked by a fixed mask with a
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

// TestAlphaMaskFromCanonicalInvertsLuminanceToAlpha asserts the Codex direction
// of ADR 0003: canonical white (luminance 255, "edit here") becomes alpha 0, the
// transparent brush area the Codex image path treats as editable, and canonical
// black becomes alpha 255, protected context. A mid-grey inverts proportionally
// rather than snapping to an extreme, so a feathered mask edge stays feathered.
func TestAlphaMaskFromCanonicalInvertsLuminanceToAlpha(t *testing.T) {
	t.Parallel()

	output, err := alphaMaskFromCanonical(canonicalMaskPNG(t))
	if err != nil {
		t.Fatalf("alphaMaskFromCanonical() error = %v, want nil", err)
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

// TestAlphaMaskFromCanonicalSurvivesRoundTrip is the regression guard for the
// premultiplied-alpha trap. Go's color.RGBA is alpha-premultiplied, so a fully
// transparent pixel that also carries a light colour has no legal premultiplied
// representation and silently loses its value through an encode/decode cycle.
// The editable region of every mask is exactly that pixel, so getting this wrong
// produces a mask that inverts correctly in memory and arrives at the Provider
// as an all-opaque image that edits nothing.
func TestAlphaMaskFromCanonicalSurvivesRoundTrip(t *testing.T) {
	t.Parallel()

	white := image.NewGray(image.Rect(0, 0, 1, 1))
	white.SetGray(0, 0, color.Gray{Y: 255})
	source := &bytes.Buffer{}
	if err := png.Encode(source, white); err != nil {
		t.Fatalf("encode all-white mask: %v", err)
	}

	output, err := alphaMaskFromCanonical(source.Bytes())
	if err != nil {
		t.Fatalf("alphaMaskFromCanonical() error = %v, want nil", err)
	}
	decoded, err := png.Decode(bytes.NewReader(output))
	if err != nil {
		t.Fatalf("decode inverted mask: %v", err)
	}
	if _, _, _, alpha := decoded.At(0, 0).RGBA(); alpha != 0 {
		t.Fatalf("all-white canonical mask -> alpha %d after round trip, want 0 (fully editable)", alpha>>8)
	}
}

// TestAlphaMaskFromCanonicalOutputIsByteStable locks the encoded output of the
// fixed fixture. ADR 0003 asks for a byte comparison rather than only a
// semantic one: a semantic test passes for either convention if someone flips
// the expectation with the code, whereas this digest changes the moment the
// emitted bytes change and forces the convention decision back into review.
func TestAlphaMaskFromCanonicalOutputIsByteStable(t *testing.T) {
	t.Parallel()

	first, err := alphaMaskFromCanonical(canonicalMaskPNG(t))
	if err != nil {
		t.Fatalf("alphaMaskFromCanonical() error = %v, want nil", err)
	}
	second, err := alphaMaskFromCanonical(canonicalMaskPNG(t))
	if err != nil {
		t.Fatalf("alphaMaskFromCanonical() second call error = %v, want nil", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("inversion is not deterministic: same canonical mask produced different bytes")
	}
	if got := hex.EncodeToString(first); got != wantInvertedMaskHex {
		t.Fatalf("inverted mask bytes changed.\n got = %s\nwant = %s\n"+
			"If this change is intended, ADR 0003 requires the mask convention "+
			"decision to be revisited, not just this digest updated.", got, wantInvertedMaskHex)
	}
}

// TestAlphaMaskFromCanonicalRejectsNonPNG asserts the Adapter refuses a mask it
// cannot decode as PNG rather than shipping arbitrary bytes upstream. Ingest
// already enforces PNG (domain.ValidateMaskFormat), so this is defence for a
// Gateway-internal inconsistency, not a client-facing validation path.
func TestAlphaMaskFromCanonicalRejectsNonPNG(t *testing.T) {
	t.Parallel()

	if _, err := alphaMaskFromCanonical([]byte("this is not a png")); !errors.Is(err, errMaskNotPNG) {
		t.Fatalf("alphaMaskFromCanonical(non-PNG) error = %v, want errMaskNotPNG", err)
	}
}
