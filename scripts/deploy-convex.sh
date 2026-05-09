#!/bin/bash
set -eo pipefail

# Deploy backend/convex/* to Convex prod (perceptive-minnow-557).
#
# Portable across machines. Secrets resolution order, first hit wins:
#
#   1. yaver vault env --project backend  (preferred for local devs;
#      sync via `yaver vault sync` so every machine has the key)
#   2. CONVEX_DEPLOY_KEY env var          (CI path; GitHub Actions sets
#      this from the repo secret of the same name)
#
# Set the vault entry once on any machine:
#
#   yaver vault add CONVEX_DEPLOY_KEY --project backend --value <key>
#   yaver vault sync   # push to your other machines
#
# Or paste it inline for a one-shot deploy:
#
#   CONVEX_DEPLOY_KEY=<key> ./scripts/deploy-convex.sh
#
# The key is the deploy key from Convex dashboard
# (perceptive-minnow-557 → Settings → Deploy Keys).

cd "$(dirname "$0")/.."

if command -v yaver >/dev/null 2>&1; then
  eval "$(yaver vault env --project backend 2>/dev/null || true)"
fi

# Prefer the rotated CONVEX_DEPLOY_KEY_2 if present; the older
# CONVEX_DEPLOY_KEY name still works as a fallback for CI workflows
# that haven't switched yet. `npx convex` only reads CONVEX_DEPLOY_KEY,
# so promote whichever variant is set into the canonical name.
if [ -n "${CONVEX_DEPLOY_KEY_2:-}" ]; then
  export CONVEX_DEPLOY_KEY="$CONVEX_DEPLOY_KEY_2"
fi

if [ -z "${CONVEX_DEPLOY_KEY:-}" ]; then
  echo "ERROR: CONVEX_DEPLOY_KEY / CONVEX_DEPLOY_KEY_2 is not set." >&2
  echo >&2
  echo "Pick one:" >&2
  echo "  1. yaver vault add CONVEX_DEPLOY_KEY_2 --project backend --value <key>" >&2
  echo "  2. CONVEX_DEPLOY_KEY_2=<key> $0" >&2
  echo >&2
  echo "The key lives at https://dashboard.convex.dev/d/perceptive-minnow-557/settings/deploy-keys" >&2
  exit 2
fi

echo "Deploying backend/convex to Convex prod..."
cd backend
exec npx convex deploy --yes
