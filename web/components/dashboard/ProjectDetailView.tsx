"use client";

import { useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import type { AgentClient, RemoteRuntimeCapabilities, RemoteRuntimeSession, RemoteRuntimeTarget, WorkspaceAppView } from "@/lib/agent-client";
import EnvironmentSwitcher from "./EnvironmentSwitcher";
import RemoteRuntimeViewer from "./RemoteRuntimeViewer";

type ProjectSummary = {
  name: string;
  path: string;
  branch?: string;
  framework?: string;
  frameworks?: string[];
  stack?: string;
  stacks?: string[];
  surfaces?: string[];
  testSurfaces?: string[];
  backend?: string;
  services?: string[];
  hosting?: string[];
  role?: string;
  executionMode?: string;
  primarySurface?: string;
  gitRemote?: string;
  tags?: string[];
};

type WorkspaceRepo = {
  name: string;
  path: string;
  branch?: string;
  remote?: string;
  dirty?: boolean;
  stack?: {
    type?: string;
    frameworks?: string[];
    services?: string[];
    actions?: string[];
  };
};

function basename(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).pop() || path;
}

function unique(values: Array<string | undefined>): string[] {
  return Array.from(new Set(values.map((v) => String(v || "").trim()).filter(Boolean)));
}

function supportsWebUI(project: ProjectSummary | null): boolean {
  const terms = unique([
    project?.framework,
    ...(project?.frameworks ?? []),
    project?.stack,
    ...(project?.stacks ?? []),
    ...(project?.surfaces ?? []),
    ...(project?.tags ?? []),
  ]).map((v) => v.toLowerCase());
  return terms.some((v) => ["web", "browser", "webview", "next", "nextjs", "vite", "react", "expo", "react-native", "flutter"].includes(v));
}

function projectTerms(project: ProjectSummary | null): Set<string> {
  const terms = new Set<string>();
  for (const raw of [
    project?.name,
    project?.path,
    project?.framework,
    ...(project?.frameworks ?? []),
    project?.stack,
    ...(project?.stacks ?? []),
    ...(project?.surfaces ?? []),
    ...(project?.testSurfaces ?? []),
    ...(project?.tags ?? []),
    project?.executionMode,
  ]) {
    const value = String(raw || "").trim().toLowerCase().replace(/\s+/g, "-").replace(/\./g, "");
    if (!value) continue;
    terms.add(value);
    for (const part of value.split(/[^a-z0-9]+/).filter(Boolean)) terms.add(part);
  }
  return terms;
}

function termsContain(terms: Set<string>, candidates: string[]): boolean {
  const all = Array.from(terms);
  return candidates.some((candidate) => terms.has(candidate) || all.some((term) => term.includes(candidate)));
}

function runtimeFrameworkForProject(project: ProjectSummary | null): string {
  const explicit = String(project?.framework || "").trim().toLowerCase();
  if (["expo", "react-native", "flutter", "swift", "kotlin", "browser", "desktop"].includes(explicit)) return explicit;
  const terms = projectTerms(project);
  if (termsContain(terms, ["web", "browser", "next", "nextjs", "vite"])) return "browser";
  if (termsContain(terms, ["flutter"])) return "flutter";
  if (termsContain(terms, ["expo"])) return "expo";
  if (termsContain(terms, ["react-native"])) return "react-native";
  if (termsContain(terms, ["swift"])) return "swift";
  if (termsContain(terms, ["kotlin"])) return "kotlin";
  return explicit;
}

function browserPreviewFrameworkForProject(project: ProjectSummary | null): string {
  const explicit = String(project?.framework || "").trim().toLowerCase().replace(/\s+/g, "-").replace(/\./g, "");
  if (["next", "nextjs", "vite", "react", "expo", "react-native", "flutter"].includes(explicit)) {
    return explicit === "next" ? "nextjs" : explicit;
  }
  const terms = projectTerms(project);
  if (["", "repo", "monorepo", "unknown"].includes(explicit)) {
    if (termsContain(terms, ["next", "nextjs"])) return "nextjs";
    if (termsContain(terms, ["vite"])) return "vite";
    if (termsContain(terms, ["web", "browser"])) return "react";
  }
  if (termsContain(terms, ["expo"])) return "expo";
  if (termsContain(terms, ["react-native"])) return "react-native";
  if (termsContain(terms, ["flutter"])) return "flutter";
  if (termsContain(terms, ["next", "nextjs"])) return "nextjs";
  if (termsContain(terms, ["vite"])) return "vite";
  if (termsContain(terms, ["react"])) return "react";
  return project?.framework || "";
}

function isMonorepoProject(project: ProjectSummary | null): boolean {
  return String(project?.framework || project?.stack || "").trim().toLowerCase() === "monorepo";
}

async function monorepoWebAppName(client: AgentClient, project: ProjectSummary | null): Promise<string | undefined> {
  if (!project || !isMonorepoProject(project)) return undefined;
  try {
    const apps = await client.getWorkspaceApps("web", project.path);
    const app = apps.find((candidate) => candidate.exists && candidate.name === "web") ||
      apps.find((candidate) => candidate.exists && candidate.kind === "web") ||
      apps.find((candidate) => candidate.exists);
    return app?.name;
  } catch {
    return undefined;
  }
}

function signedBundlePreviewUrl(client: AgentClient, bundleUrl?: string): string | null {
  if (!bundleUrl) return null;
  try {
    const parsed = new URL(bundleUrl, "http://agent.local");
    if (!parsed.searchParams.has("sig") && !parsed.searchParams.has("signature") && !parsed.searchParams.has("token")) {
      return null;
    }
  } catch {
    return null;
  }
  return client.webBundlePreviewUrl(bundleUrl);
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => window.setTimeout(resolve, ms));
}

async function waitForDevPreviewUrl(client: AgentClient, bundleUrl?: string): Promise<{ url: string; note: string }> {
  const signedUrl = signedBundlePreviewUrl(client, bundleUrl);
  if (signedUrl) return { url: signedUrl, note: "Web UI bundle ready." };
  let lastError = "";
  // ── The 12-second cliff ────────────────────────────────────────────────────
  // This loop used to run 16 times at 750ms = TWELVE SECONDS, then declare
  // failure. A cold web compile takes 30s–3min: the agent itself allows 120s
  // just for the port to bind, and Expo/Next/Flutter spend longer than that on
  // a first build. So the dashboard reported
  //
  //   web ui failed: Preview URL returned HTTP 503
  //
  // 23 seconds after the click (observed 2026-07-25 on yaver.io) while the dev
  // server was compiling normally and would have served a moment later. The
  // 503 was the agent being HONEST about starting up; the client turned it into
  // a verdict.
  //
  // Two rules this now follows: a transient "still starting" is not a failure,
  // and a wait must say how long it has been waiting (that is why the final
  // message carries elapsed seconds instead of a bare HTTP code).
  const startedAt = Date.now();
  const budgetMs = 180_000;
  for (let i = 0; Date.now() - startedAt < budgetMs; i++) {
    const status = await client.getDevServerStatus();
    if (status?.error) lastError = status.error;
    if (status?.webPort && status.webPort > 0 && client.devWebPreviewUrl) {
      const probed = await probePreviewUrl(client.devWebPreviewUrl);
      if (probed.ok) return { url: client.devWebPreviewUrl, note: status.servingLabel || "Web UI running." };
      lastError = probed.error;
    }
    if ((status?.running || status?.serving || (status?.port ?? 0) > 0) && client.devPreviewUrl) {
      const probed = await probePreviewUrl(client.devPreviewUrl);
      if (probed.ok) return { url: client.devPreviewUrl, note: status?.servingLabel || "Web UI running." };
      lastError = probed.error;
    }
    await sleep(i < 8 ? 750 : 2000);
  }
  const waitedSec = Math.round((Date.now() - startedAt) / 1000);
  throw new Error(
    lastError
      ? `Still no browser preview after ${waitedSec}s — last thing the agent said: ${lastError}. ` +
        `A first web build can take longer than this; the dev server may still be compiling — check the runtime console.`
      : `The agent accepted the Web UI start request, but no browser preview was serving after ${waitedSec}s.`,
  );
}

async function probePreviewUrl(url: string): Promise<{ ok: true } | { ok: false; error: string }> {
  try {
    const res = await fetch(url, { headers: { Accept: "text/html,application/xhtml+xml" }, cache: "no-store" });
    const text = await res.clone().text().catch(() => "");
    if (!res.ok) return { ok: false, error: `Preview URL returned HTTP ${res.status}` };
    if (/no dev server running|dev bundle URL must be signed/i.test(text)) {
      return { ok: false, error: text.trim().slice(0, 180) || "Preview URL is not ready." };
    }
    return { ok: true };
  } catch (err) {
    return { ok: false, error: err instanceof Error ? err.message : "Preview URL is not reachable yet." };
  }
}

// ── Honest labels over raw detector enums ────────────────────────────────────
// The agent reports machine enums (surfaces: "tv"/"vision", primarySurface:
// "none", executionMode: "unsupported"). These used to be dumped verbatim as
// capability chips, so prod rendered "tv, vision, none, unsupported" as if
// they were features. Rules now: sentinel values ("none"/"unsupported"/
// "unknown") NEVER render as chips, known enums get human labels, and a value
// we cannot explain is shown as-is rather than hidden — an unrecognized
// detector output is still information.
const PLATFORM_CHIP_LABELS: Record<string, string> = {
  mobile: "Mobile",
  phone: "Mobile",
  tablet: "Mobile",
  ios: "Mobile",
  android: "Mobile",
  "ios-simulator": "Mobile",
  "android-emulator": "Mobile",
  web: "Web",
  browser: "Web",
  backend: "Backend",
  watch: "Watch",
  watchos: "Watch",
  "watchos-simulator": "Watch",
  wear: "Watch",
  wearos: "Watch",
  "android-wear": "Watch",
  tv: "TV",
  tvos: "TV",
  "tvos-simulator": "TV",
  "android-tv": "TV",
  appletv: "TV",
  car: "Car",
  carplay: "Car",
  "android-auto": "Car",
  vision: "Vision / XR",
  visionos: "Vision / XR",
  "visionos-simulator": "Vision / XR",
  "android-xr": "Vision / XR",
  xr: "Vision / XR",
  glass: "Vision / XR",
  desktop: "Desktop",
};

// Transports and detector sentinels are not platforms — never chip them.
const PLATFORM_CHIP_NOISE = new Set([
  "", "none", "unsupported", "unknown", "repo", "monorepo",
  "webrtc", "webview", "hermes", "dev-server", "redroid", "simulator", "emulator",
]);

// The surfaces users vibe on lead; Backend closes the row. Unrecognized
// detector output still renders (it is information), but after the knowns.
const PLATFORM_CHIP_ORDER = ["Web", "Mobile", "Watch", "TV", "Car", "Vision / XR", "Desktop", "Backend"];

function sortPlatformChips(chips: Iterable<string>): string[] {
  const rank = (chip: string) => {
    const index = PLATFORM_CHIP_ORDER.indexOf(chip);
    return index === -1 ? PLATFORM_CHIP_ORDER.length : index;
  };
  return Array.from(new Set(chips)).sort((a, b) => rank(a) - rank(b) || a.localeCompare(b));
}

function platformChips(project: ProjectSummary | null): string[] {
  const chips: string[] = [];
  for (const value of unique([...(project?.surfaces ?? []), ...(project?.testSurfaces ?? [])])) {
    const key = value.toLowerCase();
    if (PLATFORM_CHIP_NOISE.has(key)) continue;
    chips.push(PLATFORM_CHIP_LABELS[key] ?? value);
  }
  return sortPlatformChips(chips);
}

// ── Frontend-first app classification ────────────────────────────────────────
// Yaver's projects flow exists to get a fullstack repo to a FRONTEND you can
// vibe on. Classify each workspace app by the surface it renders to so the
// vibe-able apps lead and backend/tooling reads as context, not as an equal
// choice among fourteen rows.
function appSurfaceChips(app: WorkspaceAppView): string[] {
  const fw = String(app.framework || app.stack || "").trim().toLowerCase();
  const kind = String(app.kind || "").trim().toLowerCase();
  const out = new Set<string>();
  // The agent's per-app surfaces (workspace_http.go) are the AUTHORITATIVE
  // answer — they come from the workspace manifest + the app's own config
  // files, so tvos/watchos/visionos/wear-os (swift/kotlin stacks a framework
  // string cannot express) surface as TV/Watch/Vision/Watch. Map through the
  // same PLATFORM_CHIP_LABELS the project row uses; "webrtc"/"simulator"
  // transport words have no label and are skipped.
  for (const surface of [...(app.surfaces ?? []), ...(app.testSurfaces ?? [])]) {
    const label = PLATFORM_CHIP_LABELS[String(surface).toLowerCase()];
    if (label) out.add(label);
  }
  if (["nextjs", "next", "next.js", "vite", "react", "remix", "astro", "svelte", "nuxt", "vue"].includes(fw) || kind === "web") out.add("Web");
  if (["expo", "react-native"].includes(fw)) { out.add("Mobile"); out.add("Web"); }
  if (fw === "flutter") { out.add("Mobile"); if (kind === "web") out.add("Web"); }
  if (kind === "hybrid") { out.add("Mobile"); out.add("Web"); }
  return sortPlatformChips(out);
}

// runtimeTargetForAppSurface maps an app's detected surfaces to the remote
// runtime simulator target that streams that surface over WebRTC. One app row
// (tvos) → one Open button (Apple TV Simulator); the agent's target probe
// still gates on host OS + installed runtimes, so the button is honest about
// availability when it opens.
function runtimeTargetForAppSurface(surfaces: string[]): string | null {
  for (const raw of surfaces) {
    switch (String(raw).toLowerCase()) {
      case "tvos-simulator":
      case "tv":
      case "tvos":
        return "tvos-simulator";
      case "watchos-simulator":
      case "watch":
      case "watchos":
        return "watchos-simulator";
      case "visionos-simulator":
      case "vision":
      case "visionos":
        return "visionos-simulator";
      case "android-wear":
      case "wear":
      case "wearos":
        return "android-wear";
      case "ios-simulator":
      case "mobile":
      case "ios":
        return "ios-simulator";
      case "android-emulator":
      case "android":
        return "android-emulator";
    }
  }
  return null;
}

function isFrontendApp(app: WorkspaceAppView): boolean {
  return appSurfaceChips(app).length > 0;
}

function executionModeLabel(mode?: string): string | null {
  switch (String(mode || "").trim().toLowerCase()) {
    case "rn-hermes": return "React Native bundle (Hermes)";
    case "web-webview": return "Web preview (dev server)";
    case "native-webrtc": return "Native build, streamed over WebRTC";
    case "web": return "Web dev server";
    default: return null; // "unsupported" / unknown: say nothing rather than echo the enum
  }
}

// Derive an honest primary target instead of echoing "none · unsupported".
// Prefer the agent's primarySurface when it names a real lane; otherwise fall
// back to what the platform chips imply; otherwise return null so the row can
// explain itself instead of rendering a meaningless value.
function primaryTargetLabel(project: ProjectSummary | null): string | null {
  switch (String(project?.primarySurface || "").trim().toLowerCase()) {
    case "hermes": return "Phone · native bundle";
    case "webview": return "Browser · web view";
    case "webrtc": return "Simulator · WebRTC stream";
    case "web": return "Browser";
    case "mobile": return "Phone";
  }
  const chips = platformChips(project);
  if (chips.includes("Web")) return "Browser";
  if (chips.includes("Mobile")) return "Phone";
  return null;
}

// Role row: only render detector output that means something. "unknown ·
// backend not detected" (observed on prod) told the user nothing — when the
// backend is absent that is a fact worth one plain sentence, and when the role
// itself is unknown the row is dropped entirely.
function roleInfo(project: ProjectSummary | null): { value: string; sub: string } | null {
  const role = String(project?.role || "").trim().toLowerCase();
  const backend = String(project?.backend || "").trim();
  if (!role || role === "unknown") {
    return backend ? { value: "project", sub: backend } : null;
  }
  if (backend) return { value: project?.role || role, sub: backend };
  const frontendish = ["web", "mobile", "frontend", "app"].some((v) => role.includes(v));
  return { value: project?.role || role, sub: frontendish ? "No backend detected — frontend-only project" : "" };
}

function projectFromRepo(repo: WorkspaceRepo): ProjectSummary {
  const frameworks = repo.stack?.frameworks ?? [];
  const services = repo.stack?.services ?? [];
  const actions = repo.stack?.actions ?? [];
  const stackType = repo.stack?.type;
  const lower = new Set([stackType, ...frameworks, ...actions].filter(Boolean).map((v) => String(v).toLowerCase()));
  const surfaces = new Set<string>();
  if (lower.has("monorepo")) {
    surfaces.add("web");
    surfaces.add("mobile");
    surfaces.add("backend");
  }
  if (["expo", "react-native", "flutter", "swift", "kotlin", "mobile"].some((v) => lower.has(v))) surfaces.add("mobile");
  if (["next.js", "nextjs", "vite", "react", "web", "dev-server"].some((v) => lower.has(v))) surfaces.add("web");
  if (["go", "python", "rust", "backend"].some((v) => lower.has(v))) surfaces.add("backend");
  return {
    name: repo.name || basename(repo.path),
    path: repo.path,
    branch: repo.branch,
    framework: stackType === "monorepo" ? "monorepo" : frameworks[0] || stackType,
    frameworks,
    stack: stackType,
    stacks: [stackType, ...frameworks].filter(Boolean) as string[],
    surfaces: Array.from(surfaces),
    services,
    hosting: services.filter((v) => ["cloudflare", "vercel", "netlify"].includes(v)),
    role: stackType === "monorepo" ? "repo" : stackType || "repo",
    executionMode: actions.includes("hot-reload") ? "native-webrtc" : actions.includes("dev-server") ? "web" : undefined,
    primarySurface: surfaces.has("web") ? "web" : surfaces.has("mobile") ? "mobile" : stackType,
    gitRemote: repo.remote,
    tags: [stackType, ...frameworks, ...services, ...actions, repo.dirty ? "dirty" : undefined].filter(Boolean) as string[],
  };
}

function targetGroup(target: RemoteRuntimeTarget): "browser" | "simulator" | "container" | "device" | "computer" | "advanced" | "unavailable" {
  if (!target.enabled) return "unavailable";
  const id = String(target.id || "").toLowerCase();
  const surface = String(target.surface || "").toLowerCase();
  if (surface === "browser" || id === "browser-window") return "browser";
  if (id.includes("redroid")) return "container";
  if (id.includes("device")) return "device";
  if (id === "physical-pc-kvm" || surface === "desktop") return "computer";
  if (["phone", "tablet"].includes(surface) && (id.includes("simulator") || id.includes("emulator"))) return "simulator";
  return "advanced";
}

const targetGroupLabels: Record<ReturnType<typeof targetGroup>, string> = {
  browser: "Browser",
  simulator: "Phone / tablet simulators",
  container: "Android containers",
  device: "Physical devices",
  computer: "Computers",
  advanced: "Watch / TV / XR / car",
  unavailable: "Unavailable",
};

export default function ProjectDetailView({ client, directory, onClose }: { client: AgentClient; directory: string; onClose: () => void }) {
  const [project, setProject] = useState<ProjectSummary | null>(null);
  const [caps, setCaps] = useState<RemoteRuntimeCapabilities | null>(null);
  const [session, setSession] = useState<RemoteRuntimeSession | null>(null);
  const [webPreviewUrl, setWebPreviewUrl] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const [workspaceApps, setWorkspaceApps] = useState<WorkspaceAppView[] | null>(null);

  const slug = basename(directory);
  const stackLabels = useMemo(() => unique([
    project?.framework,
    ...(project?.frameworks ?? []),
    project?.stack,
    ...(project?.stacks ?? []),
    project?.backend,
    ...(project?.services ?? []),
    ...(project?.hosting ?? []),
  ]).filter((v) => !PLATFORM_CHIP_NOISE.has(v.toLowerCase())), [project]);
  // Stack detection runs FIRST, from the strongest source available: the
  // workspace-app rows name real frameworks (nextjs, expo, convex, go…) even
  // when the project row carries only "monorepo" — which the chip filter
  // rightly treats as noise. Falling through to "Stack not detected yet"
  // while the apps list below plainly showed the stack was the bug.
  const appStackLabels = useMemo(() => unique(
    (workspaceApps ?? []).flatMap((app) => [app.framework, app.stack]),
  ).filter((v) => !PLATFORM_CHIP_NOISE.has(v.toLowerCase())), [workspaceApps]);
  const effectiveStackLabels = stackLabels.length ? stackLabels : appStackLabels;
  const frontendApps = useMemo(() => (workspaceApps ?? []).filter(isFrontendApp), [workspaceApps]);
  const backendApps = useMemo(() => (workspaceApps ?? []).filter((app) => !isFrontendApp(app)), [workspaceApps]);
  const platformLabels = useMemo(() => sortPlatformChips([
    ...platformChips(project),
    ...(workspaceApps ?? []).flatMap(appSurfaceChips),
    ...(backendApps.length > 0 ? ["Backend"] : []),
  ]), [project, workspaceApps, backendApps]);
  // null project = still asking the agent; monorepo apps still loading counts
  // as detecting too. "Not detected" is a verdict — only render it once the
  // probes have actually come back empty.
  const detectingStack = !project || (isMonorepoProject(project) && workspaceApps === null);
  const runsAs = executionModeLabel(project?.executionMode);
  const primaryLabel = primaryTargetLabel(project);
  const role = roleInfo(project);
  const canVibeWeb = supportsWebUI(project) || frontendApps.some((app) => appSurfaceChips(app).includes("Web"));
  const defaultWebApp = useMemo(() =>
    frontendApps.find((app) => app.name === "web" && appSurfaceChips(app).includes("Web")) ||
    frontendApps.find((app) => appSurfaceChips(app).includes("Web")),
  [frontendApps]);

  // Route THROUGH the vibe page, not around it: picking a browser/frontend
  // target hands off to /dashboard?tab=vibe with this project preselected and
  // the web preview auto-started, so the user lands where prompting and the
  // live preview live together instead of a dead-end iframe on this tab.
  function goVibe(appName?: string) {
    const url = new URL(window.location.href);
    url.searchParams.set("tab", "vibe");
    url.searchParams.set("project", directory);
    url.searchParams.set("preview", "web");
    if (appName) url.searchParams.set("app", appName);
    else url.searchParams.delete("app");
    window.history.pushState(null, "", url.toString());
    window.dispatchEvent(new PopStateEvent("popstate"));
  }

  useEffect(() => {
    let mounted = true;
    (async () => {
      try {
        const [rows, repos] = await Promise.all([
          client.listProjects(),
          client.listWorkspaceRepos(),
        ]);
        const found = rows.find((p) => p.path === directory) || rows.find((p) => basename(p.path) === slug);
        const repo = repos.find((p) => p.path === directory) || repos.find((p) => basename(p.path) === slug);
        if (mounted) setProject(found || (repo ? projectFromRepo(repo) : { name: slug, path: directory }));
      } catch {
        if (mounted) setProject({ name: slug, path: directory });
      }
    })();
    return () => { mounted = false; };
  }, [client, directory, slug]);

  // Monorepo sub-apps for the Target step — the same /workspace/apps rows the
  // dev-server start already resolves against, surfaced so the user can see
  // WHICH app the browser lane will boot instead of guessing.
  useEffect(() => {
    let mounted = true;
    if (!isMonorepoProject(project)) { setWorkspaceApps(null); return; }
    (async () => {
      try {
        const apps = await client.getWorkspaceApps(undefined, directory);
        if (mounted) setWorkspaceApps(apps.filter((app) => app.exists));
      } catch {
        if (mounted) setWorkspaceApps(null);
      }
    })();
    return () => { mounted = false; };
  }, [client, project, directory]);

  async function openWebUI() {
    setBusy("web");
    setMessage(null);
    try {
      const framework = browserPreviewFrameworkForProject(project);
      const staticBundleFramework = ["expo", "react-native"].includes(framework.toLowerCase());
      if (staticBundleFramework) {
        setMessage(`Building ${project?.name || slug} web bundle...`);
        const built = await client.buildWebJSBundle({ projectName: project?.name || slug, projectPath: directory });
        if (!built.ok) throw new Error(built.error || "Could not build Web UI bundle.");
        const signedUrl = client.webBundlePreviewUrl(built.bundleUrl);
        if (!signedUrl) throw new Error("No signed Web UI bundle URL is available.");
        setWebPreviewUrl(signedUrl);
        setMessage(`Web UI bundle ready: ${built.fileCount} files.`);
        return;
      }
      const app = await monorepoWebAppName(client, project);
      const response = await client.startDevServer(app ? {
        app,
        root: directory,
        platform: "web",
        surface: "web-reload",
      } : {
        framework,
        workDir: directory,
        platform: "web",
        surface: "web-reload",
      });
      if (response.mode === "static-bundle") {
        const existingSignedUrl = signedBundlePreviewUrl(client, response.bundleUrl);
        if (response.bundleReady && existingSignedUrl) {
          setWebPreviewUrl(existingSignedUrl);
          setMessage(response.bundleHint || "Web UI bundle ready.");
          return;
        }
        const built = await client.buildWebJSBundle({ projectName: project?.name || slug, projectPath: directory });
        if (!built.ok) throw new Error(built.error || "Could not build Web UI bundle.");
        const signedUrl = client.webBundlePreviewUrl(built.bundleUrl);
        if (!signedUrl) throw new Error("No signed Web UI bundle URL is available.");
        setWebPreviewUrl(signedUrl);
        setMessage(`Web UI bundle ready: ${built.fileCount} files.`);
        return;
      }
      const preview = await waitForDevPreviewUrl(client, response.bundleUrl);
      setWebPreviewUrl(preview.url);
      setMessage(response.bundleHint || preview.note);
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "Could not open Web UI.");
    } finally {
      setBusy(null);
    }
  }

  async function loadRuntimeTargets() {
    setBusy("targets");
    setMessage(null);
    setSession(null);
    try {
      // refresh=1: an explicit "Load simulator targets" click probes the box
      // fresh instead of replaying the agent's 2-minute caps cache.
      const next = await client.getRemoteRuntimeCapabilities(directory, runtimeFrameworkForProject(project), true);
      setCaps({ ...next, targets: [...(next.targets || [])] });
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "Could not load render targets.");
    } finally {
      setBusy(null);
    }
  }

  async function openRuntimeTarget(target: RemoteRuntimeTarget) {
    setBusy(target.id);
    setMessage(null);
    try {
      const next = await client.startRemoteRuntimeSession(directory, runtimeFrameworkForProject(project), target.id, "direct-webrtc");
      setSession(next);
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "Could not open runtime target.");
    } finally {
      setBusy(null);
    }
  }

  // One-click simulator lane from an app row: tvos → Apple TV Simulator over
  // WebRTC, watchos → Watch, visionos → Vision, wear-os → Wear emulator.
  // The target probe gates on host OS + installed runtimes, so this opens the
  // session if the box can stream it and says why not when it can't.
  async function openAppRuntimeTarget(app: WorkspaceAppView) {
    setBusy(`app:${app.name}`);
    setMessage(null);
    const targetId = runtimeTargetForAppSurface([...(app.surfaces ?? []), ...(app.testSurfaces ?? [])]);
    if (!targetId) {
      setMessage(`${app.name} has no streamable simulator surface — use Load simulator targets to see every lane for this machine.`);
      setBusy(null);
      return;
    }
    try {
      const next = await client.startRemoteRuntimeSession(directory, runtimeFrameworkForProject(project), targetId, "direct-webrtc");
      setSession(next);
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "Could not open runtime target.");
    } finally {
      setBusy(null);
    }
  }

  const groupedTargets = useMemo(() => {
    const groups: Record<string, RemoteRuntimeTarget[]> = {};
    for (const target of caps?.targets ?? []) {
      const group = targetGroup(target);
      groups[group] = [...(groups[group] ?? []), target];
    }
    return groups;
  }, [caps]);

  const capsModeLabel = caps ? executionModeLabel(caps.executionMode) : null;
  const step2Done = Boolean(caps || webPreviewUrl || session);
  const step3Live = Boolean(webPreviewUrl || session);

  return (
    <div className="min-h-full bg-[#f2f4f7] p-4 text-[#1f2933] dark:bg-[#101318] dark:text-[#e6e8ec]">
      <div className="mb-4 flex items-center gap-3">
        <button onClick={onClose} className="text-sm font-medium text-[#475467] hover:text-[#1f2933] dark:text-[#a7b0bc] dark:hover:text-white">← Projects</button>
        <div className="min-w-0 flex-1">
          <h2 className="truncate font-mono text-xl font-semibold text-[#1f2933] dark:text-[#e6e8ec]">{project?.name || slug}</h2>
          <div className="truncate font-mono text-xs text-[#667085] dark:text-[#9aa3af]">{directory}</div>
        </div>
        {canVibeWeb ? (
          <button
            onClick={() => goVibe(defaultWebApp?.name)}
            className="shrink-0 rounded-md bg-[#1f2933] px-3 py-1.5 text-xs font-semibold text-white hover:bg-[#344054] dark:bg-[#e6e8ec] dark:text-[#101318] dark:hover:bg-white"
          >
            Vibe on this project →
          </button>
        ) : null}
      </div>

      <div className="grid gap-3 xl:grid-cols-[minmax(0,1fr)_340px]">
        <main>
          <ol className="list-none">
            {/* ── Step 1 · Stack — what this project is ──────────────────── */}
            <WizardStep step={1} title="Stack" hint="what was detected in this repo" state={effectiveStackLabels.length ? "done" : "active"}>
              <div className="rounded-md border border-[#d7dce3] bg-white p-3 dark:border-[#2a3039] dark:bg-[#161b22]">
                {effectiveStackLabels.length ? (
                  <ChipRow values={effectiveStackLabels} />
                ) : (
                  <div className="text-xs text-[#98a2b3] dark:text-[#6b7482]">
                    {detectingStack ? "Detecting stack from the repo…" : "Stack not detected — no framework markers found in this repo."}
                  </div>
                )}
                {platformLabels.length > 0 ? (
                  <>
                    <SectionLabel className="mt-3">Platforms</SectionLabel>
                    <ChipRow values={platformLabels} />
                  </>
                ) : null}
                <div className="mt-3 grid gap-3 md:grid-cols-3">
                  <Meta label="Git" value={project?.branch || "unknown"} sub={project?.gitRemote || "remote not detected"} />
                  {primaryLabel ? (
                    <Meta label="Primary target" value={primaryLabel} sub={runsAs || ""} />
                  ) : (
                    <Meta label="Primary target" value="Not detected yet" sub="Probing targets in step 2 will find one" muted />
                  )}
                  {role ? <Meta label="Role" value={role.value} sub={role.sub} /> : null}
                </div>
                {caps ? (
                  <div className="mt-3 border-t border-[#eef1f5] pt-2 text-xs text-[#667085] dark:border-[#232a33] dark:text-[#9aa3af]">
                    Agent probe: {[caps.framework, capsModeLabel, caps.currentHostClass].filter(Boolean).join(" · ")}
                    {caps.cached ? " · cached" : caps.probeDurationMs ? ` · probed in ${Math.round(caps.probeDurationMs / 1000)}s` : ""}
                    {caps.executionMode === "unsupported" ? " · no in-container lane — use a streamed target below" : ""}
                  </div>
                ) : null}
              </div>
            </WizardStep>

            {/* ── Step 2 · Target — where to run it ──────────────────────── */}
            <WizardStep step={2} title="Target" hint="pick where this project should run" state={step2Done ? "done" : project ? "active" : "todo"}>
              <div className="space-y-2">
                <div className="grid gap-2 md:grid-cols-2">
                  <ActionCard
                    title="Vibe in browser"
                    meta="opens the Vibe page · dev server · live preview"
                    disabled={!canVibeWeb || busy !== null}
                    onClick={() => goVibe(defaultWebApp?.name)}
                  />
                  <ActionCard
                    title="Load simulator targets"
                    meta="WebRTC · simulator / browser / Redroid"
                    disabled={busy !== null}
                    busy={busy === "targets"}
                    onClick={loadRuntimeTargets}
                  />
                </div>
                {canVibeWeb ? (
                  <button
                    onClick={() => void openWebUI()}
                    disabled={busy !== null}
                    className="text-xs text-[#667085] underline decoration-dotted underline-offset-2 hover:text-[#1f2933] disabled:cursor-not-allowed disabled:opacity-45 dark:text-[#9aa3af] dark:hover:text-white"
                  >
                    {busy === "web" ? "Starting inline preview…" : "Quick inline preview here instead (skips the Vibe page)"}
                  </button>
                ) : null}
                {workspaceApps && workspaceApps.length > 0 ? (
                  <div className="rounded-md border border-[#d7dce3] bg-white p-3 dark:border-[#2a3039] dark:bg-[#161b22]">
                    {frontendApps.length > 0 ? (
                      <>
                        <SectionLabel>Frontend apps — vibe on these</SectionLabel>
                        <div className="space-y-1">
                          {frontendApps.map((app) => (
                            <div key={app.name} className="flex items-center gap-2 text-xs">
                              <span className="font-mono font-medium text-[#344054] dark:text-[#d7dce3]">{app.name}</span>
                              <span className="text-[#667085] dark:text-[#9aa3af]">{app.framework || app.stack || "app"}</span>
                              <span className="flex gap-1">
                                {appSurfaceChips(app).map((surface) => (
                                  <span key={surface} className="rounded border border-[#d7dce3] bg-[#f8fafc] px-1.5 py-0.5 text-[10px] font-medium text-[#475467] dark:border-[#2a3039] dark:bg-[#121720] dark:text-[#c5ccd6]">{surface}</span>
                                ))}
                              </span>
                              <span className="flex-1" />
                              {appSurfaceChips(app).includes("Web") ? (
                                <button
                                  onClick={() => goVibe(app.name)}
                                  disabled={busy !== null}
                                  className="rounded-md bg-[#1f2933] px-2.5 py-1 text-[11px] font-semibold text-white disabled:cursor-not-allowed disabled:opacity-35 dark:bg-[#e6e8ec] dark:text-[#101318]"
                                >
                                  Vibe
                                </button>
                              ) : null}
                              {runtimeTargetForAppSurface([...(app.surfaces ?? []), ...(app.testSurfaces ?? [])]) ? (
                                <button
                                  onClick={() => void openAppRuntimeTarget(app)}
                                  disabled={busy !== null}
                                  title={`Stream ${app.name} in a simulator over WebRTC`}
                                  className="rounded-md border border-[#d7dce3] bg-white px-2.5 py-1 text-[11px] font-semibold text-[#475467] hover:bg-[#f8fafc] disabled:cursor-not-allowed disabled:opacity-35 dark:border-[#2a3039] dark:bg-[#161b22] dark:text-[#d7dce3]"
                                >
                                  {busy === `app:${app.name}` ? "Opening…" : "Open"}
                                </button>
                              ) : null}
                            </div>
                          ))}
                        </div>
                      </>
                    ) : null}
                    {backendApps.length > 0 ? (
                      <>
                        <SectionLabel className={frontendApps.length > 0 ? "mt-3" : ""}>Backend &amp; tooling</SectionLabel>
                        <div className="text-xs text-[#98a2b3] dark:text-[#6b7482]">
                          {backendApps.map((app) => `${app.name} (${app.framework || app.stack || "app"})`).join(" · ")}
                        </div>
                      </>
                    ) : null}
                    <div className="mt-2 text-xs text-[#98a2b3] dark:text-[#6b7482]">Vibe opens the app on the Vibe page with the preview attached; simulator targets pick per app.</div>
                  </div>
                ) : null}
                {message ? <div className="rounded-md border border-[#d7dce3] bg-white px-3 py-2 text-xs text-[#475467] dark:border-[#2a3039] dark:bg-[#161b22] dark:text-[#c5ccd6]">{message}</div> : null}
                {busy === "targets" && !caps ? (
                  <div className="rounded-md border border-[#d7dce3] bg-white px-3 py-2 text-xs text-[#475467] dark:border-[#2a3039] dark:bg-[#161b22] dark:text-[#c5ccd6]">
                    Probing browser, simulator, emulator, Redroid, and physical-device render lanes from this machine...
                  </div>
                ) : null}
                {caps ? (
                  <div className="space-y-4 pt-1">
                    {(["browser", "simulator", "container", "device", "advanced", "unavailable"] as const).map((group) => {
                      const targets = groupedTargets[group] ?? [];
                      if (targets.length === 0) return null;
                      return (
                        <div key={group} className="space-y-2">
                          <SectionLabel>{targetGroupLabels[group]}</SectionLabel>
                          <div className="grid gap-2 md:grid-cols-2">
                            {targets.map((target) => (
                              <div key={target.id} className="rounded-md border border-[#d7dce3] bg-white p-3 dark:border-[#2a3039] dark:bg-[#161b22]">
                                <div className="flex items-start justify-between gap-3">
                                  <div className="min-w-0">
                                    <div className="truncate text-sm font-medium text-[#1f2933] dark:text-[#e6e8ec]">{target.label}</div>
                                    <div className="mt-1 text-xs text-[#667085] dark:text-[#9aa3af]">{target.surface || "runtime"} · {target.id} · {target.requiredCli || "tools"}</div>
                                  </div>
                                  <button
                                    disabled={!target.enabled || busy !== null}
                                    onClick={() => void openRuntimeTarget(target)}
                                    aria-label={`${target.enabled ? "Open" : "Unavailable"} ${target.label} (${target.id})`}
                                    className="rounded-md bg-[#1f2933] px-3 py-1.5 text-xs font-semibold text-white disabled:cursor-not-allowed disabled:opacity-35"
                                  >
                                    {busy === target.id ? "Opening..." : target.enabled ? "Open" : "Unavailable"}
                                  </button>
                                </div>
                                {target.reason ? <div className="mt-2 text-xs text-[#667085] dark:text-[#9aa3af]">{target.reason}</div> : null}
                                {target.checks?.length ? (
                                  <div className="mt-2 grid gap-1">
                                    {target.checks.map((check) => (
                                      <div key={`${target.id}-${check.id}`} className="flex min-w-0 items-start gap-2 text-[11px] text-[#667085] dark:text-[#9aa3af]">
                                        <span className={`mt-0.5 h-1.5 w-1.5 shrink-0 rounded-full ${check.ok ? "bg-emerald-500" : "bg-rose-500"}`} />
                                        <span className="min-w-0 truncate">
                                          {check.label}{check.reason ? ` · ${check.reason}` : ""}
                                        </span>
                                      </div>
                                    ))}
                                  </div>
                                ) : null}
                              </div>
                            ))}
                          </div>
                        </div>
                      );
                    })}
                  </div>
                ) : null}
              </div>
            </WizardStep>

            {/* ── Step 3 · Render — watch it run ─────────────────────────── */}
            <WizardStep step={3} title="Render" hint="the live preview" state={step3Live ? "active" : "todo"} isLast>
              {webPreviewUrl ? (
                <div className="space-y-2">
                  <div className="flex items-center justify-between">
                    <SectionLabel>Web UI</SectionLabel>
                    <button onClick={() => setWebPreviewUrl(null)} className="rounded-md border border-[#d7dce3] bg-white px-2 py-1 text-xs text-[#475467] dark:border-[#2a3039] dark:bg-[#161b22] dark:text-[#d7dce3]">Close</button>
                  </div>
                  <iframe src={webPreviewUrl} title="Project Web UI preview" className="h-[560px] w-full rounded-md border border-[#d7dce3] bg-white dark:border-[#2a3039]" />
                </div>
              ) : null}
              {session ? (
                <div className="space-y-2">
                  <SectionLabel>{session.targetLabel || "Remote runtime"}</SectionLabel>
                  <RemoteRuntimeViewer session={session} onSessionChange={setSession} />
                </div>
              ) : null}
              {!webPreviewUrl && !session ? (
                <div className="rounded-md border border-dashed border-[#c9d0da] px-3 py-4 text-xs text-[#98a2b3] dark:border-[#333b46] dark:text-[#6b7482]">
                  Pick a target in step 2 — the preview renders here.
                </div>
              ) : null}
            </WizardStep>
          </ol>
        </main>

        <aside className="space-y-3">
          <section className="rounded-md border border-[#d7dce3] bg-white p-3 dark:border-[#2a3039] dark:bg-[#161b22]">
            <SectionLabel>Environment</SectionLabel>
            <EnvironmentSwitcher directory={directory} variant="bare" />
          </section>
        </aside>
      </div>
    </div>
  );
}

// ── Wizard rail ──────────────────────────────────────────────────────────────
// The stack → target → render flow really is a sequence, so the numbered rail
// encodes state, not decoration: a filled marker with an inline-SVG check means
// the step has produced its output, an outlined marker is where the user is,
// and a muted one is not reachable yet.
function WizardStep({ step, title, hint, state, isLast, children }: {
  step: number;
  title: string;
  hint: string;
  state: "done" | "active" | "todo";
  isLast?: boolean;
  children: ReactNode;
}) {
  const marker = state === "done"
    ? "border-[#1f2933] bg-[#1f2933] text-white dark:border-[#e6e8ec] dark:bg-[#e6e8ec] dark:text-[#101318]"
    : state === "active"
      ? "border-[#1f2933] bg-white text-[#1f2933] dark:border-[#e6e8ec] dark:bg-[#161b22] dark:text-[#e6e8ec]"
      : "border-[#c9d0da] bg-white text-[#98a2b3] dark:border-[#333b46] dark:bg-[#161b22] dark:text-[#6b7482]";
  return (
    <li className={`relative pl-10 ${isLast ? "" : "pb-5"}`}>
      {isLast ? null : <span aria-hidden className="absolute bottom-0 left-[13px] top-8 w-px bg-[#d7dce3] dark:bg-[#2a3039]" />}
      <span aria-hidden className={`absolute left-0 top-0 flex h-7 w-7 items-center justify-center rounded-full border text-xs font-semibold ${marker}`}>
        {state === "done" ? (
          <svg width="12" height="12" viewBox="0 0 12 12" fill="none" aria-hidden>
            <path d="M2.5 6.5l2.5 2.5 4.5-5.5" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        ) : step}
      </span>
      <div className="mb-2 flex items-baseline gap-2 pt-1">
        <h3 className="text-sm font-semibold text-[#1f2933] dark:text-[#e6e8ec]">
          <span className="sr-only">Step {step}: </span>{title}
        </h3>
        <span className="text-xs text-[#98a2b3] dark:text-[#6b7482]">{hint}</span>
      </div>
      {children}
    </li>
  );
}

function SectionLabel({ children, className = "" }: { children: ReactNode; className?: string }) {
  return <div className={`mb-2 text-[11px] font-semibold uppercase tracking-wide text-[#5d6673] dark:text-[#9aa3af] ${className}`}>{children}</div>;
}

function ChipRow({ values }: { values: string[] }) {
  return (
    <div className="flex flex-wrap gap-1.5">
      {values.map((value) => (
        <span key={value} className="rounded-md border border-[#d7dce3] bg-[#f8fafc] px-2 py-1 text-xs font-medium text-[#344054] dark:border-[#2a3039] dark:bg-[#121720] dark:text-[#d7dce3]">{value}</span>
      ))}
    </div>
  );
}

function Meta({ label, value, sub, muted }: { label: string; value: string; sub: string; muted?: boolean }) {
  return (
    <div>
      <div className="text-[10px] font-semibold uppercase tracking-wide text-[#667085] dark:text-[#9aa3af]">{label}</div>
      <div className={`truncate text-sm ${muted ? "font-normal text-[#98a2b3] dark:text-[#6b7482]" : "font-semibold text-[#1f2933] dark:text-[#e6e8ec]"}`}>{value}</div>
      {sub ? <div className="truncate text-xs text-[#667085] dark:text-[#9aa3af]">{sub}</div> : null}
    </div>
  );
}

function ActionCard({ title, meta, disabled, busy, onClick }: { title: string; meta: string; disabled?: boolean; busy?: boolean; onClick: () => void }) {
  return (
    <button
      disabled={disabled}
      onClick={() => void onClick()}
      aria-label={title}
      className="rounded-md border border-[#d7dce3] bg-white p-3 text-left transition hover:border-[#aeb7c4] hover:bg-[#fbfcfd] disabled:cursor-not-allowed disabled:opacity-45 dark:border-[#2a3039] dark:bg-[#161b22] dark:hover:border-[#3a4350] dark:hover:bg-[#1b222b]"
    >
      <div className="text-sm font-semibold text-[#1f2933] dark:text-[#e6e8ec]">{busy ? "Working..." : title}</div>
      <div className="mt-1 text-xs text-[#667085] dark:text-[#9aa3af]">{meta}</div>
    </button>
  );
}
