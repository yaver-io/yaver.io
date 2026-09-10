"use client";

import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type PointerEvent, type WheelEvent } from "react";
import {
  agentClient,
  type ConversationTurn,
  type McpServer,
  type ModelInfo,
  type ProjectStartGitProvider,
  type RemoteRuntimeCapabilities,
  type RemoteRuntimeSession,
  type RemoteRuntimeTarget,
  type Runner,
  type RunnerBrowserAuthSession,
  type Task,
  type TaskStatus,
  type WorkspaceAppView,
} from "@/lib/agent-client";
import { isRunnerBrowserAuthTerminal } from "@/lib/agent-client";
import { saveLastProjectToConvex, loadLastProjectFromConvex, saveMCPServersToConvex, loadMCPServersFromConvex, setUseLatestMCPEnabled, setUseLatestProjectEnabled, useLatestMCPEnabled, useLatestProjectEnabled } from "@/lib/runtimeProjectSettings";
import { isAgentAuthErrorMessage } from "@/lib/agentAuthError";
import { detectCompileFailure } from "@/lib/compileFailure";
import {
  gapAIFixLabel,
  gapFixBody,
  capabilityGapFromDevEvent,
  gapBody,
  gapFixLabel,
  gapInstallTool,
  gapTitle,
  type CapabilityGap,
} from "@/lib/capabilityGap";
import { validateOpenCodeModel } from "@/lib/opencodeModel";
import {
  decideComposerKey,
  insertNewline,
  newlineIsNative,
} from "@/lib/composerKeys";
import { streamTaskOutputWithRecovery, type TaskStreamHealth } from "@/lib/taskStreamWithRecovery";
import { tombstoneAgentTask } from "@/lib/taskSnapshots";
import { AnsiConsoleText, hasConsoleMarkup } from "./AnsiConsoleText";
import RemoteRuntimeViewer from "./RemoteRuntimeViewer";
import { StreamHealthNotice } from "./StreamHealthNotice";
import { clampDevPct, formatDevProgressLine } from "@/lib/devEventLine";
import { runnerAuthFlowKind, runnerAuthLivenessLine } from "@/lib/runnerAuthFlow";
import { describeRunnerTurn } from "@/lib/runnerTurnHeartbeat";
import { assembleTrace } from "@/lib/_core/trace";
import { CONVEX_URL } from "@/lib/constants";
import { useAuth } from "@/lib/use-auth";
import { isExplicitRenderPrompt, useAutoRenderVibing } from "@/lib/autoRenderVibing";
import type { Device } from "@/lib/use-devices";
import { machineRolesSplitActive, type MachineRolesRow } from "@/lib/useMachineRoles";
import { probeFailureAllowsBoxAlive } from "@/lib/connection-error";
import { resolveSeededRole } from "@/lib/connectionFanout";
import { classifyRuntimeTargetProbeFailure } from "@/lib/runtimeTargetProbeFailure";
import { collapseTopLevelProjects } from "@/lib/projectTopLevel";
import { desktopDeviceLabel, type DesktopSurfaceInfo } from "@/lib/desktopSurface";
import { openCodeSnapshotFromConfig, usePrimaryRunnerByDevice } from "./DevicesView";
import { ScreenContextChip } from "./ScreenContextChip";
import { DomInspectChip } from "./DomInspectChip";
// Read-aloud must never recite Yaver's own prompt header — see lib/promptFraming.ts.
import { containsYaverFraming, sliceAfterFrameBoundary } from "@/lib/promptFraming";

type Project = {
  name: string;
  path: string;
  framework?: string;
  executionMode?: string;
  frameworks?: string[];
  stack?: string;
  surfaces?: string[];
  tags?: string[];
  monorepoRoot?: string;
  monorepoApp?: string;
  branch?: string;
  remote?: string;
  gitRemote?: string;
};

type WorkspaceRepo = {
  name: string;
  path: string;
  branch?: string;
  remote?: string;
  stack?: {
    type?: string;
    frameworks?: string[];
    services?: string[];
    actions?: string[];
  };
};

type MachineRolesDoctorInitial = {
  ready?: boolean;
  code?: string;
  summary?: string;
};

type MobilePreviewMode = "phone" | "tablet";
type ReasoningEffort = NonNullable<ModelInfo["defaultReasoningEffort"]>;

type RuntimeProjectPreference = {
  deviceId: string;
  projectName: string;
  repoName?: string;
  gitProvider?: string;
  gitRemote?: string;
  branch?: string;
  framework?: string;
  updatedAt?: number;
};

const mobilePreviewDevices: Record<MobilePreviewMode, { label: string; width: number; height: number; radius: number }> = {
  phone: { label: "Mobile", width: 393, height: 852, radius: 34 },
  tablet: { label: "Tablet", width: 820, height: 1180, radius: 26 },
};

const RUNTIME_CHAT_WIDTH_KEY = "yaver.runtime.chatPaneWidth";
const RUNTIME_CHAT_WIDTH_MIN = 340;
const RUNTIME_CHAT_WIDTH_MAX = 1100;

function defaultRuntimeChatWidth(mode: MobilePreviewMode = "phone") {
  if (mode === "tablet") return 420;
  if (typeof window === "undefined") return 720;
  return Math.min(RUNTIME_CHAT_WIDTH_MAX, Math.max(620, Math.round(window.innerWidth * 0.42)));
}

function clampRuntimeChatWidth(width: number) {
  const viewportMax = typeof window === "undefined" ? RUNTIME_CHAT_WIDTH_MAX : Math.max(RUNTIME_CHAT_WIDTH_MIN, window.innerWidth - 560);
  const max = Math.min(RUNTIME_CHAT_WIDTH_MAX, viewportMax);
  return Math.min(max, Math.max(RUNTIME_CHAT_WIDTH_MIN, Math.round(width)));
}

function formatPressureAge(atMs: number): string {
  const min = Math.max(0, Math.round((Date.now() - atMs) / 60000));
  if (min < 1) return "moments ago";
  if (min < 60) return `${min}m ago`;
  const hr = Math.round(min / 60);
  if (hr < 48) return `${hr}h ago`;
  return `${Math.round(hr / 24)}d ago`;
}

function RuntimePreviewLoadingScreen({
  note,
  projectName,
}: {
  note?: string | null;
  projectName?: string;
}) {
  return (
    <div className="flex h-full flex-col bg-[#05070a] text-white">
      <div className="flex h-9 items-center justify-between px-5 text-[11px] font-semibold text-white/80">
        <span>9:41</span>
        <div className="flex items-center gap-1.5">
          <span className="h-1.5 w-4 rounded-full bg-white/70" />
          <span className="h-2.5 w-4 rounded-sm border border-white/70" />
        </div>
      </div>
      <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-4 px-8 text-center">
        <div className="flex items-center justify-center gap-3">
          <div className="flex h-14 w-14 items-center justify-center overflow-hidden rounded-2xl bg-white shadow-2xl shadow-white/10">
            <img src="/icon-192.png" alt="Yaver" className="h-full w-full object-cover" />
          </div>
          <div className="text-4xl font-black tracking-[0.08em] text-white">YAVER</div>
        </div>
        <div>
          <div className="text-[11px] font-bold uppercase tracking-[0.35em] text-white/55">Remote AI Runtime</div>
          <div className="mt-3 text-xs font-medium text-white/45">{projectName || "Mobile app"}</div>
        </div>
        <div className="mt-2 flex items-center gap-2 rounded-full border border-white/10 bg-white/[0.06] px-3 py-1.5 text-[12px] text-white/75">
          <span className="h-2 w-2 animate-pulse rounded-full bg-emerald-400" />
          <span>{note || "Building web preview..."}</span>
        </div>
      </div>
      <div className="flex justify-center pb-3">
        <div className="h-1 w-28 rounded-full bg-white/35" />
      </div>
    </div>
  );
}

function RuntimePreviewLoadingSurface({
  mobile,
  device,
  scale = 1,
  note,
  projectName,
}: {
  mobile: boolean;
  device: { label: string; width: number; height: number; radius: number };
  scale?: number;
  note?: string | null;
  projectName?: string;
}) {
  const body = <RuntimePreviewLoadingScreen note={note} projectName={projectName} />;

  if (!mobile) {
    return (
      <div className="flex h-[520px] w-full items-center justify-center rounded-md border border-[#d7dce3] bg-[#0b0d11] dark:border-[#2a3039]">
        <div className="w-full max-w-sm">{body}</div>
      </div>
    );
  }

  return (
    <div
      className="flex w-full justify-center overflow-hidden rounded-md border border-[#d7dce3] bg-[#0b0d11] p-3 dark:border-[#2a3039]"
      style={{ minHeight: Math.round((device.height + 20) * scale) + 24 }}
    >
      <div style={{ width: (device.width + 20) * scale, height: (device.height + 20) * scale }}>
      <div
        className="shrink-0 overflow-hidden bg-[#1f2933] p-[10px] shadow-2xl"
        style={{
          borderRadius: device.radius,
          width: device.width + 20,
          height: device.height + 20,
          transform: `scale(${scale})`,
          transformOrigin: "top left",
        }}
      >
        <div
          className="overflow-hidden bg-black"
          style={{
            borderRadius: Math.max(0, device.radius - 10),
            width: device.width,
            height: device.height,
          }}
        >
          {body}
        </div>
      </div>
      </div>
    </div>
  );
}

export type RuntimeLabIntent = {
  nonce: number;
  kind: "runtime" | "tmux";
  projectQuery?: string;
  surface?: string;
  platform?: string;
  tmuxQuery?: string;
};

function targetIdFor(surface?: string, platform?: string): string {
  const s = String(surface || "").toLowerCase();
  const p = String(platform || "").toLowerCase();
  if (s === "watch" && (p === "ios" || p === "watchos")) return "watchos-simulator";
  if (s === "watch" && (p === "android" || p === "wear" || p === "wearos")) return "android-wear";
  if (s === "tv" && (p === "ios" || p === "tvos")) return "tvos-simulator";
  if (s === "tv" && p === "android") return "android-tv";
  if ((s === "vision" || s === "xr") && (p === "ios" || p === "visionos")) return "visionos-simulator";
  if ((s === "vision" || s === "xr") && p === "android") return "android-xr";
  if (s === "phone" && p === "android") return "android-emulator";
  if (s === "phone" || p === "ios") return "ios-simulator";
  return "";
}

function projectMatches(project: Project, query?: string): boolean {
  const q = String(query || "").trim().toLowerCase();
  if (!q) return false;
  return project.name.toLowerCase().includes(q) || project.path.toLowerCase().includes(q);
}

function projectTerms(project: Project | null): Set<string> {
  const terms = new Set<string>();
  for (const raw of [
    project?.name,
    project?.path,
    project?.framework,
    ...(project?.frameworks ?? []),
    project?.stack,
    ...(project?.surfaces ?? []),
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

function sanitizeRuntimeRemote(remote?: string | null): string | undefined {
  const raw = String(remote || "").trim();
  if (!raw) return undefined;
  try {
    const url = new URL(raw);
    url.username = "";
    url.password = "";
    url.hash = "";
    return url.toString().replace(/\/$/, "");
  } catch {
    if (/^https?:/i.test(raw) || /\/\/[^/\s]+@/.test(raw)) return undefined;
    return raw.slice(0, 300);
  }
}

function gitProviderFromRemote(remote?: string): string | undefined {
  const value = String(remote || "").toLowerCase();
  if (!value) return undefined;
  if (value.includes("github.com")) return "github";
  if (value.includes("gitlab.com")) return "gitlab";
  if (value.includes("bitbucket.org")) return "bitbucket";
  if (/^https?:\/\//.test(value)) return "git-http";
  if (value.includes("@") && value.includes(":")) return "git-ssh";
  return "git";
}

function repoNameFromRemote(remote?: string): string | undefined {
  const value = String(remote || "").trim().replace(/\.git$/i, "");
  if (!value) return undefined;
  try {
    const url = new URL(value);
    const parts = url.pathname.split("/").filter(Boolean);
    return parts.length >= 2 ? parts.slice(-2).join("/") : parts[0];
  } catch {
    const scp = value.match(/^[^@]+@[^:]+:(.+)$/);
    const path = (scp?.[1] || value).replace(/\\/g, "/");
    const parts = path.split("/").filter(Boolean);
    return parts.length >= 2 ? parts.slice(-2).join("/") : parts[0];
  }
}

function runtimeProjectPreferenceFor(project: Project, deviceId: string): RuntimeProjectPreference {
  const gitRemote = sanitizeRuntimeRemote(project.gitRemote || project.remote);
  const repoName = repoNameFromRemote(gitRemote) || project.name;
  return {
    deviceId,
    projectName: project.name,
    ...(repoName ? { repoName } : {}),
    ...(gitRemote ? { gitRemote, gitProvider: gitProviderFromRemote(gitRemote) } : {}),
    ...(project.branch ? { branch: project.branch } : {}),
    ...(project.framework ? { framework: project.framework } : {}),
    updatedAt: Date.now(),
  };
}

function runtimeProjectIdentityScore(project: Project, pref?: RuntimeProjectPreference): number {
  if (!pref) return 0;
  const meta = runtimeProjectPreferenceFor(project, pref.deviceId || "device");
  let score = 0;
  if (pref.gitRemote && meta.gitRemote && pref.gitRemote === meta.gitRemote) score += 8;
  if (pref.repoName && meta.repoName && pref.repoName.toLowerCase() === meta.repoName.toLowerCase()) score += 4;
  if (pref.projectName && pref.projectName === project.name) score += 3;
  if (pref.branch && project.branch && pref.branch === project.branch) score += 1;
  return score;
}

function resolveRuntimeProjectPreference(rows: Project[], pref?: RuntimeProjectPreference): Project | null {
  if (!pref) return null;
  return rows
    .map((project) => ({ project, score: runtimeProjectIdentityScore(project, pref) }))
    .filter((row) => row.score > 0)
    .sort((a, b) => b.score - a.score)[0]?.project || null;
}

function termsContain(terms: Set<string>, candidates: string[]): boolean {
  const all = Array.from(terms);
  return candidates.some((candidate) => terms.has(candidate) || all.some((term) => term.includes(candidate)));
}

function runtimeFrameworkForProject(project: Project | null): string {
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

function isMobileRuntimeProject(project: Project | null): boolean {
  const terms = projectTerms(project);
  return termsContain(terms, [
    "mobile",
    "expo",
    "react-native",
    "react-native-expo",
    "flutter",
    "swift",
    "kotlin",
    "iosnative",
    "androidnative",
  ]);
}

function browserPreviewFrameworkForProject(project: Project | null): string {
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

function isMonorepoProject(project: Project | null): boolean {
  return String(project?.framework || project?.stack || "").trim().toLowerCase() === "monorepo";
}

async function monorepoWebAppName(project: Project | null): Promise<string | undefined> {
  if (!project || !isMonorepoProject(project)) return undefined;
  try {
    const apps = await agentClient.getWorkspaceApps("web", project.path, "render");
    const app = apps.find((candidate) => candidate.exists && candidate.name === "web") ||
      apps.find((candidate) => candidate.exists && candidate.kind === "web") ||
      apps.find((candidate) => candidate.exists);
    return app?.name;
  } catch {
    return undefined;
  }
}

async function expandMonorepoProjects(
  rows: Project[],
  source: "connected" | "render" = "connected",
): Promise<Project[]> {
  const expanded: Project[] = [];
  const seen = new Set<string>();
  for (const project of rows) {
    const key = project.path;
    if (key && !seen.has(key)) {
      expanded.push(project);
      seen.add(key);
    }
    if (!isMonorepoProject(project)) continue;
    let apps: WorkspaceAppView[] = [];
    try {
      apps = await agentClient.getWorkspaceApps(["web", "mobile"], project.path, source);
    } catch {
      continue;
    }
    for (const app of apps) {
      if (!app.exists) continue;
      const appPath = app.absPath || app.path;
      if (!appPath || seen.has(appPath)) continue;
      seen.add(appPath);
      expanded.push({
        name: `${project.name} / ${app.name}`,
        path: appPath,
        framework: app.framework || app.stack || app.kind,
        frameworks: app.framework ? [app.framework] : [],
        stack: app.stack || app.kind,
        surfaces: app.kind ? [app.kind] : undefined,
        tags: [app.kind, app.framework, app.stack, app.name].filter(Boolean) as string[],
        monorepoRoot: project.path,
        monorepoApp: app.name,
        remote: project.remote || project.gitRemote,
        gitRemote: project.gitRemote || project.remote,
        branch: project.branch,
      });
    }
  }
  return expanded;
}

function signedBundlePreviewUrl(bundleUrl?: string): string | null {
  if (!bundleUrl) return null;
  try {
    const parsed = new URL(bundleUrl, "http://agent.local");
    if (!parsed.searchParams.has("sig") && !parsed.searchParams.has("signature") && !parsed.searchParams.has("token")) {
      return null;
    }
  } catch {
    return null;
  }
  return agentClient.webBundlePreviewUrl(bundleUrl);
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => window.setTimeout(resolve, ms));
}

function formatBuildElapsed(ms: number): string {
  const totalSec = Math.max(0, Math.floor(ms / 1000));
  return `${Math.floor(totalSec / 60)}:${String(totalSec % 60).padStart(2, "0")}`;
}

type RuntimeConsoleRow = { stamp: string; text: string; count: number };

// Collapse CONSECUTIVE duplicate messages (ignoring their timestamps) into one
// row with a ×N counter. Content is never altered — build/dev lines stay
// verbatim; a multi-line payload (stack trace) folds behind an explicit
// per-row toggle in the console renderer.
function groupRuntimeConsoleLines(lines: readonly string[]): RuntimeConsoleRow[] {
  const rows: RuntimeConsoleRow[] = [];
  for (const raw of lines) {
    const match = raw.match(/^\[([^\]]*)\]\s?([\s\S]*)$/);
    const stamp = match ? match[1] : "";
    const text = match ? match[2] : raw;
    const last = rows[rows.length - 1];
    if (last && last.text === text) {
      last.count += 1;
      last.stamp = stamp; // show the latest occurrence's time
    } else {
      rows.push({ stamp, text, count: 1 });
    }
  }
  return rows;
}

function normalizeRunnerId(runnerId?: string | null): string {
  const normalized = String(runnerId || "").trim().toLowerCase();
  if (normalized === "claude-code") return "claude";
  return normalized;
}

// Used only when device inventory gave us nothing. Codex deliberately has no
// client-side fallback: release-managed model/reasoning matrices live in
// Convex and reach this surface through /agent/runners. An unavailable catalog
// is rendered as unavailable instead of being replaced by a stale client list.
const FALLBACK_MODELS: Record<string, Array<{ id: string; name: string; isDefault?: boolean; source?: string }>> = {
  claude: [],
  codex: [],
  opencode: [],
};

function runnerName(id: string): string {
  if (id === "claude") return "Claude Code";
  if (id === "codex") return "OpenAI Codex";
  if (id === "opencode") return "OpenCode";
  return id || "Runner";
}

function runnersFromDeviceInventory(device?: Device | null): Runner[] {
  const rows = device?.runners || [];
  return rows
    .map((row): Runner | null => {
      const id = normalizeRunnerId(row.runnerId);
      const installed = row.installed ?? true;
      if (!id || !installed) return null;
      const ready = row.ready ?? row.authVerified ?? row.authConfigured;
      return {
        id,
        name: runnerName(id),
        installed,
        active: false,
        ready,
        authConfigured: row.authConfigured,
        authSource: row.authSource,
        error: row.error,
        warning: row.warning,
        supportsBrowserAuth: id === "claude" || id === "codex",
        supportsModelSelection: id === "claude" || id === "codex" || id === "opencode",
        models: FALLBACK_MODELS[id] || [],
      };
    })
    .filter((row): row is Runner => Boolean(row));
}

function runnerRowsEqual(a: Runner[], b: Runner[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i += 1) {
    const left = a[i];
    const right = b[i];
    if (
      left.id !== right.id ||
      left.name !== right.name ||
      left.installed !== right.installed ||
      left.active !== right.active ||
      left.isDefault !== right.isDefault ||
      left.ready !== right.ready ||
      left.authConfigured !== right.authConfigured ||
      left.authSource !== right.authSource ||
      left.warning !== right.warning ||
      left.error !== right.error ||
      (left.models || []).map((m) => m.id).join("|") !== (right.models || []).map((m) => m.id).join("|")
    ) {
      return false;
    }
  }
  return true;
}

function taskRowsEqual(a: Task[], b: Task[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i += 1) {
    if (
      a[i].id !== b[i].id ||
      a[i].title !== b[i].title ||
      a[i].status !== b[i].status ||
      a[i].updatedAt !== b[i].updatedAt
    ) {
      return false;
    }
  }
  return true;
}

function taskStreamEqual(
  a: { id: string; title: string; status: TaskStatus; lines: string[]; turns?: ConversationTurn[]; pendingUserTurns?: ConversationTurn[] } | null,
  b: { id: string; title: string; status: TaskStatus; lines: string[]; turns?: ConversationTurn[]; pendingUserTurns?: ConversationTurn[] } | null,
): boolean {
  if (a === b) return true;
  if (!a || !b) return false;
  if (
    a.id !== b.id ||
    a.title !== b.title ||
    a.status !== b.status ||
    a.lines.length !== b.lines.length ||
    (a.turns?.length || 0) !== (b.turns?.length || 0) ||
    (a.pendingUserTurns?.length || 0) !== (b.pendingUserTurns?.length || 0)
  ) {
    return false;
  }
  for (let i = 0; i < a.lines.length; i += 1) {
    if (a.lines[i] !== b.lines[i]) return false;
  }
  for (let i = 0; i < (a.turns?.length || 0); i += 1) {
    if (a.turns?.[i]?.role !== b.turns?.[i]?.role || a.turns?.[i]?.content !== b.turns?.[i]?.content) return false;
  }
  for (let i = 0; i < (a.pendingUserTurns?.length || 0); i += 1) {
    if (a.pendingUserTurns?.[i]?.content !== b.pendingUserTurns?.[i]?.content) return false;
  }
  return true;
}

function isModelCompatibleWithRunner(modelId: string | null | undefined, runnerId: string | null | undefined): boolean {
  const model = String(modelId || "").trim().toLowerCase();
  const runner = normalizeRunnerId(runnerId);
  if (!model || !runner) return false;
  if (runner === "claude") return model.startsWith("claude-");
  if (runner === "codex") return model.startsWith("gpt-") || model.startsWith("o") || model.includes("codex");
  if (runner === "opencode") {
    const [provider, modelName, ...extra] = model.split("/");
    return Boolean(provider && modelName && extra.length === 0);
  }
  return true;
}

function safeModelForRunner(
  runnerId: string | null | undefined,
  selectedModel: string | null | undefined,
  availableModels: Array<{ id: string; isDefault?: boolean }> | undefined,
): string | undefined {
  const runner = normalizeRunnerId(runnerId);
  if (!runner || runner === "custom") return undefined;
  const selected = String(selectedModel || "").trim();
  if (selected && isModelCompatibleWithRunner(selected, runner) && (availableModels?.length ? availableModels.some((m) => m.id === selected) : true)) {
    return selected;
  }
  const fallback =
    availableModels?.find((model) => model.isDefault && isModelCompatibleWithRunner(model.id, runner))?.id ||
    availableModels?.find((model) => isModelCompatibleWithRunner(model.id, runner))?.id;
  return fallback || undefined;
}

function taskOutputLines(task: Pick<Task, "output" | "resultText" | "status" | "turns"> | null | undefined, fallback: string[] = []): string[] {
  const output = Array.isArray(task?.output)
    ? task.output.flatMap((line) => String(line || "").split(/\r?\n/).map((part) => part.trimEnd()).filter(Boolean))
    : [];
  if (output.length) return output.slice(-240);
  const assistantTurns = Array.isArray(task?.turns)
    ? task.turns
        .filter((turn) => turn?.role === "assistant")
        .flatMap((turn) => String(turn.content || "").split(/\r?\n/).map((part) => part.trimEnd()).filter(Boolean))
    : [];
  if (assistantTurns.length) return assistantTurns.slice(-240);
  const result = String(task?.resultText || "").trim();
  if (result) return result.split(/\r?\n/).map((line) => line.trimEnd()).filter(Boolean).slice(-240);
  if (fallback.length) return fallback.slice(-240);
  const status = task?.status;
  if (status && status !== "queued" && status !== "running") {
    return [`No runner output was captured for this ${status} task.`];
  }
  return [];
}

function taskConversationTurns(task: Pick<Task, "title" | "description" | "turns" | "createdAt"> | null | undefined): ConversationTurn[] {
  const turns = Array.isArray(task?.turns)
    ? task.turns
        .filter((turn): turn is ConversationTurn =>
          (turn?.role === "user" || turn?.role === "assistant") &&
          turn.hidden !== true &&
          typeof turn.content === "string" &&
          turn.content.trim().length > 0)
        .slice(-50)
    : [];
  if (turns.length) return turns;
  const description = typeof task?.description === "string" ? task.description : "";
  const title = typeof task?.title === "string" ? task.title : "";
  const content = description.trim().length > 0 ? description : title;
  if (!content.trim()) return [];
  return [{ role: "user", content, timestamp: new Date(task?.createdAt || Date.now()).toISOString() }];
}

function taskPendingFollowUpTurns(task: Pick<Task, "pendingFollowUps"> | null | undefined): ConversationTurn[] {
  if (!Array.isArray(task?.pendingFollowUps)) return [];
  return task.pendingFollowUps
    .map((followUp) => String(followUp?.input ?? ""))
    .filter((input) => input.trim().length > 0)
    .map((content) => ({ role: "user", content, timestamp: "" }) as ConversationTurn);
}

function hasConversationTurn(turns: ConversationTurn[] | undefined, role: ConversationTurn["role"], content: string): boolean {
  const want = String(content ?? "");
  if (!want.trim()) return false;
  const normalizedWant = want.trim();
  return (turns || []).some((turn) => {
    if (turn.role !== role) return false;
    const current = String(turn.content ?? "");
    return current === want || current.trim() === normalizedWant;
  });
}

function mergePendingUserTurns(localTurns: ConversationTurn[] | undefined, serverTurns: ConversationTurn[] | undefined, persistedTurns: ConversationTurn[] | undefined): ConversationTurn[] {
  const merged: ConversationTurn[] = [];
  const add = (turn: ConversationTurn) => {
    if (turn.role !== "user") return;
    const content = String(turn.content ?? "");
    if (!content.trim()) return;
    if (hasConversationTurn(persistedTurns, "user", content) || hasConversationTurn(merged, "user", content)) return;
    merged.push({ ...turn, content });
  };
  (localTurns || []).forEach(add);
  (serverTurns || []).forEach(add);
  return merged.slice(-10);
}

function runtimeChatMessages(stream: { title: string; status: TaskStatus; lines: string[]; turns?: ConversationTurn[] }): ConversationTurn[] {
  const messages = (stream.turns?.length
    ? stream.turns
    : [{ role: "user", content: stream.title, timestamp: "" } as ConversationTurn]
  ).filter((turn) => turn.hidden !== true && String(turn.content || "").trim());
  const liveOutput = stream.lines.join("\n").trim();
  if (liveOutput && (stream.status === "queued" || stream.status === "running" || !messages.some((turn) => turn.role === "assistant"))) {
    const last = messages[messages.length - 1];
    if (last?.role === "assistant") {
      return [...messages.slice(0, -1), { ...last, content: liveOutput }];
    }
    return [...messages, { role: "assistant", content: liveOutput, timestamp: "" }];
  }
  if ((stream.status === "queued" || stream.status === "running") && !messages.some((turn) => turn.role === "assistant")) {
    return [...messages, { role: "assistant", content: "", timestamp: "" }];
  }
  return messages;
}

function taskTimeLabel(task: Pick<Task, "createdAt" | "updatedAt">): string {
  const ts = task.updatedAt || task.createdAt;
  if (!ts) return "";
  const deltaMs = Date.now() - ts;
  if (deltaMs < 60_000) return "now";
  if (deltaMs < 3_600_000) return `${Math.max(1, Math.round(deltaMs / 60_000))}m`;
  if (deltaMs < 86_400_000) return `${Math.max(1, Math.round(deltaMs / 3_600_000))}h`;
  return new Date(ts).toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

// taskStartedAtMs is when THIS turn began, in ms, or null when the task carries
// no usable timestamp. Used so re-attaching to a turn already five minutes old
// reports "5:02 elapsed" and not "0:00" — an elapsed counter that restarts on
// every reattach is worse than none, because it reads as progress.
function taskStartedAtMs(task: Pick<Task, "createdAt" | "updatedAt">): number | null {
  const ts = Number(task.createdAt) || 0;
  // Guard the unit: these are epoch MILLISECONDS everywhere in this client
  // (taskTimeLabel above relies on it). A seconds-valued field would render as
  // 1970 and print a five-digit "elapsed", so refuse it rather than print it.
  if (ts < 1_000_000_000_000) return null;
  if (ts > Date.now() + 60_000) return null; // clock skew on the box
  return ts;
}

function taskStatusAllowsRender(status: TaskStatus): boolean {
  return status === "completed" || status === "review";
}

function taskStatusMeansRunnerIsCoding(status?: TaskStatus | null): boolean {
  return status === "queued" || status === "running";
}

function canRunGuestOnRemoteTarget(targetId?: string): boolean {
  return [
    "ios-simulator",
    "ipados-simulator",
    "watchos-simulator",
    "tvos-simulator",
    "visionos-simulator",
    "android-emulator",
    "android-wear",
    "android-tv",
    "android-xr",
    "android-auto",
    "android-device",
    "android-redroid",
  ].includes(String(targetId || ""));
}

async function waitForDevPreviewUrl(bundleUrl?: string): Promise<{ url: string; note: string }> {
  const signedUrl = signedBundlePreviewUrl(bundleUrl);
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
    const status = await agentClient.getDevServerStatus();
    if (status?.error) lastError = status.error;
    if (status?.webPort && status.webPort > 0 && agentClient.devWebPreviewUrl) {
      const probed = await probePreviewUrl(agentClient.devWebPreviewUrl);
      if (probed.ok) return { url: agentClient.devWebPreviewUrl, note: status.servingLabel || "Web UI running in this dashboard." };
      lastError = probed.error;
    }
    if ((status?.running || status?.serving || (status?.port ?? 0) > 0) && agentClient.devPreviewUrl) {
      const probed = await probePreviewUrl(agentClient.devPreviewUrl);
      if (probed.ok) return { url: agentClient.devPreviewUrl, note: status?.servingLabel || "Web UI running in this dashboard." };
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

function projectFromRepo(repo: WorkspaceRepo): Project {
  const frameworks = repo.stack?.frameworks ?? [];
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
  return {
    name: repo.name || repo.path.split(/[\\/]/).filter(Boolean).pop() || repo.path,
    path: repo.path,
    framework: stackType === "monorepo" ? "monorepo" : frameworks[0] || stackType,
    frameworks,
    stack: stackType,
    surfaces: Array.from(surfaces),
    executionMode: actions.includes("hot-reload") ? "native-webrtc" : actions.includes("dev-server") ? "web" : undefined,
    tags: [stackType, ...frameworks, ...actions].filter(Boolean) as string[],
    remote: repo.remote,
    gitRemote: repo.remote,
    branch: repo.branch,
  };
}

function mergeProjectInventory(projects: Project[], repos: WorkspaceRepo[]): Project[] {
  const byPath = new Map<string, Project>();
  for (const project of projects) {
    if (project.path) byPath.set(project.path, project);
  }
  for (const repo of repos) {
    if (!repo.path) continue;
    const existing = byPath.get(repo.path);
    if (existing) {
      byPath.set(repo.path, {
        ...existing,
        remote: existing.remote || repo.remote,
        gitRemote: existing.gitRemote || repo.remote,
        branch: existing.branch || repo.branch,
        frameworks: existing.frameworks?.length ? existing.frameworks : repo.stack?.frameworks,
        stack: existing.stack || repo.stack?.type,
      });
      continue;
    }
    byPath.set(repo.path, projectFromRepo(repo));
  }
  return Array.from(byPath.values()).sort((a, b) => {
    const ay = `${a.name} ${a.path}`.toLowerCase().includes("yaver.io");
    const by = `${b.name} ${b.path}`.toLowerCase().includes("yaver.io");
    if (ay !== by) return ay ? -1 : 1;
    return a.name.localeCompare(b.name);
  });
}

function targetSort(a: RemoteRuntimeTarget, b: RemoteRuntimeTarget): number {
  const surfaceOrder = ["browser", "phone", "tablet", "watch", "tv", "vision", "car", "desktop"];
  const ai = surfaceOrder.indexOf(String(a.surface || ""));
  const bi = surfaceOrder.indexOf(String(b.surface || ""));
  if (ai !== bi) return (ai === -1 ? 99 : ai) - (bi === -1 ? 99 : bi);
  return a.label.localeCompare(b.label);
}

function targetActionLabel(target: RemoteRuntimeTarget): string {
  return `${target.enabled ? "Open" : "Unavailable"} ${target.label} (${target.id})`;
}

function isPrimaryRuntimeTarget(target: RemoteRuntimeTarget): boolean {
  if (!target.enabled) return false;
  const id = String(target.id || "").toLowerCase();
  const surface = String(target.surface || "").toLowerCase();
  if (surface === "browser") return true;
  if (["phone", "tablet"].includes(surface) && (id.includes("simulator") || id.includes("emulator"))) return true;
  if (id === "browser-window") return true;
  if (["ios-simulator", "ipados-simulator", "android-emulator"].includes(id)) return true;
  return false;
}

function runtimeTargetGroup(target: RemoteRuntimeTarget): "browser" | "simulator" | "container" | "device" | "advanced" | "unavailable" {
  if (!target.enabled) return "unavailable";
  const id = String(target.id || "").toLowerCase();
  const surface = String(target.surface || "").toLowerCase();
  if (surface === "browser" || id === "browser-window") return "browser";
  if (id.includes("redroid")) return "container";
  if (id.includes("device")) return "device";
  if (["phone", "tablet"].includes(surface) && (id.includes("simulator") || id.includes("emulator"))) return "simulator";
  if (id.includes("simulator") || id.includes("emulator")) return "advanced";
  return "advanced";
}

// isAgentAuthErrorMessage was file-local here until 2026-07 (audit §6 item 4);
// it now lives in @/lib/agentAuthError so every dashboard view shares it.

const runtimeGroupLabels: Record<ReturnType<typeof runtimeTargetGroup>, string> = {
  browser: "Browser",
  simulator: "Phone / tablet simulators",
  container: "Android containers",
  device: "Physical devices",
  advanced: "Watch / TV / XR / car",
  unavailable: "Unavailable",
};

export default function RuntimeLabView({
  intent,
  onOpenTmux,
  onReconnect,
  connectedDevice,
  devices,
  machineRoles,
  onSaveMachineRoles,
  onClearMachineRoles,
  desktopSurface = { isDesktop: false, localDeviceId: null },
}: {
  intent?: RuntimeLabIntent | null;
  onOpenTmux?: (sessionName: string) => void;
  onReconnect?: () => Promise<void>;
  connectedDevice?: Device | null;
  /** Full device list — names for the machine-roles badge + route editor. */
  devices?: Device[];
  /** Favorite runner/render split row (userSettings.machineRolesByProject).
   *  agentClient routing is set by the dashboard shell; this prop is for
   *  DISPLAY + editing, so the two sources are never silent. */
  machineRoles?: MachineRolesRow | null;
  onSaveMachineRoles?: (row: MachineRolesRow) => Promise<void>;
  onClearMachineRoles?: () => Promise<void>;
  desktopSurface?: DesktopSurfaceInfo;
}) {
  const { token } = useAuth();
  const autoRenderVibing = useAutoRenderVibing();
  const {
    primaryRunnerByDevice,
    primaryModelByDevice,
    primaryReasoningEffortByDevice,
    opencodeConfigByDevice,
    setPrimaryRunner,
    setOpenCodeConfigSnapshot,
  } = usePrimaryRunnerByDevice(token);
  const [projects, setProjects] = useState<Project[]>([]);
  const [selectedPath, setSelectedPath] = useState("");
  const [projectStartOpen, setProjectStartOpen] = useState(false);
  const [projectStartName, setProjectStartName] = useState("");
  const [projectStartGit, setProjectStartGit] = useState<ProjectStartGitProvider>("yaver-git");
  const [projectStartBusy, setProjectStartBusy] = useState(false);
  const [projectStartError, setProjectStartError] = useState<string | null>(null);
  const [runners, setRunners] = useState<Runner[]>([]);
  const [selectedRunner, setSelectedRunner] = useState("");
  const [selectedModel, setSelectedModel] = useState("");
  const [selectedReasoningEffort, setSelectedReasoningEffort] = useState<ReasoningEffort | "">("");
  const [runnerAuthBusy, setRunnerAuthBusy] = useState(false);
  // "Save for machine" used to answer only into the runtime log pane, which
  // the chat surface never shows — the user clicked and got NOTHING, on a save
  // that also never verified the runner was signed in on the box (2026-07-27).
  const [runnerSaveNotice, setRunnerSaveNotice] = useState<{ tone: "ok" | "warn" | "error"; text: string } | null>(null);
  // The primary picker's machine dropdown (2026-08-12, user directive): the
  // runner MACHINE used to be selectable only behind "Route", so pointing the
  // AI at another box (e.g. this Mac) was a buried 4-step detour. The dropdown
  // saves the role + re-routes agentClient in one shot; this note answers
  // into the same panel instead of the log pane.
  const [runnerMachineNote, setRunnerMachineNote] = useState<{ tone: "ok" | "error"; text: string } | null>(null);
  const [runnerAuthStatus, setRunnerAuthStatus] = useState<RunnerBrowserAuthSession | null>(null);
  const [runnerAuthError, setRunnerAuthError] = useState<string | null>(null);
  // action:"noop" from the agent — it declined to start a sign-in because the
  // runner already looks signed in. An answer, not an error; `reauthable`
  // offers the confirmed restart (switch account), the only path that reaps.
  const [runnerAuthDeclined, setRunnerAuthDeclined] = useState<{ reason: string; reauthable: boolean } | null>(null);
  const [runnerAuthCallbackUrl, setRunnerAuthCallbackUrl] = useState("");
  const [runnerAuthCallbackBusy, setRunnerAuthCallbackBusy] = useState(false);
  const [runnerAuthCodeInput, setRunnerAuthCodeInput] = useState("");
  const [runnerAuthCodeBusy, setRunnerAuthCodeBusy] = useState(false);
  const [composer, setComposer] = useState("");
  const [sending, setSending] = useState(false);
  const [useLatestProject, setUseLatestProject] = useState(() => useLatestProjectEnabled());
  const [useLatestMCP, setUseLatestMCP] = useState(() => useLatestMCPEnabled());
  const [mcpServers, setMcpServers] = useState<McpServer[]>([]);
  const [selectedMcpServers, setSelectedMcpServers] = useState<string[]>([]);
  const [includeYaverMcp, setIncludeYaverMcp] = useState(false);
  // Convex MCP sync — same mcpServersByDevice row the Chat composer and
  // mobile write, so an MCP selection made on the Vibing tab is remembered
  // on the phone and vice versa (2026-08-10). Load on connect; write on user
  // change (guarded so the initial restore is not written back as a "change").
  const mcpRestoredRef = useRef(false);
  useEffect(() => {
    if (!useLatestMCP || !connectedDevice?.id || !token) return;
    const deviceId = connectedDevice.id;
    let cancelled = false;
    const restore = async () => {
      try {
        const pref = await loadMCPServersFromConvex(CONVEX_URL, token, deviceId);
        if (cancelled || !pref) return;
        if (Array.isArray(pref.mcpServers)) {
          setSelectedMcpServers(pref.mcpServers.filter((name) => mcpServers.some((s) => s.name === name)));
        }
        if (typeof pref.includeYaverMcp === "boolean") setIncludeYaverMcp(pref.includeYaverMcp);
        mcpRestoredRef.current = true;
      } catch {}
    };
    void restore();
    return () => { cancelled = true; };
    // mcpServers intentionally omitted: restore once the agent's server list
    // is loaded; a later list refresh must not overwrite an in-session pick.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [connectedDevice?.id, token, useLatestMCP]);
  useEffect(() => {
    if (!mcpRestoredRef.current || !connectedDevice?.id || !token) return;
    void saveMCPServersToConvex(CONVEX_URL, token, {
      deviceId: connectedDevice.id,
      mcpServers: selectedMcpServers,
      includeYaverMcp,
      updatedAt: Date.now(),
    }).catch(() => {});
  }, [connectedDevice?.id, includeYaverMcp, selectedMcpServers, token]);
  const [chatRunnerControlsOpen, setChatRunnerControlsOpen] = useState(false);
  // Project, MCP and preview-inspection controls are useful but secondary to
  // the conversation. Keep them mounted (the context chips own live preview
  // listeners) while hiding their chrome behind the composer's ellipsis.
  // This also reaches the desktop GUI, which renders this dashboard verbatim.
  const [chatScopeControlsOpen, setChatScopeControlsOpen] = useState(false);
  // Machine-roles (runner/render split) route editor in the chat header.
  const [machinesEditOpen, setMachinesEditOpen] = useState(false);
  const [machinesDraftRunner, setMachinesDraftRunner] = useState("");
  const [machinesDraftRender, setMachinesDraftRender] = useState("");
  const [machinesBusy, setMachinesBusy] = useState(false);
  const [machinesNote, setMachinesNote] = useState<string | null>(null);
  const [machineRecoverBusy, setMachineRecoverBusy] = useState(false);
  // Per-role reachability test in the Route editor — saving a routing you
  // cannot reach is the "inventory says yes" trap; the probe attempts the
  // operation before you commit to it.
  const [machinesTest, setMachinesTest] = useState<{
    runner: { pinging?: boolean; ok?: boolean; rttMs?: number; error?: string };
    render: { pinging?: boolean; ok?: boolean; rttMs?: number; error?: string };
  }>({ runner: {}, render: {} });
  // Failure note for the Load-Targets-row render-machine picker — the chat
  // aside's machinesNote is off-screen from that row, and a save that fails
  // silently is an unfalsifiable state.
  const [renderPickNote, setRenderPickNote] = useState<string | null>(null);
  // Connection truth for the render box, probed from this browser over
  // relay/tunnel. The probe-failure card renders it so "recovery failed"
  // never appears without answering the user's first question: is there a
  // connection to that machine at all?
  const [renderConnCheck, setRenderConnCheck] = useState<{
    deviceId: string;
    pinging?: boolean;
    ok?: boolean;
    path?: string;
    rttMs?: number;
    error?: string;
    at: number;
  } | null>(null);
  const [chatPaneWidth, setChatPaneWidth] = useState(() => {
    if (typeof window === "undefined") return defaultRuntimeChatWidth("phone");
    const parsed = Number(window.localStorage.getItem(RUNTIME_CHAT_WIDTH_KEY));
    if (!Number.isFinite(parsed)) return defaultRuntimeChatWidth("phone");
    return clampRuntimeChatWidth(parsed);
  });
  const taskStreamStopRef = useRef<(() => void) | null>(null);
  const taskPollRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const recognitionRef = useRef<any>(null);
  const autoRenderRef = useRef<string>("");
  const webPreviewReloadInFlightRef = useRef(false);
  const queuedWebPreviewReloadRef = useRef<"fast" | "full" | null>(null);
  const chatPaneResizeRef = useRef<{ startX: number; startWidth: number } | null>(null);
  const runtimeConsoleRef = useRef<HTMLDivElement | null>(null);
  // Points at the chat pane's ONE scroll container (the pane itself, not the
  // runner bubble) — nested scrollbars made the stream feel like two views.
  const taskConsoleRef = useRef<HTMLDivElement | null>(null);
  const runnerSelectRef = useRef<HTMLSelectElement | null>(null);
  const modelSelectRef = useRef<HTMLSelectElement | null>(null);
  const mobilePreviewFrameRef = useRef<HTMLIFrameElement | null>(null);
  // DOM-mode targeting: the web preview renders as one of TWO iframes
  // (phone-shaped mockup vs plain web pane) depending on the panel skin, never
  // both. This ref is set by a callback on EACH iframe, so the mounted one
  // wins and the Browse|Inspect chip always posts into the live frame.
  const domInspectFrameRef = useRef<HTMLIFrameElement | null>(null);
  const [runtimeConsolePinned, setRuntimeConsolePinned] = useState(true);
  const [taskConsolePinned, setTaskConsolePinned] = useState(true);
  // The runner bubble shows the TAIL of the stream by default — that is where
  // the runner narrates what it is doing and states its answer. Raw tool
  // walls (grep dumps, file lists) fold behind a disclosure instead of
  // burying the narration. The user's own bubble is untouched.
  const [taskStreamExpanded, setTaskStreamExpanded] = useState(false);
  const [recentTasks, setRecentTasks] = useState<Task[]>([]);
  const [activeTaskStream, setActiveTaskStream] = useState<{
    id: string;
    deviceId?: string;
    title: string;
    status: TaskStatus;
    lines: string[];
    turns: ConversationTurn[];
    pendingUserTurns: ConversationTurn[];
  } | null>(null);
  // Live-output stream health. A cut stream used to end in silence, freezing
  // this console on its last line under a "running" badge.
  const [taskStreamHealth, setTaskStreamHealth] = useState<TaskStreamHealth>(null);
  const [caps, setCaps] = useState<RemoteRuntimeCapabilities | null>(null);
  const [session, setSession] = useState<RemoteRuntimeSession | null>(null);
  const [log, setLog] = useState<string[]>([]);
  const [devEventsUrl, setDevEventsUrl] = useState<string | null>(() => agentClient.devEventsUrl);
  // A named capability gap off the dev-events stream. The Runtime Lab console
  // is where a web user watches a start fail; before this it rendered
  // "dev error: exec flutter: executable file not found in $PATH" as one more
  // grey line and offered nothing.
  const [runtimeGap, setRuntimeGap] = useState<CapabilityGap | null>(null);
  const [runtimeGapBusy, setRuntimeGapBusy] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [recoveringAgentAuth, setRecoveringAgentAuth] = useState(false);
  const [showAdvancedTargets, setShowAdvancedTargets] = useState(false);
  const [webPreviewUrl, setWebPreviewUrl] = useState<string | null>(null);
  const [webPreviewPanelOpen, setWebPreviewPanelOpen] = useState(false);
  const [runtimeControlsOpen, setRuntimeControlsOpen] = useState(false);
  const [vibingSettingsOpen, setVibingSettingsOpen] = useState(false);
  const [sessionsOpen, setSessionsOpen] = useState(false);
  const [runtimeConsoleOpen, setRuntimeConsoleOpen] = useState(true);
  const [runtimeConsoleCopied, setRuntimeConsoleCopied] = useState(false);
  const [taskConsoleCopied, setTaskConsoleCopied] = useState(false);
  const [agentRenderRequest, setAgentRenderRequest] = useState<{
    id: string;
    taskId: string;
    reason: string;
    workDir?: string;
  } | null>(null);
  const [renderReady, setRenderReady] = useState(false);
  const [explicitRenderRequest, setExplicitRenderRequest] = useState<"fast" | null>(null);
  const [sttAvailable] = useState(
    () => typeof window !== "undefined" && !!((window as any).SpeechRecognition || (window as any).webkitSpeechRecognition),
  );
  const [ttsAvailable] = useState(
    () => typeof window !== "undefined" && "speechSynthesis" in window && "SpeechSynthesisUtterance" in window,
  );
  const [dictating, setDictating] = useState(false);
  const [speaking, setSpeaking] = useState(false);
  const [webPreviewFrameReady, setWebPreviewFrameReady] = useState(false);
  const [webPreviewBusy, setWebPreviewBusy] = useState(false);
  // A reload is in flight but the previous frame must STAY visible underneath —
  // the user may be reading it. Drives the subtle "reloading…" pill over the
  // preview and suppresses the full branded loader during a refresh.
  const [webPreviewReloading, setWebPreviewReloading] = useState(false);
  const [webPreviewStopping, setWebPreviewStopping] = useState(false);
  const [webPreviewNote, setWebPreviewNote] = useState<string | null>(null);
  // Raw dev-server output tail feeding the compile-failure card (gap D5).
  // Cleared on every "ready" event so a fixed compile drops the card.
  const [devLogTail, setDevLogTail] = useState<string[]>([]);
  // ONE lean heartbeat row while a build runs, fed by structured `progress`
  // dev events (real 0-100 pct): bar + percent + elapsed + last-output age.
  // Console lines stay verbatim underneath. Cleared on ready/error.
  const [buildProgress, setBuildProgress] = useState<{
    topic: string;
    pct: number;
    phase?: string;
    startedAt: number;
    lastOutputAt: number;
  } | null>(null);
  const [buildNowTick, setBuildNowTick] = useState(() => Date.now());
  // Runner-turn heartbeat. The status pill says "RUNNING", which is the
  // INVENTORY — a word that looks identical one second and forty minutes into a
  // turn, and identical again when the runner has silently stopped producing.
  // The preview lane already narrates itself ("62% · 3:53 elapsed · last output
  // 4s ago"); the runner lane did not, so the one question a user actually has
  // — "is the AI still working, and can I stop it?" — had no answer on screen.
  // Deliberately three facts and one button, rendered only while the turn is
  // live: a control surface accretes until the thing you came for is buried.
  const [runnerTurn, setRunnerTurn] = useState<{
    taskId: string;
    startedAt: number;
    lastOutputAt: number;
  } | null>(null);
  const [runnerStopBusy, setRunnerStopBusy] = useState(false);
  // Runtime-console rows expanded to show their full multi-line payload.
  const [expandedLogRows, setExpandedLogRows] = useState<Set<string>>(new Set());
  // "Fix with <runner>" dispatch state — one fix task in flight per box, ever.
  const [fixTaskBusy, setFixTaskBusy] = useState(false);
  const [fixTaskId, setFixTaskId] = useState<string | null>(null);
  // "Sync" dispatch state — git pull origin; a conflict hands the repo to the
  // runner (same createTask+attachTaskSession path as fix, so the resolution
  // streams into this chat pane).
  const [syncBusy, setSyncBusy] = useState(false);
  // Bumped by Fast/Full Reload to re-mount the preview iframe even when
  // the (signed) bundle URL is unchanged — e.g. a fast reload that
  // re-served the existing fresh bundle.
  const [webPreviewNonce, setWebPreviewNonce] = useState(0);
  // The iframe stays MOUNTED across reloads (stable key) so the last-good
  // frame never blanks; the reload nonce rides in the src query instead.
  // A src change reloads the iframe in place — the old page stays visible
  // until the new document starts painting.
  const previewFrameSrc = useMemo(() => {
    if (!webPreviewUrl) return null;
    try {
      const u = new URL(webPreviewUrl);
      u.searchParams.set("__preview_reload", String(webPreviewNonce));
      return u.toString();
    } catch {
      const join = webPreviewUrl.includes("?") ? "&" : "?";
      return `${webPreviewUrl}${join}__preview_reload=${webPreviewNonce}`;
    }
  }, [webPreviewUrl, webPreviewNonce]);
  const [mobilePreviewMode, setMobilePreviewMode] = useState<MobilePreviewMode>("phone");
  const [viewportHeight, setViewportHeight] = useState(() => typeof window === "undefined" ? 900 : window.innerHeight);
  const [runtimeProjectDefaultByDevice, setRuntimeProjectDefaultByDevice] = useState<Record<string, RuntimeProjectPreference>>({});
  // Saved render target rows, per (deviceId, projectName). A row without a
  // projectName is the machine-wide fallback. Pairs with the default project:
  // when BOTH resolve, the Vibing tab renders with zero clicks.
  const [runtimeTargetDefaults, setRuntimeTargetDefaults] = useState<Array<{ deviceId: string; projectName?: string; targetId: string; targetKind?: string }>>([]);
  const [runtimeProjectSaving, setRuntimeProjectSaving] = useState(false);
  const [runtimeProjectNote, setRuntimeProjectNote] = useState<string | null>(null);

  // Vibing's own default-project row — the SAME Convex
  // defaultRuntimeProjectByDevice the web Chat composer and mobile write, so
  // a project picked on the Vibing tab is remembered on the phone and vice
  // versa (2026-08-10). `savedRuntimeProject` (loaded below) still drives the
  // auto-render + the "★ Default" badge; this effect is the WRITE side, and
  // it deliberately only fires on USER selection, not on the initial
  // auto-selection from Convex (which would be a no-op write of the same row).
  const userSelectedProjectRef = useRef(false);
  const selectedProject = useMemo(
    () => projects.find((p) => p.path === selectedPath) || null,
    [projects, selectedPath],
  );
  useEffect(() => {
    if (!connectedDevice?.id || !selectedProject || !userSelectedProjectRef.current) return;
    if (!token) return;
    void saveLastProjectToConvex(CONVEX_URL, token, {
      deviceId: connectedDevice.id,
      projectName: selectedProject.name,
      ...(selectedProject.gitRemote ? { gitRemote: selectedProject.gitRemote } : {}),
      ...(selectedProject.branch ? { branch: selectedProject.branch } : {}),
      updatedAt: Date.now(),
    }).catch(() => {});
  }, [connectedDevice?.id, selectedProject, token]);
  const selectedProjectIsMobile = useMemo(() => isMobileRuntimeProject(selectedProject), [selectedProject]);
  const mobilePreviewDevice = mobilePreviewDevices[mobilePreviewMode];
  const mobilePreviewOuterWidth = mobilePreviewDevice.width + 20;
  const mobilePreviewOuterHeight = mobilePreviewDevice.height + 20;
  const mobilePreviewScale = selectedProjectIsMobile
    ? Math.min(1, Math.max(0.58, (viewportHeight - 250) / mobilePreviewOuterHeight))
    : 1;
  const runtimeCapabilitySummary = caps
    ? `${caps.framework} · ${caps.executionMode} · ${caps.primarySurface} · ${caps.currentHostClass || "host unknown"}${
        caps.cached ? " · cached" : caps.probeDurationMs ? ` · probed in ${Math.round(caps.probeDurationMs / 1000)}s` : ""
      }`
    : "";
  const savedRuntimeProject = connectedDevice?.id ? runtimeProjectDefaultByDevice[connectedDevice.id] : undefined;
  const selectedProjectIsSavedDefault = !!(
    selectedProject &&
    savedRuntimeProject &&
    runtimeProjectIdentityScore(selectedProject, savedRuntimeProject) > 0
  );
  // Heartbeat-snapshot fallback for the live /agent/runners fetch. With a
  // machine-role split active, tasks run on the RUNNER box — so the fallback
  // must read that box's heartbeat row, not the connected (render) box's,
  // or the auth gate answers for the wrong machine.
  const runnerFallbackDevice = useMemo(() => {
    const runnerId = machineRoles?.runnerDeviceId;
    if (runnerId && runnerId !== connectedDevice?.id) {
      return (devices || []).find((d) => d.id === runnerId) || connectedDevice;
    }
    return connectedDevice;
  }, [connectedDevice, devices, machineRoles?.runnerDeviceId]);
  const deviceRunnerFallback = useMemo(() => runnersFromDeviceInventory(runnerFallbackDevice), [runnerFallbackDevice?.id, runnerFallbackDevice?.runners]);

  const appendLog = useCallback((line: string) => {
    const stamp = new Date().toLocaleTimeString();
    setLog((prev) => [...prev.slice(-160), `[${stamp}] ${line}`]);
  }, []);

  const scrollToBottom = useCallback((node: HTMLElement | null) => {
    if (!node) return;
    node.scrollTop = node.scrollHeight;
  }, []);

  const isNearBottom = useCallback((node: HTMLElement) => {
    return node.scrollHeight - node.scrollTop - node.clientHeight < 36;
  }, []);

  useEffect(() => {
    if (runtimeConsolePinned) scrollToBottom(runtimeConsoleRef.current);
  }, [log, runtimeConsoleOpen, runtimeConsolePinned, scrollToBottom]);

  const taskLineCount = activeTaskStream?.lines.length || 0;
  const taskLastLine = taskLineCount ? activeTaskStream?.lines[taskLineCount - 1] || "" : "";
  const taskTurnCount = activeTaskStream?.turns.length || 0;
  const pendingTurnCount = activeTaskStream?.pendingUserTurns.length || 0;
  const pendingLastTurn = pendingTurnCount ? activeTaskStream?.pendingUserTurns[pendingTurnCount - 1]?.content || "" : "";

  useEffect(() => {
    if (taskConsolePinned) scrollToBottom(taskConsoleRef.current);
  }, [pendingLastTurn, pendingTurnCount, scrollToBottom, taskConsolePinned, taskLastLine, taskLineCount, taskTurnCount]);

  // New task → back to tail-only view; an expansion is a per-task choice.
  useEffect(() => {
    setTaskStreamExpanded(false);
  }, [activeTaskStream?.id]);

  useEffect(() => {
    let cancelled = false;
      const refresh = async () => {
        const rows = await agentClient.listTasks(8).catch(() => []);
      if (!cancelled) setRecentTasks((prev) => taskRowsEqual(prev, rows) ? prev : rows);
      };
    void refresh();
    const id = window.setInterval(() => void refresh(), 6000);
    return () => {
      cancelled = true;
      window.clearInterval(id);
    };
  }, []);

  useEffect(() => {
    if (!selectedProjectIsMobile) return;
    setMobilePreviewMode("phone");
    setChatPaneWidth(clampRuntimeChatWidth(defaultRuntimeChatWidth("phone")));
  }, [selectedPath, selectedProjectIsMobile]);

  const stopActiveTaskStream = useCallback(() => {
    taskStreamStopRef.current?.();
    taskStreamStopRef.current = null;
    if (taskPollRef.current) clearInterval(taskPollRef.current);
    taskPollRef.current = null;
    setTaskStreamHealth(null);
    // The heartbeat describes a LIVE turn. Every path that stops watching the
    // turn — it finished, the session closed, the component unmounted — must
    // clear it, or the row keeps counting elapsed time for a runner that is no
    // longer running, which is a lie in the one place the user is trusting.
    setRunnerTurn(null);
  }, []);

  useEffect(() => () => {
    stopActiveTaskStream();
    try { recognitionRef.current?.stop?.(); } catch {}
    recognitionRef.current = null;
    try { window.speechSynthesis?.cancel(); } catch {}
  }, [appendLog, stopActiveTaskStream]);

  useEffect(() => {
    const onResize = () => setViewportHeight(window.innerHeight);
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, []);

  const loadRuntimeSettings = useCallback(async () => {
    if (!token) return null;
    const res = await fetch(`${CONVEX_URL}/settings`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `settings: HTTP ${res.status}`);
    const settings = data?.settings || {};
    const defaults: Record<string, RuntimeProjectPreference> = {};
    for (const row of settings.defaultRuntimeProjectByDevice || []) {
      if (row?.deviceId && row?.projectName) defaults[row.deviceId] = row;
    }
    setRuntimeProjectDefaultByDevice(defaults);
    setRuntimeTargetDefaults(
      (settings.defaultRuntimeTargetByDevice || []).filter((row: any) => row?.deviceId && row?.targetId),
    );
    return settings;
  }, [token]);

  const postRuntimeSettings = useCallback(async (body: Record<string, unknown>) => {
    if (!token) return;
    const res = await fetch(`${CONVEX_URL}/settings`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `settings: HTTP ${res.status}`);
  }, [token]);

  // Project-scoped row wins over the machine-wide fallback row.
  const savedRuntimeTargetFor = useCallback((deviceId: string | undefined, projectName: string | undefined) => {
    if (!deviceId) return undefined;
    const scoped = runtimeTargetDefaults.find(
      (row) => row.deviceId === deviceId && !!projectName && row.projectName === projectName,
    );
    return scoped ?? runtimeTargetDefaults.find((row) => row.deviceId === deviceId && !row.projectName);
  }, [runtimeTargetDefaults]);

  const saveRuntimeTargetDefault = useCallback(async (target: { id: string; surface?: string }) => {
    if (!connectedDevice?.id) return;
    const row = {
      deviceId: connectedDevice.id,
      projectName: selectedProject?.name ?? null,
      targetId: target.id,
      targetKind: target.surface ?? null,
      updatedAt: Date.now(),
    };
    try {
      await postRuntimeSettings({ defaultRuntimeTargetForDevice: row });
      setRuntimeTargetDefaults((prev) => [
        ...prev.filter((r) => !(r.deviceId === row.deviceId && (r.projectName || "") === (row.projectName || ""))),
        { deviceId: row.deviceId, ...(row.projectName ? { projectName: row.projectName } : {}), targetId: row.targetId, ...(row.targetKind ? { targetKind: row.targetKind } : {}) },
      ]);
      // Visible confirmation, not just the log pane — an ack the user cannot
      // see is the "Save for machine" silence all over again (2026-07-27).
      setRuntimeProjectNote(`★ Default target saved: ${target.id}${selectedProject?.name ? ` for ${selectedProject.name}` : " (machine-wide)"}. Vibing will auto-render when the default project is set too.`);
      appendLog(`default target saved: ${target.id}${selectedProject?.name ? ` for ${selectedProject.name}` : ""}`);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setRuntimeProjectNote(`Could not save default target: ${msg}`);
      appendLog(`default target save failed: ${msg}`);
    }
  }, [appendLog, connectedDevice?.id, postRuntimeSettings, selectedProject?.name]);

  const seedRuntimeProjectCatalog = useCallback(async (rows: Project[]) => {
    if (!connectedDevice?.id || !token || rows.length === 0) return;
    const projects = rows.map((project) => {
      const { deviceId: _deviceId, ...meta } = runtimeProjectPreferenceFor(project, connectedDevice.id);
      return meta;
    });
    await postRuntimeSettings({
      runtimeProjectCatalogForDevice: {
        deviceId: connectedDevice.id,
        projects,
        updatedAt: Date.now(),
      },
    });
  }, [connectedDevice?.id, postRuntimeSettings, token]);

  const saveRuntimeProjectDefault = useCallback(async () => {
    if (!connectedDevice?.id || !selectedProject) return;
    const next = runtimeProjectPreferenceFor(selectedProject, connectedDevice.id);
    setRuntimeProjectSaving(true);
    setRuntimeProjectNote(null);
    try {
      await postRuntimeSettings({ defaultRuntimeProjectForDevice: next });
      setRuntimeProjectDefaultByDevice((prev) => ({ ...prev, [connectedDevice.id]: next }));
      setRuntimeProjectNote("Default saved for this machine.");
    } catch (err) {
      setRuntimeProjectNote(err instanceof Error ? err.message : "Could not save default project.");
    } finally {
      setRuntimeProjectSaving(false);
    }
  }, [connectedDevice?.id, postRuntimeSettings, selectedProject]);

  const loadProjects = useCallback(async () => {
    setError(null);
    try {
      // Machine-role split: the project LIST must come from the box the
      // render probe will actually ask (devBaseUrl), not the connected/AI
      // box. Before 2026-08-12 the list always came from agentClient
      // (connected box) while Load Targets probed the render box — so the
      // picker offered projects from one box while another box performed the
      // operation. A failed render inventory MUST NOT fall back to the
      // connected box: that recreates the false choice that produced
      // "sfmg / mobile" on a renderer that never had it.
      const split = machineRolesSplitActive(machineRoles);
      const [projectRows, repoRows, mobileRows, settings] = await Promise.all([
        split ? agentClient.listRenderProjects() : agentClient.listProjects(),
        agentClient.listWorkspaceRepos(split ? "render" : "connected"),
        agentClient.listProjectsByCapability("mobile", split ? "render" : "connected"),
        loadRuntimeSettings().catch(() => null),
      ]);
      // MCP servers for the composer chips — same /mcp/servers the Chat tab
      // reads, so the Vibing composer offers the same toggles (2026-08-10).
      try { setMcpServers((await agentClient.listMcpServers()).filter((s) => s.enabled)); } catch {}
      const merged = collapseTopLevelProjects(
        mergeProjectInventory([...(projectRows as Project[]), ...(mobileRows as Project[])], repoRows),
      );
      const rows = await expandMonorepoProjects(merged, split ? "render" : "connected");
      setProjects(rows);
      // Seed the Convex runtimeProjectCatalog with TOP-LEVEL rows only —
      // this function is the catalog's only writer, and previously it
      // persisted the expanded "<root> / <app>" rows, so "yaver.io / mobile"
      // style sub-project names lived in Convex and surfaced elsewhere
      // (2026-08-09). The lab's own app-level rows are fine for its UI; the
      // shared catalog must stay top-level.
      void seedRuntimeProjectCatalog(rows.filter((row) => !row.monorepoApp)).catch((err) => {
        const detail = err instanceof Error ? err.message : String(err);
        appendLog(`default project catalog sync skipped: ${detail || "settings unavailable"}`);
      });
      // A usable remote project inventory should produce a usable composer
      // without asking the user to repeat the machine's answer in a select.
      // Keep an in-session choice when it still exists; otherwise prefer the
      // remembered cross-surface project and finally the first project the
      // render machine actually returned. The picker remains available under
      // the composer ellipsis for explicit retargeting.
      if (!rows.some((row) => row.path === selectedPath)) {
        const saved = connectedDevice?.id
          ? (settings?.defaultRuntimeProjectByDevice || []).find((row: RuntimeProjectPreference) => row.deviceId === connectedDevice.id)
          : undefined;
        const next = (useLatestProject ? resolveRuntimeProjectPreference(rows, saved) : null) || rows[0] || null;
        setSelectedPath(next?.path || "");
      }
      appendLog(`projects loaded: ${rows.length}`);
    } catch (err) {
      const split = machineRolesSplitActive(machineRoles);
      if (split) {
        setProjects([]);
        setSelectedPath("");
        const renderId = machineRoles?.renderDeviceId || machineRoles?.runnerDeviceId || "";
        const renderName = (devices || []).find((device) => device.id === renderId)?.name || renderId.slice(0, 8) || "the renderer";
        const detail = err instanceof Error ? err.message : "Could not reach its project inventory.";
        setError(`Could not load projects from ${renderName}. No projects from another machine were substituted. ${detail}`);
      } else {
        setError(err instanceof Error ? err.message : "Could not load projects.");
      }
    }
  }, [appendLog, connectedDevice?.id, devices, loadRuntimeSettings, machineRoles, seedRuntimeProjectCatalog, selectedPath, useLatestProject]);

  const refreshRunners = useCallback(async () => {
    const deviceFallback = deviceRunnerFallback;
    try {
      const rows = (await agentClient.getRunners()).filter((runner) => {
        if (!runner.installed) return false;
        const id = String(runner.id || "").toLowerCase();
        return !id.includes("aider") && !id.includes("ollama");
      });
      const merged = rows.length ? rows : deviceFallback;
      setRunners((prev) => runnerRowsEqual(prev, merged) ? prev : merged);
      const explicitRunner = connectedDevice?.id ? primaryRunnerByDevice[connectedDevice.id] : "";
      const preferred =
        merged.find((runner) => runner.id === explicitRunner) ||
        merged.find((runner) => runner.active) ||
        merged.find((runner) => runner.isDefault) ||
        merged.find((runner) => runner.ready) ||
        merged[0];
      if (preferred && (!selectedRunner || !merged.some((runner) => runner.id === selectedRunner))) {
        setSelectedRunner(preferred.id);
      }
    } catch {
      setRunners((prev) => runnerRowsEqual(prev, deviceFallback) ? prev : deviceFallback);
      if (deviceFallback.length && (!selectedRunner || !deviceFallback.some((runner) => runner.id === selectedRunner))) {
        const explicitRunner = connectedDevice?.id ? primaryRunnerByDevice[connectedDevice.id] : "";
        const preferred = deviceFallback.find((runner) => runner.id === explicitRunner) || deviceFallback.find((runner) => runner.ready) || deviceFallback[0];
        setSelectedRunner(preferred.id);
      }
    }
  }, [connectedDevice?.id, deviceRunnerFallback, primaryRunnerByDevice, selectedRunner]);

  useEffect(() => {
    void refreshRunners();
    const id = window.setInterval(() => void refreshRunners(), 5000);
    return () => window.clearInterval(id);
  }, [refreshRunners]);

  // ── Machine-role split (runner/render) — display + editing ─────────
  // agentClient routing itself is set by the dashboard shell; here we make
  // the two sources VISIBLE (badge) and editable, and refresh the seams
  // that changed sides the moment the roles change: the runner list is now
  // answered by the runner box, dev events by the render box.
  const deviceNameById = useMemo(
    () => new Map((devices || []).map((d) => [d.id, d.name || d.id.slice(0, 8)])),
    [devices],
  );
  const machineSplitActive = machineRolesSplitActive(machineRoles);
  const runnerBoxName = machineRoles?.runnerDeviceId
    ? deviceNameById.get(machineRoles.runnerDeviceId) || machineRoles.runnerDeviceId.slice(0, 8)
    : null;
  const renderBoxName = (() => {
    const id = machineRoles?.renderDeviceId || machineRoles?.runnerDeviceId;
    return id ? deviceNameById.get(id) || id.slice(0, 8) : null;
  })();
  const roleEligibleDevices = useMemo(
    () => devices || [],
    [devices],
  );
  // The box that will actually answer the target probe / serve previews.
  //
  // The account's SEEDED roles decide this, in order: primary render →
  // secondary render → the runner box (a single-machine setup, not a
  // degradation). Only when no role is seeded at all does it fall back to
  // whatever device happens to be open.
  //
  // The old expression was `renderDeviceId || runnerDeviceId || connectedDevice`
  // — it never consulted the seeded SECONDARY, so a dead primary render had no
  // fallback and the panel simply reported failure while a perfectly good
  // secondary sat unused (magara, 2026-08-01).
  const seededRender = useMemo(
    () =>
      resolveSeededRole("render", machineRoles, (id) =>
        // Prefer a machine we have actually reached. The only verified answer
        // we hold here is the render probe, so an unreachable primary demotes
        // to the seeded secondary; everything else is treated as usable rather
        // than assumed dead.
        renderConnCheck && renderConnCheck.deviceId === id && renderConnCheck.ok === false ? false : true,
      ),
    [machineRoles, renderConnCheck],
  );
  const effectiveRenderDeviceId = seededRender.deviceId || connectedDevice?.id || null;
  const effectiveRenderBoxName = effectiveRenderDeviceId
    ? deviceNameById.get(effectiveRenderDeviceId) || effectiveRenderDeviceId.slice(0, 8)
    : null;
  // The warden's last heartbeat word about the render box. Once the box is
  // dark this is the only evidence of WHY — it upgrades "no connection" to
  // "it reported fork exhaustion; power-cycle it" (mac mini, 2026-07-27).
  const renderBoxPressure = useMemo(() => {
    if (!effectiveRenderDeviceId) return null;
    const rp = (devices || []).find((d) => d.id === effectiveRenderDeviceId)?.resourcePressure;
    if (!rp || (rp.level !== "critical" && rp.canFork !== false)) return null;
    return rp;
  }, [devices, effectiveRenderDeviceId]);

  useEffect(() => {
    void refreshRunners();
    setDevEventsUrl(agentClient.devEventsUrl);
    // refreshRunners identity churns every render; the roles ids are the
    // real trigger here.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [machineRoles?.runnerDeviceId, machineRoles?.renderDeviceId]);

  const openMachinesEditor = useCallback(() => {
    setMachinesDraftRunner(machineRoles?.runnerDeviceId || connectedDevice?.id || "");
    setMachinesDraftRender(machineRoles?.renderDeviceId || machineRoles?.runnerDeviceId || connectedDevice?.id || "");
    setMachinesNote(null);
    setMachinesTest({ runner: {}, render: {} });
    setMachinesEditOpen((open) => !open);
  }, [connectedDevice?.id, machineRoles?.renderDeviceId, machineRoles?.runnerDeviceId]);

  const saveMachineRoles = useCallback(async () => {
    if (!onSaveMachineRoles || !machinesDraftRunner) return;
    setMachinesBusy(true);
    setMachinesNote(null);
    const renderId = machinesDraftRender || machinesDraftRunner;
    try {
      await onSaveMachineRoles({
        runnerDeviceId: machinesDraftRunner,
        ...(machineRoles?.secondaryRunnerDeviceId ? { secondaryRunnerDeviceId: machineRoles.secondaryRunnerDeviceId } : {}),
        renderDeviceId: renderId,
        ...(machineRoles?.secondaryRenderDeviceId ? { secondaryRenderDeviceId: machineRoles.secondaryRenderDeviceId } : {}),
        workspace: machineRoles?.workspace || "runner-clone",
        autoPush: machineRoles?.autoPush || "ask",
      });
      const rn = deviceNameById.get(machinesDraftRunner) || machinesDraftRunner.slice(0, 8);
      const dn = deviceNameById.get(renderId) || renderId.slice(0, 8);
      const summary = machinesDraftRunner === renderId
        ? `Saved — ${rn} runs tasks and renders (single-box).`
        : `Saved — chat streams from ${rn}; previews build and serve on ${dn}.`;
      setMachinesNote(summary);
      appendLog(`machine roles: ${machinesDraftRunner === renderId ? `single-box on ${rn}` : `chat→${rn} · render→${dn}`}`);
      setMachinesEditOpen(false);
    } catch (err) {
      setMachinesNote(`Could not save: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setMachinesBusy(false);
    }
  }, [appendLog, deviceNameById, machineRoles?.autoPush, machineRoles?.secondaryRenderDeviceId, machineRoles?.secondaryRunnerDeviceId, machineRoles?.workspace, machinesDraftRender, machinesDraftRunner, onSaveMachineRoles]);

  const testMachineRole = useCallback(async (role: "runner" | "render") => {
    const id = role === "runner" ? machinesDraftRunner : (machinesDraftRender || machinesDraftRunner);
    const device = (devices || []).find((d) => d.id === id);
    if (!id || !device || !token) {
      setMachinesTest((prev) => ({
        ...prev,
        [role]: { ok: false, error: !id ? "pick a machine first" : !device ? "unknown device" : "not signed in" },
      }));
      return;
    }
    setMachinesTest((prev) => ({ ...prev, [role]: { pinging: true } }));
    const started = Date.now();
    try {
      const probe = await agentClient.probeDeviceStatus({
        host: device.host,
        port: device.port,
        token,
        deviceId: device.id,
        tunnelUrls: Array.from(
          new Set(
            [
              ...(Array.isArray(device.publicEndpoints) ? device.publicEndpoints : []),
              ...(device.tunnelUrl ? [device.tunnelUrl] : []),
            ]
              .map((u) => String(u || "").trim())
              .filter(Boolean),
          ),
        ),
      });
      setMachinesTest((prev) => ({
        ...prev,
        [role]: probe.ok
          ? { ok: true, rttMs: Date.now() - started }
          : { ok: false, error: probe.error || "unreachable" },
      }));
    } catch (err) {
      setMachinesTest((prev) => ({
        ...prev,
        [role]: { ok: false, error: err instanceof Error ? err.message : String(err) },
      }));
    }
  }, [devices, machinesDraftRender, machinesDraftRunner, token]);

  // Silent (no console log) — callers decide what to narrate. Probes the same
  // relay/tunnel/direct ladder as the Route editor's Test connection.
  const probeRenderConnectivity = useCallback(async (deviceId: string) => {
    const device = (devices || []).find((d) => d.id === deviceId);
    if (!deviceId || !device || !token) return null;
    setRenderConnCheck({ deviceId, pinging: true, at: Date.now() });
    const started = Date.now();
    let check: { deviceId: string; ok: boolean; path?: string; rttMs?: number; error?: string; at: number };
    try {
      const probe = await agentClient.probeDeviceStatus({
        host: device.host,
        port: device.port,
        token,
        deviceId: device.id,
        tunnelUrls: Array.from(
          new Set(
            [
              ...(Array.isArray(device.publicEndpoints) ? device.publicEndpoints : []),
              ...(device.tunnelUrl ? [device.tunnelUrl] : []),
            ]
              .map((u) => String(u || "").trim())
              .filter(Boolean),
          ),
        ),
      });
      check = probe.ok
        ? { deviceId, ok: true, path: probe.path, rttMs: Date.now() - started, at: Date.now() }
        : { deviceId, ok: false, error: probe.error || "no relay, tunnel, or direct path answered", at: Date.now() };
    } catch (err) {
      check = { deviceId, ok: false, error: err instanceof Error ? err.message : String(err), at: Date.now() };
    }
    setRenderConnCheck(check);
    return check;
  }, [devices, token]);

  const clearMachineRoles = useCallback(async () => {
    if (!onClearMachineRoles) return;
    setMachinesBusy(true);
    setMachinesNote(null);
    try {
      await onClearMachineRoles();
      setMachinesNote("Cleared — the connected machine runs and renders (single-box).");
      appendLog("machine roles cleared: single-box");
      setMachinesEditOpen(false);
    } catch (err) {
      setMachinesNote(`Could not clear: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setMachinesBusy(false);
    }
  }, [appendLog, onClearMachineRoles]);

  const ensureMachineRolesReady = useCallback(async (surface: "targets" | "web preview") => {
    if (!machineSplitActive) return true;
    try {
      const result = await agentClient.callOps("machine_roles_doctor", {
        projectName: selectedProject?.name || undefined,
        timeoutMs: 2500,
      });
      const initial = (result.initial || {}) as MachineRolesDoctorInitial;
      const code = String(initial.code || result.code || "");
      // /ops answers unknown verbs with HTTP 200 {ok:false, code:"unknown_verb"},
      // so a released agent without this verb lands HERE, not in the catch.
      if (code === "unknown_verb") {
        appendLog("machine roles doctor unavailable on this agent; continuing with direct probe");
        return true;
      }
      if (initial.ready === false || result.ok === false) {
        const summary = initial.summary || result.error || "runner/render split is not reachable";
        // Only an unreachable RENDER box blocks a render surface. Runner
        // trouble and doctor-infrastructure failures (auth_required,
        // settings_unreachable, timeouts) must not gate a capability that
        // may already work — the direct probe stays the oracle.
        if (code === "render_unreachable") {
          const message = `${surface} blocked [${code}]: ${summary}`;
          appendLog(`machine roles doctor: ${code} - ${summary}`);
          setError(message);
          if (surface === "web preview") setWebPreviewNote(message);
          return false;
        }
        appendLog(`machine roles doctor warning (${code || "not_ready"}): ${summary} — continuing with direct probe`);
        return true;
      }
      appendLog(`machine roles doctor: ${initial.code || "ready"}`);
      return true;
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      // The doctor is advisory; its own failure must never sit in the
      // critical path of the operation it annotates.
      appendLog(`machine roles doctor failed (${message}); continuing with direct probe`);
      return true;
    }
  }, [appendLog, machineSplitActive, selectedProject?.name]);

  const selectedRunnerRow = useMemo(
    () => runners.find((runner) => runner.id === selectedRunner) || null,
    [runners, selectedRunner],
  );
  const opencodeSnapshot = connectedDevice?.id ? opencodeConfigByDevice[connectedDevice.id] : undefined;
  const availableModels = useMemo(() => {
    if (normalizeRunnerId(selectedRunner) === "opencode" && opencodeSnapshot?.models?.length) {
      return opencodeSnapshot.models.map((model) => ({
        id: model.id,
        name: model.name || model.id,
        provider: model.provider,
        isDefault: model.isDefault,
        source: model.source || "opencode-config",
        defaultReasoningEffort: undefined,
        supportedReasoningEfforts: undefined,
      }));
    }
    return selectedRunnerRow?.models || [];
  }, [opencodeSnapshot?.models, selectedRunner, selectedRunnerRow?.models]);
  const selectedModelRow = useMemo(
    () => availableModels.find((model) => model.id === selectedModel),
    [availableModels, selectedModel],
  );
  const availableReasoningEfforts = useMemo(
    () => selectedModelRow?.supportedReasoningEfforts?.map((item) => item.reasoningEffort) || [],
    [selectedModelRow],
  );
  const effectiveChatModel = safeModelForRunner(selectedRunner, selectedModel, availableModels) || selectedModel;
  const selectedRunnerName = selectedRunnerRow?.name || selectedRunner || "Runner";
  // The one line that answers "is the remote runner actually working?". Null for
  // every non-live status, so the row simply is not there the rest of the time.
  const runnerHeartbeat = runnerTurn && runnerTurn.taskId === activeTaskStream?.id
    ? describeRunnerTurn({
        status: activeTaskStream?.status,
        runnerName: selectedRunnerRow?.name || selectedRunner,
        startedAt: runnerTurn.startedAt,
        lastOutputAt: runnerTurn.lastOutputAt,
        now: buildNowTick,
        hasOutput: (activeTaskStream?.lines.length || 0) > 0,
      })
    : null;
  const chatStatusTone = activeTaskStream?.status === "failed"
    ? "border-rose-500/30 bg-rose-500/10 text-rose-700 dark:text-rose-200"
    : activeTaskStream?.status === "completed"
      ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-200"
      : activeTaskStream
        ? "border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-200"
        : "border-[#d7dce3] bg-[#f2f4f7] text-[#667085] dark:border-[#2a3039] dark:bg-[#101318] dark:text-[#9aa3af]";
  const runtimeGridStyle = {
    "--runtime-chat-width": `${chatPaneWidth}px`,
  } as CSSProperties;

  const chooseMobilePreviewMode = useCallback((mode: MobilePreviewMode) => {
    setMobilePreviewMode(mode);
    setChatPaneWidth(clampRuntimeChatWidth(defaultRuntimeChatWidth(mode)));
  }, []);

  const beginChatPaneResize = useCallback((event: PointerEvent<HTMLButtonElement>) => {
    chatPaneResizeRef.current = { startX: event.clientX, startWidth: chatPaneWidth };
    event.currentTarget.setPointerCapture(event.pointerId);
  }, [chatPaneWidth]);

  const moveChatPaneResize = useCallback((event: PointerEvent<HTMLButtonElement>) => {
    const active = chatPaneResizeRef.current;
    if (!active) return;
    setChatPaneWidth(clampRuntimeChatWidth(active.startWidth - (event.clientX - active.startX)));
  }, []);

  const endChatPaneResize = useCallback((event: PointerEvent<HTMLButtonElement>) => {
    chatPaneResizeRef.current = null;
    try {
      event.currentTarget.releasePointerCapture(event.pointerId);
    } catch {
      // Pointer capture may already be released by the browser.
    }
  }, []);

  useEffect(() => {
    try {
      window.localStorage.setItem(RUNTIME_CHAT_WIDTH_KEY, String(chatPaneWidth));
    } catch {
      // Best-effort layout preference.
    }
  }, [chatPaneWidth]);

  useEffect(() => {
    if (!connectedDevice?.id || normalizeRunnerId(selectedRunner) !== "opencode") return;
    let cancelled = false;
    void agentClient.openCodeConfig(connectedDevice.id).then((cfg) => {
      if (cancelled) return;
      const snapshot = openCodeSnapshotFromConfig(connectedDevice.id, cfg);
      if (!snapshot.model && !snapshot.provider && !snapshot.models?.length && !snapshot.providers?.length) return;
      void setOpenCodeConfigSnapshot(snapshot).catch(() => {});
    }).catch(() => {});
    return () => { cancelled = true; };
  }, [connectedDevice?.id, selectedRunner, setOpenCodeConfigSnapshot]);

  // SEED the model, never FIGHT the user for it.
  //
  // This effect used to call setSelectedModel(explicitModel) unconditionally
  // whenever the machine had a saved model — with `selectedModel` in its own
  // dependency array. Picking anything else re-ran the effect, saw the saved
  // default still present, and snapped the value back within a frame. The model
  // was UNCHANGEABLE on any machine that had one saved, and the only way to
  // change the saved value was… to select a different one first. A closed loop
  // with no exit (2026-08-02: the owner could not move off a gpt-5.4 that his
  // ChatGPT-account Codex login cannot even run).
  //
  // The fix is to seed per (device, runner) and then leave the user alone. A
  // selection that is still valid for the current runner is the user's, and an
  // effect must not overwrite it. Guard: web/lib/modelSelectSeeding.test.ts.
  const modelSeedKeyRef = useRef<string>("");
  useEffect(() => {
    const seedKey = `${connectedDevice?.id || ""}|${normalizeRunnerId(selectedRunner)}`;
    const alreadySeeded = modelSeedKeyRef.current === seedKey;
    const currentIsValid = !!selectedModel && availableModels.some((model) => model.id === selectedModel);
    // Same device+runner as last time and the pick still resolves → the user
    // owns this value. Do nothing.
    if (alreadySeeded && currentIsValid) return;
    if (!availableModels.length) return;
    modelSeedKeyRef.current = seedKey;
    const explicitModel = connectedDevice?.id ? primaryModelByDevice[connectedDevice.id] || opencodeSnapshot?.model || "" : "";
    if (explicitModel && availableModels.some((model) => model.id === explicitModel)) {
      setSelectedModel(explicitModel);
      return;
    }
    if (!currentIsValid) {
      setSelectedModel(availableModels.find((model) => model.isDefault)?.id || availableModels[0]?.id || "");
    }
  }, [availableModels, connectedDevice?.id, opencodeSnapshot?.model, primaryModelByDevice, selectedModel, selectedRunner]);

  useEffect(() => {
    if (normalizeRunnerId(selectedRunner) !== "codex") {
      setSelectedReasoningEffort("");
      return;
    }
    if (!availableReasoningEfforts.length) return;
    const runnerMachineId = machineRoles?.runnerDeviceId || connectedDevice?.id || "";
    const saved = runnerMachineId ? primaryReasoningEffortByDevice[runnerMachineId] || "" : "";
    setSelectedReasoningEffort((current) => {
      if (current && availableReasoningEfforts.includes(current)) return current;
      if (availableReasoningEfforts.includes(saved as ReasoningEffort)) return saved as ReasoningEffort;
      return selectedModelRow?.defaultReasoningEffort || availableReasoningEfforts[0] || "";
    });
  }, [availableReasoningEfforts, connectedDevice?.id, machineRoles?.runnerDeviceId, primaryReasoningEffortByDevice, selectedModelRow?.defaultReasoningEffort, selectedRunner]);

  useEffect(() => {
    if (!runnerAuthStatus?.id || ["completed", "failed", "cancelled", "account_not_eligible"].includes(runnerAuthStatus.status)) return;
    const id = window.setInterval(async () => {
      try {
        const next = await agentClient.getRunnerBrowserAuthStatus(runnerAuthStatus.id);
        setRunnerAuthStatus(next);
        if (["completed", "failed", "cancelled", "account_not_eligible"].includes(next.status)) {
          setRunnerAuthBusy(false);
          void refreshRunners();
        }
      } catch (err) {
        setRunnerAuthError(err instanceof Error ? err.message : String(err));
      }
    }, 2000);
    return () => window.clearInterval(id);
  }, [refreshRunners, runnerAuthStatus?.id, runnerAuthStatus?.status]);

  useEffect(() => {
    const unsubscribe = agentClient.on("connectionState", () => setDevEventsUrl(agentClient.devEventsUrl));
    setDevEventsUrl(agentClient.devEventsUrl);
    return unsubscribe;
  }, []);

  useEffect(() => {
    if (!devEventsUrl) return;
    const es = new EventSource(devEventsUrl);
    // Rolling raw tail for the compile-failure detector (audit gap D5):
    // the runtime console prefixes lines for humans, but the detector wants
    // the agent's raw words. A "ready" event means a successful (re)compile —
    // clear the tail so a fixed error doesn't keep the card up.
    const pushTail = (line: string) => {
      setDevLogTail((prev) => {
        const next = [...prev, line];
        return next.length > 120 ? next.slice(-120) : next;
      });
    };
    // Any dev output while a build is running refreshes the heartbeat row's
    // "last output Ns ago" — the reader must be able to tell fetching from hung.
    const touchBuildOutput = () => {
      setBuildProgress((prev) => (prev ? { ...prev, lastOutputAt: Date.now() } : prev));
    };
    es.onmessage = (msg) => {
      try {
        const ev = JSON.parse(msg.data);
        if (ev.type === "log" && typeof ev.message === "string") { appendLog(`dev: ${ev.message}`); pushTail(ev.message); touchBuildOutput(); }
        else if (ev.type === "phase" && ev.topic && ev.phase) {
          appendLog(`${ev.topic}: ${ev.phase}`);
          touchBuildOutput();
          // The transport tracker's TERMINAL phase is `delivered` (or `error`)
          // — a distinct SSE `type:"phase"`, NOT a `type:"ready"` event. Without
          // clearing here, the "webview/transport · streaming NN%" bar for the
          // static web-bundle lane never settles and sticks (e.g. at 16% — the
          // byte share the iframe fetches before the rest go unrequested).
          if (ev.phase === "delivered" || ev.phase === "error") setBuildProgress(null);
        }
        // Agent pct is already 0..100 (devserver.go Pct) — multiplying by
        // 100 here printed "1575% streaming". formatDevProgressLine clamps.
        else if (ev.type === "progress" && ev.topic) {
          appendLog(formatDevProgressLine(ev.topic, ev.pct, ev.phase));
          const pct = clampDevPct(ev.pct);
          setBuildProgress((prev) => ({
            topic: String(ev.topic),
            pct,
            phase: typeof ev.phase === "string" ? ev.phase : prev?.phase,
            startedAt: prev?.startedAt ?? Date.now(),
            lastOutputAt: Date.now(),
          }));
        }
        else if (ev.type === "ready") { appendLog("dev server ready"); setDevLogTail([]); setBuildProgress(null); }
        else if (ev.type === "error") {
          if (ev.error) { appendLog(`dev error: ${ev.error}`); pushTail(String(ev.error)); }
          if (ev.message) { appendLog(`dev error: ${ev.message}`); pushTail(String(ev.message)); }
          setBuildProgress(null);
          // The route. Same object mobile renders, same code lookup.
          const gap = capabilityGapFromDevEvent(ev);
          if (gap) setRuntimeGap(gap);
        }
        else if (ev.type === "snapshot" && ev.snapshot?.recentLogs?.length) {
          for (const line of ev.snapshot.recentLogs.slice(-3)) { appendLog(`dev: ${line}`); pushTail(String(line)); }
          touchBuildOutput();
        }
      } catch {
        if (msg.data) appendLog(`dev: ${String(msg.data).slice(0, 240)}`);
      }
    };
    es.onerror = () => appendLog("dev events stream interrupted");
    return () => es.close();
  }, [appendLog, devEventsUrl]);

  const runtimeCompileCard = useMemo(() => detectCompileFailure(null, devLogTail), [devLogTail]);

  // 1 Hz tick for the build AND runner-turn heartbeat rows — only while one of
  // them is live, so a working preview never gets a surprise re-render from an
  // idle timer. Both rows read the same clock: two tickers would drift and print
  // two different "elapsed" values for the same minute.
  const buildProgressActive = buildProgress !== null;
  const runnerTurnActive = runnerTurn !== null;
  useEffect(() => {
    if (!buildProgressActive && !runnerTurnActive) return;
    setBuildNowTick(Date.now());
    const id = window.setInterval(() => {
      setBuildNowTick(Date.now());
      // Stall watchdog. The transport can stop emitting mid-stream — the iframe
      // stops requesting files, or the `delivered` ack never arrives — leaving
      // the bar frozen. A progress bar that cannot advance must not imply
      // "still building" forever, so settle it after a quiet window. The build
      // itself already resolved over HTTP; this only clears stale UI.
      setBuildProgress((prev) => {
        if (!prev) return prev;
        const last = prev.lastOutputAt ?? prev.startedAt ?? Date.now();
        return Date.now() - last > 12000 ? null : prev;
      });
    }, 1000);
    return () => window.clearInterval(id);
  }, [buildProgressActive, runnerTurnActive]);

  /** POST the gap's fix and stream it into the runtime console the user is
   *  already reading. Driven entirely by the typed route the agent shipped. */
  const runRuntimeGapFix = useCallback(async (gap: CapabilityGap) => {
    const tool = gapInstallTool(gap);
    if (!tool || runtimeGapBusy) return;
    setRuntimeGapBusy(true);
    appendLog(`fix: POST ${gap.fix!.path} …`);
    const started = await agentClient.installTool(tool).catch((e: any) => ({ ok: false, stream: "", error: e?.message || String(e) }));
    if (!started.ok) {
      appendLog(`fix: ${gap.fix!.path} refused: ${started.error || "unknown error"}`);
      setRuntimeGapBusy(false);
      return;
    }
    const streamName = started.stream || gap.fix!.stream;
    appendLog(`fix: streaming /streams/${streamName}`);
    const stop = agentClient.streamLog(streamName, (ev: any) => {
      if (ev?.type === "line" && typeof ev.text === "string") appendLog(`fix: ${ev.text}`);
      else if (ev?.type === "result") {
        stop();
        setRuntimeGapBusy(false);
        if (ev.status === "ok") { setRuntimeGap(null); appendLog("fix: installed — start the preview again"); }
        else appendLog(`fix: install failed: ${ev.error || "unknown error"}`);
      }
    });
  }, [appendLog, runtimeGapBusy]);

  const startSelectedRunnerSignIn = useCallback(async (confirm = false) => {
    if (!selectedRunnerRow || !["claude", "codex"].includes(selectedRunnerRow.id)) return;
    setRunnerAuthBusy(true);
    setRunnerAuthError(null);
    setRunnerAuthDeclined(null);
    setRunnerAuthCallbackUrl("");
    setRunnerAuthCodeInput("");
    try {
      const res = await agentClient.runnerBrowserAuthStart({
        runner: selectedRunnerRow.id as "claude" | "codex",
        trigger: confirm ? "confirmed" : "explicit",
        confirm,
      });
      if (!res.ok) throw new Error(res.error || "Could not start sign-in on the machine.");
      if (res.action === "noop") {
        // The agent answered "already signed in" — render its sentence, don't
        // treat the missing session as a failure.
        setRunnerAuthBusy(false);
        setRunnerAuthDeclined({
          reason: res.reason || `${selectedRunnerRow.name} is already signed in on that machine.`,
          reauthable: res.reauthable !== false,
        });
        return;
      }
      const session = res.session;
      if (!session) throw new Error(res.reason || "The machine did not start a sign-in session and did not say why.");
      setRunnerAuthStatus(session);
      if (session.openUrl) window.open(session.openUrl, "_blank", "noopener,noreferrer");
    } catch (err) {
      setRunnerAuthBusy(false);
      setRunnerAuthError(err instanceof Error ? err.message : String(err));
    }
  }, [selectedRunnerRow]);

  const openRunnerControls = useCallback((field: "runner" | "model" = "runner") => {
    setVibingSettingsOpen(true);
    window.setTimeout(() => {
      const node = field === "model" ? modelSelectRef.current : runnerSelectRef.current;
      node?.focus();
    }, 0);
  }, []);

  const submitRunnerAuthCode = useCallback(async () => {
    const code = runnerAuthCodeInput.trim();
    if (!runnerAuthStatus?.id || !code || runnerAuthCodeBusy) return;
    setRunnerAuthCodeBusy(true);
    setRunnerAuthError(null);
    try {
      const next = await agentClient.submitRunnerBrowserAuthCode(runnerAuthStatus.id, code);
      setRunnerAuthStatus(next);
      setRunnerAuthCodeInput("");
      appendLog(`runner oauth code submitted: ${runnerAuthStatus.runner}`);
    } catch (err) {
      setRunnerAuthError(err instanceof Error ? err.message : String(err));
    } finally {
      setRunnerAuthCodeBusy(false);
    }
  }, [appendLog, runnerAuthCodeBusy, runnerAuthCodeInput, runnerAuthStatus?.id, runnerAuthStatus?.runner]);

  const submitRunnerAuthCallback = useCallback(async () => {
    const callbackUrl = runnerAuthCallbackUrl.trim();
    if (!runnerAuthStatus?.id || !callbackUrl || runnerAuthCallbackBusy) return;
    setRunnerAuthCallbackBusy(true);
    setRunnerAuthError(null);
    try {
      const next = await agentClient.submitRunnerBrowserAuthCallback(runnerAuthStatus.id, callbackUrl);
      setRunnerAuthStatus(next);
      setRunnerAuthCallbackUrl("");
      appendLog(`runner oauth callback delivered: ${runnerAuthStatus.runner}`);
    } catch (err) {
      setRunnerAuthError(err instanceof Error ? err.message : String(err));
    } finally {
      setRunnerAuthCallbackBusy(false);
    }
  }, [appendLog, runnerAuthCallbackBusy, runnerAuthCallbackUrl, runnerAuthStatus?.id, runnerAuthStatus?.runner]);

  const saveRunnerChoice = useCallback(async () => {
    // The preference belongs to the box that RUNS the AI — which is the
    // routed runner machine when a split is active, NOT necessarily the
    // device the dashboard is connected to. Before 2026-08-12 "Save for
    // machine" always wrote the CONNECTED device, so after routing the
    // runner to another box the save kept landing on the old one.
    const runnerMachineId = machineRoles?.runnerDeviceId || connectedDevice?.id || "";
    if (!runnerMachineId || !selectedRunner) return;
    const provider = normalizeRunnerId(selectedRunner) === "opencode" && selectedModel.includes("/")
      ? selectedModel.split("/")[0]
      : null;
    setRunnerSaveNotice({ tone: "warn", text: "Saving…" });
    const runnerName = selectedRunnerRow?.name || selectedRunner;
    const chosen = `${runnerName}${selectedModel ? ` · ${selectedModel}` : ""}`;
    const target = machineRoles?.runnerDeviceId
      ? deviceNameById.get(machineRoles.runnerDeviceId) || machineRoles.runnerDeviceId.slice(0, 8)
      : connectedDevice?.name || "this machine";
    try {
      await setPrimaryRunner(
        runnerMachineId,
        selectedRunner,
        selectedModel || null,
        undefined,
        provider,
        normalizeRunnerId(selectedRunner) === "codex" ? selectedReasoningEffort || null : null,
      );
      // Saving the preference is the inventory; whether the runner can take
      // the next task is the operation. The box's own runner row carries that
      // answer — say it, and when sign-in is missing, route straight into the
      // remote OAuth flow instead of leaving a dead end.
      if (selectedRunnerRow && selectedRunnerRow.ready === false) {
        setRunnerSaveNotice({
          tone: "warn",
          text: `Saved ${chosen} for ${target} — but it is not signed in on that machine yet.`,
        });
        if (selectedRunnerRow.supportsBrowserAuth) void startSelectedRunnerSignIn();
      } else {
        setRunnerSaveNotice({ tone: "ok", text: `Saved ${chosen} for ${target} — signed in and ready.` });
      }
      appendLog(`runner set: ${selectedRunner}${selectedModel ? ` ${selectedModel}` : ""} on ${target}`);
    } catch (err) {
      setRunnerSaveNotice({
        tone: "error",
        text: `Save failed: ${err instanceof Error ? err.message : String(err)}`,
      });
    }
  }, [appendLog, connectedDevice?.id, connectedDevice?.name, deviceNameById, machineRoles?.runnerDeviceId, selectedModel, selectedReasoningEffort, selectedRunner, selectedRunnerRow, setPrimaryRunner, startSelectedRunnerSignIn]);

  // Point the AI RUNNER at a specific box, right from the primary picker.
  // Previously only the Route editor could change which machine runs the AI;
  // this is the same save+route in one shot (mirrors setRenderDeviceAndReprobe
  // — routes are set directly because the dashboard-shell effect fires on the
  // NEXT commit and a send would race it).
  const setRunnerMachine = useCallback(async (next: string) => {
    if (!onSaveMachineRoles || !next) return;
    setMachinesBusy(true);
    setRunnerMachineNote(null);
    const renderId = machineRoles?.renderDeviceId || next;
    try {
      await onSaveMachineRoles({
        runnerDeviceId: next,
        ...(machineRoles?.secondaryRunnerDeviceId ? { secondaryRunnerDeviceId: machineRoles.secondaryRunnerDeviceId } : {}),
        renderDeviceId: renderId,
        ...(machineRoles?.secondaryRenderDeviceId ? { secondaryRenderDeviceId: machineRoles.secondaryRenderDeviceId } : {}),
        workspace: machineRoles?.workspace || "runner-clone",
        autoPush: machineRoles?.autoPush || "ask",
      });
      agentClient.setMachineRoleRoutes({ runnerDeviceId: next, renderDeviceId: renderId });
      const nm = deviceNameById.get(next) || next.slice(0, 8);
      setRunnerMachineNote({ tone: "ok", text: `Saved — chat streams from ${nm}.` });
      appendLog(`runner machine → ${nm}`);
    } catch (err) {
      setRunnerMachineNote({
        tone: "error",
        text: `Could not save: ${err instanceof Error ? err.message : String(err)}`,
      });
    } finally {
      setMachinesBusy(false);
    }
  }, [appendLog, deviceNameById, machineRoles?.autoPush, machineRoles?.renderDeviceId, machineRoles?.secondaryRenderDeviceId, machineRoles?.secondaryRunnerDeviceId, machineRoles?.workspace, onSaveMachineRoles]);

  const attachTaskSession = useCallback((task: Task) => {
    stopActiveTaskStream();
    const turns = taskConversationTurns(task);
    const initial = {
      id: task.id,
      deviceId: task.deviceId || connectedDevice?.id,
      title: task.title,
      status: task.status,
      lines: taskOutputLines(task),
      turns,
      pendingUserTurns: mergePendingUserTurns([], taskPendingFollowUpTurns(task), turns),
    };
    setActiveTaskStream((prev) => taskStreamEqual(prev, initial) ? prev : initial);
    setRecentTasks((prev) => {
      const next = [task, ...prev.filter((row) => row.id !== task.id)].slice(0, 8);
      return taskRowsEqual(prev, next) ? prev : next;
    });
    if (task.status !== "queued" && task.status !== "running") return;
    // The turn is live from here on, so start narrating it. startedAt is the
    // task's own timestamp when it has one: attaching to a turn that began five
    // minutes ago must not report "0:00 elapsed".
    const attachedAt = Date.now();
    const taskStartedAt = taskStartedAtMs(task) ?? attachedAt;
    setRunnerTurn({ taskId: task.id, startedAt: taskStartedAt, lastOutputAt: attachedAt });
    // Recovery-wrapped: a severed stream is named + reattached instead of
    // freezing this transcript on its last line. lib/taskStreamWithRecovery.ts.
    taskStreamStopRef.current = streamTaskOutputWithRecovery(
      agentClient,
      task.id,
      (line) => {
        const trimmed = String(line || "").trimEnd();
        if (!trimmed) return;
        setRunnerTurn((prev) => (prev && prev.taskId === task.id ? { ...prev, lastOutputAt: Date.now() } : prev));
        setActiveTaskStream((prev) => {
          if (!prev || prev.id !== task.id) return prev;
          if (prev.lines[prev.lines.length - 1] === trimmed) return prev;
          const lines = [...prev.lines, trimmed];
          return { ...prev, status: "running", lines: lines.slice(-240) };
        });
      },
      (event) => {
        if (event?.type !== "runtime_render_requested") return;
        const reason = String(event.reason || "task-output");
        setAgentRenderRequest({
          id: `${task.id}:${String(event.ts || Date.now())}:${reason}`,
          taskId: task.id,
          reason,
          workDir: typeof event.workDir === "string" ? event.workDir : undefined,
        });
        appendLog(`agent requested render: ${reason}`);
      },
      { onHealth: setTaskStreamHealth },
    );
    // The poll is the status chip's only source of truth, so its failures must
    // narrate themselves. With errors swallowed, a dropped relay left the chip
    // on "running" forever while the box finished the turn — the exact
    // "stuck in running" the stream ladder above already fixed for output.
    let pollFailureStreak = 0;
    let pollSetHealth = false;
    taskPollRef.current = setInterval(() => {
      void agentClient.getTask(task.id).then((fresh) => {
        pollFailureStreak = 0;
        if (pollSetHealth) {
          pollSetHealth = false;
          setTaskStreamHealth(null);
        }
        setActiveTaskStream((prev) => {
          if (!prev || prev.id !== task.id) return prev;
          const lines = taskOutputLines(fresh, prev.lines);
          // The poll is a SECOND source of output. If the SSE leg is severed and
          // only polling still sees the turn growing, the heartbeat must credit
          // that growth — otherwise it reports "no output for 4m" about a runner
          // that is visibly writing, which is the same false alarm in reverse.
          if (lines.length > prev.lines.length) {
            setRunnerTurn((turn) => (turn && turn.taskId === task.id ? { ...turn, lastOutputAt: Date.now() } : turn));
          }
          const serverTurns = taskConversationTurns(fresh);
          const pendingUserTurns = mergePendingUserTurns(prev.pendingUserTurns, taskPendingFollowUpTurns(fresh), serverTurns);
          const next = {
            ...prev,
            status: fresh.status,
            lines,
            turns: serverTurns.length ? serverTurns : prev.turns,
            pendingUserTurns,
          };
          return taskStreamEqual(prev, next) ? prev : next;
        });
        setRecentTasks((prev) => {
          const next = [fresh, ...prev.filter((row) => row.id !== fresh.id)].slice(0, 8);
          return taskRowsEqual(prev, next) ? prev : next;
        });
        if (fresh.status !== "queued" && fresh.status !== "running") stopActiveTaskStream();
      }).catch(() => {
        pollFailureStreak += 1;
        if (pollFailureStreak === 3 && !pollSetHealth) {
          pollSetHealth = true;
          setTaskStreamHealth({
            kind: "reattaching",
            message: "The machine stopped answering status checks — retrying. The task keeps running on the box.",
          });
        }
      });
    }, 2000);
  }, [connectedDevice?.id, stopActiveTaskStream]);

  const startProject = useCallback(async () => {
    const name = projectStartName.trim();
    if (!name || projectStartBusy) return;
    setProjectStartBusy(true);
    setProjectStartError(null);
    try {
      const started = await agentClient.startProject({ name, gitProvider: projectStartGit, palette: "ocean" });
      userSelectedProjectRef.current = true;
      setProjects((current) => [
        { name, path: started.directory, framework: "expo-rn", surfaces: ["mobile", "backend"] },
        ...current.filter((project) => project.path !== started.directory),
      ]);
      setSelectedPath(started.directory);
      setCaps(null);
      setSession(null);
      attachTaskSession(started.task);
      setProjectStartOpen(false);
      setProjectStartName("");
      appendLog(`started ${name} on the AI machine · ${started.gitProvider} · Ocean`);
    } catch (err) {
      setProjectStartError(err instanceof Error ? err.message : String(err));
    } finally {
      setProjectStartBusy(false);
    }
  }, [appendLog, attachTaskSession, projectStartBusy, projectStartGit, projectStartName]);

  // stopRunnerTurn asks the BOX to end the turn — POST /tasks/{id}/stop, which
  // has existed the whole time. Distinct from closeChatSession and from
  // stopActiveTaskStream, both of which only stop this browser from WATCHING:
  // before this button, every control in the chat header let go of the rope and
  // none of them stopped the runner, so a turn the user wanted to abandon kept
  // spending tokens on the box with no way to say stop from the surface they
  // were looking at.
  //
  // It does NOT flip the pill itself. The 2 s poll owns the status, and a UI
  // that reports "stopped" before the box agrees is the false green this
  // codebase keeps re-learning; the button reports only that the request was
  // accepted, and the pill changes when the box actually changes.
  const stopRunnerTurn = useCallback(async () => {
    const taskId = activeTaskStream?.id;
    if (!taskId || runnerStopBusy) return;
    setRunnerStopBusy(true);
    try {
      await agentClient.stopTask(taskId);
      appendLog(`asked the box to stop task ${taskId}`);
    } catch (err) {
      // Say so, in the console the user already reads. A stop that silently
      // failed is worse than no button: they believe it stopped and it did not.
      appendLog(`stop failed: ${err instanceof Error ? err.message : String(err)} — the turn is still running on the box`);
    } finally {
      setRunnerStopBusy(false);
    }
  }, [activeTaskStream?.id, appendLog, runnerStopBusy]);

  // Hand the failure to a coding agent, using the route the AGENT built — its
  // body already carries the compiler's own words, so the runner is not asked to
  // re-derive what the agent already knew. Never invent the prompt here: a
  // client-side guess is how two surfaces end up sending different instructions
  // for the same failure.
  const runRuntimeGapAIFix = useCallback(async (gap: CapabilityGap) => {
    const fix = gap.aiFix;
    if (!fix || runtimeGapBusy) return;
    setRuntimeGapBusy(true);
    try {
      await agentClient.invokeGapFix(fix.method, fix.path, gapFixBody(fix));
      appendLog(`sent the compile error to the runner (${fix.label})`);
      setRuntimeGap(null);
    } catch (err) {
      // Say it failed. A silent no-op here is the false green this whole seam
      // exists to remove.
      appendLog(`could not hand it to the runner: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setRuntimeGapBusy(false);
    }
  }, [appendLog, runtimeGapBusy]);

  const openTaskHistoryItem = useCallback(async (taskId: string) => {
    if (!taskId) return;
    try {
      const task = await agentClient.getTask(taskId);
      attachTaskSession(task);
    } catch (err) {
      appendLog(`task history failed: ${err instanceof Error ? err.message : String(err)}`);
    }
  }, [appendLog, attachTaskSession]);

  const closeChatSession = useCallback(() => {
    stopActiveTaskStream();
    setActiveTaskStream(null);
    setTaskConsolePinned(true);
  }, [stopActiveTaskStream]);

  const deleteChatSession = useCallback(async () => {
    if (!activeTaskStream?.id) return;
    const confirmed = typeof window === "undefined" || window.confirm(`Delete chat session "${activeTaskStream.title}"?`);
    if (!confirmed) return;
    const taskId = activeTaskStream.id;
    const deviceId = activeTaskStream.deviceId || connectedDevice?.id;
    stopActiveTaskStream();
    setActiveTaskStream(null);
    setRecentTasks((prev) => prev.filter((task) => task.id !== taskId));
    if (!token || !deviceId) return;
    try {
      await tombstoneAgentTask(CONVEX_URL, token, deviceId, taskId);
      void agentClient.deleteTask(taskId).catch(() => undefined);
      appendLog(`deleted chat session ${taskId}`);
    } catch {}
  }, [activeTaskStream?.id, activeTaskStream?.title, activeTaskStream?.deviceId, appendLog, connectedDevice?.id, stopActiveTaskStream, token]);

  const startNewChatSession = useCallback(() => {
    closeChatSession();
    setComposer("");
  }, [closeChatSession]);

  const toggleDictation = useCallback(() => {
    if (!sttAvailable) return;
    if (recognitionRef.current) {
      try { recognitionRef.current.stop(); } catch {}
      recognitionRef.current = null;
      setDictating(false);
      return;
    }
    const Ctor = (window as any).SpeechRecognition || (window as any).webkitSpeechRecognition;
    const rec = new Ctor();
    rec.lang = navigator.language || "en-US";
    rec.interimResults = false;
    rec.maxAlternatives = 1;
    rec.onresult = (ev: any) => {
      const text = String(ev.results?.[0]?.[0]?.transcript || "").trim();
      if (!text) return;
      setComposer((prev) => {
        const base = prev.trimEnd();
        return base ? `${base} ${text}` : text;
      });
    };
    rec.onend = () => {
      recognitionRef.current = null;
      setDictating(false);
    };
    rec.onerror = () => {
      recognitionRef.current = null;
      setDictating(false);
    };
    recognitionRef.current = rec;
    setDictating(true);
    try {
      rec.start();
    } catch {
      recognitionRef.current = null;
      setDictating(false);
    }
  }, [sttAvailable]);

  const toggleSpeakSession = useCallback(() => {
    if (!ttsAvailable) return;
    if (speaking) {
      window.speechSynthesis.cancel();
      setSpeaking(false);
      return;
    }
    const lines = activeTaskStream?.lines || [];
    const raw = (lines.length ? lines.slice(-40).join("\n") : activeTaskStream?.title || "").trim();
    // These lines are the RAW task.output stream. On a current agent the prompt
    // frame never enters it; on an older one it is the first ~11 KB, and this
    // path hands 3500 chars of it straight to the browser's speech synthesizer.
    // Slice it out, and refuse outright if anything recognisable survives.
    const text = sliceAfterFrameBoundary(raw).trim();
    if (!text || containsYaverFraming(text)) return;
    const utterance = new SpeechSynthesisUtterance(text.slice(-3500));
    utterance.lang = navigator.language || "en-US";
    utterance.rate = 1;
    utterance.onend = () => setSpeaking(false);
    utterance.onerror = () => setSpeaking(false);
    window.speechSynthesis.cancel();
    setSpeaking(true);
    window.speechSynthesis.speak(utterance);
  }, [activeTaskStream?.lines, activeTaskStream?.title, speaking, ttsAvailable]);

  // Runner/render split: hand the runner box the project's git identity so
  // it can ensure-clone when the source isn't there, plus the push policy
  // that converges the result back (agent task_ensure_clone.go). Only when
  // a split is actually active — single-box tasks carry nothing new.
  const splitTaskFields = useMemo(() => {
    if (!machineSplitActive) return {} as { gitRemote?: string; gitBranch?: string; autoPush?: "never" | "ask" | "always" };
    const remote = selectedProject?.gitRemote || selectedProject?.remote || "";
    return {
      gitRemote: remote || undefined,
      gitBranch: selectedProject?.branch || undefined,
      autoPush: machineRoles?.autoPush || "ask",
    };
  }, [machineSplitActive, machineRoles?.autoPush, selectedProject?.gitRemote, selectedProject?.remote, selectedProject?.branch]);

  const sendPrompt = useCallback(async () => {
    // Preserve the textarea exactly for the runner payload and the blue user
    // bubble. Trim only for the blank-message gate.
    const prompt = composer;
    if (!prompt.trim() || sending) return;
    if (isExplicitRenderPrompt(prompt)) {
      setComposer("");
      setRenderReady(false);
      setExplicitRenderRequest("fast");
      return;
    }
    // A send is NEVER refused while the runner or a reload is busy: the agent
    // queues mid-run follow-ups (PendingFollowUps) and drains them when the
    // current response finishes, Claude-Desktop style. The old silent
    // early-return here ("send paused: preview reload…") went to a log pane
    // the user never sees — from the chat it read as "secondary prompts don't
    // work at all" (2026-07-27).
    setSending(true);
    let optimisticTaskId: string | null = null;
    let optimisticPrompt = "";
    try {
      const existingTaskId = activeTaskStream?.id;
      if (existingTaskId) {
        const optimisticTurn: ConversationTurn = { role: "user", content: prompt, timestamp: new Date().toISOString() };
        optimisticTaskId = existingTaskId;
        optimisticPrompt = prompt;
        setActiveTaskStream((prev) =>
          prev && prev.id === existingTaskId
            ? {
                ...prev,
                status: prev.status === "completed" || prev.status === "review" || prev.status === "failed" || prev.status === "stopped" ? "queued" : prev.status,
                pendingUserTurns: hasConversationTurn(prev.turns, "user", prompt) || hasConversationTurn(prev.pendingUserTurns, "user", prompt)
                  ? prev.pendingUserTurns
                  : [...prev.pendingUserTurns, optimisticTurn].slice(-10),
              }
            : prev);
        setTaskConsolePinned(true);
        window.setTimeout(() => scrollToBottom(taskConsoleRef.current), 0);
        await agentClient.continueTask(existingTaskId, prompt);
        const fresh = await agentClient.getTask(existingTaskId);
        attachTaskSession(fresh);
        // Running-task follow-ups are queued on the agent and do not become
        // persisted Turns until the current answer drains. Keep the local user
        // bubble visible across the detail refresh instead of collapsing back
        // to the previous exchange.
        setActiveTaskStream((prev) =>
          prev && prev.id === existingTaskId && !hasConversationTurn(prev.turns, "user", prompt) && !hasConversationTurn(prev.pendingUserTurns, "user", prompt)
            ? { ...prev, pendingUserTurns: [...prev.pendingUserTurns, optimisticTurn].slice(-10) }
            : prev);
        appendLog(`continued chat session ${existingTaskId}`);
        setComposer("");
        return;
      }
      const effectiveModel = safeModelForRunner(selectedRunner, selectedModel, availableModels);
      if (selectedModel && selectedRunner && effectiveModel !== selectedModel) {
        appendLog(`model corrected for ${selectedRunner}: ${selectedModel} -> ${effectiveModel || "runner default"}`);
      }
      // Pre-send validation against the box's probed opencode config
      // (audit §6 item 5): a model the box has no provider for used to
      // travel all the way to the runner and die minutes later as
      // ProviderModelNotFoundError buried in task output.
      if (normalizeRunnerId(selectedRunner) === "opencode") {
        const validation = validateOpenCodeModel(opencodeSnapshot, effectiveModel);
        if (!validation.ok) throw new Error(validation.error);
      }
      const task = await agentClient.createTask({
        // Title is a one-line label, so collapsing whitespace there is fine.
        // `description` carries the prompt verbatim, newlines and all.
        title: prompt.replace(/\s+/g, " ").slice(0, 80),
        description: prompt,
        runner: selectedRunner || undefined,
        model: effectiveModel,
        reasoningEffort: normalizeRunnerId(selectedRunner) === "codex" ? selectedReasoningEffort || undefined : undefined,
        projectName: selectedProject?.name,
        workDir: selectedProject?.path,
        mcpServers: selectedMcpServers,
        includeYaverMcp,
        ...splitTaskFields,
      });
      attachTaskSession(task);
      appendLog(`task ${task.id} started with ${selectedRunner || "default runner"}${effectiveModel ? ` ${effectiveModel}` : ""}`);
      setComposer("");
    } catch (err) {
      if (optimisticTaskId && optimisticPrompt) {
        setActiveTaskStream((prev) => {
          if (!prev || prev.id !== optimisticTaskId) return prev;
          let idx = -1;
          for (let i = prev.pendingUserTurns.length - 1; i >= 0; i -= 1) {
            if (String(prev.pendingUserTurns[i]?.content ?? "") === optimisticPrompt) {
              idx = i;
              break;
            }
          }
          if (idx < 0) return prev;
          return { ...prev, pendingUserTurns: [...prev.pendingUserTurns.slice(0, idx), ...prev.pendingUserTurns.slice(idx + 1)] };
        });
      }
      appendLog(`task failed: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setSending(false);
    }
  }, [activeTaskStream?.id, appendLog, attachTaskSession, availableModels, composer, includeYaverMcp, opencodeSnapshot, scrollToBottom, selectedMcpServers, selectedModel, selectedProject, selectedReasoningEffort, selectedRunner, sending, splitTaskFields]);

  // "Fix with <runner>" — the route-to-fix on a failed build/preview. It
  // dispatches a coding task on the SAME box+project through the EXACT path
  // the chat composer uses (agentClient.createTask + attachTaskSession, same
  // runner/model resolution) — no new endpoint. attachTaskSession is what the
  // chat send already does, so the fix streams into the same chat pane.
  const fixTaskRunning = !!(fixTaskId && activeTaskStream?.id === fixTaskId && taskStatusMeansRunnerIsCoding(activeTaskStream?.status));
  const dispatchBuildFix = useCallback(async (errorText: string) => {
    if (fixTaskBusy || !selectedProject) return;
    if (selectedRunnerRow && selectedRunnerRow.ready === false) {
      // Say so instead of failing silently — route into the sign-in flow.
      setChatRunnerControlsOpen(true);
      if (selectedRunnerRow.supportsBrowserAuth) void startSelectedRunnerSignIn();
      return;
    }
    setFixTaskBusy(true);
    try {
      // Bounded: keep the END of the captured output — that is where the
      // compiler states the failure.
      const bounded = errorText.trim().slice(-4000);
      const prompt = `The dev preview build failed. Fix the underlying cause, then verify the build compiles. Build error follows:\n\n${bounded}`;
      const effectiveModel = safeModelForRunner(selectedRunner, selectedModel, availableModels);
      const task = await agentClient.createTask({
        title: prompt.replace(/\s+/g, " ").slice(0, 80),
        description: prompt,
        runner: selectedRunner || undefined,
        model: effectiveModel,
        reasoningEffort: normalizeRunnerId(selectedRunner) === "codex" ? selectedReasoningEffort || undefined : undefined,
        projectName: selectedProject?.name,
        workDir: selectedProject?.path,
        mcpServers: selectedMcpServers,
        includeYaverMcp,
        ...splitTaskFields,
      });
      setFixTaskId(task.id);
      attachTaskSession(task);
      appendLog(`fix task ${task.id} started with ${selectedRunner || "default runner"}`);
    } catch (err) {
      appendLog(`fix task failed: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setFixTaskBusy(false);
    }
  }, [appendLog, attachTaskSession, availableModels, fixTaskBusy, includeYaverMcp, selectedMcpServers, selectedModel, selectedProject, selectedReasoningEffort, selectedRunner, selectedRunnerRow, startSelectedRunnerSignIn, splitTaskFields]);

  // "Sync" — git pull origin for the selected project; when the pull fails
  // (merge conflicts, or local changes blocking the fast-forward), hand the
  // repo to the AI runner through the EXACT task path the chat composer and
  // Fix-with-runner use, so the resolution streams into the same chat pane.
  // No new endpoint: /git/pull already exists and returns the raw git
  // output; conflict text is the trigger, not a parse of exit codes.
  const dispatchSync = useCallback(async () => {
    if (syncBusy || !selectedProject) return;
    setSyncBusy(true);
    appendLog(`syncing ${selectedProject.name}: git pull origin…`);
    try {
      const res = await agentClient.gitPull(selectedProject.path);
      const out = String(res?.message || res?.error || "").trim();
      const conflictLike = /conflict|both modified|local changes would be overwritten|not up to date|failed/i.test(out);
      if (res?.error && !conflictLike) {
        appendLog(`sync failed: ${out.slice(0, 400) || res.error}`);
        return;
      }
      if (!res?.error || !conflictLike) {
        appendLog(`sync ok: ${out.slice(0, 400) || "up to date"}`);
        return;
      }
      // Pull refused — conflicts or local changes in the way. The runner
      // resolves them in place; it must never push.
      const bounded = out.slice(-4000) || "git pull origin failed";
      const prompt = `The repository at ${selectedProject.path} failed to sync with its remote (git pull origin). ` +
        `Resolve the state so the working tree is up to date with the remote branch and the project still builds. ` +
        `What git said:\n\n${bounded}\n\n` +
        `How to proceed: if there are merge conflicts, resolve each one to the correct merged content and ` +
        `git add the resolved files; if local uncommitted changes blocked the pull, commit or stash them first, ` +
        `then pull and re-apply; if the branch has diverged, prefer rebase onto origin/<branch>. Never push, ` +
        `never force anything. When done, report which files you changed and how.`;
      const effectiveModel = safeModelForRunner(selectedRunner, selectedModel, availableModels);
      const task = await agentClient.createTask({
        title: `Sync ${selectedProject.name} (resolve git conflicts)`,
        description: prompt,
        runner: selectedRunner || undefined,
        model: effectiveModel,
        reasoningEffort: normalizeRunnerId(selectedRunner) === "codex" ? selectedReasoningEffort || undefined : undefined,
        projectName: selectedProject?.name,
        workDir: selectedProject?.path,
        mcpServers: selectedMcpServers,
        includeYaverMcp,
        ...splitTaskFields,
      });
      attachTaskSession(task);
      appendLog(`sync conflict — ${selectedRunnerName} task ${task.id} is resolving`);
    } catch (err) {
      appendLog(`sync failed: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setSyncBusy(false);
    }
  }, [appendLog, attachTaskSession, availableModels, includeYaverMcp, selectedMcpServers, selectedModel, selectedProject, selectedReasoningEffort, selectedRunner, selectedRunnerName, splitTaskFields, syncBusy]);

  const runnerNotReadyForFix = !!(selectedRunnerRow && selectedRunnerRow.ready === false);
  const fixWithRunnerLabel = fixTaskBusy
    ? "Dispatching fix…"
    : fixTaskRunning
      ? `${selectedRunnerName} is fixing…`
      : runnerNotReadyForFix
        ? `Sign in ${selectedRunnerName} to fix`
        : `Fix with ${selectedRunnerName}`;
  // Rendered INSIDE the failure boxes, directly under the title and ABOVE any
  // log dump — the route to the fix must never be crowded out by advisory
  // content, in pixels or in order.
  const renderFixWithRunnerRow = (errorText: string) => (
    <div className="mt-2 flex flex-wrap items-center gap-2">
      <button
        type="button"
        disabled={fixTaskBusy || fixTaskRunning || !selectedProject || !selectedRunner}
        onClick={() => void dispatchBuildFix(errorText)}
        className="rounded-md bg-[#7c5cff] px-3 py-1.5 text-[11px] font-semibold text-white shadow-sm disabled:cursor-not-allowed disabled:opacity-50"
      >
        {fixWithRunnerLabel}
      </button>
      {fixTaskRunning ? (
        <span className="text-[11px] text-[#667085] dark:text-[#9aa3af]">Streaming in the chat pane.</span>
      ) : null}
    </div>
  );

  const toggleLogRow = useCallback((key: string) => {
    setExpandedLogRows((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }, []);

  const runtimeConsoleRows = useMemo(() => groupRuntimeConsoleLines(log), [log]);

  const copyRuntimeConsole = useCallback(async () => {
    const text = log.length ? log.join("\n") : "No runtime operations yet.";
    try {
      // Full paste-ready trace (2026-08-09) — same shape mobile copies.
      const trace = assembleTrace({
        surface: "web",
        surfaceVersion: typeof document !== "undefined" ? document.title.split("·")[0]?.trim() || undefined : undefined,
        device: connectedDevice ? `${connectedDevice.name} (${connectedDevice.id})` : undefined,
        relay: "configured",
        logTail: text,
      });
      await navigator.clipboard.writeText(trace);
      setRuntimeConsoleCopied(true);
      window.setTimeout(() => setRuntimeConsoleCopied(false), 1400);
    } catch (err) {
      appendLog(`copy runtime console failed: ${err instanceof Error ? err.message : String(err)}`);
    }
  }, [appendLog, log, connectedDevice]);

  const clearRuntimeConsole = useCallback(() => {
    setLog([]);
    setDevLogTail([]);
    setBuildProgress(null);
    appendLog("runtime console cleared");
  }, [appendLog, setBuildProgress, setLog]);

  const copyTaskConsole = useCallback(async () => {
    const text = activeTaskStream?.lines.length
      ? activeTaskStream.lines.join("\n")
      : activeTaskStream?.title || "";
    if (!text.trim()) return;
    try {
      const trace = assembleTrace({
        surface: "web",
        surfaceVersion: typeof document !== "undefined" ? document.title.split("·")[0]?.trim() || undefined : undefined,
        device: connectedDevice ? `${connectedDevice.name} (${connectedDevice.id})` : undefined,
        task: activeTaskStream
          ? {
              id: activeTaskStream.id,
              status: activeTaskStream.status,
              title: activeTaskStream.title,
            }
          : undefined,
        logTail: text,
      });
      await navigator.clipboard.writeText(trace);
      setTaskConsoleCopied(true);
      window.setTimeout(() => setTaskConsoleCopied(false), 1400);
    } catch (err) {
      appendLog(`copy chat output failed: ${err instanceof Error ? err.message : String(err)}`);
    }
  }, [activeTaskStream?.lines, activeTaskStream?.title, activeTaskStream?.id, activeTaskStream?.status, appendLog, connectedDevice]);

  useEffect(() => {
    void loadProjects();
  }, [loadProjects]);

  const loadCapabilities = useCallback(async (project: Project | null = selectedProject) => {
    if (!project) return;
    setBusy(true);
    setError(null);
    setCaps(null);
    setSession(null);
    const runtimeFramework = runtimeFrameworkForProject(project);
    appendLog(`probing render targets for ${project.name} ${runtimeFramework || "unknown"}`);
    try {
      if (!(await ensureMachineRolesReady("targets"))) return;
      // refresh=1: an explicit Load Targets click probes the box fresh — a
      // 2-minute stale caps cache must not hide a just-installed runtime or a
      // newly-landed lane (2026-08-13).
      const next = await agentClient.getRemoteRuntimeCapabilities(project.path, runtimeFramework, true);
      next.targets = [...(next.targets || [])].sort(targetSort);
      setCaps(next);
      const primaryCount = next.targets.filter(isPrimaryRuntimeTarget).length;
      appendLog(`targets: ${primaryCount} primary, ${Math.max(0, next.targets.length - primaryCount)} advanced/unavailable${next.cached ? " (cached)" : ""}`);
    } catch (err) {
      const message = err instanceof Error ? err.message : "Could not load runtime capabilities.";
      if (isAgentAuthErrorMessage(message)) {
        appendLog(`agent auth failed while loading targets: ${message}`);
        setError("Agent auth expired or mismatched. Reconnect this machine, then retry Load Targets.");
      } else {
        setError(message);
      }
    } finally {
      setBusy(false);
    }
  }, [appendLog, ensureMachineRolesReady, selectedProject]);

  const recoverRenderMachine = useCallback(async () => {
    const deviceId = machineRoles?.renderDeviceId || machineRoles?.runnerDeviceId;
    if (!deviceId) return;
    setMachineRecoverBusy(true);
    setError(null);
    setWebPreviewNote(null);
    const boxName = deviceNameById.get(deviceId) || deviceId.slice(0, 8);
    appendLog(`recover renderer ${boxName}`);
    // Connectivity first: "the box is dark" and "the box answered but repair
    // failed" are different failures with different next steps, and the
    // outcome line must say which one happened.
    const conn = await probeRenderConnectivity(deviceId);
    if (conn) {
      appendLog(conn.ok
        ? `render box connection: reachable via ${conn.path || "relay"} (${conn.rttMs}ms)`
        : `render box connection: NONE — ${conn.error}`);
    }
    const connLine = conn
      ? conn.ok
        ? ` Connection to ${boxName}: OK via ${conn.path || "relay"} (${conn.rttMs}ms).`
        : probeFailureAllowsBoxAlive(conn.error)
          ? ` Connection to ${boxName}: NONE (${conn.error}) — the relay has no tunnel to it, so the agent was never contacted and may still be running.`
          : ` Connection to ${boxName}: NONE (${conn.error}) — the agent on it is not answering on any path, so no remote repair can reach it.`
      : "";
    try {
      const result = await agentClient.callOps("machine_repair", {
        action: "restart_agent",
        deviceId,
      });
      const outcome = result.initial?.outcome || result.error || "repair attempted";
      appendLog(`machine repair: ${result.code || (result.ok ? "ok" : "failed")} - ${outcome}`);
      if (!result.ok) {
        if (String(result.code || "") === "unknown_verb" || /unknown verb/i.test(String(outcome))) {
          setError(
            `Machine repair is not supported by the connected agent [unknown_verb] — it predates the repair verb (needs agent 1.99.388+). Update the agent, then retry.${connLine}`,
          );
        } else {
          setError(`Renderer recovery failed: ${outcome}.${connLine}`);
        }
        return;
      }
      await loadCapabilities();
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      appendLog(`machine repair failed: ${message}`);
      setError(`Renderer recovery failed: ${message}.${connLine}`);
    } finally {
      setMachineRecoverBusy(false);
    }
  }, [appendLog, deviceNameById, loadCapabilities, machineRoles?.renderDeviceId, machineRoles?.runnerDeviceId, probeRenderConnectivity]);

  // Render-machine picker on the Load Targets row: save the chosen box as the
  // account favorite, re-point agentClient in the same breath, then re-probe.
  // Routes are set directly (not only via the dashboard-shell effect) because
  // that effect fires on the NEXT commit — an immediate reprobe would race it
  // and hit the old box.
  const setRenderDeviceAndReprobe = useCallback(async (renderId: string) => {
    if (!onSaveMachineRoles || !renderId) return;
    const runnerId = machineRoles?.runnerDeviceId || connectedDevice?.id || renderId;
    setMachinesBusy(true);
    setRenderPickNote(null);
    try {
      await onSaveMachineRoles({
        runnerDeviceId: runnerId,
        ...(machineRoles?.secondaryRunnerDeviceId ? { secondaryRunnerDeviceId: machineRoles.secondaryRunnerDeviceId } : {}),
        renderDeviceId: renderId,
        ...(machineRoles?.secondaryRenderDeviceId ? { secondaryRenderDeviceId: machineRoles.secondaryRenderDeviceId } : {}),
        workspace: machineRoles?.workspace || "runner-clone",
        autoPush: machineRoles?.autoPush || "ask",
      });
      agentClient.setMachineRoleRoutes({ runnerDeviceId: runnerId, renderDeviceId: renderId });
      appendLog(`render machine → ${deviceNameById.get(renderId) || renderId.slice(0, 8)}`);
      void loadCapabilities();
    } catch (err) {
      setRenderPickNote(`Could not set render machine: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setMachinesBusy(false);
    }
  }, [appendLog, connectedDevice?.id, deviceNameById, loadCapabilities, machineRoles?.autoPush, machineRoles?.runnerDeviceId, machineRoles?.secondaryRenderDeviceId, machineRoles?.secondaryRunnerDeviceId, machineRoles?.workspace, onSaveMachineRoles]);

  const recoverAgentAuthAndReloadTargets = useCallback(async () => {
    if (!selectedProject || recoveringAgentAuth) return;
    setRecoveringAgentAuth(true);
    setError(null);
    appendLog("recovering agent auth before loading targets");
    try {
      if (onReconnect) {
        await onReconnect();
      } else {
        agentClient.triggerReconnect();
      }
      appendLog("agent reconnect complete; retrying target probe");
      await loadCapabilities(selectedProject);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      appendLog(`agent auth recovery failed: ${message}`);
      setError(`Agent auth recovery failed: ${message}`);
    } finally {
      setRecoveringAgentAuth(false);
    }
  }, [appendLog, loadCapabilities, onReconnect, recoveringAgentAuth, selectedProject]);

  const createSession = useCallback(async (targetId: string) => {
    if (!selectedProject) return;
    setBusy(true);
    setError(null);
    appendLog(`create session ${targetId}`);
    try {
      // One session per view: starting a new target must REPLACE the old
      // one, not stack a second stream beside it. Before 2026-08-12 clicking
      // "Web UI in browser" while a WebRTC session was live just added
      // another session — the viewer kept showing the prior stream ("it
      // still kept prior one, it should have overwritten").
      if (session && session.id && session.id !== "" && session.status !== "failed" && session.status !== "ended") {
        try {
          await agentClient.closeRemoteRuntimeSession(session.id);
          appendLog(`replaced session ${session.id}`);
        } catch (err) {
          appendLog(`could not close prior session ${session.id}: ${err instanceof Error ? err.message : String(err)}`);
        }
      }
      const next = await agentClient.startRemoteRuntimeSession(
        selectedProject.path,
        runtimeFrameworkForProject(selectedProject),
        targetId,
        "direct-webrtc",
      );
      setSession(next);
      appendLog(`session ${next.id} ${next.status} ${next.transportMode || ""}`);
      try {
        const frame = await agentClient.fetchRemoteRuntimeFrame(next.id);
        appendLog(`first frame ok: ${frame.type || "image"} ${frame.size} bytes`);
      } catch (err) {
        appendLog(`first frame failed: ${err instanceof Error ? err.message : String(err)}`);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not create runtime session.");
    } finally {
      setBusy(false);
    }
  }, [appendLog, selectedProject, session]);

  const openWebUI = useCallback(async () => {
    if (!selectedProject) return;
    setWebPreviewPanelOpen(true);
    setRuntimeControlsOpen(false);
    setRuntimeConsoleOpen(true);
    setWebPreviewUrl(null);
    setWebPreviewFrameReady(false);
    setWebPreviewBusy(true);
    setWebPreviewNote(null);
    setBuildProgress(null);
    setError(null);
    appendLog(`web ui ${selectedProject.name}`);
    try {
      if (!(await ensureMachineRolesReady("web preview"))) return;
      const framework = browserPreviewFrameworkForProject(selectedProject);
      const staticBundleFramework = ["expo", "react-native"].includes(framework);
      if (staticBundleFramework) {
        setWebPreviewNote(`Building ${selectedProject.name} web bundle...`);
        const built = await agentClient.buildWebJSBundle({
          projectName: selectedProject.name,
          projectPath: selectedProject.path,
        });
        if (!built.ok) throw new Error(built.error || "Could not build Web UI bundle.");
        const signedUrl = agentClient.webBundlePreviewUrl(built.bundleUrl);
        if (!signedUrl) throw new Error("No signed Web UI bundle URL is available.");
        setWebPreviewUrl(signedUrl);
        setWebPreviewNote(`Web UI bundle ready: ${built.fileCount} files.`);
        appendLog(`web ui ready ${signedUrl}`);
        return;
      }
      const app = await monorepoWebAppName(selectedProject);
      const response = await agentClient.startDevServer(app ? {
        app,
        root: selectedProject.path,
        platform: "web",
        surface: "web-reload",
      } : {
        framework,
        workDir: selectedProject.path,
        platform: "web",
        surface: "web-reload",
      });
      if (response.mode === "static-bundle") {
        const existingSignedUrl = signedBundlePreviewUrl(response.bundleUrl);
        if (response.bundleReady && existingSignedUrl) {
          setWebPreviewUrl(existingSignedUrl);
          setWebPreviewNote(response.bundleHint || "Web UI bundle ready.");
          appendLog(`web ui ready ${existingSignedUrl}`);
          return;
        }
        const built = await agentClient.buildWebJSBundle({
          projectName: selectedProject.name,
          projectPath: selectedProject.path,
        });
        if (!built.ok) throw new Error(built.error || "Could not build Web UI bundle.");
        const signedUrl = agentClient.webBundlePreviewUrl(built.bundleUrl);
        if (!signedUrl) throw new Error("No signed Web UI bundle URL is available.");
        setWebPreviewUrl(signedUrl);
        setWebPreviewNote(`Web UI bundle ready: ${built.fileCount} files.`);
        appendLog(`web ui ready ${signedUrl}`);
        return;
      }
      const preview = await waitForDevPreviewUrl(response.bundleUrl);
      setWebPreviewUrl(preview.url);
      setWebPreviewNote(response.bundleHint || preview.note);
      appendLog(`web ui ready ${preview.url}`);
    } catch (err) {
      const message = err instanceof Error ? err.message : "Could not open Web UI.";
      setWebPreviewNote(message);
      setError(message);
      appendLog(`web ui failed: ${message}`);
    } finally {
      setWebPreviewBusy(false);
    }
  }, [appendLog, ensureMachineRolesReady, selectedProject]);

  /** Fast/Full Reload for the open web preview (agent 1.99.374+).
   *
   *  Static-bundle lane (Expo / RN): POST /dev/build-native with
   *  mode:"fast" — the agent re-serves the existing bundle when the
   *  built commit still matches HEAD and the tracked tree is clean
   *  (sub-second), and otherwise re-exports on the warm persistent
   *  Metro cache. mode:"full" always re-exports (still warm cache).
   *
   *  Dev-server lane (Vite / Next / Flutter web): POST /dev/reload with
   *  the same mode — Flutter maps fast→"r" (hot reload) and full→"R"
   *  (hot restart). Either way the iframe is re-mounted afterwards. */
  const reloadWebPreview = useCallback(async (kind: "fast" | "full") => {
    if (!selectedProject || webPreviewReloadInFlightRef.current) return;
    if (taskStatusMeansRunnerIsCoding(activeTaskStream?.status)) {
      queuedWebPreviewReloadRef.current = kind;
      setWebPreviewNote(`${kind === "fast" ? "Fast reload" : "Full reload"} queued until the current task finishes.`);
      appendLog(`${kind} reload queued until task ${activeTaskStream?.id || "current"} finishes`);
      return;
    }
    if (webPreviewBusy) {
      queuedWebPreviewReloadRef.current = kind;
      setWebPreviewNote(`${kind === "fast" ? "Fast reload" : "Full reload"} queued until the current preview operation finishes.`);
      appendLog(`${kind} reload queued until current preview operation finishes`);
      return;
    }
    webPreviewReloadInFlightRef.current = true;
    const framework = browserPreviewFrameworkForProject(selectedProject);
    const staticBundleFramework = ["expo", "react-native"].includes(framework);
    appendLog(`${kind} reload ${selectedProject.name}`);
    setRuntimeConsoleOpen(true);
    setError(null);
    setWebPreviewBusy(true);
    // The prior frame stays mounted and visible; only a subtle "reloading…"
    // pill marks the refresh. Cleared when the new frame actually loads.
    setWebPreviewReloading(true);
    if (staticBundleFramework) {
      setWebPreviewNote(kind === "fast" ? "Fast reload: checking bundle freshness..." : "Full reload: re-exporting web bundle...");
      try {
        const built = await agentClient.buildWebJSBundle({
          projectName: selectedProject.name,
          projectPath: selectedProject.path,
          mode: kind,
        });
        if (!built.ok) throw new Error(built.error || "Could not rebuild the Web UI bundle.");
        const signedUrl = agentClient.webBundlePreviewUrl(built.bundleUrl);
        if (signedUrl) setWebPreviewUrl(signedUrl);
        setWebPreviewNonce((n) => n + 1);
        setWebPreviewNote(
          built.reused
            ? "Fast reload: bundle already matches HEAD — re-served instantly."
            : `Web UI bundle rebuilt: ${built.fileCount} files.`,
        );
        appendLog(built.reused ? "fast reload: reused fresh bundle" : `web bundle re-exported (${kind})`);
      } catch (err) {
        const message = err instanceof Error ? err.message : "Reload failed.";
        setWebPreviewNote(message);
        setError(message);
        setWebPreviewReloading(false);
        appendLog(`${kind} reload failed: ${message}`);
      } finally {
        webPreviewReloadInFlightRef.current = false;
        setWebPreviewBusy(false);
      }
      return;
    }
    try {
      setWebPreviewNote(kind === "fast" ? "Fast reload: notifying dev server..." : "Full reload: notifying dev server...");
      await agentClient.reloadDevServer({ mode: kind });
      appendLog(`${kind} reload sent to dev server`);
      setWebPreviewNote(kind === "fast" ? "Fast reload sent." : "Full reload sent.");
    } catch (err) {
      appendLog(`${kind} reload failed: ${err instanceof Error ? err.message : String(err)}`);
      setWebPreviewNote(err instanceof Error ? err.message : `${kind} reload failed`);
      setError(err instanceof Error ? err.message : `${kind} reload failed`);
      setWebPreviewReloading(false);
    } finally {
      webPreviewReloadInFlightRef.current = false;
      setWebPreviewBusy(false);
    }
    setWebPreviewNonce((n) => n + 1);
  }, [activeTaskStream?.id, activeTaskStream?.status, appendLog, selectedProject, webPreviewBusy]);

  const renderCurrentSurface = useCallback(async (source: string) => {
    let dispatched = false;
    if (webPreviewPanelOpen || !session?.id) {
      await reloadWebPreview("fast");
      dispatched = true;
    }
    if (session?.id && canRunGuestOnRemoteTarget(session.targetId)) {
      dispatched = true;
      try {
        const result = await agentClient.sendRemoteRuntimeCommand(
          session.id,
          "run-guest",
          source,
          selectedProject?.path,
        );
        if ((result as any)?.session) setSession((result as any).session as RemoteRuntimeSession);
      } catch (err) {
        appendLog(`render failed: ${err instanceof Error ? err.message : String(err)}`);
      }
    }
    if (!dispatched) setWebPreviewNote("Open a preview before rendering updates.");
  }, [appendLog, reloadWebPreview, selectedProject?.path, session?.id, session?.targetId, webPreviewPanelOpen]);

  // A bare chat command such as "re-render" is an explicit user action, not a
  // coding turn. The same reload function queues it while coding and drains it
  // once the current turn is safe, preserving queued prompt order.
  useEffect(() => {
    if (!explicitRenderRequest) return;
    if (taskStatusMeansRunnerIsCoding(activeTaskStream?.status) || (activeTaskStream?.pendingUserTurns?.length ?? 0) > 0) {
      setWebPreviewNote("Render queued until the coding queue finishes.");
      return;
    }
    setExplicitRenderRequest(null);
    void renderCurrentSurface("explicit-chat-render");
  }, [activeTaskStream?.pendingUserTurns?.length, activeTaskStream?.status, explicitRenderRequest, renderCurrentSurface]);

  useEffect(() => {
    const queuedKind = queuedWebPreviewReloadRef.current;
    if (!queuedKind || !selectedProject || webPreviewBusy || taskStatusMeansRunnerIsCoding(activeTaskStream?.status) || (activeTaskStream?.pendingUserTurns?.length ?? 0) > 0) return;
    queuedWebPreviewReloadRef.current = null;
    appendLog(`${queuedKind} reload queue draining`);
    void reloadWebPreview(queuedKind);
  }, [activeTaskStream?.pendingUserTurns?.length, activeTaskStream?.status, appendLog, reloadWebPreview, selectedProject, webPreviewBusy]);

  const closeWebPreview = useCallback(() => {
    setWebPreviewPanelOpen(false);
    setRuntimeControlsOpen(false);
    setRuntimeConsoleOpen(true);
    setWebPreviewUrl(null);
    setWebPreviewFrameReady(false);
    setWebPreviewReloading(false);
    setWebPreviewNote(null);
    setBuildProgress(null);
  }, []);

  const stopWebPreview = useCallback(async () => {
    if (webPreviewStopping) return;
    setWebPreviewStopping(true);
    setRuntimeConsoleOpen(true);
    setWebPreviewNote("Stopping preview...");
    appendLog("stopping active preview");
    try {
      const result = await agentClient.stopDevServer();
      const failed = result.ok === false || result.verified === false;
      const message = result.message || (failed ? "Stop incomplete." : "Preview stopped.");
      appendLog(`stop preview: ${message}`);
      if (failed) {
        setError(message);
      } else {
        setWebPreviewUrl(null);
        setWebPreviewFrameReady(false);
      }
      setWebPreviewNote(message);
      if (!failed) setBuildProgress(null);
      if (result.buildsCancelled) appendLog(`cancelled ${result.buildsCancelled} in-flight build(s)`);
    } catch (err) {
      const message = err instanceof Error ? err.message : "Stop preview failed.";
      appendLog(`stop preview failed: ${message}`);
      setError(message);
      setWebPreviewNote(message);
    } finally {
      setWebPreviewStopping(false);
    }
  }, [appendLog, webPreviewStopping]);

  useEffect(() => {
    if (!activeTaskStream || !selectedProject) return;
    const structuredRequest = agentRenderRequest?.taskId === activeTaskStream.id ? agentRenderRequest : null;
    if (!taskStatusAllowsRender(activeTaskStream.status)) return;
    if (!structuredRequest) return;
    if (!autoRenderVibing.enabled) {
      setRenderReady(true);
      return;
    }
    const key = [
      activeTaskStream.id,
      activeTaskStream.status,
      // Per-TURN discriminator. A follow-up runs on the SAME task and ends at
      // "completed" again, so WITHOUT this the second turn's key equals the
      // first turn's key → the render is deduped away and the preview never
      // updates past the first vibe message ("only first message works",
      // 2026-07-28). Output lines accumulate per turn; the count plus the tail
      // of the final line make each completed turn a distinct key even if the
      // line buffer is capped (identical count, different content).
      String(activeTaskStream.lines?.length ?? 0),
      (activeTaskStream.lines?.[activeTaskStream.lines.length - 1] ?? "").slice(-80),
      `mcp:${structuredRequest.id}`,
      webPreviewPanelOpen ? "web" : "",
      session?.id || "",
      session?.targetId || "",
    ].join(":");
    if (autoRenderRef.current === key) return;

    let dispatched = false;
    if (webPreviewPanelOpen && webPreviewUrl && !webPreviewBusy) {
      appendLog(structuredRequest ? `task finished: refreshing Web UI (${structuredRequest.reason})` : "task finished: refreshing Web UI");
      void reloadWebPreview("fast");
      dispatched = true;
    }
    if (session?.id && canRunGuestOnRemoteTarget(session.targetId)) {
      appendLog(structuredRequest
        ? `task finished: refreshing ${session.targetLabel || session.targetId} stream (${structuredRequest.reason})`
        : `task finished: refreshing ${session.targetLabel || session.targetId} stream`);
      void agentClient.sendRemoteRuntimeCommand(session.id, "run-guest", "agent-requested-render", structuredRequest.workDir || selectedProject.path)
        .then((result) => {
          if ((result as any)?.session) setSession((result as any).session as RemoteRuntimeSession);
        })
        .catch((err) => appendLog(`agent-requested render failed: ${err instanceof Error ? err.message : String(err)}`));
      dispatched = true;
    }
    // Only burn the dedupe key once a render actually went out. If the iframe
    // lane was skipped because a prior reload was still busy (and there is no
    // session lane), leave the key unset so the effect retries when
    // webPreviewBusy clears — otherwise this turn's render is lost for good.
    if (dispatched) {
      autoRenderRef.current = key;
      setRenderReady(false);
    }
  }, [
    activeTaskStream,
    agentRenderRequest,
    autoRenderVibing.enabled,
    appendLog,
    reloadWebPreview,
    selectedProject,
    session?.id,
    session?.targetId,
    session?.targetLabel,
    webPreviewBusy,
    webPreviewPanelOpen,
    webPreviewUrl,
  ]);

  const scrollMobilePreviewFrame = useCallback((event: WheelEvent<HTMLDivElement>) => {
    const frame = mobilePreviewFrameRef.current;
    if (!frame) return;
    try {
      const win = frame.contentWindow;
      const doc = frame.contentDocument;
      if (!win || !doc) return;
      const root = doc.scrollingElement || doc.documentElement || doc.body;
      const beforeTop = root?.scrollTop ?? 0;
      const beforeLeft = root?.scrollLeft ?? 0;
      win.scrollBy({ top: event.deltaY, left: event.deltaX, behavior: "auto" });
      const afterTop = root?.scrollTop ?? beforeTop;
      const afterLeft = root?.scrollLeft ?? beforeLeft;
      if (afterTop !== beforeTop || afterLeft !== beforeLeft) {
        event.preventDefault();
        event.stopPropagation();
      }
    } catch {
      // Cross-origin previews keep native iframe scrolling; the dashboard must
      // not turn a security boundary into a broken preview.
    }
  }, []);

  useEffect(() => {
    if (!webPreviewUrl) {
      setWebPreviewFrameReady(false);
      setWebPreviewReloading(false);
      return;
    }
    setWebPreviewFrameReady(false);
  }, [webPreviewUrl]);

  useEffect(() => {
    if (webPreviewPanelOpen && webPreviewFrameReady) setRuntimeConsoleOpen(false);
  }, [webPreviewFrameReady, webPreviewPanelOpen]);

  useEffect(() => {
    if (!intent || intent.kind !== "runtime" || projects.length === 0) return;
    const project = projects.find((p) => projectMatches(p, intent.projectQuery)) || projects[0];
    setSelectedPath(project.path);
    void (async () => {
      await loadCapabilities(project);
      const wanted = targetIdFor(intent.surface, intent.platform);
      if (!wanted) return;
      const nextCaps = await agentClient.getRemoteRuntimeCapabilities(project.path, runtimeFrameworkForProject(project));
      const target = nextCaps.targets.find((t) => t.id === wanted);
      if (!target) {
        appendLog(`chat target not found: ${wanted}`);
        return;
      }
      if (!target.enabled) {
        setError(target.reason || `${target.label} is unavailable.`);
        appendLog(`chat target disabled: ${target.reason || wanted}`);
        return;
      }
      await createSession(wanted);
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [intent?.nonce, projects.length]);

  useEffect(() => {
    if (!intent || intent.kind !== "tmux") return;
    const q = String(intent.tmuxQuery || intent.projectQuery || "").trim();
    if (!q) return;
    appendLog(`chat requested Yaver session ${q}`);
    onOpenTmux?.(q);
  }, [appendLog, intent, onOpenTmux]);

  // Zero-click render: when this machine has BOTH a saved default project and
  // a saved default target for it, opening the Vibing tab starts the preview
  // by itself (user directive 2026-07-27). Once per mount; an explicit chat
  // intent wins; never while a session/preview is already up or busy — the
  // reload queue owns every re-render after the first.
  const autoRenderedRef = useRef(false);
  useEffect(() => {
    if (autoRenderedRef.current || !caps || busy || session || webPreviewPanelOpen) return;
    if (intent) {
      autoRenderedRef.current = true;
      return;
    }
    if (!connectedDevice?.id || !selectedProject || !selectedProjectIsSavedDefault) return;
    const saved = savedRuntimeTargetFor(connectedDevice.id, selectedProject.name);
    if (!saved) return;
    const target = caps.targets?.find((t) => t.id === saved.targetId);
    if (!target || !target.enabled) return;
    autoRenderedRef.current = true;
    appendLog(`auto-render: ${target.id} — saved default for ${selectedProject.name} on this machine`);
    void createSession(target.id);
  }, [appendLog, busy, caps, connectedDevice?.id, createSession, intent, savedRuntimeTargetFor, selectedProject, selectedProjectIsSavedDefault, session, webPreviewPanelOpen]);

  const targetProbeFailurePlan = error ? classifyRuntimeTargetProbeFailure(error) : null;

  // Whenever the probe-failure card is visible, answer the user's first
  // question unprompted: does a connection to the render box exist at all?
  // Freshness guard (15s) terminates the setState → re-run cycle and keeps
  // the periodic devices refresh from turning this into a probe storm.
  useEffect(() => {
    if (!error || !effectiveRenderDeviceId || isAgentAuthErrorMessage(error)) return;
    if (
      renderConnCheck &&
      renderConnCheck.deviceId === effectiveRenderDeviceId &&
      (renderConnCheck.pinging || Date.now() - renderConnCheck.at < 15_000)
    ) {
      return;
    }
    void probeRenderConnectivity(effectiveRenderDeviceId);
  }, [error, effectiveRenderDeviceId, probeRenderConnectivity, renderConnCheck]);

  return (
    <div
      className="grid h-full min-h-0 gap-3 bg-[#f2f4f7] p-3 text-[#1f2933] dark:bg-[#101318] dark:text-[#e6e8ec] sm:p-4 xl:[grid-template-columns:minmax(0,1fr)_10px_var(--runtime-chat-width)]"
      style={runtimeGridStyle}
    >
      <div className="min-h-0 min-w-0 space-y-3 overflow-y-auto">
        {!webPreviewPanelOpen ? (
        <div className="flex flex-wrap items-end gap-2">
          <label className="min-w-[260px] flex-1">
            <span className="mb-1 block text-[11px] font-semibold uppercase tracking-wide text-[#5d6673] dark:text-[#9aa3af]">Project</span>
            <select
              value={selectedPath}
              onChange={(e) => { userSelectedProjectRef.current = true; setSelectedPath(e.target.value); setRuntimeProjectNote(null); setCaps(null); setSession(null); setWebPreviewPanelOpen(false); setRuntimeControlsOpen(false); setWebPreviewUrl(null); setWebPreviewNote(null); }}
              className="h-10 w-full rounded-md border border-[#d7dce3] bg-white px-3 text-sm text-[#1f2933] dark:border-[#2a3039] dark:bg-[#161b22] dark:text-[#e6e8ec]"
            >
              {projects.map((p) => (
                <option key={p.path} value={p.path}>{p.name} · {p.framework || "unknown"}</option>
              ))}
            </select>
          </label>
          <button
            type="button"
            onClick={() => { setProjectStartError(null); setProjectStartOpen(true); }}
            className="inline-flex h-10 shrink-0 items-center rounded-md bg-[#075985] px-3 text-xs font-semibold text-white hover:bg-[#0c6f9f]"
          >
            Start a project
          </button>
          {/* h-10 matches the select's height exactly; shrink-0 keeps the
              button from compressing below it when the row wraps tight. */}
          <button
            disabled={!selectedProject || busy}
            onClick={() => void loadCapabilities()}
            className="inline-flex h-10 shrink-0 items-center rounded-md bg-[#1f2933] px-3 text-xs font-semibold text-white disabled:opacity-40"
          >
            {busy ? "Loading targets..." : "Load Targets"}
          </button>
          <button
            type="button"
            disabled={!connectedDevice?.id || !selectedProject || runtimeProjectSaving || selectedProjectIsSavedDefault}
            onClick={() => void saveRuntimeProjectDefault()}
            className="inline-flex h-10 shrink-0 items-center rounded-md border border-[#d7dce3] bg-white px-3 text-xs font-semibold text-[#475467] disabled:cursor-not-allowed disabled:opacity-40 dark:border-[#2a3039] dark:bg-[#161b22] dark:text-[#d7dce3]"
            title="Save this project as the default runtime target for this machine"
          >
            {runtimeProjectSaving ? "Saving..." : selectedProjectIsSavedDefault ? "Default" : "Save default"}
          </button>
          {/* AI-machine picker, on the same Load Targets row as Render —
              the runner box that executes coding tasks. Mirrors the render
              picker: saves the account-favorite role + re-routes in one
              shot. Added 2026-08-12 (user directive: "next to render machine
              make AI machine too as option"); reuses setRunnerMachine so the
              two pickers can't drift. */}
          {onSaveMachineRoles && roleEligibleDevices.length > 0 ? (
            <label className="shrink-0">
              <span className="mb-1 block text-[11px] font-semibold uppercase tracking-wide text-[#5d6673] dark:text-[#9aa3af]">AI machine</span>
              <select
                value={machineRoles?.runnerDeviceId || connectedDevice?.id || ""}
                disabled={machinesBusy || busy}
                onChange={(e) => {
                  const next = e.target.value;
                  if (next) void setRunnerMachine(next);
                }}
                title="Which machine runs the AI coding tasks — chat streams from this box"
                className="h-10 rounded-md border border-[#d7dce3] bg-white px-2 text-xs text-[#1f2933] dark:border-[#2a3039] dark:bg-[#161b22] dark:text-[#e6e8ec]"
              >
                {!machineRoles?.runnerDeviceId ? <option value="">— pick a machine —</option> : null}
                {roleEligibleDevices.map((device) => (
                  <option key={device.id} value={device.id}>
                    {desktopDeviceLabel(device, desktopSurface)}{device.online === false ? " (offline)" : ""}
                  </option>
                ))}
              </select>
            </label>
          ) : null}
          {/* Render-machine picker, ON the row that probes it. The Route
              editor in the chat aside also sets this, but the box a probe is
              about to hit must be visible and changeable where the probe is
              launched — not two panes away. */}
          {onSaveMachineRoles && roleEligibleDevices.length > 0 ? (
            <label className="shrink-0">
              <span className="mb-1 block text-[11px] font-semibold uppercase tracking-wide text-[#5d6673] dark:text-[#9aa3af]">Render machine</span>
              <select
                value={effectiveRenderDeviceId || ""}
                disabled={machinesBusy || busy}
                onChange={(e) => void setRenderDeviceAndReprobe(e.target.value)}
                title="Which machine boots simulators, emulators, and previews — Load Targets probes this box"
                className="h-10 rounded-md border border-[#d7dce3] bg-white px-2 text-xs text-[#1f2933] dark:border-[#2a3039] dark:bg-[#161b22] dark:text-[#e6e8ec]"
              >
                {!effectiveRenderDeviceId ? <option value="">— pick a machine —</option> : null}
                {roleEligibleDevices.map((device) => (
                  <option key={device.id} value={device.id}>
                    {desktopDeviceLabel(device, desktopSurface)}{device.online === false ? " (offline)" : ""}
                  </option>
                ))}
              </select>
            </label>
          ) : null}
          {renderPickNote ? (
            <span className="inline-flex h-10 items-center text-xs text-rose-600 dark:text-rose-300">{renderPickNote}</span>
          ) : null}
          {runnerMachineNote ? (
            <span className={`inline-flex h-10 items-center text-xs ${runnerMachineNote.tone === "ok" ? "text-emerald-600 dark:text-emerald-300" : "text-rose-600 dark:text-rose-300"}`}>
              {runnerMachineNote.text}
            </span>
          ) : null}
          {runtimeProjectNote ? (
            <span className="inline-flex h-10 min-w-[160px] items-center text-xs text-[#667085] dark:text-[#9aa3af]">{runtimeProjectNote}</span>
          ) : null}
        </div>
        ) : null}

        {error ? (
          <div className={`rounded-md border p-3 text-sm ${
            isAgentAuthErrorMessage(error)
              ? "border-amber-500/30 bg-amber-500/10 text-amber-800 dark:text-amber-100"
              : "border-rose-500/30 bg-rose-500/10 text-rose-700 dark:text-rose-200"
          }`}>
            <div className="font-semibold">
              {isAgentAuthErrorMessage(error) ? "Agent auth needs refresh" : "Runtime target probe failed"}
            </div>
            <div className="mt-1">{error}</div>
            {!isAgentAuthErrorMessage(error) && effectiveRenderDeviceId &&
              renderConnCheck && renderConnCheck.deviceId === effectiveRenderDeviceId ? (
              <div className="mt-2 text-xs">
                {renderConnCheck.pinging ? (
                  <span className="text-[#667085] dark:text-[#9aa3af]">
                    Checking connection to {effectiveRenderBoxName}…
                  </span>
                ) : renderConnCheck.ok ? (
                  <span className="font-medium text-emerald-700 dark:text-emerald-300">
                    ✓ Connection to {effectiveRenderBoxName}: OK via {renderConnCheck.path || "relay"} ({renderConnCheck.rttMs}ms) — the box is up; the failure is in the operation, not the connection.
                  </span>
                ) : (
                  <span className="font-medium">
                    ✗ No connection to {effectiveRenderBoxName} — {renderConnCheck.error || "no relay, tunnel, or direct path answered"}.{" "}
                    {probeFailureAllowsBoxAlive(renderConnCheck.error) ? (
                      <>
                        The relay answered about itself, not about the box: it has no tunnel to {effectiveRenderBoxName}, so the agent was never contacted and may well be running. Sign that box in (`yaver auth` on the machine) or use Recover below — a power-cycle would not address this.
                      </>
                    ) : (
                      <>
                        No relay, tunnel, or direct path answered, so nothing remote can repair it. Power it on (or power-cycle it), or pick another render machine.
                      </>
                    )}
                    {renderBoxPressure ? (
                      <span className="mt-1 block">
                        Its last heartbeat reported {renderBoxPressure.canFork === false
                          ? "process-table exhaustion — the box cannot start any new process, so the agent cannot restart itself and SSH cannot execute commands"
                          : `critical resource pressure${renderBoxPressure.reasons?.length ? ` (${renderBoxPressure.reasons[0]})` : ""}`}
                        {renderBoxPressure.at ? `, ${formatPressureAge(renderBoxPressure.at)}` : ""}. A physical power-cycle is the expected recovery.
                      </span>
                    ) : null}
                  </span>
                )}
              </div>
            ) : null}
            {isAgentAuthErrorMessage(error) ? (
              <div className="mt-3 flex flex-wrap items-center gap-2">
                <button
                  type="button"
                  onClick={() => void recoverAgentAuthAndReloadTargets()}
                  disabled={recoveringAgentAuth || busy}
                  className="rounded-md border border-amber-500/40 bg-amber-500/15 px-3 py-1.5 text-xs font-semibold text-amber-800 disabled:opacity-40 dark:text-amber-100"
                >
                  {recoveringAgentAuth ? "Recovering..." : "Reconnect & Retry"}
                </button>
                <button
                  type="button"
                  onClick={() => void loadCapabilities()}
                  disabled={recoveringAgentAuth || busy}
                  className="rounded-md border border-[#d7dce3] bg-white px-3 py-1.5 text-xs font-semibold text-[#475467] disabled:opacity-40 dark:border-[#2a3039] dark:bg-[#101318] dark:text-[#d7dce3]"
                >
                  Retry only
                </button>
                {connectedDevice ? (
                  <span className="text-xs text-amber-800/70 dark:text-amber-100/70">
                    {connectedDevice.name || connectedDevice.id}
                  </span>
                ) : null}
              </div>
            ) : targetProbeFailurePlan && !targetProbeFailurePlan.showFixWithRunner ? (
              // Relay-presence failure: the relay answered FOR the render box —
              // it has no live tunnel. A coding agent cannot fix an offline
              // machine, so this branch names the box and offers deterministic
              // routes instead of "Fix with <runner>".
              <div className="mt-2 space-y-2">
                {targetProbeFailurePlan.kind === "relay-presence" ? (
                  // The routing-config throw ("only reachable over a relay…")
                  // already names its own cause + remedy; this sentence is for
                  // the relay-presence 502 only.
                  <div className="text-xs">
                    The render machine <span className="font-semibold">{effectiveRenderBoxName || "(unknown)"}</span> has
                    no live relay connection, so the target probe never reached it. Bring that box online, or pick a
                    different render machine above.
                  </div>
                ) : null}
                {targetProbeFailurePlan.kind === "project-missing" ? (
                  // The project is not on the render box. The picker is now
                  // sourced exclusively from that renderer, so this can only
                  // be a stale selection or a checkout removed after inventory.
                  <div className="text-xs">
                    <span className="font-semibold">{effectiveRenderBoxName || "The render machine"}</span> has no
                    project by that name now. Reload its project inventory, or choose another renderer; Yaver will not
                    substitute a project from a different machine.
                  </div>
                ) : null}
                {targetProbeFailurePlan.kind === "agent-verb-skew" ? (
                  // Version skew is deterministic: the installed agent predates
                  // a verb this dashboard calls. Never offer "Fix with runner"
                  // here — an LLM cannot add a verb to a released binary.
                  <div className="text-xs">
                    The agent answering this call is older than the dashboard expects (machine repair needs agent{" "}
                    <span className="font-semibold">1.99.388+</span>). Update it on{" "}
                    <span className="font-semibold">{runnerBoxName || "the runner box"}</span> with{" "}
                    <code className="rounded bg-black/10 px-1 dark:bg-white/10">npm install -g yaver-cli@latest</code>, then retry.
                  </div>
                ) : null}
                <div className="flex flex-wrap items-center gap-2">
                  {targetProbeFailurePlan.retry ? (
                    <button
                      type="button"
                      onClick={() => void loadCapabilities()}
                      disabled={busy || machinesBusy || machineRecoverBusy}
                      className="rounded-md border border-[#d7dce3] bg-white px-3 py-1.5 text-xs font-semibold text-[#475467] disabled:opacity-40 dark:border-[#2a3039] dark:bg-[#101318] dark:text-[#d7dce3]"
                    >
                      Retry probe
                    </button>
                  ) : null}
                  {machineSplitActive && (machineRoles?.renderDeviceId || machineRoles?.runnerDeviceId) ? (
                    <button
                      type="button"
                      onClick={() => void recoverRenderMachine()}
                      disabled={busy || machinesBusy || machineRecoverBusy}
                      className="rounded-md border border-amber-500/35 bg-amber-500/10 px-3 py-1.5 text-xs font-semibold text-amber-800 disabled:opacity-40 dark:text-amber-100"
                    >
                      {machineRecoverBusy ? "Recovering renderer..." : `Recover ${effectiveRenderBoxName || "renderer"}`}
                    </button>
                  ) : null}
                  {targetProbeFailurePlan.useRunnerFallback && machineSplitActive && machineRoles?.runnerDeviceId ? (
                    <button
                      type="button"
                      onClick={() => void setRenderDeviceAndReprobe(machineRoles.runnerDeviceId)}
                      disabled={busy || machinesBusy || machineRecoverBusy}
                      className="rounded-md border border-emerald-500/30 bg-emerald-500/10 px-3 py-1.5 text-xs font-semibold text-emerald-700 disabled:opacity-40 dark:text-emerald-200"
                    >
                      Render on {runnerBoxName} instead
                    </button>
                  ) : null}
                  {desktopSurface.isDesktop && desktopSurface.localDeviceId &&
                  desktopSurface.localDeviceId !== effectiveRenderDeviceId ? (
                    <button
                      type="button"
                      onClick={() => void setRenderDeviceAndReprobe(desktopSurface.localDeviceId!)}
                      disabled={busy || machinesBusy || machineRecoverBusy}
                      className="rounded-md border border-indigo-500/30 bg-indigo-500/10 px-3 py-1.5 text-xs font-semibold text-indigo-700 disabled:opacity-40 dark:text-indigo-200"
                    >
                      Render on This PC
                    </button>
                  ) : null}
                </div>
              </div>
            ) : (
              // Non-auth failure (target probe / web ui failed): the route to
              // the fix, in place — dispatch the selected runner on this
              // box+project with the captured error.
              renderFixWithRunnerRow(error)
            )}
          </div>
        ) : null}

        {caps ? (
          <div className="space-y-3">
            {(() => {
              const enabledTargets = caps.targets.filter((target) => target.enabled);
              const groupedTargets = enabledTargets.reduce<Record<string, RemoteRuntimeTarget[]>>((acc, target) => {
                const group = runtimeTargetGroup(target);
                acc[group] = [...(acc[group] ?? []), target];
                return acc;
              }, {});
              const unavailableTargets = caps.targets.filter((target) => !target.enabled);
              const primaryTargets = caps.targets.filter(isPrimaryRuntimeTarget);
              const groupOrder: ReturnType<typeof runtimeTargetGroup>[] = ["browser", "simulator", "container", "device", "advanced"];
              // Whole card is clickable (same handler as Open); the visible
              // button stays for affordance + keyboard access and stops
              // propagation so a button click never fires the card twice.
              const renderTarget = (target: RemoteRuntimeTarget, compact = false) => (
                <div
                  key={target.id}
                  onClick={() => {
                    if (target.enabled && !busy) void createSession(target.id);
                  }}
                  className={`rounded-md border p-3 ${target.enabled ? "cursor-pointer border-[#d7dce3] bg-white transition-colors hover:border-[#98a2b3] dark:border-[#2a3039] dark:bg-[#161b22] dark:hover:border-[#3d4551]" : "border-[#e1e5eb] bg-[#f8fafc] dark:border-[#252b33] dark:bg-[#121720]"}`}
                >
                  {/* No flex-wrap: the Open button holds a consistent
                      top-right on every card; text truncates instead. */}
                  <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0 flex-1">
                      <div className={`truncate text-sm font-medium ${target.enabled ? "text-[#1f2933] dark:text-[#e6e8ec]" : "text-[#667085] dark:text-[#8b949e]"}`}>{target.label}</div>
                      <div className="mt-1 truncate text-xs text-[#667085] dark:text-[#9aa3af]">
                        {target.displaySurface || target.surface || "runtime"} · {target.id} · {target.viewport ? `${target.viewport.width}x${target.viewport.height}` : target.requiredCli || "tools"}
                      </div>
                    </div>
                    {target.enabled ? (
                      <button
                        type="button"
                        onClick={(event) => {
                          event.stopPropagation();
                          void saveRuntimeTargetDefault(target);
                        }}
                        title={
                          savedRuntimeTargetFor(connectedDevice?.id, selectedProject?.name)?.targetId === target.id
                            ? "Default target for this project on this machine"
                            : "Set as default target — with a default project, Vibing renders automatically"
                        }
                        aria-label={`Set ${target.label} as the default target`}
                        className={`shrink-0 rounded-md px-1.5 py-1.5 text-sm ${
                          savedRuntimeTargetFor(connectedDevice?.id, selectedProject?.name)?.targetId === target.id
                            ? "text-amber-500"
                            : "text-[#98a2b3] hover:text-amber-500"
                        }`}
                      >
                        {savedRuntimeTargetFor(connectedDevice?.id, selectedProject?.name)?.targetId === target.id ? "★" : "☆"}
                      </button>
                    ) : null}
                    <button
                      disabled={!target.enabled || busy}
                      onClick={(event) => {
                        event.stopPropagation();
                        void createSession(target.id);
                      }}
                      aria-label={targetActionLabel(target)}
                      className="shrink-0 rounded-md bg-amber-500/15 px-3 py-1.5 text-xs font-semibold text-amber-700 disabled:cursor-not-allowed disabled:opacity-40 dark:text-amber-200"
                    >
                      {target.enabled ? "Open" : "Unavailable"}
                    </button>
                  </div>
                  {target.reason ? (
                    <div className={`mt-2 text-xs ${compact ? "text-surface-500" : "text-rose-700 dark:text-rose-300"}`}>{target.reason}</div>
                  ) : null}
                </div>
              );
              const renderGroup = (group: ReturnType<typeof runtimeTargetGroup>, targets: RemoteRuntimeTarget[]) => {
                if (targets.length === 0) return null;
                return (
                  <section key={group} className="space-y-2">
                    <div className="text-[11px] font-semibold uppercase tracking-wide text-[#5d6673] dark:text-[#9aa3af]">
                      {runtimeGroupLabels[group]}
                    </div>
                    <div className="grid gap-2 md:grid-cols-2">
                      {targets.map((target) => renderTarget(target))}
                    </div>
                  </section>
                );
              };
              return (
                <>
                  {!webPreviewPanelOpen ? (
                  <section className="space-y-2">
                    <div className="text-[11px] font-semibold uppercase tracking-wide text-[#5d6673] dark:text-[#9aa3af]">
                      Browser
                    </div>
                    <div className="grid gap-2 md:grid-cols-2">
                      <div
                        onClick={() => {
                          if (selectedProject && !webPreviewBusy) void openWebUI();
                        }}
                        className={`rounded-md border border-[#d7dce3] bg-white p-3 dark:border-[#2a3039] dark:bg-[#161b22] ${
                          selectedProject && !webPreviewBusy ? "cursor-pointer transition-colors hover:border-[#98a2b3] dark:hover:border-[#3d4551]" : ""
                        }`}
                      >
                        <div className="flex items-start justify-between gap-3">
                          <div className="min-w-0 flex-1">
                            <div className="truncate text-sm font-medium text-[#1f2933] dark:text-[#e6e8ec]">Web UI in browser</div>
                            <div className="mt-1 truncate text-xs text-[#667085] dark:text-[#9aa3af]">
                              browser · direct iframe · dev server
                            </div>
                          </div>
                          <button
                            disabled={!selectedProject || webPreviewBusy}
                            onClick={(event) => {
                              event.stopPropagation();
                              void openWebUI();
                            }}
                            aria-label="Open Web UI in browser"
                            className="shrink-0 rounded-md bg-sky-500/15 px-3 py-1.5 text-xs font-semibold text-sky-700 disabled:cursor-not-allowed disabled:opacity-40 dark:text-sky-200"
                          >
                            {webPreviewBusy ? "Building..." : "Open"}
                          </button>
                        </div>
                        {webPreviewNote ? <div className="mt-2 text-xs text-[#667085] dark:text-[#9aa3af]">{webPreviewNote}</div> : null}
                      </div>
                      {(groupedTargets.browser ?? []).map((target) => renderTarget(target))}
                    </div>
                  </section>
                  ) : null}
                  {webPreviewPanelOpen ? (
                    <section className="space-y-2">
                      <div className="flex min-h-[62px] items-center justify-between gap-3 rounded-md border border-[#d7dce3] bg-white px-3 py-2.5 dark:border-[#2a3039] dark:bg-[#161b22]">
                        <div className="flex min-w-0 flex-wrap items-center gap-2.5">
                          <img src="/icon-192.png" alt="Yaver" className="h-7 w-7 rounded-md" />
                          <div className="min-w-0 self-center">
                            <div className="truncate text-sm font-semibold text-[#1f2933] dark:text-[#e6e8ec]">
                              {selectedProject?.name || "Preview"}
                            </div>
                            <div className="truncate text-[11px] text-[#667085] dark:text-[#9aa3af]">
                              {webPreviewNote || (webPreviewBusy ? "Building web preview..." : selectedProjectIsMobile ? "Mobile Web UI" : "Web UI")}
                            </div>
                          </div>
                          {selectedProjectIsMobile ? (
                            <div className="inline-flex h-8 items-center rounded-md border border-[#d7dce3] bg-white p-0.5 dark:border-[#2a3039] dark:bg-[#161b22]">
                              {(Object.keys(mobilePreviewDevices) as MobilePreviewMode[]).map((mode) => {
                                const device = mobilePreviewDevices[mode];
                                const selected = mobilePreviewMode === mode;
                                return (
                                  <button
                                    key={mode}
                                    type="button"
                                    onClick={() => chooseMobilePreviewMode(mode)}
                                    className={`h-7 rounded px-2 text-[11px] font-medium ${
                                      selected
                                        ? "bg-[#1f2933] text-white dark:bg-[#e6e8ec] dark:text-[#101318]"
                                        : "text-[#667085] hover:text-[#1f2933] dark:text-[#9aa3af] dark:hover:text-[#e6e8ec]"
                                    }`}
                                  >
                                    {device.label}
                                  </button>
                                );
                              })}
                            </div>
                          ) : null}
                        </div>
                        <div className="flex shrink-0 items-center gap-1">
                          <button
                            type="button"
                            onClick={() => setRuntimeControlsOpen((v) => !v)}
                            title="Preview controls"
                            aria-label="Preview controls"
                            className={`flex h-8 w-8 items-center justify-center rounded-md border ${
                              runtimeControlsOpen
                                ? "border-sky-500/40 bg-sky-500/10 text-sky-700 dark:text-sky-200"
                                : "border-[#d7dce3] bg-white text-[#475467] hover:text-[#1f2933] dark:border-[#2a3039] dark:bg-[#101318] dark:text-[#d7dce3]"
                            }`}
                          >
                            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                              <line x1="4" y1="21" x2="4" y2="14" />
                              <line x1="4" y1="10" x2="4" y2="3" />
                              <line x1="12" y1="21" x2="12" y2="12" />
                              <line x1="12" y1="8" x2="12" y2="3" />
                              <line x1="20" y1="21" x2="20" y2="16" />
                              <line x1="20" y1="12" x2="20" y2="3" />
                              <line x1="2" y1="14" x2="6" y2="14" />
                              <line x1="10" y1="8" x2="14" y2="8" />
                              <line x1="18" y1="16" x2="22" y2="16" />
                            </svg>
                          </button>
                          <button
                            type="button"
                            onClick={() => void reloadWebPreview("fast")}
                            disabled={!selectedProject || webPreviewStopping}
                            title="Fast Reload — re-serve the fresh bundle instantly, or hot-reload the dev server"
                            aria-label="Fast Reload"
                            className="h-8 rounded-md bg-[#1f2933] px-2.5 text-[11px] font-semibold text-white disabled:opacity-40 dark:bg-[#e6e8ec] dark:text-[#101318]"
                          >
                            Fast Reload
                          </button>
                          <button
                            type="button"
                            onClick={() => void reloadWebPreview("full")}
                            disabled={!selectedProject || webPreviewStopping}
                            title="Full Reload — force a re-export / hot restart (warm cache, never a cold start)"
                            aria-label="Full Reload"
                            className="h-8 rounded-md border border-[#d7dce3] bg-white px-2.5 text-[11px] font-semibold text-[#475467] hover:text-[#1f2933] disabled:opacity-40 dark:border-[#2a3039] dark:bg-[#101318] dark:text-[#d7dce3]"
                          >
                            Full Reload
                          </button>
                          <button
                            type="button"
                            onClick={() => void stopWebPreview()}
                            disabled={webPreviewStopping}
                            title="Stop the active preview on this machine"
                            aria-label="Stop preview"
                            className="h-8 rounded-md border border-rose-500/30 bg-rose-500/10 px-2.5 text-[11px] font-semibold text-rose-700 disabled:opacity-40 dark:text-rose-200"
                          >
                            {webPreviewStopping ? "Stopping..." : "Stop"}
                          </button>
                          <button
                            type="button"
                            onClick={closeWebPreview}
                            title="Close preview"
                            aria-label="Close preview"
                            className="flex h-8 w-8 items-center justify-center rounded-md border border-[#d7dce3] bg-white text-[#475467] hover:text-[#1f2933] dark:border-[#2a3039] dark:bg-[#101318] dark:text-[#d7dce3]"
                          >
                            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                              <line x1="18" y1="6" x2="6" y2="18" />
                              <line x1="6" y1="6" x2="18" y2="18" />
                            </svg>
                          </button>
                        </div>
                      </div>
                      {runtimeGap ? (
                        // The ROUTE, above every diagnostic: a named capability
                        // gap has a deterministic one-tap fix, so it must never
                        // sit under (or be crowded out by) advisory output.
                        <div className="mb-2 rounded-md border border-amber-500/30 bg-amber-500/10 p-3">
                          <div className="text-xs font-semibold text-amber-700 dark:text-amber-200">{gapTitle(runtimeGap)}</div>
                          {gapBody(runtimeGap) ? (
                            <div className="mt-1 text-[11px] leading-4 text-amber-700/90 dark:text-amber-200/80">{gapBody(runtimeGap)}</div>
                          ) : null}
                          {gapFixLabel(runtimeGap) ? (
                            <button
                              type="button"
                              disabled={runtimeGapBusy}
                              onClick={() => void runRuntimeGapFix(runtimeGap)}
                              className="mt-2 rounded-md bg-emerald-600 px-3 py-1.5 text-[11px] font-semibold text-white disabled:opacity-60"
                            >
                              {runtimeGapBusy ? "Installing… (output in the runtime console)" : gapFixLabel(runtimeGap)}
                            </button>
                          ) : gapAIFixLabel(runtimeGap) ? (
                            // NO DETERMINISTIC FIXER, BUT A CODING AGENT CAN TRY.
                            // The constraint still shows — the user must know the
                            // dev server is fine and it is their source that does
                            // not compile — and the escalation sits under it as a
                            // route rather than as advice to retype the error into
                            // the chat by hand.
                            <div className="mt-2">
                              <div className="text-[11px] text-amber-700/90 dark:text-amber-200/80">
                                {runtimeGap.constraint}
                              </div>
                              <button
                                type="button"
                                disabled={runtimeGapBusy}
                                onClick={() => void runRuntimeGapAIFix(runtimeGap)}
                                className="mt-2 rounded-md bg-[#7c5cff] px-3 py-1.5 text-[11px] font-semibold text-white disabled:opacity-60"
                              >
                                {runtimeGapBusy ? "Sending to the runner…" : gapAIFixLabel(runtimeGap)}
                              </button>
                            </div>
                          ) : (
                            <div className="mt-2 text-[11px] text-amber-700/90 dark:text-amber-200/80">
                              {runtimeGap.constraint || "Yaver has no installer for this on this machine."}
                            </div>
                          )}
                        </div>
                      ) : null}
                      {runtimeCompileCard ? (
                        // Compile failure on a healthy server (audit gap D5):
                        // without this, a broken build renders as a blank
                        // iframe under a green status. Lead with the agent's
                        // words; full output stays in the runtime console.
                        <div className="mb-2 rounded-md border border-rose-500/30 bg-rose-500/10 p-3">
                          <div className="text-xs font-semibold text-rose-700 dark:text-rose-300">{runtimeCompileCard.title}</div>
                          {/* Route first, log dump second — the action row
                              renders ABOVE the error wall, always. */}
                          {renderFixWithRunnerRow(devLogTail.length ? devLogTail.join("\n") : runtimeCompileCard.detail)}
                          <pre className="mt-1 max-h-40 overflow-auto whitespace-pre-wrap break-words font-mono text-[11px] leading-4 text-rose-700/90 dark:text-rose-200/90">{runtimeCompileCard.detail}</pre>
                          <div className="mt-1 text-[10px] text-rose-700/70 dark:text-rose-200/60">
                            The dev server is still running — the preview stays blank until the next successful compile. Full output is in the runtime console.
                          </div>
                        </div>
                      ) : null}
                      {buildProgress ? (
                        // Compact single-line transport strip while the box
                        // compiles — deliberately slim (h-8) so it never eats
                        // the vertical space the mobile app preview needs.
                        <div className="flex h-8 items-center gap-2.5 rounded-md border border-[#d7dce3] bg-white px-2.5 text-[10px] text-[#667085] dark:border-[#2a3039] dark:bg-[#161b22] dark:text-[#9aa3af]">
                          <span className="min-w-0 shrink truncate font-medium text-[#344054] dark:text-[#d7dce3]">
                            {buildProgress.topic}{buildProgress.phase ? ` · ${buildProgress.phase}` : ""}
                          </span>
                          <div className="h-0.5 min-w-[60px] flex-1 overflow-hidden rounded-full bg-[#e4e7ec] dark:bg-[#242b35]">
                            <div className="h-full rounded-full bg-sky-500 transition-[width] duration-500" style={{ width: `${buildProgress.pct}%` }} />
                          </div>
                          <span className="shrink-0 font-medium tabular-nums text-[#344054] dark:text-[#d7dce3]">{buildProgress.pct}%</span>
                          <span className="hidden shrink-0 tabular-nums opacity-70 sm:inline">
                            {formatBuildElapsed(buildNowTick - buildProgress.startedAt)} · last output {Math.max(0, Math.round((buildNowTick - buildProgress.lastOutputAt) / 1000))}s ago
                          </span>
                        </div>
                      ) : null}
                      {!webPreviewUrl ? (
                        <RuntimePreviewLoadingSurface
                          mobile={selectedProjectIsMobile}
                          device={mobilePreviewDevice}
                          scale={mobilePreviewScale}
                          note={webPreviewNote}
                          projectName={selectedProject?.name}
                        />
                      ) : selectedProjectIsMobile ? (
                        <div
                          className="flex w-full justify-center overflow-hidden rounded-md border border-[#d7dce3] bg-[#0b0d11] p-3 dark:border-[#2a3039]"
                          style={{ minHeight: Math.round(mobilePreviewOuterHeight * mobilePreviewScale) + 24 }}
                        >
                          <div style={{ width: mobilePreviewOuterWidth * mobilePreviewScale, height: mobilePreviewOuterHeight * mobilePreviewScale }}>
                          <div
                            className="shrink-0 overflow-hidden bg-[#1f2933] p-[10px] shadow-2xl"
                            style={{
                              borderRadius: mobilePreviewDevice.radius,
                              width: mobilePreviewOuterWidth,
                              height: mobilePreviewOuterHeight,
                              transform: `scale(${mobilePreviewScale})`,
                              transformOrigin: "top left",
                            }}
                          >
                            <div
                              className="relative overflow-hidden bg-black"
                              onWheel={scrollMobilePreviewFrame}
                              style={{
                                borderRadius: Math.max(0, mobilePreviewDevice.radius - 10),
                                width: mobilePreviewDevice.width,
                                height: mobilePreviewDevice.height,
                              }}
                            >
                              <iframe
                                key="mobile-preview"
                                ref={(n) => {
                                  mobilePreviewFrameRef.current = n;
                                  domInspectFrameRef.current = n;
                                }}
                                src={previewFrameSrc ?? undefined}
                                width={mobilePreviewDevice.width}
                                height={mobilePreviewDevice.height}
                                className="border-none bg-white"
                                style={{ touchAction: "pan-y" }}
                                title={`${mobilePreviewDevice.label} Web UI preview`}
                                onLoad={() => window.setTimeout(() => { setWebPreviewFrameReady(true); setWebPreviewReloading(false); }, 900)}
                              />
                              {!webPreviewFrameReady && !webPreviewBusy && !webPreviewReloading ? (
                                <div className="absolute inset-0">
                                  <RuntimePreviewLoadingScreen
                                    note={webPreviewNote || "Starting mobile web preview..."}
                                    projectName={selectedProject?.name}
                                  />
                                </div>
                              ) : null}
                              {webPreviewReloading ? (
                                <div className="pointer-events-none absolute inset-x-0 top-1.5 flex justify-center">
                                  <div className="flex items-center gap-1.5 rounded-full border border-white/15 bg-black/55 px-2.5 py-1 text-[10px] font-semibold text-white/90 backdrop-blur">
                                    <span className="h-2 w-2 animate-spin rounded-full border border-emerald-400 border-t-transparent" />
                                    reloading…
                                  </div>
                                </div>
                              ) : null}
                            </div>
                          </div>
                          </div>
                        </div>
                      ) : (
                        <div className="relative h-[520px] w-full overflow-hidden rounded-md border border-[#d7dce3] bg-[#0b0d11]">
                          <iframe
                            key="web-preview"
                            ref={(n) => {
                              domInspectFrameRef.current = n;
                            }}
                            src={previewFrameSrc ?? undefined}
                            className="h-full w-full border-none bg-white"
                            title="Project Web UI preview"
                            onLoad={() => window.setTimeout(() => { setWebPreviewFrameReady(true); setWebPreviewReloading(false); }, 900)}
                          />
                          {!webPreviewFrameReady && !webPreviewBusy && !webPreviewReloading ? (
                            <div className="absolute inset-0">
                              <RuntimePreviewLoadingScreen
                                note={webPreviewNote || "Starting web preview..."}
                                projectName={selectedProject?.name}
                              />
                            </div>
                          ) : null}
                          {webPreviewReloading ? (
                            <div className="pointer-events-none absolute inset-x-0 top-2 flex justify-center">
                              <div className="flex items-center gap-1.5 rounded-full border border-white/15 bg-black/55 px-2.5 py-1 text-[10px] font-semibold text-white/90 backdrop-blur">
                                <span className="h-2 w-2 animate-spin rounded-full border border-emerald-400 border-t-transparent" />
                                reloading…
                              </div>
                            </div>
                          ) : null}
                        </div>
                      )}
                    </section>
                  ) : null}
                  {!webPreviewPanelOpen || runtimeControlsOpen ? (
                  <>
                  {webPreviewPanelOpen ? (
                    <div className="rounded-md border border-[#d7dce3] bg-white p-3 dark:border-[#2a3039] dark:bg-[#161b22]">
                      <div className="mb-2 text-[11px] font-semibold uppercase tracking-wide text-[#5d6673] dark:text-[#9aa3af]">Project</div>
                      <div className="flex flex-wrap items-center gap-2">
                        <label className="min-w-[260px] flex-1">
                          <select
                            value={selectedPath}
              onChange={(e) => { userSelectedProjectRef.current = true; setSelectedPath(e.target.value); setRuntimeProjectNote(null); setCaps(null); setSession(null); setWebPreviewPanelOpen(false); setRuntimeControlsOpen(false); setWebPreviewUrl(null); setWebPreviewNote(null); }}
                            className="h-10 w-full rounded-md border border-[#d7dce3] bg-white px-3 text-sm text-[#1f2933] dark:border-[#2a3039] dark:bg-[#101318] dark:text-[#e6e8ec]"
                          >
                            {projects.map((p) => (
                              <option key={p.path} value={p.path}>{p.name} · {p.framework || "unknown"}</option>
                            ))}
                          </select>
                        </label>
                        <button
                          disabled={!selectedProject || busy}
                          onClick={() => void loadCapabilities()}
                          className="inline-flex h-10 shrink-0 items-center rounded-md bg-[#1f2933] px-3 text-xs font-semibold text-white disabled:opacity-40"
                        >
                          {busy ? "Loading..." : "Load Targets"}
                        </button>
                        <button
                          type="button"
                          disabled={!connectedDevice?.id || !selectedProject || runtimeProjectSaving || selectedProjectIsSavedDefault}
                          onClick={() => void saveRuntimeProjectDefault()}
                          className="inline-flex h-10 shrink-0 items-center rounded-md border border-[#d7dce3] bg-white px-3 text-xs font-semibold text-[#475467] disabled:cursor-not-allowed disabled:opacity-40 dark:border-[#2a3039] dark:bg-[#101318] dark:text-[#d7dce3]"
                        >
                          {runtimeProjectSaving ? "Saving..." : selectedProjectIsSavedDefault ? "Default" : "Save default"}
                        </button>
                        <button
                          type="button"
                          disabled={!selectedProject || syncBusy}
                          onClick={() => void dispatchSync()}
                          title="git pull origin; if the pull conflicts, the AI runner resolves it here in the chat stream"
                          className="inline-flex h-10 shrink-0 items-center rounded-md border border-[#d7dce3] bg-white px-3 text-xs font-semibold text-[#475467] disabled:cursor-not-allowed disabled:opacity-40 dark:border-[#2a3039] dark:bg-[#101318] dark:text-[#d7dce3]"
                        >
                          {syncBusy ? "Syncing…" : "Sync"}
                        </button>
                      </div>
                      {runtimeProjectNote ? (
                        <div className="mt-2 text-xs text-[#667085] dark:text-[#9aa3af]">{runtimeProjectNote}</div>
                      ) : null}
                    </div>
                  ) : null}
                  {primaryTargets.length === 0 && enabledTargets.length > 0 ? (
                    <div className="grid gap-2 md:grid-cols-2">
                      {enabledTargets.map((target) => renderTarget(target))}
                    </div>
                  ) : (
                    <div className="space-y-4">
                      {groupOrder.filter((group) => group !== "browser").map((group) => renderGroup(group, groupedTargets[group] ?? []))}
                    </div>
                  )}
                  {unavailableTargets.length ? (
                    <div className="rounded-md border border-[#d7dce3] bg-[#f8fafc] dark:border-[#2a3039] dark:bg-[#121720]">
                      <button
                        type="button"
                        onClick={() => setShowAdvancedTargets((v) => !v)}
                        className="flex w-full items-center justify-between gap-3 px-3 py-2 text-left"
                      >
                        <span className="text-xs font-semibold uppercase tracking-wide text-[#5d6673] dark:text-[#9aa3af]">
                          Unavailable targets
                        </span>
                        <span className="text-xs text-[#667085] dark:text-[#9aa3af]">
                          {unavailableTargets.length} {showAdvancedTargets ? "hide" : "show"}
                        </span>
                      </button>
                      {showAdvancedTargets ? (
                        <div className="grid gap-2 border-t border-[#d7dce3] p-3 dark:border-[#2a3039] md:grid-cols-2">
                          {unavailableTargets.map((target) => renderTarget(target, true))}
                        </div>
                      ) : null}
                    </div>
                  ) : null}
                  </>
                  ) : null}
                </>
              );
            })()}
          </div>
        ) : (
          <div className="rounded-md border border-[#d7dce3] bg-white p-4 text-sm text-[#667085] dark:border-[#2a3039] dark:bg-[#161b22] dark:text-[#9aa3af]">
            {busy ? "Probing browser, simulator, emulator, Redroid, and physical-device render lanes from this machine..." : "Load targets to boot watchOS, Wear OS, TV, phone, browser, and other runtime surfaces from this machine."}
          </div>
        )}

        {session ? (
          <div className="space-y-2">
            <div className="text-xs text-[#667085] dark:text-[#9aa3af]">
              session <span className="font-mono text-[#344054] dark:text-[#d7dce3]">{session.id}</span> · {session.targetLabel} · {session.status}
            </div>
            <RemoteRuntimeViewer session={session} onSessionChange={setSession} onClose={() => setSession(null)} />
          </div>
        ) : null}
      </div>

      <button
        type="button"
        className="group hidden min-h-0 cursor-col-resize items-stretch justify-center rounded-md border border-transparent py-1 hover:border-[#d7dce3] focus:outline-none focus:ring-2 focus:ring-[#7c5cff]/40 dark:hover:border-[#2a3039] xl:flex"
        onPointerDown={beginChatPaneResize}
        onPointerMove={moveChatPaneResize}
        onPointerUp={endChatPaneResize}
        onPointerCancel={endChatPaneResize}
        title="Drag to resize Chat"
        aria-label="Resize Chat pane"
      >
        <span className="h-full w-1 rounded-full bg-[#d7dce3] transition-colors group-hover:bg-[#98a2b3] dark:bg-[#2a3039] dark:group-hover:bg-[#667085]" />
      </button>

      <aside className="flex min-h-0 flex-col gap-3 overflow-hidden border-t border-[#d7dce3] pt-3 dark:border-[#2a3039] xl:border-l-0 xl:border-t-0 xl:pt-0">
        <div className="flex min-h-[420px] flex-1 flex-col overflow-hidden rounded-md border border-[#d7dce3] bg-white shadow-sm dark:border-[#2a3039] dark:bg-[#141820]">
          <div className="border-b border-[#e4e7ec] px-4 py-3 dark:border-[#242b35]">
            <div className="flex items-start justify-between gap-3">
              <div className="min-w-0">
                <div className="text-[11px] font-semibold uppercase tracking-wide text-[#5d6673] dark:text-[#9aa3af]">Chat</div>
                <div className="mt-1 min-w-0 truncate text-sm font-semibold text-[#1f2933] dark:text-[#e6e8ec]">
                  {selectedProject?.name || "No project selected"}
                </div>
                {runtimeCapabilitySummary ? (
                  <div className="mt-1 max-w-[320px] truncate text-[11px] font-medium text-[#667085] dark:text-[#9aa3af]">
                    {runtimeCapabilitySummary}
                  </div>
                ) : null}
              </div>
              <div className="flex shrink-0 items-center gap-1.5">
                <button
                  type="button"
                  onClick={() => void copyTaskConsole()}
                  disabled={!activeTaskStream}
                  title="Copy chat output"
                  aria-label="Copy chat output"
                  className="flex h-7 w-7 items-center justify-center rounded-md border border-[#d7dce3] bg-[#f8fafc] text-[#475467] hover:text-[#1f2933] disabled:cursor-not-allowed disabled:opacity-40 dark:border-[#2a3039] dark:bg-[#101318] dark:text-[#d7dce3]"
                >
                  {taskConsoleCopied ? (
                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                      <path d="M20 6 9 17l-5-5" />
                    </svg>
                  ) : (
                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                      <rect x="9" y="9" width="13" height="13" rx="2" />
                      <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
                    </svg>
                  )}
                </button>
                <span className={`rounded-full border px-2 py-1 text-[10px] font-semibold uppercase tracking-wide ${chatStatusTone}`}>
                  {activeTaskStream?.status || "Ready"}
                </span>
              </div>
            </div>
            <div className="mt-3">
              {/* Runner + model only — machine routing is shown by the
                  AI/Render pickers on the Load Targets row above, so the
                  chat header does not restate it (LESS IS MORE: a second
                  rendering of the same state was a chip grid nobody read;
                  2026-08-12). */}
              <div className="grid min-h-11 min-w-0 grid-cols-[minmax(0,0.8fr)_minmax(0,0.7fr)_auto] items-center gap-2 rounded-md border border-[#d7dce3] bg-[#f8fafc] px-2 py-1.5 dark:border-[#2a3039] dark:bg-[#101318]">
                <span className="min-w-0 truncate text-[11px] leading-5 text-[#667085] dark:text-[#9aa3af]">
                  <span className="font-semibold uppercase tracking-wide">Runner</span>
                  <span className="mx-1.5 text-[#98a2b3]">/</span>
                  <span className="font-medium text-[#344054] dark:text-[#d7dce3]">{selectedRunnerRow?.name || selectedRunner || "No runner"}</span>
                </span>
                <span className="min-w-0 truncate text-[11px] leading-5 text-[#667085] dark:text-[#9aa3af]">
                  <span className="font-semibold uppercase tracking-wide">Model</span>
                  <span className="mx-1.5 text-[#98a2b3]">/</span>
                  <span className="font-medium text-[#344054] dark:text-[#d7dce3]">{effectiveChatModel || selectedModel || "runner default"}</span>
                </span>
                <span className="flex shrink-0 items-center gap-1.5">
                  <button
                    type="button"
                    onClick={() => setChatRunnerControlsOpen((open) => !open)}
                    className="flex h-8 shrink-0 items-center rounded-md border border-[#d7dce3] bg-white px-2 text-[10px] font-semibold text-[#475467] hover:text-[#1f2933] dark:border-[#2a3039] dark:bg-[#0b0d11] dark:text-[#d7dce3]"
                    aria-expanded={chatRunnerControlsOpen}
                  >
                    {chatRunnerControlsOpen ? "Fold" : "Edit"}
                  </button>
                  {onSaveMachineRoles ? (
                    <button
                      type="button"
                      onClick={openMachinesEditor}
                      className="flex h-8 shrink-0 items-center rounded-md border border-[#d7dce3] bg-white px-2 text-[10px] font-semibold text-[#475467] hover:text-[#1f2933] dark:border-[#2a3039] dark:bg-[#0b0d11] dark:text-[#d7dce3]"
                      aria-expanded={machinesEditOpen}
                    >
                      {machinesEditOpen ? "Fold" : "Route"}
                    </button>
                  ) : null}
                </span>
              </div>
              {machinesEditOpen ? (
                <div className="mt-2 grid gap-2 rounded-md border border-[#d7dce3] bg-[#f8fafc] p-2 dark:border-[#2a3039] dark:bg-[#101318]">
                  <div className="grid gap-2 sm:grid-cols-2">
                    <label className="min-w-0">
                      <span className="mb-1 block text-[10px] font-semibold uppercase tracking-wide text-[#667085] dark:text-[#9aa3af]">AI runner (chat streams from)</span>
                      <select
                        value={machinesDraftRunner}
                        onChange={(event) => {
                          setMachinesDraftRunner(event.target.value);
                          setMachinesTest((prev) => ({ ...prev, runner: {} }));
                        }}
                        className="w-full rounded-md border border-[#d7dce3] bg-white px-2 py-1.5 text-xs text-[#1f2933] dark:border-[#2a3039] dark:bg-[#0b0d11] dark:text-[#e6e8ec]"
                      >
                        <option value="">— pick a machine —</option>
                        {roleEligibleDevices.map((device) => (
                          <option key={device.id} value={device.id}>
                            {desktopDeviceLabel(device, desktopSurface)}{device.online === false ? " (offline)" : ""}
                          </option>
                        ))}
                      </select>
                      <span className="mt-1 flex items-center gap-2">
                        <button
                          type="button"
                          disabled={machinesBusy || !!machinesTest.runner.pinging || !machinesDraftRunner}
                          onClick={() => void testMachineRole("runner")}
                          className="rounded border border-[#d7dce3] bg-white px-1.5 py-0.5 text-[10px] font-semibold text-[#475467] hover:text-[#1f2933] disabled:cursor-not-allowed disabled:opacity-40 dark:border-[#2a3039] dark:bg-[#0b0d11] dark:text-[#d7dce3]"
                        >
                          {machinesTest.runner.pinging ? "Testing…" : "Test connection"}
                        </button>
                        {machinesTest.runner.ok === true ? (
                          <span className="text-[10px] font-semibold text-emerald-600 dark:text-emerald-400">✓ {machinesTest.runner.rttMs}ms</span>
                        ) : machinesTest.runner.ok === false ? (
                          <span className="min-w-0 truncate text-[10px] text-rose-600 dark:text-rose-300" title={machinesTest.runner.error}>
                            {machinesTest.runner.error}
                          </span>
                        ) : null}
                      </span>
                    </label>
                    <label className="min-w-0">
                      <span className="mb-1 block text-[10px] font-semibold uppercase tracking-wide text-[#667085] dark:text-[#9aa3af]">Renderer (builds + previews)</span>
                      <select
                        value={machinesDraftRender}
                        onChange={(event) => {
                          setMachinesDraftRender(event.target.value);
                          setMachinesTest((prev) => ({ ...prev, render: {} }));
                        }}
                        className="w-full rounded-md border border-[#d7dce3] bg-white px-2 py-1.5 text-xs text-[#1f2933] dark:border-[#2a3039] dark:bg-[#0b0d11] dark:text-[#e6e8ec]"
                      >
                        <option value="">same as runner</option>
                        {roleEligibleDevices.map((device) => (
                          <option key={device.id} value={device.id}>
                            {desktopDeviceLabel(device, desktopSurface)}{device.online === false ? " (offline)" : ""}
                          </option>
                        ))}
                      </select>
                      <span className="mt-1 flex items-center gap-2">
                        <button
                          type="button"
                          disabled={machinesBusy || !!machinesTest.render.pinging || (!machinesDraftRender && !machinesDraftRunner)}
                          onClick={() => void testMachineRole("render")}
                          className="rounded border border-[#d7dce3] bg-white px-1.5 py-0.5 text-[10px] font-semibold text-[#475467] hover:text-[#1f2933] disabled:cursor-not-allowed disabled:opacity-40 dark:border-[#2a3039] dark:bg-[#0b0d11] dark:text-[#d7dce3]"
                        >
                          {machinesTest.render.pinging ? "Testing…" : "Test connection"}
                        </button>
                        {machinesTest.render.ok === true ? (
                          <span className="text-[10px] font-semibold text-emerald-600 dark:text-emerald-400">✓ {machinesTest.render.rttMs}ms</span>
                        ) : machinesTest.render.ok === false ? (
                          <span className="min-w-0 truncate text-[10px] text-rose-600 dark:text-rose-300" title={machinesTest.render.error}>
                            {machinesTest.render.error}
                          </span>
                        ) : null}
                      </span>
                    </label>
                  </div>
                  <div className="flex flex-wrap items-center gap-2">
                    <button
                      type="button"
                      disabled={machinesBusy || !machinesDraftRunner}
                      onClick={() => void saveMachineRoles()}
                      className="rounded-md border border-emerald-500/30 bg-emerald-500/10 px-2.5 py-1.5 text-xs font-semibold text-emerald-700 disabled:cursor-not-allowed disabled:opacity-40 dark:text-emerald-200"
                    >
                      Save routing
                    </button>
                    {machineRoles && onClearMachineRoles ? (
                      <button
                        type="button"
                        disabled={machinesBusy}
                        onClick={() => void clearMachineRoles()}
                        className="rounded-md border border-[#d7dce3] px-2.5 py-1.5 text-xs font-semibold text-[#475467] disabled:opacity-40 hover:text-[#1f2933] dark:border-[#2a3039] dark:text-[#d7dce3]"
                      >
                        Single-box
                      </button>
                    ) : null}
                    <span className="text-[10px] text-[#98a2b3] dark:text-[#667085]">
                      Applies account-wide · also in Settings → Machine roles
                    </span>
                  </div>
                </div>
              ) : null}
              {machinesNote ? (
                <p className="mt-1 text-[11px] leading-4 text-[#667085] dark:text-[#9aa3af]">{machinesNote}</p>
              ) : null}
              {chatRunnerControlsOpen ? (
                <div className="mt-2 grid gap-2 rounded-md border border-[#d7dce3] bg-[#f8fafc] p-2 dark:border-[#2a3039] dark:bg-[#101318]">
                  {/* Machine selection lives on the Load Targets row (AI
                      machine + Render machine side by side, 2026-08-12) — NOT
                      duplicated here. This panel is runner/model/mode only. */}
                  <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
                    <label className="min-w-0">
                      <span className="mb-1 block text-[10px] font-semibold uppercase tracking-wide text-[#667085] dark:text-[#9aa3af]">Runner</span>
                      <select
                        ref={runnerSelectRef}
                        value={selectedRunner}
                        onChange={(event) => setSelectedRunner(event.target.value)}
                        className="w-full rounded-md border border-[#d7dce3] bg-white px-2 py-1.5 text-xs text-[#1f2933] dark:border-[#2a3039] dark:bg-[#0b0d11] dark:text-[#e6e8ec]"
                      >
                        {runners.length === 0 ? <option value="">No runners detected</option> : null}
                        {runners.map((runner) => (
                          <option key={runner.id} value={runner.id}>
                            {runner.name}{runner.ready === false ? " (sign-in needed)" : ""}
                          </option>
                        ))}
                      </select>
                    </label>
                    {availableModels.length > 0 ? (
                      <label className="min-w-0">
                        <span className="mb-1 block text-[10px] font-semibold uppercase tracking-wide text-[#667085] dark:text-[#9aa3af]">Model</span>
                        <select
                          ref={modelSelectRef}
                          value={selectedModel}
                          onChange={(event) => {
                            const nextId = event.target.value;
                            const nextModel = availableModels.find((model) => model.id === nextId);
                            const efforts = nextModel?.supportedReasoningEfforts?.map((item) => item.reasoningEffort) || [];
                            setSelectedModel(nextId);
                            if (!selectedReasoningEffort || !efforts.includes(selectedReasoningEffort)) {
                              setSelectedReasoningEffort(nextModel?.defaultReasoningEffort || efforts[0] || "");
                            }
                          }}
                          className="w-full rounded-md border border-[#d7dce3] bg-white px-2 py-1.5 text-xs text-[#1f2933] dark:border-[#2a3039] dark:bg-[#0b0d11] dark:text-[#e6e8ec]"
                        >
                          {availableModels.map((model) => (
                            <option key={model.id} value={model.id}>{model.name || model.id}</option>
                          ))}
                        </select>
                      </label>
                    ) : (
                      <div className="min-w-0">
                        <span className="mb-1 block text-[10px] font-semibold uppercase tracking-wide text-[#667085] dark:text-[#9aa3af]">Model</span>
                        <div className="truncate rounded-md border border-[#d7dce3] bg-white px-2 py-1.5 text-xs text-[#667085] dark:border-[#2a3039] dark:bg-[#0b0d11] dark:text-[#9aa3af]">
                          runner default
                        </div>
                      </div>
                    )}
                    {normalizeRunnerId(selectedRunner) === "codex" && availableReasoningEfforts.length > 0 ? (
                      <label className="min-w-0">
                        <span className="mb-1 block text-[10px] font-semibold uppercase tracking-wide text-[#667085] dark:text-[#9aa3af]">Reasoning</span>
                        <select
                          value={selectedReasoningEffort}
                          onChange={(event) => setSelectedReasoningEffort(event.target.value as ReasoningEffort)}
                          className="w-full rounded-md border border-[#d7dce3] bg-white px-2 py-1.5 text-xs text-[#1f2933] dark:border-[#2a3039] dark:bg-[#0b0d11] dark:text-[#e6e8ec]"
                        >
                          {availableReasoningEfforts.map((effort) => (
                            <option key={effort} value={effort}>
                              {effort === "xhigh" ? "Extra high" : effort === "max" ? "More reasoning" : `${effort[0].toUpperCase()}${effort.slice(1)}`}
                            </option>
                          ))}
                        </select>
                      </label>
                    ) : null}
                  </div>
                  <div className="flex flex-wrap items-center gap-2">
                    <button
                      type="button"
                      disabled={!connectedDevice?.id || !selectedRunner}
                      onClick={() => void saveRunnerChoice()}
                      className="rounded-md border border-emerald-500/30 bg-emerald-500/10 px-2.5 py-1.5 text-xs font-semibold text-emerald-700 disabled:cursor-not-allowed disabled:opacity-40 dark:text-emerald-200"
                    >
                      Save for machine
                    </button>
                    {selectedRunnerRow?.supportsBrowserAuth && selectedRunnerRow.ready === false ? (
                      <button
                        type="button"
                        disabled={runnerAuthBusy}
                        onClick={() => void startSelectedRunnerSignIn()}
                        className="rounded-md border border-sky-500/30 bg-sky-500/10 px-2.5 py-1.5 text-xs font-semibold text-sky-700 disabled:opacity-40 dark:text-sky-200"
                      >
                        {runnerAuthBusy ? "Opening..." : "Remote OAuth"}
                      </button>
                    ) : null}
                  </div>
                  {runnerSaveNotice ? (
                    <p className={`text-[11px] leading-4 ${runnerSaveNotice.tone === "ok" ? "text-emerald-600 dark:text-emerald-300" : runnerSaveNotice.tone === "warn" ? "text-amber-600 dark:text-amber-300" : "text-rose-600 dark:text-rose-300"}`}>
                      {runnerSaveNotice.text}
                    </p>
                  ) : null}
                </div>
              ) : null}
              {runnerHeartbeat ? (
                // ONE quiet row while the turn is live: what is happening, for
                // how long, when it last spoke — and the one action that was
                // missing entirely. `RUNNING` alone cannot distinguish a runner
                // thinking from a runner that stopped, and nothing in this
                // header could end a turn on the box (Close and Delete only stop
                // this browser watching). Disappears the moment the turn does.
                <div className="mt-2 flex min-h-9 items-center gap-2 rounded-md border border-[#d7dce3] bg-[#f8fafc] px-2.5 py-1.5 dark:border-[#2a3039] dark:bg-[#101318]">
                  <span
                    aria-hidden="true"
                    className={`h-1.5 w-1.5 shrink-0 rounded-full ${runnerHeartbeat.warn ? "bg-amber-500" : "animate-pulse bg-emerald-500"}`}
                  />
                  <span
                    className={`min-w-0 flex-1 truncate text-[11px] leading-5 tabular-nums ${runnerHeartbeat.warn ? "text-amber-700 dark:text-amber-300" : "text-[#475467] dark:text-[#9aa3af]"}`}
                    title={runnerHeartbeat.text}
                  >
                    {runnerHeartbeat.text}
                  </span>
                  {runnerHeartbeat.canStop ? (
                    <button
                      type="button"
                      onClick={() => void stopRunnerTurn()}
                      disabled={runnerStopBusy}
                      title="Ask the machine to end this turn"
                      className="shrink-0 rounded-md border border-[#d7dce3] bg-white px-2 py-1 text-[10px] font-semibold text-[#475467] hover:text-[#1f2933] disabled:cursor-not-allowed disabled:opacity-40 dark:border-[#2a3039] dark:bg-[#0b0d11] dark:text-[#d7dce3] dark:hover:text-[#e6e8ec]"
                    >
                      {runnerStopBusy ? "Stopping…" : "Stop"}
                    </button>
                  ) : null}
                </div>
              ) : null}
            </div>
            <div className="mt-3 flex min-w-0 items-center gap-2">
              <div className="flex min-w-0 flex-1 items-center gap-1.5">
                <button
                  type="button"
                  onClick={startNewChatSession}
                  disabled={!activeTaskStream}
                  className="shrink-0 rounded-md border border-[#d7dce3] bg-[#f8fafc] px-2 py-1 text-[10px] font-semibold text-[#475467] disabled:cursor-not-allowed disabled:opacity-40 dark:border-[#2a3039] dark:bg-[#101318] dark:text-[#d7dce3]"
                >
                  New session
                </button>
                {activeTaskStream ? (
                  <>
                    <button
                      type="button"
                      onClick={closeChatSession}
                      className="shrink-0 rounded-md border border-[#d7dce3] bg-[#f8fafc] px-2 py-1 text-[10px] font-semibold text-[#475467] dark:border-[#2a3039] dark:bg-[#101318] dark:text-[#d7dce3]"
                    >
                      Close
                    </button>
                    <button
                      type="button"
                      onClick={() => void deleteChatSession()}
                      className="shrink-0 rounded-md border border-rose-500/30 bg-rose-500/10 px-2 py-1 text-[10px] font-semibold text-rose-700 dark:text-rose-200"
                    >
                      Delete
                    </button>
                    <span className="min-w-0 truncate font-mono text-[10px] text-[#667085] dark:text-[#9aa3af]">
                      session {activeTaskStream.id}
                    </span>
                  </>
                ) : (
                  <span className="min-w-0 truncate text-[10px] text-[#667085] dark:text-[#9aa3af]">
                    next send starts one persistent session
                  </span>
                )}
              </div>
              {recentTasks.length ? (
                <button
                  type="button"
                  onClick={() => setSessionsOpen((open) => !open)}
                  className="shrink-0 rounded-md border border-[#d7dce3] bg-[#f8fafc] px-2 py-1 text-[10px] font-semibold uppercase tracking-wide text-[#667085] hover:text-[#1f2933] dark:border-[#2a3039] dark:bg-[#101318] dark:text-[#9aa3af] dark:hover:text-[#e6e8ec]"
                  aria-expanded={sessionsOpen}
                >
                  Sessions · {recentTasks.length} {sessionsOpen ? "hide" : "show"}
                </button>
              ) : null}
            </div>
            {recentTasks.length && sessionsOpen ? (
              <div className="mt-2 flex min-w-0 items-center gap-1.5 overflow-x-auto pb-0.5">
                {recentTasks.slice(0, 5).map((task) => {
                  const selected = activeTaskStream?.id === task.id;
                  return (
                    <button
                      key={task.id}
                      type="button"
                      onClick={() => void openTaskHistoryItem(task.id)}
                      title={task.title}
                      className={`min-w-[92px] max-w-[132px] shrink-0 rounded-md border px-2 py-1 text-left text-[10px] ${
                        selected
                          ? "border-[#7c5cff]/50 bg-[#7c5cff]/10 text-[#5b3ee4] dark:text-[#c9bfff]"
                          : "border-[#d7dce3] bg-[#f8fafc] text-[#667085] hover:text-[#1f2933] dark:border-[#2a3039] dark:bg-[#101318] dark:text-[#9aa3af] dark:hover:text-[#e6e8ec]"
                      }`}
                    >
                      <span className="block truncate font-semibold">{task.title || task.id}</span>
                      <span className="mt-0.5 block truncate font-mono opacity-80">
                        {task.status} · {taskTimeLabel(task)}
                      </span>
                    </button>
                  );
                })}
              </div>
            ) : null}
          </div>
          <div className="flex min-h-0 flex-1 flex-col bg-[#f8fafc] dark:bg-[#0f1218]">
            <div
              ref={taskConsoleRef}
              onScroll={(event) => setTaskConsolePinned(isNearBottom(event.currentTarget))}
              className="min-h-0 flex-1 overflow-y-auto px-4 py-4"
            >
          {activeTaskStream ? (
                <div className="flex min-h-full flex-col space-y-3">
                  {(() => {
                    const messages = runtimeChatMessages(activeTaskStream);
                    const pendingTurns = activeTaskStream.pendingUserTurns || [];
                    return (
                      <>
                  {messages.map((message, index) => {
                    if (message.role === "user") {
                      return (
                        <div key={`turn-${index}`} className="flex justify-end" data-testid="runtime-chat-user-bubble">
                          <div className="max-w-[88%] whitespace-pre-wrap break-words rounded-2xl rounded-br-md bg-[#7c5cff] px-3 py-2 text-sm leading-5 text-white shadow-sm">
                            {message.content}
                          </div>
                        </div>
                      );
                    }
                    const isLatestAssistant = index === messages.length - 1;
                    const rawLines = String(message.content || "").split(/\r?\n/).map((line) => line.trimEnd()).filter(Boolean);
                    const TAIL = 30;
                    // The fold is for FINISHED long outputs — never while the
                    // runner is coding. A live stream must stay fully visible or
                    // the chat reads as dead ("opencode is working" with an
                    // empty pane — the 2026-08-12 fix-task report: the fix
                    // streamed fine but was folded, so it looked like nothing
                    // happened). Auto-expand when the task becomes live.
                    const runnerCoding = taskStatusMeansRunnerIsCoding(activeTaskStream?.status);
                    const folded = isLatestAssistant && !taskStreamExpanded && !runnerCoding && rawLines.length > TAIL + 10;
                    const visible = folded ? rawLines.slice(-TAIL) : rawLines;
                    return (
                      <div key={`turn-${index}`} className="flex flex-col rounded-2xl rounded-bl-md border border-[#e4e7ec] bg-white shadow-sm dark:border-[#242b35] dark:bg-[#171b23]">
                        <div className="flex items-center justify-between gap-2 border-b border-[#eef1f5] px-3 py-2 dark:border-[#242b35]">
                          <div className="min-w-0 truncate text-xs font-semibold text-[#344054] dark:text-[#d7dce3]">
                            {selectedRunnerName}
                          </div>
                          {isLatestAssistant ? (
                            <div className="flex shrink-0 items-center gap-1.5">
                              <button
                                type="button"
                                onClick={() => void copyTaskConsole()}
                                title="Copy chat output"
                                aria-label="Copy chat output"
                                className="rounded-md border border-[#d7dce3] bg-white p-1.5 text-[#475467] hover:text-[#1f2933] dark:border-[#2a3039] dark:bg-[#101318] dark:text-[#d7dce3]"
                              >
                                {taskConsoleCopied ? (
                                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                                    <path d="M20 6 9 17l-5-5" />
                                  </svg>
                                ) : (
                                  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                                    <rect x="9" y="9" width="13" height="13" rx="2" />
                                    <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
                                  </svg>
                                )}
                              </button>
                              <div className="text-[10px] uppercase tracking-wide text-[#667085] dark:text-[#9aa3af]">
                                {activeTaskStream.status}
                              </div>
                            </div>
                          ) : null}
                        </div>
                        {folded || (isLatestAssistant && taskStreamExpanded) ? (
                          <button
                            type="button"
                            onClick={() => setTaskStreamExpanded((open) => !open)}
                            className="mx-3 mt-2 self-start rounded-md border border-[#d7dce3] bg-[#f8fafc] px-2 py-1 text-[10px] font-semibold text-[#475467] hover:text-[#1f2933] dark:border-[#2a3039] dark:bg-[#101318] dark:text-[#9aa3af]"
                          >
                            {folded
                              ? `Show earlier output (${rawLines.length - TAIL} lines)`
                              : "Collapse to latest output"}
                          </button>
                        ) : null}
                        <pre className="whitespace-pre-wrap break-words p-3 text-[11px] leading-5 text-[#344054] dark:text-[#d5dae1]">
                          {visible.length ? (
                            hasConsoleMarkup(visible.join("\n")) ? (
                              // Console-alike (2026-08-12 user request): the
                              // chat stream should read like the runner's own
                              // console — $ prompts, > build · <model> banners,
                              // diff +/- — via the SAME shared painter the
                              // dashboard console + mobile + tvOS use. Plain
                              // text falls back to the raw <pre>.
                              <AnsiConsoleText text={visible.join("\n")} />
                            ) : (
                              visible.join("\n")
                            )
                          ) : "Waiting for runner output..."}
                        </pre>
                      </div>
                    );
                  })}
                  {pendingTurns.map((turn, index) => (
                    <div key={`pending-turn-${index}`} className="flex justify-end" data-testid="runtime-chat-user-bubble">
                      <div className="max-w-[88%] rounded-2xl rounded-br-md bg-[#7c5cff] px-3 py-2 text-white shadow-sm">
                        <div className="whitespace-pre-wrap break-words text-sm leading-5">{turn.content}</div>
                        <div className="mt-1 text-[10px] font-semibold uppercase tracking-wide text-white/65">queued</div>
                      </div>
                    </div>
                  ))}
                  {renderReady ? (
                    <div className="flex items-center justify-between gap-3 rounded-xl border border-sky-500/25 bg-sky-500/10 px-3 py-2 text-xs text-[#344054] dark:text-[#d7dce3]">
                      <span>UI updates are ready. The current preview stays visible until you choose.</span>
                      <button
                        type="button"
                        onClick={() => { setRenderReady(false); void renderCurrentSurface("response-render-action"); }}
                        className="shrink-0 rounded-md bg-sky-600 px-3 py-1.5 font-semibold text-white"
                      >
                        Render updates
                      </button>
                    </div>
                  ) : null}
                      </>
                    );
                  })()}
                  <StreamHealthNotice health={taskStreamHealth} />
                </div>
              ) : (
                <div className="flex h-full min-h-[340px] items-center justify-center px-3 py-8">
                  <div className="max-w-[300px] text-center">
                    <div className="mx-auto flex h-10 w-10 items-center justify-center rounded-full border border-[#d7dce3] bg-white text-sm font-black text-[#1f2933] shadow-sm dark:border-[#2a3039] dark:bg-[#171b23] dark:text-[#e6e8ec]">
                      Y
                    </div>
                    <div className="mt-3 text-sm font-semibold text-[#344054] dark:text-[#d7dce3]">
                      Ready for {selectedProject?.name || "a project"}
                    </div>
                    <div className="mt-1 text-xs leading-5 text-[#667085] dark:text-[#9aa3af]">
                      {selectedRunner ? `${selectedRunnerName} will run the next task.` : "Select a runner below."}
                    </div>
                  </div>
                </div>
              )}
              </div>
            <div className="relative border-t border-[#e4e7ec] bg-white p-3 dark:border-[#242b35] dark:bg-[#141820]">
              <div
                id="runtime-composer-options"
                data-testid="runtime-composer-options"
                className={`${chatScopeControlsOpen ? "absolute" : "hidden"} bottom-full left-3 right-3 z-30 mb-1 max-h-[min(58vh,420px)] overflow-y-auto rounded-lg border border-[#d7dce3] bg-white p-3 shadow-2xl dark:border-[#2a3039] dark:bg-[#141820]`}
              >
              {/* What the runner is told about the screen you're looking at.
                  Shown, not implied: a prompt this surface silently enriches
                  is a prompt the user cannot reason about — and the toggle
                  DELETES what was reported, so "off" means the agent is not
                  holding your screen. */}
              <ScreenContextChip
                agentClient={agentClient}
                workDir={selectedProject?.path}
                className="mb-2"
              />
              {/* DOM mode: the element the user clicked in the preview, attached
                  to the next prompt. Browse|Inspect radio + attached-element
                  chip; off DELETES what was reported. Same visibility rule as
                  the screen chip above. */}
              <DomInspectChip
                agentClient={agentClient}
                workDir={selectedProject?.path}
                iframeRef={domInspectFrameRef}
                className="mb-2"
              />
              {/* Project picker — the composer MUST be able to pick which
                  project the next task runs in, because every render surface
                  (TV, watch, car, AR/VR, phone, browser) is project-scoped:
                  Load Targets probes the SELECTED project's targets. Without
                  this the chat always ran the first project (yaver.io repo
                  root → browser-only targets) and tvOS/watch/car/vision could
                  never be vibed from the chat (2026-08-12 audit). Same
                  selectedPath + state-reset the Project panel select uses. */}
              <label className="min-w-0 flex-1" title="Project the next task runs in — Load Targets probes its render surfaces">
                <span className="mb-1 block text-[10px] font-semibold uppercase tracking-wide text-surface-500">Project</span>
                <select
                  value={selectedPath}
                  onChange={(e) => { userSelectedProjectRef.current = true; setSelectedPath(e.target.value); setRuntimeProjectNote(null); setCaps(null); setSession(null); setWebPreviewPanelOpen(false); setRuntimeControlsOpen(false); setWebPreviewUrl(null); setWebPreviewNote(null); }}
                  className="w-full rounded-md border border-[#d7dce3] bg-white px-2 py-1.5 text-xs text-[#1f2933] dark:border-[#2a3039] dark:bg-[#161b22] dark:text-[#e6e8ec]"
                >
                  <option value="">No project</option>
                  {projects.length === 0 ? <option value="">No projects on the render machine</option> : null}
                  {projects.map((p) => (
                    <option key={p.path} value={p.path}>{p.name} · {p.framework || "unknown"}</option>
                  ))}
                </select>
              </label>
              <div className="mb-2 flex flex-wrap items-center gap-3 text-[11px] text-surface-500">
                <label className="flex items-center gap-2">
                  <input
                    type="checkbox"
                    checked={useLatestProject}
                    onChange={(event) => {
                      const next = event.target.checked;
                      setUseLatestProject(next);
                      setUseLatestProjectEnabled(next);
                    }}
                  />
                  latest project
                </label>
                <label className="flex items-center gap-2">
                  <input
                    type="checkbox"
                    checked={useLatestMCP}
                    onChange={(event) => {
                      const next = event.target.checked;
                      setUseLatestMCP(next);
                      setUseLatestMCPEnabled(next);
                      if (!next) { setSelectedMcpServers([]); setIncludeYaverMcp(false); }
                    }}
                  />
                  latest MCP
                </label>
              </div>
              {/* MCP doorway + external servers — parity with the Chat tab
                  composer (page.tsx). The yaver toggle renders ALWAYS (it is
                  the doorway to Yaver's own MCP, present on every box even
                  with zero external servers — gating it on mcpServers.length
                  hid it exactly when the user needed to turn the doorway off,
                  2026-08-11); external servers render only when the agent
                  reported any. Defaults: yaver ON, no externals; the
                  selection rides on createTask so a Vibing-started task
                  carries the same MCP set as one started from Chat. */}
              <div className="mb-2 flex flex-wrap items-center gap-2 text-[11px] text-surface-500">
                <button
                  type="button"
                  onClick={() => setIncludeYaverMcp((v) => !v)}
                  className={`rounded-full border px-2.5 py-1 font-semibold ${
                    includeYaverMcp
                      ? "border-brand/40 bg-brand-soft text-brand-softFg"
                      : "border-surface-800 bg-surface-950 text-surface-400 hover:border-surface-700"
                  }`}
                  title="Yaver's own MCP tools (yaver mcp) are attached to this task. Toggle off to run with only the external MCPs below."
                >
                  yaver{includeYaverMcp ? "" : " (off)"}
                </button>
                {mcpServers.map((server) => {
                    const active = selectedMcpServers.includes(server.name);
                    return (
                      <button
                        key={server.name}
                        type="button"
                        onClick={() => {
                          setSelectedMcpServers((prev) =>
                            prev.includes(server.name)
                              ? prev.filter((name) => name !== server.name)
                              : [...prev, server.name],
                          );
                        }}
                        className={`rounded-full border px-2.5 py-1 font-semibold ${
                          active
                            ? "border-brand/40 bg-brand-soft text-brand-softFg"
                            : "border-surface-800 bg-surface-950 text-surface-400 hover:border-surface-700"
                        }`}
                        title={`${server.url} · ${server.toolCount ?? 0} tools`}
                      >
                        {server.name}
                      </button>
                    );
                  })}
                </div>
              </div>
              <div className="grid grid-cols-[minmax(0,1fr)_auto] items-end gap-2 rounded-md border border-[#d7dce3] bg-[#f8fafc] p-2 focus-within:border-[#98a2b3] dark:border-[#2a3039] dark:bg-[#101318]">
                <textarea
                  value={composer}
                  onChange={(event) => setComposer(event.target.value)}
                  onKeyDown={(event) => {
                    // See lib/composerKeys.ts — this used to be a bare
                    // `Enter && !shiftKey -> send`, which sent on an IME commit
                    // and turned every non-Shift newline chord into a send that
                    // destroyed the rest of the message.
                    const decision = decideComposerKey({
                      key: event.key,
                      shiftKey: event.shiftKey,
                      altKey: event.altKey,
                      ctrlKey: event.ctrlKey,
                      metaKey: event.metaKey,
                      isComposing: event.nativeEvent.isComposing,
                      keyCode: event.keyCode,
                    });
                    if (decision === "send") {
                      event.preventDefault();
                      void sendPrompt();
                      return;
                    }
                    if (decision === "newline" && !newlineIsNative({ key: event.key, shiftKey: event.shiftKey })) {
                      // Alt/Ctrl/Cmd+Enter are inert in a textarea, so insert
                      // the break ourselves rather than swallowing the keystroke.
                      event.preventDefault();
                      const field = event.currentTarget;
                      const next = insertNewline(field.value, field.selectionStart, field.selectionEnd);
                      setComposer(next.value);
                      requestAnimationFrame(() => {
                        try {
                          field.setSelectionRange(next.caret, next.caret);
                        } catch {}
                      });
                    }
                  }}
                  rows={3}
                  placeholder={selectedProject ? `Ask ${selectedRunner || "the runner"} to change ${selectedProject.name}` : "Pick a project first"}
                  className="max-h-40 min-h-[76px] resize-none border-0 bg-transparent px-1 py-1 text-sm leading-5 text-[#1f2933] outline-none placeholder:text-[#98a2b3] dark:text-[#e6e8ec] dark:placeholder:text-[#667085]"
                />
                <div className="flex shrink-0 items-end gap-1">
                  <button
                    type="button"
                    onClick={() => setChatScopeControlsOpen((open) => !open)}
                    title="Project, MCP and preview inspection options"
                    aria-label="More task options"
                    aria-expanded={chatScopeControlsOpen}
                    aria-controls="runtime-composer-options"
                    data-testid="runtime-composer-more"
                    className={`flex h-9 w-9 items-center justify-center rounded-md border text-base font-semibold tracking-widest ${
                      chatScopeControlsOpen
                        ? "border-[#7c5cff]/50 bg-[#7c5cff]/10 text-[#5b3ee4] dark:text-[#c9bfff]"
                        : "border-[#d7dce3] bg-white text-[#475467] hover:text-[#1f2933] dark:border-[#2a3039] dark:bg-[#101318] dark:text-[#d7dce3]"
                    }`}
                  >
                    <span aria-hidden="true">•••</span>
                  </button>
                  <button
                    type="button"
                    onClick={toggleSpeakSession}
                    disabled={!ttsAvailable || !activeTaskStream}
                    title={ttsAvailable ? "Read session output" : "Text to speech is not available in this browser"}
                    aria-label={speaking ? "Stop speaking" : "Read session output"}
                    className={`flex h-9 w-9 items-center justify-center rounded-md border disabled:cursor-not-allowed disabled:opacity-40 ${
                      speaking
                        ? "border-sky-500/50 bg-sky-500/15 text-sky-700 dark:text-sky-200"
                        : "border-[#d7dce3] bg-white text-[#475467] hover:text-[#1f2933] dark:border-[#2a3039] dark:bg-[#101318] dark:text-[#d7dce3]"
                    }`}
                  >
                    {speaking ? (
                      <svg width="15" height="15" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
                        <rect x="5" y="6" width="5" height="12" rx="1" />
                        <rect x="14" y="6" width="5" height="12" rx="1" />
                      </svg>
                    ) : (
                      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                        <path d="M11 5 6 9H2v6h4l5 4V5Z" />
                        <path d="M15.5 8.5a5 5 0 0 1 0 7" />
                        <path d="M19 5a9 9 0 0 1 0 14" />
                      </svg>
                    )}
                  </button>
                  <button
                    disabled={!composer.trim() || sending}
                    onClick={() => void sendPrompt()}
                    className="h-9 shrink-0 rounded-md bg-[#7c5cff] px-4 text-xs font-semibold text-white shadow-sm disabled:cursor-not-allowed disabled:bg-[#d7dce3] disabled:text-[#98a2b3] dark:disabled:bg-[#242b35]"
                  >
                    {sending ? "Sending" : "Send"}
                  </button>
                </div>
              </div>
            </div>
          </div>
        </div>
        <div className="min-w-0 rounded-md border border-[#d7dce3] bg-white p-3 dark:border-[#2a3039] dark:bg-[#141820]">
          <div className={`${runtimeConsoleOpen ? "mb-2" : ""} flex min-w-0 items-center justify-between gap-2`}>
            <div>
              <div className="text-[11px] font-semibold uppercase tracking-wide text-[#5d6673] dark:text-[#9aa3af]">Runtime Console</div>
              <div className="mt-0.5 text-[11px] text-[#667085] dark:text-[#9aa3af]">{log.length} events</div>
            </div>
            <div className="flex shrink-0 items-center gap-1.5">
            <button
              type="button"
              onClick={clearRuntimeConsole}
              disabled={log.length === 0}
              title="Clear session logs"
              aria-label="Clear session logs"
              className="rounded-md border border-[#d7dce3] bg-white p-1.5 text-[#475467] hover:text-rose-600 disabled:opacity-40 dark:border-[#2a3039] dark:bg-[#101318] dark:text-[#d7dce3]"
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <path d="M3 6h18" />
                <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6" />
                <path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
              </svg>
            </button>
            <button
              type="button"
              onClick={() => void copyRuntimeConsole()}
              title="Copy runtime console"
              aria-label="Copy runtime console"
              className="rounded-md border border-[#d7dce3] bg-white p-1.5 text-[#475467] hover:text-[#1f2933] dark:border-[#2a3039] dark:bg-[#101318] dark:text-[#d7dce3]"
            >
              {runtimeConsoleCopied ? (
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <path d="M20 6 9 17l-5-5" />
                </svg>
              ) : (
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                  <rect x="9" y="9" width="13" height="13" rx="2" />
                  <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
                </svg>
              )}
            </button>
            {!runtimeConsoleOpen ? (
              <button
                type="button"
                onClick={() => {
                  setRuntimeConsoleOpen(true);
                  setRuntimeConsolePinned(true);
                  window.setTimeout(() => scrollToBottom(runtimeConsoleRef.current), 0);
                }}
                className="rounded-md border border-[#d7dce3] bg-white px-2 py-1.5 text-[10px] font-semibold text-[#475467] hover:text-[#1f2933] dark:border-[#2a3039] dark:bg-[#101318] dark:text-[#d7dce3]"
              >
                Expand
              </button>
            ) : (
              <button
                type="button"
                onClick={() => setRuntimeConsoleOpen(false)}
                className="rounded-md border border-[#d7dce3] bg-white px-2 py-1.5 text-[10px] font-semibold text-[#475467] hover:text-[#1f2933] dark:border-[#2a3039] dark:bg-[#101318] dark:text-[#d7dce3]"
              >
                Fold
              </button>
            )}
            {runtimeConsoleOpen && !runtimeConsolePinned ? (
              <button
                type="button"
                onClick={() => {
                  setRuntimeConsolePinned(true);
                  scrollToBottom(runtimeConsoleRef.current);
                }}
                className="rounded-full border border-sky-500/30 bg-sky-500/10 px-2.5 py-1 text-[10px] font-semibold text-sky-700 dark:text-sky-300"
              >
                Follow logs
              </button>
            ) : null}
            </div>
          </div>
          {/* whitespace-pre + overflow-x-auto: long unbroken lines (file
              paths, URLs from expo export) scroll INSIDE this box; they
              must never push the pane wider. Ancestors carry min-w-0. */}
          {runtimeConsoleOpen ? (
            <div
              ref={runtimeConsoleRef}
              onScroll={(event) => setRuntimeConsolePinned(isNearBottom(event.currentTarget))}
              className="h-52 min-w-0 overflow-y-auto overflow-x-auto rounded-md border border-[#1f2933] bg-[#0b0d11] p-3 font-mono text-[11px] leading-5 text-[#d5dae1]"
            >
              {runtimeConsoleRows.length ? (
                runtimeConsoleRows.map((row, index) => {
                  const rowLines = row.text.split("\n");
                  const rowKey = `${row.stamp}|${rowLines[0]}`;
                  const expanded = expandedLogRows.has(rowKey);
                  return (
                    <div key={`${index}-${rowKey}`} className="whitespace-pre">
                      <span className="text-[#5d6673]">[{row.stamp}]</span> {rowLines[0]}
                      {row.count > 1 ? (
                        <span className="ml-2 rounded bg-[#1f2933] px-1.5 text-[10px] text-[#9aa3af]">×{row.count}</span>
                      ) : null}
                      {rowLines.length > 1 ? (
                        <button
                          type="button"
                          onClick={() => toggleLogRow(rowKey)}
                          className="ml-2 text-[10px] text-sky-400 hover:text-sky-300"
                        >
                          {expanded ? "hide" : `${rowLines.length - 1} more lines`}
                        </button>
                      ) : null}
                      {expanded && rowLines.length > 1 ? (
                        <div className="whitespace-pre pl-6 text-[#9aa3af]">{rowLines.slice(1).join("\n")}</div>
                      ) : null}
                    </div>
                  );
                })
              ) : (
                "No runtime operations yet."
              )}
            </div>
          ) : null}
        </div>
        <div className="hidden rounded-md border border-[#d7dce3] bg-white p-3 dark:border-[#2a3039] dark:bg-[#161b22]">
          <div className="mb-2 flex items-center justify-between gap-3">
            <div className="text-[11px] font-semibold uppercase tracking-wide text-[#5d6673] dark:text-[#9aa3af]">Vibing</div>
            <button
              type="button"
              onClick={() => setVibingSettingsOpen((open) => !open)}
              className="shrink-0 text-xs font-semibold text-sky-700 dark:text-sky-300"
              aria-expanded={vibingSettingsOpen}
            >
              {vibingSettingsOpen ? "Hide details" : "Details"}
            </button>
          </div>
          <div className="grid gap-2">
            <div className="grid gap-2 sm:grid-cols-[minmax(0,0.9fr)_minmax(0,1.2fr)]">
              <label className="min-w-0">
                <span className="mb-1 block text-[10px] font-semibold uppercase tracking-wide text-[#667085] dark:text-[#9aa3af]">Runner</span>
                <select
                  value={selectedRunner}
                  onChange={(event) => setSelectedRunner(event.target.value)}
                  className="w-full rounded-md border border-[#d7dce3] bg-white px-2 py-1.5 text-xs text-[#1f2933] dark:border-[#2a3039] dark:bg-[#101318] dark:text-[#e6e8ec]"
                >
                  {runners.length === 0 ? <option value="">No runners detected</option> : null}
                  {runners.map((runner) => (
                    <option key={runner.id} value={runner.id}>
                      {runner.name}{runner.ready === false ? " (sign-in needed)" : ""}
                    </option>
                  ))}
                </select>
              </label>
              {availableModels.length > 0 ? (
                <label className="min-w-0">
                  <span className="mb-1 block text-[10px] font-semibold uppercase tracking-wide text-[#667085] dark:text-[#9aa3af]">Model</span>
                  <select
                    value={selectedModel}
                    onChange={(event) => setSelectedModel(event.target.value)}
                    className="w-full rounded-md border border-[#d7dce3] bg-white px-2 py-1.5 text-xs text-[#1f2933] dark:border-[#2a3039] dark:bg-[#101318] dark:text-[#e6e8ec]"
                  >
                    {availableModels.map((model) => (
                      <option key={model.id} value={model.id}>{model.name || model.id}</option>
                    ))}
                  </select>
                </label>
              ) : (
                <div className="min-w-0">
                  <span className="mb-1 block text-[10px] font-semibold uppercase tracking-wide text-[#667085] dark:text-[#9aa3af]">Model</span>
                  <div className="truncate rounded-md border border-[#d7dce3] bg-[#f8fafc] px-2 py-1.5 text-xs text-[#667085] dark:border-[#2a3039] dark:bg-[#101318] dark:text-[#9aa3af]">
                    runner default
                  </div>
                </div>
              )}
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <button
                type="button"
                disabled={!connectedDevice?.id || !selectedRunner}
                onClick={() => void saveRunnerChoice()}
                className="rounded-md border border-emerald-500/30 bg-emerald-500/10 px-2.5 py-1.5 text-xs font-semibold text-emerald-700 disabled:cursor-not-allowed disabled:opacity-40 dark:text-emerald-200"
              >
                Save for machine
              </button>
              {selectedRunnerRow?.supportsBrowserAuth && selectedRunnerRow.ready === false ? (
                <button
                  type="button"
                  disabled={runnerAuthBusy}
                  onClick={() => void startSelectedRunnerSignIn()}
                  className="rounded-md border border-sky-500/30 bg-sky-500/10 px-2.5 py-1.5 text-xs font-semibold text-sky-700 disabled:opacity-40 dark:text-sky-200"
                >
                  {runnerAuthBusy ? "Opening..." : "Remote OAuth"}
                </button>
              ) : null}
              <button
                type="button"
                onClick={() => openRunnerControls("runner")}
                className="min-w-0 truncate rounded-md border border-transparent px-1 py-0.5 text-left text-[11px] text-[#667085] hover:border-[#d7dce3] hover:text-[#1f2933] dark:text-[#9aa3af] dark:hover:border-[#2a3039] dark:hover:text-[#e6e8ec]"
                title="Change runner/model"
              >
                {selectedRunnerRow?.name || selectedRunner || "No runner"} / {safeModelForRunner(selectedRunner, selectedModel, availableModels) || selectedModel || "runner default"}
              </button>
            </div>
            {runnerSaveNotice ? (
              <p className={`mt-1 text-[11px] leading-4 ${runnerSaveNotice.tone === "ok" ? "text-emerald-600 dark:text-emerald-300" : runnerSaveNotice.tone === "warn" ? "text-amber-600 dark:text-amber-300" : "text-rose-600 dark:text-rose-300"}`}>
                {runnerSaveNotice.text}
              </p>
            ) : null}
          </div>
          {vibingSettingsOpen || runnerAuthStatus || runnerAuthError || runnerAuthDeclined ? (
            <div className="mt-3 grid gap-2">
              {availableModels.length > 0 ? (
                <div>
                  {normalizeRunnerId(selectedRunner) === "opencode" ? (
                    <div className="mt-2 rounded-md border border-cyan-500/20 bg-cyan-500/5 p-2 text-[10px] text-[#475467] dark:text-[#c7d2e1]">
                      <div className="font-semibold uppercase tracking-wide text-cyan-700 dark:text-cyan-200">OpenCode config</div>
                      <div className="mt-1 grid gap-1">
                        <div>Provider: <span className="font-mono">{safeModelForRunner(selectedRunner, selectedModel, availableModels)?.split("/")[0] || opencodeSnapshot?.provider || "from opencode default"}</span></div>
                        <div>Model: <span className="font-mono">{safeModelForRunner(selectedRunner, selectedModel, availableModels) || opencodeSnapshot?.model || "from opencode default"}</span></div>
                        <div>Agent: <span className="font-mono">{opencodeSnapshot?.defaultAgent || "build"}</span></div>
                        {opencodeSnapshot?.buildModel ? <div>Build model: <span className="font-mono">{opencodeSnapshot.buildModel}</span></div> : null}
                        {opencodeSnapshot?.planModel ? <div>Plan model: <span className="font-mono">{opencodeSnapshot.planModel}</span></div> : null}
                        {opencodeSnapshot?.providers?.length ? (
                          <div>
                            Auth: {opencodeSnapshot.providers.map((provider) => `${provider.id}${provider.hasApiKey ? " key" : ""}`).join(", ")}
                          </div>
                        ) : null}
                        {opencodeSnapshot?.updatedAt ? (
                          <div>Snapshot: {new Date(opencodeSnapshot.updatedAt).toLocaleTimeString()}</div>
                        ) : null}
                      </div>
                      {opencodeSnapshot?.diagnostics?.length ? (
                        <div className="mt-2 text-amber-700 dark:text-amber-200">
                          {opencodeSnapshot.diagnostics.join(" ")}
                        </div>
                      ) : null}
                      <div className="mt-2 text-[#667085] dark:text-[#9aa3af]">
                        OpenCode uses provider/model IDs such as zai-coding-plan/glm-4.7. Unqualified Codex models are blocked before send.
                      </div>
                    </div>
                  ) : null}
                </div>
              ) : null}
              {runnerAuthDeclined ? (
                /* Informational, not error-red: the agent declined because the
                   runner already looks signed in. */
                <div className="rounded-md border border-[#d7dce3] bg-[#f8fafc] p-2 text-[11px] text-[#475467] dark:border-[#2a3039] dark:bg-[#101318] dark:text-[#d7dce3]">
                  <div className="font-semibold uppercase tracking-wide text-[#667085] dark:text-[#9aa3af]">Already signed in</div>
                  <div className="mt-1">{runnerAuthDeclined.reason}</div>
                  {runnerAuthDeclined.reauthable ? (
                    <button
                      type="button"
                      disabled={runnerAuthBusy}
                      onClick={() => void startSelectedRunnerSignIn(true)}
                      className="mt-2 rounded-md border border-sky-500/30 bg-sky-500/10 px-2 py-1 text-[11px] font-semibold text-sky-700 disabled:opacity-40 dark:text-sky-200"
                    >
                      Sign in anyway (switch account)
                    </button>
                  ) : null}
                </div>
              ) : null}
              {runnerAuthStatus ? (
                <div className="rounded-md border border-[#d7dce3] bg-[#f8fafc] p-2 text-[11px] text-[#475467] dark:border-[#2a3039] dark:bg-[#101318] dark:text-[#d7dce3]">
                  <div className="font-semibold uppercase tracking-wide text-[#667085] dark:text-[#9aa3af]">OAuth status</div>
                  <div className="mt-1">{runnerAuthStatus.status.replaceAll("_", " ")}</div>
                  {!isRunnerBrowserAuthTerminal(runnerAuthStatus.status)
                    ? (() => {
                        const line = runnerAuthLivenessLine(Date.now(), runnerAuthStatus.startedAt, runnerAuthStatus.lastOutputAt);
                        return line ? <div className="mt-1 text-[#667085] dark:text-[#9aa3af]">{line}</div> : null;
                      })()
                    : null}
                  {runnerAuthStatus.callbackPort ? (
                    <div className="mt-1 text-[#667085] dark:text-[#9aa3af]">
                      Waiting on localhost:{runnerAuthStatus.callbackPort}. If the auth tab ends on a localhost callback page, paste its address below.
                    </div>
                  ) : null}
                  {runnerAuthStatus.code ? (
                    /* Codex/kimi device-auth code — the ONE thing the user
                       must carry to the provider tab. Plain text with no
                       copy affordance forced hand-selection of XXXX-XXXX. */
                    <div className="mt-1 flex items-center gap-2">
                      <span className="font-mono">Code: {runnerAuthStatus.code}</span>
                      <button
                        type="button"
                        onClick={() => { void navigator.clipboard?.writeText(runnerAuthStatus.code || ""); }}
                        className="rounded border border-[#d7dce3] px-2 py-0.5 text-[10px] font-semibold text-[#475467] dark:border-[#2a3039] dark:text-[#d7dce3]"
                      >
                        Copy code
                      </button>
                    </div>
                  ) : null}
                  {runnerAuthStatus.openUrl ? (
                    <div className="mt-1 space-y-1">
                      <a href={runnerAuthStatus.openUrl} target="_blank" rel="noreferrer" className="inline-block text-sky-700 underline dark:text-sky-300">
                        Open auth page
                      </a>
                      {/* One-line, one-tap copy — this panel used to show only
                          the link text, so the URL itself could not be copied
                          to another browser/device at all. */}
                      <div className="flex items-center gap-2">
                        <input
                          readOnly
                          value={runnerAuthStatus.openUrl}
                          onFocus={(event) => event.target.select()}
                          spellCheck={false}
                          className="w-full truncate rounded border border-[#d7dce3] bg-white px-2 py-1 font-mono text-[10px] text-[#475467] outline-none dark:border-[#2a3039] dark:bg-[#0b0e12] dark:text-[#d7dce3]"
                        />
                        <button
                          type="button"
                          onClick={() => { void navigator.clipboard?.writeText(runnerAuthStatus.openUrl || ""); }}
                          className="shrink-0 rounded border border-[#d7dce3] px-2 py-1 text-[10px] font-semibold text-[#475467] dark:border-[#2a3039] dark:text-[#d7dce3]"
                        >
                          Copy URL
                        </button>
                      </div>
                    </div>
                  ) : null}
                  {runnerAuthStatus.detail ? <div className="mt-1">{runnerAuthStatus.detail}</div> : null}
                  {/* The 45s silence watchdog and the session deadline write
                      their remedy into `error` — this panel dropped it, so
                      the named fix never reached the surface (2026-07 audit). */}
                  {runnerAuthStatus.error ? (
                    <div className="mt-1 text-red-700 dark:text-red-300">{runnerAuthStatus.error}</div>
                  ) : null}
                  {runnerAuthStatus.runner === "claude" &&
                  runnerAuthFlowKind(runnerAuthStatus.openUrl) !== "localhost-callback" &&
                  !isRunnerBrowserAuthTerminal(runnerAuthStatus.status) ? (
                    <div className="mt-2 space-y-1">
                      <div className="text-[#667085] dark:text-[#9aa3af]">
                        Claude Code code/token
                      </div>
                      <input
                        value={runnerAuthCodeInput}
                        onChange={(event) => {
                          setRunnerAuthCodeInput(event.target.value);
                          setRunnerAuthError(null);
                        }}
                        onPaste={(event) => {
                          const pasted = event.clipboardData.getData("text") || "";
                          const cleaned = pasted.trim();
                          if (cleaned !== pasted) {
                            event.preventDefault();
                            setRunnerAuthCodeInput(cleaned);
                          }
                        }}
                        onKeyDown={(event) => {
                          if (event.key === "Enter" && runnerAuthCodeInput.trim()) {
                            event.preventDefault();
                            void submitRunnerAuthCode();
                          }
                        }}
                        placeholder="Paste Claude Code authentication code or token"
                        spellCheck={false}
                        autoComplete="off"
                        autoCorrect="off"
                        autoCapitalize="off"
                        className="w-full rounded-md border border-[#d7dce3] bg-white px-2 py-1.5 font-mono text-[11px] text-[#1f2933] outline-none focus:border-[#98a2b3] dark:border-[#2a3039] dark:bg-[#0b0d11] dark:text-[#e6e8ec]"
                      />
                      <button
                        type="button"
                        disabled={!runnerAuthCodeInput.trim() || runnerAuthCodeBusy}
                        onClick={() => void submitRunnerAuthCode()}
                        className="rounded-md border border-sky-500/30 bg-sky-500/10 px-2 py-1 text-[11px] font-semibold text-sky-700 disabled:opacity-40 dark:text-sky-200"
                      >
                        {runnerAuthCodeBusy ? "Submitting..." : "Submit Claude Code token"}
                      </button>
                    </div>
                  ) : null}
                  {runnerAuthStatus.callbackPort && !isRunnerBrowserAuthTerminal(runnerAuthStatus.status) ? (
                    <div className="mt-2 space-y-1">
                      <div className="text-[#667085] dark:text-[#9aa3af]">
                        If the auth tab ends at localhost:{runnerAuthStatus.callbackPort}, paste that full address here.
                      </div>
                      <input
                        value={runnerAuthCallbackUrl}
                        onChange={(event) => setRunnerAuthCallbackUrl(event.target.value)}
                        onPaste={(event) => {
                          const pasted = event.clipboardData.getData("text") || "";
                          const cleaned = pasted.trim();
                          if (cleaned !== pasted) {
                            event.preventDefault();
                            setRunnerAuthCallbackUrl(cleaned);
                          }
                        }}
                        onKeyDown={(event) => {
                          if (event.key === "Enter" && runnerAuthCallbackUrl.trim()) {
                            event.preventDefault();
                            void submitRunnerAuthCallback();
                          }
                        }}
                        placeholder={`http://localhost:${runnerAuthStatus.callbackPort}/callback?...`}
                        spellCheck={false}
                        autoComplete="off"
                        autoCorrect="off"
                        autoCapitalize="off"
                        className="w-full rounded-md border border-[#d7dce3] bg-white px-2 py-1.5 font-mono text-[11px] text-[#1f2933] outline-none focus:border-[#98a2b3] dark:border-[#2a3039] dark:bg-[#0b0d11] dark:text-[#e6e8ec]"
                      />
                      <button
                        type="button"
                        disabled={!runnerAuthCallbackUrl.trim() || runnerAuthCallbackBusy}
                        onClick={() => void submitRunnerAuthCallback()}
                        className="rounded-md border border-sky-500/30 bg-sky-500/10 px-2 py-1 text-[11px] font-semibold text-sky-700 disabled:opacity-40 dark:text-sky-200"
                      >
                        {runnerAuthCallbackBusy ? "Delivering..." : "Deliver callback"}
                      </button>
                    </div>
                  ) : null}
                </div>
              ) : null}
              {runnerAuthError ? <div className="text-[11px] text-rose-700 dark:text-rose-300">{runnerAuthError}</div> : null}
            </div>
          ) : null}
        </div>
      </aside>
      {projectStartOpen ? (
        <div className="fixed inset-0 z-[90] flex items-center justify-center bg-black/55 p-4" role="dialog" aria-modal="true" aria-labelledby="project-start-title">
          <div className="w-full max-w-md rounded-2xl border border-[#67E8F9]/60 bg-[#F0F9FF] p-5 text-[#075985] shadow-2xl dark:bg-[#0b1f2a] dark:text-[#e6f8ff]">
            <div className="flex items-start justify-between gap-4">
              <div>
                <h2 id="project-start-title" className="text-lg font-bold">Start a project</h2>
                <p className="mt-1 text-sm opacity-75">Ocean is the initial visual direction. Developing will ask what the app should do.</p>
              </div>
              <button type="button" onClick={() => setProjectStartOpen(false)} aria-label="Close" className="text-xl opacity-70">×</button>
            </div>
            <label className="mt-5 block text-xs font-semibold uppercase tracking-wide">
              Project name
              <input
                autoFocus
                value={projectStartName}
                onChange={(event) => setProjectStartName(event.target.value)}
                onKeyDown={(event) => { if (event.key === "Enter") void startProject(); }}
                placeholder="My new app"
                className="mt-2 h-11 w-full rounded-lg border border-[#67E8F9] bg-white px-3 text-sm text-[#0c4a6e] outline-none focus:ring-2 focus:ring-[#0EA5E9] dark:bg-[#101820] dark:text-white"
              />
            </label>
            <fieldset className="mt-4">
              <legend className="text-xs font-semibold uppercase tracking-wide">Git home</legend>
              <div className="mt-2 grid grid-cols-3 gap-2">
                {(["yaver-git", "github", "gitlab"] as const).map((provider) => (
                  <button
                    key={provider}
                    type="button"
                    onClick={() => setProjectStartGit(provider)}
                    className={`rounded-lg border px-2 py-3 text-xs font-semibold ${projectStartGit === provider ? "border-[#075985] bg-[#0EA5E9] text-white" : "border-[#67E8F9] bg-white/80 text-[#075985] dark:bg-[#101820] dark:text-[#dff7ff]"}`}
                  >
                    {provider === "yaver-git" ? "Yaver Git" : provider === "github" ? "GitHub" : "GitLab"}
                  </button>
                ))}
              </div>
            </fieldset>
            {projectStartError ? <p className="mt-3 text-sm text-rose-600 dark:text-rose-300">{projectStartError}</p> : null}
            <button
              type="button"
              disabled={!projectStartName.trim() || projectStartBusy}
              onClick={() => void startProject()}
              className="mt-5 h-11 w-full rounded-lg bg-[#075985] text-sm font-bold text-white disabled:opacity-45"
            >
              {projectStartBusy ? "Initializing…" : "Initialize and develop"}
            </button>
          </div>
        </div>
      ) : null}
    </div>
  );
}
