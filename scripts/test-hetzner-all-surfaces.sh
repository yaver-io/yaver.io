#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

node --experimental-strip-types --test \
  "$repo_root/shared/zero-knowledge/hetzner-power-contract.test.mts" \
  "$repo_root/shared/zero-knowledge/relay-envelope.test.mts"

(
  cd "$repo_root/mobile"
  node --experimental-strip-types --test \
    src/lib/hetznerDirectCore.test.mts \
    src/lib/hetznerRecovery.test.mts \
    src/lib/hetznerCustodyWiring.test.mts \
    src/lib/credentialHandoff.test.mts
  ./node_modules/.bin/tsc --noEmit
)

(
  cd "$repo_root/mobile-headless"
  bun test test/hetzner-power-state.test.ts

  # The real-account lifecycle test is opt-in and skipped in normal CI. It
  # imports the same mobile core and requires process-local HCLOUD_TOKEN.
  bun test test/hetzner-live-account.test.ts
)

(
  cd "$repo_root/desktop/agent"
  go test -count=1 -run 'Test(HetznerPower|HetznerRename|EndpointCredentialHandoff)' .
  go test -count=1 ./e2ee
)

node "$repo_root/scripts/verify-zero-knowledge-boundary.mjs"
