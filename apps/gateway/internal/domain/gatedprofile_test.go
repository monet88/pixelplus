package domain_test

import (
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

func TestZeroGatedProfileEnablesNothing(t *testing.T) {
	t.Parallel()

	var profile domain.GatedProfile
	for _, mode := range []domain.AuthMode{
		domain.AuthModeChatGPTCodexOAuth,
		domain.AuthModeGeminiAntigravityOAuth,
		domain.AuthModeGrokXAIOAuth,
		domain.AuthModeChatGPTWebAccess,
		domain.AuthModeGrokWebSSO,
	} {
		if profile.AllowsGated(mode) {
			t.Errorf("zero GatedProfile allows %s; production composition must enable nothing", mode)
		}
	}
	// The zero value must block every gated mode: that is what keeps an ordinary
	// production deployment fail-closed without naming the control.
	if !profile.BlocksGated(domain.AuthModeChatGPTCodexOAuth) {
		t.Error("zero GatedProfile does not block chatgpt_codex_oauth")
	}
	if !profile.BlocksGated(domain.AuthModeGrokXAIOAuth) {
		t.Error("zero GatedProfile does not block grok_xai_oauth")
	}
}

func TestGatedProfileEnablesOnlyNamedGatedModes(t *testing.T) {
	t.Parallel()

	profile := domain.NewGatedProfile(domain.AuthModeChatGPTCodexOAuth)

	if !profile.AllowsGated(domain.AuthModeChatGPTCodexOAuth) {
		t.Fatal("named gated mode is not allowed")
	}
	if profile.BlocksGated(domain.AuthModeChatGPTCodexOAuth) {
		t.Error("named gated mode is still blocked")
	}
	// Enabling one gated mode must not enable a sibling gated mode: Auth Mode is
	// the unit of risk decision (§1.4).
	if profile.AllowsGated(domain.AuthModeGrokXAIOAuth) {
		t.Error("enabling chatgpt_codex_oauth also enabled grok_xai_oauth")
	}
	if !profile.BlocksGated(domain.AuthModeGrokXAIOAuth) {
		t.Error("unnamed gated mode is not blocked")
	}
}

func TestGatedProfileIgnoresProhibitedAndExperimentalModes(t *testing.T) {
	t.Parallel()

	// An operator writing a prohibited or experimental mode into gated
	// configuration must not get it enabled through this door. Grok Web SSO is
	// hard off (§2 status table, §5.5); experimental modes are governed by their
	// own lab profile (decision 0013), never by a gated profile.
	profile := domain.NewGatedProfile(
		domain.AuthModeGrokWebSSO,
		domain.AuthModeChatGPTWebAccess,
		domain.AuthModeGeminiWebCookie,
		domain.AuthMode("not_a_real_mode"),
	)

	for _, mode := range []domain.AuthMode{
		domain.AuthModeGrokWebSSO,
		domain.AuthModeChatGPTWebAccess,
		domain.AuthModeGeminiWebCookie,
		domain.AuthMode("not_a_real_mode"),
	} {
		if profile.AllowsGated(mode) {
			t.Errorf("gated profile enabled non-gated mode %s", mode)
		}
	}
	// An experimental mode is not blocked BY THIS CONTROL either: its own
	// lab-profile gate applies.
	if profile.BlocksGated(domain.AuthModeChatGPTWebAccess) {
		t.Error("gated profile blocks an experimental mode; lab profile is a separate control")
	}
}

func TestGatedProfileEnablesMultipleGatedModesWhenNamed(t *testing.T) {
	t.Parallel()

	profile := domain.NewGatedProfile(
		domain.AuthModeChatGPTCodexOAuth,
		domain.AuthModeGeminiAntigravityOAuth,
		domain.AuthModeGrokXAIOAuth,
	)

	if !profile.AllowsGated(domain.AuthModeChatGPTCodexOAuth) {
		t.Error("chatgpt_codex_oauth not allowed")
	}
	if !profile.AllowsGated(domain.AuthModeGeminiAntigravityOAuth) {
		t.Error("gemini_antigravity_oauth not allowed")
	}
	if !profile.AllowsGated(domain.AuthModeGrokXAIOAuth) {
		t.Error("grok_xai_oauth not allowed")
	}
}
