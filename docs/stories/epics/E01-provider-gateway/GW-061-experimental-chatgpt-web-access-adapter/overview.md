# Overview

## Current Behavior

Every Provider Adapter port in the Gateway resolves to a fail-closed foundation.
`internal/adapters` holds only `doc.go`; no package translates a real Provider
protocol. Chat, streaming, probe, and capability observation all run against
injected fixture fakes in `contracttest` or against
`vault.NewFailClosed*` substitutes in production composition.

ChatGPT Web Access is already classified `experimental` in the domain
(`AuthMode.RiskStatus()` → `RiskExperimental`), and every execution surface
fails it closed with a hardcoded rejection:

- `application/chat.go` `candidateRejection` rejects `Experimental()` accounts.
- `application/render.go` `candidateRejection` rejects `Experimental()` accounts.
- `application/routing.go` `policyCandidateRejection` rejects them with the
  comment "Production fail-closed: experimental modes have no lab profile."
- `domain/routing.go` `ValidateRoutingPolicyShape` rejects an experimental
  `fallback_auth_modes` entry.

Three surfaces, however, did NOT reject an experimental mode — they checked only
`AuthMode.Prohibited()`. Discovered while auditing the gate sites for this story:

- `application/provideraccount.go` create
- `application/credential.go` `authModeGate`
- `application/capability.go` `accountAllowsOffers` (the `/v1/models` catalog)

So before this story a Tenant could create a `chatgpt_web_access` account in
ordinary production, submit a credential to it, activate it, and see it
advertised on `/v1/models`, provided it acknowledged the residual risk. Risk
envelope §6.1 forbids exactly that. Three of the six gate changes in this story
are therefore a tightening of production behavior, not the loosening the rest of
the story implies.

That comment names the gap this story closes: the code says *no lab profile
exists*, so an authorized lab deployment has no way to exercise the Auth Mode at
all. There is also no place to put ChatGPT Web protocol knowledge, so the
capability evidence in `docs/spec/research/chatgpt-auth-mode-capability-evidence.md`
has no executable expression and no drift guard.

## Target Behavior

- A `chatgptweb` Adapter package translates the ChatGPT Web Access protocol and
  nothing else. It owns no Tenant selection, no durable state, and no
  full-operation retry; those stay with the routing/spine/job layers that
  already own them.
- Composition gains an explicit experimental lab profile. It is off by default,
  so ordinary production composition neither registers nor exposes the mode: the
  existing fail-closed rejections keep firing unchanged when the profile is off.
- With the lab profile explicitly enabled AND a recorded Tenant risk
  acknowledgement, the mode becomes connectable and executable in that lab
  deployment only. Enabling the profile alone is not enough — disclosure comes
  before any protected credential use.
- Sanitized fixtures cover credential preparation, chat, streaming, image
  generation/edit, challenge, quota/rate, and protocol drift. No fixture carries
  a real secret.
- A fresh account probe records evidence at the canonical baseline from
  `chatgpt-auth-mode-capability-evidence.md` §2.1 and can never exceed it. An
  Adapter that reports `verified` for an operation the evidence caps at
  `conditionally supported` is clamped down, so probe success cannot promote
  capability (risk envelope §2.2, §7 rule 2).
- Kill/pause (`AuthModeExecutionEnabled == false`, or lab profile off) stops new
  Adapter use before Vault decrypt and before any Provider call.

## Affected Users

- Operators of an authorized lab deployment exercising ChatGPT Web Access.
- Tenant clients of ordinary production deployments, who must continue to see
  the mode as unavailable.
- Reviewers verifying that an `experimental` mode cannot leak into production
  self-service.

## Affected Product Docs

- `docs/spec/auth-mode-risk-envelope-and-kill-criteria.md` (§5.1, §6.1, §7)
- `docs/spec/research/chatgpt-auth-mode-capability-evidence.md` (§2.1, §3.1, §10)
- `docs/spec/capability-snapshot-and-model-availability-semantics.md`
- `docs/spec/provider-account-connection-and-credential-lifecycle.md`

## Non-Goals

- Any live probe against a real ChatGPT account. §10.1 gap 1 is explicit that no
  live probe has been run; running one requires authorization for the exact
  account and is out of scope here.
- Promoting ChatGPT Web Access out of `experimental`. §2.1 requires a recorded
  product-owner decision; this story keeps the status unchanged.
- Real HTTP egress to `chatgpt.com`. The Adapter translates protocol against a
  transport seam; the production transport implementation is a separate concern.
- Productized challenge solving. Refused outright (§3.3 OP-G6).
- The other five Auth Modes. Gemini Web Cookie is also `experimental` and shares
  the lab-profile gate, but its Adapter is not built here.
- Numeric KS-2/FG-5 threshold enforcement (#17 observability owns the counters).
