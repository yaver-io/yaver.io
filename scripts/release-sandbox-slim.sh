#!/usr/bin/env bash
#
# release-sandbox-slim.sh — local mirror of the build-yaver-sandbox-slim
# GitHub Actions workflow. Multi-arch (amd64+arm64) buildx, then push to
# both GHCR and Docker Hub.
#
# Per CLAUDE.md "Local deploy first, CI second" — this script is the
# canonical path. The GH workflow is just the same commands wrapped in
# YAML for unattended/scheduled runs.
#
# Prerequisites:
#   1. Docker Desktop running with buildx enabled (Mac default since 4.x)
#   2. `docker login ghcr.io -u <gh-user> -p <PAT>` — PAT needs write:packages
#   3. `docker login` to Docker Hub — Hub credentials from your account
#      (optional; pass --skip-dockerhub to push to GHCR only)
#
# Usage:
#   ./scripts/release-sandbox-slim.sh
#   ./scripts/release-sandbox-slim.sh --skip-dockerhub
#   ./scripts/release-sandbox-slim.sh --owner some-org   # GHCR namespace override
#   ./scripts/release-sandbox-slim.sh --version 1.99.222 # tag override
#
# After a successful push:
#   * ghcr.io/<owner>/yaver-sandbox-slim:latest
#   * ghcr.io/<owner>/yaver-sandbox-slim:<version>
#   * yaver/sandbox-slim:latest             (unless --skip-dockerhub)
#   * yaver/sandbox-slim:<version>

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

OWNER="${YAVER_GHCR_OWNER:-kivanccakmak}"
DOCKERHUB_NAMESPACE="${YAVER_DOCKERHUB_NAMESPACE:-yaver}"
DOCKERHUB_REPO="${YAVER_DOCKERHUB_REPO:-sandbox-slim}"
SKIP_DOCKERHUB=0
VERSION=""

usage() {
  cat <<'EOF'
Usage:
  scripts/release-sandbox-slim.sh [options]

Options:
  --owner <gh-user>          GHCR namespace (default: kivanccakmak)
  --version <semver>         Tag override (default: versions.json#cli)
  --skip-dockerhub           Push to GHCR only (skip yaver/sandbox-slim)
  --skip-ghcr                Push to Docker Hub only (rare)
  --dry-run                  Build but don't push
  -h, --help                 This message
EOF
}

SKIP_GHCR=0
DRY_RUN=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --owner)            OWNER="${2:-}"; shift 2 ;;
    --version)          VERSION="${2:-}"; shift 2 ;;
    --skip-dockerhub)   SKIP_DOCKERHUB=1; shift ;;
    --skip-ghcr)        SKIP_GHCR=1; shift ;;
    --dry-run)          DRY_RUN=1; shift ;;
    -h|--help)          usage; exit 0 ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

require() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 1
  }
}

require docker
docker info >/dev/null 2>&1 || {
  echo "docker daemon not running. Start Docker Desktop, then re-run." >&2
  exit 1
}

# Resolve version: --version flag wins, else versions.json#cli, else dev.
if [[ -z "$VERSION" ]]; then
  if [[ -f "$ROOT_DIR/versions.json" ]]; then
    VERSION="$(node -e "console.log(JSON.parse(require('fs').readFileSync('$ROOT_DIR/versions.json','utf8')).cli)" 2>/dev/null || true)"
  fi
fi
[[ -n "$VERSION" ]] || VERSION="dev"
echo "[release-sandbox-slim] version=$VERSION"

# Build the tag list. Multiple --tag flags = same manifest, multiple
# names. buildx handles cross-registry pushes in a single build (no
# separate push per registry).
TAGS=()
if [[ "$SKIP_GHCR" -ne 1 ]]; then
  TAGS+=("--tag" "ghcr.io/$OWNER/yaver-sandbox-slim:latest")
  TAGS+=("--tag" "ghcr.io/$OWNER/yaver-sandbox-slim:$VERSION")
fi
if [[ "$SKIP_DOCKERHUB" -ne 1 ]]; then
  TAGS+=("--tag" "$DOCKERHUB_NAMESPACE/$DOCKERHUB_REPO:latest")
  TAGS+=("--tag" "$DOCKERHUB_NAMESPACE/$DOCKERHUB_REPO:$VERSION")
fi
if [[ ${#TAGS[@]} -eq 0 ]]; then
  echo "Nothing to push (both --skip-ghcr and --skip-dockerhub set)." >&2
  exit 2
fi

# Belt-and-suspenders: ensure the user is actually logged in to the
# registries we're about to push to. `docker buildx build --push` fails
# with a cryptic 401 otherwise.
if [[ "$DRY_RUN" -ne 1 ]]; then
  if [[ "$SKIP_GHCR" -ne 1 ]]; then
    if ! docker info 2>/dev/null | grep -qi "ghcr.io"; then
      if [[ ! -f "$HOME/.docker/config.json" ]] || ! grep -q "ghcr.io" "$HOME/.docker/config.json" 2>/dev/null; then
        echo "Not logged in to ghcr.io. Run:" >&2
        echo "  echo \$GHCR_PAT | docker login ghcr.io -u \$YOUR_GH_USERNAME --password-stdin" >&2
        echo "Your PAT needs the 'write:packages' scope." >&2
        exit 1
      fi
    fi
  fi
  if [[ "$SKIP_DOCKERHUB" -ne 1 ]]; then
    if [[ ! -f "$HOME/.docker/config.json" ]] || ! grep -q "https://index.docker.io" "$HOME/.docker/config.json" 2>/dev/null; then
      echo "Not logged in to Docker Hub. Run:" >&2
      echo "  docker login                              # default registry = Docker Hub" >&2
      echo "Or pass --skip-dockerhub to push to GHCR only." >&2
      exit 1
    fi
  fi
fi

# Ensure the builder exists. The default `docker buildx` builder on
# Docker Desktop is a single-platform 'desktop-linux' driver — won't do
# multi-arch in one shot. Create a dedicated 'yaver' builder using the
# docker-container driver which DOES support multi-arch via emulation.
if ! docker buildx inspect yaver-multiarch >/dev/null 2>&1; then
  echo "[release-sandbox-slim] creating buildx builder 'yaver-multiarch'"
  docker buildx create --name yaver-multiarch --driver docker-container --use >/dev/null
else
  docker buildx use yaver-multiarch >/dev/null
fi

PUSH_FLAG="--push"
if [[ "$DRY_RUN" -eq 1 ]]; then
  PUSH_FLAG="--load"     # local-only build, single-arch (multi-arch can't --load)
  PLATFORMS="linux/$(uname -m | sed 's/aarch64/arm64/; s/x86_64/amd64/')"
  echo "[release-sandbox-slim] DRY RUN: building single-arch $PLATFORMS for local load"
else
  PLATFORMS="linux/amd64,linux/arm64"
fi

echo "[release-sandbox-slim] building $PLATFORMS — this takes ~10 min on a cold cache"
docker buildx build \
  --file "$ROOT_DIR/desktop/agent/Dockerfile.sandbox.slim" \
  --platform "$PLATFORMS" \
  $PUSH_FLAG \
  "${TAGS[@]}" \
  "$ROOT_DIR/desktop/agent"

echo
echo "[release-sandbox-slim] done"
if [[ "$DRY_RUN" -eq 1 ]]; then
  echo "  (dry run — image is in your local Docker daemon, not pushed)"
else
  echo "  pulled by users via:"
  if [[ "$SKIP_GHCR" -ne 1 ]]; then
    echo "    docker pull ghcr.io/$OWNER/yaver-sandbox-slim:latest"
  fi
  if [[ "$SKIP_DOCKERHUB" -ne 1 ]]; then
    echo "    docker pull $DOCKERHUB_NAMESPACE/$DOCKERHUB_REPO:latest"
  fi
fi
