# Overview

## Current Behavior

Owning Tenants can create Provider Account drafts and activate them with a
direct credential submission plus controlled probe (#45/#46). There is no
server-owned OAuth browser/device journey: OAuth Auth Modes can only reach
usable state through the lab direct `oauth_token_import` path.

## Target Behavior

An owning Tenant starts and polls a server-owned OAuth connection journey on a
pure draft OAuth Auth Mode account through the frozen Public API operations
`startOAuthAuthorization` and `getOAuthAuthorization`. Exchange material stays
server-side; a successful exchange stores a credential version through the
Vault and lands `pending_validation` without activating. Activation still
requires the existing protected probe path. Failed or expired journeys store no
usable credential and restore `draft`. Only one journey may be in flight per
account. Tokens, codes, and PKCE secrets never appear on the wire.

## Affected Users

- Tenant operators connecting OAuth/CLI Auth Modes.
- AFK agents implementing later reauthentication cutover (#48) on this seam.

## Affected Product Docs

- `docs/spec/provider-account-connection-and-credential-lifecycle.md` sections 4.2-4.7, 8.3, 8.5, 8.7
- `docs/spec/credential-vault-and-sensitive-data-lifecycle.md` sections 5.2, 7, 9
- `docs/spec/auth-mode-risk-envelope-and-kill-criteria.md` sections 6.1-6.2
- `docs/spec/api-versioning-compatibility-idempotency-contract-testing-policy.md` sections 2.2, 5
- `contracts/openapi/pixelplus-public-api-v1.yaml` (`OAuthStartRequest`, `OAuthAuthorization`, start/poll operations)

## Non-Goals

- Dual-version reauthentication cutover while an active credential remains (#48).
- Disable/enable/delete administrative controls (#49).
- Capability Snapshot publication (#50).
- Real Provider OAuth SDK surfaces (#61-#66); this slice uses a controlled exchange port.
