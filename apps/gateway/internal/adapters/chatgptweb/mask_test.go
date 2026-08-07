package chatgptweb

import (
	"bytes"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// canonicalMaskLuminance is the fixed canonical mask every inversion test reads.
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

// wantInvertedMaskHex is the golden encoding of the inverted fixture. It is a
// digest of intent, not of convenience: it changes only when the emitted mask
// bytes change, which per ADR 0003 means the convention decision is back in
// play and belongs in review rather than in a silent digest bump.
const wantInvertedMaskHex = "89504e470d0a1a0a0000000d4948445200000004000000040806000000a9f19e7e0000002a49444154789c62614080ff208219caa987d20a4c501907060686030c0c0c8c30a5ff61fa00010000ffff9e8c05a6007bdc930000000049454e44ae426082"

// TestAlphaMaskFromCanonicalInvertsLuminanceToAlpha asserts the ChatGPT Web
// direction of ADR 0003 (same transform, different reference surface).
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
			if gotAlpha>>8 != wantAlpha {
				t.Fatalf("at (%d,%d) canonical luminance %d -> alpha %d, want %d",
					x, y, luminance, gotAlpha>>8, wantAlpha)
			}
		}
	}
}

// TestAlphaMaskFromCanonicalSurvivesRoundTrip guards against the premultiplied-
// alpha trap (same trap, different package — if one Adapter copies code into
// RGBA, this test catches it).
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
		t.Fatalf("all-white canonical mask -> alpha %d after round trip, want 0", alpha>>8)
	}
}

// TestAlphaMaskFromCanonicalOutputIsByteStable locks the encoded output.
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
		t.Fatal("inversion is not deterministic")
	}
	if got := hex.EncodeToString(first); got != wantInvertedMaskHex {
		t.Fatalf("inverted mask bytes changed.\n got = %s\nwant = %s\n"+
			"If this change is intended, ADR 0003 requires the mask convention "+
			"decision to be revisited, not just this digest updated.", got, wantInvertedMaskHex)
	}
}

// TestAlphaMaskFromCanonicalRejectsNonPNG asserts the Adapter refuses bytes it
// cannot decode as PNG.
func TestAlphaMaskFromCanonicalRejectsNonPNG(t *testing.T) {
	t.Parallel()
	if _, err := alphaMaskFromCanonical([]byte("not a png")); !errors.Is(err, errMaskNotPNG) {
		t.Fatalf("alphaMaskFromCanonical(non-PNG) error = %v, want errMaskNotPNG", err)
	}
}
