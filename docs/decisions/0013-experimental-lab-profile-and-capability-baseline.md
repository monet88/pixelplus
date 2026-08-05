# 0013 Experimental Lab Profile and Canonical Capability Baseline

Date: 2026-08-05

## Status

Accepted

## Context

Gateway T18 (#61) adds the first `experimental` Auth Mode Adapter, ChatGPT Web
Access. Two questions had no locked answer, and both change security-relevant
behavior, so neither belonged in a code comment.

**1. What does "default off everywhere" mean mechanically?**

The risk envelope §2 status table says an `experimental` mode is "default off
everywhere ... unless an operator deliberately enables a lab profile", and §6.1
adds that it "MUST NOT appear in ordinary production Tenant self-serve
catalogs". §7 rule 1 requires the composition root to consult risk status before
registering an Adapter. None of that says how the deployment expresses the
enablement, and the code had no representation of a lab profile at all.

Auditing the existing gates while answering this found three places that
checked only `AuthMode.Prohibited()` and therefore admitted an `experimental`
mode in ordinary production once the Tenant acknowledged risk:

- `ProviderAccountService` create (`internal/application/provideraccount.go`)
- the credential Auth Mode gate (`internal/application/credential.go`)
- `/v1/models` offer eligibility (`internal/application/capability.go`)

So the question was not only "how do we add a lab profile" but "what closes the
production hole that exists today".

**2. What stops a probe from inflating accepted capability?**

`docs/spec/research/chatgpt-auth-mode-capability-evidence.md` §2.1 records every
primary operation on both ChatGPT modes as `conditionally supported`
(reference-learned) and none as `verified`; §10.1 gap 1 records that no live
probe was ever run. But `domain.NewLiveProbeSnapshot` accepted whatever status an
Adapter reported. A fresh account probe returning `verified` would have minted a
snapshot claiming stronger support than any evidence backs, and §7 rule 2 says a
snapshot must not imply risk acceptance.

## Decision

**A `domain.LabProfile` value type is the enablement mechanism.** Composition
builds it from `Config.ExperimentalLabAuthModes` and passes it to every service
that gates on Auth Mode. Three properties are load-bearing:

- The zero value enables nothing, so production is fail-closed by construction
  rather than by remembering to set a flag.
- `NewLabProfile` silently drops any mode whose risk status is not
  `experimental`. Naming `grok_web_sso` (prohibited) or an OAuth mode (gated) has
  no effect, so the profile can never become a general-purpose bypass or a way
  to skip a gated mode's own feature flag.
- `BlocksExperimental` is the single predicate all six gate sites call, so the
  rule has one definition.

**Registration is a composition-time decision, not a runtime branch.**
`internal/adapters` gained four Auth-Mode-dispatching registries, and
`internal/composition/experimental.go` builds one only when the profile named a
mode that has an Adapter. With the profile off, the Adapter is absent from the
composed object graph — not present and bypassed. Enabling a mode still grants
no egress: the Adapter's `Transport` is a separate dependency an operator must
supply deliberately.

**The capability baseline is a domain-level ceiling.**
`domain.CanonicalCapabilityBaseline` encodes the evidence document's ceiling for
the two ChatGPT modes, and `NewLiveProbeSnapshot` clamps both operation facts
and per-model rows through it. It is a ceiling and not a floor: `unsupported`
and `unverified` observations pass through unchanged, so "we saw it fail"
survives. Raising an operation past `conditionally_supported` requires editing
the evidence document — new authority, not a probe result or a code change.

**The cross-mode fallback rule stays strict.** `fallback_auth_modes` continues
to reject an experimental mode even inside an enabled lab profile, because FG-2
and §6.3 forbid silent cross-mode fallback regardless of enablement.
`ValidateRoutingPolicyShape` was deliberately left untouched (upstream impact
analysis: HIGH risk, 7 impacted symbols, 4 affected processes including the
durable routing ledger).

It needs no change because the rule it enforces is *unconditional*, not because
a lab policy happens to be shaped a particular way. The validator takes only
`RoutingPolicy` and never receives a `LabProfile`, so it structurally cannot
consult enablement — and must not, since FG-2 and §6.3 forbid cross-mode
fallback into an experimental mode *regardless* of enablement. Its refusal is
therefore correct in both compositions for the same reason.

Two code paths make that exhaustive, and neither depends on how an operator
writes a lab policy:

- With `fallback_enabled: false`, declaring any `fallback_auth_modes` is already
  a shape error (`fallback disabled with chain or modes`), so the per-mode loop
  is never reached with an entry to inspect.
- With `fallback_enabled: true`, the per-mode loop rejects any
  `mode.Experimental()` outright with `ErrRoutingPolicyModeUnavailable`.

`TestCrossModeFallbackIntoTheExperimentalModeStaysRefused` pins the second path
from inside an *enabled* lab profile — the only case where a regression could
hide — and also asserts `routing.Replace` ran zero times, so a refused shape is
never persisted.

An earlier draft of this record justified the omission by saying "a lab policy
names a single experimental account with `fallback_enabled: false`". That
reasoning was wrong even though the decision was right: it described a usage
convention rather than an enforced invariant, and it would have implied the
function becomes unsafe as soon as a lab policy enables fallback. It does not —
the refusal above is unconditional.

## Alternatives Considered

1. **A build tag for the experimental Adapter.** Rejected: the negative claim
   ("production does not register the mode") could then only be tested by
   building the binary twice. A runtime profile lets one test assert both
   directions against the same composition root, which is what
   `TestLabRegistrationReplacesTheStubAdapterWithoutGrantingEgress` does.

2. **Register the Adapter unconditionally and let the Auth Mode gate refuse.**
   Rejected: §6.1 and §7 rule 1 are about registration, not only about request
   outcomes. An Adapter present in the object graph is one deploy-time mistake
   away from reachable.

3. **Clamp the capability status inside the Adapter.** Rejected: a second
   Adapter — or a fixture adapter that lies — would bypass it, and the evidence
   ceiling would live in as many places as there are Adapters. In the domain
   there is exactly one place to re-read when the evidence document changes.

4. **Model the lab profile as a port.** Rejected: it is a deployment-time value
   with no I/O. A port would imply it can vary per request, which is precisely
   the property that must not exist.

## Consequences

Positive:

- Three pre-existing production holes are closed. An `experimental` account can
  no longer be created, activated, or advertised on `/v1/models` in ordinary
  production.
- The Auth Mode gate runs before any Vault call, so an Auth Mode kill (§3.5.4)
  stops new Adapter use before decrypt and before any Provider call. Contract
  tests assert the zero counters, not just the status code.
- No live probe can inflate an accepted capability baseline on either ChatGPT
  mode.

Tradeoffs:

- Six gate sites now consult the lab profile. Adding a seventh gate means
  remembering the predicate; nothing structurally forces it.
- Enabling the profile replaces the controlled stub Adapter with the real one
  for that Auth Mode, so a contract test cannot both enable the mode and script
  a controlled capability observation for it. The clamp is therefore proved on
  the sibling `chatgpt_codex_oauth` mode, which shares the code path.
- `CanonicalCapabilityBaseline` covers only the two ChatGPT modes, because only
  their evidence document is normative here. Gemini and Grok snapshots stay
  unclamped until their own evidence lands.

## An image-only turn is UNKNOWN, not committed and not not-committed

The Adapter decodes the image asset pointer (the reference's three-part rule) but
the canonical chat vocabulary has nowhere to put it: `domain.ChatChoice.Message`
and `domain.ChatDelta` both hold text only, and this Adapter implements no
`ports.RenderAdapter`. So an image-only turn has a real upstream generation and
no deliverable result.

Neither ordinary class is honest. `committed` returns a successful, empty answer
— indistinguishable to the caller from "the model said nothing" — and discards
the only evidence that a generation happened. `not_committed` is authoritative
no-commit proof, which authorizes the spine's single fallback re-attempt, so it
would pay for a second image the upstream already produced and likely billed.

The turn is therefore classified UNKNOWN / `execution_possibly_committed`: it
withholds the fabricated success and withholds permission to re-generate. The
pointer is never returned, since `domain.ChatCompletion` carries no raw Provider
payload.

The alternative — adding an asset carrier to the canonical chat types — was
rejected as out of scope. It changes a shared `domain` contract every Provider
Adapter depends on, and image operations belong to the render surface. Surfacing
image results on the chat surface is a product decision for a later story, not an
implementation detail of T18.

## The render surface refuses an experimental mode in every composition

The render candidate gate is the one gate site that does **not** consult
`LabProfile`. `RenderService.candidateRejection` refuses
`account.AuthMode.Experimental()` unconditionally, and `RenderDependencies`
deliberately carries no `LabProfile` field.

The reason is that enabling a mode and being able to serve it are different
facts, and only on this surface do they come apart. `chatgptweb.Adapter`
implements `ports.ChatAdapter`, `ChatStreamAdapter`, `ProbeAdapter`, and
`CapabilityAdapter` — but not `ports.RenderAdapter` — and
`composition/experimental.go` builds no render registry. So the only render port
an experimental account can reach is `FailClosedRenderAdapter`, in the lab
composition exactly as in production.

Cause and effect if the gate consulted the profile instead: an enabled deployment
answers `POST /v1/images/generations` with `202 Accepted`, durably enqueues the
Render Job, returns to the caller — and the worker then fails it against the
fail-closed foundation. That converts a refusal the Gateway can make
synchronously, before any durable side effect, into an accepted job that dies
later, which is precisely the asynchronous-spend risk AC6 exists to prevent.

An earlier revision of this record justified the missing `renderAdapter` helper
by saying the render "candidate gate already refuses this Auth Mode". That was
true in production and false in the lab composition, which is the only case where
it mattered: the gate consulted `BlocksExperimental`, so an enabled profile
admitted the job. `TestRenderRefusesTheExperimentalModeInEveryComposition` now
pins both compositions, asserts zero enqueues and zero Vault use in each, and
carries a `gated`-mode control so a render path that refused everything could not
pass it.

A later story that gives an experimental Adapter a real `ports.RenderAdapter`
must relax this gate and add the render registry **together**; either alone is
incoherent.

The Capability Snapshot is deliberately unaffected. `Adapter.Observe` keeps
reporting the image operations as `conditionally_supported`, matching the
capability spec's own baseline matrix (§4.3: ChatGPT Web Access is `cond` for
`image_generation`, `image_edit`, and `inpaint`). A snapshot records what the
**account** can do, not what this Gateway build can currently serve, so
downgrading it to `unsupported` would misreport the evidence in order to describe
a composition gap.

## Follow-Up

- A Gemini Web Cookie baseline when that mode's capability evidence is written.
- The numeric FG-5 / KS-2 challenge-rate and drift thresholds (#61 non-goal).
- A real `chatgptweb.Transport` implementation, which is a separate authorized
  change and requires authorization for the exact account before any live probe.
- Whether ChatGPT Web image results should be deliverable at all, and on which
  surface. Until that is decided, an image-only chat turn is UNKNOWN rather than
  a silently empty success.
- A `renderAdapter` helper in `composition/experimental.go` if a later story
  gives an experimental Adapter a real `ports.RenderAdapter`, together with
  relaxing the unconditional experimental refusal in
  `RenderService.candidateRejection`. Its absence is currently intentional and
  recorded in a comment at both sites.
- An operator-facing way to enable the lab profile in the shipped binary (#96).
  `cmd/gateway` parses no `ExperimentalLabAuthModes`, so only a test composition
  can set one today. Deferred deliberately: adding the environment surface widens
  the production attack surface and #61 authorized no live probe.
- A distinguishable challenge outcome on the probe path (#97). `signalChallenged`
  currently maps to `ports.ErrDependencyUnavailable`, so a bot interstitial is
  indistinguishable from a 500 and the FG-5 / KS-2 challenge-rate counters cannot
  be fed from it. `domain.HealthReasonChallengeDetected` already exists, but
  `ports` has no challenge-class probe outcome, so this needs a port change and
  is out of T18's scope.
