#!/usr/bin/env bash
# Talos-IoT machine-hijack end-to-end test — host orchestrator.
#
# Proves the Yaver machine engine's full edge flow against a live Modbus device,
# with NO hardware and NO cloud: a Modbus-TCP emulator (the PLC) + an edge
# harness (the Pi agent) that absorbs the register map, observes the live
# counter, writes a setpoint verified by read-back, and syncs the schematic to a
# mock Talos commander. One Linux container, no privilege. Cost: $0.
#
#   ./scripts/test-machine-e2e.sh
#
# Like an RPi on the factory floor: cross-compile for linux/arm64 by default on
# Apple Silicon (set MESH_TEST_IMAGE / arch via the host).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${MESH_TEST_IMAGE:-nicolaka/netshoot}"

if ! docker info >/dev/null 2>&1; then
  echo "ERROR: Docker is not running." >&2
  exit 2
fi
case "$(uname -m)" in
  arm64|aarch64) goarch=arm64 ;;   # RPi-like
  x86_64|amd64)  goarch=amd64 ;;
  *) echo "ERROR: unsupported arch $(uname -m)" >&2; exit 2 ;;
esac

emu="$(mktemp -t modbus-emu.XXXXXX)"
edge="$(mktemp -t machinetest.XXXXXX)"
trap 'rm -f "$emu" "$edge"' EXIT

echo "Building modbus-emu + machinetest for linux/$goarch …"
( cd "$REPO_ROOT/desktop/agent" && GOOS=linux GOARCH="$goarch" CGO_ENABLED=0 go build -o "$emu"  ./cmd/modbus-emu )
( cd "$REPO_ROOT/desktop/agent" && GOOS=linux GOARCH="$goarch" CGO_ENABLED=0 go build -o "$edge" ./cmd/machinetest )

echo "Running Talos-IoT machine e2e in a $IMAGE container …"
docker run --rm \
  -v "$emu:/modbus-emu:ro" \
  -v "$edge:/machinetest:ro" \
  -v "$REPO_ROOT/ci/machine/e2e-in-container.sh:/run.sh:ro" \
  --entrypoint bash "$IMAGE" /run.sh
