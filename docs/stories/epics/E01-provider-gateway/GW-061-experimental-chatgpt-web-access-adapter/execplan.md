# Exec Plan

## Goal

Let an authorized lab deployment connect and exercise ChatGPT Web Access
through its own protocol Adapter and sanitized fixtures, while ordinary
production composition neither exposes nor registers the mode, and while probe
evidence can never raise an operation above its canonical capability baseline.

## Scope

In scope:

- `domain.LabProfile`: explicit, zero-value-closed experimental enablement.
- `domain.CanonicalCapabilityBaseline` and the clamp inside
  `NewLiveProbeSnapshot`.
- Consulting the profile at the six existing experimental gate sites, including
  closing the three sites that check only `Prohibited` today.
- `internal/adapters/chatgptweb`: protocol-only Adapter over a `Transport` seam,
  implementing the probe, capability, chat, and chat-stream ports.
- Sanitized fixtures: credential preparation, chat, stream, image generate,
  image edit, challenge, quota/rate, protocol drift.
- Auth-Mode-dispatching Adapter registries in `internal/adapters`.
- Composition wiring: build and register the registries only when the lab
  profile names an experimental mode with an Adapter.
- Contract tests through the public HTTP seam for all six acceptance criteria.

Out of scope:

- Live probes against real ChatGPT accounts (evidence §10.1 gap 1; requires
  authorization for the exact account).
- A real HTTP transport to `chatgpt.com`.
- Promotion of ChatGPT Web Access out of `experimental`.
- Challenge solving of any kind (OP-G6 refuses it; KS-5 makes new anti-bot
  reverse engineering a kill trigger).
- Gemini Web Cookie's Adapter, though it shares the lab-profile gate.
- Numeric KS-2/FG-5 thresholds and their counters (#17).
- Lab console UI.

## Risk Classification

Risk flags:

- Auth — credential preparation and auth-class failure classification.
- Authorization — the profile decides whether a mode is connectable at all.
- Audit/security — OP-G3 forbids credential material in logs, errors, metrics.
- External systems — a Provider protocol Adapter.
- Public contracts — `/v1/models` offerability and capability snapshot values
  change for experimental accounts.
- Existing behavior — six gate sites and `NewLiveProbeSnapshot` change.
- Weak proof — no live probe exists for this surface; fixtures are the only
  available evidence.
- Multi-domain — domain, application, adapters, composition.

Hard gates:

- Auth.
- Authorization.
- Audit/security.
- External provider behavior.

## Work Phases

1. **Discovery.** Read risk envelope §5.1/§6.1/§7, capability evidence
   §2.1/§3.1/§10, existing gate sites, composition root, and the `.ref`
   protocol evidence. *(complete)*
2. **Design.** Lab profile shape, baseline clamp, Adapter seam, registry
   dispatch. Recorded in `design.md`. *(complete)*
3. **Validation planning.** Recorded in `validation.md`. *(complete)*
4. **Implementation.** Domain first (profile + clamp), then gate sites, then the
   Adapter and fixtures, then composition wiring.
5. **Verification.** `go vet`, `go build`, package tests per step, full
   `go test ./...` at the end, then `/code-review`.
6. **Harness update.** `harness-cli story add` / `story update`, and a durable
   decision record for the lab profile and the capability clamp.

## Stop Conditions

Pause for human confirmation if:

- Implementing an acceptance criterion would require a live probe against a real
  ChatGPT account. Stop and ask for authorization for the exact account
  (AC "any live probe requires authorization for the exact account").
- Any path would require solving or bypassing a challenge. Refuse outright
  rather than pausing (OP-G6); record it and stop.
- The capability baseline would have to be raised to make a test pass. That is
  a change to accepted evidence and needs new authority, not a code change.
- Closing the three production holes (create / credential / offers) breaks an
  existing contract test in a way that suggests production genuinely depends on
  connecting experimental modes. That would be a product-behavior question, not
  an implementation detail.
- A gate would have to be removed rather than made conditional.
