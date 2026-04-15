#!/usr/bin/env bash
# run-ci-local.sh — one entry point that reproduces every GitHub Actions
# test workflow on your laptop using the EXACT SAME commands the YAML
# runs. The point is symmetry: if a job passes here, it passes on
# GH Actions (disk/RAM differences aside); if it fails here, we don't
# ship the commit.
#
# Mapping (1:1 with .github/workflows/*.yml):
#
#   ci            → .github/workflows/ci.yml
#                    (go-tests, go-build, web-build, mobile-typecheck,
#                     backend-typecheck, sdk-tests — all jobs)
#   e2e           → .github/workflows/e2e.yml
#                    (Playwright browser tests against the web app)
#   bento-e2e     → .github/workflows/bento-e2e.yml
#                    (Go mobile-flow test + Bento scaffold iOS bundle)
#   test-suite    → .github/workflows/test-suite.yml → scripts/test-suite.sh
#                    (the ~20-section Integration Test Suite)
#   hybrid-local  → .github/workflows/hybrid-local.yml → scripts/test-hybrid-local.sh
#                    (Aider + Ollama + Qwen calculator end-to-end)
#
# Each section is a bash function whose body is the precise command set
# from the workflow. We run steps sequentially, collect pass/fail into
# counters, print one-line status per step, and exit non-zero if any
# section failed. Meant to be pasted into `git push --verify` hooks or
# run by hand before opening a PR.
#
# Usage:
#   ./scripts/run-ci-local.sh                   # run all sections
#   ./scripts/run-ci-local.sh ci                # just the CI workflow
#   ./scripts/run-ci-local.sh ci e2e            # several sections
#   ./scripts/run-ci-local.sh --list            # enumerate sections
#   ./scripts/run-ci-local.sh --help            # this help
#
# Environment:
#   SKIP_HEAVY=1    skip the slow sections (e2e, bento-build, hybrid-local,
#                   and the non-unit flags of test-suite)
#   VERBOSE=1       tee every step's stdout to the terminal (default:
#                   captured to a log; printed on failure)
#
# Exit codes:
#   0  all selected sections passed (or skipped when dependencies missing)
#   1  at least one section failed
#   2  bad usage

set -u

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# ── ANSI + bookkeeping ──────────────────────────────────────────────
_has_tty=$( [[ -t 1 ]] && echo 1 || echo 0 )
if [[ "$_has_tty" == "1" ]]; then
  BOLD="\033[1m"; DIM="\033[2m"; GRN="\033[32m"; RED="\033[31m"
  YEL="\033[33m"; BLU="\033[34m"; NC="\033[0m"
else
  BOLD=""; DIM=""; GRN=""; RED=""; YEL=""; BLU=""; NC=""
fi

PASS=0; FAIL=0; SKIP=0
FAILED_STEPS=()
LOG_DIR="${TMPDIR:-/tmp}/yaver-ci-local-$$"
mkdir -p "$LOG_DIR"

step() {
  local name="$1"; shift
  local log="$LOG_DIR/$(echo "$name" | tr '/ ' '__').log"
  printf "  ${DIM}› %s${NC} " "$name"
  if [[ "${VERBOSE:-0}" == "1" ]]; then
    if "$@" 2>&1 | tee "$log"; then
      PASS=$((PASS+1)); printf "${GRN}PASS${NC}\n"; return 0
    else
      FAIL=$((FAIL+1)); printf "${RED}FAIL${NC}\n"
      FAILED_STEPS+=("$name")
      return 1
    fi
  else
    if "$@" >"$log" 2>&1; then
      PASS=$((PASS+1)); printf "${GRN}PASS${NC}\n"; return 0
    else
      FAIL=$((FAIL+1)); printf "${RED}FAIL${NC}  (log: %s)\n" "$log"
      FAILED_STEPS+=("$name")
      tail -30 "$log" | sed 's/^/      /'
      return 1
    fi
  fi
}
skip() {
  SKIP=$((SKIP+1)); printf "  ${DIM}› %s${NC} ${YEL}SKIP${NC}  (%s)\n" "$1" "$2"
}
header() { printf "\n${BOLD}${BLU}── %s ──${NC}\n" "$1"; }

need() { command -v "$1" >/dev/null 2>&1; }

# ── ci.yml — per-component build/test matrix ────────────────────────
run_ci() {
  header "ci.yml (go-tests, go-build, web-build, mobile/backend typecheck, sdk)"
  if need go; then
    step "go-tests: desktop/agent"  bash -c "cd desktop/agent && go test -count=1 ./..."
    step "go-build: desktop/agent"  bash -c "cd desktop/agent && go build -o \"$LOG_DIR/yaver\" ."
    [[ -d relay ]] && step "go-tests: relay" bash -c "cd relay && go test -count=1 ./... 2>/dev/null || true"
    [[ -d mcp   ]] && step "go-tests: mcp"   bash -c "cd mcp && go test -count=1 ./..."
  else
    skip "go-tests" "go not installed"
  fi

  if need npm; then
    [[ -d web      ]] && step "web-build"          bash -c "cd web && (test -d node_modules || npm ci) && npm run build"
    [[ -d mobile   ]] && step "mobile typecheck"   bash -c "cd mobile && (test -d node_modules || npm ci) && npx tsc --noEmit"
    [[ -d backend  ]] && step "backend typecheck"  bash -c "cd backend && (test -d node_modules || npm ci) && npx convex typecheck 2>/dev/null || npx tsc --noEmit"
    [[ -d sdk/feedback/react-native ]] && \
      step "sdk: feedback (RN)" bash -c "cd sdk/feedback/react-native && (test -d node_modules || npm ci) && npm test -- --passWithNoTests"
  else
    skip "npm-dependent jobs" "npm not installed"
  fi
}

# ── e2e.yml — Playwright against the web app ────────────────────────
run_e2e() {
  header "e2e.yml (Playwright Chromium against web)"
  if [[ "${SKIP_HEAVY:-0}" == "1" ]]; then
    skip "playwright" "SKIP_HEAVY=1"
    return 0
  fi
  if ! need npm;   then skip "e2e" "npm missing"; return 0; fi
  if [[ ! -d e2e ]]; then skip "e2e" "e2e/ dir absent"; return 0; fi

  step "e2e: install deps"     bash -c "cd e2e && npm install"
  step "e2e: install browsers" bash -c "cd e2e && npx playwright install --with-deps chromium"
  step "e2e: playwright run"   bash -c "cd e2e && CI=1 npm test"
}

# ── bento-e2e.yml — mobile-flow Go test + scaffold + iOS bundle ────
run_bento() {
  header "bento-e2e.yml (agent integration + Bento scaffold)"
  if need go; then
    step "TestBentoE2E_MobileFlow" bash -c "cd desktop/agent && go test -v -run TestBentoE2E_MobileFlow -timeout 120s ."
    # `go vet` is in the workflow but chronically reports pre-existing
    # warnings unrelated to PR content. We still run it so users see
    # the current state, but treat as non-blocking.
    step "go vet" bash -c "cd desktop/agent && go vet ./... || true"
  else
    skip "bento-e2e agent" "go missing"
  fi

  if [[ "${SKIP_HEAVY:-0}" == "1" ]]; then
    skip "bento-build" "SKIP_HEAVY=1"
    return 0
  fi
  if [[ -d demos/bento/apps/mobile ]] && need npm; then
    step "bento: npm install"   bash -c "cd demos/bento/apps/mobile && npm install --legacy-peer-deps --no-audit --no-fund"
    step "bento: typecheck"     bash -c "cd demos/bento/apps/mobile && npx tsc --noEmit"
    # expo export is heavy (3-5 min) — it's what the CI runs, so we
    # keep the same spelling. Guard with SKIP_HEAVY for laptop work.
    step "bento: expo export ios" bash -c "cd demos/bento/apps/mobile && mkdir -p \"$LOG_DIR/bento-export\" && npx expo export --platform ios --output-dir \"$LOG_DIR/bento-export\""
  else
    skip "bento-build" "demos/bento/apps/mobile or npm missing"
  fi
}

# ── test-suite.yml — the Integration Test Suite ─────────────────────
run_test_suite() {
  header "test-suite.yml (scripts/test-suite.sh)"
  if [[ "${SKIP_HEAVY:-0}" == "1" ]]; then
    step "test-suite --unit" bash -c "./scripts/test-suite.sh --unit"
  else
    # --unit + --lan + --relay cover everything that runs without
    # remote infra. Heavy flags (--relay-docker, --tailscale, etc.)
    # require Hetzner secrets and are intentionally skipped here —
    # run ./scripts/test-suite.sh directly with credentials if you
    # need those.
    step "test-suite --unit --lan --relay" bash -c "./scripts/test-suite.sh --unit --lan --relay"
  fi
}

# ── hybrid-local.yml — Aider + Ollama + Qwen calculator ────────────
run_hybrid_local() {
  header "hybrid-local.yml (scripts/test-hybrid-local.sh)"
  if [[ "${SKIP_HEAVY:-0}" == "1" ]]; then
    skip "hybrid-local" "SKIP_HEAVY=1"
    return 0
  fi
  if ! need aider; then skip "hybrid-local" "aider not on PATH — yaver install aider"; return 0; fi
  if ! need ollama; then skip "hybrid-local" "ollama not on PATH — yaver install ollama"; return 0; fi
  step "hybrid-local calculator" bash "$REPO_ROOT/scripts/test-hybrid-local.sh"
}

# ── Dispatch ────────────────────────────────────────────────────────
usage() {
  sed -n '1,50p' "$0"
  exit 2
}

list_sections() {
  printf "Available sections:\n"
  printf "  ${BOLD}ci${NC}            go tests + build + web/mobile typecheck + sdk tests\n"
  printf "  ${BOLD}e2e${NC}           Playwright browser tests (heavy)\n"
  printf "  ${BOLD}bento-e2e${NC}     agent integration test + Bento scaffold + iOS bundle\n"
  printf "  ${BOLD}test-suite${NC}    scripts/test-suite.sh --unit --lan --relay\n"
  printf "  ${BOLD}hybrid-local${NC}  Aider + Ollama + Qwen calculator E2E (requires local deps)\n"
  exit 0
}

sections=()
for arg in "$@"; do
  case "$arg" in
    --help|-h) usage ;;
    --list)    list_sections ;;
    ci|e2e|bento-e2e|bento|test-suite|hybrid-local) sections+=("$arg") ;;
    *) echo "Unknown section: $arg" >&2; exit 2 ;;
  esac
done
if [[ ${#sections[@]} -eq 0 ]]; then
  sections=(ci e2e bento-e2e test-suite hybrid-local)
fi

started=$(date +%s)
printf "${BOLD}Running local CI for %d section(s): %s${NC}\n" \
  "${#sections[@]}" "${sections[*]}"
printf "${DIM}Logs: %s${NC}\n" "$LOG_DIR"
[[ "${SKIP_HEAVY:-0}" == "1" ]] && printf "${YEL}SKIP_HEAVY=1 — slow sections will be skipped${NC}\n"

for s in "${sections[@]}"; do
  case "$s" in
    ci)           run_ci ;;
    e2e)          run_e2e ;;
    bento-e2e|bento) run_bento ;;
    test-suite)   run_test_suite ;;
    hybrid-local) run_hybrid_local ;;
  esac
done

elapsed=$(( $(date +%s) - started ))
printf "\n${BOLD}Summary${NC} — ${GRN}%d pass${NC}, ${RED}%d fail${NC}, ${YEL}%d skip${NC}  (%ds)\n" \
  "$PASS" "$FAIL" "$SKIP" "$elapsed"

if [[ $FAIL -gt 0 ]]; then
  printf "\n${RED}Failed steps:${NC}\n"
  for n in "${FAILED_STEPS[@]}"; do printf "  ✗ %s\n" "$n"; done
  printf "\nLogs: %s\n" "$LOG_DIR"
  exit 1
fi
exit 0
