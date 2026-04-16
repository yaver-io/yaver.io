#!/usr/bin/env bash
set -euo pipefail

HOST="${HOST:-root@37.27.184.85}"
PUBLIC_URL="${PUBLIC_URL:-https://public.yaver.io/health}"
PRIVATE_URL="${PRIVATE_URL:-https://kivanc.relay.yaver.io/health}"

ssh -o StrictHostKeyChecking=no "$HOST" \
  "PUBLIC_URL='$PUBLIC_URL' PRIVATE_URL='$PRIVATE_URL' bash -s" <<'REMOTE'
set -euo pipefail

print_header() {
  printf '\n== %s ==\n' "$1"
}

show_unit() {
  local unit="$1"
  local enabled active
  enabled="$(systemctl is-enabled "$unit" 2>/dev/null || true)"
  active="$(systemctl is-active "$unit" 2>/dev/null || true)"
  printf '%-40s enabled=%-10s active=%s\n' "$unit" "${enabled:-unknown}" "${active:-unknown}"
}

check_http() {
  local label="$1"
  local url="$2"
  local tmp status
  tmp="$(mktemp)"
  status="$(curl -sS -o "$tmp" -w '%{http_code}' --max-time 10 "$url" || true)"
  printf '%-20s %s -> HTTP %s\n' "$label" "$url" "${status:-000}"
  if [[ -s "$tmp" ]]; then
    cat "$tmp"
    printf '\n'
  fi
  rm -f "$tmp"
}

print_header "Watchdogs"
show_unit yaver-public-relay-watchdog.timer
show_unit yaver-public-relay-watchdog.service
show_unit yaver-private-relay-watchdog.timer
show_unit yaver-private-relay-watchdog.service

print_header "Timers"
systemctl list-timers \
  yaver-public-relay-watchdog.timer \
  yaver-private-relay-watchdog.timer \
  --no-pager || true

print_header "Recent Public Watchdog Logs"
journalctl -u yaver-public-relay-watchdog.service -n 6 --no-pager || true

print_header "Recent Private Watchdog Logs"
journalctl -u yaver-private-relay-watchdog.service -n 6 --no-pager || true

print_header "Relay Health"
check_http "public relay" "$PUBLIC_URL"
check_http "private relay" "$PRIVATE_URL"
REMOTE
