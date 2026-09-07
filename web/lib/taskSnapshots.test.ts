import assert from "node:assert/strict";
import test from "node:test";
import { reconcileTasksWithAgentSnapshots } from "./taskSnapshots.ts";

const now = 10_000_000;

test("fresh owner snapshot removes a cached ghost", () => {
  const tasks = reconcileTasksWithAgentSnapshots([
    { id: "ghost", title: "Ghost", description: "", status: "review", deviceId: "box-a", output: [], createdAt: 1, updatedAt: 1 },
  ], [{ deviceId: "box-a", deviceName: "A", deviceOnline: true, deviceLastHeartbeat: now, observedAt: now, tasks: [] }], now);
  assert.deepEqual(tasks, []);
});

test("stale snapshot cannot erase direct task truth", () => {
  const current = [{ id: "live", title: "Live", description: "", status: "running" as const, deviceId: "box-a", output: [], createdAt: 1, updatedAt: 1 }];
  const tasks = reconcileTasksWithAgentSnapshots(current, [
    { deviceId: "box-a", deviceName: "A", deviceOnline: false, deviceLastHeartbeat: 1, observedAt: 1, tasks: [] },
  ], now + 4 * 60 * 60 * 1000);
  assert.equal(tasks.length, 1);
});

test("cross-surface placeholder contains no user content", () => {
  const tasks = reconcileTasksWithAgentSnapshots([], [
    { deviceId: "box-a", deviceName: "A", deviceOnline: true, deviceLastHeartbeat: now, observedAt: now, tasks: [
      { taskId: "task-1", yaverSessionId: "session-1", status: "review", updatedAt: now },
    ] },
  ], now);
  assert.equal(tasks[0].source, "session-index");
  assert.equal(tasks[0].title, "Task on A");
  assert.equal(tasks[0].description, "Connect to this machine to load the conversation.");
});
