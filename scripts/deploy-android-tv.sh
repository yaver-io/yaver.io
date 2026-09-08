#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
UPLOAD=0
SKIP_BUILD=0

usage() {
  cat <<'EOF'
Usage: scripts/deploy-android-tv.sh [--upload] [--skip-build]

Build and verify the standalone Android TV release surface. The TV package is
io.yaver.tv and is built from androidtv/; it is not the Expo phone AAB.

Options:
  --upload      Upload the built AAB to Google Play internal testing.
  --skip-build  Reuse the existing app-release.aab and release manifest.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --upload) UPLOAD=1 ;;
    --skip-build) SKIP_BUILD=1 ;;
    --help|-h) usage; exit 0 ;;
    *) echo "Unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

AAB="$ROOT/androidtv/app/build/outputs/bundle/release/app-release.aab"
BANNER="$ROOT/androidtv/app/src/main/res/drawable-xhdpi/tv_banner.png"

# shellcheck source=scripts/lib/android-sdk.sh
source "$ROOT/scripts/lib/android-sdk.sh"
yaver_resolve_android_sdk
# shellcheck source=scripts/lib/android-gradle-memory.sh
source "$ROOT/scripts/lib/android-gradle-memory.sh"
yaver_android_gradle_memory_args

if pgrep -f '[x]codebuild' >/dev/null 2>&1; then
  echo "ERROR: refusing Android TV compilation while an Xcode build is active." >&2
  echo "Wait for the mobile build to finish, then retry deploy/deploy.sh android-tv." >&2
  exit 2
fi

if [ "$SKIP_BUILD" != "1" ]; then
  if [ ! -f "$ROOT/mobile/android/keystore.properties" ] || [ ! -f "$ROOT/keys/yaver-upload.keystore" ]; then
    echo "Android TV release signing material is missing; running the Yaver signing bootstrap..."
    (cd "$ROOT" && ./scripts/bootstrap-android-signing.sh)
  fi
  if [ ! -f "$ROOT/mobile/android/keystore.properties" ] || [ ! -f "$ROOT/keys/yaver-upload.keystore" ]; then
    echo "ERROR: Android TV release signing requires mobile/android/keystore.properties and keys/yaver-upload.keystore." >&2
    echo "Run ./scripts/bootstrap-android-signing.sh with the configured Yaver vault, then retry." >&2
    exit 2
  fi
  "$ROOT/mobile/android/gradlew" -p "$ROOT/androidtv" bundleRelease \
    "${YAVER_ANDROID_GRADLE_ARGS[@]}"
fi

if ! MANIFEST="$(yaver_release_manifest_path "$ROOT/androidtv")"; then
  echo "ERROR: Android TV release manifest not found under androidtv/app/build/intermediates." >&2
  echo "Run without --skip-build to generate it." >&2
  exit 1
fi

missing=0
for needle in \
  "android.software.leanback" \
  "android.intent.category.LEANBACK_LAUNCHER" \
  "android.hardware.touchscreen" \
  "@drawable/tv_banner"; do
  if grep -q "$needle" "$MANIFEST"; then
    echo "OK: Android TV manifest contains $needle"
  else
    echo "ERROR: Android TV release manifest missing $needle" >&2
    missing=1
  fi
done

if [ ! -f "$BANNER" ]; then
  echo "ERROR: Android TV banner missing: $BANNER" >&2
  missing=1
else
  python3 - "$BANNER" <<'PY'
import struct
import sys

path = sys.argv[1]
with open(path, "rb") as f:
    header = f.read(24)
if len(header) < 24 or header[:8] != b"\x89PNG\r\n\x1a\n":
    raise SystemExit(f"ERROR: TV banner is not a PNG: {path}")
width, height = struct.unpack(">II", header[16:24])
if (width, height) != (320, 180):
    raise SystemExit(f"ERROR: TV banner must be 320x180, got {width}x{height}: {path}")
print(f"OK: Android TV banner is {width}x{height}")
PY
fi

if [ "$missing" != "0" ]; then
  exit 1
fi

if [ ! -f "$AAB" ]; then
  echo "ERROR: release AAB not found: $AAB" >&2
  exit 1
fi

echo "Android TV AAB ready: $AAB"

if [ "$UPLOAD" = "1" ]; then
  PLAY_PACKAGE_NAME="io.yaver.tv" \
    AAB_PATH="$AAB" \
    PLAY_STORE_KEY_FILE="${PLAY_STORE_KEY_FILE:-$ROOT/keys/google-play-service-account.json}" \
    "$ROOT/scripts/run-playstore-upload.sh"
fi
