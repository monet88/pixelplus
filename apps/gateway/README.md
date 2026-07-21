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

## Provider Account request spine

Issue #45 adds the protected Public API request spine and proves it through the
three Provider Account draft operations under the stable `/v1` base:

| Method | Path | Scope | Idempotency-Key |
| --- | --- | --- | --- |
| `POST` | `/v1/provider-accounts` | `accounts.manage` | required |
| `GET` | `/v1/provider-accounts` | `accounts.read` | not applicable |
| `GET` | `/v1/provider-accounts/{provider_account_id}` | `accounts.read` | not applicable |

The spine enforces the normative admission order before any durable draft side
effect: authenticate (A0) -> scope and same-Tenant ownership on named ids (A1)
-> request-size (A2) -> request validation -> replay claim -> rate/concurrency/
quota admission (A3-A5) -> accept. The Security Principal derives Tenant and
Client API Key identity server-side; a client-supplied `tenant_id` is rejected.
Foreign, unknown, and deleted identifiers return the same non-enumerating
`resource_not_found`. The production foundation `PrincipalStore` provisions no
Client API Keys yet, so the production process authenticates nothing and every
key returns `authentication_failed` (401). Contract tests inject controlled
ports through the same production composition constructor.

## Disposable Docker live-probe sandbox

Issue #68 adds a disposable Docker sandbox for runtime parity, isolation, and
authorized live probes. It is **not** the inner development loop: direct
`go run` / `go test` above remain the recommended fast path. Build and run
Docker only when you need parity, isolation, a startup/readiness smoke, or an
authorized live probe.

The image builds and runs the **same** production composition entrypoint
(`cmd/gateway` from #44); there is no sandbox-only main, handler, or runtime
path. Artifacts live under [`deploy/sandbox/`](deploy/sandbox) plus the module
[`Dockerfile`](Dockerfile) and [`.dockerignore`](.dockerignore).

```bash
# From apps/gateway/deploy/sandbox (requires a running Docker daemon):
./sandbox.sh build   # reproducible image build from tracked module sources
./sandbox.sh start   # start the hardened, disposable container
./sandbox.sh probe   # wait for /readyz, then run the controlled HTTP smoke
./sandbox.sh stop    # stop and remove the container (no state retained)
./sandbox.sh smoke   # build + start + probe + stop in one disposable run
```

Compose is also provided for the same profile:

```bash
docker compose -f deploy/sandbox/docker-compose.yml up --build -d
docker compose -f deploy/sandbox/docker-compose.yml down
```

Security envelope (enforced by both the script flags and the Compose file):

- Published port bound to host loopback `127.0.0.1` only; no host networking.
- Non-root user (uid 65532), read-only root filesystem, all Linux capabilities
  dropped, `no-new-privileges`, and bounded CPU/memory/PIDs.
- A single narrow, non-executable, size-bounded `tmpfs` at `/tmp`; no host bind
  mount, no Docker socket, no user home, no repository-wide or `.ref/` mount.
- No Provider Credential enters through CLI, image layer, Compose, generic
  `.env`, or logs. The only authorized credential path is Public API -> Vault
  -> protected Adapter, which does not exist in this slice, so no credential can
  enter the sandbox. Grok Web SSO remains prohibited and unreachable.

The controlled smoke uses no Provider secrets: it asserts the probes answer and
that `POST /v1/provider-accounts` fails closed with `401` from the foundation
principal store, proving the `/v1` spine is wired without provisioning secrets.
