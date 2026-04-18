#!/bin/bash
set -e

cd "$(dirname "$0")/../mobile/android"

# Bump versionCode
GRADLE_FILE="app/build.gradle"
CURRENT_VERSION_CODE=$(grep 'versionCode ' "$GRADLE_FILE" | head -1 | sed 's/[^0-9]//g')
NEW_VERSION_CODE=$((CURRENT_VERSION_CODE + 1))
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
