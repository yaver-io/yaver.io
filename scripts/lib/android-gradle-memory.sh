#!/bin/bash

# Populates YAVER_ANDROID_GRADLE_ARGS for standalone Android-family builds.
# `--no-daemon` still launches a single-use Gradle process, and Kotlin may start
# additional compiler daemons unless explicitly kept in-process. A nominal
# 4 GiB worker therefore needs one worker and a 1 GiB shared JVM ceiling;
# the remaining memory is required by aapt2, signing, the operating system, and
# the agent workloads the box is still serving during a release.
yaver_android_gradle_memory_args() {
  local total_memory_kb=""
  if [ -r /proc/meminfo ]; then
    total_memory_kb=$(awk '/^MemTotal:/ {print $2; exit}' /proc/meminfo)
  elif command -v sysctl >/dev/null 2>&1; then
    local total_memory_bytes
    total_memory_bytes=$(sysctl -n hw.memsize 2>/dev/null || true)
    [ -n "$total_memory_bytes" ] && total_memory_kb=$((total_memory_bytes / 1024))
  fi

  if [ -n "$total_memory_kb" ] && [ "$total_memory_kb" -lt $((10 * 1024 * 1024)) ]; then
    YAVER_ANDROID_GRADLE_ARGS=(
      --no-daemon
      --max-workers=1
      '-Dorg.gradle.jvmargs=-Xmx1024m -XX:MaxMetaspaceSize=256m -XX:+UseSerialGC'
      '-Pkotlin.compiler.execution.strategy=in-process'
    )
  else
    YAVER_ANDROID_GRADLE_ARGS=(--no-daemon --max-workers=2)
  fi
}
