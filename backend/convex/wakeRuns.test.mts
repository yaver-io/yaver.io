import test from "node:test";
import assert from "node:assert/strict";

import { blockedActionForWakeProgress } from "./wakeRuns.js";

test("wake progress maps auth and resize phases to structured blockers", () => {
  assert.equal(
    blockedActionForWakeProgress({ status: "blocked", phase: "awaiting-yaver-auth" }),
    "yaver_auth_required",
  );
  assert.equal(
    blockedActionForWakeProgress({ status: "blocked", phase: "authorizing-runners" }),
    "runner_auth_required",
  );
  assert.equal(
    blockedActionForWakeProgress({ status: "blocked", phase: "resize-required" }),
    "resize_required",
  );
});

test("wake progress maps terminal provider failures to wake_failed", () => {
  assert.equal(
    blockedActionForWakeProgress({ status: "failed", phase: "error" }),
    "wake_failed",
  );
  assert.equal(
    blockedActionForWakeProgress({ status: "cancelled", phase: "error" }),
    "wake_failed",
  );
});

test("wake progress leaves ordinary running phases retryable", () => {
  assert.equal(
    blockedActionForWakeProgress({ status: "running", phase: "registering" }),
    undefined,
  );
  assert.equal(
    blockedActionForWakeProgress({ status: "queued", phase: "queued" }),
    undefined,
  );
});
