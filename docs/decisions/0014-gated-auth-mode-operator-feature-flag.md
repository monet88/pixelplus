# 0014 Gated Auth Mode Operator Feature Flag

Date: 2026-08-06

## Status

Accepted

## Context

Gateway T19 (#62) adds the first `gated` Auth Mode Adapter, ChatGPT Codex OAuth.
Decision 0013 built `domain.LabProfile` for the `experimental` Auth Modes and
made it experimental-only by construction: `NewLabProfile` silently drops any
mode whose risk status is not `experimental`, so the profile can never become a
general-purpose bypass or a way to skip a gated mode's own controls. That
decision left the `gated` enablement question open on purpose — a gated mode is
governed by a different control (risk envelope §5.2: "Feature flag default off
until deployment opts in. Require Tenant acknowledgement of residual ToS/ban
risk at connection"), and 0013 was already a large, security-relevant change.

Two facts force a locked answer now, and both change authorization behavior, so
neither belongs in a code comment.

**1. What does "feature flag default off" mean mechanically for a gated mode?**

The risk envelope §2 status table says a `gated` mode is "default off at
deployment and Tenant levels until gates are satisfied", and §5.2 makes the
operator feature flag the first gate. §7 rule 1 requires the composition root to
consult risk status before registering an Adapter. The code had a representation
for the experimental answer (`LabProfile`) and none for the gated answer, so a
`gated` mode had no deployment-level enablement at all.

Auditing the existing gates while answering this found that the create gate
(`internal/application/provideraccount.go`) checked only `Prohibited()` and
`BlocksExperimental()`, so a `gated` mode could be **created** without an
operator flag. The Tenant acknowledgement check (`RequiresRiskAck` &&
`!RiskAcknowledged`) already existed at credential use, capability offer, and
render candidate, but the operator flag — the gate §5.2 puts first — did not.

So the question is not only "how do we add a gated profile" but "what closes the
operator-flag hole that exists today before any credential is stored".

**2. Why not reuse `LabProfile`?**

Because 0013 deliberately made it the wrong shape. `LabProfile.AllowsExperimental`
returns false for every non-experimental mode, including a `gated` mode this
deployment may legitimately run. Reading gated enablement out of it would
require either widening `LabProfile` to accept gated modes (which 0013 rejected
to keep it from becoming a bypass) or inventing a parallel field on it. Either
conflates two controls that the spec keeps separate: the experimental lab
profile is operator-only with no Tenant ack on the experimental surface, while a
gated mode requires BOTH the operator flag AND the Tenant residual-risk
acknowledgement (§5.2, §6.2). Conflating them would let naming a gated mode in
the experimental list skip the Tenant acknowledgement — the opposite of #62 AC1.

## Decision

**A `domain.GatedProfile` value type is the enablement mechanism for `gated`
modes, parallel to `LabProfile`.** Composition builds it from
`Config.GatedAuthModes` and passes it to every service that gates on Auth Mode.
Three properties are load-bearing, mirroring 0013:

- The zero value enables nothing, so production is fail-closed by construction
  rather than by remembering to set a flag.
- `NewGatedProfile` silently drops any mode whose risk status is not `gated`.
  Naming `grok_web_sso` (prohibited), a Web Access mode (experimental), or an
  unknown mode has no effect, so the profile can never become a bypass for a
  prohibited mode or a way to skip an experimental mode's own lab profile.
- `BlocksGated` is the single predicate all five gate sites call, so the rule
  has one definition.

**Registration is a composition-time decision, not a runtime branch.**
`internal/composition/gated.go` builds a `gatedAdapters` set only when the
profile named a mode that has an Adapter, and `runtime.go` constructs the
Adapter only when a `Transport` is also supplied. With the profile off or the
transport nil, the Adapter is absent from the composed object graph — not
present and bypassed — and the mode dispatches to the fail-closed fallback on
every surface. Granting egress is therefore always a deliberate, second operator
decision beyond naming the mode. (This narrows 0013's "register a nil-transport
Adapter that fails closed" for the gated code path; the nil-transport fail-closed
posture is still proved by the chatgptcodex package test TestNilTransportFailsClosed,
so no property is lost.)

**The Tenant acknowledgement stays a separate, already-enforced gate.**
`RequiresRiskAck` + `RiskAcknowledged` runs at credential use, capability offer,
and render candidate, after the operator flag and before any Vault decrypt or
Adapter call. AC1 is therefore two gates in order: operator flag first
(`BlocksGated`), then Tenant ack (`RequiresRiskAck`). Neither can substitute for
the other.

**The cross-mode fallback rule stays strict and is pinned for Codex.**
`fallback_auth_modes` continues to refuse an experimental mode unconditionally
(0013). T19 makes no change to `routing.go`: the existing
`mode.Experimental()` refusal already keeps ChatGPT Web Access out of any
fallback chain, so a Codex account can never silently fall back to Web Access —
which is exactly FG-2 / §6.3 for the Codex→Web direction. A gated fallback
target the operator did not enable is refused at candidate selection by
`authModeGate` (`BlocksGated`), so it cannot be reached either. The existing
T18 contract test TestCrossModeFallbackIntoTheExperimentalModeStaysRefused pins
the Codex primary → Web fallback case from inside an *enabled* profile — the
only place a regression could hide.

**The render surface does NOT single out gated modes in T19.** The render
candidate gate refuses `prohibited` and `experimental` modes (the 0013 rule for
the experimental Web surface) and otherwise behaves like any mode without a
`ports.RenderAdapter`: a gated Codex account can be a render candidate, and
because the Codex Adapter implements chat, stream, probe, and capability — not
`ports.RenderAdapter` — a render job for it fails closed at execution against
the fail-closed foundation. That accept-then-fail posture is the same one that
already applies to any mode lacking a RenderAdapter, so T19 introduces nothing
new on the render surface. A later story that gives Codex a real
`ports.RenderAdapter` must add the gated render registry (and may then tighten
the candidate gate together with it). In T19 the Codex image capability is still
reported in the Capability Snapshot (`conditionally_supported`), because a
snapshot records what the account can do, not what this build can currently
serve.

## Alternatives Considered

1. **Widen `LabProfile` to accept gated modes.** Rejected: it would let naming a
   gated mode in `ExperimentalLabAuthModes` satisfy the operator-flag gate
   without the Tenant acknowledgement, which inverts AC1. The two controls must
   stay separate types.

2. **Register the Codex Adapter unconditionally and let the gate refuse.**
   Rejected for the same reason 0013 rejected it: §6.1 and §7 rule 1 are about
   registration, not only request outcomes. An Adapter present in the object
   graph is one deploy-time mistake away from reachable.

3. **A build tag for the Codex Adapter.** Rejected for the same reason 0013
   rejected it: the negative claim could then only be tested by building twice.
   A runtime profile lets one test assert both directions against the same
   composition root.

4. **Implement `ports.RenderAdapter` for Codex in this story.** Rejected as out
   of scope. It adds the render spine, `AuthorizedRender` wiring, a gated render
   registry, and a render-candidate gate relaxation — a coherent second story.
   The chat/probe/capability Adapter proves the gating mechanism and the Codex
   protocol translation independently, exactly as T18 did for experimental.

## Consequences

Positive:

- The pre-existing operator-flag hole is closed. A `gated` account can no longer
  be created, activated, advertised on `/v1/models`, or selected as a render
  candidate in a deployment that did not enable the mode.
- The operator-flag gate runs before any Vault call, so disabling the mode stops
  new Adapter use before decrypt and before any Provider call (§3.5.4 Adapter
  path pause). Contract tests assert the zero counters, not just the status
  code.
- The Codex→Web silent-fallback invariant is pinned from inside an enabled gated
  profile, the only case where a regression could hide.
- `GatedProfile` covers every `gated` mode (Codex, Antigravity, Grok xAI), so
  the next gated Adapter reuses the mechanism without a new decision.

Tradeoffs:

- Five gate sites now consult the gated profile. Adding a sixth gate means
  remembering the predicate; nothing structurally forces it (the same
  tradeoff 0013 accepted for the six experimental gate sites).
- Enabling the profile replaces the fail-closed foundation with the real Adapter
  for that Auth Mode, so a contract test cannot both enable the mode and script
  a controlled capability observation for it. The clamp is proved on the sibling
  `chatgpt_codex_oauth` mode's baseline, which is already in place from 0013.
- The `Transport` per-account credential binding gap (#111) is still latent:
  Probe and Observe carry identifiers but no credential, and composition
  supplies one Transport per deployment. T19 ships the field nil so both methods
  fail closed first; a real Transport MUST NOT be wired before #111 resolves
  (recorded on the `Transport` doc comment, as 0013 did).

## Follow-Up

- A `ports.RenderAdapter` for Codex plus the gated render registry, so a gated
  Codex account can be served on the render surface rather than failing closed
  at execution against the fail-closed foundation. A later story adds both
  together and may then tighten the render candidate gate; T19 leaves the gate
  unchanged for gated modes.
- An operator-facing way to enable the gated profile in the shipped binary
  (`cmd/gateway` parsing `GatedAuthModes`), sibling to the 0013 follow-up for
  `ExperimentalLabAuthModes` (#96). Deferred deliberately: adding the
  environment surface widens the production attack surface and T19 authorizes no
  live probe.
- Per-account credential binding on `ports.ProbeAdapter` /
  `ports.CapabilityAdapter` (#111), which is what unblocks a real Codex
  `Transport`. T19 ships fixtures only.
- A distinguishable challenge outcome on the probe path (#97), so the FG-5/KS-2
  challenge-rate counters can be fed. Codex carries no sentinel/PoW/Turnstile
  (evidence §6: all `unsupported`), so this is lower priority for Codex than for
  Web Access but still needed for the Cloudflare-on-image-path case.
- Gemini Antigravity OAuth and Grok xAI OAuth Adapters reuse `GatedProfile`
  without a new decision; their own capability baselines land with their
  evidence documents.
