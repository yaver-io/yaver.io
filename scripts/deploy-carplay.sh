#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
IOS_DIR="$ROOT/mobile/ios"
INFO_PLIST="$IOS_DIR/Yaver/Info.plist"
ENTITLEMENTS="$IOS_DIR/Yaver/Yaver.entitlements"
SCENE_DELEGATE="$IOS_DIR/Yaver/YaverCarPlaySceneDelegate.swift"
UPLOAD=0

usage() {
  cat <<'EOF'
Usage: scripts/deploy-carplay.sh [--upload]

Preflight the native CarPlay voice-runtime surface, then build or upload the
shared iOS/TestFlight artifact. CarPlay is not a separate App Store binary;
it ships inside the iPhone app after the granted managed capability is enabled
on the App ID and the provisioning profile includes it.

Options:
  --upload   Run scripts/deploy-testflight.sh after CarPlay preflight passes.
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

if [ ! -f "$INFO_PLIST" ]; then
  echo "ERROR: missing iOS Info.plist at $INFO_PLIST" >&2
  exit 1
fi
if [ ! -f "$ENTITLEMENTS" ]; then
  echo "ERROR: missing iOS entitlements at $ENTITLEMENTS" >&2
  exit 1
fi
if [ ! -f "$SCENE_DELEGATE" ]; then
  echo "ERROR: missing native CarPlay scene delegate at $SCENE_DELEGATE" >&2
  exit 1
fi

if ! /usr/libexec/PlistBuddy -c "Print :UIApplicationSceneManifest:UISceneConfigurations:CPTemplateApplicationSceneSessionRoleApplication:0:UISceneDelegateClassName" "$INFO_PLIST" >/dev/null 2>&1; then
  echo "ERROR: Info.plist is missing the CPTemplateApplicationScene CarPlay scene manifest." >&2
  exit 1
fi
if ! /usr/libexec/PlistBuddy -c "Print :com.apple.developer.carplay-voice-based-conversation" "$ENTITLEMENTS" >/dev/null 2>&1; then
  if [ "$UPLOAD" = "1" ]; then
    echo "ERROR: CarPlay upload requires com.apple.developer.carplay-voice-based-conversation in Yaver.entitlements and in a regenerated provisioning profile." >&2
    exit 1
  fi
  echo "WARN: CarPlay entitlement is not enabled in Yaver.entitlements; simulator build can run, but CarPlay upload remains Apple-entitlement gated."
fi
if ! grep -q "CPTemplateApplicationSceneDelegate" "$SCENE_DELEGATE"; then
  echo "ERROR: CarPlay scene delegate does not implement CPTemplateApplicationSceneDelegate." >&2
  exit 1
fi

if ! xcodebuild -showsdks | grep -q "iphoneos"; then
  echo "ERROR: Xcode iOS SDK is not installed." >&2
  exit 1
fi

if [ "$UPLOAD" != "1" ]; then
  # -destination only, never -sdk. `-sdk iphonesimulator` is applied to EVERY
  # target in the scheme, which overrides the embedded YaverWatch target's
  # SDKROOT=watchos and rebuilds it as an iOS app. That target is
  # PRODUCT_NAME=Yaver, so it then lands on the same
  # Release-iphonesimulator/Yaver.app path as the real app and the build dies
  # with "Multiple commands produce .../Yaver.app". -destination lets each
  # target resolve its own SDK, which is what we wanted here all along.
  xcodebuild -workspace "$IOS_DIR/Yaver.xcworkspace" \
    -scheme Yaver \
    -configuration Release \
    -destination "generic/platform=iOS Simulator" \
    -derivedDataPath /tmp/YaverCarPlayBuild \
    CODE_SIGNING_ALLOWED=NO \
    build
  echo "CarPlay preflight/build passed. Upload with scripts/deploy-carplay.sh --upload after the App ID/profile carries the CarPlay entitlement."
  exit 0
fi

"$ROOT/scripts/deploy-testflight.sh"
