#!/usr/bin/env bash
# Factory/DIY provisioning for Raspberry Pi OS Lite 64-bit.
# Usage: sudo ./install-pi.sh ./yaver-agent-linux-arm64
set -euo pipefail

[ "$(id -u)" -eq 0 ] || { echo "run with sudo" >&2; exit 1; }
[ $# -eq 1 ] || { echo "usage: sudo $0 ./yaver-agent-linux-arm64" >&2; exit 2; }
AGENT="$1"
[ -f "$AGENT" ] || { echo "agent binary not found: $AGENT" >&2; exit 1; }

apt-get update
apt-get install -y --no-install-recommends ca-certificates ffmpeg v4l-utils

if ! id yaver >/dev/null 2>&1; then
  useradd --system --create-home --home-dir /var/lib/yaver --shell /usr/sbin/nologin yaver
fi
usermod -a -G video,plugdev yaver
install -m 0755 "$AGENT" /usr/local/bin/yaver
install -d -o yaver -g yaver -m 0700 /var/lib/yaver/.yaver

install -m 0644 /dev/stdin /etc/systemd/system/yaver-physical-kvm.service <<'UNIT'
[Unit]
Description=Yaver Physical KVM agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=yaver
Group=yaver
SupplementaryGroups=video plugdev
Environment=HOME=/var/lib/yaver
ExecStart=/usr/local/bin/yaver serve
Restart=on-failure
RestartSec=3
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ReadWritePaths=/var/lib/yaver

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable yaver-physical-kvm.service

echo "Pi packages, agent, and boot service are installed."
echo "Next: sudo -u yaver HOME=/var/lib/yaver /usr/local/bin/yaver auth --headless"
echo "Then: systemctl start yaver-physical-kvm.service && yaver kvm doctor"
