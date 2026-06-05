#!/usr/bin/env bash
# Relay HTTP-over-QUIC tunnel end-to-end test — host orchestrator.
# Proves: external HTTP client -> relay /d/<id> -> QUIC tunnel -> agent -> body.
# Runs a relay + a meshtest relay-http agent + curl in one Linux container.
# No cloud, no Convex, no OAuth, no privilege. Cost: $0.
#
#   ./scripts/test-relay-tunnel-e2e.sh
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${MESH_TEST_IMAGE:-nicolaka/netshoot}"

if ! docker info >/dev/null 2>&1; then
  echo "ERROR: Docker is not running." >&2
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

echo "Running relay HTTP-tunnel e2e in a $IMAGE container …"
docker run --rm \
  -v "$meshbin:/meshtest:ro" \
  -v "$relaybin:/relay:ro" \
  -v "$REPO_ROOT/ci/relay/http-tunnel-in-container.sh:/run.sh:ro" \
  --entrypoint bash "$IMAGE" /run.sh
