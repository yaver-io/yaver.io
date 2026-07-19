/**
 * taskPlacement.test.mts — `npx tsx src/lib/taskPlacement.test.mts`.
 * Pins the mobile placement helpers that decide whether a prompt stays local
 * while Cloud Workspace wakes. These must match web/lib/task-placement.ts.
 */
import assert from "node:assert/strict";
import test from "node:test";

import {
  activationBlockReason,
  expensiveCloudPlacementMessage,
  mobileManagedArtifactStorageDeniedReason,
  pendingCloudDispatchTaskStatus,
  planIncludesYaverArtifactStorage,
  shouldConfirmExpensiveCloudPlacement,
  shouldDeferTaskForCloudWorkspace,
  taskPlacementRequestBody,
  type PlacementHelperDecision,
} from "./taskPlacementCore.ts";

const baseDecision: PlacementHelperDecision = {
  lane: "cloud_standard",
  resourceClass: "standard",
  status: "planned",
  wakeRequired: false,
  targetDeviceId: "cloud-device",
  creditEstimate: {
    display: "~0.5 standard credits from included Cloud Workspace allowance",
  },
};

test("mobile placement helper defers only cloud work that is not ready to dispatch", () => {
  assert.equal(shouldDeferTaskForCloudWorkspace(baseDecision), false);
  assert.equal(
    shouldDeferTaskForCloudWorkspace({
      ...baseDecision,
      wakeRequired: true,
    }),
    true,
  );
  assert.equal(
    shouldDeferTaskForCloudWorkspace({
      ...baseDecision,
      status: "queued",
    }),
    true,
  );
  assert.equal(
    shouldDeferTaskForCloudWorkspace({
      ...baseDecision,
      targetDeviceId: null,
    }),
    true,
  );
  assert.equal(
    shouldDeferTaskForCloudWorkspace({
      ...baseDecision,
      lane: "owned_machine",
      resourceClass: "standard",
      wakeRequired: true,
      targetDeviceId: null,
    }),
    false,
  );
});

test("mobile Yaver artifact storage helper is Cloud Workspace only", () => {
  assert.equal(planIncludesYaverArtifactStorage("cloud-workspace"), true);
  assert.equal(planIncludesYaverArtifactStorage("cloud-agent"), true);
  assert.equal(planIncludesYaverArtifactStorage("yaver-cloud-byok"), true);
  assert.equal(planIncludesYaverArtifactStorage("relay-pro"), false);
  assert.equal(planIncludesYaverArtifactStorage("free"), false);

  assert.equal(mobileManagedArtifactStorageDeniedReason({
    provider: "external",
    url: "https://example.com/app.apk",
  } as any), null);
  assert.match(
    mobileManagedArtifactStorageDeniedReason({ provider: "convex", storageId: "kg123" }) || "",
    /Cloud Workspace/,
  );
  assert.equal(mobileManagedArtifactStorageDeniedReason({
    provider: "convex",
    storageId: "kg123",
    confirmedCloudWorkspaceStorage: true,
  }), null);
});

test("mobile placement helper confirms heavy/build Cloud Workspace work without provider details", () => {
  assert.equal(shouldConfirmExpensiveCloudPlacement(baseDecision), false);
  assert.equal(
    shouldConfirmExpensiveCloudPlacement({
      ...baseDecision,
      lane: "cloud_heavy",
      resourceClass: "heavy",
    }),
    true,
  );
  assert.equal(
    shouldConfirmExpensiveCloudPlacement({
      ...baseDecision,
      lane: "cloud_build",
      resourceClass: "build",
    }),
    true,
  );

  const copy = expensiveCloudPlacementMessage({
    ...baseDecision,
    lane: "cloud_build",
    resourceClass: "build",
  });
  assert.match(copy, /Heavy Build/);
  assert.match(copy, /standard credits/);
  assert.doesNotMatch(copy, /cx\d|cpx|hetzner|hourly/i);
});

test("mobile placement helper surfaces wake failure activation blockers", () => {
  assert.equal(
    activationBlockReason({ action: "wake_failed", error: "provider capacity unavailable" }),
    "provider capacity unavailable",
  );
  assert.equal(
    activationBlockReason({ action: "wake_failed" }),
    "Cloud Workspace wake failed before this task could run.",
  );
  assert.equal(activationBlockReason({ action: "wake_scheduled" }), null);
});

test("mobile pending cloud dispatch maps terminal dispatch status to task status", () => {
  assert.equal(pendingCloudDispatchTaskStatus("queued"), "queued");
  assert.equal(pendingCloudDispatchTaskStatus("blocked"), "queued");
  assert.equal(pendingCloudDispatchTaskStatus("failed"), "failed");
  assert.equal(pendingCloudDispatchTaskStatus("cancelled"), "stopped");
  assert.equal(pendingCloudDispatchTaskStatus("expired"), "stopped");
});

test("mobile placement request body strips prompt-bearing runtime extras", () => {
  const body = taskPlacementRequestBody({
    taskId: "task-1",
    kind: "build",
    sourceSurface: "mobile-tasks",
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
    sourceSurface: "mobile-tasks",
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
