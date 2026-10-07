#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
source "$ROOT/scripts/lib/android-gradle-home.sh"
fixture=$(mktemp -d)
trap 'find "$fixture" -depth -delete' EXIT
(
 unset GRADLE_USER_HOME
 yaver_configure_android_gradle_home "$fixture/repo with spaces"
 [ "$GRADLE_USER_HOME" = "$fixture/repo with spaces/.yaver-build/android-gradle" ]
 [ -d "$GRADLE_USER_HOME" ]
)
(
 export GRADLE_USER_HOME="$fixture/explicit-cache"
 yaver_configure_android_gradle_home "$fixture/repo"
 [ "$GRADLE_USER_HOME" = "$fixture/explicit-cache" ]
)
for script in deploy-playstore.sh deploy-wear-os.sh deploy-android-tv.sh; do
 grep -q 'yaver_configure_android_gradle_home' "$ROOT/scripts/$script"
done
echo 'Android release cache isolation tests passed'
