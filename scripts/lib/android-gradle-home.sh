#!/usr/bin/env bash

# Keep release state separate from shared workstation caches. Deleting ~/.gradle
# while a daemon survives leaves stale Kotlin workspace metadata in memory and
# makes every subsequent build fail before compilation. Respect an explicit CI
# cache override; otherwise all Yaver Android surfaces share this owned cache.
yaver_configure_android_gradle_home() {
  local repo_root="$1"
  export GRADLE_USER_HOME="${GRADLE_USER_HOME:-$repo_root/.yaver-build/android-gradle}"
  mkdir -p "$GRADLE_USER_HOME"
  echo "Using Yaver Gradle cache: $GRADLE_USER_HOME"
}
