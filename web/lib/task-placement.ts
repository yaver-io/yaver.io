"use client";

import { CONVEX_URL } from "@/lib/constants";

export type TaskPlacementKind =
  | "vibe"
  | "build"
  | "deploy"
  | "test"
  | "source"
  | "autorun"
  | "unknown";

export type TaskPlacementLane =
  | "phone_sandbox"
  | "relay_source"
  | "owned_machine"
  | "cloud_standard"
  | "cloud_heavy"
  | "cloud_build"
  | "external_deploy"
  | "manual";

export type TaskPlacementResourceClass =
  | "phone"
  | "relay-source"
  | "standard"
  | "heavy"
  | "build";

export type TaskPlacementCreditEstimate = {
  unit: "usd_cents";
  estimatedCents: number;
  hourlyCents: number;
  estimatedMinutes: number;
  includedHoursBucket?: number | null;
  standardCredits?: number | null;
  includedStandardCreditsBucket?: number | null;
  creditWeight?: number | null;
  billingScope: "none" | "relay-included" | "cloud-included-then-metered" | "external";
  resourceClass: TaskPlacementResourceClass;
  display: string;
};

export type TaskPlacementRequest = {
  kind?: TaskPlacementKind;
  sourceSurface?: string;
  projectSlug?: string;
  requestedRunner?: string;
  targetDeviceId?: string;
  forceCloud?: boolean;
  forceRelaySource?: boolean;
  appCount?: number;
  repoSizeMb?: number;
  fileCount?: number;
  hasNativeMobile?: boolean;
  hasDocker?: boolean;
};

export type ProjectProfileInput = {
  projectSlug: string;
  sourceDeviceId?: string;
  stack?: string;
  appCount?: number;
  repoSizeMb?: number;
  fileCount?: number;
  hasNativeMobile?: boolean;
  hasDocker?: boolean;
  resourceClass?: TaskPlacementResourceClass;
  confidence?: number;
};

export type TaskPlacementDecision = {
  id?: string;
  taskId?: string;
  lane: TaskPlacementLane;
  resourceClass: TaskPlacementResourceClass;
  targetDeviceId?: string | null;
  cloudMachineId?: string | null;
  subscriptionPlan?: string | null;
  entitlement: "free" | "relay-pro" | "cloud-workspace" | "owner-dev";
  status: "planned" | "queued" | "running" | "completed" | "failed" | "superseded";
  reason: string;
  wakeRequired: boolean;
  wakeTargetMs?: number | null;
  estimatedCreditCost?: number | null;
  creditEstimate?: TaskPlacementCreditEstimate | null;
  createdAt?: number;
  updatedAt?: number;
};

type PlacementStatus = "queued" | "running" | "completed" | "failed" | "superseded";

export type TaskPlacementActivation = {
  ok: boolean;
  action:
    | "none"
    | "already_active"
    | "already_in_flight"
    | "wake_scheduled"
    | "wake_failed"
    | "provision_scheduled"
    | "reconcile_scheduled"
    | "resize_required"
    | "resize_failed"
    | "yaver_auth_required"
    | "runner_auth_required"
    | "billing_required";
  lane?: string;
  status?: string;
  productId?: "cloud-workspace";
  machineId?: string;
  machineType?: string;
  currentMachineType?: string;
  wakeRunId?: string | null;
  targetDeviceId?: string;
  machineStatus?: string;
  phase?: string;
  profileMatched?: boolean;
  reason?: string;
  error?: string;
};

export function activationBlockReason(activation: TaskPlacementActivation): string | null {
  if (activation.action === "yaver_auth_required") {
    return activation.reason || "Yaver Cloud Workspace needs Yaver account authorization before this task can run.";
  }
  if (activation.action === "runner_auth_required") {
    return activation.reason || "Cloud Workspace is online, but the selected runner needs browser authorization.";
  }
  if (activation.action === "billing_required") {
    return activation.reason || "Cloud Workspace requires an active subscription from Yaver web before this task can run.";
  }
  if (activation.action === "resize_required") {
    return activation.reason || "Cloud Workspace needs a larger profile before this task can run.";
  }
  if (activation.action === "resize_failed") {
    return activation.error || activation.reason || "Cloud Workspace resize request failed.";
  }
  if (activation.action === "wake_failed") {
    return activation.error || activation.reason || "Cloud Workspace wake failed before this task could run.";
  }
  return null;
}

export type CloudWakeRun = {
  id: string;
  machineId: string;
  placementId?: string | null;
  taskId?: string | null;
  kind: "provision" | "wake" | "park";
  status: "queued" | "running" | "succeeded" | "failed" | "retrying" | "blocked" | "cancelled";
  phase?: string | null;
  progress?: number | null;
  resourceClass?: string | null;
  machineType?: string | null;
  targetDeviceId?: string | null;
  reason?: string | null;
  error?: string | null;
  provider?: string | null;
  providerResourceId?: string | null;
  providerActionId?: string | null;
  providerStatus?: string | null;
  dryRun?: boolean | null;
  startedAt: number;
  updatedAt: number;
  completedAt?: number | null;
};

export type TaskPlacementStatus = TaskPlacementDecision & {
  latestWakeRun?: CloudWakeRun | null;
};

export type TaskDispatchIntentStatus =
  | "queued"
  | "dispatching"
  | "dispatched"
  | "blocked"
  | "failed"
  | "cancelled"
  | "expired";

export type TaskDispatchIntent = {
  id: string;
  localTaskId: string;
  placementId?: string | null;
  taskId?: string | null;
  sourceSurface?: string | null;
  lane?: string | null;
  targetDeviceId?: string | null;
  cloudMachineId?: string | null;
  requestedRunner?: string | null;
  projectSlug?: string | null;
  status: TaskDispatchIntentStatus;
  blockedAction?: TaskPlacementActivation["action"] | null;
  reason?: string | null;
  lastError?: string | null;
  attempts: number;
  expiresAt: number;
  createdAt: number;
  updatedAt: number;
  completedAt?: number | null;
};

export type RelaySourceIntentStatus =
  | "queued"
  | "claimed"
  | "committed"
  | "handoff_ready"
  | "blocked"
  | "failed"
  | "cancelled"
  | "expired";

export type RelaySourceIntent = {
  id: string;
  localTaskId: string;
  taskId?: string | null;
  placementId?: string | null;
  shareId: string;
  membershipId?: string | null;
  sourceSurface?: string | null;
  projectSlug: string;
  repoUrl: string;
  baseBranch: string;
  branch: string;
  providerKind?: string | null;
  providerHost?: string | null;
  providerRepo?: string | null;
  providerBranch?: string | null;
  providerBranchUrl?: string | null;
  providerAppInstallationId?: string | null;
  providerAuthMode?: string | null;
  providerAuthStatus?: string | null;
  kind: string;
  status: RelaySourceIntentStatus;
  reason?: string | null;
  lastError?: string | null;
  relayId?: string | null;
  attempts: number;
  expiresAt: number;
  createdAt: number;
  updatedAt: number;
  completedAt?: number | null;
};

export type ProjectArtifactVisibility = "private" | "project" | "public-link";

export type ProjectArtifact = {
  id: string;
  shareId: string;
  taskId?: string | null;
  localTaskId?: string | null;
  projectSlug: string;
  kind: string;
  title: string;
  description?: string | null;
  provider: string;
  storageId?: string | null;
  objectKey?: string | null;
  url?: string | null;
  contentType?: string | null;
  sizeBytes?: number | null;
  checksum?: string | null;
  visibility: ProjectArtifactVisibility;
  shareToken?: string | null;
  shareUrlExpiresAt?: number | null;
  expiresAt?: number | null;
  status: "active" | "hidden" | "expired" | "deleted";
  createdAt: number;
  updatedAt: number;
  lastAccessedAt?: number | null;
};

export type ProjectArtifactUsageBucket = {
  activeCount: number;
  storageBytes: number;
  reservedUploadBytes: number;
  totalMeteredBytes: number;
  quotaBytes: number;
  remainingBytes: number;
  quotaPercent: number;
  overQuota: boolean;
  publicLinkCount: number;
  byKind: Record<string, number>;
  oldestCreatedAt: number | null;
  newestCreatedAt: number | null;
};

export type ProjectArtifactUsage = {
  shareId: string;
  projectSlug: string;
  project: ProjectArtifactUsageBucket;
  owner: ProjectArtifactUsageBucket;
};

export type ProjectArtifactCleanupResult = {
  ok: boolean;
  shareId: string;
  projectSlug: string;
  scanned: number;
  expired: number;
  storageDeleteAttempted: number;
  storageDeleteFailed: number;
  remainingExpired: number;
};

export type FeedbackWorkTarget = "task" | "issue" | "branch" | "triage";
export type FeedbackWorkStatus =
  | "queued"
  | "claimed"
  | "task_created"
  | "issue_draft_created"
  | "issue_created"
  | "branch_created"
  | "blocked"
  | "cancelled"
  | "rejected"
  | "expired";

export type FeedbackWorkItem = {
  id: string;
  shareId: string;
  membershipId?: string | null;
  projectSlug: string;
  sourceSurface?: string | null;
  title: string;
  body: string;
  kind: string;
  priority: string;
  component?: string | null;
  appVersion?: string | null;
  platform?: string | null;
  artifactIds: string[];
  attachmentUrls: string[];
  target: FeedbackWorkTarget;
  status: FeedbackWorkStatus;
  relaySourceIntentId?: string | null;
  taskId?: string | null;
  issueUrl?: string | null;
  branch?: string | null;
  reason?: string | null;
  lastError?: string | null;
  workerId?: string | null;
  attempts: number;
  expiresAt: number;
  createdAt: number;
  updatedAt: number;
  completedAt?: number | null;
};

async function placementFetch<T>(token: string, path: string, init: RequestInit = {}): Promise<T> {
  const res = await fetch(`${CONVEX_URL}${path}`, {
    ...init,
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
      Authorization: `Bearer ${token}`,
      ...(init.headers || {}),
    },
  });
  const text = await res.text();
  const data = text ? JSON.parse(text) : {};
  if (!res.ok) {
    throw new Error(data?.error || `Task placement request failed (${res.status})`);
  }
  return data as T;
}

export function placementLaneLabel(lane?: string | null): string | null {
  switch (lane) {
    case "phone_sandbox":
      return "Phone sandbox";
    case "relay_source":
      return "Relay source";
    case "owned_machine":
      return "Your machine";
    case "cloud_standard":
      return "Cloud workspace";
    case "cloud_heavy":
      return "Heavy workspace";
    case "cloud_build":
      return "Heavy build";
    case "external_deploy":
      return "Deploy target";
    case "manual":
      return "Manual routing";
    default:
      return null;
  }
}

export function placementCreditLabel(
  decision?: Pick<TaskPlacementDecision, "creditEstimate" | "estimatedCreditCost"> | null,
): string | null {
  if (decision?.creditEstimate?.display) return decision.creditEstimate.display;
  if (typeof decision?.estimatedCreditCost === "number" && decision.estimatedCreditCost > 0) {
    return `~$${(decision.estimatedCreditCost / 100).toFixed(2)}`;
  }
  return null;
}

export function shouldDeferTaskForCloudWorkspace(decision?: TaskPlacementDecision | null): boolean {
  if (!decision?.lane?.startsWith("cloud_")) return false;
  return decision.wakeRequired === true || decision.status === "queued" || !decision.targetDeviceId;
}

export function shouldConfirmExpensiveCloudPlacement(decision?: TaskPlacementDecision | null): boolean {
  if (!decision?.lane?.startsWith("cloud_")) return false;
  return (
    decision.lane === "cloud_heavy" ||
    decision.lane === "cloud_build" ||
    decision.resourceClass === "heavy" ||
    decision.resourceClass === "build"
  );
}

export function expensiveCloudPlacementMessage(decision?: TaskPlacementDecision | null): string {
  const label =
    decision?.lane === "cloud_build" || decision?.resourceClass === "build"
      ? "Heavy Build"
      : "Heavy Workspace";
  const estimate = decision?.creditEstimate?.display || placementCreditLabel(decision);
  return [
    `This is a ${label}.`,
    "It may use more of your included Cloud Workspace allowance or require Boost if you run many of these this month.",
    estimate ? `Estimate: ${estimate}` : "",
    "Continue?",
  ].filter(Boolean).join("\n\n");
}

export function pendingPlacementTaskId(): string {
  const suffix =
    typeof crypto !== "undefined" && typeof crypto.randomUUID === "function"
      ? crypto.randomUUID()
      : `${Date.now()}-${Math.random().toString(36).slice(2)}`;
  return `pending-cloud:${suffix}`;
}

export function taskPlacementRequestBody(
  req: TaskPlacementRequest & { taskId?: string },
): TaskPlacementRequest & { taskId?: string } {
  return {
    taskId: req.taskId,
    kind: req.kind ?? "unknown",
    sourceSurface: req.sourceSurface ?? "web",
    projectSlug: req.projectSlug,
    requestedRunner: req.requestedRunner,
    targetDeviceId: req.targetDeviceId,
    forceCloud: req.forceCloud,
    forceRelaySource: req.forceRelaySource,
    appCount: req.appCount,
    repoSizeMb: req.repoSizeMb,
    fileCount: req.fileCount,
    hasNativeMobile: req.hasNativeMobile,
    hasDocker: req.hasDocker,
  };
}

export async function previewTaskPlacement(token: string, req: TaskPlacementRequest): Promise<TaskPlacementDecision> {
  return placementFetch<TaskPlacementDecision>(token, "/tasks/placement/preview", {
    method: "POST",
    body: JSON.stringify(taskPlacementRequestBody(req)),
  });
}

export async function recordTaskPlacement(
  token: string,
  req: TaskPlacementRequest & { taskId: string },
): Promise<TaskPlacementDecision> {
  return placementFetch<TaskPlacementDecision>(token, "/tasks/placement/record", {
    method: "POST",
    body: JSON.stringify(taskPlacementRequestBody(req)),
  });
}

export async function listRecentTaskPlacements(
  token: string,
  opts: { projectSlug?: string; limit?: number } = {},
): Promise<TaskPlacementDecision[]> {
  const params = new URLSearchParams();
  if (opts.projectSlug) params.set("projectSlug", opts.projectSlug);
  if (opts.limit) params.set("limit", String(opts.limit));
  const suffix = params.toString() ? `?${params.toString()}` : "";
  return placementFetch<TaskPlacementDecision[]>(token, `/tasks/placement/recent${suffix}`, { method: "GET" });
}

export async function markTaskPlacementStatus(
  token: string,
  placementId: string,
  status: PlacementStatus,
): Promise<{ ok: boolean }> {
  return placementFetch<{ ok: boolean }>(token, "/tasks/placement/status", {
    method: "POST",
    body: JSON.stringify({ placementId, status }),
  });
}

export async function getTaskPlacementStatus(
  token: string,
  opts: { placementId?: string; taskId?: string },
): Promise<TaskPlacementStatus> {
  const params = new URLSearchParams();
  if (opts.placementId) params.set("placementId", opts.placementId);
  if (opts.taskId) params.set("taskId", opts.taskId);
  return placementFetch<TaskPlacementStatus>(token, `/tasks/placement/status?${params.toString()}`, {
    method: "GET",
  });
}

export async function rebindTaskPlacement(
  token: string,
  placementId: string,
  taskId: string,
  status?: PlacementStatus,
): Promise<{ ok: boolean }> {
  return placementFetch<{ ok: boolean }>(token, "/tasks/placement/rebind", {
    method: "POST",
    body: JSON.stringify({ placementId, taskId, status }),
  });
}

export async function activateTaskPlacement(
  token: string,
  req: { placementId?: string; taskId?: string },
): Promise<TaskPlacementActivation> {
  return placementFetch<TaskPlacementActivation>(token, "/tasks/placement/activate", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export async function upsertProjectProfile(
  token: string,
  req: ProjectProfileInput,
): Promise<{ id: string; resourceClass: TaskPlacementResourceClass }> {
  return placementFetch<{ id: string; resourceClass: TaskPlacementResourceClass }>(token, "/tasks/project-profile", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export async function listRecentWakeRuns(
  token: string,
  opts: { limit?: number } = {},
): Promise<CloudWakeRun[]> {
  const params = new URLSearchParams();
  if (opts.limit) params.set("limit", String(opts.limit));
  const suffix = params.toString() ? `?${params.toString()}` : "";
  return placementFetch<CloudWakeRun[]>(token, `/cloud/wake-runs/recent${suffix}`, { method: "GET" });
}

export type CreateTaskDispatchIntentRequest = {
  localTaskId: string;
  placementId?: string;
  sourceSurface?: string;
  lane?: string;
  targetDeviceId?: string | null;
  cloudMachineId?: string | null;
  requestedRunner?: string;
  projectSlug?: string;
  reason?: string;
  ttlMs?: number;
};

export type UpdateTaskDispatchIntentRequest = {
  intentId?: string;
  localTaskId?: string;
  status: TaskDispatchIntentStatus;
  taskId?: string;
  targetDeviceId?: string;
  lastError?: string;
  reason?: string;
  blockedAction?: TaskPlacementActivation["action"];
  clearBlockedAction?: boolean;
  bumpAttempt?: boolean;
};

export function taskDispatchIntentCreateBody(req: CreateTaskDispatchIntentRequest): CreateTaskDispatchIntentRequest {
  return {
    localTaskId: req.localTaskId,
    placementId: req.placementId,
    sourceSurface: req.sourceSurface,
    lane: req.lane,
    targetDeviceId: req.targetDeviceId,
    cloudMachineId: req.cloudMachineId,
    requestedRunner: req.requestedRunner,
    projectSlug: req.projectSlug,
    reason: req.reason,
    ttlMs: req.ttlMs,
  };
}

export function taskDispatchIntentUpdateBody(req: UpdateTaskDispatchIntentRequest): UpdateTaskDispatchIntentRequest {
  return {
    intentId: req.intentId,
    localTaskId: req.localTaskId,
    status: req.status,
    taskId: req.taskId,
    targetDeviceId: req.targetDeviceId,
    lastError: req.lastError,
    reason: req.reason,
    blockedAction: req.blockedAction,
    clearBlockedAction: req.clearBlockedAction,
    bumpAttempt: req.bumpAttempt,
  };
}

export async function createTaskDispatchIntent(
  token: string,
  req: CreateTaskDispatchIntentRequest,
): Promise<TaskDispatchIntent> {
  return placementFetch<TaskDispatchIntent>(token, "/tasks/dispatch-intents", {
    method: "POST",
    body: JSON.stringify(taskDispatchIntentCreateBody(req)),
  });
}

export async function updateTaskDispatchIntent(
  token: string,
  req: UpdateTaskDispatchIntentRequest,
): Promise<TaskDispatchIntent> {
  return placementFetch<TaskDispatchIntent>(token, "/tasks/dispatch-intents/status", {
    method: "POST",
    body: JSON.stringify(taskDispatchIntentUpdateBody(req)),
  });
}

export async function listTaskDispatchIntents(
  token: string,
  opts: { limit?: number; includeTerminal?: boolean } = {},
): Promise<TaskDispatchIntent[]> {
  const params = new URLSearchParams();
  if (opts.limit) params.set("limit", String(opts.limit));
  if (opts.includeTerminal) params.set("includeTerminal", "1");
  const suffix = params.toString() ? `?${params.toString()}` : "";
  return placementFetch<TaskDispatchIntent[]>(token, `/tasks/dispatch-intents${suffix}`, { method: "GET" });
}

export async function createRelaySourceIntent(
  token: string,
  req: {
    localTaskId: string;
    placementId?: string;
    shareId?: string;
    projectSlug?: string;
    sourceSurface?: string;
    kind?: string;
    branch?: string;
    reason?: string;
    ttlMs?: number;
  },
): Promise<RelaySourceIntent> {
  return placementFetch<RelaySourceIntent>(token, "/tasks/relay-source-intents", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export async function updateRelaySourceIntent(
  token: string,
  req: {
    intentId?: string;
    localTaskId?: string;
    status: RelaySourceIntentStatus;
    taskId?: string;
    relayId?: string;
    reason?: string;
    lastError?: string;
    bumpAttempt?: boolean;
  },
): Promise<RelaySourceIntent> {
  return placementFetch<RelaySourceIntent>(token, "/tasks/relay-source-intents/status", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export async function claimRelaySourceIntent(
  token: string,
  req: { projectSlug?: string; relayId?: string } = {},
): Promise<RelaySourceIntent | { ok: true; intent: null }> {
  return placementFetch<RelaySourceIntent | { ok: true; intent: null }>(token, "/tasks/relay-source-intents/claim", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export async function listRelaySourceIntents(
  token: string,
  opts: { projectSlug?: string; limit?: number; includeTerminal?: boolean; scope?: "mine" | "owned" | "all" } = {},
): Promise<RelaySourceIntent[]> {
  const params = new URLSearchParams();
  if (opts.projectSlug) params.set("projectSlug", opts.projectSlug);
  if (opts.limit) params.set("limit", String(opts.limit));
  if (opts.includeTerminal) params.set("includeTerminal", "1");
  if (opts.scope) params.set("scope", opts.scope);
  const suffix = params.toString() ? `?${params.toString()}` : "";
  return placementFetch<RelaySourceIntent[]>(token, `/tasks/relay-source-intents${suffix}`, { method: "GET" });
}

export async function createProjectArtifact(
  token: string,
  req: {
    shareId?: string;
    projectSlug?: string;
    taskId?: string;
    localTaskId?: string;
    kind?: string;
    title: string;
    description?: string;
    provider?: string;
    storageId?: string;
    uploadIntentId?: string;
    objectKey?: string;
    url?: string;
    contentType?: string;
    sizeBytes?: number;
    checksum?: string;
    visibility?: ProjectArtifactVisibility;
    shareTtlMs?: number;
    expiresAt?: number;
  },
): Promise<ProjectArtifact> {
  return placementFetch<ProjectArtifact>(token, "/project-artifacts", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export async function createProjectArtifactUploadUrl(
  token: string,
  req: { shareId?: string; projectSlug?: string; sizeBytes: number },
): Promise<{ uploadUrl: string; uploadIntentId: string; sizeBytes: number }> {
  return placementFetch<{ uploadUrl: string; uploadIntentId: string; sizeBytes: number }>(token, "/project-artifacts/upload-url", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export async function listProjectArtifacts(
  token: string,
  opts: { shareId?: string; projectSlug?: string; kind?: string; limit?: number } = {},
): Promise<ProjectArtifact[]> {
  const params = new URLSearchParams();
  if (opts.shareId) params.set("shareId", opts.shareId);
  if (opts.projectSlug) params.set("projectSlug", opts.projectSlug);
  if (opts.kind) params.set("kind", opts.kind);
  if (opts.limit) params.set("limit", String(opts.limit));
  const suffix = params.toString() ? `?${params.toString()}` : "";
  return placementFetch<ProjectArtifact[]>(token, `/project-artifacts${suffix}`, { method: "GET" });
}

export async function getProjectArtifactUsage(
  token: string,
  opts: { shareId?: string; projectSlug?: string } = {},
): Promise<ProjectArtifactUsage> {
  const params = new URLSearchParams();
  if (opts.shareId) params.set("shareId", opts.shareId);
  if (opts.projectSlug) params.set("projectSlug", opts.projectSlug);
  const suffix = params.toString() ? `?${params.toString()}` : "";
  return placementFetch<ProjectArtifactUsage>(token, `/project-artifacts/usage${suffix}`, { method: "GET" });
}

export async function cleanupExpiredProjectArtifacts(
  token: string,
  req: { shareId?: string; projectSlug?: string; limit?: number; deleteStorage?: boolean },
): Promise<ProjectArtifactCleanupResult> {
  return placementFetch<ProjectArtifactCleanupResult>(token, "/project-artifacts/cleanup", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export async function hideProjectArtifact(token: string, artifactId: string): Promise<ProjectArtifact> {
  return placementFetch<ProjectArtifact>(token, "/project-artifacts/hide", {
    method: "POST",
    body: JSON.stringify({ artifactId }),
  });
}

export async function getPublicProjectArtifact(shareToken: string): Promise<ProjectArtifact> {
  const params = new URLSearchParams({ token: shareToken });
  const res = await fetch(`${CONVEX_URL}/project-artifacts/public?${params.toString()}`, {
    headers: { Accept: "application/json" },
  });
  const text = await res.text();
  const data = text ? JSON.parse(text) : {};
  if (!res.ok) throw new Error(data?.error || `Project artifact request failed (${res.status})`);
  return data as ProjectArtifact;
}

export async function createFeedbackWorkItem(
  sdkToken: string,
  req: {
    shareId?: string;
    projectSlug?: string;
    title: string;
    body: string;
    kind?: string;
    priority?: "low" | "normal" | "high";
    component?: string;
    appVersion?: string;
    platform?: string;
    artifactIds?: string[];
    attachmentUrls?: string[];
    target?: FeedbackWorkTarget;
    ttlMs?: number;
  },
): Promise<FeedbackWorkItem> {
  const res = await fetch(`${CONVEX_URL}/feedback-work-items`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${sdkToken}`,
      "Content-Type": "application/json",
      Accept: "application/json",
    },
    body: JSON.stringify(req),
  });
  const text = await res.text();
  const data = text ? JSON.parse(text) : {};
  if (!res.ok) throw new Error(data?.error || `Feedback work request failed (${res.status})`);
  return data as FeedbackWorkItem;
}

export async function listFeedbackWorkItems(
  token: string,
  opts: { shareId?: string; projectSlug?: string; scope?: "owned" | "mine"; status?: FeedbackWorkStatus; limit?: number } = {},
): Promise<FeedbackWorkItem[]> {
  const params = new URLSearchParams();
  if (opts.shareId) params.set("shareId", opts.shareId);
  if (opts.projectSlug) params.set("projectSlug", opts.projectSlug);
  if (opts.scope) params.set("scope", opts.scope);
  if (opts.status) params.set("status", opts.status);
  if (opts.limit) params.set("limit", String(opts.limit));
  const suffix = params.toString() ? `?${params.toString()}` : "";
  return placementFetch<FeedbackWorkItem[]>(token, `/feedback-work-items${suffix}`, { method: "GET" });
}

export async function claimFeedbackWorkItem(
  token: string,
  req: { shareId?: string; projectSlug?: string; workerId?: string } = {},
): Promise<FeedbackWorkItem | { ok: true; item: null }> {
  return placementFetch<FeedbackWorkItem | { ok: true; item: null }>(token, "/feedback-work-items/claim", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export async function updateFeedbackWorkItemStatus(
  token: string,
  req: {
    itemId: string;
    status: FeedbackWorkStatus;
    taskId?: string;
    issueUrl?: string;
    branch?: string;
    reason?: string;
    lastError?: string;
    workerId?: string;
  },
): Promise<FeedbackWorkItem> {
  return placementFetch<FeedbackWorkItem>(token, "/feedback-work-items/status", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export async function routeFeedbackWorkItem(
  token: string,
  req: {
    itemId: string;
    target: FeedbackWorkTarget;
    reason?: string;
    workerId?: string;
  },
): Promise<FeedbackWorkItem> {
  return placementFetch<FeedbackWorkItem>(token, "/feedback-work-items/route", {
    method: "POST",
    body: JSON.stringify(req),
  });
}

export async function queueFeedbackWorkItemRelaySource(
  token: string,
  req: { itemId: string; branch?: string; workerId?: string; ttlMs?: number },
): Promise<{ item: FeedbackWorkItem; relaySourceIntent: RelaySourceIntent }> {
  return placementFetch<{ item: FeedbackWorkItem; relaySourceIntent: RelaySourceIntent }>(
    token,
    "/feedback-work-items/queue-relay-source",
    {
      method: "POST",
      body: JSON.stringify(req),
    },
  );
}
