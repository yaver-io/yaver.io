#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SKIP_PHONE_BUILD=0
SKIP_WEAR_BUILD=0

usage() {
  cat <<'EOF'
Usage: scripts/prepare-android-surfaces.sh [--skip-phone-build] [--skip-wear-build]

Build and preflight every Android-family submission surface without uploading:
  - phone/tablet Play AAB
  - Android TV eligibility in the shared AAB
  - Android Auto messaging eligibility in the shared AAB/source
  - standalone Wear OS AAB

This script never uploads to Google Play. Use the surface deploy scripts with
--upload only after explicit release approval.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --skip-phone-build) SKIP_PHONE_BUILD=1 ;;
    --skip-wear-build) SKIP_WEAR_BUILD=1 ;;
    --help|-h) usage; exit 0 ;;
    *) echo "Unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

if [ "$SKIP_PHONE_BUILD" != "1" ]; then
  "$ROOT/scripts/deploy-playstore.sh"
fi

"$ROOT/scripts/deploy-android-tv.sh" --skip-build
"$ROOT/scripts/deploy-android-auto.sh"

if [ "$SKIP_WEAR_BUILD" != "1" ]; then
  "$ROOT/scripts/deploy-wear-os.sh"
else
  "$ROOT/scripts/deploy-wear-os.sh" --skip-build
fi

cat <<EOF

Android-family surfaces are prepared locally.
  Phone/Android TV/Android Auto AAB:
    $ROOT/mobile/android/app/build/outputs/bundle/release/app-release.aab
  Wear OS AAB:
    $ROOT/wear/app/build/outputs/bundle/release/app-release.aab

No upload was performed.
EOF
