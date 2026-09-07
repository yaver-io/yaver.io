import assert from "node:assert/strict";
import test from "node:test";
import { taskOwnerDeviceId, taskOwnerNeedsConnection } from "./taskOwnerRouting.ts";

const devices = [
  { id: "mac-id", name: "MacBook.local" },
  { id: "ubuntu-id", name: "ubuntu-4gb-hel1-1" },
];

test("an explicit task owner wins over mutable focus and runner roles", () => {
  assert.equal(
    taskOwnerDeviceId({ deviceId: "ubuntu-id", deviceName: "MacBook.local" }, devices),
    "ubuntu-id",
  );
});

test("an explicit owner survives a temporarily incomplete device list", () => {
  assert.equal(taskOwnerDeviceId({ deviceId: "ubuntu-id" }, []), "ubuntu-id");
});

test("legacy cached rows may resolve their owner by normalized hostname", () => {
  assert.equal(taskOwnerDeviceId({ deviceName: "MACBOOK" }, devices), "mac-id");
});

test("a task owner is connected independently of the focused machine", () => {
  assert.equal(taskOwnerNeedsConnection("ubuntu-id", ["mac-id"]), true);
  assert.equal(taskOwnerNeedsConnection("ubuntu-id", ["mac-id", "ubuntu-id"]), false);
  assert.equal(taskOwnerNeedsConnection(null, ["mac-id"]), false);
});
