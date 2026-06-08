#!/usr/bin/env bash
# publish-android-rootfs.sh — upload the prebuilt Android on-device sandbox rootfs
# tarball to the kivanccakmak/yaver-models GitHub Release so RootfsInstaller.kt
# can download it on first enable. This is the asset pinned by
# mobile/src/lib/sandboxRootfsManifest.ts (ROOTFS_MANIFEST.url).
#
# Gated by design: this publishes a ~38 MB binary to a PUBLIC release and needs
# `gh` auth. It does NOT run in any automated flow — invoke it explicitly.
#
# Usage:
#   scripts/build-android-rootfs-alpine-arm64.sh --version <ver>   # produce tarball
#   scripts/publish-android-rootfs.sh <ver>                        # upload it
#
# After upload, update sandboxRootfsManifest.ts (version/sha256/sizeBytes) and
# flip ROOTFS_PUBLISHED to true.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODELS_REPO="kivanccakmak/yaver-models"
ASSET="yaver-rootfs-alpine-arm64.tar.gz"
TARBALL="$REPO_ROOT/out/android-rootfs/$ASSET"

VERSION="${1:-}"
if [[ -z "$VERSION" ]]; then
  echo "usage: $0 <version>   (e.g. 2026-06-08-1)" >&2
  exit 2
fi
TAG="rootfs-$VERSION"

if ! command -v gh >/dev/null 2>&1; then
  echo "ERROR: gh CLI not found — install + 'gh auth login' first." >&2
  exit 2
fi
if [[ ! -f "$TARBALL" ]]; then
  echo "ERROR: $TARBALL not found." >&2
  echo "Build it first: scripts/build-android-rootfs-alpine-arm64.sh --version $VERSION" >&2
  exit 2
fi

SHA="$(shasum -a 256 "$TARBALL" | cut -d' ' -f1)"
SIZE="$(wc -c < "$TARBALL" | tr -d ' ')"
echo "==> Tarball: $TARBALL"
echo "    version : $VERSION"
echo "    sha256  : $SHA"
echo "    size    : $SIZE bytes"

# The models repo is a release host only; create it if missing (public, no code).
if ! gh repo view "$MODELS_REPO" >/dev/null 2>&1; then
  echo "==> $MODELS_REPO does not exist — creating (public)."
  gh repo create "$MODELS_REPO" --public \
    --description "Yaver downloadable assets (on-device rootfs, model weights)" \
    --add-readme
fi

# Create-or-update the release, then upload (clobbering an existing same-name asset).
if gh release view "$TAG" -R "$MODELS_REPO" >/dev/null 2>&1; then
  echo "==> Release $TAG exists — uploading asset (clobber)."
else
  echo "==> Creating release $TAG."
  gh release create "$TAG" -R "$MODELS_REPO" \
    --title "Android sandbox rootfs $VERSION" \
    --notes "Alpine arm64 proot rootfs for the Yaver Android on-device sandbox.
sha256: \`$SHA\`
size: $SIZE bytes
Consumed by RootfsInstaller.kt via mobile/src/lib/sandboxRootfsManifest.ts."
fi
gh release upload "$TAG" "$TARBALL" -R "$MODELS_REPO" --clobber

echo
echo "==> Uploaded. Now update mobile/src/lib/sandboxRootfsManifest.ts:"
echo "      version:   \"$VERSION\""
echo "      sha256:    \"$SHA\""
echo "      sizeBytes: $SIZE"
echo "    and set ROOTFS_PUBLISHED = true"
echo
echo "    URL: https://github.com/$MODELS_REPO/releases/download/$TAG/$ASSET"
