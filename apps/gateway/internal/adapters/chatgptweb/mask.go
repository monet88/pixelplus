package chatgptweb

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
var errMaskNotPNG = errors.New("chatgptweb: mask asset did not decode as PNG")

// alphaMaskFromCanonical converts a canonical mask into the alpha convention the
// ChatGPT Web image path consumes.
//
// The canonical Mask Convention (ADR 0003) is luminance, white = edit, opaque
// PNG. The ChatGPT family consumes the opposite: alpha 0 marks the editable
// region and non-transparent pixels are protected context. On this path the
// evidence is `.ref/chatgpt2api`, whose
// `openai_v1_image_edit.py:36-44` does `putalpha(luminance)` and whose line 26
// states that the low-alpha region is the region to edit — sending canonical
// white = edit into it unchanged would edit the black region, the exact inverted
// result. ADR 0003 records that `.ref/chatgpt2api/NOTES.md` §4.4 reason 3 claims
// the opposite and is wrong.
//
// So the transform is alpha_out = 255 - luminance_in, the same direction as the
// Codex path but reached through a different reference surface. The duplication
// between the two Adapters is deliberate: ADR 0003 makes the Adapter that talks
// to upstream own its inversion, so a future divergence on one Provider path
// cannot silently change the other.
//
// The output is NRGBA, not RGBA, deliberately. Go's color.RGBA is
// alpha-premultiplied, so a light pixel that must become fully transparent has
// no valid premultiplied representation (R > A is not a legal RGBA color) and
// the value would not survive an encode/decode round trip. NRGBA stores the
// alpha channel independently, which is what the wire format needs.
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
