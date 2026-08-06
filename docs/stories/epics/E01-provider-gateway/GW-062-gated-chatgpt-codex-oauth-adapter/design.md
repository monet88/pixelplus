# Design

## Domain Model

T19 adds the **ChatGPT Codex OAuth Adapter** for the `chatgpt_codex_oauth` Auth
Mode. That mode is already a first-class `domain.AuthMode` value
(`AuthModeChatGPTCodexOAuth`): `RiskStatus()` returns `gated`,
`SupportsRefresh()` returns `true`, `RequiresRiskAck()` returns `true`, and
`RequiredCredentialClass()` returns `oauth_token_import`. The
`CanonicalCapabilityBaseline` already covers it (every primary operation clamps
to `conditionally_supported`), so no domain capability change is needed.

The one missing domain concept is the **operator feature flag for `gated`
modes**. Decision 0013 built `domain.LabProfile` for `experimental` modes only:
`NewLabProfile` silently drops any non-experimental mode, and `BlocksExperimental`
is the single predicate the gate sites call. A `gated` mode is governed by a
different control (risk envelope §5.2: "Feature flag default off until
deployment opts in. Require Tenant acknowledgement … at connection"), so reading
it out of `LabProfile` is deliberately impossible.

T19 introduces `domain.GatedProfile` as the parallel enablement value:

- The zero value enables nothing, so production is fail-closed by construction
  rather than by remembering to set a flag (same property that made `LabProfile`
  safe).
- `NewGatedProfile(modes...)` accepts **only** `gated` modes. Naming
  `grok_web_sso` (prohibited), a Web Access mode (experimental), or an unknown
  mode has no effect, so the profile can never become a general-purpose bypass
  or a way to skip an experimental mode's own lab profile.
- `BlocksGated(mode)` is the single predicate every gate site calls: it returns
  true only when the mode is `gated` AND this deployment did not enable it.
  Non-gated modes return false; their own gates (`Prohibited`,
  `BlocksExperimental`, `RequiresRiskAck`) still apply.

It is a value, not a port: deployment-time, no I/O, no failure mode. The Tenant
residual-risk acknowledgement (`RequiresRiskAck` + `RiskAcknowledged`) is a
**separate, already-enforced** gate that runs after the operator flag and before
any Vault decrypt or Adapter call (AC1: both must reject before credential
storage/use when absent).

The Adapter itself is a stateless protocol translator, identical in shape to the
T18 `chatgptweb.Adapter`: the only field is a `Transport` seam; a nil transport
makes every method fail closed with `ErrTransportUnavailable`. Registering the
Adapter is not the same as granting egress.

## Application Flow

Composition builds a `gatedAdapters` set (parallel to `experimentalAdapters`)
only for the gated modes the operator named in `Config.GatedAuthModes` AND for
which a `Transport` dependency is supplied. With the profile off, the Adapter is
absent from the composed object graph — not present and bypassed (§7 rule 1).

The gate sites gain one predicate each, in the established order
(prohibited → experimental-blocked → **gated-blocked** → risk-ack):

| Gate site | File | Added check |
| --- | --- | --- |
| Provider Account create | `application/provideraccount.go` | `BlocksGated(command.AuthMode)` |
| Credential usability | `application/credential.go` | `BlocksGated(account.AuthMode)` |
| `/v1/models` offer | `application/capability.go` | `BlocksGated(account.AuthMode)` |
| Render candidate | `application/render.go` | `BlocksGated(account.AuthMode)` added, alongside the existing prohibited + experimental refusal; two-tier for gated modes: an operator-disabled gated mode is refused here, while an operator-enabled gated account (Codex) passes this gate and fails closed later, at execution, for lack of a `ports.RenderAdapter` |
| Routing fallback target | `application/routing.go` | no gate change in T19: the existing `mode.Experimental()` refusal already keeps ChatGPT Web Access out of any fallback chain (so Codex→Web stays refused, FG-2 / §6.3); a gated fallback target the operator did not enable is refused at candidate selection by `authModeGate` (`BlocksGated`) |

The chat execution candidate gate (`application/chat.go`) DOES check
`BlocksGated`: `candidateRejection` calls it ahead of any Vault decrypt or
Adapter call, so an operator-disabled gated mode (Codex) is rejected at the
candidate gate itself, before credential use. Composition-time registration
remains a second, independent line of defence: when the operator did not
enable the gated profile and supply a transport, the gated chat registry is
absent, so a Codex chat command that somehow reached dispatch would still fail
closed (dependency_unavailable) before any Provider call. The Tenant
acknowledgement still applies on this surface regardless.

AC6 (kill recovery requires documented evidence, no silent fallback to ChatGPT
Web Access) is enforced at the routing fallback gate: `fallback_auth_modes` may
not contain `chatgpt_web_access` as a recovery target for a Codex account. The
existing experimental refusal (`mode.Experimental()` in `routing.go:223`) already
keeps Web Access out of fallback chains; T19 adds a test pinning that a Codex
account cannot fall back to Web Access even inside an enabled gated profile.

The Adapter implements `ports.ChatAdapter`, `ports.ChatStreamAdapter`,
`ports.ProbeAdapter`, and `ports.CapabilityAdapter`. It does **not** implement
`ports.RenderAdapter` in this story. The render candidate gate refuses
`prohibited` and `experimental` modes (the 0013 rule for the experimental Web
surface) and otherwise behaves like any mode without a RenderAdapter, so a gated
Codex account can be a render candidate and a render job for it fails closed at
execution against the fail-closed foundation — the same posture as any mode
lacking a RenderAdapter. T19 adds nothing new on the render surface and records
a follow-up to add a `ports.RenderAdapter` + gated render registry **together**
in a later story. The Codex image capability is still reported in the Capability
Snapshot (`conditionally_supported`), matching the evidence baseline; a snapshot
records what the account can do, not what this build can currently serve.

## Interface Contract

No Public API shape change. The frozen `auth_mode` enum already includes
`chatgpt_codex_oauth`; the connection, probe, chat, and stream seams already
dispatch by Auth Mode through the registries. T19 fills the registry entry for
that mode behind the operator flag.

The Adapter's `Transport` seam carries the same `UNRESOLVED` constraint 0013
recorded: `ports.ProbeAdapter` and `ports.CapabilityAdapter` carry identifiers
but no credential, and composition supplies one Transport per deployment, so a
real client MUST NOT ship against this seam before per-account credential
binding lands (tracked on the `Transport` doc comment, blocked by #111). T19
ships the field nil; both methods fail closed with `ErrTransportUnavailable`
before any exchange. Fixtures implement `Transport` over sanitized payloads.

OAuth refresh stays inside the Adapter/Vault boundary (AC2): the Codex bundle
(`access_token`, `refresh_token`, `account_id`) is injected via
`ports.CredentialInjection.Use`; refresh is performed inside that callback and
the rotated material never reaches a struct field, log, error string, or
returned outcome (OP-G3). The Adapter exposes no refresh port; the Vault owns
persistence of rotated tokens.

## Data Model

No schema change. `GatedProfile` is an in-memory value built from
`Config.GatedAuthModes`; it is never persisted. Provider Account, Credential,
Health, and Capability Snapshot schemas are unchanged. The Codex credential
bundle is stored encrypted in the Vault under the existing
Tenant/account/AuthMode/version binding (`CredentialClassOAuthTokenImport`).

## UI / Platform Impact

`cmd/gateway` parses no `GatedAuthModes` flag yet, mirroring the 0013 follow-up
that left `ExperimentalLabAuthModes` un-wired in the shipped binary. Only a test
composition can set a gated profile today; adding the environment surface widens
the production attack surface and T19 authorizes no live probe. This is recorded
as a follow-up (operator-facing enablement, #96 sibling).

## Observability

No new metric. The Adapter's signal classification (`usage_limit_reached`,
`rate_limit_error`, auth-expired, protocol drift) maps onto the existing
`HealthReason` / `ProbeSignalClass` vocabulary the health and probe spines
already consume. The FG-5/KS-2 challenge-rate counters are out of scope (0013
follow-up #97): the Codex executor path carries no sentinel/PoW/Turnstile
(evidence §6: all `unsupported`), so the challenge-class signals are Cloudflare
on image paths only and are classified as `challenged` exactly as Web Access
classifies a 403.

## Alternatives Considered

1. **Read gated enablement out of `LabProfile`.** Rejected: 0013 deliberately
   made `LabProfile` experimental-only so it can never become a bypass for a
   gated mode's own feature flag. A gated mode is a different control (operator
   flag + Tenant ack); conflating them would let naming a gated mode in
   `ExperimentalLabAuthModes` skip the Tenant acknowledgement, which is the
   opposite of AC1.

2. **Register the Codex Adapter unconditionally and let the gate refuse.**
   Rejected for the same reason 0013 rejected it for experimental: §6.1 and §7
   rule 1 are about registration, not only request outcomes. An Adapter present
   in the object graph is one deploy-time mistake away from reachable.

3. **Implement `ports.RenderAdapter` for Codex in this story.** Rejected as
   out of scope. It adds the render spine, `AuthorizedRender` wiring, and a
   gated render registry, and relaxes the render candidate gate — a coherent
   second story. Shipping the chat/probe/capability Adapter first lets the
   gating mechanism and the Codex protocol translation be proved independently,
   exactly as T18 proved the experimental mechanism on the chat surface.

4. **A build tag for the Codex Adapter.** Rejected for the same reason 0013
   rejected it: the negative claim ("production does not register the mode")
   could then only be tested by building twice. A runtime profile lets one test
   assert both directions against the same composition root.
