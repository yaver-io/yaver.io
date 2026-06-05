#!/usr/bin/env bash
# Relay HTTP-over-QUIC tunnel end-to-end test (runs in a plain Linux container —
# no privilege needed; it's just TCP/QUIC). Proves the core relay product path:
#   external HTTP client -> relay /d/<id>/health -> QUIC tunnel -> agent -> response
# A meshtest "relay-http" agent registers with the relay and answers proxied
# requests; curl through the relay's HTTP port must return the agent's body.
set -euo pipefail

PASSWORD=meshtest-relay-secret
DID=agent-http-0001   # >= 8 chars (relay deviceId shape)

echo "== Relay HTTP-tunnel e2e: $(uname -sr) =="
RELAY_PASSWORD="$PASSWORD" /relay serve --quic-port 4433 --http-port 8443 >/tmp/relay.log 2>&1 &
sleep 2
/meshtest relay-http "$DID" 127.0.0.1:4433 "$PASSWORD" &
sleep 3

echo "== GET http://127.0.0.1:8443/d/$DID/health (client -> relay -> agent) =="
# The relay /d/<id>/ proxy is gated by the relay password (X-Relay-Password).
OUT="$(curl -fsS --max-time 10 -H "X-Relay-Password: $PASSWORD" "http://127.0.0.1:8443/d/$DID/health")" || {
  echo "curl failed"; echo "--- relay log ---"; tail -20 /tmp/relay.log; exit 1; }
echo "response: $OUT"

if echo "$OUT" | grep -q '"ok":true' && echo "$OUT" | grep -q "$DID" && echo "$OUT" | grep -q '"path":"/health"'; then
  echo
  echo "RESULT: PASS — HTTP traversed relay -> agent -> back ✓"
  exit 0
fi
echo
echo "RESULT: FAIL — response did not round-trip through the agent"
tail -20 /tmp/relay.log || true
exit 1
