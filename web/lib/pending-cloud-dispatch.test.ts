/**
 * pending-cloud-dispatch.test.ts — `npx tsx lib/pending-cloud-dispatch.test.ts`
 * from web/. Ensures prompt-free dispatch-intent metadata updates local pending
 * placeholders without touching prompt-bearing task params.
 */
import {
  cloudWorkspaceRequiredBlockedAction,
  listPendingCloudDispatches,
  mergePendingCloudPlacementStatus,
  mergePendingCloudDispatchIntents,
  pendingCloudDispatchNeedsUserAction,
  pendingCloudDispatchTaskStatus,
  pendingCloudTaskPlaceholder,
  saveCloudWorkspaceRequiredDispatch,
  savePendingCloudDispatch,
  updatePendingCloudDispatch,
} from "./pending-cloud-dispatch";
import {
  activationBlockReason,
  expensiveCloudPlacementMessage,
  placementLaneLabel,
  shouldConfirmExpensiveCloudPlacement,
  taskDispatchIntentCreateBody,
  taskDispatchIntentUpdateBody,
  type TaskDispatchIntent,
  type TaskPlacementDecision,
  type TaskPlacementStatus,
} from "./task-placement";
import {
  CloudWorkspaceRequiredError,
  decodeCloudWorkspaceRequiredError,
} from "./cloud-workspace-required";

let passed = 0;
let failed = 0;
function ok(cond: boolean, msg: string) {
  if (cond) {
    passed++;
  } else {
    failed++;
    console.error("  ✗ " + msg);
  }
}
function eq<T>(got: T, want: T, msg: string) {
  ok(got === want, `${msg} (got ${String(got)}, want ${String(want)})`);
}

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
}

(globalThis as any).window = { localStorage: new MemoryStorage() };
(globalThis as any).localStorage = (globalThis as any).window.localStorage;
const future = Date.now() + 60_000;

async function testCloudRequiredDecoder() {
  const cloudRequired = await decodeCloudWorkspaceRequiredError(new Response(JSON.stringify({
    action: "cloud_workspace_required",
    pendingTaskId: "pending-cloud:web-409",
    reason: "wake scheduled",
    placement: {
      id: "placement-web-409",
      lane: "cloud_standard",
      targetDeviceId: "cloud-dev",
    },
    activation: {
      ok: true,
      action: "wake_scheduled",
      targetDeviceId: "cloud-dev",
    },
  }), { status: 409, headers: { "content-type": "application/json" } }));
  ok(cloudRequired instanceof CloudWorkspaceRequiredError, "web decoder returns typed cloud required error");
  eq(cloudRequired?.pendingTaskId, "pending-cloud:web-409", "web decoder preserves pending id");
  eq(cloudRequired?.placement?.targetDeviceId, "cloud-dev", "web decoder preserves target device");
  eq(await decodeCloudWorkspaceRequiredError(new Response("{}", { status: 400 })), null, "web decoder ignores non-409 responses");
}

async function testCloudRequiredDispatchHelper() {
  const originalFetch = globalThis.fetch;
  let capturedBody: any = null;
  (globalThis as any).fetch = async (_url: string, init?: RequestInit) => {
    capturedBody = init?.body ? JSON.parse(String(init.body)) : null;
    return new Response(JSON.stringify({
      id: "intent-helper",
      localTaskId: "pending-cloud:helper",
      status: "queued",
      attempts: 4,
      expiresAt: future,
      createdAt: 1,
      updatedAt: 2,
    }), { status: 200, headers: { "content-type": "application/json" } });
  };
  try {
    const err = new CloudWorkspaceRequiredError({
      pendingTaskId: "pending-cloud:helper",
      reason: "server selected cloud",
      placement: {
        id: "placement-helper",
        lane: "cloud_build",
        targetDeviceId: "cloud-dev",
        cloudMachineId: "machine-helper",
        creditEstimate: { display: "~1 standard credit" },
      },
      activation: {
        ok: false,
        action: "runner_auth_required",
        targetDeviceId: "cloud-dev",
        reason: "Codex sign-in required",
      },
    });
    const row = saveCloudWorkspaceRequiredDispatch({
      err,
      token: "owner-token",
      sourceSurface: "web-test",
      requestedRunner: "codex",
      projectSlug: "private-repo",
      params: {
        title: "Build",
        description: "secret prompt body",
        userPrompt: "private user words",
        workDir: "/Users/me/private/repo",
        runner: "codex",
      },
    }, 1234);
    eq(row.dispatchStatus, "blocked", "helper marks user-action activation as blocked");
    eq(row.blockedAction, "runner_auth_required", "helper preserves blocker action");
    await new Promise((resolve) => setTimeout(resolve, 0));
    const stored = listPendingCloudDispatches().find((item) => item.localTaskId === "pending-cloud:helper");
    eq(stored?.params.userPrompt, "private user words", "helper keeps prompt only in local pending row");
    eq(stored?.dispatchStatus, "blocked", "intent refresh cannot clear local user-action block");
    eq(stored?.dispatchIntentId, "intent-helper", "helper merges dispatch intent id");
    ok(capturedBody, "helper posts dispatch-intent metadata");
    ok(!JSON.stringify(capturedBody).includes("secret prompt body"), "helper does not send description to dispatch intent");
    ok(!JSON.stringify(capturedBody).includes("/Users/me/private/repo"), "helper does not send workDir to dispatch intent");
    eq(capturedBody.localTaskId, "pending-cloud:helper", "helper sends local task id metadata");
    eq(capturedBody.projectSlug, "private-repo", "helper sends coarse project slug");
  } finally {
    globalThis.fetch = originalFetch;
  }
}

eq(cloudWorkspaceRequiredBlockedAction("runner_auth_required"), "runner_auth_required", "known blocker action is preserved");
eq(cloudWorkspaceRequiredBlockedAction("wake_scheduled"), undefined, "non-blocking activation stays queued");

function basePlacementLike(id: string): TaskPlacementDecision & { id: string } {
  return {
    id,
    lane: "cloud_standard",
    resourceClass: "standard",
    entitlement: "cloud-workspace",
    status: "queued",
    reason: "standard workspace",
    wakeRequired: true,
    creditEstimate: {
      unit: "usd_cents",
      estimatedCents: 30,
      hourlyCents: 60,
      estimatedMinutes: 30,
      standardCredits: 0.5,
      includedStandardCreditsBucket: 120,
      creditWeight: 1,
      billingScope: "cloud-included-then-metered",
      resourceClass: "standard",
      display: "~0.5 standard credits from included Cloud Workspace allowance",
    },
    createdAt: 1,
    updatedAt: 2,
  };
}

savePendingCloudDispatch({
  localTaskId: "pending-cloud:web",
  placementId: "placement-old",
  placementLane: "cloud_standard",
  targetDeviceId: "old-target",
  params: {
    title: "Build apk",
    description: "full prompt body",
    userPrompt: "secret prompt",
    runner: "codex",
  },
  createdAt: 1,
  updatedAt: 1,
  attempts: 0,
});

const intents: TaskDispatchIntent[] = [{
  id: "intent-1",
  localTaskId: "pending-cloud:web",
  placementId: "placement-new",
  status: "blocked",
  lane: "cloud_build",
  targetDeviceId: "cloud-dev",
  attempts: 3,
  reason: "runner auth required",
  lastError: "runner not authorized",
  expiresAt: future,
  createdAt: 2,
  updatedAt: 3,
}];

const merged = mergePendingCloudDispatchIntents(intents);
eq(merged.length, 1, "one pending row remains");
eq(merged[0].dispatchIntentId, "intent-1", "intent id merged");
eq(merged[0].dispatchStatus, "blocked", "status merged");
eq(merged[0].placementLane, "cloud_build", "lane merged");
eq(merged[0].targetDeviceId, "cloud-dev", "target merged");
eq(merged[0].attempts, 3, "attempts merged");
eq(merged[0].dispatchExpiresAt, future, "server dispatch expiry merged");
eq(merged[0].blockedReason, "runner auth required", "blocked reason merged");
eq(merged[0].lastError, "runner not authorized", "last error merged");
eq(merged[0].params.userPrompt, "secret prompt", "prompt params preserved locally");
eq(pendingCloudDispatchNeedsUserAction(merged[0]), false, "plain blocked intent remains retryable");

const authBlocked = {
  ...merged[0],
  blockedAction: "runner_auth_required" as const,
  blockedReason: "Cloud Workspace needs Codex sign-in.",
};
eq(pendingCloudDispatchNeedsUserAction(authBlocked), true, "runner auth blocker needs user action");
eq(
  pendingCloudDispatchNeedsUserAction({
    ...merged[0],
    blockedAction: "wake_failed" as const,
    blockedReason: "provider capacity unavailable",
  }),
  true,
  "wake failure blocker needs user action",
);
eq(
  pendingCloudDispatchNeedsUserAction({
    ...merged[0],
    dispatchStatus: "queued",
    blockedAction: "runner_auth_required" as const,
  }),
  false,
  "non-blocked row remains dispatchable",
);
savePendingCloudDispatch(authBlocked);
updatePendingCloudDispatch("pending-cloud:web", {
  dispatchStatus: "queued",
  lastError: "",
});
const preservedLocalBlock = listPendingCloudDispatches()[0];
eq(preservedLocalBlock.dispatchStatus, "blocked", "stale queued local update cannot clear user-action blocker");
eq(preservedLocalBlock.blockedAction, "runner_auth_required", "stale queued local update preserves blocker action");
mergePendingCloudDispatchIntents([{
  ...intents[0],
  status: "queued",
  reason: "workspace reachable",
  lastError: "",
}]);
const preservedIntentBlock = listPendingCloudDispatches()[0];
eq(preservedIntentBlock.dispatchStatus, "blocked", "stale queued intent cannot clear user-action blocker");
eq(preservedIntentBlock.blockedReason, "Cloud Workspace needs Codex sign-in.", "stale queued intent preserves blocker reason");
savePendingCloudDispatch({
  ...merged[0],
  localTaskId: "pending-cloud:server-blocked",
  dispatchStatus: "queued",
  blockedAction: undefined,
  blockedReason: undefined,
});
const serverBlocked = mergePendingCloudDispatchIntents([{
  ...intents[0],
  id: "intent-server-blocked",
  localTaskId: "pending-cloud:server-blocked",
  status: "blocked",
  blockedAction: "billing_required",
  reason: "Cloud Workspace subscription needed.",
}]).find((row) => row.localTaskId === "pending-cloud:server-blocked");
ok(!!serverBlocked, "server-blocked row merged");
eq(serverBlocked?.blockedAction, "billing_required", "server blocker action merged");
eq(pendingCloudDispatchNeedsUserAction(serverBlocked!), true, "server blocker action stops auto-dispatch");

const wakeStatus: TaskPlacementStatus = {
  ...basePlacementLike("placement-new"),
  status: "queued",
  targetDeviceId: "cloud-wake-target",
  latestWakeRun: {
    id: "wake-1",
    machineId: "machine-1",
    placementId: "placement-new",
    kind: "provision",
    status: "running",
    phase: "installing-agent",
    progress: 64,
    targetDeviceId: "cloud-wake-target",
    reason: "Cloud Workspace wake: installing-agent (64%)",
    startedAt: 10,
    updatedAt: 20,
  },
};
const wakeMerged = mergePendingCloudPlacementStatus(merged[0], wakeStatus);
eq(wakeMerged.targetDeviceId, "cloud-wake-target", "placement status target merged");
eq(wakeMerged.wakePhase, "installing-agent", "wake phase merged");
eq(wakeMerged.wakeProgress, 64, "wake progress merged");
eq(wakeMerged.placementReason, "Cloud Workspace wake: installing-agent (64%)", "wake reason merged");
eq(wakeMerged.dispatchStatus, "blocked", "non-terminal wake does not clear existing dispatch status");
const wakeRendered = JSON.stringify(pendingCloudTaskPlaceholder(wakeMerged));
ok(wakeRendered.includes("Wake phase: installing-agent (64%)"), "placeholder renders wake progress");
ok(!wakeRendered.includes("secret prompt"), "wake placeholder does not leak prompt");

const runnerAuthMerged = mergePendingCloudPlacementStatus(merged[0], {
  ...wakeStatus,
  latestWakeRun: {
    ...wakeStatus.latestWakeRun!,
    status: "blocked",
    phase: "authorizing-runners",
    progress: 90,
    reason: "Cloud Workspace is awake but Codex needs sign-in.",
  },
});
eq(runnerAuthMerged.dispatchStatus, "blocked", "wake blocker marks dispatch blocked");
eq(runnerAuthMerged.blockedAction, "runner_auth_required", "runner auth wake phase maps to blocker action");
eq(pendingCloudDispatchNeedsUserAction(runnerAuthMerged), true, "runner auth wake blocker needs user action");

const stillBlockedOnStaleStatus = mergePendingCloudPlacementStatus(runnerAuthMerged, {
  ...wakeStatus,
  latestWakeRun: {
    ...wakeStatus.latestWakeRun!,
    status: "running",
    phase: "registering",
    progress: 70,
    reason: "Cloud Workspace wake: registering (70%)",
  },
});
eq(stillBlockedOnStaleStatus.dispatchStatus, "blocked", "non-ready status does not clear user-action blocker");
eq(stillBlockedOnStaleStatus.blockedAction, "runner_auth_required", "non-ready status preserves blocker action");

const unblockedByRunningPlacement = mergePendingCloudPlacementStatus(runnerAuthMerged, {
  ...wakeStatus,
  status: "running",
  targetDeviceId: "cloud-wake-target",
  latestWakeRun: {
    ...wakeStatus.latestWakeRun!,
    status: "succeeded",
    phase: "online",
    progress: 100,
    reason: "Cloud Workspace is ready.",
  },
});
eq(unblockedByRunningPlacement.dispatchStatus, "queued", "ready placement clears blocker back to queued");
eq(unblockedByRunningPlacement.blockedAction, undefined, "ready placement clears blocker action");
eq(unblockedByRunningPlacement.blockedReason, undefined, "ready placement clears blocker reason");
eq(unblockedByRunningPlacement.lastError, undefined, "ready placement clears stale blocker error");
eq(unblockedByRunningPlacement.clearedBlockedAction, true, "ready placement marks backend blocker for clearing");
eq(pendingCloudDispatchNeedsUserAction(unblockedByRunningPlacement), false, "ready placement can dispatch again");

const placeholder = pendingCloudTaskPlaceholder(merged[0]);
eq(placeholder.status, "queued", "blocked cloud placeholder remains queued/actionable");
const rendered = JSON.stringify(placeholder);
for (const want of ["Dispatch status: blocked", "Blocked: runner auth required", "Last dispatch attempt: runner not authorized"]) {
  ok(rendered.includes(want), `placeholder includes ${want}`);
}
for (const forbidden of ["secret prompt", "full prompt body", "userPrompt"]) {
  ok(!rendered.includes(forbidden), `placeholder does not leak ${forbidden}`);
}
const authBlockedRendered = JSON.stringify(pendingCloudTaskPlaceholder(authBlocked));
ok(authBlockedRendered.includes("Needs your action"), "auth-blocked placeholder explains user action");
ok(!authBlockedRendered.includes("secret prompt"), "auth-blocked placeholder does not leak prompt");

eq(listPendingCloudDispatches()[0].dispatchStatus, "blocked", "merged row persisted");
eq(pendingCloudDispatchTaskStatus("failed"), "failed", "failed dispatch renders as failed task");
eq(pendingCloudDispatchTaskStatus("cancelled"), "stopped", "cancelled dispatch renders as stopped task");
eq(pendingCloudDispatchTaskStatus("expired"), "stopped", "expired dispatch renders as stopped task");
eq(pendingCloudDispatchTaskStatus("blocked"), "queued", "blocked dispatch remains queued task");
const expiredPlaceholder = pendingCloudTaskPlaceholder({
  ...merged[0],
  dispatchStatus: "queued",
  dispatchExpiresAt: Date.now() - 1,
});
eq(expiredPlaceholder.status, "stopped", "locally expired placeholder renders stopped");
ok(
  JSON.stringify(expiredPlaceholder).includes("Dispatch status: expired"),
  "locally expired placeholder explains expired dispatch status",
);

const baseDecision: TaskPlacementDecision = {
  lane: "cloud_standard",
  resourceClass: "standard",
  entitlement: "cloud-workspace",
  status: "planned",
  reason: "standard workspace",
  wakeRequired: false,
  creditEstimate: {
    unit: "usd_cents",
    estimatedCents: 30,
    hourlyCents: 60,
    estimatedMinutes: 30,
    standardCredits: 0.5,
    includedStandardCreditsBucket: 120,
    creditWeight: 1,
    billingScope: "cloud-included-then-metered",
    resourceClass: "standard",
    display: "~0.5 standard credits from included Cloud Workspace allowance",
  },
};
ok(!shouldConfirmExpensiveCloudPlacement(baseDecision), "standard cloud placement does not require confirmation");
ok(
  shouldConfirmExpensiveCloudPlacement({
    ...baseDecision,
    lane: "cloud_build",
    resourceClass: "build",
    reason: "native build",
  }),
  "build cloud placement requires confirmation",
);
const confirmCopy = expensiveCloudPlacementMessage({
  ...baseDecision,
  lane: "cloud_heavy",
  resourceClass: "heavy",
});
ok(confirmCopy.includes("Heavy Workspace"), "heavy confirmation names the heavy workspace");
ok(confirmCopy.includes("standard credits"), "confirmation uses standard-credit allowance copy");
ok(!/cx\d|cpx|hetzner|hourly/i.test(confirmCopy), "confirmation hides provider types and hourly pricing");
eq(placementLaneLabel("cloud_heavy"), "Heavy workspace", "heavy cloud lane uses product-level label");
eq(placementLaneLabel("cloud_build"), "Heavy build", "build cloud lane uses product-level label");
ok(!/16G|32G|GB|cx\d|cpx|hetzner|hourly/i.test([
  placementLaneLabel("cloud_heavy"),
  placementLaneLabel("cloud_build"),
].join(" ")), "cloud lane labels hide provider/RAM vocabulary");
eq(
  activationBlockReason({ ok: false, action: "wake_failed", error: "provider capacity unavailable" }),
  "provider capacity unavailable",
  "wake_failed activation surfaces provider-safe error text",
);
eq(
  activationBlockReason({ ok: false, action: "wake_failed" }),
  "Cloud Workspace wake failed before this task could run.",
  "wake_failed activation has an honest fallback blocker",
);
const createIntentBody = taskDispatchIntentCreateBody({
  localTaskId: "pending-cloud:web-leak",
  placementId: "placement-1",
  sourceSurface: "web",
  lane: "cloud_standard",
  targetDeviceId: "cloud-device",
  cloudMachineId: "cloud-machine",
  requestedRunner: "codex",
  projectSlug: "demo",
  reason: "workspace waking",
  ttlMs: 60_000,
  description: "secret prompt body",
  userPrompt: "make my app",
  workDir: "/Users/me/private/repo",
  images: ["data:image/png;base64,secret"],
  customCommand: "deploy prod",
} as any);
const createIntentJson = JSON.stringify(createIntentBody);
ok(!createIntentJson.includes("secret prompt body"), "dispatch create body strips description");
ok(!createIntentJson.includes("/Users/me/private/repo"), "dispatch create body strips workDir");
ok(!createIntentJson.includes("data:image"), "dispatch create body strips images");
ok(!("customCommand" in (createIntentBody as any)), "dispatch create body strips customCommand");
const updateIntentBody = taskDispatchIntentUpdateBody({
  localTaskId: "pending-cloud:web-leak",
  status: "dispatching",
  taskId: "task-1",
  targetDeviceId: "cloud-device",
  lastError: "will retry",
  reason: "workspace ready",
  blockedAction: "runner_auth_required",
  clearBlockedAction: true,
  bumpAttempt: true,
  description: "secret prompt body",
  workDir: "/Users/me/private/repo",
  stdout: "private logs",
  files: [{ path: "App.tsx", content: "secret" }],
} as any);
const updateIntentJson = JSON.stringify(updateIntentBody);
ok(!updateIntentJson.includes("secret prompt body"), "dispatch update body strips description");
ok(!updateIntentJson.includes("/Users/me/private/repo"), "dispatch update body strips workDir");
ok(!updateIntentJson.includes("private logs"), "dispatch update body strips stdout");
ok(!("files" in (updateIntentBody as any)), "dispatch update body strips files");

testCloudRequiredDecoder()
  .then(() => testCloudRequiredDispatchHelper())
  .catch((err) => {
    failed++;
    console.error("  ✗ web decoder threw", err);
  })
  .finally(() => {
    console.log(`\npending-cloud-dispatch (web): ${passed} passed, ${failed} failed`);
    if (failed > 0) process.exit(1);
  });
