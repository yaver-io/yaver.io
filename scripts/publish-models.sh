#!/usr/bin/env bash
# publish-models.sh — one-time/occasional publisher for the on-device LLM
# weights the mobile app downloads at runtime.
#
# WHY THIS IS MANUAL: the GGUF weights (0.8–2.2 GB each) are NOT in any git
# repo and are NOT uploaded by Claude. This script — run by you, with your
# GitHub auth — fetches each model from Hugging Face, uploads it as a GitHub
# Release asset on kivanccakmak/yaver-models, and prints the sha256 to paste
# back into mobile/src/lib/localAgent/models.ts (the `sha256` fields, currently
# empty placeholders). The app verifies that sha256 after download.
#
# Hosting choice: GitHub Releases (NOT in-repo) — up to 2 GB/asset, free
# CDN-backed bandwidth, ~100% uptime. Repo stays lean (Releases only).
#
# Requirements: gh (authed: `gh auth status`), curl, shasum/sha256sum.
#
# Usage:
#   scripts/publish-models.sh            # publish all models in the table
#   scripts/publish-models.sh router-v1  # publish only one release tag's models
#
# After it prints the sha256s, paste them into models.ts and ship a mobile
# build. Bundled router needs NO upload (it ships inside the app binary).

set -euo pipefail

REPO="kivanccakmak/yaver-models"
WORKDIR="${TMPDIR:-/tmp}/yaver-models-publish"
mkdir -p "$WORKDIR"

# model_id | release_tag | asset_filename | huggingface_url
# Keep asset_filename + tag in sync with the downloadUrl in models.ts:
#   https://github.com/kivanccakmak/yaver-models/releases/download/<tag>/<asset>
# HF URLs point at the Q4_K_M GGUF in each model's GGUF repo (update if a repo moves).
MODELS=(
  "qwen2.5-1.5b-instruct-q4|router-v1|qwen2.5-1.5b-instruct-q4_k_m.gguf|https://huggingface.co/Qwen/Qwen2.5-1.5B-Instruct-GGUF/resolve/main/qwen2.5-1.5b-instruct-q4_k_m.gguf"
  "qwen2.5-coder-1.5b-q4|coder-v1|qwen2.5-coder-1.5b-q4_k_m.gguf|https://huggingface.co/Qwen/Qwen2.5-Coder-1.5B-Instruct-GGUF/resolve/main/qwen2.5-coder-1.5b-instruct-q4_k_m.gguf"
  "qwen2.5-coder-3b-q4|coder-v1|qwen2.5-coder-3b-q4_k_m.gguf|https://huggingface.co/Qwen/Qwen2.5-Coder-3B-Instruct-GGUF/resolve/main/qwen2.5-coder-3b-instruct-q4_k_m.gguf"
)

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}';
  else shasum -a 256 "$1" | awk '{print $1}'; fi
}

ensure_repo() {
  if ! gh repo view "$REPO" >/dev/null 2>&1; then
    echo "Creating public repo $REPO …"
    gh repo create "$REPO" --public \
      --description "On-device LLM weights for the Yaver mobile app (GGUF, downloaded at runtime)." \
      --confirm
  fi
}

ensure_release() {
  local tag="$1"
  if ! gh release view "$tag" -R "$REPO" >/dev/null 2>&1; then
    echo "Creating release $tag …"
    gh release create "$tag" -R "$REPO" \
      --title "$tag" \
      --notes "On-device GGUF model assets ($tag). Downloaded + sha256-verified by the Yaver mobile app."
  fi
}

FILTER="${1:-}"
echo "=== Yaver model publisher → $REPO ==="
command -v gh >/dev/null || { echo "gh CLI required"; exit 1; }
gh auth status >/dev/null 2>&1 || { echo "Run 'gh auth login' first"; exit 1; }
ensure_repo

declare -a RESULTS
for row in "${MODELS[@]}"; do
  IFS='|' read -r id tag asset url <<<"$row"
  [ -n "$FILTER" ] && [ "$FILTER" != "$tag" ] && continue
  ensure_release "$tag"
  local_path="$WORKDIR/$asset"
  if [ ! -f "$local_path" ]; then
    echo "Downloading $id from Hugging Face …"
    curl -L --fail -o "$local_path" "$url"
  else
    echo "Using cached $local_path"
  fi
  sum="$(sha256_of "$local_path")"
  echo "Uploading $asset → $REPO ($tag) …"
  gh release upload "$tag" "$local_path" -R "$REPO" --clobber
  RESULTS+=("$id  sha256=$sum")
done

echo ""
echo "=== DONE. Paste these sha256 values into mobile/src/lib/localAgent/models.ts ==="
for r in "${RESULTS[@]}"; do echo "  $r"; done
echo ""
echo "Each model's downloadUrl is already:"
echo "  https://github.com/$REPO/releases/download/<tag>/<asset>"
echo "Set the matching sha256, rebuild the app, and runtime downloads will verify."
