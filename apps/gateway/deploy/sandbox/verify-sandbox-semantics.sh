#!/usr/bin/env bash
# Sandbox state semantics verification for #125.
#
# Proves:
#   1. Ephemeral stop removes the named volume — a marker written in run N does
#      not survive into run N+1.
#   2. Persistent stop (--keep-state) retains the volume — the marker survives.
#   3. Negative control: a marker left without the opt-in makes the ephemeral
#      test fail.
#
# Run from the repository root:
#   bash apps/gateway/deploy/sandbox/verify-sandbox-semantics.sh
set -euo pipefail

# Same Git Bash / MSYS path-rewriting guard as sandbox.sh: container-side paths
# must reach docker unconverted. See read_marker for why a silent conversion
# here would be worse than a crash — it would make the central assertion pass
# for the wrong reason. No-op on macOS, Linux and CI.
export MSYS2_ARG_CONV_EXCL="/data;/var/lib;/tmp"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SANDBOX="${SCRIPT_DIR}/sandbox.sh"
MARKER_FILE="sandbox-ephemeral-test-marker"
MARKER_VALUE="ephemeral-test-$(date +%s)"
VOLUME="pixelplus-gateway-state"
HELPER_IMAGE="alpine:3.20@sha256:c64c687cbea9300178b30c95835354e34c4e4febc4badfe27102879de0483b5e"

pass()  { printf '  PASS %s\n' "$*"; }
fail() { printf '  FAIL %s\n' "$*" >&2; exit 1; }

require_docker() {
  command -v docker >/dev/null 2>&1 || { echo "SKIP: docker not found"; exit 0; }
  docker info >/dev/null 2>&1 || { echo "SKIP: docker daemon not reachable"; exit 0; }
}

# -- helpers that operate on the named volume, not the container ------------

write_marker() {
  docker run --rm -v "${VOLUME}:/data" "${HELPER_IMAGE}" \
    sh -c "echo '${MARKER_VALUE}' > /data/${MARKER_FILE}" >/dev/null 2>&1
}

read_marker() {
  # The path stays INSIDE the quoted `sh -c` string rather than being a bare
  # argv element. Git Bash / MSYS rewrites a lone `/data/...` argument into
  # `C:/Program Files/Git/data/...` before docker sees it, so a bare
  # `cat /data/marker` reads a nonexistent host path and returns empty — which
  # would make the ephemeral assertion ("marker absent") pass unconditionally,
  # on a platform artifact rather than on the behaviour under test.
  docker run --rm -v "${VOLUME}:/data" "${HELPER_IMAGE}" \
    sh -c "cat /data/${MARKER_FILE} 2>/dev/null || true"
}

# -- tests ------------------------------------------------------------------

test_ephemeral_stop_removes_volume() {
  echo "--- Ephemeral: marker must NOT survive stop ---"

  # Clean slate
  docker rm -f pixelplus-gateway-sandbox >/dev/null 2>&1 || true
  docker volume rm -f "${VOLUME}" >/dev/null 2>&1 || true

  # Build and start (creates the volume)
  bash "${SANDBOX}" build >/dev/null 2>&1 || fail "build failed"
  bash "${SANDBOX}" start >/dev/null 2>&1 || fail "start failed"

  # Write a marker into the named volume
  write_marker
  local written
  written="$(read_marker)"
  [ "${written}" = "${MARKER_VALUE}" ] || fail "marker write failed: expected '${MARKER_VALUE}', got '${written}'"
  pass "marker written to volume before stop"

  # Ephemeral stop (default — no --keep-state)
  bash "${SANDBOX}" stop >/dev/null 2>&1 || fail "stop failed"

  # Volume must be gone
  if docker volume ls --format '{{.Name}}' | grep -qx "${VOLUME}"; then
    fail "volume ${VOLUME} still exists after ephemeral stop"
  fi
  pass "volume removed after ephemeral stop"

  # Start again (creates fresh volume)
  bash "${SANDBOX}" start >/dev/null 2>&1 || fail "second start failed"

  # Marker must be absent from fresh volume
  local after
  after="$(read_marker)"
  if [ -n "${after}" ]; then
    fail "marker survived ephemeral stop: got '${after}'"
  fi
  pass "marker absent after ephemeral restart"

  # Clean up
  bash "${SANDBOX}" stop >/dev/null 2>&1 || true
  echo "  Ephemeral test: PASSED"
}

test_persistent_stop_retains_volume() {
  echo "--- Persistent: marker MUST survive --keep-state stop ---"

  # Clean slate
  docker rm -f pixelplus-gateway-sandbox >/dev/null 2>&1 || true
  docker volume rm -f "${VOLUME}" >/dev/null 2>&1 || true

  # Build and start
  bash "${SANDBOX}" build >/dev/null 2>&1 || fail "build failed"
  bash "${SANDBOX}" start >/dev/null 2>&1 || fail "start failed"

  # Write a marker
  write_marker
  local written
  written="$(read_marker)"
  [ "${written}" = "${MARKER_VALUE}" ] || fail "marker write failed: expected '${MARKER_VALUE}', got '${written}'"
  pass "marker written to volume before persistent stop"

  # Persistent stop (--keep-state)
  bash "${SANDBOX}" stop --keep-state >/dev/null 2>&1 || fail "stop --keep-state failed"

  # Volume must still exist
  if ! docker volume ls --format '{{.Name}}' | grep -qx "${VOLUME}"; then
    fail "volume ${VOLUME} missing after persistent stop"
  fi
  pass "volume retained after persistent stop"

  # Start again — reuses the persistent volume
  bash "${SANDBOX}" start >/dev/null 2>&1 || fail "second start failed"

  # Marker MUST survive
  local after
  after="$(read_marker)"
  if [ "${after}" != "${MARKER_VALUE}" ]; then
    fail "marker lost after persistent restart: expected '${MARKER_VALUE}', got '${after}'"
  fi
  pass "marker survived persistent restart"

  # Clean up
  bash "${SANDBOX}" stop >/dev/null 2>&1 || true
  echo "  Persistent test: PASSED"
}

test_negative_control() {
  echo "--- Negative control: the ephemeral assertion must FAIL on retained state ---"

  # A test that only ever passes proves nothing. Test 1 asserts the marker is
  # absent after an ephemeral stop; this control proves that assertion actually
  # discriminates, by running it against a teardown that deliberately retains
  # state — which is precisely the #125 bug (`stop` kept the volume while
  # logging "no state retained"). `stop --keep-state` reproduces that behaviour
  # exactly, so it stands in for the pre-fix code without editing the script
  # and reverting. If the marker-absence check still reported PASS here, it
  # would be vacuous and every other result in this file would be worthless.

  # Clean slate
  docker rm -f pixelplus-gateway-sandbox >/dev/null 2>&1 || true
  docker volume rm -f "${VOLUME}" >/dev/null 2>&1 || true

  bash "${SANDBOX}" build >/dev/null 2>&1 || fail "build failed"
  bash "${SANDBOX}" start >/dev/null 2>&1 || fail "start failed"

  write_marker
  local planted
  planted="$(read_marker)"
  [ "${planted}" = "${MARKER_VALUE}" ] || fail "marker plant failed: expected '${MARKER_VALUE}', got '${planted}'"
  pass "marker written before the deliberately-broken teardown"

  # The injected defect: teardown that retains state, i.e. the pre-fix `stop`.
  bash "${SANDBOX}" stop --keep-state >/dev/null 2>&1 || fail "stop --keep-state failed"
  bash "${SANDBOX}" start >/dev/null 2>&1 || fail "restart failed"

  # Now apply test 1's assertion verbatim. It MUST report a violation.
  local after
  after="$(read_marker)"
  if [ -z "${after}" ]; then
    fail "NEGATIVE CONTROL BROKEN: state was retained, yet the marker-absence assertion saw no marker. The ephemeral test cannot detect leaked state and its PASS is vacuous."
  fi
  pass "assertion correctly observed the retained marker ('${after}') — the ephemeral gate has teeth"

  # Revert the injected defect: return to the ephemeral default and confirm the
  # same assertion now reports clean, so this control leaves no state behind.
  bash "${SANDBOX}" stop >/dev/null 2>&1 || fail "reverting stop failed"
  if docker volume ls --format '{{.Name}}' | grep -qx "${VOLUME}"; then
    fail "volume ${VOLUME} survived the reverting ephemeral stop"
  fi
  pass "defect reverted: ephemeral stop removed the volume"
  echo "  Negative control: PASSED"
}

# -- main -------------------------------------------------------------------

echo "=== Sandbox State Semantics Verification (#125) ==="
echo ""

require_docker

test_ephemeral_stop_removes_volume
echo ""
test_persistent_stop_retains_volume
echo ""
test_negative_control

echo ""
echo "=== All sandbox state semantics tests PASSED ==="
