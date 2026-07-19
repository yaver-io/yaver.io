export type TaskDispatchIntentStatus =
  | "queued"
  | "dispatching"
  | "dispatched"
  | "blocked"
  | "failed"
  | "cancelled"
  | "expired";

export type TaskPlacementActivationAction =
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
  blockedAction?: TaskPlacementActivationAction;
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
