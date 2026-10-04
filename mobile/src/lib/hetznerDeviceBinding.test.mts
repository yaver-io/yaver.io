import assert from "node:assert/strict";
import test from "node:test";

import { isBoundHetznerDevice } from "./hetznerDeviceBinding.ts";

const managed = { id: 42, name: "kivanc-yaver-vps", ip: "49.13.122.160" };

test("binds a Yaver device to the pinned Hetzner VPS by public address", () => {
  assert.equal(isBoundHetznerDevice({ name: "3ac1d65d55b6", host: "49.13.122.160", lanIps: [] }, managed), true);
  assert.equal(isBoundHetznerDevice({ name: "3ac1d65d55b6", host: "relay", lanIps: ["49.13.122.160"] }, managed), true);
});

test("uses the user-owned Yaver alias when the agent name is a hardware id", () => {
  assert.equal(isBoundHetznerDevice({ name: "3ac1d65d55b6", alias: "kivanc-yaver-vps", host: "relay", lanIps: [] }, managed), true);
});

test("never wakes a different Yaver device", () => {
  assert.equal(isBoundHetznerDevice({ name: "yaver-1962.local", alias: "mac-2", host: "192.168.1.5", lanIps: [] }, managed), false);
});
