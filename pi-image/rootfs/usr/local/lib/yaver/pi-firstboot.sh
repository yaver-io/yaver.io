#!/usr/bin/env bash
set -euo pipefail

STATE_DIR="/var/lib/yaver"
DONE_FILE="$STATE_DIR/.firstboot-complete"
LOG_FILE="/var/log/yaver-pi-firstboot.log"
BOOT_CONFIG_FILE="/boot/firmware/yaver-firstboot.env"

mkdir -p "$STATE_DIR" "$(dirname "$LOG_FILE")"
exec > >(tee -a "$LOG_FILE") 2>&1

if [[ -f "$DONE_FILE" ]]; then
  echo "[yaver-pi-firstboot] already completed"
  exit 0
fi

export DEBIAN_FRONTEND=noninteractive
export PATH="/root/.local/bin:$PATH"
YAVER_AUTO_UPDATE="${YAVER_AUTO_UPDATE:-daily}"

if [[ -f "$BOOT_CONFIG_FILE" ]]; then
  # shellcheck disable=SC1090
  source "$BOOT_CONFIG_FILE"
fi

echo "[yaver-pi-firstboot] starting"
echo "[yaver-pi-firstboot] auto-update preference: $YAVER_AUTO_UPDATE"

apt-get update
apt-get install -y git gh jq tmux ffmpeg python3 python3-pip python3-venv curl ca-certificates unzip xz-utils docker.io docker-compose-v2

if ! command -v yaver >/dev/null 2>&1; then
  echo "[yaver-pi-firstboot] yaver binary missing at /usr/local/bin/yaver" >&2
  exit 1
fi

yaver install pi-dev-node

systemctl daemon-reload
usermod -aG docker root || true
systemctl enable docker || true
systemctl start docker || true
/usr/local/lib/yaver/pi-configure-auto-update.sh "$YAVER_AUTO_UPDATE"
systemctl enable yaver-agent.service
systemctl restart yaver-agent.service

cat >/etc/motd <<'EOF'
Yaver Pi dev-node is bootstrapping.

Useful commands:
  journalctl -u yaver-pi-firstboot.service -f
  journalctl -u yaver-agent.service -f
  systemctl status yaver-pi-auto-update.timer
  yaver install backend-dev
  yaver install tailscale
  yaver install cloudflared
  yaver install hybrid
EOF

date -u +"%Y-%m-%dT%H:%M:%SZ" > "$DONE_FILE"
echo "[yaver-pi-firstboot] complete"
