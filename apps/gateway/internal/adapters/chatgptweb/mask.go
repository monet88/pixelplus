package chatgptweb

import (
	"fmt"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

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
// The mechanical transform (alpha_out = 255 - luminance_in, NRGBA encoding) is
// shared with the sibling chatgptcodex Adapter via domain.AlphaMaskFromCanonical
// so a convention drift cannot silently fork the two Adapters; the decision to
// invert belongs here, per ADR 0003, at the component that talks to upstream.
//
// The error is wrapped, not re-created: callers match on the single
// domain.ErrMaskNotPNG sentinel (via errors.Is), while the wrap adds the
// Adapter name to the message for operator triage.
func alphaMaskFromCanonical(canonical []byte) ([]byte, error) {
	encoded, err := domain.AlphaMaskFromCanonical(canonical)
	if err != nil {
		return nil, fmt.Errorf("chatgptweb: %w", err)
	}
	return encoded, nil
}
