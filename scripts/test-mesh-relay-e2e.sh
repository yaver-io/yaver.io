#!/usr/bin/env bash
# Yaver Mesh RELAY-as-DERP end-to-end test — host orchestrator.
#
# Proves the symmetric-NAT fallback: two agents with NO direct path still tunnel
# over WireGuard by relaying frames through the relay's mesh_relay stream. Runs
# a relay + two NAT'd mesh nodes (in two netns that can't reach each other) in
# one privileged Linux container. No cloud, no Convex, no OAuth. Cost: $0.
#
#   ./scripts/test-mesh-relay-e2e.sh
#
# Requires: docker (running), Go. Cross-compiles cmd/meshtest (agent module) and
# the relay binary (relay module), then runs ci/mesh/e2e-relay-in-container.sh.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${MESH_TEST_IMAGE:-nicolaka/netshoot}"

if ! docker info >/dev/null 2>&1; then
  echo "ERROR: Docker is not running. Start Docker Desktop (macOS) or dockerd (Linux)." >&2
  exit 2
fi

case "$(uname -m)" in
  arm64|aarch64) goarch=arm64 ;;
  x86_64|amd64)  goarch=amd64 ;;
  *) echo "ERROR: unsupported arch $(uname -m)" >&2; exit 2 ;;
esac

meshbin="$(mktemp -t meshtest-linux.XXXXXX)"
relaybin="$(mktemp -t relay-linux.XXXXXX)"
trap 'rm -f "$meshbin" "$relaybin"' EXIT

echo "Building cmd/meshtest + relay for linux/$goarch …"
( cd "$REPO_ROOT/desktop/agent" && GOOS=linux GOARCH="$goarch" CGO_ENABLED=0 go build -o "$meshbin" ./cmd/meshtest )
( cd "$REPO_ROOT/relay"         && GOOS=linux GOARCH="$goarch" CGO_ENABLED=0 go build -o "$relaybin" . )

echo "Running relay-DERP e2e in a privileged $IMAGE container …"
docker run --rm --privileged \
  -v "$meshbin:/meshtest:ro" \
  -v "$relaybin:/relay:ro" \
  -v "$REPO_ROOT/ci/mesh/e2e-relay-in-container.sh:/run.sh:ro" \
  --entrypoint bash "$IMAGE" /run.sh
