# Validation

## Proof Strategy

All acceptance proof enters through public HTTP backed by
`composition.New`/`Runtime.Handler()` with controlled Principal, Admission,
Replay, AccountStore, Vault, Probe, OAuth, Capability, Audit, Clock, and ID
ports. No private use-case calls or concrete schema assertions are completion
evidence.

## Test Plan

| Layer | Cases |
| --- | --- |
| Unit | pending/current version transitions and lifecycle restoration |
| Integration | direct active/disabled reauth, OAuth reauth, single-flight, monotonic failed versions, pending-only validation/probe, successful cutover, stale writer fencing, old-version revocation, safe redaction |
| E2E | N/A |
| Platform | `go test`, `go test -race`, `go vet` on Windows |
| Performance | N/A |
| Logs/Audit | no material, ciphertext, token, or raw provider payload in outputs |

## Fixtures

- Tenant A manage principal and Tenant B foreign principal
- Active and disabled accounts with version 1
- Controlled Vault validation/revocation observations
- Controlled OAuth pending/succeeded/failed outcomes
- Controlled capability observation for pending versions

## Commands

```text
go -C apps/gateway test ./internal/contracttest -run 'Reauth|Replacement|Cutover'
go -C apps/gateway test -race ./...
go -C apps/gateway vet ./...
scripts/bin/harness-cli.exe story verify GW-048
git diff --check
```

## Acceptance Evidence

- Public HTTP composition tests passed for direct active/disabled replacement,
  OAuth reauthentication, pending-version cutover, prior-version revocation,
  failed replacement restoration, monotonic failed-version allocation, OAuth
  failure origin preservation, retry reuse of a staged pending version,
  disabled-origin single-flight (second reauth blocked while pending),
  reauthentication non-enumeration, probe reject while replacement marker held,
  and stale RequirePendingVersion promotion fencing.
- `node scripts/validate-public-api-contract.mjs` passed: 26 operations and
  205 Draft 2020-12 examples.
- `go -C apps/gateway test ./...` passed.
- `go -C apps/gateway test -race ./...` passed.
- `go -C apps/gateway vet ./...` passed.
- `scripts/bin/harness-cli.exe story verify GW-048` passed.
- `git diff --check` passed.

Proof remains controlled in-memory composition; live Provider and durable SQL
adapters are explicitly outside this story's scope.
