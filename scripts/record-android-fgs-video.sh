#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEVICE="${ANDROID_SERIAL:-}"
OUT="${OUT:-$ROOT/tmp/yaver-fgs-special-use-redroid.mp4}"
PACKAGE="${PACKAGE:-io.yaver.mobile}"
MAIN_ACTIVITY="${MAIN_ACTIVITY:-io.yaver.mobile/.MainActivity}"
DURATION="${DURATION:-45}"

usage() {
  cat <<'EOF'
Usage: scripts/record-android-fgs-video.sh

Record the Google Play foreground-service declaration video from an attached
Android/Redroid device.

Environment:
  ANDROID_SERIAL  adb serial to use when multiple devices are attached.
  OUT             Output mp4 path. Default: tmp/yaver-fgs-special-use-redroid.mp4
  PACKAGE         App package. Default: io.yaver.mobile
  DURATION        Max screenrecord seconds. Default: 45

Before running:
  1. Start Redroid or attach an Android device.
  2. Install a Yaver APK/AAB split set on that device.
  3. Sign in if the UI flow requires it.

What to do while recording:
  Open "This phone as a box", start the on-device box / host my assistant, pull
  the notification shade to show the persistent Yaver foreground notification,
  return to the app, and stop the service.
EOF
}

if [ "${1:-}" = "--help" ] || [ "${1:-}" = "-h" ]; then
  usage
  exit 0
fi

ADB=(adb)
if [ -n "$DEVICE" ]; then
  ADB+=(-s "$DEVICE")
fi

mkdir -p "$(dirname "$OUT")"

"${ADB[@]}" wait-for-device
"${ADB[@]}" shell cmd package resolve-activity "$PACKAGE" >/dev/null
"${ADB[@]}" shell settings put system screen_off_timeout 600000 >/dev/null 2>&1 || true
"${ADB[@]}" shell input keyevent WAKEUP >/dev/null 2>&1 || true
"${ADB[@]}" shell wm dismiss-keyguard >/dev/null 2>&1 || true
"${ADB[@]}" shell monkey -p "$PACKAGE" -c android.intent.category.LAUNCHER 1 >/dev/null

echo "Recording $PACKAGE foreground-service flow to $OUT"
echo "Perform the UI flow now. Recording stops after ${DURATION}s or Ctrl+C."
REMOTE="/sdcard/yaver-fgs-special-use.mp4"
"${ADB[@]}" shell rm -f "$REMOTE" >/dev/null 2>&1 || true
"${ADB[@]}" shell screenrecord --time-limit "$DURATION" "$REMOTE" || true
"${ADB[@]}" pull "$REMOTE" "$OUT"
echo "Saved $OUT"
