#!/usr/bin/env bash
set -euo pipefail

MODE="${1:-reauth}"

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
AGENT_DIR="$REPO_ROOT/desktop/agent"
WORK_DIR="$(mktemp -d)"

CONVEX_SITE_URL="${CONVEX_SITE_URL:-https://shocking-echidna-394.eu-west-1.convex.site}"
RELAY_HTTP_URL="${RELAY_HTTP_URL:-}"
CI_TEST_EMAIL="${CI_TEST_EMAIL:-ci-test@yaver.io}"
CI_TEST_PASSWORD="${CI_TEST_PASSWORD:-ciTestPass2026!}"
CI_TEST_FULLNAME="${CI_TEST_FULLNAME:-CI Test User}"

PRIMARY_HOST="${YAVER_CI_SSH_HOST_PRIMARY:-}"
SECONDARY_HOST="${YAVER_CI_SSH_HOST_SECONDARY:-}"
SSH_USER="${YAVER_CI_SSH_USER:-root}"
SSH_PORT="${YAVER_CI_SSH_PORT:-22}"

LOCAL_PID=""
LOCAL_HOME=""

declare -A BUILT_BINARIES=()
declare -A REMOTE_ROOTS=()
declare -A REMOTE_PORTS=()
declare -A DEVICE_IDS=()

log() {
  printf '[remote-infra] %s\n' "$*"
}

die() {
  printf '[remote-infra] ERROR: %s\n' "$*" >&2
  exit 1
}

require_env() {
  local name="$1"
  local value="${!name:-}"
  [ -n "$value" ] || die "missing env: $name"
}

ssh_base() {
  printf '%s' "-p $SSH_PORT -o BatchMode=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=$HOME/.ssh/known_hosts"
}

ssh_run() {
  local host="$1"
  shift
  ssh $(ssh_base) "$SSH_USER@$host" "$@"
}

scp_to() {
  local src="$1"
  local host="$2"
  local dst="$3"
  scp $(ssh_base) "$src" "$SSH_USER@$host:$dst"
}

json_get() {
  local key="$1"
  python3 - "$key" <<'PY'
import json, sys
key = sys.argv[1]
data = json.load(sys.stdin)
value = data
for part in key.split("."):
    if isinstance(value, dict):
        value = value.get(part)
    else:
        value = None
        break
if value is None:
    sys.exit(1)
if isinstance(value, bool):
    print("true" if value else "false")
elif isinstance(value, (dict, list)):
    print(json.dumps(value))
else:
    print(value)
PY
}

fetch_ci_token() {
  local token
  token="$(
    curl -sf -X POST "$CONVEX_SITE_URL/auth/login" \
      -H "Content-Type: application/json" \
      -d "{\"email\":\"$CI_TEST_EMAIL\",\"password\":\"$CI_TEST_PASSWORD\"}" \
      | python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])' 2>/dev/null || true
  )"
  if [ -z "$token" ]; then
    token="$(
      curl -sf -X POST "$CONVEX_SITE_URL/auth/signup" \
        -H "Content-Type: application/json" \
        -d "{\"email\":\"$CI_TEST_EMAIL\",\"fullName\":\"$CI_TEST_FULLNAME\",\"password\":\"$CI_TEST_PASSWORD\"}" \
        | python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])' 2>/dev/null || true
    )"
  fi
  [ -n "$token" ] || die "could not acquire CI auth token"
  printf '%s' "$token"
}

resolve_relay_url() {
  if [ -n "$RELAY_HTTP_URL" ]; then
    printf '%s' "${RELAY_HTTP_URL%/}"
    return
  fi
  local relay
  relay="$(
    curl -sf "$CONVEX_SITE_URL/config" \
      | python3 -c 'import json,sys; data=json.load(sys.stdin); relays=data.get("relayServers") or []; print((relays[0].get("httpUrl") if relays else ""))' 2>/dev/null || true
  )"
  [ -n "$relay" ] || die "could not resolve relay URL from Convex config"
  printf '%s' "${relay%/}"
}

random_id() {
  python3 - <<'PY'
import secrets
print(secrets.token_hex(6))
PY
}

remote_goarch() {
  local host="$1"
  local arch
  arch="$(ssh_run "$host" "uname -m")"
  case "$arch" in
    x86_64|amd64) printf '%s' "amd64" ;;
    aarch64|arm64) printf '%s' "arm64" ;;
    *) die "unsupported remote arch on $host: $arch" ;;
  esac
}

ensure_built_binary() {
  local host="$1"
  local goarch
  goarch="$(remote_goarch "$host")"
  if [ -n "${BUILT_BINARIES[$goarch]:-}" ] && [ -f "${BUILT_BINARIES[$goarch]}" ]; then
    printf '%s' "${BUILT_BINARIES[$goarch]}"
    return
  fi
  local out="$WORK_DIR/yaver-linux-$goarch"
  log "building linux/$goarch agent binary"
  (
    cd "$AGENT_DIR"
    GOOS=linux GOARCH="$goarch" CGO_ENABLED=0 go build -o "$out" .
  )
  BUILT_BINARIES[$goarch]="$out"
  printf '%s' "$out"
}

prepare_remote_root() {
  local host="$1"
  local label="$2"
  local suffix
  suffix="$(random_id)"
  local root="/tmp/yaver-ci-${label}-${suffix}"
  REMOTE_ROOTS[$host]="$root"
  printf '%s' "$root"
}

upload_remote_config() {
  local host="$1"
  local device_id="$2"
  local token="$3"
  local root="${REMOTE_ROOTS[$host]}"
  local cfg="$WORK_DIR/${device_id}.json"
  python3 - "$cfg" "$token" "$device_id" "$CONVEX_SITE_URL" <<'PY'
import json, sys
path, token, device_id, convex = sys.argv[1:]
with open(path, "w", encoding="utf-8") as fh:
    json.dump({
        "auth_token": token,
        "device_id": device_id,
        "convex_site_url": convex,
    }, fh, indent=2)
PY
  scp_to "$cfg" "$host" "$root/home/.yaver/config.json"
}

start_remote_agent() {
  local host="$1"
  local label="$2"
  local device_id="$3"
  local token="$4"
  local http_port="$5"
  local quic_port="$6"
  local binary
  binary="$(ensure_built_binary "$host")"
  local root
  root="${REMOTE_ROOTS[$host]:-}"
  if [ -z "$root" ]; then
    root="$(prepare_remote_root "$host" "$label")"
  fi
  REMOTE_PORTS[$host]="$http_port"
  DEVICE_IDS[$host]="$device_id"
  ssh_run "$host" "mkdir -p '$root/bin' '$root/home/.yaver' '$root/work'"
  scp_to "$binary" "$host" "$root/bin/yaver"
  upload_remote_config "$host" "$device_id" "$token"
  ssh_run "$host" "chmod +x '$root/bin/yaver'"
  ssh_run "$host" "sh -lc 'cd \"$root\" && nohup env HOME=\"$root/home\" \"$root/bin/yaver\" serve --debug --port $http_port --quic-port $quic_port --work-dir \"$root/work\" --dummy > \"$root/agent.log\" 2>&1 < /dev/null & echo \$! > \"$root/agent.pid\"'"
}

stop_remote_agent() {
  local host="$1"
  local root="${REMOTE_ROOTS[$host]:-}"
  [ -n "$root" ] || return 0
  ssh_run "$host" "sh -lc 'if [ -f \"$root/agent.pid\" ]; then kill \$(cat \"$root/agent.pid\") 2>/dev/null || true; fi'" || true
}

rewrite_remote_token() {
  local host="$1"
  local token="$2"
  local root="${REMOTE_ROOTS[$host]}"
  ssh_run "$host" "python3 -c 'import json, pathlib; p=pathlib.Path(\"$root/home/.yaver/config.json\"); d=json.loads(p.read_text()); d[\"auth_token\"]=\"$token\"; p.write_text(json.dumps(d, indent=2))'"
}

remote_cached_relay_count() {
  local host="$1"
  local root="${REMOTE_ROOTS[$host]}"
  ssh_run "$host" "python3 -c 'import json, pathlib; d=json.loads(pathlib.Path(\"$root/home/.yaver/config.json\").read_text()); print(len(d.get(\"cached_relay_servers\") or []))'"
}

remote_token_matches() {
  local host="$1"
  local expected="$2"
  local root="${REMOTE_ROOTS[$host]}"
  ssh_run "$host" "python3 -c 'import json, pathlib, sys; d=json.loads(pathlib.Path(\"$root/home/.yaver/config.json\").read_text()); sys.exit(0 if d.get(\"auth_token\") == \"$expected\" else 1)'"
}

wait_for_relay_health() {
  local device_id="$1"
  local needle="$2"
  local timeout="${3:-60}"
  local url="$RELAY_HTTP_URL/d/$device_id/health"
  local start
  start="$(date +%s)"
  while true; do
    local body
    body="$(curl -sf "$url" 2>/dev/null || true)"
    if [ -n "$body" ] && printf '%s' "$body" | grep -q "$needle"; then
      return 0
    fi
    if [ $(( $(date +%s) - start )) -ge "$timeout" ]; then
      printf '%s\n' "$body" >&2
      return 1
    fi
    sleep 2
  done
}

wait_for_relay_up() {
  local device_id="$1"
  local timeout="${2:-90}"
  local url="$RELAY_HTTP_URL/d/$device_id/health"
  local start
  start="$(date +%s)"
  while true; do
    if curl -sf "$url" >/dev/null 2>&1; then
      return 0
    fi
    if [ $(( $(date +%s) - start )) -ge "$timeout" ]; then
      return 1
    fi
    sleep 2
  done
}

perform_recovery() {
  local device_id="$1"
  local token="$2"
  local resp pair_code
  resp="$(
    curl -sf -X POST "$RELAY_HTTP_URL/d/$device_id/auth/recover" \
      -H "Authorization: Bearer $token" \
      -H "Content-Type: application/json" \
      -d '{"mode":"pair"}'
  )"
  pair_code="$(printf '%s' "$resp" | python3 -c 'import json,sys; print(json.load(sys.stdin)["pairCode"])')"
  [ -n "$pair_code" ] || die "recover did not return a pair code"
  curl -sf -X POST "$RELAY_HTTP_URL/d/$device_id/auth/pair/submit?code=$pair_code" \
    -H "Content-Type: application/json" \
    -d "{\"token\":\"$token\",\"convexSiteUrl\":\"$CONVEX_SITE_URL\"}" >/dev/null
}

cleanup() {
  if [ -n "$LOCAL_PID" ]; then
    kill "$LOCAL_PID" 2>/dev/null || true
    wait "$LOCAL_PID" 2>/dev/null || true
  fi
  for host in "${!REMOTE_ROOTS[@]}"; do
    stop_remote_agent "$host"
    ssh_run "$host" "rm -rf '${REMOTE_ROOTS[$host]}'" >/dev/null 2>&1 || true
  done
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT

run_reauth() {
  require_env PRIMARY_HOST
  local token
  token="$(fetch_ci_token)"
  RELAY_HTTP_URL="$(resolve_relay_url)"
  local device_id="ci-reauth-$(random_id)"
  local http_port="19080"
  local quic_port="19443"

  log "staging remote agent on $PRIMARY_HOST"
  start_remote_agent "$PRIMARY_HOST" "reauth" "$device_id" "$token" "$http_port" "$quic_port"
  wait_for_relay_up "$device_id" 120 || die "device never came up on relay"

  local cached_count
  cached_count="$(remote_cached_relay_count "$PRIMARY_HOST")"
  [ "${cached_count:-0}" -gt 0 ] || die "agent never cached relay settings locally"

  log "forcing auth-expired state"
  stop_remote_agent "$PRIMARY_HOST"
  rewrite_remote_token "$PRIMARY_HOST" "expired-ci-token"
  start_remote_agent "$PRIMARY_HOST" "reauth" "$device_id" "expired-ci-token" "$http_port" "$quic_port"
  wait_for_relay_health "$device_id" '"authExpired":true' 120 || die "agent did not surface authExpired via relay"

  log "recovering through relay"
  perform_recovery "$device_id" "$token"
  wait_for_relay_up "$device_id" 60 || die "device did not return on relay after recovery"
  if curl -sf "$RELAY_HTTP_URL/d/$device_id/health" | grep -q '"authExpired":true'; then
    die "agent stayed authExpired after recovery"
  fi
  remote_token_matches "$PRIMARY_HOST" "$token" || die "remote config token was not restored"
  log "reauth PASS"
}

wait_for_inventory() {
  local base="$1"
  local token="$2"
  local first="$3"
  local second="$4"
  local start
  start="$(date +%s)"
  while true; do
    local body
    body="$(
      curl -sf "$base/console/machines" \
        -H "Authorization: Bearer $token" 2>/dev/null || true
    )"
    if [ -n "$body" ] && printf '%s' "$body" | grep -q "$first"; then
      if [ -z "$second" ] || printf '%s' "$body" | grep -q "$second"; then
        return 0
      fi
    fi
    if [ $(( $(date +%s) - start )) -ge 120 ]; then
      printf '%s\n' "$body" >&2
      return 1
    fi
    sleep 3
  done
}

run_mesh() {
  require_env PRIMARY_HOST
  require_env SECONDARY_HOST
  local token
  token="$(fetch_ci_token)"
  RELAY_HTTP_URL="$(resolve_relay_url)"

  local primary_device="ci-mesh-a-$(random_id)"
  local secondary_device="ci-mesh-b-$(random_id)"
  start_remote_agent "$PRIMARY_HOST" "mesh-a" "$primary_device" "$token" "19081" "19444"
  start_remote_agent "$SECONDARY_HOST" "mesh-b" "$secondary_device" "$token" "19082" "19445"
  wait_for_relay_up "$primary_device" 120 || die "primary mesh node never came up on relay"
  wait_for_relay_up "$secondary_device" 120 || die "secondary mesh node never came up on relay"

  local local_bin="$WORK_DIR/yaver-local"
  (
    cd "$AGENT_DIR"
    go build -o "$local_bin" .
  )
  LOCAL_HOME="$WORK_DIR/local-home"
  mkdir -p "$LOCAL_HOME/.yaver"
  python3 - "$LOCAL_HOME/.yaver/config.json" "$token" "$CONVEX_SITE_URL" <<'PY'
import json, sys, time
path, token, convex = sys.argv[1:]
with open(path, "w", encoding="utf-8") as fh:
    json.dump({
        "auth_token": token,
        "device_id": f"ci-mesh-local-{int(time.time())}",
        "convex_site_url": convex,
    }, fh, indent=2)
PY

  log "starting local mesh coordinator"
  HOME="$LOCAL_HOME" "$local_bin" serve --debug --port 18080 --quic-port 4433 --work-dir "$WORK_DIR/local-work" --dummy >"$WORK_DIR/local-agent.log" 2>&1 &
  LOCAL_PID="$!"
  wait_for_relay_up "$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["device_id"])' "$LOCAL_HOME/.yaver/config.json")" 120 || die "local mesh coordinator never came up on relay"
  wait_for_inventory "http://127.0.0.1:18080" "$token" "$primary_device" "$secondary_device" || die "local agent never saw both remote mesh nodes"

  log "running mesh smoke against $primary_device"
  HOME="$LOCAL_HOME" "$local_bin" agent mesh-smoke --device "$primary_device"
  log "mesh PASS"
}

case "$MODE" in
  reauth) run_reauth ;;
  mesh) run_mesh ;;
  *) die "usage: $0 [reauth|mesh]" ;;
esac
