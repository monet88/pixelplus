# Validation

## Proof Strategy

Acceptance is proven only through public HTTP over real `composition.New` with
controlled Principal, Admission, Replay, Account, OAuth, Vault, Probe, Audit,
Clock, and ID ports. Private use-case calls are not completion evidence.

## Test Plan

| Layer | Cases |
| --- | --- |
| Unit | Domain purpose/flow/status validity; fingerprint; lifecycle acceptors |
| Integration | Start accepted; poll pending/succeeded/failed; probe still required; single-flight; wrong mode/purpose; scope; non-enumeration; zero Vault/OAuth effects on reject; no secret leakage |
| E2E | N/A |
| Platform | `go test` / `go test -race` on Windows |
| Logs/Audit | Safe started/polled actions only |

## Fixtures

- Tenant A manage key, Tenant A read-only key, Tenant B manage key
- Pure draft OAuth account with risk ack
- Controlled OAuth adapter outcomes: pending, succeed, fail, expire

## Commands

```text
gofmt -l on touched Go files
go -C apps/gateway vet ./...
go -C apps/gateway test ./...
go -C apps/gateway test -race ./...
scripts/bin/harness-cli.exe story verify GW-047
git diff --check
```

## Acceptance Evidence

- `go -C apps/gateway test ./...` pass
- `go -C apps/gateway test -race ./...` pass
- `scripts/bin/harness-cli.exe story verify GW-047` pass
- Public HTTP contract tests cover start, pending/succeeded/failed/expired poll, probe activation, single-flight second OAuth, direct-submit blocked during journey, scope/non-enumeration, gate rejections, and no secret leakage.
