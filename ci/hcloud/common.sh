#!/usr/bin/env bash
# Shared helpers for ci/hcloud/*.sh. Sourced, never executed.
# Every script using these expects HCLOUD_TOKEN to be set.
set -euo pipefail

: "${HCLOUD_TOKEN:?HCLOUD_TOKEN must be set}"
export HCLOUD_TOKEN

# Deterministic ephemeral-server name.
CI_SERVER_NAME="${CI_SERVER_NAME:-yaver-ci-${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-1}}"
CI_SERVER_TYPE="${CI_SERVER_TYPE:-cax21}"
CI_SERVER_LOCATION="${CI_SERVER_LOCATION:-hel1}"
CI_SERVER_IMAGE="${CI_SERVER_IMAGE:-ubuntu-24.04}"

# Artifacts dir — where IP / id / logs land for later steps.
CI_ARTIFACTS="${CI_ARTIFACTS:-$(pwd)/ci/.artifacts}"
mkdir -p "$CI_ARTIFACTS"

log() { printf '\n[ci] %s\n' "$*"; }
