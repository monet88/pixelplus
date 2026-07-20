# Gateway

SaaS Provider Gateway pure Go. The module seams, dependency direction,
dependency budget, composition-root rule, and public-HTTP contract-test seam
are locked in
[`docs/decisions/0009-pure-go-module-seams-and-dependency-budget.md`](../../docs/decisions/0009-pure-go-module-seams-and-dependency-budget.md).

Issue #21 remains specification-only: the Gateway runtime, concrete ports,
Provider adapters, persistence schema, and worker implementation belong to
follow-up stories.
