#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="${REPO_DIR:-/opt/yaver}"
CONVEX_SITE_URL="${CONVEX_SITE_URL:-https://shocking-echidna-394.eu-west-1.convex.site}"
MODEL="${MODEL:-qwen2.5-coder:1.5b}"
export PATH="/usr/local/go/bin:$PATH"
LOG_DIR="/var/log/yaver-ci"
mkdir -p "$LOG_DIR"
LOG_FILE="${LOG_DIR}/verify-ops-ollama.log"
exec > >(tee -a "$LOG_FILE") 2>&1

tmp_dir="$(mktemp -d -t yaver-ops-ollama-XXXXXX)"
agent_pid=""
started_ollama=false

cleanup() {
  if [ -n "$agent_pid" ] && kill -0 "$agent_pid" 2>/dev/null; then
    kill "$agent_pid" 2>/dev/null || true
    wait "$agent_pid" 2>/dev/null || true
  fi
  if $started_ollama; then
    pkill -f "^ollama serve$" 2>/dev/null || true
  fi
  rm -rf "$tmp_dir"
}
trap cleanup EXIT

log() {
  printf '\033[36m[ops-ollama]\033[0m %s\n' "$*"
}

fail() {
  printf '\033[31m[ops-ollama FAIL]\033[0m %s\n' "$*" >&2
  exit 1
}

get_free_port() {
  python3 -c "import socket; s=socket.socket(); s.bind((\"\",0)); print(s.getsockname()[1]); s.close()"
}

gen_uuid() {
  uuidgen 2>/dev/null || python3 -c 'import uuid;print(uuid.uuid4())' | tr '[:upper:]' '[:lower:]'
}

ensure_ollama() {
  if ! curl -fsS http://127.0.0.1:11434/api/tags >/dev/null 2>&1; then
    log "starting ollama daemon"
    ollama serve >/tmp/yaver-ops-ollama.log 2>&1 &
    started_ollama=true
    for _ in $(seq 1 20); do
      if curl -fsS http://127.0.0.1:11434/api/tags >/dev/null 2>&1; then
        break
      fi
      sleep 1
    done
  fi
  curl -fsS http://127.0.0.1:11434/api/tags >/dev/null 2>&1 \
    || fail "ollama daemon never became ready"
  if ! ollama list 2>/dev/null | grep -q "^${MODEL}[[:space:]]"; then
    log "pulling ${MODEL}"
    ollama pull "$MODEL"
  fi
}

get_ci_token() {
  local email password fullname resp token
  email="${CI_TEST_EMAIL:-ci-test@yaver.io}"
  password="${CI_TEST_PASSWORD:-ciTestPass2026!}"
  fullname="${CI_TEST_FULLNAME:-CI Test User}"

  resp="$(curl -sf -X POST "${CONVEX_SITE_URL}/auth/login" \
    -H "Content-Type: application/json" \
    -d "{\"email\":\"${email}\",\"password\":\"${password}\"}" 2>/dev/null || true)"
  token="$(echo "$resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get(\"token\",\"\"))" 2>/dev/null || true)"
  if [ -n "$token" ]; then
    printf '%s\n' "$token"
    return 0
  fi

  resp="$(curl -sf -X POST "${CONVEX_SITE_URL}/auth/signup" \
    -H "Content-Type: application/json" \
    -d "{\"email\":\"${email}\",\"fullName\":\"${fullname}\",\"password\":\"${password}\"}" 2>/dev/null || true)"
  token="$(echo "$resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get(\"token\",\"\"))" 2>/dev/null || true)"
  [ -n "$token" ] || return 1
  printf '%s\n' "$token"
}

build_agent() {
  local output="$1"
  (cd "${REPO_DIR}/desktop/agent" && go build -o "$output" .)
}

start_agent() {
  local binary="$1" port="$2" quic_port="$3" token="$4" device_id="$5" work_dir="$6"
  local config_dir
  config_dir="${work_dir}/.yaver-config"
  mkdir -p "${config_dir}/.yaver" "$work_dir"
  cat > "${config_dir}/.yaver/config.json" <<EOF
{
  "auth_token": "${token}",
  "device_id": "${device_id}",
  "convex_site_url": "${CONVEX_SITE_URL}"
}
EOF

  HOME="$config_dir" CLAUDECODE= "$binary" serve --debug \
    --port "$port" --quic-port "$quic_port" --work-dir "$work_dir" --no-relay \
    > "${work_dir}/agent.log" 2>&1 &
  agent_pid=$!

  for _ in $(seq 1 20); do
    if curl -fsS "http://127.0.0.1:${port}/health" >/dev/null 2>&1; then
      return 0
    fi
    if ! kill -0 "$agent_pid" 2>/dev/null; then
      tail -n 40 "${work_dir}/agent.log" >&2 || true
      fail "agent exited before becoming healthy"
    fi
    sleep 1
  done

  tail -n 40 "${work_dir}/agent.log" >&2 || true
  fail "agent did not become healthy"
}

log "repo dir: ${REPO_DIR}"
ensure_ollama

token="$(get_ci_token)" || fail "could not obtain CI auth token from ${CONVEX_SITE_URL}"
http_port="$(get_free_port)"
quic_port="$(get_free_port)"
device_id="test-ops-ollama-$(gen_uuid)"
work_dir="${tmp_dir}/agent-work"
agent_bin="$(command -v yaver || true)"

if [ -z "$agent_bin" ]; then
  agent_bin="${tmp_dir}/yaver"
  log "building agent binary"
  build_agent "$agent_bin"
else
  log "using installed yaver binary: ${agent_bin}"
fi
log "starting agent on ${http_port}"
start_agent "$agent_bin" "$http_port" "$quic_port" "$token" "$device_id" "$work_dir"

resp_file="${tmp_dir}/ops-response.json"
curl -sf -X POST "http://127.0.0.1:${http_port}/ops" \
  -H "Authorization: Bearer ${token}" \
  -H "Content-Type: application/json" \
  -d "{\"machine\":\"local\",\"verb\":\"run\",\"payload\":{\"command\":\"OLLAMA_NOPROGRESS=1 ollama run ${MODEL} 'Respond with exactly: hello yaver' </dev/null\",\"timeoutSec\":360}}" \
  > "$resp_file" || fail "POST /ops failed"

python3 - "$resp_file" <<'PY' || {
import json, sys
path = sys.argv[1]
data = json.load(open(path))
if not data.get("ok"):
    raise SystemExit(f"ops call failed: {data}")
initial = data.get("initial") or {}
exit_code = initial.get("exitCode")
stdout = initial.get("stdout", "")
stderr = initial.get("stderr", "")
if exit_code != 0:
    raise SystemExit(f"ops run exitCode={exit_code} stderr={stderr!r}")
if "hello yaver" not in stdout.lower():
    raise SystemExit(f"stdout missing hello yaver: {stdout!r}")
print("ops.run returned exitCode=0 and stdout contained hello yaver")
PY
  tail -n 40 "${work_dir}/agent.log" >&2 || true
  fail "ops smoke validation failed"
}

printf '\033[32m[ops-ollama PASS]\033[0m %s\n' "POST /ops run reached local ollama successfully"
