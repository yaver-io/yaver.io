import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const schema = readFileSync(new URL("./schema.ts", import.meta.url), "utf8");
const snapshots = readFileSync(new URL("./agentTaskSnapshots.ts", import.meta.url), "utf8");
const http = readFileSync(new URL("./http.ts", import.meta.url), "utf8");
const go = readFileSync(new URL("../../desktop/agent/task_snapshot_convex.go", import.meta.url), "utf8");
const goSync = readFileSync(new URL("../../desktop/agent/convex_state_sync.go", import.meta.url), "utf8");
const mobile = readFileSync(new URL("../../mobile/app/(tabs)/tasks.tsx", import.meta.url), "utf8");
const web = readFileSync(new URL("../../web/app/dashboard/page.tsx", import.meta.url), "utf8");

test("task deletion is a prompt-free Convex tombstone, independent of box reachability", () => {
  assert.match(schema, /deletedTasks: v\.optional\(v\.array\(v\.object\(\{/);
  assert.match(snapshots, /export const tombstoneByToken = internalMutation/);
  assert.match(http, /path: "\/task-tombstones",\s*method: "POST"/);
  assert.match(mobile, /tombstoneAgentTask[\s\S]{0,1200}client\.deleteTask/);
  assert.doesNotMatch(mobile.slice(mobile.indexOf("const handleDeleteTask"), mobile.indexOf("const handleCompleteTask")), /if \(!client\.isConnected\)/);
  assert.match(web.slice(web.indexOf("const deleteTaskFromUI"), web.indexOf("const onTaskCreated")), /tombstoneAgentTask[\s\S]*deleteTask/);
});

test("an agent polls central tombstones and closes the local task when it reconnects", () => {
  assert.match(http, /path: "\/task-tombstones",\s*method: "GET"/);
  assert.match(snapshots, /export const listDeletedByToken = internalQuery/);
  assert.match(goSync, /reconcileTaskTombstonesFromConvex\(ctx, s\.taskMgr\)/);
  assert.match(go, /tm\.DeleteTask\(row\.TaskID\)/);
});
