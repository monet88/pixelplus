package domain

// CanonicalCapabilityBaseline returns the strongest CapabilityStatus the
// accepted evidence supports for one Auth Mode and operation, and whether a
// baseline is recorded at all.
//
// The values come from the ChatGPT capability evidence document
// (docs/spec/research/chatgpt-auth-mode-capability-evidence.md §2.1 and §2.2),
// which records every one of the five primary operations on BOTH ChatGPT Auth
// Modes as `conditionally supported` — reference-learned, never end-to-end
// upstream-verified (§11 conclusion 2). No operation is `verified` on either
// mode, so the ceiling for both is `conditionally_supported`.
//
// This is a CEILING, not a floor. An Adapter that observes `unsupported` keeps
// `unsupported`; only a claim STRONGER than the accepted evidence is lowered.
//
// Cause and effect: a lab probe of a fresh ChatGPT Web account succeeds and the
// Adapter reports `verified` chat. Without this ceiling the minted snapshot
// would publish a stronger promise than any accepted evidence supports, purely
// because one probe worked. §2.2 makes capability maturity orthogonal to risk
// and §7 rule 2 forbids a snapshot from implying risk acceptance; raising the
// ceiling therefore requires editing the evidence document (new authority), not
// running a luckier probe.
//
// A mode with no recorded baseline returns ok == false and is left unclamped:
// the Gemini and Grok evidence documents are separate normative inputs and
// belong to their own Adapter stories, and inventing a ceiling for them here
// would be an unevidenced product decision.
func CanonicalCapabilityBaseline(mode AuthMode, operation CapabilityOperation) (CapabilityStatus, bool) {
	if !operation.Valid() {
		return "", false
	}
	switch mode {
	case AuthModeChatGPTWebAccess, AuthModeChatGPTCodexOAuth:
		return CapabilityConditionallySupported, true
	default:
		return "", false
	}
}

// ClampToCanonicalBaseline lowers an observed status to the accepted evidence
// ceiling for the Auth Mode and operation, and returns it unchanged when no
// baseline is recorded or the observation is already at or below the ceiling.
func ClampToCanonicalBaseline(mode AuthMode, operation CapabilityOperation, observed CapabilityStatus) CapabilityStatus {
	baseline, ok := CanonicalCapabilityBaseline(mode, operation)
	if !ok {
		return observed
	}
	return weakerCapabilityStatus(observed, baseline)
}

// capabilityStatusRank orders the status vocabulary by how strong a claim it
// makes. `unverified` and `unsupported` are both weaker than any offerable
// status; they are distinct outcomes (nothing was learned vs. it was learned to
// be absent) and clamping never converts one into the other, so they share a
// rank floor and are returned unchanged whenever they are the observation.
func capabilityStatusRank(status CapabilityStatus) int {
	switch status {
	case CapabilityVerified:
		return 3
	case CapabilityConditionallySupported:
		return 2
	default:
		// unsupported / unverified / unknown values: weaker than any ceiling this
		// function can impose, so they are never raised by a clamp.
		return 1
	}
}

// weakerCapabilityStatus returns whichever status makes the weaker claim,
// preserving the observed value's identity when it is already weaker. Unlike
// WeakerOfferableStatus it accepts non-offerable statuses, because a clamp must
// be able to pass `unsupported` and `unverified` through untouched.
func weakerCapabilityStatus(observed, ceiling CapabilityStatus) CapabilityStatus {
	if capabilityStatusRank(observed) <= capabilityStatusRank(ceiling) {
		return observed
	}
	return ceiling
}
