#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
UPLOAD=0
SKIP_BUILD=0

usage() {
  cat <<'EOF'
Usage: scripts/deploy-android-tv.sh [--upload] [--skip-build]

Build and verify the Android TV release surface. Android TV ships in the same
Play AAB as the phone app; this script verifies the release manifest contains
the leanback launcher/features before optionally uploading to Play internal.

Options:
  --upload      Upload the built AAB to Google Play internal testing.
  --skip-build  Reuse the existing app-release.aab and release manifest.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --upload) UPLOAD=1 ;;
    --skip-build) SKIP_BUILD=1 ;;
    --help|-h) usage; exit 0 ;;
    *) echo "Unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

MANIFEST="$ROOT/mobile/android/app/build/intermediates/merged_manifests/release/processReleaseManifest/AndroidManifest.xml"
AAB="$ROOT/mobile/android/app/build/outputs/bundle/release/app-release.aab"
BANNER="$ROOT/mobile/android/app/src/main/res/drawable-xhdpi/tv_banner.png"

if [ "$SKIP_BUILD" != "1" ]; then
  "$ROOT/scripts/deploy-playstore.sh"
fi

if [ ! -f "$MANIFEST" ]; then
  echo "ERROR: release manifest not found: $MANIFEST" >&2
  echo "Run without --skip-build to generate it." >&2
  exit 1
fi

missing=0
for needle in \
  "android.software.leanback" \
  "android.intent.category.LEANBACK_LAUNCHER" \
  "android.hardware.touchscreen" \
  "@drawable/tv_banner"; do
  if grep -q "$needle" "$MANIFEST"; then
    echo "OK: Android TV manifest contains $needle"
  else
    echo "ERROR: Android TV release manifest missing $needle" >&2
    missing=1
  fi
done

if [ ! -f "$BANNER" ]; then
  echo "ERROR: Android TV banner missing: $BANNER" >&2
  missing=1
else
  python3 - "$BANNER" <<'PY'
import struct
import sys

path = sys.argv[1]
with open(path, "rb") as f:
    header = f.read(24)
if len(header) < 24 or header[:8] != b"\x89PNG\r\n\x1a\n":
    raise SystemExit(f"ERROR: TV banner is not a PNG: {path}")
width, height = struct.unpack(">II", header[16:24])
if (width, height) != (320, 180):
    raise SystemExit(f"ERROR: TV banner must be 320x180, got {width}x{height}: {path}")
print(f"OK: Android TV banner is {width}x{height}")
PY
fi

if [ "$missing" != "0" ]; then
  exit 1
fi

if [ ! -f "$AAB" ]; then
  echo "ERROR: release AAB not found: $AAB" >&2
  exit 1
fi

echo "Android TV AAB ready: $AAB"

if [ "$UPLOAD" = "1" ]; then
  PLAY_STORE_KEY_FILE="${PLAY_STORE_KEY_FILE:-$ROOT/keys/google-play-service-account.json}" \
    python3 "$ROOT/scripts/upload-playstore.py"
fi
