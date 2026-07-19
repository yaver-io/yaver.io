export type YaverNativeSurface =
  | "web"
  | "ios"
  | "android"
  | "tablet"
  | "tvos"
  | "android-tv"
  | "watch"
  | "car"
  | "visionos"
  | "xr"
  | "remote-runner"
  | "mcp";

export type YaverNativeCatalogApp = {
  id: string;
  slug: string;
  title: string;
  subtitle: string;
  kind: "game" | "app" | "devtool" | "assistant";
  status: "planned" | "prototype" | "public-free" | "internal";
  owner: "yaver" | "developer";
  surfaces: readonly YaverNativeSurface[];
  companionOnlySurfaces?: readonly YaverNativeSurface[];
  route?: string;
  launchLabel: string;
  manifestFile: "yaver.game.yaml" | "yaver.app.yaml";
  auth: {
    provider: "yaver-oauth";
    requiredInYaverBuild: true;
  };
};

export const YAVER_NATIVE_CATALOG: readonly YaverNativeCatalogApp[] = [
  {
    id: "game_sfmg",
    slug: "sfmg",
    title: "SFMG",
    subtitle: "Football manager and owner strategy simulation.",
    kind: "game",
    status: "planned",
    owner: "yaver",
    surfaces: ["web", "ios", "android", "tablet", "tvos", "android-tv", "watch", "car", "remote-runner", "mcp"],
    companionOnlySurfaces: ["watch", "car"],
    route: "/remote-runtime?app=sfmg",
    launchLabel: "Open SFMG runtime",
    manifestFile: "yaver.game.yaml",
    auth: { provider: "yaver-oauth", requiredInYaverBuild: true },
  },
  {
    id: "game_carrotbet",
    slug: "carrotbet",
    title: "Carrotbet",
    subtitle: "Developer-owned casual strategy game pack through Yaver.",
    kind: "game",
    status: "prototype",
    owner: "developer",
    surfaces: ["web", "ios", "android", "tablet", "tvos", "android-tv", "watch", "car", "visionos", "xr", "remote-runner", "mcp"],
    companionOnlySurfaces: ["watch", "car"],
    route: "/remote-runtime?app=carrotbet",
    launchLabel: "Open Carrotbet runtime",
    manifestFile: "yaver.game.yaml",
    auth: { provider: "yaver-oauth", requiredInYaverBuild: true },
  },
  {
    // Remote PC control. `framework=desktop` is a pseudo-framework: it streams
    // the MACHINE rather than a project, so it carries no workDir (the agent
    // and remote-runtime.tsx both special-case it).
    //
    // Listed on watch/car/tv as companion-only on purpose — those surfaces
    // cannot usefully render a desktop video stream, but they CAN drive the
    // same machine by voice through the `desktop_voice` ops verb, which reads
    // the accessibility tree and answers out loud with no video at all.
    id: "app_remote_pc",
    slug: "remote-pc",
    title: "Remote PC",
    subtitle: "Control your Windows, Linux, or Mac desktop — video stream or voice only.",
    kind: "devtool",
    status: "prototype",
    owner: "yaver",
    surfaces: ["web", "ios", "android", "tablet", "tvos", "android-tv", "watch", "car", "visionos", "xr", "remote-runner", "mcp"],
    companionOnlySurfaces: ["watch", "car", "tvos", "android-tv"],
    route: "/remote-runtime?framework=desktop",
    launchLabel: "Control this machine",
    manifestFile: "yaver.app.yaml",
    auth: { provider: "yaver-oauth", requiredInYaverBuild: true },
  },
  {
    id: "app_personal_runtime",
    slug: "personal-runtime",
    title: "Personal Runtime",
    subtitle: "Cross-device assistant for user-owned apps, redroid, browsers, and MCP.",
    kind: "assistant",
    status: "planned",
    owner: "yaver",
    surfaces: ["web", "ios", "android", "tablet", "tvos", "android-tv", "watch", "car", "visionos", "xr", "remote-runner", "mcp"],
    companionOnlySurfaces: ["watch", "car"],
    route: "/remote-runtime",
    launchLabel: "Open runtime",
    manifestFile: "yaver.app.yaml",
    auth: { provider: "yaver-oauth", requiredInYaverBuild: true },
  },
  {
    id: "app_feedback_loop",
    slug: "feedback-loop",
    title: "Feedback Loop",
    subtitle: "Screenshots, bug capture, triage, and agent-routed fixes.",
    kind: "devtool",
    status: "prototype",
    owner: "yaver",
    surfaces: ["web", "ios", "android", "tablet", "remote-runner", "mcp"],
    route: "/(tabs)/shots",
    launchLabel: "Open feedback",
    manifestFile: "yaver.app.yaml",
    auth: { provider: "yaver-oauth", requiredInYaverBuild: true },
  },
  {
    id: "app_personal_health_agent",
    slug: "personal-health-agent",
    title: "Personal Health Agent",
    subtitle: "Health portal checks, reminders, and local-first summaries.",
    kind: "assistant",
    status: "planned",
    owner: "yaver",
    surfaces: ["web", "ios", "android", "tablet", "watch", "car", "remote-runner", "mcp"],
    companionOnlySurfaces: ["watch", "car"],
    route: "/remote-runtime?app=personal-health-agent",
    launchLabel: "Open health runtime",
    manifestFile: "yaver.app.yaml",
    auth: { provider: "yaver-oauth", requiredInYaverBuild: true },
  },
];

export function yaverNativeAppsForSurface(surface: YaverNativeSurface): YaverNativeCatalogApp[] {
  return YAVER_NATIVE_CATALOG.filter((app) => app.surfaces.includes(surface));
}

export function yaverNativePrimaryAppsForSurface(surface: YaverNativeSurface): YaverNativeCatalogApp[] {
  return yaverNativeAppsForSurface(surface).filter((app) => !(app.companionOnlySurfaces ?? []).includes(surface));
}

export function yaverNativeCompanionAppsForSurface(surface: YaverNativeSurface): YaverNativeCatalogApp[] {
  return yaverNativeAppsForSurface(surface).filter((app) => (app.companionOnlySurfaces ?? []).includes(surface));
}

export function yaverNativeSurfaceSummary(surface: YaverNativeSurface): string {
  const primary = yaverNativePrimaryAppsForSurface(surface);
  const companion = yaverNativeCompanionAppsForSurface(surface);
  const playable = primary.filter((app) => app.kind === "game").map((app) => app.title).join(", ");
  if (playable) return `${playable}${companion.length ? " plus companion approvals" : ""}`;
  if (primary.length) return primary.map((app) => app.title).join(", ");
  if (companion.length) return `${companion.length} companion surface${companion.length === 1 ? "" : "s"}`;
  return "No Yaver-native apps for this surface yet";
}
