#!/usr/bin/env bash
set -euo pipefail

HEALTH_URL="${HEALTH_URL:-https://public.yaver.io/health}"
STATE_DIR="${STATE_DIR:-/var/lib/yaver-public-relay-watchdog}"
STATE_FILE="${STATE_DIR}/state"
MAX_TIME="${MAX_TIME:-10}"
ALERT_TO="${ALERT_TO:-}"
ALERT_FROM="${ALERT_FROM:-}"
RESEND_API_KEY="${RESEND_API_KEY:-}"
HOST_LABEL="${HOST_LABEL:-$(hostname -f 2>/dev/null || hostname)}"
CHECK_LABEL="${CHECK_LABEL:-Yaver relay}"

mkdir -p "$STATE_DIR"

send_email() {
  local subject="$1"
  local html="$2"

  if [[ -z "$RESEND_API_KEY" || -z "$ALERT_TO" || -z "$ALERT_FROM" ]]; then
    echo "email config incomplete; skipping alert send" >&2
    return 1
  fi

  curl -fsS https://api.resend.com/emails \
    -H "Authorization: Bearer ${RESEND_API_KEY}" \
    -H "Content-Type: application/json" \
    -d "$(printf '{"from":"%s","to":["%s"],"subject":"%s","html":"%s"}' \
      "$(printf '%s' "$ALERT_FROM" | sed 's/"/\\"/g')" \
      "$(printf '%s' "$ALERT_TO" | sed 's/"/\\"/g')" \
      "$(printf '%s' "$subject" | sed 's/"/\\"/g')" \
      "$(printf '%s' "$html" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read())[1:-1])')")" \
    >/dev/null
  echo "alert email sent: ${subject}"
}

mark_state() {
  printf '%s\n' "$1" >"$STATE_FILE"
}

previous_state="unknown"
if [[ -f "$STATE_FILE" ]]; then
  previous_state="$(tr -d '\r\n' <"$STATE_FILE")"
fi

timestamp="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
body=""
status="down"

if body="$(curl -fsS --max-time "$MAX_TIME" "$HEALTH_URL" 2>&1)"; then
  if printf '%s' "$body" | grep -q '"ok"[[:space:]]*:[[:space:]]*true'; then
    status="up"
  fi
fi

if [[ "$status" == "up" ]]; then
  echo "relay health ok: ${HEALTH_URL}"
  if [[ "$previous_state" != "up" ]]; then
    send_email \
      "${CHECK_LABEL} recovered" \
      "<p><strong>${CHECK_LABEL} recovered.</strong></p><p>Host: ${HOST_LABEL}</p><p>Checked URL: ${HEALTH_URL}</p><p>Time (UTC): ${timestamp}</p><pre>${body}</pre>" || true
  fi
  mark_state "up"
  exit 0
fi

echo "relay health failed: ${HEALTH_URL}" >&2

# SELF-HEAL: this watchdog runs ON the relay box, so try to recover before
# escalating to a human. A restart fixes a HUNG relay (process alive but not
# serving) — which systemd's Restart=on-failure can't catch — as well as a
# crash. Re-check after the restart; only alert as "down" if it stays down.
healed="no"
if command -v systemctl >/dev/null 2>&1; then
  echo "self-heal: systemctl restart yaver-relay"
  systemctl restart yaver-relay 2>/dev/null || true
  sleep 5
  if body2="$(curl -fsS --max-time "$MAX_TIME" "$HEALTH_URL" 2>&1)" \
     && printf '%s' "$body2" | grep -q '"ok"[[:space:]]*:[[:space:]]*true'; then
    healed="yes"
    echo "relay recovered after restart"
  fi
fi

if [[ "$healed" == "yes" ]]; then
  # Surface the auto-heal once per down-streak so recurring flaps are visible,
  # but don't page on every routine self-recovery.
  if [[ "$previous_state" != "up" ]]; then
    send_email \
      "${CHECK_LABEL} auto-healed" \
      "<p><strong>${CHECK_LABEL} was unhealthy; the watchdog restarted it and it recovered.</strong></p><p>Host: ${HOST_LABEL}</p><p>Time (UTC): ${timestamp}</p><pre>${body2}</pre>" || true
  fi
  mark_state "up"
  exit 0
fi

# Restart didn't help — escalate (box-level problem, needs a human).
if [[ "$previous_state" != "down" ]]; then
  send_email \
    "${CHECK_LABEL} is down (self-heal failed)" \
    "<p><strong>${CHECK_LABEL} health check failed and a restart did not recover it.</strong></p><p>Host: ${HOST_LABEL}</p><p>Checked URL: ${HEALTH_URL}</p><p>Time (UTC): ${timestamp}</p><pre>${body:-no response}</pre>" || true
fi

mark_state "down"
exit 1
