// Tests for the direct-probe negative cache added 2026-07-19 (audit §2).
//
// Run: npx tsx src/lib/unroutableCache.test.ts

import {
  _resetForTest,
  currentNetwork,
  isKnownUnroutable,
  observedTailnetUp,
  rememberReachable,
  rememberUnroutable,
  setNetworkIdentity,
} from "./unroutableCache";

let failures = 0;
function assert(label: string, cond: boolean) {
  if (!cond) {
    failures++;
    console.error(`FAIL ${label}`);
  } else {
    console.log(`ok   ${label}`);
  }
}

// A fresh cache starts unknown, with no cached candidates.
_resetForTest();
assert("empty cache reports unknown network", currentNetwork() === "unknown");
assert(
  "empty cache never claims a leg known-unroutable",
  isKnownUnroutable("lan-tailscale", "100.89.155.25", 18080) === false,
);
assert("empty cache does not claim tailnet observed", observedTailnetUp() === false);

// Set a network, remember an unroutable leg, and check that it is remembered.
setNetworkIdentity("wifi:test");
const t0 = 1_000_000;
rememberUnroutable("lan-tailscale", "100.89.155.25", 18080, t0);
assert(
  "unroutable leg is remembered within TTL",
  isKnownUnroutable("lan-tailscale", "100.89.155.25", 18080, t0 + 1000),
);
assert(
  "unrelated leg is not remembered",
  isKnownUnroutable("lan-heartbeat", "192.168.1.10", 18080, t0 + 1000) === false,
);

// After the TTL, the entry is forgotten (the network could have changed).
assert(
  "unroutable leg expires after 5 minutes",
  isKnownUnroutable("lan-tailscale", "100.89.155.25", 18080, t0 + 10 * 60 * 1000) === false,
);

// Switching network wipes prior entries — the same leg may be routable elsewhere.
_resetForTest();
setNetworkIdentity("wifi:a");
rememberUnroutable("lan-mesh", "100.96.0.5", 18080, t0);
assert(
  "before network flip, mesh leg is unroutable",
  isKnownUnroutable("lan-mesh", "100.96.0.5", 18080, t0 + 500),
);
setNetworkIdentity("wifi:b");
assert(
  "network flip wipes prior negative cache entries",
  isKnownUnroutable("lan-mesh", "100.96.0.5", 18080, t0 + 500) === false,
);

// observedTailnetUp requires POSITIVE proof — merely not-cached-unroutable
// isn't enough. This is the "gate on observed membership, not preference"
// bit from audit §2.
_resetForTest();
setNetworkIdentity("cellular");
assert("cellular starts without tailnet evidence", observedTailnetUp() === false);
rememberReachable("lan-tailscale");
assert("a confirmed tailnet reach sets observedTailnetUp", observedTailnetUp() === true);
setNetworkIdentity("wifi:home");
assert("network flip forgets tailnet observation too", observedTailnetUp() === false);

// The same network identity is idempotent — setting it twice is a no-op.
_resetForTest();
setNetworkIdentity("wifi:x");
rememberUnroutable("lan-heartbeat", "10.0.0.1", 18080, t0);
setNetworkIdentity("wifi:x"); // same, must not wipe
assert(
  "setting the same network twice does not wipe entries",
  isKnownUnroutable("lan-heartbeat", "10.0.0.1", 18080, t0 + 100),
);

if (failures > 0) {
  console.error(`\n${failures} failure(s)`);
  process.exit(1);
}
console.log("\nall passed");
