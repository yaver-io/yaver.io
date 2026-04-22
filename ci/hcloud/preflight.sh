#!/usr/bin/env bash
# Validate Hetzner CI secrets before any remote workflow burns time on SSH
# waits or deep test steps. Supports:
#   preflight.sh persistent
#   preflight.sh ephemeral
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=/dev/null
source "$here/common.sh"

mode="${1:-}"
[ -n "$mode" ] || { echo "usage: $0 <persistent|ephemeral>" >&2; exit 2; }

require_secret() {
  local name="$1"
  local value="${!name:-}"
  [ -n "$value" ] || {
    log "missing required secret: $name"
    exit 1
  }
}

verify_hcloud_token() {
  if ! hcloud context list >/dev/null 2>&1; then
    log "HCLOUD_TOKEN is invalid or unauthorized"
    exit 1
  fi
}

case "$mode" in
  persistent)
    require_secret HETZNER_TEST_SERVER_ID
    require_secret HETZNER_TEST_SERVER_IP
    verify_hcloud_token
    server_json="$CI_ARTIFACTS/persistent-server.json"
    hcloud server describe "$HETZNER_TEST_SERVER_ID" -o json > "$server_json"
    actual_ip="$(jq -r '.public_net.ipv4.ip // ""' "$server_json")"
    actual_name="$(jq -r '.name // ""' "$server_json")"
    [ -n "$actual_ip" ] || {
      log "Hetzner server $HETZNER_TEST_SERVER_ID has no public IPv4"
      exit 1
    }
    if [ "$actual_ip" != "$HETZNER_TEST_SERVER_IP" ]; then
      log "HETZNER_TEST_SERVER_IP secret is stale: expected $actual_ip from server $HETZNER_TEST_SERVER_ID, got $HETZNER_TEST_SERVER_IP"
      exit 1
    fi
    mkdir -p "$CI_ARTIFACTS"
    printf '%s' "$actual_ip" > "$CI_ARTIFACTS/server-ip"
    printf '%s' "${actual_name:-yaver-test-ephemeral}" > "$CI_ARTIFACTS/server-name"
    printf '%s' "$HETZNER_TEST_SERVER_ID" > "$CI_ARTIFACTS/server-id"
    log "persistent server preflight ok: id=$HETZNER_TEST_SERVER_ID ip=$actual_ip name=${actual_name:-unknown}"
    ;;
  ephemeral)
    require_secret HETZNER_TEST_SNAPSHOT_ID
    verify_hcloud_token
    if ! hcloud image describe "$HETZNER_TEST_SNAPSHOT_ID" >/dev/null 2>&1; then
      log "HETZNER_TEST_SNAPSHOT_ID is invalid or not accessible"
      exit 1
    fi
    log "ephemeral preflight ok: snapshot=$HETZNER_TEST_SNAPSHOT_ID"
    ;;
  *)
    echo "usage: $0 <persistent|ephemeral>" >&2
    exit 2
    ;;
esac
