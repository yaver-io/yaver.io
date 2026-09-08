#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
export PLAY_STORE_KEY_FILE="${PLAY_STORE_KEY_FILE:-$ROOT/keys/google-play-service-account.json}"

# Phone/tablet is the shared Android Auto + headset-compatible AAB. Upload it
# exactly once; the Auto/XR checks below verify the already-submitted artifact
# instead of wasting version codes by uploading identical bytes repeatedly.
"$ROOT/scripts/deploy-playstore.sh"
"$ROOT/scripts/run-playstore-upload.sh"
"$ROOT/scripts/deploy-android-auto.sh"
"$ROOT/scripts/deploy-android-xr.sh" --skip-build

# Wear uses the same package/listing on its dedicated form-factor track. TV is
# a standalone package/listing. Both scripts build, verify, sign, and upload.
"$ROOT/scripts/deploy-wear-os.sh" --upload
"$ROOT/scripts/deploy-android-tv.sh" --upload

echo "Android stack submitted: phone/tablet + Auto/XR shared AAB, Wear OS, Android TV."
