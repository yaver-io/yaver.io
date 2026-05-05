// Haptic feedback for the task-chat flow. Maps spec X1 events to
// expo-haptics calls. All wrapped in best-effort try/catch — haptics
// fail closed (no error to user) on devices/simulators that lack
// the engine. Keep this list narrow; per-tool-call haptics were
// considered and rejected as too noisy (X1 footnote).
import * as Haptics from "expo-haptics";

function safe(fn: () => Promise<void> | void): void {
  try {
    void Promise.resolve(fn()).catch(() => {});
  } catch {
    // intentionally swallow
  }
}

export const taskHaptics = {
  /** User tapped Send. Medium impact — confirms input committed. */
  send(): void {
    safe(() => Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Medium));
  },
  /** Task completed successfully. */
  taskCompleted(): void {
    safe(() => Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success));
  },
  /** Task failed. */
  taskFailed(): void {
    safe(() => Haptics.notificationAsync(Haptics.NotificationFeedbackType.Error));
  },
  /** User tapped Stop. Warning — destructive but expected. */
  stop(): void {
    safe(() => Haptics.notificationAsync(Haptics.NotificationFeedbackType.Warning));
  },
  /** User tapped Retry. */
  retry(): void {
    safe(() => Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Medium));
  },
  /** A smart-retry suggestion just appeared in an error card. Light. */
  suggestionAppeared(): void {
    safe(() => Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light));
  },
};
