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
const QUICK_ICON_COLOR_KEY = 'yaver_feedback_quickicon_color';

export type QuickIconColorPreset =
  | 'orange'
  | 'lime'
  | 'cyan'
  | 'pink'
  | 'yellow'
  | 'slate';

export const QUICK_ICON_COLOR_PRESETS: Record<
  QuickIconColorPreset,
  {
    label: string;
    backgroundColor: string;
    foregroundColor: string;
    borderColor: string;
    shadowColor: string;
  }
> = {
  orange: {
    label: 'Orange',
    backgroundColor: '#ff6b2c',
    foregroundColor: '#111111',
    borderColor: 'rgba(255,255,255,0.92)',
    shadowColor: '#000000',
  },
  lime: {
    label: 'Lime',
    backgroundColor: '#a3e635',
    foregroundColor: '#111111',
    borderColor: 'rgba(255,255,255,0.85)',
    shadowColor: '#365314',
  },
  cyan: {
    label: 'Cyan',
    backgroundColor: '#22d3ee',
    foregroundColor: '#082f49',
    borderColor: 'rgba(255,255,255,0.82)',
    shadowColor: '#083344',
  },
  pink: {
    label: 'Pink',
    backgroundColor: '#fb7185',
    foregroundColor: '#fff1f2',
    borderColor: 'rgba(255,255,255,0.78)',
    shadowColor: '#4c0519',
  },
  yellow: {
    label: 'Yellow',
    backgroundColor: '#facc15',
    foregroundColor: '#1c1917',
    borderColor: 'rgba(255,255,255,0.88)',
    shadowColor: '#713f12',
  },
  slate: {
    label: 'Slate',
    backgroundColor: '#475569',
    foregroundColor: '#f8fafc',
    borderColor: 'rgba(255,255,255,0.68)',
    shadowColor: '#020617',
  },
};

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

export async function getQuickIconColorPreset(): Promise<QuickIconColorPreset | null> {
  if (!AsyncStorage) return null;
  try {
    const v = await AsyncStorage.getItem(QUICK_ICON_COLOR_KEY);
    if (!v) return null;
    if (Object.prototype.hasOwnProperty.call(QUICK_ICON_COLOR_PRESETS, v)) {
      return v as QuickIconColorPreset;
    }
    return null;
  } catch {
    return null;
  }
}

export async function setQuickIconColorPreset(
  preset: QuickIconColorPreset | null,
): Promise<void> {
  if (!AsyncStorage) return;
  try {
    if (!preset) {
      await AsyncStorage.removeItem(QUICK_ICON_COLOR_KEY);
      return;
    }
    await AsyncStorage.setItem(QUICK_ICON_COLOR_KEY, preset);
  } catch {
    // best-effort
  }
}

export async function clearQuickIconColorPreset(): Promise<void> {
  await setQuickIconColorPreset(null);
}
