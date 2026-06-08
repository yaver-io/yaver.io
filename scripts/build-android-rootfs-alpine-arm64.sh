#!/usr/bin/env bash
#
# build-android-rootfs-alpine-arm64.sh — build the Alpine arm64 proot rootfs
# tarball that the Android on-device sandbox downloads + extracts
# (RootfsInstaller.kt). This is the `yaver-rootfs-alpine-arm64` asset referenced
# by scripts/build-android-sandbox.sh and SandboxService — it was never built
# before, which blocked the whole on-device path.
#
# CONTRACT (RootfsInstaller.kt): a .tar.gz of an Alpine arm64 root tree,
# extracted into filesDir/rootfs, containing node · npm · git · ripgrep · bash
# (+ the coding CLIs, best-effort) so proot can run Metro/hermesc and the real
# CLIs against a full Linux userland. The installer's ustar parser handles dirs,
# regular files, symlinks and hardlinks.
#
# hermesc: we bake the self-contained musl/arm64 hermesc bundle
# (scripts/build-hermesc-alpine-arm64.sh output) into /usr/local/libexec/yaver/
# so /dev/build-native can compile HBC on-device. The bundle carries its own
# ICU + libstdc++ via rpath, so the rootfs Alpine version is free to be newer
# (3.20 → fresh node for Expo 54) than hermesc's 3.17 build base.
#
# WHERE IT RUNS: Docker on an arm64 host (Apple Silicon native; no QEMU). Free —
# no Hetzner. Output: out/android-rootfs/yaver-rootfs-alpine-arm64.tar.gz + sha256.
#
# AFTER: publish the tarball as the `yaver-rootfs-alpine-arm64` asset on
# kivanccakmak/yaver-models and pin the sha256 + version in the mobile rootfs
# config (sandboxControl.ts / RootfsInstaller call site).
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

ALPINE_TAG="3.20"                   # fresh node (≥20) for Expo 54 / RN 0.81
HERMESC_DIR="$ROOT_DIR/out/hermesc-alpine-arm64"   # output of build-hermesc-alpine-arm64.sh
OUT_DIR="$ROOT_DIR/out/android-rootfs"
VERSION=""                          # rootfs version stamp; default: date+short-rev via args, else "dev"
SKIP_CLIS=0                         # 1 → base rootfs only (node/git/bash/rg/hermesc), no coding CLIs

usage() {
  cat <<'EOF'
Usage:
  scripts/build-android-rootfs-alpine-arm64.sh [options]

Options:
  --alpine <tag>     Alpine base (default: 3.20)
  --hermesc <dir>    dir holding hermesc + lib*.so* (default: out/hermesc-alpine-arm64)
  --version <str>    rootfs version stamp (default: dev)
  --skip-clis        skip the coding-CLI npm installs (smaller, faster base rootfs)
  --out <dir>        output dir (default: out/android-rootfs)
  -h, --help         this message
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --alpine)    ALPINE_TAG="${2:-}"; shift 2 ;;
    --hermesc)   HERMESC_DIR="${2:-}"; shift 2 ;;
    --version)   VERSION="${2:-}"; shift 2 ;;
    --skip-clis) SKIP_CLIS=1; shift ;;
    --out)       OUT_DIR="${2:-}"; shift 2 ;;
    -h|--help)   usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

require() { command -v "$1" >/dev/null 2>&1 || { echo "missing required command: $1" >&2; exit 1; }; }
require docker
docker info >/dev/null 2>&1 || { echo "docker daemon not running" >&2; exit 1; }

if [[ ! -f "$HERMESC_DIR/hermesc" ]]; then
  echo "ERROR: hermesc not found at $HERMESC_DIR/hermesc" >&2
  echo "       Run scripts/build-hermesc-alpine-arm64.sh first." >&2
  exit 2
fi
[[ -n "$VERSION" ]] || VERSION="dev"

echo "[rootfs] building Alpine $ALPINE_TAG arm64 rootfs (version=$VERSION, skip_clis=$SKIP_CLIS)"
mkdir -p "$OUT_DIR"

BUILD_CTX="$(mktemp -d)"
trap 'rm -rf "$BUILD_CTX"' EXIT
# hermesc bundle into the build context so the Dockerfile can COPY it.
mkdir -p "$BUILD_CTX/hermesc"
cp "$HERMESC_DIR/hermesc" "$HERMESC_DIR"/lib*.so* "$BUILD_CTX/hermesc/"

CLI_STEP="RUN echo 'skipping coding CLIs (--skip-clis)'"
if [[ "$SKIP_CLIS" -ne 1 ]]; then
  # Best-effort: a CLI that fails to install must NOT fail the whole rootfs —
  # the hermes-reload path needs only node/hermesc, and the other CLIs still
  # land. npm global installs symlink into /usr/bin (on PATH inside proot).
  CLI_STEP=$(cat <<'CLI'
RUN npm install -g --no-fund --no-audit @anthropic-ai/claude-code || echo "WARN: claude-code install failed" \
 && npm install -g --no-fund --no-audit @openai/codex             || echo "WARN: codex install failed" \
 && npm install -g --no-fund --no-audit opencode-ai               || echo "WARN: opencode install failed" \
 && true
CLI
)
fi

cat > "$BUILD_CTX/Dockerfile" <<DOCKERFILE
# syntax=docker/dockerfile:1
FROM --platform=linux/arm64 alpine:${ALPINE_TAG}
# Runtime userland the sandbox needs. libstdc++/libgcc for node; coreutils/
# findutils so tools that assume GNU-ish behavior don't trip on busybox;
# ca-certificates for npm/git over https. We deliberately OMIT the native
# toolchain (python3/make/g++ ≈ +180 MB) — Metro + hermesc don't need it and
# RN/Expo deps ship prebuilt binaries; add it back only if a real package needs
# node-gyp.
RUN apk add --no-cache \
      nodejs npm git ripgrep bash coreutils findutils grep sed tar \
      ca-certificates libstdc++ libgcc openssh-client
# Coding CLIs (best-effort).
${CLI_STEP}
# Bake the self-contained hermesc bundle (carries its own ICU/libstdc++ rpath).
RUN install -d /usr/local/libexec/yaver
COPY hermesc/ /usr/local/libexec/yaver/
RUN chmod 0755 /usr/local/libexec/yaver/hermesc
# Trim npm/apk caches to keep the asset small.
RUN rm -rf /root/.npm /var/cache/apk/* /tmp/* 2>/dev/null || true
# Sanity: node + hermesc must run in-image (proves the rootfs is functional).
RUN node --version && git --version && /usr/local/libexec/yaver/hermesc --version 2>&1 | head -1
DOCKERFILE

IMG="yaver-rootfs-alpine-arm64:build"
echo "[rootfs] docker build (native arm64)…"
docker build --platform linux/arm64 -t "$IMG" "$BUILD_CTX"

# Flatten the image filesystem to a tar via `docker export` of a throwaway
# container, then gzip. docker export emits relative paths (usr/bin/node, …) —
# exactly what RootfsInstaller's ustar parser extracts into filesDir/rootfs.
TARBALL="$OUT_DIR/yaver-rootfs-alpine-arm64.tar.gz"
echo "[rootfs] exporting filesystem → $TARBALL"
CID="$(docker create --platform linux/arm64 "$IMG")"
trap 'docker rm -f "$CID" >/dev/null 2>&1 || true; rm -rf "$BUILD_CTX"' EXIT
docker export "$CID" | gzip -9 > "$TARBALL"
docker rm -f "$CID" >/dev/null 2>&1 || true

SIZE="$(du -h "$TARBALL" | awk '{print $1}')"
if command -v shasum >/dev/null 2>&1; then
  SHA="$(shasum -a 256 "$TARBALL" | awk '{print $1}')"
else
  SHA="$(sha256sum "$TARBALL" | awk '{print $1}')"
fi

echo ""
echo "[rootfs] DONE"
echo "[rootfs] tarball: $TARBALL ($SIZE)"
echo "[rootfs] version: $VERSION"
echo "[rootfs] sha256:  $SHA"
echo ""
echo "Next:"
echo "  • Publish as the 'yaver-rootfs-alpine-arm64' asset on kivanccakmak/yaver-models:"
echo "      gh release upload <tag> \"$TARBALL\" --repo kivanccakmak/yaver-models"
echo "  • Pin URL + sha256 ($SHA) + version ($VERSION) in the mobile rootfs config"
echo "    (sandboxControl.ts installRootfs call site)."
