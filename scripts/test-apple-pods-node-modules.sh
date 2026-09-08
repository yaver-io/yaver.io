#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=scripts/apple-pods-node-modules.sh
. "$ROOT/scripts/apple-pods-node-modules.sh"

TMP_BASE="$(mktemp -d /tmp/yaver-pods-node-modules-test.XXXXXX)"
cleanup() {
  find "$TMP_BASE" -depth -delete 2>/dev/null || true
}
trap cleanup EXIT

mkdir -p "$TMP_BASE/checkout/mobile/node_modules/react-native/scripts/xcode"
touch "$TMP_BASE/checkout/mobile/node_modules/react-native/scripts/xcode/with-environment.sh"
mkdir -p "$TMP_BASE/checkout/mobile/ios"
ln -s "$TMP_BASE/external/mobile/ios/Pods" "$TMP_BASE/checkout/mobile/ios/Pods"
[ ! -e "$TMP_BASE/checkout/mobile/ios/Pods" ] || {
  echo "FAIL: test fixture must begin with a dangling Pods symlink" >&2
  exit 1
}
apple_ensure_pods_directory "$TMP_BASE/checkout/mobile/ios/Pods"
[ -d "$TMP_BASE/external/mobile/ios/Pods" ] || {
  echo "FAIL: dangling external Pods directory was not restored" >&2
  exit 1
}

# A stale external-volume link is generated state, not a reason to strand the
# release. Reproduce an unavailable target with a regular file in the path:
# mkdir cannot recreate it, so the helper must fall back to a local Pods dir.
mkdir -p "$TMP_BASE/fallback/mobile/ios"
touch "$TMP_BASE/unavailable-volume"
ln -s "$TMP_BASE/unavailable-volume/mobile/ios/Pods" "$TMP_BASE/fallback/mobile/ios/Pods"
apple_ensure_pods_directory "$TMP_BASE/fallback/mobile/ios/Pods"
[ -d "$TMP_BASE/fallback/mobile/ios/Pods" ] && [ ! -L "$TMP_BASE/fallback/mobile/ios/Pods" ] || {
  echo "FAIL: unavailable external Pods target did not fall back locally" >&2
  exit 1
}

mkdir -p "$TMP_BASE/external/mobile/node_modules"

apple_ensure_pods_node_modules_layout \
  "$TMP_BASE/checkout/mobile/ios/Pods" \
  "$TMP_BASE/checkout/mobile/node_modules"
[ -L "$TMP_BASE/external/mobile/node_modules" ] || {
  echo "FAIL: empty external node_modules was not repaired with a symlink" >&2
  exit 1
}
[ -f "$TMP_BASE/external/mobile/node_modules/react-native/scripts/xcode/with-environment.sh" ] || {
  echo "FAIL: repaired path cannot resolve React Native's archive script" >&2
  exit 1
}

rm "$TMP_BASE/external/mobile/node_modules"
mkdir -p "$TMP_BASE/external/mobile/node_modules/react-native"
if apple_ensure_pods_node_modules_layout \
  "$TMP_BASE/checkout/mobile/ios/Pods" \
  "$TMP_BASE/checkout/mobile/node_modules" >/dev/null 2>&1; then
  echo "FAIL: non-empty incomplete external dependencies must not be overwritten" >&2
  exit 1
fi

echo "apple CocoaPods node_modules layout tests passed"
