#!/bin/bash
set -eo pipefail

cd "$(dirname "$0")/../mobile/ios"

# Load secrets from the Yaver vault (project="mobile" + globals). Vault
# values win when present; values not in the vault fall through from the
# parent env. Locally: `yaver vault add APP_STORE_KEY_PATH --project mobile`.
# In CI: just don't put the secret in the vault — GitHub Actions env vars
# pass through unchanged.
if command -v yaver >/dev/null 2>&1; then
  eval "$(yaver vault env --project mobile 2>/dev/null || true)"
fi

# Vault-locked fallback (mirrors deploy-web.sh's ~/.androidplay/yaver.env).
# After kivanc's auth token rotates more than once, `yaver vault env`
# returns "wrong passphrase or corrupted vault" until YAVER_VAULT_PASSPHRASE
# is supplied — and `yaver deploy all` runs this script non-interactively,
# so there's no chance to set it. Without this, the script dies at the
# APP_STORE_KEY_PATH:? guard below with a misleading "secret not set"
# error when the real cause is a locked vault. `~/.appstoreconnect/yaver.env`
# is gitignored and pre-seeded with all four App Store Connect exports
# (see CLAUDE.md "iOS — TestFlight"). Vault values still win when readable;
# this only fills the gap when the vault can't be opened.
if [ -f "$HOME/.appstoreconnect/yaver.env" ]; then
  # shellcheck source=/dev/null
  set -a; source "$HOME/.appstoreconnect/yaver.env"; set +a
fi

# App Store Connect API key — set these env vars or in the Yaver vault.
AUTH_KEY="${APP_STORE_KEY_PATH:?APP_STORE_KEY_PATH unset. Likely cause: the Yaver vault is locked (auth token rotated >1x). Recover with ANY of: (1) pre-seed ~/.appstoreconnect/yaver.env (gitignored, 4 exports — see CLAUDE.md \"iOS — TestFlight\"); (2) YAVER_VAULT_PASSPHRASE=<old-token> before deploy; (3) re-add: yaver vault add APP_STORE_KEY_PATH --project mobile --value ...}"
AUTH_KEY_ID="${APP_STORE_KEY_ID:?Set APP_STORE_KEY_ID (env or yaver vault)}"
AUTH_KEY_ISSUER="${APP_STORE_KEY_ISSUER:?Set APP_STORE_KEY_ISSUER (env or yaver vault)}"

# Bump build number
PLIST="Yaver/Info.plist"
CURRENT_BUILD=$(/usr/libexec/PlistBuddy -c "Print CFBundleVersion" "$PLIST")
NEW_BUILD=$((CURRENT_BUILD + 1))
/usr/libexec/PlistBuddy -c "Set CFBundleVersion $NEW_BUILD" "$PLIST"
echo "Build $CURRENT_BUILD → $NEW_BUILD"

# Clean stale archive so a failed build can't silently reuse it
rm -rf /tmp/Yaver.xcarchive

# Archive
echo "Archiving..."
xcodebuild -workspace Yaver.xcworkspace -scheme Yaver -configuration Release \
  -archivePath /tmp/Yaver.xcarchive archive \
  DEVELOPMENT_TEAM="${APPLE_TEAM_ID:?Set APPLE_TEAM_ID}" CODE_SIGN_STYLE=Automatic \
  ENABLE_USER_SCRIPT_SANDBOXING=NO -allowProvisioningUpdates \
  -authenticationKeyPath "$AUTH_KEY" \
  -authenticationKeyID "$AUTH_KEY_ID" \
  -authenticationKeyIssuerID "$AUTH_KEY_ISSUER" \
  -derivedDataPath /tmp/YaverBuild 2>&1 | tail -3

# Verify archive was created
if [ ! -d /tmp/Yaver.xcarchive ]; then
  echo "ERROR: Archive failed — no .xcarchive produced"
  exit 1
fi

# ExportOptions (no single-quote on EOF so APPLE_TEAM_ID expands)
# uploadSymbols=false: rnwhisper framework has missing dSYMs that
# Xcode 15+ treats as a fatal export error. Crash reports still work
# from Apple's symbolication — we just skip uploading our local dSYMs.
cat > /tmp/ExportOptions.plist <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>method</key><string>app-store-connect</string>
    <key>teamID</key><string>${APPLE_TEAM_ID}</string>
    <key>signingStyle</key><string>automatic</string>
    <key>destination</key><string>upload</string>
    <key>uploadSymbols</key><false/>
</dict>
</plist>
EOF

# Export & upload (destination=upload sends directly to App Store Connect)
echo "Exporting & uploading..."
EXPORT_OUTPUT=$(xcodebuild -exportArchive -archivePath /tmp/Yaver.xcarchive \
  -exportOptionsPlist /tmp/ExportOptions.plist \
  -exportPath /tmp/YaverExport -allowProvisioningUpdates \
  -authenticationKeyPath "$AUTH_KEY" \
  -authenticationKeyID "$AUTH_KEY_ID" \
  -authenticationKeyIssuerID "$AUTH_KEY_ISSUER" 2>&1)
EXPORT_EXIT=$?

echo "$EXPORT_OUTPUT" | tail -3

# Check for success: either exit 0, or "Redundant Binary" (already uploaded)
if [ $EXPORT_EXIT -ne 0 ] && ! echo "$EXPORT_OUTPUT" | grep -q "Redundant Binary Upload"; then
  echo "ERROR: Export/upload failed (exit $EXPORT_EXIT)"
  exit 1
fi

echo "✓ TestFlight build $NEW_BUILD uploaded"

mobile-cache-cleanup.sh mark-deployed yaver || true
