package domain

// LabProfile is the explicit operator enablement of `experimental` Auth Modes in
// one deployment. The zero value enables nothing, so a composition that never
// names a mode keeps every experimental gate failing closed — which is the
// required posture for ordinary production (auth-mode risk envelope §2 table:
// experimental modes default "off everywhere ... unless an operator deliberately
// enables a lab profile", §5.1, §6.1).
//
// It is a value, not a port: it performs no I/O and has no failure mode, so
// there is nothing to fail closed at runtime beyond the zero value already
// being closed.
type LabProfile struct {
	// enabled is nil for the zero value. A nil map reads as empty, so
	// AllowsExperimental is safe on an uninitialized LabProfile.
	enabled map[AuthMode]struct{}
}

// NewLabProfile builds a profile enabling exactly the named `experimental` Auth
// Modes. Any mode that is not experimental is ignored rather than accepted:
//
//   - A `prohibited` mode (Grok Web SSO) is hard off and configuration that
//     enables it in a product deployment is a policy defect (§2 status table),
//     so this door must not be a way in.
//   - A `gated` mode is governed by its own operator feature flag plus Tenant
//     acknowledgement (§5.2, §5.4, §5.6), not by a lab profile.
//   - An unknown mode fails closed to `prohibited` via RiskStatus and is
//     therefore ignored for the same reason as Grok Web SSO.
func NewLabProfile(modes ...AuthMode) LabProfile {
	profile := LabProfile{}
	for _, mode := range modes {
		if mode.RiskStatus() != RiskExperimental {
			continue
		}
		if profile.enabled == nil {
			profile.enabled = make(map[AuthMode]struct{}, len(modes))
		}
		profile.enabled[mode] = struct{}{}
	}
	return profile
}

// AllowsExperimental reports whether this deployment deliberately enabled the
// `experimental` Auth Mode as a lab profile.
//
// It answers only the enablement question. Enablement is necessary but never
// sufficient: the caller must still apply the Tenant residual-risk
// acknowledgement (RequiresRiskAck), lifecycle, health, and administrative
// controls, so the operator flag opens the path and the Tenant disclosure walks
// through it (§6.1 experimental row, §6.2).
//
// A non-experimental mode always returns false, including a `gated` mode that
// this deployment may legitimately run: gated enablement is a different control
// and must not be read out of this one.
func (profile LabProfile) AllowsExperimental(mode AuthMode) bool {
	if mode.RiskStatus() != RiskExperimental {
		return false
	}
	_, ok := profile.enabled[mode]
	return ok
}

// BlocksExperimental is the gate-site form of the question: it reports whether
// the Auth Mode must be refused because it is experimental and this deployment
// did not enable it. Non-experimental modes are never blocked by this control;
// their own gates (Prohibited, RequiresRiskAck) still apply.
func (profile LabProfile) BlocksExperimental(mode AuthMode) bool {
	return mode.Experimental() && !profile.AllowsExperimental(mode)
}
