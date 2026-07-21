import test from "node:test";
import assert from "node:assert/strict";

import { selectInferenceCandidate, type InferenceCandidate } from "./inferencePlacement.js";

const now = Date.UTC(2026, 6, 21);

function candidate(overrides: Partial<InferenceCandidate>): InferenceCandidate {
  return {
    provider: "aws",
    model: "deepseek.r1-v1:0",
    label: "DeepSeek R1",
    contextTokens: 128_000,
    estimatedUsdPerMillionInputTokens: 0.62,
    estimatedUsdPerMillionOutputTokens: 1.85,
    latencyMs: 500,
    qualityScore: 10,
    productionEligible: true,
    ...overrides,
  };
}

test("inference selector can choose Bedrock DeepSeek when managed credits exist", () => {
  const decision = selectInferenceCandidate(
    {
      intent: "trial",
      allowManaged: true,
      allowByo: false,
      now,
    },
    [
      candidate({
        provider: "aws",
        model: "deepseek.r1-v1:0",
        creditUsdRemaining: 1_000,
        creditExpiresAt: now + 20 * 86_400_000,
      }),
      candidate({
        provider: "external",
        model: "custom",
        creditUsdRemaining: 0,
      }),
    ],
  );
  assert.equal(decision.ok, true);
  assert.equal(decision.ok && decision.candidate.provider, "aws");
  assert.equal(decision.ok && decision.candidate.model, "deepseek.r1-v1:0");
});

test("inference selector can choose BYO to avoid Yaver model spend", () => {
  const decision = selectInferenceCandidate(
    {
      intent: "paid",
      allowManaged: true,
      allowByo: true,
      now,
    },
    [
      candidate({ provider: "aws", model: "deepseek.r1-v1:0", creditUsdRemaining: 0 }),
      candidate({ provider: "byo", model: "claude-code", latencyMs: 200, qualityScore: 10 }),
    ],
  );
  assert.equal(decision.ok, true);
  assert.equal(decision.ok && decision.candidate.provider, "byo");
});

test("inference selector rejects non-production managed backend", () => {
  const decision = selectInferenceCandidate(
    {
      intent: "trial",
      allowManaged: true,
      allowByo: false,
      now,
    },
    [
      candidate({ provider: "azure", model: "future", productionEligible: false }),
    ],
  );
  assert.equal(decision.ok, false);
});
