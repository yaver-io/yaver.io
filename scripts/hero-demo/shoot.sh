#!/usr/bin/env bash
# Live-shoot orchestrator for the hero demo video.
#
# What this does:
#   1. Runs the pre-shoot checklist and aborts if any item fails.
#   2. Starts a macOS screen recording of the FULL DESKTOP via
#      ffmpeg + avfoundation. Includes whatever AirPlay phone mirror
#      you have on screen.
#   3. Walks you through the 20-second shot, one beat at a time,
#      printing the current prompt + what you should type or click.
#      Press Enter on the Mac to advance to the next beat.
#   4. Stops recording. Encodes the raw .mov to a web-ready H.264
#      MP4 under 2 MB.
#   5. Offers to copy the result into web/public/demo-push.mp4 so
#      the landing hero uses the real footage instead of the
#      synthetic Playwright version.
#
# Requirements:
#   - ffmpeg (brew install ffmpeg)
#   - macOS with Screen Recording permission granted to your
#     terminal (System Settings → Privacy & Security → Screen
#     Recording → enable iTerm/Terminal).
#   - Phone with Yaver app open, AirPlay mirroring to THIS Mac.
#   - `yaver` CLI 1.96.10+ on PATH.
#   - An RN project to push (DEMO_PROJECT env var or
#     demos/bento/).
#
# Usage:
#   ./scripts/hero-demo/shoot.sh              # interactive
#   DEMO_PROJECT=/abs/path ./shoot.sh         # different project
#   SKIP_CHECKLIST=1 ./shoot.sh               # skip preflight
#
# Output:
#   scripts/hero-demo/output/raw-<timestamp>.mov — raw recording
#   scripts/hero-demo/output/hero.mp4           — encoded, web-ready

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT_DIR="$SCRIPT_DIR/output"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
DEMO_PROJECT="${DEMO_PROJECT:-$REPO_ROOT/demos/bento}"

TS="$(date +%Y%m%d-%H%M%S)"
RAW="$OUT_DIR/raw-$TS.mov"
ENCODED="$OUT_DIR/hero.mp4"

mkdir -p "$OUT_DIR"

say() { printf "\n\033[1;36m▶ %s\033[0m\n" "$*"; }
warn() { printf "\n\033[1;33m⚠ %s\033[0m\n" "$*"; }
fail() { printf "\n\033[1;31m✘ %s\033[0m\n" "$*"; exit 1; }
wait_enter() { printf "\n   press [Enter] to continue..."; read -r _; }

# ─── 1. preflight ────────────────────────────────────────────────
if [[ -z "${SKIP_CHECKLIST:-}" ]]; then
  say "Pre-shoot checklist"

  command -v ffmpeg >/dev/null || fail "ffmpeg not found — brew install ffmpeg"
  command -v yaver >/dev/null || fail "yaver not on PATH"
  YAVER_VER=$(yaver version 2>/dev/null | head -1 || true)
  printf "   yaver: %s\n" "$YAVER_VER"

  if [[ ! -d "$DEMO_PROJECT" ]]; then
    fail "demo project not found: $DEMO_PROJECT  (set DEMO_PROJECT=/abs/path)"
  fi
  printf "   project: %s\n" "$DEMO_PROJECT"

  say "Manual checks (confirm each then press Enter):"
  printf "   [ ] iTerm/Terminal is full-screen, dark, no notifications\n"
  printf "   [ ] Phone: Yaver app open, paired, on 'Waiting for push' home\n"
  printf "   [ ] Phone: AirPlay mirroring to this Mac, visible on desktop\n"
  printf "   [ ] macOS menu bar hidden (System Settings → auto-hide)\n"
  printf "   [ ] Editor already open on a theme/color file in the project\n"
  printf "   [ ] Did a dry-run push already (warms hermesc cache)\n"
  wait_enter
fi

# ─── 2. start recording ──────────────────────────────────────────
say "Starting screen recording (1080p, 30fps)"
printf "   output: %s\n" "$RAW"
printf "   You have 3 seconds to get ready...\n"
for i in 3 2 1; do printf "   %s\n" "$i"; sleep 1; done

# Record the full main display (index 1 on macOS is the primary
# screen for avfoundation). Using '1:none' for video-only, no audio.
# 30 fps is plenty for a product demo loop.
ffmpeg -hide_banner -loglevel warning \
  -f avfoundation -capture_cursor 1 -capture_mouse_clicks 1 \
  -framerate 30 -i "1:none" \
  -pix_fmt yuv420p -c:v libx264 -preset ultrafast -crf 18 \
  -vsync cfr \
  "$RAW" &
FF_PID=$!

# Give ffmpeg a moment to start capturing.
sleep 1.5
say "Recording STARTED (pid $FF_PID). Walking the shots now."

# ─── 3. guided shots ─────────────────────────────────────────────
# Each beat prints what you should do ON screen. The script does
# NOT type for you — it only prompts. YOU type and drive the
# demo. Press Enter when that beat is captured, move to the next.

show_beat() {
  clear
  printf "\n\033[1;35m━━━ beat %s ━━━\033[0m\n" "$1"
  printf "\n%s\n" "$2"
  printf "\n\033[2m(press [Enter] when this frame is captured)\033[0m"
  read -r _
}

show_beat "1 / 8   (0:00–0:01)"   "Clean prompt in $DEMO_PROJECT.
Phone: Yaver app 'Waiting for push…' home.
Hold still — just a quiet opening second."

show_beat "2 / 8   (0:01–0:03)"   "Type:  yaver push   then hit Enter.
Terminal prints: [analyze] Bento, RN 0.81.5"

show_beat "3 / 8   (0:03–0:06)"   "Watch the terminal:
  [compile] JS → Hermes bytecode (3.2 MB)
  [push]    transferring...
Phone: 'Receiving bundle' screen, progress bar 0 → 100%."

show_beat "4 / 8   (0:06–0:08)"   "Terminal: ✓ live on device in N.Ns
Phone: app launches, menu cards appear in the original theme."

show_beat "5 / 8   (0:08–0:11)"   "Switch to the editor.
Change  primary: '#6366f1'  →  '#10b981'
Cmd+S to save."

show_beat "6 / 8   (0:11–0:14)"   "Terminal:
  [watch]  change: src/theme.ts
  [reload] pushed ✓
Phone: theme shifts to the new color, 'Hot reload ✓' toast pops."

show_beat "7 / 8   (0:14–0:17)"   "Back to a clean prompt.
Phone stays on the new theme — user can confirm it stuck."

show_beat "8 / 8   (0:17–0:20)"   "Hold still. Quiet closing seconds
so the clip loops cleanly on the landing."

# ─── 4. stop recording ───────────────────────────────────────────
say "Stopping recording"
# Send SIGINT to ffmpeg so it writes the moov atom cleanly.
kill -INT "$FF_PID" 2>/dev/null || true
wait "$FF_PID" 2>/dev/null || true

if [[ ! -s "$RAW" ]]; then
  fail "recording produced no file or 0 bytes: $RAW"
fi
RAW_SIZE=$(du -h "$RAW" | cut -f1)
say "Raw recording saved: $RAW ($RAW_SIZE)"

# ─── 5. encode ───────────────────────────────────────────────────
say "Encoding to web-ready H.264 MP4"
"$SCRIPT_DIR/encode.sh" "$RAW" "$ENCODED"

OUT_SIZE=$(du -h "$ENCODED" | cut -f1)
say "Encoded hero video: $ENCODED ($OUT_SIZE)"

# ─── 6. offer install ───────────────────────────────────────────
WEB_TARGET="$REPO_ROOT/web/public/demo-push.mp4"
say "Next step"
printf "   To replace the synthetic landing video with this real footage:\n"
printf "\n     cp \"%s\" \"%s\"\n\n" "$ENCODED" "$WEB_TARGET"
printf "   Then commit + deploy as usual.\n"
printf "   (Not doing it automatically so you can review the clip first.)\n"
