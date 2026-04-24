#!/usr/bin/env bash
# provision-machine.sh — Provision a Yaver cloud dev machine on Hetzner.
#
# Called by the Convex webhook when a customer subscribes to CPU/GPU machine.
# Sets up a single Docker container with everything pre-installed:
#   - Yaver agent in multi-user mode
#   - Dev tools (Node.js, Python, Go, Rust, Docker-in-Docker)
#   - GPU stack (Ollama, PersonaPlex) for GPU machines
#   - Managed relay tunnel
#
# Usage:
#   ./scripts/provision-machine.sh --server <ip> --type <cpu|gpu> \
#     --team <team-id> --token <admin-token> [--relay-domain <domain>]
#
# Environment (from .env.test or GitHub Actions secrets):
#   HETZNER_API_TOKEN — for server creation (if --server not provided)
#   CONVEX_SITE_URL   — for Yaver auth
#   RELAY_PASSWORD    — for managed relay

set -euo pipefail

# ── Parse args ──────────────────────────────────────────────────
SERVER_IP=""
MACHINE_TYPE="cpu"
TEAM_ID=""
ADMIN_TOKEN=""
RELAY_DOMAIN=""

while [[ $# -gt 0 ]]; do
  case $1 in
    --server)     SERVER_IP="$2"; shift 2 ;;
    --type)       MACHINE_TYPE="$2"; shift 2 ;;
    --team)       TEAM_ID="$2"; shift 2 ;;
    --token)      ADMIN_TOKEN="$2"; shift 2 ;;
    --relay-domain) RELAY_DOMAIN="$2"; shift 2 ;;
    *) echo "Unknown arg: $1"; exit 1 ;;
  esac
done

if [[ -z "$SERVER_IP" ]]; then
  echo "ERROR: --server <ip> is required"
  exit 1
fi

SSH="ssh -o StrictHostKeyChecking=no root@${SERVER_IP}"

echo "=== Provisioning $MACHINE_TYPE machine at $SERVER_IP ==="

# ── 1. Base system setup ────────────────────────────────────────
echo ">>> Installing base packages..."
$SSH "apt-get update -qq && apt-get install -y -qq \
  curl git docker.io docker-compose tmux htop jq unzip bubblewrap uidmap \
  build-essential pkg-config libssl-dev \
  2>&1 | tail -5"
$SSH "bash -s" <<'SANDBOX'
set -e
mkdir -p /etc/sysctl.d
cat > /etc/sysctl.d/99-yaver-runner-sandbox.conf <<'EOF'
kernel.unprivileged_userns_clone=1
user.max_user_namespaces=1048576
EOF
if [[ -f /proc/sys/kernel/apparmor_restrict_unprivileged_userns ]]; then
  echo 'kernel.apparmor_restrict_unprivileged_userns=0' >> /etc/sysctl.d/99-yaver-runner-sandbox.conf
fi
sysctl --system >/dev/null 2>&1 || true
systemctl enable --now docker || true
SANDBOX

# ── 2. Install dev tools ───────────────────────────────────────
echo ">>> Installing dev tools..."
$SSH 'bash -s' <<'DEVTOOLS'
# Node.js (via nvm)
if ! command -v node &>/dev/null; then
  curl -fsSL https://raw.githubusercontent.com/nvm-sh/nvm/v0.39.7/install.sh | bash
  export NVM_DIR="$HOME/.nvm"
  source "$NVM_DIR/nvm.sh"
  nvm install 20
  npm install -g expo-cli eas-cli yarn pnpm
fi

# Go
if ! command -v go &>/dev/null; then
  curl -fsSL https://go.dev/dl/go1.22.4.linux-amd64.tar.gz | tar -C /usr/local -xz
  echo 'export PATH=$PATH:/usr/local/go/bin' >> /etc/profile.d/golang.sh
fi

# Rust
if ! command -v rustc &>/dev/null; then
  curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y
fi

# Python
if ! command -v python3 &>/dev/null; then
  apt-get install -y -qq python3 python3-pip python3-venv
fi

echo "Dev tools installed"
DEVTOOLS

# ── 3. Install Yaver agent ─────────────────────────────────────
echo ">>> Installing Yaver agent..."
$SSH 'bash -s' <<'YAVER'
# Download latest Yaver CLI
ARCH=$(uname -m)
if [[ "$ARCH" == "aarch64" ]]; then
  YAVER_ARCH="linux-arm64"
else
  YAVER_ARCH="linux-amd64"
fi

# TODO: Download from GitHub releases when available
# For now, build from source
if [[ -d /opt/yaver ]]; then
  cd /opt/yaver && git pull
else
  git clone https://github.com/kivanccakmak/yaver.io.git /opt/yaver
fi
cd /opt/yaver/desktop/agent
export PATH=$PATH:/usr/local/go/bin
go build -o /usr/local/bin/yaver .
echo "Yaver agent installed: $(yaver version 2>/dev/null || echo 'built from source')"
YAVER

# ── 4. GPU-specific setup ──────────────────────────────────────
if [[ "$MACHINE_TYPE" == "gpu" ]]; then
  echo ">>> Setting up GPU stack..."
  $SSH 'bash -s' <<'GPU'
# Install NVIDIA drivers + container toolkit
if ! command -v nvidia-smi &>/dev/null; then
  apt-get install -y -qq nvidia-driver-535 nvidia-container-toolkit
fi

# Ollama
if ! command -v ollama &>/dev/null; then
  curl -fsSL https://ollama.com/install.sh | sh
fi

# Pre-pull Qwen 2.5 Coder 32B
ollama pull qwen2.5-coder:32b &

# PersonaPlex setup
if [[ ! -d /opt/personaplex ]]; then
  mkdir -p /opt/personaplex
  # PersonaPlex is set up via yaver voice setup --provider personaplex
fi

echo "GPU stack ready"
GPU
fi

# ── 5. Configure Yaver for multi-user mode ──────────────────────
echo ">>> Configuring Yaver multi-user mode..."
TEAM_FLAG=""
if [[ -n "$TEAM_ID" ]]; then
  TEAM_FLAG="--team $TEAM_ID"
fi

$SSH "cat > /etc/systemd/system/yaver-agent.service" <<EOF
[Unit]
Description=Yaver Agent (Multi-User)
After=network.target docker.service
Wants=docker.service

[Service]
Type=simple
User=root
Environment=HOME=/root
ExecStart=/usr/local/bin/yaver serve --multi-user $TEAM_FLAG --port 18080 --work-dir /var/yaver/workspaces --debug
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

$SSH "systemctl daemon-reload && systemctl enable yaver-agent && systemctl start yaver-agent"

# ── 6. Managed relay (if domain provided) ──────────────────────
if [[ -n "$RELAY_DOMAIN" ]]; then
  echo ">>> Setting up managed relay at $RELAY_DOMAIN..."
  # Relay setup handled separately by relay provisioning
fi

# ── 7. Health check ────────────────────────────────────────────
echo ">>> Waiting for agent to start..."
sleep 3
HEALTH=$($SSH "curl -sf http://localhost:18080/health || echo 'FAILED'")
if [[ "$HEALTH" == "FAILED" ]]; then
  echo "WARNING: Agent health check failed. Check logs with: ssh root@$SERVER_IP journalctl -u yaver-agent -f"
else
  echo ">>> Agent is running!"
  echo "$HEALTH" | jq . 2>/dev/null || echo "$HEALTH"
fi

echo ""
echo "=== Machine provisioned ==="
echo "  Type:    $MACHINE_TYPE"
echo "  Server:  $SERVER_IP"
echo "  Agent:   http://$SERVER_IP:18080"
echo "  Multi-user: enabled"
if [[ -n "$TEAM_ID" ]]; then
  echo "  Team:    $TEAM_ID"
fi
echo ""
echo "Team members can now:"
echo "  1. Open Yaver app"
echo "  2. The machine appears automatically in their device list"
echo "  3. Tap Connect → authenticate with their own account"
echo "  4. Each user gets isolated workspace + sessions"
