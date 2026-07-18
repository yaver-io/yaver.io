import test from "node:test";
import assert from "node:assert/strict";

import {
  classifyProjectForPlacement,
  strongestResourceClass,
} from "./taskPlacementClassifier.js";

test("classifyProjectForPlacement keeps source/vibe work on relay for small projects", () => {
  assert.deepEqual(
    classifyProjectForPlacement({
      kind: "vibe",
      projectSlug: "todo-app",
      stack: "vite react",
      appCount: 1,
      repoSizeMb: 120,
      fileCount: 3000,
    }),
    {
      resourceClass: "relay-source",
      largeMonorepo: false,
      largeProject: false,
      reason: "source-only/vibe task can start on relay",
    },
  );
});

test("classifyProjectForPlacement routes native mobile metadata to heavy", () => {
  const result = classifyProjectForPlacement({
    kind: "vibe",
    projectSlug: "my-app",
    stack: "expo react-native",
    appCount: 1,
    repoSizeMb: 600,
    fileCount: 12_000,
  });

  assert.equal(result.resourceClass, "heavy");
  assert.equal(result.largeMonorepo, false);
  assert.equal(result.largeProject, false);
});

test("classifyProjectForPlacement routes build/deploy and huge repos to build", () => {
  assert.equal(
    classifyProjectForPlacement({ kind: "build", projectSlug: "single-app" }).resourceClass,
    "build",
  );
  assert.deepEqual(
    classifyProjectForPlacement({
      kind: "test",
      projectSlug: "big-workspace",
      stack: "pnpm workspace",
      repoSizeMb: 9_000,
      fileCount: 160_000,
    }),
    {
      resourceClass: "build",
      largeMonorepo: true,
      largeProject: true,
      reason: "large/native monorepo needs 32GB build capacity",
    },
  );
});

test("classifyProjectForPlacement detects large/native monorepos", () => {
  for (const input of [
    {
      kind: "test" as const,
      projectSlug: "workspace-app",
      stack: "pnpm monorepo expo react-native",
      appCount: 2,
      repoSizeMb: 2_500,
      fileCount: 45_000,
    },
  ]) {
    const result = classifyProjectForPlacement(input);
    assert.equal(result.resourceClass, "build");
    assert.equal(result.largeMonorepo, true);
    assert.equal(result.largeProject, true);
  }
});

test("strongestResourceClass never downgrades stored project profiles", () => {
  assert.equal(strongestResourceClass("heavy", "relay-source"), "heavy");
  assert.equal(strongestResourceClass("standard", "build"), "build");
  assert.equal(strongestResourceClass("phone", "relay-source"), "relay-source");
});
