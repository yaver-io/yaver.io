#!/usr/bin/env bash
set -euo pipefail

# Local yaver-to-yaver harness:
# - starts a target agent on localhost:18080
# - optionally starts a controller agent on another port
# - uses the controller HOME/config to run real `yaver ... --to <device>`
#   commands against the target
#
# This exercises Yaver's own remote-control surface locally without
# GitHub Actions or external SSH infra.
#
# Usage:
#   ./scripts/test-yaver-to-yaver-local.sh
#   ./scripts/test-yaver-to-yaver-local.sh autoinit
#   ./scripts/test-yaver-to-yaver-local.sh autoideas
#   ./scripts/test-yaver-to-yaver-local.sh autodev
#
# Environment:
#   RUNNER_SPEC=claude|codex|hybrid|...
#   MODEL_SPEC=sonnet|opus|...            (optional; forwarded where supported)
#   PLANNER_SPEC=claude:opus|codex        (optional; hybrid)
#   IMPLEMENTER_SPEC=aider-ollama|codex   (optional; hybrid)
#   ANTHROPIC_API_KEY / OPENAI_API_KEY    optional depending on chosen runner
#   YAVER_BIN                             optional prebuilt binary

MODE="${1:-autoinit}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
YAVER_BIN="${YAVER_BIN:-$ROOT_DIR/desktop/agent/yaver}"
CONVEX_SITE_URL="${CONVEX_SITE_URL:-https://shocking-echidna-394.eu-west-1.convex.site}"
RUNNER_SPEC="${RUNNER_SPEC:-}"
MODEL_SPEC="${MODEL_SPEC:-}"
PLANNER_SPEC="${PLANNER_SPEC:-}"
IMPLEMENTER_SPEC="${IMPLEMENTER_SPEC:-}"
CI_TEST_EMAIL="${CI_TEST_EMAIL:-ci-test@yaver.io}"
CI_TEST_PASSWORD="${CI_TEST_PASSWORD:-ciTestPass2026!}"
CI_TEST_FULLNAME="${CI_TEST_FULLNAME:-CI Test User}"
WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/yaver-peer-local-XXXXXX")"
TARGET_HOME="$WORK_DIR/target-home"
CONTROLLER_HOME="$WORK_DIR/controller-home"
FIXTURE_DIR="$WORK_DIR/fixture"
TARGET_PID=""
CONTROLLER_PID=""

RED='\033[0;31m'
GREEN='\033[0;32m'
CYAN='\033[0;36m'
NC='\033[0m'

log() { echo -e "${CYAN}[peer-local]${NC} $*"; }
pass() { echo -e "${GREEN}[peer-local PASS]${NC} $*"; }
fail() { echo -e "${RED}[peer-local FAIL]${NC} $*" >&2; exit 1; }

cleanup() {
  if [ -n "$CONTROLLER_PID" ]; then
    kill "$CONTROLLER_PID" 2>/dev/null || true
    wait "$CONTROLLER_PID" 2>/dev/null || true
  fi
  if [ -n "$TARGET_PID" ]; then
    kill "$TARGET_PID" 2>/dev/null || true
    wait "$TARGET_PID" 2>/dev/null || true
  fi
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT

need_prereqs() {
  command -v python3 >/dev/null 2>&1 || fail "python3 not installed"
  command -v curl >/dev/null 2>&1 || fail "curl not installed"
  command -v git >/dev/null 2>&1 || fail "git not installed"
  if [ ! -x "$YAVER_BIN" ]; then
    log "building yaver binary..."
    (cd "$ROOT_DIR/desktop/agent" && go build -o yaver .) || fail "failed to build yaver"
  fi
}

get_ci_token() {
  local resp token
  resp="$(curl -sf -X POST "${CONVEX_SITE_URL}/auth/login" \
    -H "Content-Type: application/json" \
    -d "{\"email\":\"${CI_TEST_EMAIL}\",\"password\":\"${CI_TEST_PASSWORD}\"}" 2>/dev/null)" || true
  token="$(echo "$resp" | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])" 2>/dev/null)" || true
  if [ -n "$token" ]; then
    printf '%s' "$token"
    return 0
  fi
  resp="$(curl -sf -X POST "${CONVEX_SITE_URL}/auth/signup" \
    -H "Content-Type: application/json" \
    -d "{\"email\":\"${CI_TEST_EMAIL}\",\"fullName\":\"${CI_TEST_FULLNAME}\",\"password\":\"${CI_TEST_PASSWORD}\"}" 2>/dev/null)" || true
  token="$(echo "$resp" | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])" 2>/dev/null)" || true
  [ -n "$token" ] || fail "could not obtain CI token from Convex"
  printf '%s' "$token"
}

make_fixture() {
  mkdir -p "$FIXTURE_DIR"
  cd "$FIXTURE_DIR"
  git init -q .
  git config user.email "peer-local@example.com"
  git config user.name "peer-local"
  mkdir -p src
  cat > README.md <<'EOF'
# Peer local fixture

Tiny throwaway repo used for local yaver-to-yaver validation.
EOF
  cat > package.json <<'EOF'
{"name":"peer-local-fixture","version":"0.0.1","main":"src/index.js"}
EOF
  cat > src/index.js <<'EOF'
export function greet(name) {
  return `Hello, ${name}!`;
}
EOF
  cat > remained.md <<'EOF'
- [ ] add a second exported helper named `farewell(name)` in src/index.js that returns `Bye, ${name}!`
EOF
  git add . >/dev/null
  git commit -q -m "init"
}

write_config() {
  local home="$1" token="$2" device_id="$3"
  mkdir -p "$home/.yaver"
  cat > "$home/.yaver/config.json" <<EOF
{
  "auth_token": "$token",
  "device_id": "$device_id",
  "convex_site_url": "$CONVEX_SITE_URL"
}
EOF
}

start_agent() {
  local home="$1" port="$2" quic_port="$3" token="$4" device_id="$5" log_file="$6"
  write_config "$home" "$token" "$device_id"
  HOME="$home" "$YAVER_BIN" serve --debug --port "$port" --quic-port "$quic_port" --work-dir "$WORK_DIR/work-$device_id" --no-relay >"$log_file" 2>&1 &
  local pid="$!"
  for _ in $(seq 1 30); do
    if curl -sf "http://127.0.0.1:${port}/health" >/dev/null 2>&1; then
      printf '%s' "$pid"
      return 0
    fi
    sleep 1
  done
  tail -60 "$log_file" >&2 || true
  fail "agent on port ${port} did not become healthy"
}

wait_device_online() {
  local home="$1" want="$2"
  for _ in $(seq 1 40); do
    if HOME="$home" "$YAVER_BIN" devices 2>/dev/null | grep -q "$want"; then
      return 0
    fi
    sleep 1
  done
  return 1
}

poll_autoinit_file() {
  for _ in $(seq 1 60); do
    if [ -f "$FIXTURE_DIR/init.md" ] && grep -q "yaver:autoinit:generated:start" "$FIXTURE_DIR/init.md"; then
      return 0
    fi
    sleep 2
  done
  return 1
}

poll_autoideas_file() {
  for _ in $(seq 1 60); do
    if [ -f "$FIXTURE_DIR/ideas.md" ] && grep -q -- "- \\[ \\]" "$FIXTURE_DIR/ideas.md"; then
      return 0
    fi
    sleep 5
  done
  return 1
}

poll_autodev_effect() {
  for _ in $(seq 1 80); do
    if grep -q "farewell" "$FIXTURE_DIR/src/index.js"; then
      return 0
    fi
    sleep 5
  done
  return 1
}

capture_remote_stream_snippet() {
  local target_hint="$1"
  local stream_name="$2"
  local out_file="$3"
  HOME="$CONTROLLER_HOME" python3 - "$YAVER_BIN" "$target_hint" "$stream_name" "$out_file" <<'PY'
import subprocess
import sys

bin_path, target_hint, stream_name, out_file = sys.argv[1:5]
cmd = [bin_path, "stream", "--to", target_hint, stream_name]
try:
    proc = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    out, _ = proc.communicate(timeout=8)
except subprocess.TimeoutExpired:
    proc.kill()
    out, _ = proc.communicate()

with open(out_file, "w", encoding="utf-8") as fh:
    fh.write(out)
PY
}

assert_human_stream_output() {
  local out_file="$1"
  if grep -q '"type":' "$out_file"; then
    fail "stream output leaked raw JSON events"
  fi
  if ! grep -Eq '\[yaver\]|done ·|tailing autodev:' "$out_file"; then
    fail "stream output did not look like a human transcript"
  fi
}

run_remote_autoinit() {
  local target_hint="$1"
  log "controller -> target autoinit"
  local cmd=( "$YAVER_BIN" autoinit fixture --to "$target_hint" )
  if [ -n "$RUNNER_SPEC" ]; then
    cmd+=( --runner "$RUNNER_SPEC" )
  fi
  if [ -n "$MODEL_SPEC" ]; then
    cmd+=( --model "$MODEL_SPEC" )
  fi
  (cd "$FIXTURE_DIR" && HOME="$CONTROLLER_HOME" "${cmd[@]}") || fail "remote autoinit command failed"
  poll_autoinit_file || fail "remote autoinit did not produce init.md"
  grep -q "## What is this" "$FIXTURE_DIR/init.md" || fail "init.md missing expected section"
  pass "remote autoinit completed through yaver-to-yaver"
}

run_remote_autoideas() {
  local target_hint="$1"
  log "controller -> target autoideas"
  local cmd=( "$YAVER_BIN" autoideas fixture --to "$target_hint" --hours 1 --max-batches 1 --tick 60 )
  if [ -n "$RUNNER_SPEC" ]; then
    cmd+=( --runner "$RUNNER_SPEC" )
  fi
  (cd "$FIXTURE_DIR" && HOME="$CONTROLLER_HOME" "${cmd[@]}") || fail "remote autoideas command failed"
  poll_autoideas_file || fail "remote autoideas did not produce ideas.md"
  pass "remote autoideas completed through yaver-to-yaver"
}

run_remote_autodev() {
  local target_hint="$1"
  log "controller -> target autodev"
  local cmd=( "$YAVER_BIN" autodev fixture --to "$target_hint" --hours 1 --max-iterations 1 --no-autotest --remained remained.md )
  if [ -n "$RUNNER_SPEC" ]; then
    cmd+=( --runner "$RUNNER_SPEC" )
  fi
  if [ -n "$MODEL_SPEC" ]; then
    cmd+=( --model "$MODEL_SPEC" )
  fi
  if [ -n "$PLANNER_SPEC" ]; then
    cmd+=( --planner "$PLANNER_SPEC" )
  fi
  if [ -n "$IMPLEMENTER_SPEC" ]; then
    cmd+=( --implementer "$IMPLEMENTER_SPEC" )
  fi
  (cd "$FIXTURE_DIR" && HOME="$CONTROLLER_HOME" "${cmd[@]}") || fail "remote autodev command failed"
  poll_autodev_effect || fail "remote autodev did not modify fixture as expected"
  capture_remote_stream_snippet "$target_hint" "fixture-autodev" "$WORK_DIR/stream.txt"
  assert_human_stream_output "$WORK_DIR/stream.txt"
  pass "remote autodev completed through yaver-to-yaver"
}

need_prereqs
TOKEN="$(get_ci_token)"
TARGET_ID="peer-target-$(python3 - <<'PY'
import uuid
print(uuid.uuid4())
PY
)"
CONTROLLER_ID="peer-controller-$(python3 - <<'PY'
import uuid
print(uuid.uuid4())
PY
)"
make_fixture

TARGET_PID="$(start_agent "$TARGET_HOME" 18080 4433 "$TOKEN" "$TARGET_ID" "$WORK_DIR/target.log")"
CONTROLLER_PID="$(start_agent "$CONTROLLER_HOME" 18081 4434 "$TOKEN" "$CONTROLLER_ID" "$WORK_DIR/controller.log")"
wait_device_online "$CONTROLLER_HOME" "${TARGET_ID%%-*}" || fail "controller never saw target in device inventory"

case "$MODE" in
  autoinit) run_remote_autoinit "${TARGET_ID%%-*}" ;;
  autoideas) run_remote_autoideas "${TARGET_ID%%-*}" ;;
  autodev) run_remote_autodev "${TARGET_ID%%-*}" ;;
  *)
    fail "unknown mode: $MODE (use autoinit|autoideas|autodev)"
    ;;
esac
