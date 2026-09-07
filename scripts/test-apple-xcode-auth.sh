#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=scripts/apple-xcode-auth.sh
. "$ROOT/scripts/apple-xcode-auth.sh"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

ENV_FIXTURE="$(mktemp /tmp/yaver-apple-auth-env.XXXXXX)"
cat >"$ENV_FIXTURE" <<'EOF'
APP_STORE_KEY_PATH=/env/default/key.p8
APP_STORE_KEY_ID=ENVKEY
APP_STORE_KEY_ISSUER=ENVISSUER
APPLE_TEAM_ID=ENVTEAM1234
EOF
APP_STORE_KEY_PATH=/caller/key.p8
APP_STORE_KEY_ID=CALLERKEY
unset APP_STORE_KEY_ISSUER APPLE_TEAM_ID
apple_source_env_defaults "$ENV_FIXTURE"
[ "$APP_STORE_KEY_PATH" = /caller/key.p8 ] || fail "env defaults must not replace an explicit key path"
[ "$APP_STORE_KEY_ID" = CALLERKEY ] || fail "env defaults must not replace an explicit key id"
[ "$APP_STORE_KEY_ISSUER" = ENVISSUER ] || fail "env defaults should fill a missing issuer"
[ "$APPLE_TEAM_ID" = ENVTEAM1234 ] || fail "env defaults should fill a missing team id"
find "$ENV_FIXTURE" -delete

unset APP_STORE_KEY_PATH APP_STORE_KEY_ID APP_STORE_KEY_ISSUER APPLE_TEAM_ID
apple_configure_xcode_auth >/dev/null
[ "$APPLE_XCODE_AUTH_MODE" = "xcode-account" ] || fail "empty credentials should use Xcode account"
[ "${#APPLE_XCODE_AUTH_ARGS[@]}" -eq 0 ] || fail "Xcode account mode should add no auth flags"

APP_STORE_KEY_ID="partial"
if apple_configure_xcode_auth >/dev/null 2>&1; then
  fail "partial API credentials must fail"
fi

APP_STORE_KEY_PATH="/definitely/missing/yaver-auth-key.p8"
APP_STORE_KEY_ISSUER="issuer"
if apple_configure_xcode_auth >/dev/null 2>&1; then
  fail "an unreadable API key path must fail"
fi

AUTH_FIXTURE="$(mktemp /tmp/yaver-apple-auth-key.XXXXXX)"
printf '%s\n' 'test-key' >"$AUTH_FIXTURE"
BLOCKED_AUTH_FIXTURE="${AUTH_FIXTURE}.fifo"
mkfifo "$BLOCKED_AUTH_FIXTURE"
trap 'rm -f "$AUTH_FIXTURE" "$BLOCKED_AUTH_FIXTURE"' EXIT
APP_STORE_KEY_PATH="$AUTH_FIXTURE"
apple_configure_xcode_auth >/dev/null
[ "$APPLE_XCODE_AUTH_MODE" = "api-key" ] || fail "complete credentials should use API-key mode"
[ "${#APPLE_XCODE_AUTH_ARGS[@]}" -eq 6 ] || fail "API-key mode should emit six xcodebuild arguments"

APP_STORE_KEY_PATH="$BLOCKED_AUTH_FIXTURE"
APPLE_KEY_READ_TIMEOUT_SECONDS=1
if apple_configure_xcode_auth >/dev/null 2>&1; then
  fail "a readable path whose contents block must fail the operational key probe"
fi
unset APPLE_KEY_READ_TIMEOUT_SECONDS
APP_STORE_KEY_PATH="$AUTH_FIXTURE"

AUTH_BANNER='xcodebuild -authenticationKeyPath /private/AuthKey_SECRET.p8 -authenticationKeyID SECRETID -authenticationKeyIssuerID SECRET-ISSUER archive'
REDACTED_BANNER="$(printf '%s\n' "$AUTH_BANNER" | apple_redact_xcode_auth_output)"
case "$REDACTED_BANNER" in
  *SECRET*) fail "Xcode auth output redactor must remove key paths and credential metadata" ;;
esac
[ "$(printf '%s\n' "$REDACTED_BANNER" | grep -o '<redacted>' | wc -l | tr -d ' ')" -eq 3 ] || \
  fail "Xcode auth output redactor must replace all three authentication values"
printf '%s\n' "$REDACTED_BANNER" | grep -q 'xcodebuild .* archive' || \
  fail "Xcode auth output redactor must preserve useful build diagnostics"

unset APPLE_TEAM_ID
apple_resolve_team_id "$ROOT/mobile/ios/Yaver.xcodeproj/project.pbxproj"
[ "${#APPLE_TEAM_ID}" -eq 10 ] || fail "iOS team ID was not derived"
IOS_TEAM="$APPLE_TEAM_ID"
unset APPLE_TEAM_ID
apple_resolve_team_id "$ROOT/tvos/project.yml"
[ "$APPLE_TEAM_ID" = "$IOS_TEAM" ] || fail "tvOS and iOS team IDs differ"

APPLE_XCODE_AUTH_MODE="xcode-account"
if apple_require_explicit_build_without_api_key TVOS_BUILD_NUMBER "" >/dev/null 2>&1; then
  fail "Xcode account mode must reject an absent explicit build"
fi
apple_require_explicit_build_without_api_key TVOS_BUILD_NUMBER 42

apple_validate_build_number TEST_BUILD 42
if apple_validate_build_number TEST_BUILD nope >/dev/null 2>&1; then
  fail "non-numeric builds must fail"
fi

apple_require_working_xcode \
  /Applications/Xcode.app/Contents/Developer \
  0 \
  /Applications/Xcode.app/Contents/Developer/usr/bin/git
if apple_require_working_xcode \
  /Applications/Xcode.app/Contents/Developer \
  134 \
  'dlopen(@rpath/libxcodebuildLoader.dylib): Symbol not found: _XPCTypeBool' \
  >/dev/null 2>&1; then
  fail "an Xcode runtime loader crash must fail before the SDK/version check"
fi
if apple_require_working_xcode \
  /Library/Developer/CommandLineTools \
  0 \
  /Library/Developer/CommandLineTools/usr/bin/git \
  >/dev/null 2>&1; then
  fail "standalone Command Line Tools must not pass as full Xcode"
fi

for apple_deploy in deploy-tvos.sh deploy-visionos.sh deploy-watchos.sh \
  deploy-macos-testflight.sh deploy-carplay.sh; do
  if ! grep -q '^apple_require_working_xcode$' "$ROOT/scripts/$apple_deploy"; then
    fail "$apple_deploy must run the operational Xcode probe before its build"
  fi
done

# Every successful option branch must consume its argument. The tvOS upload
# lane once spun forever at 100% CPU because --upload set its flag but left $1
# unchanged, so the preflight/build never began and emitted no useful status.
tvos_deploy="$ROOT/scripts/deploy-tvos.sh"
grep -Fq '*/Xcode*.app/Contents/Developer/usr/bin/xcodebuild)' "$tvos_deploy" || \
  fail "deploy-tvos.sh must accept versioned full-Xcode app names used by CI"
grep -q -- '--upload) UPLOAD=1; shift ;;' "$tvos_deploy" || \
  fail "deploy-tvos.sh --upload must advance the argument parser"
grep -q -- '--simulator) SIM_MODE=1; shift ;;' "$tvos_deploy" || \
  fail "deploy-tvos.sh --simulator must advance the argument parser"
grep -q 'A physical Apple TV is not required' "$tvos_deploy" || \
  fail "tvOS signing failure must route TestFlight uploads to an App Store profile"
grep -q 'APPLE_XCODE_AUTH_MODE.*api-key' "$tvos_deploy" || \
  fail "tvOS API-key deploys must avoid the expirable Xcode account upload session"
grep -q -- '--type appletvos --apiKey' "$tvos_deploy" || \
  fail "tvOS API-key deploys must validate/upload the exported IPA with altool"
grep -q 'PACKAGE_AUTH_SETTINGS=(-packageAuthorizationProvider netrc -scmProvider system)' "$tvos_deploy" || \
  fail "tvOS public package resolution must not block on the login keychain in headless deploys"
tvos_agent_client="$ROOT/tvos/YaverTV/AgentClient.swift"
create_task_signature="$(sed -n '/func createTask(/,/async throws -> TaskSummary/p' "$tvos_agent_client")"
printf '%s\n' "$create_task_signature" | grep -q 'sessionStartedFrom: String = "tasks"' || \
  fail "tvOS createTask must accept and default the Yaver session origin"

# A clean mobile checkout has no node_modules. Dependency self-healing must run
# before either Node-based target injector, and must include their xcode module.
testflight_script="$ROOT/scripts/deploy-testflight.sh"
ensure_line="$(grep -n '^ensure_mobile_dependencies$' "$testflight_script" | head -1 | cut -d: -f1)"
watch_inject_line="$(grep -n '^node .*add-watch-ios-target.js' "$testflight_script" | head -1 | cut -d: -f1)"
[ -n "$ensure_line" ] && [ -n "$watch_inject_line" ] && [ "$ensure_line" -lt "$watch_inject_line" ] || \
  fail "mobile dependencies must be restored before Watch target injection"
grep -q 'node_modules/xcode/package.json' "$testflight_script" || \
  fail "mobile dependency preflight must verify the xcode module used by target injection"
grep -q "require.resolve('is-arrayish'" "$testflight_script" || \
  fail "mobile dependency preflight must resolve the transitive module Metro loads during archive"
grep -q "require.resolve('semver/functions/satisfies'" "$testflight_script" || \
  fail "mobile dependency preflight must resolve Reanimated's Metro dependency"
grep -q -- "-destination 'generic/platform=iOS'" "$testflight_script" || \
  fail "iOS archive must target a generic iOS device, never the CI runner's My Mac destination"
node_export_line="$(grep -n '^export NODE_BINARY$' "$testflight_script" | head -1 | cut -d: -f1)"
archive_line="$(grep -n '^  xcodebuild -workspace Yaver.xcworkspace' "$testflight_script" | head -1 | cut -d: -f1)"
[ -n "$node_export_line" ] && [ -n "$archive_line" ] && [ "$node_export_line" -lt "$archive_line" ] || \
  fail "TestFlight deploy must export NODE_BINARY before xcodebuild so relocated Pods can bundle React Native"
grep -q 'NODE_BINARY.*command -v node' "$ROOT/mobile/ios/.xcode.env" || \
  fail "the versioned Xcode environment must resolve Node dynamically"
if grep -q '/Users/' "$ROOT/mobile/ios/.xcode.env"; then
  fail "the versioned Xcode environment must not pin one developer's home directory"
fi
grep -q -- "-destination 'generic/platform=iOS'" "$ROOT/.github/workflows/release-mobile.yml" || \
  fail "Release Mobile CI must target generic iOS before automatic provisioning"
grep -q 'runs-on: macos-26' "$ROOT/.github/workflows/release-mobile.yml" || \
  fail "Release Mobile CI must use a runner with Apple's required iOS 26 SDK"
grep -q 'apple_require_store_sdk iphoneos 26' "$ROOT/.github/workflows/release-mobile.yml" || \
  fail "Release Mobile CI must reject a stale Store SDK before dependency install and archive"
grep -q 'xcrun simctl bootstatus "$SIM_UDID" -b' "$ROOT/.github/workflows/test-suite.yml" || \
  fail "iOS simulator smoke must wait for a cold simulator to finish booting"
grep -q '"bootstatus", udid, "-b"' "$ROOT/desktop/agent/testkit/driver_iossim.go" || \
  fail "the reusable iOS simulator driver must wait for boot readiness"
grep -q 'git restore --source=HEAD --worktree -- mobile/ios' "$ROOT/.github/workflows/release-mobile.yml" || \
  fail "Release Mobile CI must restore tracked native overlays after Expo clean prebuild"
grep -q 'cp mobile/sdk-manifest.json mobile/ios/Yaver/sdk-manifest.json' "$ROOT/.github/workflows/release-mobile.yml" || \
  fail "Release Mobile CI must restore the generated SDK manifest after Expo clean prebuild"
grep -q 'node scripts/add-watch-ios-target.js' "$ROOT/.github/workflows/release-mobile.yml" || \
  fail "Release Mobile CI must restore the Watch target after Expo clean prebuild"
grep -q 'node scripts/add-liveactivity-ios-target.js' "$ROOT/.github/workflows/release-mobile.yml" || \
  fail "Release Mobile CI must restore the Live Activity target after Expo clean prebuild"
grep -q 'APPLE_XCODE_AUTH_MODE.*api-key' "$testflight_script" || \
  fail "iOS API-key deploys must avoid the expirable Xcode account upload session"
grep -q 'EXPORT_DESTINATION="export"' "$testflight_script" || \
  fail "iOS API-key deploys must export locally before App Store authentication"
[ "$(grep -c -- '--type ios --apiKey' "$testflight_script")" -eq 2 ] || \
  fail "iOS API-key deploys must validate/upload the exported IPA with altool"
grep -q 'Watch/YaverWatch.app' "$testflight_script" || \
  fail "iOS archive validation must check the collision-free YaverWatch product name"
if grep -q 'Watch/Yaver.app' "$testflight_script"; then
  fail "iOS archive validation must not regress to the colliding Yaver.app Watch product"
fi

# Xcode echoes its full invocation, including API-key flags. Every Apple build
# lane that supplies those flags must filter the stream before it reaches a
# terminal or persistent log.
for script_and_count in \
  'deploy-testflight.sh:2' \
  'deploy-tvos.sh:3' \
  'deploy-visionos.sh:2' \
  'deploy-watchos.sh:2'; do
  apple_script="${script_and_count%:*}"
  expected_redactors="${script_and_count##*:}"
  actual_redactors="$(grep -c 'apple_redact_xcode_auth_output' "$ROOT/scripts/$apple_script")"
  [ "$actual_redactors" -eq "$expected_redactors" ] || \
    fail "$apple_script must redact every Xcode authentication invocation"
done

apple_ensure_simulator_runtime \
  watchOS watchsimulator \
  'watchOS 26.5 (26.5 - 23T570) - com.apple.CoreSimulator.SimRuntime.watchOS-26-5' \
  26.5 23T570
# Compatible SDK/runtime builds need not be identical. Xcode 26.6 pairs the
# iOS 23F81a SDK with the iOS 23F77 runtime.
apple_ensure_simulator_runtime \
  iOS iphonesimulator \
  'iOS 26.5 (26.5 - 23F77) - com.apple.CoreSimulator.SimRuntime.iOS-26-5' \
  26.5 23F81a
# Xcode calls the registered runtime visionOS even though its identifier is
# xrOS. This registered profile is the capability actool actually consumes.
apple_ensure_simulator_runtime \
  visionOS xrsimulator \
  'visionOS 26.5 (26.5 - 23O470) - com.apple.CoreSimulator.SimRuntime.xrOS-26-5' \
  26.5 23O469
# A disk-image label is only inventory. This exact false green occurred while
# actool reported "No available simulator runtimes for platform xrsimulator".
if YAVER_SKIP_XCODE_PLATFORM_DOWNLOAD=1 apple_ensure_simulator_runtime \
  visionOS xrsimulator \
  'xrOS 26.5 (23O470) - 855BBF93-1BD0-4091-A27E-E4CAE0004E4E (Ready)' \
  26.5 23O469 >/dev/null 2>&1; then
  fail "a Ready disk image without a registered visionOS runtime must not pass"
fi
if YAVER_SKIP_XCODE_PLATFORM_DOWNLOAD=1 apple_ensure_simulator_runtime \
  visionOS xrsimulator \
  'xrOS 26.5 (23O470) - B6D5690F-054C-4E67-9843-1B7BD0B470E7 (Unusable - Other Failure)' \
  26.5 23O469 >/dev/null 2>&1; then
  fail "an unusable visionOS disk image must not pass"
fi
if YAVER_SKIP_XCODE_PLATFORM_DOWNLOAD=1 apple_ensure_simulator_runtime \
  iOS iphonesimulator \
  'iOS 26.5 (26.5 - 23F77) - com.apple.CoreSimulator.SimRuntime.iOS-26-5 (unavailable, runtime profile not found)' \
  26.5 23F81a >/dev/null 2>&1; then
  fail "an unavailable matching-version runtime must not pass"
fi
if YAVER_SKIP_XCODE_PLATFORM_DOWNLOAD=1 apple_ensure_simulator_runtime \
  watchOS watchsimulator \
  'watchOS 11.2 (11.2 - 22S99) - com.apple.CoreSimulator.SimRuntime.watchOS-11-2' \
  26.5 23T570 >/dev/null 2>&1; then
  fail "a stale simulator runtime must not pass for a newer Xcode SDK"
fi

apple_require_store_sdk iphoneos 26 26.0
if apple_require_store_sdk iphoneos 26 18.2 >/dev/null 2>&1; then
  fail "an SDK below Apple's upload floor must fail before archive"
fi

echo "apple-xcode-auth tests passed"
