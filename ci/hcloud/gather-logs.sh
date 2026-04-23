#!/usr/bin/env bash
# Collect logs from the remote box into ci/.artifacts/logs/ so the
# workflow can upload them as artifacts. Best-effort — never fails the
# run; even a partial log is useful for debugging.
set -uo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=/dev/null
source "$here/common.sh"

: "${HCLOUD_SSH_PRIVATE_KEY_PATH:?set HCLOUD_SSH_PRIVATE_KEY_PATH to the CI key}"

if [ ! -f "$CI_ARTIFACTS/server-ip" ]; then
  log "no server-ip — skipping log gather"
  exit 0
fi
ip="$(cat "$CI_ARTIFACTS/server-ip")"
dest="$CI_ARTIFACTS/logs"
mkdir -p "$dest"

ssh_opts=(-i "$HCLOUD_SSH_PRIVATE_KEY_PATH"
          -o StrictHostKeyChecking=no
          -o UserKnownHostsFile=/dev/null
          -o ConnectTimeout=10
          -o BatchMode=yes)

scp "${ssh_opts[@]}" "root@$ip:/var/log/yaver-*.log" "$dest/" 2>/dev/null || true
scp "${ssh_opts[@]}" "root@$ip:/var/log/yaver-ci/*.log" "$dest/" 2>/dev/null || true
ssh "${ssh_opts[@]}" "root@$ip" \
  'docker ps -a 2>/dev/null; echo ---; docker logs yaver 2>&1 | tail -200 || true; \
   echo ---; journalctl -u ollama --no-pager -n 200 2>/dev/null || true' \
  > "$dest/remote-summary.txt" 2>&1 || true

log "logs saved to $dest"
