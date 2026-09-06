import assert from "node:assert/strict";
import test from "node:test";

import { shouldNotifyTaskReply, taskReplyNotificationCopy } from "./taskReviewRoute.ts";

test("notifies when a running task moves to review", () => {
  assert.equal(shouldNotifyTaskReply("running", "review"), true);
});

test("notifies when a queued task moves to review", () => {
  assert.equal(shouldNotifyTaskReply("queued", "review"), true);
});

test("notifies when a normal running turn replies without claiming full review", () => {
  assert.equal(shouldNotifyTaskReply("running", "ready"), true);
});

test("does not notify for initial review rows", () => {
  assert.equal(shouldNotifyTaskReply(undefined, "review"), false);
});

test("does not notify for completed", () => {
  assert.equal(shouldNotifyTaskReply("running", "completed"), false);
});

test("notification copy leads with the semantic reply and removes markdown noise", () => {
  assert.deepEqual(taskReplyNotificationCopy({
    status: "ready",
    taskTitle: "Fix task replies",
    assistantText: "## Update\n\nI fixed the **message lane** and verified it.\n\n```sh\nnpm test\n```",
  }), {
    title: "Yaver replied",
    body: "Update I fixed the message lane and verified it.",
  });
  assert.equal(taskReplyNotificationCopy({
    status: "review",
    taskTitle: "Fix task replies",
  }).title, "Ready to review");
});

test("does not notify when a review task is manually completed", () => {
  assert.equal(shouldNotifyTaskReply("review", "completed"), false);
});
