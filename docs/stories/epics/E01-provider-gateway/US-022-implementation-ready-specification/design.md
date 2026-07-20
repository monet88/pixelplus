# Design - US-022 Implementation-Ready Provider Gateway Specification

## Domain Model

The story adds no new product entity. It creates a specification-package model
for existing truth:

- `authority` identifies the glossary, stable wire artifact, architecture
  decisions, normative specifications, and evidence sources.
- `capabilities` records one starting claim per Auth Mode and primary
  operation, plus the independent risk state and evidence document.
- `decisions` records stable cross-domain IDs and the four dimensions required
  by issue #22: observable behavior, failure semantics, security impact, and
  dependencies.
- `implementation_slices` defines a dependency graph of vertical runtime work
  and names the public seam at which behavior must later be proven.
- `deferred_items` records a stable ID, reason, dependencies, and reopen
  trigger without changing the originating invariant.
- `completion_gate` lists the required Auth Modes, operations, decisions,
  slices, document sections, and separate implementation-issue rule.

## Application Flow

The static validation flow is:

```text
load manifest
  -> validate schema/gate identity
  -> resolve every authority artifact
  -> enforce exact capability vocabulary and required matrix
  -> enforce decision dimensions and required decision IDs
  -> enforce implementation slice dependencies and required slice IDs
  -> enforce deferred reason/dependency/reopen trigger
  -> verify required human-spec sections
  -> report package counts
```

The future runtime flow is not implemented here. The assembly specification
describes its protected-operation order and links each step to the normative
owner.

## Interface Contract

The public static interface is:

```text
node scripts/validate-provider-gateway-implementation-spec.mjs [manifest]
```

On success it exits zero and prints issue, separate implementation issue,
capability-claim, decision, implementation-slice, deferred-item, and authority
file counts. On failure it exits non-zero with the first actionable contract
violation.

The human interface is
`docs/spec/provider-gateway-implementation-ready-specification.md`. It explains
authority and conflict resolution, presents the ledgers, defines the vertical
work breakdown, and distinguishes current locked semantics from future
triggered choices.

## Data Model

The manifest is versioned JSON committed to the repository. It contains paths
and prose contract summaries but no secret, live Provider credential, Tenant
record, runtime identifier, or mutable operational state. Harness remains the
durable operational store for intake, story status, proof, and trace.

The validator uses filesystem existence and structured JSON checks. It does
not parse Markdown to rediscover product semantics; required headings are only
a navigation/completeness gate. The explicit manifest IDs avoid ad hoc text
matching for capability and decision coverage.

## UI / Platform Impact

No browser, Photoshop, CLI product, service runtime, or deployment behavior
changes. GitHub issue #42 is an implementation-planning surface only.

## Observability

The validator emits one bounded success/failure line and no repository content,
secret material, external evidence payload, or live Provider response. The
future runtime observability contract remains owned by decision 0009 and the
sensitive-data/error specifications.

## Alternatives Considered

1. **Copy all normative rules into one monolithic replacement spec.** Rejected
   because copied semantics would drift and create competing authorities.
2. **Use only a link index.** Rejected because issue #22 requires observable,
   failure, security, dependency, capability, implementation, and deferral
   completeness rather than a file list.
3. **Validate Markdown with broad regular expressions.** Rejected because
   structured IDs and arrays provide a stable machine contract and clearer
   mutation failures.
4. **Begin the Gateway while assembling the handoff.** Rejected because issue
   #22 explicitly forbids runtime implementation and requires a separate issue.
5. **Treat all implementation details as unresolved product questions.**
   Rejected because concrete driver/vendor/topology choices can be safely
   deferred when their logical guarantees and reopen evidence are explicit.
