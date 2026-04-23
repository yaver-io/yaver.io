#!/usr/bin/env bash
set -euo pipefail

LOG_DIR="${HOST_SHARE_SMOKE_LOG_DIR:-/tmp}"
LOG="$LOG_DIR/host-share-lifecycle.log"
mkdir -p "$LOG_DIR"
exec > >(tee -a "$LOG") 2>&1

banner() { printf '\n========== %s ==========\n' "$*"; }
die() { echo "ERROR: $*" >&2; exit 1; }

require_env() {
  local name="$1"
  [ -n "${!name:-}" ] || die "$name is required"
}

json_get() {
  local expr="$1"
  python3 -c '
import json, sys
expr = sys.argv[1]
data = json.load(sys.stdin)
value = data
for part in expr.split("."):
    if not part:
        continue
    if isinstance(value, list):
        value = value[int(part)]
    else:
        value = value.get(part)
print("" if value is None else value)
' "$expr"
}

auth_token() {
  local email="$1" password="$2" fullname="$3"
  local resp token
  resp="$(curl -fsS -X POST "${CONVEX_SITE_URL}/auth/login" \
    -H 'Content-Type: application/json' \
    -d "{\"email\":\"${email}\",\"password\":\"${password}\"}" 2>/dev/null || true)"
  token="$(printf '%s' "$resp" | json_get "token" 2>/dev/null || true)"
  if [ -n "$token" ]; then
    printf '%s\n' "$token"
    return 0
  fi
  resp="$(curl -fsS -X POST "${CONVEX_SITE_URL}/auth/signup" \
    -H 'Content-Type: application/json' \
    -d "{\"email\":\"${email}\",\"fullName\":\"${fullname}\",\"password\":\"${password}\"}" 2>/dev/null || true)"
  token="$(printf '%s' "$resp" | json_get "token" 2>/dev/null || true)"
  [ -n "$token" ] || die "failed to authenticate ${email}"
  printf '%s\n' "$token"
}

rand() { head -c16 /dev/urandom | od -An -tx1 | tr -d ' \n'; }

cleanup() {
  if [ -n "${HOST_SHARE_CODE:-}" ] && [ -n "${HOST_TOKEN:-}" ]; then
    curl -fsS -X POST "${CONVEX_SITE_URL}/host-share/revoke" \
      -H "Authorization: Bearer ${HOST_TOKEN}" \
      -H 'Content-Type: application/json' \
      -d "{\"code\":\"${HOST_SHARE_CODE}\"}" >/dev/null 2>&1 || true
  fi
  if [ -n "${GUEST_TOKEN:-}" ]; then
    curl -fsS -X POST "${CONVEX_SITE_URL}/auth/delete-account" \
      -H "Authorization: Bearer ${GUEST_TOKEN}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

require_env CONVEX_SITE_URL
require_env HOST_SHARE_HOST_BASE_URL
require_env HOST_SHARE_HOST_DEVICE_ID
require_env HOST_SHARE_HOST_EMAIL
require_env HOST_SHARE_HOST_PASSWORD

HOST_SHARE_EXPECT_RUNNER="${HOST_SHARE_EXPECT_RUNNER:-codex}"
HOST_SHARE_GUEST_EMAIL="${HOST_SHARE_GUEST_EMAIL:-hostshare-life-$(rand)@yaver.test}"
HOST_SHARE_GUEST_PASSWORD="${HOST_SHARE_GUEST_PASSWORD:-$(rand)}"
HOST_SHARE_HOST_NAME="${HOST_SHARE_HOST_NAME:-Host Smoke User}"
HOST_SHARE_GUEST_NAME="${HOST_SHARE_GUEST_NAME:-Lifecycle Guest Smoke}"

banner "authenticate host and guest"
HOST_TOKEN="$(auth_token "$HOST_SHARE_HOST_EMAIL" "$HOST_SHARE_HOST_PASSWORD" "$HOST_SHARE_HOST_NAME")"
GUEST_TOKEN="$(auth_token "$HOST_SHARE_GUEST_EMAIL" "$HOST_SHARE_GUEST_PASSWORD" "$HOST_SHARE_GUEST_NAME")"
echo "host auth ok"
echo "guest auth ok"

banner "create invite"
create_resp="$(curl -fsS -X POST "${CONVEX_SITE_URL}/host-share/create" \
  -H "Authorization: Bearer ${HOST_TOKEN}" \
  -H 'Content-Type: application/json' \
  -d "{
    \"guestEmail\":\"${HOST_SHARE_GUEST_EMAIL}\",
    \"label\":\"agentless-lifecycle\",
    \"hostDeviceId\":\"${HOST_SHARE_HOST_DEVICE_ID}\",
    \"inviteTtlMinutes\":30,
    \"sessionTtlMinutes\":60,
    \"idleTimeoutMinutes\":15,
    \"toolingPreset\":\"all-coding-tools\",
    \"resourcePreset\":\"balanced\",
    \"allowInfra\":true,
    \"allowTerminal\":true,
    \"allowTunnel\":false,
    \"useHostAgentTools\":true,
    \"useHostInfra\":true,
    \"allowedRunners\":[\"${HOST_SHARE_EXPECT_RUNNER}\"]
  }")"
HOST_SHARE_CODE="$(printf '%s' "$create_resp" | json_get "inviteCode")"
[ -n "$HOST_SHARE_CODE" ] || die "host-share create failed: $create_resp"
echo "invite created"

banner "join"
join_resp="$(curl -fsS -X POST "${CONVEX_SITE_URL}/host-share/join" \
  -H "Authorization: Bearer ${GUEST_TOKEN}" \
  -H 'Content-Type: application/json' \
  -d "{\"code\":\"${HOST_SHARE_CODE}\"}")"
HOST_SHARE_SESSION_ID="$(printf '%s' "$join_resp" | json_get "sessionId")"
[ -n "$HOST_SHARE_SESSION_ID" ] || die "host-share join failed: $join_resp"
echo "join ok"

banner "pre-end access works"
pre_status="$(curl -sS -o /tmp/hostshare_lifecycle_pre.json -w '%{http_code}' \
  "${HOST_SHARE_HOST_BASE_URL}/agent/runners" \
  -H "Authorization: Bearer ${GUEST_TOKEN}")"
[ "$pre_status" = "200" ] || die "guest /agent/runners before end expected 200, got ${pre_status}"
echo "pre-end access ok"

banner "host ends session"
curl -fsS -X POST "${CONVEX_SITE_URL}/host-share/end" \
  -H "Authorization: Bearer ${HOST_TOKEN}" \
  -H 'Content-Type: application/json' \
  -d "{\"sessionId\":\"${HOST_SHARE_SESSION_ID}\"}" >/dev/null
echo "session ended"

banner "guest loses access"
deadline=$(( $(date +%s) + 30 ))
while true; do
  post_status="$(curl -sS -o /tmp/hostshare_lifecycle_post.json -w '%{http_code}' \
    "${HOST_SHARE_HOST_BASE_URL}/agent/runners" \
    -H "Authorization: Bearer ${GUEST_TOKEN}" || true)"
  case "$post_status" in
    401|403)
      echo "lockout-ok"
      break
      ;;
  esac
  if [ "$(date +%s)" -ge "$deadline" ]; then
    die "guest still had access after host-share end (last status ${post_status})"
  fi
  sleep 2
done

banner "host-share lifecycle smoke passed"
echo "policy=end-session-lockout"
