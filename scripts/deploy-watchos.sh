#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WATCH_DIR="$ROOT/watch"
UPLOAD=0
CONFIGURATION="${CONFIGURATION:-Release}"
SCHEME="${SCHEME:-YaverWatch}"
ARCHIVE_PATH="${ARCHIVE_PATH:-/tmp/YaverWatchDist.xcarchive}"
DERIVED_DATA_PATH="${DERIVED_DATA_PATH:-/tmp/YaverWatchDistBuild}"
MARKETING_VERSION="${WATCHOS_MARKETING_VERSION:-}"
BUILD_NUMBER="${WATCHOS_BUILD_NUMBER:-}"
WATCHOS_PROVISIONING_PROFILE_SPECIFIER="${WATCHOS_PROVISIONING_PROFILE_SPECIFIER:-Yaver Watch IOS_APP_STORE profile}"
WATCHOS_CODE_SIGN_IDENTITY="${WATCHOS_CODE_SIGN_IDENTITY:-Apple Distribution}"

usage() {
  cat <<'EOF'
Usage: scripts/deploy-watchos.sh [--upload]

Archive the standalone Yaver watchOS app with App Store distribution signing.

Environment:
  WATCHOS_MARKETING_VERSION  Override MARKETING_VERSION for the archive.
  WATCHOS_BUILD_NUMBER       Override CURRENT_PROJECT_VERSION for the archive.
  WATCHOS_PROVISIONING_PROFILE_SPECIFIER
                            App Store profile name. Defaults to
                            "Yaver Watch IOS_APP_STORE profile".
  WATCHOS_CODE_SIGN_IDENTITY Signing identity. Defaults to "Apple Distribution".

Note:
  Xcode 17 currently refuses App Store export for this standalone watch archive
  and only offers release-testing, enterprise, and debugging export methods.
  Real App Store submission needs the watch app embedded in the iOS archive or
  a manually created App Store Connect watch app flow that Apple accepts.
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

if ! xcodebuild -showsdks | grep -q "watchos"; then
  echo "ERROR: Xcode watchOS SDK is not installed. Install the watchOS platform component in Xcode, then retry." >&2
  exit 1
fi

if [ ! -d "$WATCH_DIR/YaverWatch.xcodeproj" ]; then
  if ! command -v xcodegen >/dev/null 2>&1; then
    echo "ERROR: watch/YaverWatch.xcodeproj is missing and xcodegen is not installed." >&2
    exit 1
  fi
  (cd "$WATCH_DIR" && xcodegen generate)
fi

if [ -f "$HOME/.appstoreconnect/yaver.env" ]; then
  set -a; source "$HOME/.appstoreconnect/yaver.env"; set +a
fi
APPLE_TEAM_ID="${APPLE_TEAM_ID:?APPLE_TEAM_ID unset}"

EXTRA_SETTINGS=()
if [ -n "$MARKETING_VERSION" ]; then
  EXTRA_SETTINGS+=(MARKETING_VERSION="$MARKETING_VERSION")
fi
if [ -n "$BUILD_NUMBER" ]; then
  EXTRA_SETTINGS+=(CURRENT_PROJECT_VERSION="$BUILD_NUMBER")
fi

ls -la "$ARCHIVE_PATH" "$DERIVED_DATA_PATH" 2>/dev/null || true
rm -rf "$ARCHIVE_PATH"

xcodebuild -project "$WATCH_DIR/YaverWatch.xcodeproj" \
  -scheme "$SCHEME" \
  -configuration "$CONFIGURATION" \
  -sdk watchos \
  -destination "generic/platform=watchOS" \
  -archivePath "$ARCHIVE_PATH" \
  -derivedDataPath "$DERIVED_DATA_PATH" \
  DEVELOPMENT_TEAM="$APPLE_TEAM_ID" \
  CODE_SIGN_STYLE=Manual \
  CODE_SIGN_IDENTITY="$WATCHOS_CODE_SIGN_IDENTITY" \
  PROVISIONING_PROFILE_SPECIFIER="$WATCHOS_PROVISIONING_PROFILE_SPECIFIER" \
  SKIP_INSTALL=NO \
  ${EXTRA_SETTINGS[@]+"${EXTRA_SETTINGS[@]}"} \
  archive

echo "watchOS archive ready: $ARCHIVE_PATH"

if [ "$UPLOAD" = "1" ]; then
  echo "ERROR: standalone watchOS upload is blocked by Xcode export method support on this project." >&2
  echo "Xcode accepts only release-testing/enterprise/debugging for this archive, not app-store-connect." >&2
  echo "Embed the watch app in the iOS archive or complete an Apple-accepted watch App Store flow first." >&2
  exit 2
fi
