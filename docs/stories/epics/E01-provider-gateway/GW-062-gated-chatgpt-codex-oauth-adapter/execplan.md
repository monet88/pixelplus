# Exec Plan

## Goal

Add the gated ChatGPT Codex OAuth Adapter and the operator feature-flag
mechanism that governs every `gated` Auth Mode, so a Tenant behind both the
operator flag and the explicit acknowledgement can connect and exercise Codex
chat/stream through stable Public API and production composition, with
controlled conformance fixtures and no silent fallback to ChatGPT Web Access.

## Scope

In scope:

- `domain.GatedProfile` — the operator feature-flag value for `gated` modes
  (zero value fail-closed; accepts only gated modes; `BlocksGated` predicate).
- `adapters/chatgptcodex` — stateless protocol Adapter (chat, stream, probe,
  capability) over a `Transport` seam, with Codex Responses translation, OAuth
  refresh inside the credential boundary, and the commit-certainty ladder.
- `composition/gated.go` + `runtime.go` — `gatedAdapters` set + wiring, built
  only when the profile named the mode and a Transport was supplied.
- Application gate updates — `BlocksGated` at create, credential use,
  `/v1/models` offer, render candidate, and routing fallback target.
- Fixtures — refresh, chat/stream, image operations, entitlement, quota/rate,
  challenge, protocol drift (sanitized; no real credential material).
- Contract tests through the public API seam — gated mode rejected without
  operator flag; rejected without Tenant ack; accepted and served with both;
  no Codex→Web fallback.

Out of scope:

- `ports.RenderAdapter` for Codex + gated render registry (render refused at
  candidate gate; follow-up).
- Real `Transport` / live probe (#111).
- `cmd/gateway` `GatedAuthModes` environment surface (deferred).
- FG-5/KS-2 numeric thresholds and counters (#97).
- Multi-account load balancing (D-MULTI-ACCT).

## Risk Classification

Risk flags:

- Auth (Codex OAuth refresh, token custody).
- Authorization (operator feature flag, Tenant acknowledgement, Tenant
  isolation).
- Audit/security (credential/vault boundary, OP-G3 redaction).
- External systems (ChatGPT Codex `/backend-api/codex/responses` surface).
- Public contracts (public API seam; no shape change, but new mode dispatch).
- Existing behavior (composition root, five gate sites, routing fallback).

Hard gates:

- Auth.
- Authorization.
- External provider behavior.
- Audit/security.

## Work Phases

1. Discovery — read T18 template, normative authority, gate sites, decision
   0013 (done).
2. Design — `GatedProfile` mechanism, Adapter shape, gate integration, fixture
   plan (this packet + ADR 0014).
3. Validation planning — fixture matrix + contract test matrix (validation.md).
4. Implementation — TDD: domain → adapter → composition → gates → fixtures →
   contract tests.
5. Verification — `go build ./...`, `go vet ./...`, `go test ./...` green; the
   full suite once at the end.
6. Harness update — `harness-cli story add` / `story update` for GW-062 proof
   status; ADR row via `decision add`.

## Stop Conditions

Pause for human confirmation if:

- The gated enablement mechanism would need to weaken the Tenant
  acknowledgement or the no-silent-fallback invariant.
- A gate-site change touches the durable routing ledger or `ValidateRoutingPolicyShape`
  (0013 left it untouched on purpose; HIGH upstream impact).
- A fixture would require real credential material or a live network call.
- The render surface coherence rule (refuse gated + no RenderAdapter) cannot be
  maintained without a behavior change outside T19 scope.
