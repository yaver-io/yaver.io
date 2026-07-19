/**
 * pendingCloudDispatch.test.mts — `npx tsx src/lib/pendingCloudDispatch.test.mts`.
 * Pins mobile's prompt-held Cloud Workspace queue behavior. Convex only gets
 * prompt-free dispatch metadata; the prompt stays in AsyncStorage until the
 * assigned workspace is ready.
 */
import assert from "node:assert/strict";
import test from "node:test";

import AsyncStorage from "@react-native-async-storage/async-storage";
import { CloudWorkspaceRequiredError } from "./cloudWorkspaceRequired.ts";
import {
  cloudWorkspaceRequiredBlockedAction,
  listPendingCloudDispatches,
  mergePendingCloudDispatchIntents,
  mergePendingCloudPlacementStatus,
  pendingCloudDispatchNeedsUserAction,
  pendingCloudTaskPlaceholder,
  saveCloudWorkspaceRequiredDispatch,
  savePendingCloudDispatch,
  updatePendingCloudDispatch,
} from "./pendingCloudDispatch.ts";
import {
  taskDispatchIntentCreateBody,
  taskDispatchIntentUpdateBody,
} from "./taskDispatchIntentBody.ts";
import type { TaskDispatchIntent, TaskPlacementStatus } from "./taskPlacement.ts";

const future = Date.now() + 60_000;
const now = Date.now();
const storage = ((AsyncStorage as any).default ?? AsyncStorage) as {
  clear: () => Promise<void>;
};

class MemoryStorage {
  private data = new Map<string, string>();
  getItem(key: string) {
    return this.data.get(key) ?? null;
  }
  setItem(key: string, value: string) {
    this.data.set(key, value);
  }
  removeItem(key: string) {
    this.data.delete(key);
  }
  clear() {
    this.data.clear();
  }
}

(globalThis as any).window = { localStorage: new MemoryStorage() };
globalThis.localStorage = (globalThis as any).window.localStorage;

test("mobile cloud workspace required helper keeps prompt local and marks user blockers", async () => {
  await storage.clear();
  assert.equal(cloudWorkspaceRequiredBlockedAction("runner_auth_required"), "runner_auth_required");
  assert.equal(cloudWorkspaceRequiredBlockedAction("wake_scheduled"), undefined);

  const row = await saveCloudWorkspaceRequiredDispatch({
    err: new CloudWorkspaceRequiredError({
      pendingTaskId: "pending-cloud:helper",
      reason: "server selected cloud",
      placement: {
        id: "placement-helper",
        lane: "cloud_build",
        targetDeviceId: "cloud-device",
        cloudMachineId: "machine-helper",
        creditEstimate: { display: "~1 standard credit" },
      },
      activation: {
        ok: false,
        action: "runner_auth_required",
        targetDeviceId: "cloud-device",
        reason: "Codex sign-in required",
      },
    }),
    sourceSurface: "mobile-test",
    requestedRunner: "codex",
    projectSlug: "private-repo",
    params: {
      title: "Build apk",
      description: "secret prompt body",
      runner: "codex",
      workDir: "/Users/me/private/repo",
    },
    createDispatchIntent: async (req) => {
      const json = JSON.stringify(req);
      assert.doesNotMatch(json, /secret prompt body/);
      assert.doesNotMatch(json, /\/Users\/me\/private\/repo/);
      assert.equal(req.localTaskId, "pending-cloud:helper");
      assert.equal(req.projectSlug, "private-repo");
      return {
        id: "intent-helper",
        localTaskId: req.localTaskId,
        status: "queued",
        attempts: 3,
        expiresAt: future,
        createdAt: 1,
        updatedAt: 2,
      } as any;
    },
  }, now);

  assert.equal(row.dispatchStatus, "blocked");
  assert.equal(row.blockedAction, "runner_auth_required");
  assert.equal(row.blockedReason, "Codex sign-in required");
  assert.equal(row.targetDeviceId, "cloud-device");
  assert.equal(row.placementCreditLabel, "~1 standard credit");

  const stored = (await listPendingCloudDispatches())[0];
  assert.equal(stored.params.description, "secret prompt body");
  assert.equal(stored.params.workDir, "/Users/me/private/repo");
  assert.equal(stored.dispatchIntentId, "intent-helper");
  assert.equal(stored.attempts, 3);
  const rendered = JSON.stringify(pendingCloudTaskPlaceholder(stored));
  assert.match(rendered, /Needs your action/);
  assert.doesNotMatch(rendered, /secret prompt body/);
  assert.doesNotMatch(rendered, /\/Users\/me\/private\/repo/);
});

function baseStatus(id: string): TaskPlacementStatus {
  return {
    id,
    lane: "cloud_standard",
    resourceClass: "standard",
    entitlement: "cloud-workspace",
    status: "queued",
    reason: "standard workspace",
    wakeRequired: true,
    targetDeviceId: "cloud-device",
    creditEstimate: {
      unit: "usd_cents",
      estimatedCents: 30,
      hourlyCents: 60,
      estimatedMinutes: 30,
      standardCredits: 0.5,
      billingScope: "cloud-included-then-metered",
      resourceClass: "standard",
      display: "~0.5 standard credits from included Cloud Workspace allowance",
    },
    createdAt: 1,
    updatedAt: 2,
  };
}

test("mobile pending cloud dispatch merges prompt-free intent metadata without leaking prompts", async () => {
  await storage.clear();
  await savePendingCloudDispatch({
    localTaskId: "pending-cloud:mobile",
    placementId: "placement-old",
    placementLane: "cloud_standard",
    targetDeviceId: "old-target",
    params: {
      title: "Build apk",
      description: "full prompt body",
      runner: "codex",
    },
    createdAt: now,
    updatedAt: now,
    attempts: 0,
  });

  const intents: TaskDispatchIntent[] = [{
    id: "intent-1",
    localTaskId: "pending-cloud:mobile",
    placementId: "placement-new",
    status: "blocked",
    lane: "cloud_build",
    targetDeviceId: "cloud-dev",
    attempts: 2,
    reason: "runner auth required",
    lastError: "runner not authorized",
    expiresAt: future,
    createdAt: 2,
    updatedAt: 3,
  }];

  const merged = await mergePendingCloudDispatchIntents(intents);
  assert.equal(merged.length, 1);
  assert.equal(merged[0].dispatchIntentId, "intent-1");
  assert.equal(merged[0].placementLane, "cloud_build");
  assert.equal(merged[0].targetDeviceId, "cloud-dev");
  assert.equal(merged[0].attempts, 2);
  assert.equal(merged[0].blockedReason, "runner auth required");
  assert.equal(merged[0].params.description, "full prompt body");

  const rendered = JSON.stringify(pendingCloudTaskPlaceholder(merged[0]));
  assert.match(rendered, /Dispatch status: blocked/);
  assert.match(rendered, /Blocked: runner auth required/);
  assert.doesNotMatch(rendered, /full prompt body/);
});

test("mobile pending cloud dispatch preserves user-action blockers against stale queued updates", async () => {
  await storage.clear();
  await savePendingCloudDispatch({
    localTaskId: "pending-cloud:auth",
    placementId: "placement-auth",
    placementLane: "cloud_standard",
    dispatchStatus: "blocked",
    blockedAction: "runner_auth_required",
    blockedReason: "Cloud Workspace needs Codex sign-in.",
    params: { title: "Build apk", description: "secret prompt" },
    createdAt: now,
    updatedAt: now,
    attempts: 0,
  });

  await updatePendingCloudDispatch("pending-cloud:auth", {
    dispatchStatus: "queued",
    lastError: "",
  });
  const row = (await listPendingCloudDispatches())[0];
  assert.equal(row.dispatchStatus, "blocked");
  assert.equal(row.blockedAction, "runner_auth_required");
  assert.equal(pendingCloudDispatchNeedsUserAction(row), true);
});

test("mobile pending cloud dispatch merges wake progress and blocker phases", async () => {
  const row = {
    localTaskId: "pending-cloud:wake",
    placementId: "placement-wake",
    placementLane: "cloud_standard",
    params: { title: "Build apk", description: "secret prompt" },
    createdAt: now,
    updatedAt: now,
    attempts: 0,
  };
  const merged = mergePendingCloudPlacementStatus(row, {
    ...baseStatus("placement-wake"),
    latestWakeRun: {
      id: "wake-1",
      machineId: "machine-1",
      placementId: "placement-wake",
      kind: "provision",
      status: "blocked",
      phase: "authorizing-runners",
      progress: 90,
      reason: "Cloud Workspace is awake but Codex needs sign-in.",
      startedAt: 10,
      updatedAt: 20,
    },
  });

  assert.equal(merged.dispatchStatus, "blocked");
  assert.equal(merged.blockedAction, "runner_auth_required");
  assert.equal(merged.wakePhase, "authorizing-runners");
  assert.equal(merged.wakeProgress, 90);
  assert.equal(pendingCloudDispatchNeedsUserAction(merged), true);
  const rendered = JSON.stringify(pendingCloudTaskPlaceholder(merged));
  assert.match(rendered, /Wake phase: authorizing-runners \(90%\)/);
  assert.doesNotMatch(rendered, /secret prompt/);
});

test("mobile pending cloud dispatch clears user-action blocker only after ready placement status", async () => {
  const blocked = {
    localTaskId: "pending-cloud:ready",
    placementId: "placement-ready",
    placementLane: "cloud_standard",
    dispatchStatus: "blocked",
    blockedAction: "runner_auth_required" as const,
    blockedReason: "Cloud Workspace needs Codex sign-in.",
    lastError: "runner not authorized",
    params: { title: "Build apk", description: "secret prompt" },
    createdAt: now,
    updatedAt: now,
    attempts: 0,
  };

  const stale = mergePendingCloudPlacementStatus(blocked, {
    ...baseStatus("placement-ready"),
    latestWakeRun: {
      id: "wake-stale",
      machineId: "machine-1",
      placementId: "placement-ready",
      kind: "provision",
      status: "running",
      phase: "registering",
      progress: 70,
      reason: "Cloud Workspace wake: registering (70%)",
      startedAt: 10,
      updatedAt: 20,
    },
  });
  assert.equal(stale.dispatchStatus, "blocked");
  assert.equal(stale.blockedAction, "runner_auth_required");
  assert.equal(pendingCloudDispatchNeedsUserAction(stale), true);

  const ready = mergePendingCloudPlacementStatus(blocked, {
    ...baseStatus("placement-ready"),
    status: "running",
    targetDeviceId: "cloud-device",
    latestWakeRun: {
      id: "wake-ready",
      machineId: "machine-1",
      placementId: "placement-ready",
      kind: "provision",
      status: "succeeded",
      phase: "online",
      progress: 100,
      reason: "Cloud Workspace is ready.",
      startedAt: 10,
      updatedAt: 30,
    },
  });
  assert.equal(ready.dispatchStatus, "queued");
  assert.equal(ready.blockedAction, undefined);
  assert.equal(ready.blockedReason, undefined);
  assert.equal(ready.lastError, undefined);
  assert.equal(ready.clearedBlockedAction, true);
  assert.equal(pendingCloudDispatchNeedsUserAction(ready), false);
});

test("mobile task dispatch intent HTTP bodies strip prompt-bearing runtime extras", () => {
  const createBody = taskDispatchIntentCreateBody({
    localTaskId: "pending-cloud:mobile-leak",
    placementId: "placement-1",
    sourceSurface: "mobile",
    lane: "cloud_standard",
    targetDeviceId: "cloud-device",
    cloudMachineId: "cloud-machine",
    requestedRunner: "codex",
    projectSlug: "demo",
    reason: "workspace waking",
    ttlMs: 60_000,
    description: "secret prompt body",
    customCommand: "deploy prod",
    speechContext: "private dictated prompt",
    images: ["data:image/png;base64,secret"],
    workDir: "/Users/me/private/repo",
  } as any);
  const createJson = JSON.stringify(createBody);
  assert.doesNotMatch(createJson, /secret prompt body/);
  assert.doesNotMatch(createJson, /private dictated prompt/);
  assert.doesNotMatch(createJson, /data:image/);
  assert.doesNotMatch(createJson, /\/Users\/me\/private\/repo/);
  assert.equal("customCommand" in (createBody as any), false);

  const updateBody = taskDispatchIntentUpdateBody({
    localTaskId: "pending-cloud:mobile-leak",
    status: "dispatching",
    taskId: "task-1",
    targetDeviceId: "cloud-device",
    lastError: "will retry",
    reason: "workspace ready",
    blockedAction: "runner_auth_required",
    clearBlockedAction: true,
    bumpAttempt: true,
    description: "secret prompt body",
    stdout: "private logs",
    files: [{ path: "App.tsx", content: "secret" }],
    workDir: "/Users/me/private/repo",
  } as any);
  const updateJson = JSON.stringify(updateBody);
  assert.doesNotMatch(updateJson, /secret prompt body/);
  assert.doesNotMatch(updateJson, /private logs/);
  assert.doesNotMatch(updateJson, /\/Users\/me\/private\/repo/);
  assert.equal("files" in (updateBody as any), false);
});
