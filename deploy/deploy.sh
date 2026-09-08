#!/usr/bin/env bash
set -euo pipefail

# Yaver deploy front door. This is the one canonical human/agent path:
#   ./deploy/deploy.sh <target> [options]
#
# Keep this file as a thin layer over the maintained deploy machinery in
# scripts/ and `yaver deploy`. Do not add sibling wrapper scripts; one path is
# the point.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

require_owner_locked_path() {
  local path="$1"
  if [ ! -e "$path" ]; then
    echo "ERROR: missing path for deploy permission check: $path" >&2
    exit 2
  fi

  local owner mode
  if stat -c '%U' "$path" >/dev/null 2>&1; then
    owner="$(stat -c '%U' "$path" 2>/dev/null || true)"
    mode="$(stat -c '%a' "$path" 2>/dev/null || true)"
  else
    owner="$(stat -f '%Su' "$path" 2>/dev/null || true)"
    mode="$(stat -f '%Lp' "$path" 2>/dev/null || true)"
  fi

  if [ -n "$owner" ] && [ "$owner" != "$(id -un)" ]; then
    echo "ERROR: refusing deploy because $path is owned by $owner, not $(id -un)." >&2
    exit 2
  fi

  if [ -n "$mode" ]; then
    local last_two=$((10#$mode % 100))
    local group_digit=$((last_two / 10))
    local other_digit=$((last_two % 10))
    if [ $((group_digit & 2)) -ne 0 ] || [ $((other_digit & 2)) -ne 0 ]; then
      echo "ERROR: refusing deploy because $path is group/other-writable (mode $mode)." >&2
      echo "Fix: chmod go-w '$path'" >&2
      exit 2
    fi
  fi
}

require_deploy_boundary() {
  require_owner_locked_path "$ROOT"
  require_owner_locked_path "$ROOT/deploy/deploy.sh"

  if [ "${YAVER_DEPLOY_ALLOW_SHARED:-}" = "1" ]; then
    return 0
  fi

  case "$(umask)" in
    0077|0027|0022|077|027|022) ;;
    *)
      echo "ERROR: deploy requires a conservative umask (077, 027, or 022)." >&2
      echo "Current umask: $(umask)" >&2
      exit 2
      ;;
  esac
}

usage() {
  cat <<'USAGE'
Usage:
  ./deploy/deploy.sh <target> [options]

Targets:
  all          Full sequential Yaver release via `yaver deploy all`
  backend      Convex backend prod deploy
  convex       Alias for backend
  cloudflare   Cloudflare Workers web deploy
  web          Alias for cloudflare
  ios          TestFlight deploy
  testflight   Alias for ios
  android      Play internal deploy + upload
  playstore    Alias for android
  android-package
               Build signed AAB/APK, publish APK to R2 + GitHub; do not upload Play
  android-upload
               Upload the already-built AAB to Play Internal; do not rebuild
  tvos         Apple TV standalone archive/upload (App Store Connect)
  android-tv   Android TV Play AAB + leanback manifest verification
  tv           Alias for android-tv + tvos
  wear-os      Wear OS AAB + Play internal upload
  visionos     visionOS archive/upload (App Store Connect)
  watchos      Build watchOS companion (embedded in iOS — no own record)
  carplay      CarPlay iOS target archive/upload
  android-auto Android Auto Play AAB upload
  npm          CLI npm release via `yaver deploy npm`
  cli          Alias for npm
  feedback-sdk Publish the React Native feedback SDK to npm
  desktop      Signed macOS/Windows + Linux GUI release via protected gui/v* tag
  gui          Alias for desktop
  desktop-mas  Build + locally verify the sandboxed macOS App Store package
  desktop-testflight
               Build, validate, and upload macOS desktop to TestFlight
  mcp          Publish/sync MCP registry metadata

Common:
  --dry-run    Print the command instead of running it where the target supports it

Examples:
  ./deploy/deploy.sh all --dry-run
  ./deploy/deploy.sh backend
  ./deploy/deploy.sh cloudflare
  ./deploy/deploy.sh ios
USAGE
}

if [ "${1:-}" = "" ] || [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
  usage
  exit 0
fi

target="$1"
shift

dry_run=0
pass_args=()
for arg in "$@"; do
  case "$arg" in
    --dry-run)
      dry_run=1
      pass_args+=("$arg")
      ;;
    *)
      pass_args+=("$arg")
      ;;
  esac
done

# Expand pass_args SAFELY under `set -u`.
#
# macOS ships bash 3.2, where "${empty_array[@]}" is an UNBOUND VARIABLE error
# rather than the empty list bash 4.4+ gives you. So the documented front door
# — `./deploy/deploy.sh npm`, which CLAUDE.md tells every human and agent to
# use — died with "pass_args[@]: unbound variable" whenever no extra flag was
# passed, i.e. the common case. It only worked if you happened to type one.
# Found 2026-08-03 while cutting 1.99.400.
#
# `${name[@]+"${name[@]}"}` is the portable idiom used at the call sites below:
# it expands to nothing when unset, and to the properly-quoted elements
# otherwise. Do not "simplify" it back to a bare expansion — CI runs bash 5,
# this laptop does not, and the laptop is where CLAUDE.md says to deploy from.

run() {
  if [ "$dry_run" -eq 1 ]; then
    printf '[dry-run] cd %q &&' "$ROOT"
    printf ' %q' "$@"
    printf '\n'
    return 0
  fi
  (cd "$ROOT" && "$@")
}

run_shell() {
  if [ "$dry_run" -eq 1 ]; then
    printf '[dry-run] cd %q && %s\n' "$ROOT" "$*"
    return 0
  fi
  # Export ROOT so bash -lc children can reference $ROOT/scripts/... —
  # a login shell does not inherit the parent's unexported variables.
  (cd "$ROOT" && export ROOT && bash -lc "$*")
}

case "$target" in
  all)
    require_deploy_boundary
    if ! command -v go >/dev/null 2>&1; then
      echo "ERROR: Go is required to run the repository's full release controller." >&2
      exit 2
    fi
    # Release from the checked-out controller source. A globally installed
    # wrapper can truthfully print the same semantic version while still
    # containing an older repository detector; on 2026-08-25 that stale
    # detector classified yaver.io as a generic app and attempted Convex with
    # no production deployment configuration. The source being released must
    # own its own target detection, version bumps, uploads, commit, and push.
    (cd "$ROOT/desktop/agent" && go run . deploy all ${pass_args[@]+"${pass_args[@]}"})
    ;;
  backend|convex)
    require_deploy_boundary
    run "$ROOT/scripts/deploy-convex.sh"
    ;;
  cloudflare|web)
    require_deploy_boundary
    run "$ROOT/scripts/deploy-web.sh"
    ;;
  ios|testflight)
    require_deploy_boundary
    run "$ROOT/scripts/deploy-testflight.sh"
    ;;
  android|playstore)
    require_deploy_boundary
    run_shell './scripts/deploy-playstore.sh && PLAY_STORE_KEY_FILE=keys/google-play-service-account.json ./scripts/run-playstore-upload.sh'
    ;;
  android-package|apk)
    require_deploy_boundary
    run_shell '
      set -euo pipefail
      ./scripts/deploy-playstore.sh
      mobile_version=$(node -e "console.log(require(\"./versions.json\").mobile)")
      version_code=$(sed -n "s/.*versionCode \([0-9][0-9]*\).*/\1/p" mobile/android/app/build.gradle | head -1)
      apk_dir="$ROOT/mobile/android/app/build/outputs/apk/release"
      apk_path="$apk_dir/yaver-${mobile_version}-${version_code}.apk"
      ANDROID_APK_OUTPUT="$apk_path" ./scripts/publish-android-r2.sh
      command -v gh >/dev/null 2>&1 || { echo "ERROR: gh is required for the Android GitHub release." >&2; exit 2; }
      tag="android/v${mobile_version}"
      if gh release view "$tag" >/dev/null 2>&1; then
        gh release upload "$tag" "$apk_path" --clobber
      else
        gh release create "$tag" "$apk_path" --title "Yaver Android ${mobile_version}" --notes "Signed universal APK for direct installation. Google Play uses the matching AAB build ${version_code}."
      fi
      echo "Android APK published: https://github.com/yaver-io/yaver.io/releases/tag/$tag"
    '
    ;;
  android-upload|playstore-upload)
    require_deploy_boundary
    run_shell 'test -f mobile/android/app/build/outputs/bundle/release/app-release.aab || { echo "ERROR: build the signed AAB first with ./deploy/deploy.sh android-package." >&2; exit 2; }; PLAY_STORE_KEY_FILE=keys/google-play-service-account.json ./scripts/run-playstore-upload.sh'
    ;;
  npm|cli)
    require_deploy_boundary
    if ! command -v go >/dev/null 2>&1; then
      echo "ERROR: Go is required to run the repository's CLI release controller." >&2
      exit 2
    fi
    # Release from the checked-out controller source. The globally installed
    # npm wrapper can legitimately be one release behind and its generic
    # project detector may interpret this monorepo as web-headless, publishing
    # the wrong package. The canonical repo lane must execute the code it is
    # about to release.
    if [ "$dry_run" -eq 1 ]; then
      (cd "$ROOT/desktop/agent" && go run . deploy npm --dry-run)
    else
      (cd "$ROOT/desktop/agent" && go run . deploy npm)
    fi
    ;;
  feedback-sdk|feedback-rn)
    require_deploy_boundary
    run "$ROOT/scripts/publish-feedback-rn.sh"
    ;;
  desktop|gui)
    require_deploy_boundary
    gui_version="$(node -e "console.log(require('./versions.json').gui)")"
    package_version="$(node -e "console.log(require('./electron/package.json').version)")"
    if [ "$gui_version" != "$package_version" ]; then
      echo "ERROR: versions.json gui=$gui_version but electron/package.json=$package_version." >&2
      echo "Run ./scripts/sync-versions.sh and review the result before releasing." >&2
      exit 2
    fi
    if [ -n "$(git status --porcelain)" ]; then
      echo "ERROR: desktop release requires a clean committed worktree." >&2
      exit 2
    fi
    if [ "$(git branch --show-current)" != "main" ]; then
      echo "ERROR: desktop release must run from main after review/merge." >&2
      exit 2
    fi
    gui_tag="gui/v${gui_version}"
    if git ls-remote --exit-code --tags origin "refs/tags/$gui_tag" >/dev/null 2>&1; then
      echo "ERROR: remote tag $gui_tag already exists; versions are immutable." >&2
      exit 2
    fi
    run git push origin "HEAD:refs/tags/$gui_tag"
    ;;
  desktop-mas)
    require_deploy_boundary
    run "$ROOT/scripts/deploy-macos-testflight.sh"
    ;;
  desktop-testflight|macos-testflight|mac-testflight)
    require_deploy_boundary
    run_shell 'source ~/.appstoreconnect/yaver.env 2>/dev/null; bash "$ROOT/scripts/deploy-macos-testflight.sh" --upload'
    ;;
  mcp)
    require_deploy_boundary
    run "$ROOT/scripts/publish-mcp-registries.sh" --all
    ;;
  tvos)
    require_deploy_boundary
    run_shell 'source ~/.appstoreconnect/yaver.env 2>/dev/null; bash "$ROOT/scripts/deploy-tvos.sh" --upload'
    ;;
  android-tv)
    require_deploy_boundary
    run "$ROOT/scripts/deploy-android-tv.sh" --upload
    ;;
  tv)
    require_deploy_boundary
    run "$ROOT/scripts/deploy-tv.sh" --upload
    ;;
  wear-os|wearos|wear)
    require_deploy_boundary
    run "$ROOT/scripts/deploy-wear-os.sh" --upload
    ;;
  visionos)
    require_deploy_boundary
    # Same App Store Connect creds as TestFlight/tvOS — sourced here so the
    # script's own APP_STORE_KEY_*:? guards pass without a manual export
    # (2026-08-11: visionOS deploy failed "Set APP_STORE_KEY_PATH" while
    # ~/.appstoreconnect/yaver.env existed — TestFlight sourced it, this
    # wrapper did not).
    run_shell 'source ~/.appstoreconnect/yaver.env 2>/dev/null; bash "$ROOT/scripts/deploy-visionos.sh" --upload'
    ;;
  watchos)
    require_deploy_boundary
    run "$ROOT/scripts/deploy-watchos.sh"
    ;;
  carplay)
    require_deploy_boundary
    run_shell 'source ~/.appstoreconnect/yaver.env 2>/dev/null; bash "$ROOT/scripts/deploy-carplay.sh" --upload'
    ;;
  android-auto|androidauto|auto)
    require_deploy_boundary
    run "$ROOT/scripts/deploy-android-auto.sh" --upload
    ;;
  *)
    echo "ERROR: unknown deploy target '$target'." >&2
    echo >&2
    usage >&2
    exit 2
    ;;
esac
