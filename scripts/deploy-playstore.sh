#!/bin/bash
set -e

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# A fresh checkout intentionally contains only force-tracked Yaver native
# overlays; Expo owns the ignored scaffold. Ensure the actual build tree exists
# and still contains both generated config-plugin sources and Yaver's dogfood,
# bundle-loader, and on-device sandbox host before touching signing or Play.
"$REPO_ROOT/scripts/prebuild-android-native.sh" --ensure
cd "$REPO_ROOT/mobile/android"
# shellcheck source=scripts/lib/android-sdk.sh
source "$REPO_ROOT/scripts/lib/android-sdk.sh"
# shellcheck source=scripts/lib/java-home.sh
source "$REPO_ROOT/scripts/lib/java-home.sh"
# shellcheck source=scripts/lib/android-ninja-memory.sh
source "$REPO_ROOT/scripts/lib/android-ninja-memory.sh"
# shellcheck source=scripts/lib/android-aab-signing.sh
source "$REPO_ROOT/scripts/lib/android-aab-signing.sh"
yaver_resolve_java_home 17
yaver_resolve_android_sdk

# React Native ships a Linux x86_64 Hermes compiler but its Gradle host mapper
# rejects Linux ARM64 before trying it. This worker already executes Google's
# x86_64-only NDK host tools through binfmt, so resolve and probe Hermes by the
# real operation as well. Other hosts keep React Native's normal %OS-BIN% path.
case "$(uname -s):$(uname -m)" in
  Linux:aarch64|Linux:arm64)
    export YAVER_ANDROID_HERMES_COMMAND="$REPO_ROOT/mobile/node_modules/react-native/sdks/hermesc/linux64-bin/hermesc"
    if [ ! -x "$YAVER_ANDROID_HERMES_COMMAND" ] || \
       ! "$YAVER_ANDROID_HERMES_COMMAND" -version >/dev/null 2>&1; then
      echo "ERROR: React Native's Linux Hermes compiler cannot execute on $(uname -m)." >&2
      echo "Install qemu-user-static, binfmt-support, and the amd64 libc/libstdc++ runtime, then retry." >&2
      exit 2
    fi
    ;;
esac

# The Gradle daemon has its own 3 GiB cap in gradle.properties. This is the
# short-lived launcher JVM: an unconditional 8 GiB launcher took a nominal
# 4 GiB remote worker into swap before the build even began. Size it from real
# memory unless the operator already provided an -Xmx override.
LOW_MEMORY_GRADLE_ARGS=()
LOW_MEMORY_ASSET_GRADLE_ARGS=()
if [[ " ${GRADLE_OPTS:-} " != *" -Xmx"* ]]; then
  TOTAL_MEMORY_KB=""
  if [ -r /proc/meminfo ]; then
    TOTAL_MEMORY_KB=$(awk '/^MemTotal:/ {print $2; exit}' /proc/meminfo)
  elif command -v sysctl >/dev/null 2>&1; then
    TOTAL_MEMORY_BYTES=$(sysctl -n hw.memsize 2>/dev/null || true)
    [ -n "$TOTAL_MEMORY_BYTES" ] && TOTAL_MEMORY_KB=$((TOTAL_MEMORY_BYTES / 1024))
  fi
  if [ -n "$TOTAL_MEMORY_KB" ] && [ "$TOTAL_MEMORY_KB" -lt $((10 * 1024 * 1024)) ]; then
    export GRADLE_OPTS="${GRADLE_OPTS:-} -Xmx1g -XX:MaxMetaspaceSize=512m"
    # The project default caps the Gradle daemon at 3 GiB, but Kotlin may also
    # start multiple independent 3 GiB compiler daemons (different plugin
    # versions can require one each). On a 4 GiB worker that turns an apparently
    # bounded build into an OOM. Keep Kotlin inside the 2 GiB Gradle process and
    # serialize workers for this lane; larger CI/Mac builders retain defaults.
    LOW_MEMORY_GRADLE_ARGS=(
      --no-daemon
      --max-workers=1
      '-Dorg.gradle.jvmargs=-Xmx1536m -XX:MaxMetaspaceSize=384m'
      '-Pkotlin.compiler.execution.strategy=in-process'
    )
    # The React Native bundle/Hermes task and expo-updates manifest task each
    # start a large Node process. Keep their Gradle orchestration JVM smaller
    # and, critically, in separate no-daemon invocations. When both ran inside
    # bundleRelease, Gradle retained ~2.2 GiB of compilation state while Expo
    # Updates peaked above 1 GiB, so the kernel killed Gradle despite every
    # advertised worker count being one.
    LOW_MEMORY_ASSET_GRADLE_ARGS=(
      --no-daemon
      --max-workers=1
      '-Dorg.gradle.jvmargs=-Xmx768m -XX:MaxMetaspaceSize=384m'
      '-Pkotlin.compiler.execution.strategy=in-process'
    )
    export YAVER_ANDROID_NINJA_JOBS="${YAVER_ANDROID_NINJA_JOBS:-1}"
    export YAVER_ANDROID_METRO_WORKERS="${YAVER_ANDROID_METRO_WORKERS:-1}"
    case "$YAVER_ANDROID_METRO_WORKERS" in
      ''|*[!0-9]*|0)
        echo "ERROR: YAVER_ANDROID_METRO_WORKERS must be a positive integer; got: $YAVER_ANDROID_METRO_WORKERS" >&2
        exit 2
        ;;
    esac
    yaver_android_limit_ninja_jobs
    yaver_android_probe_ndk_host
  else
    export GRADLE_OPTS="${GRADLE_OPTS:-} -Xmx8g -XX:MaxMetaspaceSize=1g"
  fi
fi

# Android signing creds + Play service account path. ~/.androidplay/yaver.env
# is gitignored — pre-seed it with the exports the build/upload need
# (PLAY_STORE_KEY_FILE, ANDROID_RELEASE_SHA256, any keystore overrides). In CI
# they arrive as GitHub secrets in the parent env.
#
# This used to be a fallback behind `yaver vault env`. It is now the source: the
# vault call swallowed its own failure, and `yaver deploy all` runs this
# non-interactively, so a locked vault could not even be reported let alone
# unlocked.
if [ -f "$HOME/.androidplay/yaver.env" ]; then
  # shellcheck source=/dev/null
  set -a; source "$HOME/.androidplay/yaver.env"; set +a
fi

"$REPO_ROOT/scripts/check-no-native-payment-sdks.sh" source
if [ ! -f "keystore.properties" ] || [ ! -f "$REPO_ROOT/keys/yaver-upload.keystore" ]; then
  echo "Android release signing material missing; running scripts/bootstrap-android-signing.sh..."
  if ! (cd "$REPO_ROOT" && ./scripts/bootstrap-android-signing.sh); then
    echo "ERROR: Android release signing material is missing and bootstrap failed." >&2
    echo "Expected $REPO_ROOT/keys/yaver-upload.keystore and mobile/android/keystore.properties before building." >&2
    exit 1
  fi
fi

STORE_FILE=$(awk -F= '/^storeFile=/ {print substr($0, index($0, "=") + 1)}' keystore.properties | tail -1)
STORE_PASSWORD=$(awk -F= '/^storePassword=/ {print substr($0, index($0, "=") + 1)}' keystore.properties | tail -1)
if [ -z "$STORE_FILE" ] || [ -z "$STORE_PASSWORD" ]; then
  echo "ERROR: mobile/android/keystore.properties is missing storeFile or storePassword." >&2
  exit 1
fi
case "$STORE_FILE" in
  /*) KEYSTORE_FILE="$STORE_FILE" ;;
  *) KEYSTORE_FILE="$(cd app && pwd)/$STORE_FILE" ;;
esac
if [ ! -f "$KEYSTORE_FILE" ]; then
  echo "ERROR: Android release keystore file not found at $KEYSTORE_FILE." >&2
  echo "Run ./scripts/bootstrap-android-signing.sh or restore the local gitignored keys/yaver-upload.keystore file." >&2
  exit 1
fi
if command -v keytool >/dev/null 2>&1; then
  if ! keytool -list -keystore "$KEYSTORE_FILE" -storepass "$STORE_PASSWORD" >/dev/null 2>&1; then
    echo "ERROR: Android release keystore exists but keytool cannot open it with keystore.properties." >&2
    echo "Re-run ./scripts/bootstrap-android-signing.sh so the keystore and passwords come from the same vault snapshot." >&2
    exit 1
  fi
fi

if [ -x "./gradlew" ]; then
  GRADLE="./gradlew"
elif command -v gradle >/dev/null 2>&1; then
  GRADLE="gradle"
else
  echo "ERROR: No Gradle runner found."
  echo "Expected ./mobile/android/gradlew or a global 'gradle' binary."
  exit 1
fi

MIN_FREE_GB="${YAVER_PLAYSTORE_MIN_FREE_GB:-16}"
# A low-disk release worker may keep its owner-locked source checkout locally
# while routing Gradle caches, node_modules, native intermediates, and outputs
# to a private mounted build volume. Probe that real operation volume when it
# is explicitly supplied; checking the small source filesystem would be a
# false red even though every material write lands elsewhere.
BUILD_VOLUME_PATH="${YAVER_PLAYSTORE_VOLUME_PATH:-.}"
if [ ! -d "$BUILD_VOLUME_PATH" ]; then
  echo "ERROR: configured Android build volume does not exist: $BUILD_VOLUME_PATH" >&2
  exit 1
fi
AVAILABLE_KB=$(df -Pk "$BUILD_VOLUME_PATH" | awk 'NR==2 {print $4}')
MIN_FREE_KB=$((MIN_FREE_GB * 1024 * 1024))
if [ -n "$AVAILABLE_KB" ] && [ "$AVAILABLE_KB" -lt "$MIN_FREE_KB" ]; then
  AVAILABLE_GB=$((AVAILABLE_KB / 1024 / 1024))
  echo "ERROR: Play deploy needs at least ${MIN_FREE_GB} GiB free on the Android build volume ($BUILD_VOLUME_PATH); only ${AVAILABLE_GB} GiB is available." >&2
  echo "Clean generated artifacts (mobile/android/app/build, mobile/android/.gradle, node_modules native .cxx/build outputs, or Gradle caches) or run the Play deploy on CI." >&2
  exit 1
fi

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
# The separated empty-suffix in-place form is BSD-only and fails on the Linux
# arm64 build worker. The
# attached-backup form is supported by both BSD and GNU sed.
sed -i.bak "s/versionCode $CURRENT_VERSION_CODE/versionCode $NEW_VERSION_CODE/" "$GRADLE_FILE"
rm -f "$GRADLE_FILE.bak"
echo "versionCode $CURRENT_VERSION_CODE -> $NEW_VERSION_CODE"

# On-device sandbox payload: cross-compile the Go agent (+ proot when a source is
# configured) into jniLibs so the shipped AAB can host the phone-as-Linux-box
# (SandboxService → libyaver.so + proot rootfs). Skipped only when explicitly
# opted out; otherwise missing Go or a failed payload build is fatal. proot
# needs PROOT_SRC or YAVER_PROOT_URL.
# A production deploy must never turn a failed build into a success response;
# YAVER_SKIP_SANDBOX=1 is the explicit operator opt-out. cwd is mobile/android.
if [ "${YAVER_SKIP_SANDBOX:-0}" != "1" ]; then
  if command -v go >/dev/null 2>&1; then
    echo "Building on-device sandbox payload (jniLibs)..."
    if ../../scripts/build-android-sandbox.sh; then
      [ -f "app/src/main/jniLibs/.sandbox-payload.txt" ] && \
        sed 's/^/  sandbox: /' "app/src/main/jniLibs/.sandbox-payload.txt" || true
    else
      echo "ERROR: sandbox payload build failed; refusing to publish a silently degraded Yaver AAB." >&2
      echo "Fix the streamed build failure or explicitly set YAVER_SKIP_SANDBOX=1." >&2
      exit 1
    fi
  else
    echo "ERROR: 'go' is required for the on-device Yaver sandbox payload." >&2
    echo "Install Go or explicitly set YAVER_SKIP_SANDBOX=1." >&2
    exit 1
  fi
fi

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
"$GRADLE" :react-native-worklets:prefabReleasePackage \
  ${YAVER_PLAYSTORE_ABI:+-PreactNativeArchitectures="$YAVER_PLAYSTORE_ABI"} \
  "${LOW_MEMORY_GRADLE_ARGS[@]}"

# Reanimated 4.x imports libworklets.so from the legacy AGP
# intermediates/cmake/release path, while the current worklets/AGP build emits
# it under intermediates/cxx/RelWithDebInfo/<hash>/obj. Bridge the exact ABI
# files after building worklets so bundleRelease cannot fail later with
# "libworklets.so missing and no known rule to make it".
WORKLETS_ANDROID="../../mobile/node_modules/react-native-worklets/android"
WORKLETS_EXPECTED="$WORKLETS_ANDROID/build/intermediates/cmake/release/obj"
for abi in arm64-v8a armeabi-v7a x86 x86_64; do
  src=$(find "$WORKLETS_ANDROID/build/intermediates/cxx/RelWithDebInfo" -path "*/obj/$abi/libworklets.so" -print -quit 2>/dev/null || true)
  if [ -n "$src" ]; then
    mkdir -p "$WORKLETS_EXPECTED/$abi"
    dst="$WORKLETS_EXPECTED/$abi/libworklets.so"
    # SAME FILE IS NOT A FAILURE (2026-08-02). AGP can make these two paths
    # resolve to one inode, and `cp a a` then exits NON-ZERO with "are
    # identical (not copied)". Under `set -e` that killed the deploy right
    # after "BUILD SUCCESSFUL" and before bundleRelease — so the log's last
    # useful line said success, no .aab was ever produced, and nothing reached
    # Play. A bridge step that is already satisfied must be a no-op, not a
    # fatal error.
    if [ ! -e "$dst" ] || ! [ "$src" -ef "$dst" ]; then
      cp -f "$src" "$dst"
    fi
  fi
done
if [ ! -f "$WORKLETS_EXPECTED/arm64-v8a/libworklets.so" ]; then
  echo "ERROR: react-native-worklets built no arm64 libworklets.so; cannot build Reanimated release." >&2
  exit 1
fi

# On low-memory workers, materialize the two Node-heavy release outputs in
# isolated Gradle processes. The final bundleRelease consumes them as
# up-to-date inputs, so no Gradle process retains native/Kotlin/DEX state while
# Metro, Hermes, or Expo Updates is resident. Larger builders keep the shorter
# single-invocation path.
if [ "${#LOW_MEMORY_ASSET_GRADLE_ARGS[@]}" -gt 0 ]; then
  echo "Staging React Native release bundle within the low-memory envelope..."
  "$GRADLE" :app:createBundleReleaseJsAndAssets \
    ${YAVER_PLAYSTORE_ABI:+-PreactNativeArchitectures="$YAVER_PLAYSTORE_ABI"} \
    "${LOW_MEMORY_ASSET_GRADLE_ARGS[@]}"

  echo "Staging Expo Updates resources within the low-memory envelope..."
  "$GRADLE" :app:createReleaseUpdatesResources \
    ${YAVER_PLAYSTORE_ABI:+-PreactNativeArchitectures="$YAVER_PLAYSTORE_ABI"} \
    "${LOW_MEMORY_ASSET_GRADLE_ARGS[@]}"
fi

# bundleRelease with the same lint skip CI uses (release-mobile.yml): local
# lintVital* is slow AND downloads ~200 MB of lint jars — neither is needed to
# produce the AAB. YAVER_PLAYSTORE_ABI (e.g. arm64-v8a) restricts the build to
# one ABI via React Native's supported architecture property — a 4x smaller
# native footprint for a disk-constrained machine doing an internal-test build;
# the CI build keeps all ABIs. Do not use android.injected.build.abi here: AGP
# adds android:testOnly="true" to that bundle and Google Play rejects it.
"$GRADLE" bundleRelease \
  ${YAVER_PLAYSTORE_ABI:+-PreactNativeArchitectures="$YAVER_PLAYSTORE_ABI"} \
  "${LOW_MEMORY_GRADLE_ARGS[@]}" \
  -x lint -x lintVitalRelease -x lintVitalAnalyzeRelease

PAYMENT_DEP_REPORT="$(mktemp -t yaver-android-release-deps.XXXXXX)"
if ! "$GRADLE" :app:dependencies --configuration releaseRuntimeClasspath \
  "${LOW_MEMORY_GRADLE_ARGS[@]}" >"$PAYMENT_DEP_REPORT"; then
  rm -f "$PAYMENT_DEP_REPORT"
  echo "ERROR: could not resolve the Android release dependency graph for payment-SDK verification." >&2
  exit 1
fi
"$REPO_ROOT/scripts/check-no-native-payment-sdks.sh" android "$PAYMENT_DEP_REPORT"
rm -f "$PAYMENT_DEP_REPORT"

AAB_PATH="app/build/outputs/bundle/release/app-release.aab"

MERGED_MANIFEST="app/build/intermediates/merged_manifests/release/processReleaseManifest/AndroidManifest.xml"
if [ -f "$MERGED_MANIFEST" ] && grep -q 'android:testOnly="true"' "$MERGED_MANIFEST"; then
  echo "ERROR: release bundle is marked android:testOnly=true; Google Play will reject it." >&2
  echo "Use reactNativeArchitectures for ABI restriction, never android.injected.build.abi." >&2
  exit 1
fi

if [ ! -f "$AAB_PATH" ]; then
  echo "ERROR: AAB not found at $AAB_PATH"
  exit 1
fi

yaver_verify_aab_signer "$AAB_PATH"

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
