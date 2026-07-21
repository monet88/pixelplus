# GW-068 Disposable Docker Live-Probe Sandbox

## Status

implemented

## Lane

high-risk

## Product Contract

A reproducible container image and disposable localhost-only sandbox start the
Gateway through the SAME production composition entrypoint (`cmd/gateway` from
#44). Direct `go run`/`go test` remain the inner development loop; Docker is a
disposable parity, isolation, and authorized live-probe environment that never
selects production topology and never opens a credential side channel.

## Relevant Product Docs

- `docs/decisions/0009-pure-go-module-seams-and-dependency-budget.md`
- `docs/spec/provider-gateway-implementation-ready-specification.md` (topology deferred)
- `docs/spec/auth-mode-risk-envelope-and-kill-criteria.md` (Grok Web SSO prohibition)
- `docs/spec/provider-account-connection-and-credential-lifecycle.md` (credential custody)

## Acceptance Criteria

- Image construction is reproducible from tracked sources and uses the
  production composition entrypoint from #44, not a sandbox-only main/handler.
- The documented direct-Go loop remains the recommended fast path; Docker is
  reserved for parity, isolation, and authorized live probes.
- The container runs non-root with a read-only root filesystem, localhost-only
  publication, no host network/privileged mode, all capabilities dropped,
  no-new-privileges, bounded CPU/memory/PIDs, explicit readiness, deterministic
  shutdown, and disposable state by default.
- No Docker socket, home directory, `.ref/`, broad repository mount, or
  credential-bearing file is mounted.
- No Provider Credential can enter through CLI, image, Compose, generic `.env`,
  or logs; the only authorized path is Public API -> Vault -> protected Adapter.
- Controlled smoke passes without real Provider secrets and proves startup,
  readiness, HTTP access, and cleanup.
- Grok Web SSO remains unreachable and no production deployment topology is
  selected.
- Security/configuration checks and `git diff --check` are part of verification.

## Design Notes

- `apps/gateway/Dockerfile`: multi-stage, `golang:1.25.5-alpine` build with
  `CGO_ENABLED=0 -trimpath -ldflags=-s -w`, runtime on
  `gcr.io/distroless/static:nonroot` (uid 65532, no shell/package manager).
  Build context is the gateway module only; copies just `go.mod`, `cmd`, and
  `internal`.
- `apps/gateway/.dockerignore`: excludes tests, README, and artifacts from the
  build context.
- `deploy/sandbox/docker-compose.yml` and `deploy/sandbox/sandbox.sh` apply the
  identical hardening profile: `127.0.0.1:8080` publication, `--user
  65532:65532`, `--read-only`, `--cap-drop ALL`, `--security-opt
  no-new-privileges`, single `tmpfs /tmp` (noexec,nosuid,nodev,16m),
  `--pids-limit 128`, `--memory 256m`, `--cpus 1.0`. No `--privileged`, no
  `--network host`, no bind mount, no docker socket.
- The `sandbox.sh probe` smoke asserts `/healthz` and `/readyz` answer and that
  `POST /v1/provider-accounts` fails closed with `401` (the fail-closed
  foundation principal store), proving the spine is wired without provisioning
  or transmitting any secret.

## Validation

| Layer | Expected proof |
| --- | --- |
| Unit | Not applicable; no Go behavior added. |
| Integration | `sandbox.sh smoke` builds, starts, probes readiness, runs the fail-closed HTTP smoke, and tears down. |
| E2E | Not applicable. |
| Platform | Image builds from the module context; container runs non-root/read-only on the Docker host. |
| Release | `bash -n sandbox.sh`, `git diff --check`, and container configuration inspection. |

## Harness Delta

Adds `GW-068` as the disposable sandbox proof row. No Go verify command; the
proof is the container build plus `sandbox.sh smoke`.

## Evidence

- `bash -n deploy/sandbox/sandbox.sh` passed (syntax OK).
- `git diff --check` passed.
- `go -C apps/gateway build ./cmd/gateway` builds the same production entrypoint
  the image uses.
- BLOCKED: `docker build` and `sandbox.sh smoke` were not executed because the
  Docker daemon is unavailable in this environment (Docker Desktop paused;
  `docker info` did not respond). The image and hardened profile are complete
  and ready to build/run where a Docker daemon is reachable.
