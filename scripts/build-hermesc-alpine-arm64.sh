#!/usr/bin/env bash
#
# build-hermesc-alpine-arm64.sh — cross-build a musl-linked aarch64 `hermesc`
# for the Android on-device proot sandbox (P1 of docs/android-local-hermes-reload.md).
#
# WHY THIS EXISTS
#   The Yaver container is a RELEASE Hermes build (bytecode-only, no compiler),
#   so on-device Hermes reload needs a real `hermesc` to turn the Metro JS
#   bundle into HBC. The embedded hermesc (desktop/agent/hermesc_embedded.go)
#   only ships darwin-arm64 / darwin-x64 / linux-x64 — there is NO arm64 build,
#   and Meta's prebuilt linux hermesc is glibc-linked so it can't run inside the
#   Android sandbox's Alpine (musl) arm64 rootfs.
#
#   This script builds hermesc INSIDE an Alpine arm64 container so it links musl
#   natively and runs unmodified in the rootfs. The agent then resolves it at
#   /usr/local/libexec/yaver/hermesc (desktop/agent/hermesc_resolver.go
#   ::findSystemHermesc, sandbox branch).
#
# VERSION PINNING (load-bearing)
#   hermesc MUST match the container's Hermes BC version or YaverBundleValidator
#   rejects the output with BC_VERSION_MISMATCH. The ref is read from the
#   mobile RN install's .hermesversion (mobile/node_modules/react-native/
#   sdks/.hermesversion), e.g.:
#     hermes-2025-07-07-RNv0.81.0-<sha>
#   The trailing <sha> is the facebook/hermes commit we check out. Re-run this
#   script and re-publish the asset whenever the container's RN/Hermes bumps.
#
# WHERE IT RUNS
#   Anywhere with Docker + buildx (arm64 native on Apple Silicon; emulated via
#   QEMU on x86). Per CLAUDE.md "Local deploy first" this is the canonical path;
#   on the Hetzner arm64 box (yaver-test-ephemeral) it builds natively/faster.
#
# AFTER A SUCCESSFUL BUILD
#   1. Verify:   out/hermesc --version            # shows "HBC bytecode version: N"
#   2. Ship it into the rootfs at /usr/local/libexec/yaver/hermesc (rebuild the
#      yaver-rootfs-alpine-arm64 asset with the binary baked in), OR publish it
#      as a standalone yaver-models asset that RootfsInstaller drops into place.
#   3. Pin the new SHA256 (printed below) wherever the rootfs/asset is verified.
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

HERMES_REF=""                       # facebook/hermes commit/tag; default: derive from .hermesversion
OUT_DIR="$ROOT_DIR/out/hermesc-alpine-arm64"
ALPINE_TAG="3.20"                   # match (or stay <=) the rootfs Alpine major so musl is ABI-compatible
STATIC=0                            # 1 → attempt fully-static link (portable across Alpine majors)

usage() {
  cat <<'EOF'
Usage:
  scripts/build-hermesc-alpine-arm64.sh [options]

Options:
  --ref <commit|tag>     facebook/hermes ref to build (default: derived from
                         mobile/node_modules/react-native/sdks/.hermesversion)
  --out <dir>            output directory (default: out/hermesc-alpine-arm64)
  --alpine <tag>         Alpine base image tag (default: 3.20)
  --static               attempt a fully-static binary (-static); portable but
                         can fail on ICU — omit unless the rootfs Alpine differs
  -h, --help             this message

Examples:
  scripts/build-hermesc-alpine-arm64.sh
  scripts/build-hermesc-alpine-arm64.sh --ref e0fc67142ec0763c6b6153ca2bf96df815539782
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --ref)     HERMES_REF="${2:-}"; shift 2 ;;
    --out)     OUT_DIR="${2:-}"; shift 2 ;;
    --alpine)  ALPINE_TAG="${2:-}"; shift 2 ;;
    --static)  STATIC=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

require() { command -v "$1" >/dev/null 2>&1 || { echo "missing required command: $1" >&2; exit 1; }; }
require docker
docker buildx version >/dev/null 2>&1 || { echo "docker buildx not available — install/enable buildx" >&2; exit 1; }

# Derive the Hermes ref from the RN install if not given. The .hermesversion
# format is hermes-<date>-RNv<x.y.z>-<sha>; we want the trailing <sha>.
if [[ -z "$HERMES_REF" ]]; then
  HV_FILE="$ROOT_DIR/mobile/node_modules/react-native/sdks/.hermesversion"
  if [[ -f "$HV_FILE" ]]; then
    raw="$(tr -d '[:space:]' < "$HV_FILE")"
    HERMES_REF="${raw##*-}"   # everything after the last '-' = commit sha
    echo "[hermesc] derived ref from .hermesversion: $HERMES_REF (full: $raw)"
  else
    echo "ERROR: no --ref given and $HV_FILE not found." >&2
    echo "       Run 'cd mobile && npm install --legacy-peer-deps' first, or pass --ref <sha>." >&2
    exit 2
  fi
fi

LINK_FLAGS=""
[[ "$STATIC" -eq 1 ]] && LINK_FLAGS="-static"

echo "[hermesc] building facebook/hermes@$HERMES_REF for linux/arm64 (Alpine $ALPINE_TAG, musl)"
mkdir -p "$OUT_DIR"

BUILD_CTX="$(mktemp -d)"
trap 'rm -rf "$BUILD_CTX"' EXIT

# Multi-stage: build hermesc in Alpine arm64, then export ONLY the binary via a
# scratch stage so `--output type=local` writes just hermesc to OUT_DIR.
cat > "$BUILD_CTX/Dockerfile" <<DOCKERFILE
# syntax=docker/dockerfile:1
FROM --platform=linux/arm64 alpine:${ALPINE_TAG} AS build
# Hermes compiler build deps. icu-dev/zlib-dev satisfy the few host-tool deps;
# the rest is a standard C++17 + cmake + python toolchain. ninja keeps the
# emulated build from crawling.
RUN apk add --no-cache \
      build-base cmake ninja python3 git \
      icu-dev zlib-dev libexecinfo-dev linux-headers
WORKDIR /src
RUN git clone https://github.com/facebook/hermes.git hermes
WORKDIR /src/hermes
RUN git checkout ${HERMES_REF}
# Build ONLY the hermesc host tool (not the full VM) in Release. Hermes vendors
# most of its deps; we point CMAKE_EXE_LINKER_FLAGS at LINK_FLAGS for the
# optional -static case.
RUN cmake -S . -B /build -G Ninja \
      -DCMAKE_BUILD_TYPE=Release \
      -DHERMES_BUILD_APPLE_FRAMEWORK=OFF \
      -DCMAKE_EXE_LINKER_FLAGS="${LINK_FLAGS}" \
 && cmake --build /build --target hermesc -j "\$(nproc)"
RUN strip /build/bin/hermesc && /build/bin/hermesc --version

FROM scratch AS export
COPY --from=build /build/bin/hermesc /hermesc
DOCKERFILE

# Ensure a buildx builder that can do linux/arm64 (the desktop-linux default
# can't emulate; the docker-container driver can via QEMU).
if ! docker buildx inspect yaver-multiarch >/dev/null 2>&1; then
  echo "[hermesc] creating buildx builder 'yaver-multiarch'"
  docker buildx create --name yaver-multiarch --driver docker-container --use >/dev/null
else
  docker buildx use yaver-multiarch >/dev/null
fi

docker buildx build \
  --platform linux/arm64 \
  --target export \
  --output "type=local,dest=$OUT_DIR" \
  "$BUILD_CTX"

BIN="$OUT_DIR/hermesc"
[[ -f "$BIN" ]] || { echo "ERROR: build produced no hermesc at $BIN" >&2; exit 1; }
chmod 0755 "$BIN"

echo ""
echo "[hermesc] DONE → $BIN"
echo "[hermesc] file:   $(file "$BIN" 2>/dev/null || echo '(file(1) unavailable)')"
if command -v shasum >/dev/null 2>&1; then
  echo "[hermesc] sha256: $(shasum -a 256 "$BIN" | awk '{print $1}')"
elif command -v sha256sum >/dev/null 2>&1; then
  echo "[hermesc] sha256: $(sha256sum "$BIN" | awk '{print $1}')"
fi
echo ""
echo "Next:"
echo "  • Verify on an arm64 host / the rootfs:  $BIN --version   (expect 'HBC bytecode version: N')"
echo "  • Bake into the rootfs at /usr/local/libexec/yaver/hermesc, or publish as a"
echo "    yaver-models asset for RootfsInstaller to drop into place."
echo "  • Pin the sha256 above wherever the rootfs/asset is verified."
