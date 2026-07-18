# Design — US-017 Health Spec Review Fixes

## Domain Model

#17 Health State + Health Reason become the canonical operational-health model. Legacy #9 labels are accepted only at compatibility boundaries and normalized through a total mapping. Scoped conditions, credential-version fencing, and state precedence remain unchanged.

## Application Flow

Documentation-only change. Future implementations normalize legacy/source observations into canonical State/Reason before persistence, routability evaluation, capability invalidation, or canonical-error mapping.

## Interface Contract

The logical contract retains finite retry timing and `retry_after_class=provider_cooldown`, but does not choose the JSON field or HTTP header carrying the numeric delay. #18/#20 remain authoritative for encoding.

## Data Model

No schema is selected. The spec continues to require Health State, Health Reason, scope, credential version, condition revision, and retry timing evidence as logical fields.

## UI / Platform Impact

No UI or runtime implementation. Tenant/operator projections continue to expose bounded state, reason, affected scope, remediation, and finite timing only when truthful.

## Observability

Legacy source tokens may be retained as bounded provenance/audit metadata, but ordinary metrics and management projections use canonical State/Reason classes and preserve existing redaction rules.

## Alternatives Considered

1. Keep #9 tokens canonical and treat #17 states as display-only — rejected because #17 explicitly owns the new state/reason model and scoped routability.
2. Update only #17 with an informal mapping — rejected because dependent canonical specs would still instruct implementers to compare obsolete tokens.
3. Update all affected normative consumers while preserving source-fidelity research documents — selected as the smallest coherent contract repair.

No new durable decision record is needed: this work reconciles existing accepted issue #17 intent and prerequisite specs rather than changing product authority or architecture.
