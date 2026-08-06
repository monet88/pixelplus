package domain

// GatedProfile is the explicit operator enablement of `gated` Auth Modes in one
// deployment. The zero value enables nothing, so a composition that never names
// a mode keeps every gated gate failing closed — which is the required posture
// for ordinary production (auth-mode risk envelope §2 status table: a gated mode
// is "default off at deployment and Tenant levels until gates are satisfied",
// §5.2, §6.1).
//
// It is the gated twin of LabProfile. The two are separate types on purpose
// (decision 0014): an experimental lab profile is operator-only on the
// experimental surface, while a gated mode requires BOTH the operator flag AND
// the Tenant residual-risk acknowledgement (§5.2, §6.2). Conflating them would
// let naming a gated mode in the experimental list skip the Tenant
// acknowledgement, which is the opposite of #62 AC1.
//
// It is a value, not a port: it performs no I/O and has no failure mode, so
// there is nothing to fail closed at runtime beyond the zero value already
// being closed.
type GatedProfile struct {
	// enabled is nil for the zero value. A nil map reads as empty, so
	// AllowsGated is safe on an uninitialized GatedProfile.
	enabled map[AuthMode]struct{}
}

// NewGatedProfile builds a profile enabling exactly the named `gated` Auth
// Modes. Any mode that is not gated is ignored rather than accepted:
//
//   - A `prohibited` mode (Grok Web SSO) is hard off and configuration that
//     enables it in a product deployment is a policy defect (§2 status table),
//     so this door must not be a way in.
//   - An `experimental` mode is governed by its own lab profile (decision 0013),
//     not by a gated profile.
//   - An unknown mode fails closed to `prohibited` via RiskStatus and is
//     therefore ignored for the same reason as Grok Web SSO.
func NewGatedProfile(modes ...AuthMode) GatedProfile {
	profile := GatedProfile{}
	for _, mode := range modes {
		if mode.RiskStatus() != RiskGated {
			continue
		}
		if profile.enabled == nil {
			profile.enabled = make(map[AuthMode]struct{}, len(modes))
		}
		profile.enabled[mode] = struct{}{}
	}
	return profile
}

// AllowsGated reports whether this deployment deliberately enabled the `gated`
// Auth Mode.
//
// It answers only the operator-flag question. Enablement is necessary but never
// sufficient: the caller must still apply the Tenant residual-risk
// acknowledgement (RequiresRiskAck), lifecycle, health, and administrative
// controls, so the operator flag opens the path and the Tenant disclosure walks
// through it (§5.2 operator obligations, §6.2 acknowledgement themes).
//
// A non-gated mode always returns false, including an `experimental` mode that
// this deployment may legitimately run under a lab profile: experimental
// enablement is a different control and must not be read out of this one.
func (profile GatedProfile) AllowsGated(mode AuthMode) bool {
	if mode.RiskStatus() != RiskGated {
		return false
	}
	_, ok := profile.enabled[mode]
	return ok
}

// BlocksGated is the gate-site form of the question: it reports whether the Auth
// Mode must be refused because it is gated and this deployment did not enable
// it. Non-gated modes are never blocked by this control; their own gates
// (Prohibited, BlocksExperimental, RequiresRiskAck) still apply.
func (profile GatedProfile) BlocksGated(mode AuthMode) bool {
	return mode.Gated() && !profile.AllowsGated(mode)
}

// Gated reports whether the Auth Mode is `gated` under the product risk
// envelope. It is the gated twin of Experimental and lets a gate site name the
// risk class without repeating the RiskStatus comparison.
func (mode AuthMode) Gated() bool {
	return mode.RiskStatus() == RiskGated
}
