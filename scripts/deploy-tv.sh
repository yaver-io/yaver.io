#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
UPLOAD=0
SKIP_TVOS=0
SKIP_ANDROID_TV=0

usage() {
  cat <<'EOF'
Usage: scripts/deploy-tv.sh [--upload] [--skip-tvos] [--skip-android-tv]

Platform wrapper for Yaver TV surfaces:
  - Android TV: shared Play AAB with leanback verification.
  - tvOS: standalone SwiftUI app under tvos/.

Use --upload to submit store uploads after successful build/verification.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --upload) UPLOAD=1 ;;
    --skip-tvos) SKIP_TVOS=1 ;;
    --skip-android-tv) SKIP_ANDROID_TV=1 ;;
    --help|-h) usage; exit 0 ;;
    *) echo "Unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

ANDROID_ARGS=()
TVOS_ARGS=()
if [ "$UPLOAD" = "1" ]; then
  ANDROID_ARGS+=(--upload)
  TVOS_ARGS+=(--upload)
fi

if [ "$SKIP_ANDROID_TV" != "1" ]; then
  "$ROOT/scripts/deploy-android-tv.sh" "${ANDROID_ARGS[@]}"
fi

if [ "$SKIP_TVOS" != "1" ]; then
  "$ROOT/scripts/deploy-tvos.sh" "${TVOS_ARGS[@]}"
fi
