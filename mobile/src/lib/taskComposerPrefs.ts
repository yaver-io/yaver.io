import AsyncStorage from "@react-native-async-storage/async-storage";

export const TASK_VIDEO_SUMMARY_KEY = "@yaver/tasks_video_summary_enabled";

export async function loadTaskVideoSummaryEnabled(): Promise<boolean> {
  try {
    const raw = await AsyncStorage.getItem(TASK_VIDEO_SUMMARY_KEY);
    return raw === "1";
  } catch {
    return false;
  }
}

export async function saveTaskVideoSummaryEnabled(enabled: boolean): Promise<void> {
  try {
    await AsyncStorage.setItem(TASK_VIDEO_SUMMARY_KEY, enabled ? "1" : "0");
  } catch {
    // Ignore local preference write failures; task creation still works.
  }
}
