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
#   ./sandbox.sh probe      # wait for /readyz then run a controlled HTTP smoke
#   ./sandbox.sh stop       # stop and remove the container (disposable)
#   ./sandbox.sh up         # build + start + probe (then leaves it running)
#   ./sandbox.sh smoke      # build + start + probe + stop (full disposable run)
set -euo pipefail

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
  #   --pids-limit/--memory/--cpus  bounded resources
  # No --privileged, no --network host, no -v bind mount, no docker socket.
  docker run -d \
    --name "${NAME}" \
    --publish "${HOST_ADDR}:${HOST_PORT}:${CONTAINER_PORT}" \
    --env PIXELPLUS_GATEWAY_ADDR="0.0.0.0:${CONTAINER_PORT}" \
    --env PIXELPLUS_GATEWAY_STARTUP_TIMEOUT="10s" \
    --env PIXELPLUS_GATEWAY_SHUTDOWN_TIMEOUT="10s" \
    --user "65532:65532" \
    --read-only \
    --cap-drop ALL \
    --security-opt no-new-privileges \
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
  log "waiting for readiness at ${base}/readyz"
  local ready="" attempt
  for attempt in $(seq 1 60); do
    if curl -fsS -o /dev/null "${base}/readyz" 2>/dev/null; then
      ready="yes"
      break
    fi
    sleep 0.5
  done
  [ -n "${ready}" ] || { docker logs "${NAME}" 2>&1 | tail -n 40; die "gateway did not become ready"; }

  # Controlled, non-secret HTTP smoke through the production composition.
  log "readiness OK; running controlled HTTP smoke (no Provider secrets)"

  # 1) Health and readiness probes answer.
  curl -fsS "${base}/healthz" >/dev/null || die "healthz failed"
  curl -fsS "${base}/readyz" >/dev/null || die "readyz failed"

  # 2) A product operation is reachable and fails CLOSED without a Client API
  #    Key: the fail-closed foundation principal store returns 401. This proves
  #    the /v1 spine is wired without provisioning or transmitting any secret.
  local code
  code="$(curl -s -o /dev/null -w '%{http_code}' \
    -X POST "${base}/v1/provider-accounts" \
    -H 'Idempotency-Key: sandbox-smoke' \
    -H 'Content-Type: application/json' \
    --data '{"provider":"chatgpt","auth_mode":"chatgpt_codex_oauth","label":"smoke"}')"
  [ "${code}" = "401" ] || die "expected 401 authentication_failed from fail-closed spine, got ${code}"

  log "smoke passed: probes answer and /v1 spine is wired and fail-closed (401)"
}

cmd_stop() {
  require_docker
  log "stopping and removing ${NAME} (disposable; no state retained)"
  docker rm -f "${NAME}" >/dev/null 2>&1 || true
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
