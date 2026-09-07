#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT/scripts/publish-feedback-rn.sh"

auth_line="$(grep -n 'npm whoami' "$SCRIPT" | head -n 1 | cut -d: -f1 || true)"
install_line="$(grep -n '^npm ci$' "$SCRIPT" | head -n 1 | cut -d: -f1 || true)"

if [ -z "$auth_line" ] || [ -z "$install_line" ] || [ "$auth_line" -ge "$install_line" ]; then
  echo "FAIL: npm authentication must be probed before dependency installation" >&2
  exit 1
fi

grep -q 'npm login --auth-type=web' "$SCRIPT" || {
  echo "FAIL: npm auth failure must name the local recovery command" >&2
  exit 1
}

grep -q 'NPM_PUBLISH_VERIFY_TIMEOUT_SECONDS:-300' "$SCRIPT" || {
  echo "FAIL: accepted npm publishes need a registry-processing verification window" >&2
  exit 1
}

grep -q 'npm accepted .* but did not serve it within' "$SCRIPT" || {
  echo "FAIL: publish verification failure must distinguish acceptance from registry visibility" >&2
  exit 1
}

echo "PASS: feedback SDK publish authenticates early and tolerates registry processing"
