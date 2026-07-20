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

Issues #21 and #22 are specification-only. Gateway runtime work belongs to
the separate implementation umbrella #42 and must be decomposed into vertical
stories before `go.mod`, concrete ports, Provider adapters, persistence, Vault,
workers, or deployment artifacts are added.
