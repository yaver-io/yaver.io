#!/usr/bin/env bash
# Provision an ephemeral CI server. If HETZNER_TEST_SNAPSHOT_ID is
# set, restore from the snapshot instead of a raw Ubuntu image — that
# way Docker/Ollama/etc. are already installed and bootstrap is a no-op.
#
# Writes server id / ip / name under ci/.artifacts/ for later steps.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=/dev/null
source "$here/common.sh"

: "${HCLOUD_SSH_KEY_NAME:=yaver-ci}"

image="${HETZNER_TEST_SNAPSHOT_ID:-$CI_SERVER_IMAGE}"
log "creating $CI_SERVER_NAME ($CI_SERVER_TYPE, $CI_SERVER_LOCATION, image=$image)"

hcloud server create \
  --name "$CI_SERVER_NAME" \
  --type "$CI_SERVER_TYPE" \
  --location "$CI_SERVER_LOCATION" \
  --image "$image" \
  --ssh-key "$HCLOUD_SSH_KEY_NAME" \
  --label purpose=ci \
  --label ephemeral=true \
  --label managed-by=yaver-ci \
  --label run-id="${GITHUB_RUN_ID:-local}" \
  -o json > "$CI_ARTIFACTS/server.json"

jq -r '.server.id'        "$CI_ARTIFACTS/server.json" > "$CI_ARTIFACTS/server-id"
jq -r '.server.public_net.ipv4.ip' "$CI_ARTIFACTS/server.json" > "$CI_ARTIFACTS/server-ip"
echo "$CI_SERVER_NAME" > "$CI_ARTIFACTS/server-name"

log "created id=$(cat "$CI_ARTIFACTS/server-id") ip=$(cat "$CI_ARTIFACTS/server-ip")"
