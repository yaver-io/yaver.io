import test from "node:test";
import assert from "node:assert/strict";

import { taskPlacementRequestBody } from "./task-placement";

test("web task placement request body strips prompt-bearing runtime extras", () => {
  const body = taskPlacementRequestBody({
    taskId: "task-1",
    kind: "build",
    sourceSurface: "web-vibe-coding",
    projectSlug: "demo",
    requestedRunner: "codex",
    targetDeviceId: "local-dev",
    appCount: 2,
    repoSizeMb: 50,
    fileCount: 1200,
    hasNativeMobile: true,
    hasDocker: true,
    title: "secret title",
    description: "secret prompt wrapper",
    userPrompt: "private user words",
    workDir: "/Users/me/private/repo",
    diff: "secret diff",
  } as any);

  assert.deepEqual(body, {
    taskId: "task-1",
    kind: "build",
    sourceSurface: "web-vibe-coding",
    projectSlug: "demo",
    requestedRunner: "codex",
    targetDeviceId: "local-dev",
    forceCloud: undefined,
    forceRelaySource: undefined,
    appCount: 2,
    repoSizeMb: 50,
    fileCount: 1200,
    hasNativeMobile: true,
    hasDocker: true,
  });
  for (const key of ["title", "description", "userPrompt", "workDir", "diff"]) {
    assert.equal(Object.hasOwn(body, key), false, key);
  }
});

test("web task placement request body applies safe defaults", () => {
  assert.deepEqual(taskPlacementRequestBody({}), {
    taskId: undefined,
    kind: "unknown",
    sourceSurface: "web",
    projectSlug: undefined,
    requestedRunner: undefined,
    targetDeviceId: undefined,
    forceCloud: undefined,
    forceRelaySource: undefined,
    appCount: undefined,
    repoSizeMb: undefined,
    fileCount: undefined,
    hasNativeMobile: undefined,
    hasDocker: undefined,
  });
});
