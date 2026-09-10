import assert from "node:assert/strict";
import test from "node:test";
import { reconcileTasksWithAgentSnapshots } from "./taskSnapshotMerge.ts";

const now = 10_000_000;

test("fresh owning-agent snapshot removes a cached ghost review task", () => {
  const result = reconcileTasksWithAgentSnapshots([
    { id: "old", title: "old", description: "", status: "review", output: [], deviceId: "box-a", createdAt: 1, updatedAt: 1 },
  ] as any, [{ deviceId: "box-a", deviceName: "Box", deviceOnline: true, deviceLastHeartbeat: now, observedAt: now, tasks: [] }], now);
  assert.deepEqual(result, []);
});

test("stale snapshot cannot delete cached task state", () => {
  const cached = [{ id: "old", title: "old", description: "", status: "review", output: [], deviceId: "box-a", createdAt: 1, updatedAt: 1 }] as any;
  const result = reconcileTasksWithAgentSnapshots(cached, [
    { deviceId: "box-a", deviceName: "Box", deviceOnline: false, deviceLastHeartbeat: 1, observedAt: 1, tasks: [] },
  ], now + 4 * 60 * 60 * 1000);
  assert.equal(result.length, 1);
});

test("task ids are scoped by device and lifecycle is updated", () => {
  const result = reconcileTasksWithAgentSnapshots([
    { id: "same", title: "A", description: "", status: "review", output: [], deviceId: "box-a", createdAt: 1, updatedAt: 1 },
    { id: "same", title: "B", description: "", status: "review", output: [], deviceId: "box-b", createdAt: 1, updatedAt: 1 },
  ] as any, [
    { deviceId: "box-a", deviceName: "A", deviceOnline: true, deviceLastHeartbeat: now, observedAt: now, tasks: [{ taskId: "same", status: "stopped", updatedAt: now }] },
    { deviceId: "box-b", deviceName: "B", deviceOnline: true, deviceLastHeartbeat: now, observedAt: now, tasks: [{ taskId: "same", status: "running", updatedAt: now }] },
  ] as any, now);
  assert.deepEqual(result.map((task) => `${task.deviceId}:${task.status}`).sort(), ["box-a:stopped", "box-b:running"]);
});

test("unknown cross-surface session is represented without private content", () => {
  const result = reconcileTasksWithAgentSnapshots([], [
    { deviceId: "box-a", deviceName: "Primary", deviceOnline: true, deviceLastHeartbeat: now, observedAt: now, tasks: [{ taskId: "t1", yaverSessionId: "ys_1", status: "review", updatedAt: now }] },
  ] as any, now);
  assert.equal(result[0].title, "Task on Primary");
  assert.equal(result[0].description, "Connect to this machine to load the conversation.");
  assert.equal(result[0].executionSession?.yaverSessionId, "ys_1");
});

test("a central tombstone wins even when the owning snapshot is stale or still lists the task", () => {
  const cached = [{ id: "gone", title: "gone", status: "running", output: [], deviceId: "box-a", createdAt: 1, updatedAt: 1 }] as any;
  const result = reconcileTasksWithAgentSnapshots(cached, [{
    deviceId: "box-a", deviceName: "Box", deviceOnline: false, deviceLastHeartbeat: 1, observedAt: 1,
    tasks: [{ taskId: "gone", status: "running", updatedAt: 1 }],
    deletedTasks: [{ taskId: "gone", deletedAt: now }],
  }] as any, now + 4 * 60 * 60 * 1000);
  assert.deepEqual(result, []);
});
