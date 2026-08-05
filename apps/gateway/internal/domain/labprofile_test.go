package domain_test

import (
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

func TestZeroLabProfileEnablesNothing(t *testing.T) {
	t.Parallel()

	var profile domain.LabProfile
	for _, mode := range []domain.AuthMode{
		domain.AuthModeChatGPTWebAccess,
		domain.AuthModeGeminiWebCookie,
		domain.AuthModeChatGPTCodexOAuth,
		domain.AuthModeGrokWebSSO,
	} {
		if profile.AllowsExperimental(mode) {
			t.Errorf("zero LabProfile allows %s; production composition must enable nothing", mode)
		}
	}
	// The zero value must block every experimental mode: that is what keeps an
	// ordinary production deployment fail-closed without naming the control.
	if !profile.BlocksExperimental(domain.AuthModeChatGPTWebAccess) {
		t.Error("zero LabProfile does not block chatgpt_web_access")
	}
	if !profile.BlocksExperimental(domain.AuthModeGeminiWebCookie) {
		t.Error("zero LabProfile does not block gemini_web_cookie")
	}
}

func TestLabProfileEnablesOnlyNamedExperimentalModes(t *testing.T) {
	t.Parallel()

	profile := domain.NewLabProfile(domain.AuthModeChatGPTWebAccess)

	if !profile.AllowsExperimental(domain.AuthModeChatGPTWebAccess) {
		t.Fatal("named experimental mode is not allowed")
	}
	if profile.BlocksExperimental(domain.AuthModeChatGPTWebAccess) {
		t.Error("named experimental mode is still blocked")
	}
	// Enabling one experimental mode must not enable the sibling experimental
	// mode: Auth Mode is the unit of risk decision (§1.4).
	if profile.AllowsExperimental(domain.AuthModeGeminiWebCookie) {
		t.Error("enabling chatgpt_web_access also enabled gemini_web_cookie")
	}
	if !profile.BlocksExperimental(domain.AuthModeGeminiWebCookie) {
		t.Error("unnamed experimental mode is not blocked")
	}
}

func TestLabProfileIgnoresProhibitedAndGatedModes(t *testing.T) {
	t.Parallel()

	// An operator writing a prohibited or gated mode into lab configuration must
	// not get it enabled through this door. Grok Web SSO is hard off (§2 status
	// table, §5.5); gated modes are governed by their own feature flag plus
	// Tenant acknowledgement, never by the lab profile.
	profile := domain.NewLabProfile(
		domain.AuthModeGrokWebSSO,
		domain.AuthModeChatGPTCodexOAuth,
		domain.AuthModeGeminiAntigravityOAuth,
		domain.AuthModeGrokXAIOAuth,
		domain.AuthMode("not_a_real_mode"),
	)

	for _, mode := range []domain.AuthMode{
		domain.AuthModeGrokWebSSO,
		domain.AuthModeChatGPTCodexOAuth,
		domain.AuthModeGeminiAntigravityOAuth,
		domain.AuthModeGrokXAIOAuth,
		domain.AuthMode("not_a_real_mode"),
	} {
		if profile.AllowsExperimental(mode) {
			t.Errorf("lab profile enabled non-experimental mode %s", mode)
		}
	}
	// A gated mode is not blocked BY THIS CONTROL either: its own gates apply.
	if profile.BlocksExperimental(domain.AuthModeChatGPTCodexOAuth) {
		t.Error("lab profile blocks a gated mode; gating is a separate control")
	}
}

func TestLabProfileEnablesBothExperimentalModesWhenNamed(t *testing.T) {
	t.Parallel()

	profile := domain.NewLabProfile(domain.AuthModeChatGPTWebAccess, domain.AuthModeGeminiWebCookie)

	if !profile.AllowsExperimental(domain.AuthModeChatGPTWebAccess) {
		t.Error("chatgpt_web_access not allowed")
	}
	if !profile.AllowsExperimental(domain.AuthModeGeminiWebCookie) {
		t.Error("gemini_web_cookie not allowed")
	}
}
