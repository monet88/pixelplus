# Exec Plan — US-021 Pure-Go Module Seams and Dependency Budget

## Goal

Turn the accepted domain and Public API decisions into one concrete Pure-Go
Gateway architecture that future implementation work can follow without
letting transport, Provider protocols, secrets, storage schemas, or queue
delivery become canonical product contracts.

## Scope

In scope:

- `apps/gateway` package layout and dependency direction.
- Domain/application/port boundaries for Public HTTP, Adapter, Vault,
  persistence, JobRuntime, Clock/ID, and observability.
- Composition-root lifecycle and production/test wiring rule.
- Dependency budget and exception process.
- Public-HTTP contract-test seam and side-effect observations.
- Accepted decision record and durable story/proof metadata.

Out of scope:

- Gateway Go source, `go.mod`, handlers, workers, or concrete composition.
- Provider protocol implementations, credential cryptography, KMS choice,
  database schema/migrations, queue product, or deployment wiring.
- OpenAPI contract changes and new runtime tests.

## Risk Classification

Risk flags:

- Authorization and Tenant ownership.
- Audit/security and secret handling.
- Public API composition and compatibility.
- Durable persistence and data lifecycle boundaries.
- External Provider and job-runtime boundaries.
- Multi-domain architecture with initially weak executable proof.

Hard gates:

- Secrets must remain fail-closed and out of all ordinary projections.
- No dependency or package rule may weaken the accepted #15/#16/#17/#20
  contracts.
- No runtime implementation is added to this specification-only ticket.

## Work Phases

1. Discovery: read issue #21, prerequisites #15/#16/#17/#20, domain glossary,
   architecture rules, and contract-test policy.
2. Design: define the module tree, inward dependency graph, logical ports,
   composition constructor, test seam, and dependency slots.
3. Validation planning: map each acceptance criterion to static document
   evidence and future public-HTTP conformance proof.
4. Implementation: add the accepted decision and high-risk story packet;
   update the Gateway README pointer only. Do not create runtime packages.
5. Verification: run whitespace, retained contract, story, and decision
   checks; inspect the final changed-file set and review the decision against
   the issue acceptance criteria.
6. Harness update: record intake, story/decision rows, and a detailed trace.

## Stop Conditions

Pause for human confirmation if:

- a later implementation needs a new dependency slot, Provider SDK, framework,
  queue contract, or concrete storage technology not covered by this decision;
- a port would expose secret material, foreign-resource existence, concrete
  schema/transaction types, or Provider protocol values;
- a proposed runtime change moves retry ownership or authorization gates;
- a database migration, deletion policy, or public API compatibility rule is
  required to proceed;
- acceptance would require weakening the public-HTTP composition or
  side-effect observation requirements.
