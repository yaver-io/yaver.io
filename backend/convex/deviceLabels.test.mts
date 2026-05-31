// deviceLabels.test.mts — unit tests for the device auto-label / alias slug
// helpers. Mirrors the repo convention (see cloudMachines.test.mts):
// node:test + node:assert, importing the compiled ./deviceLabels.js.
// Run: node --test convex/deviceLabels.test.mts  (after tsc/convex codegen)
//
// Locks the behavior registerDevice's auto-label path and the on-device
// voice-helper alias resolution depend on.

import test from "node:test";
import assert from "node:assert/strict";

import {
  smartDeviceLabel,
  smartAliasSlug,
  uniqueAliasSlug,
  isRawHostname,
} from "./deviceLabels.js";

test("smartDeviceLabel — cloud boxes win, provider + region", () => {
  assert.equal(
    smartDeviceLabel({ platform: "linux", cloudProvider: "hetzner", cloudRegion: "hel1" }),
    "Hetzner box (hel1)",
  );
  assert.equal(smartDeviceLabel({ platform: "linux", cloudProvider: "hetzner" }), "Hetzner box");
  assert.equal(
    smartDeviceLabel({ platform: "linux", cloudProvider: "aws", cloudRegion: "us-east-1" }),
    "AWS box (us-east-1)",
  );
});

test("smartDeviceLabel — macOS variants from hostname", () => {
  assert.equal(smartDeviceLabel({ platform: "macos", hostname: "Kivancs-MacBook-Pro" }), "MacBook");
  assert.equal(smartDeviceLabel({ platform: "macos", hostname: "mac-mini-1" }), "Mac mini");
  assert.equal(smartDeviceLabel({ platform: "macos", hostname: "weird-host" }), "Mac");
});

test("smartDeviceLabel — linux / pi / wsl / windows", () => {
  assert.equal(smartDeviceLabel({ platform: "linux", hostname: "ubuntu-4gb-hel1-1" }), "Linux box");
  assert.equal(smartDeviceLabel({ platform: "linux", hostname: "raspberrypi" }), "Raspberry Pi");
  assert.equal(
    smartDeviceLabel({ platform: "linux", hostname: "node1", hardwareModel: "raspberry pi 5 model b" }),
    "Raspberry Pi",
  );
  assert.equal(smartDeviceLabel({ platform: "linux", isWsl: true, hostname: "DESKTOP-ABC" }), "WSL box");
  assert.equal(smartDeviceLabel({ platform: "windows", hostname: "DESKTOP-XYZ" }), "Windows PC");
});

test("smartAliasSlug — memorable slugs", () => {
  assert.equal(smartAliasSlug({ platform: "linux", cloudProvider: "hetzner" }), "hetzner");
  assert.equal(smartAliasSlug({ platform: "macos" }), "mac");
  assert.equal(smartAliasSlug({ platform: "linux" }), "linux");
  assert.equal(smartAliasSlug({ platform: "linux", hostname: "raspberrypi" }), "pi");
  assert.equal(smartAliasSlug({ platform: "windows" }), "windows");
});

test("uniqueAliasSlug — collision suffixes", () => {
  assert.equal(uniqueAliasSlug("mac", new Set()), "mac");
  assert.equal(uniqueAliasSlug("mac", new Set(["mac"])), "mac-2");
  assert.equal(uniqueAliasSlug("mac", new Set(["mac", "mac-2", "mac-3"])), "mac-4");
  assert.equal(uniqueAliasSlug(null, new Set()), null);
  assert.equal(uniqueAliasSlug("Hetzner!!", new Set()), "hetzner");
});

test("isRawHostname — only replace machine-generated/bare names", () => {
  assert.equal(isRawHostname("ubuntu-4gb-hel1-1", "linux"), true);
  assert.equal(isRawHostname("linux", "linux"), true);
  assert.equal(isRawHostname("localhost", "linux"), true);
  assert.equal(isRawHostname("ip-10-0-0-5", "linux"), true);
  assert.equal(isRawHostname(undefined, "linux"), true);
  assert.equal(isRawHostname("Kivancs-MacBook-Pro", "macos"), false);
  assert.equal(isRawHostname("prod-db", "linux"), false);
});
