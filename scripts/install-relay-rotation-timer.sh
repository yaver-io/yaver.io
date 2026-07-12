#!/usr/bin/env bash
# install-relay-rotation-timer.sh — install the box-local monthly password
# rotation (relay/deploy/rotate-relay-password-local.sh + its systemd
# service+timer) ONTO the relay box, and enable it.
#
# Usage: scripts/install-relay-rotation-timer.sh --host <RELAY_BOX_IP> [--ssh-key <path>]
#
# READ FIRST: this auto-rotates the SHARED fallback password monthly. Only
# enable if the shared password is NOT actively used by clients (official relay
# = per-user Convex primary). Otherwise rotate manually with
# scripts/rotate-relay-password.sh.
set -euo pipefail
HOST="${RELAY_SSH_HOST:-}"; SSH_USER="root"; SSH_KEY=""
while [ $# -gt 0 ]; do case "$1" in
  --host) HOST="$2"; shift 2 ;; --ssh-user) SSH_USER="$2"; shift 2 ;;
  --ssh-key) SSH_KEY="$2"; shift 2 ;; -h|--help) grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
  *) echo "unknown arg: $1" >&2; exit 2 ;; esac; done
[ -n "$HOST" ] || { echo "ERROR: --host (or \$RELAY_SSH_HOST) required" >&2; exit 2; }
D="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/relay/deploy"
OPTS=(-o StrictHostKeyChecking=accept-new); [ -n "$SSH_KEY" ] && OPTS+=(-i "$SSH_KEY")

scp "${OPTS[@]}" "$D/rotate-relay-password-local.sh" "$SSH_USER@$HOST:/opt/yaver-relay/rotate-relay-password-local.sh"
scp "${OPTS[@]}" "$D/yaver-relay-rotate.service"    "$SSH_USER@$HOST:/etc/systemd/system/yaver-relay-rotate.service"
scp "${OPTS[@]}" "$D/yaver-relay-rotate.timer"      "$SSH_USER@$HOST:/etc/systemd/system/yaver-relay-rotate.timer"
ssh "${OPTS[@]}" "$SSH_USER@$HOST" bash -s <<'REMOTE'
set -euo pipefail
chmod 0755 /opt/yaver-relay/rotate-relay-password-local.sh
systemctl daemon-reload
systemctl enable --now yaver-relay-rotate.timer
echo "=== timer status ==="; systemctl --no-pager status yaver-relay-rotate.timer | head -8
echo "=== next run ==="; systemctl list-timers yaver-relay-rotate.timer --no-pager | head -3
REMOTE
echo "✓ monthly relay password rotation installed + enabled on $HOST"
