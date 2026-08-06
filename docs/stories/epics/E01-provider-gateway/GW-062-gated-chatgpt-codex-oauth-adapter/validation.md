# Validation

## Proof Strategy

Every test enters through the **public API seam** and the production
composition root: the HTTP handler for connection/probe/chat/stream, and the
composition-vended Adapter for the protocol-decoder unit tests. Private
functions, handler stubs, direct use-case calls, concrete schema queries,
Provider SDK shapes, and goroutine-layout assertions are not completion
evidence (issue #62 public proof seam).

The gated enablement and the no-silent-fallback invariants are proved against
the **same** composition root in both directions (profile off → rejected;
profile on + ack → served), which is what a runtime profile lets one test do
that a build tag cannot (decision 0013 alternatives).

No real credential material and no live network call appear anywhere. The
Adapter's `Transport` is a fixture over sanitized payloads; a nil transport
fails closed, which is the shipped production posture.

## Test Plan

| Layer | Cases |
| --- | --- |
| Unit | `GatedProfile` zero-value fail-closed; accepts only gated modes; drops prohibited/experimental/unknown; `BlocksGated` predicate. Adapter signal classification (auth-expired, `usage_limit_reached`, `rate_limit_error`, Cloudflare-challenged, protocol drift). SSE decoder for Codex Responses events (content delta, finish, usage-limit, image tool, drift). Commit-certainty ladder: nothing-produced → NotCommitted; drift-after-evidence → Unknown; truncated → Unknown; clean → Committed. No-retry invariant (one exchange per Run/Stream). Probe binds to the named account/mode/version. |
| Integration | Composition: profile off → no Codex Adapter registered (fail-closed dispatch). Profile on + nil transport → registered, every surface fail-closed. Profile on + fixture transport + ack → chat/stream/probe/capability served through the public HTTP seam. |
| E2E | Connection → acknowledgement → probe → chat (non-stream) → chat stream, all through the HTTP handler against the composed runtime with a fixture transport. |
| Platform | n/a (no platform-shell change). |
| Performance | n/a (fixture transport; no latency contract). |
| Logs/Audit | No credential material, refresh token, or raw Provider payload in any audit event, log, or error (OP-G3). Audit events carry tenant/account/mode/version/operation/model only. |

## Fixtures

Sanitized `Transport` implementations over `testdata/` payloads (no real
secrets):

- `token_refresh.json` — refresh exchange result (rotated bundle projection;
  no secret material).
- `chat_stream.sse` — one short Codex Responses SSE turn (content delta +
  finish + `[DONE]`).
- `image_generate.sse` — Codex `image_generation` tool `generate` event stream.
- `image_edit.sse` — Codex `image_generation` tool `edit` event stream.
- `entitlement_free.json` — free-plan account attributes (image tool not
  injected → image operation `conditionally_supported`/entitlement-missing).
- `quota_rate.json` — `usage_limit_reached` + `rate_limit_error` bodies with
  `resets_in_seconds`.
- `challenge.json` — Cloudflare/bot block on an image path (403 shape).
- `protocol_drift.sse` — an undecodable Responses event sequence.

## Commands

```text
cd apps/gateway
go build ./...
go vet ./...
go test ./...
```

## Acceptance Evidence

Verified green: `cd apps/gateway && go build ./... && go vet ./... && go test ./...`
(all packages pass; 28 chatgptcodex unit tests plus the gated contract tests).

- [x] AC1: operator flag + Tenant ack reject before credential storage/use when
      absent — `TestGatedCodexCreateRefusedWithoutOperatorFlag`,
      `TestGatedCodexCredentialUseRefusedBeforeAnyVaultWriteWithoutFlag`,
      `TestGatedCodexCredentialUseRequiresExplicitTenantAck`, and the control
      `TestGatedCodexCreateAllowedWhenOperatorEnablesTheMode` (all public seam).
      Positive registration invariant:
      `TestGatedAdaptersRegisterOnlyWhenEnabledAndGivenATransport` (the Codex
      adapter is built only when the operator names the mode AND supplies a
      transport; with either missing it dispatches to the fail-closed fallback).
- [x] AC2: OAuth refresh and protocol values stay inside Adapter/Vault boundary —
      `TestRefreshAndRetryOnAuthFailure`, `TestRefreshFailureIsAuthExpired`,
      `TestNoRefreshWithoutRefreshToken` (refresh runs inside
      `CredentialInjection.Use`; rotated tokens never reach an outcome), plus the
      `TestFixturesCarryNoRealSecrets` guard (OP-G3).
- [x] AC3: fixtures cover refresh (token_refresh.json), chat/stream
      (chat_stream.sse), image operations (image_generate.sse, image_edit.sse),
      entitlement (entitlement_free.json), quota/rate (quota_rate.json),
      challenge (challenge.json), protocol drift (protocol_drift.sse) — pinned by
      `TestEveryRequiredFixtureFamilyIsPresent`.
- [x] AC4: Adapter does not retry a full chat/stream operation
      (`TestRunDoesNotRetryAFullOperation` asserts one Responses exchange; a
      401 triggers only a single in-boundary refresh+resend) and reports
      NotCommitted / Unknown / Committed commit certainty
      (`TestRunNothingProducedIsNotCommitted`, `TestRunDriftAfterEvidenceIsUnknown`,
      `TestRunTruncatedIsUnknown`, `TestRunImageOnlyTurnIsUnknown`,
      `TestRunCommitsACleanTurn`).
- [x] AC5: probe evidence binds to the exact account/mode/version/operation/model
      via `ProbeCommand`/`CapabilityObservationCommand` +
      `ProbeSurface` (`TestProbeProvesAuthWithoutRunningAGeneration`,
      `TestObserveReportsOnlyEvidenceBackedCapability`). The per-account
      credential transport binding remains an open follow-up (#111), recorded in
      the ADR and `Transport` doc comment; Probe/Observe fail closed on a nil
      transport.
- [x] AC6: no silent fallback to ChatGPT Web Access — the existing
      `TestCrossModeFallbackIntoTheExperimentalModeStaysRefused` pins the
      Codex primary → Web fallback from inside an enabled profile (FG-2 / §6.3).
      Kill recovery via documented evidence is the existing §3.5.5 reopen
      checklist; T19 ships no silent cross-mode fallback path.
