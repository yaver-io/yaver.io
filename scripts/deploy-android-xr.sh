#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
UPLOAD=0
SKIP_BUILD=0
IMMERSIVE=0

usage() {
  cat <<'EOF'
Usage: scripts/deploy-android-xr.sh [--upload] [--skip-build] [--immersive]

Build and analyze the Android XR / VR-compatible release lane. The current app
ships through the shared Android Play AAB by default; this script verifies the
AAB exists and checks the headset compatibility declarations before optionally
delegating upload to the Play internal track.

Use --immersive for a dedicated OpenXR / Quest-style release. That mode requires
hard XR/VR manifest declarations and fails the shared compatibility artifact.

Options:
  --upload      Upload the built AAB to Google Play internal testing.
  --skip-build  Reuse the existing app-release.aab and release manifest.
  --immersive   Require native immersive OpenXR/Quest manifest declarations.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --upload) UPLOAD=1 ;;
    --skip-build) SKIP_BUILD=1 ;;
    --immersive) IMMERSIVE=1 ;;
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
if [ ! -f "$MANIFEST" ]; then
  echo "ERROR: release manifest not found for XR declaration analysis: $MANIFEST" >&2
  echo "Run without --skip-build to generate it." >&2
  exit 1
fi

require_manifest_text() {
  local needle="$1"
  if grep -q "$needle" "$MANIFEST"; then
    echo "OK: Android XR manifest contains $needle"
  else
    echo "ERROR: Android XR release manifest missing $needle" >&2
    return 1
  fi
}

feature_block() {
  local feature="$1"
  awk -v feature="$feature" '
    /<uses-feature/ {
      block=$0
      in_block=1
      if ($0 ~ /\/>/) {
        in_block=0
        if (block ~ feature) {
          print block
          exit
        }
        block=""
      }
      next
    }
    in_block {
      block=block " " $0
      if ($0 ~ /\/>/) {
        in_block=0
        if (block ~ feature) {
          print block
          exit
        }
        block=""
      }
    }
  ' "$MANIFEST"
}

require_feature_required() {
  local feature="$1"
  local required="$2"
  local label="$3"
  local block
  block="$(feature_block "$feature")"
  if [ -n "$block" ] && printf '%s\n' "$block" | grep -Fq "android:required=\"$required\""; then
    echo "OK: Android XR manifest satisfies $label"
  else
    echo "ERROR: Android XR release manifest does not satisfy $label" >&2
    return 1
  fi
}

missing=0
for needle in \
  "android.software.xr.api.openxr" \
  "android.hardware.vr.headtracking" \
  "com.oculus.intent.category.VR" \
  "android.hardware.touchscreen" \
  "android.hardware.camera.any" \
  "android.hardware.camera" \
  "android.hardware.bluetooth"; do
  require_manifest_text "$needle" || missing=1
done

require_feature_required "android.hardware.touchscreen" "false" "touchscreen optional for headset-class devices" || missing=1
require_feature_required "android.hardware.camera" "false" "camera optional despite CAMERA permission" || missing=1

if [ "$IMMERSIVE" = "1" ]; then
  require_feature_required "android.software.xr.api.openxr" "true" "OpenXR feature required for XR-differentiated apps" || missing=1
  require_feature_required "android.hardware.vr.headtracking" "true" "Meta Quest headtracking required for immersive release" || missing=1
  require_manifest_text "android.window.PROPERTY_XR_ACTIVITY_START_MODE" || missing=1
  require_manifest_text "XR_ACTIVITY_START_MODE_FULL_SPACE_UNMANAGED" || missing=1
  require_manifest_text "libopenxr.google.so" || missing=1
else
  echo "Android XR compatibility mode: shared Play AAB is headset-visible but not a dedicated immersive OpenXR binary."
fi

if [ "$missing" != "0" ]; then
  exit 1
fi

if [ "$UPLOAD" = "1" ]; then
  PLAY_STORE_KEY_FILE="${PLAY_STORE_KEY_FILE:-$ROOT/keys/google-play-service-account.json}" \
    python3 "$ROOT/scripts/upload-playstore.py"
fi
