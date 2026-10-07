#!/bin/bash

# Shared App Store Connect authentication preflight for local Apple deploys.
#
# Xcode supports two real upload paths:
#   1. an App Store Connect API key (headless/CI), or
#   2. the Apple account already signed in to Xcode (interactive Mac).
#
# The deploy scripts used to accept only (1), so a Mac that had just proved it
# could provision and upload from Xcode still failed immediately with
# "APP_STORE_KEY_PATH unset". Keep API-key auth when it is complete, reject
# partial/broken configuration, and otherwise let xcodebuild use its signed-in
# account. The latter cannot query ASC for the highest build number, so callers
# must require an explicit build override before consuming an upload slot.

apple_source_env_defaults() {
  local env_file="$1"
  [ -f "$env_file" ] || return 0

  local key_path_set="${APP_STORE_KEY_PATH+x}" key_path="${APP_STORE_KEY_PATH-}"
  local key_id_set="${APP_STORE_KEY_ID+x}" key_id="${APP_STORE_KEY_ID-}"
  local issuer_set="${APP_STORE_KEY_ISSUER+x}" issuer="${APP_STORE_KEY_ISSUER-}"
  local team_set="${APPLE_TEAM_ID+x}" team="${APPLE_TEAM_ID-}"

  set -a
  # shellcheck source=/dev/null
  . "$env_file"
  set +a

  if [ "$key_path_set" = x ]; then APP_STORE_KEY_PATH="$key_path"; export APP_STORE_KEY_PATH; fi
  if [ "$key_id_set" = x ]; then APP_STORE_KEY_ID="$key_id"; export APP_STORE_KEY_ID; fi
  if [ "$issuer_set" = x ]; then APP_STORE_KEY_ISSUER="$issuer"; export APP_STORE_KEY_ISSUER; fi
  if [ "$team_set" = x ]; then APPLE_TEAM_ID="$team"; export APPLE_TEAM_ID; fi
}

apple_configure_xcode_auth() {
  local key_path="${APP_STORE_KEY_PATH:-}"
  local key_id="${APP_STORE_KEY_ID:-}"
  local key_issuer="${APP_STORE_KEY_ISSUER:-}"
  local configured=0

  [ -n "$key_path" ] && configured=$((configured + 1))
  [ -n "$key_id" ] && configured=$((configured + 1))
  [ -n "$key_issuer" ] && configured=$((configured + 1))

  APPLE_XCODE_AUTH_ARGS=()
  APPLE_XCODE_AUTH_MODE="xcode-account"

  if [ "$configured" -ne 0 ] && [ "$configured" -ne 3 ]; then
    echo "ERROR: App Store Connect API-key authentication is only partially configured." >&2
    echo "       Set APP_STORE_KEY_PATH, APP_STORE_KEY_ID, and APP_STORE_KEY_ISSUER together," >&2
    echo "       or unset all three to use the Apple account signed in to Xcode." >&2
    return 1
  fi

  if [ "$configured" -eq 3 ]; then
    if [ ! -r "$key_path" ]; then
      echo "ERROR: APP_STORE_KEY_PATH is not a readable file: $key_path" >&2
      return 1
    fi
    # File Provider placeholders can pass -r while blocking forever on the
    # first content read. Probe the operation before CocoaPods or an ASC query
    # can wedge an unattended deploy.
    if ! APPLE_KEY_READ_TIMEOUT_SECONDS="${APPLE_KEY_READ_TIMEOUT_SECONDS:-5}" \
      /usr/bin/perl -e '
        $SIG{ALRM} = sub { die "timed out\n" };
        alarm($ENV{"APPLE_KEY_READ_TIMEOUT_SECONDS"});
        open(my $key, "<", $ARGV[0]) or die "open failed\n";
        read($key, my $byte, 1) == 1 or die "empty key\n";
        alarm(0);
      ' "$key_path" 2>/dev/null; then
      echo "ERROR: APP_STORE_KEY_PATH exists but its contents could not be read within ${APPLE_KEY_READ_TIMEOUT_SECONDS:-5}s." >&2
      echo "       If it is a cloud placeholder, download it locally; otherwise point" >&2
      echo "       APP_STORE_KEY_PATH at a local App Store Connect .p8 file and retry." >&2
      return 1
    fi
    APPLE_XCODE_AUTH_MODE="api-key"
    APPLE_XCODE_AUTH_ARGS=(
      -authenticationKeyPath "$key_path"
      -authenticationKeyID "$key_id"
      -authenticationKeyIssuerID "$key_issuer"
    )
    echo "Apple upload authentication: App Store Connect API key"
    return 0
  fi

  # Absence of API-key variables does not prove Xcode has a usable account
  # token. Its preferences can list an Apple ID while archive reports
  # "No Accounts" because the token is absent/expired. Only the provisioning
  # operation can prove this lane, so never print a false signed-in success.
  echo "Apple upload authentication: Xcode-managed account (validated during archive)"
}

# Xcode prints its full command invocation before a build. When API-key flags
# are present that banner includes the private-key path plus credential
# metadata, even though callers never echo those values themselves. Keep
# compiler diagnostics visible while removing the authentication arguments
# from every persisted or streamed build log.
apple_redact_xcode_auth_output() {
  LC_ALL=C sed -E \
    -e 's#(-authenticationKeyPath[ =]+)("[^"]*"|[^[:space:]]+)#\1<redacted>#g' \
    -e 's#(-authenticationKeyID[ =]+)("[^"]*"|[^[:space:]]+)#\1<redacted>#g' \
    -e 's#(-authenticationKeyIssuerID[ =]+)("[^"]*"|[^[:space:]]+)#\1<redacted>#g'
}

apple_resolve_team_id() {
  local project_source="$1"
  local resolved="${APPLE_TEAM_ID:-}"

  if [ -z "$resolved" ] && [ -f "$project_source" ]; then
    resolved="$(sed -nE \
      's/.*DEVELOPMENT_TEAM[[:space:]]*=[[:space:]]*([A-Z0-9]+);.*/\1/p' \
      "$project_source" | head -1)"
    if [ -z "$resolved" ]; then
      resolved="$(sed -nE \
        's/.*DEVELOPMENT_TEAM:[[:space:]]*//p' \
        "$project_source" | head -1 | tr -d '"[:space:]')"
    fi
  fi

  case "$resolved" in
    [A-Z0-9][A-Z0-9][A-Z0-9][A-Z0-9][A-Z0-9][A-Z0-9][A-Z0-9][A-Z0-9][A-Z0-9][A-Z0-9]) ;;
    *)
      echo "ERROR: APPLE_TEAM_ID is missing or invalid, and no 10-character team ID" >&2
      echo "       could be derived from $project_source." >&2
      return 1
      ;;
  esac

  APPLE_TEAM_ID="$resolved"
  export APPLE_TEAM_ID
}

apple_require_explicit_build_without_api_key() {
  local variable_name="$1"
  local value="$2"

  if [ "$APPLE_XCODE_AUTH_MODE" = "xcode-account" ] && [ -z "$value" ]; then
    echo "ERROR: $variable_name is required when uploading through the signed-in Xcode account." >&2
    echo "       Without an API key Yaver cannot query App Store Connect for the highest" >&2
    echo "       existing build, and guessing can collide and consume an upload slot." >&2
    return 1
  fi
}

apple_validate_build_number() {
  local variable_name="$1"
  local value="$2"
  case "$value" in
    ''|*[!0-9]*)
      echo "ERROR: $variable_name must be a positive integer; got '${value:-<empty>}'." >&2
      return 1
      ;;
    0)
      echo "ERROR: $variable_name must be greater than zero." >&2
      return 1
      ;;
  esac
}

# Prove that full Xcode can load and execute against the host OS before any
# deploy mutates generated state. On 2026-08-18 macOS Tahoe 26.6.2 was paired
# with Xcode 16.2: `xcodebuild -version` was green, but every real tool lookup
# aborted while loading CoreDevice because Tahoe's Mercury framework no longer
# exported _XPCTypeBool. A version string is inventory; asking xcodebuild to
# locate a tool through the active macOS SDK is the operation.
#
# The optional arguments make the loader result deterministic in the shell
# regression test: developer dir, probe exit status, and probe output.
apple_require_working_xcode() {
  local developer_dir="${1:-}"
  local probe_status="${2:-}"
  local probe_output="${3:-}"

  if [ -z "$developer_dir" ]; then
    developer_dir="$(xcode-select -p 2>/dev/null || true)"
  fi

  case "$developer_dir" in
    */Xcode*.app/Contents/Developer) ;;
    *)
      echo "ERROR: full Xcode is not selected; active developer directory is ${developer_dir:-<unset>}." >&2
      echo "       Command Line Tools can provide Git, but cannot archive or upload Apple apps." >&2
      echo "       Install a Tahoe-compatible Xcode, then run:" >&2
      echo "         sudo xcode-select -s /Applications/Xcode.app/Contents/Developer" >&2
      return 1
      ;;
  esac

  if [ -z "$probe_status" ]; then
    local xcodebuild_bin="$developer_dir/usr/bin/xcodebuild"
    if [ ! -x "$xcodebuild_bin" ]; then
      probe_status=127
      probe_output="missing executable: $xcodebuild_bin"
    elif probe_output="$(DEVELOPER_DIR="$developer_dir" "$xcodebuild_bin" -sdk macosx -find git 2>&1)"; then
      probe_status=0
    else
      probe_status=$?
    fi
  fi

  if [ "$probe_status" -ne 0 ]; then
    local macos_version xcode_version
    macos_version="$(sw_vers -productVersion 2>/dev/null || true)"
    xcode_version="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' \
      "${developer_dir%/Contents/Developer}/Contents/Info.plist" 2>/dev/null || true)"
    echo "ERROR: active Xcode cannot load its required libraries (probe exit $probe_status)." >&2
    echo "       macOS ${macos_version:-unknown}; Xcode ${xcode_version:-unknown}; developer dir $developer_dir." >&2
    printf '%s\n' "$probe_output" | tail -4 >&2
    echo "       Wait for any active installation to finish, or update/reinstall full Xcode" >&2
    echo "       to a release compatible with this macOS version. Command Line Tools alone" >&2
    echo "       are not enough for TestFlight. Then select Xcode and run its first-launch setup." >&2
    return 1
  fi

  case "$probe_output" in
    /*/git) ;;
    *)
      echo "ERROR: Xcode's operational probe did not resolve Git; got: ${probe_output:-<empty>}." >&2
      echo "       Reinstall full Xcode, select it with xcode-select, and rerun first-launch setup." >&2
      return 1
      ;;
  esac
}

# Asset catalogs are compiled with simulator tooling even for generic device
# archives. On 2026-08-18 Xcode 26.6 had every device SDK but only the previous
# generation of simulator runtimes; the watch build passed `-showsdks` and then
# actool failed with "No simulator runtime version ... available". Match the
# runtime VERSION to the active simulator SDK, not merely the platform family.
# Do not compare build numbers: Apple can ship a compatible runtime whose build
# differs from the SDK in the same Xcode (iOS 26.5 is SDK 23F81a and runtime
# 23F77). Xcode's supported download command is the authority for that pairing.
# Missing matching runtimes are an unambiguous, idempotent repair, so stream
# Xcode's supported platform download and return to the deploy automatically.
#
# Optional inventory/version/build arguments keep the shell test deterministic.
apple_ensure_simulator_runtime() {
  local platform="$1"
  local simulator_sdk="$2"
  local inventory="${3:-}"
  local required_version="${4:-}"
  local required_build="${5:-}"

  if [ -z "$required_version" ]; then
    required_version="$(xcrun --sdk "$simulator_sdk" --show-sdk-version 2>/dev/null || true)"
  fi
  if [ -z "$required_build" ]; then
    required_build="$(xcrun --sdk "$simulator_sdk" --show-sdk-build-version 2>/dev/null || true)"
  fi
  if [ -z "$required_version" ] || [ -z "$required_build" ]; then
    echo "ERROR: could not determine the active $platform simulator SDK version/build." >&2
    echo "       Reinstall the $platform platform in Xcode, then retry." >&2
    return 1
  fi

  if [ -z "$inventory" ]; then
    inventory="$(xcrun simctl list runtimes 2>/dev/null || true)"
  fi
  local platform_pattern="$platform"
  if [ "$platform" = "visionOS" ]; then
    platform_pattern='(visionOS|xrOS)'
  fi
  if printf '%s\n' "$inventory" | grep -Eq \
    "^$platform_pattern $required_version \\(.*\\) - com\\.apple\\.CoreSimulator\\.SimRuntime\\.[^ ]+$"; then
    return 0
  fi

  echo "Xcode has $platform SDK $required_version ($required_build), but no matching simulator runtime." >&2
  echo "Installing the matching $platform runtime; download progress follows." >&2
  if [ "${YAVER_SKIP_XCODE_PLATFORM_DOWNLOAD:-}" = "1" ]; then
    echo "ERROR: automatic platform download is disabled." >&2
    echo "       Fix: xcodebuild -downloadPlatform $platform -architectureVariant arm64" >&2
    return 1
  fi

  if ! xcodebuild -downloadPlatform "$platform" -architectureVariant arm64; then
    echo "ERROR: Xcode could not download the matching $platform runtime." >&2
    echo "       Retry the command above or use Xcode > Settings > Components." >&2
    return 1
  fi

  # Download success is not capability success. Xcode 26.6 once produced two
  # xrOS disk-image records (one Ready, one duplicate) while actool still saw
  # zero xrsimulator runtimes. Ask CoreSimulator to register/mount the asset,
  # then require the same inventory actool consumes.
  xcrun simctl runtime scan-and-mount >/dev/null 2>&1 || true
  inventory="$(xcrun simctl list runtimes 2>/dev/null || true)"
  if ! printf '%s\n' "$inventory" | grep -Eq \
    "^$platform_pattern $required_version \\(.*\\) - com\\.apple\\.CoreSimulator\\.SimRuntime\\.[^ ]+$"; then
    echo "ERROR: $platform runtime download returned success, but version $required_version is still unavailable." >&2
    echo "       CoreSimulator may have duplicate/stale disk-image registrations even when" >&2
    echo "       Settings says Ready. Inspect: xcrun simctl runtime list" >&2
    echo "       Repair the exact unusable duplicate, then run: xcrun simctl runtime scan-and-mount" >&2
    return 1
  fi
}

# Fail before dependency generation, build-number mutation, and a long archive
# when Apple's upload floor has moved beyond this Mac's active SDK. App Store
# Connect is the real capability probe, but its rejection comes far too late;
# this mirrors Apple's published SDK floor locally and names the host upgrade
# required to make the operation possible.
apple_require_store_sdk() {
  local sdk="$1"
  local minimum_major="$2"
  local detected="${3:-}"

  if [ -z "$detected" ]; then
    apple_require_working_xcode || return 1
    detected="$(xcrun --sdk "$sdk" --show-sdk-version 2>/dev/null || true)"
  fi
  case "$detected" in
    ''|*[!0-9.]*)
      echo "ERROR: could not determine the active $sdk SDK version." >&2
      echo "       Select a complete Xcode installation with xcode-select, then retry." >&2
      return 1
      ;;
  esac

  local major="${detected%%.*}"
  if [ "$major" -lt "$minimum_major" ]; then
    local xcode_version macos_version
    xcode_version="$(xcodebuild -version 2>/dev/null | tr '\n' ' ' || true)"
    macos_version="$(sw_vers -productVersion 2>/dev/null || true)"
    echo "ERROR: App Store Connect requires the $sdk $minimum_major SDK or later; active SDK is $detected." >&2
    echo "       Active ${xcode_version:-Xcode version is unknown}; macOS ${macos_version:-version is unknown}." >&2
    echo "       Install macOS Sequoia 15.6 or later and Xcode 26 or later, then rerun the deploy." >&2
    return 1
  fi
}

# altool can log an HTTP 500, retry internally, and then succeed. Require its
# final explicit success verdict; neither exit zero nor an earlier error alone
# describes the outcome. A later error/failure invalidates an earlier success.
apple_upload_log_succeeded() {
  awk '
    /UPLOAD FAILED|Validation failed|Failed to upload package|Cannot determine the platform|ERROR:/ { ok=0 }
    /UPLOAD SUCCEEDED with no errors/ { ok=1 }
    END { exit(ok ? 0 : 1) }
  ' "$1"
}
