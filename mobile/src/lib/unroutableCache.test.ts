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
  forgetTunnelLegs,
  isTunnelPath,
  ttlForPath,
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


// ── Tunnel legs (the Tailscale bug, 2026-07-20) ─────────────────────────────
//
// Both phone and mac mini were on the tailnet, the mini green in the Tailscale
// app, and the phone still logged
//   lan-tailscale 100.89.155.25:18080 failed — unroutable
// on repeat, forcing everything onto a flaky relay. Cause: bringing a VPN up
// does not change the Wi-Fi SSID, so the network identity is unchanged, so
// nothing is wiped, so a leg that failed while the tunnel was down stays
// negative. A LAN address is a property of the network; a tunnel address is a
// property of a daemon that can start at any instant.

_resetForTest();
setNetworkIdentity("wifi:home");
const tt0 = 5_000_000;
rememberUnroutable("lan-tailscale", "100.89.155.25", 18080, tt0, ttlForPath("lan-tailscale"));
rememberUnroutable("lan-heartbeat", "192.168.1.50", 18080, tt0, ttlForPath("lan-heartbeat"));
assert(
  "tunnel leg expires fast — a daemon can come up without the network changing",
  isKnownUnroutable("lan-tailscale", "100.89.155.25", 18080, tt0 + 60_000) === false,
);
assert(
  "LAN leg keeps the long TTL — it is a property of the network",
  isKnownUnroutable("lan-heartbeat", "192.168.1.50", 18080, tt0 + 60_000) === true,
);

_resetForTest();
setNetworkIdentity("wifi:home");
const tt1 = 6_000_000;
rememberUnroutable("lan-tailscale", "100.89.155.25", 18080, tt1);
rememberUnroutable("lan-mesh", "100.70.1.2", 18080, tt1);
rememberUnroutable("lan-heartbeat", "192.168.1.50", 18080, tt1);
const droppedTunnel = forgetTunnelLegs();
assert("forgetTunnelLegs drops exactly the tunnel legs", droppedTunnel === 2);
assert(
  "the address the user just enabled Tailscale for is retried",
  isKnownUnroutable("lan-tailscale", "100.89.155.25", 18080, tt1 + 1) === false,
);
assert(
  "mesh legs are re-armed too — they depend on a daemon as well",
  isKnownUnroutable("lan-mesh", "100.70.1.2", 18080, tt1 + 1) === false,
);
assert(
  "a LAN leg is NOT re-armed — nothing about the network changed",
  isKnownUnroutable("lan-heartbeat", "192.168.1.50", 18080, tt1 + 1) === true,
);

assert("lan-tailscale is a tunnel path", isTunnelPath("lan-tailscale") === true);
assert("lan-mesh is a tunnel path", isTunnelPath("lan-mesh") === true);
assert("lan-heartbeat is not a tunnel path", isTunnelPath("lan-heartbeat") === false);
assert("lan-convex-ip is not a tunnel path", isTunnelPath("lan-convex-ip") === false);

// Summary LAST. It used to sit mid-file, so every assert appended after it
// ran but could never fail the run — a test suite that cannot go red.
if (failures > 0) {
  console.error(`\n${failures} failure(s)`);
  process.exit(1);
}
console.log("\nall passed");
