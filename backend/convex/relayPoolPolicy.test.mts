import test from "node:test";
import assert from "node:assert/strict";

// Pure relay pool policy — real-path import per scripts/test-suite.sh
// policy-test rule (relayPool.ts imports Convex internals, so the pure
// decisions live in relayPoolPolicy.ts).
import {
  relayHostKey,
  relayHostKeyFromSeed,
  relayPublicHostname,
  relayTenantsPerHost,
  selectRelayHostSlot,
  sharedHostDeletionDecision,
  sharedHostGraceSnapshotDecision,
} from "./relayPoolPolicy.ts";

test("tenant packing target rejects unsafe environment values", () => {
  assert.equal(relayTenantsPerHost({}), 20);
  assert.equal(relayTenantsPerHost({ YAVER_RELAY_TENANTS_PER_HOST: "30" }), 30);
  assert.equal(relayTenantsPerHost({ YAVER_RELAY_TENANTS_PER_HOST: "-1" }), 20);
  assert.equal(relayTenantsPerHost({ YAVER_RELAY_TENANTS_PER_HOST: "5000" }), 20);
  assert.equal(relayTenantsPerHost({ YAVER_RELAY_TENANTS_PER_HOST: "2.5" }), 20);
});

// ── selectRelayHostSlot ─────────────────────────────────────────────────────

test("slot selection first-fits an existing host under capacity", () => {
  const slot = selectRelayHostSlot({ region: "eu", hostCounts: { "relay-v2-eu-0": 3 }, capacity: 20 });
  assert.equal(slot.hostKey, "relay-v2-eu-0");
  assert.equal(slot.needsProvision, false);
  assert.equal(slot.tenantsOnHost, 4);
});

test("slot selection starts a new host when the first is full", () => {
  const slot = selectRelayHostSlot({ region: "eu", hostCounts: { "relay-v2-eu-0": 20 }, capacity: 20 });
  assert.equal(slot.hostKey, "relay-v2-eu-1");
  assert.equal(slot.needsProvision, true);
  assert.equal(slot.tenantsOnHost, 1);
});

test("slot selection is region-isolated", () => {
  const eu = selectRelayHostSlot({ region: "eu", hostCounts: { "relay-v2-us-0": 19 } });
  assert.equal(eu.hostKey, "relay-v2-eu-0");
  const us = selectRelayHostSlot({ region: "us", hostCounts: { "relay-v2-us-0": 19 } });
  assert.equal(us.hostKey, "relay-v2-us-0");
});

test("relayHostKey is stable and human readable", () => {
  assert.equal(relayHostKey("eu", 0), "relay-v2-eu-0");
  assert.equal(relayHostKey("US", 2), "relay-v2-us-2");
});

test("relayHostKeyFromSeed creates a stable unbounded v3 pool key", () => {
  assert.equal(relayHostKeyFromSeed("EU", "j57Ab_C-123"), "relay-v3-eu-j57abc123");
  assert.notEqual(relayHostKeyFromSeed("eu", "relay-a"), relayHostKeyFromSeed("eu", "relay-b"));
});

test("legacy first-fit policy has no 1,000-host ceiling", () => {
  const hostCounts: Record<string, number> = {};
  for (let i = 0; i < 1_001; i++) {
    hostCounts[relayHostKey("eu", i)] = 20;
  }

  assert.deepEqual(selectRelayHostSlot({ region: "eu", hostCounts, capacity: 20 }), {
    hostKey: relayHostKey("eu", 1_001),
    needsProvision: true,
    tenantsOnHost: 1,
    reason: `new shared host ${relayHostKey("eu", 1_001)}`,
  });
});

test("pooled tenants share the host TLS name while dedicated relays are per-user", () => {
  assert.deepEqual(
    relayPublicHostname({ dedicated: false, shortUserId: "ignored", hostKey: "relay-v2-eu-3" }),
    { subdomain: "relay-v2-eu-3.relay", domain: "relay-v2-eu-3.relay.yaver.io" },
  );
  assert.deepEqual(
    relayPublicHostname({ dedicated: true, shortUserId: "abcd1234" }),
    { subdomain: "abcd1234.relay", domain: "abcd1234.relay.yaver.io" },
  );
});

// ── sharedHostDeletionDecision — never delete a box others still use ────────

test("dedicated relay is always deletable", () => {
  const d = sharedHostDeletionDecision({ sharedHostKey: null, liveTenantsOnHost: 0 });
  assert.equal(d.deleteServer, true);
  const d2 = sharedHostDeletionDecision({ liveTenantsOnHost: 3 });
  assert.equal(d2.deleteServer, true, "absent sharedHostKey means dedicated");
});

test("shared host with other tenants must stay", () => {
  const d = sharedHostDeletionDecision({ sharedHostKey: "relay-eu-0", liveTenantsOnHost: 4 });
  assert.equal(d.deleteServer, false);
  assert.match(d.reason, /tenant/);
});

test("last tenant on a shared host drains and deletes it", () => {
  const d = sharedHostDeletionDecision({ sharedHostKey: "relay-eu-0", liveTenantsOnHost: 0 });
  assert.equal(d.deleteServer, true);
});

test("pinned hybrid host is never deleted when paid tenants reach zero", () => {
  const d = sharedHostDeletionDecision({
    sharedHostKey: "relay-hybrid-anchor-eu",
    liveTenantsOnHost: 0,
    pinned: true,
  });
  assert.equal(d.deleteServer, false);
  assert.match(d.reason, /never delete/);
});

// ── sharedHostGraceSnapshotDecision — no billed orphans from pooled hosts ──

test("dedicated relays keep a grace snapshot", () => {
  assert.equal(sharedHostGraceSnapshotDecision(null), true);
  assert.equal(sharedHostGraceSnapshotDecision(undefined), true);
});

test("shared pool hosts never snapshot on teardown (billed-orphan class)", () => {
  assert.equal(sharedHostGraceSnapshotDecision("relay-eu-0"), false);
});
