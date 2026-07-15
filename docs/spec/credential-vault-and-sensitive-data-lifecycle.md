# Credential Vault and Sensitive-Data Lifecycle

- Status: Accepted for specification (issue #15)
- Date: 2026-07-15
- Parent: [#1](https://github.com/monet88/pixelplus/issues/1)
- Issue: [#15](https://github.com/monet88/pixelplus/issues/15)
- Vocabulary source: `CONTEXT.md`
- Related ownership invariants: `docs/spec/tenant-ownership-authorization-invariants.md` (#6)
- Related risk envelope: `docs/spec/auth-mode-risk-envelope-and-kill-criteria.md` (#7)
- Related Client API Key / admission: `docs/spec/client-api-key-lifecycle-and-admission-controls.md` (#8)
- Related connection / credential lifecycle: `docs/spec/provider-account-connection-and-credential-lifecycle.md` (#9)
- Related Capability Snapshot / model availability: `docs/spec/capability-snapshot-and-model-availability-semantics.md` (#10)
- Related tenant-scoped routing / fallback / affinity / lease: `docs/spec/tenant-scoped-routing-fallback-affinity-leases.md` (#11)
- Related chat execution / streaming: `docs/spec/chat-execution-and-streaming-lifecycle.md` (#12)
- Related Asset exchange / authorization / retention: `docs/spec/asset-exchange-authorization-and-retention-lifecycle.md` (#13)
- Related durable Render Job / output retry: `docs/spec/durable-render-job-and-output-retry-lifecycle.md` (#14)

## 1. Scope and non-goals

### 1.1 Scope

This specification locks the **logical confidentiality boundary and lifecycle policy** for Provider Credential, Client API Key material, prompt and request data, Asset bytes, Render Job staging data, and the metadata/audit records that accompany them.

It codifies parent #1 user stories 5, 7, 8, 18, 27, 31, 44–46, 55, and 58–60. It consumes the Tenant ownership and non-enumeration invariants (#6), Client API Key hashing and admission rules (#8), Provider Account and Credential lifecycle (#9), Capability Snapshot redaction (#10), chat execution (#12), Asset retention (#13), and durable Render Job/output-retry lifecycle (#14).

This is **specification work**. It does not implement the Gateway, KMS/HSM integration, database, object store, queue, worker, Adapter, or Public API transport.

It covers:

1. A data-classification matrix for secret, Tenant-confidential, safe metadata, and redacted operational data.
2. Independent storage and encryption boundaries that do not depend on a particular storage implementation.
3. A logical envelope-encryption contract with Tenant/resource binding, key versions, and separation between business credential rotation and cryptographic key rotation.
4. The authorization boundary for encrypt, decrypt, rewrap, revoke, delete, and purge operations.
5. Redaction rules for Public API responses, logs, metrics, traces, audit, support views, fixtures, and contract artifacts.
6. Retention, logical deletion, cryptographic deletion, physical reclamation, and the precedence of retention holds.
7. Audit semantics that prove sensitive-data access without storing the secret or content being accessed.
8. Fail-closed behavior when the vault, key service, audit path, retention state, or binding metadata is unavailable or inconsistent.
9. Observable conformance and security-test obligations for the downstream Gateway and contract work.

### 1.2 Non-goals

This document does **not**:

- Implement a vault, KMS, HSM, database, object store, encryption library, or zeroization routine.
- Select a cloud KMS vendor, mandate a provider-specific API, or freeze a particular deployment topology.
- Redefine Tenant ownership, non-enumeration, Client API Key format/hash, Provider Account state transitions, Capability Snapshot freshness, chat/job state machines, Asset validation, or Asset retention classes owned by #6–#14.
- Freeze exact JSON schema, OpenAPI paths, HTTP headers, or canonical error-code strings (#16/#18/#20).
- Freeze numeric cryptoperiods, purge windows, audit retention, or cache budgets. This document names the classes and fail-closed semantics; #17 or the implementation plan tunes values without weakening the invariants.
- Make a Provider Credential or Asset shareable across Tenants, even if an external identity or content checksum is equal.
- Define a legal/compliance jurisdiction's retention schedule. It defines the product safety boundary that any such schedule must preserve.

Downstream work **MUST** preserve every decision here. It may choose equivalent storage and key-service implementations, but it MUST NOT:

- Store Provider Credential plaintext or Client API Key plaintext after the one-time response boundary.
- Let a credential handle, resource id, worker identity, or Tenant-supplied field authorize decrypt by itself.
- Reuse a ciphertext, DEK, prompt record, Asset, staging object, or audit event across Tenants.
- Treat a cryptographic key rotation as a Provider Credential rotation or as proof that a new credential was probed.
- Serve content after its owning lifecycle has expired or been logically deleted merely because physical reclamation has not completed.
- Use a retention hold to keep a deleted credential decryptable or to restore Public API retrieval.
- Put secrets, raw prompt/image content, temporary Provider URLs, or ciphertext envelope blobs into telemetry or contract artifacts.
- Fail open when a key, audit, retention, or Tenant-binding decision cannot be established.

### 1.3 Normative language

- **MUST / MUST NOT / REQUIRED**: product and security policy. Violation is a defect.
- **SHALL**: same force as MUST for observable behavior.
- **SHOULD**: strongly preferred default; deviation needs an operator-recorded exception.
- **MAY**: optional behavior that cannot weaken a MUST rule.

### 1.4 Relationship to prior issues

| Topic | Already locked | This document adds |
|---|---|---|
| Tenant boundary | Every listed resource has one immutable `tenant_id`; foreign ids are non-enumerating (#6) | Cryptographic binding and access authorization carry the same Tenant boundary |
| Client API Key | One-time plaintext display, HMAC-SHA-256 verifier with platform pepper, bounded revocation (#8) | Pepper custody/rotation is separate from vault encryption; key material and verifier retention are explicit |
| Provider Credential | Account metadata is separate from vaulted secret; validation/probe/refresh/reauth/revoke/delete moments (#9) | Envelope format, purpose-bound decrypt, cryptographic rotation, compromise response, and purge semantics |
| Capability Snapshot | Per-account, per-version, redacted and non-authorizing on durable account failure (#10) | Snapshot is classified as Tenant-confidential metadata and never a credential side channel |
| Chat execution | Decrypt occurs after account selection and selected-account gate at X3 (#12) | Purpose token, audit-before-decrypt, plaintext lifetime, and prompt/replay retention |
| Asset lifecycle | Tenant ownership, retention classes, expiry, deletion, tombstone and storage release (#13) | At-rest encryption, logical-delete/read-gate ordering, and physical/cryptographic reclamation |
| Render Job | Immutable result manifest, staging, output-only retry, same-Tenant worker/credential use (#14) | Staging/temporary-handle classification and retention boundary for durable recovery |

### 1.5 Decision unit

**Every sensitive-data operation is a Tenant-scoped, purpose-scoped, resource-bound decision.** The operation either produces a durable audit record and a bounded result, or fails closed before plaintext or protected content is released.

Cause → effect:

1. A Tenant A job requests a Provider Credential for account A and purpose `provider_execution`.
2. The vault authorization includes Tenant A, account A, the current credential version, the job/request identity, and the execution purpose.
3. A ciphertext or handle from Tenant B, another account, another job, or another purpose fails the binding/authorization checks before plaintext is returned.
4. No Adapter call is made with the wrong material, and the failure does not confirm the foreign resource's existence to the Public API caller.

---

## 2. Data classification and ownership

### 2.1 Classification levels

| Level | Meaning | Required treatment |
|---|---|---|
| **Secret** | A bearer or authentication material that can act at an external or platform boundary. | Vault-only or non-reversible verifier; no ordinary metadata projection; no telemetry; decrypt only for an allowlisted purpose. |
| **Tenant-confidential** | Content or metadata whose disclosure would reveal a Tenant's prompt, image, request, execution, account, or result. | Tenant-scoped authorization and authenticated encryption at rest; no cross-Tenant lookup; retention class required. |
| **Safe metadata** | Non-secret metadata needed for state, routing, accounting, or support, still owned by one Tenant. | Tenant-scoped reads and writes; storage encryption according to platform policy; redacted projections only. |
| **Operational-redacted** | Deliberately reduced data suitable for telemetry or contract diagnostics. | Stable local identifiers and safe classes only; no content, secret, ciphertext, foreign existence, or bearer handle. |

Classification is about the data itself, not only its storage column. A raw Provider URL may be a bearer capability even when its field name says `url`; a request fingerprint may reveal prompt/model relationships even though it is a digest; an Asset checksum may be safe for same-Tenant integrity but is still Tenant-confidential correlation data.

### 2.2 Data-classification matrix

| Data class | Examples | Level | Durable boundary | Retention owner |
|---|---|---|---|---|
| **Provider Credential material** | Web token, cookie jar, SSO token, OAuth access/refresh token, id token, class-specific bundle | Secret | Credential Vault only; ciphertext plus envelope metadata | #9 lifecycle + `RETAIN-CREDENTIAL-*` in §8 |
| **Client API Key material** | Full `sk-pxp_<public_locator>_<secret>` string and secret segment | Secret | Request memory and one-time create/rotate response only | No plaintext retention; #8 metadata remains separately |
| **Client API Key verifier** | HMAC-SHA-256 result, `hash_version`, public locator | Safe metadata with secret-derived value | Key metadata store; verifier is non-reversible and never returned as material | #8 key metadata policy + `RETAIN-KEY-METADATA` |
| **Server pepper** | Platform secret used by `H-KEY-HMAC` | Secret | Platform secret custody, separate from Tenant vault records | Platform key-management policy |
| **Prompt and generated text** | Chat prompt, image prompt, assistant output, tool/result text, conversation content | Tenant-confidential | Ephemeral execution memory by default; encrypted Tenant Confidential Store only when durable replay/recovery requires it | `RETAIN-PROMPT-EXECUTION` / `RETAIN-IDEMPOTENCY-REPLAY` |
| **Request and execution metadata** | Operation, model, account id, Asset references, request fingerprint, usage, timestamps, lease/job state | Safe metadata or Tenant-confidential when it can reconstruct content/relationships | Tenant-scoped metadata store; content-bearing fields encrypted | `RETAIN-REQUEST-METADATA` / resource lifecycle |
| **Asset bytes** | `input`, `mask`, and `output` image bytes | Tenant-confidential | Tenant-scoped encrypted Asset Store | #13 `RETAIN-INPUT`, `RETAIN-OUTPUT`, `RETAIN-EPHEMERAL` |
| **Render staging and result handles** | Staging bytes, temporary Provider URL, Provider retrieval token/handle, result-manifest retrieval reference | Tenant-confidential; bearer handle portions are secret-like | Encrypted Tenant staging boundary; never ordinary job/log projection | #14 `RENDER-STAGING-RETENTION-CLASS` |
| **Account/capability/job metadata** | Lifecycle state, health, capability status, model slug, progress, safe error class, output-entry state | Safe metadata, Tenant-owned | Tenant-scoped metadata store and projections | Resource-specific lifecycle; #9/#10/#14 |
| **Idempotency records** | Scoped key, request fingerprint, terminal status, safe replay reference | Tenant-confidential metadata | `(tenant_id, scope, key)` partition; encrypted content-bearing replay payload if retained | #16/#20 idempotency TTL |
| **Audit records** | Actor, Tenant, resource id, purpose, decision, version, outcome, timestamp | Safe metadata with Tenant-confidential relationships | Append-only Audit Store; encrypted at rest; never Provider Credential plaintext | `RETAIN-AUDIT` |
| **Logs, metrics, traces, contract fixtures** | Request id, safe class, duration, counters, synthetic placeholders | Operational-redacted | Telemetry/contract boundary | Operational retention; no sensitive-data recovery path |

### 2.3 Ownership and attachment rules

1. Every durable row or object in the matrix has one immutable `tenant_id`, except platform-global key metadata that never contains Tenant-owned plaintext or content.
2. Provider Credential attachment is `(tenant_id, provider_account_id, auth_mode, credential_version)`; the credential cannot be retargeted.
3. Prompt/request payloads retained for a Render Job inherit the job Tenant and cannot be replayed by another Tenant or Client API Key.
4. Asset bytes and staging objects inherit the owning Asset or Render Job Tenant. Output placement uses the #14 placement key and #13 storage reservation; encryption does not create a second ownership path.
5. Audit events are scoped to the Tenant that owns the accessed resource. A platform audit index MAY support incident search by safe event fields, but it MUST NOT expose foreign resource existence through ordinary Tenant APIs.
6. A storage implementation MUST enforce the Tenant/resource binding in its authorization and cryptographic context, not only in a caller-supplied query filter.

---

## 3. Storage and confidentiality boundaries

### 3.1 Logical boundaries

The implementation MUST expose logical boundaries even if one physical database, object store, or KMS is used underneath:

| Boundary | May contain | MUST NOT contain |
|---|---|---|
| **Credential Vault** | Encrypted Provider Credential envelopes and version/revocation metadata | Plaintext credential in account rows, API responses, logs, or general-purpose cache |
| **Tenant Confidential Store** | Encrypted prompt, response, request payload, replay payload, and content-bearing recovery data | Cross-Tenant records, unbounded transcript history by default, raw telemetry copies |
| **Asset Store** | Encrypted immutable image objects and #13 lifecycle metadata | Provider Credential, Client API Key material, foreign Tenant references |
| **Render Staging Store** | Encrypted result bytes and temporary retrieval references needed by #14 | Permanent raw Provider response copies, unbounded temporary URLs, unscoped handles |
| **Metadata Store** | Tenant-scoped account, job, snapshot, idempotency, quota, and safe lifecycle metadata | Plaintext secret, prompt/image bytes, raw upstream exceptions containing secret material |
| **Audit Store** | Append-only secret-free access and lifecycle events | Credential material, ciphertext blobs, prompt/image content, raw Authorization headers |
| **Telemetry / Contract boundary** | Redacted identifiers, safe classes, timings, counters, and synthetic fixture placeholders | Secret, content, bearer handle, envelope blob, foreign existence, raw upstream payload |

A physical co-location of these boundaries is conforming only when the logical access controls, encryption contexts, retention policies, and audit semantics remain distinct. “Same database” is not permission to use one broad repository method for every data class.

### 3.2 Transport and process boundaries

1. Provider Credential and Client API Key material enter only through TLS-protected, purpose-specific intake/authentication boundaries. Query-string and ordinary logging transports are forbidden (#8/#9).
2. Plaintext Provider Credential may exist only in the authorized vault/Adapter operation that needs it. It MUST NOT be placed in a general-purpose request context, durable job row, shared cache, trace span, panic/exception value, or worker message.
3. Prompt, Asset, and staging plaintext may exist in the smallest process boundary needed for execution, validation, retrieval, or placement. Implementations SHOULD zeroize or release buffers promptly; they MUST NOT retain a reusable global cache without an explicit Tenant and retention policy.
4. A worker message carries opaque references and Tenant/resource identity, not Provider Credential plaintext, Client API Key material, prompt bytes, or Asset bytes unless the relevant operation explicitly requires an encrypted payload and its own retention class.
5. Public API projections expose only safe metadata and Tenant-authorized content. They never expose a vault handle as if it were a credential, and they never expose a temporary Provider handle as a downloadable public URL without a separately authorized delivery contract.

### 3.3 Storage-independent vault contract

The logical vault interface MUST support these operations without prescribing the backing technology:

- `put` — encrypt and persist a new version under an immutable Tenant/resource binding.
- `authorize_decrypt` — evaluate ownership, purpose, resource state, version, scope, and audit preconditions.
- `decrypt` — release plaintext only after an authorization decision; return no plaintext on mismatch.
- `rewrap` — change cryptographic key wrapping without changing business identity or credential version; plaintext MUST remain inside the vault boundary when the implementation supports provider-side rewrap.
- `revoke` — make a credential version non-decryptable for new work.
- `logical_delete` — make a data object non-retrievable/non-usable immediately while physical reclamation may be asynchronous.
- `cryptographic_purge` — destroy the object key or equivalent material so ciphertext is no longer recoverable through the product.
- `audit` — emit an append-only, secret-free record for access and lifecycle decisions.

A handle or storage locator is an address, not an authorization. Every operation receives the Tenant and resource binding from trusted server state, not from a caller-provided string alone.

---

## 4. Envelope encryption and binding

### 4.1 Logical key hierarchy

The implementation MAY use an equivalent hierarchy, but it MUST preserve this blast-radius model:

1. A versioned platform **Key-Encryption Key** (`platform_KEK`) is held by approved KMS/HSM custody and is not exported to ordinary application storage.
2. Each Tenant has a logically separate versioned **Tenant Wrapping Key** (`tenant_TWK`) protected by the platform key hierarchy. A platform root may protect many Tenant keys, but a Tenant plaintext wrapping key or object DEK MUST NOT be shared across Tenants.
3. Each encrypted object or credential version uses a fresh random **object DEK**. The object DEK encrypts the payload with an approved authenticated-encryption construction and is wrapped by the current Tenant wrapping key.
4. Envelope, payload, and lifecycle metadata carry explicit `crypto_key_version` and `envelope_schema_version` values.

The names are logical. The implementation may use KMS envelope APIs, per-Tenant KMS keys, HSM operations, or an equivalent design. It MUST NOT replace authenticated encryption with reversible encoding, unauthenticated encryption, or a single application-wide plaintext DEK.

### 4.2 Logical envelope fields

An encrypted object has, at minimum:

| Field | Purpose |
|---|---|
| `data_class` | Prevents a credential envelope from being accepted as an Asset/prompt envelope |
| `envelope_schema_version` | Allows safe parser and migration changes |
| `crypto_suite` | Identifies the approved authenticated-encryption construction |
| `crypto_key_version` | Identifies the Tenant/platform wrapping-key generation |
| `resource_binding` | Tenant/resource kind/id and, where applicable, account/job/Asset identity |
| `business_version` | `credential_version` for Provider Credential; absent for unrelated data |
| `nonce` / authentication tag | Authenticated-encryption inputs |
| `wrapped_object_dek` | Object DEK protected by the wrapping-key hierarchy |
| `ciphertext` | Encrypted payload |

Envelope metadata MAY be indexed for lifecycle work only when it cannot reveal secret content or foreign existence. The complete envelope blob, wrapped DEK, nonce, and ciphertext MUST NOT be placed in logs, traces, metrics, error strings, audit payloads, or contract examples.

### 4.3 Associated-data binding

Authenticated data MUST bind the ciphertext to the trusted logical context. The binding includes, as applicable:

- `tenant_id`
- data class
- resource kind and stable resource id
- `provider_account_id` for Provider Credential
- `auth_mode` for Provider Credential
- `credential_version` for Provider Credential
- `job_id` / `asset_id` / `result_manifest_id` when the lifecycle requires it
- envelope schema version and crypto key version

The binding MUST NOT include plaintext secret material. On decrypt, the implementation verifies the stored envelope metadata and trusted request context match before releasing any plaintext.

Cause → effect:

1. A corrupted queue message changes Tenant A's account id to Tenant B's account id.
2. The vault receives the trusted job binding for A but the envelope binding for B.
3. Authenticated-data verification fails.
4. The vault returns a safe integrity/authorization failure, emits no plaintext, and makes no Provider call.

### 4.4 No cross-Tenant ciphertext reuse

1. Identical Asset checksums, prompts, external identities, or credential material MUST NOT create a shared plaintext object or cross-Tenant decrypt path.
2. Content-addressed storage MAY deduplicate within one Tenant only, and only if the dedupe key and encryption binding cannot cause one Tenant to observe another Tenant's existence.
3. A successful decrypt for Tenant A MUST NOT populate an unscoped process cache usable by Tenant B.
4. Rewrapping an object preserves its Tenant/resource binding and does not make the ciphertext portable to another account or Tenant.

---

## 5. Authorization and decrypt boundary

### 5.1 Common authorization pipeline

Every protected-data access follows this logical order:

1. Resolve a trusted Security Principal or same-Tenant system-job context.
2. Resolve the target resource under the owning `tenant_id`; foreign/unknown Public API ids retain #6 404-class non-enumeration.
3. Check the requested purpose against the allowlist in §5.2.
4. Check Client API Key scope, account/job/Asset lifecycle state, current credential version, Capability/routing gates, and retention/deletion state as applicable.
5. Create an append-only audit intent with actor, Tenant, resource, purpose, version, and outcome.
6. Ask the vault to verify the envelope binding and revocation state.
7. Release plaintext only inside the bounded operation that requested it; otherwise return a safe failure.

The vault MUST NOT infer authorization from a credential handle, `provider_account_id`, `asset_id`, worker identity, database row visibility, or possession of an envelope blob alone.

### 5.2 Purpose allowlist

| Purpose | Allowed caller/context | Data that may be released | Required boundary |
|---|---|---|---|
| `provider_execution` | Chat X3, image execution, or Render Job worker after #8/#9/#10/#11/#14 gates | Current Provider Credential version for the selected same-Tenant account | One request/job; no cross-account fallback after possible commit |
| `provider_probe` | Authorized `accounts.manage` action or same-Tenant probe worker | Credential needed for the minimal required probe | Probe scope only; no general account export |
| `provider_refresh` | Same-Tenant lifecycle worker when `refresh_supported` is true | Current refresh-capable credential | Singleflight per `(tenant_id, provider_account_id)` as #9 requires |
| `asset_validation` | Same-Tenant upload/reference/job validation | Input/mask bytes needed for canonical validation | Asset scope and retention state apply; no telemetry copy |
| `asset_retrieve` | Owning Tenant principal with `assets.read` | Own Asset bytes or safe metadata | #13 expiry/delete gate applies before bytes |
| `asset_job_input` | Same-Tenant Render Job worker | Immutable input/mask bytes referenced by that job | Job Tenant and Asset ids must match |
| `output_placement` | Same-Tenant output-delivery worker | Result bytes for one #14 output entry | Stable placement key and #13 storage reservation |
| `prompt_execution` | Same-Tenant admitted chat/job execution | Prompt/request payload required for the current execution | No durable plaintext beyond the operation |
| `replay_or_recovery` | Same-Tenant idempotency/job recovery owner | Only the encrypted payload/result needed to replay or recover the same operation | Never starts a new possibly-duplicate Provider attempt |
| `vault_rewrap` | Vault-internal key migration | Ciphertext transformation only; no application-visible plaintext | Must preserve all business/resource bindings |

The following purposes are **not** allowed to decrypt Provider Credential, prompt, Asset, or staging content:

- account list/get, routing candidate construction, Capability Snapshot read, health display, progress/status display, audit read, support diagnostics, operator search, or ordinary metrics;
- Client API Key authentication (the verifier is HMAC-checked, not decrypted);
- revoke, disable, delete, expiry, tombstone, or storage-cap accounting;
- a worker that has not proven the job/resource Tenant and current fencing/lease authority;
- a foreign or unknown resource path;
- a fallback path after an upstream attempt whose commit status is `committed` or `unknown`.

### 5.3 Provider Credential execution sequence

The #12 `§3.1 X3 Selected-account gate → Credential decrypt` and #14 execution sequence are refined, not replaced. This section refines the cryptographic authorization, audit, and failure semantics inside X3; it does not move decrypt before selected-account authorization or create a second execution flow:

1. Admission and same-Tenant ownership pass.
2. Routing resolves one account and the account lease/worker lease is established.
3. Usability, Capability Snapshot, risk, and request-time scope gates are reaffirmed.
4. The caller submits `provider_execution` with `(tenant_id, provider_account_id, credential_version, job/request id)`.
5. The audit intent and vault binding checks succeed.
6. The vault releases plaintext only to the Adapter operation; the job/request stores only the version and safe handle.
7. After the Adapter operation, plaintext is released/eligible for zeroization; in-flight residual rules remain those of #12/#14.

Releasing a credential does not grant authority to any later request. A second upstream attempt requires a new execution decision and remains subject to the #12/#14 commit boundary.

### 5.4 Prompt, Asset, and staging access

1. A prompt or request payload is decrypted only for the same Tenant's current execution, authorized idempotency replay, or same-job recovery. A request fingerprint alone is not permission to recover the original prompt.
2. Asset bytes are released only after #13 ownership, scope, expiry, deletion, and storage lifecycle gates. A foreign/unknown `asset_id` never reaches content decryption.
3. Render staging and temporary Provider handles are released only to the same Tenant's output retrieval/placement/recovery worker. A temporary URL is treated as bearer-sensitive even if it is not a Provider Credential.
4. A completed Render Job's output retry reads the existing result manifest/staging object and MUST NOT decrypt a new prompt or invoke generation again merely because placement failed.
5. Audit/support/operator paths receive safe metadata and remediation classes only; they do not receive prompt, Asset, staging, or credential plaintext.

### 5.5 Operator and break-glass posture

#6's default-deny operator posture remains binding:

- There is no ordinary operator decrypt path in MVP.
- A future break-glass design would require a separate ADR, explicit purpose, dual control, time bound, Tenant/resource binding, and audit-before-decrypt.
- Even a future break-glass path MUST NOT return Provider Credential plaintext to a support transcript, log, or ordinary operator UI field.

---

## 6. Rotation, revocation, compromise, and recovery

### 6.1 Three independent version families

The implementation MUST keep these versions distinct:

| Version | Owns | Rotation effect |
|---|---|---|
| `credential_version` | Provider Credential business identity and #9 validation/probe/reauth lifecycle | New material version; may require probe/inheritance and changes which version is usable |
| `crypto_key_version` | Encryption/wrapping key generation | Rewrap/re-encrypt protection; does **not** change account identity, credential lifecycle, or probe satisfaction |
| `hash_version` | Client API Key HMAC/pepper verifier generation (#8) | Recompute/verify key hashes; does **not** decrypt API keys or Provider Credentials |

A crypto-key rotation MUST NOT be used as evidence that a Provider Credential was refreshed, reauthenticated, or capability-probed. A pepper rotation MUST NOT be implemented as decryption of a historical API key.

### 6.2 Provider Credential rotation and reauthentication

The business rotation sequence follows #9:

1. Keep the prior valid version for already-authorized in-flight work only.
2. Accept replacement material through the controlled intake path; do not copy the old plaintext into a general-purpose store.
3. Validate and encrypt the new material under a new `credential_version`.
4. Run the required probe or explicitly permitted refresh-inheritance path.
5. Cut over new admissions to the new version only after #9 `I-USABLE-GATE` is satisfied.
6. Vault-revoke the prior version for new work; its ciphertext may remain only under an explicit bounded retention/recovery class and cannot decrypt for new admissions.
7. Audit the version transition without either material.

If validation, encryption, probe, audit, or cutover fails, the prior active version remains governed by #9; the implementation MUST NOT silently replace it with an unprobed or partially written version.

### 6.3 Cryptographic key rotation and rewrap

1. New encrypted objects use the current `crypto_key_version`.
2. Existing objects MAY be rewrapped in place or rewritten to a new envelope, but the operation preserves `tenant_id`, resource binding, `data_class`, `credential_version`, retention state, and audit provenance.
3. A successful rewrap does not require a Provider probe, does not alter account health, and does not create a new Render Job or idempotency identity.
4. The old crypto key remains read-capable until all live ciphertext has been migrated and the migration is durably verified. Retiring a key early is a security and availability defect.
5. Rewrap is idempotent. A crash may leave an old or new envelope, but never an unbound plaintext fallback or a partially accepted envelope that bypasses authentication.
6. If an old crypto key or envelope is unavailable, access to the affected object fails closed. The implementation MUST NOT return ciphertext as if it were plaintext, disable AAD checks, or copy the object into an unencrypted temporary store.

### 6.4 Compromise response

| Suspected compromise | Required immediate effect | Recovery |
|---|---|---|
| Provider Credential material | Revoke the affected credential version/account for new work; attempt #12/#14 cancellation where applicable; no new decrypt | Reauthenticate with a new version after validation/probe; do not silently reuse the suspect version |
| Tenant wrapping key or object DEK | Block decrypt for the affected Tenant/object scope until integrity is established | Rotate/reissue the wrapping key, rewrap verified objects, and audit every recovery decision |
| Platform KEK/KMS custody | Fail closed for affected key generations; incident scope is platform-controlled | Rotate root custody, rewrap affected Tenant keys/objects, and re-enable only after integrity verification |
| Client API Key pepper | Follow #8 hash-version/pepper rotation and revoke-all runbook as required | Bounded dual-verify migration only; no plaintext recovery and no Provider Credential decrypt path |
| Audit or retention control | Block sensitive allow operations and destructive purge that cannot prove policy state | Restore durable audit/retention state; reconcile pending operations from idempotent records |

Compromise handling MUST be Tenant-scoped where possible. It MUST NOT solve a Tenant A incident by borrowing Tenant B keys, credentials, counters, or plaintext.

### 6.5 Revocation and account deletion

1. A durable Provider Credential revoke is authoritative for **new** decrypt decisions. Positive authorization caches MUST NOT bypass the revoked version; unavailable revocation state fails closed.
2. Plaintext already held by an in-flight Adapter follows #12/#14 cancellation, residual, and accounting rules. Revocation does not falsely claim that an upstream operation stopped.
3. Provider Account delete follows #9's order: stop new decrypt → mark/revoke credential → delete or cryptographically purge material according to retention policy → delete/tombstone account metadata. The account id becomes not-found for ordinary Public API reads.
4. Client API Key revoke follows #8. Full material is never recovered; the non-reversible verifier and safe metadata may remain under `RETAIN-KEY-METADATA` for audit before purge.
5. Logical deletion and vault revocation are security boundaries, not merely cleanup hints. A delayed physical purge MUST NOT make a deleted credential, prompt, Asset, or staging object retrievable or executable.

---

## 7. Redaction and non-disclosure

### 7.1 Absolute no-secret locations

The following MUST NOT contain Provider Credential plaintext, Client API Key material, secret hashes, server pepper, prompt/image bytes, raw Provider responses, temporary bearer handles, envelope blobs, or foreign-resource existence details:

- Public API success bodies, error bodies, headers, streaming events, job status, progress, and Capability Snapshot projections.
- Provider Account metadata, Client API Key list/get metadata, routing records, quota records, and ordinary job rows.
- Logs, stdout/stderr, panic/exception messages, metrics labels/values, tracing attributes, profiling output, and request dumps.
- Audit event payloads and support/operator transcripts.
- OpenAPI examples, contract fixtures, snapshots, generated SDK artifacts, and test failure snapshots.
- Queue messages, lease records, retry reasons, dead-letter payloads, and cache keys.

The prohibition includes values that are merely “encrypted” or “hashed” if the value is an envelope blob, wrapped DEK, secret hash, raw bearer URL, or a high-cardinality content-derived identifier that could become a correlation oracle.

### 7.2 Allowed projections

A same-Tenant authorized projection MAY include:

- stable local ids (`tenant_id`, `provider_account_id`, `client_api_key_id`, `job_id`, `asset_id`) subject to #6 scope/non-enumeration;
- lifecycle state, safe health/remediation class, capability status, observed model slug, progress knownness, and safe failure stage;
- credential/hash/crypto **version numbers**, presence booleans, timestamps, and expiry hints when the owning spec permits them;
- byte size, media type, checksum, and output-entry state when #13/#14 permit the owning Tenant to see them;
- request id, correlation id, and redacted retryability class.

Safe projection does not mean globally public. Tenant-scoped metadata remains subject to authorization and retention.

### 7.3 Input and error redaction

1. Redaction happens before structured logging, tracing, metrics extraction, error wrapping, and audit serialization. It is not a downstream dashboard filter.
2. `Authorization` headers, cookie headers, OAuth codes/tokens, query strings carrying accidental secrets, and multipart fields with credential material MUST be filtered at ingress and again at exception boundaries.
3. Provider errors are normalized into #16 classes. Raw upstream bodies, URLs with bearer query parameters, and secret-bearing headers are discarded or stored only in a separately authorized, encrypted incident record with a retention class; they are never attached to the Public API error.
4. Prompt and Asset bytes MUST NOT be used as metric labels or trace attributes. Content-derived hashes MAY be used for same-Tenant integrity, but SHOULD be omitted from broad telemetry and MUST never reveal foreign existence.
5. Synthetic fixtures use clearly fake material and placeholders. A fixture that resembles a real token/cookie is non-conforming even if it is not currently valid.

---

## 8. Retention, deletion, and legal holds

### 8.1 General retention rules

1. Every durable sensitive-data class has a named retention class and an owning lifecycle. No content-bearing data is retained indefinitely by accident.
2. Logical expiry/deletion is enforced by the read/use gate at the lifecycle boundary. Physical byte/key reclamation MAY be asynchronous, but it cannot extend retrieval or execution.
3. Retention workers are cleanup mechanisms, not authorization mechanisms. If a cleanup worker is delayed or unavailable, the read/decrypt gate still returns a safe gone/not-found or non-usable result.
4. Deletion is idempotent within the owning lifecycle's bounded tombstone/idempotency window. A retry cannot release storage, purge keys, or emit a second destructive side effect.
5. Retention metadata contains only the minimum safe lifecycle fields. A tombstone does not retain prompt, Asset bytes, credential material, or foreign-resource details.

### 8.2 Named sensitive-data retention classes

| Class | Applies to | MVP lifecycle rule |
|---|---|---|
| `RETAIN-CREDENTIAL-ACTIVE` | Current Provider Credential ciphertext | Retain only while the attached account/version may be needed by #9, #12, or #14; decrypt remains purpose- and state-gated |
| `RETAIN-CREDENTIAL-REVOKED` | Revoked/old Provider Credential ciphertext | Retain only for bounded in-flight/recovery/audit needs; never decrypt for new work; cryptographically purge after the class expires |
| `RETAIN-KEY-METADATA` | Client API Key verifier and safe lifecycle metadata | Retain revoked metadata for audit; never retain full material; purge verifier when the class expires |
| `RETAIN-PROMPT-EXECUTION` | Encrypted prompt/request payload needed for an active or recoverable execution | Retain through the execution/accounting/recovery boundary, then purge unless a separate replay policy applies |
| `RETAIN-IDEMPOTENCY-REPLAY` | Encrypted replay response or content needed by a scoped idempotency record | Retain only until the #16/#20 idempotency record expires; Tenant scope remains mandatory |
| `RETAIN-REQUEST-METADATA` | Safe request, usage, fingerprint, and lifecycle metadata | Retain under the request/audit policy; fingerprints do not authorize content recovery |
| `RETAIN-ACCOUNT-METADATA` | Deleted Provider Account safe metadata/tombstone | Retain only the minimum non-secret audit state; deleted ids remain not-found to ordinary APIs |
| `RETAIN-JOB-METADATA` | Terminal Render Job state, manifest references, and safe progress/accounting record | Retain according to #14 replay/audit policy; no prompt/Asset bytes after their content class expires |
| `RETAIN-AUDIT` | Secret-free access and lifecycle audit events | Append-only retention for security/accountability; deletion of a source resource does not erase the integrity record absent an explicit legal policy |
| `RENDER-STAGING-RETENTION-CLASS` | #14 staging bytes, result handles, and retrieval references | Retain only until output delivery/recovery resolves under #14; temporary handles are never exposed as logs or permanent credentials |
| `RETAIN-INPUT` / `RETAIN-OUTPUT` / `RETAIN-EPHEMERAL` | #13 Assets | #13 owns expiry, download cutoff, tombstone, and storage-release semantics; encryption does not extend them |

Numeric values are deliberately named, not frozen here. #17 may tune them, but it MUST NOT make default Provider Credential, prompt, Asset, or staging retention unbounded or make an expired/deleted object retrievable.

### 8.3 Content-specific deletion behavior

| Data class | Logical deletion/expiry | Physical or cryptographic action |
|---|---|---|
| Provider Credential | Revoke/non-usable immediately; account delete makes id not-found | Destroy object DEK or equivalent after in-flight/recovery and retention obligations; no decryptable legal-hold copy |
| Client API Key material | Plaintext already absent after create/rotate response; revoke blocks auth per #8 | Purge verifier/metadata after `RETAIN-KEY-METADATA`; no plaintext reconstruction |
| Prompt/request payload | Stop replay/recovery access at execution/TTL boundary | Delete ciphertext and object key; retain only safe fingerprint/audit metadata if policy allows |
| Asset bytes | #13 expiry/delete makes bytes non-retrievable immediately and releases usage once | Reclaim object/key asynchronously; tombstone contains no bytes/content metadata |
| Render staging/temporary handle | #14 output-entry/retention state stops retrieval when expired/failed | Delete bytes and bearer handle; no re-render or output recreation is implicit |
| Account/job metadata | Mark deleted/terminal and restrict ordinary reads | Retain safe audit projection only for named class, then purge/tombstone per owning spec |
| Audit record | Normally append-only and not a content retrieval surface | Retain safe event; legal deletion must not expose secret or restore access |

### 8.4 Retention holds

A **Retention Hold** is a platform-controlled policy record that may preserve encrypted evidence for a defined scope and review period. It is not a Public API content-sharing feature.

1. A hold binds to `(tenant_id, resource kind/id, data class)`, authority, reason, creation time, review/expiry time, and hold state.
2. A hold MAY delay physical or cryptographic destruction of encrypted Tenant-confidential evidence when policy requires it.
3. A hold MUST NOT:
   - make an expired/deleted Asset downloadable;
   - make a deleted Provider Account usable;
   - allow a revoked Provider Credential to decrypt or execute;
   - restore a prompt, staging object, or replay record to a Public API;
   - prevent #13 storage accounting from releasing headroom at logical deletion;
   - turn a Tenant A hold into access to Tenant B data.
4. Provider Credential holds are **not decryptable holds**. If evidence must be preserved, it remains cryptographically unusable for product execution; the product does not retain a live bearer secret solely for evidence.
5. Apply, release, deny, expiry, and failed hold checks are audited. If hold state is unavailable, physical purge fails closed, while logical non-retrievability and non-usability remain enforced.
6. A new cross-Tenant or operator break-glass hold workflow requires a separate ADR; MVP has no ordinary operator decrypt path.

### 8.5 Deletion ordering and uncertainty

For destructive lifecycle operations, the safe ordering is:

1. Stop new authorization and mark the resource non-usable/non-retrievable.
2. Revoke the relevant credential/version or content-access capability.
3. Persist the lifecycle/tombstone and audit intent atomically where the storage boundary supports it.
4. Release #8/#13 accounting exactly once where the owning spec requires it.
5. Destroy object keys/ciphertext and reclaim physical storage according to retention/hold state.
6. Purge the tombstone only after its bounded idempotency window.

If any step after logical non-use is uncertain, recovery MUST preserve non-use and retry conservatively. It MUST NOT re-enable, re-serve, or assume a deletion/revocation did not happen merely because physical cleanup was not acknowledged.

---

## 9. Audit semantics

### 9.1 Audit event contract

Every sensitive-data access decision and lifecycle mutation has an append-only, idempotent audit event with safe fields:

| Field | Rule |
|---|---|
| `event_id` | Stable idempotency identity; duplicate delivery does not create a second logical event |
| `event_type` | Action/purpose class, not a secret or raw Provider message |
| `tenant_id` | Owning Tenant; never a caller-supplied substitute |
| `actor_type` / `actor_id` | Public principal, same-Tenant worker/system job, vault, or denied operator attempt |
| `resource_kind` / `resource_id` | Safe local id within Tenant scope |
| `purpose` | `provider_execution`, `provider_probe`, `provider_refresh`, `asset_retrieve`, `prompt_execution`, `vault_rewrap`, etc. |
| `decision` / `outcome` | Allowed/denied, success/failure, and safe failure class |
| `business_version` | `credential_version` or resource revision when relevant |
| `crypto_key_version` | Key generation used, if needed for incident/recovery evidence |
| `request_id` / `job_id` | Safe correlation when available |
| `occurred_at` | Server-owned timestamp |
| `retention_class` / `hold_state` | Lifecycle evidence without content |

The event MUST NOT contain plaintext/ciphertext Provider Credential, Client API Key material/hash/pepper, prompt, Asset bytes, raw Provider response, temporary bearer URL/handle, envelope blob, or foreign-resource existence detail.

### 9.2 Required audit events

At minimum, the system records:

- allowed and denied vault decrypt authorization, including purpose and version;
- Provider Credential submit/validate/probe/refresh/reauth/rotate/revoke/delete/purge transitions (#9);
- Client API Key create/rotate/revoke and abuse-revoke outcomes without material (#8);
- encryption, rewrap, key-version migration, integrity failure, and compromise-response decisions;
- prompt/request payload retention, replay/recovery, expiry, and deletion decisions;
- Asset upload/reference/retrieve/delete/expiry/placement outcomes as required by #13/#14;
- retention-hold apply/release/deny/expiry and failed hold-state checks;
- operator/break-glass attempts, including default-deny decisions.

High-volume allowed execution decrypts MAY use an equivalent append-only access ledger rather than a human-readable event stream, but the ledger MUST preserve the same fields, Tenant binding, idempotency, and retention guarantees.

### 9.3 Audit availability and failure

1. A sensitive allow operation (credential/prompt/Asset/staging decrypt) MUST NOT release plaintext unless its audit intent is durably accepted or atomically coupled to the authorization decision.
2. If the audit path is unavailable, new sensitive decrypt/refresh/probe/rewrap operations fail closed with a safe service/dependency class. No raw fallback logging is allowed.
3. A security-tightening operation (revoke, logical delete, expiry, disable) may proceed only when its lifecycle state and audit intent are durably coupled. If the coupled write is unavailable, the caller MUST NOT receive a false success; a safe non-use marker MAY be recorded by the owning state store and reconciled later.
4. Audit delivery is at-least-once at the transport level but exactly-once at the logical `event_id` level. Replay/recovery MUST NOT duplicate a destructive lifecycle effect.
5. Audit readers receive only safe same-Tenant fields. Audit search is not a credential-recovery or foreign-resource-enumeration mechanism.

---

## 10. Failure semantics and security impact

| Failure | Required behavior | MUST NOT |
|---|---|---|
| Tenant/resource binding mismatch | Deny before plaintext; safe internal integrity/security class; no Adapter call | Try another Tenant, infer ownership from handle, or expose foreign details |
| Envelope authentication/AAD failure | Fail closed; audit integrity failure; quarantine/recovery path | Return unauthenticated plaintext or retry with relaxed binding |
| KMS/HSM/key service unavailable | New encrypt/decrypt/rewrap fails closed; existing in-flight work follows #12/#14 | Use plaintext fallback, a shared foreign key, or allow-all cache |
| Credential ciphertext missing/corrupt | Account/version becomes non-usable for new work; remediation is reauth/operator recovery | Probe upstream with another credential or silently select another Tenant/account |
| Credential revoked during new request | Vault denies new decrypt; in-flight follows account/job cancellation and residual rules | Treat stale positive authorization as permission to start new work |
| Prompt/Asset/staging retention state unavailable | Retrieval/decrypt fails closed; cleanup may retry | Serve data past an unknown expiry or copy it to an unencrypted store |
| Audit store/outbox unavailable | Sensitive allow operation fails closed; no secret-bearing fallback logs | Release plaintext without audit or claim a successful audited transition |
| Redaction failure or unknown field | Drop/block the telemetry event or operation at the safe boundary | Emit the raw value “temporarily” for debugging |
| Retention hold state unavailable | Physical purge waits; logical expiry/delete and non-use remain enforced | Restore access or delete evidence without knowing hold state |
| Rewrap interrupted | Resume idempotently using old/new envelope; preserve old read key until verified | Delete the only readable key or write an unbound partial envelope |
| Worker/queue message has injected foreign ids | Fencing/ownership/AAD checks fail before decrypt | Let worker identity or message possession bypass Tenant authorization |
| Public API asks for foreign/unknown id | #6 404-class non-enumeration, zero content decrypt | Return a distinct 403 or metadata that confirms foreign existence |

### 10.1 Security impact summary

| Defect | Impact |
|---|---|
| Plaintext Provider Credential in a general store | Database/log/trace leak becomes Provider account takeover |
| Shared DEK or missing Tenant AAD | Cross-Tenant ciphertext substitution and confused deputy |
| Credential handle treated as authority | Any leaked locator can trigger unauthorized decrypt |
| Crypto rotation changes business version | Unprobed credentials or stale Capability Snapshot authorize execution |
| Revocation bypassed by a positive cache | Stolen credential continues acting after revoke |
| Prompt/Asset retention without read-gate enforcement | Confidential content served past policy and deletion |
| Temporary Provider URL in logs | Log reader obtains a bearer capability to a result |
| Audit event contains secret/content | Security evidence becomes a second exfiltration channel |
| Audit unavailable but decrypt allowed | Sensitive access cannot be attributed or investigated |
| Legal hold restores retrieval/use | Deleted data remains live and violates both privacy and account-revocation policy |
| Cleanup failure treated as permission | Data is served or re-used because physical state was mistaken for logical state |

---

## 11. Test obligations

Exact HTTP/OpenAPI harness arrives with #18–#20. These are required observable conformance cases for #15; they do not require production secrets.

### 11.1 Classification and redaction

1. Provider Account list/get, Capability Snapshot, job status, progress, error, logs, metrics, traces, audit, and contract fixtures contain no Provider Credential material.
2. Client API Key create/rotate returns full material once; later responses/logs contain no material, secret segment, or secret hash.
3. Prompt, Asset bytes, raw Provider payloads, temporary URLs/handles, envelope blobs, and wrapped DEKs do not appear in telemetry or contract artifacts.
4. Safe version/status/timestamp metadata remains available to the owning Tenant without exposing secret material.

### 11.2 Ownership, binding, and decrypt purpose

5. Tenant A cannot decrypt, retrieve, or use a Provider Credential, prompt, Asset, staging object, or replay payload belonging to Tenant B.
6. A corrupted account/job/Asset binding fails authenticated-data verification before plaintext and before Adapter execution.
7. A credential handle, resource id, worker identity, or Client API Key id without the matching Tenant/resource/purpose authorization cannot decrypt.
8. Valid `provider_execution` access succeeds only after account usability, capability, routing/lease, and current-version gates.
9. `provider_probe` and `provider_refresh` are allowed only on same-Tenant authorized lifecycle paths; list/get/routing/audit/support paths never decrypt.
10. Revoke, delete, expiry, storage accounting, and audit-read paths do not decrypt Provider Credential or content merely to perform their operation.

### 11.3 Rotation and revocation

11. Provider Credential rotation creates a new `credential_version`, requires #9 validation/probe/inheritance, and keeps the prior version unavailable to new work after cutover.
12. Crypto-key rewrap changes `crypto_key_version` only; it does not change `credential_version`, account state, Capability Snapshot satisfaction, or business idempotency.
13. Client API Key `hash_version`/pepper rotation never attempts to decrypt a historical key or Provider Credential.
14. Revoked credential versions cannot authorize new decrypt even when an old positive authorization/cache entry exists; in-flight execution follows #12/#14 residual rules.
15. Missing/retired key material fails closed; no plaintext or unencrypted fallback is accepted.

### 11.4 Retention and deletion

16. Prompt/request payloads are ephemeral by default and are retained only when an explicit execution/replay/recovery class requires it.
17. Render Job recovery can use encrypted retained payload/staging data within #14's lifecycle, but output placement retry performs zero new generation/edit/inpaint calls.
18. Asset expiry/delete makes retrieval fail before physical byte reclamation and releases #13 storage usage exactly once.
19. Provider Account delete stops new decrypt before credential purge; the deleted account is not-found through ordinary APIs.
20. Key metadata, prompt/replay data, staging, account metadata, job metadata, and audit records each follow their named retention class rather than an accidental shared TTL.
21. A retention hold may preserve unusable encrypted evidence but never restores Public API retrieval, credential decrypt, output download, or storage accounting.
22. Retention-worker outage does not cause expired/deleted content to be served.

### 11.5 Audit and unavailable dependencies

23. Every allowed/denied sensitive-data access has a Tenant-scoped, secret-free, idempotent audit record with purpose, actor, resource, version, and outcome.
24. Audit-store outage blocks new sensitive decrypt/refresh/probe/rewrap and does not emit raw fallback logs.
25. Destructive lifecycle acknowledgement is not returned when the lifecycle/audit write is not durable; recovery does not repeat the destructive side effect.
26. Audit replay deduplicates by `event_id` and cannot resurrect a deleted credential, Asset, prompt, or staging object.

### 11.6 Failure and non-enumeration

27. Foreign and unknown identifiers produce the same safe Public API outcome and zero foreign content/decrypt access.
28. Envelope/AAD mismatch, corrupt ciphertext, key-service outage, or retention-state uncertainty produces a safe failure and zero Adapter call.
29. A secret-bearing value injected into an upstream exception is removed before logs, traces, metrics, audit, or Public API error serialization.
30. Concurrent rotation, revoke, delete, rewrap, expiry, and recovery operations settle exactly once and never create a second valid secret or access path.

---

## 12. Core invariants

1. **I-SENSITIVE-CLASS** — Every sensitive-data value belongs to one explicit data class with a Tenant owner, storage boundary, allowed purpose, and retention class.
2. **I-CREDENTIAL-VAULT-ONLY** — Provider Credential plaintext exists only inside the authorized vault/Adapter/probe/refresh boundary; account metadata and ordinary stores never contain it.
3. **I-KEY-MATERIAL-ONCE** — Client API Key plaintext is displayed only at create/rotate response time and is never durably reconstructible; its verifier is non-reversible.
4. **I-ENVELOPE-TENANT-BIND** — Authenticated encryption binds ciphertext to Tenant, resource, data class, and relevant business version; cross-Tenant/resource substitution fails before plaintext.
5. **I-PURPOSE-BOUND-DECRYPT** — Decrypt requires a same-Tenant, resource-bound, allowlisted purpose and current lifecycle/version authorization; a handle or id is never sufficient.
6. **I-NO-AMBIENT-PLAINTEXT** — Decrypted material/content is not a reusable global cache or worker ambient authority; one Tenant's plaintext cannot authorize another Tenant's request.
7. **I-CRYPTO-BUSINESS-VERSION-SEPARATE** — `credential_version`, `crypto_key_version`, and `hash_version` have independent semantics; cryptographic rewrap does not imply reauth/probe.
8. **I-ROTATION-FAIL-CLOSED** — Key migration preserves old readable ciphertext until verified, is idempotent, and never falls back to plaintext, unauthenticated ciphertext, or another Tenant's key.
9. **I-REVOKE-NONUSE** — Durable credential revoke/delete prevents new decrypt immediately at the authoritative boundary; in-flight work follows #12/#14 without false cancellation claims.
10. **I-REDACT-SENSITIVE** — Secrets, prompt/image content, raw Provider payloads, bearer handles, envelope blobs, and foreign existence never appear in Public API, logs, metrics, traces, audit, support, or contract artifacts.
11. **I-RETENTION-BOUNDED** — Durable content has a named bounded retention class; no cleanup delay extends logical retrieval or execution authorization.
12. **I-DELETE-LOGICAL-FIRST** — Delete/expiry first makes data non-retrievable/non-usable and settles ownership/accounting exactly once; physical reclamation is asynchronous but cannot restore access.
13. **I-HOLD-NO-USE** — A retention hold may delay physical destruction of encrypted evidence but never preserves decryptable Provider Credential, restores retrieval, or changes Tenant ownership.
14. **I-AUDIT-NO-SECRET** — Sensitive access and lifecycle events are auditable with safe actor/purpose/resource/version/outcome metadata, never with the secret or content itself.
15. **I-AUDIT-BEFORE-ALLOW** — New sensitive allow operations require a durable audit intent or atomic audit coupling; audit/key/retention uncertainty fails closed.
16. **I-AUDIT-IDEMPOTENT** — Audit and destructive lifecycle replay are idempotent; retries cannot duplicate a purge, release, revoke, or access grant.
17. **I-SAME-TENANT-DATA** — Provider Credential, prompt, request payload, Asset, staging, replay data, metadata, and audit access remain within the owning Tenant; no shared pool or silent cross-Tenant path exists.
18. **I-NO-CONTRACT-SECRET** — OpenAPI examples, fixtures, generated artifacts, and public error contracts contain placeholders and safe classes only, never real or reversible secret material.
19. **I-FAIL-CLOSED-SENSITIVE** — Missing key service, corrupt envelope, unavailable revocation/audit/retention state, or binding mismatch cannot result in plaintext release, Provider execution, or content retrieval.

---

## 13. Open follow-ups

| Topic | Issue / owner | Constraint retained here |
|---|---|---|
| KMS/HSM vendor, approved AEAD suite, key custody topology, cryptoperiods | Implementation architecture / #21 | Logical hierarchy, authenticated binding, versioning, and fail-closed semantics are fixed; vendor choice is not |
| Numeric retention, purge, key-rotation, and audit windows | #17 | Named classes and logical deletion/non-use are fixed; numbers may be tuned without unbounded retention |
| Canonical error-code strings and dependency outage problem details | #16 | Failure stage/class and safe redaction are fixed |
| Public JSON/OpenAPI envelope, replay, asset, job, and account schemas | #18/#20 | Logical fields and redaction semantics are fixed; wire names remain open |
| Pure-Go vault ports and module/dependency budget | #21 | Storage-independent logical operations and authorization inputs are fixed |
| Jurisdiction-specific retention schedules and formal compliance attestations | Future security/compliance decision | Any schedule must preserve logical expiry, non-retrievability, non-use, no plaintext credential hold, and Tenant isolation |
| Full Tenant deletion/export workflow | Future product decision | Resource-level deletion, retention, and audit behavior here remains binding; no child transfer is implied |
| Break-glass operator access | Future ADR | MVP default deny; any future path needs dual control, time bound, purpose binding, and audit-before-decrypt |
| Encrypted chat-history product and user-visible transcript controls | Reopen `D-CHAT-HISTORY` | MVP does not retain transcript by default; explicit replay/recovery classes remain bounded and Tenant-scoped |
| Content-addressed Asset dedupe across Tenants | Reopen `D-ASSET-DEDUPE` | Cross-Tenant ciphertext/metadata sharing remains forbidden |

---

## 14. ADR decision

No new ADR is filed for the MVP. Tenant isolation, BYOA, Provider Credential separation, Client API Key hashing, account lifecycle, Asset retention, and durable job recovery were already product-locked in parent #1 and #6–#14. This document is the durable normative expansion under `docs/spec/` for confidentiality boundaries, envelope semantics, purpose-bound decrypt, retention/deletion, redaction, and access audit.

A separate ADR **would** be warranted if the product later introduced:

- shared decryptable Provider Credentials or Tenant wrapping keys;
- a fail-open path when key, revocation, audit, or retention state is unavailable;
- legal holds that preserve decryptable Provider Credentials or restore deleted Asset retrieval;
- ordinary support/operator plaintext access;
- default durable chat history or cross-Tenant content dedupe;
- or a cryptographic key rotation that intentionally changes Provider Account/credential business semantics.

---

## 15. Constants and reopen ids

| Id | Meaning |
|---|---|
| `CREDENTIAL-ENVELOPE-V1` | Logical authenticated envelope for Provider Credential material |
| `TENANT-TWK` / `platform_KEK` / `object_DEK` | Logical key hierarchy; implementation may use equivalent KMS/HSM primitives |
| `credential_version` | Provider Credential business version owned by #9 |
| `crypto_key_version` | Envelope/wrapping key version owned by vault crypto |
| `hash_version` / `H-KEY-HMAC` | Client API Key verifier version and default HMAC scheme owned by #8 |
| `RETAIN-CREDENTIAL-ACTIVE` / `RETAIN-CREDENTIAL-REVOKED` | Provider Credential ciphertext lifecycle classes |
| `RETAIN-KEY-METADATA` | Revoked Client API Key verifier/metadata retention class |
| `RETAIN-PROMPT-EXECUTION` / `RETAIN-IDEMPOTENCY-REPLAY` / `RETAIN-REQUEST-METADATA` | Prompt/request content and metadata classes |
| `RETAIN-ACCOUNT-METADATA` / `RETAIN-JOB-METADATA` / `RETAIN-AUDIT` | Safe lifecycle/audit metadata classes |
| `RENDER-STAGING-RETENTION-CLASS` | #14 staging/result-handle retention class |
| `RETAIN-INPUT` / `RETAIN-OUTPUT` / `RETAIN-EPHEMERAL` | #13 Asset retention classes |
| `I-SENSITIVE-*` | Invariants in §12 |
| `D-CHAT-HISTORY` | Reopen for default durable transcript/history product |
| `D-VAULT-KEY-TOPOLOGY` | Reopen if a future architecture changes the logical key hierarchy |
| `D-LEGAL-HOLD-CREDENTIAL` | Reopen only for a legal requirement that would change the no-decryptable-credential-hold rule; requires ADR |

---

## 16. Acceptance criteria traceability

| AC (issue #15) | Where satisfied |
|---|---|
| Encryption boundary, key/envelope rotation, and decrypt rights are independent of storage implementation | §3.1–§3.3, §4, §5, §6.1–§6.4, `I-ENVELOPE-TENANT-BIND`, `I-PURPOSE-BOUND-DECRYPT`, `I-CRYPTO-BUSINESS-VERSION-SEPARATE` |
| Secret does not appear in Public API response, log, metric, trace, or contract artifact | §2.2, §3.2, §7, §11.1, `I-CREDENTIAL-VAULT-ONLY`, `I-REDACT-SENSITIVE`, `I-NO-CONTRACT-SECRET` |
| Retention and deletion cover credential, metadata, prompt, and Asset lifecycle | §2.2, §6.5, §8.1–§8.5, §11.4, `I-RETENTION-BOUNDED`, `I-DELETE-LOGICAL-FIRST`, `I-HOLD-NO-USE` |
| Audit records required access without recording secret or out-of-policy content | §2.2, §5.1, §7, §9, §11.5, `I-AUDIT-NO-SECRET`, `I-AUDIT-BEFORE-ALLOW`, `I-AUDIT-IDEMPOTENT` |

---

## 17. Document control

| Field | Value |
|---|---|
| Status | Accepted for specification (issue #15) |
| Check date of evidence inputs | 2026-07-15 |
| Supersedes | n/a (initial credential vault and sensitive-data lifecycle lock) |
| Next review | On #8 pepper/key changes, #9 credential/account changes, #12 chat retention changes, #13 Asset retention changes, #14 staging/recovery changes, #16 error ownership, #17 numeric tuning, or #21 vault/module architecture |
| Authors | Spec decision agent for issue #15 |
