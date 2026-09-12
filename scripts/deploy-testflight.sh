#!/bin/bash
set -eo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
UPLOAD=1

# Keep every non-secret archive/export/log artifact under one configurable
# root. This lets a space-constrained release Mac use an attached APFS volume
# without relocating the repo or bypassing the disk floor. The helper probes
# symlink creation because FAT/exFAT can report ample capacity while being
# operationally unusable by Xcode. Upload credentials deliberately remain in
# a short-lived local /tmp directory below and are deleted by the EXIT trap.
# shellcheck source=scripts/apple-artifact-root.sh
. "$ROOT/scripts/apple-artifact-root.sh"
REQUESTED_ARTIFACT_ROOT="${YAVER_IOS_ARTIFACT_ROOT:-}"
if [ -z "$REQUESTED_ARTIFACT_ROOT" ]; then
  REQUESTED_ARTIFACT_ROOT="$(apple_detect_artifact_root /Volumes yaver-ios)" || exit 1
  if [ -n "$REQUESTED_ARTIFACT_ROOT" ]; then
    echo "Detected marked external artifact volume: $REQUESTED_ARTIFACT_ROOT"
  else
    REQUESTED_ARTIFACT_ROOT=/tmp
  fi
fi
apple_prepare_artifact_root "$REQUESTED_ARTIFACT_ROOT"
ARTIFACT_ROOT="$APPLE_ARTIFACT_ROOT"
ARCHIVE_PATH="$ARTIFACT_ROOT/Yaver.xcarchive"
EXPORT_PATH="$ARTIFACT_ROOT/YaverExport"
EXPORT_OPTIONS="$ARTIFACT_ROOT/ExportOptions.plist"
ARCHIVE_LOG="$ARTIFACT_ROOT/arch_full.log"
EXPORT_LOG="$ARTIFACT_ROOT/yaver_export.log"

usage() {
  cat <<'EOF'
Usage: scripts/deploy-testflight.sh [--upload | --build-only]

Archive the existing Yaver iOS project with automatic signing.
  --upload       Export and upload to App Store Connect (the historical and
                 canonical no-argument behavior).
  --build-only   Stop after producing and verifying the signed archive. This is
                 the default used by mobile_platform_deploy upload=false.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --upload) UPLOAD=1 ;;
    --build-only) UPLOAD=0 ;;
    --help|-h) usage; exit 0 ;;
    *) echo "Unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

# Refuse an obsolete SDK before touching generated native state or consuming a
# build number. Apple has rejected iOS SDKs below 26 since 2026-04-28.
# shellcheck source=scripts/apple-xcode-auth.sh
. "$ROOT/scripts/apple-xcode-auth.sh"
apple_require_store_sdk iphoneos 26
apple_ensure_simulator_runtime iOS iphonesimulator

# Target-injection scripts are Node programs and must run only after their
# dependencies exist. On 2026-08-18 this check lived below those scripts, so a
# clean checkout died with a raw "Cannot find module .../node_modules/xcode"
# stack trace even though the canonical deploy already knew how to run npm ci.
ensure_mobile_dependencies() {
  local retry="${1:-0}"
  local mobile_dir="$ROOT/mobile"
  local xcode_package="$mobile_dir/node_modules/xcode/package.json"
  local sqlite_package="$mobile_dir/node_modules/expo-sqlite/package.json"
  local audio_package="$mobile_dir/node_modules/react-native-audio-api/package.json"

  # npm is allowed to hoist transitives. Requiring one physical nested layout
  # made every healthy install look incomplete when is-arrayish lived at the
  # root, so each deploy reran npm ci, erased hydrated native binaries, and
  # downloaded them all again. Probe the operation Node/Metro performs instead.
  local metro_transitive_ok=0
  local reanimated_transitive_ok=0
  local expo_plist_compat_ok=0
  local lockfile_versions_ok=0
  local expo_config_ok=0
  (cd "$mobile_dir" && node -e \
    "const p=require('path'); require.resolve('is-arrayish',{paths:[p.dirname(require.resolve('simple-swizzle/package.json'))]})" \
    >/dev/null 2>&1) && metro_transitive_ok=1
  (cd "$mobile_dir" && node -e \
    "const p=require('path'); require.resolve('semver/functions/satisfies',{paths:[p.dirname(require.resolve('react-native-reanimated/package.json'))]})" \
    >/dev/null 2>&1) && reanimated_transitive_ok=1
  # @xmldom/xmldom 0.9 requires an explicit MIME type while Expo SDK 54's
  # plist reader still calls its one-argument API. The pinned patch preserves
  # xmldom's security fixes and restores Expo's XML-only call contract. Probe
  # the actual parse operation: package presence alone was a false green that
  # let a clean TestFlight deploy die during Expo prebuild.
  (cd "$mobile_dir" && node -e \
    "require('@expo/plist').default.parse('\\n<?xml version=\"1.0\"?><plist version=\"1.0\"><dict/></plist>')" \
    >/dev/null 2>&1) && expo_plist_compat_ok=1
  # A stale node_modules tree can satisfy every file-exists probe while
  # containing a different package than package-lock.json. On 2026-09-12 the
  # lock pinned react-native-vision-camera 4.7.3, node_modules held 5.0.1, and
  # Expo config crashed only inside Xcode's EXConstants phase after a five-minute
  # archive. Compare installed package versions to the lockfile and probe the
  # exact Expo config operation that EXConstants performs.
  (cd "$mobile_dir" && node <<'NODE'
const lock = require('./package-lock.json');
const checked = [
  'react-native-vision-camera',
  'react-native-reanimated',
  'expo',
  'expo-constants',
];
for (const name of checked) {
  const locked = lock.packages?.[`node_modules/${name}`]?.version;
  const installed = require(`./node_modules/${name}/package.json`).version;
  if (!locked || locked !== installed) {
    throw new Error(`${name} installed=${installed || '<missing>'} lockfile=${locked || '<missing>'}`);
  }
}
NODE
  ) >/dev/null 2>&1 && lockfile_versions_ok=1
  (cd "$mobile_dir" && NODE_ENV=production npx expo config --json >/dev/null 2>&1) && expo_config_ok=1

  if [ -f "$xcode_package" ] && [ -f "$sqlite_package" ] && [ -f "$audio_package" ] && \
     [ "$metro_transitive_ok" -eq 1 ] && [ "$reanimated_transitive_ok" -eq 1 ] && \
     [ "$expo_plist_compat_ok" -eq 1 ] && [ "$lockfile_versions_ok" -eq 1 ] && \
     [ "$expo_config_ok" -eq 1 ]; then
    return 0
  fi

  if ! command -v npm >/dev/null 2>&1; then
    echo "ERROR: mobile dependencies are incomplete and npm is unavailable." >&2
    echo "       Install Node.js/npm, then rerun the deploy; Yaver will restore the lockfile." >&2
    exit 1
  fi
  if [ ! -f "$mobile_dir/package-lock.json" ]; then
    echo "ERROR: mobile dependencies are incomplete and mobile/package-lock.json is missing." >&2
    echo "       Restore the tracked lockfile before deploying; refusing an unpinned install." >&2
    exit 1
  fi
  if [ "$retry" = "1" ]; then
    echo "ERROR: mobile dependencies are still inconsistent after npm ci." >&2
    echo "       Check package-lock.json, node_modules package versions, and Expo config plugins." >&2
    echo "       The archive would fail later while generating expo-constants app.config." >&2
    exit 1
  fi

  echo "Mobile native dependencies are incomplete — restoring mobile/package-lock.json with npm ci."
  echo "Install output follows; the deploy will resume automatically when it finishes."
  (cd "$mobile_dir" && npm ci --legacy-peer-deps --no-audit --no-fund)
  ensure_mobile_dependencies 1
}

ensure_mobile_dependencies

# A checkout intentionally carries only the hand-maintained iOS overlays. The
# Podfile, workspace, launch storyboard, assets, and Expo support files are
# generated state. The canonical deploy used to assume some earlier local run
# had produced them, then failed inside restore-ios-splash-storyboard.js with
# "run Expo's iOS prebuild first" — a diagnosis with no route to its fix.
#
# Generate the missing project here, while snapshotting every tracked iOS
# overlay from the CURRENT working tree. This preserves uncommitted native
# work as well as committed files, and lets Expo own only its generated state.
IOS_OVERLAY_SNAPSHOT=""
restore_ios_overlay_snapshot() {
  if [ -n "$IOS_OVERLAY_SNAPSHOT" ] && [ -f "$IOS_OVERLAY_SNAPSHOT" ]; then
    tar -xf "$IOS_OVERLAY_SNAPSHOT" -C "$ROOT"
    rm -f "$IOS_OVERLAY_SNAPSHOT"
    IOS_OVERLAY_SNAPSHOT=""
  fi
}
ensure_generated_ios_project() {
  if [ -f "$ROOT/mobile/ios/Podfile" ] && \
     [ -f "$ROOT/mobile/ios/Yaver/SplashScreen.storyboard" ] && \
     [ -f "$ROOT/mobile/ios/Yaver.xcworkspace/contents.xcworkspacedata" ]; then
    return 0
  fi

  echo "Generated iOS project is incomplete — running Expo prebuild and preserving Yaver's tracked native overlays."
  IOS_OVERLAY_SNAPSHOT="$(mktemp "$ARTIFACT_ROOT/yaver-ios-overlays.XXXXXX.tar")"
  git -C "$ROOT" ls-files -z mobile/ios \
    | tar --null -T - -cf "$IOS_OVERLAY_SNAPSHOT" -C "$ROOT"
  trap restore_ios_overlay_snapshot EXIT HUP INT TERM

  prebuild_rc=0
  # Deploys are non-interactive. We already snapshot and restore every tracked
  # native overlay above, so bypass Expo's dirty-tree confirmation explicitly.
  (cd "$ROOT/mobile" && EXPO_NO_GIT_STATUS=1 npx expo prebuild --platform ios --clean --no-install) || prebuild_rc=$?
  restore_ios_overlay_snapshot
  trap - EXIT HUP INT TERM
  if [ "$prebuild_rc" -ne 0 ]; then
    echo "ERROR: Expo could not regenerate the missing iOS project (exit $prebuild_rc)." >&2
    echo "       Yaver's tracked native overlays were restored; fix the prebuild error and rerun the deploy." >&2
    exit "$prebuild_rc"
  fi
}

ensure_generated_ios_project

# Keep the Apple Watch companion target present in the committed iOS project.
# The phone bridge is injected by Expo prebuild; this target is what makes the
# real paired watch app install alongside the iPhone app.
node "$ROOT/scripts/add-watch-ios-target.js"

# Same deal for the Live Activity widget extension: it is what puts Yaver on the
# CarPlay Dashboard (and the Lock Screen / Dynamic Island / Watch Smart Stack).
# Idempotent — a no-op when the target is already in the committed pbxproj.
node "$ROOT/scripts/add-liveactivity-ios-target.js"

# Embedded-target version guard (2026-08-12 snowball): the two target-injection
# scripts used to rewrite MARKETING_VERSION/CURRENT_PROJECT_VERSION to their
# 1.0.0 scaffold defaults on EVERY run, clobbering the committed 1.18.167 in
# the working tree. The archive then shipped a watch/Live-Activity extension
# versioned 1.0.0 under an app versioned 1.18.167 — and nothing in the deploy
# pipeline said a word. The scripts are fixed to preserve committed versions,
# but a future regression must not ship silently: fail fast when ANY
# MARKETING_VERSION in the pbxproj differs from the app target's, naming the
# fix. This is a version-alignment check, not a store rule.
MARKETING_VERSIONS="$(grep -E '^\s*MARKETING_VERSION = ' "$ROOT/mobile/ios/Yaver.xcodeproj/project.pbxproj" \
  | sed -E 's/.*MARKETING_VERSION = ([^;]+);.*/\1/' | tr -d ' ' | sort -u)"
if [ "$(printf '%s\n' "$MARKETING_VERSIONS" | wc -l | tr -d ' ')" -ne 1 ]; then
  echo "ERROR: embedded-target version drift in project.pbxproj:" >&2
  echo "  $(printf '%s\n' "$MARKETING_VERSIONS" | tr '\n' ' ')" >&2
  echo "  The watch / Live Activity targets must match the app's MARKETING_VERSION." >&2
  echo "  Fix: scripts/add-watch-ios-target.js and add-liveactivity-ios-target.js" >&2
  echo "  must preserve committed versions (they do as of 2026-08-12); restore the" >&2
  echo "  committed pbxproj if a stale script run clobbered it." >&2
  exit 1
fi
APP_MARKETING_VERSION="$(printf '%s\n' "$MARKETING_VERSIONS" | head -1)"

# Fail before CocoaPods work or a build-number bump when the archive cannot
# possibly fit. A cold archive has measured at 5.8 GB of build products; leave
# headroom for module caches and export. A warm cache needs less new space but
# still needs room for the archive and IPA. 2026-08-20: 4.3 GB free looked
# plausible, then the first archive filled APFS while generating the
# AVFoundation PCM after twelve minutes and even the deploy-lease log could no
# longer be written.
if [ -n "${YAVER_IOS_DERIVED_DATA:-}" ]; then
  DERIVED="$YAVER_IOS_DERIVED_DATA"
elif [ "$ARTIFACT_ROOT" != "/private/tmp" ] && [ "$ARTIFACT_ROOT" != "/tmp" ]; then
  DERIVED="$ARTIFACT_ROOT/DerivedData"
else
  DERIVED="$HOME/.yaver/build/ios"
fi
CACHE_STATE="warm"
# A partially-populated DerivedData directory is not proof that the next
# archive is incremental. 2026-08-22: a failed 4.7 GB tree selected the old
# 3 GiB warm threshold; Xcode rebuilt the app target and filled APFS to zero.
# Keep the warm floor high enough for another full app link + archive/export.
MIN_FREE_GIB="${YAVER_IOS_MIN_FREE_GIB_WARM:-10}"
if [ ! -d "$DERIVED/Build" ]; then
  CACHE_STATE="COLD (first archive here — expect the slow one)"
  MIN_FREE_GIB="${YAVER_IOS_MIN_FREE_GIB_COLD:-10}"
fi
mkdir -p "$DERIVED"
AVAILABLE_KB="$(df -Pk "$DERIVED" | awk 'END { print $4 }')"
REQUIRED_KB=$((MIN_FREE_GIB * 1024 * 1024))
if [ -z "$AVAILABLE_KB" ] || ! [[ "$AVAILABLE_KB" =~ ^[0-9]+$ ]]; then
  echo "ERROR: could not measure free disk space for the TestFlight archive." >&2
  echo "       Check: df -h '$DERIVED'" >&2
  exit 1
fi
if [ "$AVAILABLE_KB" -lt "$REQUIRED_KB" ]; then
  AVAILABLE_GIB=$((AVAILABLE_KB / 1024 / 1024))
  echo "ERROR: TestFlight build storage needs at least ${MIN_FREE_GIB} GiB free (${CACHE_STATE}); ${AVAILABLE_GIB} GiB is available at $DERIVED." >&2
  echo "       Inspect Yaver's generated cache: du -sh '$DERIVED' '$ROOT/mobile/ios/build'" >&2
  echo "       Reclaim only reviewed generated artifacts, then rerun ./deploy/deploy.sh ios." >&2
  exit 1
fi
# CocoaPods and tracked generated state still live with the checkout even when
# Xcode products are external. Preserve a small absolute floor there rather
# than pretending an empty USB card makes a completely full source volume safe.
LOCAL_AVAILABLE_KB="$(df -Pk "$ROOT" | awk 'END { print $4 }')"
LOCAL_REQUIRED_KB=$((2 * 1024 * 1024))
if [ -z "$LOCAL_AVAILABLE_KB" ] || ! [[ "$LOCAL_AVAILABLE_KB" =~ ^[0-9]+$ ]] || \
   [ "$LOCAL_AVAILABLE_KB" -lt "$LOCAL_REQUIRED_KB" ]; then
  echo "ERROR: the checkout volume needs at least 2 GiB free for CocoaPods and generated source state." >&2
  echo "       Check: df -h '$ROOT'" >&2
  exit 1
fi

cd "$ROOT/mobile/ios"

"$ROOT/scripts/check-no-native-payment-sdks.sh" source

hydrate_native_dependency_artifacts() {
  local sqlite_dir="$ROOT/mobile/node_modules/expo-sqlite"
  if [ -d "$sqlite_dir/ios" ]; then
    for file in sqlite3.c sqlite3.h; do
      if [ ! -f "$sqlite_dir/ios/$file" ] && [ -f "$sqlite_dir/vendor/sqlite3/$file" ]; then
        echo "Hydrating ExpoSQLite iOS artifact: $file"
        cp "$sqlite_dir/vendor/sqlite3/$file" "$sqlite_dir/ios/$file"
      fi
    done
  fi

  local audio_dir="$ROOT/mobile/node_modules/react-native-audio-api"
  local audio_external="$audio_dir/common/cpp/audioapi/external"
  if [ -d "$audio_dir" ] && [ ! -d "$audio_external/ffmpeg_ios" ]; then
    if [ -x "$audio_dir/scripts/download-prebuilt-binaries.sh" ]; then
      echo "Hydrating RNAudioAPI prebuilt binaries"
      (cd "$audio_dir" && ./scripts/download-prebuilt-binaries.sh)
    else
      echo "ERROR: RNAudioAPI prebuilt binaries are missing and download-prebuilt-binaries.sh is unavailable." >&2
      exit 1
    fi
  fi

  local missing=0
  for path in \
    "$sqlite_dir/ios/sqlite3.c" \
    "$sqlite_dir/ios/sqlite3.h" \
    "$audio_external/ffmpeg_ios" \
    "$audio_external/iphoneos" \
    "$audio_external/iphonesimulator"; do
    if [ ! -e "$path" ]; then
      echo "ERROR: required native deploy artifact missing: $path" >&2
      missing=1
    fi
  done
  [ "$missing" = 0 ] || exit 1
}

node "$ROOT/scripts/restore-ios-share-extension.js"
node "$ROOT/scripts/restore-ios-splash-storyboard.js"
# The native project is generated state, but deploys deliberately do not rerun
# Expo prebuild because it can rewrite the hand-maintained companion targets.
# Reapply config-plugin Podfile repairs directly before CocoaPods regenerates
# Yaver does not use VisionCamera frame processors. A warm CocoaPods cache hid
# the missing optional react-native-worklets-core pod until 2026-08-30; a clean
# deploy then failed before archiving. Keep the generated Podfile aligned with
# app.json even when the project already exists and Expo prebuild is skipped.
node "$ROOT/scripts/configure-vision-camera-podfile.js" "$ROOT/mobile/ios/Podfile"
if ! command -v pod >/dev/null 2>&1; then
  echo "ERROR: CocoaPods is required to regenerate the validated iOS Pods project." >&2
  echo "       Install CocoaPods, then rerun the deploy; the locked install resumes automatically." >&2
  exit 1
fi
# CocoaPods cannot recreate the target of a dangling Pods symlink: it reports
# the misleading deterministic error "File exists @ dir_s_mkdir". Restore the
# explicitly configured directory before invoking it so external-volume builds
# recover after their generated cache is cleared.
# shellcheck source=scripts/apple-pods-node-modules.sh
. "$ROOT/scripts/apple-pods-node-modules.sh"
apple_ensure_pods_directory "$ROOT/mobile/ios/Pods"
# CocoaPods resolves a few locked pods from Git hosts. A one-off network timeout
# used to abort the entire release after npm/prebuild had completed, even though
# retrying the same install immediately reused every downloaded artifact and
# succeeded. Keep deterministic pod errors fail-fast, but make transient fetch
# failures self-healing and preserve the useful error when retries are exhausted.
LANG=en_US.UTF-8 LC_ALL=en_US.UTF-8 \
  bash "$ROOT/scripts/pod-install-with-retry.sh" "$ROOT/mobile/ios"
# A Pods symlink can make CocoaPods spell the same generated provider through
# both checkout-relative and physical-volume paths. Swift rejects those aliases
# as duplicate source filenames. Remove an alias only after proving both paths
# resolve to the identical file; distinct sources fail loudly.
node "$ROOT/scripts/dedupe-expo-modules-provider.mjs" \
  "$ROOT/mobile/ios/Yaver.xcodeproj/project.pbxproj"
"$ROOT/scripts/check-no-native-payment-sdks.sh" ios
hydrate_native_dependency_artifacts

# CocoaPods resolves a relocated Pods symlink to its physical directory when
# generating React Native/Hermes phases. That makes ${PODS_ROOT}/../../node_modules
# point beside the external Pods directory, not at the checkout. A dependency
# restore in the checkout therefore used to pass every preflight and then die
# at archive time with a raw missing with-environment.sh error. Probe the exact
# script the archive executes and self-heal the unambiguous empty-path case.
apple_ensure_pods_node_modules_layout \
  "$ROOT/mobile/ios/Pods" \
  "$ROOT/mobile/node_modules"

# Load secrets from the Yaver vault (project="mobile" + globals). Vault
# values win when present; values not in the vault fall through from the
# parent env. Locally: `yaver vault add APP_STORE_KEY_PATH --project mobile`.
# In CI: just don't put the secret in the vault — GitHub Actions env vars
# pass through unchanged.

# App Store Connect credentials. When provisioned,
# `~/.appstoreconnect/yaver.env` is gitignored and carries all four exports
# (see CLAUDE.md "iOS — TestFlight"); in CI they arrive as GitHub secrets in
# the parent env. Its absence is valid and selects Xcode-managed account auth.
#
# This used to be a fallback behind `yaver vault env`. It is now the source.
# The vault call swallowed its own failure, so a locked vault died further down
# at the APP_STORE_KEY_PATH:? guard reading "secret not set" — naming the wrong
# cause, non-interactively, with no chance to supply a passphrase.
if [ -f "$HOME/.appstoreconnect/yaver.env" ]; then
  apple_source_env_defaults "$HOME/.appstoreconnect/yaver.env"
fi

# API keys are ideal for CI/headless uploads, but an interactive Mac may use
# the Apple account already signed in to Xcode. Partial credentials still fail
# loudly. Without API access, an explicit build number is mandatory so we do
# not guess and collide with an existing TestFlight build.
apple_resolve_team_id "$ROOT/mobile/ios/Yaver.xcodeproj/project.pbxproj"
apple_configure_xcode_auth

# macOS login-password secret for headless login.keychain-db unlocks (the
# single-keychain layout). Owner-only, gitignored, never in CI — the same
# file runner_auth.go reads for the Claude-Code keychain. Sourced here so the
# login-keychain unlock block below can see it without the operator exporting
# it by hand on every deploy.
if [ -f "$HOME/.yaver/local-secrets.env" ]; then
  # shellcheck source=/dev/null
  set -a; source "$HOME/.yaver/local-secrets.env"; set +a
fi

# Headless codesigning: unlock the signing keychain in THIS process.
#
# Why this exists: on a remote/SSH deploy (the Mac mini worker), codesign dies
# with the useless error `errSecInternalComponent` even though
# `security find-identity` happily lists the Distribution cert. Three separate
# things cause it, and you have to fix all three:
#   1. The identity lives in a keychain that is LOCKED. find-identity reads the
#      certificate (public) fine, so it looks healthy — only the private-key
#      access fails. The error names none of this.
#   2. A keychain unlock does NOT reliably survive across SSH invocations, so
#      unlocking in an earlier command and signing in a later one still fails.
#      The unlock has to happen in the same run as the build — i.e. right here.
#   3. Even unlocked, the private key's ACL blocks non-GUI callers unless
#      `set-key-partition-list` has granted `codesign:` access (done once, at
#      keychain-provisioning time).
#
# The GUI-session workarounds people reach for first do not work headlessly:
# `launchctl asuser` needs root, and the login password does not help when the
# cert sits in a *different* keychain with its own password.
#
# Set YAVER_SIGNING_KEYCHAIN (+ _PASSWORD) via the Yaver vault or the gitignored
# ~/.appstoreconnect/yaver.env. Unset = no-op, so local GUI deploys, where the
# login keychain is already unlocked, behave exactly as before.
if [ -n "${YAVER_SIGNING_KEYCHAIN:-}" ]; then
  echo "Unlocking signing keychain: $YAVER_SIGNING_KEYCHAIN"
  if ! security unlock-keychain -p "${YAVER_SIGNING_KEYCHAIN_PASSWORD:?YAVER_SIGNING_KEYCHAIN set but YAVER_SIGNING_KEYCHAIN_PASSWORD is not}" "$YAVER_SIGNING_KEYCHAIN"; then
    echo "ERROR: could not unlock $YAVER_SIGNING_KEYCHAIN — codesign would fail later with the opaque 'errSecInternalComponent'." >&2
    exit 1
  fi
  # Search it first so xcodebuild resolves the Distribution identity from the
  # keychain we just unlocked, not from some other (locked) one that happens to
  # hold the same cert — that shadowing is failure mode #1 above.
  security list-keychains -d user -s "$YAVER_SIGNING_KEYCHAIN" login.keychain
  # No lock-on-sleep: a mini that naps mid-archive must not relock and fail the
  # export an hour into the build.
  security set-keychain-settings -t 100000 -u "$YAVER_SIGNING_KEYCHAIN" || true
fi

# Single-keychain machines (MacBook Air / laptop): the signing identities live
# in login.keychain-db, not a dedicated yaver-ci.keychain-db. In a headless
# (SSH / agent / launchd-Background) session that keychain is LOCKED and
# codesign dies with errSecInternalComponent exactly like a missing key.
# `security find-identity` still lists the certs, so the failure looks like a
# missing cert. Unlock + partition-list in THIS process using the macOS login
# password from ~/.yaver/local-secrets.env (YAVER_LOGIN_PASSWORD, chmod 600,
# owner-only) or the parent env. Same three moves as the Claude-Code keychain
# unlock in runner_auth.go. Unset = no-op (GUI session already has it open).
# Found 2026-08-12: the deploy script only unlocked YAVER_SIGNING_KEYCHAIN, so
# a single-keychain box could never self-unlock headlessly.
LOGIN_KC="${YAVER_LOGIN_KEYCHAIN_PATH:-$HOME/Library/Keychains/login.keychain-db}"
if [ -n "${YAVER_LOGIN_PASSWORD:-}" ]; then
  if [ -f "$LOGIN_KC" ]; then
    echo "Unlocking login keychain (headless codesign): $LOGIN_KC"
    security unlock-keychain -p "$YAVER_LOGIN_PASSWORD" "$LOGIN_KC"
    security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$YAVER_LOGIN_PASSWORD" "$LOGIN_KC" || true
    security set-keychain-settings -t 100000 -u "$LOGIN_KC" || true
  else
    echo "WARN: YAVER_LOGIN_PASSWORD set but login keychain not found at $LOGIN_KC" >&2
  fi
fi

# Deploy lease (AUTORUN_STORE.md §6.1) — refuse if a sibling autorun is already
# deploying TestFlight, so two runs can't race the same archive/upload and burn
# the ~18/day cap (the 2026-07-19 incident this store exists to prevent). Also
# refuses when the daily quota is exhausted. Best-effort: skipped if the yaver
# CLI isn't on PATH. Stable id across acquire/release = this script's pid.
DEPLOY_ID="deploy-testflight-$$"
LEASE_HELD=0
DEPLOY_OUTCOME=failure
run_yaver_bounded() {
  local label="$1"; shift
  local limit="${YAVER_DEPLOY_LEASE_TIMEOUT_SECONDS:-20}"
  local log="$ARTIFACT_ROOT/yaver-${label}-${DEPLOY_ID}.log"
  yaver "$@" >"$log" 2>&1 &
  local pid=$!
  local start=$SECONDS
  while kill -0 "$pid" 2>/dev/null; do
    if [ $((SECONDS - start)) -ge "$limit" ]; then
      echo "WARN: yaver $label timed out after ${limit}s; continuing without blocking deploy teardown." >&2
      kill -TERM "$pid" 2>/dev/null || true
      sleep 1
      kill -KILL "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
      return 124
    fi
    sleep 1
  done
  wait "$pid"
}
if command -v yaver >/dev/null 2>&1; then
  BR="$(git -C "$ROOT" rev-parse --abbrev-ref HEAD 2>/dev/null || echo '')"
  if run_yaver_bounded deploy-lease-acquire autorun deploy-lease acquire --target testflight --autorun "$DEPLOY_ID" --workdir "$ROOT" --branch "$BR"; then
    LEASE_HELD=1
  else
    rc=$?
    [ "$rc" -eq 3 ] && { echo "Another autorun holds the TestFlight deploy lease — aborting to avoid a race."; exit 3; }
    [ "$rc" -eq 4 ] && { echo "TestFlight daily upload quota exhausted — wait a day."; exit 4; }
    echo "WARN: could not acquire deploy lease (rc=$rc) — continuing without coordination."
  fi
fi
release_lease() {
  [ "$LEASE_HELD" = 1 ] && command -v yaver >/dev/null 2>&1 && \
    run_yaver_bounded deploy-lease-release autorun deploy-lease release --target testflight --autorun "$DEPLOY_ID" --outcome "$DEPLOY_OUTCOME" || true
}
trap release_lease EXIT

# Bump build number. YAVER_IOS_BUILD_NUMBER is the explicit escape hatch for
# signed-in-Xcode uploads, where no API credential exists to query ASC.
#
# BUG FIX (2026-07-19): this used to bump from the LOCAL Info.plist only. The
# local number drifts from reality — three autorun clones on one box sat at 445,
# 446, 447 while App Store Connect already had all of 441–447 from a prior day —
# so every upload collided (ITMS "build number already used") and burned a slot
# of the ~18/day TestFlight cap. Bump from max(local, ASC-highest) + 1 so a new
# build can never collide. The ASC query is best-effort: if the crypto libs or
# network aren't there, fall back to local+1 and warn (the export-error handler
# below now surfaces a collision clearly either way).
PLIST="Yaver/Info.plist"
CURRENT_BUILD=$(/usr/libexec/PlistBuddy -c "Print CFBundleVersion" "$PLIST")
BUILD_OVERRIDE="${YAVER_IOS_BUILD_NUMBER:-}"
apple_require_explicit_build_without_api_key YAVER_IOS_BUILD_NUMBER "$BUILD_OVERRIDE"

if [ -n "$BUILD_OVERRIDE" ]; then
  apple_validate_build_number YAVER_IOS_BUILD_NUMBER "$BUILD_OVERRIDE"
  if [ "$BUILD_OVERRIDE" -le "$CURRENT_BUILD" ]; then
    echo "ERROR: YAVER_IOS_BUILD_NUMBER=$BUILD_OVERRIDE must be above local build $CURRENT_BUILD." >&2
    exit 1
  fi
  NEW_BUILD="$BUILD_OVERRIDE"
fi

# This box has SEVERAL python3s (/usr/local, /opt/homebrew, Xcode's), and which
# one answers `python3` depends on PATH order — the same trap mini-deploy.sh
# already pins around for google-auth. On 2026-07-20 the one that answered here
# had no PyJWT, so the ASC lookup returned nothing on every run, the build was
# bumped from local 450 while ASC held 451, and the collision retry burned a slot
# of the ~15-20/day cap. Pick an interpreter that can actually do the query
# rather than the one that happens to be first.
if [ -z "$BUILD_OVERRIDE" ]; then
  ASC_PY=""
  for cand in "${YAVER_PYTHON:-}" python3 /usr/local/bin/python3 /opt/homebrew/bin/python3 /usr/bin/python3; do
    [ -n "$cand" ] || continue
    if command -v "$cand" >/dev/null 2>&1 && "$cand" -c 'import jwt, requests' >/dev/null 2>&1; then
      ASC_PY="$cand"; break
    fi
  done
  if [ -z "$ASC_PY" ]; then
    echo "ERROR: no python3 can import PyJWT+requests, so ASC build-number lookup cannot run." >&2
    echo "       Install PyJWT+requests or set YAVER_IOS_BUILD_NUMBER explicitly." >&2
    exit 1
  fi
  ASC_MAX=$("$ASC_PY" "$ROOT/scripts/asc-max-build.py" || echo "")
  if [ -z "$ASC_MAX" ]; then
    echo "ERROR: ASC max build is unreadable; refusing to guess and consume an upload slot." >&2
    echo "       Set YAVER_IOS_BUILD_NUMBER explicitly after checking TestFlight." >&2
    exit 1
  fi
  if [ "$ASC_MAX" -ge "$CURRENT_BUILD" ] 2>/dev/null; then
    echo "ASC highest build is $ASC_MAX (local $CURRENT_BUILD) — bumping from max"
    NEW_BUILD=$((ASC_MAX + 1))
  else
    NEW_BUILD=$((CURRENT_BUILD + 1))
  fi
fi
# PlistBuddy rewrites the whole plist and DROPS XML COMMENTS. Info.plist
# carries a long comment explaining why NSAllowsArbitraryLoads is set (the
# 100.64/10 CGNAT range that NSAllowsLocalNetworking does not exempt) — that
# reasoning was being silently deleted on every single deploy. Patch the one
# value as text instead so the documentation survives.
python3 - "$PLIST" "$NEW_BUILD" <<'PYEOF'
import re, sys
path, new_build = sys.argv[1], sys.argv[2]
with open(path) as f:
    s = f.read()
s2 = re.sub(r'(<key>CFBundleVersion</key>\s*\n\s*<string>)[^<]*(</string>)',
            lambda m: m.group(1) + new_build + m.group(2), s, count=1)
if s2 == s:
    sys.exit("deploy-testflight: could not patch CFBundleVersion in " + path)
with open(path, "w") as f:
    f.write(s2)
PYEOF
echo "Build $CURRENT_BUILD → $NEW_BUILD"
# Record the real build number in the lease (same id re-acquires + overwrites).
[ "$LEASE_HELD" = 1 ] && command -v yaver >/dev/null 2>&1 && \
  run_yaver_bounded deploy-lease-build autorun deploy-lease acquire --target testflight --autorun "$DEPLOY_ID" --workdir "$ROOT" --build "$NEW_BUILD" || true

# Clean stale archive so a failed build can't silently reuse it
ls -la "$ARCHIVE_PATH" 2>/dev/null || true
rm -rf "$ARCHIVE_PATH"

# DERIVED DATA MUST OUTLIVE /tmp (2026-08-02).
#
# This passed `-derivedDataPath /tmp/YaverBuild`. macOS purges /tmp, so the
# incremental build cache was gone by the next deploy and EVERY archive was a
# cold build: measured at 2,270 compiled files, 5.8 GB of build products, and
# ~45 minutes of wall clock for a change that touched a handful of TS files.
# The script was already the incremental path in every other respect — no
# `clean`, no `prebuild` — so the one flag was the whole cost.
#
# ~/.yaver/build/ios persists, is owner-only, and is outside the repo so it
# can never be swept by a `git clean`. Override with YAVER_IOS_DERIVED_DATA if
# a run genuinely wants a throwaway cache (a signing-cache bisect, say).
# Archive. Run the long-lived pipeline in a subshell so wait observes
# xcodebuild's exit status while the persisted log is credential-redacted.
#
# Do not pipe xcodebuild through tee/tail here. On 2026-08-09 a TestFlight
# deploy spent hours alive but silent inside Xcode's build-log machinery after
# `xcodebuild | tee /tmp/arch_full.log | tail -3`; the terminal looked quiet,
# the archive never materialized, and the EXIT trap then hung trying to release
# the lease. Write directly to the log, print bounded heartbeats, and fail
# loudly when the log stops moving.
echo "Archiving... (derived data: $DERIVED — $CACHE_STATE)"
: > "$ARCHIVE_LOG"
# Pods may live on another volume. In that layout the generated React Native
# bundle phase looks for .xcode.env beside the PHYSICAL Pods directory rather
# than beside this checkout, so NODE_BINARY arrives empty and the phase tries
# to execute an empty command. Export the exact Node executable into
# xcodebuild's environment as the authoritative fallback; the versioned
# .xcode.env still covers ordinary Xcode launches.
NODE_BINARY="${NODE_BINARY:-$(command -v node || true)}"
if [ -z "$NODE_BINARY" ] || [ ! -x "$NODE_BINARY" ]; then
  echo "ERROR: Node.js is required by the React Native archive bundle phase." >&2
  exit 1
fi
export NODE_BINARY
# Bound Xcode's compile fan-out on memory-constrained local release Macs. The
# canonical TestFlight lane runs on an 8 GB machine as well as larger builders;
# unconstrained xcodebuild can create enough concurrent Clang/Swift workers to
# force sustained swap and amplify SSD pressure. Allow an explicit override,
# while keeping a conservative default on machines with 8 GiB or less.
XCODE_JOBS_ARGS=()
XCODE_JOBS="${YAVER_IOS_XCODE_JOBS:-}"
if [ -z "$XCODE_JOBS" ]; then
  HOST_MEMORY_BYTES="$(sysctl -n hw.memsize 2>/dev/null || echo 0)"
  if [ "$HOST_MEMORY_BYTES" -gt 0 ] 2>/dev/null && \
     [ "$HOST_MEMORY_BYTES" -le $((8 * 1024 * 1024 * 1024)) ]; then
    XCODE_JOBS=2
  fi
fi
if [ -n "$XCODE_JOBS" ]; then
  if ! [[ "$XCODE_JOBS" =~ ^[1-9][0-9]*$ ]]; then
    echo "ERROR: YAVER_IOS_XCODE_JOBS must be a positive integer; got '$XCODE_JOBS'." >&2
    exit 1
  fi
  XCODE_JOBS_ARGS=(-jobs "$XCODE_JOBS")
  echo "Xcode parallel jobs: $XCODE_JOBS"
fi
(
  set +e
  xcodebuild -workspace Yaver.xcworkspace -scheme Yaver -configuration Release \
    -destination 'generic/platform=iOS' \
    -archivePath "$ARCHIVE_PATH" archive \
    DEVELOPMENT_TEAM="${APPLE_TEAM_ID:?Set APPLE_TEAM_ID}" CODE_SIGN_STYLE=Automatic \
    MARKETING_VERSION="$APP_MARKETING_VERSION" CURRENT_PROJECT_VERSION="$NEW_BUILD" \
    CODE_SIGN_ALLOW_ENTITLEMENTS_MODIFICATION=YES \
    ENABLE_USER_SCRIPT_SANDBOXING=NO -allowProvisioningUpdates \
    ${APPLE_XCODE_AUTH_ARGS[@]+"${APPLE_XCODE_AUTH_ARGS[@]}"} \
    ${XCODE_JOBS_ARGS[@]+"${XCODE_JOBS_ARGS[@]}"} \
    -derivedDataPath "$DERIVED" 2>&1 | apple_redact_xcode_auth_output
  exit "${PIPESTATUS[0]}"
) >"$ARCHIVE_LOG" &
ARCHIVE_PID=$!
ARCHIVE_STARTED=$SECONDS
ARCHIVE_LAST_SIZE=0
ARCHIVE_LAST_PROGRESS=$SECONDS
ARCHIVE_SILENCE_LIMIT="${YAVER_IOS_ARCHIVE_SILENCE_TIMEOUT_SECONDS:-900}"
while kill -0 "$ARCHIVE_PID" 2>/dev/null; do
  sleep 30
  ARCHIVE_SIZE=$(stat -f%z "$ARCHIVE_LOG" 2>/dev/null || echo 0)
  if [ "$ARCHIVE_SIZE" != "$ARCHIVE_LAST_SIZE" ]; then
    ARCHIVE_LAST_SIZE="$ARCHIVE_SIZE"
    ARCHIVE_LAST_PROGRESS=$SECONDS
    echo "Archive still running ($((SECONDS - ARCHIVE_STARTED))s, log ${ARCHIVE_SIZE} bytes). Last lines:"
    tail -3 "$ARCHIVE_LOG" || true
  elif [ $((SECONDS - ARCHIVE_LAST_PROGRESS)) -ge "$ARCHIVE_SILENCE_LIMIT" ]; then
    echo "ERROR: xcodebuild archive produced no log output for ${ARCHIVE_SILENCE_LIMIT}s." >&2
    echo "       This usually means Xcode/SwiftBuild is wedged, not that compilation is progressing." >&2
    echo "       Last archive log lines:" >&2
    tail -20 "$ARCHIVE_LOG" >&2 || true
    kill -TERM "$ARCHIVE_PID" 2>/dev/null || true
    sleep 5
    kill -KILL "$ARCHIVE_PID" 2>/dev/null || true
    wait "$ARCHIVE_PID" 2>/dev/null || true
    exit 1
  else
    echo "Archive still running ($((SECONDS - ARCHIVE_STARTED))s, no new log for $((SECONDS - ARCHIVE_LAST_PROGRESS))s)."
  fi
done
set +e
wait "$ARCHIVE_PID"
ARCHIVE_EXIT=$?
set -e
if [ "$ARCHIVE_EXIT" -ne 0 ]; then
  echo "ERROR: Archive failed (exit $ARCHIVE_EXIT). Last archive log lines:" >&2
  tail -40 "$ARCHIVE_LOG" >&2 || true
  if grep -q "doesn't match the entitlements file's value for the com.apple.security.application-groups entitlement" "$ARCHIVE_LOG"; then
    echo "DIAGNOSIS: the selected provisioning profile has no assigned App Group." >&2
    echo "  Assign group.io.yaver.mobile to the app, Watch app, and Share Extension" >&2
    echo "  identifiers in Apple Developer, then regenerate/download their profiles." >&2
    echo "  A profile merely existing is a false green; its application-groups array" >&2
    echo "  must contain the exact group declared by the entitlements file." >&2
  fi
  if grep -q "No Accounts: Add a new account in Accounts settings" "$ARCHIVE_LOG"; then
    echo "DIAGNOSIS: Xcode has no usable Apple account token for provisioning." >&2
    echo "  A browser session and an Apple ID listed in Xcode preferences do not prove" >&2
    echo "  signing auth. Open Xcode > Settings > Accounts and sign in/refresh the" >&2
    echo "  SIMKAB team account, or configure a complete App Store Connect API key." >&2
  fi
  if grep -q 'ShareExtension.entitlements.*could not be opened' "$ARCHIVE_LOG"; then
    echo "DIAGNOSIS: expo-share-intent's generated iOS source directory is absent." >&2
    echo "  scripts/restore-ios-share-extension.js should recreate it before archive;" >&2
    echo "  rerun that script and treat another miss as a generator regression." >&2
  fi
  if grep -q "deployment target.*18.4.*supported deployment target.*18.2" "$ARCHIVE_LOG"; then
    echo "DIAGNOSIS: this Xcode SDK predates iOS 18.4." >&2
    echo "  Yaver's widget source must compile its pre-18.4 fallback on this toolchain," >&2
    echo "  or Xcode must be upgraded before the CarPlay supplemental family is enabled." >&2
  fi
  exit "$ARCHIVE_EXIT"
fi

# Verify archive was created
if [ ! -d "$ARCHIVE_PATH" ]; then
  echo "ERROR: Archive failed — no .xcarchive produced"
  exit 1
fi

# Inspect the archive that will actually be exported, not the project settings
# that were supposed to produce it. App Store Connect rejects embedded bundles
# whose marketing/build versions drift from their containing app; historically
# the Watch and Live Activity plists hardcoded build 1 while the phone had
# already reached the hundreds. Fail before export so a bad archive cannot
# consume an upload slot.
ARCHIVE_APP="$ARCHIVE_PATH/Products/Applications/Yaver.app"
ARCHIVE_BUNDLES=(
  "$ARCHIVE_APP"
  "$ARCHIVE_APP/PlugIns/YaverActivity.appex"
  "$ARCHIVE_APP/PlugIns/ShareExtension.appex"
  # The Watch target product is deliberately YaverWatch.app. Naming it
  # Yaver.app collides with the containing iOS product in Xcode's mixed
  # archive graph; the old validator path produced a false failure after a
  # fully successful archive and blocked the upload.
  "$ARCHIVE_APP/Watch/YaverWatch.app"
)
for bundle in "${ARCHIVE_BUNDLES[@]}"; do
  if [ ! -d "$bundle" ]; then
    echo "ERROR: required embedded bundle is missing from archive: $bundle" >&2
    exit 1
  fi
  bundle_version=$(/usr/libexec/PlistBuddy -c "Print CFBundleShortVersionString" "$bundle/Info.plist")
  bundle_build=$(/usr/libexec/PlistBuddy -c "Print CFBundleVersion" "$bundle/Info.plist")
  bundle_id=$(/usr/libexec/PlistBuddy -c "Print CFBundleIdentifier" "$bundle/Info.plist")
  if [ "$bundle_version" != "$APP_MARKETING_VERSION" ] || [ "$bundle_build" != "$NEW_BUILD" ]; then
    echo "ERROR: archive version drift in $bundle_id: version=$bundle_version build=$bundle_build" >&2
    echo "       Expected version=$APP_MARKETING_VERSION build=$NEW_BUILD for every embedded bundle." >&2
    exit 1
  fi
  echo "Archive bundle verified: $bundle_id $bundle_version ($bundle_build)"
done

if [ "$UPLOAD" != "1" ]; then
  DEPLOY_OUTCOME=success
  echo "✓ Signed iOS archive ready (build-only): $ARCHIVE_PATH"
  exit 0
fi

# ExportOptions (no single-quote on EOF so APPLE_TEAM_ID expands)
# uploadSymbols=false: rnwhisper framework has missing dSYMs that
# Xcode 15+ treats as a fatal export error. Crash reports still work
# from Apple's symbolication — we just skip uploading our local dSYMs.
#
# API-key deploys export locally, then validate/upload with altool below. tvOS
# and visionOS already use this lane. On 2026-08-20 the same API key could query
# App Store Connect and archive/provision all four iOS-family bundles, but
# Xcode's destination=upload session returned NOT_AUTHORIZED before it even
# exported the IPA. Authentication inventory was green; the upload operation
# was not. altool signs its own bearer token from the key and avoids that
# expirable Xcode session. Xcode-account deploys still need destination=upload.
EXPORT_DESTINATION="upload"
if [ "$APPLE_XCODE_AUTH_MODE" = "api-key" ]; then
  EXPORT_DESTINATION="export"
fi
cat > "$EXPORT_OPTIONS" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>method</key><string>app-store-connect</string>
    <key>teamID</key><string>${APPLE_TEAM_ID}</string>
    <key>signingStyle</key><string>automatic</string>
    <key>destination</key><string>${EXPORT_DESTINATION}</string>
    <key>uploadSymbols</key><false/>
</dict>
</plist>
EOF

# Export, then upload. Xcode-account mode uploads during export; API-key mode
# produces an IPA which is validated and uploaded with altool below.
#
# BUG FIX (2026-07-19): this used to be `EXPORT_OUTPUT=$(xcodebuild …)`. Under
# `set -eo pipefail` (line 2), a FAILED command substitution aborts the script
# AT that assignment — so the diagnostic below and the "Redundant Binary Upload"
# tolerance were unreachable dead code, and every real failure surfaced as a
# bare `exit 70/65` with no message. That masked a full day of debugging (the
# actual cause was a build-number collision + a locked signing keychain).
# Stream to a log with `set +e` so the exit code is captured AND the error is
# visible.
echo "Exporting & uploading..."
# Xcode refuses or can leave stale products when the export directory already
# exists. It is generated and disposable, but list the exact path before the
# bounded cleanup so a path regression is visible in the deploy log.
ls -la "$EXPORT_PATH" 2>/dev/null || true
if [ -e "$EXPORT_PATH" ]; then
  find "$EXPORT_PATH" -depth -delete
fi
set +e
xcodebuild -exportArchive -archivePath "$ARCHIVE_PATH" \
  -exportOptionsPlist "$EXPORT_OPTIONS" \
  -exportPath "$EXPORT_PATH" -allowProvisioningUpdates \
  ${APPLE_XCODE_AUTH_ARGS[@]+"${APPLE_XCODE_AUTH_ARGS[@]}"} 2>&1 | \
  apple_redact_xcode_auth_output | tee "$EXPORT_LOG"
EXPORT_EXIT=${PIPESTATUS[0]}
set -e

# Success = exit 0, OR "Redundant Binary" (build already uploaded — treat as ok).
if [ "$EXPORT_EXIT" -ne 0 ] && ! grep -q "Redundant Binary Upload" "$EXPORT_LOG"; then
  echo "ERROR: Export/upload failed (exit $EXPORT_EXIT). Diagnosis:"
  # Surface the two most common real causes explicitly (learned 2026-07-19):
  if grep -q "errSecInternalComponent" "$EXPORT_LOG"; then
    echo "  → codesign errSecInternalComponent: a signing keychain is LOCKED to this"
    echo "    (headless) session. Unlock BOTH yaver-ci.keychain-db and login.keychain-db"
    echo "    + set-key-partition-list. See CLAUDE.md → 'Headless codesign'."
  fi
  if grep -qiE "bundle version.*already|redundant|The build.*has already been used|ITMS-4238" "$EXPORT_LOG"; then
    echo "  → build number $NEW_BUILD collides with an existing App Store Connect build."
    echo "    Bump CFBundleVersion above the ASC max and retry."
  fi
  grep -iE "error|errSec|EXPORT FAILED|ITMS-" "$EXPORT_LOG" | tail -8
  exit 1
fi

if [ "$APPLE_XCODE_AUTH_MODE" = "api-key" ]; then
  IPA_PATH="$(find "$EXPORT_PATH" -maxdepth 1 -type f -name '*.ipa' -print -quit)"
  if [ -z "$IPA_PATH" ]; then
    echo "ERROR: Xcode export succeeded but produced no iOS IPA under $EXPORT_PATH." >&2
    exit 1
  fi

  # altool discovers API keys by filename under ./private_keys. Keep that
  # compatibility directory owner-only and ephemeral; never copy a private key
  # into the repository or a persistent shared location.
  UPLOAD_AUTH_DIR="$(mktemp -d /tmp/yaver-ios-asc.XXXXXX)"
  chmod 700 "$UPLOAD_AUTH_DIR"
  mkdir -m 700 "$UPLOAD_AUTH_DIR/private_keys"
  cp "$APP_STORE_KEY_PATH" "$UPLOAD_AUTH_DIR/private_keys/AuthKey_${APP_STORE_KEY_ID}.p8"
  chmod 600 "$UPLOAD_AUTH_DIR/private_keys/AuthKey_${APP_STORE_KEY_ID}.p8"
  cleanup_ios_upload_auth() {
    find "$UPLOAD_AUTH_DIR" -depth -delete 2>/dev/null || true
  }
  trap 'cleanup_ios_upload_auth; release_lease' EXIT

  echo "Validating iOS IPA with App Store Connect…"
  (cd "$UPLOAD_AUTH_DIR" && xcrun altool --validate-app --file "$IPA_PATH" \
    --type ios --apiKey "$APP_STORE_KEY_ID" --apiIssuer "$APP_STORE_KEY_ISSUER")
  echo "Uploading iOS IPA to TestFlight…"
  (cd "$UPLOAD_AUTH_DIR" && xcrun altool --upload-app --file "$IPA_PATH" \
    --type ios --apiKey "$APP_STORE_KEY_ID" --apiIssuer "$APP_STORE_KEY_ISSUER")
fi

DEPLOY_OUTCOME=success   # the trap releases the lease with this outcome + quota++
echo "✓ TestFlight build $NEW_BUILD uploaded"

# The shared cache helper is installed outside the repo and is not guaranteed to
# be on PATH in GUI/headless deploy sessions. Resolve its documented fallback
# explicitly so a successful upload never ends with a misleading command-not-
# found error.
CACHE_CLEANUP="$(command -v mobile-cache-cleanup.sh 2>/dev/null || true)"
if [ -z "$CACHE_CLEANUP" ] && [ -x "$HOME/.local/bin/mobile-cache-cleanup.sh" ]; then
  CACHE_CLEANUP="$HOME/.local/bin/mobile-cache-cleanup.sh"
fi
if [ -n "$CACHE_CLEANUP" ]; then
  "$CACHE_CLEANUP" mark-deployed yaver || true
else
  echo "Note: mobile cache cleanup helper is not installed; upload is complete."
fi
