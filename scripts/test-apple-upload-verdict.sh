#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
source "$ROOT/scripts/apple-xcode-auth.sh"
log=$(mktemp)
trap 'rm -f "$log"' EXIT
printf '%s\n' 'ERROR: HTTP 500' 'UPLOAD SUCCEEDED with no errors' > "$log"
apple_upload_log_succeeded "$log"
for verdict in 'ERROR: HTTP 500' 'UPLOAD FAILED' 'No final verdict'; do
  printf '%s\n' "$verdict" > "$log"
  if apple_upload_log_succeeded "$log"; then echo "accepted failure: $verdict"; exit 1; fi
done
printf '%s\n' 'UPLOAD SUCCEEDED with no errors' 'ERROR: server rejected completion' > "$log"
if apple_upload_log_succeeded "$log"; then echo 'accepted later rejection'; exit 1; fi
echo 'Apple upload verdict tests passed'
