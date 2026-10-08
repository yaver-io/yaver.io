import assert from "node:assert/strict";
import test from "node:test";

import {
  DEFAULT_FREE_OWNED_DEVICE_LIMIT,
  deviceEntitlement,
  freeOwnedDeviceLimit,
  ownedDeviceLimit,
} from "./entitlements";

test("free accounts default to two owned devices", () => {
  assert.equal(freeOwnedDeviceLimit(), 2);
  assert.deepEqual(deviceEntitlement("free"), {
    plan: "free",
    maxOwnedDevices: DEFAULT_FREE_OWNED_DEVICE_LIMIT,
  });
});

test("free owned-device limit is tuneable with a bounded non-negative integer", () => {
  assert.equal(freeOwnedDeviceLimit("0"), 0);
  assert.equal(freeOwnedDeviceLimit(" 7 "), 7);
  assert.equal(freeOwnedDeviceLimit("-1"), 2);
  assert.equal(freeOwnedDeviceLimit("unlimited"), 2);
  assert.equal(freeOwnedDeviceLimit("10001"), 2);
});

test("Relay Pro has no owned-device limit at the edge", () => {
  assert.equal(ownedDeviceLimit("relay-pro", "1"), null);
  assert.deepEqual(deviceEntitlement("relay-pro", "1"), {
    plan: "relay-pro",
    maxOwnedDevices: null,
  });
});
