/**
 * taskRequestBody.test.mts — `npx tsx src/lib/taskRequestBody.test.mts`.
 * Ensures Cloud Workspace handoff flags are serialized only for final target
 * POSTs, not for initial task creation.
 */
import assert from "node:assert/strict";
import test from "node:test";

import { buildSendTaskRequestBody } from "./taskRequestBody.ts";

test("mobile task request body omits allowLocalFallback for initial sends", () => {
  const body = buildSendTaskRequestBody({
    title: "Build apk",
    description: "",
    runner: "codex",
    codeMode: true,
  });
  assert.equal(body.source, "mobile-code");
  assert.equal(Object.prototype.hasOwnProperty.call(body, "allowLocalFallback"), false);
});

test("mobile task request body includes allowLocalFallback only for final handoff", () => {
  const body = buildSendTaskRequestBody({
    title: "Build apk",
    description: "",
    runner: "codex",
    codeMode: true,
    allowLocalFallback: true,
  });
  assert.equal(body.allowLocalFallback, true);
});
