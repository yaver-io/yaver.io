#!/usr/bin/env bash
# Yaver relay-password smoke check — runs on the Hetzner test box as a
# systemd oneshot (see yaver-smoke-relay-password.{service,timer}).
#
# What it guards against:
#   - Regression of the "{"error":"invalid relay password"}" class of
#     bugs the web Hot Reload preview iframe hit: platform config
#     drift, fresh-install race on userSettings.relayPassword, and
#     broken /settings/repair-relay behaviour.
#
# What it does NOT do:
#   - Touch production user data. The deep check uses a throwaway
#     `e2e-smoke-<uuid>@yaver.test` signup that it deletes before exit.
#   - Store credentials in this repo. The throwaway account is created
#     fresh each run; no secrets needed. Convex URL comes from
#     /etc/yaver-smoke.env on the box (populated during bootstrap).
#
# Exit codes:
#   0  all checks passed
#   1  unrecoverable — convex unreachable, config malformed, etc.
#   2  regression detected — the exact "invalid relay password" class
#      failure we deployed the fix for.
#
# Output goes to stdout/stderr → systemd captures it into journalctl.

set -euo pipefail

SCRIPT_NAME="yaver-smoke-relay-password"
CONVEX_URL="${CONVEX_URL:-https://perceptive-minnow-557.eu-west-1.convex.site}"
TIMEOUT_SEC="${SMOKE_TIMEOUT:-10}"
TMPDIR="$(mktemp -d -t yaver-smoke.XXXXXX)"
trap 'rm -rf "$TMPDIR"' EXIT

log()   { printf '[%s] %s\n' "$SCRIPT_NAME" "$*"; }
fail()  { printf '[%s] FAIL: %s\n' "$SCRIPT_NAME" "$*" >&2; exit "${2:-1}"; }
regress() { printf '[%s] REGRESSION: %s\n' "$SCRIPT_NAME" "$*" >&2; exit 2; }

require() {
  for cmd in "$@"; do
    command -v "$cmd" >/dev/null 2>&1 || fail "missing dependency: $cmd"
  done
}
require curl jq

# Load optional overrides (no secrets required, see install.sh).
if [ -f /etc/yaver-smoke.env ]; then
  # shellcheck disable=SC1091
  . /etc/yaver-smoke.env
fi

HTTP_CODE=""
HTTP_BODY=""
# Populates the globals HTTP_CODE + HTTP_BODY. Caller must NOT pipe to
# read these — earlier versions did and lost the values to the subshell.
http_call() {
  # http_call METHOD URL [--data '...'] [--auth TOKEN]
  local method="$1" url="$2" body_file="$TMPDIR/body.$RANDOM"
  shift 2
  local auth="" data=""
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --data) data="$2"; shift 2 ;;
      --auth) auth="$2"; shift 2 ;;
      *) shift ;;
    esac
  done
  local args=(-sS -o "$body_file" -w '%{http_code}' --max-time "$TIMEOUT_SEC" -X "$method")
  args+=(-H "Accept: application/json")
  [ -n "$auth" ] && args+=(-H "Authorization: Bearer $auth")
  [ -n "$data" ] && args+=(-H "Content-Type: application/json" --data "$data")
  HTTP_CODE="$(curl "${args[@]}" "$url" 2>/dev/null || true)"
  [ -z "$HTTP_CODE" ] && HTTP_CODE="000"
  HTTP_BODY="$(cat "$body_file" 2>/dev/null || true)"
  rm -f "$body_file"
}

# ── 1. Shallow check: platform config has a populated relay password ─
log "checking ${CONVEX_URL}/config"
http_call GET "${CONVEX_URL}/config"
if [ "$HTTP_CODE" != "200" ]; then
  fail "GET /config returned HTTP $HTTP_CODE"
fi
relay_count="$(printf '%s' "$HTTP_BODY" | jq -r '(.relayServers // []) | length')"
[ "$relay_count" -gt 0 ] || fail "platform config lists no relay servers"
first_password="$(printf '%s' "$HTTP_BODY" | jq -r '.relayServers[0].password // ""')"
first_http_url="$(printf '%s' "$HTTP_BODY" | jq -r '.relayServers[0].httpUrl // ""')"
if [ -z "$first_password" ]; then
  regress "platformConfig.relay_servers[0].password is empty — fresh signups will fail"
fi
log "platform default relay password present (len=${#first_password}), httpUrl=$first_http_url"

# ── 2. Deep check: throwaway signup flow end-to-end ──────────────────
# 12 hex chars — works on both GNU and BSD tr (macOS tr chokes on
# non-LC_ALL=C input when reading /dev/urandom directly).
uuid="$(LC_ALL=C tr -dc 'a-z0-9' </dev/urandom | head -c 12 || true)"
if [ -z "$uuid" ]; then
  # Fallback: openssl is on every modern system.
  uuid="$(openssl rand -hex 6 2>/dev/null || date +%s%N)"
fi
test_email="e2e-smoke-${uuid}@yaver.test"
test_password="SmokeTest!${uuid}"
cleanup_token=""
cleanup() {
  local ec=$?
  if [ -n "$cleanup_token" ]; then
    http_call POST "${CONVEX_URL}/auth/delete-account" --auth "$cleanup_token" >/dev/null 2>&1 || true
  fi
  rm -rf "$TMPDIR"
  exit "$ec"
}
trap cleanup EXIT

log "signing up throwaway user ${test_email}"
signup_body="$(jq -nc --arg email "$test_email" --arg password "$test_password" --arg fullName "Smoke Test" \
  '{email:$email, password:$password, fullName:$fullName}')"
http_call POST "${CONVEX_URL}/auth/signup" --data "$signup_body"
if [ "$HTTP_CODE" != "200" ]; then
  fail "signup failed: HTTP $HTTP_CODE body=${HTTP_BODY:0:200}"
fi
cleanup_token="$(printf '%s' "$HTTP_BODY" | jq -r '.token // ""')"
[ -n "$cleanup_token" ] || fail "signup response missing token"

log "fetching ${CONVEX_URL}/settings for throwaway user"
http_call GET "${CONVEX_URL}/settings" --auth "$cleanup_token"
if [ "$HTTP_CODE" != "200" ]; then
  fail "GET /settings failed: HTTP $HTTP_CODE"
fi
user_pw="$(printf '%s' "$HTTP_BODY" | jq -r '.settings.relayPassword // ""')"
user_relay="$(printf '%s' "$HTTP_BODY" | jq -r '.settings.relayUrl // ""')"
if [ -z "$user_pw" ]; then
  # New-user race → call the repair endpoint and retry once. This is exactly
  # the recovery path the web dashboard uses, so if it doesn't work here
  # it won't work for a real fresh signup either.
  log "user has no relayPassword — exercising /settings/repair-relay"
  http_call POST "${CONVEX_URL}/settings/repair-relay" --auth "$cleanup_token"
  if [ "$HTTP_CODE" != "200" ]; then
    regress "/settings/repair-relay returned HTTP $HTTP_CODE body=${HTTP_BODY:0:200}"
  fi
  repaired="$(printf '%s' "$HTTP_BODY" | jq -r '.repaired // false')"
  log "repair-relay result: $HTTP_BODY"
  # Retry settings fetch
  http_call GET "${CONVEX_URL}/settings" --auth "$cleanup_token"
  user_pw="$(printf '%s' "$HTTP_BODY" | jq -r '.settings.relayPassword // ""')"
  if [ -z "$user_pw" ]; then
    regress "relayPassword still empty after /settings/repair-relay (repaired=$repaired)"
  fi
  log "relayPassword populated after repair"
else
  log "relayPassword already populated (len=${#user_pw})"
fi

# Sanity: every synced user's password should equal the platform default.
# If they diverge, the sync migration needs to be re-run and the web
# dashboard will keep 401-ing until it is.
if [ "$user_pw" != "$first_password" ]; then
  regress "user relayPassword != platform default — syncRelayPasswordsToAllUsers needs to run"
fi

# ── 3. End-to-end: relay actually accepts the password ───────────────
# /relay/validate is the public endpoint the relay uses to validate
# per-user passwords. Hitting it directly is the cheapest way to catch
# "relay says this password is invalid" without routing a real tunnel.
log "validating relay password through ${CONVEX_URL}/relay/validate"
validate_body="$(jq -nc --arg pw "$user_pw" '{password:$pw}')"
http_call POST "${CONVEX_URL}/relay/validate" --data "$validate_body"
if [ "$HTTP_CODE" != "200" ]; then
  regress "/relay/validate returned HTTP $HTTP_CODE body=${HTTP_BODY:0:200}"
fi
valid_user_id="$(printf '%s' "$HTTP_BODY" | jq -r '.userId // empty')"
if [ -z "$valid_user_id" ]; then
  regress "/relay/validate did not recognise the freshly-synced password — relay would 401 with \"invalid relay password\""
fi
log "relay accepts password — userId=$valid_user_id"

# ── 4. Optional: hit the live relay HTTP endpoint with __rp ──────────
# Matches what the web iframe does. We use a bogus deviceId so the
# relay's device-not-registered path returns a 5xx — but crucially NOT
# a 401 "invalid relay password". If we see that 401 here, the
# password is wrong as far as the running relay is concerned.
if [ -n "$first_http_url" ]; then
  probe_url="${first_http_url%/}/d/smoke-nonexistent-device/health?__rp=${user_pw}"
  log "probing live relay: ${first_http_url}/d/<bogus>/health?__rp=..."
  http_call GET "$probe_url"
  if [ "$HTTP_CODE" = "401" ]; then
    regress "live relay rejected __rp with 401 (${HTTP_BODY:0:120})"
  fi
  log "live relay did not 401 (code=$HTTP_CODE)"
fi

log "OK — all relay-password checks passed"
