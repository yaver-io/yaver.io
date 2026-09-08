#!/usr/bin/env bash
# build-android-sandbox.sh — produce the on-device sandbox payload that ships in
# the Yaver Android APK's jniLibs so the phone can run the real claude/codex CLI
# inside a proot Alpine rootfs. See docs/coding-agent-on-device.md.
#
# Outputs into mobile/android/app/src/main/jniLibs/arm64-v8a/:
#   libyaver.so          — the Go agent, android/arm64 (named lib*.so so Android
#                          extracts it executable; W^X-safe)
#   libproot.so          — proot static executable (arm64)
#   libproot-loader.so   — proot's loader stub
#
# The Alpine rootfs is NOT bundled here — it is downloaded+verified on first run
# by RootfsInstaller.kt from a GitHub Release (see ROOTFS_* below). This keeps
# the APK small and the rootfs independently versioned.
#
# Requires: Go 1.26+. proot/loader binaries are fetched prebuilt (Termux's are
# the reference arm64 build); pin by sha256.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
AGENT_DIR="$REPO_ROOT/desktop/agent"

# ABI selects the target jniLibs dir + Go GOARCH. Default arm64-v8a (real
# phones); x86_64 covers Android emulators / redroid (the magara closed loop),
# so the on-device sandbox is buildable + testable WITHOUT a physical device.
ABI="${ABI:-arm64-v8a}"
case "$ABI" in
  arm64-v8a) GOARCH=arm64 ;;
  x86_64)    GOARCH=amd64 ;;
  *) echo "unsupported ABI: $ABI (use arm64-v8a or x86_64)" >&2; exit 2 ;;
esac
JNI_DIR="$REPO_ROOT/mobile/android/app/src/main/jniLibs/$ABI"
mkdir -p "$JNI_DIR"

# The agent package is intentionally broad and its single compile action can
# exceed 3.5 GiB RSS even when Gradle and Go package parallelism are disabled.
# Bound the Go runtime itself on a nominal 4 GiB build worker; explicit caller
# values win. Compiler tables also consume non-heap memory, so the low-memory
# release lane stages this build before mounting Gradle's tmpfs and then reuses
# the checksum-verified result during the AAB build.
TOTAL_MEMORY_KB=""
if [ -r /proc/meminfo ]; then
  TOTAL_MEMORY_KB=$(awk '/^MemTotal:/ {print $2; exit}' /proc/meminfo)
elif command -v sysctl >/dev/null 2>&1; then
  TOTAL_MEMORY_BYTES=$(sysctl -n hw.memsize 2>/dev/null || true)
  [ -n "$TOTAL_MEMORY_BYTES" ] && TOTAL_MEMORY_KB=$((TOTAL_MEMORY_BYTES / 1024))
fi
if [ -n "$TOTAL_MEMORY_KB" ] && [ "$TOTAL_MEMORY_KB" -lt $((10 * 1024 * 1024)) ]; then
  export GOMEMLIMIT="${GOMEMLIMIT:-1536MiB}"
  export GOGC="${GOGC:-20}"
  export GOMAXPROCS="${GOMAXPROCS:-1}"
  case " ${GOFLAGS:-} " in
    *" -p="*|*" -p "*) ;;
    *) export GOFLAGS="${GOFLAGS:+$GOFLAGS }-p=1" ;;
  esac
  echo "    low-memory Go policy: GOMEMLIMIT=$GOMEMLIMIT GOGC=$GOGC GOMAXPROCS=$GOMAXPROCS GOFLAGS=$GOFLAGS"
fi

echo "==> Cross-compiling Go agent for android/$GOARCH (ABI=$ABI)"
# A low-memory release worker may build this monolithic agent in a separate
# phase before mounting Gradle's tmpfs cache. Reuse is allowed only with an
# explicit digest; a path alone can never silently substitute stale bytes.
YAVER_ANDROID_AGENT_SRC="${YAVER_ANDROID_AGENT_SRC:-}"
YAVER_ANDROID_AGENT_SHA256="${YAVER_ANDROID_AGENT_SHA256:-}"
if [[ -n "$YAVER_ANDROID_AGENT_SRC" ]]; then
  [[ -f "$YAVER_ANDROID_AGENT_SRC" ]] || {
    echo "prebuilt Android agent not found: $YAVER_ANDROID_AGENT_SRC" >&2
    exit 1
  }
  [[ "$YAVER_ANDROID_AGENT_SHA256" =~ ^[0-9a-fA-F]{64}$ ]] || {
    echo "YAVER_ANDROID_AGENT_SHA256 must be set to a 64-character digest when reusing an agent." >&2
    exit 1
  }
  if command -v sha256sum >/dev/null 2>&1; then
    AGENT_SHA256=$(sha256sum "$YAVER_ANDROID_AGENT_SRC" | awk '{print $1}')
  else
    AGENT_SHA256=$(shasum -a 256 "$YAVER_ANDROID_AGENT_SRC" | awk '{print $1}')
  fi
  AGENT_SHA256_LOWER=$(printf '%s' "$AGENT_SHA256" | tr '[:upper:]' '[:lower:]')
  EXPECTED_AGENT_SHA256=$(printf '%s' "$YAVER_ANDROID_AGENT_SHA256" | tr '[:upper:]' '[:lower:]')
  [[ "$AGENT_SHA256_LOWER" == "$EXPECTED_AGENT_SHA256" ]] || {
    echo "prebuilt Android agent SHA-256 mismatch: got=$AGENT_SHA256 want=$YAVER_ANDROID_AGENT_SHA256" >&2
    exit 1
  }
  cp "$YAVER_ANDROID_AGENT_SRC" "$JNI_DIR/libyaver.so"
  chmod +x "$JNI_DIR/libyaver.so"
  echo "    reused checksum-verified prebuilt agent: $AGENT_SHA256"
# -checklinkname=0 is REQUIRED: github.com/wlynxg/anet uses //go:linkname
#   against net.zoneCache, which the Go 1.26 linker rejects on GOOS=android.
# -s -w strips debug info.
# arm64: CGO off → self-contained, no NDK. amd64: GOOS=android/amd64 REQUIRES
#   external (cgo) linking, so it needs the NDK x86_64 clang as CC (verified
#   2026-06-08 — CGO_ENABLED=0 GOARCH=amd64 fails "requires external linking").
elif [[ "$GOARCH" == "arm64" ]]; then
  ( cd "$AGENT_DIR" && \
    CGO_ENABLED=0 GOOS=android GOARCH=arm64 \
      go build -trimpath -ldflags="-checklinkname=0 -s -w" -o "$JNI_DIR/libyaver.so" . )
else
  NDK_DIR="${ANDROID_NDK_HOME:-$(ls -d "$HOME/Library/Android/sdk/ndk/"*/ 2>/dev/null | sort -V | tail -1)}"
  HOST_TAG="$(uname | tr '[:upper:]' '[:lower:]')-x86_64"
  CC_X86="${NDK_DIR}toolchains/llvm/prebuilt/${HOST_TAG}/bin/x86_64-linux-android24-clang"
  [[ -x "$CC_X86" ]] || { echo "NDK x86_64 clang not found ($CC_X86) — set ANDROID_NDK_HOME" >&2; exit 1; }
  ( cd "$AGENT_DIR" && \
    CGO_ENABLED=1 GOOS=android GOARCH=amd64 CC="$CC_X86" \
      go build -trimpath -ldflags="-checklinkname=0 -s -w" -o "$JNI_DIR/libyaver.so" . )
fi
echo "    libyaver.so ($ABI): $(du -h "$JNI_DIR/libyaver.so" | cut -f1)"

# --- proot + loader -------------------------------------------------------
# proot for Android is a userspace ptrace chroot (no root). The canonical arm64
# build ships with Termux's proot package. Drop the two binaries here and pin
# their sha256 so a supply-chain swap is caught. We do NOT vendor them into git
# (binary blobs); CI restores them from a pinned release asset.
# Sources, in priority order:
#   PROOT_SRC        — local dir holding `proot` (+ optional `loader`). Defaults
#                      to out/android-proot, the output of
#                      scripts/build-android-proot-arm64.sh (build from source —
#                      no binary trust). Modern proot embeds its loader, so the
#                      loader file is optional (Go treats PROOT_LOADER optional).
#   YAVER_PROOT_URL  — direct URL to a .tar.gz with proot (+ loader) inside;
#                      verified against YAVER_PROOT_SHA256 when set.
# If neither yields a proot we ship agent-only (control-plane; runners need
# proot+rootfs, so the on-device box stays disabled until proot is in).
# proot source dir is ABI-aware: arm64 → out/android-proot (build-android-proot-arm64.sh),
# x86_64 → out/android-proot-x86_64 (build-android-proot-x86_64.sh).
if [[ "$ABI" == "x86_64" ]]; then
  PROOT_SRC="${PROOT_SRC:-$REPO_ROOT/out/android-proot-x86_64}"
else
  PROOT_SRC="${PROOT_SRC:-$REPO_ROOT/out/android-proot}"
fi
YAVER_PROOT_URL="${YAVER_PROOT_URL:-}"
YAVER_PROOT_SHA256="${YAVER_PROOT_SHA256:-}"
PROOT_OK=0

install_proot_from_dir() {
  local dir="$1"
  [[ -f "$dir/proot" ]] || return 1
  cp "$dir/proot" "$JNI_DIR/libproot.so"
  chmod +x "$JNI_DIR/libproot.so"
  # Loader is optional — modern proot embeds it (src/build.h). Ship it if present.
  if [[ -f "$dir/loader" ]]; then
    cp "$dir/loader" "$JNI_DIR/libproot-loader.so"
    chmod +x "$JNI_DIR/libproot-loader.so"
  fi
  return 0
}

if [[ -d "$PROOT_SRC" && -f "$PROOT_SRC/proot" ]]; then
  echo "==> Installing proot from $PROOT_SRC"
  if install_proot_from_dir "$PROOT_SRC"; then PROOT_OK=1; else
    echo "!! PROOT_SRC=$PROOT_SRC has no proot binary." >&2
  fi
elif [[ -n "$YAVER_PROOT_URL" ]]; then
  echo "==> Fetching proot from $YAVER_PROOT_URL"
  tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
  curl -fsSL "$YAVER_PROOT_URL" -o "$tmp/proot.tgz"
  if [[ -n "$YAVER_PROOT_SHA256" ]]; then
    got="$(shasum -a 256 "$tmp/proot.tgz" | cut -d' ' -f1)"
    if [[ "$got" != "$YAVER_PROOT_SHA256" ]]; then
      echo "!! proot sha256 mismatch: got=$got want=$YAVER_PROOT_SHA256" >&2; exit 1
    fi
  else
    echo "!! YAVER_PROOT_SHA256 not set — supply-chain swap would go undetected." >&2
  fi
  tar -xzf "$tmp/proot.tgz" -C "$tmp"
  # accept either flat (proot/loader at top) or a single wrapping dir
  srcdir="$tmp"; [[ -f "$tmp/proot" ]] || srcdir="$(dirname "$(find "$tmp" -name proot -type f | head -1)")"
  if install_proot_from_dir "$srcdir"; then PROOT_OK=1; else
    echo "!! fetched tarball has no proot binary." >&2
  fi
else
  echo "!! No proot found at $PROOT_SRC (and no YAVER_PROOT_URL)."
  echo "   Build it once: scripts/build-android-proot-arm64.sh   (Docker, arm64)"
  echo "   Shipping agent-only — the on-device Linux box stays disabled until proot is bundled."
fi

# Payload report: a gitignored breadcrumb so the build + the deploy script can
# tell at a glance what actually shipped (agent-only vs full).
{
  echo "built: scripts/build-android-sandbox.sh"
  echo "libyaver.so: present ($(du -h "$JNI_DIR/libyaver.so" | cut -f1))"
  if [[ "$PROOT_OK" == 1 ]]; then
    echo "libproot.so: present ($(du -h "$JNI_DIR/libproot.so" | cut -f1))"
    echo "status: FULL (agent + proot)"
  else
    echo "libproot.so: MISSING"
    echo "status: AGENT-ONLY (control-plane; on-device runners disabled)"
  fi
} > "$JNI_DIR/../.sandbox-payload.txt"

cat <<EOF

==> Done. jniLibs payload: $JNI_DIR
    extractNativeLibs is enabled via gradle.properties
    (expo.useLegacyPackaging=true) so these binaries land executable on disk.
    SandboxService.kt launches them from applicationInfo.nativeLibraryDir.
$( [[ "$PROOT_OK" == 1 ]] && echo "    payload: FULL (agent + proot)" || echo "    payload: AGENT-ONLY (proot missing — on-device box disabled)" )

    Rootfs (downloaded on first enable, NOT in the APK):
      build:   scripts/build-android-rootfs-alpine-arm64.sh --version <ver>
      publish: scripts/publish-android-rootfs.sh <ver>
      pin:     mobile/src/lib/sandboxRootfsManifest.ts (version/sha256/sizeBytes + ROOTFS_PUBLISHED)
EOF
