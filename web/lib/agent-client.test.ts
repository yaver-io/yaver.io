/**
 * agent-client.test.ts — `npx tsx lib/agent-client.test.ts`.
 * Pins task-create request body serialization that Cloud Workspace handoff
 * depends on.
 */
import assert from "node:assert/strict";
import test from "node:test";

import { buildCreateTaskBody } from "./agent-client";

test("web createTask body defaults allowLocalFallback to false", () => {
  const body = buildCreateTaskBody({
    title: "Build apk",
    description: "",
    userPrompt: "secret prompt",
    runner: "codex",
  });
  assert.equal(body.source, "web");
  assert.equal(body.allowLocalFallback, false);
  assert.equal(body.userPrompt, "secret prompt");
});

test("web createTask body can mark final Cloud Workspace handoff", () => {
  const body = buildCreateTaskBody({
    title: "Build apk",
    description: "",
    runner: "codex",
    allowLocalFallback: true,
  });
  assert.equal(body.allowLocalFallback, true);
});
