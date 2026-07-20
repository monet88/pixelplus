# Overview — US-021 Pure-Go Module Seams and Dependency Budget

## Current Behavior

The Provider Gateway is not implemented. `apps/gateway/README.md` identifies
it as a future Pure-Go service, while `docs/ARCHITECTURE.md` still provides a
generic consumer layering template. Issues #15, #16, #17, and #20 have locked
the domain, sensitive-data, canonical-error/retry, health, and stable Public
API behavior, but the concrete Go package seams and composition root were
intentionally deferred.

## Target Behavior

- `docs/decisions/0009-pure-go-module-seams-and-dependency-budget.md` is the
  accepted authority for the future `apps/gateway` module.
- Domain, application, ports, HTTP transport, Provider adapters, Vault,
  persistence, job runtime, observability, composition, and contract-test
  boundaries are explicit.
- Dependency direction keeps transport and Provider protocols outside the
  canonical domain, and the standard-library-first budget rejects incidental
  frameworks, SDKs, ORMs, and retry libraries.
- Future tests use real HTTP composition with controlled implementations at
  ports and observe side-effect order/count without private-function,
  concrete-schema, or goroutine-layout seams.
- The decision remains specification-only: no Gateway runtime, Go module,
  database schema, queue, or Provider adapter is added by this story.

## Affected Users

- Gateway implementers building the first Pure-Go runtime.
- Security and platform operators reviewing Vault, audit, readiness, and
  dependency boundaries.
- Client and plugin implementers relying on stable Public API behavior.
- Test and review agents proving Tenant isolation, retry ownership, and
  secret-free observability.

## Affected Product Docs

- `CONTEXT.md`
- `docs/ARCHITECTURE.md`
- `docs/spec/credential-vault-and-sensitive-data-lifecycle.md`
- `docs/spec/canonical-errors-and-retry-ownership.md`
- `docs/spec/provider-account-health-cooldown-and-operator-controls.md`
- `docs/spec/api-versioning-compatibility-idempotency-contract-testing-policy.md`
- `docs/spec/tenant-ownership-authorization-invariants.md`
- `docs/spec/tenant-scoped-routing-fallback-affinity-leases.md`
- `docs/spec/chat-execution-and-streaming-lifecycle.md`
- `docs/spec/durable-render-job-and-output-retry-lifecycle.md`
- `docs/spec/asset-exchange-authorization-and-retention-lifecycle.md`
- `apps/gateway/README.md`

## Non-Goals

- Implementing the Gateway, HTTP handlers, workers, Provider adapters, Vault,
  persistence, queue, or observability runtime.
- Selecting a concrete database schema, migration tool, queue product, KMS,
  Provider SDK, web framework, or deployment platform.
- Changing the stable OpenAPI contract or the accepted domain/lifecycle/error
  decisions from #6–#20.
- Adding a generated client, Photoshop Plugin implementation, or runtime
  contract test before the composition root exists.
