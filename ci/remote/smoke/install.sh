#!/usr/bin/env bash
# Install the Yaver smoke-check systemd units on the current host.
#
# Run as root on the Hetzner test box (or any Linux box you want to
# watch for the "invalid relay password" regression). Idempotent —
# safe to rerun.
#
#   sudo bash ci/remote/smoke/install.sh
#
# What this leaves on the box:
#   /usr/local/libexec/yaver-smoke/relay-password.sh   (the check)
#   /etc/systemd/system/yaver-smoke-relay-password.{service,timer}
#   /etc/yaver-smoke.env                                (env overrides)
#
# To tail results:
#   journalctl -u yaver-smoke-relay-password.service -f
#   systemctl list-timers yaver-smoke-relay-password.timer
#
# To uninstall:
#   sudo bash ci/remote/smoke/install.sh --uninstall

set -euo pipefail

if [ "${EUID:-$(id -u)}" -ne 0 ]; then
  echo "install.sh: must run as root" >&2
  exit 1
fi

ACTION="${1:-install}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIBEXEC=/usr/local/libexec/yaver-smoke
SYSTEMD_DIR=/etc/systemd/system
ENV_FILE=/etc/yaver-smoke.env

case "$ACTION" in
  install)
    install -d -m 0755 "$LIBEXEC"
    install -m 0755 "$SCRIPT_DIR/relay-password.sh" "$LIBEXEC/relay-password.sh"
    install -m 0644 "$SCRIPT_DIR/systemd/yaver-smoke-relay-password.service" \
      "$SYSTEMD_DIR/yaver-smoke-relay-password.service"
    install -m 0644 "$SCRIPT_DIR/systemd/yaver-smoke-relay-password.timer" \
      "$SYSTEMD_DIR/yaver-smoke-relay-password.timer"
    if [ ! -f "$ENV_FILE" ]; then
      cat > "$ENV_FILE" <<'EOF'
# Yaver smoke-check environment overrides. Nothing secret here —
# everything the smoke script needs is either public or created
# fresh per run. Values below are the defaults; uncomment to override.
#
# CONVEX_URL=https://perceptive-minnow-557.eu-west-1.convex.site
# SMOKE_TIMEOUT=10
EOF
      chmod 0644 "$ENV_FILE"
    fi
    systemctl daemon-reload
    systemctl enable --now yaver-smoke-relay-password.timer
    echo "installed. tail with:"
    echo "  journalctl -u yaver-smoke-relay-password.service -f"
    echo "  systemctl list-timers yaver-smoke-relay-password.timer"
    ;;
  --uninstall|uninstall)
    systemctl disable --now yaver-smoke-relay-password.timer 2>/dev/null || true
    systemctl stop yaver-smoke-relay-password.service 2>/dev/null || true
    rm -f "$SYSTEMD_DIR/yaver-smoke-relay-password.timer"
    rm -f "$SYSTEMD_DIR/yaver-smoke-relay-password.service"
    rm -rf "$LIBEXEC"
    # Leave /etc/yaver-smoke.env alone — it may have user edits.
    systemctl daemon-reload
    echo "uninstalled."
    ;;
  *)
    echo "usage: $0 [install|uninstall]" >&2
    exit 2
    ;;
esac
