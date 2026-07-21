# GW-045 Create and Read Provider Account Drafts Through the Protected Request Spine

## Status

implemented

## Lane

high-risk

## Product Contract

An authenticated Tenant creates, lists, and reads its own Provider Account
drafts through the stable `/v1` Public API while authentication, scope,
admission ordering, non-enumeration, replay ownership, audit, canonical error,
and request logging execute in their locked order through the same
`composition.New` runtime used by production.

## Relevant Product Docs

- `docs/spec/tenant-ownership-authorization-invariants.md` sections 2.2-2.4 and 5.1-5.3
- `docs/spec/client-api-key-lifecycle-and-admission-controls.md` sections 4.3, 5.4, and 6-9
- `docs/spec/canonical-errors-and-retry-ownership.md` sections 3, 4.1, 7, and 8
- `docs/spec/api-versioning-compatibility-idempotency-contract-testing-policy.md` section 5
- `contracts/openapi/pixelplus-public-api-v1.yaml` (frozen wire contract)

## Acceptance Criteria

- Invalid, unknown, revoked, and hash-mismatched Client API Keys return
  indistinguishable authentication failures and never form a Security Principal.
- The Security Principal derives Tenant and Client API Key identity server-side;
  client-supplied Tenant authority is ignored.
- Scope, request-size, rate, concurrency, and quota checks run in the normative
  order before draft persistence.
- Foreign, unknown, and deleted Provider Account identifiers return the same
  non-enumerating outcome before protected access or mutation.
- Concurrent matching creates have one replay owner and one durable draft;
  conflict, in-progress, and uncertain claims never steal or duplicate work.
- Responses, errors, audit, telemetry, and the single request log contain only
  safe allowlisted fields.

## Design Notes

- Layers follow #44 direction: domain <- ports <- application <- transport;
  composition is the only wiring package.
- Spine order (application): request_id -> A0 authenticate -> A1 scope (and
  named-id ownership for get) -> A2 size -> request validation -> replay claim
  -> A3-A5 admission -> A6 durable create -> replay complete -> one
  audit/telemetry/request-log emission.
- Replay claim precedes admission so terminal/in-progress/conflict replays never
  debit admission or quota; a fresh claim rejected at A3-A5 is abandoned so a
  later retry can re-claim.
- Request-size and strict-decode outcomes are observed at the transport boundary
  but forwarded as flags so the single normative A0->A1->A2 order holds and an
  unauthenticated oversize/malformed request still fails as
  `authentication_failed`.
- Production foundation `PrincipalStore` is empty and fail-closed; contract
  tests inject controlled seeded principals to exercise the spine through real
  composition.
- Public test seam: real `httptest` server around `Runtime.Handler()` with
  controlled Principal/Admission/Replay/Account/Audit/Telemetry/RequestLog/Clock/
  ID ports.

## Validation

| Layer | Expected proof |
| --- | --- |
| Unit | Architecture import rules; command/query validation. |
| Integration | Six acceptance criteria exercised over HTTP through real composition with controlled ports. |
| E2E | Not applicable; no browser/user workflow. |
| Platform | Module builds and full/race suites pass on Windows. |
| Release | `go vet`, full Go tests, race tests, gofmt. |

## Harness Delta

Adds `GW-045` as the protected request-spine proof row with a reusable
`go -C apps/gateway test -race ./...` verification command.

## Evidence

- `go -C apps/gateway build ./...`, `go -C apps/gateway vet ./...`,
  `go -C apps/gateway test ./...`, and `go -C apps/gateway test -race ./...`
  passed.
- New contract tests in `internal/contracttest` enter through the real composed
  HTTP surface and cover indistinguishable authentication failures, server-side
  Tenant derivation, normative admission ordering (including unauthenticated
  oversize failing 401 before 413), non-enumerating foreign/unknown ids,
  concurrent single-owner/single-draft replay across 8 racers, replay
  conflict/in-progress/uncertain never admitting or persisting, and safe
  allowlisted response/audit/telemetry/single-request-log fields.
- `gofmt -l` is clean across the touched files.
- `git diff --check` passed.
