#!/usr/bin/env bash
# Diagnose why `expo export:embed --platform ios` fails for carrotbet on
# yaver-test-ephemeral when sfmg succeeds on the same box.
#
# Hypothesis (from local-reproduce + structural compare): carrotbet is an
# npm-workspaces monorepo that depends on @backgammon/game-engine and
# @backgammon/game-session via version "*". Metro's nodeModulesPaths
# walks ./node_modules then ../node_modules, so those packages MUST be
# materialised at carrotbet/node_modules/@backgammon/ as symlinks into
# packages/. If `npm install` was only run inside mobile/ on the box,
# those symlinks never get created and Metro errors out. sfmg has no
# workspace deps, hence works.
#
# Run ON the box:
#   bash /root/Workspace/yaver.io/scripts/diagnose-carrotbet-bundle.sh

set -uo pipefail

CARROTBET="${CARROTBET:-/root/carrotbet}"
MOBILE="$CARROTBET/mobile"

note() { printf "%s %s\n" "$(date +%H:%M:%S)" "$*" >&2; }
ok()   { note "[ OK ]  $*"; }
bad()  { note "[FAIL]  $*"; }
warn() { note "[WARN]  $*"; }

note "═══ carrotbet bundle-build diagnostic ═══"
note "host: $(uname -srm)  free RAM: $(free -m 2>/dev/null | awk '/^Mem:/ {print $7" MiB available of "$2}')"

# ── 1. Project layout ──────────────────────────────────────────────
[[ -d "$CARROTBET" ]] || { bad "carrotbet checkout missing at $CARROTBET"; exit 1; }
[[ -d "$MOBILE"    ]] || { bad "mobile workspace missing at $MOBILE";    exit 1; }
[[ -f "$CARROTBET/package.json" ]] || { bad "no root package.json"; exit 1; }
ok "carrotbet root + mobile present"

# ── 2. Workspace symlinks ─────────────────────────────────────────
# This is the load-bearing check. If these are missing the bundle WILL
# fail with a Metro resolver error like "Unable to resolve module
# @backgammon/game-engine from /root/carrotbet/mobile/App.tsx".
ENGINE_LINK="$CARROTBET/node_modules/@backgammon/game-engine"
SESSION_LINK="$CARROTBET/node_modules/@backgammon/game-session"
if [[ -e "$ENGINE_LINK" && -e "$SESSION_LINK" ]]; then
  ok "@backgammon workspace symlinks present at root node_modules"
  ls -l "$ENGINE_LINK" "$SESSION_LINK" 2>&1 | sed 's/^/        /'
else
  bad "@backgammon symlinks MISSING — this is almost certainly the bundle failure"
  bad "  expected: $ENGINE_LINK  →  ../../packages/game-engine"
  bad "  expected: $SESSION_LINK →  ../../packages/game-session"
  warn "  fix:    cd $CARROTBET && npm install   # workspace-wide install creates the symlinks"
fi

# ── 3. Hermesc presence + architecture ────────────────────────────
# Not the export:embed failure (that step is JS-only) but matters for
# the Yaver agent's downstream Hermes compile step.
HERMES="$MOBILE/node_modules/react-native/sdks/hermesc/linux64-bin/hermesc"
if [[ -x "$HERMES" ]]; then
  HERMES_ARCH=$(file "$HERMES" | grep -oE 'aarch64|arm64|x86-64|x86_64' | head -1)
  HOST_ARCH=$(uname -m)
  if [[ "$HERMES_ARCH" == "x86-64" || "$HERMES_ARCH" == "x86_64" ]] && [[ "$HOST_ARCH" == "aarch64" || "$HOST_ARCH" == "arm64" ]]; then
    if command -v qemu-x86_64-static >/dev/null 2>&1 || [[ -f /proc/sys/fs/binfmt_misc/qemu-x86_64 ]]; then
      ok "hermesc is $HERMES_ARCH but qemu-user emulation is registered — would still run"
    else
      warn "hermesc is $HERMES_ARCH on $HOST_ARCH host AND no qemu-user registered → Hermes compile step would fail. Install qemu-user-static + binfmt-support."
    fi
  else
    ok "hermesc arch matches host ($HERMES_ARCH on $HOST_ARCH)"
  fi
else
  warn "hermesc not found at $HERMES — react-native might not be installed in mobile/"
fi

# ── 4. Reproduce the failing command, capture full stderr ─────────
note "running the exact command that fails through Yaver, capturing all output…"
cd "$MOBILE" || exit 1
rm -rf .yaver-build
mkdir -p .yaver-build
LOG=$(mktemp /tmp/carrotbet-bundle-XXXXXX.log)
note "log → $LOG"
# 90s timeout — if it OOMs or hangs, we want the partial output, not a wall-clock wait.
timeout 90 npx expo export:embed \
  --platform ios \
  --bundle-output .yaver-build/main.jsbundle \
  --assets-dest .yaver-build/assets \
  --dev false --minify true --reset-cache 2>&1 | tee "$LOG"
RC=${PIPESTATUS[0]}
note "── exit code $RC ──"
if [[ $RC -eq 0 ]]; then
  ok "bundle succeeded — re-run the Yaver reload now, the issue may have been a transient symlink/install state"
elif [[ $RC -eq 124 ]]; then
  bad "bundle TIMED OUT at 90s. Most likely OOM-kill or a hung Metro worker. tail of log:"
  tail -30 "$LOG" | sed 's/^/        /'
else
  bad "bundle failed (exit $RC). last 40 lines of stderr:"
  tail -40 "$LOG" | sed 's/^/        /'
fi

note "═══ done ═══"
exit $RC
