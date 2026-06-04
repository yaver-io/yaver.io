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
# chromium: web-ghost (chromedp) drives web-UI ERPs headlessly.
# xserver/openbox: minimal X session for the RustDesk client window that the
#   Linux ghost operates (RustDesk blackbox mode — drive the customer's PC where
#   only RustDesk is installed). ffmpeg also gives the recorder x11grab for the
#   temporary operation recordings shown in onboarding.
apt-get install -y git gh jq tmux ffmpeg python3 python3-pip python3-venv curl ca-certificates unzip xz-utils docker.io docker-compose-v2 chromium \
  xserver-xorg xinit openbox x11-xserver-utils

# RustDesk client — for the "blackbox" deployment where the customer installs
# ONLY RustDesk on their Logo PC and this appliance remote-controls it. Pinned
# arm64 .deb; bump RUSTDESK_VER as needed. Non-fatal if the download fails
# (web-ghost + native paths still work).
RUSTDESK_VER="${RUSTDESK_VER:-1.3.7}"
if ! command -v rustdesk >/dev/null 2>&1; then
  ARCH="$(dpkg --print-architecture 2>/dev/null || echo arm64)"
  case "$ARCH" in
    arm64) RD_ASSET="rustdesk-${RUSTDESK_VER}-aarch64.deb" ;;
    amd64) RD_ASSET="rustdesk-${RUSTDESK_VER}-x86_64.deb" ;;
    *)     RD_ASSET="" ;;
  esac
  if [ -n "$RD_ASSET" ]; then
    if curl -fsSL -o "/tmp/${RD_ASSET}" "https://github.com/rustdesk/rustdesk/releases/download/${RUSTDESK_VER}/${RD_ASSET}"; then
      apt-get install -y "/tmp/${RD_ASSET}" || dpkg -i "/tmp/${RD_ASSET}" || echo "[yaver-pi-firstboot] rustdesk install failed (non-fatal)"
      rm -f "/tmp/${RD_ASSET}"
    else
      echo "[yaver-pi-firstboot] rustdesk download failed (non-fatal); install later for blackbox mode"
    fi
  fi
fi

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
