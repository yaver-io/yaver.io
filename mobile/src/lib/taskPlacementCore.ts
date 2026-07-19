export type PlacementHelperDecision = {
  lane?: string | null;
  resourceClass?: string | null;
  status?: string | null;
  wakeRequired?: boolean | null;
  targetDeviceId?: string | null;
  creditEstimate?: { display?: string | null } | null;
  estimatedCreditCost?: number | null;
};

export type PlacementActivationHelper = {
  action?: string | null;
  reason?: string | null;
  error?: string | null;
};

export type TaskPlacementRequestBodyInput = {
  taskId?: string;
  kind?: "vibe" | "build" | "deploy" | "test" | "source" | "autorun" | "unknown";
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

export function planIncludesYaverArtifactStorage(plan?: string | null): boolean {
  const value = String(plan || "").trim();
  return value === "cloud-workspace" || value === "cloud-agent" || value.startsWith("yaver-cloud");
}

export function mobileManagedArtifactStorageDeniedReason(req: {
  provider?: string;
  storageId?: string;
  uploadIntentId?: string;
  confirmedCloudWorkspaceStorage?: boolean;
}): string | null {
  const provider = String(req.provider || "").trim().toLowerCase();
  const usesYaverStorage =
    provider === "convex" ||
    provider === "yaver-storage" ||
    Boolean(req.storageId) ||
    Boolean(req.uploadIntentId);
  if (!usesYaverStorage) return null;
  if (req.confirmedCloudWorkspaceStorage === true) return null;
  return "Yaver artifact storage requires Cloud Workspace on web. Save an external HTTPS artifact link from mobile.";
}

export function taskPlacementRequestBody(
  req: TaskPlacementRequestBodyInput,
  defaultSourceSurface = "mobile",
): TaskPlacementRequestBodyInput {
  return {
    taskId: req.taskId,
    kind: req.kind ?? "unknown",
    sourceSurface: req.sourceSurface ?? defaultSourceSurface,
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

export function activationBlockReason(activation: PlacementActivationHelper): string | null {
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

export function pendingCloudDispatchTaskStatus(
  dispatchStatus?: string | null,
): "queued" | "failed" | "stopped" {
  if (dispatchStatus === "failed") return "failed";
  if (dispatchStatus === "cancelled" || dispatchStatus === "expired") return "stopped";
  return "queued";
}

export function shouldDeferTaskForCloudWorkspace(decision?: PlacementHelperDecision | null): boolean {
  if (!decision?.lane?.startsWith("cloud_")) return false;
  return decision.wakeRequired === true || decision.status === "queued" || !decision.targetDeviceId;
}

export function shouldConfirmExpensiveCloudPlacement(decision?: PlacementHelperDecision | null): boolean {
  if (!decision?.lane?.startsWith("cloud_")) return false;
  return (
    decision.lane === "cloud_heavy" ||
    decision.lane === "cloud_build" ||
    decision.resourceClass === "heavy" ||
    decision.resourceClass === "build"
  );
}

export function placementCreditLabel(decision?: PlacementHelperDecision | null): string | null {
  if (decision?.creditEstimate?.display) return decision.creditEstimate.display;
  if (typeof decision?.estimatedCreditCost === "number" && decision.estimatedCreditCost > 0) {
    return `~$${(decision.estimatedCreditCost / 100).toFixed(2)}`;
  }
  return null;
}

export function expensiveCloudPlacementMessage(decision?: PlacementHelperDecision | null): string {
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
