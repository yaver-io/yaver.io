#!/usr/bin/env bash
# Install the Yaver external watchdog systemd unit. Idempotent —
# rerun anytime.
#
#   sudo bash ci/remote/watchdog/install.sh
#   sudo bash ci/remote/watchdog/install.sh --uninstall
#
# This deliberately replaces the older ci/remote/smoke/ systemd units:
# the smoke logic now lives inside the agent (see
# desktop/agent/smoke_relay_password.go), so we no longer need a
# standalone 15-min timer hitting Convex from outside. The watchdog's
# job is exclusively "is the agent still breathing?".

set -euo pipefail

if [ "${EUID:-$(id -u)}" -ne 0 ]; then
  echo "install.sh: must run as root" >&2
  exit 1
fi

ACTION="${1:-install}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIBEXEC=/usr/local/libexec/yaver-watchdog
SYSTEMD_DIR=/etc/systemd/system
ENV_FILE=/etc/yaver-watchdog.env

case "$ACTION" in
  install)
    install -d -m 0755 "$LIBEXEC"
    install -m 0755 "$SCRIPT_DIR/yaver-watchdog.sh" "$LIBEXEC/yaver-watchdog.sh"
    install -m 0644 "$SCRIPT_DIR/systemd/yaver-watchdog.service" \
      "$SYSTEMD_DIR/yaver-watchdog.service"
    install -m 0644 "$SCRIPT_DIR/systemd/yaver-watchdog.timer" \
      "$SYSTEMD_DIR/yaver-watchdog.timer"
    if [ ! -f "$ENV_FILE" ]; then
      cat > "$ENV_FILE" <<'EOF'
# Yaver watchdog config. Everything here is optional; defaults live in
# the script. No secrets — the watchdog only observes.
#
# Uncomment to have the watchdog restart yaver-agent.service when
# the agent looks dead. Off by default: on a dev box you probably
# want to see the crash, not have it auto-healed.
# WATCHDOG_RESTART_ON_FAILURE=1
# WATCHDOG_RESTART_UNIT=yaver-agent.service
#
# Override the beacon path if you run the agent as a non-default user.
# YAVER_BEACON_PATH=/home/youruser/.yaver/last-healthy
#
# Maximum acceptable beacon staleness (seconds). Must be > the agent's
# watchdog cadence (~30s) + a safety margin.
# WATCHDOG_MAX_AGE_SEC=180
EOF
      chmod 0644 "$ENV_FILE"
    fi

    # Sunset the previous smoke systemd units. They're superseded by
    # the in-agent smoke task — keeping both alive just doubles up.
    if systemctl list-unit-files yaver-smoke-relay-password.timer >/dev/null 2>&1; then
      systemctl disable --now yaver-smoke-relay-password.timer 2>/dev/null || true
      rm -f /etc/systemd/system/yaver-smoke-relay-password.timer
      rm -f /etc/systemd/system/yaver-smoke-relay-password.service
      rm -rf /usr/local/libexec/yaver-smoke
      echo "removed superseded yaver-smoke-relay-password unit (now runs inside agent)"
    fi

    systemctl daemon-reload
    systemctl enable --now yaver-watchdog.timer
    echo "installed. tail with:"
    echo "  journalctl -u yaver-watchdog.service -f"
    echo "  systemctl list-timers yaver-watchdog.timer"
    ;;
  --uninstall|uninstall)
    systemctl disable --now yaver-watchdog.timer 2>/dev/null || true
    systemctl stop yaver-watchdog.service 2>/dev/null || true
    rm -f "$SYSTEMD_DIR/yaver-watchdog.timer"
    rm -f "$SYSTEMD_DIR/yaver-watchdog.service"
    rm -rf "$LIBEXEC"
    systemctl daemon-reload
    echo "uninstalled."
    ;;
  *)
    echo "usage: $0 [install|uninstall]" >&2
    exit 2
    ;;
esac
