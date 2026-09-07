#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TVOS_DIR="$ROOT/tvos"
# shellcheck source=scripts/apple-xcode-auth.sh
. "$ROOT/scripts/apple-xcode-auth.sh"
"$ROOT/scripts/check-no-native-payment-sdks.sh" source
UPLOAD=0
DEVICE_MODE=0
SIM_MODE=0
CONFIGURATION="${CONFIGURATION:-Release}"
SCHEME="${SCHEME:-YaverTV}"
ARCHIVE_PATH="${ARCHIVE_PATH:-/tmp/YaverTV.xcarchive}"
EXPORT_PATH="${EXPORT_PATH:-/tmp/YaverTVExport}"
DERIVED_DATA_PATH="${DERIVED_DATA_PATH:-/tmp/YaverTVBuild}"
MARKETING_VERSION="${TVOS_MARKETING_VERSION:-}"
BUILD_NUMBER="${TVOS_BUILD_NUMBER:-}"

usage() {
  cat <<'EOF'
Usage: scripts/deploy-tvos.sh [--upload] [--device [UDID]]

Build the standalone Yaver tvOS app.
  --upload            archive + upload to App Store Connect (TestFlight).
  --device [UDID]     build with automatic DEV signing + install straight to a
                      network-paired Apple TV via devicectl (no TestFlight).
                      UDID optional: uses the first paired Apple TV when omitted.
                      This is the fast iterate loop — minutes, not an hour.
  --simulator         build for the booted Apple TV simulator + install + launch
                      (xcrun simctl). THE hot-reload-grade loop: same app, same
                      agent connection, ~1-2 min per iteration, no TestFlight,
                      no device pairing. Hardware-only bits (Siri Remote
                      dictation) still need a real TV.

Environment:
  TVOS_MARKETING_VERSION  Override MARKETING_VERSION for the archive.
  TVOS_BUILD_NUMBER       Override CURRENT_PROJECT_VERSION for the archive.
  TVOS_PROVISIONING_PROFILE_SPECIFIER
                           App Store profile name for manual upload signing.
                           Empty (the default) uses automatic signing.
  TVOS_CODE_SIGN_IDENTITY  Signing identity for manual upload signing.
                           Defaults to "Apple Distribution".
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --upload) UPLOAD=1; shift ;;
    --device) DEVICE_MODE=1; DEVICE_UDID=""; shift; [ $# -gt 0 ] && case "$1" in --*) ;; *) DEVICE_UDID="$1"; shift ;; esac ;;
    --simulator) SIM_MODE=1; shift ;;
    --help|-h) usage; exit 0 ;;
    *) echo "Unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
done

apple_require_working_xcode
apple_ensure_simulator_runtime tvOS appletvsimulator

if ! command -v xcodebuild >/dev/null 2>&1; then
  echo "ERROR: xcodebuild is missing. Install full Xcode, then retry." >&2
  exit 1
fi

XCODEBUILD_PATH="$(xcrun -find xcodebuild 2>/dev/null || true)"
case "$XCODEBUILD_PATH" in
  */Xcode*.app/Contents/Developer/usr/bin/xcodebuild) ;;
  *)
    echo "ERROR: xcodebuild is not the full Xcode toolchain (${XCODEBUILD_PATH:-not found}). Install/select Xcode.app with: sudo xcode-select -s /Applications/Xcode.app/Contents/Developer" >&2
    exit 1
    ;;
esac

if ! xcodebuild -showsdks | grep -q "appletvos"; then
  echo "ERROR: Xcode tvOS SDK is not installed. Install the tvOS platform component in Xcode, then retry." >&2
  exit 1
fi

# ALWAYS regenerate the project from project.yml, never reuse the on-disk
# .xcodeproj. It is gitignored and drifts from the source tree: a new
# Views/*.swift added since the last generate is silently NOT compiled, which
# surfaced as `cannot find 'VibeTurnPanel' in scope` (2026-07-28) — the file
# existed on disk but wasn't in the stale project's compile sources. A
# conditional "only if missing" regenerate is the exact inventory-vs-operation
# trap: the project's presence is not proof it matches the sources.
if ! command -v xcodegen >/dev/null 2>&1; then
  echo "ERROR: xcodegen is required to generate tvos/YaverTV.xcodeproj from project.yml." >&2
  echo "Install: brew install xcodegen" >&2
  exit 1
fi
(cd "$TVOS_DIR" && xcodegen generate)

EXTRA_SETTINGS=()
# tvOS uses public Swift packages. In a headless session Xcode's default
# keychain provider can block forever asking Security.framework for an
# irrelevant GitHub credential before it prints any archive output. Force the
# non-interactive provider and system Git for every build lane.
PACKAGE_AUTH_SETTINGS=(-packageAuthorizationProvider netrc -scmProvider system)
if [ -n "$MARKETING_VERSION" ]; then
  EXTRA_SETTINGS+=(MARKETING_VERSION="$MARKETING_VERSION")
fi
if [ -n "$BUILD_NUMBER" ]; then
  EXTRA_SETTINGS+=(CURRENT_PROJECT_VERSION="$BUILD_NUMBER")
fi

if [ "$SIM_MODE" = "1" ]; then
  # Hot-reload-grade loop (2026-08-13): build for the Apple TV simulator and
  # install + launch on the booted sim. Same app, same agent connection
  # (localhost/relay) as the real TV — everything except Siri-Remote dictation
  # and hardware video decode is testable here, in ~1-2 min per iteration.
  SIM_TARGET="${TVOS_SIM_UDID:-booted}"
  xcodebuild -project "$TVOS_DIR/YaverTV.xcodeproj" \
    -scheme "$SCHEME" \
    -configuration "Debug" \
    -sdk appletvsimulator \
    -destination "platform=tvOS Simulator,id=$SIM_TARGET" \
    -derivedDataPath "$DERIVED_DATA_PATH" \
    "${PACKAGE_AUTH_SETTINGS[@]}" \
    CODE_SIGNING_ALLOWED=NO \
    ${EXTRA_SETTINGS[@]+"${EXTRA_SETTINGS[@]}"} \
    build

  APP_PATH="$DERIVED_DATA_PATH/Build/Products/Debug-appletvsimulator/Yaver.app"
  if [ ! -d "$APP_PATH" ]; then
    echo "ERROR: simulator app not found at $APP_PATH" >&2
    exit 1
  fi
  echo "Installing to Apple TV simulator ($SIM_TARGET) …"
  xcrun simctl install "$SIM_TARGET" "$APP_PATH"
  xcrun simctl launch "$SIM_TARGET" io.yaver.mobile
  echo "Installed + launched on the simulator from $APP_PATH"
  exit 0
fi

if [ "$DEVICE_MODE" = "1" ]; then
  # Fast iterate loop (2026-08-13): build with AUTOMATIC development signing
  # and install straight to a network-paired Apple TV via devicectl — no
  # TestFlight processing wait. Prereq: pair the Apple TV once in Xcode
  # (Window > Devices and Simulators > "+" > pick the Apple TV on the LAN);
  # Xcode registers its UDID and provisions the dev profile automatically
  # (-allowProvisioningUpdates). ~2-4 min per iteration vs an hour for
  # TestFlight.
  if [ -f "$HOME/.appstoreconnect/yaver.env" ]; then
    set -a; source "$HOME/.appstoreconnect/yaver.env"; set +a
  fi
  # Device installs use automatic development signing. They must authenticate
  # against the same Apple account/API key as the archive lane and must be
  # allowed to register the newly paired Apple TV before Xcode can create the
  # tvOS development profile. Without these flags pairing succeeds, but the
  # build fails with "No profiles for ... were found" (2026-08-19).
  apple_configure_xcode_auth
  APPLE_TEAM_ID="${APPLE_TEAM_ID:-5SJZ4KA39A}"

  xcodebuild -project "$TVOS_DIR/YaverTV.xcodeproj" \
    -scheme "$SCHEME" \
    -configuration "Debug" \
    -sdk appletvos \
    -destination "generic/platform=tvOS" \
    -derivedDataPath "$DERIVED_DATA_PATH" \
    "${PACKAGE_AUTH_SETTINGS[@]}" \
    CODE_SIGN_STYLE=Automatic \
    DEVELOPMENT_TEAM="$APPLE_TEAM_ID" \
    -allowProvisioningUpdates \
    -allowProvisioningDeviceRegistration \
    ${APPLE_XCODE_AUTH_ARGS[@]+"${APPLE_XCODE_AUTH_ARGS[@]}"} \
    ${EXTRA_SETTINGS[@]+"${EXTRA_SETTINGS[@]}"} \
    build 2>&1 | apple_redact_xcode_auth_output

  APP_PATH="$DERIVED_DATA_PATH/Build/Products/Debug-appletvos/Yaver.app"
  if [ ! -d "$APP_PATH" ]; then
    echo "ERROR: physical-device app not found at $APP_PATH" >&2
    exit 1
  fi

  # Resolve the target Apple TV: explicit UDID, else the first network Apple TV.
  TARGET_UDID="$DEVICE_UDID"
  if [ -z "$TARGET_UDID" ]; then
    TARGET_UDID="$(xcrun devicectl list devices 2>/dev/null | awk -F'   +' '$2 ~ /\.coredevice\.local/ { print $3; exit }')"
  fi
  if [ -z "$TARGET_UDID" ]; then
    echo "ERROR: no network Apple TV found. Pair it in Xcode (Window > Devices and Simulators > +), then retry." >&2
    exit 1
  fi
  echo "Installing to Apple TV $TARGET_UDID …"
  xcrun devicectl device install app --device "$TARGET_UDID" "$APP_PATH"
  xcrun devicectl device process launch --device "$TARGET_UDID" io.yaver.mobile
  echo "Installed + launched on $TARGET_UDID from $APP_PATH"
  exit 0
fi

if [ "$UPLOAD" != "1" ]; then
  xcodebuild -project "$TVOS_DIR/YaverTV.xcodeproj" \
    -scheme "$SCHEME" \
    -configuration "$CONFIGURATION" \
    -sdk appletvos \
    -destination "generic/platform=tvOS" \
    -derivedDataPath "$DERIVED_DATA_PATH" \
    "${PACKAGE_AUTH_SETTINGS[@]}" \
    CODE_SIGNING_ALLOWED=NO \
    ${EXTRA_SETTINGS[@]+"${EXTRA_SETTINGS[@]}"} \
    build
  exit 0
fi

if [ -f "$HOME/.appstoreconnect/yaver.env" ]; then
  set -a; source "$HOME/.appstoreconnect/yaver.env"; set +a
fi

apple_resolve_team_id "$TVOS_DIR/project.yml"
apple_configure_xcode_auth

# Automatic signing is the safe default: enabling a capability invalidates old
# named profiles, while -allowProvisioningUpdates lets Xcode regenerate them.
# Set a profile name explicitly only for a deliberately manual signing lane.
TVOS_PROVISIONING_PROFILE_SPECIFIER="${TVOS_PROVISIONING_PROFILE_SPECIFIER:-}"
TVOS_CODE_SIGN_IDENTITY="${TVOS_CODE_SIGN_IDENTITY:-Apple Distribution}"

# Build number. Without this, CURRENT_PROJECT_VERSION comes from project.yml,
# where it is the literal "1" — so an --upload run archives for minutes and is
# then REJECTED as a duplicate build number, burning a slot of the ~15-20/day
# TestFlight cap. Bump from max(ASC, local) + 1 like deploy-testflight.sh.
# TVOS_BUILD_NUMBER still wins when set explicitly.
if [ -z "$BUILD_NUMBER" ]; then
  apple_require_explicit_build_without_api_key TVOS_BUILD_NUMBER "$BUILD_NUMBER"
  # shellcheck source=scripts/asc-next-build.sh
  . "$ROOT/scripts/asc-next-build.sh"
  TVOS_LOCAL_BUILD="$(sed -n 's/.*CURRENT_PROJECT_VERSION: *"\{0,1\}\([0-9][0-9]*\)"\{0,1\}.*/\1/p' "$TVOS_DIR/project.yml" | head -1)"
  # project.yml intentionally carries only a scaffold build number. If ASC is
  # temporarily unreadable, local+1 is guaranteed to regress and be rejected;
  # fail before archiving instead of spending minutes and an upload attempt.
  if ! BUILD_NUMBER="$(asc_next_build TV_OS "${TVOS_LOCAL_BUILD:-0}" require_remote)"; then
    echo "ERROR: cannot safely choose the next tvOS build number. Retry when App Store Connect responds, or set TVOS_BUILD_NUMBER explicitly from a verified ASC maximum." >&2
    exit 75
  fi
  echo "tvOS build number: $BUILD_NUMBER"
  EXTRA_SETTINGS+=(CURRENT_PROJECT_VERSION="$BUILD_NUMBER")
fi
apple_validate_build_number TVOS_BUILD_NUMBER "$BUILD_NUMBER"

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

TVOS_ARCHIVE_LOG="${TVOS_ARCHIVE_LOG:-/tmp/yaver_tvos_archive.log}"
set +e
xcodebuild -project "$TVOS_DIR/YaverTV.xcodeproj" \
  -scheme "$SCHEME" \
  -configuration "$CONFIGURATION" \
  -sdk appletvos \
  -destination "generic/platform=tvOS" \
  -archivePath "$ARCHIVE_PATH" \
  -derivedDataPath "$DERIVED_DATA_PATH" \
  "${PACKAGE_AUTH_SETTINGS[@]}" \
  "${SIGNING_SETTINGS[@]}" \
  ${ALLOW_PROVISIONING_UPDATES[@]+"${ALLOW_PROVISIONING_UPDATES[@]}"} \
  ${APPLE_XCODE_AUTH_ARGS[@]+"${APPLE_XCODE_AUTH_ARGS[@]}"} \
  ${EXTRA_SETTINGS[@]+"${EXTRA_SETTINGS[@]}"} \
  archive 2>&1 | apple_redact_xcode_auth_output | tee "$TVOS_ARCHIVE_LOG"
TVOS_ARCHIVE_EXIT=${PIPESTATUS[0]}
set -e
if [ "$TVOS_ARCHIVE_EXIT" -ne 0 ]; then
  if grep -qiE 'team has no devices|No profiles.*App Development' "$TVOS_ARCHIVE_LOG"; then
    echo "ERROR: Xcode requested a tvOS development profile, but this is a TestFlight upload." >&2
    echo "       A physical Apple TV is not required. Create/download a tvOS App Store" >&2
    echo "       provisioning profile for io.yaver.mobile, then retry with:" >&2
    echo "         TVOS_PROVISIONING_PROFILE_SPECIFIER='<profile name>' ./deploy/deploy.sh tvos" >&2
  fi
  exit "$TVOS_ARCHIVE_EXIT"
fi

EXPORT_OPTIONS="$(mktemp /tmp/YaverTVExportOptions.plist.XXXXXX)"
EXPORT_DESTINATION="upload"
# Xcode's `destination=upload` can still consult the Apple ID account token
# even when a complete App Store Connect API key was supplied. On 2026-08-18
# it archived + signed successfully, then failed with "Account credentials have
# expired". Exporting the immutable IPA locally and handing it to altool keeps
# the API-key lane API-key-only, like the working macOS TestFlight path.
if [ "$APPLE_XCODE_AUTH_MODE" = "api-key" ]; then
  EXPORT_DESTINATION="export"
fi
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
"    <key>destination</key><string>${EXPORT_DESTINATION}</string>" \
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
  ${APPLE_XCODE_AUTH_ARGS[@]+"${APPLE_XCODE_AUTH_ARGS[@]}"} \
  2>&1 | apple_redact_xcode_auth_output

if [ "$APPLE_XCODE_AUTH_MODE" = "api-key" ]; then
  IPA_PATH="$(find "$EXPORT_PATH" -maxdepth 1 -type f -name '*.ipa' -print -quit)"
  if [ -z "$IPA_PATH" ]; then
    echo "ERROR: Xcode export succeeded but produced no tvOS IPA under $EXPORT_PATH." >&2
    exit 1
  fi

  # altool discovers API keys by filename under ./private_keys. Keep that
  # compatibility directory owner-only and ephemeral; never copy a private key
  # into the repository or a persistent shared location.
  UPLOAD_AUTH_DIR="$(mktemp -d /tmp/yaver-tvos-asc.XXXXXX)"
  chmod 700 "$UPLOAD_AUTH_DIR"
  mkdir -m 700 "$UPLOAD_AUTH_DIR/private_keys"
  cp "$APP_STORE_KEY_PATH" "$UPLOAD_AUTH_DIR/private_keys/AuthKey_${APP_STORE_KEY_ID}.p8"
  chmod 600 "$UPLOAD_AUTH_DIR/private_keys/AuthKey_${APP_STORE_KEY_ID}.p8"
  cleanup_tvos_upload_auth() {
    find "$UPLOAD_AUTH_DIR" -depth -delete 2>/dev/null || true
  }
  trap cleanup_tvos_upload_auth EXIT

  echo "Validating tvOS IPA with App Store Connect…"
  (cd "$UPLOAD_AUTH_DIR" && xcrun altool --validate-app --file "$IPA_PATH" \
    --type appletvos --apiKey "$APP_STORE_KEY_ID" --apiIssuer "$APP_STORE_KEY_ISSUER")
  echo "Uploading tvOS IPA to TestFlight…"
  (cd "$UPLOAD_AUTH_DIR" && xcrun altool --upload-app --file "$IPA_PATH" \
    --type appletvos --apiKey "$APP_STORE_KEY_ID" --apiIssuer "$APP_STORE_KEY_ISSUER")
  echo "tvOS build $BUILD_NUMBER accepted by App Store Connect."
else
  echo "tvOS upload submitted from $ARCHIVE_PATH"
fi
