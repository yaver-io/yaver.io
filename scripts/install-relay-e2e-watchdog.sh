#!/usr/bin/env bash
#
# install-relay-e2e-watchdog — install the external relay watchdog as a systemd
# timer on THIS host (intended: the Ubuntu Hetzner box, not the relay).
#
# The watchdog probes the relay from outside every 2 minutes: relay liveness
# always, plus a signed self-test (zombie-tunnel detection) when a watchdog key
# is present. See docs/adr/relay-watchdog-protocol.md.
#
# NO SECRETS IN THIS SCRIPT OR IN THE UNIT. Yaver is a public repo. Every secret
# (Resend key, alert addresses, the watchdog signing key) is read at RUNTIME from
# an operator-created env file that is NOT in git:
#
#     /etc/yaver/relay-e2e-watchdog.env    (root:root, 0600)
#
# Example contents (fill in real values on the box, never commit):
#     RELAY_URL=https://public.yaver.io
#     WATCHDOG_KEY_PATH=/etc/yaver/watchdog.ed25519
#     RESEND_API_KEY=...
#     ALERT_TO=you@example.com
#     ALERT_FROM=watchdog@yaver.io
#
# Generate the signing key once, on the box, and register its PUBLIC half on the
# relay (relay config: watchdog_pubkeys). The private half never leaves:
#     openssl genpkey -algorithm ed25519 -out /etc/yaver/watchdog.ed25519
#     chmod 600 /etc/yaver/watchdog.ed25519
#     # public key to hand to the relay:
#     openssl pkey -in /etc/yaver/watchdog.ed25519 -pubout -outform DER \
#       | base64 -w0
#
set -euo pipefail

if [[ $EUID -ne 0 ]]; then
  echo "run as root (installs a systemd unit): sudo $0" >&2
  exit 1
fi

SCRIPT_SRC="$(cd "$(dirname "$0")" && pwd)/relay-e2e-watchdog.sh"
[[ -f "$SCRIPT_SRC" ]] || { echo "cannot find relay-e2e-watchdog.sh next to this installer" >&2; exit 1; }

INSTALL_DIR="/usr/local/lib/yaver"
ENV_FILE="/etc/yaver/relay-e2e-watchdog.env"
UNIT="yaver-relay-e2e-watchdog"

install -d -m 0755 "$INSTALL_DIR"
install -m 0755 "$SCRIPT_SRC" "$INSTALL_DIR/relay-e2e-watchdog.sh"

# Create a template env file only if the operator has not made one — never
# overwrite real secrets, never write example secrets into it.
install -d -m 0755 /etc/yaver
if [[ ! -f "$ENV_FILE" ]]; then
  umask 077
  cat > "$ENV_FILE" <<'EOF'
# Yaver relay watchdog config. Fill in real values. This file holds secrets and
# must never be committed. See docs/adr/relay-watchdog-protocol.md.
RELAY_URL=https://public.yaver.io
# WATCHDOG_KEY_PATH=/etc/yaver/watchdog.ed25519   # enables signed zombie detection
# RESEND_API_KEY=
# ALERT_TO=
# ALERT_FROM=
EOF
  chmod 600 "$ENV_FILE"
  echo "created ${ENV_FILE} (0600) — edit it with real values, then: systemctl restart ${UNIT}.timer"
else
  echo "kept existing ${ENV_FILE} (not overwriting your secrets)"
fi

cat > "/etc/systemd/system/${UNIT}.service" <<EOF
[Unit]
Description=Yaver relay external end-to-end watchdog
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
# All secrets come from this file at runtime — nothing is baked into the unit.
EnvironmentFile=${ENV_FILE}
ExecStart=${INSTALL_DIR}/relay-e2e-watchdog.sh
# Least privilege: this only needs to run curl and read one key file.
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/yaver-relay-e2e-watchdog
StateDirectory=yaver-relay-e2e-watchdog
EOF

cat > "/etc/systemd/system/${UNIT}.timer" <<EOF
[Unit]
Description=Run the Yaver relay external watchdog every 2 minutes

[Timer]
OnBootSec=90s
OnUnitActiveSec=2min
AccuracySec=15s

[Install]
WantedBy=timers.target
EOF

systemctl daemon-reload
systemctl enable --now "${UNIT}.timer"

echo "installed. status:"
systemctl status "${UNIT}.timer" --no-pager | head -4 || true
echo
echo "next:"
echo "  1. edit ${ENV_FILE} (RELAY_URL, and WATCHDOG_KEY_PATH for zombie detection)"
echo "  2. generate + register the Ed25519 watchdog key (see header of this script)"
echo "  3. run once now:  systemctl start ${UNIT}.service && journalctl -u ${UNIT}.service -n 20"
