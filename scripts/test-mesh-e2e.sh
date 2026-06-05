#!/usr/bin/env bash
# Yaver Mesh data-plane end-to-end test — host orchestrator.
#
# Proves the REAL mesh data plane (TUN creation + wireguard-go handshake +
# netconfig + ICMP over the 100.96.x overlay) on a real Linux kernel, with NO
# cloud box, NO Convex, and NO OAuth — by running two mesh nodes in two network
# namespaces inside one privileged Linux container. On macOS the container uses
# Docker Desktop's LinuxKit VM (a real Linux kernel); on Linux/CI it runs
# natively. Cost: $0.
#
#   ./scripts/test-mesh-e2e.sh
#
# Requires: docker (running), Go toolchain. Builds cmd/meshtest for the
# container arch and runs ci/mesh/e2e-in-container.sh inside nicolaka/netshoot.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
AGENT_DIR="$REPO_ROOT/desktop/agent"
IMAGE="${MESH_TEST_IMAGE:-nicolaka/netshoot}"

if ! docker info >/dev/null 2>&1; then
  echo "ERROR: Docker is not running. Start Docker Desktop (macOS) or dockerd (Linux)." >&2
  echo "  macOS: open -a Docker" >&2
  exit 2
fi

# Match the container's Linux arch (Docker Desktop on Apple Silicon = arm64).
host_arch="$(uname -m)"
case "$host_arch" in
  arm64|aarch64) goarch=arm64 ;;
  x86_64|amd64)  goarch=amd64 ;;
  *) echo "ERROR: unsupported arch $host_arch" >&2; exit 2 ;;
esac

bin="$(mktemp -t meshtest-linux.XXXXXX)"
trap 'rm -f "$bin"' EXIT
echo "Building cmd/meshtest for linux/$goarch …"
( cd "$AGENT_DIR" && GOOS=linux GOARCH="$goarch" CGO_ENABLED=0 go build -o "$bin" ./cmd/meshtest )

echo "Running mesh e2e in a privileged $IMAGE container …"
docker run --rm --privileged \
  -v "$bin:/meshtest:ro" \
  -v "$REPO_ROOT/ci/mesh/e2e-in-container.sh:/run.sh:ro" \
  --entrypoint bash "$IMAGE" /run.sh
