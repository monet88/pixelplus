# Validation

## Proof Strategy

Every acceptance criterion is proved through the public HTTP seam over
production composition (`composition.Runtime.Handler()` served by `httptest`).
Protocol translation is additionally proved by package tests in
`internal/adapters/chatgptweb` driving sanitized fixtures through the
`Transport` seam; those are unit proof of a leaf package, never completion
evidence for an acceptance criterion.

Two fixtures are needed because the story's central claim is a *difference*
between two compositions: one with no lab profile (ordinary production) and one
with the profile naming `chatgpt_web_access`. A criterion that says "production
does not expose the mode" is only proved when the same request that succeeds in
the lab fixture fails in the production fixture.

| AC | Proof |
|---|---|
| AC1 Adapter translates protocol only | Package tests assert translation only; an architecture test asserts the Adapter package imports no store, no admission, no routing, and holds no cross-call state. |
| AC2 Production does not expose or register the mode | Production fixture: create, credential submit, probe, `/v1/models`, chat, and render on a `chatgpt_web_access` account all fail closed; the composed graph carries no ChatGPT Web Adapter. |
| AC3 Lab requires explicit enablement and disclosure | Lab fixture: profile on but risk not acknowledged → `ack_risk`, zero Vault decrypts, zero Adapter calls. Profile on and acknowledged → succeeds. |
| AC4 Fixtures cover the protocol surface | Package tests over all eight fixture families; one test enumerates the required families so dropping one fails the suite, and another asserts no fixture contains a plausible secret. |
| AC5 Fresh probe cannot exceed the baseline | An Adapter reporting `verified` for every operation: the snapshot read back over HTTP shows `conditionally_supported`, and `unsupported` survives unchanged. See the deviation note under Acceptance Evidence for why this runs on the sibling ChatGPT mode. |
| AC6 Kill/pause stops use before Vault or Provider | Lab fixture with `AuthModeExecutionEnabled = false`: request fails closed with zero Vault decrypts and zero Adapter calls. Same for profile-off. |

The "zero Vault decrypts and zero Adapter calls" assertion is the load-bearing
one for AC3 and AC6, and it is why counting fakes are used rather than only
checking status codes: a 4xx response proves the request was refused, but only
the counters prove it was refused *before* the credential was touched. §3.5.4
requires the kill to stop use before the Adapter is entered, not merely to
discard its result.

## Test Plan

| Layer | Cases |
| --- | --- |
| Unit | `LabProfile` zero value allows nothing; enabling a prohibited mode is ignored; enabling a gated mode is ignored; enabling an experimental mode allows exactly that mode. Baseline clamp lowers `verified` to `conditionally_supported`, preserves `unsupported`, leaves unknown modes unclamped, and clamps model rows as well as operation facts. |
| Unit | Adapter protocol translation: SSE append patch, path-elided delta, batch patch array, `finished_successfully` → `stop`, moderation blocked → `content_filter`, marker/title/metadata events produce no delta, `[DONE]` ends the stream, unparseable payload classifies as drift, image pointer accepted only for `role=tool` + `async_task_type=image_gen`, `sediment://` on a user message rejected as input attachment, 401 → `Authenticated:false` as outcome not error, `limits_progress remaining=0` → quota-exhausted signal with reset seconds, 429 → rate-limited signal, challenge requirement classified without any solve attempt, nil transport → port unavailable error. |
| Integration | The six AC rows above, through the public HTTP seam over real composition. |
| Integration | Cross-mode fallback into `chatgpt_web_access` stays rejected even with the lab profile on (FG-2 / §6.3). |
| Integration | The three previously-open production sites (create, credential submit, `/v1/models` offers) now fail closed for an experimental mode with the profile off. |
| E2E | None. No live Provider probe is authorized in this story. |
| Platform | None. |
| Performance | None. |
| Logs/Audit | A fixture asserts that no audit event, error body, or log line emitted along the ChatGPT Web paths contains fixture secret sentinels (OP-G3). |

## Fixtures

Sanitized protocol fixtures under
`apps/gateway/internal/adapters/chatgptweb/testdata/`, one family per evidence
§10.2 probe recommendation:

| Family | Contents |
|---|---|
| `credential_prepare` | `/backend-api/me`, `conversation/init`, `accounts/check` responses with placeholder ids |
| `chat_stream` | full SSE transcript: `v1`, message add, appended deltas, batch patch, `[DONE]` |
| `moderation_blocked` | refusal text delivered as content, then `moderation_response.blocked` |
| `image_generate` | tool message with `file-service://` pointer and `async_task_type: image_gen` |
| `image_edit` | user `sediment://` input attachment plus tool output pointer |
| `challenge` | sentinel chat-requirements demanding proof-of-work and Turnstile |
| `quota_rate` | `limits_progress` with `remaining: 0` and a reset, plus a 429 |
| `protocol_drift` | unknown event type, malformed JSON, missing `conversation_id` |

Every credential-shaped value is an obvious placeholder
(`fixture-not-a-real-token`), and a test asserts that shape so a real secret
cannot be pasted in later without failing. The non-streaming surface reuses
`chat_stream.sse` rather than carrying its own fixture, because a non-stream
response on this Provider IS a client-side aggregation over the same SSE body
(evidence §2.1) — a separate fixture would let the two paths drift apart.

## Commands

```text
cd apps/gateway
go vet ./...
go build ./...
go test ./internal/domain/... -run 'LabProfile|Baseline' -count=1
go test ./internal/adapters/... -count=1
go test ./internal/contracttest/... -count=1
go test ./... -count=1
```

## Acceptance Evidence

Verified 2026-08-05 on branch `feature/issue-61-gateway-t18-chatgpt-web-adapter`.

```text
cd apps/gateway
go vet ./...                 # clean
go build ./...               # clean
go test ./... -count=1
  ok  cmd/gateway                              1.378s
  ok  internal/adapters                        0.047s
  ok  internal/adapters/chatgptweb             0.069s
  ok  internal/application                     0.063s
  ok  internal/composition                     0.145s
  ok  internal/contracttest                    0.765s
  ok  internal/domain                          0.040s
  ok  internal/infrastructure/jobs             0.087s
  ok  internal/infrastructure/persistence      0.176s
  ok  internal/infrastructure/vault            0.040s
  ok  internal/ports                           0.037s
```

`gofmt -l internal/ cmd/` reports only
`internal/contracttest/chat_settlement_detached_test.go`, which is unformatted on
`main` and untouched by this story.

| AC | Where proved | Result |
|---|---|---|
| AC1 | `internal/adapters/chatgptweb/boundary_test.go`: no import of application/composition/persistence/vault/transport; no `http.Client`/`NewRequest`/`Do`; the `Adapter` struct holds only `transport`; no mutable package-level var | pass |
| AC2 | `TestProductionSelfServiceRefusesTheExperimentalMode`, `TestProductionRefusesCredentialUseBeforeAnyVaultDecrypt`, `TestModelCatalogAdvertisesTheExperimentalModeOnlyInTheLab`, `TestProductionChatRefusesTheExperimentalModeBeforeTheAdapter`, `TestProductionRenderRefusesTheExperimentalModeBeforeEnqueue`, `TestLabRegistrationReplacesTheStubAdapterWithoutGrantingEgress`, `TestLabRegistrationDoesNotCaptureOtherAuthModes` | pass |
| AC3 | `TestLabProfileAdmitsTheExperimentalModeItNamed`, `TestLabConnectionRequiresDisclosureAsWellAsEnablement` (both sub-cases) | pass |
| AC4 | `TestEveryRequiredFixtureFamilyIsPresent`, `TestFixturesCarryNoRealSecrets`, plus the 14 protocol tests and 9 probe tests over the eight fixture families | pass |
| AC5 | `TestFreshProbeCannotRaiseAnOperationPastItsCanonicalBaseline`, `TestTheBaselineClampDoesNotDowngradeOtherAuthModes`, `TestLabProfileCannotEnableAProhibitedMode`, plus 5 domain clamp tests | pass |
| AC6 | `TestAuthModeKillStopsAdapterUseBeforeDecryptInsideTheLab`, `TestProductionRefusesCredentialUseBeforeAnyVaultDecrypt` | pass |

Supporting proofs beyond the six criteria:

- `TestCrossModeFallbackIntoTheExperimentalModeStaysRefused` — FG-2 / §6.3 hold
  even with the profile on, and the refused policy is not persisted.
- `TestExperimentalPathsNeverEmitCredentialMaterial` — OP-G3: the submitted
  sentinel appears in no response, read-back, audit event, telemetry event, or
  request-log projection.
- `TestChallengeIsClassifiedAndNeverSolved` — OP-G6 / KS-5: behind a
  proof-of-work, Turnstile, or Arkose requirement the conversation is opened 0
  times and the sentinel is called exactly once (no solve, no retry).

### Deviations from the plan above

Two rows were proved differently than planned, both because enabling the lab
profile registers the *real* Adapter in place of the controlled stub:

1. **AC5** was planned as "lab fixture with an Adapter reporting `verified`".
   That is unreachable: with the profile on, the registry dispatches
   `chatgpt_web_access` to the real Adapter, so no controlled `verified`
   observation can be produced for that mode. The clamp is keyed on Auth Mode in
   the domain and covers both ChatGPT modes identically, so it is proved through
   the public seam on `chatgpt_codex_oauth` and directly on
   `chatgpt_web_access` in `internal/domain/capability_baseline_test.go`.

2. **AC2's "the composed graph carries no ChatGPT Web Adapter"** is proved
   indirectly but sharply: with the profile on, the always-succeeding stub Probe
   Adapter is called 0 times and the probe returns 503, which is only possible if
   a different object served the call. With the profile off the stub answers
   normally. That difference is the registration proof.

### Added beyond the plan

`internal/composition/production_posture_test.go` pins the posture of the
production composition *inputs*, which the contract tests structurally cannot:
they build their own `Config`, so they prove "a composition without a lab profile
refuses the mode" but not "the shipped binary is such a composition".

- `TestProductionConfigEnablesNoExperimentalAuthMode` — the zero `Config` (what
  `cmd/gateway` starts from) names no experimental mode.
- `TestProductionDependenciesSupplyNoExperimentalTransport` —
  `ProductionDependencies()` supplies no ChatGPT Web transport, so enabling a
  mode and granting egress remain two separate deliberate operator acts.
- `TestEveryExperimentalAuthModeIsBlockedByTheZeroLabProfile` — both
  `experimental` modes are blocked by the zero profile, so Gemini Web Cookie is
  covered by the same gate without an Adapter of its own.

`go test -race` was additionally run clean over `internal/adapters/...`,
`internal/domain`, `internal/composition`, and `internal/contracttest`.

### Mutation checks on the absence proofs

Three assertions in this story are *absence* proofs ("zero Vault decrypts",
"zero Adapter calls", "the secret appears nowhere"). An absence proof can pass
because the thing genuinely did not happen, or because the test was looking in
the wrong place — so each was deliberately broken and confirmed to fail:

| Assertion | Mutation applied | Result |
|---|---|---|
| OP-G3 secret scan | scan for `pa_web_secret` (a value that IS present) instead of the credential sentinel | fails at all four scan sites — response body, read-back, and 3 audit events |
| AC6 kill precedes decrypt | flip `AuthModeExecutionEnabled` back to `true` | fails: status becomes 503, so the request reached the Adapter and the 409 really was the kill |
| AC2 registration difference | give the "production" harness the lab profile too | fails: production probe becomes 503, so the test measures the difference between compositions rather than a constant |

Each mutation was reverted and the suite re-run green.

### Four commit-classification bugs found and fixed during review

All four were found by probing the decoder and the failure paths with
transcripts no fixture covered, and all four were the same class of error: the
Adapter reported a commit certainty the evidence did not support. That is the
most damaging thing this Adapter can get wrong, because the spine treats
`committed` as authoritative (stops the fallback walk, bills the caller) and
treats `not_committed` as authorization to generate again.

1. **Unknown typed event with a `conversation_id` was silently ignored.**
   `decodeObject` fell through its `type` switch to a catch-all that treated any
   payload carrying a `conversation_id` as non-content. So a Provider that added
   one new self-describing event produced an empty completion classified
   `committed`/`stop`. Fixed by making an unrecognized `type` return
   `eventDrift` explicitly (`protocol.go`, `default` arm of the type switch).
   Regression:
   `TestUnknownTypedEventIsDriftEvenWhenItCarriesAConversationID`, plus a second
   unknown-type line added to `protocol_drift.sse`.

2. **A transcript of only recognized non-content events committed.** With no
   drift to detect and no content, `turnResult` was empty but nothing refused it,
   so `Run` minted one choice with an empty message and a `stop` finish class the
   Provider never sent. Fixed with `turnResult.producedNothing()` — no content, no
   image, no moderation block, and no terminal marker means not-committed on both
   surfaces (`chat.go`). Regression:
   `TestTranscriptThatProducesNothingIsNotCommitted` (streaming and
   non-streaming sub-cases).

3. **A non-streaming break after partial content claimed authoritative
   no-commit.** `chatFailureOutcome` reasoned from client exposure ("`Run`
   buffers, so nothing was delivered") when the question is what the UPSTREAM
   did. The upstream had demonstrably generated content and may have committed
   and billed it, so an authoritative `not_committed` would authorize the spine's
   fallback to generate a second time for one client request. Fixed by passing
   the accumulated turn in and forfeiting certainty on `result.sawContent`, which
   is the same rule the streaming path already applied. Regression:
   `TestNonStreamMidTurnBreakAfterContentForfeitsCommitCertainty` plus its
   control `TestNonStreamBreakBeforeAnyContentStaysAuthoritativeNoCommit` — the
   control exists because the fix could otherwise have over-corrected every
   failure into `unknown` and silently disabled fallback for the whole surface.

Fixes 1 and 2 were checked against the happy paths to confirm they do not
over-reject: content, moderation-block, and image turns still commit.

4. **A truncated stream was reported as a clean `stop`.** Found by the code
   review. Content arrived and then the body ended with neither a finish marker
   nor `[DONE]` — a connection drop mid-generation. The Adapter returned
   `committed` with `FinishClass: stop`, telling the caller the model chose to end
   there when the answer was actually cut off and the upstream may have kept
   generating and billed the rest. Fixed with `turnResult.truncated()`, which
   keys on the absence of `[DONE]` (`chat.go`).

   The distinguisher matters: `[DONE]` alone is sufficient evidence of a normal
   end, because an image turn legitimately terminates with `[DONE]` and no
   message-status marker (`image_generate.sse`). Requiring the marker would have
   misclassified every image generation as truncated. Regression:
   `TestTruncatedStreamIsNotReportedAsACleanStop` (both surfaces) with its
   control `TestDoneWithoutAFinishMarkerStillCompletes`.

