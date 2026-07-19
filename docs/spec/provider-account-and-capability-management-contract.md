# Provider Account and Capability Management Contract (Prototype)

- Status: Accepted for prototype evidence (issue #19)
- Date: 2026-07-18
- Parent: [#1](https://github.com/monet88/pixelplus/issues/1)
- Issue: [#19](https://github.com/monet88/pixelplus/issues/19)
- Base commit: `8447494`
- Vocabulary source: `CONTEXT.md`
- Related ownership / authorization: `docs/spec/tenant-ownership-authorization-invariants.md` (#6)
- Related Auth Mode risk: `docs/spec/auth-mode-risk-envelope-and-kill-criteria.md` (#7)
- Related Client API Key / admission: `docs/spec/client-api-key-lifecycle-and-admission-controls.md` (#8)
- Related Provider Account / credential lifecycle: `docs/spec/provider-account-connection-and-credential-lifecycle.md` (#9)
- Related Capability Snapshot: `docs/spec/capability-snapshot-and-model-availability-semantics.md` (#10)
- Related routing / fallback: `docs/spec/tenant-scoped-routing-fallback-affinity-leases.md` (#11)
- Related Credential Vault: `docs/spec/credential-vault-and-sensitive-data-lifecycle.md` (#15)
- Related canonical errors: `docs/spec/canonical-errors-and-retry-ownership.md` (#16)
- Related Provider Account Health: `docs/spec/provider-account-health-cooldown-and-operator-controls.md` (#17)
- OpenAPI artifact: `contracts/openapi/pixelplus-management-api-v0alpha.yaml`
- One-command prototype: `node scripts/prototype-management-contract.mjs`

## 1. Scope and non-goals

### 1.1 Scope

This document retains the **non-final management contract prototype** for Provider Account connection, credential intake/reauthentication, probing, lifecycle controls, Capability Snapshot reads, and Tenant Routing Policy management.

The prototype chooses concrete wire representation and executable cause→effect behavior for:

1. Provider Account create/list/get/delete
2. Direct Provider Credential submission
3. OAuth authorization start/poll without exposing exchanged tokens
4. Probe, disable, enable, and reauthentication journeys
5. Per-account Capability Snapshot inspection
6. Tenant Routing Policy read/replace
7. Client API Key scope, non-enumeration, redaction, and side-effect boundaries

It is **contract and throwaway logic evidence**, not Gateway runtime. It does not implement an HTTP server, database, Credential Vault encryption, Adapter, OAuth token exchanger, worker, or persistence.

### 1.2 Decision separation

| Class | Content | Authority |
|---|---|---|
| Locked inherited semantics | Tenant ownership, Client API Key authority, Auth Mode risk gates, Provider Account lifecycle/usability, Credential Vault rights, Capability Snapshot, routing/fallback, canonical errors, health/operator controls | #6–#17 specs |
| #19 representation decisions | Management paths, scope metadata, request/response field names, direct-secret request boundaries, OAuth journey representation, stale-snapshot read behavior, examples, validator, deterministic state scenarios | this document + artifacts |
| Resolved after prototype | Unified inference + management package and stable versioning/idempotency policy | #20 stable policy/artifact |
| Still deferred | Production runtime architecture and HTTP conformance harness | #21 and runtime tickets |

Issue #17 invariant `I-ACCOUNT-ENABLE-PROBED` takes precedence over the older optional short-disable path in #9: **every enable enters `pending_probe` and runs a current-credential-version safe probe**.

### 1.3 Non-goals

This prototype does not:

- Change the issue #18 inference tracer or its validator.
- Publish stable management path or schema compatibility by itself.
- Let a client supply `tenant_id`.
- Return Provider Credential material, OAuth exchange tokens, raw Provider responses, or vault envelopes.
- Implement cross-Tenant administration, shared credentials, silent routing fallback, or static Provider/model capability catalogs.
- Treat stored credential material, healthy status, or successful enable request as sufficient proof that an account is usable.

Issue #20 has consolidated this management tracer with inference in `contracts/openapi/pixelplus-public-api-v1.yaml`; stable policy is `docs/spec/api-versioning-compatibility-idempotency-contract-testing-policy.md`.

---

## 2. Server, security, and operation surface

### 2.1 Shared server and Client API Key

- OpenAPI version: `3.1.1`
- JSON Schema dialect: Draft 2020-12
- Server base: `/v1`
- Security scheme: HTTP bearer `ClientApiKey`
- Bearer format: `sk-pxp_<public_locator>_<secret>`
- Prototype marker: `x-pixelplus-artifact-status: prototype`
- Prototype version: `0.0.0-prototype`

The server derives `tenant_id` and `client_api_key_id` from the authenticated key. No operation accepts client-supplied `tenant_id`.

### 2.2 Scope metadata

| Surface | Required scope |
|---|---|
| Provider Account reads | `accounts.read` |
| Provider Account mutation, credential, OAuth, probe, controls | `accounts.manage` |
| Capability Snapshot read | either `accounts.read` or `capabilities.read` |
| Routing Policy read | `routing.read` |
| Routing Policy replace | `routing.manage` |

Authorization precedence is observable and stable:

1. Malformed, unknown, or revoked Client API Key → indistinguishable `401 authentication_failed`.
2. For resource-addressed operations, an unknown, foreign, or deleted Tenant-scoped identifier → indistinguishable `404 resource_not_found` before scope evaluation.
3. An authenticated same-Tenant principal without the required scope → `403 forbidden`.
4. Ownership rejection happens before vault decrypt or Adapter call.

### 2.3 Operations relative to `/v1`

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/provider-accounts` | Create a `draft` account shell |
| `GET` | `/provider-accounts` | List ordinary non-deleted owner-Tenant accounts |
| `GET` | `/provider-accounts/{provider_account_id}` | Read safe lifecycle/health/control metadata |
| `DELETE` | `/provider-accounts/{provider_account_id}` | Stop use/decrypt and remove ordinary visibility |
| `POST` | `/provider-accounts/{provider_account_id}/credentials` | Direct credential intake boundary |
| `POST` | `/provider-accounts/{provider_account_id}/oauth-authorizations` | Start server-owned OAuth journey |
| `GET` | `/provider-accounts/{provider_account_id}/oauth-authorizations/{authorization_id}` | Poll safe OAuth status |
| `POST` | `/provider-accounts/{provider_account_id}/probe` | Run an authorized safe account probe |
| `POST` | `/provider-accounts/{provider_account_id}/reauthentication` | Direct credential replacement boundary |
| `POST` | `/provider-accounts/{provider_account_id}/disable` | Disable new use without claiming health failure |
| `POST` | `/provider-accounts/{provider_account_id}/enable` | Enter current-version probe path |
| `GET` | `/provider-accounts/{provider_account_id}/capability-snapshot` | Inspect per-account capability evidence |
| `GET` | `/routing-policy` | Read Tenant singleton Routing Policy |
| `PUT` | `/routing-policy` | Atomically replace Tenant Routing Policy |

---

## 3. Provider Credential and secret boundary

### 3.1 Approved direct-secret requests

Only these request schemas may contain direct Provider Credential material:

1. `DirectCredentialSubmissionRequest` on `/credentials`
2. `DirectReauthenticationRequest` on `/reauthentication`

`CredentialSubmission.credential_class` and `CredentialSubmission.material` are required; material is `writeOnly`, at least eight characters, and rejects control characters. OpenAPI examples intentionally omit direct material, including redacted placeholders, so reusable examples cannot normalize secret-shaped values into logs or generated docs.

OAuth access/refresh tokens, device codes, authorization codes, PKCE verifiers, cookies, ciphertext, passwords, and similar material are not response fields. OAuth token exchange is server-side; polling returns only safe journey state and remediation.

### 3.2 Purpose-bound effects

A valid probe may use the Credential Vault purpose `provider_probe` after authentication, scope, ownership, lifecycle, version, and administrative gates pass.

These operations have **no vault decrypt purpose**:

- account list/get
- delete command itself
- disable
- Capability Snapshot read
- Routing Policy read/write
- OAuth status read

Foreign or unknown resource rejection produces zero vault decrypt and zero Adapter call. Safe responses, errors, examples, scenario traces, health, snapshots, and routing state are scanned for secret-bearing keys.

### 3.3 Delete and retention

Delete makes the account unavailable to ordinary list/get and stops future credential use/decrypt. Every stored current or pending credential version is revoked before deletion. Retention Hold is internal system policy, not a client-supplied DELETE field; it may keep encrypted evidence, but that evidence remains non-decryptable and non-restorable for product use.

---

## 4. Provider Account lifecycle and journeys

### 4.1 Lifecycle vocabulary

The contract exposes exactly:

```text
draft
pending_validation
pending_probe
active
reauth_required
disabled
revoked
deleted
```

Lifecycle, Health, and Administrative Controls are separate projections. One dimension cannot silently overwrite or bypass another.

### 4.2 Create and direct intake

Cause→effect:

```text
create
  -> draft shell
  -> no credential version
  -> no vault write/decrypt
  -> no Adapter call

valid direct credential submission
  -> vault write for next credential version
  -> observable 202 pending_validation
  -> still not active
  -> separate server-owned validation completion
       -> success: pending_probe
       -> failure: staged version revoked/discarded, draft + submit_credential remediation
  -> no Adapter call or provider_probe before validation success
  -> no activation until the later explicit probe succeeds
```

Request-shape, credential-class, and control-character failures occur before vault write. Post-store validation is a separate server-owned step rather than a transition hidden inside the intake response: success advances to `pending_probe`, while failure revokes/discards the staged version, keeps the account non-usable, and exposes only safe remediation. Direct `/credentials` intake is valid only for a pure `draft`; later lifecycle states must use reauthentication and cannot bypass disable/enable. Storing material alone never activates the account.

### 4.3 OAuth journey

OAuth start is account-scoped and carries `purpose=connect|reauthenticate`. `connect` requires a pure `draft`; `reauthenticate` stages a replacement while preserving the prior lifecycle target. In particular, starting or completing OAuth reauthentication for a disabled account keeps public lifecycle `disabled`; the authorization state remains observable through the server-owned OAuth resource instead of hiding administrative intent behind `pending_validation`. The account remains non-decryptable and returns to `disabled` after a successful replacement probe. The server owns `authorization_id` and returns safe fields such as flow, status, verification URI/user code where applicable, expiry, and remediation.

A successful server-side exchange stores a current version for connect or a pending version for reauthentication, then opens the appropriate current/pending-version probe path without overriding disabled intent. Disable intent is sticky across the OAuth authorization/exchange window: even a successful connect exchange plus probe remains `disabled` and non-decryptable until explicit enable clears that intent. A failed authorization is terminal for that `authorization_id`, stores no credential version, and returns only `failed` plus safe `complete_oauth` remediation. A failed connect restores `draft` and clears the journey-only disabled marker because no credential exists to protect; a failed reauthentication restores `reauth_required` or preserves `disabled` according to its origin. Only one replacement journey may be in flight per account: another OAuth start, direct replacement, or enable request is rejected until the pending authorization/validation/probe marker reaches a terminal state. No token is returned by start or poll, and status polling has no vault decrypt purpose.

### 4.4 Probe outcomes

A probe uses the cheapest same-Tenant Auth-Mode-defined path that can prove the required account/auth/capability facts without creating user work. The same public probe operation checks the current version during activation/enable or the pending version during reauthentication. Administrative controls reject before vault decrypt or Adapter call; only pending-version success performs atomic cutover.

| Probe result | Effect |
|---|---|
| success and all independent gates pass | current credential version may become `active`; health becomes healthy; fresh snapshot can be built |
| auth-class failure | pending replacement is revoked/discarded without promotion; an active-origin journey becomes `reauth_required`, while disabled administrative intent remains `disabled`; snapshot invalidated |
| transient upstream failure | remains non-usable (for example `pending_probe` + degraded); does not falsely claim credential expiry |
| administrative/risk gate failure | probe/execution fails closed; health success cannot bypass the gate |

### 4.5 Disable and enable

- A pure `draft` shell cannot be disabled because no usable credentialed account exists yet.
- Disable is idempotent for an already-disabled account.
- Disable changes lifecycle usability but does not invent an upstream health failure.
- Every enable follows:

```text
disabled
  -> pending_probe
  -> current credential version provider_probe
  -> active | reauth_required | other non-usable state
```

The `202` enable response remains `pending_probe`; it never predicts probe success. Enable is rejected while an OAuth authorization, credential validation, replacement version, or one-shot probe marker is still in flight, preventing an administrative enable from racing or overwriting the journey that preserved `disabled` publicly.

### 4.6 Reauthentication and cutover

Reauthentication preserves the logical `provider_account_id`, Tenant ownership, Provider, and immutable Auth Mode. New direct material is assigned a monotonically increasing credential version, stored as pending, and returned as an observable `pending_validation` active-origin state; a disabled-origin journey keeps public lifecycle `disabled` while the same validation marker remains internal. A rejected/discarded pending version is never reused. While staging, the public credential projection remains on the old current version. The account becomes non-decryptable for new admissions; the prior version may serve only already-authorized in-flight work until cutover.

Server-owned validation success opens the pending-version `provider_probe` path without promoting the pending version. While validation remains pending, any probe rejects before vault decrypt, Adapter, or Provider probe call, including when public lifecycle stays `disabled`. Validation failure revokes/discards the pending version before any Adapter or Provider probe call, leaves the old current version public, and records safe `reauthenticate` remediation in `reauth_required` or preserves `disabled`. Pending-version `provider_probe` success atomically promotes the pending version and revokes the old version. Probe auth failure revokes/discards the pending version without promotion; quarantine or Auth Mode kill rejects before decrypt/Adapter. Disable intent wins over the replacement journey whether disable happened before staging, during validation, or while the replacement was pending probe: success, auth failure, or transient retry cannot reactivate the account. A successful disabled replacement remains `disabled` and non-decryptable until the ordinary enable plus current-version probe path succeeds. Direct and OAuth replacement share a single-flight gate, and an open current-version one-shot probe marker also blocks replacement, so a second journey cannot overwrite or orphan pending work.

---

## 5. Health and administrative controls

Health State is one of:

```text
unknown | healthy | degraded | cooling_down | challenged | expired | blocked
```

Conditions retain account/operation/model scope, reason, credential version, observation time, and remediation. Raw Provider responses are never part of the public projection.

`retry_after_seconds` is present only when the effective gate is both finite and waitable. For example:

- operation-scoped `cooling_down` with a known 30-second gate may expose `retry_after_seconds: 30`;
- `challenged`, credential expiry, quarantine, or another operator/non-time gate omits retry timing.

Lifecycle state, health state, drain/quarantine, and Auth Mode execution enablement remain independent. `draining` blocks new execution admission and routing selection but does not by itself forbid an authorized probe. Health gates apply only at their declared scope: for example, a `chat` operation cooldown blocks chat admission/selection but does not block `image_generation`. A healthy probe cannot clear quarantine, enable a killed Auth Mode, clear drain, or activate a disabled account by itself.

---

## 6. Capability Snapshot semantics

A Capability Snapshot is per-account and requires:

- `provider_account_id`
- current `credential_version`
- `verified_at`
- freshness: `fresh|stale|invalid`
- provenance/evidence
- all five operation facts
- observed model slugs

Required operations:

```text
chat
chat_streaming
image_generation
image_edit
inpaint
```

Capability status is `verified|conditionally_supported|unsupported|unverified`. Only `verified` and `conditionally_supported` may be offerable, and only while the snapshot is `fresh`. Reference-learned evidence by itself remains `unverified` and non-offerable; only account-bound upstream/live probe evidence may elevate an operation to `verified` or `conditionally_supported`.

Important read-versus-authorize distinction:

- If an existing snapshot is stale or invalid, management `GET .../capability-snapshot` returns `200` with its explicit freshness and non-offerable facts so an operator can inspect why it is blocked.
- If no snapshot exists yet, the read returns `409 capability_unverified`.
- Routing/execution authorization rejects stale/invalid evidence with `snapshot_stale` and rejects unsupported/unverified facts before vault decrypt or Adapter call.
- `inpaint` is first-class; unsupported inpaint never degrades to `image_edit`.

Model rows contain only slugs observed for that account/auth surface. Provider name or Auth Mode is not a static model catalog.

---

## 7. Routing Policy semantics

Routing Policy is a Tenant singleton with:

- `candidate_accounts`
- deterministic `selection_order`
- `fallback_enabled` (default `false`)
- explicit `fallback_chain`
- explicit cross-Auth-Mode `fallback_auth_modes`
- affinity policy
- lease policy
- safe update metadata

Rules:

1. Candidate accounts must all belong to the authenticated Tenant.
2. A foreign/unknown candidate rejects the whole `PUT` as `resource_not_found`.
3. Rejection is atomic: zero policy writes, zero vault decrypt, zero Adapter calls, no partial candidate persistence.
4. Fallback is opt-in and fail-closed; an absent policy or `fallback_enabled=false` means no fallback.
5. Explicit account pins never silently fall back.
6. Fallback and explicit-pin selection cannot bypass lifecycle, decryptability, drain/quarantine/Auth Mode controls, scoped health, capability freshness/offerability, or commit-safety gates.

---

## 8. Canonical errors and retry truthfulness

The tracer uses the #16 canonical envelope and includes reusable examples for:

- `authentication_failed`
- `forbidden`
- `resource_not_found`
- `invalid_request`
- `account_not_usable`
- `auth_mode_unavailable`
- `capability_unverified`
- `snapshot_stale`

Foreign/unknown `resource_not_found` omits a resource reference. Error examples and responses must not contain credential material, raw Provider payloads, foreign identifiers, or client-supplied Tenant authority.

`retry_after_seconds` is not a generic optimism signal. It appears only for a finite waitable gate and never bypasses ownership, lifecycle, vault, capability, risk, health, or commit-safety decisions.

---

## 9. Executable cause→effect evidence

`scripts/run-management-contract-scenarios.mjs` is a deterministic, in-memory, throwaway state runner. Every action prints:

1. authenticated principal projection
2. redacted request
3. full relevant state before
4. safe wire response
5. prototype-only transition trace when applicable
6. full relevant state after
7. side-effect delta
8. assertion result

The scenarios cover authentication/scope/ownership precedence, same-Tenant list isolation, valid Auth Mode gates, credential-class and malformed-material rejection, observable post-store validation success/failure, probe-before-validation rejection, direct and OAuth intake, OAuth connect/reauthentication failed-remediation and terminal recovery, single-flight replacement/enable/current-probe-marker race rejection, monotonically increasing current/pending-version probe outcomes, disable/enable, direct and OAuth reauthentication cutover, quarantine and drain zero-side-effect rejection, sticky public disabled intent across direct validation/probe staging and OAuth connect/reauth authorization-exchange windows, operation-scoped health admission, five-operation snapshot behavior, missing/stale/invalid read-versus-authorization behavior, reference-only evidence remaining unverified, unsupported inpaint, successful and atomic-rejected routing writes, explicit-pin no-fallback, finite/non-time health gates, deletion of current plus pending credentials, and internal retention evidence.

Side effects are explicit counters (`vaultWrites`, `vaultDecrypts`, `vaultRevokes`, `vaultDeletes`, `adapterCalls`, `providerProbeCalls`, `oauthExchanges`, `policyWrites`). Therefore a scenario can prove not only its response but also that a forbidden operation did not reach a sensitive boundary.

---

## 10. Artifacts and validation

| Artifact | Role |
|---|---|
| `docs/spec/provider-account-and-capability-management-contract.md` | Retained prototype decision record |
| `contracts/openapi/pixelplus-management-api-v0alpha.yaml` | Separate OpenAPI 3.1.1 management tracer |
| `scripts/validate-management-openapi-contract.mjs` | Representation/schema/redaction/invariant validator |
| `scripts/run-management-contract-scenarios.mjs` | Deterministic in-memory cause→effect runner |
| `scripts/prototype-management-contract.mjs` | One-command validator + scenario entrypoint |

Run from repository root:

```bash
node scripts/prototype-management-contract.mjs
```

The command first validates the OpenAPI artifact, then runs the state scenarios. The validator:

1. parses JSON-compatible YAML without a YAML dependency;
2. checks OpenAPI version, top-level-only prototype marker, server, security, reusable identifier parameters, scopes, and operation coverage;
3. rejects client-supplied `tenant_id`;
4. resolves internal `$ref`s and rejects external/cyclic references;
5. restricts secret-bearing fields to approved write-only request schemas;
6. scans examples for secret-bearing keys/patterns;
7. validates schema examples with Python `jsonschema` Draft 2020-12 already available in the environment;
8. checks lifecycle, enable-probe, snapshot, routing, no-decrypt, canonical error, and invariant descriptions.

The repository adds no package dependency. This remains neither a full external OpenAPI metaschema validation nor a runtime HTTP/E2E implementation test.

---

## 11. Finality statement

This remains a retained management prototype, not the stable client artifact.

- The artifact is separately named `pixelplus-management-api-v0alpha.yaml`.
- `info.version` remains `0.0.0-prototype`.
- Paths and field names remain accepted prototype evidence.
- Issue #18 inference artifacts remain unchanged as retained evidence.
- Issue #20 reconciled shared components/errors and published `pixelplus-public-api-v1.yaml`; changes to stable `/v1` now follow the #20 policy.
