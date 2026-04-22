#!/usr/bin/env bash
# Take a snapshot of the current test box. Used to cheaply preserve
# an installed state before deleting (Hetzner bills stopped servers
# at full rate — a snapshot is pennies/month).
#
# Reads server name from ci/.artifacts/server-name or from the
# argument, in that order.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=/dev/null
source "$here/common.sh"

name="${1:-}"
if [ -z "$name" ] && [ -f "$CI_ARTIFACTS/server-name" ]; then
  name="$(cat "$CI_ARTIFACTS/server-name")"
fi

if [ -z "$name" ]; then
  echo "usage: $0 <server-name>" >&2
  exit 2
fi

desc="yaver-ci snapshot of $name at $(date -u +%Y-%m-%dT%H:%M:%SZ)"
log "snapshotting $name"
hcloud server create-image --type snapshot --description "$desc" "$name" -o json \
  | tee "$CI_ARTIFACTS/snapshot.json"
