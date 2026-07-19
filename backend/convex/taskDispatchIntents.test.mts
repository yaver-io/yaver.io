import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import {
  dispatchBlockedActionNeedsUserAction,
  dispatchIntentForeignResourceDeniedMessage,
  isTerminalTaskDispatchStatus,
  shouldPreserveDispatchUserActionBlock,
  taskDispatchIntentUserLocalIndexName,
} from "./taskDispatchIntents.js";

const schemaSource = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "schema.ts"), "utf8");
const moduleSource = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "taskDispatchIntents.ts"), "utf8");

test("task dispatch terminal states include failed", () => {
  for (const status of ["dispatched", "failed", "cancelled", "expired"]) {
    assert.equal(isTerminalTaskDispatchStatus(status), true, status);
  }
  for (const status of ["queued", "dispatching", "blocked", "", undefined]) {
    assert.equal(isTerminalTaskDispatchStatus(status), false, String(status));
  }
});

test("dispatch local task idempotency is scoped per user", () => {
  assert.equal(taskDispatchIntentUserLocalIndexName(), "by_user_local_task");
  assert.match(
    schemaSource,
    /\.index\("by_user_local_task", \["userId", "localTaskId"\]\)/,
  );
});

test("dispatch foreign references are denied before metadata is stored", () => {
  assert.equal(dispatchIntentForeignResourceDeniedMessage("placement"), "placement not found");
  assert.equal(dispatchIntentForeignResourceDeniedMessage("cloudMachine"), "cloud machine not found");
  assert.match(moduleSource, /internal\.cloudMachines\.getInternal/);
  assert.match(moduleSource, /String\(machine\.userId\) !== String\(args\.userId\)/);
});

test("dispatch blocked actions distinguish user-action blockers from retryable reachability", () => {
  for (const action of ["runner_auth_required", "yaver_auth_required", "billing_required", "resize_required", "resize_failed", "wake_failed"]) {
    assert.equal(dispatchBlockedActionNeedsUserAction(action), true, action);
  }
  for (const action of ["", undefined, "workspace_unreachable", "wake_scheduled", "already_in_flight"]) {
    assert.equal(dispatchBlockedActionNeedsUserAction(action), false, String(action));
  }
});

test("dispatch stale queued/dispatching updates preserve user-action blockers", () => {
  for (const nextStatus of ["queued", "dispatching"]) {
    assert.equal(
      shouldPreserveDispatchUserActionBlock({
        currentStatus: "blocked",
        currentBlockedAction: "runner_auth_required",
        nextStatus,
      }),
      true,
      nextStatus,
    );
  }
  for (const nextStatus of ["dispatched", "failed", "cancelled", "expired", "blocked"]) {
    assert.equal(
      shouldPreserveDispatchUserActionBlock({
        currentStatus: "blocked",
        currentBlockedAction: "runner_auth_required",
        nextStatus,
      }),
      false,
      nextStatus,
    );
  }
  assert.equal(
    shouldPreserveDispatchUserActionBlock({
      currentStatus: "blocked",
      currentBlockedAction: undefined,
      nextStatus: "queued",
    }),
    false,
    "plain reachability block stays retryable",
  );
  assert.equal(
    shouldPreserveDispatchUserActionBlock({
      currentStatus: "queued",
      currentBlockedAction: "runner_auth_required",
      nextStatus: "dispatching",
    }),
    false,
    "only existing blocked rows are preserved",
  );
});

test("dispatch blocker preservation allows explicit readiness clears", () => {
  assert.equal(
    shouldPreserveDispatchUserActionBlock({
      currentStatus: "blocked",
      currentBlockedAction: "runner_auth_required",
      nextStatus: "queued",
      clearBlockedAction: true,
    }),
    false,
    "explicit clear can move back to queued",
  );
  assert.equal(
    shouldPreserveDispatchUserActionBlock({
      currentStatus: "blocked",
      currentBlockedAction: "runner_auth_required",
      nextStatus: "dispatching",
      clearBlockedAction: true,
    }),
    false,
    "explicit clear can move forward to dispatching",
  );
});
