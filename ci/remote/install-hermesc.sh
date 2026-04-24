#!/usr/bin/env bash
# Build + install hermesc at /usr/local/libexec/yaver/hermesc on the
# Hetzner test box (and any other Linux host that needs Hermes reload
# on an arch with no upstream prebuilt — notably linux-arm64).
#
# Idempotent: skips the build if the installed binary already reports
# bytecode version 96 (the version baked into the Yaver container).
# Safe to re-run from bootstrap.sh.
#
# Runs the same steps the Go agent's buildProjectHermesc() would run
# lazily on first reload, but one-shots them at provisioning time so
# the first customer push doesn't wait 1–2 min for CMake.

set -euo pipefail

HERMES_REF="${HERMES_REF:-rn/0.81-stable}"
TARGET_BC="${TARGET_BC:-96}"
INSTALL_PREFIX="${INSTALL_PREFIX:-/usr/local/libexec/yaver}"
INSTALL_PATH="$INSTALL_PREFIX/hermesc"
BUILD_ROOT="${BUILD_ROOT:-/var/cache/yaver-hermesc-build}"

log() { printf '[install-hermesc] %s\n' "$*"; }

if [ "${EUID:-$(id -u)}" -ne 0 ]; then
  echo "install-hermesc.sh: must run as root (writes to $INSTALL_PREFIX)" >&2
  exit 1
fi

# Short-circuit if the installed binary already matches TARGET_BC.
if [ -x "$INSTALL_PATH" ]; then
  if got="$("$INSTALL_PATH" --version 2>&1 | awk '/HBC bytecode version/ {print $NF}')"; then
    if [ "$got" = "$TARGET_BC" ]; then
      log "already installed: $INSTALL_PATH (bytecode v$got)"
      exit 0
    fi
    log "installed binary reports bytecode v$got; expected v$TARGET_BC — rebuilding"
  fi
fi

log "installing build dependencies (idempotent)"
export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y --no-install-recommends \
  git cmake ninja-build clang python3 build-essential ca-certificates

log "preparing build tree at $BUILD_ROOT"
mkdir -p "$BUILD_ROOT"
cd "$BUILD_ROOT"

if [ ! -d hermes/.git ]; then
  git clone --depth 1 --branch "$HERMES_REF" https://github.com/facebook/hermes.git hermes
else
  cd hermes
  git fetch --depth 1 origin "$HERMES_REF"
  git checkout -q FETCH_HEAD
  cd "$BUILD_ROOT"
fi

log "configuring (cmake + ninja)"
mkdir -p hermes/build
cmake -S hermes -B hermes/build -G Ninja -DCMAKE_BUILD_TYPE=Release

log "compiling hermesc (~1–2 min on cax21)"
cmake --build hermes/build --target hermesc -j"$(nproc)"

built="$BUILD_ROOT/hermes/build/bin/hermesc"
if [ ! -x "$built" ]; then
  echo "[install-hermesc] FAIL: built binary missing at $built" >&2
  exit 1
fi

got="$("$built" --version 2>&1 | awk '/HBC bytecode version/ {print $NF}')"
if [ "$got" != "$TARGET_BC" ]; then
  echo "[install-hermesc] FAIL: built hermesc reports bytecode v$got, expected v$TARGET_BC" >&2
  exit 1
fi

log "installing to $INSTALL_PATH (bytecode v$got)"
install -d -m 0755 "$INSTALL_PREFIX"
install -m 0755 "$built" "$INSTALL_PATH"

log "verifying"
"$INSTALL_PATH" --version | head -8

log "done. Yaver agent will use this binary via findSystemHermesc()."
