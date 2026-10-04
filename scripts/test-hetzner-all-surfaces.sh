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
  cd "$repo_root/desktop/agent"
  go test -count=1 -run 'Test(HetznerPower|EndpointCredentialHandoff)' .
  go test -count=1 ./e2ee
)

node "$repo_root/scripts/verify-zero-knowledge-boundary.mjs"
