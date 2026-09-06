/**
 * Pure notification-to-route contract. Kept outside the native notification
 * module so it can be proved in Node as well as used during a cold Expo launch.
 */
export type TaskReviewNotificationData = {
  kind?: unknown;
  taskId?: unknown;
  deviceId?: unknown;
  openedAt?: unknown;
};

export function shouldNotifyTaskReply(
  previousStatus: string | null | undefined,
  nextStatus: string | null | undefined,
): boolean {
  // `ready` is the normal end of a conversational runner turn. Waiting only
  // for `review` meant most successful Codex/Claude/OpenCode replies never
  // notified the phone at all.
  return (nextStatus === "ready" || nextStatus === "review") &&
    (previousStatus === "running" || previousStatus === "queued");
}

// Compatibility name for older imports and notification payload tests.
export const shouldNotifyTaskReview = shouldNotifyTaskReply;

export type TaskReplyNotificationCopy = {
  title: string;
  body: string;
};

function readableNotificationText(value: unknown, max = 220): string {
  const text = String(value ?? "")
    .replace(/```[\s\S]*?```/g, " ")
    .replace(/^\s{0,3}#{1,6}\s+/gm, "")
    .replace(/^\s*[-*]\s+/gm, "")
    .replace(/[*_`]/g, "")
    .replace(/\s+/g, " ")
    .trim();
  return text.length > max ? `${text.slice(0, max - 1).trimEnd()}…` : text;
}

/** Copy shown outside the task screen. It may use only semantic assistant
 * presentation; callers must never pass transcript/raw console text here. */
export function taskReplyNotificationCopy(input: {
  status?: string | null;
  taskTitle?: string | null;
  assistantText?: string | null;
}): TaskReplyNotificationCopy {
  const body = readableNotificationText(input.assistantText) ||
    readableNotificationText(input.taskTitle) ||
    "A coding task has a new reply.";
  return {
    title: input.status === "review" ? "Ready to review" : "Yaver replied",
    body,
  };
}

function asNonEmptyString(value: unknown): string | null {
  if (typeof value === "number" && Number.isFinite(value)) return String(value);
  return typeof value === "string" && value.trim() ? value.trim() : null;
}

export function taskReviewNotificationRoute(data: TaskReviewNotificationData): {
  pathname: "/(tabs)/tasks";
  params: Record<string, string>;
} | null {
  if (data?.kind !== "task-review") return null;
  const taskId = asNonEmptyString(data.taskId);
  const deviceId = asNonEmptyString(data.deviceId);
  const openedAt = asNonEmptyString(data.openedAt) || String(Date.now());
  return {
    pathname: "/(tabs)/tasks",
    params: {
      ...(taskId ? { taskId } : {}),
      ...(deviceId ? { taskDeviceId: deviceId } : {}),
      taskNotificationNonce: openedAt,
    },
  };
}
