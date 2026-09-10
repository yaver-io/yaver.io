import type { Task, TaskStatus } from "./agent-client";

export interface AgentTaskLifecycle {
  taskId: string;
  yaverSessionId?: string;
  status: TaskStatus;
  hostKind?: "terminal_tmux" | "desktop_gui" | "runner_process";
  updatedAt: number;
}

export interface AgentTaskSnapshot {
  deviceId: string;
  deviceName: string;
  deviceOnline: boolean;
  deviceLastHeartbeat: number;
  observedAt: number;
  tasks: AgentTaskLifecycle[];
  deletedTasks?: Array<{ taskId: string; deletedAt: number }>;
}

export const TASK_SNAPSHOT_FRESH_MS = 3 * 60 * 60 * 1000;
const TASK_DELETION_OUTBOX = "yaver.task_deletion_outbox.v1";

type PendingDeletion = { deviceId: string; taskId: string; deletedAt: number };
function deletionOutbox(): PendingDeletion[] {
  if (typeof window === "undefined") return [];
  try {
    const rows = JSON.parse(localStorage.getItem(TASK_DELETION_OUTBOX) || "[]");
    return Array.isArray(rows) ? rows.filter((row) => row?.deviceId && row?.taskId) : [];
  } catch { return []; }
}
function saveDeletionOutbox(rows: PendingDeletion[]) {
  if (typeof window !== "undefined") localStorage.setItem(TASK_DELETION_OUTBOX, JSON.stringify(rows.slice(-1000)));
}

async function postTaskTombstone(convexUrl: string, token: string, deviceId: string, taskId: string): Promise<void> {
  const response = await fetch(`${convexUrl}/task-tombstones`, {
    method: "POST",
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
    body: JSON.stringify({ deviceId, taskId }),
  });
  const payload = await response.json().catch(() => undefined);
  if (!response.ok) throw new Error(payload?.error || `Could not synchronize task removal (${response.status})`);
}

async function flushTaskDeletionOutbox(convexUrl: string, token: string) {
  for (const row of deletionOutbox()) {
    try {
      await postTaskTombstone(convexUrl, token, row.deviceId, row.taskId);
      saveDeletionOutbox(deletionOutbox().filter((candidate) => !(candidate.deviceId === row.deviceId && candidate.taskId === row.taskId)));
    } catch { return; }
  }
}

export async function listAgentTaskSnapshots(convexUrl: string, token: string): Promise<AgentTaskSnapshot[]> {
  await flushTaskDeletionOutbox(convexUrl, token);
  const response = await fetch(`${convexUrl}/task-snapshots`, {
    headers: { Accept: "application/json", Authorization: `Bearer ${token}` },
  });
  const payload = await response.json().catch(() => undefined);
  if (!response.ok) throw new Error(payload?.error || `Failed to synchronize sessions (${response.status})`);
  return Array.isArray(payload) ? payload as AgentTaskSnapshot[] : [];
}

export async function tombstoneAgentTask(convexUrl: string, token: string, deviceId: string, taskId: string): Promise<void> {
  saveDeletionOutbox([
    ...deletionOutbox().filter((row) => !(row.deviceId === deviceId && row.taskId === taskId)),
    { deviceId, taskId, deletedAt: Date.now() },
  ]);
  await postTaskTombstone(convexUrl, token, deviceId, taskId);
  saveDeletionOutbox(deletionOutbox().filter((row) => !(row.deviceId === deviceId && row.taskId === taskId)));
}

function taskKey(deviceId: string | undefined, taskId: string): string {
  return `${deviceId || "local"}:${taskId}`;
}

function localOnly(task: Task): boolean {
  return task.source === "phone-local" || task.id.startsWith("pending-cloud:");
}

/** A fresh snapshot is the owning agent's full lifecycle index. It may remove
 * a cached ghost; stale/missing Convex state never overrides direct truth. */
export function reconcileTasksWithAgentSnapshots(
  current: Task[],
  snapshots: AgentTaskSnapshot[],
  now = Date.now(),
): Task[] {
  const deleted = new Set(snapshots.flatMap((snapshot) =>
    (snapshot.deletedTasks ?? []).map((task) => taskKey(snapshot.deviceId, task.taskId)),
  ));
  const fresh = new Map(snapshots
    .filter((snapshot) => snapshot.deviceId && now - snapshot.observedAt <= TASK_SNAPSHOT_FRESH_MS)
    .map((snapshot) => [snapshot.deviceId, snapshot]));
  const indexed = new Map<string, { snapshot: AgentTaskSnapshot; task: AgentTaskLifecycle }>();
  for (const snapshot of fresh.values()) {
    for (const task of snapshot.tasks) indexed.set(taskKey(snapshot.deviceId, task.taskId), { snapshot, task });
  }

  const result: Task[] = [];
  const present = new Set<string>();
  for (const task of current) {
    const key = taskKey(task.deviceId, task.id);
    if (deleted.has(key)) continue;
    if (localOnly(task) || !task.deviceId || !fresh.has(task.deviceId)) {
      result.push(task);
      present.add(key);
      continue;
    }
    const lifecycle = indexed.get(key);
    if (!lifecycle) continue;
    result.push({ ...task, status: lifecycle.task.status, updatedAt: lifecycle.task.updatedAt || task.updatedAt });
    present.add(key);
  }

  for (const [key, lifecycle] of indexed) {
    if (deleted.has(key)) continue;
    if (present.has(key)) continue;
    const label = lifecycle.snapshot.deviceName || lifecycle.snapshot.deviceId.slice(0, 8);
    result.push({
      id: lifecycle.task.taskId,
      title: `Task on ${label}`,
      description: "Connect to this machine to load the conversation.",
      status: lifecycle.task.status,
      source: "session-index",
      hostKind: lifecycle.task.hostKind,
      deviceId: lifecycle.snapshot.deviceId,
      deviceName: lifecycle.snapshot.deviceName,
      output: [],
      createdAt: lifecycle.task.updatedAt || lifecycle.snapshot.observedAt,
      updatedAt: lifecycle.task.updatedAt || lifecycle.snapshot.observedAt,
    });
  }
  return result.sort((a, b) => (b.updatedAt || 0) - (a.updatedAt || 0));
}
