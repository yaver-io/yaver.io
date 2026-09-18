export const MOBILE_REVIEW_MAX_AGE_MS = 24 * 60 * 60 * 1000;

export type ReviewInboxTask = {
  id: string;
  deviceId?: string | null;
  status: string;
  updatedAt?: number | null;
};

export function reviewInboxTaskKey(task: ReviewInboxTask): string {
  return `${task.deviceId || "local"}:${task.id}`;
}

function isRecent(task: ReviewInboxTask, now: number): boolean {
  const updatedAt = Number(task.updatedAt || 0);
  const age = now - updatedAt;
  return updatedAt > 0 && age >= 0 && age <= MOBILE_REVIEW_MAX_AGE_MS;
}

/**
 * Review is a bounded inbox, not task history. Explicit review/ready rows age
 * out after 24 hours. When there is no active work and exactly one recently
 * completed task, present that task in Review so the default Active view does
 * not become an empty dead end immediately after the user's only task lands.
 * The underlying agent status remains completed.
 */
export function mobileReviewInboxPolicy<T extends ReviewInboxTask>(
  tasks: readonly T[],
  now = Date.now(),
): { visibleTaskKeys: Set<string>; promotedCompletedTaskKey: string | null } {
  const visible = tasks.filter((task) => {
    if (task.status !== "review" && task.status !== "ready") return true;
    return isRecent(task, now);
  });
  const activeCount = visible.filter(
    (task) => task.status === "running" || task.status === "queued",
  ).length;
  const recentCompleted = visible.filter(
    (task) => task.status === "completed" && isRecent(task, now),
  );
  return {
    visibleTaskKeys: new Set(visible.map(reviewInboxTaskKey)),
    promotedCompletedTaskKey:
      activeCount === 0 && recentCompleted.length === 1
        ? reviewInboxTaskKey(recentCompleted[0])
        : null,
  };
}
