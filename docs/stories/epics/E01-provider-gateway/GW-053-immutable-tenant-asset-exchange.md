# GW-053 Exchange Immutable Tenant Assets

## Status

implemented

## Lane

high-risk

## Product Contract

An authenticated Tenant uploads, inspects, and retrieves immutable image Assets
through the stable `/v1` Public API. Create validates decoded media, dimensions,
request size, and atomic per-Tenant storage capacity before committing metadata
or content. Identity, Tenant owner, and kind are immutable. Foreign and unknown
identifiers are non-enumerating. Expired or deleted content is non-retrievable
and releases storage accounting exactly once. Asset bytes, storage
representations, and foreign facts never enter prohibited projections. All of
this runs through the same `composition.New` runtime used by production.

## Relevant Product Docs

- `docs/spec/asset-exchange-authorization-and-retention-lifecycle.md` sections 3-8 and 10
- `docs/spec/credential-vault-and-sensitive-data-lifecycle.md` sections 5.4 and 8.3
- `docs/spec/tenant-ownership-authorization-invariants.md` (`I-ASSET-ISO`, `I-NON-ENUM`)
- `docs/spec/client-api-key-lifecycle-and-admission-controls.md` sections 5.2, 7.5, and 7.7
- `contracts/openapi/pixelplus-public-api-v1.yaml` (frozen `Asset` schema and the
  `createAsset` / `getAsset` / `getAssetContent` operations)

## Acceptance Criteria

- Create validates format, decodability, dimensions, request size, and storage
  capacity before committing metadata or content.
- Asset identity, Tenant owner, and kind are immutable; edits create new output
  Assets (output creation is #14 render-job territory and out of this seam).
- Concurrent creates use atomic committed-plus-reserved byte/count accounting and
  cannot exceed Tenant caps.
- Foreign and unknown ids return the same result before content access, capacity
  mutation, or relationship disclosure.
- Expiry and deletion make content non-retrievable and release storage accounting
  exactly once.
- Asset bytes, raw URLs, storage representations, and foreign facts never enter
  prohibited projections.

## Design Notes

- Layers follow #44/#45 direction: domain <- ports <- application <- transport;
  composition is the only wiring package; adapters host byte-level media
  inspection.
- Create spine order (application): request_id -> A0 authenticate -> A1 scope
  (`assets.write`) -> A2 request-size -> request validation (multipart framing,
  `kind`, `Idempotency-Key`) -> content validation (decode, media type,
  dimensions, checksum) -> replay claim (scoped by kind+checksum fingerprint) ->
  A3-A5 admission -> atomic storage reservation -> content + metadata commit ->
  reservation commit -> replay complete -> one audit/telemetry/request-log
  emission.
- Storage capacity is enforced by an atomic `committed + reserved + candidate`
  check for both byte and object dimensions inside a single accountant
  operation, so concurrent creates near a cap cannot both pass a stale read.
- A failed create after the reservation releases the hold and abandons the fresh
  replay claim exactly once; a lost replay-complete after a durable commit is
  `idempotency_uncertain` and never abandons.
- Retrieval resolves within the principal's Tenant only. Foreign, unknown,
  expired, and deleted ids all return `resource_not_found` (matching the frozen
  `getAssetContent` 404 for "expired asset") with no resource reference, so no
  caller can distinguish them.
- Expiry/deletion release committed accounting exactly once: the first retrieval
  that observes a non-retrievable own Asset performs an idempotent tombstone
  transition and a single committed release; repeats are no-ops.
- Production foundation stores are empty and fail-closed; contract tests inject
  controlled seeded stores/accountant to exercise the spine through real
  composition.
- Public test seam: real `httptest` server around `Runtime.Handler()` driving the
  three frozen Asset HTTP operations.

## Validation

| Layer | Expected proof |
| --- | --- |
| Unit | Architecture import rules; media inspector classification; canonical error tuples. |
| Integration | Six acceptance criteria exercised over HTTP through real composition with controlled ports. |
| E2E | Not applicable; no browser/user workflow. |
| Platform | Module builds and full/race suites pass on Windows. |
| Release | `go vet`, full Go tests, race tests, gofmt. |

## Harness Delta

Adds `GW-053` as the immutable Tenant Asset exchange proof row with the reusable
`go -C apps/gateway test -race ./...` verification command.

## Evidence

- `go -C apps/gateway build ./...`, `go -C apps/gateway vet ./...`,
  `go -C apps/gateway test ./...`, and `go -C apps/gateway test -race ./...`
  passed.
- New contract tests in `internal/contracttest` enter through the real composed
  HTTP surface and cover early content/size/dimension/capacity validation,
  server-side Tenant stamping and immutable kind, atomic concurrent reservation
  under a cap, non-enumerating foreign/unknown/expired/deleted ids, one-time
  release on expiry/deletion, and safe allowlisted response/audit/telemetry/
  single-request-log fields.
- `gofmt -l` is clean across the touched files.
- `git diff --check` passed.
