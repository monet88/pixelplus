# Design

## Domain Model

`CredentialMetadata.Version` remains the publicly projected active/authorized
version. Allocation advances only via `LastAllocatedVersion`. A replacement sets
internal `PendingCredentialVersion` (and `PendingOrigin`) without changing public
`credential.version` until validation and probe promote the pending version
(ADR 0011 dual-version).

## Application Flow

1. Authenticate, scope, size/shape validate, and resolve same-Tenant ownership.
2. Check the Auth Mode, lifecycle, risk, and shared replacement marker.
3. Claim scoped idempotency and admission.
4. Put material in the Vault under the next version and persist the pending
   marker with an account-store fence.
5. Validate/probe the pending version only.
6. On success, publish pending-version capability evidence, promote the pending
   version under the same fence, then revoke the prior version. On failure,
   revoke/discard pending material and restore the origin lifecycle.

OAuth exchange remains server-owned; its one-shot material is immediately passed
to the Vault and never retained or projected.

## Interface Contract

- `POST /v1/provider-accounts/{id}/reauthentication` accepts the existing
  `DirectReauthenticationRequest` shape and requires `accounts.manage` plus
  `Idempotency-Key`.
- Existing OAuth start/poll routes accept `purpose=reauthenticate` for active,
  disabled, reauth-required, and revoked accounts subject to the shared gate.
- Existing probe route validates and probes the pending version when present.

## Data Model

No schema migration. The Memory AccountStore and controlled Vault ports gain
version fencing/revocation operations while preserving fail-closed production
defaults.

## UI / Platform Impact

None. This is a server-side Public API and composition change.

## Observability

Existing secret-free audit actions are reused for submission, OAuth polling, and
probe/promotion. Version numbers may be recorded; material, ciphertext, and
provider payloads may not.

## Alternatives Considered

1. Reuse the first-connect `/credentials` route for active accounts. Rejected:
   it would blur first-connect and replacement idempotency semantics and break
   the stable route contract.
2. Replace the account row before probing the new version. Rejected: it would
   allow an unprobed version to appear usable and make rollback ambiguous.
