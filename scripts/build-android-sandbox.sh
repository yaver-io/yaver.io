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
PROOT_SRC="${PROOT_SRC:-$REPO_ROOT/out/android-proot}"
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
