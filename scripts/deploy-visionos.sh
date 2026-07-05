#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
UPLOAD=0
CONFIGURATION="${CONFIGURATION:-Release}"
DERIVED_DATA_PATH="${DERIVED_DATA_PATH:-/tmp/YaverVisionBuild}"
VISION_DIR="${VISION_DIR:-$ROOT/visionos}"
IOS_INFO_PLIST="${IOS_INFO_PLIST:-$ROOT/mobile/ios/Yaver/Info.plist}"
IOS_ENTITLEMENTS="${IOS_ENTITLEMENTS:-$ROOT/mobile/ios/Yaver/Yaver.entitlements}"

usage() {
  cat <<'EOF'
Usage: scripts/deploy-visionos.sh [--upload]

Analyze/build the Apple Vision Pro release lane. A native visionOS project is
optional today; when absent, this script verifies that the iOS app can be
analyzed for compatible iPad-on-visionOS distribution and exits before upload.

Environment:
  VISION_DIR      Override native visionOS project directory. Default: visionos/
  SCHEME          Native visionOS scheme when VISION_DIR exists. Default: YaverVision
  IOS_INFO_PLIST  Compatible iOS/iPadOS app Info.plist to analyze.
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

analyze_compatible_ios_for_visionos() {
  plutil -lint "$IOS_INFO_PLIST"
  plutil -lint "$IOS_ENTITLEMENTS"

  required_caps="$(/usr/libexec/PlistBuddy -c 'Print :UIRequiredDeviceCapabilities' "$IOS_INFO_PLIST" 2>/dev/null || true)"
  if echo "$required_caps" | grep -qi 'arkit'; then
    echo "ERROR: UIRequiredDeviceCapabilities requires ARKit; that would unnecessarily block compatible Vision Pro distribution." >&2
    return 1
  fi
  echo "OK: UIRequiredDeviceCapabilities does not require ARKit."

  if /usr/libexec/PlistBuddy -c 'Print :UISupportedInterfaceOrientations~ipad' "$IOS_INFO_PLIST" >/tmp/yaver-visionos-ipad-orientations.$$ 2>/dev/null; then
    if grep -q 'UIInterfaceOrientationLandscapeLeft' /tmp/yaver-visionos-ipad-orientations.$$ && \
       grep -q 'UIInterfaceOrientationLandscapeRight' /tmp/yaver-visionos-ipad-orientations.$$; then
      echo "OK: iPad orientation set includes both landscape orientations for Vision Pro compatible mode."
    else
      echo "ERROR: iPad orientations should include both landscape orientations for Vision Pro compatible mode." >&2
      rm -f /tmp/yaver-visionos-ipad-orientations.$$
      return 1
    fi
    rm -f /tmp/yaver-visionos-ipad-orientations.$$
  else
    echo "ERROR: UISupportedInterfaceOrientations~ipad is missing; Vision Pro compatible distribution should preserve iPad layout support." >&2
    return 1
  fi

  full_screen="$(/usr/libexec/PlistBuddy -c 'Print :UIRequiresFullScreen' "$IOS_INFO_PLIST" 2>/dev/null || true)"
  if [ "$full_screen" = "true" ]; then
    echo "ERROR: UIRequiresFullScreen=true limits iPad-style multitasking and is a poor fit for Vision Pro compatible mode." >&2
    return 1
  fi
  echo "OK: UIRequiresFullScreen is not true."
}

if [ ! -d "$VISION_DIR" ]; then
  echo "No native visionOS project found at $VISION_DIR."
  echo "Running compatible iOS app analysis instead."
  analyze_compatible_ios_for_visionos
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

analyze_compatible_ios_for_visionos

xcodebuild "${PROJECT_ARGS[@]}" \
  -scheme "$SCHEME" \
  -configuration "$CONFIGURATION" \
  -sdk xros \
  -destination "generic/platform=visionOS" \
  -derivedDataPath "$DERIVED_DATA_PATH" \
  CODE_SIGNING_ALLOWED=NO \
  build

echo "visionOS build analysis passed."
