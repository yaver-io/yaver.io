import test from "node:test";
import assert from "node:assert/strict";

import {
  providerSupportsProfile,
  requiredCapabilitiesForProfile,
  selectPlacementCandidate,
  type PlacementCandidate,
  type ProviderCreditState,
} from "./cloudProviderPlacement.js";
import type { ProviderCapabilities } from "./cloudProviders/types.js";

const now = Date.UTC(2026, 6, 21);

function candidate(overrides: Partial<PlacementCandidate>): PlacementCandidate {
  return {
    provider: "hetzner",
    profile: "linux-runner",
    region: "eu",
    sku: "cx32",
    estimatedMonthlyUsd: 25,
    expectedWakeMs: 90_000,
    historicalFailureRate: 0,
    productionEligible: true,
    kind: "builder",
    capabilities: requiredCapabilitiesForProfile("linux-runner"),
    ...overrides,
  };
}

test("provider profile gate rejects Azure until it is production eligible", () => {
  const azure: ProviderCapabilities = {
    provider: "azure",
    profiles: [],
    capabilities: requiredCapabilitiesForProfile("linux-runner"),
    regions: ["westeurope"],
    productionEligible: false,
  };
  assert.equal(providerSupportsProfile(azure, "linux-runner"), false);
});

test("credits do not bypass missing required capabilities", () => {
  const decision = selectPlacementCandidate(
    {
      profile: "linux-runner-webrtc",
      requiredCapabilities: requiredCapabilitiesForProfile("linux-runner-webrtc"),
      kind: "builder",
      intent: "trial",
      now,
    },
    [
      candidate({
        provider: "gcp",
        capabilities: requiredCapabilitiesForProfile("linux-runner"),
        estimatedMonthlyUsd: 100,
      }),
    ],
    [
      {
        provider: "gcp",
        creditUsdRemaining: 100_000,
        creditExpiresAt: now + 30 * 86_400_000,
        supportsCompute: true,
        supportsInference: true,
        lastSyncedAt: now,
      },
    ],
  );
  assert.equal(decision.ok, false);
  assert.equal(decision.ok ? "" : decision.code, "no_capable_provider");
});

test("trial placement prefers expiring credits among capable candidates", () => {
  const decision = selectPlacementCandidate(
    {
      profile: "linux-runner",
      requiredCapabilities: requiredCapabilitiesForProfile("linux-runner"),
      kind: "builder",
      intent: "trial",
      now,
    },
    [
      candidate({ provider: "hetzner", estimatedMonthlyUsd: 25 }),
      candidate({ provider: "gcp", estimatedMonthlyUsd: 90, sku: "e2-standard-4" }),
    ],
    [
      {
        provider: "gcp",
        creditUsdRemaining: 2_000,
        creditExpiresAt: now + 10 * 86_400_000,
        supportsCompute: true,
        supportsInference: true,
        lastSyncedAt: now,
      },
    ],
  );
  assert.equal(decision.ok, true);
  assert.equal(decision.ok && decision.candidate.provider, "gcp");
});

test("paid placement prefers Hetzner when credits do not dominate", () => {
  const decision = selectPlacementCandidate(
    {
      profile: "linux-runner",
      requiredCapabilities: requiredCapabilitiesForProfile("linux-runner"),
      kind: "builder",
      intent: "paid",
      now,
    },
    [
      candidate({ provider: "hetzner", estimatedMonthlyUsd: 25 }),
      candidate({ provider: "aws", estimatedMonthlyUsd: 70, sku: "m7i.xlarge" }),
    ],
    [],
  );
  assert.equal(decision.ok, true);
  assert.equal(decision.ok && decision.candidate.provider, "hetzner");
});

test("hard budget stop blocks otherwise capable credited provider", () => {
  const credits: ProviderCreditState[] = [
    {
      provider: "aws",
      creditUsdRemaining: 1_000,
      monthToDateSpendUsd: 100,
      hardStopAtUsd: 100,
      supportsCompute: true,
      supportsInference: true,
      lastSyncedAt: now,
    },
  ];
  const decision = selectPlacementCandidate(
    {
      profile: "linux-runner",
      requiredCapabilities: requiredCapabilitiesForProfile("linux-runner"),
      kind: "builder",
      intent: "trial",
      now,
    },
    [candidate({ provider: "aws", estimatedMonthlyUsd: 70, sku: "m7i.xlarge" })],
    credits,
  );
  assert.equal(decision.ok, false);
  assert.equal(decision.ok ? "" : decision.code, "budget_blocked");
});
