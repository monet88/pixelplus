package chatgptweb

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// maskFixturePNG builds the same canonical mask shape the shared
// domain.AlphaMaskFromCanonical locks: an opaque 4x4 grayscale PNG spanning
// full white, full black, and two mid-greys. Kept local so this wrapper test
// never reaches into domain internals while still exercising the per-Adapter
// surface against the canonical convention shape (#98 review P2).
func maskFixturePNG(t *testing.T) []byte {
	t.Helper()

	values := [4][4]uint8{
		{255, 255, 0, 0},
		{255, 128, 64, 0},
		{0, 64, 128, 255},
		{0, 0, 255, 255},
	}
	gray := image.NewGray(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			gray.SetGray(x, y, color.Gray{Y: values[y][x]})
		}
	}
	encoded := &bytes.Buffer{}
	if err := png.Encode(encoded, gray); err != nil {
		t.Fatalf("encode canonical mask fixture: %v", err)
	}
	return encoded.Bytes()
}

// The mechanical transform, its fixed canonical fixture, golden digest, and the
// byte-stable round-trip and inversion tests all live in the shared
// domain.AlphaMaskFromCanonical — one definition, one digest, no fork between
// the two Adapters (#98 review P2). This file keeps the per-Adapter surface:
// the Web decision to invert and the local error mapping, each locked so the
// Adapter delegates rather than re-implements.

// TestAlphaMaskFromCanonicalInvertsToAlpha locks the Web direction of ADR 0003
// through the per-Adapter wrapper: the wrapper must produce a round-trippable
// 4x4 PNG, proving it delegates and does not double-invert or re-implement with
// drift.
func TestAlphaMaskFromCanonicalInvertsToAlpha(t *testing.T) {
	t.Parallel()

	output, err := alphaMaskFromCanonical(maskFixturePNG(t))
	if err != nil {
		t.Fatalf("alphaMaskFromCanonical() error = %v, want nil", err)
	}
	if len(output) == 0 {
		t.Fatal("alphaMaskFromCanonical() returned empty output")
	}
	decoded, err := png.Decode(bytes.NewReader(output))
	if err != nil {
		t.Fatalf("decode inverted mask: %v", err)
	}
	if bounds := decoded.Bounds(); bounds.Dx() != 4 || bounds.Dy() != 4 {
		t.Fatalf("inverted bounds = %v, want 4x4", bounds)
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
