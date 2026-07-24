# Exec Plan

## Goal

Ship Tenant-scoped Routing Policy read/replace as a focused vertical slice
through real public HTTP composition, reusing #50/#51 eligibility semantics
and the frozen OpenAPI shapes.

## Scope

In scope:

- Domain `RoutingPolicy` + affinity/lease fields
- `RoutingPolicyStore` port with atomic `Replace`
- Memory + File foundation stores
- Application `GetRoutingPolicy` / `ReplaceRoutingPolicy` on the existing
  Provider Account service (shared eligibility helpers)
- Transport `GET/PUT /v1/routing-policy`
- Composition + contract fixture wiring
- Public contract tests via `Runtime.Handler()`

Out of scope:

- Runtime chat/render routing decision engine
- Numeric lease/affinity timers (#17)
- OpenAPI changes
- Dependency or database migrations

## Risk Classification

Risk flags: Authorization, Audit/security, Public contracts, Existing behavior.

Hard gates: Authorization (tenant isolation, scopes).

Lane: **high-risk**.

## Work Phases

1. Story packet + implement notes
2. Domain/port/store foundation
3. Application write/read with eligibility
4. Transport + composition wiring
5. Contract tests (Routing + Models regression)
6. Validation suite + self-review + local commit

## Stop Conditions

Pause for human confirmation if:

- Frozen OpenAPI is proven wrong
- Missing-policy error vocabulary is product-ambiguous beyond fail-closed default
- Eligibility rules for policy write diverge from ListModels in a product way
