#!/usr/bin/env bash

# Resolve a usable JDK by exercising java itself. A JAVA_HOME directory or a
# java executable on PATH is only inventory; Android's Gradle plugins require
# the requested major version. Keep this portable across macOS release hosts
# and owner-scoped Linux workers.
yaver_java_home_has_major() {
  local home="${1:-}"
  local major="${2:-}"
  [ -n "$home" ] && [ -x "$home/bin/java" ] || return 1
  "$home/bin/java" -version 2>&1 | head -1 | grep -Eq "version \"${major}([.\"]|$)"
}

yaver_resolve_java_home() {
  local major="${1:-17}"
  local resolved=""

  if yaver_java_home_has_major "${JAVA_HOME:-}" "$major"; then
    resolved="$JAVA_HOME"
  elif [ "$(uname -s 2>/dev/null || true)" = "Darwin" ] && [ -x /usr/libexec/java_home ]; then
    resolved="$(/usr/libexec/java_home -v "$major" 2>/dev/null || true)"
    yaver_java_home_has_major "$resolved" "$major" || resolved=""
  else
    local candidate
    for candidate in \
      "$HOME/.yaver/runtimes/jdk-$major" \
      "$HOME/.yaver/runtimes/java-$major" \
      /usr/lib/jvm/java-${major}-openjdk-* \
      /usr/lib/jvm/java-1.${major}.0-openjdk-*; do
      if yaver_java_home_has_major "$candidate" "$major"; then
        resolved="$candidate"
        break
      fi
    done
  fi

  if [ -z "$resolved" ]; then
    echo "ERROR: JDK $major is required, but no executable JAVA_HOME for that version was found." >&2
    echo "Install JDK $major or set JAVA_HOME to an owner-readable JDK $major directory, then retry the same ./deploy/deploy.sh target." >&2
    return 1
  fi

  export JAVA_HOME="$resolved"
  export PATH="$JAVA_HOME/bin:$PATH"
  echo "Using JDK $major: $JAVA_HOME"
}
