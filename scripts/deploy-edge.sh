#!/usr/bin/env bash
set -euo pipefail

# Deploy the Yaver identity + automatic tunnel edge only. This intentionally
# does not deploy the web dashboard, so transport changes can converge without
# consuming an unrelated web build/deploy.

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
EDGE_DIR="$REPO_ROOT/cloudflare/edge"

command -v node >/dev/null 2>&1 || { echo "ERROR: Node.js is required." >&2; exit 2; }
command -v npm >/dev/null 2>&1 || { echo "ERROR: npm is required." >&2; exit 2; }

cd "$EDGE_DIR"

echo "→ installing locked Yaver Edge toolchain"
npm ci

echo "→ auditing deployment toolchain"
npm audit --audit-level=moderate

echo "→ testing Yaver Edge"
npm test
npm run typecheck

if ! npx wrangler secret list --format json | node -e '
let input = "";
process.stdin.on("data", chunk => input += chunk);
process.stdin.on("end", () => {
  const rows = JSON.parse(input);
  if (!Array.isArray(rows) || !rows.some(row => row.name === "DEVICE_ID_HMAC_KEY")) process.exit(1);
});'; then
  echo "ERROR: DEVICE_ID_HMAC_KEY is not installed for yaver-edge." >&2
  echo "Install it through the operator-owned Wrangler credential store; never put it in the repo." >&2
  exit 2
fi

echo "→ applying remote D1 migrations"
npx wrangler d1 migrations apply yaver-edge --remote

echo "→ deploying isolated edge.yaver.io worker"
npx wrangler deploy

echo "→ probing the deployed operation"
health=""
for attempt in $(seq 1 30); do
  if health="$(curl --fail --silent --show-error --max-time 5 https://edge.yaver.io/health 2>/dev/null)"; then
    break
  fi
  # A VPN-scoped macOS resolver can retain an NXDOMAIN/AAAA-only view for a
  # newly-created custom domain even after Cloudflare authoritative DNS serves
  # IPv4. Deployment verification may ask Cloudflare DNS directly and pin that
  # one probe; product traffic continues to use the system resolver.
  if command -v dig >/dev/null 2>&1; then
    edge_ip="$(dig @1.1.1.1 +short edge.yaver.io A 2>/dev/null | head -1)"
    if [ -n "$edge_ip" ] && health="$(curl --fail --silent --show-error --max-time 5 --resolve "edge.yaver.io:443:$edge_ip" https://edge.yaver.io/health 2>/dev/null)"; then
      break
    fi
  fi
  if [ "$attempt" -lt 30 ]; then
    sleep 2
  fi
done
if [ -z "$health" ]; then
  echo "ERROR: edge.yaver.io did not resolve and answer /health within 60 seconds." >&2
  exit 1
fi
if ! node -e '
const result = JSON.parse(process.argv[1]);
if (result.ok !== true || result.service !== "yaver-edge" || result.storesUserContent !== false) process.exit(1);
' "$health"; then
  echo "ERROR: deployed edge health response does not satisfy the zero-content contract." >&2
  exit 1
fi

echo "✓ yaver-edge deployed and operation-level health passed"
