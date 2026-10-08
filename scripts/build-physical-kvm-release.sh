#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HW="$ROOT/hardware/yaver-physical-kvm"
FW="$HW/firmware"
VERSION="$(tr -d '[:space:]' < "$HW/VERSION")"
OUT="${1:-$ROOT/dist/physical-kvm/v$VERSION}"

grep -Fq -- "-DYAVER_KVM_VERSION=\\\"$VERSION\\\"" "$FW/platformio.ini" || {
  echo "firmware build version does not match hardware/yaver-physical-kvm/VERSION ($VERSION)" >&2
  exit 1
}

available_kb="$(df -Pk "$ROOT" | awk 'NR==2 {print $4}')"
if [ "${available_kb:-0}" -lt 1048576 ]; then
  echo "physical-kvm release needs at least 1 GiB free; only ${available_kb:-0} KiB is available" >&2
  echo "Remove disposable build caches, then retry. Source trees and credentials are not cleanup targets." >&2
  exit 1
fi
command -v pio >/dev/null || { echo "PlatformIO (pio) is required" >&2; exit 1; }
command -v go >/dev/null || { echo "Go is required" >&2; exit 1; }
command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }
PIO_INFO="$(pio system info --json-output)"
PIO_DATA_DIR="$(jq -r '.core_dir.value' <<<"$PIO_INFO")"
PIO_PYTHON="$(jq -r '.python_exe.value' <<<"$PIO_INFO")"
ESPTOOL="$PIO_DATA_DIR/packages/tool-esptoolpy/esptool.py"
BOOT_APP="$PIO_DATA_DIR/packages/framework-arduinoespressif32/tools/partitions/boot_app0.bin"
test -f "$BOOT_APP" || { echo "PlatformIO boot_app0.bin not found at $BOOT_APP" >&2; exit 1; }
test -f "$ESPTOOL" || { echo "PlatformIO esptool.py not found at $ESPTOOL" >&2; exit 1; }
test -x "$PIO_PYTHON" || { echo "PlatformIO Python not found at $PIO_PYTHON" >&2; exit 1; }

mkdir -p "$OUT"

(
  cd "$FW"
  pio run
  BUILD=.pio/build/m5stack-atoms3u
  "$PIO_PYTHON" "$ESPTOOL" --chip esp32s3 merge_bin \
    --output "$OUT/yaver-kvm-atoms3u-$VERSION.factory.bin" \
    --flash_mode dio --flash_freq 80m --flash_size 8MB \
    0x0000 "$BUILD/bootloader.bin" \
    0x8000 "$BUILD/partitions.bin" \
    0xe000 "$BOOT_APP" \
    0x10000 "$BUILD/firmware.bin"
  cp "$BUILD/firmware.bin" "$OUT/yaver-kvm-atoms3u-$VERSION.ota.bin"
)

(
  cd "$ROOT/desktop/agent"
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
    go build -trimpath -ldflags="-s -w -X main.version=$VERSION" \
    -o "$OUT/yaver-agent-linux-arm64"
)

cp "$HW/bom.csv" "$OUT/bom.csv"
cp "$HW/supplier-seeds.json" "$OUT/supplier-seeds.json"
cp "$HW/ENCLOSURE.md" "$OUT/ENCLOSURE.md"
cp "$HW/VERSION" "$OUT/VERSION"
cp "$HW/enclosure/yaver_kvm_v0.scad" "$OUT/yaver-kvm-v0.scad"
cp "$HW/README.md" "$OUT/README.md"
cp "$HW/install-pi.sh" "$OUT/install-pi.sh"
cp "$HW/configure-private-link.sh" "$OUT/configure-private-link.sh"
cp "$HW/commission-m5.sh" "$OUT/commission-m5.sh"
chmod 0755 "$OUT/install-pi.sh"
chmod 0755 "$OUT/configure-private-link.sh"
chmod 0755 "$OUT/commission-m5.sh"

if command -v openscad >/dev/null 2>&1; then
  openscad -D 'part="base"' -o "$OUT/yaver-kvm-v0-base.stl" "$HW/enclosure/yaver_kvm_v0.scad"
  openscad -D 'part="lid"' -o "$OUT/yaver-kvm-v0-lid.stl" "$HW/enclosure/yaver_kvm_v0.scad"
fi

(
  cd "$OUT"
  find . -maxdepth 1 -type f ! -name checksums.txt -print | LC_ALL=C sort | xargs shasum -a 256 > checksums.txt
)

echo "Physical KVM v$VERSION artifacts:"
ls -la "$OUT"
