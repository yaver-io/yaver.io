#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TVOS_DIR="$ROOT/tvos"
UPLOAD=0
CONFIGURATION="${CONFIGURATION:-Release}"
SCHEME="${SCHEME:-YaverTV}"
ARCHIVE_PATH="${ARCHIVE_PATH:-/tmp/YaverTV.xcarchive}"
EXPORT_PATH="${EXPORT_PATH:-/tmp/YaverTVExport}"
DERIVED_DATA_PATH="${DERIVED_DATA_PATH:-/tmp/YaverTVBuild}"
MARKETING_VERSION="${TVOS_MARKETING_VERSION:-}"
BUILD_NUMBER="${TVOS_BUILD_NUMBER:-}"

usage() {
  cat <<'EOF'
Usage: scripts/deploy-tvos.sh [--upload]

Build the standalone Yaver tvOS app. With --upload, archive and upload to App
Store Connect using the same APP_STORE_KEY_* / APPLE_TEAM_ID environment as the
iOS TestFlight script.

Environment:
  TVOS_MARKETING_VERSION  Override MARKETING_VERSION for the archive.
  TVOS_BUILD_NUMBER       Override CURRENT_PROJECT_VERSION for the archive.
  TVOS_PROVISIONING_PROFILE_SPECIFIER
                           App Store profile name for manual upload signing.
                           Defaults to "Yaver TVOS_APP_STORE profile".
                           Set to empty to use automatic signing.
  TVOS_CODE_SIGN_IDENTITY  Signing identity for manual upload signing.
                           Defaults to "Apple Distribution".
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

if ! xcodebuild -showsdks | grep -q "appletvos"; then
  echo "ERROR: Xcode tvOS SDK is not installed. Install the tvOS platform component in Xcode, then retry." >&2
  exit 1
fi

if [ ! -d "$TVOS_DIR/YaverTV.xcodeproj" ]; then
  if ! command -v xcodegen >/dev/null 2>&1; then
    echo "ERROR: tvos/YaverTV.xcodeproj is missing and xcodegen is not installed." >&2
    exit 1
  fi
  (cd "$TVOS_DIR" && xcodegen generate)
fi

EXTRA_SETTINGS=()
if [ -n "$MARKETING_VERSION" ]; then
  EXTRA_SETTINGS+=(MARKETING_VERSION="$MARKETING_VERSION")
fi
if [ -n "$BUILD_NUMBER" ]; then
  EXTRA_SETTINGS+=(CURRENT_PROJECT_VERSION="$BUILD_NUMBER")
fi

if [ "$UPLOAD" != "1" ]; then
  xcodebuild -project "$TVOS_DIR/YaverTV.xcodeproj" \
    -scheme "$SCHEME" \
    -configuration "$CONFIGURATION" \
    -sdk appletvos \
    -destination "generic/platform=tvOS" \
    -derivedDataPath "$DERIVED_DATA_PATH" \
    CODE_SIGNING_ALLOWED=NO \
    ${EXTRA_SETTINGS[@]+"${EXTRA_SETTINGS[@]}"} \
    build
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
TVOS_PROVISIONING_PROFILE_SPECIFIER="${TVOS_PROVISIONING_PROFILE_SPECIFIER-Yaver TVOS_APP_STORE profile}"
TVOS_CODE_SIGN_IDENTITY="${TVOS_CODE_SIGN_IDENTITY:-Apple Distribution}"

ls -la "$ARCHIVE_PATH" "$EXPORT_PATH" "$DERIVED_DATA_PATH" 2>/dev/null || true
rm -rf "$ARCHIVE_PATH" "$EXPORT_PATH"

SIGNING_SETTINGS=(DEVELOPMENT_TEAM="$APPLE_TEAM_ID")
EXPORT_SIGNING_STYLE="automatic"
ALLOW_PROVISIONING_UPDATES=(-allowProvisioningUpdates)
if [ -n "$TVOS_PROVISIONING_PROFILE_SPECIFIER" ]; then
  SIGNING_SETTINGS+=(
    CODE_SIGN_STYLE=Manual
    CODE_SIGN_IDENTITY="$TVOS_CODE_SIGN_IDENTITY"
    PROVISIONING_PROFILE_SPECIFIER="$TVOS_PROVISIONING_PROFILE_SPECIFIER"
  )
  EXPORT_SIGNING_STYLE="manual"
  ALLOW_PROVISIONING_UPDATES=()
else
  SIGNING_SETTINGS+=(CODE_SIGN_STYLE=Automatic)
fi

xcodebuild -project "$TVOS_DIR/YaverTV.xcodeproj" \
  -scheme "$SCHEME" \
  -configuration "$CONFIGURATION" \
  -sdk appletvos \
  -destination "generic/platform=tvOS" \
  -archivePath "$ARCHIVE_PATH" \
  -derivedDataPath "$DERIVED_DATA_PATH" \
  "${SIGNING_SETTINGS[@]}" \
  ${ALLOW_PROVISIONING_UPDATES[@]+"${ALLOW_PROVISIONING_UPDATES[@]}"} \
  -authenticationKeyPath "$AUTH_KEY" \
  -authenticationKeyID "$AUTH_KEY_ID" \
  -authenticationKeyIssuerID "$AUTH_KEY_ISSUER" \
  ${EXTRA_SETTINGS[@]+"${EXTRA_SETTINGS[@]}"} \
  archive

EXPORT_OPTIONS="$(mktemp /tmp/YaverTVExportOptions.plist.XXXXXX)"
if [ -n "$TVOS_PROVISIONING_PROFILE_SPECIFIER" ]; then
  PROVISIONING_PROFILES_XML="
    <key>provisioningProfiles</key>
    <dict>
        <key>io.yaver.mobile</key><string>${TVOS_PROVISIONING_PROFILE_SPECIFIER}</string>
    </dict>"
else
  PROVISIONING_PROFILES_XML=""
fi
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
"${PROVISIONING_PROFILES_XML}" \
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

echo "tvOS upload submitted from $ARCHIVE_PATH"
