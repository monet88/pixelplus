# Exec Plan

## Goal

Implement direct and server-owned OAuth Provider Credential reauthentication for
an existing Provider Account, with fenced pending-version validation/probe and
atomic observable cutover semantics through the stable Public API.

## Scope

In scope:

- `POST /v1/provider-accounts/{provider_account_id}/reauthentication`
- OAuth `purpose=reauthenticate` on the existing start/poll routes
- One account-level replacement single-flight gate shared by direct and OAuth
- Monotonic credential version allocation, including failed versions
- Pending-version validation, probe, capability evidence, promotion, and prior
  version revocation
- Active-origin and disabled-origin replacement behavior
- Public HTTP contract tests through real composition

Out of scope:

- Enable/disable/delete administrative operations (#49)
- Silent refresh worker behavior
- Live Provider OAuth or Provider SDK integrations (#61-#66)
- Durable SQL schema migration; the current Gateway slice uses controlled ports

## Risk Classification

Risk flags:

- authentication
- authorization
- audit/security
- external provider behavior
- public contracts
- existing behavior
- weak proof
- multi-domain

Hard gates:

- credential material never leaves the Vault boundary or public projections
- replacement rejects before Vault/Adapter effects when ownership, lifecycle,
  risk, or single-flight gates fail
- a pending version cannot execute or cut over before its own validation and
  probe succeed
- stale writers cannot promote a different pending version

## Work Phases

1. Add red public HTTP contract cases for direct/OAuth replacement.
2. Extend domain, ports, application, and transport with pending-version fencing.
3. Run focused Gateway tests, vet, and race tests.
4. Update Harness proof and implementation note.
5. Run full validation, detect GitNexus changes, review, and commit.

## Stop Conditions

Pause for human confirmation if:

- the frozen OpenAPI route or response shape must change
- a SQL migration or durable storage ownership is required
- cutover requires a new transaction boundary outside the accepted Pure-Go
  ports
