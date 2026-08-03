# Validation

## Proof Strategy

Use stable HTTP operations on the composed Gateway fixture. Controlled Adapter and admission counters prove no replacement render or image-generation admission occurs; lifecycle status alone is insufficient proof.

## Test Plan

| Layer | Cases |
| --- | --- |
| Unit | Existing lifecycle transitions, terminal immutability, lease recovery, and placement-key idempotency. |
| Integration | Durable cancellation/recovery and output placement are covered by application and persistence suites. |
| E2E | Public HTTP creates a job, runs one controlled Adapter call, then retries the same output twice with no additional render call or image-generation admission. |
| Platform | Go build/test on the supported local platform; cross-platform durability checks remain from GW-056. |
| Performance | Not changed. |
| Logs/Audit | Existing safe audit actions; foreign errors omit resource references. |

## Fixtures

- Deterministic Tenant A and Tenant B Client API Keys.
- Controlled same-Tenant Provider Account/capability/routing fixture.
- Counting Render Adapter and operation-aware admission fake.

## Commands

```text
gofmt -l apps/gateway/internal/contracttest/render_jobs_test.go apps/gateway/internal/contracttest/spine_fakes_test.go
go -C apps/gateway test ./internal/contracttest -run 'Test(.*Cancel|.*OutputRetry|.*ForeignRenderJob)' -count=1
go -C apps/gateway build ./...
go -C apps/gateway vet ./...
go -C apps/gateway test ./... -count=1
go -C apps/gateway test -race ./... -count=1
GitNexus detect_changes(scope=all)
```

## Acceptance Evidence

Verified on 2026-07-29:

- Public HTTP regression: storage-cap after a committed render yields `completed` with `pending` output; after capacity recovery, two output retries return the same available `asset_id` with `re_render=false`, one Render Adapter call total, one `create_image_generation` admission, and two `retry_render_job_output` admissions.
- Public HTTP regression: foreign cancel and output-retry requests return non-enumerating `404 resource_not_found`, omit `resource_reference`, and cause neither admission nor render execution.
- Focused contract and application tests, `gofmt`, `go build ./...`, `go vet ./...`, `go test ./... -count=1`, and `go test -race ./... -count=1` passed.
- GitNexus `detect_changes(scope=all)` reports only the expected Gateway application/contract-test scope with low aggregate risk. **Index caveat (not proof of zero blast radius):** `ExecuteJob` is a worker/recovery seam shared across render paths, and the GitNexus index does not fully model runtime goroutine/fencing effects; "no affected execution flows" reflects index coverage, not a completeness claim. Blast radius was instead confirmed manually via the focused cancel/output-retry tests below plus `go build`/`go vet`/`go test`/`go test -race`, which exercise the shared worker path directly.
