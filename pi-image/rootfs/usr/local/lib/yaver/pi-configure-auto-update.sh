#!/usr/bin/env bash
set -euo pipefail

PREFERENCE="${1:-daily}"
SERVICE_PATH="/etc/systemd/system/yaver-pi-auto-update.service"
TIMER_PATH="/etc/systemd/system/yaver-pi-auto-update.timer"

case "$PREFERENCE" in
  off|false|never|disabled)
    if command -v yaver >/dev/null 2>&1; then
      yaver config set auto-update false || true
    fi
    systemctl disable --now yaver-pi-auto-update.timer >/dev/null 2>&1 || true
    rm -f "$TIMER_PATH"
    systemctl daemon-reload
    echo "[yaver-pi-firstboot] auto-update disabled"
    exit 0
    ;;
  weekly)
    ON_CALENDAR="weekly"
    ;;
  daily|true|enabled|"")
    ON_CALENDAR="daily"
    ;;
  *)
    echo "[yaver-pi-firstboot] unknown auto-update preference '$PREFERENCE'; falling back to daily"
    ON_CALENDAR="daily"
    ;;
esac

cat >"$SERVICE_PATH" <<'EOF'
[Unit]
Description=Yaver Pi Auto Update
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/lib/yaver/pi-auto-update.sh
EOF

cat >"$TIMER_PATH" <<EOF
[Unit]
Description=Periodic Yaver Pi Auto Update ($ON_CALENDAR)

[Timer]
OnCalendar=$ON_CALENDAR
RandomizedDelaySec=30m
Persistent=true

[Install]
WantedBy=timers.target
EOF

systemctl daemon-reload
systemctl enable --now yaver-pi-auto-update.timer

if command -v yaver >/dev/null 2>&1; then
  yaver config set auto-update true || true
fi

echo "[yaver-pi-firstboot] auto-update timer configured: $ON_CALENDAR"
