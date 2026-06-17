#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
UPLOAD=0
SKIP_BUILD=0

usage() {
  cat <<'EOF'
Usage: scripts/deploy-android-tv.sh [--upload] [--skip-build]

Build and verify the Android TV release surface. Android TV ships in the same
Play AAB as the phone app; this script verifies the release manifest contains
the leanback launcher/features before optionally uploading to Play internal.

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

MANIFEST="$ROOT/mobile/android/app/build/intermediates/merged_manifests/release/processReleaseManifest/AndroidManifest.xml"
AAB="$ROOT/mobile/android/app/build/outputs/bundle/release/app-release.aab"

if [ "$SKIP_BUILD" != "1" ]; then
  "$ROOT/scripts/deploy-playstore.sh"
fi

if [ ! -f "$MANIFEST" ]; then
  echo "ERROR: release manifest not found: $MANIFEST" >&2
  echo "Run without --skip-build to generate it." >&2
  exit 1
fi

missing=0
for needle in \
  "android.software.leanback" \
  "android.intent.category.LEANBACK_LAUNCHER" \
  "android.hardware.touchscreen"; do
  if grep -q "$needle" "$MANIFEST"; then
    echo "OK: Android TV manifest contains $needle"
  else
    echo "ERROR: Android TV release manifest missing $needle" >&2
    missing=1
  fi
done

if [ "$missing" != "0" ]; then
  exit 1
fi

if [ ! -f "$AAB" ]; then
  echo "ERROR: release AAB not found: $AAB" >&2
  exit 1
fi

echo "Android TV AAB ready: $AAB"

if [ "$UPLOAD" = "1" ]; then
  PLAY_STORE_KEY_FILE="${PLAY_STORE_KEY_FILE:-$ROOT/keys/google-play-service-account.json}" \
    python3 "$ROOT/scripts/upload-playstore.py"
fi
