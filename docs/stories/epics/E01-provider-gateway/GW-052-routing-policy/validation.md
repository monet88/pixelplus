# Validation

## Proof Strategy

All behavioral proof enters through real `Runtime.Handler()` public HTTP
with controlled Account, Capability, Health, Circuit, and RoutingPolicy
stores. No private handler/use-case tests as primary evidence.

## Test Plan

| Layer | Cases |
| --- | --- |
| Integration (contract) | Prior proofs + cross-mode only when fallback_enabled; multi-mode selection with fallback off succeeds; allowlist 403 vs foreign 404; active+unknown health; experimental fail-closed; circuit dependency 503; open circuit → capability_unsupported; scoped health cooling_down → wait_provider_cooldown; drain/quarantine → account_remediation; /models unwidened; **request-start single instant at TTL boundary** (not multi-Now capability_unsupported flip) |
| Composition/HTTP | Corrupt/lock-occupied/semantically invalid routing companion → readiness false; GET/PUT 503 `dependency_unavailable`; no durable mutation |
| Persistence | File restore rejects null/invalid rows; append-only Replace ×2 + restart latest-row wins; second Tenant intact; **Memory Replace rejects invalid durable policy without mutation** (parity with File) |
| Models regression | Existing Models contract tests still pass |
| Platform | gofmt, go test ./..., build, vet, race internal, git diff --check, OpenAPI validators, package-lock check |

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
- Follow-up commits after 049b786/678d2bc/2da8446: allowlist; cross-mode scoped to fallback_enabled (red then green); health/drain/quarantine/open-circuit public proofs; experimental fail-closed; circuit dep 503; shared ValidateRoutingPolicyShape
- Post-7ce7f86 P2 (Standards):
  - RED Memory: `TestMemoryRoutingPolicyStoreReplaceRejectsInvalidWithoutMutation` → `Replace missing UpdatedBy: want error`
  - GREEN Memory: durable validation before map mutation; Mutations/Revision retained for persistence proofs
  - RED public: `TestRoutingPolicyCandidateFreshnessUsesRequestStartInstant` → `status=409 capability_unsupported, want 200`
  - GREEN: `sc.start` threaded into validatePolicyCandidates/policyCandidateRejection for freshness + IsOfferablePair
