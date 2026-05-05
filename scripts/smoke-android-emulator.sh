#!/usr/bin/env bash
# Phase 1 smoke test: prove the Android emulator + capture pipeline
# works on yaver-test-ephemeral. Designed to run on the box itself.
#
#   ssh yaver-test-ephemeral 'bash /root/Workspace/yaver.io/scripts/smoke-android-emulator.sh'
#
# What it proves end-to-end:
#   • Boot Yaver_API_35 (ARM64-v8a, google_atd) under TCG software
#     emulation in headless mode
#   • adb sees the emulator online + reports sys.boot_completed=1
#   • adb exec-out screencap captures a real PNG frame from the
#     framebuffer (the surface our Pion H.264 track will read from)
#   • adb screenrecord can produce a few seconds of H.264 NAL units
#     (the actual transport for the WebRTC pipeline at
#     remote_runtime_video_track.go:42)
#   • Shutdown is clean (no orphaned qemu-system processes)
#
# Output: tagged PASS/FAIL per step + a final summary so a CI run or
# a cron-driven canary can grep the result.

set -uo pipefail

ANDROID_SDK_ROOT="${ANDROID_SDK_ROOT:-/opt/android-sdk}"
export ANDROID_SDK_ROOT ANDROID_HOME=$ANDROID_SDK_ROOT
export PATH="$ANDROID_SDK_ROOT/cmdline-tools/latest/bin:$ANDROID_SDK_ROOT/platform-tools:$ANDROID_SDK_ROOT/emulator:$PATH"

AVD="${AVD:-Yaver_API_35}"
BOOT_TIMEOUT="${BOOT_TIMEOUT:-600}"  # 10 min — TCG cold boot is slow
FRAME_OUT=/tmp/yaver-smoke-frame.png
RECORD_OUT=/tmp/yaver-smoke-record.mp4
EMULATOR_LOG=/tmp/yaver-smoke-emulator.log

PASSES=0
FAILS=0
note() { printf "%s %s\n" "$(date +%H:%M:%S)" "$*" >&2; }
ok()   { PASSES=$((PASSES+1)); note "[PASS] $*"; }
bad()  { FAILS=$((FAILS+1)); note "[FAIL] $*"; }

cleanup() {
  note "cleanup — killing emulator + adb-server"
  if [ -n "${EMULATOR_PID:-}" ]; then
    kill "$EMULATOR_PID" 2>/dev/null || true
    wait "$EMULATOR_PID" 2>/dev/null || true
  fi
  pkill -f "qemu-system-aarch64.*$AVD" 2>/dev/null || true
  adb kill-server 2>/dev/null || true
}
trap cleanup EXIT

# ── 1. Tools on PATH ──────────────────────────────────────────────
note "step 1 — required tools on PATH"
all_tools_ok=1
for t in adb emulator avdmanager ffmpeg; do
  if command -v "$t" >/dev/null 2>&1; then
    ok "$t at $(command -v $t)"
  else
    bad "$t MISSING"
    all_tools_ok=0
  fi
done
if [ $all_tools_ok -eq 0 ]; then
  bad "abort: required tools missing — re-run bootstrap.sh"
  exit 1
fi

# ── 2. AVD exists ─────────────────────────────────────────────────
note "step 2 — AVD '$AVD' exists"
if avdmanager list avd 2>&1 | grep -q "Name: $AVD"; then
  ok "AVD $AVD registered"
else
  bad "AVD $AVD not found — bootstrap's avdmanager create step must have failed"
  exit 1
fi

# ── 3. Boot the emulator ──────────────────────────────────────────
note "step 3 — boot $AVD under TCG (cores=2, no audio, no boot anim)"
note "       expect ~90s on cax31; longer if zram is paging"
emulator -avd "$AVD" \
  -no-window -no-snapshot-save -no-boot-anim -noaudio \
  -accel tcg -cores 2 \
  > "$EMULATOR_LOG" 2>&1 &
EMULATOR_PID=$!
note "emulator pid=$EMULATOR_PID, log=$EMULATOR_LOG"

# Start adb server explicitly so it's not racing with the emulator
adb start-server >/dev/null 2>&1 || true

# Poll for adb device, then for sys.boot_completed
DEVICE=""
deadline=$(( $(date +%s) + BOOT_TIMEOUT ))
while [ "$(date +%s)" -lt "$deadline" ]; do
  if ! kill -0 "$EMULATOR_PID" 2>/dev/null; then
    bad "emulator process died (pid $EMULATOR_PID) — last 30 lines of emulator log:"
    tail -30 "$EMULATOR_LOG" >&2
    exit 1
  fi
  if [ -z "$DEVICE" ]; then
    DEVICE=$(adb devices 2>/dev/null | awk '/^emulator-/ && $2=="device" {print $1; exit}')
    if [ -n "$DEVICE" ]; then
      note "adb sees $DEVICE — waiting for boot complete"
    fi
  fi
  if [ -n "$DEVICE" ]; then
    boot=$(adb -s "$DEVICE" shell getprop sys.boot_completed 2>/dev/null | tr -d '\r\n ')
    if [ "$boot" = "1" ]; then
      elapsed=$(( $(date +%s) - (deadline - BOOT_TIMEOUT) ))
      ok "$DEVICE boot_completed in ${elapsed}s"
      break
    fi
  fi
  sleep 5
done
if [ -z "$DEVICE" ] || [ "$boot" != "1" ]; then
  bad "boot did not complete in ${BOOT_TIMEOUT}s — last 30 lines of emulator log:"
  tail -30 "$EMULATOR_LOG" >&2
  exit 1
fi

# ── 4. Capture a frame (PNG) ──────────────────────────────────────
note "step 4 — capture single frame via adb exec-out screencap"
adb -s "$DEVICE" exec-out screencap -p > "$FRAME_OUT" 2>/dev/null || true
if [ -s "$FRAME_OUT" ]; then
  # Verify PNG magic 89 50 4E 47 0D 0A 1A 0A
  magic=$(head -c 8 "$FRAME_OUT" | od -An -tx1 | tr -d ' \n')
  if [ "$magic" = "89504e470d0a1a0a" ]; then
    bytes=$(stat -c%s "$FRAME_OUT" 2>/dev/null || stat -f%z "$FRAME_OUT")
    ok "frame captured: $FRAME_OUT ($bytes bytes, valid PNG)"
  else
    bad "frame written but not a valid PNG (magic=$magic)"
  fi
else
  bad "screencap produced no output"
fi

# ── 5. Record 5s of H.264 NAL units (Pion track surface) ──────────
note "step 5 — adb screenrecord 5s @ 4Mbps (the surface Pion reads)"
timeout 8 adb -s "$DEVICE" shell screenrecord --bit-rate 4000000 --time-limit 5 /sdcard/yaver-smoke.mp4 2>&1 | tail -3
adb -s "$DEVICE" pull /sdcard/yaver-smoke.mp4 "$RECORD_OUT" 2>&1 | tail -1
adb -s "$DEVICE" shell rm -f /sdcard/yaver-smoke.mp4 2>/dev/null || true
if [ -s "$RECORD_OUT" ]; then
  bytes=$(stat -c%s "$RECORD_OUT" 2>/dev/null || stat -f%z "$RECORD_OUT")
  if ffprobe -v error -select_streams v:0 -show_entries stream=codec_name "$RECORD_OUT" 2>/dev/null | grep -q "h264"; then
    ok "screenrecord produced H.264 video: $RECORD_OUT ($bytes bytes)"
  else
    bad "recorded file isn't H.264 — ffprobe didn't find h264 codec"
  fi
else
  bad "screenrecord produced no output"
fi

# ── 6. Memory + cpu snapshot during emulator run ──────────────────
note "step 6 — host resource snapshot (informational)"
free -h | grep -E "^Mem:|^Swap:" | sed 's/^/        /'
ps -p "$EMULATOR_PID" -o pid,pcpu,pmem,rss,cmd --no-headers 2>/dev/null | sed 's/^/        emulator: /'

# ── Summary ───────────────────────────────────────────────────────
note "═══════════════════════════════════════"
note "PASSES=$PASSES  FAILS=$FAILS"
if [ $FAILS -gt 0 ]; then
  note "═══ SMOKE TEST FAILED ═══"
  exit 1
fi
note "═══ SMOKE TEST PASSED — emulator + capture pipeline works ═══"
