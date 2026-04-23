#!/usr/bin/env bash
# Agentless host-share smoke:
#   Hetzner shell authenticates as a guest directly against Convex,
#   redeems a host-share invite created for a live host device,
#   proves the host advertises Codex to the guest,
#   then drives the brokered terminal over websocket and writes/runs
#   hello_yaver.py on the host machine.
#
# This is intentionally NOT a full "Codex edits the repo via /tasks"
# verification yet, because the current host-share runtime still gates
# guests to terminal + workspace + file-bus surfaces, not /tasks.
set -euo pipefail

REPO="${REPO:-/opt/yaver}"
LOG_DIR="${HOST_SHARE_SMOKE_LOG_DIR:-/var/log/yaver-ci}"
mkdir -p "$LOG_DIR"
LOG="$LOG_DIR/host-share-agentless.log"
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
    if part == "":
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

cleanup() {
  if [ -n "${HOST_SHARE_SESSION_ID:-}" ] && [ -n "${HOST_TOKEN:-}" ]; then
    curl -fsS -X POST "${CONVEX_SITE_URL}/host-share/end" \
      -H "Authorization: Bearer ${HOST_TOKEN}" \
      -H 'Content-Type: application/json' \
      -d "{\"sessionId\":\"${HOST_SHARE_SESSION_ID}\"}" >/dev/null 2>&1 || true
  fi
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

rand() { head -c16 /dev/urandom | od -An -tx1 | tr -d ' \n'; }
HOST_SHARE_EXPECT_RUNNER="${HOST_SHARE_EXPECT_RUNNER:-codex}"
HOST_SHARE_GUEST_EMAIL="${HOST_SHARE_GUEST_EMAIL:-hostshare-gh-$(rand)@yaver.test}"
HOST_SHARE_GUEST_PASSWORD="${HOST_SHARE_GUEST_PASSWORD:-$(rand)}"
HOST_SHARE_HOST_NAME="${HOST_SHARE_HOST_NAME:-Host Smoke User}"
HOST_SHARE_GUEST_NAME="${HOST_SHARE_GUEST_NAME:-Hetzner Guest Smoke}"

banner "authenticate host and guest"
HOST_TOKEN="$(auth_token "$HOST_SHARE_HOST_EMAIL" "$HOST_SHARE_HOST_PASSWORD" "$HOST_SHARE_HOST_NAME")"
GUEST_TOKEN="$(auth_token "$HOST_SHARE_GUEST_EMAIL" "$HOST_SHARE_GUEST_PASSWORD" "$HOST_SHARE_GUEST_NAME")"
echo "host auth ok"
echo "guest auth ok (${HOST_SHARE_GUEST_EMAIL})"

banner "create host-share invite"
create_payload="$(cat <<JSON
{
  "guestEmail": "${HOST_SHARE_GUEST_EMAIL}",
  "label": "agentless-hetzner-smoke",
  "hostDeviceId": "${HOST_SHARE_HOST_DEVICE_ID}",
  "inviteTtlMinutes": 30,
  "sessionTtlMinutes": 60,
  "idleTimeoutMinutes": 15,
  "toolingPreset": "all-coding-tools",
  "resourcePreset": "balanced",
  "allowInfra": true,
  "allowTerminal": true,
  "allowTunnel": false,
  "useHostAgentTools": true,
  "useHostInfra": true,
  "allowedRunners": ["${HOST_SHARE_EXPECT_RUNNER}"]
}
JSON
)"
create_resp="$(curl -fsS -X POST "${CONVEX_SITE_URL}/host-share/create" \
  -H "Authorization: Bearer ${HOST_TOKEN}" \
  -H 'Content-Type: application/json' \
  -d "$create_payload")"
HOST_SHARE_CODE="$(printf '%s' "$create_resp" | json_get "inviteCode")"
[ -n "$HOST_SHARE_CODE" ] || die "host-share create failed: $create_resp"
echo "invite created"

banner "guest joins invite"
join_resp="$(curl -fsS -X POST "${CONVEX_SITE_URL}/host-share/join" \
  -H "Authorization: Bearer ${GUEST_TOKEN}" \
  -H 'Content-Type: application/json' \
  -d "{\"code\":\"${HOST_SHARE_CODE}\"}")"
HOST_SHARE_SESSION_ID="$(printf '%s' "$join_resp" | json_get "sessionId")"
[ -n "$HOST_SHARE_SESSION_ID" ] || die "host-share join failed: $join_resp"
echo "join ok"

banner "guest sees host runners"
runners_resp="$(curl -fsS "${HOST_SHARE_HOST_BASE_URL}/agent/runners" \
  -H "Authorization: Bearer ${GUEST_TOKEN}")"
RUNNERS_RESP="$runners_resp" python3 - "$HOST_SHARE_EXPECT_RUNNER" <<'PY'
import json, os, sys
want = sys.argv[1]
data = json.loads(os.environ["RUNNERS_RESP"])
runners = data.get("runners") or []
ids = [str(item.get("id", "")) for item in runners if isinstance(item, dict)]
if want not in ids:
    raise SystemExit(f"expected runner {want!r}, got {ids!r}")
print("runner-ok", ids)
PY

banner "guest cannot escalate host-managed access"
guest_cfg_get="$(curl -sS -o /tmp/hostshare_guest_cfg_get.json -w '%{http_code}' \
  "${HOST_SHARE_HOST_BASE_URL}/guests/config" \
  -H "Authorization: Bearer ${GUEST_TOKEN}")"
[ "$guest_cfg_get" = "403" ] || die "guest GET /guests/config expected 403, got ${guest_cfg_get}"

guest_cfg_post="$(curl -sS -o /tmp/hostshare_guest_cfg_post.json -w '%{http_code}' \
  -X POST "${HOST_SHARE_HOST_BASE_URL}/guests/config" \
  -H "Authorization: Bearer ${GUEST_TOKEN}" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"${HOST_SHARE_GUEST_EMAIL}\",\"allowedRunners\":[\"claude\",\"codex\"],\"requireIsolation\":false,\"allowTunnelForward\":true}")"
[ "$guest_cfg_post" = "403" ] || die "guest POST /guests/config expected 403, got ${guest_cfg_post}"

guest_exec_post="$(curl -sS -o /tmp/hostshare_guest_exec.json -w '%{http_code}' \
  -X POST "${HOST_SHARE_HOST_BASE_URL}/exec" \
  -H "Authorization: Bearer ${GUEST_TOKEN}" \
  -H 'Content-Type: application/json' \
  -d '{"cmd":"echo should-not-run"}')"
case "$guest_exec_post" in
  401|403) ;;
  *) die "guest POST /exec expected 401/403, got ${guest_exec_post}" ;;
esac
echo "escalation-blocked"

banner "brokered terminal writes hello world on the host"
if [ -n "${HOST_SHARE_TERMINAL_SMOKE_BIN:-}" ]; then
  smoke_cmd="$HOST_SHARE_TERMINAL_SMOKE_BIN"
elif command -v hostshare-terminal-smoke >/dev/null 2>&1; then
  smoke_cmd="$(command -v hostshare-terminal-smoke)"
elif command -v go >/dev/null 2>&1; then
  cd "$REPO/desktop/agent"
  smoke_cmd="go run ./cmd/hostshare-terminal-smoke"
else
  die "hostshare-terminal-smoke binary not found and go is not installed"
fi

$smoke_cmd \
  --base-url "$HOST_SHARE_HOST_BASE_URL" \
  --token "$GUEST_TOKEN" \
  --expect "hello yaver" \
  --command "mkdir -p host-share-smoke && cd host-share-smoke && printf 'print(\"hello yaver\")\n' > hello_yaver.py && python3 hello_yaver.py && (${HOST_SHARE_EXPECT_RUNNER} --version || true) && exit"

banner "host-share agentless smoke passed"
echo "guest=${HOST_SHARE_GUEST_EMAIL}"
echo "guest_account_cleanup=delete-account-on-exit"
echo "policy=terminal-allowed runners=${HOST_SHARE_EXPECT_RUNNER}"
