import test from "node:test";
import assert from "node:assert/strict";

import {
  cloudWorkspaceCreditWeightForMachineType,
  includedAllowanceCoversStart,
  weightedIncludedCoverage,
} from "./cloudLifecycle.js";

test("cloud workspace machine types map to standard-credit weights", () => {
  assert.equal(cloudWorkspaceCreditWeightForMachineType("standard"), 1);
  assert.equal(cloudWorkspaceCreditWeightForMachineType("heavy"), 2);
  assert.equal(cloudWorkspaceCreditWeightForMachineType("build"), 4);
  assert.equal(cloudWorkspaceCreditWeightForMachineType("cpu"), 4);
  assert.equal(cloudWorkspaceCreditWeightForMachineType("gpu"), 0);
});

test("weighted included coverage consumes one shared standard-credit pool", () => {
  const oneHour = 3600;
  const coverage = weightedIncludedCoverage({
    seconds: oneHour,
    usedStandardCreditSeconds: 0,
    includedStandardCreditSeconds: 120 * oneHour,
    creditWeight: 4,
  });
  assert.equal(coverage.coveredSeconds, oneHour);
  assert.equal(coverage.usedStandardCreditSeconds, 4 * oneHour);
  assert.equal(coverage.remainingStandardCreditSeconds, 116 * oneHour);
});

test("weighted included coverage partially covers when shared pool is low", () => {
  const coverage = weightedIncludedCoverage({
    seconds: 3600,
    usedStandardCreditSeconds: 119 * 3600,
    includedStandardCreditSeconds: 120 * 3600,
    creditWeight: 4,
  });
  assert.equal(coverage.coveredSeconds, 900);
  assert.equal(coverage.usedStandardCreditSeconds, 3600);
  assert.equal(coverage.remainingStandardCreditSeconds, 0);
});

test("included allowance start gate requires enough shared credits for one billable window", () => {
  assert.equal(
    includedAllowanceCoversStart({
      machineType: "standard",
      remainingStandardCreditSeconds: 3600,
    }),
    true,
  );
  assert.equal(
    includedAllowanceCoversStart({
      machineType: "heavy",
      remainingStandardCreditSeconds: 3600,
    }),
    false,
  );
  assert.equal(
    includedAllowanceCoversStart({
      machineType: "heavy",
      remainingStandardCreditSeconds: 7200,
    }),
    true,
  );
  assert.equal(
    includedAllowanceCoversStart({
      machineType: "build",
      remainingStandardCreditSeconds: 4 * 3600,
    }),
    true,
  );
  assert.equal(
    includedAllowanceCoversStart({
      machineType: "gpu",
      remainingStandardCreditSeconds: 24 * 3600,
    }),
    false,
  );
});
