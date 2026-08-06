package composition_test

import (
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/composition"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// These tests pin the posture of the production composition inputs themselves,
// which is the story's central negative claim (#61 AC2, risk envelope §6.1).
//
// The contract tests in internal/contracttest prove that a composition WITHOUT a
// lab profile refuses the experimental mode. They cannot prove that the shipped
// binary is such a composition, because they build their own Config. These do.

func TestProductionConfigEnablesNoExperimentalAuthMode(t *testing.T) {
	t.Parallel()

	// The zero Config is what cmd/gateway starts from: it sets only
	// StartupTimeout and ProviderAccountStorePath, never the lab profile. Any
	// non-empty default here would enable an experimental mode in every
	// deployment that did not explicitly clear it — the opposite of default-off.
	if modes := (composition.Config{}).ExperimentalLabAuthModes; len(modes) != 0 {
		t.Fatalf("zero Config enables %v, want no experimental Auth Mode", modes)
	}
}

func TestProductionDependenciesSupplyNoExperimentalTransport(t *testing.T) {
	t.Parallel()

	// Even with a lab profile on, an Adapter with no transport cannot reach a
	// Provider. Production must not supply one, so enabling a mode and granting
	// egress stay two separate deliberate acts by an operator.
	if transport := composition.ProductionDependencies().ExperimentalChatGPTWebTransport; transport != nil {
		t.Fatal("ProductionDependencies supplied ChatGPT Web transport; production must grant no experimental egress")
	}
}

// The gated profile has the same two-key posture as the lab profile, and after
// the F6 seam landed there are now TWO ways to grant the gated Codex Adapter
// egress: the adapter transport and the adapter-free responder. Production must
// supply neither, so this enumerates both rather than only the original field —
// otherwise the responder would be an unguarded second door to the same egress.
//
// This matters beyond posture: the Codex Transport seam is deployment-wide and
// does not yet bind an exchange to the account named in the command, so a real
// client wired here could prove account A's session for account B (#111,
// decisions 0013/0014). Keeping production egress nil is what makes that gap
// latent rather than live.
func TestProductionDependenciesGrantNoGatedCodexEgress(t *testing.T) {
	t.Parallel()

	production := composition.ProductionDependencies()
	if production.GatedChatGPTCodexTransport != nil {
		t.Error("ProductionDependencies supplied a gated Codex transport; production must grant no gated egress")
	}
	if production.GatedChatGPTCodexResponder != nil {
		t.Error("ProductionDependencies supplied a gated Codex responder; the seam must not become a second egress door")
	}
}

// The zero Config must name no gated Auth Mode, for the same default-off reason
// the lab profile guard above exists: cmd/gateway starts from the zero Config.
func TestProductionConfigEnablesNoGatedAuthMode(t *testing.T) {
	t.Parallel()

	if modes := (composition.Config{}).GatedAuthModes; len(modes) != 0 {
		t.Fatalf("zero Config enables %v, want no gated Auth Mode", modes)
	}
}

func TestEveryExperimentalAuthModeIsBlockedByTheZeroLabProfile(t *testing.T) {
	t.Parallel()

	// Enumerate every Auth Mode rather than naming chatgpt_web_access, so a
	// future experimental mode (Gemini Web Cookie already qualifies) is covered
	// the day it is added instead of quietly defaulting to enabled. The domain
	// exposes no enumerator, so the list is spelled out; adding a seventh mode
	// without adding it here is the known gap.
	allModes := []domain.AuthMode{
		domain.AuthModeChatGPTWebAccess,
		domain.AuthModeChatGPTCodexOAuth,
		domain.AuthModeGeminiWebCookie,
		domain.AuthModeGeminiAntigravityOAuth,
		domain.AuthModeGrokWebSSO,
		domain.AuthModeGrokXAIOAuth,
	}

	var experimental []domain.AuthMode
	for _, mode := range allModes {
		if mode.RiskStatus() != domain.RiskExperimental {
			continue
		}
		experimental = append(experimental, mode)
		if !(domain.LabProfile{}).BlocksExperimental(mode) {
			t.Errorf("zero lab profile does not block experimental mode %s", mode)
		}
	}
	if len(experimental) != 2 {
		t.Fatalf("found %d experimental modes (%v), want 2 (chatgpt_web_access, gemini_web_cookie); "+
			"update this guard when the risk envelope status table changes", len(experimental), experimental)
	}
}
