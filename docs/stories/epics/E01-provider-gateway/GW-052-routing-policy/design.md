# Design

## Domain Model

One `RoutingPolicy` per Tenant (singleton). Logical fields match frozen
`RoutingPolicyFields` / `RoutingPolicy`:

- `candidate_accounts`, `selection_order` (ordered subset)
- `fallback_enabled` (default false), `fallback_chain` (ordered subset),
  `fallback_auth_modes`
- `affinity` (`enabled`, optional `window_class`)
- `lease_policy` (`enabled`, `eligible_units` ∈ `chat_stream`|`render_job`)
- Server-owned `updated_at`, `updated_by`

Missing policy projects the fail-closed system default: empty candidates,
fallback off, `updated_by=system_default`. Policy never widens offers.

## Application Flow

1. Authenticate Client API Key → derive Tenant (never trust `tenant_id`).
2. Scope: `routing.read` (GET) / `routing.manage` (PUT).
3. Size/shape (PUT only): oversize then strict JSON after A0/A1.
4. Admission (A3–A5), then:
   - GET: Read store; missing → fail-closed default projection.
   - PUT: validate uniqueness/subsets/fallback opt-in/modes; validate every
     referenced id (foreign/unknown/deleted → `resource_not_found`;
     unusable/risk/capability/health/circuit → frozen classes); one atomic
     `Replace` only after all checks pass.
5. Zero Vault decrypt and zero Adapter calls on this surface.

## Interface Contract

| Method | Path | Scope | Success |
| --- | --- | --- | --- |
| GET | `/v1/routing-policy` | `routing.read` | 200 RoutingPolicy |
| PUT | `/v1/routing-policy` | `routing.manage` | 200 RoutingPolicy |

Errors: authentication_failed, forbidden, invalid_request, request_too_large,
resource_not_found, account_not_usable, auth_mode_unavailable,
capability_unverified, snapshot_stale, capability_unsupported,
dependency_unavailable — only existing canonical vocabulary.

## Data Model

No SQL migration. Memory store for tests/foundation; File store uses the
same Windows-safe **append-only JSONL + exclusive O_EXCL lock** pattern as
`FileAccountStore`. Restore/Read/Replace reload the ledger and apply
**latest-row-wins per Tenant**. `Replace` is one logical mutation (append one
line under the store lock). Compaction is deferred.

## UI / Platform Impact

None. Public API only.

## Observability

Audit actions `routing_policy.read` / `routing_policy.replaced` with Tenant,
key id, request id, outcome — never credential material or foreign ids.

## Alternatives Considered

1. Separate RoutingPolicyService with duplicated eligibility — rejected;
   reuses `accountAllowsOffers` / load composition on ProviderAccountService.
2. Soft validation that only checks same-Tenant ownership — rejected;
   issue requires full eligibility fail-closed before Replace.
3. 404 on missing GET policy — rejected; OpenAPI has no 404 on GET and
   management prototype fails closed with system default.
