#!/usr/bin/env bash
set -euo pipefail

# Deploy the payload-minimal Cloudflare Access Channel. This is deliberately a
# separate Worker from the retired inference gateway: it parses only the public
# accessSignal grammar and has no provider credentials or message retention.
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEPLOY_DIR="$REPO_ROOT/access-channel"

if [ ! -f "$DEPLOY_DIR/node_modules/wrangler/bin/wrangler.js" ]; then
  echo "Access Channel dependencies missing — running npm ci."
  (cd "$DEPLOY_DIR" && npm ci --ignore-scripts)
fi

echo "Testing and type-checking the Access Channel before deploy..."
(cd "$DEPLOY_DIR" && npm test && npm run typecheck)

echo "Deploying Yaver Access Channel to Cloudflare..."
(cd "$DEPLOY_DIR" && npx wrangler deploy)
