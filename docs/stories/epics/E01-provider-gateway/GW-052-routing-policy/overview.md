# Overview

## Current Behavior

After #50/#51 the Gateway owns Provider Accounts, Capability Snapshots,
scoped Health, and Provider Surface Circuits. There is no Tenant-owned
Routing Policy management surface: clients cannot read or replace the
singleton policy that declares candidate accounts, selection order,
fallback, affinity, or lease policy through public HTTP.

## Target Behavior

Owning Tenants read and atomically replace their singleton Routing Policy
through `GET/PUT /v1/routing-policy` on the real composed
`Runtime.Handler()`. Auth derives Tenant; the request never accepts
`tenant_id`. Writes validate shape, ordered subsets, fallback opt-in, and
every referenced account against the same eligibility semantics that
`ListModels` uses (ownership, usability, risk, capability freshness,
health, circuit). Foreign/unknown/deleted ids fail as non-enumerating
`resource_not_found` with zero mutation. Missing policy fails closed with
the system default (empty candidates, fallback off). `/v1/models` remains
same-Tenant and policy cannot widen its offers.

## Affected Users

- Tenant operators with `routing.read` / `routing.manage`
- Inference clients consuming only offerable pairs (unchanged listing)

## Affected Product Docs

- `docs/spec/tenant-scoped-routing-fallback-affinity-leases.md`
- `docs/spec/auth-mode-risk-envelope-and-kill-criteria.md` §6.3
- `CONTEXT.md` Routing Policy
- `contracts/openapi/pixelplus-public-api-v1.yaml` (frozen; no edits)

## Non-Goals

- Request-time selection/lease/affinity/fallback execution engine
- Multi-account load balancing
- OpenAPI or schema migrations
- Private handler/use-case tests as the primary proof
- Provider SDK shapes or goroutine layout assertions
