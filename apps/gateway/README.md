# Gateway

SaaS Provider Gateway pure Go. The module seams, dependency direction,
dependency budget, composition-root rule, and public-HTTP contract-test seam
are locked in
[`docs/decisions/0009-pure-go-module-seams-and-dependency-budget.md`](../../docs/decisions/0009-pure-go-module-seams-and-dependency-budget.md).

The complete implementation handoff, source hierarchy, capability evidence
ledger, decision ledger, vertical work breakdown, and deferred-item register
are in
[`docs/spec/provider-gateway-implementation-ready-specification.md`](../../docs/spec/provider-gateway-implementation-ready-specification.md).
Its machine-readable completion gate is
[`docs/spec/provider-gateway-implementation-ready-manifest.json`](../../docs/spec/provider-gateway-implementation-ready-manifest.json).
Grok xAI OAuth operation-to-surface behavior is locked separately in
[`docs/decisions/0010-grok-xai-oauth-operation-surface-policy.md`](../../docs/decisions/0010-grok-xai-oauth-operation-surface-policy.md).

Issues #21 and #22 are specification-only. Gateway runtime work belongs to
the separate implementation umbrella #42 and must be decomposed into vertical
stories before `go.mod`, concrete ports, Provider adapters, persistence, Vault,
workers, or deployment artifacts are added.

## Foundation runtime

Issue #44 provides the standard-library-only composition spine used by the
production command and deterministic contract fixtures. From the repository
root:

```powershell
go -C apps/gateway run ./cmd/gateway
go -C apps/gateway test ./...
go -C apps/gateway test -race ./...
```

The production process binds `127.0.0.1:8080` by default and exposes only the
operational probes `GET /healthz` and `GET /readyz`. Readiness is fail-closed
until required startup recovery succeeds. These probes do not authorize or
substitute for stable `/v1` product gates; product operations are implemented
by later runtime tickets.

Parsed non-secret process settings:

| Variable | Default |
| --- | --- |
| `PIXELPLUS_GATEWAY_ADDR` | `127.0.0.1:8080` |
| `PIXELPLUS_GATEWAY_STARTUP_TIMEOUT` | `10s` |
| `PIXELPLUS_GATEWAY_SHUTDOWN_TIMEOUT` | `10s` |
