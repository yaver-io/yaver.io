#!/bin/bash
set -euo pipefail

# Build the sandboxed Mac App Store variant of Yaver Desktop and optionally
# upload it to the macOS TestFlight train. The direct DMG is intentionally a
# different product lane: it embeds the Go agent; this MAS package does not.

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ELECTRON_DIR="$ROOT/electron"
# shellcheck source=scripts/apple-xcode-auth.sh
. "$ROOT/scripts/apple-xcode-auth.sh"
"$ROOT/scripts/check-no-native-payment-sdks.sh" source
MAS_BUNDLE_ID="io.yaver.mobile"
UPLOAD=0
DEV_BUILD=0

usage() {
  cat <<'EOF'
Usage: scripts/deploy-macos-testflight.sh [--upload] [--mas-dev]

  (no flag)  Build and locally verify the signed MAS .pkg; no external upload.
  --upload   Validate and upload the .pkg to App Store Connect / macOS TestFlight.
  --mas-dev  Build a locally runnable sandboxed development variant (no upload).

Required for MAS distribution:
  YAVER_MAS_PROVISIONING_PROFILE       io.yaver.mobile App Store profile path
  Mac App Distribution identity        installed, or CSC_LINK + CSC_KEY_PASSWORD
  Mac Installer Distribution identity  installed, or CSC_INSTALLER_LINK +
                                       CSC_INSTALLER_KEY_PASSWORD

Also required for --upload:
  APP_STORE_KEY_PATH, APP_STORE_KEY_ID, APP_STORE_KEY_ISSUER

Use the canonical entrypoint: ./deploy/deploy.sh desktop-mas (build only) or,
after explicit release approval, ./deploy/deploy.sh desktop-testflight.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --upload) UPLOAD=1 ;;
    --mas-dev) DEV_BUILD=1 ;;
    --help|-h) usage; exit 0 ;;
    *) echo "ERROR: unknown option '$1'." >&2; usage >&2; exit 2 ;;
  esac
  shift
done

if [ "$(uname -s)" != "Darwin" ]; then
  echo "ERROR: macOS TestFlight packaging requires a Mac with full Xcode." >&2
  exit 2
fi
apple_require_working_xcode
for tool in node npm xcrun codesign pkgutil plutil security; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "ERROR: required tool '$tool' is missing." >&2
    exit 2
  fi
done
XCODEBUILD_PATH="$(xcrun -find xcodebuild 2>/dev/null || true)"
if [ -z "$XCODEBUILD_PATH" ] || [[ "$XCODEBUILD_PATH" != *"/Xcode.app/"* ]]; then
  echo "ERROR: full Xcode is not selected. Fix: sudo xcode-select -s /Applications/Xcode.app/Contents/Developer" >&2
  exit 2
fi

if [ -f "$HOME/.appstoreconnect/yaver.env" ]; then
  set -a
  # shellcheck disable=SC1091
  source "$HOME/.appstoreconnect/yaver.env"
  set +a
fi

PROFILE_VAR="YAVER_MAS_PROVISIONING_PROFILE"
if [ "$DEV_BUILD" = "1" ]; then
  PROFILE_VAR="YAVER_MAS_DEV_PROVISIONING_PROFILE"
fi
PROFILE_PATH="${!PROFILE_VAR:-}"
if [ -z "$PROFILE_PATH" ] || [ ! -f "$PROFILE_PATH" ]; then
  echo "ERROR: $PROFILE_VAR must point to an existing $MAS_BUNDLE_ID provisioning profile." >&2
  echo "Create/download it in Apple Developer Certificates, Identifiers & Profiles, then retry." >&2
  exit 2
fi

# Decode the profile without touching the login keychain. This catches the
# expensive false-green where a profile file exists but belongs to another app;
# electron-builder would otherwise fail much later.
#
# Xcode-managed macOS Store profiles use
# `Entitlements.com.apple.application-identifier`; older/manual profiles can
# use `Entitlements.application-identifier`. Do not require App Sandbox in the
# profile itself: Xcode's valid "Mac Team Store Provisioning Profile" omits it
# and applies it to the signed app. The post-build codesign check below proves
# the capability at the layer Apple actually validates.
PROFILE_PLIST="$(mktemp -t yaver-mas-profile.XXXXXX)"
PROFILE_BUILD_COPY=""
VALIDATION_LOG=""
XCODE_PACKAGE_DIR=""
cleanup() {
  rm -f "$PROFILE_PLIST"
  if [ -n "$PROFILE_BUILD_COPY" ]; then
    rm -f "$PROFILE_BUILD_COPY"
  fi
  if [ -n "$VALIDATION_LOG" ]; then
    rm -f "$VALIDATION_LOG"
  fi
  if [ -n "$XCODE_PACKAGE_DIR" ] && [ -d "$XCODE_PACKAGE_DIR" ]; then
    case "$(basename "$XCODE_PACKAGE_DIR")" in
      yaver-mas-xcode.*)
        # The path came directly from mktemp above. Inspect the exact target
        # before removing the large temporary archive so failed builds do not
        # silently consume the user's SSD.
        ls -ld "$XCODE_PACKAGE_DIR" >/dev/null
        rm -rf -- "$XCODE_PACKAGE_DIR"
        ;;
    esac
  fi
}
trap cleanup EXIT
if ! security cms -D -i "$PROFILE_PATH" > "$PROFILE_PLIST" 2>/dev/null; then
  echo "ERROR: $PROFILE_VAR is not a readable Apple provisioning profile." >&2
  exit 2
fi
PROFILE_APP_ID=""
if PROFILE_APP_ID="$(plutil -extract Entitlements.application-identifier raw -o - "$PROFILE_PLIST" 2>/dev/null)"; then
  :
elif PROFILE_APP_ID="$(plutil -extract 'Entitlements.com\.apple\.application-identifier' raw -o - "$PROFILE_PLIST" 2>/dev/null)"; then
  :
else
  PROFILE_APP_ID=""
fi
if [[ "$PROFILE_APP_ID" != *."$MAS_BUNDLE_ID" ]]; then
  echo "ERROR: provisioning profile application-identifier does not end in .$MAS_BUNDLE_ID." >&2
  exit 2
fi

if [ "$UPLOAD" = "1" ]; then
  if [ -n "$(git -C "$ROOT" status --porcelain)" ]; then
    echo "ERROR: macOS TestFlight upload requires a clean committed worktree." >&2
    exit 2
  fi
  if [ "$(git -C "$ROOT" branch --show-current)" != "main" ]; then
    echo "ERROR: macOS TestFlight upload must run from reviewed main." >&2
    exit 2
  fi
  : "${APP_STORE_KEY_PATH:?Set APP_STORE_KEY_PATH to the App Store Connect .p8 key}"
  : "${APP_STORE_KEY_ID:?Set APP_STORE_KEY_ID}"
  : "${APP_STORE_KEY_ISSUER:?Set APP_STORE_KEY_ISSUER}"
  if [ ! -f "$APP_STORE_KEY_PATH" ]; then
    echo "ERROR: APP_STORE_KEY_PATH does not name an existing .p8 file." >&2
    exit 2
  fi
fi

# A sortable UTC timestamp is valid CFBundleVersion syntax and avoids reusing
# a local package build number. An operator can pin an ASC-derived value.
YAVER_MAC_BUILD_NUMBER="${YAVER_MAC_BUILD_NUMBER:-$(date -u +%Y%m%d%H%M%S)}"
export YAVER_MAC_BUILD_NUMBER
# Keep the durable provisioning profile owner-only. electron-builder preserves
# the source mode when embedding it in the app, and a 0600 embedded profile
# makes App Store Connect reject the otherwise valid package because ordinary
# users cannot verify its signature. Stage only this ephemeral build copy with
# the mode required inside an application bundle.
PROFILE_BUILD_COPY="$(mktemp -t yaver-mas-build-profile.XXXXXX)"
cp "$PROFILE_PATH" "$PROFILE_BUILD_COPY"
chmod 0644 "$PROFILE_BUILD_COPY"
export "$PROFILE_VAR=$PROFILE_BUILD_COPY"

BUILD_LABEL="App Store"
if [ "$DEV_BUILD" = "1" ]; then
  BUILD_LABEL="sandbox development"
fi
echo "Building Yaver macOS ${BUILD_LABEL} package ${YAVER_MAC_BUILD_NUMBER} (client-only; embedded agent excluded)…"
# A previous failed exporter can leave a valid package for an older build in
# dist-mas. electron-builder does not clear it, and `find ... '*.pkg'` would
# select that stale package after signing the new app. Start from a measured,
# exact output directory so package identity can never drift across attempts.
DIST_MAS_DIR="$ELECTRON_DIR/dist-mas"
if [ -d "$DIST_MAS_DIR" ]; then
  ls -ld "$DIST_MAS_DIR" >/dev/null
  find "$DIST_MAS_DIR" -depth -delete
fi
(cd "$ELECTRON_DIR" && npm ci)
if [ "$DEV_BUILD" = "1" ]; then
  (cd "$ELECTRON_DIR" && npm run dist:mas-dev)
else
  # electron-builder still searches for the legacy local "3rd Party Mac
  # Developer Installer" identity. Xcode 16 can instead issue a cloud-managed
  # installer certificate at export time. Preserve a fully signed app when the
  # final productbuild step is the only failure; the capability checks below
  # decide whether Xcode may package it safely.
  BUILDER_EXIT=0
  (cd "$ELECTRON_DIR" && npm run dist:mas) || BUILDER_EXIT=$?
  if [ "$BUILDER_EXIT" != "0" ]; then
    echo "electron-builder did not produce the installer; checking whether its signed app can use Xcode's Store exporter…" >&2
  fi
fi

APP_PATH="$(find "$ELECTRON_DIR/dist-mas" -type d -name 'Yaver.app' -print -quit)"
if [ -z "$APP_PATH" ]; then
  echo "ERROR: electron-builder reported success but no Yaver.app exists under electron/dist-mas." >&2
  exit 1
fi
codesign --verify --deep --strict --verbose=2 "$APP_PATH"
if [ -e "$APP_PATH/Contents/Resources/bin/yaver" ] || [ -e "$APP_PATH/Contents/Resources/bin/yaver.exe" ]; then
  echo "ERROR: MAS package contains the embedded agent; refusing a dishonest sandboxed build." >&2
  exit 1
fi
SIGNED_ENTITLEMENTS="$(codesign -d --entitlements :- "$APP_PATH" 2>/dev/null || true)"
if [[ "$SIGNED_ENTITLEMENTS" != *"com.apple.security.app-sandbox"* ]]; then
  echo "ERROR: signed app lacks the mandatory App Sandbox entitlement." >&2
  exit 1
fi

if [ "$DEV_BUILD" = "1" ]; then
  echo "Verified local MAS development app: $APP_PATH"
  exit 0
fi

PKG_PATH="$(find "$ELECTRON_DIR/dist-mas" -maxdepth 2 -type f -name '*.pkg' -print -quit)"
if [ -z "$PKG_PATH" ]; then
  # Xcode's exporter is the supported fallback for cloud-managed Mac Installer
  # Distribution assets. Build a minimal xcarchive around the already verified
  # Electron app. This does not build or mutate any iOS/tvOS/visionOS target.
  APP_BUNDLE_ID="$(plutil -extract CFBundleIdentifier raw -o - "$APP_PATH/Contents/Info.plist" 2>/dev/null || true)"
  APP_VERSION="$(plutil -extract CFBundleShortVersionString raw -o - "$APP_PATH/Contents/Info.plist" 2>/dev/null || true)"
  APP_BUILD="$(plutil -extract CFBundleVersion raw -o - "$APP_PATH/Contents/Info.plist" 2>/dev/null || true)"
  APP_ARCHES="$(lipo -archs "$APP_PATH/Contents/MacOS/Yaver" 2>/dev/null || true)"
  APP_TEAM="$(codesign -dv --verbose=4 "$APP_PATH" 2>&1 | sed -n 's/^TeamIdentifier=//p' | head -1)"
  APP_SIGNING_IDENTITY="$(codesign -dv --verbose=4 "$APP_PATH" 2>&1 | sed -n 's/^Authority=//p' | head -1)"
  if [ "$APP_BUNDLE_ID" != "$MAS_BUNDLE_ID" ] || [ -z "$APP_VERSION" ] || [ -z "$APP_BUILD" ]; then
    echo "ERROR: refusing Xcode packaging because the signed app identity/version is incomplete." >&2
    exit 1
  fi
  if [[ " $APP_ARCHES " != *" arm64 "* ]] || [[ " $APP_ARCHES " != *" x86_64 "* ]]; then
    echo "ERROR: refusing Xcode packaging because the Store app is not universal (architectures: ${APP_ARCHES:-none})." >&2
    exit 1
  fi
  if [ -z "$APP_TEAM" ] || [ -z "$APP_SIGNING_IDENTITY" ]; then
    echo "ERROR: refusing Xcode packaging because the app's signing team/identity could not be read." >&2
    exit 1
  fi

  XCODE_PACKAGE_DIR="$(mktemp -d -t yaver-mas-xcode.XXXXXX)"
  ARCHIVE_PATH="$XCODE_PACKAGE_DIR/Yaver.xcarchive"
  EXPORT_PATH="$XCODE_PACKAGE_DIR/export"
  EXPORT_OPTIONS="$XCODE_PACKAGE_DIR/ExportOptions.plist"
  mkdir -p "$ARCHIVE_PATH/Products/Applications"
  ditto "$APP_PATH" "$ARCHIVE_PATH/Products/Applications/Yaver.app"

  plutil -create xml1 "$ARCHIVE_PATH/Info.plist"
  plutil -insert ArchiveVersion -integer 2 "$ARCHIVE_PATH/Info.plist"
  plutil -insert Name -string Yaver "$ARCHIVE_PATH/Info.plist"
  plutil -insert SchemeName -string Yaver "$ARCHIVE_PATH/Info.plist"
  plutil -insert ApplicationProperties -dictionary "$ARCHIVE_PATH/Info.plist"
  plutil -insert ApplicationProperties.ApplicationPath -string Applications/Yaver.app "$ARCHIVE_PATH/Info.plist"
  plutil -insert ApplicationProperties.Architectures -json '["arm64","x86_64"]' "$ARCHIVE_PATH/Info.plist"
  plutil -insert ApplicationProperties.CFBundleIdentifier -string "$APP_BUNDLE_ID" "$ARCHIVE_PATH/Info.plist"
  # Xcode's upload path reads version/build from xcarchive metadata, not only
  # from the packaged app. Missing keys make Xcode 16 report both values empty
  # and then crash while recording the failed distribution.
  plutil -insert ApplicationProperties.CFBundleShortVersionString -string "$APP_VERSION" "$ARCHIVE_PATH/Info.plist"
  plutil -insert ApplicationProperties.CFBundleVersion -string "$APP_BUILD" "$ARCHIVE_PATH/Info.plist"
  plutil -insert ApplicationProperties.SigningIdentity -string "$APP_SIGNING_IDENTITY" "$ARCHIVE_PATH/Info.plist"
  plutil -insert ApplicationProperties.Team -string "$APP_TEAM" "$ARCHIVE_PATH/Info.plist"

  plutil -create xml1 "$EXPORT_OPTIONS"
  plutil -insert method -string app-store-connect "$EXPORT_OPTIONS"
  plutil -insert teamID -string "$APP_TEAM" "$EXPORT_OPTIONS"
  plutil -insert signingStyle -string automatic "$EXPORT_OPTIONS"
  plutil -insert destination -string export "$EXPORT_OPTIONS"
  plutil -insert manageAppVersionAndBuildNumber -bool NO "$EXPORT_OPTIONS"

  mkdir -p "$EXPORT_PATH"
  if ! xcodebuild -exportArchive -archivePath "$ARCHIVE_PATH" \
      -exportOptionsPlist "$EXPORT_OPTIONS" -exportPath "$EXPORT_PATH" \
      -allowProvisioningUpdates; then
    # Xcode's cloud-managed installer export performs network I/O and can fail
    # after a successful five-minute universal build with "network connection
    # was lost". The signed app/archive are immutable, so one bounded export
    # retry is safe and avoids rebuilding every nested Electron resource.
    echo "Xcode Store export failed; retrying the same verified archive once…" >&2
    find "$EXPORT_PATH" -mindepth 1 -depth -delete 2>/dev/null || true
    xcodebuild -exportArchive -archivePath "$ARCHIVE_PATH" \
      -exportOptionsPlist "$EXPORT_OPTIONS" -exportPath "$EXPORT_PATH" \
      -allowProvisioningUpdates
  fi
  PKG_PATH="$(find "$EXPORT_PATH" -maxdepth 1 -type f -name '*.pkg' -print -quit)"
  if [ -z "$PKG_PATH" ]; then
    echo "ERROR: Xcode export succeeded but produced no signed App Store .pkg." >&2
    exit 1
  fi
  FALLBACK_PKG="$ELECTRON_DIR/dist-mas/yaver-gui-${APP_VERSION}-mac-testflight-universal.pkg"
  ditto "$PKG_PATH" "$FALLBACK_PKG"
  PKG_PATH="$FALLBACK_PKG"
fi
pkgutil --check-signature "$PKG_PATH"

# Prove the package payload preserved the requested bundle/build identity. A
# successful Xcode export once cleared CFBundleVersion to an empty string; that
# package was signed but could never be accepted by App Store Connect.
VERIFY_DIR="$XCODE_PACKAGE_DIR/verify"
if [ -z "$XCODE_PACKAGE_DIR" ]; then
  XCODE_PACKAGE_DIR="$(mktemp -d -t yaver-mas-xcode.XXXXXX)"
  VERIFY_DIR="$XCODE_PACKAGE_DIR/verify"
fi
pkgutil --expand-full "$PKG_PATH" "$VERIFY_DIR"
PACKAGED_APP="$(find "$VERIFY_DIR" -type d -path '*/Payload/Yaver.app' -print -quit)"
PACKAGED_BUNDLE_ID="$(plutil -extract CFBundleIdentifier raw -o - "$PACKAGED_APP/Contents/Info.plist" 2>/dev/null || true)"
PACKAGED_BUILD="$(plutil -extract CFBundleVersion raw -o - "$PACKAGED_APP/Contents/Info.plist" 2>/dev/null || true)"
if [ "$PACKAGED_BUNDLE_ID" != "$MAS_BUNDLE_ID" ] || [ "$PACKAGED_BUILD" != "$YAVER_MAC_BUILD_NUMBER" ]; then
  echo "ERROR: packaged app identity/build drifted (bundle=${PACKAGED_BUNDLE_ID:-missing}, build=${PACKAGED_BUILD:-missing})." >&2
  exit 1
fi
codesign --verify --deep --strict --verbose=2 "$PACKAGED_APP"
echo "Verified App Store package: $PKG_PATH"

if [ "$UPLOAD" != "1" ]; then
  echo "Build-only complete. No App Store Connect state was changed."
  exit 0
fi

# altool finds API keys by filename in ./private_keys. Use an owner-only temp
# directory instead of copying secrets into the repo or permanently mutating
# ~/.appstoreconnect.
UPLOAD_AUTH_DIR="$(mktemp -d -t yaver-asc-upload.XXXXXX)"
chmod 700 "$UPLOAD_AUTH_DIR"
mkdir -m 700 "$UPLOAD_AUTH_DIR/private_keys"
cp "$APP_STORE_KEY_PATH" "$UPLOAD_AUTH_DIR/private_keys/AuthKey_${APP_STORE_KEY_ID}.p8"
chmod 600 "$UPLOAD_AUTH_DIR/private_keys/AuthKey_${APP_STORE_KEY_ID}.p8"
cleanup_upload() { rm -f "$UPLOAD_AUTH_DIR/private_keys/AuthKey_${APP_STORE_KEY_ID}.p8"; rmdir "$UPLOAD_AUTH_DIR/private_keys" "$UPLOAD_AUTH_DIR" 2>/dev/null || true; }
trap 'cleanup_upload; cleanup' EXIT

echo "Validating package with App Store Connect…"
VALIDATION_LOG="$(mktemp -t yaver-mas-validation.XXXXXX)"
if ! (cd "$UPLOAD_AUTH_DIR" && xcrun altool --validate-app --file "$PKG_PATH" --type macos \
  --apiKey "$APP_STORE_KEY_ID" --apiIssuer "$APP_STORE_KEY_ISSUER") 2>&1 | tee "$VALIDATION_LOG"; then
  echo "ERROR: App Store Connect package validation command failed; upload was not attempted." >&2
  exit 1
fi
# altool can return zero even when Apple's response says VERIFY FAILED. Treat
# the server verdict as authoritative so a known-invalid package is never sent
# to the upload endpoint.
if grep -qE 'VERIFY FAILED|Validation failed|Failed to validate package' "$VALIDATION_LOG"; then
  echo "ERROR: App Store Connect rejected package validation; upload was not attempted." >&2
  exit 1
fi
echo "Uploading package to macOS TestFlight…"
(cd "$UPLOAD_AUTH_DIR" && xcrun altool --upload-app --file "$PKG_PATH" --type macos \
  --apiKey "$APP_STORE_KEY_ID" --apiIssuer "$APP_STORE_KEY_ISSUER")
echo "Upload accepted. App Store Connect will process build $YAVER_MAC_BUILD_NUMBER before it appears in TestFlight."
