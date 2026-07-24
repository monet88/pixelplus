# Validation

## Proof Strategy

All behavioral proof enters through real `Runtime.Handler()` public HTTP
with controlled Account, Capability, Health, Circuit, and RoutingPolicy
stores. No private handler/use-case tests as primary evidence.

## Test Plan

| Layer | Cases |
| --- | --- |
| Integration (contract) | Tenant isolation GET/PUT; scopes; auth before size/shape; strict required/non-null fields; unique arrays; ordered subsets; fallback off rejects chain/modes; fallback on requires non-empty chain (§8.1); malformed `pa_` id → 400; foreign/unknown/deleted identical 404 (strip request_id); quota 429 before Visible/capability; unusable/risk/capability rejects; atomic Replace; missing policy fail-closed with epoch `updated_at` + `updated_by=system_default`; /models unwidened |
| Composition/HTTP | Corrupt/lock-occupied/semantically invalid routing companion → readiness false; GET/PUT 503 `dependency_unavailable`; no durable mutation |
| Persistence | File restore rejects null/invalid rows; append-only Replace ×2 + restart latest-row wins; second Tenant intact |
| Models regression | Existing Models contract tests still pass |
| Platform | gofmt, go test ./..., build, vet, race internal, git diff --check, OpenAPI validators |

## Fixtures

- Tenant A admin (`routing.read`+`routing.manage`), reader (`routing.read`),
  inference-only key, Tenant B key
- Offer-eligible active accounts with fresh snapshots
- Controlled counters on RoutingPolicyStore.Replace and Vault/Adapter

## Commands

```text
go -C apps/gateway test ./internal/contracttest -run Routing -count=1 -timeout=180s
go -C apps/gateway test ./internal/contracttest -run Models -count=1 -timeout=180s
go -C apps/gateway test ./... -count=1 -timeout=300s
go -C apps/gateway build ./...
go -C apps/gateway vet ./...
go -C apps/gateway test -race ./internal/... -count=1 -timeout=300s
git diff --check
node scripts/validate-public-api-contract.mjs
node scripts/test-public-api-contract-validator.mjs
```

## Acceptance Evidence

- `go -C apps/gateway test ./internal/contracttest -run Routing -count=1` PASS
- `go -C apps/gateway test ./internal/contracttest -run Models -count=1` PASS
- `go -C apps/gateway test ./internal/infrastructure/persistence -run Routing -count=1` PASS (append-only Replace×2 + restore)
- `go -C apps/gateway test ./internal/composition -run Routing -count=1` PASS
- `go -C apps/gateway test ./... -count=1` PASS
- `go -C apps/gateway build ./...` PASS
- `go -C apps/gateway vet ./...` PASS
- `go -C apps/gateway test -race ./internal/... -count=1` PASS
- `git diff --check` PASS
- `node scripts/validate-public-api-contract.mjs` PASS
- `node scripts/test-public-api-contract-validator.mjs` PASS
- GitNexus: new FileRoutingPolicyStore symbols UNKNOWN (index lag); composition.New HIGH (full suite required)
- Follow-up commit after 049b786 (review fixes: shape, order, non-enum, append-only file store)
