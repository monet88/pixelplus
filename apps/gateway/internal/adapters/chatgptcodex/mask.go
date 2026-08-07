package chatgptcodex

import (
	"errors"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
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
// The mechanical transform (alpha_out = 255 - luminance_in, NRGBA encoding) is
// shared with the sibling chatgptweb Adapter via domain.AlphaMaskFromCanonical
// so a convention drift cannot silently fork the two Adapters; the decision to
// invert belongs here, per ADR 0003, at the component that talks to upstream.
func alphaMaskFromCanonical(canonical []byte) ([]byte, error) {
	encoded, err := domain.AlphaMaskFromCanonical(canonical)
	if err != nil {
		return nil, errMaskNotPNG
	}
	return encoded, nil
}
