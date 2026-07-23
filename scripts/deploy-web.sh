#!/usr/bin/env bash
set -euo pipefail

# Deploy yaver.io web to Cloudflare Workers.
# Builds with @opennextjs/cloudflare and deploys via wrangler.
# Enforces a 15 MB cap on the web/ source tree (excluding
# node_modules, .next, .open-next). Matches the CI guard in
# release-web.yml (raised 10→15 MB in ddd5868d — demo videos push it over).
#
# Credentials (CLOUDFLARE_API_TOKEN, CLOUDFLARE_ACCOUNT_ID) can come from
# the existing environment OR from the Yaver vault (project="web" plus
# globals). Vault values win when present — the vault is the deliberate
# source of truth. To bypass (e.g. in CI), don't store the values in
# the vault and set them via GitHub secrets instead.

if command -v yaver >/dev/null 2>&1; then
  eval "$(yaver vault env --project web 2>/dev/null || true)"
  # Pull the mobile-project vault too — passkey assetlinks need the
  # Play app-signing SHA-256 from there. `yaver vault add
  # ANDROID_RELEASE_SHA256 --project mobile --value <fingerprint>` is
  # how the user feeds in the Play Console value without committing
  # it to the repo.
  eval "$(yaver vault env --project mobile 2>/dev/null || true)"
fi

# Vault-locked fallback. After kivanc's auth token rotates, `yaver vault
# env` returns "wrong passphrase" until YAVER_VAULT_PASSPHRASE is set
# to the previous token. Without this fallback, deploy-web silently
# ships assetlinks.json without ANDROID_RELEASE_SHA256, breaking
# passkey on Play-distributed Android builds. Source a gitignored env
# file if present — same pattern as ~/.appstoreconnect/yaver.env for
# TestFlight (see CLAUDE.md "TestFlight env-file fallback"). Vault
# values still win when readable; this only fills gaps.
if [ -f "$HOME/.androidplay/yaver.env" ]; then
  # shellcheck source=/dev/null
  set -a; source "$HOME/.androidplay/yaver.env"; set +a
fi

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEPLOY_DIR="$REPO_ROOT/web"
MAX_SIZE_MB=15

# Append the production Play app-signing SHA-256 to assetlinks.json
# right before the build when ANDROID_RELEASE_SHA256 is in the vault.
# The dev keystore fingerprint stays in tracked assetlinks.json for
# `yaver wireless push` testing; this step adds the prod one without
# committing it. Skipped silently when the vault key is empty.
ASSETLINKS_PATH="$REPO_ROOT/web/public/.well-known/assetlinks.json"
if [ -f "$ASSETLINKS_PATH" ] && [ -n "${ANDROID_RELEASE_SHA256:-}" ]; then
  if command -v jq >/dev/null 2>&1; then
    SHA="$ANDROID_RELEASE_SHA256"
    TMP="$(mktemp)"
    jq --arg sha "$SHA" '
      (.[0].target.sha256_cert_fingerprints) as $existing
      | if ($existing | index($sha)) then .
        else .[0].target.sha256_cert_fingerprints += [$sha]
        end
    ' "$ASSETLINKS_PATH" > "$TMP" && mv "$TMP" "$ASSETLINKS_PATH"
    echo "assetlinks.json: production SHA-256 merged from yaver vault (mobile project)."
  else
    echo "WARN: jq not found — skipping ANDROID_RELEASE_SHA256 merge into assetlinks.json."
  fi
fi

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

# 1b. AASA shadow guard (incident 2026-07-23).
# A physical file at public/.well-known/apple-app-site-association is served by
# Cloudflare's static-assets binding BEFORE the Next rewrite reaches the route
# handler that emits the correct JSON + application/json content-type. When it
# last existed it also nested `webcredentials` inside `applinks`, which broke
# in-app passkey / Face ID sign-in (web was unaffected). The canonical AASA is
# web/app/api/apple-app-site-association/route.ts — never a static file.
SHADOW_AASA="$DEPLOY_DIR/public/.well-known/apple-app-site-association"
if [ -e "$SHADOW_AASA" ]; then
  echo "ERROR: $SHADOW_AASA exists — it shadows the AASA route handler and breaks"
  echo "       in-app passkey / Sign in with Apple. Delete it; the route serves the AASA."
  exit 1
fi

# 2. Build and deploy
cd "$DEPLOY_DIR"
npm run deploy
