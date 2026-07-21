import test from "node:test";
import assert from "node:assert/strict";

import { requiredCapabilitiesForProfile } from "./cloudProviderPlacement.js";
import {
  leaseWouldExceedBudget,
  selectPoolEntry,
  type PoolEntry,
  type PlacementLease,
} from "./cloudPoolPlacement.js";

const now = Date.UTC(2026, 6, 21);

function entry(overrides: Partial<PoolEntry>): PoolEntry {
  return {
    key: {
      provider: "hetzner",
      region: "eu",
      profile: "linux-runner",
      sku: "cx32",
      kind: "parked-builder",
    },
    state: "parked",
    capabilities: requiredCapabilitiesForProfile("linux-runner"),
    lastWakeDurationMs: 90_000,
    estimatedMonthlyUsd: 7,
    ...overrides,
  };
}

test("pool selection rejects entries missing capability", () => {
  const decision = selectPoolEntry(
    {
      profile: "linux-runner-webrtc",
      kind: "parked-builder",
      requiredCapabilities: requiredCapabilitiesForProfile("linux-runner-webrtc"),
      now,
      allowWarmSharedPool: false,
    },
    [entry({ key: { provider: "hetzner", region: "eu", profile: "linux-runner-webrtc", sku: "cx32", kind: "parked-builder" } })],
  );
  assert.equal(decision.ok, false);
});

test("pool selection prefers warm ready machine over parked machine", () => {
  const decision = selectPoolEntry(
    {
      profile: "linux-runner",
      kind: "warm-builder",
      requiredCapabilities: requiredCapabilitiesForProfile("linux-runner"),
      now,
      allowWarmSharedPool: true,
    },
    [
      entry({ key: { provider: "hetzner", region: "eu", profile: "linux-runner", sku: "cx32", kind: "warm-builder" }, state: "warm", lastWakeDurationMs: 10_000 }),
      entry({ key: { provider: "gcp", region: "eu", profile: "linux-runner", sku: "e2-standard-4", kind: "warm-builder" }, state: "parked", lastWakeDurationMs: 120_000 }),
    ],
  );
  assert.equal(decision.ok, true);
  assert.equal(decision.ok && decision.entry.key.provider, "hetzner");
});

test("pool selection rejects expired leases and entries", () => {
  const decision = selectPoolEntry(
    {
      profile: "linux-runner",
      kind: "parked-builder",
      requiredCapabilities: requiredCapabilitiesForProfile("linux-runner"),
      now,
      allowWarmSharedPool: false,
    },
    [entry({ expiresAt: now - 1 })],
  );
  assert.equal(decision.ok, false);
});

test("lease budget check prevents concurrent over-reservation", () => {
  const leases: PlacementLease[] = [
    {
      leaseId: "a",
      userId: "u",
      provider: "aws",
      kind: "inference-budget",
      estimatedUsd: 8,
      expiresAt: now + 60_000,
      status: "reserved",
    },
  ];
  assert.equal(leaseWouldExceedBudget({
    existingLeases: leases,
    provider: "aws",
    additionalEstimatedUsd: 3,
    hardStopUsd: 10,
    now,
  }), true);
  assert.equal(leaseWouldExceedBudget({
    existingLeases: leases,
    provider: "aws",
    additionalEstimatedUsd: 2,
    hardStopUsd: 10,
    now,
  }), false);
});
