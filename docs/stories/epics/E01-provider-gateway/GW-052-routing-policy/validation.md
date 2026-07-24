# Validation

## Proof Strategy

All behavioral proof enters through real `Runtime.Handler()` public HTTP
with controlled Account, Capability, Health, Circuit, and RoutingPolicy
stores. No private handler/use-case tests as primary evidence.

## Test Plan

| Layer | Cases |
| --- | --- |
| Integration (contract) | Tenant isolation GET/PUT; scopes; auth before validation on malformed/oversized PUT; strict body; unique arrays; ordered subsets; fallback off rejects chain/modes; Grok Web SSO modes rejected; foreign/unknown/deleted resource_not_found zero mutation; unusable/risk/capability/health/circuit reject with frozen classes; atomic Replace revision; missing policy fail-closed default; GET zero vault; /models still same-Tenant offerable only |
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
- `go -C apps/gateway test ./... -count=1` PASS
- `go -C apps/gateway build ./...` PASS
- `go -C apps/gateway vet ./...` PASS
- `go -C apps/gateway test -race ./internal/... -count=1` PASS
- `git diff --check` PASS
- `node scripts/validate-public-api-contract.mjs` PASS
- `node scripts/test-public-api-contract-validator.mjs` PASS
- Harness: intake #1, story GW-052 verify+complete
