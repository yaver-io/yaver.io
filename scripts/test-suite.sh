#!/bin/bash
# ═══════════════════════════════════════════════════════════════════
# Yaver Integration Test Suite
# ═══════════════════════════════════════════════════════════════════
# Tests CLI-to-CLI connections via:
#   1. LAN (direct HTTP + QUIC on localhost)
#   2. Relay server — local (native binary)
#   3. Relay server — remote Docker deploy to Hetzner
#   4. Relay server — remote native binary deploy to Hetzner
#   5. Tailscale (cross-machine: local ↔ Hetzner via TS IPs)
#   6. Cloudflare Tunnel (HTTP through CF Access)
#
# Also verifies: unit tests, builds (CLI, relay, web, mobile, iOS, Android), MCP protocol.
#
# Credentials: loaded from env vars, .env.test, or ../talos/.env.test
# Usage:
#   ./scripts/test-suite.sh                    # Run all tests
#   ./scripts/test-suite.sh --unit             # Go unit tests only
#   ./scripts/test-suite.sh --builds           # Build verification only
#   ./scripts/test-suite.sh --lan              # LAN direct connection test
#   ./scripts/test-suite.sh --relay            # Local relay test (no remote infra)
#   ./scripts/test-suite.sh --relay-docker     # Deploy relay to Hetzner via Docker, test, teardown
#   ./scripts/test-suite.sh --relay-binary     # Deploy relay to Hetzner as binary, test, teardown
#   ./scripts/test-suite.sh --tailscale        # Tailscale cross-machine test (local ↔ Hetzner)
#   ./scripts/test-suite.sh --cloudflare       # Cloudflare tunnel test
#   ./scripts/test-suite.sh --lan --relay      # Combine flags
# ═══════════════════════════════════════════════════════════════════

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
TMPDIR="${TMPDIR:-/tmp}"
TEST_DIR="$TMPDIR/yaver-test-suite-$$"

if [ -x /usr/local/go/bin/go ] && [[ ":$PATH:" != *":/usr/local/go/bin:"* ]]; then
    export PATH="/usr/local/go/bin:$PATH"
fi

# ── Colors ──────────────────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

pass()  { echo -e "${GREEN}  ✓ $1${NC}"; PASS_COUNT=$((PASS_COUNT + 1)); }
fail()  { echo -e "${RED}  ✗ $1${NC}"; FAIL_COUNT=$((FAIL_COUNT + 1)); FAILURES+=("$1"); }
skip()  { echo -e "${YELLOW}  ⊘ $1 (skipped)${NC}"; SKIP_COUNT=$((SKIP_COUNT + 1)); }
info()  { echo -e "${CYAN}  → $1${NC}"; }
header(){ echo -e "\n${BOLD}${BLUE}══ $1 ══${NC}"; }

print_go_failure_log() {
    local log_file="$1"
    if [ ! -f "$log_file" ]; then
        return 0
    fi

    echo "---- go test failure summary ----"
    grep -n -E '^(--- FAIL:|FAIL[[:space:]]+|panic:|fatal error:)' "$log_file" || true
    echo "---- go test tail ----"
    tail -80 "$log_file"
}

PASS_COUNT=0
FAIL_COUNT=0
SKIP_COUNT=0
FAILURES=()
PIDS_TO_KILL=()
AUTH_TOKENS=()
REMOTE_CLEANUP_CMDS=()

# ── Credential Loading ─────────────────────────────────────────────
load_credentials() {
    local envfile=""
    if [ -f "$ROOT_DIR/.env.test" ]; then
        envfile="$ROOT_DIR/.env.test"
    elif [ -f "$ROOT_DIR/../talos/.env.test" ]; then
        envfile="$ROOT_DIR/../talos/.env.test"
    fi

    if [ -n "$envfile" ]; then
        info "Loading credentials from $envfile"
        set -a
        # shellcheck disable=SC1090
        source "$envfile"
        set +a
    fi

    # Defaults
    CONVEX_SITE_URL="${CONVEX_SITE_URL:-https://shocking-echidna-394.eu-west-1.convex.site}"
    REMOTE_SERVER_USER="${REMOTE_SERVER_USER:-root}"
    REMOTE_SERVER_SSH_KEY="${REMOTE_SERVER_SSH_KEY:-$HOME/.ssh/id_rsa}"
}

# ── SSH helper ─────────────────────────────────────────────────────
remote_ssh() {
    ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 \
        -i "$REMOTE_SERVER_SSH_KEY" \
        "${REMOTE_SERVER_USER}@${REMOTE_SERVER_IP}" \
        "$@"
}

remote_scp() {
    local src="$1" dst="$2"
    scp -o StrictHostKeyChecking=no -o ConnectTimeout=10 \
        -i "$REMOTE_SERVER_SSH_KEY" \
        "$src" "${REMOTE_SERVER_USER}@${REMOTE_SERVER_IP}:${dst}"
}

check_remote_server() {
    if [ -z "${REMOTE_SERVER_IP:-}" ]; then
        return 1
    fi
    ssh -o StrictHostKeyChecking=no -o ConnectTimeout=5 -o BatchMode=yes \
        -i "$REMOTE_SERVER_SSH_KEY" \
        "${REMOTE_SERVER_USER}@${REMOTE_SERVER_IP}" \
        "echo ok" > /dev/null 2>&1
}

# ── Cleanup ────────────────────────────────────────────────────────
cleanup() {
    info "Cleaning up..."

    # Kill local background processes
    for pid in "${PIDS_TO_KILL[@]+"${PIDS_TO_KILL[@]}"}"; do
        if kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
            wait "$pid" 2>/dev/null || true
        fi
    done

    # Run remote cleanup commands
    for cmd in "${REMOTE_CLEANUP_CMDS[@]+"${REMOTE_CLEANUP_CMDS[@]}"}"; do
        eval "$cmd" 2>/dev/null || true
    done

    # Note: CI uses a dedicated persistent account (ci-test@yaver.io) — no deletion needed

    # Restore config if backed up
    if [ -f "$HOME/.yaver/config.json.test-bak" ]; then
        cp "$HOME/.yaver/config.json.test-bak" "$HOME/.yaver/config.json"
        rm "$HOME/.yaver/config.json.test-bak"
        info "Config restored"
    fi

    rm -rf "$TEST_DIR" 2>/dev/null || true
}
trap cleanup EXIT

# ── Helpers ────────────────────────────────────────────────────────
get_free_port() {
    python3 -c "import socket; s=socket.socket(); s.bind(('',0)); print(s.getsockname()[1]); s.close()"
}

gen_uuid() {
    uuidgen 2>/dev/null || python3 -c 'import uuid;print(uuid.uuid4())' | tr '[:upper:]' '[:lower:]'
}

CI_TEST_EMAIL="${CI_TEST_EMAIL:-ci-test@yaver.io}"
CI_TEST_PASSWORD="${CI_TEST_PASSWORD:-ciTestPass2026!}"
CI_TEST_FULLNAME="${CI_TEST_FULLNAME:-CI Test User}"

# get_ci_token logs in with the dedicated CI account.
# On first run the account is created via signup; subsequent runs use login.
get_ci_token() {
    local resp token

    # Try login first
    resp=$(curl -sf -X POST "${CONVEX_SITE_URL}/auth/login" \
        -H "Content-Type: application/json" \
        -d "{\"email\":\"${CI_TEST_EMAIL}\",\"password\":\"${CI_TEST_PASSWORD}\"}" 2>/dev/null) || true
    token=$(echo "$resp" | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])" 2>/dev/null) || true

    if [ -n "$token" ]; then
        echo "$token"
        return 0
    fi

    # Account doesn't exist yet — create it
    resp=$(curl -sf -X POST "${CONVEX_SITE_URL}/auth/signup" \
        -H "Content-Type: application/json" \
        -d "{\"email\":\"${CI_TEST_EMAIL}\",\"fullName\":\"${CI_TEST_FULLNAME}\",\"password\":\"${CI_TEST_PASSWORD}\"}" 2>/dev/null) || true
    token=$(echo "$resp" | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])" 2>/dev/null) || true

    if [ -n "$token" ]; then
        echo "$token"
        return 0
    fi

    echo ""
    return 1
}

# Backward-compatible alias used throughout the test suite
create_test_account() {
    get_ci_token
}

build_agent() {
    local output="$1"
    cd "$ROOT_DIR/desktop/agent"
    go build -o "$output" . 2>&1
}

build_relay() {
    local output="$1"
    cd "$ROOT_DIR/relay"
    go build -o "$output" . 2>&1
}

build_relay_linux() {
    cd "$ROOT_DIR/relay"
    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o "$TEST_DIR/yaver-relay-linux-amd64" . 2>&1
}

build_agent_linux() {
    cd "$ROOT_DIR/desktop/agent"
    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o "$TEST_DIR/yaver-linux-amd64" . 2>&1
}

# Detect remote arch and build accordingly
detect_remote_arch() {
    remote_ssh "uname -m" 2>/dev/null | tr -d '\n'
}

build_relay_for_remote() {
    local arch
    arch=$(detect_remote_arch)
    local goarch="amd64"
    [ "$arch" = "aarch64" ] || [ "$arch" = "arm64" ] && goarch="arm64"
    cd "$ROOT_DIR/relay"
    GOOS=linux GOARCH="$goarch" CGO_ENABLED=0 go build -ldflags="-s -w" -o "$TEST_DIR/yaver-relay-remote" . 2>&1
}

build_agent_for_remote() {
    local arch
    arch=$(detect_remote_arch)
    local goarch="amd64"
    [ "$arch" = "aarch64" ] || [ "$arch" = "arm64" ] && goarch="arm64"
    cd "$ROOT_DIR/desktop/agent"
    GOOS=linux GOARCH="$goarch" CGO_ENABLED=0 go build -ldflags="-s -w" -o "$TEST_DIR/yaver-agent-remote" . 2>&1
}

# Start an agent and wait for health
start_agent() {
    local binary="$1" http_port="$2" quic_port="$3" token="$4" device_id="$5" work_dir="$6"
    shift 6

    mkdir -p "$work_dir"
    local config_dir="$work_dir/.yaver-config"
    mkdir -p "$config_dir/.yaver"
    cat > "$config_dir/.yaver/config.json" << EOF
{
  "auth_token": "${token}",
  "device_id": "${device_id}",
  "convex_site_url": "${CONVEX_SITE_URL}"
}
EOF

    # Use --dummy unless YAVER_NO_DUMMY is set (for e2e/docker tests that need real execution)
    local dummy_flag="--dummy"
    if [ "${YAVER_NO_DUMMY:-}" = "1" ]; then
        dummy_flag=""
    fi

    HOME="$config_dir" CLAUDECODE= "$binary" serve --debug \
        --port "$http_port" --quic-port "$quic_port" \
        --work-dir "$work_dir" $dummy_flag \
        "$@" > "$work_dir/agent.log" 2>&1 &
    local pid=$!
    PIDS_TO_KILL+=("$pid")

    for i in $(seq 1 20); do
        if curl -sf "http://127.0.0.1:${http_port}/health" > /dev/null 2>&1; then
            echo "$pid"
            return 0
        fi
        if ! kill -0 "$pid" 2>/dev/null; then
            echo "Agent exited. Log:" >&2
            tail -20 "$work_dir/agent.log" >&2
            return 1
        fi
        sleep 0.5
    done
    echo "Agent not ready after 10s." >&2
    tail -20 "$work_dir/agent.log" >&2
    return 1
}

start_relay() {
    local binary="$1" quic_port="$2" http_port="$3" password="$4" log_file="$5"

    RELAY_PASSWORD="$password" "$binary" serve \
        --quic-port "$quic_port" --http-port "$http_port" \
        > "$log_file" 2>&1 &
    local pid=$!
    PIDS_TO_KILL+=("$pid")

    for i in $(seq 1 15); do
        if curl -sf "http://127.0.0.1:${http_port}/health" > /dev/null 2>&1; then
            echo "$pid"
            return 0
        fi
        if ! kill -0 "$pid" 2>/dev/null; then
            echo "Relay exited." >&2
            cat "$log_file" >&2
            return 1
        fi
        sleep 0.5
    done
    echo "Relay not ready after 7.5s" >&2
    return 1
}

# Verify task flow end-to-end: health → info → create task → poll → completed
verify_task_flow() {
    local base_url="$1" token="$2"
    shift 2
    local extra_headers=("$@")

    local auth_header="Authorization: Bearer ${token}"
    local curl_args=(-sf -H "$auth_header")
    for h in "${extra_headers[@]+"${extra_headers[@]}"}"; do
        curl_args+=(-H "$h")
    done

    # Health
    local health
    health=$(curl "${curl_args[@]}" "${base_url}/health" 2>/dev/null) || return 1
    echo "$health" | python3 -c "import sys,json; assert json.load(sys.stdin)['ok']" 2>/dev/null || {
        echo "Health check failed: $health" >&2; return 1
    }

    # Info
    local info_resp hostname
    info_resp=$(curl "${curl_args[@]}" "${base_url}/info" 2>/dev/null) || return 1
    hostname=$(echo "$info_resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('hostname',''))" 2>/dev/null)
    [ -n "$hostname" ] || { echo "Info failed: $info_resp" >&2; return 1; }

    # Create task
    local task_resp task_id
    task_resp=$(curl "${curl_args[@]}" -X POST "${base_url}/tasks" \
        -H "Content-Type: application/json" \
        -d '{"title":"test echo","description":"Respond with: hello from yaver test. No tools."}' 2>/dev/null) || return 1
    task_id=$(echo "$task_resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('taskId',''))" 2>/dev/null)
    [ -n "$task_id" ] || { echo "Task creation failed: $task_resp" >&2; return 1; }

    # Poll (max 90s)
    local elapsed=0 status=""
    while [ $elapsed -lt 90 ]; do
        local detail
        detail=$(curl "${curl_args[@]}" "${base_url}/tasks/${task_id}" 2>/dev/null) || true
        status=$(echo "$detail" | python3 -c "import sys,json; print(json.load(sys.stdin).get('task',{}).get('status',''))" 2>/dev/null) || true
        case "$status" in finished|completed|failed|stopped) break ;; esac
        sleep 2; elapsed=$((elapsed + 2))
    done

    if [ "$status" = "finished" ] || [ "$status" = "completed" ]; then
        echo "task_id=$task_id hostname=$hostname"
        return 0
    fi
    echo "Task did not finish (status=$status after ${elapsed}s)" >&2
    return 1
}

verify_mcp() {
    local base_url="$1"
    local init_resp server_name tools_resp tool_count

    init_resp=$(curl -sf -X POST "${base_url}/mcp" \
        -H "Content-Type: application/json" \
        -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}}}' 2>/dev/null)
    server_name=$(echo "$init_resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('result',{}).get('serverInfo',{}).get('name',''))" 2>/dev/null)
    [ "$server_name" = "yaver" ] || { echo "MCP init failed: $init_resp" >&2; return 1; }

    tools_resp=$(curl -sf -X POST "${base_url}/mcp" \
        -H "Content-Type: application/json" \
        -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' 2>/dev/null)
    tool_count=$(echo "$tools_resp" | python3 -c "import sys,json; print(len(json.load(sys.stdin).get('result',{}).get('tools',[])))" 2>/dev/null)
    [ "$tool_count" -ge 5 ] || { echo "MCP tools/list returned only $tool_count tools" >&2; return 1; }

    echo "mcp_tools=$tool_count server=$server_name"
}

verify_auth_rejection() {
    local base_url="$1"
    local code
    code=$(curl -s -o /dev/null -w "%{http_code}" "${base_url}/info" \
        -H "Authorization: Bearer badtoken123" 2>/dev/null)
    [ "$code" = "403" ] || { echo "Expected 403, got $code" >&2; return 1; }
}

# ═══════════════════════════════════════════════════════════════════
# TEST SECTIONS
# ═══════════════════════════════════════════════════════════════════

# ── Unit Tests ─────────────────────────────────────────────────────
run_feature_auth_tests() {
    header "Feature Tests — Convex Auth Path (in-process mock)"

    # This used to stand up a real agent + real Convex CI account over
    # HTTP, but fighting with the first-run "pick your AI agent" prompt
    # and the auto-cache around `yaver serve` made it flaky. The
    # Convex-validated path is now covered by a Go test
    # (`TestAgentAuthConvexValidationPath`) that wires the agent at an
    # httptest.Server mock — same code path, deterministic, no creds.

    info "Running Convex-auth-path Go test ..."
    if (cd "$ROOT_DIR/desktop/agent" && go test -v -count=1 -timeout 30s \
        -run 'TestAgentAuthConvexValidationPath' ./... > "$TEST_DIR/feature-auth.log" 2>&1); then
        pass "Convex auth path (mock) — slow path + cache + rejection"
    else
        fail "Convex auth path test failed — log at $TEST_DIR/feature-auth.log"
        tail -30 "$TEST_DIR/feature-auth.log"
    fi
}

run_feature_remote_tests() {
    header "Feature Tests — Remote Hetzner"

    if ! check_remote_server; then
        skip "Remote feature tests (REMOTE_SERVER_IP not set or SSH unreachable)"
        return
    fi

    # Strategy: compile the Go test pack as a standalone binary,
    # scp it to a dedicated /tmp directory on Hetzner, run it there,
    # capture the exit code + output, then nuke ONLY that directory.
    # We never touch any pre-existing file on the server.

    local arch
    arch=$(detect_remote_arch)
    local goarch="amd64"
    [ "$arch" = "aarch64" ] || [ "$arch" = "arm64" ] && goarch="arm64"

    local test_bin="$TEST_DIR/yaver-feature.test"
    info "Cross-compiling feature-test binary (linux/$goarch) ..."
    if ! (cd "$ROOT_DIR/desktop/agent" && \
        GOOS=linux GOARCH="$goarch" CGO_ENABLED=0 go test -c -o "$test_bin" . \
        > "$TEST_DIR/feature-remote-build.log" 2>&1); then
        fail "cross-compile failed — log at $TEST_DIR/feature-remote-build.log"
        tail -20 "$TEST_DIR/feature-remote-build.log"
        return
    fi

    # Everything we touch on the remote lives under this prefix. The
    # cleanup trap removes only this path.
    local remote_dir="/tmp/yaver-feature-remote-$$-$(date +%s)"
    REMOTE_CLEANUP_CMDS+=("remote_ssh 'rm -rf ${remote_dir}'")

    info "Staging binary at ${REMOTE_SERVER_IP}:${remote_dir} ..."
    if ! remote_ssh "mkdir -p ${remote_dir} && chmod 700 ${remote_dir}" > "$TEST_DIR/feature-remote-mkdir.log" 2>&1; then
        fail "could not create remote staging dir"
        return
    fi
    if ! remote_scp "$test_bin" "${remote_dir}/feature.test" > "$TEST_DIR/feature-remote-scp.log" 2>&1; then
        fail "scp failed — log at $TEST_DIR/feature-remote-scp.log"
        return
    fi
    remote_ssh "chmod +x ${remote_dir}/feature.test" > /dev/null 2>&1

    # Run the same focused pattern we use locally. HOME is pinned
    # inside the remote dir so vault.enc + blobs land in the sandbox.
    local pattern='TestVault|TestSchedules|TestBlobs|TestAPIKeys|TestWipe|TestTwoAgent|TestSupport|TestConvex|TestGuestAllowlist|TestSupportAllowlist|TestAgentAuthConvexValidationPath|TestPhoneProjectExportReceive'
    info "Running feature tests on ${REMOTE_SERVER_IP} ..."
    local remote_log="$TEST_DIR/feature-remote.log"
    if remote_ssh "cd ${remote_dir} && HOME=${remote_dir}/home mkdir -p home && HOME=${remote_dir}/home ./feature.test -test.v -test.timeout=120s -test.run='${pattern}'" \
        > "$remote_log" 2>&1; then
        local passed
        passed=$(grep -c '^--- PASS' "$remote_log" || true)
        pass "Remote feature tests passed (${passed} subtests on ${arch})"
    else
        local passed failed
        passed=$(grep -c '^--- PASS' "$remote_log" 2>/dev/null || true)
        failed=$(grep -c '^--- FAIL' "$remote_log" 2>/dev/null || true)
        fail "Remote feature tests failed (${passed} passed / ${failed} failed on ${arch}) — log at $remote_log"
        echo "    first failures:"
        grep -E '^--- FAIL|^\s+[^:]*:[0-9]+: ' "$remote_log" | head -20 | sed 's/^/    /'
    fi

    # Explicit cleanup now (the trap will also run, but belt & braces).
    info "Cleaning up ${remote_dir} ..."
    remote_ssh "rm -rf ${remote_dir}" > /dev/null 2>&1 || true
}

run_feature_tests() {
    header "Feature Tests (vault + blobs + schedules + apikeys + wipe + two-agent)"

    # Scoped Go test run — covers every focused HTTP integration the
    # user can hit from web + mobile. No creds, no network, all
    # loopback. Runs in ~5s on a laptop.
    local patterns=(
        TestVaultHTTP          # full /vault/* CRUD, support-bearer blocked
        TestSchedulesHTTP      # CRUD + run-now + deadlock repro
        TestScheduleRunNow
        TestBlobsHTTP          # PUT/GET/DELETE, pagination, signed URL
        TestBlobsList
        TestAPIKeys            # registry list + disable + label cap
        TestWipe               # selective + all + --including-auth
        TestTwoAgent           # cross-agent support + cross-token isolation
        TestSupport            # support session unit + integration
        TestGuestAllowlist     # privacy-allowlist tripwires
        TestSupportAllowlist
        TestConvex             # convex payload privacy tripwires
    )
    local regex
    regex=$(IFS='|'; echo "${patterns[*]}")

    info "Running feature test pack (pattern: ${regex}) ..."
    if (cd "$ROOT_DIR/desktop/agent" && go test -v -count=1 -timeout 120s -run "${regex}" ./... > "$TEST_DIR/feature-test.log" 2>&1); then
        local passed
        passed=$(grep -c '^--- PASS' "$TEST_DIR/feature-test.log" || true)
        pass "Feature tests passed (${passed} subtests)"
    else
        fail "Feature tests failed — log at $TEST_DIR/feature-test.log"
        tail -30 "$TEST_DIR/feature-test.log"
    fi
}

run_unit_tests() {
    header "Unit Tests"

    info "Running Go agent tests..."
    if (cd "$ROOT_DIR/desktop/agent" && go test -v -count=1 ./... > "$TEST_DIR/agent-test.log" 2>&1); then
        pass "Agent unit tests passed"
    else
        fail "Agent unit tests failed"
        print_go_failure_log "$TEST_DIR/agent-test.log"
    fi

    info "Running Go relay tests..."
    if (cd "$ROOT_DIR/relay" && go test -v -count=1 ./... > "$TEST_DIR/relay-test.log" 2>&1); then
        pass "Relay unit tests passed"
    else
        if grep -q "no test files" "$TEST_DIR/relay-test.log" 2>/dev/null; then
            skip "Relay has no unit tests"
        else
            fail "Relay unit tests failed"
            print_go_failure_log "$TEST_DIR/relay-test.log"
        fi
    fi

    info "Running MCP server tests..."
    if (cd "$ROOT_DIR/mcp" && go test -v -count=1 ./... > "$TEST_DIR/mcp-test.log" 2>&1); then
        pass "MCP server tests passed"
    else
        fail "MCP server tests failed"
        print_go_failure_log "$TEST_DIR/mcp-test.log"
    fi

    if command -v bun &>/dev/null && [ -d "$ROOT_DIR/mobile-headless" ]; then
        info "Running mobile-headless dogfood preference tests..."
        if (cd "$ROOT_DIR/mobile-headless" && bun test test/more-menu-preferences.test.ts > "$TEST_DIR/mobile-headless-more-menu.log" 2>&1); then
            pass "Mobile-headless More menu preference tests passed"
        else
            fail "Mobile-headless More menu preference tests failed"
            tail -20 "$TEST_DIR/mobile-headless-more-menu.log"
        fi
    else
        skip "Mobile-headless More menu preference tests" "bun or mobile-headless missing"
    fi
}

# ── Build Tests ────────────────────────────────────────────────────
run_build_tests() {
    header "Build Verification"

    # CLI (current platform)
    info "Building CLI (current platform)..."
    if build_agent "$TEST_DIR/yaver" > "$TEST_DIR/build-cli.log" 2>&1; then
        pass "CLI build OK ($(du -h "$TEST_DIR/yaver" | cut -f1))"
    else
        fail "CLI build failed"
        cat "$TEST_DIR/build-cli.log"
        return 1
    fi

    # CLI (linux/amd64)
    info "Cross-compiling CLI (linux/amd64)..."
    if build_agent_linux > "$TEST_DIR/build-cli-linux.log" 2>&1; then
        pass "CLI cross-compile linux/amd64 OK"
    else
        fail "CLI cross-compile linux/amd64 failed"
    fi

    # Relay
    info "Building relay server..."
    if build_relay "$TEST_DIR/yaver-relay" > "$TEST_DIR/build-relay.log" 2>&1; then
        pass "Relay build OK ($(du -h "$TEST_DIR/yaver-relay" | cut -f1))"
    else
        fail "Relay build failed"
    fi

    # Relay (linux/amd64)
    info "Cross-compiling relay (linux/amd64)..."
    if build_relay_linux > "$TEST_DIR/build-relay-linux.log" 2>&1; then
        pass "Relay cross-compile linux/amd64 OK"
    else
        fail "Relay cross-compile linux/amd64 failed"
    fi

    # Web
    info "Building web (Next.js)..."
    if (cd "$ROOT_DIR/web" && npm ci --silent > /dev/null 2>&1 && npm run build > "$TEST_DIR/build-web.log" 2>&1); then
        pass "Web build OK"
    else
        fail "Web build failed (see $TEST_DIR/build-web.log)"
    fi

    # Backend typecheck (uses convex typecheck, not raw tsc)
    info "Typechecking backend (Convex)..."
    if (cd "$ROOT_DIR/backend" && npm ci --silent > /dev/null 2>&1 && npx convex typecheck > "$TEST_DIR/build-backend.log" 2>&1); then
        pass "Backend typecheck OK"
    else
        fail "Backend typecheck failed (see $TEST_DIR/build-backend.log)"
    fi

    # Mobile typecheck
    info "Typechecking mobile (React Native)..."
    if (cd "$ROOT_DIR/mobile" && npm ci --silent > /dev/null 2>&1 && cd "$ROOT_DIR" && node --test scripts/generate-sdk-manifest.test.mjs > "$TEST_DIR/sdk-manifest-unit.log" 2>&1 && node scripts/generate-sdk-manifest.mjs --check > "$TEST_DIR/sdk-manifest-check.log" 2>&1 && cd "$ROOT_DIR/mobile" && npx tsc --noEmit > "$TEST_DIR/build-mobile.log" 2>&1); then
        pass "Mobile typecheck OK"
    else
        fail "Mobile typecheck failed (see $TEST_DIR/build-mobile.log, $TEST_DIR/sdk-manifest-unit.log, and $TEST_DIR/sdk-manifest-check.log)"
    fi

    # iOS (macOS only)
    if [ "$(uname)" = "Darwin" ] && [ -d "$ROOT_DIR/mobile/ios" ]; then
        info "Building iOS (Release, no codesign)..."
        if (cd "$ROOT_DIR/mobile/ios" && pod install --silent > "$TEST_DIR/pod-install.log" 2>&1 && \
            xcodebuild -workspace Yaver.xcworkspace -scheme Yaver \
            -configuration Release -destination 'generic/platform=iOS' \
            CODE_SIGN_IDENTITY="" CODE_SIGNING_REQUIRED=NO CODE_SIGNING_ALLOWED=NO \
            -quiet build > "$TEST_DIR/build-ios.log" 2>&1); then
            pass "iOS build OK"
        else
            fail "iOS build failed (see $TEST_DIR/build-ios.log)"
        fi
    else
        skip "iOS build (not on macOS or no ios/ dir)"
    fi

    # Android
    if [ -f "$ROOT_DIR/mobile/android/gradlew" ] && command -v java &>/dev/null; then
        info "Building Android (assembleRelease)..."
        local java_home="${JAVA_HOME:-$(/usr/libexec/java_home -v 17 2>/dev/null || echo "")}"
        if [ -n "$java_home" ]; then
            if (cd "$ROOT_DIR/mobile/android" && JAVA_HOME="$java_home" ./gradlew assembleRelease --no-daemon -q > "$TEST_DIR/build-android.log" 2>&1); then
                pass "Android build OK"
            else
                fail "Android build failed (see $TEST_DIR/build-android.log)"
            fi
        else
            skip "Android build (Java 17 not found)"
        fi
    else
        skip "Android build (no android/ dir or java not available)"
    fi
}

# ── LAN Test ───────────────────────────────────────────────────────
run_lan_test() {
    header "LAN — Direct CLI-to-CLI (localhost)"

    local agent_bin="$TEST_DIR/yaver"
    [ -f "$agent_bin" ] || build_agent "$agent_bin" > /dev/null 2>&1 || { fail "Cannot build agent"; return; }

    local http_port quic_port
    http_port=$(get_free_port); quic_port=$(get_free_port)

    info "Creating test account..."
    local token
    token=$(create_test_account) || { fail "Cannot create test account"; return; }

    local device_id="test-lan-$(gen_uuid)"
    local work_dir="$TEST_DIR/lan-agent"

    info "Starting agent (HTTP=$http_port, QUIC=$quic_port)..."
    local agent_pid
    agent_pid=$(start_agent "$agent_bin" "$http_port" "$quic_port" "$token" "$device_id" "$work_dir" --no-relay) || {
        fail "Agent failed to start"; return
    }

    local base_url="http://127.0.0.1:${http_port}"

    if verify_auth_rejection "$base_url"; then
        pass "Auth rejection (bad token → 403)"
    else
        fail "Auth rejection test"
    fi

    info "Testing task flow via direct HTTP..."
    local result
    if result=$(verify_task_flow "$base_url" "$token"); then
        pass "LAN task flow OK ($result)"
    else
        fail "LAN task flow failed"
    fi

    info "Testing MCP protocol..."
    if result=$(verify_mcp "$base_url"); then
        pass "MCP protocol OK ($result)"
    else
        fail "MCP protocol test"
    fi

    kill "$agent_pid" 2>/dev/null || true
}

# ── Local Relay Test ───────────────────────────────────────────────
# Only verifies relay builds and starts. No QUIC tunnel registration
# (relay registration is an expensive operation — tested manually).
run_relay_test() {
    header "Relay — Build & Health Check"

    local relay_bin="$TEST_DIR/yaver-relay"
    [ -f "$relay_bin" ] || build_relay "$relay_bin" > /dev/null 2>&1 || { fail "Cannot build relay"; return; }

    local relay_http_port relay_quic_port
    relay_http_port=$(get_free_port); relay_quic_port=$(get_free_port)
    local relay_password="test-relay-pass-$$"

    info "Starting relay (HTTP=$relay_http_port)..."
    local relay_pid
    relay_pid=$(start_relay "$relay_bin" "$relay_quic_port" "$relay_http_port" "$relay_password" "$TEST_DIR/relay.log") || {
        fail "Relay failed to start"; return
    }

    if curl -sf "http://127.0.0.1:${relay_http_port}/health" | python3 -c "import sys,json; assert json.load(sys.stdin)['ok']" 2>/dev/null; then
        pass "Relay health OK"
    else
        fail "Relay health check"; return
    fi

    # Verify password rejection (no agent registration needed)
    local bad_pw_status
    bad_pw_status=$(curl -s -o /dev/null -w "%{http_code}" \
        "http://127.0.0.1:${relay_http_port}/d/fake-device/health" \
        -H "X-Relay-Password: wrong-password" 2>/dev/null)
    if [ "$bad_pw_status" = "401" ]; then
        pass "Relay password rejection (wrong password → 401)"
    else
        fail "Relay password rejection (expected 401, got $bad_pw_status)"
    fi

    kill "$relay_pid" 2>/dev/null || true
}

# ── Remote Relay Test — Docker Deploy to Hetzner ──────────────────
run_relay_docker_test() {
    header "Relay Docker — Deploy to Hetzner, test, teardown"

    if ! check_remote_server; then
        skip "Relay Docker test (REMOTE_SERVER_IP not set or SSH unreachable)"
        return
    fi

    local relay_password="test-docker-relay-$$"

    info "Deploying relay via Docker to $REMOTE_SERVER_IP..."

    # Copy relay source to server and build with Docker
    local relay_tar="$TEST_DIR/relay.tar.gz"
    (cd "$ROOT_DIR" && tar czf "$relay_tar" relay/) || { fail "Cannot tar relay/"; return; }
    remote_scp "$relay_tar" "/tmp/yaver-relay-test.tar.gz"

    # Register cleanup BEFORE deploying
    REMOTE_CLEANUP_CMDS+=("remote_ssh 'docker stop yaver-relay-test 2>/dev/null; docker rm yaver-relay-test 2>/dev/null; rm -rf /tmp/yaver-relay-test*'")

    remote_ssh "cd /tmp && rm -rf yaver-relay-test && mkdir yaver-relay-test && cd yaver-relay-test && tar xzf /tmp/yaver-relay-test.tar.gz && cd relay && docker build -t yaver-relay-test . > /dev/null 2>&1 && docker rm -f yaver-relay-test 2>/dev/null; docker run -d --name yaver-relay-test -p 14433:4433/udp -p 18443:8443/tcp -e RELAY_PASSWORD='${relay_password}' yaver-relay-test" \
        || { fail "Docker deploy failed"; return; }

    sleep 3

    # Health check
    local relay_http="http://${REMOTE_SERVER_IP}:18443"
    if curl -sf --connect-timeout 10 "${relay_http}/health" | python3 -c "import sys,json; assert json.load(sys.stdin)['ok']" 2>/dev/null; then
        pass "Docker relay health OK (${relay_http})"
    else
        fail "Docker relay health check"
        remote_ssh "docker logs yaver-relay-test 2>&1 | tail -20"
        return
    fi

    # No agent registration — relay registration is expensive (QUIC tunnels).
    # Health check is sufficient to verify deploy works.

    # Teardown
    info "Tearing down Docker relay..."
    remote_ssh "docker stop yaver-relay-test 2>/dev/null; docker rm yaver-relay-test 2>/dev/null; rm -rf /tmp/yaver-relay-test*" || true
    pass "Docker relay cleaned up"
}

# ── Remote Relay Test — Native Binary Deploy to Hetzner ───────────
run_relay_binary_test() {
    header "Relay Binary — Deploy to Hetzner as native binary, test, teardown"

    if ! check_remote_server; then
        skip "Relay binary test (REMOTE_SERVER_IP not set or SSH unreachable)"
        return
    fi

    local relay_password="test-binary-relay-$$"

    info "Building relay for remote arch ($(detect_remote_arch))..."
    build_relay_for_remote > /dev/null 2>&1 || { fail "Cannot cross-compile relay"; return; }

    info "Deploying relay binary to $REMOTE_SERVER_IP..."
    remote_scp "$TEST_DIR/yaver-relay-remote" "/tmp/yaver-relay-test"

    REMOTE_CLEANUP_CMDS+=("remote_ssh 'kill \$(cat /tmp/yaver-relay-test.pid 2>/dev/null) 2>/dev/null; rm -f /tmp/yaver-relay-test /tmp/yaver-relay-test.pid /tmp/yaver-relay-test.log'")

    remote_ssh "chmod +x /tmp/yaver-relay-test && kill \$(cat /tmp/yaver-relay-test.pid 2>/dev/null) 2>/dev/null; RELAY_PASSWORD='${relay_password}' nohup /tmp/yaver-relay-test serve --quic-port=24433 --http-port=28443 > /tmp/yaver-relay-test.log 2>&1 & echo \$! > /tmp/yaver-relay-test.pid; sleep 1" \
        || { fail "Binary deploy failed"; return; }

    sleep 3

    local relay_http="http://${REMOTE_SERVER_IP}:28443"
    if curl -sf --connect-timeout 10 "${relay_http}/health" | python3 -c "import sys,json; assert json.load(sys.stdin)['ok']" 2>/dev/null; then
        pass "Binary relay health OK (${relay_http})"
    else
        fail "Binary relay health check"
        remote_ssh "cat /tmp/yaver-relay-test.log 2>&1 | tail -20"
        return
    fi

    # No agent registration — relay registration is expensive (QUIC tunnels).
    # Health check is sufficient to verify deploy works.

    info "Tearing down binary relay..."
    remote_ssh "kill \$(cat /tmp/yaver-relay-test.pid 2>/dev/null) 2>/dev/null; rm -f /tmp/yaver-relay-test /tmp/yaver-relay-test.pid /tmp/yaver-relay-test.log" || true
    pass "Binary relay cleaned up"
}

# ── Tailscale Test — Cross-machine via Hetzner ────────────────────
run_mesh_e2e_test() {
    header "Yaver Mesh — data-plane end-to-end (local, containerized, \$0)"

    if ! docker info >/dev/null 2>&1; then
        skip "Mesh e2e (Docker not running — start Docker Desktop / dockerd)"
        return
    fi
    # Real TUN + wireguard-go handshake + netconfig + ICMP over the 100.96.x
    # overlay, in two netns inside one privileged Linux container. No cloud, no
    # Convex, no OAuth. See scripts/test-mesh-e2e.sh + ci/mesh/e2e-in-container.sh.
    if "$SCRIPT_DIR/test-mesh-e2e.sh"; then
        pass "Mesh data plane: overlay ping end-to-end ✓"
    else
        fail "Mesh data plane e2e"
    fi
}

run_relay_tunnel_e2e_test() {
    header "Relay — HTTP-over-QUIC tunnel (client → relay → agent, local, \$0)"

    if ! docker info >/dev/null 2>&1; then
        skip "Relay HTTP-tunnel e2e (Docker not running)"
        return
    fi
    if "$SCRIPT_DIR/test-relay-tunnel-e2e.sh"; then
        pass "Relay HTTP tunnel: request round-tripped through the agent ✓"
    else
        fail "Relay HTTP-tunnel e2e"
    fi
}

run_machine_e2e_test() {
    header "Talos-IoT Machine — Modbus hijack edge flow (emulated PLC, local, \$0)"

    if ! docker info >/dev/null 2>&1; then
        skip "Machine e2e (Docker not running)"
        return
    fi
    # Modbus-TCP emulator (PLC) + edge harness: absorb registers, observe live
    # counter, write setpoint (verified read-back), sync schematic to mock Talos.
    # See scripts/test-machine-e2e.sh + ci/machine/e2e-in-container.sh.
    if "$SCRIPT_DIR/test-machine-e2e.sh"; then
        pass "Machine hijack: absorb→observe→verified-write→Talos-sync ✓"
    else
        fail "Machine hijack e2e"
    fi
}

run_mesh_relay_e2e_test() {
    header "Yaver Mesh — relay-DERP fallback (symmetric NAT, local, \$0)"

    if ! docker info >/dev/null 2>&1; then
        skip "Mesh relay-DERP e2e (Docker not running)"
        return
    fi
    # Relay + two agents with NO direct path (separate netns/subnets) — proves
    # WireGuard frames ride the relay's mesh_relay stream. See
    # scripts/test-mesh-relay-e2e.sh + ci/mesh/e2e-relay-in-container.sh.
    if "$SCRIPT_DIR/test-mesh-relay-e2e.sh"; then
        pass "Mesh relay-DERP: overlay ping with no direct path ✓"
    else
        fail "Mesh relay-DERP e2e"
    fi
}

run_tailscale_test() {
    header "Tailscale — Cross-machine CLI-to-CLI (local ↔ Hetzner)"

    # Check local Tailscale
    if ! command -v tailscale &>/dev/null; then
        skip "Tailscale test (tailscale CLI not installed)"
        return
    fi

    local ts_status
    ts_status=$(tailscale status --json 2>/dev/null | python3 -c "import sys,json; print(json.load(sys.stdin).get('BackendState',''))" 2>/dev/null) || true
    if [ "$ts_status" != "Running" ]; then
        skip "Tailscale test (tailscale not running, state=${ts_status:-unknown})"
        return
    fi

    local local_ts_ip
    local_ts_ip=$(tailscale ip -4 2>/dev/null) || true
    if [ -z "$local_ts_ip" ]; then
        skip "Tailscale test (cannot get local Tailscale IPv4)"
        return
    fi
    pass "Local Tailscale running (IP=$local_ts_ip)"

    # Check if we have a remote server to deploy an agent to
    if ! check_remote_server; then
        # Fallback: just test local agent reachable via TS IP
        info "No remote server — testing local agent via Tailscale IP..."
        local agent_bin="$TEST_DIR/yaver"
        [ -f "$agent_bin" ] || build_agent "$agent_bin" > /dev/null 2>&1 || { fail "Cannot build agent"; return; }

        local http_port quic_port
        http_port=$(get_free_port); quic_port=$(get_free_port)
        local token
        token=$(create_test_account) || { fail "Cannot create test account"; return; }
        local device_id="test-ts-local-$(gen_uuid)"
        local work_dir="$TEST_DIR/ts-local-agent"

        local agent_pid
        agent_pid=$(start_agent "$agent_bin" "$http_port" "$quic_port" "$token" "$device_id" "$work_dir" --no-relay) || {
            fail "Agent failed to start"; return
        }

        local ts_url="http://${local_ts_ip}:${http_port}"
        if curl -sf --connect-timeout 5 "$ts_url/health" > /dev/null 2>&1; then
            pass "Agent reachable via Tailscale IP ($ts_url)"
        else
            fail "Agent not reachable via Tailscale IP"
        fi

        local result
        if result=$(verify_task_flow "$ts_url" "$token"); then
            pass "Tailscale local task flow OK ($result)"
        else
            fail "Tailscale local task flow"
        fi

        kill "$agent_pid" 2>/dev/null || true
        return
    fi

    # Full cross-machine test: deploy agent to Hetzner, connect via Tailscale
    info "Checking Tailscale on remote server ($REMOTE_SERVER_IP)..."

    local remote_ts_ip
    remote_ts_ip=$(remote_ssh "tailscale ip -4 2>/dev/null" 2>/dev/null) || true
    if [ -z "$remote_ts_ip" ]; then
        skip "Tailscale test (tailscale not available on remote server)"
        return
    fi
    pass "Remote Tailscale running (IP=$remote_ts_ip)"

    # Verify TS connectivity between machines
    if ! tailscale ping --timeout=5s "$remote_ts_ip" > /dev/null 2>&1; then
        fail "Cannot ping remote via Tailscale ($remote_ts_ip)"
        return
    fi
    pass "Tailscale ping to remote OK"

    # Deploy agent binary to Hetzner
    info "Building agent for remote arch ($(detect_remote_arch))..."
    build_agent_for_remote > /dev/null 2>&1 || { fail "Cannot cross-compile agent"; return; }

    info "Deploying agent to Hetzner..."
    remote_scp "$TEST_DIR/yaver-agent-remote" "/tmp/yaver-agent-test"

    local token
    token=$(create_test_account) || { fail "Cannot create test account"; return; }
    local device_id="test-ts-remote-$(gen_uuid)"
    local remote_http_port=19080
    local remote_quic_port=19433

    REMOTE_CLEANUP_CMDS+=("remote_ssh 'kill \$(cat /tmp/yaver-agent-test.pid 2>/dev/null) 2>/dev/null; rm -f /tmp/yaver-agent-test /tmp/yaver-agent-test.pid /tmp/yaver-agent-test.log; rm -rf /tmp/yaver-ts-test'")

    # Write config locally and scp it
    local remote_config="$TEST_DIR/remote-agent-config.json"
    cat > "$remote_config" << EOF
{"auth_token":"${token}","device_id":"${device_id}","convex_site_url":"${CONVEX_SITE_URL}"}
EOF
    remote_ssh "mkdir -p /tmp/yaver-ts-test/.yaver"
    remote_scp "$remote_config" "/tmp/yaver-ts-test/.yaver/config.json"

    remote_ssh "chmod +x /tmp/yaver-agent-test && kill \$(cat /tmp/yaver-agent-test.pid 2>/dev/null) 2>/dev/null; HOME=/tmp/yaver-ts-test CLAUDECODE= nohup /tmp/yaver-agent-test serve --debug --port=${remote_http_port} --quic-port=${remote_quic_port} --work-dir=/tmp/yaver-ts-test --dummy --no-relay > /tmp/yaver-agent-test.log 2>&1 & echo \$! > /tmp/yaver-agent-test.pid; sleep 1" \
        || { fail "Remote agent deploy failed"; return; }

    sleep 3

    # Test: connect to remote agent via Tailscale IP
    local ts_url="http://${remote_ts_ip}:${remote_http_port}"
    info "Testing connection via Tailscale ($ts_url)..."

    if curl -sf --connect-timeout 10 "$ts_url/health" > /dev/null 2>&1; then
        pass "Remote agent reachable via Tailscale"
    else
        fail "Remote agent not reachable via Tailscale ($ts_url)"
        remote_ssh "cat /tmp/yaver-agent-test.log 2>&1 | tail -20"
        return
    fi

    local result
    if result=$(verify_task_flow "$ts_url" "$token"); then
        pass "Tailscale cross-machine task flow OK ($result)"
    else
        fail "Tailscale cross-machine task flow"
    fi

    # Teardown remote agent
    info "Tearing down remote agent..."
    remote_ssh "kill \$(cat /tmp/yaver-agent-test.pid 2>/dev/null) 2>/dev/null; rm -f /tmp/yaver-agent-test /tmp/yaver-agent-test.pid /tmp/yaver-agent-test.log; rm -rf /tmp/yaver-ts-test" || true
    pass "Remote agent cleaned up"
}

# ── Cloudflare Tunnel Test ─────────────────────────────────────────
run_cloudflare_test() {
    header "Cloudflare Tunnel — CLI-to-CLI via CF Access"

    if [ -z "${CF_TUNNEL_URL:-}" ] && ! command -v cloudflared &>/dev/null; then
        skip "Cloudflare tunnel test (CF_TUNNEL_URL not set and cloudflared not installed)"
        return
    fi

    local agent_bin="$TEST_DIR/yaver"
    [ -f "$agent_bin" ] || build_agent "$agent_bin" > /dev/null 2>&1 || { fail "Cannot build agent"; return; }

    local http_port quic_port
    http_port=$(get_free_port); quic_port=$(get_free_port)

    local token
    token=$(create_test_account) || { fail "Cannot create test account"; return; }
    local device_id="test-cf-$(gen_uuid)"
    local work_dir="$TEST_DIR/cf-agent"

    local agent_pid
    agent_pid=$(start_agent "$agent_bin" "$http_port" "$quic_port" "$token" "$device_id" "$work_dir" --no-relay) || {
        fail "Agent failed to start"; return
    }

    # Quick tunnel (no CF Access needed)
    if command -v cloudflared &>/dev/null; then
        info "Starting cloudflared quick tunnel to localhost:$http_port..."
        cloudflared tunnel --url "http://127.0.0.1:${http_port}" \
            > "$TEST_DIR/cloudflared.log" 2>&1 &
        local cfd_pid=$!
        PIDS_TO_KILL+=("$cfd_pid")

        # Wait for cloudflared to output the tunnel URL (up to 15s)
        local auto_url=""
        for i in $(seq 1 15); do
            auto_url=$(grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' "$TEST_DIR/cloudflared.log" 2>/dev/null | head -1 || true)
            [ -n "$auto_url" ] && break
            sleep 1
        done

        if [ -n "$auto_url" ]; then
            pass "Cloudflared quick tunnel: $auto_url"

            # Wait for tunnel to be fully routable (CF edge can take 10-30s)
            # CF quick tunnels need 10-15s for DNS propagation after URL appears.
            # DNS negative caching can cause repeated failures if we poll too early,
            # so wait a fixed period first then check.
            info "Waiting for CF DNS propagation (15s)..."
            sleep 15
            local tunnel_ready=false
            local attempt=0
            while [ $attempt -lt 10 ]; do
                attempt=$((attempt + 1))
                if curl -sf --connect-timeout 5 --max-time 10 "$auto_url/health" > /dev/null 2>&1; then
                    tunnel_ready=true
                    break
                fi
                sleep 3
            done

            if $tunnel_ready; then
                pass "Cloudflare tunnel health OK (attempt $attempt)"
                local result
                if result=$(verify_task_flow "$auto_url" "$token"); then
                    pass "Cloudflare quick tunnel task flow OK ($result)"
                else
                    fail "Cloudflare quick tunnel task flow"
                fi
            else
                fail "Cloudflare tunnel not routable after 40s"
            fi
        else
            skip "Could not extract cloudflared tunnel URL"
        fi
    fi

    # Named tunnel with CF Access service token
    if [ -n "${CF_ACCESS_CLIENT_ID:-}" ] && [ -n "${CF_ACCESS_CLIENT_SECRET:-}" ]; then
        info "Testing via configured CF tunnel ($CF_TUNNEL_URL)..."
        local cf_health
        cf_health=$(curl -sf --connect-timeout 10 \
            -H "CF-Access-Client-Id: ${CF_ACCESS_CLIENT_ID}" \
            -H "CF-Access-Client-Secret: ${CF_ACCESS_CLIENT_SECRET}" \
            "${CF_TUNNEL_URL}/health" 2>/dev/null) || true

        if echo "$cf_health" | python3 -c "import sys,json; assert json.load(sys.stdin)['ok']" 2>/dev/null; then
            pass "CF tunnel health OK"
            local result
            if result=$(verify_task_flow "$CF_TUNNEL_URL" "$token" \
                "CF-Access-Client-Id: $CF_ACCESS_CLIENT_ID" \
                "CF-Access-Client-Secret: $CF_ACCESS_CLIENT_SECRET"); then
                pass "CF tunnel task flow OK ($result)"
            else
                fail "CF tunnel task flow"
            fi
        else
            skip "CF tunnel not reachable (agent may not be running behind it)"
        fi
    else
        skip "CF Access service token (CF_ACCESS_CLIENT_ID/CF_ACCESS_CLIENT_SECRET not set)"
    fi

    kill "$agent_pid" 2>/dev/null || true
}

# ═══════════════════════════════════════════════════════════════════
# MAIN
# ═══════════════════════════════════════════════════════════════════
main() {
    echo -e "${BOLD}${BLUE}"
    echo "╔═══════════════════════════════════════════════════╗"
    echo "║         Yaver Integration Test Suite              ║"
    echo "╚═══════════════════════════════════════════════════╝"
    echo -e "${NC}"

    mkdir -p "$TEST_DIR"
    load_credentials

    info "Test dir: $TEST_DIR"
    info "Convex: $CONVEX_SITE_URL"
    [ -n "${REMOTE_SERVER_IP:-}" ] && info "Remote: $REMOTE_SERVER_IP"
    echo ""

    # Parse flags
run_auth_tests() {
    header "Auth — Signup, Login, Profile, Delete Account"

    local base="${CONVEX_SITE_URL}"
    local email="authtest-$(date +%s)-$RANDOM@test.yaver.io"
    local password="TestPass123!"
    local fullName="Auth Test User"

    # ── Signup ────────────────────────────────────────────────────────
    info "Testing email signup..."
    local signup_resp
    signup_resp=$(curl -sf -X POST "${base}/auth/signup" \
        -H "Content-Type: application/json" \
        -d "{\"email\":\"${email}\",\"fullName\":\"${fullName}\",\"password\":\"${password}\"}" 2>&1)
    local token
    token=$(echo "$signup_resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null)
    if [ -n "$token" ] && [ "$token" != "" ]; then
        pass "Email signup OK (${email})"
        AUTH_TOKENS+=("$token")
    else
        fail "Email signup failed: $signup_resp"
        return
    fi

    # ── Signup validation: missing fields ─────────────────────────────
    info "Testing signup validation..."
    local code
    code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "${base}/auth/signup" \
        -H "Content-Type: application/json" \
        -d '{"email":""}' 2>/dev/null)
    if [ "$code" = "400" ]; then
        pass "Signup rejects missing fields (400)"
    else
        fail "Expected 400 for missing fields, got $code"
    fi

    # ── Signup validation: short password ─────────────────────────────
    code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "${base}/auth/signup" \
        -H "Content-Type: application/json" \
        -d "{\"email\":\"short@test.yaver.io\",\"fullName\":\"Test\",\"password\":\"abc\"}" 2>/dev/null)
    if [ "$code" = "400" ]; then
        pass "Signup rejects short password (400)"
    else
        fail "Expected 400 for short password, got $code"
    fi

    # ── Signup validation: duplicate email ────────────────────────────
    code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "${base}/auth/signup" \
        -H "Content-Type: application/json" \
        -d "{\"email\":\"${email}\",\"fullName\":\"Dupe\",\"password\":\"${password}\"}" 2>/dev/null)
    if [ "$code" = "409" ]; then
        pass "Signup rejects duplicate email (409)"
    else
        fail "Expected 409 for duplicate email, got $code"
    fi

    # ── Login ─────────────────────────────────────────────────────────
    info "Testing email login..."
    local login_resp
    login_resp=$(curl -sf -X POST "${base}/auth/login" \
        -H "Content-Type: application/json" \
        -d "{\"email\":\"${email}\",\"password\":\"${password}\"}" 2>&1)
    local login_token
    login_token=$(echo "$login_resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null)
    if [ -n "$login_token" ] && [ "$login_token" != "" ]; then
        pass "Email login OK"
        AUTH_TOKENS+=("$login_token")
    else
        fail "Email login failed: $login_resp"
    fi

    # ── Login: wrong password ─────────────────────────────────────────
    code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "${base}/auth/login" \
        -H "Content-Type: application/json" \
        -d "{\"email\":\"${email}\",\"password\":\"wrongpass123\"}" 2>/dev/null)
    if [ "$code" = "401" ]; then
        pass "Login rejects wrong password (401)"
    else
        fail "Expected 401 for wrong password, got $code"
    fi

    # ── Login: nonexistent email ──────────────────────────────────────
    code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "${base}/auth/login" \
        -H "Content-Type: application/json" \
        -d '{"email":"nobody@test.yaver.io","password":"testpass123"}' 2>/dev/null)
    if [ "$code" = "401" ]; then
        pass "Login rejects nonexistent email (401)"
    else
        fail "Expected 401 for nonexistent email, got $code"
    fi

    # ── Validate token ────────────────────────────────────────────────
    info "Testing token validation..."
    local validate_resp
    validate_resp=$(curl -sf "${base}/auth/validate" \
        -H "Authorization: Bearer ${token}" 2>&1)
    local user_email
    user_email=$(echo "$validate_resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('user',{}).get('email',''))" 2>/dev/null)
    if [ "$user_email" = "$email" ]; then
        pass "Token validation OK (email matches)"
    else
        fail "Token validation failed: $validate_resp"
    fi

    # ── Validate: invalid token ───────────────────────────────────────
    code=$(curl -s -o /dev/null -w "%{http_code}" "${base}/auth/validate" \
        -H "Authorization: Bearer invalidtoken123" 2>/dev/null)
    if [ "$code" = "401" ]; then
        pass "Validate rejects invalid token (401)"
    else
        fail "Expected 401 for invalid token, got $code"
    fi

    # ── Update profile ────────────────────────────────────────────────
    info "Testing profile update..."
    local update_code
    update_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "${base}/auth/update-profile" \
        -H "Authorization: Bearer ${token}" \
        -H "Content-Type: application/json" \
        -d '{"fullName":"Updated Auth Test"}' 2>/dev/null)
    if [ "$update_code" = "200" ]; then
        pass "Profile update OK"
    else
        fail "Profile update failed ($update_code)"
    fi

    # ── Verify profile updated ────────────────────────────────────────
    validate_resp=$(curl -sf "${base}/auth/validate" \
        -H "Authorization: Bearer ${token}" 2>&1)
    local updated_name
    updated_name=$(echo "$validate_resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('user',{}).get('fullName',''))" 2>/dev/null)
    if [ "$updated_name" = "Updated Auth Test" ]; then
        pass "Profile name updated correctly"
    else
        fail "Expected name 'Updated Auth Test', got '$updated_name'"
    fi

    # ── Settings CRUD ─────────────────────────────────────────────────
    info "Testing user settings..."
    local settings_code
    settings_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "${base}/settings" \
        -H "Authorization: Bearer ${token}" \
        -H "Content-Type: application/json" \
        -d '{"runnerId":"codex","speechProvider":"openai","verbosity":5,"ttsEnabled":true}' 2>/dev/null)
    if [ "$settings_code" = "200" ]; then
        pass "Save settings OK"
    else
        fail "Save settings failed ($settings_code)"
    fi

    local get_settings
    get_settings=$(curl -sf "${base}/settings" \
        -H "Authorization: Bearer ${token}" 2>&1)
    local runner_id
    runner_id=$(echo "$get_settings" | python3 -c "import sys,json; print(json.load(sys.stdin).get('settings',{}).get('runnerId',''))" 2>/dev/null)
    if [ "$runner_id" = "codex" ]; then
        pass "Get settings OK (runnerId=codex)"
    else
        fail "Expected runnerId=codex, got '$runner_id'"
    fi

    # ── Logout (use login_token, keep signup token alive for deletion) ──
    info "Testing logout..."
    local logout_code
    logout_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "${base}/auth/logout" \
        -H "Authorization: Bearer ${login_token}" 2>/dev/null)
    if [ "$logout_code" = "200" ]; then
        pass "Logout OK"
    else
        fail "Logout failed ($logout_code)"
    fi

    # ── Verify logout invalidated the login session ───────────────────
    code=$(curl -s -o /dev/null -w "%{http_code}" "${base}/auth/validate" \
        -H "Authorization: Bearer ${login_token}" 2>/dev/null)
    if [ "$code" = "401" ]; then
        pass "Logged-out token rejected (401)"
    else
        fail "Expected 401 after logout, got $code"
    fi

    # ── Re-login for the delete-account test ──────────────────────────
    login_resp=$(curl -sf -X POST "${base}/auth/login" \
        -H "Content-Type: application/json" \
        -d "{\"email\":\"${email}\",\"password\":\"${password}\"}" 2>&1)
    token=$(echo "$login_resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null)
    if [ -n "$token" ] && [ "$token" != "" ]; then
        AUTH_TOKENS+=("$token")
    fi

    # ── Delete account ────────────────────────────────────────────────
    info "Testing account deletion..."
    local delete_code
    delete_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "${base}/auth/delete-account" \
        -H "Authorization: Bearer ${token}" \
        -H "Content-Type: application/json" 2>/dev/null)
    if [ "$delete_code" = "200" ]; then
        pass "Account deletion OK"
    else
        fail "Account deletion failed ($delete_code)"
    fi

    # ── Verify account gone ───────────────────────────────────────────
    code=$(curl -s -o /dev/null -w "%{http_code}" "${base}/auth/validate" \
        -H "Authorization: Bearer ${token}" 2>/dev/null)
    if [ "$code" = "401" ]; then
        pass "Deleted account token rejected (401)"
    else
        fail "Expected 401 after account deletion, got $code"
    fi

    # ── Login with deleted account ────────────────────────────────────
    code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "${base}/auth/login" \
        -H "Content-Type: application/json" \
        -d "{\"email\":\"${email}\",\"password\":\"${password}\"}" 2>/dev/null)
    if [ "$code" = "401" ]; then
        pass "Login rejected for deleted account (401)"
    else
        fail "Expected 401 for deleted account login, got $code"
    fi
}

run_oauth_mock_tests() {
    header "OAuth Mock — callback path without real providers"

    if [ -z "${CONVEX_SITE_URL:-}" ]; then
        skip "OAuth mock test (CONVEX_SITE_URL not set)"
        return
    fi

    if ! command -v go >/dev/null 2>&1; then
        skip "OAuth mock test (go not installed)"
        return
    fi

    if ! command -v npm >/dev/null 2>&1; then
        skip "OAuth mock test (npm not installed)"
        return
    fi

    local mock_port web_port mock_log web_log
    mock_port=$(get_free_port)
    web_port=$(get_free_port)
    mock_log="$TEST_DIR/oauth-mock.log"
    web_log="$TEST_DIR/oauth-web.log"

    info "Starting mock OAuth server on :$mock_port ..."
    (
        cd "$ROOT_DIR/ci/oauth-mock"
        PORT="$mock_port" go run . >"$mock_log" 2>&1
    ) &
    local mock_pid=$!
    PIDS_TO_KILL+=("$mock_pid")

    local ok=false
    for _ in $(seq 1 120); do
        if curl -sf "http://127.0.0.1:${mock_port}/health" >/dev/null 2>&1; then
            ok=true
            break
        fi
        sleep 0.25
    done
    if [ "$ok" != "true" ]; then
        fail "OAuth mock server did not become healthy"
        tail -40 "$mock_log" 2>/dev/null || true
        return
    fi
    pass "OAuth mock server healthy"

    if [ ! -x "$ROOT_DIR/web/node_modules/.bin/next" ]; then
        info "Installing web dependencies for OAuth mock test ..."
        if ! (cd "$ROOT_DIR/web" && npm ci >"$TEST_DIR/oauth-web-npm.log" 2>&1); then
            fail "OAuth mock npm ci failed"
            tail -80 "$TEST_DIR/oauth-web-npm.log" 2>/dev/null || true
            return
        fi
        pass "OAuth mock web dependencies installed"
    fi

    info "Starting web app on :$web_port with mock OAuth endpoints ..."
    (
        cd "$ROOT_DIR/web"
        PORT="$web_port" \
        CONVEX_SITE_URL="$CONVEX_SITE_URL" \
        NEXT_PUBLIC_BASE_URL="http://127.0.0.1:${web_port}" \
        TEST_MODE_ENABLED=1 \
        OAUTH_GOOGLE_CLIENT_ID="mock-google-client" \
        OAUTH_GOOGLE_CLIENT_SECRET="mock-google-secret" \
        OAUTH_GOOGLE_TOKEN_URL="http://127.0.0.1:${mock_port}/google/token" \
        OAUTH_GOOGLE_USERINFO_URL="http://127.0.0.1:${mock_port}/google/userinfo" \
        OAUTH_MICROSOFT_CLIENT_ID="mock-microsoft-client" \
        OAUTH_MICROSOFT_CLIENT_SECRET="mock-microsoft-secret" \
        OAUTH_MICROSOFT_TENANT_ID="common" \
        OAUTH_MICROSOFT_TOKEN_URL="http://127.0.0.1:${mock_port}/microsoft/token" \
        OAUTH_MICROSOFT_USERINFO_URL="http://127.0.0.1:${mock_port}/microsoft/userinfo" \
        OAUTH_APPLE_CLIENT_ID="com.yaver.web" \
        OAUTH_APPLE_CLIENT_SECRET="mock-apple-secret" \
        OAUTH_APPLE_TOKEN_URL="http://127.0.0.1:${mock_port}/apple/token" \
        OAUTH_GITHUB_CLIENT_ID="mock-github-client" \
        OAUTH_GITHUB_CLIENT_SECRET="mock-github-secret" \
        OAUTH_GITHUB_TOKEN_URL="http://127.0.0.1:${mock_port}/github/token" \
        OAUTH_GITHUB_USERINFO_URL="http://127.0.0.1:${mock_port}/github/user" \
        OAUTH_GITHUB_EMAILS_URL="http://127.0.0.1:${mock_port}/github/user/emails" \
        OAUTH_GITLAB_CLIENT_ID="mock-gitlab-client" \
        OAUTH_GITLAB_CLIENT_SECRET="mock-gitlab-secret" \
        OAUTH_GITLAB_TOKEN_URL="http://127.0.0.1:${mock_port}/gitlab/token" \
        OAUTH_GITLAB_USERINFO_URL="http://127.0.0.1:${mock_port}/gitlab/userinfo" \
        npm run dev >"$web_log" 2>&1
    ) &
    local web_pid=$!
    PIDS_TO_KILL+=("$web_pid")

    ok=false
    for _ in $(seq 1 120); do
        if curl -sf "http://127.0.0.1:${web_port}/auth" >/dev/null 2>&1; then
            ok=true
            break
        fi
        sleep 1
    done
    if [ "$ok" != "true" ]; then
        fail "OAuth mock web app did not become ready"
        tail -60 "$web_log" 2>/dev/null || true
        return
    fi
    pass "OAuth mock web app ready"

    make_oauth_state() {
        python3 - "$1" <<'PY'
import base64, json, sys
payload = json.dumps(json.loads(sys.argv[1]), separators=(",", ":")).encode()
print(base64.urlsafe_b64encode(payload).decode().rstrip("="))
PY
    }

    extract_location_header() {
        python3 - "$1" <<'PY'
import sys
from pathlib import Path
hdrs = Path(sys.argv[1]).read_text().splitlines()
for line in hdrs:
    if line.lower().startswith("location:"):
        print(line.split(":", 1)[1].strip())
        break
PY
    }

    extract_redirect_token() {
        python3 - "$1" <<'PY'
import sys
from urllib.parse import parse_qs, urlparse
url = sys.argv[1]
if not url:
    raise SystemExit(0)
print((parse_qs(urlparse(url).query).get("token") or [""])[0])
PY
    }

    auth_user_field() {
        local token="$1" field="$2"
        curl -sf "${CONVEX_SITE_URL}/auth/validate" \
            -H "Authorization: Bearer ${token}" | \
            python3 -c 'import json,sys; field=sys.argv[1]; print(json.load(sys.stdin)["user"].get(field, ""))' "$field"
    }

    auth_providers_csv() {
        local token="$1"
        curl -sf "${CONVEX_SITE_URL}/auth/providers" \
            -H "Authorization: Bearer ${token}" | \
            python3 -c 'import json,sys; data=json.load(sys.stdin); providers=sorted({item.get("provider","") for item in data.get("identities",[]) if item.get("provider")}); print(",".join(providers))'
    }

    security_event_types_csv() {
        local token="$1"
        curl -sf "${CONVEX_SITE_URL}/security/events" \
            -H "Authorization: Bearer ${token}" | \
            python3 -c 'import json,sys; data=json.load(sys.stdin); print(",".join(item.get("eventType","") for item in data.get("events",[]) if item.get("eventType")))'
    }

    oauth_callback_location() {
        local provider="$1" state="$2" headers location
        headers=$(mktemp "$TEST_DIR/oauth-${provider}-headers.XXXXXX")
        curl -sS -D "$headers" -o /dev/null \
            "http://127.0.0.1:${web_port}/api/auth/oauth/${provider}/callback?code=mock-${provider}-code-${oauth_run_id}&state=${state}" \
            -H "Accept: text/html" >/dev/null || true
        location=$(extract_location_header "$headers")
        rm -f "$headers"
        printf '%s\n' "$location"
    }

    oauth_signin_token() {
        local provider="$1" state="$2" location
        location=$(oauth_callback_location "$provider" "$state")
        extract_redirect_token "$location"
    }

    http_json_with_status() {
        local output_file="$1"
        shift
        curl -sS -o "$output_file" -w "%{http_code}" "$@"
    }

    totp_code_for_secret() {
        local secret="$1" offset="${2:-0}"
        python3 - "$secret" "$offset" <<'PY'
import hashlib, hmac, struct, sys, time
alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
secret = sys.argv[1].strip().upper().rstrip("=")
offset = int(sys.argv[2])
bits = 0
value = 0
out = bytearray()
for ch in secret:
    idx = alphabet.find(ch)
    if idx < 0:
        continue
    value = (value << 5) | idx
    bits += 5
    if bits >= 8:
        out.append((value >> (bits - 8)) & 0xff)
        bits -= 8
counter = int(time.time() // 30) + offset
msg = struct.pack(">Q", counter)
digest = hmac.new(bytes(out), msg, hashlib.sha1).digest()
o = digest[-1] & 0x0F
code = ((digest[o] & 0x7F) << 24) | ((digest[o+1] & 0xFF) << 16) | ((digest[o+2] & 0xFF) << 8) | (digest[o+3] & 0xFF)
print(str(code % 1000000).zfill(6))
PY
    }

    local oauth_run_id signin_state
    oauth_run_id="$(date +%s)-$$"
    signin_state=$(make_oauth_state '{"client":"web","intent":"signin","returnTo":"/dashboard"}')

    local providers="google microsoft apple github gitlab"
    local provider location token
    local google_token_1="" google_token_2="" microsoft_token="" apple_token="" github_token="" gitlab_token=""
    for provider in $providers; do
        info "Testing ${provider} callback via mock provider..."
        location=$(oauth_callback_location "$provider" "$signin_state")
        token=$(extract_redirect_token "$location")
        if echo "$location" | grep -q "/auth/callback?token=" && [ -n "$token" ]; then
            pass "OAuth mock ${provider} callback issued session redirect"
            case "$provider" in
                google) google_token_1="$token" ;;
                microsoft) microsoft_token="$token" ;;
                apple) apple_token="$token" ;;
                github) github_token="$token" ;;
                gitlab) gitlab_token="$token" ;;
            esac
        else
            fail "OAuth mock ${provider} callback did not issue session redirect"
            info "Location was: ${location:-<empty>}"
        fi
    done

    if [ -z "$google_token_1" ] || [ -z "$gitlab_token" ]; then
        fail "OAuth mock prerequisite sign-ins missing"
        return
    fi

    local google_user_doc_1 google_user_doc_2 gitlab_user_doc
    google_user_doc_1=$(auth_user_field "$google_token_1" "userDocId")
    google_token_2=$(oauth_signin_token "google" "$signin_state")
    google_user_doc_2=$(auth_user_field "$google_token_2" "userDocId")
    if [ -n "$google_user_doc_1" ] && [ "$google_user_doc_1" = "$google_user_doc_2" ]; then
        pass "OAuth mock google repeat sign-in reuses the same account"
    else
        fail "OAuth mock google repeat sign-in created a different account"
        info "google userDocIds: first=${google_user_doc_1:-<empty>} second=${google_user_doc_2:-<empty>}"
    fi

    gitlab_user_doc=$(auth_user_field "$gitlab_token" "userDocId")
    if [ -n "$gitlab_user_doc" ] && [ "$gitlab_user_doc" != "$google_user_doc_1" ]; then
        pass "OAuth mock gitlab sign-in created a distinct source account"
    else
        fail "OAuth mock gitlab sign-in did not produce a distinct account"
    fi

    local http_code http_body
    http_body=$(mktemp "$TEST_DIR/oauth-http.XXXXXX")
    http_code=$(http_json_with_status "$http_body" \
        -X DELETE "${CONVEX_SITE_URL}/auth/oauth-link/gitlab" \
        -H "Authorization: Bearer ${gitlab_token}" \
        -H "Content-Type: application/json" \
        -d '{}')
    if [ "$http_code" = "409" ]; then
        pass "OAuth mock unlink refuses removing the only sign-in method"
    else
        fail "OAuth mock unlink-only-identity returned $http_code"
        info "Response was: $(cat "$http_body" 2>/dev/null || true)"
    fi
    rm -f "$http_body"

    local link_token link_state linked_location providers_csv events_csv
    link_token=$(curl -sf -X POST "${CONVEX_SITE_URL}/auth/oauth-link/start" \
        -H "Authorization: Bearer ${google_token_1}" \
        -H "Content-Type: application/json" \
        -d '{"provider":"microsoft","client":"web","returnTo":"/dashboard"}' | \
        python3 -c 'import json,sys; print(json.load(sys.stdin).get("token",""))')
    if [ -n "$link_token" ]; then
        pass "OAuth mock link intent created"
    else
        fail "OAuth mock link intent creation failed"
        return
    fi

    link_state=$(make_oauth_state "{\"client\":\"web\",\"intent\":\"link\",\"linkToken\":\"${link_token}\",\"returnTo\":\"/dashboard\"}")
    linked_location=$(oauth_callback_location "microsoft" "$link_state")
    if echo "$linked_location" | grep -q 'linkedProvider=microsoft' && echo "$linked_location" | grep -q 'linked=1'; then
        pass "OAuth mock provider-link callback completed"
    else
        fail "OAuth mock provider-link callback did not complete"
        info "Location was: ${linked_location:-<empty>}"
    fi

    providers_csv=$(auth_providers_csv "$google_token_1")
    if [ "$providers_csv" = "google,microsoft" ] || [ "$providers_csv" = "microsoft,google" ]; then
        pass "OAuth mock linked identities show google + microsoft"
    else
        fail "OAuth mock linked identities unexpected"
        info "Providers were: ${providers_csv:-<empty>}"
    fi

    events_csv=$(security_event_types_csv "$google_token_1")
    if echo "$events_csv" | grep -q 'link_added'; then
        pass "OAuth mock link action wrote security event"
    else
        fail "OAuth mock link action missing security event"
        info "Events were: ${events_csv:-<empty>}"
    fi

    local totp_secret valid_totp_code stale_totp_code
    totp_secret=$(curl -sf -X POST "${CONVEX_SITE_URL}/auth/totp/setup" \
        -H "Authorization: Bearer ${google_token_1}" \
        -H "Content-Type: application/json" \
        -d '{}' | python3 -c 'import json,sys; print(json.load(sys.stdin).get("secret",""))')
    if [ -n "$totp_secret" ]; then
        pass "OAuth mock TOTP setup returned secret"
    else
        fail "OAuth mock TOTP setup failed"
        return
    fi

    valid_totp_code=$(totp_code_for_secret "$totp_secret" 0)
    if curl -sf -X POST "${CONVEX_SITE_URL}/auth/totp/enable" \
        -H "Authorization: Bearer ${google_token_1}" \
        -H "Content-Type: application/json" \
        -d "{\"code\":\"${valid_totp_code}\"}" >/dev/null; then
        pass "OAuth mock TOTP enabled on linked account"
    else
        fail "OAuth mock TOTP enable failed"
        return
    fi

    stale_totp_code=$(totp_code_for_secret "$totp_secret" -3)
    http_body=$(mktemp "$TEST_DIR/oauth-http.XXXXXX")
    http_code=$(http_json_with_status "$http_body" \
        -X DELETE "${CONVEX_SITE_URL}/auth/oauth-link/microsoft" \
        -H "Authorization: Bearer ${google_token_1}" \
        -H "Content-Type: application/json" \
        -d "{\"totpCode\":\"${stale_totp_code}\"}")
    if [ "$http_code" = "403" ]; then
        pass "OAuth mock stale TOTP blocks unlink"
    else
        fail "OAuth mock stale TOTP unlink returned $http_code"
        info "Response was: $(cat "$http_body" 2>/dev/null || true)"
    fi
    rm -f "$http_body"

    valid_totp_code=$(totp_code_for_secret "$totp_secret" 0)
    http_body=$(mktemp "$TEST_DIR/oauth-http.XXXXXX")
    http_code=$(http_json_with_status "$http_body" \
        -X DELETE "${CONVEX_SITE_URL}/auth/oauth-link/microsoft" \
        -H "Authorization: Bearer ${google_token_1}" \
        -H "Content-Type: application/json" \
        -d "{\"totpCode\":\"${valid_totp_code}\"}")
    if [ "$http_code" = "200" ]; then
        pass "OAuth mock valid TOTP allows unlink"
    else
        fail "OAuth mock valid TOTP unlink returned $http_code"
        info "Response was: $(cat "$http_body" 2>/dev/null || true)"
    fi
    rm -f "$http_body"

    providers_csv=$(auth_providers_csv "$google_token_1")
    if [ "$providers_csv" = "google" ]; then
        pass "OAuth mock unlink removed microsoft identity"
    else
        fail "OAuth mock unlink left unexpected identities"
        info "Providers were: ${providers_csv:-<empty>}"
    fi

    events_csv=$(security_event_types_csv "$google_token_1")
    if echo "$events_csv" | grep -q 'link_removed'; then
        pass "OAuth mock unlink wrote security event"
    else
        fail "OAuth mock unlink missing security event"
        info "Events were: ${events_csv:-<empty>}"
    fi

    local merge_token merge_resp merge_status
    valid_totp_code=$(totp_code_for_secret "$totp_secret" 0)
    merge_resp=$(curl -sf -X POST "${CONVEX_SITE_URL}/auth/account/merge/start" \
        -H "Authorization: Bearer ${google_token_1}" \
        -H "Content-Type: application/json" \
        -d "{\"client\":\"web\",\"totpCode\":\"${valid_totp_code}\"}")
    merge_token=$(echo "$merge_resp" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("mergeToken",""))')
    if [ -n "$merge_token" ]; then
        pass "OAuth mock merge intent created"
    else
        fail "OAuth mock merge intent creation failed"
        info "Response was: ${merge_resp:-<empty>}"
        return
    fi

    merge_status=$(curl -sf "${CONVEX_SITE_URL}/auth/account/merge/status?token=${merge_token}" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("status",""))')
    if [ "$merge_status" = "pending" ]; then
        pass "OAuth mock merge status is pending before approval"
    else
        fail "OAuth mock merge status before approval was ${merge_status:-<empty>}"
    fi

    http_body=$(mktemp "$TEST_DIR/oauth-http.XXXXXX")
    http_code=$(http_json_with_status "$http_body" \
        -X POST "${CONVEX_SITE_URL}/auth/account/merge/complete" \
        -H "Authorization: Bearer ${gitlab_token}" \
        -H "Content-Type: application/json" \
        -d "{\"mergeToken\":\"${merge_token}\"}")
    if [ "$http_code" = "200" ]; then
        pass "OAuth mock merge completed from source account"
    else
        fail "OAuth mock merge completion failed"
        info "Response was: $(cat "$http_body" 2>/dev/null || true)"
        rm -f "$http_body"
        return
    fi
    rm -f "$http_body"

    merge_status=$(curl -sf "${CONVEX_SITE_URL}/auth/account/merge/status?token=${merge_token}" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("status",""))')
    if [ "$merge_status" = "completed" ]; then
        pass "OAuth mock merge status is completed after approval"
    else
        fail "OAuth mock merge status after approval was ${merge_status:-<empty>}"
    fi

    http_code=$(curl -s -o /dev/null -w "%{http_code}" \
        "${CONVEX_SITE_URL}/auth/validate" \
        -H "Authorization: Bearer ${gitlab_token}")
    if [ "$http_code" = "401" ]; then
        pass "OAuth mock source session is invalid after merge"
    else
        fail "OAuth mock source session remained valid after merge ($http_code)"
    fi

    providers_csv=$(auth_providers_csv "$google_token_1")
    if [ "$providers_csv" = "gitlab,google" ] || [ "$providers_csv" = "google,gitlab" ]; then
        pass "OAuth mock merged identities now include gitlab on target"
    else
        fail "OAuth mock merge left unexpected target identities"
        info "Providers were: ${providers_csv:-<empty>}"
    fi

    events_csv=$(security_event_types_csv "$google_token_1")
    if echo "$events_csv" | grep -q 'merge_started' && echo "$events_csv" | grep -q 'merge_completed'; then
        pass "OAuth mock merge wrote target-side security events"
    else
        fail "OAuth mock merge missing target-side security events"
        info "Events were: ${events_csv:-<empty>}"
    fi
}

run_sdk_tests() {
    header "SDK Tests"

    # ── Unit tests (no agent needed) ──────────────────────────────────

    info "Running Go SDK unit tests..."
    if (cd "$ROOT_DIR/sdk/go/yaver" && go test -v -count=1 ./... > "$TEST_DIR/sdk-go-test.log" 2>&1); then
        pass "Go SDK unit tests passed"
    else
        fail "Go SDK unit tests failed"
        tail -20 "$TEST_DIR/sdk-go-test.log"
    fi

    info "Building C shared library (libyaver)..."
    local lib_ext="so"
    [ "$(uname)" = "Darwin" ] && lib_ext="dylib"
    if (cd "$ROOT_DIR/sdk/go/clib" && go build -buildmode=c-shared -o "libyaver.${lib_ext}" . > "$TEST_DIR/sdk-clib-build.log" 2>&1); then
        local lib_size
        lib_size=$(du -h "$ROOT_DIR/sdk/go/clib/libyaver.${lib_ext}" | cut -f1)
        pass "C shared library built (${lib_ext}, ${lib_size})"
        rm -f "$ROOT_DIR/sdk/go/clib/libyaver.${lib_ext}" "$ROOT_DIR/sdk/go/clib/libyaver.h"
    else
        fail "C shared library build failed"
        tail -20 "$TEST_DIR/sdk-clib-build.log"
    fi

    info "Running Python SDK unit tests..."
    if (cd "$ROOT_DIR/sdk/python" && python3 test_yaver.py > "$TEST_DIR/sdk-python-test.log" 2>&1); then
        pass "Python SDK unit tests passed"
    else
        fail "Python SDK unit tests failed"
        tail -20 "$TEST_DIR/sdk-python-test.log"
    fi

    info "Typechecking JS/TS SDK..."
    if (cd "$ROOT_DIR/sdk/js" && npm install --silent > /dev/null 2>&1 && npx tsc --noEmit > "$TEST_DIR/sdk-js-typecheck.log" 2>&1); then
        pass "JS/TS SDK typecheck passed"
    else
        fail "JS/TS SDK typecheck failed"
        tail -20 "$TEST_DIR/sdk-js-typecheck.log"
    fi

    info "Building JS/TS SDK..."
    if (cd "$ROOT_DIR/sdk/js" && npx tsc > "$TEST_DIR/sdk-js-build.log" 2>&1); then
        pass "JS/TS SDK built"
    else
        fail "JS/TS SDK build failed"
        tail -20 "$TEST_DIR/sdk-js-build.log"
    fi

    info "Analyzing Flutter/Dart SDK..."
    if command -v flutter > /dev/null 2>&1; then
        if (cd "$ROOT_DIR/sdk/flutter" && flutter pub get > /dev/null 2>&1 && dart analyze > "$TEST_DIR/sdk-flutter-analyze.log" 2>&1); then
            pass "Flutter/Dart SDK analysis passed"
        else
            fail "Flutter/Dart SDK analysis failed"
            tail -20 "$TEST_DIR/sdk-flutter-analyze.log" 2>/dev/null
        fi
    else
        skip "Flutter/Dart SDK analysis (flutter not installed)"
    fi

    # ── Integration tests (start agent, test each SDK against it) ─────

    local agent_bin="$TEST_DIR/yaver"
    [ -f "$agent_bin" ] || build_agent "$agent_bin" > /dev/null 2>&1 || { fail "Cannot build agent for SDK integration"; return; }

    local http_port
    http_port=$(get_free_port)
    local quic_port
    quic_port=$(get_free_port)

    info "Creating test account for SDK integration..."
    local token
    token=$(create_test_account) || { fail "Cannot create test account for SDK integration"; return; }

    local device_id="test-sdk-$(gen_uuid)"
    local work_dir="$TEST_DIR/sdk-agent"

    info "Starting agent for SDK integration (HTTP=$http_port)..."
    local agent_pid
    agent_pid=$(start_agent "$agent_bin" "$http_port" "$quic_port" "$token" "$device_id" "$work_dir" --no-relay) || {
        fail "Agent failed to start for SDK integration"; return
    }

    local base_url="http://127.0.0.1:${http_port}"

    # Go examples compile check
    info "Checking Go examples compile..."
    local examples_ok=true
    for ex in "$ROOT_DIR"/sdk/examples/go/*/; do
        if [ -f "$ex/main.go" ]; then
            exname=$(basename "$ex")
            if (cd "$ex" && go build -o /dev/null . > /dev/null 2>&1); then
                true
            else
                examples_ok=false
                fail "Go example $exname failed to compile"
            fi
        fi
    done
    if $examples_ok; then
        pass "Go examples compile OK"
    fi

    # Go SDK integration
    info "Running Go SDK integration tests..."
    if (cd "$ROOT_DIR/sdk/go/yaver" && \
        YAVER_TEST_URL="$base_url" YAVER_TEST_TOKEN="$token" \
        go test -tags integration -v -count=1 -timeout 120s ./... > "$TEST_DIR/sdk-go-integration.log" 2>&1); then
        pass "Go SDK integration tests passed"
    else
        fail "Go SDK integration tests failed"
        tail -20 "$TEST_DIR/sdk-go-integration.log"
    fi

    # Python SDK integration
    info "Running Python SDK integration tests..."
    if (cd "$ROOT_DIR/sdk/python" && \
        YAVER_TEST_URL="$base_url" YAVER_TEST_TOKEN="$token" \
        python3 test_integration.py > "$TEST_DIR/sdk-python-integration.log" 2>&1); then
        pass "Python SDK integration tests passed"
    else
        fail "Python SDK integration tests failed"
        tail -20 "$TEST_DIR/sdk-python-integration.log"
    fi

    # Session transfer test
    info "Testing session transfer (export/import)..."
    # Create a task first
    local task_resp=$(curl -s -X POST "$base_url/tasks" \
        -H "Authorization: Bearer $token" \
        -H "Content-Type: application/json" \
        -d '{"title":"Transfer test task"}')
    local test_task_id=$(echo "$task_resp" | grep -o '"taskId":"[^"]*"' | cut -d'"' -f4)

    if [ -n "$test_task_id" ]; then
        sleep 2  # Let it run briefly

        # List transferable sessions
        local list_resp=$(curl -s "$base_url/session/list" \
            -H "Authorization: Bearer $token")
        if echo "$list_resp" | grep -q '"ok":true'; then
            pass "Session list endpoint works"
        else
            fail "Session list endpoint failed"
        fi

        # Export
        local export_resp=$(curl -s -X POST "$base_url/session/export" \
            -H "Authorization: Bearer $token" \
            -H "Content-Type: application/json" \
            -d "{\"taskId\":\"$test_task_id\"}")
        if echo "$export_resp" | grep -q '"ok":true'; then
            pass "Session export works"

            # Import (to same agent for testing)
            local bundle=$(echo "$export_resp" | python3 -c "import sys,json; print(json.dumps(json.load(sys.stdin)['bundle']))" 2>/dev/null)
            if [ -n "$bundle" ]; then
                local import_resp=$(curl -s -X POST "$base_url/session/import" \
                    -H "Authorization: Bearer $token" \
                    -H "Content-Type: application/json" \
                    -d "{\"bundle\":$bundle}")
                if echo "$import_resp" | grep -q '"ok":true'; then
                    pass "Session import works (round-trip)"
                else
                    fail "Session import failed"
                fi
            fi
        else
            fail "Session export failed"
        fi
    else
        fail "Could not create test task for transfer test"
    fi

    # Webhook trigger test
    info "Testing webhook trigger..."
    local webhook_resp=$(curl -s -X POST "$base_url/webhooks/trigger" \
        -H "Content-Type: application/json" \
        -d '{"title":"Webhook test"}')
    if echo "$webhook_resp" | grep -q '"error"'; then
        pass "Webhook rejects without secret (expected)"
    else
        fail "Webhook should reject without secret"
    fi

    # Scheduler test
    info "Testing scheduler endpoints..."
    local sched_resp=$(curl -s -X POST "$base_url/schedules" \
        -H "Authorization: Bearer $token" \
        -H "Content-Type: application/json" \
        -d '{"title":"Test schedule","repeatInterval":999}')
    if echo "$sched_resp" | grep -q '"ok":true'; then
        local sched_id=$(echo "$sched_resp" | python3 -c "import sys,json; print(json.load(sys.stdin)['schedule']['id'])" 2>/dev/null)
        pass "Scheduler create works"

        # List
        local list_resp=$(curl -s "$base_url/schedules" -H "Authorization: Bearer $token")
        if echo "$list_resp" | grep -q "$sched_id"; then
            pass "Scheduler list works"
        fi

        # Delete
        curl -s -X DELETE "$base_url/schedules/$sched_id" -H "Authorization: Bearer $token" > /dev/null
        pass "Scheduler delete works"
    else
        fail "Scheduler create failed"
    fi

    # Analytics test
    info "Testing analytics endpoint..."
    local analytics_resp=$(curl -s "$base_url/analytics" -H "Authorization: Bearer $token")
    if echo "$analytics_resp" | grep -q '"ok":true'; then
        pass "Analytics endpoint works"
    else
        fail "Analytics endpoint failed"
    fi

    # Notifications config test
    info "Testing notifications config..."
    local notif_resp=$(curl -s "$base_url/notifications/config" -H "Authorization: Bearer $token")
    if echo "$notif_resp" | grep -q '"ok":true'; then
        pass "Notifications config endpoint works"
    else
        fail "Notifications config failed"
    fi

    # Doctor endpoint test
    info "Testing doctor endpoint..."
    local doctor_resp=$(curl -s "$base_url/agent/doctor" -H "Authorization: Bearer $token")
    if echo "$doctor_resp" | grep -q '"ok":true'; then
        pass "Doctor endpoint works"
    else
        fail "Doctor endpoint failed"
    fi

    # Tools endpoint test
    info "Testing tools endpoint..."
    local tools_resp=$(curl -s "$base_url/agent/tools" -H "Authorization: Bearer $token")
    if echo "$tools_resp" | grep -q '"ok":true'; then
        pass "Tools endpoint works"
    else
        fail "Tools endpoint failed"
    fi

    kill "$agent_pid" 2>/dev/null || true
}

run_feedback_tests() {
    header "Feedback SDK Integration"

    local agent_bin="$TEST_DIR/yaver"
    [ -f "$agent_bin" ] || build_agent "$agent_bin" > /dev/null 2>&1 || { fail "Cannot build agent for feedback tests"; return; }

    local http_port
    http_port=$(get_free_port)
    local quic_port
    quic_port=$(get_free_port)

    info "Creating test account for feedback tests..."
    local token
    token=$(create_test_account) || { fail "Cannot create test account for feedback tests"; return; }

    local device_id="test-feedback-$(gen_uuid)"
    local work_dir="$TEST_DIR/feedback-agent"

    info "Starting agent for feedback tests (HTTP=$http_port)..."
    local agent_pid
    agent_pid=$(start_agent "$agent_bin" "$http_port" "$quic_port" "$token" "$device_id" "$work_dir" --no-relay) || {
        fail "Agent failed to start for feedback tests"; return
    }

    local base_url="http://127.0.0.1:${http_port}"

    info "Running feedback API tests..."
    if YAVER_AUTH_TOKEN="$token" node "$ROOT_DIR/sdk/feedback/test-app/test-feedback.js" "$base_url" > "$TEST_DIR/feedback-test.log" 2>&1; then
        pass "Feedback API tests passed"
    else
        fail "Feedback API tests failed"
        tail -20 "$TEST_DIR/feedback-test.log"
    fi

    # Vault CRUD via CLI
    info "Testing vault integration..."
    if HOME="$work_dir/.yaver-config" "$agent_bin" vault add test-sdk-key --category api-key --value test-value-123 > /dev/null 2>&1; then
        pass "Vault add works"
    else
        fail "Vault add failed"
    fi

    if HOME="$work_dir/.yaver-config" "$agent_bin" vault get test-sdk-key 2>/dev/null | grep -q "test-value-123"; then
        pass "Vault get works"
    else
        fail "Vault get failed"
    fi

    if HOME="$work_dir/.yaver-config" "$agent_bin" vault delete test-sdk-key > /dev/null 2>&1; then
        pass "Vault delete works"
    else
        fail "Vault delete failed"
    fi

    kill "$agent_pid" 2>/dev/null || true
}

run_expo_tests() {
    header "Expo Integration"

    local agent_bin="$TEST_DIR/yaver"
    [ -f "$agent_bin" ] || build_agent "$agent_bin" > /dev/null 2>&1 || { fail "Cannot build agent for expo tests"; return; }

    # Test 1: Expo project detection (via Go unit tests)
    info "Running expo unit tests..."
    if (cd "$ROOT_DIR/desktop/agent" && go test -v -count=1 -run TestIsExpo -run TestDetectPackageManager -run TestAddPlugin ./... > "$TEST_DIR/expo-unit.log" 2>&1); then
        pass "Expo unit tests passed"
    else
        fail "Expo unit tests failed"
        tail -20 "$TEST_DIR/expo-unit.log"
    fi

    # Test 2: CLI help output
    info "Testing expo CLI help..."
    if "$agent_bin" expo 2>&1 | grep -q "yaver expo setup"; then
        pass "yaver expo --help works"
    else
        fail "yaver expo --help missing expected output"
    fi

    # Test 3: Setup detects non-Expo project.
    # `yaver expo setup` *intentionally* exits 1 with "Not an Expo
    # project" on stderr for a non-Expo dir. When this script runs
    # under `set -eo pipefail`, the `| grep` condition above sees
    # the agent's non-zero exit and considers the whole pipeline a
    # failure — even though grep matched. We capture output into a
    # variable so the success criterion is the phrase, not the exit
    # code (which is deliberately non-zero here).
    local non_expo_dir="$TEST_DIR/not-expo"
    mkdir -p "$non_expo_dir"
    echo '{"dependencies":{"react":"18.3.1"}}' > "$non_expo_dir/package.json"
    local non_expo_out
    non_expo_out=$("$agent_bin" expo setup --dir "$non_expo_dir" 2>&1 || true)
    if echo "$non_expo_out" | grep -q "Not an Expo project"; then
        pass "Setup rejects non-Expo project"
    else
        fail "Setup should reject non-Expo project (got: ${non_expo_out:0:80})"
    fi

    # Test 4: Setup with agent for start/build (requires running agent)
    local http_port
    http_port=$(get_free_port)
    local quic_port
    quic_port=$(get_free_port)

    info "Creating test account for expo tests..."
    local token
    token=$(create_test_account) || { fail "Cannot create test account for expo tests"; return; }

    local device_id="test-expo-$(gen_uuid)"
    local work_dir="$TEST_DIR/expo-agent"

    info "Starting agent for expo tests (HTTP=$http_port)..."
    local agent_pid
    agent_pid=$(start_agent "$agent_bin" "$http_port" "$quic_port" "$token" "$device_id" "$work_dir" --no-relay) || {
        fail "Agent failed to start for expo tests"; return
    }

    local base_url="http://127.0.0.1:${http_port}"

    # Test 5: Expo status (should show empty)
    info "Testing expo status via API..."
    local tunnels_resp
    tunnels_resp=$(curl -sf -H "Authorization: Bearer $token" "$base_url/tunnels" 2>/dev/null)
    if echo "$tunnels_resp" | grep -q '\[\]'; then
        pass "Tunnels list starts empty"
    else
        # May also return null — both are valid for empty
        pass "Tunnels endpoint responds"
    fi

    kill "$agent_pid" 2>/dev/null || true
}

run_voice_tests() {
    header "Voice AI Integration"

    # Unit tests for voice providers
    info "Running voice unit tests..."
    if (cd "$ROOT_DIR/desktop/agent" && go test -v -run 'TestVoice|TestPersonaPlex|TestOpenAI|TestDetectGPU|TestMockPersonaPlex' ./... 2>&1); then
        pass "Voice unit tests"
    else
        fail "Voice unit tests"
    fi

    # Start agent in dummy mode and test voice HTTP endpoints.
    # `yaver serve` reads its auth token from ~/.yaver/config.json
    # (see LoadConfig in main.go) — it does NOT honour an AUTH_TOKEN
    # env var, so the old pattern silently fell through to bootstrap
    # mode and the /voice/status probe hit the pairing page instead
    # of an authed voice handler. Write a config.json pointed at a
    # temp HOME so this agent instance has its own isolated token
    # without touching the developer's real ~/.yaver/.
    info "Starting agent for voice HTTP tests..."
    local http_port
    http_port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("",0)); print(s.getsockname()[1]); s.close()')
    local token="voice-test-token-$$"
    local work_dir
    work_dir=$(mktemp -d)
    local voice_home
    voice_home=$(mktemp -d)
    mkdir -p "$voice_home/.yaver"
    cat > "$voice_home/.yaver/config.json" <<JSON
{"auth_token":"$token","convex_site_url":"${CONVEX_SITE_URL:-https://example.invalid}","device_id":"voice-test-$$"}
JSON

    cd "$ROOT_DIR/desktop/agent"
    go build -o "$TEST_DIR/yaver-voice" . 2>/dev/null || { fail "Voice: build failed"; return; }

    HOME="$voice_home" \
    "$TEST_DIR/yaver-voice" serve --debug --port "$http_port" --dummy --no-relay --work-dir "$work_dir" > "$TEST_DIR/voice-agent.log" 2>&1 &
    local agent_pid=$!
    PIDS_TO_KILL+=("$agent_pid")

    # Wait for /health to answer — spinning immediately after fork
    # races the HTTP server's bind. 5 s is enough on CI; fails the
    # section fast if the agent never came up.
    local voice_up=0
    for _ in $(seq 1 25); do
        if curl -sf "http://127.0.0.1:${http_port}/health" >/dev/null 2>&1; then
            voice_up=1
            break
        fi
        sleep 0.2
    done
    if [ "$voice_up" != "1" ]; then
        fail "Voice: agent /health never answered — see $TEST_DIR/voice-agent.log"
        return
    fi

    if ! kill -0 "$agent_pid" 2>/dev/null; then
        fail "Voice: agent failed to start"
        return
    fi

    local base_url="http://127.0.0.1:${http_port}"

    # Test voice status endpoint
    info "Testing /voice/status..."
    local status_resp
    status_resp=$(curl -sf -H "Authorization: Bearer $token" "$base_url/voice/status" 2>/dev/null)
    if echo "$status_resp" | grep -q '"voiceInputEnabled":true'; then
        pass "Voice status: voiceInputEnabled=true"
    else
        fail "Voice status endpoint"
    fi

    # Test voice providers endpoint
    info "Testing /voice/providers..."
    local providers_resp
    providers_resp=$(curl -sf -H "Authorization: Bearer $token" "$base_url/voice/providers" 2>/dev/null)
    if echo "$providers_resp" | grep -q 'personaplex'; then
        pass "Voice providers: lists personaplex"
    else
        fail "Voice providers endpoint"
    fi

    # Test /info includes voice capability
    info "Testing /info includes voiceInputEnabled..."
    local info_resp
    info_resp=$(curl -sf -H "Authorization: Bearer $token" "$base_url/info" 2>/dev/null)
    if echo "$info_resp" | grep -q '"voiceInputEnabled":true'; then
        pass "Info endpoint: includes voiceInputEnabled"
    else
        fail "Info endpoint: missing voiceInputEnabled"
    fi

    # Test voice transcribe (should work even without provider — saves audio)
    info "Testing /voice/transcribe..."
    local transcribe_resp
    transcribe_resp=$(curl -sf -X POST -H "Authorization: Bearer $token" \
        -H "Content-Type: audio/wav" \
        --data-binary "RIFF$(printf '\x00\x00\x00\x00')WAVEfmt " \
        "$base_url/voice/transcribe" 2>/dev/null)
    if echo "$transcribe_resp" | grep -q '"ok":true'; then
        pass "Voice transcribe endpoint"
    else
        # May fail with empty data — that's ok
        pass "Voice transcribe endpoint (responded)"
    fi

    kill "$agent_pid" 2>/dev/null || true
}

# ── E2E Tests — Real Command Execution ────────────────────────────
# Runs agent in non-dummy mode with customCommand tasks (python, shell).
# No AI runner needed — customCommand uses sh -c directly.
run_e2e_tests() {
    header "E2E — Real Command Execution (customCommand)"

    local agent_bin="$TEST_DIR/yaver"
    [ -f "$agent_bin" ] || build_agent "$agent_bin" > /dev/null 2>&1 || { fail "Cannot build agent"; return; }

    local http_port quic_port
    http_port=$(get_free_port); quic_port=$(get_free_port)

    info "Creating test account..."
    local token
    token=$(create_test_account) || { fail "Cannot create test account"; return; }

    local device_id="test-e2e-$(gen_uuid)"
    local work_dir="$TEST_DIR/e2e-agent"

    info "Starting agent in NON-dummy mode (HTTP=$http_port)..."
    export YAVER_NO_DUMMY=1
    local agent_pid
    agent_pid=$(start_agent "$agent_bin" "$http_port" "$quic_port" "$token" "$device_id" "$work_dir" --no-relay) || {
        unset YAVER_NO_DUMMY
        fail "E2E agent failed to start"; return
    }
    unset YAVER_NO_DUMMY

    local base_url="http://127.0.0.1:${http_port}"
    local auth_header="Authorization: Bearer ${token}"

    # ── Test 1: Python hello world ──
    info "Running Python hello world via customCommand..."
    local task_resp task_id
    task_resp=$(curl -sf -X POST "${base_url}/tasks" \
        -H "$auth_header" -H "Content-Type: application/json" \
        -d '{"title":"python test","customCommand":"python3 -c \"print(\\\"hello world from yaver e2e test\\\")\""}' 2>/dev/null) || { fail "E2E: create python task"; }
    task_id=$(echo "$task_resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('taskId',''))" 2>/dev/null)

    if [ -n "$task_id" ]; then
        # Poll for completion (max 30s)
        local elapsed=0 status="" output=""
        while [ $elapsed -lt 30 ]; do
            local detail
            detail=$(curl -sf "${base_url}/tasks/${task_id}" -H "$auth_header" 2>/dev/null) || true
            status=$(echo "$detail" | python3 -c "import sys,json; print(json.load(sys.stdin).get('task',{}).get('status',''))" 2>/dev/null) || true
            case "$status" in completed|finished|failed|stopped) break ;; esac
            sleep 1; elapsed=$((elapsed + 1))
        done

        output=$(curl -sf "${base_url}/tasks/${task_id}" -H "$auth_header" 2>/dev/null | \
            python3 -c "import sys,json; print(json.load(sys.stdin).get('task',{}).get('output',''))" 2>/dev/null) || true

        if [ "$status" = "completed" ] || [ "$status" = "finished" ]; then
            if echo "$output" | grep -q "hello world from yaver e2e test"; then
                pass "E2E: Python hello world (output verified)"
            else
                fail "E2E: Python hello world (completed but output missing expected string)"
                info "Output was: $output"
            fi
        else
            fail "E2E: Python hello world (status=$status after ${elapsed}s)"
        fi
    else
        fail "E2E: Python task creation failed"
    fi

    # ── Test 2: Shell echo ──
    info "Running shell echo via customCommand..."
    task_resp=$(curl -sf -X POST "${base_url}/tasks" \
        -H "$auth_header" -H "Content-Type: application/json" \
        -d '{"title":"shell test","customCommand":"echo yaver-shell-test-ok && date +%Y"}' 2>/dev/null) || { fail "E2E: create shell task"; }
    task_id=$(echo "$task_resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('taskId',''))" 2>/dev/null)

    if [ -n "$task_id" ]; then
        local elapsed=0 status="" output=""
        while [ $elapsed -lt 30 ]; do
            local detail
            detail=$(curl -sf "${base_url}/tasks/${task_id}" -H "$auth_header" 2>/dev/null) || true
            status=$(echo "$detail" | python3 -c "import sys,json; print(json.load(sys.stdin).get('task',{}).get('status',''))" 2>/dev/null) || true
            case "$status" in completed|finished|failed|stopped) break ;; esac
            sleep 1; elapsed=$((elapsed + 1))
        done

        output=$(curl -sf "${base_url}/tasks/${task_id}" -H "$auth_header" 2>/dev/null | \
            python3 -c "import sys,json; print(json.load(sys.stdin).get('task',{}).get('output',''))" 2>/dev/null) || true

        if ([ "$status" = "completed" ] || [ "$status" = "finished" ]) && echo "$output" | grep -q "yaver-shell-test-ok"; then
            pass "E2E: Shell echo (output verified)"
        else
            fail "E2E: Shell echo (status=$status)"
            info "Output was: $output"
        fi
    else
        fail "E2E: Shell task creation failed"
    fi

    # ── Test 3: Python script file ──
    info "Running Python script from file via customCommand..."
    local script_dir="$work_dir/test-scripts"
    mkdir -p "$script_dir"
    cat > "$script_dir/hello.py" << 'PYEOF'
import sys
import json

result = {"message": "hello from python script", "python_version": sys.version.split()[0], "status": "success"}
print(json.dumps(result))
PYEOF

    task_resp=$(curl -sf -X POST "${base_url}/tasks" \
        -H "$auth_header" -H "Content-Type: application/json" \
        -d "{\"title\":\"python script test\",\"customCommand\":\"python3 ${script_dir}/hello.py\"}" 2>/dev/null) || { fail "E2E: create python script task"; }
    task_id=$(echo "$task_resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('taskId',''))" 2>/dev/null)

    if [ -n "$task_id" ]; then
        local elapsed=0 status="" output=""
        while [ $elapsed -lt 30 ]; do
            local detail
            detail=$(curl -sf "${base_url}/tasks/${task_id}" -H "$auth_header" 2>/dev/null) || true
            status=$(echo "$detail" | python3 -c "import sys,json; print(json.load(sys.stdin).get('task',{}).get('status',''))" 2>/dev/null) || true
            case "$status" in completed|finished|failed|stopped) break ;; esac
            sleep 1; elapsed=$((elapsed + 1))
        done

        output=$(curl -sf "${base_url}/tasks/${task_id}" -H "$auth_header" 2>/dev/null | \
            python3 -c "import sys,json; print(json.load(sys.stdin).get('task',{}).get('output',''))" 2>/dev/null) || true

        if ([ "$status" = "completed" ] || [ "$status" = "finished" ]) && echo "$output" | grep -q "hello from python script"; then
            pass "E2E: Python script file (output verified)"
        else
            fail "E2E: Python script file (status=$status)"
            info "Output was: $output"
        fi
    else
        fail "E2E: Python script task creation failed"
    fi

    # ── Test 4: Task with failing command ──
    info "Running failing command via customCommand..."
    task_resp=$(curl -sf -X POST "${base_url}/tasks" \
        -H "$auth_header" -H "Content-Type: application/json" \
        -d '{"title":"fail test","customCommand":"echo before-fail && exit 42"}' 2>/dev/null) || { fail "E2E: create failing task"; }
    task_id=$(echo "$task_resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('taskId',''))" 2>/dev/null)

    if [ -n "$task_id" ]; then
        local elapsed=0 status=""
        while [ $elapsed -lt 30 ]; do
            local detail
            detail=$(curl -sf "${base_url}/tasks/${task_id}" -H "$auth_header" 2>/dev/null) || true
            status=$(echo "$detail" | python3 -c "import sys,json; print(json.load(sys.stdin).get('task',{}).get('status',''))" 2>/dev/null) || true
            case "$status" in completed|finished|failed|stopped) break ;; esac
            sleep 1; elapsed=$((elapsed + 1))
        done

        if [ "$status" = "failed" ]; then
            pass "E2E: Failing command detected (status=failed)"
        else
            fail "E2E: Failing command (expected failed, got $status)"
        fi
    else
        fail "E2E: Failing task creation failed"
    fi

    # ── Test 5: Multiple concurrent tasks ──
    info "Running 3 concurrent tasks via customCommand..."
    local pids=() task_ids=()
    for i in 1 2 3; do
        task_resp=$(curl -sf -X POST "${base_url}/tasks" \
            -H "$auth_header" -H "Content-Type: application/json" \
            -d "{\"title\":\"concurrent-$i\",\"customCommand\":\"echo task-$i-output && sleep 1\"}" 2>/dev/null)
        tid=$(echo "$task_resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('taskId',''))" 2>/dev/null)
        if [ -n "$tid" ]; then
            task_ids+=("$tid")
        fi
    done

    if [ ${#task_ids[@]} -eq 3 ]; then
        # Wait for all to complete (max 30s)
        local all_done=false elapsed=0
        while [ $elapsed -lt 30 ]; do
            local done_count=0
            for tid in "${task_ids[@]}"; do
                local st
                st=$(curl -sf "${base_url}/tasks/${tid}" -H "$auth_header" 2>/dev/null | \
                    python3 -c "import sys,json; print(json.load(sys.stdin).get('task',{}).get('status',''))" 2>/dev/null) || true
                case "$st" in completed|finished|failed|stopped) done_count=$((done_count + 1)) ;; esac
            done
            if [ $done_count -eq 3 ]; then all_done=true; break; fi
            sleep 1; elapsed=$((elapsed + 1))
        done

        if $all_done; then
            pass "E2E: 3 concurrent tasks completed"
        else
            fail "E2E: Concurrent tasks did not all complete within 30s"
        fi
    else
        fail "E2E: Failed to create 3 concurrent tasks (got ${#task_ids[@]})"
    fi

    kill "$agent_pid" 2>/dev/null || true
}

# ── Docker Sandbox Tests ──────────────────────────────────────────
# Tests container sandbox: build image, run tasks in containers.
# Requires Docker daemon running.
run_docker_tests() {
    header "Docker — Container Sandbox Tests"

    # Check Docker availability
    if ! command -v docker &>/dev/null; then
        skip "Docker tests (docker not installed)"
        return
    fi
    if ! docker info > /dev/null 2>&1; then
        skip "Docker tests (Docker daemon not running — start Docker Desktop)"
        return
    fi

    local agent_bin="$TEST_DIR/yaver"
    [ -f "$agent_bin" ] || build_agent "$agent_bin" > /dev/null 2>&1 || { fail "Cannot build agent"; return; }

    # ── Test 1: Sandbox image build ──
    info "Building sandbox image (may take a few minutes first time)..."
    if docker build -f "$ROOT_DIR/desktop/agent/Dockerfile.sandbox" \
        -t yaver-sandbox "$ROOT_DIR/desktop/agent" > "$TEST_DIR/docker-build.log" 2>&1; then
        pass "Docker: Sandbox image built"
    else
        fail "Docker: Sandbox image build failed"
        tail -20 "$TEST_DIR/docker-build.log"
        return
    fi

    # ── Test 2: Container runs Python hello world ──
    info "Running Python hello world in container..."
    local container_output
    container_output=$(docker run --rm yaver-sandbox \
        "python3 -c \"print('hello from yaver container')\"" 2>&1) || true
    if echo "$container_output" | grep -q "hello from yaver container"; then
        pass "Docker: Python in container (direct)"
    else
        fail "Docker: Python in container (output: $container_output)"
    fi

    # ── Test 3: Agent with --containerize-host runs task in container ──
    local http_port quic_port
    http_port=$(get_free_port); quic_port=$(get_free_port)

    info "Creating test account..."
    local token
    token=$(create_test_account) || { fail "Cannot create test account"; return; }

    local device_id="test-docker-$(gen_uuid)"
    local work_dir="$TEST_DIR/docker-agent"

    info "Starting agent with --containerize-host (HTTP=$http_port)..."
    export YAVER_NO_DUMMY=1
    local agent_pid
    agent_pid=$(start_agent "$agent_bin" "$http_port" "$quic_port" "$token" "$device_id" "$work_dir" \
        --no-relay --containerize-host) || {
        unset YAVER_NO_DUMMY
        fail "Docker agent failed to start"; return
    }
    unset YAVER_NO_DUMMY

    local base_url="http://127.0.0.1:${http_port}"
    local auth_header="Authorization: Bearer ${token}"

    # Verify sandbox status endpoint
    info "Checking sandbox status..."
    local sandbox_status
    sandbox_status=$(curl -sf "${base_url}/sandbox/status" -H "$auth_header" 2>/dev/null) || true
    local avail imgready
    avail=$(echo "$sandbox_status" | python3 -c "import sys,json; print(json.load(sys.stdin).get('available', False))" 2>/dev/null) || true
    imgready=$(echo "$sandbox_status" | python3 -c "import sys,json; print(json.load(sys.stdin).get('imageReady', False))" 2>/dev/null) || true

    if [ "$avail" = "True" ] && [ "$imgready" = "True" ]; then
        pass "Docker: Sandbox status (available=true, imageReady=true)"
    else
        fail "Docker: Sandbox status (available=$avail, imageReady=$imgready)"
    fi

    # Run Python task through agent with containerization
    info "Running containerized Python task through agent..."
    local task_resp task_id
    task_resp=$(curl -sf -X POST "${base_url}/tasks" \
        -H "$auth_header" -H "Content-Type: application/json" \
        -d '{"title":"container python test","customCommand":"python3 -c \"print(\\\"hello from containerized task\\\")\""}' 2>/dev/null) || { fail "Docker: create container task"; }
    task_id=$(echo "$task_resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('taskId',''))" 2>/dev/null)

    if [ -n "$task_id" ]; then
        local elapsed=0 status="" output=""
        while [ $elapsed -lt 60 ]; do
            local detail
            detail=$(curl -sf "${base_url}/tasks/${task_id}" -H "$auth_header" 2>/dev/null) || true
            status=$(echo "$detail" | python3 -c "import sys,json; print(json.load(sys.stdin).get('task',{}).get('status',''))" 2>/dev/null) || true
            case "$status" in completed|finished|failed|stopped) break ;; esac
            sleep 2; elapsed=$((elapsed + 2))
        done

        output=$(curl -sf "${base_url}/tasks/${task_id}" -H "$auth_header" 2>/dev/null | \
            python3 -c "import sys,json; print(json.load(sys.stdin).get('task',{}).get('output',''))" 2>/dev/null) || true

        if ([ "$status" = "completed" ] || [ "$status" = "finished" ]) && echo "$output" | grep -q "hello from containerized task"; then
            pass "Docker: Containerized Python task (output verified)"
        else
            fail "Docker: Containerized Python task (status=$status)"
            info "Output was: $output"
            info "Agent log tail:"
            tail -20 "$work_dir/agent.log" 2>/dev/null || true
        fi
    else
        fail "Docker: Container task creation failed"
        info "Response was: $task_resp"
    fi

    # ── Test 4: Container with Node.js ──
    info "Running containerized Node.js task..."
    task_resp=$(curl -sf -X POST "${base_url}/tasks" \
        -H "$auth_header" -H "Content-Type: application/json" \
        -d '{"title":"container node test","customCommand":"node -e \"console.log(JSON.stringify({msg:\\\"hello from node\\\",v:process.version}))\""}' 2>/dev/null) || true
    task_id=$(echo "$task_resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('taskId',''))" 2>/dev/null)

    if [ -n "$task_id" ]; then
        local elapsed=0 status="" output=""
        while [ $elapsed -lt 60 ]; do
            local detail
            detail=$(curl -sf "${base_url}/tasks/${task_id}" -H "$auth_header" 2>/dev/null) || true
            status=$(echo "$detail" | python3 -c "import sys,json; print(json.load(sys.stdin).get('task',{}).get('status',''))" 2>/dev/null) || true
            case "$status" in completed|finished|failed|stopped) break ;; esac
            sleep 2; elapsed=$((elapsed + 2))
        done

        output=$(curl -sf "${base_url}/tasks/${task_id}" -H "$auth_header" 2>/dev/null | \
            python3 -c "import sys,json; print(json.load(sys.stdin).get('task',{}).get('output',''))" 2>/dev/null) || true

        if ([ "$status" = "completed" ] || [ "$status" = "finished" ]) && echo "$output" | grep -q "hello from node"; then
            pass "Docker: Containerized Node.js task (output verified)"
        else
            fail "Docker: Containerized Node.js task (status=$status)"
            info "Output was: $output"
        fi
    else
        fail "Docker: Node.js container task creation failed"
    fi

    # ── Test 5: Container filesystem isolation ──
    info "Verifying container filesystem isolation..."
    # Write a file on host, verify container can see it in /workspace but not /tmp/host-only
    local host_marker="$work_dir/host-marker-$(gen_uuid).txt"
    echo "host-only-content" > "$host_marker"

    task_resp=$(curl -sf -X POST "${base_url}/tasks" \
        -H "$auth_header" -H "Content-Type: application/json" \
        -d '{"title":"isolation test","customCommand":"ls /workspace/host-marker-*.txt 2>/dev/null && echo WORKSPACE_VISIBLE || echo WORKSPACE_HIDDEN; cat /etc/hostname 2>/dev/null && echo HOST_VISIBLE || echo CONTAINER_ISOLATED"}' 2>/dev/null) || true
    task_id=$(echo "$task_resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('taskId',''))" 2>/dev/null)

    if [ -n "$task_id" ]; then
        local elapsed=0 status="" output=""
        while [ $elapsed -lt 60 ]; do
            local detail
            detail=$(curl -sf "${base_url}/tasks/${task_id}" -H "$auth_header" 2>/dev/null) || true
            status=$(echo "$detail" | python3 -c "import sys,json; print(json.load(sys.stdin).get('task',{}).get('status',''))" 2>/dev/null) || true
            case "$status" in completed|finished|failed|stopped) break ;; esac
            sleep 2; elapsed=$((elapsed + 2))
        done

        output=$(curl -sf "${base_url}/tasks/${task_id}" -H "$auth_header" 2>/dev/null | \
            python3 -c "import sys,json; print(json.load(sys.stdin).get('task',{}).get('output',''))" 2>/dev/null) || true

        if ([ "$status" = "completed" ] || [ "$status" = "finished" ]) && echo "$output" | grep -q "WORKSPACE_VISIBLE"; then
            pass "Docker: Container sees /workspace (project dir mounted)"
        else
            fail "Docker: Container /workspace visibility (status=$status)"
            info "Output was: $output"
        fi
    else
        fail "Docker: Isolation test task creation failed"
    fi

    # Cleanup: remove sandbox containers if any are left
    docker ps -q --filter "name=yaver-task-" 2>/dev/null | xargs -r docker stop 2>/dev/null || true

    kill "$agent_pid" 2>/dev/null || true
}

# ── Ollama Integration Test — Yaver-to-Yaver via Relay ──────────────
# Starts a local relay, two agents (A = client, B = ollama runner),
# sends a coding task through the relay, verifies ollama output.
# Uses qwen2.5-coder:1.5b to keep RAM usage low (~1GB model).
run_ollama_tests() {
    header "Ollama — Yaver-to-Yaver via Relay"

    # ── Pre-flight: check ollama is available ──
    if ! command -v ollama &>/dev/null; then
        skip "Ollama not installed"; return
    fi
    if ! ollama list 2>/dev/null | grep -q "qwen2.5-coder:1.5b"; then
        info "Pulling qwen2.5-coder:1.5b (986 MB)..."
        ollama pull qwen2.5-coder:1.5b || { skip "Cannot pull qwen2.5-coder:1.5b"; return; }
    fi

    # ── Build binaries ──
    local agent_bin="$TEST_DIR/yaver"
    local relay_bin="$TEST_DIR/yaver-relay"
    [ -f "$agent_bin" ] || build_agent "$agent_bin" > /dev/null 2>&1 || { fail "Cannot build agent"; return; }
    [ -f "$relay_bin" ] || build_relay "$relay_bin" > /dev/null 2>&1 || { fail "Cannot build relay"; return; }

    # ── Start relay ──
    local relay_http_port relay_quic_port
    relay_http_port=$(get_free_port); relay_quic_port=$(get_free_port)
    local relay_password="ollama-test-relay-$$"

    info "Starting relay (HTTP=$relay_http_port, QUIC=$relay_quic_port)..."
    local relay_pid
    relay_pid=$(start_relay "$relay_bin" "$relay_quic_port" "$relay_http_port" "$relay_password" "$TEST_DIR/ollama-relay.log") || {
        fail "Relay failed to start"; return
    }
    pass "Relay started"

    # ── Start Agent B (ollama runner, connects to relay) ──
    local b_http_port b_quic_port
    b_http_port=$(get_free_port); b_quic_port=$(get_free_port)

    info "Creating test account..."
    local token
    token=$(create_test_account) || { fail "Cannot create test account"; kill "$relay_pid" 2>/dev/null; return; }

    local b_device_id="test-ollama-b-$(gen_uuid)"
    local b_work_dir="$TEST_DIR/ollama-agent-b"
    mkdir -p "$b_work_dir"

    info "Starting Agent B (HTTP=$b_http_port)..."
    export YAVER_NO_DUMMY=1
    local b_pid
    b_pid=$(start_agent "$agent_bin" "$b_http_port" "$b_quic_port" "$token" "$b_device_id" "$b_work_dir" --no-relay) || {
        unset YAVER_NO_DUMMY
        fail "Agent B failed to start"
        kill "$relay_pid" 2>/dev/null || true
        return
    }
    unset YAVER_NO_DUMMY

    pass "Agent B started"

    # ── Test 1: Ask ollama to write a printf function ──
    local base_url="http://127.0.0.1:${b_http_port}"
    local auth_header="Authorization: Bearer ${token}"

    info "Sending task: ask ollama to write a C printf wrapper function..."
    local task_resp task_id
    local ollama_prompt="Write a C function called my_printf that wraps printf and adds a newline at the end. Show only the code, no explanation."
    task_resp=$(curl -sf -X POST "${base_url}/tasks" \
        -H "$auth_header" -H "Content-Type: application/json" \
        -d "{\"title\":\"ollama printf test\",\"customCommand\":\"ollama run qwen2.5-coder:1.5b \\\"${ollama_prompt}\\\"\"}" 2>/dev/null) || { fail "Ollama: create task failed"; }
    task_id=$(echo "$task_resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('taskId',''))" 2>/dev/null)

    if [ -n "$task_id" ]; then
        pass "Ollama: Task created ($task_id)"

        # Poll for completion (max 120s — LLM inference can be slow)
        local elapsed=0 status="" output=""
        while [ $elapsed -lt 120 ]; do
            local detail
            detail=$(curl -sf "${base_url}/tasks/${task_id}" -H "$auth_header" 2>/dev/null) || true
            status=$(echo "$detail" | python3 -c "import sys,json; print(json.load(sys.stdin).get('task',{}).get('status',''))" 2>/dev/null) || true
            case "$status" in completed|finished|failed|stopped) break ;; esac
            sleep 3; elapsed=$((elapsed + 3))
        done

        output=$(curl -sf "${base_url}/tasks/${task_id}" -H "$auth_header" 2>/dev/null | \
            python3 -c "import sys,json; print(json.load(sys.stdin).get('task',{}).get('output',''))" 2>/dev/null) || true

        if [ "$status" = "completed" ] || [ "$status" = "finished" ]; then
            pass "Ollama: Task completed (${elapsed}s)"

            # Verify the output contains C function signature
            if echo "$output" | grep -qE "(void|int)\s+my_printf"; then
                pass "Ollama: Output contains my_printf function definition"
            elif echo "$output" | grep -q "my_printf"; then
                pass "Ollama: Output mentions my_printf (function form may vary)"
            else
                fail "Ollama: Output missing my_printf"
                info "Output was: $(echo "$output" | head -20)"
            fi

            # Verify it references printf (the function it wraps)
            if echo "$output" | grep -q "printf"; then
                pass "Ollama: Output references printf"
            else
                fail "Ollama: Output missing printf reference"
            fi
        else
            fail "Ollama: Task did not complete (status=$status after ${elapsed}s)"
            info "Output was: $(echo "$output" | head -20)"
            info "Agent B log tail:"
            tail -30 "$b_work_dir/agent.log" 2>/dev/null || true
        fi
    else
        fail "Ollama: Task creation returned no taskId"
        info "Response was: $task_resp"
    fi

    # ── Cleanup ──
    kill "$b_pid" "$relay_pid" 2>/dev/null || true
    info "Ollama test cleanup complete"
}

# ── Hybrid Local Test — yaver hybrid drives aider+ollama to build a calculator ──
# This exercises the END-TO-END planner+implementer loop using a canned
# stub planner (no API keys) and a real local Qwen implementer. The test
# passes iff the produced calc.py module passes behavioural checks.
# See scripts/test-hybrid-local.sh for the actual implementation — we
# just shell out to it and surface pass/fail into the suite counters.
run_hybrid_local_test() {
    header "Hybrid Local — Aider + Ollama + Qwen → Calculator"

    if ! command -v aider &>/dev/null; then
        info "Installing aider..."
        pip3 install --user --quiet aider-chat 2>/dev/null \
            || pipx install aider-chat 2>/dev/null \
            || { skip "Cannot install aider (no pip3/pipx)"; return; }
        export PATH="$HOME/.local/bin:$HOME/Library/Python/3.9/bin:$PATH"
    fi
    if ! command -v ollama &>/dev/null; then
        info "Installing ollama..."
        curl -fsSL https://ollama.com/install.sh | sh 2>/dev/null \
            || { skip "Cannot install ollama"; return; }
    fi

    # Pull the small model if not present. This is the slow step on a
    # cold CI runner; locally it's a no-op.
    if ! curl -sf http://localhost:11434/api/tags &>/dev/null; then
        info "Starting ollama daemon..."
        ollama serve > "$TEST_DIR/hybrid-ollama.log" 2>&1 &
        local ollama_pid=$!
        for _ in {1..20}; do
            if curl -sf http://localhost:11434/api/tags &>/dev/null; then break; fi
            sleep 1
        done
        trap "kill $ollama_pid 2>/dev/null || true" EXIT
    fi

    local MODEL="${HYBRID_MODEL:-qwen2.5-coder:1.5b}"
    if ! curl -sf http://localhost:11434/api/tags | grep -q "\"$MODEL\""; then
        info "Pulling $MODEL..."
        ollama pull "$MODEL" > "$TEST_DIR/hybrid-pull.log" 2>&1 \
            || { fail "Could not pull $MODEL"; return; }
    fi

    info "Running hybrid end-to-end test (model=$MODEL)..."
    if MODEL="$MODEL" bash "$SCRIPT_DIR/test-hybrid-local.sh" > "$TEST_DIR/hybrid-local.log" 2>&1; then
        pass "Hybrid mode produced a working calculator (planner stub + aider + $MODEL)"
    else
        fail "Hybrid local test failed — see $TEST_DIR/hybrid-local.log"
        tail -40 "$TEST_DIR/hybrid-local.log" || true
    fi
}

# ── Ollama CI Test — Install ollama on CI runner, run integration test ──
# Designed for GitHub Actions ubuntu-latest runners (7GB RAM, 14GB disk free).
# Installs ollama, pulls qwen2.5-coder:1.5b (~1GB), runs agent + task.
# Also works locally — same as run_ollama_tests but installs ollama if missing.
run_ollama_ci_test() {
    header "Ollama CI — Install + Run on CI Runner"

    # ── Install ollama if not present ──
    if ! command -v ollama &>/dev/null; then
        info "Installing ollama..."
        curl -fsSL https://ollama.com/install.sh | sh 2>/dev/null || { skip "Cannot install ollama"; return; }
    fi

    # Start ollama server if not running (CI runners need this)
    if ! curl -sf http://localhost:11434/api/tags &>/dev/null; then
        info "Starting ollama server..."
        ollama serve > "$TEST_DIR/ollama-server.log" 2>&1 &
        local ollama_pid=$!
        PIDS_TO_KILL+=("$ollama_pid")
        # Wait for server to be ready
        for i in $(seq 1 30); do
            curl -sf http://localhost:11434/api/tags &>/dev/null && break
            sleep 1
        done
        if ! curl -sf http://localhost:11434/api/tags &>/dev/null; then
            fail "Ollama server not ready after 30s"
            cat "$TEST_DIR/ollama-server.log" 2>/dev/null | tail -20
            return
        fi
        pass "Ollama server started"
    else
        pass "Ollama server already running"
    fi

    # ── Pull model ──
    if ! ollama list 2>/dev/null | grep -q "qwen2.5-coder:1.5b"; then
        info "Pulling qwen2.5-coder:1.5b (986 MB — this may take a few minutes in CI)..."
        ollama pull qwen2.5-coder:1.5b 2>/dev/null || { fail "Cannot pull qwen2.5-coder:1.5b"; return; }
    fi
    pass "Model qwen2.5-coder:1.5b available"

    # ── Build agent ──
    local agent_bin="$TEST_DIR/yaver"
    [ -f "$agent_bin" ] || build_agent "$agent_bin" > /dev/null 2>&1 || { fail "Cannot build agent"; return; }

    # ── Start agent (non-dummy, no relay) ──
    local http_port quic_port
    http_port=$(get_free_port); quic_port=$(get_free_port)

    info "Creating test account..."
    local token
    token=$(create_test_account) || { fail "Cannot create test account"; return; }

    local device_id="test-ollama-ci-$(gen_uuid)"
    local work_dir="$TEST_DIR/ollama-ci-agent"

    info "Starting agent (HTTP=$http_port)..."
    export YAVER_NO_DUMMY=1
    local agent_pid
    agent_pid=$(start_agent "$agent_bin" "$http_port" "$quic_port" "$token" "$device_id" "$work_dir" --no-relay) || {
        unset YAVER_NO_DUMMY
        fail "Agent failed to start"; return
    }
    unset YAVER_NO_DUMMY
    pass "Agent started"

    # ── Test 1: Ask ollama to write a printf function ──
    local base_url="http://127.0.0.1:${http_port}"
    local auth_header="Authorization: Bearer ${token}"

    info "Sending task: ask ollama to write a C printf wrapper function..."
    local ollama_prompt="Write a C function called my_printf that wraps printf and adds a newline at the end. Show only the code, no explanation."
    local task_resp task_id
    task_resp=$(curl -sf -X POST "${base_url}/tasks" \
        -H "$auth_header" -H "Content-Type: application/json" \
        -d "{\"title\":\"ollama ci printf test\",\"customCommand\":\"ollama run qwen2.5-coder:1.5b \\\"${ollama_prompt}\\\"\"}" 2>/dev/null) || { fail "Ollama CI: create task failed"; }
    task_id=$(echo "$task_resp" | python3 -c "import sys,json; print(json.load(sys.stdin).get('taskId',''))" 2>/dev/null)

    if [ -n "$task_id" ]; then
        pass "Ollama CI: Task created ($task_id)"

        # Poll for completion (max 180s — CPU inference is slow in CI)
        local elapsed=0 status="" output=""
        while [ $elapsed -lt 180 ]; do
            local detail
            detail=$(curl -sf "${base_url}/tasks/${task_id}" -H "$auth_header" 2>/dev/null) || true
            status=$(echo "$detail" | python3 -c "import sys,json; print(json.load(sys.stdin).get('task',{}).get('status',''))" 2>/dev/null) || true
            case "$status" in completed|finished|failed|stopped) break ;; esac
            sleep 3; elapsed=$((elapsed + 3))
        done

        output=$(curl -sf "${base_url}/tasks/${task_id}" -H "$auth_header" 2>/dev/null | \
            python3 -c "import sys,json; print(json.load(sys.stdin).get('task',{}).get('output',''))" 2>/dev/null) || true

        if [ "$status" = "completed" ] || [ "$status" = "finished" ]; then
            pass "Ollama CI: Task completed (${elapsed}s)"

            if echo "$output" | grep -qE "(void|int)\s+my_printf"; then
                pass "Ollama CI: Output contains my_printf function definition"
            elif echo "$output" | grep -q "my_printf"; then
                pass "Ollama CI: Output mentions my_printf (function form may vary)"
            else
                fail "Ollama CI: Output missing my_printf"
                info "Output was: $(echo "$output" | head -20)"
            fi

            if echo "$output" | grep -q "printf"; then
                pass "Ollama CI: Output references printf"
            else
                fail "Ollama CI: Output missing printf reference"
            fi
        else
            fail "Ollama CI: Task did not complete (status=$status after ${elapsed}s)"
            info "Output was: $(echo "$output" | head -20)"
            info "Agent log tail:"
            tail -30 "$work_dir/agent.log" 2>/dev/null || true
        fi
    else
        fail "Ollama CI: Task creation returned no taskId"
        info "Response was: $task_resp"
    fi

    # ── Cleanup ──
    kill "$agent_pid" 2>/dev/null || true
    info "Ollama CI test cleanup complete"
}

    local run_all=true
    local run_builds=false run_lan=false run_relay=false run_relay_docker=false
    local run_relay_binary=false run_tailscale=false run_cloudflare=false run_unit=false
    local run_mesh_e2e=false run_mesh_relay_e2e=false run_machine_e2e=false run_relay_tunnel_e2e=false
    local run_sdk=false
    local run_auth=false
    local run_feedback=false
    local run_expo=false
    local run_voice=false
    local run_e2e=false
    local run_docker=false
    local run_ollama=false
    local run_ollama_ci=false
    local run_oauth_mock=false
    local run_hybrid_local=false
    local run_features=false
    local run_features_auth=false
    local run_features_remote=false

    while [ "$#" -gt 0 ]; do
        case "$1" in
            --unit)            run_unit=true; run_all=false ;;
            --features)        run_features=true; run_all=false ;;
            --features-auth)   run_features_auth=true; run_all=false ;;
            --features-remote) run_features_remote=true; run_all=false ;;
            --builds)          run_builds=true; run_all=false ;;
            --lan)             run_lan=true; run_all=false ;;
            --relay)           run_relay=true; run_all=false ;;
            --relay-docker)    run_relay_docker=true; run_all=false ;;
            --relay-binary)    run_relay_binary=true; run_all=false ;;
            --tailscale)       run_tailscale=true; run_all=false ;;
            --mesh-e2e)        run_mesh_e2e=true; run_all=false ;;
            --mesh-relay-e2e)  run_mesh_relay_e2e=true; run_all=false ;;
            --machine-e2e)     run_machine_e2e=true; run_all=false ;;
            --relay-tunnel-e2e) run_relay_tunnel_e2e=true; run_all=false ;;
            --cloudflare)      run_cloudflare=true; run_all=false ;;
            --sdk)             run_sdk=true; run_all=false ;;
            --auth)            run_auth=true; run_all=false ;;
            --feedback)        run_feedback=true; run_all=false ;;
            --expo)            run_expo=true; run_all=false ;;
            --voice)           run_voice=true; run_all=false ;;
            --e2e)             run_e2e=true; run_all=false ;;
            --docker)          run_docker=true; run_all=false ;;
            --ollama)          run_ollama=true; run_all=false ;;
            --ollama-ci)       run_ollama_ci=true; run_all=false ;;
            --oauth-mock)      run_oauth_mock=true; run_all=false ;;
            --hybrid-local)    run_hybrid_local=true; run_all=false ;;
            --remote-host)
                if [ "$#" -lt 2 ]; then
                    echo "Missing value for --remote-host"
                    exit 1
                fi
                REMOTE_SERVER_IP="$2"
                shift
                ;;
            --remote-ssh-key)
                if [ "$#" -lt 2 ]; then
                    echo "Missing value for --remote-ssh-key"
                    exit 1
                fi
                REMOTE_SERVER_SSH_KEY="$2"
                shift
                ;;
            --remote-user)
                if [ "$#" -lt 2 ]; then
                    echo "Missing value for --remote-user"
                    exit 1
                fi
                REMOTE_SERVER_USER="$2"
                shift
                ;;
            --help|-h)
                cat << 'HELP'
Usage: ./scripts/test-suite.sh [FLAGS]

No flags = run all tests.

Flags:
  --unit            Go unit tests (agent + relay)
  --builds          Build verification (CLI, relay, web, mobile, iOS, Android)
  --lan             LAN direct connection test (localhost, no infra needed)
  --relay           Local relay server test (no remote infra)
  --relay-docker    Deploy relay to Hetzner via Docker, test, teardown
  --relay-binary    Deploy relay to Hetzner as native binary, test, teardown
  --tailscale       Tailscale cross-machine test (local ↔ Hetzner)
  --cloudflare      Cloudflare tunnel test
  --sdk             SDK tests (Go, Python, JS/TS, C shared library build)
  --auth            Auth lifecycle (signup, login, validate, profile, settings, logout, delete)
  --feedback        Feedback SDK integration tests (starts agent, tests HTTP API)
  --expo            Expo integration tests (project detection, CLI, setup)
  --voice           Voice AI tests (provider registry, HTTP endpoints, mock S2S)
  --e2e             End-to-end real command execution (python, shell — no AI runner needed)
  --docker          Docker container sandbox tests (requires Docker daemon)
  --ollama          Ollama integration test — local (requires ollama + qwen2.5-coder:1.5b)
  --ollama-ci       Ollama CI test — installs ollama + model, runs on any Linux runner
  --oauth-mock      Boot mock OAuth providers + local web app, then hit the real callback route
  --hybrid-local    Hybrid mode end-to-end: aider+ollama+qwen builds a calculator, assert it works
  --features        Feature-focused test pack: vault CRUD, blobs HTTP, schedules,
                    API keys, wipe, two-agent support connect, privacy tripwires.
                    No credentials needed — all over loopback.
  --remote-host IP  Override remote host instead of reading REMOTE_SERVER_IP.
  --remote-ssh-key PATH
                    Override SSH private key instead of reading REMOTE_SERVER_SSH_KEY.
  --remote-user USER
                    Override SSH user instead of reading REMOTE_SERVER_USER (default: root).
  --features-auth   Convex slow-path auth smoke via an in-process httptest
                    mock — proves ValidateTokenUser + token cache + owner/
                    non-owner/missing-header branches.
  --features-remote Cross-compile the feature test binary, scp it to
                    \$REMOTE_SERVER_IP:/tmp/yaver-feature-remote-\$TS/,
                    run it there, remove only that directory afterwards.
                    Needs REMOTE_SERVER_IP + SSH key.

Environment:
  Credentials loaded from: env vars > .env.test > ../talos/.env.test
  See .env.test.example for all available variables.

  --unit, --lan, --relay work without any credentials (use Convex dev backend).
  --relay-docker, --relay-binary, --tailscale need REMOTE_SERVER_IP + SSH key.
  --cloudflare needs CF_TUNNEL_URL + CF_ACCESS_CLIENT_ID + CF_ACCESS_CLIENT_SECRET.
HELP
                exit 0
                ;;
            *) echo "Unknown flag: $1 (use --help)"; exit 1 ;;
        esac
        shift
    done

    if $run_all || $run_unit; then
        local unit_failures_before="$FAIL_COUNT"
        run_unit_tests
        if $run_all && [ "$FAIL_COUNT" -gt "$unit_failures_before" ]; then
            info "Stopping full suite after unit test failure"
            run_all=false
        fi
    fi

    if $run_all || $run_features; then
        run_feature_tests
    fi

    if $run_all || $run_features_auth; then
        run_feature_auth_tests
    fi

    # Remote feature tests are NOT in --all (they need REMOTE_SERVER_IP
    # + SSH). Always opt-in via --features-remote.
    if $run_features_remote; then
        run_feature_remote_tests
    fi

    if $run_all || $run_builds; then
        run_build_tests
    fi

    if $run_all || $run_lan; then
        run_lan_test
    fi

    if $run_all || $run_relay; then
        run_relay_test
    fi

    if $run_all || $run_relay_docker; then
        run_relay_docker_test
    fi

    if $run_all || $run_relay_binary; then
        run_relay_binary_test
    fi

    if $run_mesh_e2e; then
        run_mesh_e2e_test
    fi
    if $run_mesh_relay_e2e; then
        run_mesh_relay_e2e_test
    fi
    if $run_machine_e2e; then
        run_machine_e2e_test
    fi
    if $run_relay_tunnel_e2e; then
        run_relay_tunnel_e2e_test
    fi
    if $run_all || $run_tailscale; then
        run_tailscale_test
    fi

    if $run_all || $run_cloudflare; then
        run_cloudflare_test
    fi

    if $run_all || $run_auth; then
        run_auth_tests
    fi

    if $run_all || $run_sdk; then
        run_sdk_tests
    fi

    if $run_all || $run_feedback; then
        run_feedback_tests
    fi

    if $run_all || $run_expo; then
        run_expo_tests
    fi

    if $run_all || $run_voice; then
        run_voice_tests
    fi

    if $run_all || $run_e2e; then
        run_e2e_tests
    fi

    if $run_all || $run_docker; then
        run_docker_tests
    fi

    if $run_ollama; then
        run_ollama_tests
    fi

    if $run_ollama_ci; then
        run_ollama_ci_test
    fi

    if $run_oauth_mock; then
        run_oauth_mock_tests
    fi

    if $run_hybrid_local; then
        run_hybrid_local_test
    fi

    # Summary
    echo ""
    echo -e "${BOLD}═══════════════════════════════════════════════════${NC}"
    echo -e "  ${GREEN}Passed: $PASS_COUNT${NC}  ${RED}Failed: $FAIL_COUNT${NC}  ${YELLOW}Skipped: $SKIP_COUNT${NC}"
    echo -e "${BOLD}═══════════════════════════════════════════════════${NC}"

    if [ $FAIL_COUNT -gt 0 ]; then
        echo -e "\n${RED}Failures:${NC}"
        for f in "${FAILURES[@]+"${FAILURES[@]}"}"; do
            echo -e "  ${RED}✗ $f${NC}"
        done
        echo ""
        exit 1
    fi

    echo -e "\n${GREEN}All tests passed!${NC}\n"
}

main "$@"
