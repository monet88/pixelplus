package chatgptcodex

import (
	"fmt"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

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
//
// The error is wrapped, not re-created: callers match on the single
// domain.ErrMaskNotPNG sentinel (via errors.Is), while the wrap adds the
// Adapter name to the message for operator triage.
func alphaMaskFromCanonical(canonical []byte) ([]byte, error) {
	encoded, err := domain.AlphaMaskFromCanonical(canonical)
	if err != nil {
		return nil, fmt.Errorf("chatgptcodex: %w", err)
	}
	return encoded, nil
}
