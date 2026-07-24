# Design

## Domain Model

Provider Account lifecycle, health, risk, capability, and administrative
controls remain independent authority planes. HealthStore owns scoped Health
Conditions and the private Recovery Permit. AccountStore owns account identity,
lifecycle, credential metadata, risk acknowledgement, and controls.

## Application Flow

1. Public HTTP requests authenticate and resolve same-Tenant account ownership.
2. Application loads AccountStore state and independently loads HealthStore
   state for the account and credential version.
3. Pre-attempt gates combine both projections without copying health authority
   back into AccountStore.
4. Cooldown observation, recovery claim, renewal, and resolution execute as
   logical HealthStore CAS operations.
5. Required audit is recorded before the durable health transition.
6. Application projects the combined safe Provider Account response.

## Interface Contract

Public HTTP routes and response shapes remain unchanged. Composition gains an
independent `ports.HealthStore` dependency. HealthStore exposes logical restore,
read, observe, recovery claim, dependency-failure renewal, and fenced resolution
operations rather than generic storage methods.

## Data Model

No database schema or migration is introduced. Foundation persistence uses
validated standard-library files with cross-process exclusion. An occupied lock
or invalid record fails closed. Container deployments mount a named volume at a
non-temporary state directory writable by the non-root Gateway user.

## UI / Platform Impact

Docker and sandbox composition keep a read-only root filesystem, but Provider
Account state persists in a named volume. Explicit sandbox cleanup may remove
the volume; ordinary restart/recreate must retain it.

## Observability

Health transition audit includes bounded Auth Mode, prior/new state and reason,
scope, credential version, source class, condition revision, safe retry timing,
request/probe identity, and outcome. It never includes credentials, raw Provider
headers, bodies, bucket identifiers, or foreign account evidence.

## Alternatives Considered

1. Keep health embedded in AccountStore. Rejected because it violates ADR 0009
   and prevents independent HealthStore proof and ownership.
2. Add SQLite or another dependency. Rejected because the review fix is scoped
   to existing standard-library dependency constraints.
3. Auto-reclaim stale file locks. Rejected because unsafe ownership stealing is
   worse than fail-closed operator remediation after a process crash.
