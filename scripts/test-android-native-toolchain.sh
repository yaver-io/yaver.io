#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SETTINGS="$ROOT/mobile/android/settings.gradle"
WRAPPER="$ROOT/mobile/android/gradle/wrapper/gradle-wrapper.properties"
DEPLOY="$ROOT/scripts/deploy-playstore.sh"
PREBUILD="$ROOT/scripts/prebuild-android-native.sh"
ANDROID_SDK_HELPER="$ROOT/scripts/lib/android-sdk.sh"
JAVA_HOME_HELPER="$ROOT/scripts/lib/java-home.sh"
TV_DEPLOY="$ROOT/scripts/deploy-android-tv.sh"
WEAR_DEPLOY="$ROOT/scripts/deploy-wear-os.sh"
XR_DEPLOY="$ROOT/scripts/deploy-android-xr.sh"
ANDROID_ALL_DEPLOY="$ROOT/scripts/deploy-android-all.sh"
GRADLE_MEMORY_HELPER="$ROOT/scripts/lib/android-gradle-memory.sh"
NINJA_MEMORY_HELPER="$ROOT/scripts/lib/android-ninja-memory.sh"
SANDBOX_BUILD="$ROOT/scripts/build-android-sandbox.sh"

grep -q 'id("expo-autolinking-settings")' "$SETTINGS"
grep -q 'includeBuild(expoPluginsPath)' "$SETTINGS"
grep -q 'expoAutolinking.useExpoModules()' "$SETTINGS"

GRADLE_VERSION=$(sed -nE 's#.*gradle-([0-9]+\.[0-9]+(\.[0-9]+)?)-[^/]+\.zip#\1#p' "$WRAPPER")
if [ -z "$GRADLE_VERSION" ]; then
  echo "could not read Gradle wrapper version" >&2
  exit 1
fi
GRADLE_MAJOR=${GRADLE_VERSION%%.*}
GRADLE_MINOR=${GRADLE_VERSION#*.}; GRADLE_MINOR=${GRADLE_MINOR%%.*}
if [ "$GRADLE_MAJOR" -lt 8 ] || { [ "$GRADLE_MAJOR" -eq 8 ] && [ "$GRADLE_MINOR" -lt 13 ]; }; then
  echo "Expo SDK 54 / AGP requires Gradle >= 8.13; found $GRADLE_VERSION" >&2
  exit 1
fi

grep -q 'yaver_resolve_android_sdk' "$DEPLOY"
grep -q 'yaver_resolve_java_home 17' "$DEPLOY"
grep -q 'prebuild-android-native.sh.*--ensure' "$DEPLOY"
grep -q 'expo prebuild --platform android --clean --no-install' "$PREBUILD"
grep -Fqx 'git -C "$ROOT" restore --source=HEAD --worktree -- mobile/android' "$PREBUILD"
grep -q '"primaryColor": "#050506"' "$ROOT/mobile/app.json"
grep -q 'color name="colorPrimary">#050506' "$PREBUILD"
grep -q 'color name="splashscreen_background">#050506' "$PREBUILD"
grep -q 'YaverDogfoodPackage' "$PREBUILD"
grep -q 'YaverBundleLoaderPackage' "$PREBUILD"
grep -q 'sandbox/SandboxService.kt' "$PREBUILD"
grep -q 'car/YaverCarMessagingModule.kt' "$PREBUILD"
grep -q 'wear/YaverWearListenerService.kt' "$PREBUILD"
if grep -q "java.srcDir '../../native-" "$ROOT/mobile/android/app/build.gradle"; then
  echo "Prebuild-copied Android overlays must not also be compiled as source directories" >&2
  exit 1
fi
for workflow in \
  "$ROOT/.github/workflows/release-mobile.yml" \
  "$ROOT/.github/workflows/test-suite.yml" \
  "$ROOT/.github/workflows/mobile-variants.yml"; do
  grep -q './scripts/prebuild-android-native.sh' "$workflow"
  if grep -q 'npx expo prebuild --platform android --clean' "$workflow"; then
    echo "Android workflows must use the shared overlay-restoring prebuild" >&2
    exit 1
  fi
done
grep -q 'TOTAL_MEMORY_KB.*10 \* 1024 \* 1024' "$DEPLOY"
grep -q 'GRADLE_OPTS=.*-Xmx768m.*MaxMetaspaceSize=384m' "$DEPLOY"
grep -q 'GRADLE_OPTS=.*-Xmx8g' "$DEPLOY"
grep -q 'LOW_MEMORY_GRADLE_ARGS' "$DEPLOY"
grep -q 'LOW_MEMORY_ASSET_GRADLE_ARGS' "$DEPLOY"
grep -q -- '--no-daemon' "$DEPLOY"
grep -q -- "-Dorg.gradle.jvmargs=-Xmx1536m -XX:MaxMetaspaceSize=512m" "$DEPLOY"
grep -q -- "-XX:+UseSerialGC" "$DEPLOY"
grep -q -- "-Pkotlin.compiler.execution.strategy=in-process" "$DEPLOY"
grep -q -- "--max-workers=1" "$DEPLOY"
grep -q 'YAVER_ANDROID_METRO_WORKERS.*:-1' "$DEPLOY"
grep -q 'extraPackagerArgs = \["--max-workers", metroWorkers\]' "$ROOT/mobile/android/app/build.gradle"
grep -q 'config.maxWorkers = metroWorkers' "$ROOT/mobile/metro.config.js"
grep -q 'YAVER_ANDROID_METRO_TMPDIR' "$DEPLOY"
grep -q 'metro-cache-probe' "$DEPLOY"
grep -q ':app:createBundleReleaseJsAndAssets' "$DEPLOY"
grep -q ':app:createReleaseUpdatesResources' "$DEPLOY"
grep -q 'YAVER_ANDROID_HERMES_COMMAND' "$DEPLOY"
grep -q 'hermesHostOverride' "$ROOT/mobile/android/app/build.gradle"
grep -q 'YAVER_PLAYSTORE_VOLUME_PATH' "$DEPLOY"
grep -q 'df -Pk "$BUILD_VOLUME_PATH"' "$DEPLOY"
grep -q 'sed -i.bak' "$DEPLOY"
if grep -q "sed -i ''" "$DEPLOY"; then
  echo "Play deploy must use portable in-place sed syntax" >&2
  exit 1
fi
if grep -q 'sed .*org\\.gradle\\.jvmargs' "$DEPLOY"; then
  echo "Play deploy must keep its larger heap process-local" >&2
  exit 1
fi
if [ "$(grep -c 'PreactNativeArchitectures=' "$DEPLOY")" -lt 2 ]; then
  echo "Every native Gradle phase must receive the requested ABI filter" >&2
  exit 1
fi
if grep -q 'Pandroid\.injected\.build\.abi=' "$DEPLOY"; then
  echo "Play deploy must not use android.injected.build.abi; it marks release bundles testOnly" >&2
  exit 1
fi
grep -q 'android:testOnly="true"' "$DEPLOY"
grep -q 'yaver_resolve_android_sdk' "$TV_DEPLOY"
grep -q 'yaver_resolve_android_sdk' "$WEAR_DEPLOY"
grep -q 'yaver_android_sdk_is_usable' "$ANDROID_SDK_HELPER"
grep -q 'yaver_java_home_has_major' "$JAVA_HOME_HELPER"
grep -q 'yaver_release_manifest_path' "$ANDROID_SDK_HELPER"
grep -q 'yaver_release_manifest_path' "$TV_DEPLOY"
grep -q 'yaver_release_manifest_path' "$WEAR_DEPLOY"
grep -q 'yaver_release_manifest_path' "$XR_DEPLOY"
grep -q 'YAVER_ANDROID_GRADLE_ARGS' "$TV_DEPLOY"
grep -q 'YAVER_ANDROID_GRADLE_ARGS' "$WEAR_DEPLOY"
grep -q -- '-Pkotlin.compiler.execution.strategy=in-process' "$GRADLE_MEMORY_HELPER"
grep -q -- '--max-workers=1' "$GRADLE_MEMORY_HELPER"
grep -q -- '-Xmx1536m -XX:MaxMetaspaceSize=512m -XX:+UseSerialGC' "$GRADLE_MEMORY_HELPER"
grep -q 'yaver_android_limit_ninja_jobs' "$DEPLOY"
grep -q 'yaver_android_probe_ndk_host' "$DEPLOY"
grep -q 'ninja.yaver-real" -j"$jobs" "$@"' "$NINJA_MEMORY_HELPER"
grep -q 'GOMEMLIMIT=.*1536MiB' "$SANDBOX_BUILD"
grep -q 'GOGC=.*20' "$SANDBOX_BUILD"
grep -q 'GOMAXPROCS=.*1' "$SANDBOX_BUILD"
grep -q 'YAVER_ANDROID_AGENT_SHA256' "$SANDBOX_BUILD"
grep -q 'reused checksum-verified prebuilt agent' "$SANDBOX_BUILD"
grep -q 'refusing to publish a silently degraded Yaver AAB' "$DEPLOY"
if grep -q 'sandbox payload build failed — continuing' "$DEPLOY"; then
  echo "Play deploy must not report success after a sandbox build failure" >&2
  exit 1
fi

# Execute the low-memory Ninja guard, not just its wiring. The fake SDK binary
# records the wrapper's final argv so this fails if -j1 stops reaching Ninja.
NINJA_TEST_ROOT=$(mktemp -d)
cleanup_ninja_test_root() {
  if [ -d "$NINJA_TEST_ROOT" ]; then
    ls -la "$NINJA_TEST_ROOT" >/dev/null
    rm -rf "$NINJA_TEST_ROOT"
  fi
}
trap cleanup_ninja_test_root EXIT
mkdir -p "$NINJA_TEST_ROOT/sdk/cmake/3.22.1/bin"
cat >"$NINJA_TEST_ROOT/sdk/cmake/3.22.1/bin/ninja" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$YAVER_NINJA_TEST_LOG"
EOF
chmod +x "$NINJA_TEST_ROOT/sdk/cmake/3.22.1/bin/ninja"
export YAVER_NINJA_TEST_LOG="$NINJA_TEST_ROOT/argv.log"
export ANDROID_SDK_ROOT="$NINJA_TEST_ROOT/sdk"
export YAVER_ANDROID_NINJA_FORCE=1
# shellcheck source=scripts/lib/android-ninja-memory.sh
source "$NINJA_MEMORY_HELPER"
yaver_android_limit_ninja_jobs
"$ANDROID_SDK_ROOT/cmake/3.22.1/bin/ninja" target
"$ANDROID_SDK_ROOT/cmake/3.22.1/bin/ninja" -t restat build.ninja
grep -q '^-j1 target$' "$YAVER_NINJA_TEST_LOG"
grep -q '^-j1 -t restat build.ninja$' "$YAVER_NINJA_TEST_LOG"
# Prove an SDK already wrapped by the original append-style limiter is upgraded
# in place; otherwise existing workers keep failing CMake tool mode forever.
cat >"$ANDROID_SDK_ROOT/cmake/3.22.1/bin/ninja" <<'EOF'
#!/bin/sh
# yaver-android-ninja-low-memory
self_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
exec "$self_dir/ninja.yaver-real" "$@" -j1
EOF
chmod +x "$ANDROID_SDK_ROOT/cmake/3.22.1/bin/ninja"
yaver_android_limit_ninja_jobs
grep -q '^# yaver-android-ninja-low-memory-v2$' "$ANDROID_SDK_ROOT/cmake/3.22.1/bin/ninja"
"$ANDROID_SDK_ROOT/cmake/3.22.1/bin/ninja" -t restat build.ninja
grep -q '^-j1 -t restat build.ninja$' "$YAVER_NINJA_TEST_LOG"
if YAVER_ANDROID_NINJA_JOBS=0 "$ANDROID_SDK_ROOT/cmake/3.22.1/bin/ninja" target >/dev/null 2>&1; then
  echo "Ninja low-memory wrapper accepted an invalid zero job count" >&2
  exit 1
fi
cleanup_ninja_test_root
trap - EXIT
grep -q 'deploy-playstore.sh' "$ANDROID_ALL_DEPLOY"
grep -q 'deploy-android-auto.sh' "$ANDROID_ALL_DEPLOY"
grep -q 'deploy-android-xr.sh.*--skip-build' "$ANDROID_ALL_DEPLOY"
grep -q 'deploy-wear-os.sh.*--upload' "$ANDROID_ALL_DEPLOY"
grep -q 'deploy-android-tv.sh.*--upload' "$ANDROID_ALL_DEPLOY"
grep -q 'android-all)' "$ROOT/deploy/deploy.sh"
grep -q 'android-xr|xr-android)' "$ROOT/deploy/deploy.sh"
grep -q 'PLAY_TRACK="${PLAY_TRACK:-wear:internal}"' "$WEAR_DEPLOY"
grep -q 'mobile/android/gradlew.*androidtv' "$TV_DEPLOY"
grep -q '"$HOME/Library/Android/sdk"' "$ANDROID_SDK_HELPER"
grep -q '"$HOME/Android/Sdk"' "$ANDROID_SDK_HELPER"

echo "Android native toolchain wiring OK (Gradle $GRADLE_VERSION)."
