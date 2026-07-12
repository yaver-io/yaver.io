#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
UPLOAD=0
SKIP_BUILD=0
PACKAGE="${WEAR_PACKAGE:-io.yaver.mobile}"
MOBILE_GRADLE="$ROOT/mobile/android/app/build.gradle"
MOBILE_VERSION_CODE="$(grep 'versionCode ' "$MOBILE_GRADLE" | head -1 | sed 's/[^0-9]//g')"
MOBILE_VERSION_NAME="$(grep 'versionName ' "$MOBILE_GRADLE" | head -1 | sed 's/.*versionName[[:space:]]*"\([^"]*\)".*/\1/')"
VERSION_CODE="${WEAR_VERSION_CODE:-$((MOBILE_VERSION_CODE + 1))}"
VERSION_NAME="${WEAR_VERSION_NAME:-${MOBILE_VERSION_NAME}-wear}"

usage() {
  cat <<'EOF'
Usage: scripts/deploy-wear-os.sh [--upload] [--skip-build]

Build the standalone Wear OS app as a watch-only bundle for the existing Yaver
Play listing. Google recommends using the same package/listing for phone and
Wear OS apps; the manifest's required android.hardware.type.watch feature keeps
this artifact watch-targeted.

Environment:
  WEAR_PACKAGE       Package for the bundle. Defaults to io.yaver.mobile.
  WEAR_VERSION_CODE  Version code for Play upload. Defaults to mobile versionCode + 1.
  WEAR_VERSION_NAME  Version name. Defaults to mobile versionName + "-wear".
  PLAY_TRACK         Google Play track for upload. Defaults to internal.

Options:
  --upload      Upload the built AAB to Google Play internal testing.
  --skip-build  Reuse the existing Wear app-release.aab.
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

MANIFEST="$ROOT/wear/app/build/intermediates/merged_manifests/release/processReleaseManifest/AndroidManifest.xml"
AAB="$ROOT/wear/app/build/outputs/bundle/release/app-release.aab"

if [ "$SKIP_BUILD" != "1" ]; then
  (cd "$ROOT/wear" && \
    ../mobile/android/gradlew -p . :app:bundleRelease \
      -PyaverWearApplicationId="$PACKAGE" \
      -PyaverWearVersionCode="$VERSION_CODE" \
      -PyaverWearVersionName="$VERSION_NAME" \
      --no-daemon --max-workers=2)
fi

if [ ! -f "$MANIFEST" ]; then
  echo "ERROR: Wear release manifest not found: $MANIFEST" >&2
  exit 1
fi

missing=0
for needle in \
  "android.hardware.type.watch" \
  "com.google.android.wearable.standalone" \
  "$PACKAGE"; do
  if grep -q "$needle" "$MANIFEST"; then
    echo "OK: Wear manifest contains $needle"
  else
    echo "ERROR: Wear release manifest missing $needle" >&2
    missing=1
  fi
done

if [ "$missing" != "0" ]; then
  exit 1
fi

if [ ! -f "$AAB" ]; then
  echo "ERROR: Wear AAB not found: $AAB" >&2
  exit 1
fi

echo "Wear OS AAB ready: $AAB"
echo "  package: $PACKAGE"
echo "  versionCode: $VERSION_CODE"
echo "  versionName: $VERSION_NAME"

if [ "$UPLOAD" = "1" ]; then
  PLAY_STORE_KEY_FILE="${PLAY_STORE_KEY_FILE:-$ROOT/keys/google-play-service-account.json}" \
  AAB_PATH="$AAB" \
  PLAY_TRACK="${PLAY_TRACK:-internal}" \
    python3 "$ROOT/scripts/upload-playstore.py"
fi
