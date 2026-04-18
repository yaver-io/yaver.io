#!/bin/bash
set -e

cd "$(dirname "$0")/../mobile/android"

# Bump versionCode
GRADLE_FILE="app/build.gradle"
CURRENT_VERSION_CODE=$(grep 'versionCode ' "$GRADLE_FILE" | head -1 | sed 's/[^0-9]//g')
OVERRIDE_VERSION_CODE="${ANDROID_VERSION_CODE:-}"
REMOTE_MAX_VERSION_CODE=""

if [ -z "$OVERRIDE_VERSION_CODE" ] && [ -n "${PLAY_STORE_KEY_FILE:-}" ] && [ -f "${PLAY_STORE_KEY_FILE}" ]; then
REMOTE_MAX_VERSION_CODE=$(PLAY_STORE_KEY_FILE="$PLAY_STORE_KEY_FILE" python3 - <<'PY'
import os
from google.oauth2.service_account import Credentials
from googleapiclient.discovery import build

key = os.environ.get("PLAY_STORE_KEY_FILE")
if not key:
    raise SystemExit("")

creds = Credentials.from_service_account_file(
    key,
    scopes=["https://www.googleapis.com/auth/androidpublisher"],
)
service = build("androidpublisher", "v3", credentials=creds)
edit = service.edits().insert(body={}, packageName="io.yaver.mobile").execute()
edit_id = edit["id"]
try:
    bundles = service.edits().bundles().list(
        packageName="io.yaver.mobile",
        editId=edit_id,
    ).execute().get("bundles", [])
    max_version = max((int(bundle["versionCode"]) for bundle in bundles), default=0)
    print(max_version)
finally:
    service.edits().delete(packageName="io.yaver.mobile", editId=edit_id).execute()
PY
)
fi

if [ -n "$OVERRIDE_VERSION_CODE" ]; then
NEW_VERSION_CODE="$OVERRIDE_VERSION_CODE"
elif [ -n "$REMOTE_MAX_VERSION_CODE" ] && [ "$REMOTE_MAX_VERSION_CODE" -ge "$CURRENT_VERSION_CODE" ]; then
NEW_VERSION_CODE=$((REMOTE_MAX_VERSION_CODE + 1))
else
NEW_VERSION_CODE=$((CURRENT_VERSION_CODE + 1))
fi
sed -i '' "s/versionCode $CURRENT_VERSION_CODE/versionCode $NEW_VERSION_CODE/" "$GRADLE_FILE"
echo "versionCode $CURRENT_VERSION_CODE -> $NEW_VERSION_CODE"

# Build release AAB.
# We deliberately do NOT `gradlew clean` here. A clean wipes every
# react-native-<lib>/android/build/generated/source/codegen/jni/
# directory, but the autolinking-generated CMakeLists.txt still
# references all of them at configure time — so the next bundleRelease
# blows up with "add_subdirectory given source ... which is not an
# existing directory" before any codegen task gets a chance to run.
# Letting Gradle do an incremental build keeps the JNI dirs around
# and avoids the chicken-and-egg.
# Build worklets prefab first — reanimated CMake configure depends on it.
echo "Building release AAB..."
./gradlew :react-native-worklets:prefabReleasePackage
./gradlew bundleRelease

AAB_PATH="app/build/outputs/bundle/release/app-release.aab"

if [ ! -f "$AAB_PATH" ]; then
  echo "ERROR: AAB not found at $AAB_PATH"
  exit 1
fi

echo ""
echo "Release AAB built successfully!"
echo "  Path: $(pwd)/$AAB_PATH"
echo "  versionCode: $NEW_VERSION_CODE"
echo ""
echo "Upload to Google Play Console:"
echo "  1. Go to https://play.google.com/console"
echo "  2. Select 'Yaver' app (io.yaver.mobile)"
echo "  3. Go to Testing > Internal testing"
echo "  4. Create new release and upload the AAB"
echo ""
echo "AAB path for upload:"
echo "  $(pwd)/$AAB_PATH"
