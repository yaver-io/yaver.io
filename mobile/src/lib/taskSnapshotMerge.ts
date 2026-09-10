import type { Task } from "./quic";
import type { AgentTaskSnapshot } from "./taskSnapshots";

export const TASK_SNAPSHOT_FRESH_MS = 3 * 60 * 60 * 1000;

export function scopedTaskIdentity(deviceId: string | undefined, taskId: string): string {
  return `${deviceId || "local"}:${taskId}`;
}

function localOnly(task: Task): boolean {
  return task.source === "phone-local" || task.runnerId === "yaver-phone" || task.runnerId === "yaver-agent" || task.id.startsWith("pending-cloud:");
}

/** Reconcile phone-local display cache with prompt-free agent truth.
 *
 * A fresh full snapshot may remove a cached row absent from the owning agent.
 * A stale/offline snapshot may never make that claim. Missing descriptions are
 * represented by a neutral address row until P2P hydration reaches the owner.
 */
export function reconcileTasksWithAgentSnapshots(
  cached: Task[],
  snapshots: AgentTaskSnapshot[],
  now = Date.now(),
): Task[] {
  const deleted = new Set(snapshots.flatMap((snapshot) =>
    (snapshot.deletedTasks ?? []).map((task) => scopedTaskIdentity(snapshot.deviceId, task.taskId)),
  ));
  const freshSnapshots = new Map(
    snapshots
      .filter((snapshot) => snapshot.deviceId && now - snapshot.observedAt <= TASK_SNAPSHOT_FRESH_MS)
      .map((snapshot) => [snapshot.deviceId, snapshot]),
  );
  const lifecycle = new Map<string, { snapshot: AgentTaskSnapshot; task: AgentTaskSnapshot["tasks"][number] }>();
  for (const snapshot of freshSnapshots.values()) {
    for (const task of snapshot.tasks) {
      lifecycle.set(scopedTaskIdentity(snapshot.deviceId, task.taskId), { snapshot, task });
    }
  }

  const reconciled: Task[] = [];
  const present = new Set<string>();
  for (const task of cached) {
    if (deleted.has(scopedTaskIdentity(task.deviceId, task.id))) continue;
    if (localOnly(task) || !task.deviceId || !freshSnapshots.has(task.deviceId)) {
      reconciled.push(task);
      present.add(scopedTaskIdentity(task.deviceId, task.id));
      continue;
    }
    const key = scopedTaskIdentity(task.deviceId, task.id);
    const authoritative = lifecycle.get(key);
    if (!authoritative) continue;
    reconciled.push({
      ...task,
      status: authoritative.task.status,
      updatedAt: authoritative.task.updatedAt || task.updatedAt,
      executionSession: authoritative.task.yaverSessionId
        ? { ...(task.executionSession || {} as any), yaverSessionId: authoritative.task.yaverSessionId, taskId: task.id }
        : task.executionSession,
    });
    present.add(key);
  }

  for (const [key, authoritative] of lifecycle) {
    if (deleted.has(key)) continue;
    if (present.has(key)) continue;
    const { snapshot, task } = authoritative;
    reconciled.push({
      id: task.taskId,
      title: `Task on ${snapshot.deviceName || snapshot.deviceId.slice(0, 8)}`,
      description: "Connect to this machine to load the conversation.",
      status: task.status,
      output: [],
      source: "session-index",
      hostKind: task.hostKind,
      deviceId: snapshot.deviceId,
      deviceName: snapshot.deviceName,
      createdAt: task.updatedAt || snapshot.observedAt,
      updatedAt: task.updatedAt || snapshot.observedAt,
      executionSession: task.yaverSessionId
        ? { yaverSessionId: task.yaverSessionId, taskId: task.taskId, hostKind: task.hostKind, sessionStartedAt: new Date(task.updatedAt || snapshot.observedAt).toISOString() } as any
        : undefined,
    });
  }
  return reconciled.sort((a, b) => (b.updatedAt || 0) - (a.updatedAt || 0));
}
