#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
UPLOAD=0
NATIVE_ONLY=0
CONFIGURATION="${CONFIGURATION:-Release}"
DERIVED_DATA_PATH="${DERIVED_DATA_PATH:-/tmp/YaverVisionBuild}"
VISION_DIR="${VISION_DIR:-$ROOT/visionos}"
IOS_INFO_PLIST="${IOS_INFO_PLIST:-$ROOT/mobile/ios/Yaver/Info.plist}"
IOS_ENTITLEMENTS="${IOS_ENTITLEMENTS:-$ROOT/mobile/ios/Yaver/Yaver.entitlements}"

usage() {
  cat <<'EOF'
Usage: scripts/deploy-visionos.sh [--upload] [--native-only]

Analyze/build the Apple Vision Pro release lane. A native visionOS project is
optional today; when absent, this script verifies that the iOS app can be
analyzed for compatible iPad-on-visionOS distribution. With --upload and no
native project, it ships that compatible artifact through the iOS/TestFlight
lane. Use --native-only to require a real visionOS project.

Options:
  --upload       Upload the native visionOS artifact, or compatible iOS artifact
                 when no native visionOS project exists.
  --native-only  Refuse compatible iOS mode; require a native visionOS project.

Environment:
  VISION_DIR      Override native visionOS project directory. Default: visionos/
  SCHEME          Native visionOS scheme when VISION_DIR exists. Default: YaverVision
  IOS_INFO_PLIST  Compatible iOS/iPadOS app Info.plist to analyze.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --upload) UPLOAD=1 ;;
    --native-only) NATIVE_ONLY=1 ;;
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
  if [ "$NATIVE_ONLY" = "1" ]; then
    if [ "$UPLOAD" = "1" ]; then
      echo "ERROR: native-only visionOS upload requires a native visionOS project." >&2
    else
      echo "ERROR: native-only visionOS analysis requires a native visionOS project." >&2
    fi
    exit 1
  fi
  if [ "$UPLOAD" = "1" ]; then
    echo "Uploading compatible Vision Pro artifact through the iOS/TestFlight lane."
    "$ROOT/scripts/deploy-testflight.sh"
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

ARCHIVE_PATH="${ARCHIVE_PATH:-/tmp/YaverVision.xcarchive}"
EXPORT_PATH="${EXPORT_PATH:-/tmp/YaverVisionExport}"
VERSION_ARGS=()
[ -n "${VISIONOS_MARKETING_VERSION:-}" ] && VERSION_ARGS+=("MARKETING_VERSION=$VISIONOS_MARKETING_VERSION")
[ -n "${VISIONOS_BUILD_NUMBER:-}" ] && VERSION_ARGS+=("CURRENT_PROJECT_VERSION=$VISIONOS_BUILD_NUMBER")

# macOS ships bash 3.2, where expanding an EMPTY array under `set -u` is an
# "unbound variable" error rather than nothing. Both xcodebuild calls below
# splat VERSION_ARGS, which is empty unless VISIONOS_MARKETING_VERSION /
# VISIONOS_BUILD_NUMBER is set — so the default invocation (no version
# override) died at the build/archive step on every Mac. The `[@]+` guard
# expands to zero words when the array is empty. Same idiom at both sites.

# The .xcodeproj is generated from project.yml and gitignored — regenerate so the
# spec stays the single source of truth (same contract as tvos/).
if command -v xcodegen >/dev/null 2>&1 && [ -f "$VISION_DIR/project.yml" ]; then
  ( cd "$VISION_DIR" && xcodegen generate >/dev/null )
fi

if [ "$UPLOAD" != "1" ]; then
  analyze_compatible_ios_for_visionos

  xcodebuild "${PROJECT_ARGS[@]}" \
    -scheme "$SCHEME" \
    -configuration "$CONFIGURATION" \
    -sdk xros \
    -destination "generic/platform=visionOS" \
    -derivedDataPath "$DERIVED_DATA_PATH" \
    CODE_SIGNING_ALLOWED=NO \
    ${VERSION_ARGS[@]+"${VERSION_ARGS[@]}"} \
    build

  echo "visionOS build analysis passed."
  exit 0
fi

: "${APP_STORE_KEY_PATH:?Set APP_STORE_KEY_PATH}"
: "${APP_STORE_KEY_ID:?Set APP_STORE_KEY_ID}"
: "${APP_STORE_KEY_ISSUER:?Set APP_STORE_KEY_ISSUER}"
: "${APPLE_TEAM_ID:?Set APPLE_TEAM_ID}"

rm -rf "$ARCHIVE_PATH" "$EXPORT_PATH"

# AUTOMATIC signing on purpose. deploy-tvos.sh pins its profile BY NAME with
# CODE_SIGN_STYLE=Manual, and that broke the moment CarPlay was enabled on the
# App ID — turning on any capability marks every existing profile INVALID.
# -allowProvisioningUpdates lets Xcode regenerate instead of dying.
echo "Archiving visionOS…"
xcodebuild "${PROJECT_ARGS[@]}" \
  -scheme "$SCHEME" \
  -configuration "$CONFIGURATION" \
  -destination "generic/platform=visionOS" \
  -archivePath "$ARCHIVE_PATH" archive \
  DEVELOPMENT_TEAM="$APPLE_TEAM_ID" CODE_SIGN_STYLE=Automatic \
  -allowProvisioningUpdates \
  -authenticationKeyPath "$APP_STORE_KEY_PATH" \
  -authenticationKeyID "$APP_STORE_KEY_ID" \
  -authenticationKeyIssuerID "$APP_STORE_KEY_ISSUER" \
  -derivedDataPath "$DERIVED_DATA_PATH" \
  ${VERSION_ARGS[@]+"${VERSION_ARGS[@]}"}

[ -d "$ARCHIVE_PATH" ] || { echo "ERROR: archive failed — no .xcarchive produced" >&2; exit 1; }

cat > /tmp/VisionExportOptions.plist <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>method</key>
  <string>app-store-connect</string>
  <key>teamID</key>
  <string>$APPLE_TEAM_ID</string>
  <key>destination</key>
  <string>upload</string>
  <key>uploadSymbols</key>
  <false/>
</dict>
</plist>
EOF

echo "Exporting & uploading visionOS…"
xcodebuild -exportArchive -archivePath "$ARCHIVE_PATH" \
  -exportOptionsPlist /tmp/VisionExportOptions.plist \
  -exportPath "$EXPORT_PATH" -allowProvisioningUpdates \
  -authenticationKeyPath "$APP_STORE_KEY_PATH" \
  -authenticationKeyID "$APP_STORE_KEY_ID" \
  -authenticationKeyIssuerID "$APP_STORE_KEY_ISSUER"

echo "✓ visionOS build uploaded to App Store Connect"
