/**
 * dogfoodConfig — shared config for the "improve Yaver with Yaver" loop.
 *
 * Stored under the SAME AsyncStorage key the Settings "Contributor dogfood"
 * section uses (`@yaver/u/<uid>/dogfood_yaver`) so the repo dir + base prompt
 * stay in sync between Settings and the Dogfood thread. We add `runner` and
 * `mode` fields; Settings ignores the extras (backward compatible).
 */

import AsyncStorage from "@react-native-async-storage/async-storage";

export type DogfoodMode = "vibe" | "pr";

export interface DogfoodConfig {
  /** Absolute path to the yaver.io checkout on the dev box (Vibe mode). */
  repoDir: string;
  /** Base instruction prepended to every dogfood task. */
  prompt: string;
  /** Coding runner to use (claude-code / codex / opencode). */
  runner: string;
  /** Last-used dispatch mode. Auto-pick still overrides per-send. */
  mode: DogfoodMode;
}

export const DEFAULT_DOGFOOD_PROMPT =
  "Refresh Yaver using Yaver. Use the Go agent for code changes, keep the mobile app loadable in Yaver, and prefer Hermes/mobile-safe workflows.";

export const DEFAULT_DOGFOOD_CONFIG: DogfoodConfig = {
  repoDir: "",
  prompt: DEFAULT_DOGFOOD_PROMPT,
  runner: "claude-code",
  mode: "vibe",
};

function key(uid?: string | null): string {
  return uid ? `@yaver/u/${uid}/dogfood_yaver` : "@yaver/dogfood_yaver";
}

export async function loadDogfoodConfig(uid?: string | null): Promise<DogfoodConfig> {
  try {
    const raw = await AsyncStorage.getItem(key(uid));
    if (!raw) return { ...DEFAULT_DOGFOOD_CONFIG };
    const parsed = JSON.parse(raw) as Partial<DogfoodConfig>;
    return {
      repoDir: typeof parsed.repoDir === "string" ? parsed.repoDir : "",
      prompt:
        typeof parsed.prompt === "string" && parsed.prompt.trim()
          ? parsed.prompt
          : DEFAULT_DOGFOOD_PROMPT,
      runner: typeof parsed.runner === "string" && parsed.runner ? parsed.runner : "claude-code",
      mode: parsed.mode === "pr" ? "pr" : "vibe",
    };
  } catch {
    return { ...DEFAULT_DOGFOOD_CONFIG };
  }
}

export async function saveDogfoodConfig(
  uid: string | null | undefined,
  patch: Partial<DogfoodConfig>,
): Promise<DogfoodConfig> {
  const current = await loadDogfoodConfig(uid);
  const next: DogfoodConfig = { ...current, ...patch };
  try {
    await AsyncStorage.setItem(key(uid), JSON.stringify(next));
  } catch {
    // best-effort
  }
  return next;
}
