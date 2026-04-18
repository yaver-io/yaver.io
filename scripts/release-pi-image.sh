#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSIONS_FILE="$ROOT_DIR/versions.json"
DEFAULT_OUTPUT_DIR="$ROOT_DIR/dist/pi-image"

usage() {
  cat <<'EOF'
Usage:
  scripts/release-pi-image.sh [options]

Options:
  --version <semver>      Override versions.json piImage version
  --build                 Build the image before printing release steps
  --docker                When used with --build, build through Docker wrapper
  --upload-downloads      Upload built artifact to Convex downloads after build
  --output-dir <path>     Artifact directory (default: dist/pi-image)
  -h, --help              Show help
EOF
}

VERSION=""
DO_BUILD=0
USE_DOCKER=0
UPLOAD_DOWNLOADS=0
OUTPUT_DIR="$DEFAULT_OUTPUT_DIR"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      VERSION="${2:-}"
      shift 2
      ;;
    --build)
      DO_BUILD=1
      shift
      ;;
    --docker)
      USE_DOCKER=1
      shift
      ;;
    --upload-downloads)
      UPLOAD_DOWNLOADS=1
      shift
      ;;
    --output-dir)
      OUTPUT_DIR="${2:-}"
      shift 2
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

if [[ -z "$VERSION" ]]; then
  VERSION="$(node -e "console.log(JSON.parse(require('fs').readFileSync('$VERSIONS_FILE','utf8')).piImage || '')")"
fi

if [[ -z "$VERSION" ]]; then
  echo "piImage version missing from versions.json" >&2
  exit 1
fi

ARTIFACT="$OUTPUT_DIR/yaver-pi5-devnode-arm64.img.xz"

if [[ "$DO_BUILD" -eq 1 ]]; then
  build_args=(--version "$VERSION" --output-dir "$OUTPUT_DIR")
  if [[ "$USE_DOCKER" -eq 1 ]]; then
    build_args+=(--docker)
  fi
  "$ROOT_DIR/scripts/build-pi-image.sh" "${build_args[@]}"
fi

if [[ "$UPLOAD_DOWNLOADS" -eq 1 ]]; then
  if [[ ! -f "$ARTIFACT" ]]; then
    echo "artifact not found: $ARTIFACT" >&2
    exit 1
  fi
  (cd "$ROOT_DIR/backend" && DOWNLOADS_DIR="$OUTPUT_DIR" node ../scripts/upload-downloads.mjs)
fi

cat <<EOF
Pi image release checklist
  Version:  $VERSION
  Artifact: $ARTIFACT

Next steps
  1. Verify the artifact exists and boots on a Pi 5.
  2. Push tag: git tag pi-image/v$VERSION && git push origin pi-image/v$VERSION
  3. Approve the GitHub Actions release workflow in the production environment.
  4. Publish the drafted GitHub release after smoke test passes.
EOF
