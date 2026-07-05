#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WATCH_DIR="$ROOT/watch"
CONFIGURATION="${CONFIGURATION:-Release}"
SCHEME="${SCHEME:-YaverWatch}"
ARCHIVE_PATH="${ARCHIVE_PATH:-/tmp/YaverWatch.xcarchive}"
EXPORT_PATH="${EXPORT_PATH:-/tmp/YaverWatchExport}"
DERIVED_DATA_PATH="${DERIVED_DATA_PATH:-/tmp/YaverWatchBuild}"
DESTINATION="${DESTINATION:-generic/platform=watchOS}"
MARKETING_VERSION="${WATCHOS_MARKETING_VERSION:-}"
BUILD_NUMBER="${WATCHOS_BUILD_NUMBER:-}"
WATCHOS_PROVISIONING_PROFILE_SPECIFIER="${WATCHOS_PROVISIONING_PROFILE_SPECIFIER:-}"
WATCHOS_CODE_SIGN_IDENTITY="${WATCHOS_CODE_SIGN_IDENTITY:-Apple Distribution}"
UPLOAD=0

usage() {
  cat <<'EOF'
Usage: scripts/deploy-watchos.sh [--upload]

Build, archive, and optionally upload the standalone Yaver watchOS app.
With --upload, xcodebuild exports directly to App Store Connect using the
same APP_STORE_KEY_* / APPLE_TEAM_ID environment as the iOS TestFlight script.

Environment:
  CONFIGURATION       Xcode configuration. Defaults to Release.
  DESTINATION         Xcode destination. Defaults to generic/platform=watchOS.
  DERIVED_DATA_PATH   Build output path. Defaults to /tmp/YaverWatchBuild.
  ARCHIVE_PATH        Archive path. Defaults to /tmp/YaverWatch.xcarchive.
  EXPORT_PATH         Export path. Defaults to /tmp/YaverWatchExport.
  WATCHOS_MARKETING_VERSION
                      Override MARKETING_VERSION for the archive.
  WATCHOS_BUILD_NUMBER
                      Override CURRENT_PROJECT_VERSION for the archive.
  WATCHOS_PROVISIONING_PROFILE_SPECIFIER
                      Optional App Store profile name for manual signing.
                      Empty means automatic signing.
  WATCHOS_CODE_SIGN_IDENTITY
                      Signing identity for manual signing. Defaults to
                      "Apple Distribution".

Options:
  --upload            Export and upload the archive to App Store Connect.
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

EXTRA_SETTINGS=()
if [ -n "$MARKETING_VERSION" ]; then
  EXTRA_SETTINGS+=(MARKETING_VERSION="$MARKETING_VERSION")
fi
if [ -n "$BUILD_NUMBER" ]; then
  EXTRA_SETTINGS+=(CURRENT_PROJECT_VERSION="$BUILD_NUMBER")
fi

if [ "$UPLOAD" != "1" ]; then
  xcodebuild -project "$WATCH_DIR/YaverWatch.xcodeproj" \
    -scheme "$SCHEME" \
    -configuration "$CONFIGURATION" \
    -destination "$DESTINATION" \
    -derivedDataPath "$DERIVED_DATA_PATH" \
    CODE_SIGNING_ALLOWED=NO \
    ${EXTRA_SETTINGS[@]+"${EXTRA_SETTINGS[@]}"} \
    build

  APP_PATH="$DERIVED_DATA_PATH/Build/Products/${CONFIGURATION}-watchos/Yaver.app"
  if [ ! -d "$APP_PATH" ]; then
    echo "ERROR: expected watchOS app not found: $APP_PATH" >&2
    exit 1
  fi

  echo "watchOS app ready: $APP_PATH"
  exit 0
fi

if command -v yaver >/dev/null 2>&1; then
  eval "$(yaver vault env --project mobile 2>/dev/null || true)"
fi
if [ -f "$HOME/.appstoreconnect/yaver.env" ]; then
  set -a; source "$HOME/.appstoreconnect/yaver.env"; set +a
fi

AUTH_KEY="${APP_STORE_KEY_PATH:?APP_STORE_KEY_PATH unset}"
AUTH_KEY_ID="${APP_STORE_KEY_ID:?APP_STORE_KEY_ID unset}"
AUTH_KEY_ISSUER="${APP_STORE_KEY_ISSUER:?APP_STORE_KEY_ISSUER unset}"
APPLE_TEAM_ID="${APPLE_TEAM_ID:?APPLE_TEAM_ID unset}"

ls -la "$ARCHIVE_PATH" "$EXPORT_PATH" "$DERIVED_DATA_PATH" 2>/dev/null || true
rm -rf "$ARCHIVE_PATH" "$EXPORT_PATH"

SIGNING_SETTINGS=(DEVELOPMENT_TEAM="$APPLE_TEAM_ID")
EXPORT_SIGNING_STYLE="automatic"
ALLOW_PROVISIONING_UPDATES=(-allowProvisioningUpdates)
if [ -n "$WATCHOS_PROVISIONING_PROFILE_SPECIFIER" ]; then
  SIGNING_SETTINGS+=(
    CODE_SIGN_STYLE=Manual
    CODE_SIGN_IDENTITY="$WATCHOS_CODE_SIGN_IDENTITY"
    PROVISIONING_PROFILE_SPECIFIER="$WATCHOS_PROVISIONING_PROFILE_SPECIFIER"
  )
  EXPORT_SIGNING_STYLE="manual"
  ALLOW_PROVISIONING_UPDATES=()
else
  SIGNING_SETTINGS+=(CODE_SIGN_STYLE=Automatic)
fi

xcodebuild -project "$WATCH_DIR/YaverWatch.xcodeproj" \
  -scheme "$SCHEME" \
  -configuration "$CONFIGURATION" \
  -sdk watchos \
  -destination "$DESTINATION" \
  -archivePath "$ARCHIVE_PATH" \
  -derivedDataPath "$DERIVED_DATA_PATH" \
  "${SIGNING_SETTINGS[@]}" \
  ${ALLOW_PROVISIONING_UPDATES[@]+"${ALLOW_PROVISIONING_UPDATES[@]}"} \
  -authenticationKeyPath "$AUTH_KEY" \
  -authenticationKeyID "$AUTH_KEY_ID" \
  -authenticationKeyIssuerID "$AUTH_KEY_ISSUER" \
  SKIP_INSTALL=NO \
  ${EXTRA_SETTINGS[@]+"${EXTRA_SETTINGS[@]}"} \
  archive

if [ ! -d "$ARCHIVE_PATH" ]; then
  echo "ERROR: expected watchOS archive not found: $ARCHIVE_PATH" >&2
  exit 1
fi

EXPORT_OPTIONS="$(mktemp /tmp/YaverWatchExportOptions.plist.XXXXXX)"
printf '%s\n' \
'<?xml version="1.0" encoding="UTF-8"?>' \
'<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' \
'<plist version="1.0">' \
'<dict>' \
'    <key>method</key><string>app-store-connect</string>' \
"    <key>teamID</key><string>${APPLE_TEAM_ID}</string>" \
"    <key>signingStyle</key><string>${EXPORT_SIGNING_STYLE}</string>" \
'    <key>destination</key><string>upload</string>' \
'    <key>uploadSymbols</key><false/>' \
'</dict>' \
'</plist>' > "$EXPORT_OPTIONS"
plutil -lint "$EXPORT_OPTIONS"

xcodebuild -exportArchive \
  -archivePath "$ARCHIVE_PATH" \
  -exportOptionsPlist "$EXPORT_OPTIONS" \
  -exportPath "$EXPORT_PATH" \
  ${ALLOW_PROVISIONING_UPDATES[@]+"${ALLOW_PROVISIONING_UPDATES[@]}"} \
  -authenticationKeyPath "$AUTH_KEY" \
  -authenticationKeyID "$AUTH_KEY_ID" \
  -authenticationKeyIssuerID "$AUTH_KEY_ISSUER"

echo "watchOS upload submitted from $ARCHIVE_PATH"
