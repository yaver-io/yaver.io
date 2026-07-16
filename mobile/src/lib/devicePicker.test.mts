// devicePicker.test.mts — remote-box picker candidates + ordering.
// Run: npx tsx src/lib/devicePicker.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import { eligibleRemoteBoxDevices, isPickableRemoteBox, versionPatchDistance } from "./devicePicker.ts";
import type { Device } from "../context/DeviceContext";

function dev(over: Partial<Device> & { id: string; name: string }): Device {
  return {
    host: "10.0.0.2",
    port: 18080,
    online: true,
    lastSeen: Date.now(),
    os: "macos",
    runners: [],
    hwid: `hw-${over.id}`,
    publicKey: `pk-${over.id}`,
    ...over,
  } as Device;
}

const ids = (list: Device[]) => list.map((d) => d.id);

test("versionPatchDistance", () => {
  assert.equal(versionPatchDistance("1.99.300", "1.99.300"), 0);
  assert.equal(versionPatchDistance("1.99.297", "1.99.300"), 3);
  assert.equal(versionPatchDistance("1.98.1", "1.99.1"), -1, "different minor is undecidable");
  assert.equal(versionPatchDistance("", "1.99.1"), -1);
  assert.equal(versionPatchDistance("garbage", "1.99.1"), -1);
});

// The regression this file exists for: a Mac mini that rebooted, lost its
// auth token and re-registered via /devices/bootstrap comes back from
// Convex as { online: true, needsAuth: true }. It must be offered, not
// hidden — hiding it left a reachable box with no way to be signed in.
test("a needs-auth box stays in the list", () => {
  const mini = dev({ id: "229aeb03", name: "Mobiles-Mac-mini.local", online: true, needsAuth: true });
  const laptop = dev({ id: "6e8db080", name: "Kvancs-MacBook-Air.local", online: true });
  const out = eligibleRemoteBoxDevices([mini, laptop], []);
  assert.ok(ids(out).includes("229aeb03"), "needs-auth box must be pickable");
  assert.deepEqual(ids(out), ["6e8db080", "229aeb03"], "healthy online box outranks needs-auth");
});

test("an offline box is listed so it can render as offline", () => {
  const offline = dev({ id: "8663ea57", name: "Ofis2", online: false, lastSeen: 1 });
  const out = eligibleRemoteBoxDevices([offline], []);
  assert.deepEqual(ids(out), ["8663ea57"]);
});

test("ordering: pooled, then online, then needs-auth, then offline", () => {
  const list = [
    dev({ id: "d-offline", name: "d", online: false }),
    dev({ id: "c-needsauth", name: "c", online: true, needsAuth: true }),
    dev({ id: "b-online", name: "b", online: true }),
    dev({ id: "a-pooled", name: "a", online: true }),
  ];
  const out = eligibleRemoteBoxDevices(list, ["a-pooled"]);
  assert.deepEqual(ids(out), ["a-pooled", "b-online", "c-needsauth", "d-offline"]);
});

test("pooled beats online even when alphabetically last", () => {
  const list = [dev({ id: "aaa", name: "aaa", online: true }), dev({ id: "zzz", name: "zzz", online: false })];
  const out = eligibleRemoteBoxDevices(list, ["zzz"]);
  assert.deepEqual(ids(out), ["zzz", "aaa"], "a pooled connection outranks a name sort");
});

test("same rank sorts by name", () => {
  const list = [dev({ id: "2", name: "beta", online: true }), dev({ id: "1", name: "alpha", online: true })];
  assert.deepEqual(ids(eligibleRemoteBoxDevices(list, [])), ["1", "2"]);
});

test("ghost rows are dropped, but never merely-offline ones", () => {
  const ghost = dev({ id: "ghost", name: "ghost", online: true, hwid: undefined, publicKey: undefined });
  const real = dev({ id: "real", name: "real", online: false });
  assert.equal(isPickableRemoteBox(ghost), false);
  assert.equal(isPickableRemoteBox(real), true);
  assert.deepEqual(ids(eligibleRemoteBoxDevices([ghost, real], [])), ["real"]);
});

test("a terminal managed row is gone, not asleep", () => {
  const removed = dev({
    id: "cloud-mn-1",
    name: "yaver-primary-kivanc",
    online: false,
    managed: true,
    machineStatus: "removed",
  });
  const parked = dev({
    id: "cloud-mn-2",
    name: "mn777j15",
    online: false,
    managed: true,
    machineStatus: "paused",
  });
  const out = eligibleRemoteBoxDevices([removed, parked], []);
  assert.deepEqual(ids(out), ["cloud-mn-2"], "removed row drops; parked row stays");
});

test("connected or active rows survive even when otherwise unpickable", () => {
  const ghostButPooled = dev({ id: "g1", name: "g1", online: true, hwid: undefined, publicKey: undefined });
  assert.deepEqual(ids(eligibleRemoteBoxDevices([ghostButPooled], ["g1"])), ["g1"]);
  assert.deepEqual(ids(eligibleRemoteBoxDevices([ghostButPooled], [], "g1")), ["g1"]);
});
