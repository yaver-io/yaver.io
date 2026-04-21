/**
 * SDK user preferences persisted across launches.
 *
 * Currently only the quick-action icon's user-level dismiss flag
 * lives here: the dev enables the icon via `FeedbackConfig.quickIcon`,
 * but the *user* can long-press → Hide to opt out, and we remember
 * that choice across launches so their next app session still
 * respects it.
 *
 * AsyncStorage is an optional peer dep — if it's not installed the
 * getters return `false` and the setters silently no-op, so the icon
 * still works (it just can't remember the disable beyond the
 * in-memory session).
 */

let AsyncStorage: {
  getItem: (key: string) => Promise<string | null>;
  setItem: (key: string, value: string) => Promise<void>;
  removeItem: (key: string) => Promise<void>;
} | null = null;
try {
  AsyncStorage = require('@react-native-async-storage/async-storage').default;
} catch {
  // not installed — degrade gracefully
}

const QUICK_ICON_DISABLED_KEY = 'yaver_feedback_quickicon_disabled';

/** True if the user has long-pressed the icon and chosen "Hide". */
export async function getQuickIconDisabled(): Promise<boolean> {
  if (!AsyncStorage) return false;
  try {
    const v = await AsyncStorage.getItem(QUICK_ICON_DISABLED_KEY);
    return v === '1';
  } catch {
    return false;
  }
}

export async function setQuickIconDisabled(disabled: boolean): Promise<void> {
  if (!AsyncStorage) return;
  try {
    if (disabled) {
      await AsyncStorage.setItem(QUICK_ICON_DISABLED_KEY, '1');
    } else {
      await AsyncStorage.removeItem(QUICK_ICON_DISABLED_KEY);
    }
  } catch {
    // best-effort
  }
}

export async function clearQuickIconDisabled(): Promise<void> {
  await setQuickIconDisabled(false);
}
