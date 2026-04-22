#!/usr/bin/env bash
# Delete the ephemeral server. ALWAYS run this in cleanup, even on
# prior-step failure. Tolerates already-missing resources so
# re-running is safe.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=/dev/null
source "$here/common.sh"

id=""
if [ -f "$CI_ARTIFACTS/server-id" ]; then
  id="$(cat "$CI_ARTIFACTS/server-id")"
fi

# Fallback: resolve by name.
if [ -z "$id" ]; then
  id="$(hcloud server list -o noheader -o columns=id,name \
        | awk -v n="$CI_SERVER_NAME" '$2==n {print $1; exit}')"
fi

if [ -z "$id" ]; then
  log "no server to delete (name=$CI_SERVER_NAME)"
  exit 0
fi

log "deleting server id=$id"
if ! hcloud server delete "$id" 2>&1; then
  log "delete returned non-zero (server may already be gone)"
fi
