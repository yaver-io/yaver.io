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
WATCHOS_EXPORT_METHOD="${WATCHOS_EXPORT_METHOD:-release-testing}"
WATCHOS_EXPORT_DESTINATION="${WATCHOS_EXPORT_DESTINATION:-export}"
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
  WATCHOS_EXPORT_METHOD
                      Export method for xcodebuild. Defaults to
                      "release-testing"; Xcode 17 rejects app-store-connect
                      for standalone watchOS archives.
  WATCHOS_EXPORT_DESTINATION
                      Export destination. Defaults to "export".

Options:
  --upload            Archive and export a signed build. Does NOT upload:
                      YaverWatch is a companion with no App Store record of
                      its own and ships inside the iPhone build. Use
                      scripts/deploy-testflight.sh to get it to users.
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

if command -v xcodegen >/dev/null 2>&1; then
  (cd "$WATCH_DIR" && xcodegen generate)
elif [ ! -d "$WATCH_DIR/YaverWatch.xcodeproj" ]; then
  echo "ERROR: watch/YaverWatch.xcodeproj is missing and xcodegen is not installed." >&2
  exit 1
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
"    <key>method</key><string>${WATCHOS_EXPORT_METHOD}</string>" \
"    <key>teamID</key><string>${APPLE_TEAM_ID}</string>" \
"    <key>signingStyle</key><string>${EXPORT_SIGNING_STYLE}</string>" \
"    <key>destination</key><string>${WATCHOS_EXPORT_DESTINATION}</string>" \
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

if [ "$WATCHOS_EXPORT_DESTINATION" = "upload" ]; then
  echo "watchOS upload submitted from $ARCHIVE_PATH"
  exit 0
fi

IPA_PATH="$(find "$EXPORT_PATH" -maxdepth 2 -name '*.ipa' -print -quit)"
if [ -z "$IPA_PATH" ]; then
  echo "ERROR: expected exported watchOS IPA under $EXPORT_PATH" >&2
  exit 1
fi

# There is no watch-only channel for this app, so there is nothing for altool
# to upload to. YaverWatch is a COMPANION: scripts/add-watch-ios-target.js
# embeds it in the iPhone app and it ships inside the TestFlight build at
# Yaver.app/Watch/Yaver.app (bundle io.yaver.mobile.watch, nested under
# io.yaver.mobile). See watch/README.md.
#
# altool only accepts {macos | ios | appletvos | visionos} — there is no
# "watchos" — which is the tool telling us the same thing. Forcing
# --platform ios here would push a companion binary as a standalone app.
#
# This used to archive for ~5 minutes and then die on "Cannot determine the
# platform", which reads like a flag that needs fixing rather than a channel
# that does not exist. Say so before the archive instead.
cat >&2 <<EOF
watchOS build + export succeeded: $IPA_PATH

NOT uploading: the watch app has no App Store record of its own. It reaches
users embedded in the iPhone build — run scripts/deploy-testflight.sh, which
invokes add-watch-ios-target.js and archives the companion inside Yaver.app.

Verify a TestFlight build carried it with:
  ls /tmp/Yaver.xcarchive/Products/Applications/Yaver.app/Watch/
EOF
