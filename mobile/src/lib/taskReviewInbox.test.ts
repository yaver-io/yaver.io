import assert from "node:assert/strict";
import test from "node:test";
import {
  MOBILE_REVIEW_MAX_AGE_MS,
  mobileReviewInboxPolicy,
  reviewInboxTaskKey,
} from "./taskReviewInbox.ts";

const now = Date.UTC(2026, 8, 18, 12);
const task = (id: string, status: string, age: number) => ({
  id,
  deviceId: "box-1",
  status,
  updatedAt: now - age,
});

test("review inbox hides review work older than 24 hours", () => {
  const fresh = task("fresh", "review", MOBILE_REVIEW_MAX_AGE_MS - 1);
  const old = task("old", "review", MOBILE_REVIEW_MAX_AGE_MS + 1);
  const policy = mobileReviewInboxPolicy([fresh, old], now);
  assert.equal(policy.visibleTaskKeys.has(reviewInboxTaskKey(fresh)), true);
  assert.equal(policy.visibleTaskKeys.has(reviewInboxTaskKey(old)), false);
});

test("a lone recent completion is presented in Review when Active is empty", () => {
  const completed = task("done", "completed", 60_000);
  const policy = mobileReviewInboxPolicy([completed], now);
  assert.equal(policy.promotedCompletedTaskKey, reviewInboxTaskKey(completed));
});

test("completion is not promoted while work is active or several results landed", () => {
  const completed = task("done", "completed", 60_000);
  const second = task("done-2", "completed", 90_000);
  const running = task("run", "running", 1_000);
  assert.equal(mobileReviewInboxPolicy([completed, running], now).promotedCompletedTaskKey, null);
  assert.equal(mobileReviewInboxPolicy([completed, second], now).promotedCompletedTaskKey, null);
});
