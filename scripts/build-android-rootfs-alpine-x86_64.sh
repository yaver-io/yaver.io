#!/usr/bin/env bash
#
# build-android-rootfs-alpine-x86_64.sh — build the Alpine x86_64 proot rootfs
# the Android on-device sandbox extracts when running on an x86_64 surface (an
# Android x86_64 emulator / redroid — the magara closed-loop box). Sibling of
# build-android-rootfs-alpine-arm64.sh, which targets real phones.
#
# CONTRACT (RootfsInstaller / SandboxService): a root tree extracted into
# filesDir/rootfs containing node · npm · git · bash · ripgrep so proot can run
# Metro/hermesc and the coding CLIs against a full Linux userland. With proot
# (build-android-proot-x86_64.sh) present in the x86_64 jniLibs, SandboxService
# starts the agent WITH proot (proot=true) and runner/PTY subprocesses run inside
# this rootfs — i.e. the phone really is the dev box.
#
# METHOD: `docker export` of an amd64 Alpine container with the tools installed —
# simplest reproducible way to get a populated root tree. Runs natively on an
# x86_64 host (fast — this is how magara built it); under QEMU on Apple Silicon.
# Output: out/android-rootfs-x86_64/yaver-rootfs-alpine-x86_64.tar.gz + sha256.
#
# Verified 2026-06-08: extracted on redroid (Android 13 x86_64), proot entered it
# and ran sh/busybox/node.
#
# AFTER: publish the tarball as `yaver-rootfs-alpine-x86_64` on
# kivanccakmak/yaver-models and pin version+sha256 in mobile/src/lib/
# sandboxRootfsManifest.ts (per-ABI entry).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
ALPINE_TAG="${ALPINE_TAG:-3.20}"
OUT_DIR="${OUT_DIR:-$ROOT_DIR/out/android-rootfs-x86_64}"
PKGS="${PKGS:-nodejs npm git bash ripgrep coreutils}"

require() { command -v "$1" >/dev/null 2>&1 || { echo "missing: $1" >&2; exit 1; }; }
require docker
docker info >/dev/null 2>&1 || { echo "docker daemon not running" >&2; exit 1; }

mkdir -p "$OUT_DIR"
NAME="yaver-rootfs-build-$$"
echo "[rootfs] building alpine x86_64 rootfs ($ALPINE_TAG) with: $PKGS"

docker rm -f "$NAME" >/dev/null 2>&1 || true
docker run --name "$NAME" --platform linux/amd64 "alpine:${ALPINE_TAG}" \
  sh -c "apk add --no-cache $PKGS && node --version && git --version"

STAGE="$(mktemp -d)"
trap 'docker rm -f "$NAME" >/dev/null 2>&1 || true; rm -rf "$STAGE"' EXIT
docker export "$NAME" | tar -x -C "$STAGE"
touch "$STAGE/.installed"

TARBALL="$OUT_DIR/yaver-rootfs-alpine-x86_64.tar.gz"
( cd "$STAGE" && tar -czf "$TARBALL" . )
docker rm -f "$NAME" >/dev/null 2>&1 || true

if command -v shasum >/dev/null 2>&1; then SHA="$(shasum -a 256 "$TARBALL" | awk '{print $1}')"; else SHA="$(sha256sum "$TARBALL" | awk '{print $1}')"; fi
echo "[rootfs] built $TARBALL ($(du -h "$TARBALL" | cut -f1))"
echo "[rootfs] sha256: $SHA"
echo "[rootfs] publish as yaver-rootfs-alpine-x86_64 + pin in sandboxRootfsManifest.ts"
