#!/usr/bin/env bash
set -euo pipefail

# Local-only real Anthropic / Claude test path.
# This is intentionally NOT for GitHub Actions. It uses the caller's
# local Claude auth / Anthropic API key and drives the real Yaver HTTP
# surface so we test Yaver itself, not just a naked `claude` subprocess.
#
# Modes:
#   autoinit   - POST /autoinit/start and poll /autoinit/status
#   autoideas  - POST /autoideas/start and read /autoideas/file
#
# Usage:
#   ./scripts/test-anthropic-local.sh
#   ./scripts/test-anthropic-local.sh autoinit
#   ./scripts/test-anthropic-local.sh autoideas
#
# Environment:
#   ANTHROPIC_API_KEY  optional if `claude` is already logged in locally
#   CONVEX_SITE_URL    defaults to the shared Yaver deployment
#   YAVER_BIN          optional prebuilt binary
#   CLAUDE_BIN         override runner binary (default: claude)

MODE="${1:-autoinit}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
YAVER_BIN="${YAVER_BIN:-$ROOT_DIR/desktop/agent/yaver}"
CLAUDE_BIN="${CLAUDE_BIN:-claude}"
CONVEX_SITE_URL="${CONVEX_SITE_URL:-https://shocking-echidna-394.eu-west-1.convex.site}"
CI_TEST_EMAIL="${CI_TEST_EMAIL:-ci-test@yaver.io}"
CI_TEST_PASSWORD="${CI_TEST_PASSWORD:-ciTestPass2026!}"
CI_TEST_FULLNAME="${CI_TEST_FULLNAME:-CI Test User}"
WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/yaver-anthropic-local-XXXXXX")"
AGENT_HOME="$WORK_DIR/home"
FIXTURE_DIR="$WORK_DIR/fixture"
AGENT_PID=""

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

log() { echo -e "${CYAN}[anthropic-local]${NC} $*"; }
pass() { echo -e "${GREEN}[anthropic-local PASS]${NC} $*"; }
fail() { echo -e "${RED}[anthropic-local FAIL]${NC} $*" >&2; exit 1; }

cleanup() {
  if [ -n "$AGENT_PID" ]; then
    kill "$AGENT_PID" 2>/dev/null || true
    wait "$AGENT_PID" 2>/dev/null || true
  fi
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT

get_free_port() {
  python3 -c 'import socket; s=socket.socket(); s.bind(("", 0)); print(s.getsockname()[1]); s.close()'
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

need_prereqs() {
  command -v python3 >/dev/null 2>&1 || fail "python3 not installed"
  command -v curl >/dev/null 2>&1 || fail "curl not installed"
  command -v git >/dev/null 2>&1 || fail "git not installed"
  command -v "$CLAUDE_BIN" >/dev/null 2>&1 || fail "$CLAUDE_BIN not on PATH"
  if [ ! -x "$YAVER_BIN" ]; then
    log "building yaver binary..."
    (cd "$ROOT_DIR/desktop/agent" && go build -o yaver .) || fail "failed to build yaver"
  fi
  if [ -z "${ANTHROPIC_API_KEY:-}" ]; then
    log "ANTHROPIC_API_KEY is not set. Assuming $CLAUDE_BIN is already authenticated locally."
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
  git config user.email "autodev@example.com"
  git config user.name "autodev"
  mkdir -p src
  cat > README.md <<'EOF'
# Fixture project for yaver local Anthropic tests

Tiny throwaway repo used to validate Yaver's real local Claude flows.
EOF
  cat > package.json <<'EOF'
{"name":"fixture","version":"0.0.1","main":"src/index.js"}
EOF
  cat > src/index.js <<'EOF'
export function greet(name) {
  return `Hello, ${name}!`;
}
EOF
  git add . >/dev/null
  git commit -q -m "init"
}

start_agent() {
  local token="$1"
  local http_port quic_port device_id
  http_port="$(get_free_port)"
  quic_port="$(get_free_port)"
  device_id="anthropic-local-$(python3 - <<'PY'
import uuid
print(uuid.uuid4())
PY
)"
  mkdir -p "$AGENT_HOME/.yaver" "$WORK_DIR/agent-work"
  cat > "$AGENT_HOME/.yaver/config.json" <<EOF
{
  "auth_token": "$token",
  "device_id": "$device_id",
  "convex_site_url": "$CONVEX_SITE_URL"
}
EOF
  HOME="$AGENT_HOME" "$YAVER_BIN" serve --debug --port "$http_port" --quic-port "$quic_port" --work-dir "$WORK_DIR/agent-work" --no-relay >"$WORK_DIR/agent.log" 2>&1 &
  AGENT_PID="$!"
  for _ in $(seq 1 30); do
    if curl -sf "http://127.0.0.1:${http_port}/health" >/dev/null 2>&1; then
      echo "$http_port"
      return 0
    fi
    sleep 1
  done
  tail -60 "$WORK_DIR/agent.log" >&2 || true
  fail "local agent did not become healthy"
}

poll_autoinit() {
  local base="$1" token="$2"
  for _ in $(seq 1 60); do
    local status
    status="$(curl -sf "${base}/autoinit/status?work_dir=$(python3 -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1]))' "$FIXTURE_DIR")" \
      -H "Authorization: Bearer ${token}" 2>/dev/null || true)"
    if [ -n "$status" ]; then
      local done has_gen has_hist
      done="$(printf '%s' "$status" | json_get done 2>/dev/null || true)"
      has_gen="$(printf '%s' "$status" | json_get has_generated_section 2>/dev/null || true)"
      has_hist="$(printf '%s' "$status" | json_get has_history_section 2>/dev/null || true)"
      if [ "$done" = "true" ] && [ "$has_gen" = "true" ] && [ "$has_hist" = "true" ]; then
        return 0
      fi
    fi
    sleep 2
  done
  return 1
}

run_autoinit() {
  local base="$1" token="$2"
  log "starting autoinit through Yaver HTTP"
  curl -sf -X POST "${base}/autoinit/start" \
    -H "Authorization: Bearer ${token}" \
    -H "Content-Type: application/json" \
    -d "{\"project\":\"fixture\",\"work_dir\":\"${FIXTURE_DIR}\",\"prompt\":\"tiny JS demo app for local yaver validation\"}" >/dev/null \
    || fail "failed to start /autoinit/start"
  poll_autoinit "$base" "$token" || fail "autoinit did not complete"
  grep -q "## What is this" "$FIXTURE_DIR/init.md" || fail "init.md missing expected section"
  grep -q "yaver:autoinit:generated:start" "$FIXTURE_DIR/init.md" || fail "init.md missing generated markers"
  grep -q "yaver:autoinit:history:start" "$FIXTURE_DIR/init.md" || fail "init.md missing history markers"
  pass "autoinit completed via local Yaver HTTP"
}

run_autoideas() {
  local base="$1" token="$2"
  log "starting autoideas through Yaver HTTP"
  curl -sf -X POST "${base}/autoideas/start" \
    -H "Authorization: Bearer ${token}" \
    -H "Content-Type: application/json" \
    -d "{\"project\":\"fixture\",\"work_dir\":\"${FIXTURE_DIR}\",\"hours\":\"1\",\"max_batches\":1,\"tick\":60,\"prompt\":\"tiny JS product ideas only\"}" >/dev/null \
    || fail "failed to start /autoideas/start"
  for _ in $(seq 1 60); do
    local ideas
    ideas="$(curl -sf "${base}/autoideas/file?work_dir=$(python3 -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1]))' "$FIXTURE_DIR")" \
      -H "Authorization: Bearer ${token}" 2>/dev/null || true)"
    if [ -n "$ideas" ] && printf '%s' "$ideas" | grep -q '"title"'; then
      if [ "$(printf '%s' "$ideas" | python3 -c 'import json,sys; print(len(json.load(sys.stdin).get("items", [])))' 2>/dev/null || echo 0)" -gt 0 ]; then
        pass "autoideas produced at least one idea via local Yaver HTTP"
        return 0
      fi
    fi
    sleep 5
  done
  fail "autoideas did not produce any checklist items"
}

need_prereqs
make_fixture
TOKEN="$(get_ci_token)"
BASE="http://127.0.0.1:$(start_agent "$TOKEN")"
log "agent ready at $BASE"

case "$MODE" in
  autoinit) run_autoinit "$BASE" "$TOKEN" ;;
  autoideas) run_autoideas "$BASE" "$TOKEN" ;;
  *)
    fail "unknown mode: $MODE (use autoinit or autoideas)"
    ;;
esac
