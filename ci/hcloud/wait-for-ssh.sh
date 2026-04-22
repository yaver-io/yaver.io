#!/usr/bin/env bash
# Poll port 22 until sshd accepts connections. Assumes
# create-server.sh wrote the IP to ci/.artifacts/server-ip.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=/dev/null
source "$here/common.sh"

: "${HCLOUD_SSH_PRIVATE_KEY_PATH:?set HCLOUD_SSH_PRIVATE_KEY_PATH to the CI key}"

ip="$(cat "$CI_ARTIFACTS/server-ip")"
log "waiting for ssh on $ip"

for i in $(seq 1 60); do
  if ssh -i "$HCLOUD_SSH_PRIVATE_KEY_PATH" \
        -o StrictHostKeyChecking=no \
        -o UserKnownHostsFile=/dev/null \
        -o ConnectTimeout=5 \
        -o BatchMode=yes \
        "root@$ip" 'echo SSH_READY' 2>/dev/null | grep -q SSH_READY; then
    log "ssh ready after $i attempts"
    exit 0
  fi
  sleep 3
done

log "ssh never became ready"
exit 1
