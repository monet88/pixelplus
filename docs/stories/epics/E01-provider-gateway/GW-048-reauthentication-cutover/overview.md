# Overview

## Current Behavior

The Gateway can submit a first credential for a draft account and can start a
server-owned OAuth connect journey. Active or disabled accounts cannot replace a
credential, and OAuth reauthentication is restricted to `reauth_required`.

## Target Behavior

An owning Tenant can replace credential material through either the direct
reauthentication route or OAuth `purpose=reauthenticate` while preserving the
Provider Account id, Tenant ownership, Provider, and immutable Auth Mode. Each
attempt allocates a new monotonic credential version. The prior active version
remains the only version for authorized in-flight work until the pending version
passes validation and probe; successful probe promotes it and revokes the prior
version for new work. Failed pending versions are revoked and never reused.

Disabled-origin replacement keeps the account disabled after successful cutover.
Direct and OAuth replacement share one account-level single-flight gate, and
public responses contain only safe account/OAuth metadata.

## Affected Users

- Tenant operators recovering or rotating Provider Credentials.
- Provider Account workers consuming the versioned Vault boundary.

## Affected Product Docs

- `docs/spec/provider-account-connection-and-credential-lifecycle.md` sections
  4.9, 5.1, 9, 14.2
- `docs/spec/credential-vault-and-sensitive-data-lifecycle.md` sections 6.1,
  6.2, 11.3
- `contracts/openapi/pixelplus-public-api-v1.yaml`

## Non-Goals

- Real Provider protocol implementation.
- Silent refresh and administrative enable/disable/delete routes.
- Cross-Tenant routing or capability policy changes.
