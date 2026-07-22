// startupScreen.ts — which tab the app opens on.
//
// Defaults to Projects: Yaver is overwhelmingly used for mobile app
// development, and the first thing a user wants after opening the app is the
// thing they are building. Tasks is the right default for someone driving
// long-running agent work, which is why this is a preference rather than a
// constant.
//
// Stored locally with AsyncStorage so the very first render can read it
// synchronously-ish without waiting on the network — an opening screen that
// arrives after a round-trip would flash the wrong tab. The value is ALSO
// mirrored to the agent's user settings so it follows the account across
// devices; the local copy is the source of truth for boot, the remote copy is
// the source of truth for sync. When they disagree, remote wins on the next
// launch, never mid-session (a tab that moves under you is worse than a stale
// preference).

import AsyncStorage from "@react-native-async-storage/async-storage";

export type StartupScreen = "projects" | "tasks";

export const STARTUP_SCREEN_KEY = "yaver.startupScreen";

/** The route name each choice maps to inside app/(tabs). */
export const STARTUP_SCREEN_ROUTES: Record<StartupScreen, string> = {
  projects: "apps",
  tasks: "tasks",
};

export const DEFAULT_STARTUP_SCREEN: StartupScreen = "projects";

export function isStartupScreen(v: unknown): v is StartupScreen {
  return v === "projects" || v === "tasks";
}

/** Read the preference. Never throws — a broken value falls back to the default. */
export async function getStartupScreen(): Promise<StartupScreen> {
  try {
    const raw = await AsyncStorage.getItem(STARTUP_SCREEN_KEY);
    return isStartupScreen(raw) ? raw : DEFAULT_STARTUP_SCREEN;
  } catch {
    return DEFAULT_STARTUP_SCREEN;
  }
}

/** Persist locally. Remote mirroring is the caller's job so this stays offline-safe. */
export async function setStartupScreen(next: StartupScreen): Promise<void> {
  try {
    await AsyncStorage.setItem(STARTUP_SCREEN_KEY, next);
  } catch {
    // A failed write costs a preference, not a session. Never surface it.
  }
}

/** The tabs route to open on launch. */
export async function getStartupRoute(): Promise<string> {
  return STARTUP_SCREEN_ROUTES[await getStartupScreen()];
}
