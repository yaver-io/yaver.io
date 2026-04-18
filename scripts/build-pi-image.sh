#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

VERSION="${VERSION:-dev}"
WORK_DIR="${WORK_DIR:-$ROOT_DIR/.tmp-pi-image}"
OUTPUT_DIR="${OUTPUT_DIR:-$ROOT_DIR/dist/pi-image}"
BASE_IMAGE_URL="${BASE_IMAGE_URL:-https://cdimage.ubuntu.com/releases/noble/release/ubuntu-24.04.4-preinstalled-server-arm64+raspi.img.xz}"
BASE_IMAGE_PATH=""
YAVER_BINARY=""
KEEP_WORK=0
COMPRESS=1
USE_DOCKER=0
INTERNAL_LINUX_BUILD=0
REQUIRED_GO_VERSION="1.26.0"

usage() {
  cat <<'EOF'
Usage:
  scripts/build-pi-image.sh [options]

Options:
  --version <semver>         Version to embed in release metadata
  --work-dir <path>          Working directory (default: .tmp-pi-image)
  --output-dir <path>        Output directory (default: dist/pi-image)
  --base-image-url <url>     Base Raspberry Pi image URL
  --base-image-path <path>   Use an existing downloaded .img.xz instead of fetching
  --yaver-binary <path>      Prebuilt linux/arm64 yaver binary to inject
  --docker                   Run the build inside a privileged Ubuntu container
  --keep-work                Preserve the working directory after completion
  --no-compress              Keep the raw .img instead of producing .img.xz
  -h, --help                 Show help
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      VERSION="${2:-}"
      shift 2
      ;;
    --work-dir)
      WORK_DIR="${2:-}"
      shift 2
      ;;
    --output-dir)
      OUTPUT_DIR="${2:-}"
      shift 2
      ;;
    --base-image-url)
      BASE_IMAGE_URL="${2:-}"
      shift 2
      ;;
    --base-image-path)
      BASE_IMAGE_PATH="${2:-}"
      shift 2
      ;;
    --yaver-binary)
      YAVER_BINARY="${2:-}"
      shift 2
      ;;
    --docker)
      USE_DOCKER=1
      shift
      ;;
    --keep-work)
      KEEP_WORK=1
      shift
      ;;
    --no-compress)
      COMPRESS=0
      shift
      ;;
    --internal-linux-build)
      INTERNAL_LINUX_BUILD=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

run_in_docker() {
  require_cmd docker
  if ! docker info >/dev/null 2>&1; then
    echo "docker daemon is not running. Start Docker Desktop/Engine first, or run this script on Linux." >&2
    exit 1
  fi
  local workspace_mount="/workspace"
  local build_cmd="./scripts/build-pi-image.sh --internal-linux-build --version \"$VERSION\" --work-dir \"$WORK_DIR\" --output-dir \"$OUTPUT_DIR\""
  if [[ -n "$BASE_IMAGE_URL" ]]; then
    build_cmd+=" --base-image-url \"$BASE_IMAGE_URL\""
  fi
  if [[ -n "$BASE_IMAGE_PATH" ]]; then
    build_cmd+=" --base-image-path \"$BASE_IMAGE_PATH\""
  fi
  if [[ -n "$YAVER_BINARY" ]]; then
    build_cmd+=" --yaver-binary \"$YAVER_BINARY\""
  fi
  if [[ "$KEEP_WORK" -eq 1 ]]; then
    build_cmd+=" --keep-work"
  fi
  if [[ "$COMPRESS" -eq 0 ]]; then
    build_cmd+=" --no-compress"
  fi
  docker run --rm --privileged \
    -v "$ROOT_DIR:$workspace_mount" \
    -w "$workspace_mount" \
    ubuntu:24.04 \
    bash -lc "apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y curl golang-go rsync xz-utils unzip util-linux sudo ca-certificates && $build_cmd"
  exit 0
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 1
  }
}

normalize_go_version() {
  printf '%s' "$1" | sed 's/^go//'
}

go_version_lt() {
  local a="$1" b="$2"
  IFS=. read -r a1 a2 a3 <<<"$a"
  IFS=. read -r b1 b2 b3 <<<"$b"
  a1="${a1:-0}"
  a2="${a2:-0}"
  a3="${a3:-0}"
  b1="${b1:-0}"
  b2="${b2:-0}"
  b3="${b3:-0}"
  if ((10#$a1 != 10#$b1)); then
    ((10#$a1 < 10#$b1))
    return
  fi
  if ((10#$a2 != 10#$b2)); then
    ((10#$a2 < 10#$b2))
    return
  fi
  ((10#$a3 < 10#$b3))
}

ensure_go_toolchain() {
  local raw current
  raw="$(go version | awk '{print $3}')"
  current="$(normalize_go_version "$raw")"
  if ! go_version_lt "$current" "$REQUIRED_GO_VERSION"; then
    return 0
  fi

  local archive="go${REQUIRED_GO_VERSION}.linux-amd64.tar.gz"
  local url="https://go.dev/dl/${archive}"
  local toolchain_root="$WORK_DIR/go-toolchain"
  local archive_path="$WORK_DIR/$archive"

  echo "[pi-image] system Go $current is too old; downloading Go $REQUIRED_GO_VERSION"
  mkdir -p "$toolchain_root"
  if [[ ! -f "$archive_path" ]]; then
    curl -L --fail --retry 3 --connect-timeout 20 --max-time 1800 -o "$archive_path" "$url"
  fi
  rm -rf "$toolchain_root/go"
  tar -C "$toolchain_root" -xzf "$archive_path"
  export PATH="$toolchain_root/go/bin:$PATH"
  echo "[pi-image] using $(go version)"
}

if [[ "$USE_DOCKER" -eq 1 && "$INTERNAL_LINUX_BUILD" -eq 0 ]]; then
  run_in_docker
fi

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "build-pi-image.sh requires Linux loop-device tooling. Re-run on Linux or use --docker from macOS." >&2
  exit 1
fi

require_cmd curl
require_cmd go
require_cmd rsync
require_cmd sudo
require_cmd mount
require_cmd umount
require_cmd losetup
require_cmd xz
require_cmd unzip
require_cmd findmnt

mkdir -p "$WORK_DIR" "$OUTPUT_DIR"
WORK_DIR="$(cd "$WORK_DIR" && pwd)"
OUTPUT_DIR="$(cd "$OUTPUT_DIR" && pwd)"
ensure_go_toolchain

DOWNLOAD_PATH="$WORK_DIR/base.img.xz"
RAW_IMAGE="$WORK_DIR/yaver-pi5-devnode-arm64.img"
BOOT_MNT="$WORK_DIR/mnt-boot"
ROOT_MNT="$WORK_DIR/mnt-root"
BUILD_BINARY="$WORK_DIR/yaver-linux-arm64"
LOOPDEV=""

cleanup() {
  set +e
  sync || true
  if [[ -n "$ROOT_MNT" ]] && findmnt "$ROOT_MNT" >/dev/null 2>&1; then
    sudo umount "$ROOT_MNT"
  fi
  if [[ -n "$BOOT_MNT" ]] && findmnt "$BOOT_MNT" >/dev/null 2>&1; then
    sudo umount "$BOOT_MNT"
  fi
  if [[ -n "$LOOPDEV" ]]; then
    sudo losetup -d "$LOOPDEV" >/dev/null 2>&1 || true
  fi
  if [[ "$KEEP_WORK" -ne 1 ]]; then
    rm -rf "$BOOT_MNT" "$ROOT_MNT"
  fi
}
trap cleanup EXIT

if [[ -f "$DOWNLOAD_PATH" && -z "$BASE_IMAGE_PATH" ]]; then
  echo "[pi-image] reusing downloaded base image: $DOWNLOAD_PATH"
elif [[ -n "$BASE_IMAGE_PATH" ]]; then
  cp "$BASE_IMAGE_PATH" "$DOWNLOAD_PATH"
else
  echo "[pi-image] downloading base image: $BASE_IMAGE_URL"
  curl -L --fail --retry 3 --connect-timeout 20 --max-time 3600 -o "$DOWNLOAD_PATH" "$BASE_IMAGE_URL"
fi

if [[ -n "$YAVER_BINARY" ]]; then
  cp "$YAVER_BINARY" "$BUILD_BINARY"
else
  echo "[pi-image] building yaver linux/arm64 binary"
  (
    cd "$ROOT_DIR/desktop/agent"
    GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o "$BUILD_BINARY" .
  )
fi
chmod +x "$BUILD_BINARY"

echo "[pi-image] expanding base image"
xz -dc "$DOWNLOAD_PATH" > "$RAW_IMAGE"

mkdir -p "$BOOT_MNT" "$ROOT_MNT"

echo "[pi-image] attaching loop device"
LOOPDEV="$(sudo losetup --find --show --partscan "$RAW_IMAGE")"
BOOT_PART="${LOOPDEV}p1"
ROOT_PART="${LOOPDEV}p2"
[[ -b "$BOOT_PART" ]] || { echo "boot partition not found: $BOOT_PART" >&2; exit 1; }
[[ -b "$ROOT_PART" ]] || { echo "root partition not found: $ROOT_PART" >&2; exit 1; }

sudo mount "$BOOT_PART" "$BOOT_MNT"
sudo mount "$ROOT_PART" "$ROOT_MNT"

echo "[pi-image] applying cloud-init seed"
sudo install -d -m 0755 "$BOOT_MNT"
sudo cp "$ROOT_DIR/pi-image/cloud-init/user-data" "$BOOT_MNT/user-data"
sudo cp "$ROOT_DIR/pi-image/cloud-init/meta-data" "$BOOT_MNT/meta-data"
sudo cp "$ROOT_DIR/pi-image/cloud-init/network-config" "$BOOT_MNT/network-config"
if [[ -f "$ROOT_DIR/pi-image/rootfs/boot/firmware/yaver-firstboot.env" ]]; then
  sudo cp "$ROOT_DIR/pi-image/rootfs/boot/firmware/yaver-firstboot.env" "$BOOT_MNT/yaver-firstboot.env"
fi

echo "[pi-image] copying rootfs overlay"
sudo rsync -a "$ROOT_DIR/pi-image/rootfs/" "$ROOT_MNT/"
sudo install -d -m 0755 "$ROOT_MNT/usr/local/bin"
sudo install -m 0755 "$BUILD_BINARY" "$ROOT_MNT/usr/local/bin/yaver"
sudo chmod +x "$ROOT_MNT/usr/local/lib/yaver/pi-firstboot.sh"

sudo install -d -m 0755 "$ROOT_MNT/etc/yaver"
cat <<EOF | sudo tee "$ROOT_MNT/etc/yaver/pi-image-release.json" >/dev/null
{
  "version": "$VERSION",
  "baseImageUrl": "$BASE_IMAGE_URL",
  "builtAtUtc": "$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
}
EOF

sync
sudo umount "$ROOT_MNT"
sudo umount "$BOOT_MNT"
sudo losetup -d "$LOOPDEV"
LOOPDEV=""

FINAL_IMG="$OUTPUT_DIR/yaver-pi5-devnode-arm64.img"
mv "$RAW_IMAGE" "$FINAL_IMG"

if [[ "$COMPRESS" -eq 1 ]]; then
  echo "[pi-image] compressing final image"
  xz -T0 -z -f -9 "$FINAL_IMG"
  FINAL_IMG="${FINAL_IMG}.xz"
fi

echo "[pi-image] built: $FINAL_IMG"
