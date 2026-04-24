#!/usr/bin/env bash
#
# deploy-yaver-agent-hetzner.sh — provision a Hetzner box (or any
# Linux server you can SSH into) and run the Yaver agent on it as a
# systemd service. The whole point is the same one solo dev who
# wants their CI to cost $0 *might* also want a tiny always-on box to
# host the runner so their phone can reach it from anywhere — without
# locking themselves into our hosted plan.
#
# Two modes:
#
#   1) Bring your own server. The dev has a $5 Hetzner CX22 (or any
#      Ubuntu 22.04+ box). They run this script with --host <ip>
#      --user root and we install the agent + Chrome + the systemd
#      unit. They keep ssh access. Total cost: ~$5/mo. Yaver gets $0.
#
#   2) Managed Yaver cloud (separate, NOT in this script). When/if we
#      run a hosted offering we'll take a small margin on top of
#      the same Hetzner box price. The dev's choice.
#
# Strict open-source rule: this script must NEVER hardcode any IP,
# hostname, ssh key, or credential. The dev passes them in via flags
# or env vars. The script verifies before running anything destructive.

set -euo pipefail

usage() {
  cat <<'EOF'
Usage: deploy-yaver-agent-hetzner.sh --host <ip-or-hostname> [--user root] [--keyfile ~/.ssh/id_ed25519]

Options:
  --host       SSH host (required, no default for safety)
  --user       SSH user (default: root)
  --keyfile    SSH private key (default: ssh-agent)
  --port       Yaver agent HTTP port (default: 18080)
  --multi-user Enable Yaver multi-user mode on the box
  --team       Restrict multi-user mode to one Yaver team id
  --max-users  Max concurrent multi-user sessions (default: 0 = unlimited)
  --allow-ips  Comma-separated CIDR allowlist for the agent
  --containerize-guests  Run guest tasks in Docker containers
  --no-chrome  Skip Chrome install (use if you only need agent + tasks, not yaver-test-sdk)
  --uninstall  Tear down: stop the systemd service and remove the binary

The script:
  1. Verifies it can SSH in non-interactively.
  2. Installs google-chrome-stable + tmux + curl (apt-get).
  3. Downloads the latest yaver release tarball for linux/amd64 from GitHub.
  4. Drops it into /usr/local/bin/yaver.
  5. Creates a 'yaver' user, home directory, and systemd unit
     (yaver-agent.service) that runs `yaver serve` on boot.
  6. Prints how to point your phone at the new agent (relay URL or
     Tailscale IP).

After this finishes, the dev:
  - Adds the new device's relay URL or LAN IP to their mobile app.
  - Opens the "Local CI" tab and taps "Run all specs".
  - Sees results streaming over the existing Yaver P2P transport.

Cost: $5/mo for the Hetzner box. $0 to Yaver.
EOF
}

HOST=""
USER="root"
KEYFILE=""
PORT="18080"
INSTALL_CHROME="yes"
UNINSTALL="no"
MULTI_USER="no"
TEAM_ID=""
MAX_USERS="0"
ALLOW_IPS=""
CONTAINERIZE_GUESTS="no"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --host)       HOST="$2"; shift 2;;
    --user)       USER="$2"; shift 2;;
    --keyfile)    KEYFILE="$2"; shift 2;;
    --port)       PORT="$2"; shift 2;;
    --multi-user) MULTI_USER="yes"; shift;;
    --team)       TEAM_ID="$2"; shift 2;;
    --max-users)  MAX_USERS="$2"; shift 2;;
    --allow-ips)  ALLOW_IPS="$2"; shift 2;;
    --containerize-guests) CONTAINERIZE_GUESTS="yes"; shift;;
    --no-chrome)  INSTALL_CHROME="no"; shift;;
    --uninstall)  UNINSTALL="yes"; shift;;
    -h|--help)    usage; exit 0;;
    *) echo "unknown flag: $1" >&2; usage; exit 2;;
  esac
done

if [[ -z "$HOST" ]]; then
  echo "error: --host is required (don't hardcode this in any committed file)" >&2
  exit 2
fi

SSH_OPTS=("-o" "StrictHostKeyChecking=accept-new" "-o" "BatchMode=yes" "-o" "ConnectTimeout=10")
if [[ -n "$KEYFILE" ]]; then
  SSH_OPTS+=("-i" "$KEYFILE")
fi

ssh_run() {
  ssh "${SSH_OPTS[@]}" "$USER@$HOST" "$@"
}

echo "=> verifying SSH access to $USER@$HOST..."
if ! ssh_run true; then
  echo "error: cannot SSH non-interactively. Set up keys first." >&2
  exit 3
fi

if [[ "$UNINSTALL" == "yes" ]]; then
  echo "=> tearing down yaver-agent on $HOST"
  ssh_run "set -e; \
    sudo systemctl stop yaver-agent || true; \
    sudo systemctl disable yaver-agent || true; \
    sudo rm -f /etc/systemd/system/yaver-agent.service /usr/local/bin/yaver; \
    sudo userdel -r yaver 2>/dev/null || true; \
    sudo systemctl daemon-reload"
  echo "✓ uninstalled"
  exit 0
fi

echo "=> installing dependencies..."
APT_PACKAGES="tmux curl ca-certificates git jq docker.io bubblewrap uidmap"
if [[ "$INSTALL_CHROME" == "yes" ]]; then
  APT_PACKAGES="$APT_PACKAGES wget gnupg fonts-liberation libasound2t64 libnss3 libgbm1 libxshmfence1 xdg-utils"
fi
ssh_run "sudo apt-get update -y && sudo DEBIAN_FRONTEND=noninteractive apt-get install -y $APT_PACKAGES"
ssh_run "sudo systemctl enable --now docker || true"
ssh_run "set -e; \
  sudo mkdir -p /etc/sysctl.d; \
  sudo tee /etc/sysctl.d/99-yaver-runner-sandbox.conf >/dev/null <<'EOF'\n\
kernel.unprivileged_userns_clone=1\n\
user.max_user_namespaces=1048576\n\
EOF\n\
  if [ -f /proc/sys/kernel/apparmor_restrict_unprivileged_userns ]; then \
    echo kernel.apparmor_restrict_unprivileged_userns=0 | sudo tee -a /etc/sysctl.d/99-yaver-runner-sandbox.conf >/dev/null; \
  fi; \
  sudo sysctl --system >/dev/null 2>&1 || true"

if [[ "$INSTALL_CHROME" == "yes" ]]; then
  echo "=> installing google-chrome-stable..."
  ssh_run "set -e; \
    if ! command -v google-chrome >/dev/null 2>&1; then \
      wget -q https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb -O /tmp/chrome.deb; \
      sudo apt-get install -y /tmp/chrome.deb || sudo dpkg -i /tmp/chrome.deb || true; \
      sudo apt-get install -fy; \
    fi"
fi

echo "=> creating yaver user..."
ssh_run "id yaver >/dev/null 2>&1 || sudo useradd -r -m -d /home/yaver -s /bin/bash yaver"
ssh_run "getent group docker >/dev/null 2>&1 || sudo groupadd docker || true"
ssh_run "sudo usermod -aG docker yaver || true"

echo "=> downloading latest yaver agent for linux/amd64..."
LATEST_TAG="$(curl -fsSL 'https://api.github.com/repos/kivanccakmak/yaver.io/releases?per_page=100' | grep -o '\"tag_name\": *\"v[0-9][^\"]*\"' | head -n1 | sed 's/.*\"\(v[^\"]*\)\"/\1/')"
if [[ -z "$LATEST_TAG" ]]; then
  echo "error: could not determine latest yaver release tag" >&2
  exit 4
fi
LATEST_URL="https://github.com/kivanccakmak/yaver.io/releases/download/${LATEST_TAG}/yaver-${LATEST_TAG}-linux-amd64.tar.gz"
ssh_run "set -e; \
  rm -rf /tmp/yaver-dist && mkdir -p /tmp/yaver-dist && \
  curl -fL $LATEST_URL -o /tmp/yaver.tar.gz && \
  tar -xzf /tmp/yaver.tar.gz -C /tmp/yaver-dist && \
  sudo install -m 0755 /tmp/yaver-dist/yaver-linux-amd64 /usr/local/bin/yaver && \
  rm -rf /tmp/yaver.tar.gz /tmp/yaver-dist"

echo "=> writing systemd unit..."
SERVE_CMD=(/usr/local/bin/yaver serve --debug --port "$PORT" --work-dir /home/yaver)
if [[ "$MULTI_USER" == "yes" ]]; then
  SERVE_CMD+=(--multi-user)
fi
if [[ -n "$TEAM_ID" ]]; then
  SERVE_CMD+=(--team "$TEAM_ID")
fi
if [[ "$MAX_USERS" != "0" ]]; then
  SERVE_CMD+=(--max-users "$MAX_USERS")
fi
if [[ -n "$ALLOW_IPS" ]]; then
  SERVE_CMD+=(--allow-ips "$ALLOW_IPS")
fi
if [[ "$CONTAINERIZE_GUESTS" == "yes" ]]; then
  SERVE_CMD+=(--containerize-guests)
fi
printf -v EXEC_START '%q ' "${SERVE_CMD[@]}"
EXEC_START="${EXEC_START% }"

SERVICE=$(cat <<UNIT
[Unit]
Description=Yaver Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=yaver
Group=yaver
Environment=HOME=/home/yaver
ExecStart=$EXEC_START
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
ProtectSystem=full
ProtectHome=read-only
ReadWritePaths=/home/yaver

[Install]
WantedBy=multi-user.target
UNIT
)
echo "$SERVICE" | ssh_run "sudo tee /etc/systemd/system/yaver-agent.service >/dev/null"

ssh_run "sudo systemctl daemon-reload && sudo systemctl enable yaver-agent && sudo systemctl restart yaver-agent"

sleep 2
ssh_run "systemctl status yaver-agent --no-pager | head -20" || true

echo
echo "✓ yaver agent is running on $HOST:$PORT"
echo
echo "Next steps:"
echo "  1. SSH in as the box owner and run 'yaver auth --headless' or 'yaver auth pair'."
echo "  2. Add your Codex/OpenAI key to the encrypted vault:"
echo "       yaver vault add OPENAI_API_KEY --category api-key"
if [[ "$MULTI_USER" == "yes" ]]; then
echo "  3. This box is in multi-user mode. Pair your own devices first, then let CI/guest users connect with separate tokens."
else
echo "  3. If this box should serve more than one user, redeploy with --multi-user."
fi
echo "  4. Do not use --uninstall on a persistent shared Hetzner box unless you intentionally want to tear it down."
echo "  5. In your Yaver mobile app, add this device — either via the"
echo "     existing relay (recommended) or a Tailscale IP if you use one."
echo "  6. Open the 'Local CI' tab and tap 'Run all specs'."
echo
echo "Cost: \$5-7/mo for the Hetzner box. \$0 to Yaver."
