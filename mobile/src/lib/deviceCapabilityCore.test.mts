// deviceCapabilityCore.test.mts — RAM→class mapping + the hard load gate.
// Run: npx tsx src/lib/deviceCapabilityCore.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import { bytesToMb, ramToModelClass, canRunModel } from "./deviceCapabilityCore.ts";
import type { DeviceCapability } from "./localAgent/tiers.ts";

test("bytesToMb converts and guards", () => {
  assert.equal(bytesToMb(8 * 1024 * 1024 * 1024), 8192);
  assert.equal(bytesToMb(0), undefined);
  assert.equal(bytesToMb(null), undefined);
  assert.equal(bytesToMb(undefined), undefined);
});

test("ramToModelClass thresholds", () => {
  assert.equal(ramToModelClass(8192), "medium"); // 8GB → coder eligible
  assert.equal(ramToModelClass(6144), "small"); // 6GB → router
  assert.equal(ramToModelClass(3000), "tiny");
  assert.equal(ramToModelClass(2000), "none");
  assert.equal(ramToModelClass(undefined), undefined);
});

test("canRunModel: requires RAM headroom", () => {
  const big: DeviceCapability = { totalRamMb: 8192 };
  const small: DeviceCapability = { totalRamMb: 6144 };
  // 3B coder needs 7500
  assert.equal(canRunModel(7500, big), true);
  assert.equal(canRunModel(7500, small), false); // 6GB phone can't → won't load
  // 1.5B router needs 4000
  assert.equal(canRunModel(4000, small), true);
});

test("canRunModel: unknown RAM refuses (safe default)", () => {
  assert.equal(canRunModel(4000, {}), false);
  assert.equal(canRunModel(4000, { totalRamMb: 0 }), false);
});

test("canRunModel: thermally hot refuses everything", () => {
  assert.equal(canRunModel(4000, { totalRamMb: 8192, thermalState: "hot" }), false);
  assert.equal(canRunModel(4000, { totalRamMb: 8192, thermalState: "nominal" }), true);
});
