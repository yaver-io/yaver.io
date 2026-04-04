#!/bin/bash
set -eo pipefail

cd "$(dirname "$0")/../mobile/ios"

# App Store Connect API key — set these env vars or edit for your team
AUTH_KEY="${APP_STORE_KEY_PATH:?Set APP_STORE_KEY_PATH to your AuthKey_*.p8 file}"
AUTH_KEY_ID="${APP_STORE_KEY_ID:?Set APP_STORE_KEY_ID}"
AUTH_KEY_ISSUER="${APP_STORE_KEY_ISSUER:?Set APP_STORE_KEY_ISSUER}"

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
cat > /tmp/ExportOptions.plist <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>method</key><string>app-store-connect</string>
    <key>teamID</key><string>${APPLE_TEAM_ID}</string>
    <key>signingStyle</key><string>automatic</string>
    <key>destination</key><string>upload</string>
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
