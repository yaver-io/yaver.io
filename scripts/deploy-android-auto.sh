#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MANIFEST="$ROOT/mobile/android/app/src/main/AndroidManifest.xml"
AUTOMOTIVE_DESC="$ROOT/mobile/android/app/src/main/res/xml/automotive_app_desc.xml"
MAIN_APPLICATION="$ROOT/mobile/android/app/src/main/java/io/yaver/mobile/MainApplication.kt"
NATIVE_DIR="$ROOT/mobile/native-androidauto/android"
UPLOAD=0
BUILD=0

usage() {
  cat <<'EOF'
Usage: scripts/deploy-android-auto.sh [--upload] [--build]

Preflight the Android Auto messaging release lane. Android Auto ships through
the shared Play AAB, but this script fails fast if the car metadata, Automotive
descriptor, native MessagingStyle bridge, or RemoteInput reply receiver drift.

Options:
  --upload  Run the shared Play build/upload lane after preflight passes.
  --build   Run the shared Play build after preflight passes, without upload.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --upload) UPLOAD=1 ;;
    --build) BUILD=1 ;;
    --help|-h) usage; exit 0 ;;
    *) echo "Unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

require_file() {
  local path="$1"
  local label="$2"
  if [ ! -f "$path" ]; then
    echo "ERROR: missing $label at $path" >&2
    exit 1
  fi
}

require_text() {
  local path="$1"
  local needle="$2"
  local label="$3"
  if grep -Fq "$needle" "$path"; then
    echo "OK: $label"
  else
    echo "ERROR: $label missing $needle in $path" >&2
    exit 1
  fi
}

require_file "$MANIFEST" "Android manifest"
require_file "$AUTOMOTIVE_DESC" "Android Auto automotive app descriptor"
require_file "$MAIN_APPLICATION" "MainApplication"
require_file "$NATIVE_DIR/YaverCarMessagingModule.kt" "Android Auto native module"
require_file "$NATIVE_DIR/YaverCarMessagingPackage.kt" "Android Auto package"

xmllint --noout "$MANIFEST"
xmllint --noout "$AUTOMOTIVE_DESC"

require_text "$MANIFEST" 'com.google.android.gms.car.application' "Manifest declares Android Auto application metadata"
require_text "$MANIFEST" '@xml/automotive_app_desc' "Manifest points at automotive_app_desc"
require_text "$MANIFEST" 'io.yaver.mobile.car.YaverCarReplyReceiver' "Manifest registers Android Auto reply receiver"
require_text "$AUTOMOTIVE_DESC" '<uses name="notification"' "Automotive descriptor uses notification/messaging lane"
require_text "$MAIN_APPLICATION" 'import io.yaver.mobile.car.YaverCarMessagingPackage' "MainApplication imports Android Auto package"
require_text "$MAIN_APPLICATION" 'add(YaverCarMessagingPackage())' "MainApplication registers Android Auto package"
require_text "$NATIVE_DIR/YaverCarMessagingModule.kt" 'NotificationCompat.MessagingStyle' "Native module builds MessagingStyle notifications"
require_text "$NATIVE_DIR/YaverCarMessagingModule.kt" 'RemoteInput.Builder' "Native module exposes Android Auto voice reply"
require_text "$NATIVE_DIR/YaverCarMessagingModule.kt" 'YaverCarReplyReceiver' "Native module targets reply receiver"

echo "Android Auto preflight passed: shared Play AAB is car-messaging eligible."

if [ "$UPLOAD" = "1" ]; then
  PLAY_STORE_KEY_FILE="${PLAY_STORE_KEY_FILE:-$ROOT/keys/google-play-service-account.json}" \
    "$ROOT/scripts/deploy-playstore.sh"
  PLAY_STORE_KEY_FILE="${PLAY_STORE_KEY_FILE:-$ROOT/keys/google-play-service-account.json}" \
    python3 "$ROOT/scripts/upload-playstore.py"
elif [ "$BUILD" = "1" ]; then
  PLAY_STORE_KEY_FILE="${PLAY_STORE_KEY_FILE:-$ROOT/keys/google-play-service-account.json}" \
    "$ROOT/scripts/deploy-playstore.sh"
else
  echo "Use --build for a release AAB or --upload for Play internal after this preflight."
fi
