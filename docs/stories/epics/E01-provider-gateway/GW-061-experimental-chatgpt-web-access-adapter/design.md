# Design

## Seams (agreed before tests)

Contract tests enter only through the public HTTP surface of production
composition (`Runtime.Handler()` served over `httptest`):

- `POST /v1/provider-accounts` and `POST /v1/provider-accounts/{id}/credential`
  (management: connection and disclosure)
- `POST /v1/provider-accounts/{id}/probe` (management: credential preparation)
- `GET /v1/models` and `GET /v1/provider-accounts/{id}/capability-snapshot`
  (capability baseline)
- `POST /v1/chat/completions`, streaming and non-streaming
- `POST /v1/renders` (render surface risk gate)

The Adapter's own protocol translation is proved by package tests in
`internal/adapters/chatgptweb` that feed sanitized fixture payloads through the
`Transport` seam. Those are protocol-unit tests of a leaf package, not
completion evidence for the acceptance criteria: every acceptance criterion is
proved through the public HTTP seam above.

No test calls a private function, a handler stub, `application.*` directly, a
concrete store query, or asserts goroutine layout.

## Domain Model

### `LabProfile`

A new value type in `internal/domain/labprofile.go`:

```go
type LabProfile struct { enabled map[AuthMode]struct{} }
func NewLabProfile(modes ...AuthMode) LabProfile
func (LabProfile) AllowsExperimental(AuthMode) bool
// BlocksExperimental is the gate-site form and is what the five profile-gated
// gate sites call (render.go deliberately rejects experimental unconditionally):
// mode.Experimental() && !AllowsExperimental(mode). Stating the refusal directly
// keeps a gate from having to remember that a NON-experimental mode must not be
// blocked by this control — negating AllowsExperimental alone would refuse every
// gated and allowed mode too.
func (LabProfile) BlocksExperimental(AuthMode) bool
```

The zero value allows nothing. That is the whole point: production composition
constructs `Dependencies` without naming any experimental mode, so the zero
value flows through and every experimental gate keeps failing closed exactly as
it does today. Enablement is only ever additive and explicit.

`NewLabProfile` silently ignores any mode that is not `RiskExperimental`. A
`prohibited` mode can therefore never be enabled through this door — Grok Web
SSO stays hard off no matter what an operator writes in config, which is what
§2 "hard off ... configuration that enables it in product deployments is a
policy defect" requires. A `gated` mode is likewise not a lab-profile concern;
gating is the feature-flag + acknowledgement path that already exists.

### Capability baseline clamp

`CanonicalCapabilityBaseline(AuthMode, CapabilityOperation) (CapabilityStatus, bool)`
encodes the accepted evidence ceiling. For both ChatGPT Auth Modes, evidence
§2.1 and §2.2 record every one of the five primary operations as
`conditionally supported` — none is `verified`. The baseline therefore caps all
five at `conditionally_supported`.

`NewLiveProbeSnapshot` applies the clamp to the operation facts and to every
model row before minting. An Adapter that reports `verified` for chat is
recorded as `conditionally_supported`; an Adapter that reports `unsupported`
keeps `unsupported`, because the clamp is a ceiling and never a floor.

Cause and effect: a lab operator connects a fresh ChatGPT Web account, the
Adapter probes successfully and (whether by drift or by an over-confident
implementation) claims `verified` chat. Without the clamp that snapshot would
publish a stronger capability promise than any accepted evidence supports, and
§7 rule 2 plus §2.2 forbid exactly that — "Gateway MUST NOT promote risk status
because a probe succeeded", and capability maturity is orthogonal to risk. The
clamp makes probe success unable to raise the ceiling; raising it requires
editing the evidence document, which is the "new authority" the acceptance
criterion asks for.

Modes with no recorded baseline are returned `ok == false` and left unclamped.
Only the two ChatGPT modes are encoded here because only their evidence
document is normative for this story; the Gemini and Grok evidence documents
are separate inputs and belong to their own Adapter stories.

## Application Flow

`LabProfile` is a plain value dependency (not a port — it carries no I/O and no
failure mode) on `ProviderAccountService`, `ChatService`, and `RenderService`.
Six existing gate sites change from an unconditional experimental rejection to
one that consults the profile:

| Site | Today | After |
|---|---|---|
| `provideraccount.go` create | rejects `Prohibited` only | also rejects experimental when the profile is off |
| `credential.go authModeGate` | rejects `Prohibited` | also rejects experimental when the profile is off |
| `capability.go accountAllowsOffers` | rejects `Prohibited` | also rejects experimental when the profile is off |
| `routing.go policyCandidateRejection` | rejects all experimental | rejects experimental not in the profile |
| `chat.go candidateRejection` | rejects all experimental | rejects experimental not in the profile |
| `render.go candidateRejection` | rejects all experimental | rejects experimental not in the profile |

The first three are a tightening, not a loosening. Today an `experimental`
account can be created, activated, and advertised on `/v1/models` in ordinary
production as long as the Tenant acknowledged risk, because only `Prohibited`
was checked at those three sites. §6.1 says an `experimental` mode "MUST NOT
appear in ordinary production Tenant self-serve catalogs", so those three sites
were a hole and this story closes it. The last three are the loosening the lab
profile authorizes.

Risk acknowledgement is untouched and still required: `RequiresRiskAck()`
already returns true for every experimental mode, and the profile check is
ANDed with it, never substituted for it. An operator who enables the lab profile
but whose Tenant has not acknowledged still gets `account_not_usable` with the
`ack_risk` remediation. That is the "disclosure before protected credential
use" ordering — the profile opens the door, the acknowledgement walks through
it, and neither alone reaches the Vault.

### `ValidateRoutingPolicyShape` is deliberately untouched

Routing policy shape validation rejects an experimental entry in
`fallback_auth_modes`, and it stays that way. A lab policy names a single
experimental account with `fallback_enabled: false`, which leaves
`fallback_auth_modes` empty and validates unchanged. Cross-mode fallback INTO
an experimental mode stays forbidden, which is FG-2 and §6.3 "no silent
cross-mode fallback" — a dead Codex OAuth account must never be silently
replaced by ChatGPT Web Access. Leaving this function alone also avoids
disturbing a symbol whose upstream blast radius covers the durable routing
ledger.

## Interface Contract

### Adapter package `internal/adapters/chatgptweb`

The Adapter implements four existing ports and introduces no new application
concept:

- `ports.ProbeAdapter` — credential preparation (`/backend-api/me`,
  `/backend-api/conversation/init`, `/backend-api/accounts/check`), returning
  `Authenticated` plus the normalized quota/rate signal.
- `ports.CapabilityAdapter` — observes the operations and model slugs the
  session reports.
- `ports.ChatAdapter` — non-streaming chat, aggregated from the SSE stream.
- `ports.ChatStreamAdapter` — streaming chat, emitting canonical deltas.

It owns none of: Tenant selection, account choice, durable state, replay,
admission, or full-operation retry. It holds no mutable field across calls. The
one retry-shaped thing it may do is nothing at all — a failed exchange is
classified and returned, and the spine decides whether another account is tried.

### `Transport` seam

```go
type Transport interface {
    Exchange(ctx context.Context, request Request) (Response, error)
}
type Request  struct { Method, Path, Body string; Headers map[string]string }
type Response struct { Status int; Body string; Stream Stream }
type Stream   interface { Next() (string, bool, error); Close() error }
```

`Stream.Next` yields one SSE `data:` payload at a time so streaming translation
is incremental rather than buffered. Fixtures implement `Transport` over
sanitized payload files; no HTTP client ships in this story (non-goal).

A nil Transport makes every Adapter method return the port's unavailable error.
That is the production state: the lab profile can be on and the Adapter
registered, and it still cannot reach a Provider without an operator
deliberately supplying transport.

### Credential handling

Every method that needs a credential receives `ports.CredentialInjection` and
builds its request headers inside the `Use` callback. The secret never lands in
a struct field, a log, an error, or the returned outcome. `ProbeAdapter` is the
exception in shape only — it receives `ProbeCommand` and is invoked by the spine
after the Vault validated the stored version, so it proves auth against the
session the Vault owns rather than handling material itself.

### Adapter registry

`internal/adapters` gains four thin multiplexers
(`ChatAdapterRegistry`, `ChatStreamAdapterRegistry`, `ProbeAdapterRegistry`,
`CapabilityAdapterRegistry`). Each holds a fallback plus a
`map[domain.AuthMode]<port>` and dispatches on the Auth Mode already present on
every command struct. A mode with no entry falls through to the fallback, which
in production is the fail-closed foundation.

This is what "register the mode" means literally. Composition builds a registry
only when the lab profile names an experimental mode that has an Adapter; with
the profile off it builds nothing, so the Adapter is not merely bypassed at
runtime — it is absent from the composed object graph.

## Protocol Translation

Translated from `.ref/chatgpt2api/docs/upstream-sse-conversation.md` and
`.ref/chatgpt2api/services/openai_backend_api.py`. The reference is research
evidence, not permission (§3.1 rule 4); what is reused is the observable wire
shape, not the reference's policy choices.

| Upstream payload | Canonical translation |
|---|---|
| `"v1"` | protocol marker, ignored |
| `{"p":"/message/content/parts/0","o":"append","v":"..."}` | one `ChatDelta` |
| bare `{"v":"..."}` string | one `ChatDelta` (path-elided append) |
| `{"o":"patch","v":[...]}` | each array element applied in order |
| `/message/status` → `finished_successfully` | finish class `stop` |
| `{"type":"moderation","moderation_response":{"blocked":true}}` | finish class `content_filter` |
| `{"type":"message_marker"}`, `title_generation`, `server_ste_metadata` | non-content, no delta |
| `[DONE]` | end of stream |
| unparseable payload | protocol drift, not a delta |

Image results follow the reference's three-part rule and nothing looser: a
pointer counts as output only when the message role is `tool`, the metadata
`async_task_type` is `image_gen`, and the pointer scheme is `file-service://`
or `sediment://`. A `sediment://` pointer on a `user` message is an input
attachment and is never returned as output.

### Challenges are classified, never solved

The reference calls `solve_turnstile_token`. This Adapter does not, and must
not. When `/backend-api/sentinel/chat-requirements` reports an Arkose,
proof-of-work, or Turnstile requirement, the Adapter classifies the exchange as
challenged and returns; it never computes a token, never retries the challenge,
and never calls a solver. OP-G6 refuses challenge solving as a product
capability outright, and KS-5 makes any new anti-bot reverse engineering a kill
trigger rather than a feature. A challenged classification is the honest
observable that feeds the FG-5/KS-2 counters.

### Quota and rate

`conversation/init.limits_progress[feature_name=image_gen]` yields
`remaining` and `reset_after`. `remaining == 0` maps to
`ProbeSignalQuotaExhausted` with the relative reset as `RetryAfterSeconds`;
HTTP 429 maps to `ProbeSignalRateLimited`. Both keep `Authenticated: true` —
the credential proved itself and the account activates with a scoped cooldown,
which is the existing probe contract.

### Auth failure

HTTP 401 on any backend path is an auth-class failure: `Authenticated: false`,
returned as an outcome and never as an error, so the account moves to
`reauth_required` rather than surfacing a dependency 503. Web Access has no
silent refresh (`SupportsRefresh()` is already false for it), so there is no
refresh attempt to make.

## Data Model

No new tables, ledgers, or durable records. The Adapter is stateless and the
lab profile is composition configuration, not persisted state. Capability
snapshots continue to be written by the existing capability path; the only
change is that the values written are clamped to the evidence baseline.

## UI / Platform Impact

None in this story. `experimental` modes are forbidden from ordinary production
connection UX (§6.1) and the lab console is not built here.

## Observability

The existing audit, telemetry, and request-log projections cover the new paths
unchanged, because the Adapter sits behind the same protected boundaries as
every other Adapter. Two obligations are load-bearing:

- OP-G3: the Adapter never places credential material, raw cookies, session
  tokens, or challenge tokens in a log, an error string, or a metric label. Its
  errors carry a canonical class and a path, never a body.
- §3.5.3: challenge classifications and auth failures are distinguishable
  outcomes so the FG-5/KS-2 counters can be built on them later. The counters
  themselves are #17.

## Alternatives Considered

1. **A build tag for the lab profile.** Rejected: a build tag makes the lab
   binary a different artifact from the production binary, so the negative
   proof ("production composition does not register the mode") could only be
   tested by building twice. A runtime profile lets one test assert both
   directions against the same composition root, which is what the acceptance
   criteria ask for.

2. **Registering the Adapter unconditionally and gating only at execution.**
   Rejected: AC2 says ordinary production composition must not *register* the
   mode. Gating at execution alone leaves the Adapter wired into the graph and
   makes every future gate a new opportunity to forget one.

3. **Clamping capability in the Adapter rather than in the domain.** Rejected:
   the clamp is a product rule about accepted evidence, not a protocol detail.
   Putting it in the domain means a future Adapter — or a fixture Adapter that
   lies — cannot bypass it, and only one place has to be re-read when the
   evidence document changes.

4. **Threading `LabProfile` as a port.** Rejected: it has no I/O and no failure
   mode, so a port would add an interface and a fail-closed substitute to
   express a value. A zero-value-safe struct fails closed by construction.
