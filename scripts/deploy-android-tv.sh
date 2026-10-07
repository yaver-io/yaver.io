#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
sh "$ROOT/mobile/native/plain-ssh/build.sh" android
UPLOAD=0
SKIP_BUILD=0
PACKAGE="${TV_PACKAGE:-io.yaver.mobile}"
MOBILE_GRADLE="$ROOT/mobile/android/app/build.gradle"
MOBILE_VERSION_CODE="$(grep 'versionCode ' "$MOBILE_GRADLE" | head -1 | sed 's/[^0-9]//g')"
MOBILE_VERSION_NAME="$(grep 'versionName ' "$MOBILE_GRADLE" | head -1 | sed 's/.*versionName[[:space:]]*"\([^"]*\)".*/\1/')"
VERSION_CODE="${TV_VERSION_CODE:-$((MOBILE_VERSION_CODE + 2))}"
VERSION_NAME="${TV_VERSION_NAME:-${MOBILE_VERSION_NAME}-tv}"

usage() {
  cat <<'EOF'
Usage: scripts/deploy-android-tv.sh [--upload] [--skip-build]

Build and verify the standalone Android TV release surface. It is a distinct
TV-targeted AAB built from androidtv/ and distributed from the existing
io.yaver.mobile Play listing on its dedicated TV form-factor track.

Options:
  --upload      Upload the built AAB to Google Play internal testing.
  --skip-build  Reuse the existing app-release.aab and release manifest.

Environment:
  TV_VERSION_CODE  Explicit version code. Uploads otherwise choose the first
                   unused code at or above mobile versionCode + 2.
  TV_VERSION_NAME  Version name. Defaults to mobile versionName + "-tv".
  TV_PACKAGE       Play application ID. Defaults to io.yaver.mobile.
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

# Play permanently reserves every uploaded versionCode. Resolve the live
# maximum before compiling rather than discovering a collision after the
# expensive standalone TV build has completed.
if [ "$UPLOAD" = "1" ] && [ -z "${TV_VERSION_CODE:-}" ]; then
  PLAY_STORE_KEY_FILE="${PLAY_STORE_KEY_FILE:-$ROOT/keys/google-play-service-account.json}"
  [ -f "$PLAY_STORE_KEY_FILE" ] || {
    echo "ERROR: Play credentials are required to choose a collision-free Android TV versionCode." >&2
    exit 2
  }
  VERSION_CODE="$(PLAY_STORE_KEY_FILE="$PLAY_STORE_KEY_FILE" \
    "$ROOT/scripts/run-playstore-upload.sh" --next-version-code \
    "$PACKAGE" "$((MOBILE_VERSION_CODE + 2))" | tail -n 1)"
  [[ "$VERSION_CODE" =~ ^[0-9]+$ ]] || {
    echo "ERROR: Play returned an invalid Android TV versionCode: $VERSION_CODE" >&2
    exit 2
  }
fi

AAB="$ROOT/androidtv/app/build/outputs/bundle/release/app-release.aab"
BANNER="$ROOT/androidtv/app/src/main/res/drawable-xhdpi/tv_banner.png"

# shellcheck source=scripts/lib/android-sdk.sh
source "$ROOT/scripts/lib/android-sdk.sh"
source "$ROOT/scripts/lib/android-gradle-home.sh"
yaver_configure_android_gradle_home "$ROOT"
yaver_resolve_android_sdk
# shellcheck source=scripts/lib/android-gradle-memory.sh
source "$ROOT/scripts/lib/android-gradle-memory.sh"
yaver_android_gradle_memory_args
# shellcheck source=scripts/lib/android-aab-signing.sh
source "$ROOT/scripts/lib/android-aab-signing.sh"

if [ "$SKIP_BUILD" != "1" ]; then
  # Compilation competes heavily with Xcode on the local release Mac. A
  # verified --skip-build upload does not compile anything, so it must remain
  # usable while another Apple release is archiving.
  if pgrep -f '[x]codebuild' >/dev/null 2>&1; then
    echo "ERROR: refusing Android TV compilation while an Xcode build is active." >&2
    echo "Wait for the mobile build to finish, then retry deploy/deploy.sh android-tv." >&2
    exit 2
  fi
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
    -PyaverTvApplicationId="$PACKAGE" \
    -PyaverTvVersionCode="$VERSION_CODE" \
    -PyaverTvVersionName="$VERSION_NAME" \
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

yaver_verify_aab_signer "$AAB"

echo "Android TV AAB ready: $AAB"
echo "  versionCode: $VERSION_CODE"
echo "  versionName: $VERSION_NAME"

if [ "$UPLOAD" = "1" ]; then
  PLAY_PACKAGE_NAME="$PACKAGE" \
    AAB_PATH="$AAB" \
    PLAY_VERSION_CODE="$VERSION_CODE" \
    PLAY_TRACK="${PLAY_TRACK:-tv:internal}" \
    PLAY_STORE_KEY_FILE="${PLAY_STORE_KEY_FILE:-$ROOT/keys/google-play-service-account.json}" \
    "$ROOT/scripts/run-playstore-upload.sh"
fi
