#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
UPLOAD=0
SKIP_BUILD=0
PACKAGE="${WEAR_PACKAGE:-io.yaver.mobile}"
VERSION_CODE="${WEAR_VERSION_CODE:-263}"
VERSION_NAME="${WEAR_VERSION_NAME:-1.18.141-watch}"

usage() {
  cat <<'EOF'
Usage: scripts/deploy-wear-os.sh [--upload] [--skip-build]

Build the standalone Wear OS app as a watch-only bundle for the existing Yaver
Play listing. Google recommends using the same package/listing for phone and
Wear OS apps; the manifest's required android.hardware.type.watch feature keeps
this artifact watch-targeted.

Environment:
  WEAR_PACKAGE       Package for the bundle. Defaults to io.yaver.mobile.
  WEAR_VERSION_CODE  Version code for Play upload. Defaults to 263.
  WEAR_VERSION_NAME  Version name. Defaults to 1.18.141-watch.

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

if [ "$UPLOAD" = "1" ]; then
  PLAY_STORE_KEY_FILE="${PLAY_STORE_KEY_FILE:-$ROOT/keys/google-play-service-account.json}" \
  AAB_PATH="$AAB" \
  PLAY_TRACK="${PLAY_TRACK:-wear:internal}" \
    python3 "$ROOT/scripts/upload-playstore.py"
fi
