# Exec Plan

## Goal

Prove a server-owned OAuth connect journey through real composition and the
frozen Public API without projecting exchange secrets or activating without probe.

## Scope

In scope:

- OAuth start + poll HTTP operations
- Controlled OAuth exchange port
- Vault store on successful exchange
- Single-flight journey marker
- Safe wire projection and negative gates

Out of scope:

- Active-account dual-version reauth cutover (#48)
- Enable/disable/delete (#49)
- Capability snapshots (#50)
- Live Provider OAuth (#61-#66)

## Risk Classification

Risk flags:

- authentication, authorization, public contract, durable credential boundary

Hard gates:

- secrets never on wire/logs
- fail closed before Vault/Adapter on ownership/risk/lifecycle rejection
- activation only via existing probe path

## Work Phases

1. Red contract tests over `Runtime.Handler()`
2. Domain/ports/application/transport/composition vertical slice
3. Focused + full + race verify
4. Code review and local commit

## Stop Conditions

Pause for human confirmation if:

- Reauthentication dual-version semantics must expand beyond pure draft connect
- Frozen OpenAPI fields need mutation
