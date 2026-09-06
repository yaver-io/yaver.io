import { router } from "expo-router";
import {
  shouldNotifyTaskReply,
  taskReplyNotificationCopy,
  taskReviewNotificationRoute,
  type TaskReviewNotificationData,
} from "./taskReviewRoute";

export { shouldNotifyTaskReply, shouldNotifyTaskReview, taskReviewNotificationRoute } from "./taskReviewRoute";

export const TASK_REVIEW_NOTIFICATION_CHANNEL_ID = "yaver-task-review";

export type TaskReviewNotificationTarget = {
  taskId?: string | null;
  deviceId?: string | null;
};

let notificationHandlerInstalled = false;

/**
 * A task reply is an exact conversation destination, never a generic alert.
 * Keep the target in the native payload: the app can be launched cold and has
 * no in-memory task row to infer it from after the user taps the notification.
 */
export async function notifyTaskReply(
  taskTitle: string,
  target: TaskReviewNotificationTarget = {},
  reply: { status?: string; assistantText?: string } = {},
): Promise<boolean> {
  try {
    // eslint-disable-next-line @typescript-eslint/no-require-imports
    const Notifications: any = require("expo-notifications");
    if (!Notifications) return false;

    if (!notificationHandlerInstalled && typeof Notifications.setNotificationHandler === "function") {
      Notifications.setNotificationHandler({
        handleNotification: async () => ({
          shouldShowAlert: true,
          shouldShowBanner: true,
          shouldShowList: true,
          shouldPlaySound: true,
          shouldSetBadge: false,
        }),
      });
      notificationHandlerInstalled = true;
    }

    let permission = await Notifications.getPermissionsAsync();
    if (permission?.status !== "granted") {
      if (permission?.canAskAgain === false) return false;
      permission = await Notifications.requestPermissionsAsync();
      if (permission?.status !== "granted") return false;
    }

    // eslint-disable-next-line @typescript-eslint/no-require-imports
    const { Platform } = require("react-native");
    if (Platform?.OS === "android" && typeof Notifications.setNotificationChannelAsync === "function") {
      await Notifications.setNotificationChannelAsync(TASK_REVIEW_NOTIFICATION_CHANNEL_ID, {
        name: "Task review",
        importance: Notifications.AndroidImportance?.HIGH ?? 4,
      });
    }

    const copy = taskReplyNotificationCopy({
      status: reply.status,
      taskTitle,
      assistantText: reply.assistantText,
    });
    await Notifications.scheduleNotificationAsync({
      content: {
        title: copy.title,
        body: copy.body,
        sound: "default",
        data: {
          kind: "task-review",
          taskId: target.taskId || undefined,
          deviceId: target.deviceId || undefined,
          // A second notification for the same task must reopen the detail
          // after the user dismissed it; Tasks consumes this route once.
          openedAt: Date.now(),
        },
      },
      trigger: null,
    });
    return true;
  } catch {
    return false;
  }
}

// Compatibility export for callers compiled against the review-only name.
export const notifyTaskNeedsReview = notifyTaskReply;

/**
 * Installs the process-wide handler, rather than a Tasks-screen listener, so
 * tapping a review notification works when the app is backgrounded or starts
 * cold on another tab. Old notifications without a task id still land on
 * Tasks; new notifications always identify the exact conversation.
 */
export function installTaskReviewNotificationListener(): () => void {
  try {
    // eslint-disable-next-line @typescript-eslint/no-require-imports
    const Notifications: any = require("expo-notifications");
    if (!Notifications) return () => {};
    const open = (response: any) => {
      const data = response?.notification?.request?.content?.data as TaskReviewNotificationData | undefined;
      const destination = taskReviewNotificationRoute(data || {});
      if (!destination) return;
      try {
        router.navigate(destination as any);
      } catch {
        // Navigation may still be mounting during a cold launch. A short retry
        // keeps the payload-owned destination instead of dropping the tap.
        setTimeout(() => {
          try { router.navigate(destination as any); } catch {}
        }, 250);
      }
    };
    const subscription = typeof Notifications.addNotificationResponseReceivedListener === "function"
      ? Notifications.addNotificationResponseReceivedListener(open)
      : null;
    if (typeof Notifications.getLastNotificationResponseAsync === "function") {
      void Notifications.getLastNotificationResponseAsync().then((response: any) => {
        if (response) {
          open(response);
          // Expo retains the launch response. Clear it once consumed so an old
          // Review tap cannot reopen itself on a later ordinary app launch.
          if (typeof Notifications.clearLastNotificationResponseAsync === "function") {
            void Notifications.clearLastNotificationResponseAsync().catch(() => {});
          }
        }
      }).catch(() => {});
    }
    return () => subscription?.remove?.();
  } catch {
    return () => {};
  }
}
