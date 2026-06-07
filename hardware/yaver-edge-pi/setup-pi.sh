#!/usr/bin/env bash
# Yaver Pi Edge — zero-config installer for Widgets A & B.
# Turns a fresh Raspberry Pi OS Lite (64-bit) into a Yaver machine-edge node:
#   agent (yaver serve --netcapture) + RS485 serial + BLE no-Wi-Fi bridge + mesh,
#   started on every boot. Idempotent — safe to re-run.
#
#   sudo ./setup-pi.sh            # interactive claim (prints a short code + URL)
#   sudo ./setup-pi.sh --token YV_PROVISION_TOKEN   # unattended (from a QR/manifest)
#
# Requires: Raspberry Pi with BLE (Zero 2 W / 3 / 4 / 5). Run as root.
set -euo pipefail

[ "$(id -u)" -eq 0 ] || { echo "run as root (sudo)"; exit 1; }

TOKEN=""
while [ $# -gt 0 ]; do case "$1" in
  --token) TOKEN="$2"; shift 2 ;;
  *) echo "unknown arg: $1"; exit 2 ;;
esac; done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
log(){ echo -e "\033[1;36m[yaver-pi]\033[0m $*"; }

# ── 1. base packages + Node + yaver-cli (the only supported install path) ──
log "installing base packages…"
apt-get update -y
apt-get install -y --no-install-recommends \
  ca-certificates curl git python3 python3-pip python3-dbus bluez

if ! command -v node >/dev/null 2>&1; then
  log "installing Node.js (NodeSource LTS)…"
  curl -fsSL https://deb.nodesource.com/setup_lts.x | bash -
  apt-get install -y nodejs
fi

log "installing yaver-cli…"
npm install -g yaver-cli@latest

# ── 2. enable the UART / RS485 serial port (free it from the login console) ──
log "enabling serial hardware (RS485)…"
if command -v raspi-config >/dev/null 2>&1; then
  raspi-config nonint do_serial_hw 0   || true   # enable UART
  raspi-config nonint do_serial_cons 1 || true   # disable serial login console
fi
# A USB-RS485 dongle shows up as /dev/ttyUSB0; the GPIO UART as /dev/ttyAMA0/ttyS0.
# netcapture/machine auto-detect; nothing to configure here.

# ── 3. claim + autostart the agent (zero-config) ──
log "claiming this device into your Yaver account…"
if [ -n "$TOKEN" ]; then
  yaver provision claim --token "$TOKEN" || yaver auth --headless
else
  yaver auth --headless    # prints a short code + URL; sign in from any browser
fi
log "enabling agent autostart (yaver serve --netcapture)…"
# `yaver serve` auto-installs a systemd unit on Linux; pass the netcapture flag via
# config so the unit picks it up on every boot.
yaver config set netcapture_enabled true || true
yaver serve --netcapture --install-only 2>/dev/null || \
  systemctl enable --now yaver 2>/dev/null || \
  log "  (start the agent once with: yaver serve --netcapture)"

# ── 4. BLE no-Wi-Fi bridge ──
log "installing the BLE transport bridge…"
pip3 install --break-system-packages bluezero 2>/dev/null || pip3 install bluezero
install -d /opt/yaver/ble-bridge
install -m 0755 "$SCRIPT_DIR/ble-bridge/peripheral.py" /opt/yaver/ble-bridge/peripheral.py
install -m 0644 "$SCRIPT_DIR/ble-bridge/yaver-ble.service" /etc/systemd/system/yaver-ble.service
systemctl daemon-reload
systemctl enable --now bluetooth || true
systemctl enable --now yaver-ble || true

# ── 5. join Yaver mesh (best-effort; works once the agent is authed) ──
log "joining Yaver mesh…"
yaver mesh up 2>/dev/null || log "  (mesh will form once internet is reachable; BLE covers the no-Wi-Fi case)"

log "done. This Pi is a Yaver machine-edge node:"
log "  • agent + netcapture on every boot"
log "  • reachable over mesh (internet) OR BLE (no-Wi-Fi floor)"
log "  • wire RS485 A/B/GND to the machine (see ../yaver-kits/KITS.md)"
log "Widget B: point a phone's camera at the machine and open this device in the Yaver app."
