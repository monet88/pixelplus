package composition

import (
	"context"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/adapters/chatgptcodex"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// gatedRegistrationTransport is a no-op chatgptcodex.Transport used only to test
// whether newGatedAdapters registers the Codex adapter (the transport is never
// exercised here).
type gatedRegistrationTransport struct{}

func (gatedRegistrationTransport) Exchange(context.Context, chatgptcodex.Request) (chatgptcodex.Response, error) {
	return chatgptcodex.Response{Status: 500}, nil
}

// TestGatedAdaptersRegisterOnlyWhenEnabledAndGivenATransport pins the
// registration invariant (decision 0014): the gated Codex adapter is built only
// when BOTH the operator profile names the mode AND a transport is supplied.
// With either missing, no gated adapter is registered and the mode dispatches to
// the fail-closed fallback. This is the positive counterpart to the contracttest
// AC1 refusal tests, and it lives here (not in contracttest) because the
// composition layer is the one that may import adapter packages.
func TestGatedAdaptersRegisterOnlyWhenEnabledAndGivenATransport(t *testing.T) {
	t.Parallel()

	mode := domain.AuthModeChatGPTCodexOAuth

	t.Run("enabled and a transport supplied -> registered", func(t *testing.T) {
		enabled := newGatedAdapters(Config{GatedAuthModes: []domain.AuthMode{mode}}, Dependencies{
			GatedChatGPTCodexTransport: gatedRegistrationTransport{},
		})
		if enabled.none() {
			t.Fatal("no gated adapter registered despite an enabled profile and a supplied transport")
		}
		byMode := gatedByMode[ports.ChatAdapter](enabled)
		if len(byMode) != 1 || byMode[mode] == nil {
			t.Fatalf("chat registry for the enabled mode = %#v, want exactly one chat adapter for %s", byMode, mode)
		}
	})

	t.Run("enabled but no transport -> not registered", func(t *testing.T) {
		enabled := newGatedAdapters(Config{GatedAuthModes: []domain.AuthMode{mode}}, Dependencies{})
		if !enabled.none() {
			t.Fatal("a gated adapter was registered without a transport; enabling a mode must not grant egress")
		}
	})

	t.Run("transport supplied but mode not named -> not registered", func(t *testing.T) {
		enabled := newGatedAdapters(Config{}, Dependencies{GatedChatGPTCodexTransport: gatedRegistrationTransport{}})
		if !enabled.none() {
			t.Fatal("a gated adapter was registered without the operator naming the mode")
		}
	})
}
