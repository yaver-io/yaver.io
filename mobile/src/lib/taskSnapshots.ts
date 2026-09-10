import { getToken } from "./auth";
import { getConvexSiteUrlSync } from "./backendConfig";
import type { TaskStatus } from "./quic";
import { acknowledgeTaskDeletion, getPendingTaskDeletions, queueTaskDeletion } from "./storage";

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

/** Record deletion before contacting the box. Box reachability is never part
 * of this operation; Convex carries the intent until the owner reconnects. */
export async function tombstoneAgentTask(deviceId: string, taskId: string): Promise<void> {
  await queueTaskDeletion(deviceId, taskId);
  const token = await getToken();
  if (!token) throw new Error("Sign in to remove this task everywhere.");
  const response = await fetch(`${getConvexSiteUrlSync()}/task-tombstones`, {
    method: "POST",
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
    body: JSON.stringify({ deviceId, taskId }),
  });
  const payload = await response.json().catch(() => undefined);
  if (!response.ok) throw new Error(payload?.error || `Could not synchronize task removal (${response.status})`);
  await acknowledgeTaskDeletion(deviceId, taskId);
}

async function flushTaskDeletionOutbox(): Promise<void> {
  for (const row of await getPendingTaskDeletions()) {
    try { await tombstoneAgentTask(row.deviceId, row.taskId); } catch { return; }
  }
}

/** Prompt-free session addresses published by each Go agent. Descriptive task
 * content is fetched from that agent P2P after the user opens the session. */
export async function listAgentTaskSnapshots(): Promise<AgentTaskSnapshot[]> {
  await flushTaskDeletionOutbox();
  const token = await getToken();
  if (!token) return [];
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 10_000);
  try {
    const response = await fetch(`${getConvexSiteUrlSync()}/task-snapshots`, {
      signal: controller.signal,
      headers: { Accept: "application/json", Authorization: `Bearer ${token}` },
    });
    const payload = await response.json().catch(() => undefined);
    if (!response.ok) throw new Error(payload?.error || `Failed to synchronize sessions (${response.status})`);
    return Array.isArray(payload) ? payload as AgentTaskSnapshot[] : [];
  } finally {
    clearTimeout(timeout);
  }
}
