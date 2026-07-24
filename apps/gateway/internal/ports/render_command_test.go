package ports_test

import (
	"reflect"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// Application must not hand prompt plaintext through an ordinary Adapter
// command (review finding #4 / ADR 0009).
func TestRenderCommandHasNoPromptField(t *testing.T) {
	t.Parallel()

	if _, ok := reflect.TypeOf(ports.RenderCommand{}).FieldByName("Prompt"); ok {
		t.Fatal("RenderCommand must not carry Prompt; confidential material is resolved only inside AuthorizedRender")
	}
	if _, ok := reflect.TypeOf(ports.AuthorizedRenderRequest{}).FieldByName("Prompt"); ok {
		t.Fatal("AuthorizedRenderRequest must not carry Prompt; the port resolves confidential material internally")
	}
	// AuthorizedRender is the application-facing port; RenderAdapter is the
	// post-authorization low-level surface.
	var _ ports.AuthorizedRender = nil
	var _ ports.RenderPromptStore = nil
}
