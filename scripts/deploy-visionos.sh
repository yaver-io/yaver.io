#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
UPLOAD=0
CONFIGURATION="${CONFIGURATION:-Release}"
DERIVED_DATA_PATH="${DERIVED_DATA_PATH:-/tmp/YaverVisionBuild}"
VISION_DIR="${VISION_DIR:-$ROOT/visionos}"

usage() {
  cat <<'EOF'
Usage: scripts/deploy-visionos.sh [--upload]

Analyze/build the Apple Vision Pro release lane. A native visionOS project is
optional today; when absent, this script verifies that the iOS app can be
analyzed for compatible iPad-on-visionOS distribution and exits before upload.

Environment:
  VISION_DIR      Override native visionOS project directory. Default: visionos/
  SCHEME          Native visionOS scheme when VISION_DIR exists. Default: YaverVision
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --upload) UPLOAD=1 ;;
    --help|-h) usage; exit 0 ;;
    *) echo "Unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

if ! xcodebuild -showsdks | grep -Eq "xros|visionos"; then
  echo "ERROR: Xcode visionOS SDK is not installed. Install the visionOS platform component in Xcode, then retry." >&2
  exit 1
fi

if [ ! -d "$VISION_DIR" ]; then
  echo "No native visionOS project found at $VISION_DIR."
  echo "Running compatible iOS app analysis instead."
  plutil -lint "$ROOT/mobile/ios/Yaver/Info.plist"
  plutil -lint "$ROOT/mobile/ios/Yaver/Yaver.entitlements"
  echo "visionOS native upload is gated until a visionOS platform/project exists in App Store Connect."
  if [ "$UPLOAD" = "1" ]; then
    echo "ERROR: refusing upload without a native visionOS project. Ship compatible iPad mode through the iOS/TestFlight lane." >&2
    exit 1
  fi
  exit 0
fi

if [ ! -d "$VISION_DIR/YaverVision.xcodeproj" ] && [ ! -d "$VISION_DIR/YaverVision.xcworkspace" ]; then
  echo "ERROR: native visionOS directory exists but no YaverVision project/workspace was found in $VISION_DIR." >&2
  exit 1
fi

SCHEME="${SCHEME:-YaverVision}"
PROJECT_ARGS=()
if [ -d "$VISION_DIR/YaverVision.xcworkspace" ]; then
  PROJECT_ARGS=(-workspace "$VISION_DIR/YaverVision.xcworkspace")
else
  PROJECT_ARGS=(-project "$VISION_DIR/YaverVision.xcodeproj")
fi

if [ "$UPLOAD" = "1" ]; then
  echo "ERROR: native visionOS upload is not wired yet. Add signing/export settings once the App Store Connect visionOS record is ready." >&2
  exit 1
fi

xcodebuild "${PROJECT_ARGS[@]}" \
  -scheme "$SCHEME" \
  -configuration "$CONFIGURATION" \
  -sdk xros \
  -destination "generic/platform=visionOS" \
  -derivedDataPath "$DERIVED_DATA_PATH" \
  CODE_SIGNING_ALLOWED=NO \
  build

echo "visionOS build analysis passed."
