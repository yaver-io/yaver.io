#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MOBILE="$ROOT/mobile"
ANDROID="$MOBILE/android"

verify_android_native_tree() {
  local failed=0

  require_file() {
    if [ ! -f "$ANDROID/$1" ]; then
      echo "ERROR: Android native prebuild is missing $1" >&2
      failed=1
    fi
  }

  require_text() {
    local relative="$1"
    local marker="$2"
    require_file "$relative"
    if [ -f "$ANDROID/$relative" ] && ! grep -Fq "$marker" "$ANDROID/$relative"; then
      echo "ERROR: Android native prebuild lost '$marker' from $relative" >&2
      failed=1
    fi
  }

  # Generated scaffold: these files are intentionally ignored by git and only
  # exist after Expo prebuild. A partial force-tracked overlay is not buildable.
  require_text "settings.gradle" 'id("expo-autolinking-settings")'
  require_file "gradlew"
  require_file "gradle/wrapper/gradle-wrapper.properties"

  # Force-tracked Yaver host overlays. `expo prebuild --clean` replaces these
  # with Expo templates unless the release path restores them from HEAD.
  require_text "app/src/main/java/io/yaver/mobile/MainApplication.kt" "add(YaverDogfoodPackage())"
  require_text "app/src/main/java/io/yaver/mobile/MainApplication.kt" "add(YaverBundleLoaderPackage())"
  require_text "app/src/main/java/io/yaver/mobile/MainApplication.kt" "add(io.yaver.mobile.sandbox.SandboxPackage())"
  require_file "app/src/main/java/io/yaver/mobile/YaverDogfoodModule.kt"
  require_file "app/src/main/java/io/yaver/mobile/YaverBundleLoaderModule.kt"
  require_file "app/src/main/java/io/yaver/mobile/sandbox/SandboxService.kt"
  require_text "app/src/main/AndroidManifest.xml" "io.yaver.mobile.sandbox.SandboxService"
  require_text "app/src/main/AndroidManifest.xml" "io.yaver.mobile.sandbox.RemotelessTaskService"

  # Config-plugin outputs are generated, not tracked under mobile/android. If a
  # plugin silently stops running, the tracked MainApplication imports compile
  # against files that do not exist and the shared phone/TV/car/wear AAB drifts.
  require_file "app/src/main/java/io/yaver/mobile/car/YaverCarMessagingModule.kt"
  require_file "app/src/main/java/io/yaver/mobile/car/YaverCarMessagingPackage.kt"
  require_file "app/src/main/java/io/yaver/mobile/wear/YaverWearBridgeModule.kt"
  require_file "app/src/main/java/io/yaver/mobile/wear/YaverWearListenerService.kt"
  require_file "app/src/main/java/io/yaver/mobile/wear/YaverWearPackage.kt"

  return "$failed"
}

mode="${1:-rebuild}"
case "$mode" in
  --verify-only)
    verify_android_native_tree
    echo "Android native scaffold and Yaver overlays are complete."
    exit 0
    ;;
  --ensure)
    if verify_android_native_tree 2>/dev/null; then
      echo "Android native scaffold and Yaver overlays are already complete."
      exit 0
    fi
    echo "Android native scaffold is incomplete; regenerating it and restoring Yaver overlays..."
    ;;
  rebuild|"")
    ;;
  *)
    echo "Usage: $0 [--ensure|--verify-only]" >&2
    exit 2
    ;;
esac

if [ ! -d "$MOBILE/node_modules/@expo" ]; then
  echo "ERROR: mobile dependencies are missing; cannot generate the Android native scaffold." >&2
  echo "Run: cd mobile && npm ci --legacy-peer-deps" >&2
  exit 1
fi

# Preserve the local, gitignored signing properties across `--clean`. The
# keystore itself lives outside mobile/android in the canonical release layout.
saved_keystore_properties=""
if [ -e "$ANDROID/keystore.properties" ]; then
  saved_keystore_properties="$(mktemp "${TMPDIR:-/tmp}/yaver-android-keystore.XXXXXX")"
  chmod 600 "$saved_keystore_properties"
  cp -L "$ANDROID/keystore.properties" "$saved_keystore_properties"
fi
cleanup() {
  [ -z "$saved_keystore_properties" ] || rm -f "$saved_keystore_properties"
}
trap cleanup EXIT

(cd "$MOBILE" && EXPO_NO_GIT_STATUS=1 npx expo prebuild --platform android --clean --no-install)

# Code is authoritative: restore every force-tracked native overlay, including
# the host, manifest, dogfood, bundle-loader, sandbox, TV/car resources, and
# release Gradle configuration. Untracked generated scaffold/plugin files stay.
git -C "$ROOT" restore --source=HEAD --worktree -- mobile/android

if [ -n "$saved_keystore_properties" ]; then
  cp "$saved_keystore_properties" "$ANDROID/keystore.properties"
  chmod 600 "$ANDROID/keystore.properties"
fi

verify_android_native_tree
echo "Android native prebuild complete: generated scaffold + Yaver overlays verified."
