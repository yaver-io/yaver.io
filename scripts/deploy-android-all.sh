#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
export PLAY_STORE_KEY_FILE="${PLAY_STORE_KEY_FILE:-$ROOT/keys/google-play-service-account.json}"

require_surface_disk() {
  local available_kb
  available_kb="$(df -Pk "$ROOT" | awk 'NR == 2 { print $4 }')"
  if ! [[ "$available_kb" =~ ^[0-9]+$ ]] || [ "$available_kb" -lt $((2 * 1024 * 1024)) ]; then
    echo "ERROR: Android surface release needs at least 2 GiB free; only $(( ${available_kb:-0} / 1024 )) MiB is available." >&2
    echo "Inspect generated build directories before cleaning: mobile/android/app/build, mobile/android/app/.cxx, wear/app/build, androidtv/app/build, and project build/.gradle directories." >&2
    exit 2
  fi
}

# Phone/tablet is the shared Android Auto + headset-compatible AAB. Upload it
# exactly once; the Auto/XR checks below verify the already-submitted artifact
# instead of wasting version codes by uploading identical bytes repeatedly.
"$ROOT/scripts/deploy-playstore.sh"
"$ROOT/scripts/run-playstore-upload.sh"
"$ROOT/scripts/deploy-android-auto.sh"
"$ROOT/scripts/deploy-android-xr.sh" --skip-build
require_surface_disk

# Wear and TV use the same package/listing on dedicated form-factor tracks.
# Both scripts build distinct artifacts, verify, sign, and upload them.
"$ROOT/scripts/deploy-wear-os.sh" --upload
require_surface_disk
"$ROOT/scripts/deploy-android-tv.sh" --upload

echo "Android stack submitted: phone/tablet + Auto/XR shared AAB, Wear OS, Android TV."
