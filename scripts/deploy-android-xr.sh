#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
UPLOAD=0
SKIP_BUILD=0

usage() {
  cat <<'EOF'
Usage: scripts/deploy-android-xr.sh [--upload] [--skip-build]

Build and analyze the Android XR / VR-compatible release lane. The current app
ships through the shared Android Play AAB; this script verifies the AAB exists
and reports XR/VR manifest declarations before optionally delegating upload to
the Play internal track.

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

if [ ! -f "$AAB" ]; then
  echo "ERROR: release AAB not found: $AAB" >&2
  exit 1
fi

echo "Android XR AAB ready: $AAB"
if [ -f "$MANIFEST" ]; then
  for needle in \
    "android.hardware.vr.headtracking" \
    "android.software.xr.api" \
    "com.oculus.intent.category.VR"; do
    if grep -q "$needle" "$MANIFEST"; then
      echo "OK: Android XR manifest contains $needle"
    else
      echo "WARN: Android XR release manifest does not contain $needle"
    fi
  done
else
  echo "WARN: release manifest not found for XR declaration analysis: $MANIFEST"
fi

if [ "$UPLOAD" = "1" ]; then
  PLAY_STORE_KEY_FILE="${PLAY_STORE_KEY_FILE:-$ROOT/keys/google-play-service-account.json}" \
    python3 "$ROOT/scripts/upload-playstore.py"
fi
