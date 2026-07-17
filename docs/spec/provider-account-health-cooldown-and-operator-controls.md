# Provider Account Health, Cooldown, and Operator Controls

- Status: Accepted for specification (issue #17)
- Date: 2026-07-17
- Parent: [#1](https://github.com/monet88/pixelplus/issues/1)
- Issue: [#17](https://github.com/monet88/pixelplus/issues/17)
- Vocabulary source: `CONTEXT.md`
- Related ownership invariants: `docs/spec/tenant-ownership-authorization-invariants.md` (#6)
- Related risk envelope: `docs/spec/auth-mode-risk-envelope-and-kill-criteria.md` (#7)
- Related Client API Key / admission: `docs/spec/client-api-key-lifecycle-and-admission-controls.md` (#8)
- Related Provider Account / credential lifecycle: `docs/spec/provider-account-connection-and-credential-lifecycle.md` (#9)
- Related Capability Snapshot / model availability: `docs/spec/capability-snapshot-and-model-availability-semantics.md` (#10)
- Related tenant-scoped routing / fallback / affinity / leases: `docs/spec/tenant-scoped-routing-fallback-affinity-leases.md` (#11)
- Related chat execution / streaming: `docs/spec/chat-execution-and-streaming-lifecycle.md` (#12)
- Related durable Render Job / output retry: `docs/spec/durable-render-job-and-output-retry-lifecycle.md` (#14)
- Related canonical errors / retry ownership: `docs/spec/canonical-errors-and-retry-ownership.md` (#16)
- Reference baselines (research/prior art only): `.ref/CLIProxyAPI@9f4f53ca`, `.ref/chatgpt2api@1f96b49`, `.ref/gemini-web-to-api@9792a98`, `.ref/grok2api@688702f`

## Problem Statement

A PixelPlus Tenant may connect several Provider Accounts with different Auth Modes, capabilities, quota buckets, credentials, and upstream failure behavior. The Gateway must decide whether each account is healthy enough for a specific operation and model without confusing runtime health with lifecycle, risk, capability, or administrative controls.

Without a canonical health and control contract, several harmful outcomes are possible:

1. One image-model rate limit can incorrectly disable chat for the whole account.
2. A process restart can erase cooldown and immediately hammer the Provider again.
3. Cooldown expiry can be mistaken for proof of recovery, releasing full traffic into an unresolved incident.
4. A late success can overwrite a newer challenge, credential rejection, ban, or protocol-drift observation.
5. Operator disable, maintenance drain, security quarantine, operational circuit, and Auth Mode kill can collapse into one ambiguous flag with unsafe in-flight behavior.
6. Automatic fallback can move work outside the Tenant's Routing Policy or repeat an operation whose Provider commit status is uncertain.
7. A health probe can accidentally create expensive or non-idempotent Provider work merely to test availability.
8. Retry guidance can expose a misleading timer when recovery actually requires reauthentication, operator action, or a risk-policy reopen.
9. Failure evidence from one Tenant can leak account existence or become sufficient for that Tenant to poison a shared Provider surface.
10. Raw Provider errors, credentials, challenge material, prompts, or Assets can leak through health diagnostics and operator tooling.

The user-facing problem is therefore not simply “is this account up?” It is: **for this Tenant, account, operation, model, credential version, and current control state, can PixelPlus safely start new Provider work, what must happen if it cannot, and how can the account recover without violating routing, retry, security, or accounting invariants?**

## Solution

PixelPlus will model Provider Account operational health as scoped, durable evidence rather than a single overloaded status token.

Each health observation is normalized into:

- a **Health State** describing current readiness;
- a **Health Reason** describing the canonical observed cause;
- an `account`, `operation`, or `model` scope;
- a credential version and monotonic condition revision;
- recovery timing and evidence where applicable.

The canonical Health States are:

- `unknown`
- `healthy`
- `degraded`
- `cooling_down`
- `challenged`
- `expired`
- `blocked`

Runtime health remains orthogonal to Provider Account lifecycle, Capability Snapshot, Auth Mode risk/execution gates, Account Drain, Account Quarantine, and Tenant Routing Policy. Effective routability is derived from all applicable gates; no one field is allowed to override the others.

Rate-limit and quota conditions create durable scoped cooldowns. A cooldown survives restart, uses the narrowest scope supported by evidence, and defaults to account-wide when the upstream bucket is unknown. Its timer expiry opens one bounded half-open recovery permit; it never marks an account healthy by itself.

Recovery uses Auth-Mode-defined probes that are single-flight, bounded, low-cost, and non-side-effecting. Probes do not create images, Render Jobs, user prompts, or Assets by default. Where no safe probe exists, a real request may serve as the one half-open permit only when the operation's idempotency and commit contract makes that safe.

Administrative and platform controls are explicit and separately authorized:

- Tenant disable pauses one Tenant-owned account through the lifecycle state.
- Account Drain stops new selections while bounded existing work follows its accepted execution and accounting contracts.
- Account Quarantine immediately isolates one account for security or integrity reasons.
- Provider Surface Circuit temporarily pauses a correlated Provider/Auth Mode/surface failure domain.
- Auth Mode kill remains the risk/policy authority defined by #7 and cannot be bypassed at account level.

The Gateway exposes safe health state, reason, scope, remediation, and finite retry timing to the owning Tenant without exposing raw Provider details or information about other Tenants. All transitions and controls are auditable. Numeric policies are named, bounded platform policy classes with testable defaults; Tenants cannot weaken safety minima.

## User Stories

1. As a Tenant administrator, I want to see whether my Provider Account is healthy, so that I know whether it can serve new work.
2. As a Tenant administrator, I want health reason separated from health state, so that I can distinguish readiness from the cause of degradation.
3. As a Tenant administrator, I want to see which operation or model is affected, so that one narrow failure does not look like a total account outage.
4. As a Tenant administrator, I want a finite retry time only when waiting can actually resolve the condition, so that my client does not blind-retry a reauthentication or operator incident.
5. As a Tenant administrator, I want stable remediation guidance, so that I know whether to wait, reauthenticate, enable the account, or contact an operator.
6. As a Tenant administrator, I want to disable one of my accounts immediately for new work, so that I can pause cost or investigate unexpected behavior.
7. As a Tenant administrator, I want enable to verify the current credential safely, so that stale or expired material is not silently returned to production.
8. As a Tenant administrator, I want disable to preserve the last truthful upstream health observation, so that administrative state is not mistaken for Provider failure.
9. As a Tenant administrator, I want repeated disable and enable requests to have deterministic outcomes, so that automation can reconcile safely.
10. As a Tenant administrator, I want a manual re-probe operation scoped to my own account and protected by `accounts.manage`, so that I can request recovery without gaining broader operator authority.
11. As a Tenant user, I want chat to continue when only image generation is cooling down, so that unrelated Provider capabilities remain available.
12. As a Tenant user, I want one model cooldown not to hide other verified models, so that the Gateway preserves usable capacity.
13. As a Tenant user, I want an account-wide unknown rate-limit bucket treated conservatively, so that my traffic does not worsen an upstream block.
14. As a Tenant user, I want routing to prefer healthy accounts over degraded peers at the same policy rung, so that reliability improves without changing my declared policy.
15. As a Tenant user, I want explicit account selection to fail clearly when the selected account is hard-blocked, so that the Gateway does not silently substitute another account.
16. As a Tenant user, I want fallback to occur only when my Routing Policy allows it, so that health automation never changes my account or Auth Mode choices.
17. As a Tenant user, I want fallback after an upstream attempt to obey commit certainty, so that health recovery does not duplicate chat or image operations.
18. As a Tenant user, I want a streamed chat already in progress to follow its cancellation and residual contract during disable or drain, so that the Gateway reports an honest terminal outcome.
19. As a Tenant user, I want a durable Render Job already committed upstream to retain its account and recovery semantics during drain, so that maintenance does not cause a duplicate render.
20. As a Tenant user, I want a cooling account excluded before a new upstream payload is sent, so that the Gateway does not waste my request on a known unavailable path.
21. As a Tenant user, I want cooldown to survive Gateway restart, so that restart cannot bypass Provider backoff.
22. As a Tenant user, I want cooldown expiry to admit only one recovery attempt, so that a thundering herd does not hit the Provider at the reset boundary.
23. As a Tenant user, I want a successful recovery to clear only the scope it proved, so that unrelated unresolved failures remain protected.
24. As a Tenant user, I want an auth failure during cooldown recovery to become `expired`, so that I receive reauthentication guidance instead of a longer meaningless timer.
25. As a Tenant user, I want a challenge during recovery to become `challenged`, so that PixelPlus does not imply that waiting or a challenge solver will fix it.
26. As a Tenant user, I want a permanent Provider ban to become `blocked`, so that PixelPlus stops attempting the account until an authorized recovery decision exists.
27. As a Tenant user, I want protocol drift to invalidate only affected capability/surface evidence where possible, so that unrelated verified paths are not disabled without evidence.
28. As a platform operator, I want health observations tied to credential version, so that a result from retired material cannot make a new credential usable.
29. As a platform operator, I want condition revisions to reject stale success updates, so that late completions do not erase newer hard failures.
30. As a platform operator, I want correlated upstream failures to open a scoped Provider Surface Circuit, so that the platform reduces hammering during an outage.
31. As a platform operator, I want one Tenant's failures to be insufficient to open a shared circuit without independent corroboration, so that a Tenant cannot poison the platform.
32. As a platform operator, I want circuit scope to include deployment, region, Provider, Auth Mode, and surface, so that an outage is contained to the smallest evidenced domain.
33. As a platform operator, I want a surface circuit distinct from an Auth Mode risk kill, so that transient operations recovery does not rewrite product/legal policy.
34. As a platform operator, I want half-open circuit traffic bounded, so that recovery does not immediately restore full concurrency.
35. As a platform operator, I want challenge and ban aggregates to feed the existing #7 kill criteria, so that operational evidence can trigger product-binding controls.
36. As a platform operator, I want an Auth Mode kill to stop new connections and executions immediately while its reconciler durably transitions affected account lifecycle without rewriting upstream health evidence, so that #9 lifecycle truth and #7 policy authority remain aligned.
37. As a platform operator, I want Account Drain to stop new work while allowing bounded in-flight settlement, so that planned maintenance is safer than immediate quarantine.
38. As a platform operator, I want Account Quarantine to isolate an account immediately during a security incident, so that suspected compromise cannot continue producing new decrypts or executions.
39. As a platform operator, I want quarantine release to require credential rotation or revocation when compromise is suspected, so that release cannot restore the suspected secret.
40. As a platform operator, I want manual cooldown clearing to open only one recovery permit, so that break-glass investigation does not disable platform safety.
41. As a platform operator, I want every manual override to carry authority, reason, expiry/review condition, and outcome, so that changes are accountable.
42. As a platform operator, I want time-bounded overrides rather than permanent hidden tuning, so that incident changes do not silently become product behavior.
43. As a platform operator, I want numeric policy classes with defaults and bounds, so that Provider-specific tuning remains testable and reviewable.
44. As a platform operator, I want Provider reset hints validated and honored conservatively, so that malformed dates do not create negative waits and valid long quota resets are not shortened unsafely.
45. As a platform operator, I want metrics aggregated by bounded Provider/Auth Mode/reason/scope classes, so that I can detect incidents without high-cardinality Tenant/account labels.
46. As a security reviewer, I want health and control responses free of credential, challenge, prompt, Asset, and raw Provider data, so that observability does not create a secret exfiltration path.
47. As a security reviewer, I want foreign and unknown account identifiers to remain indistinguishable, so that health endpoints do not become a cross-Tenant existence oracle.
48. As a security reviewer, I want recovery probes purpose-bound to `provider_probe`, so that probe authorization cannot be reused as general Provider execution authority.
49. As a security reviewer, I want killed, revoked, deleted, quarantined, or reauthentication-required accounts to remain non-probeable except through their authorized lifecycle recovery path, so that probes cannot bypass controls.
50. As a security reviewer, I want probe inputs to avoid Tenant prompts and Assets, so that health automation does not unnecessarily expose sensitive content upstream.
51. As a support operator, I want a safe correlation identifier and bounded diagnostic context, so that I can investigate without seeing raw Provider responses or secrets.
52. As a support operator, I want the owning Tenant to see its account state and affected scope, so that support can explain behavior without revealing other candidate accounts.
53. As an Adapter implementer, I want a required safe-probe contract for each Auth Mode, so that recovery behavior is consistent across Providers.
54. As an Adapter implementer, I want normalized failure evidence separated from routing decisions, so that Adapter internals cannot silently expand fallback authority.
55. As an Adapter implementer, I want unknown rate-limit scope to default account-wide, so that an incomplete Adapter classification fails safe.
56. As an Adapter implementer, I want a real request used as a half-open permit only when commit semantics authorize it, so that health checks do not create duplicate side effects.
57. As a Gateway implementer, I want one conformance seam for account health and controls, so that state transitions, eligibility, canonical errors, remediation, and audits can be tested as external behavior.
58. As a Gateway implementer, I want persisted cooldown restoration tested across restart, so that in-memory success cannot hide a durability defect.
59. As a Gateway implementer, I want concurrent observation races tested, so that state cannot regress under out-of-order completion.
60. As a Gateway implementer, I want every hard state and control mapped to deterministic routing behavior, so that candidate construction remains compatible with #11.
61. As a Gateway implementer, I want retry guidance derived after commit status and operation ownership, so that health signals cannot authorize a second retry owner.
62. As a product owner, I want experimental and gated Auth Modes to retain #7 controls during health recovery, so that operational success cannot promote product risk status.
63. As a product owner, I want Grok Web SSO to remain unprobeable and unconnectable while prohibited, so that health work does not reopen a prohibited mode.
64. As a product owner, I want a final specification that distinguishes tunable operational defaults from immutable security/routing invariants, so that later implementation tickets can change safe numbers without changing product meaning.

## Implementation Decisions

### 1. Scope, authority, and non-goals

1. This specification owns Provider Account Health State/Reason, scoped health conditions, cooldown, recovery probes, degraded/circuit behavior, Retry-After timing semantics, Account Drain, Account Quarantine, and their audit/recovery contracts.
2. This specification does not implement Gateway runtime code, database schema, transport paths, OpenAPI fields, UI, Adapter code, queues, workers, or vault cryptography.
3. #6 remains authoritative for Tenant ownership, non-enumeration, and Security Principal formation.
4. #7 remains authoritative for Auth Mode risk status, feature gates, kill triggers, and human reopen requirements. Health success cannot reopen or promote an Auth Mode.
5. #9 remains authoritative for Provider Account lifecycle and `I-USABLE-GATE`. Health and controls may make an otherwise active account non-routable; they cannot make a non-active account usable.
6. #10 remains authoritative for Capability Snapshot taxonomy, freshness, and offerability. A health probe updates capability evidence only when it satisfies #10 evidence requirements.
7. #11 remains authoritative for candidate construction, precedence, Tenant Routing Policy, fallback, affinity, and leases. Health only supplies the C5 routable-health input.
8. #12 and #14 remain authoritative for in-flight chat/stream and Render Job commit, cancellation, residual, recovery, and accounting behavior.
9. #16 remains authoritative for canonical error codes, retryability, remediation, commit certainty, and the single retry owner. Health timing never authorizes an unsafe retry.
10. `.ref/*` repositories are research/prior-art evidence only. Their persistence or selector behavior is not automatically PixelPlus product behavior.

### 2. Canonical health model

1. Operational health is represented by a Health State plus a Health Reason. Implementations MUST NOT store Provider rate limit, quota, auth expiry, challenge, ban, and protocol drift as mutually exclusive substitutes for readiness state.
2. The canonical Health States are:

| Health State | Meaning | Routability for matching scope |
|---|---|---|
| `unknown` | No current authoritative health evidence for the credential/scope | Fail closed for first activation and for any observed `active + unknown` invariant violation |
| `healthy` | Current evidence supports normal operation | Routable if every non-health gate passes |
| `degraded` | Transient/partial failure evidence is elevated but the circuit is not open | Routable with lower preference if capability remains offerable and policy permits |
| `cooling_down` | Rate/quota/backoff condition blocks new work for a bounded matching scope | Non-routable for the matching scope until half-open recovery succeeds |
| `challenged` | Provider challenge/bot-interstitial class blocks the account/surface | Hard non-routable; no productized challenge solver |
| `expired` | Current credential is expired, rejected, or no longer authorized | Hard non-routable; lifecycle moves toward `reauth_required` |
| `blocked` | Ban, hard protocol/integrity condition, or other non-timer operational block | Hard non-routable; operator/lifecycle recovery required |

3. The bounded initial Health Reason vocabulary is:

| Health Reason | Typical state | Meaning |
|---|---|---|
| `initial_unprobed` | `unknown` | No required/current probe evidence exists |
| `probe_succeeded` | `healthy` | Required/recovery probe succeeded for the recorded scope/version |
| `success_window` | `healthy` | Bounded current success evidence satisfies recovery policy |
| `elevated_error_rate` | `degraded` | Transient error ratio/consecutive threshold exceeded below circuit-open threshold |
| `upstream_unavailable` | `degraded` or `cooling_down` | Provider/surface unavailable without auth/challenge/ban evidence |
| `upstream_timeout` | `degraded` or `cooling_down` | Bounded upstream timeout evidence; retry safety still comes from #12/#14/#16 |
| `provider_rate_limited` | `cooling_down` | Provider rate-limit/backoff signal after admission |
| `provider_quota_exhausted` | `cooling_down` | Provider entitlement/quota reset signal after admission |
| `challenge_detected` | `challenged` | Bot/challenge/interstitial class was detected |
| `credential_expired` | `expired` | Credential reached known expiry or Provider reported expiry |
| `credential_rejected` | `expired` | Provider rejected current credential/authorization |
| `protocol_drift` | `degraded` or `blocked` | Provider protocol no longer matches Adapter contract; affected capability may become invalid |
| `provider_account_banned` | `blocked` | Permanent ban/provider revocation evidence for the account |
| `recovery_probe_failed` | prior state or escalated state | Probe failed without a more specific canonical classification |

4. Administrative states such as Tenant disable, drain, quarantine, and Auth Mode kill are not Health Reasons. They retain separate authority and observable fields.
5. Capability status is not a Health Reason. Protocol/quota observations may invalidate capability evidence through #10, but the Capability Snapshot remains independently authoritative.
6. The account management summary MAY show the most severe effective condition, but it MUST preserve all active affected scopes and MUST NOT imply that the summary state applies to unaffected operations/models.

### 3. Scoped Health Condition

1. A Provider Account MAY hold multiple active Scoped Health Conditions.
2. Each condition is bound to exactly one Tenant-owned account and one scope:
   - `account`;
   - `operation` with a canonical operation;
   - `model` with canonical operation + observed model slug.
3. Logical condition data includes:

| Logical field | Required | Rule |
|---|---|---|
| `tenant_id` | yes | Immutable owner; never client-selected authority |
| `provider_account_id` | yes | Same-Tenant durable account id |
| `scope_kind` | yes | `account`, `operation`, or `model` |
| `operation` | for operation/model | Canonical #10 operation |
| `model_slug` | for model | Same-Tenant observed model slug; never static cross-account inference |
| `health_state` | yes | One canonical state |
| `health_reason` | yes | One bounded reason |
| `credential_version` | yes | Observation applies only to this version unless an explicit safe inheritance rule exists |
| `condition_revision` | yes | Monotonic fencing/version for concurrent updates |
| `observed_at` | yes | Server time of normalized evidence |
| `source_class` | yes | Bounded class such as `required_probe`, `recovery_probe`, `upstream_attempt`, `provider_reset_hint`, `operator_classification`, or `aggregate_circuit` |
| `retry_not_before` | when waitable | Earliest half-open time, not proof of health |
| `provider_reset_at` | when validated | Provider reset hint retained separately from chosen policy time |
| `backoff_level` | for progressive cooldown | Monotonic bounded escalation level |
| `resolved_at` | when resolved | Audit-safe resolution time; resolved conditions do not authorize historical routing |

4. Model scope is valid only with an operation because the same model slug may have different semantics across operations.
5. Unknown/malformed scope for a Provider rate/quota signal MUST be normalized to account scope.
6. A condition never crosses Tenant or Provider Account boundaries.
7. Request-specific effective health is derived from all unresolved conditions matching the account plus the requested operation/model.
8. Condition precedence for availability is:

```text
blocked > expired > challenged > cooling_down > degraded > healthy > unknown
```

9. Precedence decides routability; it does not delete lower-severity evidence or replace lifecycle/risk/capability decisions.
10. A success resolves only the condition scope and revision it is authorized to verify.

### 4. Concurrent observation and stale-write rules

1. Every attempt/probe observation carries its start time, completion time, attempt/probe identity, credential version, and intended scope.
2. A success MUST NOT resolve a condition created after that attempt/probe began unless the recovery permit explicitly fenced that condition revision.
3. A success from an older credential version MUST NOT modify health for the current credential version.
4. Account-scope success MAY resolve narrower transient conditions only when the probe contract explicitly verifies those operation/model buckets. Generic identity success is insufficient.
5. Operation/model success MUST NOT resolve account-scope challenge, expiry, ban, or unknown-bucket cooldown.
6. Hard observations (`credential_rejected`, `challenge_detected`, `provider_account_banned`) dominate concurrent transient success until their authorized recovery path completes.
7. Concurrent condition creation/renewal/resolution uses compare-and-swap, transactional fencing, or equivalent monotonic revision semantics. Last-write-wins by completion time is forbidden.
8. A process crash between upstream observation and durable condition write MUST be recovered from an attempt/probe ledger or fail conservatively; it MUST NOT silently assume health.

### 5. Routability and degraded behavior

1. `healthy` is routable only when #9 `I-USABLE-GATE`, #7 execution gate, #10 offerability, #11 policy, vault authorization, and applicable controls all pass.
2. `degraded` remains routable before an attempt when:
   - no matching hard/cooldown condition exists;
   - requested capability/model remains offerable;
   - Provider Surface Circuit is closed for the matching surface;
   - Tenant Routing Policy permits the account.
3. Within the same #11 policy rung and otherwise equal deterministic policy facts, `healthy` is preferred over `degraded`.
4. Health preference MUST NOT reorder explicit selection, lease, affinity, or declared policy precedence beyond what #11 permits.
5. An explicit same-Tenant pin to a degraded but routable account remains pinned.
6. A degraded observation does not itself authorize fallback. Before-attempt fallback and post-attempt fallback remain #11 decisions; post-attempt fallback additionally requires #12/#14/#16 retry safety.
7. `cooling_down`, `challenged`, `expired`, and `blocked` remove the account from the candidate set for their matching scope.
8. A hard account-scope condition voids new lease steps and drops affinity as required by #11. In-flight work follows #12/#14.
9. `active + unknown` is an invariant defect and fails closed, preserving #9 §5.1.

### 6. Cooldown creation and scope

1. Provider runtime `provider_rate_limited` and `provider_quota_exhausted` signals create or renew a durable Scoped Health Condition with `health_state=cooling_down`.
2. The Gateway uses the narrowest scope proven by bounded upstream evidence:
   - model bucket → model scope;
   - operation/entitlement bucket → operation scope;
   - account/session/general bucket → account scope.
3. Missing, contradictory, or unknown bucket evidence defaults to account scope.
4. Credential rejection, challenge, and permanent ban do not create timer-only cooldowns. They transition to `expired`, `challenged`, or `blocked`.
5. Protocol drift creates a condition at the narrowest evidenced surface. If continued operation requires new private-protocol reverse engineering, #7 KS-5 applies; an account cooldown cannot hide that kill criterion.
6. Cooldown creation never changes Tenant Routing Policy and never creates cross-Tenant fallback authority.
7. A known pre-attempt cooldown may allow #11 policy fallback to another permitted same-Tenant account. A rate/quota response during an attempt additionally requires operation retry safety.
8. Cooldown cannot be disabled by a Tenant. Platform code MUST NOT provide an unrestricted `disable_cooling` production switch.
9. Privileged manual clear is a break-glass recovery action, not configuration: it records reason/authority, resolves no health evidence, and opens one half-open permit.

### 7. Cooldown timing and persistence

1. Cooldown records are durable and restored before the Gateway reports execution readiness after restart.
2. If cooldown durability cannot be read safely at startup, matching Provider execution fails closed; restart MUST NOT assume no cooldown.
3. Timing precedence is:
   1. validated Provider absolute reset time;
   2. validated Provider relative `Retry-After`;
   3. reason/Auth-Mode policy default with progressive backoff and deterministic jitter.
4. A Provider hint in the past, unparseable, or outside its plausibility bound is retained only as a malformed-hint observation and MUST NOT create a negative or immediate retry.
5. A valid Provider hint MUST NOT be shortened by a platform maximum. Platform policy MAY lengthen it for safety.
6. Rate-limit reset hints longer than `H-PROVIDER-RATE-HINT-MAX-PLAUSIBLE` and quota reset hints longer than `H-PROVIDER-QUOTA-HINT-MAX-PLAUSIBLE` require operator-visible classification instead of silent truncation.
7. Timer expiry transitions the condition from open to half-open eligibility. It does not set `healthy`, clear the condition, reactivate lifecycle, or refresh capability.
8. Exactly one recovery permit is granted per condition revision by default.
9. A successful authorized recovery resolves only the matching condition/revision.
10. A repeated matching rate/quota failure increments bounded `backoff_level` and renews the condition.
11. A more specific auth/challenge/ban/protocol outcome replaces timer recovery with its canonical state and remediation.
12. Jitter is deterministic for the account/scope/condition revision so distributed workers do not synchronize but retries remain testable.

### 8. Numeric health policy classes

The following defaults are product-chosen conservative operational values, not Provider guarantees. The allowed ranges are hard safety bounds: configuration outside them is invalid and MUST be rejected. Platform operators MAY tighten values within the range through audited, time-bounded overrides. Tenants cannot configure these classes.

| Policy ID | Default | Allowed range | Meaning |
|---|---:|---:|---|
| `H-DEGRADED-WINDOW` | 5 minutes | 1–30 minutes | Rolling transient observation window |
| `H-DEGRADED-MIN-ATTEMPTS` | 10 | 5–100 | Minimum attempts before ratio classification |
| `H-DEGRADED-ERROR-RATE` | 20% | 10–40% | Matching transient failure ratio entering `degraded` |
| `H-ACCOUNT-OPEN-CONSECUTIVE` | 5 | 3–10 | Consecutive matching transient failures opening account/scope cooldown when no Provider hint exists |
| `H-TRANSIENT-COOLDOWN-BASE` | 30 seconds | 5 seconds–5 minutes | Base cooldown for transient rate/unavailable class without hint |
| `H-TRANSIENT-COOLDOWN-MAX` | 15 minutes | 1–60 minutes | Progressive transient cooldown ceiling |
| `H-QUOTA-COOLDOWN-BASE` | 15 minutes | 5 minutes–6 hours | Base quota wait without a reset hint |
| `H-QUOTA-COOLDOWN-MAX` | 24 hours | 1 hour–31 days | Progressive quota recheck ceiling without a reset hint |
| `H-PROVIDER-RATE-HINT-MAX-PLAUSIBLE` | 24 hours | 1 hour–7 days | Longer rate hint becomes operator-visible malformed/exception evidence; it is not shortened into an earlier retry |
| `H-PROVIDER-QUOTA-HINT-MAX-PLAUSIBLE` | 31 days | 1–366 days | Longer quota hint requires operator classification; it is not silently truncated |
| `H-RECOVERY-PROBE-TIMEOUT` | 10 seconds | 2–30 seconds | Probe wall-clock timeout excluding queue wait |
| `H-RECOVERY-SUCCESS-STREAK` | 3 | 1–10 | Consecutive scoped successes required to move `degraded` to `healthy`; an explicit authoritative probe may satisfy the streak when its contract says so |
| `H-HALF-OPEN-CONCURRENCY` | 1 | fixed at 1 | Recovery permits per account cooldown condition revision; exactly one prevents reset-boundary hammering |
| `H-CIRCUIT-HALF-OPEN-CONCURRENCY` | 1 | 1–3 | Concurrent canary permits per Provider Surface Circuit revision; independent from account cooldown recovery |
| `H-SYNC-DRAIN-WINDOW` | 2 minutes | 30 seconds–15 minutes | Time before remaining synchronous/stream work becomes abort/residual handling |
| `H-DURABLE-DRAIN-WINDOW` | 15 minutes | 2–60 minutes | Time before remaining Render Job work transitions to existing cancellation/residual recovery; never proof of non-commit |
| `H-CIRCUIT-WINDOW` | 5 minutes | 1–30 minutes | Correlated Provider Surface Circuit observation window |
| `H-CIRCUIT-MIN-ATTEMPTS` | 20 | 10–200 | Minimum matching attempts before transient circuit ratio applies |
| `H-CIRCUIT-ERROR-RATE` | 50% | 30–80% | Matching transient/protocol failure ratio opening a surface circuit |
| `H-CIRCUIT-MIN-ACCOUNTS` | 3 | 2–20 | Distinct accounts required for shared circuit evidence |
| `H-CIRCUIT-MIN-TENANTS` | 2 | 2–10 | Distinct Tenants required unless an independent Provider/Adapter signal corroborates the account evidence |
| `H-CIRCUIT-OPEN-BASE` | 1 minute | 15 seconds–10 minutes | Initial surface circuit open period |
| `H-CIRCUIT-OPEN-MAX` | 15 minutes | 1–60 minutes | Progressive surface circuit ceiling before operator escalation |

Additional rules:

1. #7 challenge/ban thresholds (`FG-5`, `KS-2`, `KS-3`, and reopen constants) remain authoritative and are not replaced by this table.
2. Provider/Auth-Mode policy MAY define stricter values inside these bounds.
3. Changes to defaults or hard safety bounds require a versioned specification/product decision. Runtime configuration and incident overrides MUST remain inside the accepted range.
4. An operator override inside the accepted range carries actor, reason, prior/new value, scope, created time, expiry, and review outcome.
5. A safety mechanism MUST NOT silently interpret zero/negative values as “disabled”. Invalid or out-of-range values fail configuration validation; no break-glass path may bypass a hard bound.

### 9. Recovery Probe contract

1. A Recovery Probe is a purpose-bound `provider_probe` operation, not ordinary Provider execution.
2. It is authorized only after Tenant ownership, lifecycle, risk, vault, credential version, control, and audit-intent gates pass.
3. Probe execution is single-flight per account + condition scope/revision.
4. Probe queueing and attempts are bounded by a separate platform operational budget. They do not consume Tenant inference quota, but they are audited and rate-limited.
5. Probe inputs MUST NOT include Tenant prompts, Assets, masks, generated output, or arbitrary user content.
6. Probes default to non-mutating identity, session, entitlement, quota, model-catalog, or protocol-handshake operations.
7. Probes MUST NOT create image generations, image edits, inpaints, Render Jobs, persistent conversations, or billable artifacts by default.
8. An Adapter that has no safe non-mutating probe MUST declare that fact. A real request may become the half-open permit only if:
   - the request was independently admitted;
   - the operation's commit/idempotency contract permits the attempt;
   - exactly one recovery permit is atomically claimed;
   - no automatic second attempt is inferred from probe failure;
   - accounting remains attached to the originating Tenant and Client API Key.
9. A probe cannot mark lifecycle `active`; it supplies evidence to the #9 lifecycle transition.
10. A probe cannot reopen an Auth Mode killed by #7.
11. A probe updates Capability Snapshot only when it observes the required #10 evidence; identity success alone is insufficient.
12. Tenant-triggered re-probe requires `accounts.manage` and same-Tenant authorization.
13. System recovery probes may run without a Public API Client API Key only under the account's Tenant authority and purpose-bound service identity.
14. `disabled`, `revoked`, `deleted`, `reauth_required`, quarantined, or killed paths cannot use a generic health probe to bypass their authorized recovery flow.

### 10. Per-Auth-Mode safe probe matrix

| Auth Mode | Safe required/recovery probe class | Forbidden/default exclusions | Outcome notes |
|---|---|---|---|
| ChatGPT Web Access | Authenticated identity/session validation plus the lowest-cost entitlement/session touch that does not create content | No Sentinel/PoW/Turnstile/CF solver; no generation/conversation creation as default probe | Challenge → `challenged`; auth rejection → `expired`; remains lab-only under #7 |
| ChatGPT Codex OAuth | Refresh when due plus authenticated account/model/entitlement metadata on the bound Codex surface | No fallback to ChatGPT Web; no arbitrary prompt execution as default probe | Permanent refresh rejection → `expired` + `reauth_required` |
| Gemini Web Cookie | Session initialization that proves expected authenticated session fields rather than sign-in/challenge HTML | No generated image/chat request as default probe; no anti-bot bypass | Missing/invalid session → `expired` or `challenged` by classifier; lab-only |
| Gemini Antigravity OAuth | OAuth refresh when due plus non-mutating onboard/project/entitlement metadata such as the `loadCodeAssist`-class path | No fallback to Gemini Web Cookie; no generation as default probe | Project/entitlement evidence may refresh corresponding snapshot facts only when #10 permits |
| Grok Web SSO | None | All connection, probe, and execution paths forbidden while #7 status is `prohibited` | Research evidence cannot authorize a probe |
| Grok xAI OAuth | OAuth refresh when due plus non-mutating authenticated model/account/entitlement metadata on the bound surface family | No fallback to Grok Web SSO; no generation as default probe | Bound surface mismatch/protocol drift requires operator review |

### 11. Recovery outcomes

| Probe/half-open outcome | Health effect | Lifecycle/control effect | Routing effect |
|---|---|---|---|
| Scoped authoritative success | Resolve matching cooldown; `healthy` after required success policy | No direct activation; lifecycle transition remains #9 | Matching scope becomes eligible if all other gates pass |
| Partial success | Resolve only proven scope; keep remaining conditions | None | Only proven scope may re-enter candidate set |
| Provider rate limit | Renew matching/safer scope cooldown and increase bounded backoff | None | Matching scope remains excluded |
| Provider quota exhausted | Renew operation/model/account quota cooldown from validated reset evidence | None | Matching scope remains excluded; capability may become non-offerable under #10 |
| Credential expired/rejected | Set account `expired` | Move/keep lifecycle `reauth_required`; invalidate current usability | Account leaves candidate set |
| Challenge | Set account/surface `challenged` | May trigger quarantine or #7 aggregate controls | Account/surface leaves candidate set; no solver |
| Permanent ban | Set account `blocked` with `provider_account_banned` | Disable/quarantine + incident; #7 KS-3 evidence | Account leaves candidate set |
| Protocol drift, narrow | `degraded` or `blocked` at affected scope | Invalidate affected capability evidence | Affected operation/model non-offerable |
| Protocol drift requiring new reverse engineering | `blocked` evidence | Trigger #7 KS-5 decision/kill | Surface/Auth Mode execution stops |
| Timeout/unavailable before side effect | Renew transient condition according to policy | None | Retry/fallback only if #16 owner authorizes |
| Timeout/unknown after possible commit | Health observation recorded without claiming non-commit | Existing operation recovery only | No automatic fallback/retry based on health |
| Probe dependency/audit/vault failure | No optimistic health change | Fail closed; operator/dependency remediation | No execution |

### 12. Provider Surface Circuit

1. A Provider Surface Circuit is an operational gate scoped by deployment + region + Provider + Auth Mode + upstream surface, optionally narrowed by operation.
2. It is distinct from per-account health and from #7 Auth Mode kill.
3. It opens only from correlated bounded evidence satisfying the configured circuit policy and one of:
   - at least `H-CIRCUIT-MIN-ACCOUNTS` across `H-CIRCUIT-MIN-TENANTS`; or
   - at least the minimum account evidence plus an independent Provider/Adapter/egress/protocol signal.
4. A single Tenant's accounts alone cannot open a shared circuit without independent corroboration.
5. Challenge/ban evidence is evaluated against #7 FG/KS thresholds. This circuit does not weaken or delay a required Auth Mode kill.
6. Opening a circuit blocks new matching executions and new matching connection probes except designated recovery canaries.
7. It does not mutate every account's Health State. Account-level observations remain attached to their owners.
8. Existing in-flight work follows #12/#14 and is not deemed uncommitted merely because the circuit opened.
9. Circuit timer expiry opens bounded canary permits at `H-CIRCUIT-HALF-OPEN-CONCURRENCY`; this circuit-level fan-out never increases the fixed single permit of any matching account cooldown condition.
10. Canary success closes the circuit only after the configured recovery evidence; failure reopens with progressive bounded duration.
11. Repeated opening to `H-CIRCUIT-OPEN-MAX`, protocol drift, challenge storms, or ban clusters alerts an operator and may escalate to #7 controls.
12. Circuit routing never invents fallback; #11 Tenant policy remains authoritative.
13. Circuit status exposed to a Tenant says only that the requested surface is temporarily unavailable. It MUST NOT expose other Tenants, account counts, or identities.

### 13. Tenant disable and enable

1. Tenant disable uses #9 lifecycle `disabled`; it is not a Health State.
2. It requires `accounts.manage`, same-Tenant ownership, and an account state for which disable is valid under #9.
3. Disable is idempotent and blocks all new Provider execution/new lease steps immediately.
4. Affinity is dropped and leases are void for new work as required by #11.
5. In-flight work follows #12/#14; disable does not prove non-commit and does not automatically discard accounting.
6. Last upstream health evidence remains available as an observation but does not authorize routing while disabled.
7. PixelPlus adopts a stricter enable rule than #9's optional short-disable fast path: every `disabled → active` recovery goes through `pending_probe` and a current-version safe probe.
8. Enable preconditions include:
   - current credential present and not vault-revoked;
   - Auth Mode connectable/execution-enabled under #7;
   - no active quarantine;
   - current risk acknowledgement when required;
   - required probe success for the current credential version;
   - #10 capability evidence refreshed where required.
9. Probe auth failure moves to `reauth_required`; challenge or ban remains non-routable; success allows #9 activation only after all gates pass.
10. Enable is audited with requested, probe, and final outcomes. It never clears unrelated cooldown scopes automatically.

### 14. Account Drain

1. Account Drain is an administrative control, not a lifecycle or health state.
2. Tenant drain applies only to the Tenant's own account and requires `accounts.manage`; platform drain may target an account or Provider surface under privileged operator authority.
3. Active drain blocks new account selection and new lease steps immediately.
4. Work already accepted may continue during the applicable drain window:
   - synchronous/non-streaming/streaming work uses `H-SYNC-DRAIN-WINDOW`;
   - durable Render Job work uses `H-DURABLE-DRAIN-WINDOW`.
5. Completion before the window settles normally.
6. At window expiry, the Gateway attempts safe abort where supported. Work not proven stopped transitions to #12 residual tracking or #14 cancellation/recovery semantics.
7. Drain expiry is never proof of non-commit and never releases quota/accounting occupancy prematurely.
8. Drain reaches `drained` only when no new work can start and accepted work is terminal or durably transferred to its authorized residual/recovery owner.
9. Releasing drain does not mark health healthy. A Recovery Probe is required before new selection resumes.
10. Drain records include authority, reason, scope, start, deadline, state, release actor, and outcome.

### 15. Account Quarantine

1. Account Quarantine is a platform/operator hard control for security, credential-compromise, Provider-ban, protocol-corruption, or integrity incidents.
2. It blocks new execution, refresh, generic probe, new decrypt, and account selection immediately, except the explicit incident-remediation lifecycle purpose.
3. Tenant users can observe a safe quarantined/unavailable projection and remediation `contact_operator`; they cannot release quarantine.
4. Existing work is aborted when safe; otherwise it follows #12/#14 residual/unknown-commit/accounting rules.
5. Quarantine does not rewrite upstream Health State. The triggering health/security reason is recorded separately.
6. A suspected credential compromise requires vault revoke/rotation and a new credential version before recovery probe.
7. Provider ban requires operator incident review and MAY require delete/recreate rather than release.
8. Release requires privileged authority, recorded reason, satisfied remediation conditions, and a current-version Recovery Probe.
9. Release never directly sets lifecycle `active` or Health State `healthy`.
10. Quarantine has a required review time. Expiry of a review timer never auto-releases it.

### 16. Auth Mode kill interaction

1. Auth Mode kill remains the #7 Adapter registration/execution gate.
2. It blocks new connections and new executions immediately through the #7 execution gate. Its reconciler MUST then durably transition formerly `active` accounts to `disabled` for operational kills or `reauth_required` for credential-class invalidation/auth-class kills, exactly as required by #9 §4.2 rule 6; the reconciler preserves upstream health evidence rather than rewriting it as an administrative failure.
3. Per-account cooldown, drain, quarantine, and health cannot override a kill.
4. Recovery probes for ordinary accounts remain disabled until #7 R0–R3 authorizes controlled probe/reopen activity.
5. #7 R2 designated lab probes are explicit operator recovery actions and do not make all accounts usable.
6. After #7 reopen, each account still passes lifecycle, credential, current risk acknowledgement, health, capability, and control gates.
7. Accounts whose evidence became stale during a kill require current-version probes/snapshot refresh as applicable.
8. Policy/legal kills never reopen from elapsed cooldown or circuit success.

### 17. Retry-After and remediation semantics

1. #16 canonical codes remain unchanged:
   - pre-attempt health exclusion usually contributes to `account_not_usable`, `routing_no_candidate`, or `auth_mode_unavailable` according to the failing gate;
   - Provider runtime rate/quota outcomes remain `provider_rate_limited` / `provider_quota_exhausted`;
   - auth/challenge/ban/protocol outcomes remain their #16 codes.
2. Exact HTTP header and JSON encoding are #18/#20. This specification locks logical timing semantics.
3. `retry_after_seconds` is emitted only when every blocking matching condition is time-waitable and the earliest safe retry is finite.
4. It is computed at response time as the ceiling of seconds until the latest `retry_not_before` among matching waitable gates, with minimum `1`.
5. A non-time gate (`expired`, `challenged`, `blocked`, lifecycle disabled/revoked/deleted/reauth-required, quarantine, or Auth Mode kill) suppresses timer guidance and emits its canonical remediation.
6. `degraded` alone does not require Retry-After.
7. `retry_after_class` for both Provider runtime rate-limit and quota-exhaustion waits remains the #16 token `provider_cooldown`. Health Reason, affected scope, validated reset metadata, and authorized management/audit projections distinguish rate from quota without creating a second public retry class.
8. Raw Provider headers, timestamps, bodies, and bucket identifiers are never forwarded directly.
9. Retry-After never authorizes a client or transport to repeat an uncertain/committed non-idempotent operation. #16 retryability and owner remain authoritative.
10. When fallback safely succeeds inside the same operation, the client receives the operation outcome rather than the primary account's cooldown as a terminal error; the cooldown remains observable in account management/audit surfaces.

### 18. Safe management and operator projections

1. An owning Tenant MAY see:
   - `provider_account_id`;
   - lifecycle state;
   - effective Health State and Health Reason;
   - active affected scopes for its account;
   - finite retry time/class where applicable;
   - safe remediation;
   - credential version and safe timestamps already allowed by #9/#15;
   - whether a Tenant disable/drain is active;
   - a safe quarantine/Auth Mode unavailable indication without incident internals.
2. A Tenant MUST NOT see:
   - foreign account existence or counts;
   - shared circuit account/Tenant evidence;
   - raw Provider error/header/body;
   - Adapter stack/path internals;
   - credential, challenge token, cookie, OAuth material, prompt, Asset, or output content;
   - egress identity or information that helps bypass Provider controls.
3. Operator views may add bounded aggregate evidence and control metadata according to role, but ordinary views remain redacted.
4. A management summary that reports the most severe state MUST include affected scope information and MUST NOT flatten a model-only cooldown into a claim that all account capabilities failed.
5. Unknown/foreign account ids retain #6 non-enumerating not-found behavior.

### 19. Audit and telemetry

1. Health transition audit records include:
   - Tenant and same-Tenant account id;
   - Auth Mode;
   - old/new state and reason;
   - scope;
   - credential version;
   - source class;
   - condition revision;
   - retry timing class/time when safe;
   - request/correlation/attempt/probe id;
   - outcome.
2. Control audit records additionally include actor/authority, reason, scope, prior/new control state, deadline/review/expiry, remediation evidence reference, and release outcome.
3. Circuit events are platform-scoped and include bounded aggregate counts/classes without ordinary Tenant/account enumeration.
4. Audit/log/trace/metric surfaces never contain credentials, raw Provider payloads, raw challenge material, prompts, Assets, temporary URLs, or foreign-resource details.
5. Ordinary metrics aggregate by Provider, Auth Mode, operation, bounded model class where safe, Health State, Health Reason, scope kind, circuit/control class, and outcome.
6. `tenant_id` and `provider_account_id` MUST NOT be metric labels. Authorized audit records may contain them under #15 controls.
7. The system must count #7-required attempt, challenge, ban, and distinct-account evidence without exposing those identities to Tenants.
8. A telemetry write failure cannot make a blocked account routable. Required audit-before-control/decrypt actions fail closed according to #15.

### 20. Core invariants

1. **I-HEALTH-ORTHOGONAL** — Health, lifecycle, risk, capability, routing policy, vault authorization, and controls retain separate authority; no health success bypasses another gate.
2. **I-HEALTH-SCOPED** — Every condition belongs to one Tenant-owned account and the narrowest evidenced account/operation/model scope; unknown bucket defaults account-wide.
3. **I-HEALTH-CURRENT-VERSION** — Only current credential-version evidence may authorize current recovery.
4. **I-HEALTH-NO-STALE-CLEAR** — Stale, out-of-scope, or pre-condition success cannot clear newer failure.
5. **I-COOLDOWN-DURABLE** — Restart does not erase cooldown; unreadable cooldown state fails closed.
6. **I-COOLDOWN-HALF-OPEN** — Timer expiry opens bounded recovery, never automatic health.
7. **I-PROBE-SAFE** — Recovery probes are purpose-bound, bounded, single-flight, low-cost, non-content-bearing, and non-side-effecting by default.
8. **I-NO-COOLING-BYPASS** — Tenant configuration cannot disable cooling; manual clear is privileged, audited, and grants one permit only.
9. **I-ROUTING-POLICY-PRESERVED** — Health/circuit/control automation never invents fallback or crosses Tenant/Auth Mode policy.
10. **I-RETRY-OWNER-PRESERVED** — Health timing and probes never create a second retry owner or treat `unknown` commit as non-commit.
11. **I-DRAIN-HONEST** — Drain deadline does not prove stop/non-commit and does not prematurely release accounting.
12. **I-QUARANTINE-HARD** — Quarantine blocks new use until privileged, evidenced recovery; review timer expiry never auto-releases.
13. **I-KILL-DOMINATES** — Account health/control cannot bypass an Auth Mode kill or `prohibited` status.
14. **I-CIRCUIT-CORROBORATED** — Shared circuits require independent/cross-Tenant corroboration and do not mutate all account health.
15. **I-RETRY-AFTER-TRUTHFUL** — Retry-After exists only for finite waitable gates and never authorizes unsafe operation replay.
16. **I-HEALTH-REDACTED** — Health/control diagnostics expose no secrets, content, raw Provider internals, or foreign existence.
17. **I-ACCOUNT-ENABLE-PROBED** — PixelPlus enable always uses current-version safe probe before returning to `active`.
18. **I-SAME-TENANT-RECOVERY** — Every account probe/control action is authorized against the account's immutable Tenant; system jobs retain that Tenant authority.

## Testing Decisions

### Test philosophy

Tests will assert observable domain behavior rather than implementation details such as table layout, goroutine structure, concrete circuit-breaker library, queue topology, or Adapter private types.

The preferred highest seam is one **Provider Account control conformance seam**. A test supplies:

- Security Principal / system Tenant authority;
- Provider Account lifecycle, Auth Mode, credential version, risk and capability facts;
- current Scoped Health Conditions and active controls;
- requested operation/model and Routing Policy context;
- a normalized upstream/probe/control observation;

and observes:

- accepted/rejected transition;
- resulting durable conditions/control state;
- request-specific routability;
- recovery action/permit;
- canonical code, retryability, remediation, and retry timing semantics;
- audit-safe events.

Adapter-specific safe probes are exercised through contract fixtures behind this same logical seam. This keeps the specification testable without coupling tests to every internal module.

### Required conformance suites

1. **Health state/reason normalization**
   - each bounded reason maps to the intended state/scope;
   - admin controls never become health reasons;
   - malformed/unknown Provider signals fail conservatively.
2. **Scoped eligibility**
   - model cooldown excludes only matching model+operation;
   - operation cooldown preserves unrelated operations;
   - unknown bucket excludes account-wide;
   - summary projection retains affected scopes.
3. **Lifecycle/risk/capability orthogonality**
   - healthy cannot activate non-active account;
   - probe success cannot reopen killed/prohibited mode;
   - health does not make stale/unsupported capability offerable;
   - disabled/quarantined account remains non-routable despite healthy observation.
4. **Cooldown persistence**
   - restart restores open/half-open state before readiness;
   - unreadable durability fails closed;
   - timer expiry does not resolve condition;
   - deterministic jitter/backoff remains within bounds.
5. **Half-open concurrency**
   - concurrent account recovery requests produce exactly one permit for the condition revision;
   - circuit canaries produce at most their separately configured `H-CIRCUIT-HALF-OPEN-CONCURRENCY` permits without expanding any account cooldown permit;
   - losers receive safe recovery-pending behavior;
   - permit crash/recovery cannot open unlimited attempts.
6. **Recovery outcomes**
   - scoped success resolves exact revision;
   - late success cannot erase newer hard failure;
   - old credential-version success is ignored;
   - rate/quota renews cooldown;
   - auth/challenge/ban/protocol outcomes transition correctly.
7. **Probe safety contracts**
   - each enabled Auth Mode has a permitted fixture path;
   - probes carry no prompt/Asset/content;
   - prohibited Grok Web SSO has no probe path;
   - generic probes cannot run for revoked/deleted/quarantined/killed accounts;
   - capability changes occur only with sufficient evidence.
8. **Degraded routing**
   - healthy preference applies only within same policy rung;
   - explicit pin/lease precedence is preserved;
   - degraded alone does not authorize fallback;
   - post-attempt health does not bypass commit proof.
9. **Provider Surface Circuit**
   - one Tenant cannot open a shared circuit without independent evidence;
   - cross-Tenant corroboration opens only matching region/provider/mode/surface;
   - circuit does not mutate unrelated account health;
   - half-open canaries are bounded;
   - challenge/ban thresholds escalate to #7 rather than being hidden by transient circuit.
10. **Disable/enable**
    - disable blocks new work and is idempotent;
    - in-flight behavior delegates to #12/#14;
    - enable always enters probe path;
    - failed probe does not half-enable account;
    - enable does not clear unrelated scopes.
11. **Drain**
    - new work is blocked immediately;
    - work finishing within window settles normally;
    - deadline transitions unresolved work to residual/recovery without claiming non-commit;
    - release requires probe.
12. **Quarantine**
    - blocks new decrypt/refresh/probe/execution except incident remediation;
    - Tenant cannot release;
    - compromise requires new credential version;
    - review timer never auto-releases.
13. **Auth Mode kill**
    - account controls cannot bypass kill;
    - execution gate blocks immediately and the reconciler applies #9's required durable lifecycle transitions;
    - reconciler preserves upstream health evidence rather than rewriting it as the kill reason;
    - controlled R2 probe does not enable all accounts;
    - reopened mode still requires per-account gates.
14. **Retry-After**
    - finite matching waits use latest safe time and round up;
    - non-time gate suppresses timer;
    - raw Provider header is not forwarded;
    - Retry-After does not change #16 retryability after unknown commit.
15. **Tenant isolation/non-enumeration**
    - foreign/unknown account control/probe reads and writes are indistinguishable;
    - circuit diagnostics reveal no Tenant/account evidence;
    - system recovery retains immutable Tenant authority.
16. **Redaction/observability**
    - fixtures inject credentials, cookies, OAuth material, challenge tokens, prompts, Assets, raw headers/bodies, and confirm no ordinary output contains them;
    - metric label allowlist rejects Tenant/account ids;
    - audit retains safe ids and state transitions only.
17. **Policy bounds**
    - defaults load exactly;
    - out-of-range/zero/negative “disable” values fail validation;
    - stricter Provider/Auth-Mode overrides pass;
    - loosening requires audited time-bounded exception.
18. **Cross-spec journey tests**
    - active image-model cooldown with healthy chat;
    - rate limit before payload with permitted same-Tenant fallback;
    - rate limit after uncertain chat commit with no fallback;
    - drain during stream residual tracking;
    - quarantine during committed Render Job;
    - Auth Mode kill while accounts remain individually healthy;
    - protocol drift invalidating one capability and opening a surface circuit.

### Prior art

- #9 lifecycle transition and `I-USABLE-GATE` scenarios provide the account-state test vocabulary.
- #10 snapshot freshness/invalidation tests provide operation/model offerability cases.
- #11 candidate construction and fallback matrices provide the routing assertions.
- #12 proof-of-non-commit and residual tracking provide chat/stream in-flight assertions.
- #14 worker fencing, commit certainty, and output-only retry provide durable-job assertions.
- #15 redaction and purpose-bound decrypt tests provide probe/control security assertions.
- #16 canonical error and retry-owner matrices provide outcome assertions.
- `.ref/grok2api` persisted account/model cooldown and restart tests and `.ref/CLIProxyAPI` per-model cooldown/Retry-After structures are research prior art only; conformance is against this specification, not those implementations.

## Out of Scope

1. Implementing the Gateway, database migrations, Go modules, packages, ports, handlers, workers, or Adapter code.
2. Selecting concrete persistence technology or schema layout for conditions/controls.
3. Defining exact HTTP paths, JSON field names, headers, problem details, SSE events, or OpenAPI schemas; #18/#20 encode the logical contract.
4. Changing canonical error code meanings from #16.
5. Redefining Provider Account lifecycle states from #9, except choosing the permitted stricter “always probe on enable” policy.
6. Redefining Capability Snapshot taxonomy/model visibility from #10.
7. Redefining Tenant Routing Policy, fallback opt-in, explicit pin, affinity, or lease semantics from #11.
8. Redefining chat streaming, Render Job commit/cancellation, residual work, or accounting semantics from #12/#14.
9. Reopening, promoting, or demoting Auth Modes; #7 owns risk status and kill/reopen policy.
10. Productizing challenge solving, CAPTCHA/Turnstile bypass, anti-bot evasion, or new private-protocol reverse engineering.
11. Adding Official API Adapters or changing the six initial Auth Modes.
12. Defining operator RBAC implementation, incident-management system, dashboard layout, notification channels, or on-call process.
13. Defining commercial billing/refund policy beyond preserving existing accounting occupancy and settlement invariants.
14. Automatically copying or synchronizing `.ref/*` source into PixelPlus production code.
15. Allowing Tenants to disable cooldown/circuit safety or configure platform-wide thresholds.

## Further Notes

### Requirement alignment

Issue #17 acceptance criteria are satisfied as follows:

1. **Healthy, degraded, challenged, expired, cooling_down transitions and canonical causes** — §§2–5 and §11 define states, reasons, precedence, race safety, and recovery. `blocked` is added for ban/hard non-timer conditions that do not fit the required five states.
2. **Cooldown duration/recovery and safe probes** — §§6–10 define scoped durable cooldown, validated reset hints, bounded defaults, half-open recovery, and non-side-effecting per-Auth-Mode probes.
3. **Health affects routing without exceeding Tenant policy** — §§5, 12, 17 and invariants preserve #11 candidate/fallback/commit rules and same-Tenant isolation.
4. **Operator controls and Auth Mode kill safe scope/audit/recovery** — §§13–19 distinguish disable, drain, quarantine, surface circuit, and #7 kill with explicit authority, in-flight behavior, audit, and recovery conditions.

### Cause → effect examples

#### Example A — model-only image cooldown

1. Tenant A account supports chat and image generation.
2. An image request for `image-x` receives a validated model-bucket 429 before commit.
3. Gateway records `cooling_down/provider_rate_limited` at `(image_generation, image-x)`.
4. New `image-x` requests exclude that account or use only Tenant-policy-permitted fallback.
5. Chat remains routable on the account.
6. Reset time opens one model-scoped recovery permit; success clears only that condition.

#### Example B — unknown bucket fails account-wide

1. Provider returns 429 with no safe bucket metadata.
2. Adapter cannot prove operation/model scope.
3. Gateway records account-wide cooldown.
4. All new work on the account pauses, preventing hammering.
5. Other same-Tenant accounts may be considered only under the declared Routing Policy.

#### Example C — late success cannot clear ban

1. Attempt A begins at revision 10.
2. Attempt B receives permanent-ban evidence and creates revision 11 `blocked/provider_account_banned`.
3. Attempt A completes successfully afterward.
4. Its success is stale relative to revision 11 and cannot set the account healthy.
5. Account remains blocked/quarantined pending operator remediation.

#### Example D — drain during streaming

1. Operator drains account while a stream is open.
2. New selections stop immediately.
3. Stream may finish within `H-SYNC-DRAIN-WINDOW`.
4. If it outlives the window, Gateway attempts abort; unresolved upstream work becomes #12 residual tracking.
5. Occupancy/accounting remains until the accounting terminal.
6. Drain release requires Recovery Probe before new selection.

#### Example E — uncertain image attempt during cooldown signal

1. Render Job payload may have reached Provider.
2. Connection drops with a rate-limit-looking response but commit status is `unknown`.
3. Gateway records health evidence/cooldown as appropriate.
4. #16 forbids automatic retry/fallback because health code is not proof of non-commit.
5. Job follows #14 uncertain-attempt recovery; cooldown protects future work only.

#### Example F — one-Tenant circuit poisoning attempt

1. Tenant A generates many transient failures across three accounts.
2. No other Tenant and no independent Provider/Adapter signal corroborates them.
3. Account conditions update for Tenant A, but shared Provider Surface Circuit does not open.
4. Other Tenants remain unaffected and learn nothing about Tenant A.

#### Example G — Auth Mode kill dominates health

1. Several accounts are individually `healthy`.
2. #7 KS-6 kills their Auth Mode because its OAuth client is revoked.
3. New connections/executions stop without rewriting each account's health.
4. Account-level probe cannot bypass the kill.
5. After #7 R0–R3, each account still refreshes/reauthenticates/probes as needed before becoming usable.

### Deferred contract work

- #18 validates OpenAI-compatible inference behavior and how pre-attempt cooldown/runtime Provider failures appear across non-streaming, streaming, and image flows.
- #19 validates Provider Account/capability management projections and operator actions against this logical contract.
- #20 fixes versioning, exact wire encoding, Retry-After headers, idempotency, and contract-test compatibility.
- #21 chooses Pure-Go module seams, ports, dependency budget, persistence boundaries, and composition root.
- #22 verifies that this health/control contract remains coherent with every implementation-ready Gateway decision.
