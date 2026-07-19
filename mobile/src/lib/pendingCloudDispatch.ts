import AsyncStorage from "@react-native-async-storage/async-storage";
import type { CloudWorkspaceRequiredError } from "./cloudWorkspaceRequired";
import type { ImageAttachment, SpeechContextInput, Task } from "./quic";
import type {
  CreateTaskDispatchIntentRequest,
  TaskDispatchIntent,
  TaskPlacementActivation,
  TaskPlacementStatus,
} from "./taskPlacement";
import { pendingCloudDispatchTaskStatus, placementCreditLabel } from "./taskPlacementCore";

const STORAGE_KEY = "@yaver/pending_cloud_dispatch/v1";
const DEFAULT_PENDING_TTL_MS = 24 * 60 * 60_000;

export type PendingCloudTaskParams = {
  title: string;
  description: string;
  model?: string;
  runner?: string;
  customCommand?: string;
  speechContext?: SpeechContextInput;
  images?: ImageAttachment[];
  workDir?: string;
  mode?: string;
  video?: { enabled?: boolean; source?: "browser" | "sim-ios" | "sim-android" | "phone" };
  codeMode?: boolean;
  allowLocalFallback?: boolean;
};

export type PendingCloudDispatch = {
  localTaskId: string;
  placementId?: string;
  placementLane?: string;
  placementReason?: string;
  placementCreditLabel?: string;
  wakePhase?: string | null;
  wakeProgress?: number | null;
  targetDeviceId?: string | null;
  dispatchIntentId?: string;
  dispatchStatus?: string;
  dispatchExpiresAt?: number;
  blockedAction?: TaskPlacementActivation["action"];
  blockedReason?: string;
  clearedBlockedAction?: boolean;
  params: PendingCloudTaskParams;
  createdAt: number;
  updatedAt: number;
  attempts: number;
  lastError?: string;
};

export type CloudWorkspaceRequiredDispatchArgs = {
  err: CloudWorkspaceRequiredError;
  params: PendingCloudTaskParams;
  sourceSurface?: string;
  requestedRunner?: string;
  projectSlug?: string;
  createDispatchIntent?: (req: CreateTaskDispatchIntentRequest) => Promise<TaskDispatchIntent>;
};

export function cloudWorkspaceRequiredBlockedAction(
  action?: string | null,
): PendingCloudDispatch["blockedAction"] | undefined {
  switch (action) {
    case "runner_auth_required":
    case "yaver_auth_required":
    case "billing_required":
    case "resize_required":
    case "resize_failed":
    case "wake_failed":
      return action;
    default:
      return undefined;
  }
}

function normalizePendingRow(row: PendingCloudDispatch, now = Date.now()): PendingCloudDispatch {
  const dispatchExpiresAt = typeof row.dispatchExpiresAt === "number"
    ? row.dispatchExpiresAt
    : (row.createdAt || now) + DEFAULT_PENDING_TTL_MS;
  const expired =
    dispatchExpiresAt <= now &&
    row.dispatchStatus !== "dispatched" &&
    row.dispatchStatus !== "cancelled" &&
    row.dispatchStatus !== "failed";
  return {
    ...row,
    dispatchExpiresAt,
    dispatchStatus: expired ? "expired" : row.dispatchStatus,
    lastError: expired && !row.lastError ? "Local Cloud Workspace dispatch window expired." : row.lastError,
  };
}

async function readAll(): Promise<PendingCloudDispatch[]> {
  try {
    const raw = await AsyncStorage.getItem(STORAGE_KEY);
    const parsed = raw ? JSON.parse(raw) : [];
    return Array.isArray(parsed)
      ? parsed
          .filter((row) => row?.localTaskId && row?.params?.title)
          .map((row) => normalizePendingRow(row))
      : [];
  } catch {
    return [];
  }
}

async function writeAll(rows: PendingCloudDispatch[]): Promise<void> {
  try {
    await AsyncStorage.setItem(STORAGE_KEY, JSON.stringify(rows.slice(-50)));
  } catch {
    /* non-fatal: current UI placeholder still exists */
  }
}

export async function listPendingCloudDispatches(): Promise<PendingCloudDispatch[]> {
  return readAll();
}

export async function savePendingCloudDispatch(row: PendingCloudDispatch): Promise<void> {
  const rows = (await readAll()).filter((existing) => existing.localTaskId !== row.localTaskId);
  rows.push(normalizePendingRow({ ...row, updatedAt: Date.now() }));
  await writeAll(rows);
}

export async function updatePendingCloudDispatch(
  localTaskId: string,
  patch: Partial<PendingCloudDispatch>,
): Promise<void> {
  const rows = (await readAll()).map((row) =>
    row.localTaskId === localTaskId ? mergePendingCloudDispatchRow(row, patch) : row,
  );
  await writeAll(rows);
}

export async function saveCloudWorkspaceRequiredDispatch(
  args: CloudWorkspaceRequiredDispatchArgs,
  now = Date.now(),
): Promise<PendingCloudDispatch> {
  const activation = args.err.activation;
  const blockedAction = cloudWorkspaceRequiredBlockedAction(activation?.action);
  const row: PendingCloudDispatch = {
    localTaskId: args.err.pendingTaskId,
    placementId: args.err.placement?.id,
    placementLane: args.err.placement?.lane,
    placementReason: activation?.reason || args.err.reason || args.err.placement?.reason,
    placementCreditLabel: placementCreditLabel(args.err.placement as any) ?? undefined,
    targetDeviceId: args.err.placement?.targetDeviceId || activation?.targetDeviceId || null,
    dispatchStatus: blockedAction ? "blocked" : "queued",
    blockedAction,
    blockedReason: blockedAction ? activation?.reason || activation?.error || args.err.reason : undefined,
    params: args.params,
    createdAt: now,
    updatedAt: now,
    attempts: 0,
    lastError: activation?.error,
  };
  await savePendingCloudDispatch(row);
  try {
    const createIntent =
      args.createDispatchIntent ??
      (await import("./taskPlacement")).createTaskDispatchIntent;
    const intent = await createIntent({
      localTaskId: row.localTaskId,
      placementId: row.placementId,
      sourceSurface: args.sourceSurface || "mobile",
      lane: row.placementLane,
      targetDeviceId: row.targetDeviceId,
      cloudMachineId: args.err.placement?.cloudMachineId,
      requestedRunner: args.requestedRunner || row.params.runner,
      projectSlug: args.projectSlug,
      reason: row.placementReason,
    });
    const merged: PendingCloudDispatch = {
      ...row,
      dispatchIntentId: intent.id,
      dispatchStatus: blockedAction ? "blocked" : intent.status || row.dispatchStatus,
      dispatchExpiresAt: intent.expiresAt,
      attempts: intent.attempts ?? row.attempts,
    };
    await savePendingCloudDispatch(merged);
    return merged;
  } catch {
    return row;
  }
}

export function pendingCloudDispatchNeedsUserAction(row: PendingCloudDispatch): boolean {
  if (row.dispatchStatus !== "blocked") return false;
  switch (row.blockedAction) {
    case "runner_auth_required":
    case "yaver_auth_required":
    case "billing_required":
    case "resize_required":
    case "resize_failed":
    case "wake_failed":
      return true;
    default:
      return false;
  }
}

function mergePendingCloudDispatchRow(
  row: PendingCloudDispatch,
  patch: Partial<PendingCloudDispatch>,
): PendingCloudDispatch {
  const next = { ...row, ...patch, updatedAt: Date.now() };
  if (pendingCloudDispatchNeedsUserAction(row) && patch.dispatchStatus === "queued") {
    next.dispatchStatus = row.dispatchStatus;
    next.blockedAction = row.blockedAction;
    next.blockedReason = row.blockedReason;
    next.lastError = row.lastError;
  }
  return normalizePendingRow(next);
}

export async function mergePendingCloudDispatchIntents(
  intents: TaskDispatchIntent[],
): Promise<PendingCloudDispatch[]> {
  const byLocalTaskId = new Map<string, TaskDispatchIntent>();
  for (const intent of intents) {
    if (intent?.localTaskId) byLocalTaskId.set(intent.localTaskId, intent);
  }
  const rows = await readAll();
  let changed = false;
  const merged = rows.map((row) => {
    const intent = byLocalTaskId.get(row.localTaskId);
    if (!intent) return row;
    const intentStatus = pendingCloudDispatchNeedsUserAction(row) && intent.status === "queued"
      ? row.dispatchStatus
      : intent.status || row.dispatchStatus;
    const next: PendingCloudDispatch = {
      ...row,
      dispatchIntentId: intent.id || row.dispatchIntentId,
      dispatchStatus: intentStatus,
      dispatchExpiresAt: intent.expiresAt || row.dispatchExpiresAt,
      targetDeviceId: intent.targetDeviceId ?? row.targetDeviceId,
      attempts: Number.isFinite(intent.attempts) ? intent.attempts : row.attempts,
      lastError: intentStatus === row.dispatchStatus && pendingCloudDispatchNeedsUserAction(row)
        ? row.lastError
        : intent.lastError || row.lastError,
      blockedAction: intent.status === "blocked"
        ? intent.blockedAction || row.blockedAction
        : row.blockedAction,
      blockedReason: intent.status === "blocked" ? intent.reason || row.blockedReason : row.blockedReason,
      updatedAt: intent.updatedAt || Date.now(),
    };
    if (intent.lane) next.placementLane = intent.lane;
    if (intent.placementId) next.placementId = intent.placementId;
    if (intent.reason && !next.placementReason) next.placementReason = intent.reason;
    const normalized = normalizePendingRow(next);
    changed = changed || JSON.stringify(normalized) !== JSON.stringify(row);
    return normalized;
  });
  if (changed) await writeAll(merged);
  return merged;
}

function wakeProgressMessage(status: TaskPlacementStatus): string | undefined {
  const run = status.latestWakeRun;
  if (!run) return status.reason || undefined;
  if (run.error) return run.error;
  if (run.reason) return run.reason;
  const phase = String(run.phase || "").trim();
  if (!phase) return status.reason || undefined;
  const progress = typeof run.progress === "number" && Number.isFinite(run.progress)
    ? ` (${Math.max(0, Math.min(100, Math.round(run.progress)))}%)`
    : "";
  return `Cloud Workspace wake: ${phase}${progress}`;
}

function blockerActionForWake(status: TaskPlacementStatus): TaskPlacementActivation["action"] | undefined {
  const run = status.latestWakeRun;
  const phase = String(run?.phase || "").trim();
  if (phase === "awaiting-yaver-auth") return "yaver_auth_required";
  if (phase === "authorizing-runners") return "runner_auth_required";
  if (phase === "resize-required") return "resize_required";
  if (run?.status === "failed") return "wake_failed";
  return undefined;
}

function placementStatusClearsUserActionBlock(status: TaskPlacementStatus): boolean {
  return status.status === "running" || status.latestWakeRun?.status === "succeeded";
}

export function mergePendingCloudPlacementStatus(
  row: PendingCloudDispatch,
  status: TaskPlacementStatus,
): PendingCloudDispatch {
  if (!status?.id || (row.placementId && status.id !== row.placementId)) return normalizePendingRow(row);
  const run = status.latestWakeRun;
  const blockedAction = blockerActionForWake(status);
  const clearUserActionBlock = pendingCloudDispatchNeedsUserAction(row) &&
    !blockedAction &&
    placementStatusClearsUserActionBlock(status);
  return normalizePendingRow({
    ...row,
    placementId: status.id || row.placementId,
    placementLane: status.lane || row.placementLane,
    placementReason: wakeProgressMessage(status) || row.placementReason,
    placementCreditLabel: status.creditEstimate?.display || row.placementCreditLabel,
    targetDeviceId: status.targetDeviceId ?? run?.targetDeviceId ?? row.targetDeviceId,
    dispatchStatus: blockedAction ? "blocked" : clearUserActionBlock ? "queued" : row.dispatchStatus,
    blockedAction: blockedAction || (clearUserActionBlock ? undefined : row.blockedAction),
    blockedReason: blockedAction ? wakeProgressMessage(status) || row.blockedReason : clearUserActionBlock ? undefined : row.blockedReason,
    clearedBlockedAction: clearUserActionBlock ? true : row.clearedBlockedAction,
    wakePhase: run?.phase ?? row.wakePhase,
    wakeProgress: run?.progress ?? row.wakeProgress,
    lastError: run?.error || (clearUserActionBlock ? undefined : row.lastError),
    updatedAt: Math.max(row.updatedAt || 0, status.updatedAt || 0, run?.updatedAt || 0, Date.now()),
  });
}

export async function removePendingCloudDispatch(localTaskId: string): Promise<void> {
  await writeAll((await readAll()).filter((row) => row.localTaskId !== localTaskId));
}

export function pendingCloudTaskPlaceholder(row: PendingCloudDispatch): Task {
  const normalized = normalizePendingRow(row);
  return {
    id: normalized.localTaskId,
    title: normalized.params.title,
    description: "",
    status: pendingCloudDispatchTaskStatus(normalized.dispatchStatus),
    output: [
      "Cloud Workspace is waking. Yaver has not sent this prompt to the currently connected machine.",
      normalized.targetDeviceId
        ? "Yaver will dispatch it from this phone after the target workspace connects."
        : "Yaver will dispatch it from this phone after the workspace is assigned and connects.",
      normalized.placementCreditLabel ? `Estimate: ${normalized.placementCreditLabel}` : "",
      normalized.wakePhase ? `Wake phase: ${normalized.wakePhase}${typeof normalized.wakeProgress === "number" ? ` (${Math.round(normalized.wakeProgress)}%)` : ""}` : "",
      normalized.dispatchStatus ? `Dispatch status: ${normalized.dispatchStatus}` : "",
      pendingCloudDispatchNeedsUserAction(normalized) ? "Needs your action before Yaver can dispatch this task." : "",
      normalized.blockedReason ? `Blocked: ${normalized.blockedReason}` : "",
      normalized.lastError ? `Last dispatch attempt: ${normalized.lastError}` : "",
    ].filter(Boolean),
    runnerId: normalized.params.runner,
    model: normalized.params.model,
    createdAt: normalized.createdAt,
    updatedAt: normalized.updatedAt,
    placementId: normalized.placementId,
    placementLane: normalized.placementLane,
    placementReason: normalized.placementReason,
    placementCreditLabel: normalized.placementCreditLabel,
  };
}
