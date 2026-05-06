#!/usr/bin/env bash
# Rsync the checked-out repo to the remote box under /opt/yaver.
# Skips heavy build/test output dirs to keep the copy fast.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=/dev/null
source "$here/common.sh"

: "${HCLOUD_SSH_PRIVATE_KEY_PATH:?set HCLOUD_SSH_PRIVATE_KEY_PATH to the CI key}"

ip="$(cat "$CI_ARTIFACTS/server-ip")"
repo_root="${REPO_ROOT:-$(pwd)}"
remote_repo_dir="${REMOTE_REPO_DIR:-/opt/yaver}"

log "clear volatile remote paths under $remote_repo_dir"
ssh -i "$HCLOUD_SSH_PRIVATE_KEY_PATH" \
  -o StrictHostKeyChecking=no \
  -o UserKnownHostsFile=/dev/null \
  "root@$ip" "
    rm -rf \
      '$remote_repo_dir'/mobile/android/.gradle \
      '$remote_repo_dir'/mobile/android/app/.cxx \
      '$remote_repo_dir'/mobile/android/app/build \
      '$remote_repo_dir'/mobile/assets/models \
      '$remote_repo_dir'/scripts/screenshots/output* \
      '$remote_repo_dir'/scripts/generate-demo-videos/output \
      '$remote_repo_dir'/relay/relay-linux-amd64 \
      '$remote_repo_dir'/cli/hermesc \
      '$remote_repo_dir'/demo \
      '$remote_repo_dir'/demos \
      '$remote_repo_dir'/videos
    mkdir -p '$remote_repo_dir'
  "

log "rsync $repo_root -> root@$ip:$remote_repo_dir"
rsync -az --delete-after \
  -e "ssh -i $HCLOUD_SSH_PRIVATE_KEY_PATH -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null" \
  --exclude '.git' \
  --exclude 'node_modules' \
  --exclude '.next' \
  --exclude 'web/.open-next' \
  --exclude 'dist' \
  --exclude 'videos' \
  --exclude 'demo' \
  --exclude 'demos' \
  --exclude 'mobile/.expo' \
  --exclude 'mobile/ios/Pods' \
  --exclude 'mobile/android/.gradle' \
  --exclude 'mobile/android/app/.cxx' \
  --exclude 'mobile/android/app/build' \
  --exclude 'mobile/assets/models' \
  --exclude 'scripts/screenshots/output*' \
  --exclude 'scripts/generate-demo-videos/output' \
  --exclude 'relay/relay-linux-amd64' \
  --exclude 'cli/hermesc' \
  --exclude 'sdk/**/build' \
  --exclude 'desktop/installer/dist' \
  --exclude 'desktop/agent/agent' \
  --exclude 'desktop/agent/agent.test' \
  --exclude 'desktop/agent/yaver' \
  --exclude 'desktop/agent/yaver-*' \
  --exclude 'ci/.artifacts' \
  "$repo_root/" "root@$ip:$remote_repo_dir/"

# rsync -az preserves source uid:gid, so a Mac-side 501:staff lands on
# the Linux box and trips codex's bwrap sandbox (drops CAP_DAC_OVERRIDE,
# can't write into a foreign-owned dir). Force ownership to root since
# the test box runs the agent as root.
log "normalize ownership at $remote_repo_dir to root:root"
ssh -i "$HCLOUD_SSH_PRIVATE_KEY_PATH" \
  -o StrictHostKeyChecking=no \
  -o UserKnownHostsFile=/dev/null \
  "root@$ip" "chown -R root:root '$remote_repo_dir'"
