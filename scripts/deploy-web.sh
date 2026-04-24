#!/usr/bin/env bash
set -euo pipefail

# Deploy yaver.io web to Cloudflare Workers.
# Builds with @opennextjs/cloudflare and deploys via wrangler.
# Enforces a 10 MB cap on the web/ source tree (excluding
# node_modules, .next, .open-next).
#
# Credentials (CLOUDFLARE_API_TOKEN, CLOUDFLARE_ACCOUNT_ID) can come from
# the existing environment OR from the Yaver vault (project="web" plus
# globals). Vault values win when present — the vault is the deliberate
# source of truth. To bypass (e.g. in CI), don't store the values in
# the vault and set them via GitHub secrets instead.

if command -v yaver >/dev/null 2>&1; then
  eval "$(yaver vault env --project web 2>/dev/null || true)"
fi

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEPLOY_DIR="$REPO_ROOT/web"
MAX_SIZE_MB=10

# 1. Calculate deployed directory size (excluding node_modules and .next)
SIZE_KB=$(find "$DEPLOY_DIR" \
  -not -path '*/node_modules/*' \
  -not -path '*/.next/*' \
  -not -path '*/.open-next/*' \
  -type f -print0 \
  | xargs -0 stat -f%z 2>/dev/null \
  | awk '{s+=$1} END {printf "%.0f", s/1024}')

# Fallback for Linux
if [ -z "$SIZE_KB" ] || [ "$SIZE_KB" = "0" ]; then
  SIZE_KB=$(du -sk --exclude='node_modules' --exclude='.next' --exclude='.open-next' "$DEPLOY_DIR" 2>/dev/null | awk '{print $1}')
fi

SIZE_MB=$(awk "BEGIN {printf \"%.2f\", $SIZE_KB / 1024}")
echo "Source size (excl build artifacts): ${SIZE_MB} MB"

MAX_SIZE_KB=$((MAX_SIZE_MB * 1024))
if [ "$SIZE_KB" -gt "$MAX_SIZE_KB" ]; then
  echo "ERROR: web/ is ${SIZE_MB} MB — exceeds ${MAX_SIZE_MB} MB limit."
  exit 1
fi

echo "Size OK. Building and deploying to Cloudflare..."

# 2. Build and deploy
cd "$DEPLOY_DIR"
npm run deploy
