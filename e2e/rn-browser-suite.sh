#!/usr/bin/env bash
# rn-browser-suite.sh — closed-loop tests for the RN browser lane.
#
# Answers two questions no inventory check can:
#   1. Does an RN/Expo project actually PAINT in the browser lane?
#   2. Does a code change actually REACH the screen (vibing)?
#
# Both drive a real Chromium. That is the point: the 2026-07-24 blank-screen
# incident was green on every status check — dev server running, HTTP 200,
# bundle built — while the phone showed nothing, because the readiness probe
# accepted an Expo shell whose #root was still empty. Only loading the page and
# asking "did anything paint?" can catch that class.
#
# Usage:
#   ./rn-browser-suite.sh                 # render checks for all known projects
#   ./rn-browser-suite.sh --vibe          # also run the edit->screen vibe loop
#   ./rn-browser-suite.sh --only sfmg     # one project
#
# Projects are <name>:<path>:<themeFile>:<colorKey>. Paths are resolved at
# runtime and skipped when absent — this must run on any machine, not just the
# one it was written on.
set -uo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"
REPO="$(cd .. && pwd)"
SCRATCH="${SCRATCH:-${TMPDIR:-/tmp}/yaver-rn-browser-suite}"
mkdir -p "$SCRATCH"
export REPO SCRATCH

WORKSPACE="${YAVER_WORKSPACE:-$HOME/Workspace}"
PROJECTS=(
  "sfmg:$WORKSPACE/sfmg:src/theme/colors.ts:background"
  "talos:$WORKSPACE/talos/mobile::"
  "yaver:$REPO/mobile::"
)

RUN_VIBE=0; ONLY=""
while [ $# -gt 0 ]; do
  case "$1" in
    --vibe) RUN_VIBE=1 ;;
    --only) ONLY="$2"; shift ;;
    *) echo "unknown arg: $1"; exit 2 ;;
  esac
  shift
done

pass=0; fail=0; skip=0
results=()

for entry in "${PROJECTS[@]}"; do
  IFS=":" read -r name dir theme colorkey <<< "$entry"
  [ -n "$ONLY" ] && [ "$ONLY" != "$name" ] && continue

  if [ ! -d "$dir" ]; then
    echo "SKIP $name — not found at $dir"
    results+=("SKIP  $name  (no such directory)"); skip=$((skip+1)); continue
  fi

  out="$SCRATCH/$name-web"
  if [ ! -f "$out/index.html" ]; then
    echo ">> exporting $name web target (first run; cached afterwards)"
    if ! (cd "$dir" && npx expo export -p web --output-dir "$out" >"$SCRATCH/$name-export.log" 2>&1); then
      echo "FAIL $name — web export failed, see $SCRATCH/$name-export.log"
      results+=("FAIL  $name  web export failed"); fail=$((fail+1)); continue
    fi
  fi

  echo ">> render loop: $name"
  if node rn-browser-loop.mjs "$out" "$name"; then
    results+=("PASS  $name  renders in browser lane"); pass=$((pass+1))
  else
    results+=("FAIL  $name  render loop"); fail=$((fail+1))
  fi

  if [ "$RUN_VIBE" = "1" ] && [ -n "$theme" ]; then
    echo ">> vibe loop: $name ($theme:$colorkey)"
    if node rn-vibe-loop.mjs "$dir" "$theme" "$colorkey" "#4B0082" "$name"; then
      results+=("PASS  $name  vibe reached the screen"); pass=$((pass+1))
    else
      results+=("FAIL  $name  vibe did not reach the screen"); fail=$((fail+1))
    fi
  elif [ "$RUN_VIBE" = "1" ]; then
    results+=("SKIP  $name  vibe (no theme file mapped)"); skip=$((skip+1))
  fi
done

echo ""
echo "──────── RN browser lane ────────"
for r in "${results[@]}"; do echo "  $r"; done
echo "  ${pass} passed, ${fail} failed, ${skip} skipped"
[ "$fail" -eq 0 ]
