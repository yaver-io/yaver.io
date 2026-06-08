#!/usr/bin/env bash
#
# build-android-proot-x86_64.sh — build a static x86_64 `proot` for the Android
# on-device sandbox running on an x86_64 surface (an Android x86_64 emulator /
# redroid, e.g. the magara closed-loop box). The arm64 sibling
# (build-android-proot-arm64.sh) covers real phones; this covers x86_64 dev/CI
# emulators so the mobile sandbox can be validated WITHOUT a physical device.
#
# WHY STATIC: the binary runs on Android (bionic), not inside Alpine. A fully
# static musl build has no libc/talloc dependency, so it runs anywhere x86_64,
# including Android's bionic userland. proot embeds its loader (src/build.h), so
# there is no separate loader file to ship.
#
# WHERE IT RUNS: Docker. On an x86_64 host it builds natively (fast — this is
# how the magara closed-loop built it). On Apple Silicon it builds under QEMU
# (slower, still free). Output: out/android-proot-x86_64/proot.
#
# Verified 2026-06-08: a static-musl x86_64 proot built this way runs on redroid
# (Android 13 x86_64) and enters an Alpine x86_64 rootfs.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

PROOT_VERSION="${PROOT_VERSION:-v5.4.0}"
ALPINE_TAG="${ALPINE_TAG:-3.20}"
OUT_DIR="${OUT_DIR:-$ROOT_DIR/out/android-proot-x86_64}"

require() { command -v "$1" >/dev/null 2>&1 || { echo "missing required command: $1" >&2; exit 1; }; }
require docker
docker info >/dev/null 2>&1 || { echo "docker daemon not running" >&2; exit 1; }

echo "[proot] building static x86_64 proot $PROOT_VERSION (alpine $ALPINE_TAG)"
mkdir -p "$OUT_DIR"

BUILD_CTX="$(mktemp -d)"
trap 'rm -rf "$BUILD_CTX"' EXIT

cat > "$BUILD_CTX/Dockerfile" <<DOCKERFILE
# syntax=docker/dockerfile:1
FROM --platform=linux/amd64 alpine:${ALPINE_TAG}
# proot depends only on libtalloc; bsd-compat-headers supplies <sys/queue.h>
# (musl omits it; proot uses LIST_* from it). Static build needs the .a archives.
RUN apk add --no-cache \
      build-base git bash \
      talloc-static talloc-dev \
      musl-dev linux-headers bsd-compat-headers
WORKDIR /build
RUN git clone --depth 1 --branch ${PROOT_VERSION} https://github.com/proot-me/proot
WORKDIR /build/proot
RUN make -C src loader.elf build.h GIT=false || (echo "loader build failed" >&2; exit 1)
RUN CFLAGS="-O2 -static" LDFLAGS="-static" make -C src proot GIT=false \
 && strip src/proot
RUN file src/proot | grep -q "statically linked" || (echo "NOT static!" >&2; exit 1)
RUN ./src/proot --version 2>&1 | head -1
DOCKERFILE

IMG="yaver-proot-x86_64:build"
echo "[proot] docker build (linux/amd64)…"
docker build --platform linux/amd64 -t "$IMG" "$BUILD_CTX"

CID="$(docker create --platform linux/amd64 "$IMG")"
trap 'docker rm -f "$CID" >/dev/null 2>&1 || true; rm -rf "$BUILD_CTX"' EXIT
docker cp "$CID:/build/proot/src/proot" "$OUT_DIR/proot"
docker rm -f "$CID" >/dev/null 2>&1 || true
chmod +x "$OUT_DIR/proot"

if command -v shasum >/dev/null 2>&1; then
  SHA="$(shasum -a 256 "$OUT_DIR/proot" | awk '{print $1}')"
else
  SHA="$(sha256sum "$OUT_DIR/proot" | awk '{print $1}')"
fi
echo "[proot] built $OUT_DIR/proot"
echo "[proot] sha256: $SHA"
echo "[proot] ship it into the x86_64 jniLibs via build-android-sandbox.sh (PROOT_SRC + ABI=x86_64)."
