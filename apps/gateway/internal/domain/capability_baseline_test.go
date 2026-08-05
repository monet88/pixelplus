package domain_test

import (
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

func TestCanonicalBaselineCapsChatGPTModesAtConditionallySupported(t *testing.T) {
	t.Parallel()

	// Evidence §2.1/§2.2 record every primary operation on both ChatGPT modes as
	// `conditionally supported`; none is `verified` (§11 conclusion 2).
	for _, mode := range []domain.AuthMode{
		domain.AuthModeChatGPTWebAccess,
		domain.AuthModeChatGPTCodexOAuth,
	} {
		for _, op := range domain.PrimaryCapabilityOperations() {
			baseline, ok := domain.CanonicalCapabilityBaseline(mode, op)
			if !ok {
				t.Fatalf("%s/%s has no recorded baseline", mode, op)
			}
			if baseline != domain.CapabilityConditionallySupported {
				t.Errorf("%s/%s baseline = %s, want conditionally_supported", mode, op, baseline)
			}
		}
	}
}

func TestCanonicalBaselineIsAbsentForUnevidencedModes(t *testing.T) {
	t.Parallel()

	// Gemini and Grok evidence documents are separate normative inputs; inventing
	// a ceiling for them here would be an unevidenced product decision.
	for _, mode := range []domain.AuthMode{
		domain.AuthModeGeminiWebCookie,
		domain.AuthModeGeminiAntigravityOAuth,
		domain.AuthModeGrokWebSSO,
		domain.AuthModeGrokXAIOAuth,
	} {
		if _, ok := domain.CanonicalCapabilityBaseline(mode, domain.CapabilityOpChat); ok {
			t.Errorf("%s has a baseline recorded without evidence in this story", mode)
		}
		// Unclamped means the observation passes through unchanged.
		got := domain.ClampToCanonicalBaseline(mode, domain.CapabilityOpChat, domain.CapabilityVerified)
		if got != domain.CapabilityVerified {
			t.Errorf("%s clamped an unevidenced mode to %s", mode, got)
		}
	}
}

func TestClampLowersOnlyStrongerClaims(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		observed domain.CapabilityStatus
		want     domain.CapabilityStatus
	}{
		{name: "verified is lowered to the ceiling", observed: domain.CapabilityVerified, want: domain.CapabilityConditionallySupported},
		{name: "conditionally supported is unchanged", observed: domain.CapabilityConditionallySupported, want: domain.CapabilityConditionallySupported},
		{name: "unsupported is never raised", observed: domain.CapabilityUnsupported, want: domain.CapabilityUnsupported},
		{name: "unverified is never raised", observed: domain.CapabilityUnverified, want: domain.CapabilityUnverified},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := domain.ClampToCanonicalBaseline(domain.AuthModeChatGPTWebAccess, domain.CapabilityOpChat, test.observed)
			if got != test.want {
				t.Fatalf("clamp(%s) = %s, want %s", test.observed, got, test.want)
			}
		})
	}
}

func TestLiveProbeSnapshotClampsOperationFactsAndModelRows(t *testing.T) {
	t.Parallel()

	now := domain.NewTimestamp(time.Date(2026, time.August, 5, 0, 0, 0, 0, time.UTC))
	// An Adapter that claims `verified` on every surface — the exact drift the
	// ceiling exists to absorb.
	operations := map[domain.CapabilityOperation]domain.CapabilityFact{}
	modelOperations := map[domain.CapabilityOperation]domain.CapabilityStatus{}
	for _, op := range domain.PrimaryCapabilityOperations() {
		operations[op] = domain.CapabilityFact{
			Status:        domain.CapabilityVerified,
			EvidenceClass: domain.EvidenceLiveProbe,
		}
		modelOperations[op] = domain.CapabilityVerified
	}

	snapshot := domain.NewLiveProbeSnapshot(
		"pa_lab_web",
		domain.AuthModeChatGPTWebAccess,
		1,
		now,
		operations,
		[]domain.ModelCapability{{ModelSlug: "gpt-image-2", Operations: modelOperations}},
		"lab_fixture",
	)

	for _, op := range domain.PrimaryCapabilityOperations() {
		if got := snapshot.Operations[op].Status; got != domain.CapabilityConditionallySupported {
			t.Errorf("operation %s minted as %s, want conditionally_supported", op, got)
		}
	}
	if len(snapshot.Models) != 1 {
		t.Fatalf("model rows = %d, want 1", len(snapshot.Models))
	}
	for op, status := range snapshot.Models[0].Operations {
		if status != domain.CapabilityConditionallySupported {
			t.Errorf("model row operation %s minted as %s, want conditionally_supported", op, status)
		}
	}
}

func TestLiveProbeSnapshotPreservesUnsupportedUnderTheCeiling(t *testing.T) {
	t.Parallel()

	now := domain.NewTimestamp(time.Date(2026, time.August, 5, 0, 0, 0, 0, time.UTC))
	snapshot := domain.NewLiveProbeSnapshot(
		"pa_lab_web",
		domain.AuthModeChatGPTWebAccess,
		1,
		now,
		map[domain.CapabilityOperation]domain.CapabilityFact{
			domain.CapabilityOpInpaint: {Status: domain.CapabilityUnsupported, EvidenceClass: domain.EvidenceLiveProbe},
		},
		nil,
		"lab_fixture",
	)

	// The clamp is a ceiling, never a floor: an honest `unsupported` must survive.
	if got := snapshot.Operations[domain.CapabilityOpInpaint].Status; got != domain.CapabilityUnsupported {
		t.Fatalf("inpaint minted as %s, want unsupported", got)
	}
}

func TestLiveProbeSnapshotClampDoesNotMutateAdapterOwnedMaps(t *testing.T) {
	t.Parallel()

	now := domain.NewTimestamp(time.Date(2026, time.August, 5, 0, 0, 0, 0, time.UTC))

	// The clamp writes the lowered status back into the maps it iterates. If it
	// wrote into the CALLER's maps instead of minting clones, an Adapter that
	// reused one observation struct across two accounts would see its own data
	// silently rewritten — and a second mint would then start from already-clamped
	// input, hiding whether the ceiling ever applied.
	operations := map[domain.CapabilityOperation]domain.CapabilityFact{
		domain.CapabilityOpChat: {
			Status:        domain.CapabilityVerified,
			EvidenceClass: domain.EvidenceLiveProbe,
		},
	}
	models := []domain.ModelCapability{{
		ModelSlug: "gpt-fixture-1",
		Operations: map[domain.CapabilityOperation]domain.CapabilityStatus{
			domain.CapabilityOpChat: domain.CapabilityVerified,
		},
		ObservedAt: now,
	}}

	snapshot := domain.NewLiveProbeSnapshot(
		"pa_alias", domain.AuthModeChatGPTWebAccess, 1, now,
		operations, models, "/backend-api/models",
	)

	// The snapshot is clamped...
	if got := snapshot.Operations[domain.CapabilityOpChat].Status; got != domain.CapabilityConditionallySupported {
		t.Fatalf("snapshot chat = %s, want conditionally_supported", got)
	}
	// ...and the caller's inputs are untouched.
	if got := operations[domain.CapabilityOpChat].Status; got != domain.CapabilityVerified {
		t.Errorf("caller operation map was mutated to %s, want verified preserved", got)
	}
	if got := models[0].Operations[domain.CapabilityOpChat]; got != domain.CapabilityVerified {
		t.Errorf("caller model row was mutated to %s, want verified preserved", got)
	}
}
