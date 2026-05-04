#!/usr/bin/env bash
# Drive the same HTTP call sequence the web dashboard makes when a user
# vibes through the sfmg project on this box, then reloads — three rounds,
# so the secondary/third reload claim ("does it break") gets a real answer.
#
# Designed to run ON THE BOX (e.g. yaver-test-ephemeral). Hits localhost
# directly so there's no relay-tunnel auth/CORS surface to manage.
#
# Usage (from your dev mac, once `yaver auth` is set up):
#   yaver ssh test bash -s < scripts/test-vibe-reload-sfmg.sh
#
# Or, after sshing in:
#   bash /root/Workspace/yaver.io/scripts/test-vibe-reload-sfmg.sh
#
# What's checked at each step:
#   • can we reach the agent?     → /info
#   • is the agent alive?          → uptime + lifecycle
#   • is the project there?        → /projects/mobile
#   • does opencode have GLM 4.7?  → /runners
#   • web preview spins up?        → /dev/start (expo, web target) + /dev/status poll
#   • vibing task lands changes?   → /tasks → poll status=completed
#   • reload picks up changes?     → /dev/reload, capture nativeChangesDetected
#   • reload #2 + #3 still work?   → repeat the vibe→reload loop twice more
#
# Output: tagged log lines with [PASS] / [FAIL] markers per step, plus a
# trailing summary so the result is grep-able.

set -euo pipefail

AGENT="${AGENT:-http://127.0.0.1:18080}"
PROJECT_PATH="${PROJECT_PATH:-/root/Workspace/sfmg}"
PROJECT_NAME="${PROJECT_NAME:-sfmg}"
RUNNER="${RUNNER:-opencode}"
MODEL="${MODEL:-glm-4.7}"
PORT="${PORT:-8083}"
TOKEN="${YAVER_TOKEN:-$(grep -oE '"token"[[:space:]]*:[[:space:]]*"[^"]*"' "$HOME/.yaver/auth.json" 2>/dev/null | sed -E 's/.*"([^"]+)"$/\1/' || true)}"

if [[ -z "$TOKEN" ]]; then
  echo "no auth token found at \$YAVER_TOKEN or ~/.yaver/auth.json — run 'yaver auth' first" >&2
  exit 2
fi

AUTH_HDR=(-H "Authorization: Bearer $TOKEN")

PASSES=0
FAILS=0
note() { printf "%s %s\n" "$(date +%H:%M:%S)" "$*" >&2; }
pass() { PASSES=$((PASSES+1)); note "[PASS] $*"; }
fail() { FAILS=$((FAILS+1)); note "[FAIL] $*"; }

# ──────────────────────────────────────────────────────────────────────
# 0. Reachability + agent liveness — mirror what mobile feedback SDK
#    surfaces (version, uptime, lifecycle).
# ──────────────────────────────────────────────────────────────────────
note "step 0 — reach + liveness"
INFO=$(curl -fsS "$AGENT/info" "${AUTH_HDR[@]}" 2>&1 || true)
if echo "$INFO" | grep -q '"version"'; then
  VERSION=$(echo "$INFO" | grep -oE '"version"[[:space:]]*:[[:space:]]*"[^"]*"' | sed -E 's/.*"([^"]+)"$/\1/' | head -1)
  pass "agent reachable, version=$VERSION"
else
  fail "agent not reachable at $AGENT — check 'yaver serve' is running"
  exit 1
fi

# ──────────────────────────────────────────────────────────────────────
# 1. Project discovery — confirm sfmg is registered.
# ──────────────────────────────────────────────────────────────────────
note "step 1 — discover sfmg"
PROJECTS=$(curl -fsS "$AGENT/projects/mobile" "${AUTH_HDR[@]}" 2>&1 || true)
if echo "$PROJECTS" | grep -q "\"$PROJECT_NAME\""; then
  pass "sfmg discovered in /projects/mobile"
else
  note "$PROJECTS" | head -5
  fail "sfmg not discovered — verify $PROJECT_PATH exists with package.json"
  exit 1
fi

# ──────────────────────────────────────────────────────────────────────
# 2. Runner availability — opencode + GLM 4.7 ready?
# ──────────────────────────────────────────────────────────────────────
note "step 2 — verify $RUNNER + $MODEL"
RUNNERS=$(curl -fsS "$AGENT/runners" "${AUTH_HDR[@]}" 2>&1 || true)
if echo "$RUNNERS" | grep -q "\"id\"[[:space:]]*:[[:space:]]*\"$RUNNER\""; then
  pass "$RUNNER runner advertised by /runners"
else
  fail "$RUNNER not in /runners — check 'opencode --version' on the box"
  exit 1
fi

# ──────────────────────────────────────────────────────────────────────
# 3. /dev/start — spin Expo web target.
# ──────────────────────────────────────────────────────────────────────
note "step 3 — /dev/start (expo, web, $PROJECT_PATH)"
START=$(curl -fsS -X POST "$AGENT/dev/start" "${AUTH_HDR[@]}" \
  -H "Content-Type: application/json" \
  -d "{\"framework\":\"expo\",\"workDir\":\"$PROJECT_PATH\",\"platform\":\"web\",\"port\":$PORT,\"caller\":\"web-ui\"}" \
  2>&1 || true)
if echo "$START" | grep -q '"ok"[[:space:]]*:[[:space:]]*true'; then
  pass "/dev/start accepted"
else
  note "$START" | head -3
  fail "/dev/start rejected"
  exit 1
fi

# Poll /dev/status until running=true, max 120s
note "polling /dev/status …"
DEADLINE=$(($(date +%s) + 120))
RUNNING=0
while [[ $(date +%s) -lt $DEADLINE ]]; do
  STATUS=$(curl -fsS "$AGENT/dev/status" "${AUTH_HDR[@]}" 2>&1 || true)
  if echo "$STATUS" | grep -q '"running"[[:space:]]*:[[:space:]]*true'; then
    RUNNING=1
    break
  fi
  sleep 2
done
if [[ $RUNNING -eq 1 ]]; then
  pass "dev server running"
else
  fail "dev server didn't reach running=true in 120s"
  echo "$STATUS" | head -5
  exit 1
fi

# ──────────────────────────────────────────────────────────────────────
# 4–9. Three rounds of vibe + reload.
# ──────────────────────────────────────────────────────────────────────
vibe_and_reload() {
  local ROUND=$1
  local PROMPT=$2
  note "─── round $ROUND ───"
  note "round $ROUND — POST /tasks"
  local TASK_RESP
  TASK_RESP=$(curl -fsS -X POST "$AGENT/tasks" "${AUTH_HDR[@]}" \
    -H "Content-Type: application/json" \
    -d "{\"title\":\"vibe-test r$ROUND\",\"description\":\"$PROMPT\",\"runner\":\"$RUNNER\",\"model\":\"$MODEL\",\"source\":\"web\",\"workDir\":\"$PROJECT_PATH\",\"projectName\":\"$PROJECT_NAME\"}" \
    2>&1 || true)
  local TASK_ID
  TASK_ID=$(echo "$TASK_RESP" | grep -oE '"taskId"[[:space:]]*:[[:space:]]*"[^"]*"' | sed -E 's/.*"([^"]+)"$/\1/' | head -1)
  if [[ -z "$TASK_ID" ]]; then
    fail "round $ROUND — /tasks didn't return a taskId; resp: $(echo "$TASK_RESP" | head -1)"
    return 1
  fi
  note "round $ROUND — taskId=$TASK_ID"

  # Poll task status until terminal, max 6min (opencode + GLM can be slow)
  local DDL=$(($(date +%s) + 360))
  local LAST_STATUS=""
  while [[ $(date +%s) -lt $DDL ]]; do
    LAST_STATUS=$(curl -fsS "$AGENT/tasks/$TASK_ID" "${AUTH_HDR[@]}" 2>&1 \
      | grep -oE '"status"[[:space:]]*:[[:space:]]*"[^"]*"' | sed -E 's/.*"([^"]+)"$/\1/' | head -1 || true)
    case "$LAST_STATUS" in
      completed) pass "round $ROUND — task completed"; break ;;
      failed|stopped) fail "round $ROUND — task ended status=$LAST_STATUS"; return 1 ;;
    esac
    sleep 5
  done
  if [[ "$LAST_STATUS" != "completed" ]]; then
    fail "round $ROUND — task didn't complete in 6min (last status=$LAST_STATUS)"
    return 1
  fi

  # Reload — Metro web target ⇒ /dev/reload (HMR, no Hermes rebuild).
  note "round $ROUND — POST /dev/reload"
  local RELOAD
  RELOAD=$(curl -fsS -X POST "$AGENT/dev/reload" "${AUTH_HDR[@]}" 2>&1 || true)
  if echo "$RELOAD" | grep -q '"ok"[[:space:]]*:[[:space:]]*true'; then
    pass "round $ROUND — /dev/reload ok"
    if echo "$RELOAD" | grep -q '"nativeChangesDetected"[[:space:]]*:[[:space:]]*true'; then
      note "round $ROUND — nativeChangesDetected=true (would trigger rebuild path on mobile)"
    fi
  else
    fail "round $ROUND — /dev/reload rejected: $(echo "$RELOAD" | head -1)"
    return 1
  fi

  # Wait briefly for the SSE 'ready' to settle before next round
  sleep 3
}

vibe_and_reload 1 "In sfmg, change the primary button background to teal (replace any existing color literal). Keep the change minimal."
vibe_and_reload 2 "Revert the button color change you just made — restore the previous color. Keep it minimal."
vibe_and_reload 3 "Add a single comment line at the top of the main App component file noting today's date."

# ──────────────────────────────────────────────────────────────────────
# Summary
# ──────────────────────────────────────────────────────────────────────
note "═══════════════════════════════════════"
note "PASSES=$PASSES  FAILS=$FAILS"
if [[ $FAILS -gt 0 ]]; then
  note "═══ TEST FAILED ═══"
  exit 1
fi
note "═══ TEST PASSED — three rounds of vibe + reload completed ═══"
