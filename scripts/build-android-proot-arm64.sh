#!/usr/bin/env bash
#
# build-android-proot-arm64.sh — build a static arm64 `proot` for the Android
# on-device sandbox. proot is a userspace ptrace chroot (no root needed); the
# agent wraps runner/PTY subprocesses in it so claude/codex/opencode + hermesc
# run against the Alpine rootfs (see desktop/agent/sandbox_proot.go).
#
# WHY STATIC: the binary runs on Android (bionic), not inside Alpine. A fully
# static musl build has no libc/talloc dependency, so it runs anywhere arm64.
# Modern proot embeds its loader (src/build.h) into the binary, so there is NO
# separate loader file to ship — the Go side treats PROOT_LOADER as optional.
#
# WHERE IT RUNS: Docker on an arm64 host (Apple Silicon native; no QEMU). Free.
# Output: out/android-proot/proot  (consumed by build-android-sandbox.sh).
#
# Pinned by version + verified by the build's own sanity run (`proot --version`).
# NOTE: like the rootfs builder, this is Docker-built but still needs an
# on-device run to confirm Android's seccomp/ptrace policy accepts it.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

PROOT_VERSION="${PROOT_VERSION:-v5.4.0}"   # github.com/proot-me/proot release tag
ALPINE_TAG="${ALPINE_TAG:-3.20}"
OUT_DIR="${OUT_DIR:-$ROOT_DIR/out/android-proot}"

require() { command -v "$1" >/dev/null 2>&1 || { echo "missing required command: $1" >&2; exit 1; }; }
require docker
docker info >/dev/null 2>&1 || { echo "docker daemon not running" >&2; exit 1; }

echo "[proot] building static arm64 proot $PROOT_VERSION (alpine $ALPINE_TAG)"
mkdir -p "$OUT_DIR"

BUILD_CTX="$(mktemp -d)"
trap 'rm -rf "$BUILD_CTX"' EXIT

cat > "$BUILD_CTX/Dockerfile" <<DOCKERFILE
# syntax=docker/dockerfile:1
FROM --platform=linux/arm64 alpine:${ALPINE_TAG}
# proot itself depends only on libtalloc; libarchive is for the optional `care`
# tool which we don't build. Static build needs the .a archives.
# proot itself depends only on libtalloc; libarchive is for the optional `care`
# tool which we don't build. bsd-compat-headers supplies <sys/queue.h> which
# musl omits (proot uses LIST_* from it). Static build needs the .a archives.
RUN apk add --no-cache \
      build-base git bash \
      talloc-static talloc-dev \
      musl-dev linux-headers bsd-compat-headers
WORKDIR /build
RUN git clone --depth 1 --branch ${PROOT_VERSION} https://github.com/proot-me/proot
WORKDIR /build/proot
# Build the embedded loader first (build.h bakes it into proot), then proot
# itself, fully static. CARE flag set off (no libarchive runtime) keeps it lean;
# if a host's proot needs --link2symlink etc. it still works.
RUN make -C src loader.elf build.h GIT=false || (echo "loader build failed" >&2; exit 1)
# Pass -static via ENVIRONMENT (not make-args) so proot's GNUmakefile `LDFLAGS +=`
# keeps appending `-ltalloc` (pkg-config) AFTER the objects — a make-arg override
# would wipe it and the static link fails on undefined talloc_* symbols.
RUN CFLAGS="-O2 -static" LDFLAGS="-static" make -C src proot GIT=false \
 && strip src/proot
# Sanity: it must be static AND runnable in-image.
RUN file src/proot | grep -q "statically linked" || (echo "NOT static!" >&2; exit 1)
RUN ./src/proot --version 2>&1 | head -1
DOCKERFILE

IMG="yaver-proot-arm64:build"
echo "[proot] docker build (native arm64)…"
docker build --platform linux/arm64 -t "$IMG" "$BUILD_CTX"

CID="$(docker create --platform linux/arm64 "$IMG")"
trap 'docker rm -f "$CID" >/dev/null 2>&1 || true; rm -rf "$BUILD_CTX"' EXIT
docker cp "$CID:/build/proot/src/proot" "$OUT_DIR/proot"
docker rm -f "$CID" >/dev/null 2>&1 || true
chmod +x "$OUT_DIR/proot"

if command -v shasum >/dev/null 2>&1; then
  SHA="$(shasum -a 256 "$OUT_DIR/proot" | awk '{print $1}')"
else
  SHA="$(sha256sum "$OUT_DIR/proot" | awk '{print $1}')"
fi

echo ""
echo "[proot] DONE"
echo "[proot] binary:  $OUT_DIR/proot ($(du -h "$OUT_DIR/proot" | awk '{print $1}'))"
echo "[proot] version: $PROOT_VERSION"
echo "[proot] sha256:  $SHA"
echo ""
echo "Next: build-android-sandbox.sh picks this up automatically (default"
echo "      PROOT_SRC=out/android-proot). It ships as jniLibs/.../libproot.so."
