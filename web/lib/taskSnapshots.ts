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
}

export const TASK_SNAPSHOT_FRESH_MS = 3 * 60 * 60 * 1000;

export async function listAgentTaskSnapshots(convexUrl: string, token: string): Promise<AgentTaskSnapshot[]> {
  const response = await fetch(`${convexUrl}/task-snapshots`, {
    headers: { Accept: "application/json", Authorization: `Bearer ${token}` },
  });
  const payload = await response.json().catch(() => undefined);
  if (!response.ok) throw new Error(payload?.error || `Failed to synchronize sessions (${response.status})`);
  return Array.isArray(payload) ? payload as AgentTaskSnapshot[] : [];
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
