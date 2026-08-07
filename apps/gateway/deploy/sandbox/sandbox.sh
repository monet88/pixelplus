#!/usr/bin/env bash
# Disposable Docker live-probe sandbox controller for the Pure-Go Provider
# Gateway (#68). This is NOT the inner development loop: `go run` / `go test`
# remain the fast path. Use this only for runtime parity, isolation, and
# authorized live-probe sessions.
#
# It starts the SAME production composition entrypoint built by
# apps/gateway/Dockerfile (cmd/gateway from #44) with a hardened, disposable
# security profile:
#   - Published port bound to host loopback (127.0.0.1) only; never host network.
#   - Non-root user, read-only root filesystem, ALL capabilities dropped.
#   - no-new-privileges; bounded CPU, memory, and PIDs.
#   - No Docker socket, home, repository-wide, or `.ref/` mount.
#   - No Provider Credential via CLI, image, Compose, generic .env, or log; the
#     only authorized credential path is Public API -> Vault -> Adapter.
#
# Usage:
#   ./sandbox.sh build     # reproducible image build from tracked sources
#   ./sandbox.sh start      # start the hardened, disposable container
#   ./sandbox.sh probe      # wait for /healthz then run a controlled HTTP smoke
#   ./sandbox.sh stop       # stop and remove the container (disposable)
#   ./sandbox.sh up         # build + start + probe (then leaves it running)
#   ./sandbox.sh smoke      # build + start + probe + stop (full disposable run)
set -euo pipefail

# Git Bash / MSYS on Windows rewrites any argument that looks like a Unix path
# into a Windows one before the process sees it, so
# `--env PROVIDER_ACCOUNT_STORE_PATH=/var/lib/pixelplus/...` reaches the
# container as `C:/Program Files/Git/var/lib/...`. The gateway then tries to
# `mkdir C:` on a read-only root filesystem and startup recovery fails — a
# platform artifact that reads as a real container failure.
#
# The exclusion is deliberately selective, not `*`: the build context path DOES
# need converting (`/f/CodeBase/...` -> `F:\CodeBase\...`), so a blanket
# exclusion breaks `docker build` instead. Only the container-side paths are
# exempted. No-op on macOS, Linux and CI, where MSYS is not involved.
export MSYS2_ARG_CONV_EXCL="PROVIDER_ACCOUNT_STORE_PATH=;PIXELPLUS_GATEWAY_ADDR=;/var/lib;/tmp"

IMAGE="pixelplus/gateway-sandbox:local"
NAME="pixelplus-gateway-sandbox"
HOST_ADDR="127.0.0.1"
HOST_PORT="8080"
CONTAINER_PORT="8080"

# The build context is the gateway module directory only. The repo root,
# `.ref/`, secrets/, credentials/, and auths/ are outside this context and
# cannot be copied into any image layer.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODULE_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"

log() { printf '[sandbox] %s\n' "$*"; }
die() { printf '[sandbox] ERROR: %s\n' "$*" >&2; exit 1; }

require_docker() {
  command -v docker >/dev/null 2>&1 || die "docker is not installed or not on PATH"
  docker info >/dev/null 2>&1 || die "docker daemon is not reachable (is Docker running?)"
}

cmd_build() {
  require_docker
  log "building ${IMAGE} from tracked module sources at ${MODULE_DIR}"
  docker build --pull -t "${IMAGE}" -f "${MODULE_DIR}/Dockerfile" "${MODULE_DIR}"
}

cmd_start() {
  require_docker
  # Remove any prior disposable instance first so start is reproducible.
  docker rm -f "${NAME}" >/dev/null 2>&1 || true
  log "starting ${NAME} (loopback ${HOST_ADDR}:${HOST_PORT}, non-root, read-only, cap-drop ALL)"
  # Hardening flags mirror docker-compose.yml and are the authoritative profile:
  #   --publish 127.0.0.1:...   loopback-only publication (no host network)
  #   --user 65532:65532        non-root
  #   --read-only               read-only root filesystem
  #   --cap-drop ALL            drop every Linux capability
  #   --security-opt no-new-privileges  block privilege escalation
  #   --tmpfs /tmp              single narrow, non-exec, size-bounded writable dir
  #   --volume named state      durable /var/lib/pixelplus (survives restart)
  #   --pids-limit/--memory/--cpus  bounded resources
  # No --privileged, no --network host, no host-path bind mount, no docker socket.
  docker volume create pixelplus-gateway-state >/dev/null 2>&1 || true
  docker run -d \
    --name "${NAME}" \
    --publish "${HOST_ADDR}:${HOST_PORT}:${CONTAINER_PORT}" \
    --env PIXELPLUS_GATEWAY_ADDR="0.0.0.0:${CONTAINER_PORT}" \
    --env PIXELPLUS_GATEWAY_STARTUP_TIMEOUT="10s" \
    --env PIXELPLUS_GATEWAY_SHUTDOWN_TIMEOUT="10s" \
    --env PROVIDER_ACCOUNT_STORE_PATH="/var/lib/pixelplus/provider-accounts.json" \
    --user "65532:65532" \
    --read-only \
    --cap-drop ALL \
    --security-opt no-new-privileges \
    --volume pixelplus-gateway-state:/var/lib/pixelplus \
    --tmpfs /tmp:rw,noexec,nosuid,nodev,size=16m \
    --pids-limit 128 \
    --memory 256m \
    --memory-swap 256m \
    --cpus 1.0 \
    --stop-timeout 15 \
    --restart no \
    "${IMAGE}" >/dev/null
  log "started; container id: $(docker inspect -f '{{.Id}}' "${NAME}" | cut -c1-12)"
}

cmd_probe() {
  require_docker
  local base="http://${HOST_ADDR}:${HOST_PORT}"
  # Wait on /healthz (liveness), not /readyz. Since the render durability gate
  # (ADR 0009 / P1-C in internal/composition/runtime.go), a production
  # composition without durable render ports, a credential authorizer and a
  # usable digester keeps readiness CLOSED on purpose. The sandbox injects no
  # such production dependencies, so /readyz 503 is the specified fail-closed
  # behaviour, not a startup failure — waiting on it can only ever time out.
  log "waiting for liveness at ${base}/healthz"
  local live="" attempt
  for attempt in $(seq 1 60); do
    if curl -fsS -o /dev/null "${base}/healthz" 2>/dev/null; then
      live="yes"
      break
    fi
    sleep 0.5
  done
  [ -n "${live}" ] || { docker logs "${NAME}" 2>&1 | tail -n 40; die "gateway did not become live"; }

  # Controlled, non-secret HTTP smoke through the production composition.
  log "liveness OK; running controlled HTTP smoke (no Provider secrets)"

  local code

  # 1) Readiness answers, and answers CLOSED. Asserting the exact 503 keeps this
  #    a real check: a future composition that opened readiness without the
  #    durable render ports would be a regression this smoke must catch, and a
  #    mere "responds" assertion would absorb it.
  code="$(curl -s -o /dev/null -w '%{http_code}' "${base}/readyz")"
  [ "${code}" = "503" ] || die "expected 503 fail-closed readiness without durable render ports, got ${code}"

  # 2) A product operation is reachable and fails CLOSED without a Client API
  #    Key: the fail-closed foundation principal store returns 401. This proves
  #    the /v1 spine is wired without provisioning or transmitting any secret.
  code="$(curl -s -o /dev/null -w '%{http_code}' \
    -X POST "${base}/v1/provider-accounts" \
    -H 'Idempotency-Key: sandbox-smoke' \
    -H 'Content-Type: application/json' \
    --data '{"provider":"chatgpt","auth_mode":"chatgpt_codex_oauth","label":"smoke"}')"
  [ "${code}" = "401" ] || die "expected 401 authentication_failed from fail-closed spine, got ${code}"

  log "smoke passed: live, readiness fail-closed (503), /v1 spine wired and fail-closed (401)"
}

cmd_stop() {
  require_docker
  log "stopping and removing ${NAME} (disposable; no state retained)"
  # Graceful, deterministic shutdown: `docker stop` sends SIGTERM and honors the
  # container's --stop-timeout 15 grace window so the gateway signal handlers run
  # their ordered shutdown before removal. `docker rm -f` would send an immediate
  # SIGKILL and skip that ordered path (#68 deterministic shutdown).
  docker stop "${NAME}" >/dev/null 2>&1 || true
  docker rm "${NAME}" >/dev/null 2>&1 || true
}

cmd_up() {
  cmd_build
  cmd_start
  cmd_probe
}

cmd_smoke() {
  cmd_build
  cmd_start
  # Ensure teardown even if the probe fails.
  trap cmd_stop EXIT
  cmd_probe
  log "full disposable smoke complete"
}

main() {
  local action="${1:-}"
  case "${action}" in
    build) cmd_build ;;
    start) cmd_start ;;
    probe) cmd_probe ;;
    stop)  cmd_stop ;;
    up)    cmd_up ;;
    smoke) cmd_smoke ;;
    *) die "usage: $0 {build|start|probe|stop|up|smoke}" ;;
  esac
}

main "$@"
