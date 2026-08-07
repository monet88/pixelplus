package contracttest_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"testing"
)

// This file locks the canonical Mask Convention (#98) as a contract fact through
// the public HTTP surface over real production composition. It shares the
// harness, upload helper, and image fixtures of the asset exchange tests in this
// package.

// AC (#98): a mask that arrives already inverted is not silently corrected
// anywhere in the pipeline. The Gateway stores and serves exactly the bytes the
// client sent.
//
// This is the invariant that keeps ADR 0003's failure mode out of the codebase.
// The bug being prevented is not one wrong inversion, it is a pipeline holding a
// second opinion: once any component normalizes polarity on its own, a correct
// client and a correct Provider Adapter together produce an inverted edit, and
// no single component looks wrong in review. Inversion belongs to the Provider
// Adapter alone, which requires every other hop to be a pure carrier of bytes.
func TestMaskBytesAreCarriedUncorrectedThroughIngest(t *testing.T) {
	t.Parallel()

	harness := newAssetHarness(t, assetHarnessConfig{})

	// A mask that reads as "inverted" against the convention: white (which
	// canonically means edit) covers the region a designer meant to keep. The
	// Gateway cannot know the intent and must not guess.
	inverted := image.NewGray(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			value := uint8(255)
			if x < 4 {
				value = 0
			}
			inverted.SetGray(x, y, color.Gray{Y: value})
		}
	}
	encoded := &bytes.Buffer{}
	if err := png.Encode(encoded, inverted); err != nil {
		t.Fatalf("encode inverted mask fixture: %v", err)
	}
	sent := encoded.Bytes()

	_, payload := harness.upload(t, uploadSpec{
		bearer:   assetWriteKey,
		idemKey:  "idem-mask-no-autocorrect",
		kind:     "mask",
		partType: "image/png",
		content:  sent,
	})
	id, _ := decodeAsset(t, payload)["asset_id"].(string)
	if id == "" {
		t.Fatalf("asset id missing (body=%s)", payload)
	}

	response, served := harness.getContent(t, assetWriteKey, id)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("get content status = %d, want 200", response.StatusCode)
	}
	if !bytes.Equal(served, sent) {
		t.Fatalf("mask bytes changed in the pipeline: sent %d bytes, served %d bytes; "+
			"no component may silently correct mask polarity (ADR 0003)", len(sent), len(served))
	}
}
