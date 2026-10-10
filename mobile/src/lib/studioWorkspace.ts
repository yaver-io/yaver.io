export type StudioLane = "device" | "browser" | "live" | "logs";
export type StudioRunner = "shell" | "codex" | "claude" | "opencode";

export type StudioDefaults = {
  lane: StudioLane;
  runner: StudioRunner;
  splitRatio: number;
  tmuxSession: string;
  voiceInputEnabled: boolean;
  voiceOutputEnabled: boolean;
};

export const DEFAULT_STUDIO_DEFAULTS: StudioDefaults = {
  lane: "device",
  runner: "shell",
  splitRatio: 0.3,
  tmuxSession: "yaver-studio",
  voiceInputEnabled: false,
  voiceOutputEnabled: false,
};

/** Capability filtering is framework-based and deliberately conservative.
 * An incompatible lane is never shown and therefore cannot be launched. */
export function studioLanesFor(framework = "", surfaces: string[] = []): StudioLane[] {
  const value = `${framework} ${surfaces.join(" ")}`.toLowerCase();
  if (/swift|swiftui|ios|tvos|visionos|watchos/.test(value)) return ["device", "live", "logs"];
  if (/react-native|react native|expo/.test(value)) return ["device", "browser", "live", "logs"];
  if (/flutter/.test(value)) return ["device", "browser", "live", "logs"];
  if (/kotlin|android|wear/.test(value)) return ["device", "live", "logs"];
  if (/web|next|vite|react|vue|svelte|angular/.test(value)) return ["browser", "live", "logs"];
  if (/backend|server|go|rust|python|node/.test(value)) return ["logs"];
  return ["browser", "live", "logs"];
}

export function resolveStudioDefaults(
  saved: Partial<StudioDefaults> | null | undefined,
  framework = "",
  surfaces: string[] = [],
): StudioDefaults {
  const lanes = studioLanesFor(framework, surfaces);
  const requestedRatio = Number(saved?.splitRatio);
  return {
    lane: saved?.lane && lanes.includes(saved.lane) ? saved.lane : lanes[0],
    runner: ["shell", "codex", "claude", "opencode"].includes(saved?.runner || "")
      ? saved!.runner!
      : DEFAULT_STUDIO_DEFAULTS.runner,
    splitRatio: Number.isFinite(requestedRatio) ? Math.max(0.2, Math.min(0.5, requestedRatio)) : 0.3,
    tmuxSession: saved?.tmuxSession?.trim() || DEFAULT_STUDIO_DEFAULTS.tmuxSession,
    voiceInputEnabled: saved?.voiceInputEnabled === true,
    voiceOutputEnabled: saved?.voiceOutputEnabled === true,
  };
}
