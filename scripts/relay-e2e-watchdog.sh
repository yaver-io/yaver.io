#!/usr/bin/env bash
#
# relay-e2e-watchdog — external liveness probe for the Yaver relay.
#
# Runs on a host that is NOT the relay (e.g. the Ubuntu Hetzner box). That
# placement is the point: public-relay-watchdog.sh runs ON the relay and
# self-heals it, but if the relay box is down, wedged, or partitioned it cannot
# run and cannot email — the one failure it exists to report is the one it cannot
# report. This one can.
#
# ── WHAT THIS CAN AND CANNOT PROVE (read before trusting it) ─────────────────
#
# It CANNOT prove end-to-end delivery without credentials, and it must never
# pretend otherwise.
#
# The first draft of this script probed `GET /d/<deviceId>/info` unauthenticated
# and treated HTTP 401 as "healthy — the request crossed the tunnel". That was
# wrong, and the negative test caught it: a device id that DOES NOT EXIST returns
# the same 401, with a byte-identical body. The relay rejects unauthenticated
# callers at its own edge and never touches the tunnel at all.
#
# That is correct security behaviour — the relay deliberately refuses to reveal
# which devices exist, so an attacker cannot enumerate the fleet — and it means
# an anonymous prober is structurally incapable of testing delivery. A check that
# reports green without checking anything is worse than no check, because it
# converts an outage into a silent one.
#
# So:
#   * Without credentials  → relay liveness ONLY. Says so, loudly.
#   * With credentials     → signed self-test (see docs/adr/relay-watchdog-protocol.md),
#                            which is what actually detects a zombie tunnel.
#
# Zero secrets in this file. Everything comes from the environment.
#
# Usage:
#   RELAY_URL=https://public.yaver.io \
#   [WATCHDOG_KEY_PATH=/etc/yaver/watchdog.ed25519] \
#   [RESEND_API_KEY=... ALERT_TO=... ALERT_FROM=...] \
#     scripts/relay-e2e-watchdog.sh
#
set -uo pipefail

RELAY_URL="${RELAY_URL:-https://public.yaver.io}"
WATCHDOG_KEY_PATH="${WATCHDOG_KEY_PATH:-}"
STATE_DIR="${STATE_DIR:-/var/lib/yaver-relay-e2e-watchdog}"
MAX_TIME="${MAX_TIME:-8}"
ALERT_TO="${ALERT_TO:-}"
ALERT_FROM="${ALERT_FROM:-}"
RESEND_API_KEY="${RESEND_API_KEY:-}"
HOST_LABEL="${HOST_LABEL:-$(hostname -f 2>/dev/null || hostname)}"

mkdir -p "$STATE_DIR" 2>/dev/null || true

send_email() {
  local subject="$1" html="$2"
  if [[ -z "$RESEND_API_KEY" || -z "$ALERT_TO" || -z "$ALERT_FROM" ]]; then
    echo "email config incomplete; skipping alert: ${subject}" >&2
    return 0
  fi
  curl -fsS https://api.resend.com/emails \
    -H "Authorization: Bearer ${RESEND_API_KEY}" \
    -H "Content-Type: application/json" \
    -d "$(python3 - "$ALERT_FROM" "$ALERT_TO" "$subject" "$html" <<'PY'
import json, sys
frm, to, subject, html = sys.argv[1:5]
print(json.dumps({"from": frm, "to": [to], "subject": subject, "html": html}))
PY
)" >/dev/null && echo "alert sent: ${subject}"
}

# Alert only on a CHANGE of state. A watchdog that emails every five minutes for
# a day trains you to ignore it, and then it is decoration.
transition() {
  local key="$1" new="$2" subject="$3" body="$4"
  local file="${STATE_DIR}/${key}" prev="unknown"
  [[ -f "$file" ]] && prev="$(tr -d '\r\n' <"$file")"
  printf '%s\n' "$new" >"$file" 2>/dev/null || true
  [[ "$prev" == "$new" ]] && return 0
  send_email "$subject" "$body"
}

ts="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
overall=0

# ── 1. Is the relay reachable FROM OUTSIDE? ──────────────────────────────────
# The on-box watchdog cannot answer this, by construction.
if curl -fsS --max-time "$MAX_TIME" "${RELAY_URL}/health" 2>/dev/null | grep -q '"ok"[[:space:]]*:[[:space:]]*true'; then
  echo "relay reachable: ${RELAY_URL}"
  transition "relay" "up" \
    "Yaver relay reachable again" \
    "<p>${RELAY_URL} is answering /health again.</p><p>Probed from: ${HOST_LABEL}</p><p>${ts}</p>"
else
  echo "RELAY UNREACHABLE from ${HOST_LABEL}: ${RELAY_URL}" >&2
  transition "relay" "down" \
    "Yaver relay UNREACHABLE (external probe)" \
    "<p><strong>${RELAY_URL} is not answering /health from outside.</strong></p>
     <p>The relay's own on-box watchdog cannot report this: if the box is down or
     partitioned, it is down too. Every phone with no LAN or VPN path to its
     machine is offline right now.</p>
     <p>Probed from: ${HOST_LABEL}</p><p>${ts}</p>"
  exit 1
fi

# ── 2. Signed self-test — the only way to detect a ZOMBIE tunnel ─────────────
if [[ -z "$WATCHDOG_KEY_PATH" || ! -f "$WATCHDOG_KEY_PATH" ]]; then
  echo "NOTE: no WATCHDOG_KEY_PATH — relay liveness only." >&2
  echo "      A zombie tunnel (registered, forwarding nothing) will NOT be detected." >&2
  echo "      Anonymous probes cannot prove delivery: the relay rejects them at its" >&2
  echo "      own auth edge and never touches the tunnel — deliberately, so the fleet" >&2
  echo "      cannot be enumerated. See docs/adr/relay-watchdog-protocol.md." >&2
  exit "$overall"
fi

# Ed25519-signed, timestamped, nonced. Replay-proof, no shared secret, and the
# relay stores only our PUBLIC key. See the ADR for the wire format.
nonce="$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \n')"
epoch_ms="$(( $(date -u +%s) * 1000 ))"
body="$(printf '{"nonce":"%s","issuedAtMs":%s}' "$nonce" "$epoch_ms")"

sig="$(python3 - "$WATCHDOG_KEY_PATH" "$body" <<'PY'
import base64, sys
try:
    from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
    from cryptography.hazmat.primitives import serialization
except ImportError:
    sys.stderr.write("python3 'cryptography' package required for signed self-test\n")
    sys.exit(3)
key_path, body = sys.argv[1], sys.argv[2]
with open(key_path, "rb") as fh:
    key = serialization.load_pem_private_key(fh.read(), password=None)
if not isinstance(key, Ed25519PrivateKey):
    sys.stderr.write("watchdog key must be Ed25519\n")
    sys.exit(3)
print(base64.b64encode(key.sign(body.encode())).decode())
PY
)" || { echo "failed to sign self-test request" >&2; exit 2; }

resp="$(curl -s -w '\n%{http_code}' --max-time "$MAX_TIME" \
        -X POST "${RELAY_URL}/admin/selftest" \
        -H 'Content-Type: application/json' \
        -H "X-Yaver-Watchdog-Sig: ${sig}" \
        -d "$body" 2>/dev/null)"
code="$(printf '%s' "$resp" | tail -1)"
payload="$(printf '%s' "$resp" | sed '$d')"

case "$code" in
  200)
    # Relay probed its OWN tunnels internally and reported per-device delivery.
    # It never discloses device ids to an unauthenticated caller.
    zombies="$(printf '%s' "$payload" | python3 -c 'import json,sys;d=json.load(sys.stdin);print(d.get("zombies",0))' 2>/dev/null || echo 0)"
    if [[ "$zombies" == "0" ]]; then
      echo "self-test ok: every registered tunnel is delivering"
      transition "selftest" "up" "Yaver relay self-test healthy again" \
        "<p>All registered tunnels are forwarding.</p><p>${ts}</p>"
    else
      echo "SELF-TEST: ${zombies} zombie tunnel(s) — registered but not forwarding" >&2
      overall=1
      transition "selftest" "zombie" \
        "Yaver relay: ${zombies} ZOMBIE tunnel(s)" \
        "<p><strong>${zombies} tunnel(s) registered but delivering nothing.</strong></p>
         <p>The relay's own eviction (relay/tunnel_liveness.go) should kill these within
         a couple of probes. If this alert persists, eviction is not working — that is
         the bug worth chasing, not the tunnel.</p>
         <p>${ts}</p><pre>${payload}</pre>"
    fi
    ;;
  401|403)
    echo "self-test REJECTED (HTTP ${code}) — watchdog key not authorised on this relay" >&2
    overall=2
    transition "selftest" "unauthorised" "Yaver relay: watchdog key rejected" \
      "<p>The relay refused the watchdog's signature (HTTP ${code}). Register the public
       key on the relay, or rotate it. Until then, zombie tunnels go undetected.</p><p>${ts}</p>"
    ;;
  404)
    echo "NOTE: relay has no /admin/selftest endpoint yet (see docs/adr/relay-watchdog-protocol.md)" >&2
    ;;
  *)
    echo "self-test unexpected HTTP ${code}" >&2
    overall=1
    ;;
esac

exit "$overall"
