#!/bin/bash
set -eo pipefail

# Deploy backend/convex/* to Convex prod (perceptive-minnow-557).
#
# Portable across machines. Secrets resolution order, first hit wins:
#
#   1. CONVEX_DEPLOY_KEY_3 / _2 / CONVEX_DEPLOY_KEY env var (CI path; GitHub
#      Actions sets this from the repo secret, and local devs export it or use
#      a gitignored env file)
#
# `yaver vault` is deliberately not consulted: a v2 vault is master-key
# encrypted and unrecoverable if that key is lost, and the call swallowed its
# own failure so the deploy reported a missing key rather than a locked vault.
#
# Paste it inline for a one-shot deploy:
#
#   CONVEX_DEPLOY_KEY_3=<key> ./deploy/deploy.sh backend
#
# The key is the deploy key from Convex dashboard
# (perceptive-minnow-557 → Settings → Deploy Keys).

cd "$(dirname "$0")/.."

if [ -f "$HOME/.convex/yaver.env" ]; then
  # shellcheck source=/dev/null
  set -a; source "$HOME/.convex/yaver.env"; set +a
fi

# Prefer the newest rotated deploy key if present; older names still work as
# fallbacks for CI workflows or local machines that have not switched yet.
# `npx convex` only reads CONVEX_DEPLOY_KEY, so promote whichever variant is
# set into the canonical name.
if [ -n "${CONVEX_DEPLOY_KEY_3:-}" ]; then
  export CONVEX_DEPLOY_KEY="$CONVEX_DEPLOY_KEY_3"
elif [ -n "${CONVEX_DEPLOY_KEY_2:-}" ]; then
  export CONVEX_DEPLOY_KEY="$CONVEX_DEPLOY_KEY_2"
fi

if [ -z "${CONVEX_DEPLOY_KEY:-}" ]; then
  echo "ERROR: CONVEX_DEPLOY_KEY_3 / CONVEX_DEPLOY_KEY_2 / CONVEX_DEPLOY_KEY is not set." >&2
  echo >&2
  echo "Pick one:" >&2
  echo "  1. CONVEX_DEPLOY_KEY_3=<key> ./deploy/deploy.sh backend" >&2
  echo "  2. add it to a gitignored env file you source before running this" >&2
  echo >&2
  echo '(`yaver vault` is deliberately NOT an option: the vault ships off in v1,' >&2
  echo " and a v2 vault is unrecoverable if its master key is lost, so no deploy" >&2
  echo " may depend on it.)" >&2
  echo >&2
  echo "The key lives at https://dashboard.convex.dev/d/perceptive-minnow-557/settings/deploy-keys" >&2
  exit 2
fi

echo "Deploying backend/convex to Convex prod..."
cd backend
# A clean release worktree intentionally has no node_modules. Letting npx fetch
# only the Convex CLI is insufficient because esbuild must also resolve the
# pinned convex/server package while bundling convex.config.js.
echo "Restoring pinned backend dependencies..."
npm ci --no-audit --no-fund
npx convex deploy --yes

# A schema/function deploy does not mutate existing catalog rows. Keep the
# release atomic from the product's point of view: when clients start reading
# the new model/reasoning shape, production data must already contain it.
echo "Synchronizing the production model catalog..."
npx convex run aiModels:seed --prod
