import assert from "node:assert/strict";
import test from "node:test";

import { beginTaskTurn, mergeTaskSnapshot, taskStatusIsTerminal, withObservedTaskStatus } from "./studioTaskState.ts";

const task = (status: any, extra: Record<string, unknown> = {}) => ({
  id: "sfmg-task",
  title: "Make SFMG dark blue",
  description: "",
  status,
  output: [],
  createdAt: 1,
  updatedAt: 1,
  ...extra,
}) as any;

test("a retained raw replay cannot resurrect a completed Vibing task", () => {
  const completed = task("completed");
  assert.equal(withObservedTaskStatus(completed, "running"), completed);
  assert.equal(withObservedTaskStatus(task("review"), "queued").status, "review");
});

test("live output still advances a genuinely queued task", () => {
  assert.equal(withObservedTaskStatus(task("queued"), "running").status, "running");
});

test("an authoritative terminal snapshot ends stale local coding state", () => {
  const local = task("running", { createdAt: 8, updatedAt: 8, turns: [{ role: "user", content: "blue" }] });
  const merged = mergeTaskSnapshot(local, task("completed", { createdAt: 1, updatedAt: 9 }));
  assert.equal(merged.status, "completed");
  assert.equal(merged.createdAt, 8, "the client's observed start survives remote clock skew");
  assert.deepEqual(merged.turns, local.turns);
});

test("an accepted follow-up starts a new turn on the same completed task", () => {
  const completed = task("completed", { updatedAt: 9 });
  const running = beginTaskTurn(completed);
  assert.equal(running.status, "running");
  assert.equal(running.id, completed.id);
  assert.equal(running.updatedAt, 9, "the last server revision remains the stale-response fence");
});

test("an accepted follow-up moves a reviewed task back to active", () => {
  const review = task("review", { updatedAt: 9 });
  const running = beginTaskTurn(review);
  assert.equal(running.status, "running");
  assert.equal(running.id, review.id);
});

test("a pre-follow-up snapshot cannot close the newly accepted turn", () => {
  const running = beginTaskTurn(task("completed", { updatedAt: 9 }));
  assert.equal(mergeTaskSnapshot(running, task("completed", { updatedAt: 9 })).status, "running");
  assert.equal(mergeTaskSnapshot(running, task("running", { updatedAt: 10 })).status, "running");
  assert.equal(mergeTaskSnapshot(running, task("completed", { updatedAt: 11 })).status, "completed");
});

test("an old running snapshot cannot resurrect a finished turn", () => {
  const completed = task("completed", { updatedAt: 11 });
  assert.equal(mergeTaskSnapshot(completed, task("running", { updatedAt: 10 })).status, "completed");
});

test("terminal statuses are all classified explicitly", () => {
  assert.deepEqual(
    ["queued", "running", "review", "completed", "failed", "stopped"].filter((status) => taskStatusIsTerminal(status as any)),
    ["review", "completed", "failed", "stopped"],
  );
});
