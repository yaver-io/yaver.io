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
# Alpine 3.17 is the NEWEST release whose musl (1.2.3) still ships the LFS64
# aliases (lseek64/ftruncate64/…) that LLVH's raw_ostream.cpp needs. musl 1.2.4
# (Alpine 3.18+) removed them, so a 3.18+ build fails with
# "'::lseek64' has not been declared". We default to static linking (below) so
# the resulting binary is self-contained and runs on ANY rootfs musl/ICU.
ALPINE_TAG="3.17"
# Default is DYNAMIC + bundled ICU (not -static): Alpine ships libicudata as a
# 1.3 KB stub .a (ICU data lives only in the .so), so a -static hermesc links
# but dies at runtime on any unicode op. Instead we link dynamically against
# ICU and ship the three ICU .so next to hermesc with an rpath of the install
# dir, so the binary is self-contained yet decoupled from the rootfs ICU/node.
STATIC=0
# Where hermesc + its bundled ICU .so get installed in the rootfs. Baked into
# hermesc as an rpath so it finds its ICU next to itself with no LD_LIBRARY_PATH.
INSTALL_DIR="/usr/local/libexec/yaver"

usage() {
  cat <<'EOF'
Usage:
  scripts/build-hermesc-alpine-arm64.sh [options]

Options:
  --ref <commit|tag>     facebook/hermes ref to build (default: derived from
                         mobile/node_modules/react-native/sdks/.hermesversion)
  --out <dir>            output directory (default: out/hermesc-alpine-arm64)
  --alpine <tag>         Alpine base image tag (default: 3.17 — see note below)
  --no-static            dynamic link instead of the default fully-static binary
                         (only safe when the build Alpine == the rootfs Alpine)
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
    --alpine)    ALPINE_TAG="${2:-}"; shift 2 ;;
    --static)    STATIC=1; shift ;;
    --no-static) STATIC=0; shift ;;
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

if [[ "$STATIC" -eq 1 ]]; then
  LINK_FLAGS="-static"
else
  # rpath = the rootfs install dir, so hermesc finds its bundled ICU .so there.
  LINK_FLAGS="-Wl,-rpath,${INSTALL_DIR}"
fi

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
      icu-dev zlib-dev linux-headers
WORKDIR /src
RUN git clone https://github.com/facebook/hermes.git hermes
WORKDIR /src/hermes
RUN git checkout ${HERMES_REF}
# Build ONLY the hermesc host tool (not the full VM) in Release. Hermes vendors
# most of its deps. Notes:
#  - This ref hard-requires ICU at configure (CMakeLists.txt:563) — keep icu-dev.
#  - We link ICU dynamically (Alpine's static libicudata.a is a stub) and ship
#    the .so alongside hermesc; CMAKE_EXE_LINKER_FLAGS carries the rpath.
#  - -D_LARGEFILE64_SOURCE makes musl 1.2.3 declare lseek64/… for LLVH raw_ostream.
#  - Capped parallelism keeps the Docker VM from OOM-killing heavy TU compiles.
RUN cmake -S . -B /build -G Ninja \
      -DCMAKE_BUILD_TYPE=Release \
      -DHERMES_BUILD_APPLE_FRAMEWORK=OFF \
      -DCMAKE_C_FLAGS="-D_LARGEFILE64_SOURCE" \
      -DCMAKE_CXX_FLAGS="-D_LARGEFILE64_SOURCE" \
      -DCMAKE_EXE_LINKER_FLAGS="${LINK_FLAGS}" \
 && cmake --build /build --target hermesc -j 6
# Verify in-container (has ICU on the default loader path) and print the HBC
# bytecode version so it lands in the build log.
RUN strip /build/bin/hermesc && /build/bin/hermesc --version

# Export hermesc + every shared lib it dynamically links EXCEPT musl's loader
# (always present in any Alpine rootfs): the ICU trio (Alpine ${ALPINE_TAG} →
# ICU 72) plus the C++ runtime (libstdc++/libgcc_s — a bare Alpine lacks them).
# All ship into ${INSTALL_DIR}; the baked rpath loads them there, so hermesc is
# self-contained and decoupled from the rootfs's own ICU/toolchain versions.
FROM scratch AS export
COPY --from=build /build/bin/hermesc /hermesc
COPY --from=build /usr/lib/libicuuc.so.72 /libicuuc.so.72
COPY --from=build /usr/lib/libicui18n.so.72 /libicui18n.so.72
COPY --from=build /usr/lib/libicudata.so.72 /libicudata.so.72
COPY --from=build /usr/lib/libstdc++.so.6 /libstdc++.so.6
COPY --from=build /usr/lib/libgcc_s.so.1 /libgcc_s.so.1
DOCKERFILE

# Ensure a buildx builder that can do linux/arm64 (the desktop-linux default
# can't emulate; the docker-container driver can via QEMU).
if ! docker buildx inspect yaver-multiarch >/dev/null 2>&1; then
  echo "[hermesc] creating buildx builder 'yaver-multiarch'"
  docker buildx create --name yaver-multiarch --driver docker-container --use >/dev/null
else
  docker buildx use yaver-multiarch >/dev/null
fi

# --progress=plain so the in-container `hermesc --version` (HBC bytecode version)
# lands in stdout/log — we can't run a linux/arm64 binary on the macOS host.
docker buildx build \
  --progress=plain \
  --platform linux/arm64 \
  --target export \
  --output "type=local,dest=$OUT_DIR" \
  "$BUILD_CTX" 2>&1 | tee "$OUT_DIR/build.log"

BIN="$OUT_DIR/hermesc"
[[ -f "$BIN" ]] || { echo "ERROR: build produced no hermesc at $BIN" >&2; exit 1; }
chmod 0755 "$BIN"

echo ""
echo "[hermesc] DONE → $OUT_DIR/"
echo "[hermesc] files:  $(cd "$OUT_DIR" && ls hermesc lib*.so* 2>/dev/null | tr '\n' ' ')"
echo "[hermesc] file:   $(file "$BIN" 2>/dev/null || echo '(file(1) unavailable)')"
# The HBC bytecode version was printed by the in-container `--version` — surface
# it from the build log (can't exec a linux/arm64 binary on macOS).
BCV="$(grep -aiE 'bytecode version|HBC' "$OUT_DIR/build.log" 2>/dev/null | head -3 || true)"
if [[ -n "$BCV" ]]; then
  echo "[hermesc] --version (from build log):"
  echo "$BCV" | sed 's/^/    /'
fi
if command -v shasum >/dev/null 2>&1; then
  echo "[hermesc] sha256: $(shasum -a 256 "$BIN" | awk '{print $1}')"
elif command -v sha256sum >/dev/null 2>&1; then
  echo "[hermesc] sha256: $(sha256sum "$BIN" | awk '{print $1}')"
fi
echo ""
echo "Next:"
echo "  • The build-log --version above must show the container's HBC bytecode"
echo "    version (RN 0.81 → 96) or YaverBundleValidator rejects on-device output."
echo "  • Ship hermesc + the libicu*.so.72 into the rootfs at ${INSTALL_DIR}/ —"
echo "    the baked rpath loads ICU from there (no LD_LIBRARY_PATH needed)."
echo "  • Pin the sha256 above wherever the rootfs/asset is verified."
