export type MobileProjectAction = {
  label: string;
  target: string;
  type: string;
  framework?: string;
  platform?: string;
  command?: string;
  icon?: string;
  supported?: boolean;
  reason?: string;
};

export function isHermesMobileFramework(framework?: string): boolean {
  return framework === "expo" || framework === "react-native";
}

/**
 * What the agent's capability-detection layer says this project supports.
 *
 * Mirrors ProjectPreviewCapabilities in
 * desktop/agent/project_preview_capabilities.go. The AGENT decides — it can see
 * the project on disk, this surface cannot. Every surface (mobile, web, tvOS,
 * glass) reads the same answer instead of each maintaining its own framework
 * conditionals, which is how they drift apart.
 */
export type ProjectPreviewCapabilities = {
  framework?: string;
  selfDevelopment?: boolean;
  hasPairedDevice?: boolean;
  reason?: string;
  options?: Array<{
    id: string;
    label?: string;
    supported?: boolean;
    primary?: boolean;
    reason?: string;
    framework?: string;
  }>;
};

/** Action types that can only work inside a React Native runtime. */
const HERMES_ONLY_ACTION_TYPES = new Set(["open-native", "compile-hermes"]);

/**
 * Filter a composed action sheet down to what the agent actually detected.
 *
 * The rule this enforces: a Hermes bundle is JavaScript bytecode executed by a
 * React Native runtime. A Flutter, Kotlin, Swift or plain-web project has
 * nothing to load it into, so the option must be ABSENT — not greyed out. A
 * disabled button still tells the user the capability exists for their app and
 * invites them to go hunting for why it's off.
 *
 * Returns actions unchanged when the agent is too old to answer, so an older
 * box degrades to the previous behaviour instead of an empty sheet.
 */
export function applyPreviewCapabilities(
  actions: MobileProjectAction[],
  caps?: ProjectPreviewCapabilities | null,
): MobileProjectAction[] {
  if (!caps || !Array.isArray(caps.options) || caps.options.length === 0) {
    return actions;
  }
  const offered = new Set(caps.options.map((o) => o.id));
  const hermesOffered = offered.has("compile-hermes") || offered.has("open-native");

  const filtered = actions.filter((a) => {
    if (HERMES_ONLY_ACTION_TYPES.has(a.type) && !hermesOffered) return false;
    return true;
  });

  // Carry the agent's support flag + explanation onto the actions it knows
  // about. The agent can see things this surface cannot (no paired device, a
  // simulator that only exists on macOS).
  const byId = new Map(caps.options.map((o) => [o.id, o]));
  const annotated = filtered.map((a) => {
    const o = byId.get(a.type);
    if (!o) return a;
    return {
      ...a,
      supported: o.supported !== false && a.supported !== false,
      reason: a.reason || o.reason,
    };
  });

  // Lead with whatever the agent marked primary.
  const primaryID = caps.options.find((o) => o.primary)?.id;
  if (!primaryID) return annotated;
  const idx = annotated.findIndex((a) => a.type === primaryID);
  if (idx <= 0) return annotated;
  const [primary] = annotated.splice(idx, 1);
  return [primary, ...annotated];
}

export function isYaverSelfDevelopmentProject(project?: string, path?: string, repoURL?: string): boolean {
  const haystack = `${project || ""} ${path || ""} ${repoURL || ""}`.toLowerCase();
  return haystack.includes("yaver.io") ||
    haystack.includes("yaver-io/yaver") ||
    haystack.includes("io.yaver.mobile");
}

export const YAVER_SELF_DEV_HERMES_BLOCK_REASON =
  "Yaver developing Yaver must use WebRTC/browser preview. Loading Yaver into Yaver via Hermes puts two shake/exit owners in one process, so the preview can trap the host app.";

export function guardYaverSelfDevelopmentActions(
  actions: MobileProjectAction[],
  project?: string,
  path?: string,
  repoURL?: string,
): MobileProjectAction[] {
  if (!isYaverSelfDevelopmentProject(project, path, repoURL)) {
    return actions;
  }

  const guarded = actions.map((action) => {
    if ((action.type === "open-native" || action.type === "compile-hermes") && isHermesMobileFramework(action.framework)) {
      return {
        ...action,
        supported: false,
        reason: YAVER_SELF_DEV_HERMES_BLOCK_REASON,
      };
    }
    return action;
  });

  const firstRemoteRuntime = guarded.findIndex((action) => action.type === "remote-runtime" && isHermesMobileFramework(action.framework));
  if (firstRemoteRuntime <= 0) {
    return guarded;
  }

  const [remoteRuntime] = guarded.splice(firstRemoteRuntime, 1);
  return [remoteRuntime, ...guarded];
}
