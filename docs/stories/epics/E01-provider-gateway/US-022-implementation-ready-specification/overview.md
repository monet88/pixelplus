# Overview - US-022 Implementation-Ready Provider Gateway Specification

## Current Behavior

Issues #2-#21 produced research evidence, normative domain specifications,
prototype contracts, one stable `/v1` OpenAPI artifact, and the Pure-Go module
seams. Those sources are individually authoritative, but there was no single
completion gate that proved all implementation domains were present, capability
claims used the canonical vocabulary, deferred work had reopen triggers, or a
future implementation agent could navigate conflicts without recreating
product decisions.

`apps/gateway` remains unimplemented. That is correct for issue #22.

## Target Behavior

- `docs/spec/provider-gateway-implementation-ready-specification.md` is the
  human implementation handoff and source-ownership guide.
- `docs/spec/provider-gateway-implementation-ready-manifest.json` is the
  mechanical index of authority files, capability claims, decision domains,
  implementation slices, deferred items, and completion requirements.
- The package links the stable Public API, frozen baseline, accepted
  architecture decisions, normative specifications, research evidence, and
  retained prototypes without turning the assembly document into a competing
  contract.
- A validator rejects missing authority, non-canonical capability status,
  missing Auth Modes/operations/decisions/slices, incomplete decision
  dimensions, undeclared evidence, deferred work without reopen conditions,
  and attempts to use issue #22 as the runtime implementation issue.
- GitHub issue #42 is the separate blocked Gateway implementation umbrella.
  It is not automatically started or marked ready for autonomous execution.

## Affected Users

- Gateway implementation agents and reviewers.
- Security and platform operators reviewing Tenant, Vault, risk, health,
  audit, recovery, and deployment boundaries.
- Public API and Photoshop Plugin implementers consuming the stable contract.
- Product owners deciding when deferred behavior or operational choices may
  reopen.

## Affected Product Docs

- `CONTEXT.md`
- `docs/spec/provider-gateway-implementation-ready-specification.md`
- `docs/spec/provider-gateway-implementation-ready-manifest.json`
- `contracts/openapi/pixelplus-public-api-v1.yaml`
- `docs/decisions/0008-stable-public-api-contract-policy.md`
- `docs/decisions/0009-pure-go-module-seams-and-dependency-budget.md`
- All normative specifications and research evidence listed in the manifest.
- `docs/validation/US-022-implementation-ready-provider-gateway-specification.md`
- `apps/gateway/README.md`

## Non-Goals

- Creating `apps/gateway/go.mod`, Go packages, handlers, ports, workers,
  Provider adapters, persistence, Vault, queue, or deployment artifacts.
- Selecting a database, schema, KMS/HSM, cryptographic dependency, queue,
  hosting topology, SLO, migration plan, or production launch policy.
- Changing the stable Public API, accepted risk states, capability meanings,
  domain semantics, authorization, retention, retry ownership, or execution
  lifecycle.
- Starting issue #42 or treating its umbrella scope as one horizontal change.
