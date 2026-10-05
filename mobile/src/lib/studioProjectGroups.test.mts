import assert from "node:assert/strict";
import test from "node:test";

import { isStudioRunnableProject, studioTargetLabel, studioTargetsForRepo } from "./studioProjectGroups.ts";

const yaver = { name: "yaver", path: "/Users/dev/Workspace/yaver.io" };

test("Studio groups runnable frontends beneath one top-level repo", () => {
  const rows = studioTargetsForRepo([
    { name: "yaver / mobile", path: `${yaver.path}/mobile`, framework: "expo", monorepoRoot: yaver.path, monorepoApp: "mobile" },
    { name: "yaver / web", path: `${yaver.path}/web`, framework: "nextjs", monorepoRoot: yaver.path, monorepoApp: "web" },
    { name: "desktop-app", path: `${yaver.path}/desktop/app`, framework: "node", role: "frontend" },
    { name: "relay", path: `${yaver.path}/relay`, framework: "go" },
  ], yaver);
  assert.deepEqual(rows.map((row) => studioTargetLabel(row, yaver)), ["desktop/app", "mobile", "web"]);
});

test("Studio hides fixtures, demos, and validation apps", () => {
  for (const path of [
    `${yaver.path}/tests/fixtures/native-ios-swift`,
    `${yaver.path}/demo/mobile/todo-rn`,
    `${yaver.path}/e2e/fixture-app`,
    `${yaver.path}/sdk/feedback/flutter`,
  ]) {
    assert.equal(isStudioRunnableProject({ name: "internal", path, framework: "expo" }), false, path);
  }
});

test("backend and generic coding packages stay out of Studio", () => {
  assert.equal(isStudioRunnableProject({ name: "backend", path: `${yaver.path}/backend`, framework: "convex" }), false);
  assert.equal(isStudioRunnableProject({ name: "cli", path: `${yaver.path}/cli`, framework: "node" }), false);
  assert.equal(isStudioRunnableProject({ name: "webui", path: "/repo/webui", framework: "node" }), true);
});
