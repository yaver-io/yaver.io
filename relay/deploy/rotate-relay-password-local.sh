#!/usr/bin/env bash
# rotate-relay-password-local.sh — runs ON the relay box (systemd timer) to
# rotate the SHARED relay password IN PLACE: generate -> swap in the unit ->
# restart -> health-check -> rollback on failure. No SSH; it's local.
#
# Scope: this rotates only the shared FALLBACK password. The official relay's
# PRIMARY auth is per-user (Convex) + (soon) device signatures, which this does
# not touch — so auto-rotation just keeps the rarely-used shared secret fresh
# and closes its attack surface, without needing clients to re-fetch anything on
# the per-user path. (If you actively use the shared password for clients, do
# NOT enable this timer — rotate manually with scripts/rotate-relay-password.sh.)
set -euo pipefail
UNIT=/etc/systemd/system/yaver-relay.service
HP="${RELAY_HTTP_PORT:-8080}"
[ -f "$UNIT" ] || { echo "no yaver-relay.service"; exit 3; }
NP="$(openssl rand -hex 32)"
cp "$UNIT" "$UNIT.pre-rotate"
sed -i \
  -e "s|--password [^ ]*|--password $NP|g" \
  -e "s|--password=[^ ]*|--password=$NP|g" \
  -e "s|^\(Environment=RELAY_PASSWORD=\).*|\1$NP|g" \
  "$UNIT"
if ! grep -q "$NP" "$UNIT"; then
  echo "no --password / RELAY_PASSWORD to rotate in the unit"; cp "$UNIT.pre-rotate" "$UNIT"; rm -f "$UNIT.pre-rotate"; exit 4
fi
systemctl daemon-reload; systemctl restart yaver-relay; sleep 3
if curl -fsS -m 5 "http://127.0.0.1:${HP}/health" >/dev/null 2>&1; then
  echo "relay rotated + healthy $(date -u +%FT%TZ)"; rm -f "$UNIT.pre-rotate"
else
  echo "UNHEALTHY after rotation — rolling back"; cp "$UNIT.pre-rotate" "$UNIT"; rm -f "$UNIT.pre-rotate"
  systemctl daemon-reload; systemctl restart yaver-relay; exit 5
fi
