#!/bin/bash

# Android Gradle's worker count does not limit Ninja's own jobs. Ninja 1.10
# defaults to CPU-count + 2 and does not understand the newer GNU jobserver,
# which made six emulated clang++ processes exhaust a 4 GiB ARM64 worker.
# Wrap every installed SDK Ninja binary on low-memory Linux hosts so the actual
# native build operation is serialized. The original SDK binary remains beside
# the wrapper and can still be replaced by reinstalling that CMake package.
yaver_android_limit_ninja_jobs() {
  local sdk_root="${ANDROID_SDK_ROOT:-${ANDROID_HOME:-}}"
  local jobs="${YAVER_ANDROID_NINJA_JOBS:-1}"
  local host_os
  host_os=$(uname -s)

  case "$jobs" in
    ''|*[!0-9]*|0)
      echo "ERROR: YAVER_ANDROID_NINJA_JOBS must be a positive integer; got: $jobs" >&2
      return 2
      ;;
  esac
  [ -n "$sdk_root" ] || {
    echo "ERROR: Android SDK root is unset; cannot bound native build jobs." >&2
    return 2
  }
  if [ "$host_os" != Linux ] && [ "${YAVER_ANDROID_NINJA_FORCE:-0}" != 1 ]; then
    return 0
  fi

  local found=0 ninja real tmp
  for ninja in "$sdk_root"/cmake/*/bin/ninja; do
    [ -f "$ninja" ] || continue
    found=1
    real="${ninja}.yaver-real"
    if grep -q '^# yaver-android-ninja-low-memory$' "$ninja" 2>/dev/null; then
      [ -x "$real" ] || {
        echo "ERROR: Yaver Ninja wrapper has no executable SDK binary: $real" >&2
        return 2
      }
      continue
    fi
    if [ -e "$real" ]; then
      echo "ERROR: refusing to replace unrecognized Ninja while $real exists." >&2
      echo "Reinstall the SDK CMake package, then retry the Yaver Android build." >&2
      return 2
    fi

    # Probe the operation before changing the SDK. On ARM64 this catches a
    # missing x86_64 binfmt loader/runtime in seconds, instead of CMake later
    # reporting a misleading shell syntax error for an ELF executable.
    if ! "$ninja" --version >/dev/null 2>&1; then
      echo "ERROR: Android SDK Ninja cannot execute on $(uname -m)." >&2
      echo "On ARM64 Ubuntu install qemu-user-static, binfmt-support, and the amd64 libc/libstdc++ runtime, then retry." >&2
      return 2
    fi

    mv "$ninja" "$real"
    tmp=$(mktemp "${ninja}.yaver.XXXXXX")
    cat >"$tmp" <<'EOF'
#!/bin/sh
# yaver-android-ninja-low-memory
set -eu
self_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
jobs=${YAVER_ANDROID_NINJA_JOBS:-1}
case "$jobs" in
  ''|*[!0-9]*|0)
    echo "ERROR: YAVER_ANDROID_NINJA_JOBS must be a positive integer; got: $jobs" >&2
    exit 2
    ;;
esac
exec "$self_dir/ninja.yaver-real" "$@" -j"$jobs"
EOF
    chmod 755 "$tmp"
    mv "$tmp" "$ninja"
  done

  if [ "$found" != 1 ]; then
    echo "ERROR: no Android SDK CMake/Ninja package is installed under $sdk_root/cmake." >&2
    echo "Install the project's required SDK CMake package before building on a low-memory worker." >&2
    return 2
  fi
}

# Google publishes the Linux NDK host toolchain under linux-x86_64. Execute its
# compiler on ARM64 before Gradle so missing binfmt/runtime support has a named
# route to repair rather than a late configure/build failure.
yaver_android_probe_ndk_host() {
  local sdk_root="${ANDROID_SDK_ROOT:-${ANDROID_HOME:-}}"
  local host_arch
  host_arch=$(uname -m)
  case "$host_arch" in
    arm64|aarch64) ;;
    *) return 0 ;;
  esac

  local found=0 clang
  for clang in "$sdk_root"/ndk/*/toolchains/llvm/prebuilt/linux-x86_64/bin/clang; do
    [ -x "$clang" ] || continue
    found=1
    if ! "$clang" --version >/dev/null 2>&1; then
      echo "ERROR: Android NDK Clang cannot execute on $host_arch." >&2
      echo "Install qemu-user-static, binfmt-support, and the amd64 libc/libstdc++ runtime, then retry." >&2
      return 2
    fi
  done
  if [ "$found" != 1 ]; then
    echo "ERROR: no linux-x86_64 Android NDK host compiler is installed under $sdk_root/ndk." >&2
    return 2
  fi
}
