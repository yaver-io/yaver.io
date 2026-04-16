#!/usr/bin/env bash
set -euo pipefail

# Deploy yaver-relay to a Hetzner VPS
#
# Usage:
#   ./deploy/up.sh <server-ip>                    # Binary deploy (default)
#   ./deploy/up.sh <server-ip> --docker           # Docker deploy
#   ./deploy/up.sh <server-ip> --build-only       # Just build locally
#
# Prerequisites:
#   - SSH access to the server (root or sudo)
#   - For binary: Go 1.22+ locally
#   - For docker: Docker on the server

SERVER="${1:?Usage: $0 <server-ip> [--docker|--build-only]}"
MODE="${2:---binary}"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
RELAY_DIR="$(dirname "$SCRIPT_DIR")"
REPO_URL="https://github.com/kivanccakmak/yaver.io.git"

detect_remote_goarch() {
  local machine
  machine="$(ssh "root@${SERVER}" 'uname -m')"
  case "$machine" in
    x86_64|amd64) echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *)
      echo "Unsupported remote architecture: $machine" >&2
      return 1
      ;;
  esac
}

case "$MODE" in
  --docker)
    echo "=== Docker deploy to $SERVER ==="
    echo ""
    echo "  Cloning relay/ directory only (sparse checkout)..."

    ssh "root@${SERVER}" bash -s <<REMOTE
set -euo pipefail

# Install Docker if missing
if ! command -v docker &>/dev/null; then
    echo "  Installing Docker..."
    curl -fsSL https://get.docker.com | sh
    systemctl enable --now docker
fi

# Sparse checkout — only get relay/ directory
DEPLOY_DIR="/opt/yaver-relay"
rm -rf "\$DEPLOY_DIR"
mkdir -p "\$DEPLOY_DIR"
cd "\$DEPLOY_DIR"

git init
git remote add origin ${REPO_URL}
git sparse-checkout init
git sparse-checkout set relay
git pull origin main

cd relay

# Build and start with Docker Compose
if command -v docker-compose &>/dev/null; then
    docker-compose up -d --build
elif docker compose version &>/dev/null 2>&1; then
    docker compose up -d --build
else
    docker build -t yaver-relay .
    docker rm -f yaver-relay 2>/dev/null || true
    docker run -d --name yaver-relay \
        --restart unless-stopped \
        -p 4433:4433/udp \
        -p 8443:8443/tcp \
        yaver-relay
fi

# Open firewall ports
if command -v ufw &>/dev/null && ufw status | grep -q "active"; then
    ufw allow 4433/udp comment "yaver-relay QUIC" 2>/dev/null || true
    ufw allow 8443/tcp comment "yaver-relay HTTP" 2>/dev/null || true
fi

echo ""
echo "=== Relay running (Docker) ==="
echo "  Health: curl http://localhost:8443/health"
docker ps --filter name=yaver-relay --format "table {{.Status}}\t{{.Ports}}"
REMOTE
    ;;

  --build-only)
    TARGET_GOARCH="${TARGET_GOARCH:-amd64}"
    echo "=== Building yaver-relay for linux/${TARGET_GOARCH} ==="
    cd "$RELAY_DIR"
    GOOS=linux GOARCH="${TARGET_GOARCH}" CGO_ENABLED=0 go build -ldflags="-s -w" -o "yaver-relay-linux-${TARGET_GOARCH}" .
    echo "  Built: yaver-relay-linux-${TARGET_GOARCH} ($(du -h "yaver-relay-linux-${TARGET_GOARCH}" | cut -f1))"
    ;;

  --binary|*)
    echo "=== Binary deploy to $SERVER ==="
    cd "$RELAY_DIR"
    TARGET_GOARCH="$(detect_remote_goarch)"

    echo "  Building for linux/${TARGET_GOARCH}..."
    GOOS=linux GOARCH="${TARGET_GOARCH}" CGO_ENABLED=0 go build -ldflags="-s -w" -o "yaver-relay-linux-${TARGET_GOARCH}" .
    echo "  Built: $(du -h "yaver-relay-linux-${TARGET_GOARCH}" | cut -f1)"

    echo "  Copying binary..."
    scp "yaver-relay-linux-${TARGET_GOARCH}" "root@${SERVER}:/usr/local/bin/yaver-relay"

    echo "  Copying systemd unit..."
    scp deploy/yaver-relay.service "root@${SERVER}:/etc/systemd/system/yaver-relay.service"

    echo "  Starting service..."
    ssh "root@${SERVER}" bash -s <<'REMOTE'
chmod +x /usr/local/bin/yaver-relay
systemctl daemon-reload
systemctl enable yaver-relay
systemctl restart yaver-relay
sleep 2

# Open firewall ports
if command -v ufw &>/dev/null && ufw status | grep -q "active"; then
    ufw allow 4433/udp comment "yaver-relay QUIC" 2>/dev/null || true
    ufw allow 8443/tcp comment "yaver-relay HTTP" 2>/dev/null || true
fi

echo ""
echo "=== Service status ==="
systemctl status yaver-relay --no-pager -l || true
echo ""
echo "=== Relay running ==="
echo "  Logs:    journalctl -u yaver-relay -f"
echo "  Status:  systemctl status yaver-relay"
echo "  Stop:    systemctl stop yaver-relay"
echo "  Health:  curl http://localhost:8443/health"
REMOTE

    rm -f "yaver-relay-linux-${TARGET_GOARCH}"
    ;;
esac

echo ""
echo "=== Done ==="
echo ""
echo "Connect your agent:"
echo "  yaver serve --relay=${SERVER}:4433"
echo ""
echo "Mobile URL pattern:"
echo "  http://${SERVER}:8443/d/<deviceId>/tasks"
