package chatgptcodex

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
)

// errMaskNotPNG reports a mask that did not decode as PNG. Asset ingest already
// refuses a non-PNG mask (domain.ValidateMaskFormat), so reaching this here is a
// Gateway-internal inconsistency rather than a client mistake.
var errMaskNotPNG = errors.New("chatgptcodex: mask asset did not decode as PNG")

// alphaMaskFromCanonical converts a canonical mask into the alpha convention the
// Codex image path consumes.
//
// The canonical Mask Convention (ADR 0003) is luminance, white = edit, opaque
// PNG. The Codex path carries the mask as a base64 data URL at
// `input_image_mask.image_url` on a `/responses` `image_generation` tool call,
// and that field's semantics are stated in the reference surface as
// "transparent pixels are the editable brush area; non-transparent pixels are
// protected context" (`.ref/OpenAI-PS/src/app.js:3308`, corroborated by
// `rgba[o+3] = 255 - selected` at app.js:8221 and by
// `.ref/CLIProxyAPI/.../codex_openai_images.go:739,799`).
//
// So the transform is alpha_out = 255 - luminance_in: canonical white (255)
// becomes alpha 0, the editable region; canonical black (0) becomes alpha 255,
// protected context. This is not only a bit inversion, it changes the image type
// from an opaque grayscale/palette PNG to an RGBA PNG, which is why this encodes
// through a fresh non-premultiplied image rather than rewriting a channel in
// place.
//
// The output is NRGBA, not RGBA, deliberately. Go's color.RGBA is
// alpha-premultiplied, so a light pixel that must become fully transparent has
// no valid premultiplied representation (R > A is not a legal RGBA color) and
// the value would not survive an encode/decode round trip. NRGBA stores the
// alpha channel independently, which is what the wire format needs.
//
// This is the only place the Codex Adapter inverts a mask. ADR 0003 puts the
// inversion in the component that talks to upstream precisely so no call site
// re-decides the convention: the failure mode being avoided is not one wrong
// inversion but a codebase with two contradictory ones.
func alphaMaskFromCanonical(canonical []byte) ([]byte, error) {
	source, err := png.Decode(bytes.NewReader(canonical))
	if err != nil {
		return nil, errMaskNotPNG
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
