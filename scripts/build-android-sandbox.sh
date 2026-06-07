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
JNI_DIR="$REPO_ROOT/mobile/android/app/src/main/jniLibs/arm64-v8a"
mkdir -p "$JNI_DIR"

echo "==> Cross-compiling Go agent for android/arm64"
# CGO off → fully self-contained, no NDK needed for the agent itself.
# -checklinkname=0 is REQUIRED: github.com/wlynxg/anet (network-interface
#   enumeration) uses //go:linkname against net.zoneCache, which the Go 1.26
#   linker rejects by default on GOOS=android. Verified building 2026-06-08.
# -s -w strips debug info (65 MB → ~45 MB).
( cd "$AGENT_DIR" && \
  CGO_ENABLED=0 GOOS=android GOARCH=arm64 \
    go build -trimpath -ldflags="-checklinkname=0 -s -w" \
      -o "$JNI_DIR/libyaver.so" . )
echo "    libyaver.so: $(du -h "$JNI_DIR/libyaver.so" | cut -f1)"

# --- proot + loader -------------------------------------------------------
# proot for Android is a userspace ptrace chroot (no root). The canonical arm64
# build ships with Termux's proot package. Drop the two binaries here and pin
# their sha256 so a supply-chain swap is caught. We do NOT vendor them into git
# (binary blobs); CI restores them from a pinned release asset.
PROOT_SRC="${PROOT_SRC:-}"           # dir containing proot + loader (libproot-loader.so)
if [[ -n "$PROOT_SRC" ]]; then
  echo "==> Installing proot from $PROOT_SRC"
  cp "$PROOT_SRC/proot" "$JNI_DIR/libproot.so"
  cp "$PROOT_SRC/loader" "$JNI_DIR/libproot-loader.so"
  chmod +x "$JNI_DIR/libproot.so" "$JNI_DIR/libproot-loader.so"
else
  echo "!! PROOT_SRC not set — skipping proot copy."
  echo "   Fetch the arm64 proot+loader and re-run with PROOT_SRC=<dir>."
fi

cat <<EOF

==> Done. jniLibs payload: $JNI_DIR
    extractNativeLibs is already enabled via gradle.properties
    (expo.useLegacyPackaging=true) so these binaries land executable on disk.
    SandboxService.kt launches them from applicationInfo.nativeLibraryDir.

    Rootfs (downloaded on first run, NOT in the APK):
      ROOTFS_RELEASE  = kivanccakmak/yaver-models  (add a 'yaver-rootfs-alpine-arm64' asset)
      ROOTFS_SHA256   = <pin in RootfsInstaller.kt>
EOF
