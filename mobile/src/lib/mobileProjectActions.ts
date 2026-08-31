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
  hermesBuildState?: "needs_build" | "ready" | string;
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

/** Lane ids this surface knows how to execute, with display defaults used
 *  when the agent offers a lane the caller didn't locally compose (the
 *  wire-push case: the agent detects a USB-attached device, this surface has
 *  no local reason to guess that). The DISPATCHER in apps.tsx must have a
 *  branch for every id here — that pairing is the "handler registry". */
const KNOWN_LANE_DEFAULTS: Record<string, { label: string; icon?: string }> = {
  "open-native": { label: "Hermes Reload", icon: "\u{1F4F1}" },
  "compile-hermes": { label: "Compile Hermes bundle", icon: "\u{1F527}" },
  "dev-server": { label: "Browser Reload", icon: "\u{1F310}" },
  "remote-runtime": { label: "WebRTC Reload", icon: "\u{1F4FA}" },
  "wire-push": { label: "Install via USB", icon: "\u{1F50C}" },
};

export const UNKNOWN_PREVIEW_OPTION_REASON =
  "This machine offers a preview option this app version doesn't know yet — update the Yaver app to use it.";

/**
 * COMPOSE the action sheet from what the agent detected.
 *
 * The agent is the authority — it can see the project on disk and the
 * hardware on the box; this surface cannot. The locally built lanes serve as
 * templates (they carry per-surface knowledge like Hermes compatibility
 * verdicts) and as the FALLBACK when the agent is too old to answer.
 *
 * Rules, in order of the bugs they fix:
 *  • A lane the agent doesn't offer is ABSENT — not greyed out. A Hermes
 *    bundle has no runtime in a Flutter/Kotlin/Swift project; a disabled
 *    button would still advertise the capability and invite a hunt for why
 *    it's off.
 *  • A lane the agent DOES offer is present even without a local template —
 *    the old filter-only logic silently dropped the agent's wire-push.
 *  • An option id this app doesn't know renders DISABLED with the agent's
 *    label + reason, never vanishes: the box said it exists; hiding it makes
 *    the phone lie by omission.
 *  • Agent option order wins; the agent's primary leads.
 */
export function applyPreviewCapabilities(
  actions: MobileProjectAction[],
  caps?: ProjectPreviewCapabilities | null,
): MobileProjectAction[] {
  if (!caps || !Array.isArray(caps.options) || caps.options.length === 0) {
    return actions;
  }
  const byType = new Map(actions.map((a) => [a.type, a]));

  // The agent should already return one Hermes action, but keep the consumer
  // honest too: a mixed-version box may include both while also reporting the
  // structured state. Never show Reload before there is a bundle, and never
  // leave Compile beside Reload once one exists.
  // Old agents advertised both actions and had no build-state field. Treat
  // that ambiguity as needs_build: Compile is safe and deterministic; Reload
  // without a confirmed artifact is the dead end this guard removes. Once the
  // agent reports ready it replaces Compile with Reload.
  const hermesBuildState = caps.hermesBuildState ||
    (caps.options.some((option) => option.id === "compile-hermes") ? "needs_build" : "");
  const options = caps.options.filter((option) => {
    if (hermesBuildState === "needs_build" && option.id === "open-native") return false;
    if (hermesBuildState === "ready" && option.id === "compile-hermes") return false;
    return true;
  });

  const composed = options.map((o): MobileProjectAction => {
    const tmpl = byType.get(o.id);
    if (tmpl) {
      // Local template exists: merge the agent's verdict onto it. The local
      // surface can also veto (for example Hermes compatibility) —
      // both must agree for the lane to be enabled.
      return {
        ...tmpl,
        supported: o.supported !== false && tmpl.supported !== false,
        reason: tmpl.reason || o.reason,
      };
    }
    const known = KNOWN_LANE_DEFAULTS[o.id];
    if (known) {
      return {
        label: o.label || known.label,
        target: ".",
        type: o.id,
        icon: known.icon,
        framework: o.framework || caps.framework,
        supported: o.supported !== false,
        reason: o.reason,
      };
    }
    // Unknown id: this app predates the option. Visible but disabled, with
    // the agent's own words.
    return {
      label: o.label || o.id,
      target: ".",
      type: o.id,
      framework: o.framework || caps.framework,
      supported: false,
      reason: o.reason || UNKNOWN_PREVIEW_OPTION_REASON,
    };
  });

  // Preferred default lane order: Browser Reload first (the universal lane —
  // works for every framework and needs no native toolchain), then Hermes
  // (RN/Expo only), then WebRTC, then the compile step. A stable sort by this
  // rank so same-rank lanes keep the agent's relative order. The agent's
  // explicit `primary` still overrides the lead below.
  const laneRank: Record<string, number> = {
    "dev-server": 0, // Browser Reload
    "open-native": 1, // Hermes Reload
    "remote-runtime": 2, // WebRTC Reload
    "compile-hermes": 3, // Compile Hermes bundle
  };
  const ordered = composed
    .map((a, i) => ({ a, i }))
    .sort((x, y) => {
      const rx = laneRank[x.a.type] ?? 99;
      const ry = laneRank[y.a.type] ?? 99;
      return rx !== ry ? rx - ry : x.i - y.i; // stable within a rank
    })
    .map((x) => x.a);

  // Lead with whatever the agent marked primary (stable otherwise).
  const primaryID = options.find((o) => o.primary)?.id;
  if (!primaryID) return ordered;
  const idx = ordered.findIndex((a) => a.type === primaryID);
  if (idx <= 0) return ordered;
  const [primary] = ordered.splice(idx, 1);
  return [primary, ...ordered];
}

/** One app row from the agent's /workspace/apps projection (monorepo
 *  manifest). Mirrors web/lib/agent-client.ts::WorkspaceAppView. */
export type WorkspaceAppRow = {
  name: string;
  path: string;
  framework?: string;
  kind?: string;
  exists?: boolean;
};

/**
 * Map a monorepo's sub-apps to per-target browser lanes — the "pick a
 * sub-app" step (yaver.io → `mobile · expo` / `web · next`) that web's
 * target discovery already offers and mobile silently lacked. Each lane is
 * a dev-server action whose `target` is the sub-app's relative path, which
 * the dispatcher already resolves against the project root.
 *
 * A single-app workspace IS the project — no sub-app step. Apps the
 * manifest lists but that don't exist on disk are excluded (offering a
 * lane into a missing directory teaches the user Yaver lies).
 */
export function workspaceAppLanes(apps: WorkspaceAppRow[]): MobileProjectAction[] {
  const real = (apps || []).filter((a) => a && a.exists !== false && a.path && a.name);
  if (real.length < 2) return [];
  return real.map((a) => ({
    label: `Browser Reload — ${a.name}${a.framework ? ` · ${a.framework}` : ""}`,
    target: a.path,
    type: "dev-server",
    icon: "\u{1F310}",
    framework: a.framework,
    supported: true,
  }));
}

export function isYaverSelfDevelopmentProject(project?: string, path?: string, repoURL?: string): boolean {
  const projectName = String(project || "").trim().toLowerCase();
  const normalizedPath = String(path || "").trim().replace(/\\/g, "/").toLowerCase();
  const normalizedRepo = String(repoURL || "").trim().replace(/\\/g, "/").toLowerCase();
  return projectName === "yaver.io" ||
    /(^|\/)yaver\.io(\/|$)/.test(normalizedPath) ||
    /(^|[/:])yaver-io\/yaver(?:\.io)?(?:\.git)?$/.test(normalizedRepo) ||
    /(^|[/.])io\.yaver\.mobile($|[/.])/.test(`${projectName} ${normalizedPath} ${normalizedRepo}`);
}

export function guardYaverSelfDevelopmentActions(
  actions: MobileProjectAction[],
  project?: string,
  path?: string,
  repoURL?: string,
): MobileProjectAction[] {
  // Self-development used to disable Hermes because its JS runtime could not
  // own a trustworthy escape. The escape now lives below JS (AppDelegate on
  // iOS, YaverShakeDetector on Android), so Yaver follows the same three-lane
  // contract as every other React Native project. Keep this compatibility
  // function for mixed-version callers, but never rewrite the agent's lanes.
  void project;
  void path;
  void repoURL;
  return actions;
}
