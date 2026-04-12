#!/usr/bin/env bash
# deploy.sh — native builds only.
set -euo pipefail
cd "$(dirname "$0")/.."

case "${1:-}" in
  backend)
    cd backend && npx convex deploy --yes ;;
  testflight)
    cd apps/mobile && npx expo prebuild --platform ios
    cd apps/mobile/ios && xcodebuild -workspace *.xcworkspace \
      -scheme "Bento" -configuration Release \
      -archivePath /tmp/app.xcarchive archive \
      DEVELOPMENT_TEAM="$APPLE_TEAM_ID" CODE_SIGN_STYLE=Automatic \
      -allowProvisioningUpdates \
      -authenticationKeyPath "$APP_STORE_KEY_PATH" \
      -authenticationKeyID "$APP_STORE_KEY_ID" \
      -authenticationKeyIssuerID "$APP_STORE_KEY_ISSUER"
    xcodebuild -exportArchive -archivePath /tmp/app.xcarchive \
      -exportOptionsPlist ../../scripts/ExportOptions.plist \
      -exportPath /tmp/export \
      -allowProvisioningUpdates ;;
  playstore)
    cd apps/mobile && npx expo prebuild --platform android
    cd apps/mobile/android && JAVA_HOME=$(/usr/libexec/java_home -v 17) ./gradlew bundleRelease ;;
  *)
    echo "usage: $0 web|landing|backend|testflight|playstore" ;;
esac
