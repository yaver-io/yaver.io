"use client";

import { useAuth } from "@/lib/use-auth";
import { useDevices, usePendingClaims, setDeviceAlias, type Device } from "@/lib/use-devices";
import {
  lastSeenAgeMs,
  formatAgeShort,
  hasRecentLiveSignal,
  deriveDeviceLifecycleState,
  type DeviceLifecycleState,
} from "@/lib/device-lifecycle";
import { streamTaskOutputWithRecovery, type TaskStreamHealth } from "@/lib/taskStreamWithRecovery";
import { StreamHealthNotice } from "@/components/dashboard/StreamHealthNotice";
import WebShellModal from "@/components/dashboard/WebShellModal";
import RemoteDesktopModal from "@/components/dashboard/RemoteDesktopModal";
import { agentClient, agentClientPool, type AgentClient, type Task, type ConnectionState, type Runner, type AgentInfo, type ConnectAttemptDiagnostic, type DeviceStatusProbe, type TmuxSessionSummary, type McpServer, type ModelInfo, type TaskRunnerControlCatalog, type OpenCodeProviderSummary } from "@/lib/agent-client";
import { isRunnerSeat, listTmuxRunnerSessions, type TmuxRunnerSessionRecord } from "@/lib/tmux-sessions";
import { CONVEX_URL } from "@/lib/constants";
import { useMachineRoles } from "@/lib/useMachineRoles";
import { planConnectionFanout } from "@/lib/connectionFanout";
import { useState, useEffect, useRef, useCallback, useMemo, memo } from "react";
import "@xterm/xterm/css/xterm.css";
import { useRouter } from "next/navigation";
import { useTheme } from "@/components/ThemeProvider";
import { dedupeScopedTasks, scopedTaskKey } from "@/lib/taskIdentity";
import { listAgentTaskSnapshots, reconcileTasksWithAgentSnapshots, type AgentTaskSnapshot } from "@/lib/taskSnapshots";
import ProjectsView from "@/components/dashboard/ProjectsView";
import GitView from "@/components/dashboard/GitView";
import DownloadsView from "@/components/dashboard/DownloadsView";
import TodosView from "@/components/dashboard/TodosView";
import BuildsView from "@/components/dashboard/BuildsView";
import { DeployCapabilitiesView } from "@/components/dashboard/DeployCapabilitiesView";
import { DeployStatusView } from "@/components/dashboard/DeployStatusView";
import HealthView from "@/components/dashboard/HealthView";
import ScreenMonitorView from "@/components/dashboard/ScreenMonitorView";
import QualityView from "@/components/dashboard/QualityView";
import ConvexView from "@/components/dashboard/ConvexView";
import DataView from "@/components/dashboard/DataView";
import SwitchView from "@/components/dashboard/SwitchView";
import AccountsView from "@/components/dashboard/AccountsView";
import ObservabilityView from "@/components/dashboard/ObservabilityView";
import OpsView from "@/components/dashboard/OpsView";
import AutorunsView from "@/components/dashboard/AutorunsView";
import ToolPanelView from "@/components/dashboard/ToolPanelView";
import OverviewView from "@/components/dashboard/OverviewView";
import ExtrasView from "@/components/dashboard/ExtrasView";
import ShareView from "@/components/dashboard/ShareView";
import FeedbackWorkQueueView from "@/components/dashboard/FeedbackWorkQueueView";
import InfraView from "@/components/dashboard/InfraView";
import ConnectivityView from "@/components/dashboard/ConnectivityView";
import NetworkView from "@/components/dashboard/NetworkView";
import ToolsView from "@/components/dashboard/ToolsView";
import TwoFactorView from "@/components/dashboard/TwoFactorView";
import APIKeysView from "@/components/dashboard/APIKeysView";
import StorageView from "@/components/dashboard/StorageView";
import ArmCellView from "@/components/dashboard/ArmCellView";
import AppleTVCellView from "@/components/dashboard/AppleTVCellView";
import SchedulesView from "@/components/dashboard/SchedulesView";
import PackagesView from "@/components/dashboard/PackagesView";
import PhoneProjectsView from "@/components/dashboard/PhoneProjectsView";
import ExecView from "@/components/dashboard/ExecView";
import DomainsView from "@/components/dashboard/DomainsView";
import CompanyAIOptionsView from "@/components/dashboard/CompanyAIOptionsView";
import CompanionView from "@/components/dashboard/CompanionView";
import VibeCodingView, { AssistantMarkdown } from "@/components/dashboard/VibeCodingView";
import TaskProofCard, { taskProofVisible } from "@/components/dashboard/TaskProofCard";
import { capStreamText } from "@/lib/streamBuffer";
import PendingClaimsSection from "@/components/dashboard/PendingClaimsSection";
import WebviewView from "@/components/dashboard/WebviewView";
import RuntimeLabView, { type RuntimeLabIntent } from "@/components/dashboard/RuntimeLabView";
import DevicesView, { preferredDefaultModelForRunner, preferredDefaultRunnerForDevice, usePrimaryRunnerByDevice, RUNNER_WHITELIST_SET, MODEL_OPTIONS_BY_RUNNER, type OpenCodeCatalogueProvider } from "@/components/dashboard/DevicesView";
import { CapabilityShelf } from "@/components/dashboard/CapabilityShelf";
import RawFailureBanner, { announceRawFailure } from "@/components/dashboard/RawFailureBanner";
import { AnsiConsoleText, hasConsoleMarkup } from "@/components/dashboard/AnsiConsoleText";
import { summarizeRawConsole } from "@/lib/_core/ansi";
import {
  friendlyTaskPresentation,
  isTaskPresentationEvent,
  reduceTaskPresentation,
} from "@/lib/_core/taskPresentation";
import { firstClassTaskConversationTurns, remoteAgentConversationView, remoteAgentStatusLabel } from "@/lib/_core/taskConversation";
import { taskRunnerControlForMessage, taskRunnerControlSuggestions } from "@/lib/_core/taskRunnerControls";
import { interleaveConsolePrompts } from "@/lib/consoleInterleave";
import { SessionDeathError } from "@/lib/rawFailure";
import { isRelayCredentialDeny, RELAY_CREDENTIAL_REMEDY } from "@/lib/relayAuth";
import { usableTunnelUrls } from "@/lib/endpoints";
import { classifyFetchError, summarizeFailures } from "@/lib/connection-error";
import { clearLastFailure, recordLastFailure } from "@/lib/probe-backoff";
import { HIDE_PAID_UI } from "@/lib/launchFlags";
import { parseDashboardChatIntent } from "@/lib/dashboard-chat-intent";
import {
  loadLastProjectFromConvex,
  saveLastProjectToConvex,
  loadMCPServersFromConvex,
  saveMCPServersToConvex,
  loadSurfaceCatalogsFromConvex,
  setUseLatestMCPEnabled,
  setUseLatestProjectEnabled,
  useLatestMCPEnabled,
  useLatestProjectEnabled,
  runtimeProjectDisplayName,
  type MCPCatalogServer,
  type RuntimeProjectSeed,
} from "@/lib/runtimeProjectSettings";
import { decideComposerKey, insertNewline, newlineIsNative } from "@/lib/composerKeys";
import { runnerChipState } from "@/lib/runnerChipState";
import {
  activationBlockReason,
  activateTaskPlacement,
  createTaskDispatchIntent,
  expensiveCloudPlacementMessage,
  getTaskPlacementStatus,
  listTaskDispatchIntents,
  listRecentTaskPlacements,
  markTaskPlacementStatus,
  placementCreditLabel,
  placementLaneLabel,
  previewTaskPlacement,
  recordTaskPlacement,
  pendingPlacementTaskId,
  shouldConfirmExpensiveCloudPlacement,
  shouldDeferTaskForCloudWorkspace,
  rebindTaskPlacement,
  updateTaskDispatchIntent,
  upsertProjectProfile,
  type TaskPlacementDecision,
  type TaskPlacementKind,
  type TaskPlacementRequest,
  type TaskPlacementResourceClass,
} from "@/lib/task-placement";
import {
  listPendingCloudDispatches,
  mergePendingCloudPlacementStatus,
  mergePendingCloudDispatchIntents,
  pendingCloudDispatchNeedsUserAction,
  pendingCloudTaskPlaceholder,
  removePendingCloudDispatch,
  saveCloudWorkspaceRequiredDispatch,
  savePendingCloudDispatch,
  updatePendingCloudDispatch,
  type PendingCloudDispatch,
} from "@/lib/pending-cloud-dispatch";
import { CloudWorkspaceRequiredError } from "@/lib/cloud-workspace-required";
import { ParkedTurnError, parkedTurnNotice } from "@/lib/parkedTurn";
import StudioPanel from "@/components/dashboard/StudioPanel";
import QAPanel from "@/components/dashboard/QAPanel";
import WebTestsPanel from "@/components/dashboard/WebTestsPanel";
import SettingsView from "@/components/dashboard/SettingsView";
import { PlanUsageCard } from "@/components/dashboard/PlanUsageCard";
import { MachineRolesCard } from "@/components/dashboard/MachineRolesCard";
import { WEB_SURFACE_INFO, isThisDesktopDevice, type DesktopSurfaceInfo } from "@/lib/desktopSurface";
import type { RunnerBrowserAuthSession } from "@/lib/agent-client";
import webPkg from "../../package.json";
import { buildLabel } from "@/lib/buildStamp";

const WEB_VERSION = (webPkg as { version?: string }).version ?? "unknown";
// The semver is hand-maintained and does not move on every deploy, so it cannot
// answer "is this tab running the build I just shipped?". buildLabel appends the
// deployed git SHA — see web/lib/buildStamp.ts for the incident.
const WEB_BUILD_LABEL = buildLabel(WEB_VERSION);

function statusColor(s: string) {
  if (s === "running") return "text-amber-400";
  if (s === "review") return "text-violet-400";
  if (s === "completed") return "text-emerald-400";
  return "text-surface-400";
}

type ChatMsg = { role: "user" | "assistant"; text: string; queued?: boolean };
type OpenCodeAgentRow = { name: string; model?: string; isBuiltin?: boolean };
const DASHBOARD_TABS = [
  "home", "chat", "projects", "runtime", "vibe", "devices", "git", "todos",
  "feedback", "artifacts", "builds", "webview", "preview", "web-reload",
  "health", "quality", "convex", "data", "switch", "accounts", "company-ai",
  "companion", "observ", "ops", "autoruns", "extras", "share",
  "infra", "connect", "network", "tools", "security", "storage",
  "vault", "apikeys", "schedules", "exec", "phone", "vibe-preview",
  "domains", "screenlog", "settings", "billing", "stores", "cloud", "build",
  "arm", "appletv", "packages", "verbs", "downloads",
] as const;
type DashboardTab = typeof DASHBOARD_TABS[number];

function isDashboardTab(value: string | null): value is DashboardTab {
  return DASHBOARD_TABS.includes(value as DashboardTab);
}

function runnerModelOptions(runner?: Runner | null, runnerId?: string) {
  const live = Array.isArray(runner?.models) ? runner.models : [];
  if (live.length > 0) return live;
  return (MODEL_OPTIONS_BY_RUNNER[runnerId || ""] || []).map((model, index) => ({
    id: model.id,
    name: model.label,
    description: model.hint,
    isDefault: index === 0,
  }));
}

function openCodeProviderFromAgent(row: OpenCodeProviderSummary): OpenCodeCatalogueProvider {
  return {
    id: row.id,
    label: row.name || row.id,
    ...(row.isBuiltin ? {} : row.baseUrl ? { baseUrl: row.baseUrl } : {}),
    requiresKey: (row.environmentKeys?.length || 0) > 0,
    keyEnv: row.environmentKeys?.[0],
    blurb: row.environmentKeys?.length
      ? `API key stays on this machine (${row.environmentKeys.join(" or ")}).`
      : "Authentication and endpoint are owned by this machine's OpenCode provider.",
    isBuiltin: row.isBuiltin,
    models: (row.models || []).map((model) => ({
      id: model.id.startsWith(`${row.id}/`) ? model.id.slice(row.id.length + 1) : model.id,
      label: model.name || model.id,
      hint: model.description,
    })),
  };
}

function openCodeProvidersFromRunner(runner?: Runner | null): OpenCodeCatalogueProvider[] {
  const groups = new Map<string, OpenCodeCatalogueProvider>();
  for (const model of runner?.models || []) {
    const slash = model.id.indexOf("/");
    if (slash <= 0) continue;
    const providerID = model.provider || model.id.slice(0, slash);
    const modelID = model.id.slice(slash + 1);
    const provider = groups.get(providerID) || {
      id: providerID,
      label: model.providerName || providerID,
      requiresKey: false,
      blurb: "Models reported by OpenCode on this machine.",
      models: [],
      isBuiltin: true,
    };
    provider.models.push({
      id: modelID,
      label: model.name || modelID,
      hint: model.description,
    });
    groups.set(providerID, provider);
  }
  return [...groups.values()].sort((a, b) => a.label.localeCompare(b.label));
}

// Tasks created from the mobile "Open App" / "Run" flow carry a full
// "Project context: - Work dir: X\nUser request: Y" prompt as their title
// (the CLI uses text.slice(0, 80) for the title). Show the real ask.
function displayTaskTitle(title: string): string {
  const raw = (title || "").trim();
  if (!raw) return "untitled";
  const m = raw.match(/User request:\s*([\s\S]+?)$/i);
  if (m && m[1]) return m[1].trim().split("\n")[0] || raw;
  return raw;
}

function inferTaskPlacementKind(text: string): TaskPlacementKind {
  const lower = String(text || "").toLowerCase();
  if (/\b(deploy|publish|release|ship)\b/.test(lower)) return "deploy";
  if (/\b(build|apk|ipa|xcode|gradle|eas|archive)\b/.test(lower)) return "build";
  if (/\b(test|spec|lint|typecheck|ci)\b/.test(lower)) return "test";
  if (/\b(read|explain|review|summarize|inspect)\b/.test(lower)) return "source";
  return "vibe";
}

function projectSlugForPlacement(pathOrName?: string | null): string | undefined {
  const leaf = String(pathOrName || "")
    .split(/[\\/]/)
    .filter(Boolean)
    .pop()
    ?.trim();
  return leaf ? leaf.slice(0, 80) : undefined;
}

function resourceClassFromDashboardHints(args: {
  kind: TaskPlacementKind;
  path?: string | null;
}): TaskPlacementResourceClass {
  const haystack = String(args.path || "").toLowerCase();
  if (args.kind === "deploy" || args.kind === "build") return "build";
  if (/\b(ios|android|mobile|expo|react-native|hermes|docker|compose)\b/.test(haystack)) return "heavy";
  return args.kind === "source" || args.kind === "vibe" ? "relay-source" : "standard";
}

function DeviceIcon({ platform }: { platform: string }) {
  const normalized = String(platform || "").trim().toLowerCase();
  const isMobile = normalized === "ios" || normalized === "android";
  if (isMobile) {
    return (
      <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" d="M10.5 1.5H8.25A2.25 2.25 0 006 3.75v16.5a2.25 2.25 0 002.25 2.25h7.5A2.25 2.25 0 0018 20.25V3.75a2.25 2.25 0 00-2.25-2.25H13.5m-3 0V3h3V1.5m-3 0h3m-3 18.75h3" />
      </svg>
    );
  }
  return (
    <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
      <path strokeLinecap="round" strokeLinejoin="round" d="M9 17.25v1.007a3 3 0 01-.879 2.122L7.5 21h9l-.621-.621A3 3 0 0115 18.257V17.25m6-12V15a2.25 2.25 0 01-2.25 2.25H5.25A2.25 2.25 0 013 15V5.25A2.25 2.25 0 015.25 3h13.5A2.25 2.25 0 0121 5.25z" />
    </svg>
  );
}

// Trust the agent's authoritative isWsl bit (hardwareProfile.isWsl,
// derived from /proc/version + WSL_DISTRO_NAME on the host) when
// reported. Hostname suffix is a soft fallback for older agents.
// We deliberately do NOT use the IP-shape heuristic — Docker bridges
// (172.16-31.x.y) on real Linux boxes (Pi, plain VPS, etc.)
// false-positived as WSL with that rule.
function isLikelyWSLDevice(device: Pick<Device, "name" | "platform" | "hardwareProfile">): boolean {
  const platform = String(device.platform || "").trim().toLowerCase();
  if (platform !== "linux") return false;
  if (device.hardwareProfile?.isWsl === true) return true;
  if (device.hardwareProfile?.isWsl === false) return false;
  const name = String(device.name || "").trim().toUpperCase();
  return name.startsWith("DESKTOP-") || name.startsWith("LAPTOP-") || name.startsWith("WIN-");
}

function devicePlatformLabel(device: Pick<Device, "name" | "platform" | "hardwareProfile">): string {
  const platform = String(device.platform || "").trim().toLowerCase();
  if (isLikelyWSLDevice(device)) return "Linux (likely WSL)";
  switch (platform) {
    case "darwin":
    case "macos":
      return "macOS";
    case "linux":
      return "Linux";
    case "windows":
      return "Windows";
    case "android":
      return "Android";
    case "ios":
      return "iOS";
    default:
      return device.platform || "Unknown";
  }
}

function formatHeartbeatAge(lastSeen?: string): string {
  if (!lastSeen) return "never";
  const ts = Date.parse(lastSeen);
  if (Number.isNaN(ts)) return "unknown";
  const seconds = Math.max(0, Math.floor((Date.now() - ts) / 1000));
  if (seconds < 10) return "just now";
  if (seconds < 60) return `${seconds}s ago`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  if (seconds < 604800) return `${Math.floor(seconds / 86400)}d ago`;
  return new Date(ts).toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

function deviceReachabilitySummary(
  device: Pick<Device, "online" | "needsAuth" | "lastSeen" | "publicEndpoints" | "tunnelUrl" | "host" | "lastTunnelEvent" | "peerState" | "workspaceLive" | "probeState" | "probePath" | "probeError" | "probeInfo">,
): string {
  if (device.workspaceLive) return "Active workspace connection";
  const lifecycleState = String(device.probeInfo?.lifecycle?.state || device.probeInfo?.lifecycleState || "");
  if (lifecycleState === "bootstrap") return "Bootstrap server reached; reclaim or pair Yaver first";
  if (lifecycleState === "yaver-auth-expired") return "Agent reached, but its session is expired";
  if (lifecycleState === "ready-to-connect") return `Authenticated agent probe succeeded via ${device.probePath || "device path"}`;
  if (device.probeState === "ok") return `Authenticated agent probe succeeded via ${device.probePath || "device path"}`;
  if (device.probeState === "auth-expired") return "Agent reached, but its session is expired";
  if (device.peerState === "online") return "Live bus signal";
  if (hasRecentLiveSignal(device)) return "Live relay signal";
  if (device.peerState === "stale") return "Bus saw this machine recently, but no current transport is healthy";
  if (device.online) return "Recently confirmed by agent";
  if (device.needsAuth && device.online) {
    return "Bootstrap agent advertised recently; reclaim or pair may still work";
  }
  const age = formatAgeShort(lastSeenAgeMs(device.lastSeen));
  const hasPublicPath = Boolean(device.tunnelUrl) || Boolean(device.publicEndpoints?.length);
  if (age && hasPublicPath) return `No recent agent signal for ${age}; relay or tunnel may still be worth probing`;
  if (age) return `No recent agent signal for ${age}; no tunnel or public endpoint advertised`;
  if (hasPublicPath) return "No recent agent signal; relay or tunnel may still be worth probing";
  if (device.probeError) return device.probeError;
  if (device.host) return "No recent agent signal; direct browser access usually needs relay";
  return "No recent agent signal";
}

const DORMANT_DEVICE_HIDE_MS = 10 * 60 * 1000;

function isDormantUnreachableDevice(
  device: Pick<Device, "online" | "needsAuth" | "lastSeen" | "publicEndpoints" | "tunnelUrl" | "peerState" | "workspaceLive" | "probeState" | "probeInfo">,
): boolean {
  if (device.online) return false;
  if (device.workspaceLive) return false;
  const lifecycleState = String(device.probeInfo?.lifecycle?.state || device.probeInfo?.lifecycleState || "");
  if (lifecycleState === "bootstrap" || lifecycleState === "yaver-auth-expired" || lifecycleState === "ready-to-connect") return false;
  if (device.probeState === "ok" || device.probeState === "auth-expired") return false;
  if (device.peerState === "online") return false;
  if (device.needsAuth) return false;
  if (Boolean(device.tunnelUrl) || Boolean(device.publicEndpoints?.length)) return false;
  const age = lastSeenAgeMs(device.lastSeen);
  return age !== null && age >= DORMANT_DEVICE_HIDE_MS;
}

function duplicateHostKey(device: Pick<Device, "platform" | "name">): string | null {
  const platform = String(device.platform || "").trim().toLowerCase();
  const name = String(device.name || "").trim().toLowerCase().replace(/\.local$/, "");
  if (!platform || !name) return null;
  return `${platform}:${name}`;
}

function stableAliasRank(device: Pick<Device, "alias">): number {
  const alias = String(device.alias || "").trim().toLowerCase();
  if (!alias) return 1;
  return /-\d+$/.test(alias) ? 2 : 0;
}

function operationRank(device: Pick<Device, "online" | "needsAuth" | "workspaceLive" | "peerState" | "probeState" | "lastTunnelEvent">): number {
  if (device.workspaceLive) return 0;
  if (device.probeState === "ok") return 1;
  if (device.online || device.peerState === "online" || device.lastTunnelEvent?.online === true) return 2;
  if (!device.needsAuth) return 3;
  return 4;
}

function formatRunnerChipLabel(runner: string): string {
  const cleaned = String(runner || "").trim();
  if (!cleaned) return cleaned;
  if (cleaned === "claude-code") return "claude";
  return cleaned;
}

function runnerLabel(runnerId?: string): string {
  const normalized = formatRunnerChipLabel(String(runnerId || ""));
  if (normalized === "claude") return "Claude Code";
  if (normalized === "codex") return "Codex";
  if (normalized === "opencode") return "OpenCode";
  return normalized || "Selected runner";
}

function runnerAuthIssue(
  runner:
    | (Pick<Runner, "id" | "installed" | "ready" | "warning" | "error"> & { authConfigured?: boolean; needsAuth?: boolean })
    | null
    | undefined,
): string | null {
  if (!runner || !runner.installed) return null;

  // authConfigured is the AGENT'S OWN ANSWER about whether that runner can
  // actually run, and it was never consulted here. The old guard bailed out
  // unless ready === false, so a runner reporting authConfigured:false sailed
  // through as healthy and the sidebar rendered "✓ SIGNED IN".
  //
  // Measured 2026-07-26 on the Mac mini: /agent/runners said
  //   opencode authConfigured=true
  //   claude   authConfigured=FALSE
  //   codex    authConfigured=true
  // while the dashboard showed "runner: Claude Code ✓ SIGNED IN", made Claude
  // Code the ACTIVE runner, and every message sent from Chat stopped at
  // "⏳ Waiting for response from AI agent…" forever — dispatched to a runner
  // that cannot start. The user reported it as "chat is stuck"; it was the chip
  // lying one step upstream.
  //
  // Checked BEFORE the ready/warning heuristics below, because this is a direct
  // statement of fact from the machine rather than a string match on an error
  // message that may not exist.
  if (runner.authConfigured === false || runner.needsAuth === true) {
    return `${runnerLabel(runner.id)} is installed but NOT signed in on this machine — sign it in, or pick a runner that is. Tasks sent to it will wait forever.`;
  }

  // AN OBSERVED REFUSAL OUTRANKS A LOCAL VOUCH (2026-08-02).
  //
  // The branch below only fired when `ready === false`. On the owner's screen
  // the row said authConfigured:true / ready:true — because `codex login
  // status` had vouched for a credential file that the PROVIDER had already
  // stopped accepting — while the chat, in the same viewport, printed
  // "Codex's token has expired and could not be refreshed."
  //
  // runnerChipState centralises the rule (proof beats vouch, and an observed
  // refusal beats both) and its matcher is pinned by tests so a SyntaxError, an
  // ECONNRESET, or a model-entitlement 400 is NEVER mistaken for a dead
  // credential — routing any of those to re-auth would be a false red that
  // cannot fix the user's actual problem.
  const observed = runnerChipState({
    runnerLabel: runnerLabel(runner.id),
    installed: runner.installed,
    ready: runner.ready,
    authConfigured: runner.authConfigured,
    needsAuth: runner.needsAuth,
    lastError: String(runner.error || runner.warning || "") || null,
  });
  if (observed.tone === "expired") {
    return `${observed.detail} ${observed.action}`.trim();
  }

  if (runner.ready !== false) return null;
  const detail = String(runner.error || runner.warning || "").trim();
  const lower = detail.toLowerCase();
  if (
    lower.includes("auth") ||
    lower.includes("login") ||
    lower.includes("sign in") ||
    lower.includes("oauth") ||
    lower.includes("not authenticated")
  ) {
    return detail || `${runnerLabel(runner.id)} is installed but not authenticated on this machine.`;
  }
  return null;
}

function lanIpsForDevice(device: Pick<Device, "host" | "localIps">): string[] {
  const ips = new Set<string>();
  if (device.host && /^\d{1,3}(?:\.\d{1,3}){3}$/.test(device.host)) ips.add(device.host);
  for (const ip of device.localIps || []) {
    if (ip) ips.add(ip);
  }
  return [...ips].slice(0, 3);
}

// Inline alias editor: click the chip to edit, Enter to save, Esc
// to cancel. Device discovery is owner-only, so every rendered row is writable
// by the authenticated account. When no alias is set we render a muted
// "+ alias" affordance so the slot is still discoverable.
function DeviceAliasChip({
  device,
  token,
  onSaved,
}: {
  device: Device;
  token: string;
  onSaved: () => void;
}) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(device.alias ?? "");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!editing) setDraft(device.alias ?? "");
  }, [device.alias, editing]);

  const commit = useCallback(async () => {
    const next = draft.trim().toLowerCase();
    if (next === (device.alias ?? "")) {
      setEditing(false);
      setError(null);
      return;
    }
    setSaving(true);
    setError(null);
    const res = await setDeviceAlias(token, device.id, next);
    setSaving(false);
    if (!res.ok) {
      setError(res.error);
      return;
    }
    setEditing(false);
    onSaved();
  }, [draft, device.alias, device.id, token, onSaved]);

  if (editing) {
    return (
      <span className="inline-flex items-center gap-1">
        <input
          autoFocus
          value={draft}
          disabled={saving}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              void commit();
            } else if (e.key === "Escape") {
              e.preventDefault();
              setEditing(false);
              setError(null);
              setDraft(device.alias ?? "");
            }
          }}
          onBlur={() => {
            // Defer so click on the inline error/help link still works
            setTimeout(() => void commit(), 120);
          }}
          placeholder="prod-mac"
          spellCheck={false}
          className="w-28 rounded-full border border-emerald-500/40 bg-surface-950 px-2 py-0.5 font-mono text-[10px] text-emerald-700 dark:text-emerald-200 outline-none focus:border-emerald-400"
        />
        {error ? (
          <span title={error} className="text-[10px] text-red-700 dark:text-red-300/80">!</span>
        ) : null}
      </span>
    );
  }

  if (!device.alias) {
    return (
      <button
        type="button"
        onClick={() => setEditing(true)}
        className="rounded-full border border-dashed border-surface-700 bg-transparent px-2 py-0.5 text-[10px] font-medium text-surface-400 hover:border-emerald-500/40 hover:text-emerald-700 dark:hover:text-emerald-200"
        title="Add a short alias (used by `yaver ssh <alias>`)"
      >
        + alias
      </button>
    );
  }

  return (
    <button
      type="button"
      onClick={() => setEditing(true)}
      className="rounded-full border border-emerald-500/30 bg-emerald-500/10 px-2 py-0.5 font-mono text-[10px] font-medium text-emerald-700 dark:text-emerald-200 hover:border-emerald-400/60"
      title="Click to rename this alias"
    >
      @{device.alias}
    </button>
  );
}

function DeviceConnectCard({
  device,
  isPrimary,
  isSecondary = false,
  isSelected,
  isConnecting,
  connectionError,
  onConnect,
  onTogglePrimary,
  canTogglePrimary,
  onToggleSecondary,
  canToggleSecondary,
  onAliasSaved,
  onOpenShell,
  onOpenRemoteDesktop,
  token,
  compact = false,
}: {
  device: Device;
  isPrimary: boolean;
  isSecondary?: boolean;
  isSelected: boolean;
  isConnecting: boolean;
  connectionError?: string | null;
  onConnect: () => void;
  onTogglePrimary?: () => void;
  canTogglePrimary?: boolean;
  onToggleSecondary?: () => void;
  canToggleSecondary?: boolean;
  onAliasSaved?: () => void;
  onOpenShell?: () => void;
  onOpenRemoteDesktop?: () => void;
  token?: string | null;
  compact?: boolean;
}) {
  const heartbeatAge = formatHeartbeatAge(device.lastSeen);
  const reachability = deviceReachabilitySummary(device);
  const liveSignal = hasRecentLiveSignal(device);
  const liveSignalAge = liveSignal && device.lastTunnelEvent?.at
    ? formatHeartbeatAge(new Date(device.lastTunnelEvent.at).toISOString())
    : heartbeatAge;
  const lanIps = lanIpsForDevice(device);
  const lifecycleState = deriveDeviceLifecycleState(device);
  const statusTone =
    lifecycleState === "connected"
      ? "bg-emerald-400"
      : lifecycleState === "bootstrap"
        ? "bg-violet-400"
        : lifecycleState === "yaver-auth-expired"
          ? "bg-amber-400"
          : lifecycleState === "ready-to-connect"
            ? "bg-cyan-400"
            : "bg-surface-600";
  const statusLabel =
    lifecycleState === "connected"
      ? "connected"
      : lifecycleState === "bootstrap"
        ? "bootstrap"
        : lifecycleState === "yaver-auth-expired"
          ? "yaver auth expired"
          : lifecycleState === "ready-to-connect"
            ? "ready to connect"
            : "offline";

  return (
    <div
      className={[
        "rounded-2xl border bg-surface-900/80 shadow-sm transition-colors dark:border-surface-700/80 dark:bg-[rgba(44,46,56,0.82)] dark:shadow-[0_18px_40px_rgba(0,0,0,0.22),inset_0_1px_0_rgba(255,255,255,0.03)]",
        compact ? "p-3" : "p-3.5",
        isSelected
          ? connectionError
            ? "border-red-500/30 bg-red-500/[0.04]"
            : isConnecting
              ? "border-amber-500/30 bg-amber-500/[0.04]"
              : "border-emerald-500/30 bg-emerald-500/[0.05]"
          : "border-surface-800 hover:border-surface-700 dark:hover:border-surface-600",
      ].join(" ")}
    >
      <div className="flex items-start gap-2.5">
        <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl border border-surface-800 bg-surface-950 text-surface-400 dark:border-surface-700/80 dark:bg-[rgba(18,19,24,0.92)] dark:text-surface-300">
          <DeviceIcon platform={device.platform} />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-1.5">
            <h3 className="truncate text-[15px] font-semibold text-surface-100">{device.name}</h3>
            {token ? (
              <DeviceAliasChip
                device={device}
                token={token}
                onSaved={() => { if (onAliasSaved) onAliasSaved(); }}
              />
            ) : null}
            <span className={`inline-flex h-2 w-2 rounded-full ${connectionError ? "bg-red-400" : isConnecting ? "bg-amber-400" : statusTone}`} />
            <span className="text-[11px] text-surface-400">
              {connectionError
                ? "failed"
                : isConnecting
                  ? "connecting"
                  : statusLabel} · {liveSignalAge}
            </span>
          </div>
          <p className="mt-1 text-[11px] leading-5 text-surface-500">
            {devicePlatformLabel(device)}
            {device.host ? ` · ${device.host}:${device.port}` : ""}
          </p>
          {!connectionError && lifecycleState !== "connected" ? (
            <p className="mt-1 text-[11px] leading-5 text-amber-700 dark:text-amber-300/80">{reachability}</p>
          ) : null}
          {connectionError ? (
            <p className="mt-1 text-[11px] text-red-700 dark:text-red-300/80">{connectionError}</p>
          ) : null}
        </div>
      </div>

      <div className="mt-2.5 flex flex-wrap gap-1.5">
        {device.deviceClass ? (
          <span className="rounded-full border border-sky-500/30 bg-sky-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.14em] text-sky-700 dark:text-sky-200">
            {device.deviceClass === "edge-mobile" ? "Edge Worker" : device.deviceClass}
          </span>
        ) : null}
        {isPrimary ? (
          <span className="rounded-full border border-indigo-500/40 bg-indigo-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.14em] text-indigo-700 dark:text-indigo-200">
            Primary
          </span>
        ) : null}
        {lanIps.map((ip) => (
          <span key={`${device.id}:${ip}`} className="rounded-full border border-surface-700 bg-surface-950 px-2 py-0.5 font-mono text-[10px] text-surface-300">
            {ip}
          </span>
        ))}
      </div>

      {device.edgeProfile ? (
        <p className="mt-2 text-[11px] text-surface-500">
          {device.edgeProfile.supportsLocalInference ? "Local inference" : "Remote inference only"} · max {device.edgeProfile.maxModelClass} model
          {device.edgeProfile.preferredTasks.length > 0 ? ` · ${device.edgeProfile.preferredTasks.slice(0, 3).join(", ")}` : ""}
        </p>
      ) : null}

      <div className="mt-3 flex flex-wrap items-center gap-2">
        <button
          onClick={onConnect}
          disabled={isConnecting}
          className={`rounded-xl px-3 py-1.5 text-xs font-semibold transition-colors ${
            isConnecting
              ? "cursor-wait border border-amber-500/20 bg-amber-500/10 text-amber-700 dark:text-amber-200"
              : "border border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-200 hover:bg-emerald-500/15"
          }`}
        >
          {isConnecting ? "Connecting…" : lifecycleState === "connected" ? "Open Workspace" : lifecycleState === "bootstrap" ? "Reclaim & Connect" : lifecycleState === "yaver-auth-expired" ? "Re-auth & Connect" : "Connect"}
        </button>
        {onOpenShell ? (
          <button
            onClick={onOpenShell}
            className="rounded-xl border border-cyan-500/30 bg-cyan-500/10 px-3 py-1.5 text-xs font-semibold text-cyan-700 dark:text-cyan-200 hover:bg-cyan-500/15"
            title="Open a browser shell on this device (PTY over relay)"
          >
            <span aria-hidden className="mr-1">⌨</span>Shell
          </button>
        ) : null}
        {onOpenRemoteDesktop ? (
          <button
            onClick={onOpenRemoteDesktop}
            className="rounded-xl border border-fuchsia-500/30 bg-fuchsia-500/10 px-3 py-1.5 text-xs font-semibold text-fuchsia-700 dark:text-fuchsia-200 hover:bg-fuchsia-500/15"
            title="Open the live desktop (screen view + mouse/keyboard control) over relay"
          >
            <span aria-hidden className="mr-1">🖥</span>Desktop
          </button>
        ) : null}
        {canTogglePrimary && onTogglePrimary ? (
          <button
            onClick={onTogglePrimary}
            className="rounded-xl border border-indigo-500/30 bg-indigo-500/10 px-3 py-1.5 text-xs font-semibold text-indigo-700 dark:text-indigo-200 hover:bg-indigo-500/15"
          >
            {isPrimary ? "Unset Primary" : "Make Primary"}
          </button>
        ) : null}
        {canToggleSecondary && onToggleSecondary && !isPrimary ? (
          <button
            onClick={onToggleSecondary}
            className="rounded-xl border border-violet-500/30 bg-violet-500/10 px-3 py-1.5 text-xs font-semibold text-violet-700 dark:text-violet-200 hover:bg-violet-500/15"
            title={isSecondary ? "Clear secondary slot" : "Mark this device as your fallback secondary machine"}
          >
            {isSecondary ? "Unset Secondary" : "Make Secondary"}
          </button>
        ) : null}
      </div>
    </div>
  );
}

// Tabs whose views call the local agent over a live connection. When the
// agent isn't connected these would otherwise leak the raw
// "AgentClient is not connected. Call connect() first." string from
// agent-client's assertConnected(); instead we render the shared connect
// guidance panel (the same device-picker the chat tab uses). Tabs that work
// without a connected agent (devices, connect, network/Mesh — Convex-direct,
// billing, cloud, build, settings, security, home, domains, company-ai, infra)
// and the self-gating preview tabs (vibe, webview,
// preview, web-reload) are intentionally excluded.
const CONNECTION_REQUIRED_TABS = new Set<string>([
  "chat", "projects", "git", "runtime", "storage", "ops", "data", "convex",
  "schedules", "apikeys", "exec", "companion", "builds", "quality", "observ",
  "screenlog", "extras", "accounts", "switch", "tools", "phone", "health",
  "todos", "arm", "appletv", "verbs", "autoruns",
]);

// HN-LAUNCH-HIDE-PAID: paid surfaces (Billing, Cloud + Build/CapabilityShelf
// tabs, Yaver Cloud "rent a box" banner, metered choices) are hidden via the
// shared HIDE_PAID_UI flag (imported from @/lib/launchFlags). Owned machines
// stay reachable via the Devices tab, so hiding the Cloud tab loses no control.

// Long assistant outputs collapse behind a "Show details" toggle — same
// spirit as mobile's bubble collapse. Thresholds mirror mobile's
// buildAssistantPreview (>30 non-empty lines or >2500 chars).
const CHAT_COLLAPSE_LINES = 30;
const CHAT_COLLAPSE_CHARS = 2500;

function isLongAssistantText(text: string): boolean {
  const t = text.trim();
  const lines = t.split("\n").filter((l) => l.trim()).length;
  return lines > CHAT_COLLAPSE_LINES || t.length > CHAT_COLLAPSE_CHARS;
}

/**
 * ChatAssistantMsg — memoized assistant bubble for the chat tab.
 *
 * Memoized on (text, status, isLast) so a growing live message re-renders
 * only itself, not the whole transcript. While the LIVE tail is still growing
 * it is never collapsed — a collapsing "Show details" button that moves with
 * every token is worse than the scroll. Collapse applies to finalized long
 * messages only.
 */
const ChatAssistantMsg = memo(function ChatAssistantMsg({
  text,
  status,
  isLast,
}: {
  text: string;
  status?: string;
  isLast: boolean;
}) {
  const [expanded, setExpanded] = useState(false);
  const liveGrowing = isLast && status === "running";
  const long = isLongAssistantText(text);
  const collapsed = long && !liveGrowing && !expanded;
  const head = text.trim().split("\n").slice(0, CHAT_COLLAPSE_LINES).join("\n");
  // Never leave an unclosed fenced block at the cut — the collapsed head
  // must render as valid markdown (a lone "```" would swallow the rest of
  // the transcript into one giant code block).
  const fenceCount = (head.match(/^```/gm) || []).length;
  const shown = collapsed ? (fenceCount % 2 === 1 ? `${head}\n\`\`\`` : head) : text;
  // Console look (2026-08-09): when the assistant text carries opencode
  // console shapes (ANSI escapes, `$` prompts, `> build` banners, git
  // patches), render it through the shared ANSI console view instead of
  // flattening to markdown — orange banners, green prompts, coloured
  // patches, exactly as the terminal shows them.
  const consoleMarkup = hasConsoleMarkup(shown);
  return (
    <div>
      <div className="prose-invert break-words [&_pre]:whitespace-pre-wrap">
        {consoleMarkup ? (
          <AnsiConsoleText text={shown} />
        ) : (
          <AssistantMarkdown text={shown} />
        )}
        {liveGrowing ? (
          <span className="ml-0.5 inline-block h-3 w-1.5 translate-y-[2px] animate-pulse bg-surface-300" aria-hidden />
        ) : null}
      </div>
      {long && !liveGrowing ? (
        <button
          type="button"
          onClick={() => setExpanded((v) => !v)}
          className="mt-1 text-[11px] font-semibold text-[#818cf8] hover:text-[#a5b4fc]"
        >
          {expanded ? "Hide details" : "Show details"}
        </button>
      ) : null}
    </div>
  );
});

export default function DashboardPage() {
  // ── ALL hooks unconditionally at the top ────────────────────────
  const { user, token, isLoading, isAuthenticated, sessionExpired, logout } = useAuth();
  const { devices, refreshDevices, hiddenIds, loading: devicesLoading, error: devicesError, lastFetchedAt: devicesFetchedAt } = useDevices(token);
  // Bootstrap-pending claims — boxes that joined the user's relay but
  // don't have a Convex devices row yet. Surfaced to the user so a
  // freshly-installed remote box becomes claimable from the dashboard
  // without ever touching the LAN.
  const { pending: pendingClaims, refreshPending, claimPending } = usePendingClaims(token);
  const { theme, toggle: toggleTheme } = useTheme();
  const router = useRouter();
  const [desktopSurface, setDesktopSurface] = useState<DesktopSurfaceInfo>(WEB_SURFACE_INFO);

  useEffect(() => {
    if (typeof window === "undefined") return;
    const bridge = (window as typeof window & {
      yaver?: {
        surface?: string;
        getDesktopStatus?: () => Promise<{ surface?: string; localDeviceId?: string | null }>;
        onAgentStatus?: (listener: () => void) => (() => void);
      };
    }).yaver;
    if (bridge?.surface !== "desktop-gui" || typeof bridge.getDesktopStatus !== "function") return;
    let cancelled = false;
    const refresh = async () => {
      try {
        const status = await bridge.getDesktopStatus?.();
        if (!cancelled && status?.surface === "desktop-gui") {
          setDesktopSurface({
            isDesktop: true,
            localDeviceId: typeof status.localDeviceId === "string" && status.localDeviceId ? status.localDeviceId : null,
          });
        }
      } catch {
        if (!cancelled) setDesktopSurface({ isDesktop: true, localDeviceId: null });
      }
    };
    void refresh();
    const unsubscribe = bridge.onAgentStatus?.(() => { void refresh(); });
    return () => {
      cancelled = true;
      unsubscribe?.();
    };
  }, []);

  const [connState, setConnState] = useState<ConnectionState>("disconnected");
  const [connectedDevice, setConnectedDevice] = useState<Device | null>(null);
  const [agentInfo, setAgentInfo] = useState<AgentInfo | null>(null);
  const [tasks, setTasks] = useState<Task[]>([]);
  const [activeTask, setActiveTask] = useState<Task | null>(null);
  const [taskActionBusy, setTaskActionBusy] = useState<string | null>(null);
  const pendingDispatchRef = useRef<Set<string>>(new Set());
  // ── raw-lane note ─────────────────────────────────────────
  // Every runner (opencode, codex, claude, …) streams its RAW stdout
  // (ANSI + TUI intact) as `raw`/`raw_replay` SSE frames (agent 1.99.406+,
  // commit d671b7c02) — see tasks.go emitRaw, which runs before any
  // per-runner grooming. Since 2026-08-12 the task view IS the console:
  // rawOutput is the primary output, painted via AnsiConsoleText. The
  // groomed chat transcript survives only as the no-raw-lane fallback and
  // as the source of the `$` placeholder prompts. rawSince resume keeps a
  // reattach from re-rendering the scrollback.
  const [rawOutput, setRawOutput] = useState<string[]>([]);
  const [rawSince, setRawSince] = useState(0);
  // Follow-output toggle (xterm-style): autoscroll tracks the tail while
  // ON; the moment the user scrolls up to read, following turns off so the
  // stream can't yank them back to the bottom. A pill re-engages it.
  const [followOutput, setFollowOutput] = useState(true);
  // Pending agent_question pulled from the SSE stream. When non-null
  // the dashboard renders an inline answer card above the composer;
  // submitting POSTs to /tasks/{id}/answer (via answerTaskQuestion),
  // the daemon resolves the parked /question handler, and the runner's
  // yaver_ask_user MCP call returns. agent_answered /
  // agent_question_cancelled SSE events also clear it so a phone
  // answering first doesn't leave the card orphaned.
  const [agentQuestion, setAgentQuestion] = useState<{
    id: string;
    taskId: string;
    prompt: string;
    kind: "text" | "choice" | "secret";
    choices?: string[];
    vaultHint?: string;
    screenshot?: string; // F3 handoff: base64 PNG region
    step?: string;       // F3 handoff step type
  } | null>(null);
  const [agentAnswerText, setAgentAnswerText] = useState("");
  const [submittingAgentAnswer, setSubmittingAgentAnswer] = useState(false);
  const [outputLines, setOutputLines] = useState<string[]>([]);
  const [chatMsgs, setChatMsgs] = useState<ChatMsg[]>([]);
  const [runners, setRunners] = useState<Runner[]>([]);
  const [mcpServers, setMcpServers] = useState<McpServer[]>([]);
  const [selectedMcpServers, setSelectedMcpServers] = useState<string[]>([]);
  const [includeYaverMcp, setIncludeYaverMcp] = useState(false);
  const [useLatestProject, setUseLatestProject] = useState(() => useLatestProjectEnabled());
  const [useLatestMCP, setUseLatestMCP] = useState(() => useLatestMCPEnabled());
  // Chat composer project picker — the web twin of mobile's
  // renderProjectPickerSheet. `preferredSurfaceProjectPath` feeds task
  // workDir; the picker makes it user-visible instead of chat-intent-only
  // ("webview <path>"). Last choice is remembered via Convex
  // defaultRuntimeProjectByDevice (loadLastProjectFromConvex /
  // saveLastProjectToConvex), the SAME row mobile writes — so a project
  // picked on the phone shows up here and vice versa.
  const [chatProjects, setChatProjects] = useState<Array<{
    name: string; path: string; branch?: string; framework?: string; gitRemote?: string;
  }>>([]);
  const [chatProjectPickerOpen, setChatProjectPickerOpen] = useState(false);
  // Cross-machine surface catalogs (2026-08-13): which MCP servers / which
  // git projects live on which machine, seeded by each agent's heartbeat
  // into userSettings.mcpCatalogByDevice / runtimeProjectCatalogByDevice.
  // This is what lets the chat composer offer ANOTHER machine's MCP servers
  // as selectable chips and browse other machines' projects, without fanning
  // out to every box. Advisory: empty maps on failure, never blocks.
  const [mcpCatalogByDevice, setMcpCatalogByDevice] = useState<Record<string, MCPCatalogServer[]>>({});
  const [projectCatalogByDevice, setProjectCatalogByDevice] = useState<Record<string, RuntimeProjectSeed[]>>({});
  const [switchNotice, setSwitchNotice] = useState<string | null>(null); // "Switched to <machine>" inline notice
  const [selectedRunner, setSelectedRunner] = useState<string>("");
  const [selectedModel, setSelectedModel] = useState<string>("");
  const [selectedOpenCodeMode, setSelectedOpenCodeMode] = useState<string>("");
  const [openCodeAgents, setOpenCodeAgents] = useState<OpenCodeAgentRow[]>([]);
  // OpenCode-specific provider + key flow. Only used when
  // selectedRunner === "opencode". The chosen model id is also written
  // to selectedModel so the existing createTask path picks it up.
  const [opencodeProvider, setOpencodeProvider] = useState<string>("");
  const [openCodeCatalogue, setOpenCodeCatalogue] = useState<OpenCodeCatalogueProvider[]>([]);
  const [opencodeApiKey, setOpencodeApiKey] = useState<string>("");
  const [opencodeSaving, setOpencodeSaving] = useState(false);
  const [opencodeSaveMsg, setOpencodeSaveMsg] = useState<{ ok: boolean; text: string } | null>(null);
  // Map of provider id → whether the agent's opencode.json already has
  // a non-empty apiKey for it. P2P: read straight from
  // /runner/opencode/config, never via Convex. Drives the "✓ Key
  // configured" badge + the "Change key" toggle so users don't paste
  // the same Z.ai/Anthropic/etc key on every visit.
  const [opencodeKeyState, setOpencodeKeyState] = useState<Record<string, boolean>>({});
  // When true, a saved-key provider still shows the input so the user
  // can replace the key. Reset on provider switch.
  const [opencodeChangingKey, setOpencodeChangingKey] = useState(false);
  // Chat composer's runner/provider/model/mode picker is verbose. Hide
  // by default and let the user expand it; persist the choice across
  // page loads so they don't fight it on every visit.
  const [chatPickerExpanded, setChatPickerExpanded] = useState<boolean>(() => {
    if (typeof window === "undefined") return false;
    return window.localStorage.getItem("yaver:chat-picker-expanded") === "1";
  });
  useEffect(() => {
    if (typeof window === "undefined") return;
    try { window.localStorage.setItem("yaver:chat-picker-expanded", chatPickerExpanded ? "1" : "0"); } catch {}
  }, [chatPickerExpanded]);
  const [input, setInput] = useState("");
  const [sending, setSending] = useState(false);
  const [runnerControlMode, setRunnerControlMode] = useState<"model" | "exit" | null>(null);
  const [runnerControlCatalog, setRunnerControlCatalog] = useState<TaskRunnerControlCatalog | null>(null);
  const [runnerControlStep, setRunnerControlStep] = useState<"models" | "effort">("models");
  const [runnerControlModel, setRunnerControlModel] = useState("");
  const [runnerControlEffort, setRunnerControlEffort] = useState<"none" | "low" | "medium" | "high" | "xhigh" | "max" | "ultra">("medium");
  const [runnerControlBusy, setRunnerControlBusy] = useState(false);
  const [runnerControlError, setRunnerControlError] = useState("");
  // Local queue of follow-up prompts the user typed while the active
  // task was still running. The Yaver agent rejects POST
  // /tasks/<id>/continue with 500 until the prior turn finishes (see
  // handleSend's `continuing` comment), and the runner CLIs we wrap
  // (claude `-p`, codex `exec`, opencode `run`) are one-shot per
  // invocation — no native back-channel to inject a follow-up
  // mid-stream. So we mirror what claude-code / codex / opencode do
  // interactively: keep typing, queue up, dispatch on completion.
  const [pendingFollowUps, setPendingFollowUps] = useState<string[]>([]);
  const [reauthBusy, setReauthBusy] = useState<string | null>(null);
  const [reauthMsg, setReauthMsg] = useState<{ deviceId: string; ok: boolean; text: string } | null>(null);
  // Browser-shell modal state. We track the device the user clicked
  // "Shell" on so the modal can decide whether agentClient is already
  // pointed at it; if not it offers a "Connect & open shell" affordance
  // instead of silently opening a WS against the wrong baseUrl.
  const [shellDevice, setShellDevice] = useState<Device | null>(null);
  const [shellTmuxSession, setShellTmuxSession] = useState<string | null>(null);
  const [shellTmuxTaskId, setShellTmuxTaskId] = useState<string | null>(null);
  // Live tmux sessions for the sidebar "Vibing" list. One shared source with the
  // Vibing tab (agentClient.listTmuxSessions), polled only while connected — a
  // session list from a box we're not attached to would be fiction.
  const [sidebarTmux, setSidebarTmux] = useState<TmuxSessionSummary[]>([]);
  const sidebarTmuxSeats = useMemo(() => sidebarTmux.flatMap((session) =>
    session.panes?.length
      ? session.panes.map((pane) => ({
          ...session,
          paneId: pane.paneId,
          taskId: pane.taskId,
          agentType: pane.agent,
          inputMode: pane.inputMode,
          origin: pane.origin ?? session.origin,
        }))
      : [session]
  ), [sidebarTmux]);
  // Legacy diagnostic state retained for the explicit runtime terminal view.
  // Tasks are the user-facing session inventory, so the dashboard does not
  // poll or render a second Convex/tmux roster.
  const [sidebarConvexTmux, setSidebarConvexTmux] = useState<TmuxRunnerSessionRecord[]>([]);
  // Rows worth rendering: everything except the connected device's sessions
  // that are already shown live above (runner seats float to the top).
  const sidebarConvexRows = useMemo(() => {
    const connectedNames = new Set(connectedDevice ? sidebarTmux.map((t) => t.name) : []);
    const rows = sidebarConvexTmux.filter(
      (r) => !(r.deviceId === connectedDevice?.id && connectedNames.has(r.sessionName)),
    );
    const rank = (r: TmuxRunnerSessionRecord) =>
      (r.status === "open" && isRunnerSeat(r) ? 0 : r.status === "open" ? 1 : 2);
    return [...rows].sort((a, b) => rank(a) - rank(b) || b.lastSeenAt - a.lastSeenAt);
  }, [sidebarConvexTmux, sidebarTmux, connectedDevice]);
  const [remoteDesktopDevice, setRemoteDesktopDevice] = useState<Device | null>(null);
  const [activeTab, setActiveTab] = useState<DashboardTab>("devices");
  const [runtimeIntent, setRuntimeIntent] = useState<RuntimeLabIntent | null>(null);
  const [autoStart2faSetup, setAutoStart2faSetup] = useState(false);
  const [userMenuOpen, setUserMenuOpen] = useState(false);
  const [todoCount, setTodoCount] = useState(0);
  const [connectError, setConnectError] = useState<string | null>(null);
  const [connectDiagnostics, setConnectDiagnostics] = useState<ConnectAttemptDiagnostic[]>([]);
  const [connectedDeviceIds, setConnectedDeviceIds] = useState<string[]>([]);
  const [agentTaskSnapshots, setAgentTaskSnapshots] = useState<AgentTaskSnapshot[]>([]);
  const [copiedReauth, setCopiedReauth] = useState(false);
  const [reauthing, setReauthing] = useState(false);
  const [rescueQueuing, setRescueQueuing] = useState(false);
  const [reauthMessage, setReauthMessage] = useState<{ kind: "ok" | "err"; text: string } | null>(null);
  const [relayReady, setRelayReady] = useState(false);
  const [previewTargetId, setPreviewTargetId] = useState<string | null>(null);
  const deviceConnectQueueRef = useRef<Promise<void>>(Promise.resolve());
  const [preferredSurfaceProjectPath, setPreferredSurfaceProjectPath] = useState<string | null>(null);
  const [preferredWebviewMode, setPreferredWebviewMode] = useState<"mobile" | "web">("web");
  const [chatRunnerAuthModal, setChatRunnerAuthModal] = useState<string | null>(null);
  const [peerStates, setPeerStates] = useState<Record<string, { state: "online" | "stale" | "offline"; lastSeen?: string }>>({});
  const [probeStates, setProbeStates] = useState<Record<string, DeviceStatusProbe>>({});
  // Primary-device preference — the device auto-connect prefers when the
  // user has more than one online. Also mirrored onto mobile and CLI via
  // the /settings endpoint so every surface picks the same default.
  const [primaryDeviceId, setPrimaryDeviceId] = useState<string | null>(null);
  // Optional secondary slot — auto-connect fallback when primary is
  // offline. Loaded from /settings alongside primaryDeviceId.
  const [secondaryDeviceId, setSecondaryDeviceId] = useState<string | null>(null);
  // Machine-role split (runner/render). The favorite row drives
  // agentClient's deviceId-scoped routing: /tasks/* to the runner box,
  // /dev/* + previews to the render box, over the relay device path.
  const machineRoles = useMachineRoles(token);

  useEffect(() => {
    const isOnline = (id: string | null | undefined) => {
      if (!id) return false;
      const d = devices.find((device) => device.id === id);
      if (!d) return false;
      const probe = probeStates[id];
      const peer = peerStates[id];
      return probe?.ok === true || peer?.state === "online" || d.online === true;
    };
    const chooseRoleDevice = (primary?: string | null, secondary?: string | null) => {
      if (!primary) return secondary || null;
      if (isOnline(primary)) return primary;
      if (secondary && isOnline(secondary)) return secondary;
      // Match primary/secondary connection semantics: an explicit primary still
      // owns the role when neither candidate is known reachable. The request then
      // fails against the configured box instead of silently running on a third.
      return primary;
    };
    const row = machineRoles.favorite;
    agentClient.setMachineRoleRoutes({
      runnerDeviceId: chooseRoleDevice(row?.runnerDeviceId, row?.secondaryRunnerDeviceId),
      renderDeviceId: chooseRoleDevice(row?.renderDeviceId ?? row?.runnerDeviceId, row?.secondaryRenderDeviceId),
    });
  }, [
    devices,
    probeStates,
    peerStates,
    machineRoles.favorite?.runnerDeviceId,
    machineRoles.favorite?.secondaryRunnerDeviceId,
    machineRoles.favorite?.renderDeviceId,
    machineRoles.favorite?.secondaryRenderDeviceId,
  ]);

  useEffect(() => {
    if (typeof window === "undefined") return;
    const applyUrlTab = () => {
      const params = new URLSearchParams(window.location.search);
      const tab = params.get("tab");
      if (isDashboardTab(tab)) setActiveTab(tab);
    };
    applyUrlTab();
    window.addEventListener("popstate", applyUrlTab);
    const params = new URLSearchParams(window.location.search);
    if (params.get("setup2fa") === "1") setAutoStart2faSetup(true);
    return () => window.removeEventListener("popstate", applyUrlTab);
  }, []);

  useEffect(() => {
    if (typeof window === "undefined") return;
    const url = new URL(window.location.href);
    if (url.pathname !== "/dashboard") return;
    if (url.searchParams.get("tab") === activeTab) return;
    url.searchParams.set("tab", activeTab);
    window.history.replaceState(null, "", url.toString());
  }, [activeTab]);

  const repairRelay = useCallback(async () => {
    if (!token) throw new Error("Not signed in");
    const res = await fetch(`${CONVEX_URL}/settings/repair-relay`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
    });
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      throw new Error(body?.error || `repair HTTP ${res.status}`);
    }
    const body = await res.json();
    try {
      const r = await fetch(`${CONVEX_URL}/config`);
      let relays: any[] = [];
      if (r.ok) relays = (await r.json()).relayServers || [];
      const sr = await fetch(`${CONVEX_URL}/settings`, { headers: { Authorization: `Bearer ${token}` } });
      if (sr.ok) {
        const sd = await sr.json();
        const pw = sd.settings?.relayPassword || sd.relayPassword;
        if (pw) relays = relays.map((x: any) => ({ ...x, password: pw }));
      }
      if (relays.length > 0) {
        agentClient.setRelayServers(relays);
        agentClientPool.setRelayServersOnAll(relays);
      }
    } catch {
      // non-fatal — next connect will re-read
    }
    return { repaired: !!body.repaired, reason: body.reason || "" };
  }, [token]);

  const inputRef = useRef<HTMLTextAreaElement>(null);
  const outputRef = useRef<HTMLDivElement>(null);
  // Live-output stream health. Without this, a dropped SSE stream left the
  // transcript frozen on its last chunk with the composer still disabled —
  // indistinguishable from a task that had simply gone quiet.
  const [taskStreamHealth, setTaskStreamHealth] = useState<TaskStreamHealth>(null);
  const placementStatusSyncRef = useRef<Set<string>>(new Set());
  const relayReadyPromiseRef = useRef<Promise<void> | null>(null);
  const previousActiveTabRef = useRef<string | null>(null);
  const hydratedOpenCodePrefKeyRef = useRef("");
  // Per-device debounce of the proactive Open-Workspace re-auth.
  // The relay rate-limits agent recovery to ~5 s — without this map
  // a quick double-click on Open Workspace produces "too many
  // recovery attempts — wait 5 seconds" from the relay even on the
  // first real attempt.
  const lastAutoReauthRef = useRef<Map<string, number>>(new Map());
  // Per-device primary coding agent map. Shared with the Devices tab
  // hook (same Convex query, cached by Convex). Used to (a) pre-select
  // the chat tab's runner when a workspace opens, (b) decide which
  // runner the Hot Reload "Sign in & reconnect" CTA triggers.
  const {
    primaryRunnerByDevice,
    primaryModelByDevice,
    primaryReasoningEffortByDevice,
    primaryModeByDevice,
    primaryProviderByDevice,
    setPrimaryRunner,
  } = usePrimaryRunnerByDevice(token);
  // Pick the runner the user actually wants to use on this device.
  // Order:
  //   1. Explicit primary persisted to userSettings
  //   2. First runner the device has registered as authenticated
  //   3. null — the consumer falls back to the live runner list
  const connectedDevicePrimaryRunner = (() => {
    if (!connectedDevice) return null;
    // Always trust the explicit user-set primary first — it lives in
    // Convex (`userSettings.primaryRunnerByDevice`) and survives
    // disconnects/reconnects. Only fall back to the agent's reported
    // runner list when no preference is set.
    const explicit = primaryRunnerByDevice[connectedDevice.id];
    if (explicit) return explicit;
    const runners = (connectedDevice.runners || []) as Array<{ runnerId?: string; authConfigured?: boolean }>;
    const ready = runners.find((r) => r?.authConfigured);
    if (ready?.runnerId) return ready.runnerId;
    return null;
  })();
  // Mirror mobile's trust model: prefer the live `/agent/runners`
  // response, but if it's empty (silent fetch error, brief 401 during
  // token refresh, etc.) fall back to the Convex heartbeat snapshot in
  // `connectedDevice.runners`. The agent reports both sets via the same
  // `osexec.LookPath`, so when the heartbeat says OpenCode is there the
  // live answer would have agreed if we'd fetched it cleanly. Without
  // this, web flags "No AI runner installed" while mobile (which reads
  // the heartbeat snapshot) happily executes the same task.
  const chatRunnerChoices = useMemo<Runner[]>(() => {
    const live = runners.filter((runner) => runner.installed && RUNNER_WHITELIST_SET.has(runner.id));
    if (live.length > 0) return live;
    // With a machine-role split active, tasks run on the RUNNER box — read
    // that box's heartbeat snapshot, not the connected (render) box's.
    const runnerBoxId = machineRoles.favorite?.runnerDeviceId;
    const runnerBox = runnerBoxId && runnerBoxId !== connectedDevice?.id
      ? devices.find((d) => d.id === runnerBoxId) || connectedDevice
      : connectedDevice;
    const cached = (runnerBox?.runners || []) as Array<{ runnerId?: string; authConfigured?: boolean; needsAuth?: boolean; status?: string }>;
    const seen = new Set<string>();
    const synthetic: Runner[] = [];
    for (const row of cached) {
      const id = row?.runnerId ? String(row.runnerId) : "";
      if (!id || !RUNNER_WHITELIST_SET.has(id) || seen.has(id)) continue;
      seen.add(id);
      synthetic.push({
        id,
        name: id,
        installed: true,
        active: false,
        // `ready` is intentionally omitted (treated as undefined ≈ true)
        // so handleSend's `runnerAuthIssue` check doesn't block — mobile
        // proves the runner works; if it doesn't, the task surface will
        // bubble the real error back.
        supportsBrowserAuth: id === "claude" || id === "codex",
        supportsModelSelection: false,
        models: [],
      });
    }
    return synthetic;
  }, [runners, connectedDevice, devices, machineRoles.favorite?.runnerDeviceId]);

  // When the primary runner changes (broadcast from another tab/view),
  // also kick a device refresh so the sidebar's `runners` array
  // (authConfigured / needsAuth flags) reflects the just-tested runner
  // — without this the sidebar's badge stayed "sign in" even after
  // Devices tab proved the runner authed cleanly.
  useEffect(() => {
    if (typeof window === "undefined") return;
    const onChange = () => refreshDevices();
    window.addEventListener("yaver:primary-runner-changed", onChange);
    return () => window.removeEventListener("yaver:primary-runner-changed", onChange);
  }, [refreshDevices]);

  const probedForCurrentTabOpenRef = useRef(false);

  // Sidebar Vibing list folds by default; the header keeps the count.
  const [sidebarVibingOpen, setSidebarVibingOpen] = useState(false);

  const isConnected = connState === "connected";

  const taskClientFor = useCallback((task: Pick<Task, "deviceId"> | null | undefined): AgentClient => {
    const taskDeviceId = String(task?.deviceId || "").trim();
    if (!taskDeviceId) return agentClient;
    if (taskDeviceId === connectedDevice?.id) return agentClient;
    // Never fall through to whichever box happens to be focused: task ids are
    // scoped to their owner. A disconnected pooled client fails honestly and
    // the caller can connect that box; it cannot mutate another machine.
    return agentClientPool.get(taskDeviceId);
  }, [connectedDevice?.id]);

  const sameScopedTask = useCallback(
    (left: Pick<Task, "id" | "deviceId"> | null | undefined, right: Pick<Task, "id" | "deviceId"> | null | undefined) =>
      Boolean(left && right && left.id === right.id && (left.deviceId || "") === (right.deviceId || "")),
    [],
  );

  useEffect(() => {
    const sync = () => setConnectedDeviceIds(agentClientPool.connectedDeviceIds());
    sync();
    return agentClientPool.subscribe(sync);
  }, []);

  // Stay-in-chat rule (2026-08-13, owner directive): a connected box used to
  // auto-open the Vibing tab on landing ("came to work → open Vibing"). The
  // dashboard NEVER navigates itself to a different tab now — a user stays on
  // the tab they opened (Devices/chat), and Vibing is reached by tapping the
  // tab, not by connection state. Explicit ?tab= deep links still work.

  // Restore the last chat-composer choices from Convex on connect — the SAME
  // defaultRuntimeProjectByDevice / mcpServersByDevice rows mobile writes, so
  // a project/MCP set picked on the phone carries into the web chat and vice
  // versa (2026-08-10). Project match is by name/remote against the live
  // /projects list (Convex rows carry no absolute path — see
  // web/lib/runtimeProjectSettings.ts). Never blocks connection: a failed
  // settings read keeps the previous defaults.
  useEffect(() => {
    if (!isConnected || !token || !connectedDevice?.id || (!useLatestProject && !useLatestMCP)) return;
    const deviceId = connectedDevice.id;
    let cancelled = false;
    const restore = async () => {
      if (useLatestProject) try {
        const pref = await loadLastProjectFromConvex(CONVEX_URL, token, deviceId);
        if (cancelled || !pref?.projectName) return;
        // Match the remembered project against the fresh /projects list.
        const proj = chatProjects.find((p) =>
          p.name === pref.projectName
          || (pref.gitRemote && p.gitRemote && p.gitRemote === pref.gitRemote));
        if (proj) setPreferredSurfaceProjectPath(proj.path);
      } catch {}
      if (useLatestMCP) try {
        const mcpPref = await loadMCPServersFromConvex(CONVEX_URL, token, deviceId);
        if (cancelled || !mcpPref) return;
        if (Array.isArray(mcpPref.mcpServers)) {
          setSelectedMcpServers(mcpPref.mcpServers.filter((name) =>
            mcpServers.some((s) => s.name === name)));
        }
        if (typeof mcpPref.includeYaverMcp === "boolean") {
          setIncludeYaverMcp(mcpPref.includeYaverMcp);
        }
      } catch {}
    };
    void restore();
    return () => { cancelled = true; };
    // chatProjects/mcpServers intentionally NOT in deps — restoring on the
    // first connect after the list lands is what we want; re-restoring on
    // every list refresh would overwrite an in-session pick.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isConnected, token, connectedDevice?.id, useLatestProject, useLatestMCP]);

  // Cross-machine surface catalogs (2026-08-13): one /settings fetch that
  // answers "which MCP server / which git project lives on which machine"
  // for the chat composer's other-machine chips. Reloads when the fleet
  // changes (a box that comes online mid-session should appear). Advisory:
  // a failed read keeps the previous maps, and empty maps just mean the
  // "other machines" groups don't render.
  const fleetSignature = useMemo(
    () => devices.map((d) => d.id).sort().join(","),
    [devices],
  );
  useEffect(() => {
    if (!token) return;
    let cancelled = false;
    const pull = async () => {
      const catalogs = await loadSurfaceCatalogsFromConvex(CONVEX_URL, token);
      if (cancelled) return;
      setMcpCatalogByDevice(catalogs.mcpByDevice);
      setProjectCatalogByDevice(catalogs.projectsByDevice);
    };
    void pull();
    return () => { cancelled = true; };
  }, [token, fleetSignature]);

  useEffect(() => {
    if (isLoading) return;
    if (!isAuthenticated) return;
    // `devices.length === 0` is ALSO the pre-fetch state, so this used to bounce
    // an existing user with machines to the survey before the first fetch
    // landed. Wait for the fetch to settle, and never redirect on an error —
    // a failed load is not evidence of a new user. See DEVICE_TRUTH.md F20.
    if (devicesLoading || devicesError) return;
    if (user?.surveyCompleted === false && devices.length === 0) {
      router.replace("/survey");
    }
  }, [devices.length, devicesLoading, devicesError, isAuthenticated, isLoading, router, user?.surveyCompleted]);

  // Once per page life: the session-death banner must not re-announce on every
  // topology-refresh rung of the reconnect ladder.
  const sessionDeathAnnouncedRef = useRef(false);

  useEffect(() => {
    let cancelled = false;
    let resolve: () => void;
    relayReadyPromiseRef.current = new Promise<void>(r => { resolve = r; });

    const refreshRelayTopology = async (opts?: { syncPrimary?: boolean }) => {
      // Fetch platform relay servers (already includes password)
      const r = await fetch(`${CONVEX_URL}/config`);
      let relays: any[] = [];
      if (r.ok) { const d = await r.json(); relays = d.relayServers || []; }

      // Fetch user settings to get relay password override + primary device
      if (token) {
        try {
          const sr = await fetch(`${CONVEX_URL}/settings`, { headers: { Authorization: `Bearer ${token}` } });
          if (sr.ok) {
            const sd = await sr.json();
            const pw = sd.settings?.relayPassword || sd.relayPassword;
            if (pw) { relays = relays.map((r: any) => ({ ...r, password: pw })); }
            if (!cancelled && opts?.syncPrimary) {
              setPrimaryDeviceId(sd.settings?.primaryDeviceId ?? null);
              setSecondaryDeviceId(sd.settings?.secondaryDeviceId ?? null);
            }
          } else if ((sr.status === 401 || sr.status === 403) && !cancelled) {
            // SESSION DEATH MUST NAME ITSELF (incident 2026-07-28): a token is
            // present but Convex rejects it, so the relays below get NO
            // per-user password, every relay probe 401s, and — before this
            // guard — the UI blamed the AGENT ("connection was rejected")
            // while a fresh sign-in fixed everything. Announce once through
            // the existing RawFailureBanner seam (dismissible, single).
            if (!sessionDeathAnnouncedRef.current) {
              sessionDeathAnnouncedRef.current = true;
              announceRawFailure(
                new SessionDeathError(`GET /settings returned HTTP ${sr.status} with a token present`),
              );
            }
          }
        } catch {}
      }

      if (!cancelled && relays.length > 0) {
        agentClient.setRelayServers(relays);
        agentClientPool.setRelayServersOnAll(relays);
      }
    };

    (async () => {
      try {
        await refreshRelayTopology({ syncPrimary: true });
      } catch {}
      if (!cancelled) setRelayReady(true);
      resolve!();
    })();
    // Topology-refresh rung for the reconnect ladder (audit gap T2, mobile
    // parity): every 3rd failed attempt the AgentClient asks us to re-pull
    // the relay list + passwords so a relay restart / password rotation
    // doesn't strand the ladder on the coordinates it was born with.
    const refreshTopologyHook = () => refreshRelayTopology();
    agentClient.setTopologyRefreshHook(refreshTopologyHook);
    agentClientPool.setTopologyRefreshHook(refreshTopologyHook);
    return () => {
      cancelled = true;
      agentClient.setTopologyRefreshHook(null);
      agentClientPool.setTopologyRefreshHook(null);
    };
  }, [token]);

  useEffect(() => {
    if (typeof window === "undefined") return;
    const saved = window.localStorage.getItem("yaver.previewTargetId");
    if (saved) setPreviewTargetId(saved);
  }, []);

  useEffect(() => {
    if (typeof window === "undefined") return;
    if (previewTargetId) window.localStorage.setItem("yaver.previewTargetId", previewTargetId);
    else window.localStorage.removeItem("yaver.previewTargetId");
  }, [previewTargetId]);

  useEffect(() => {
    if (!isConnected) return;
    let cancelled = false;
    (async () => {
      try {
        const target = await agentClient.getDevServerTarget();
        if (!cancelled) setPreviewTargetId(target?.targetDeviceId || null);
      } catch {}
    })();
    return () => { cancelled = true; };
  }, [isConnected, connectedDevice?.id]);

  useEffect(() => {
    if (!isConnected) {
      setPeerStates({});
      return;
    }
    let cancelled = false;
    const refreshPeerStates = async () => {
      try {
        const peers = await agentClient.machinePeers();
        if (cancelled) return;
        const next: Record<string, { state: "online" | "stale" | "offline"; lastSeen?: string }> = {};
        for (const peer of peers) {
          if (!peer?.deviceId) continue;
          next[peer.deviceId] = { state: peer.state, lastSeen: peer.lastSeen };
        }
        setPeerStates(next);
      } catch {
        if (!cancelled) setPeerStates({});
      }
    };
    void refreshPeerStates();
    const interval = setInterval(refreshPeerStates, 5000);
    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, [isConnected, connectedDevice?.id]);

  useEffect(() => {
    if (activeTab !== "devices") {
      probedForCurrentTabOpenRef.current = false;
      previousActiveTabRef.current = activeTab;
      return;
    }
    previousActiveTabRef.current = activeTab;
    if (probedForCurrentTabOpenRef.current) return;
    // Wait until relay servers are loaded — without them probeDeviceStatus
    // skips the relay branch and every cross-network device (remote VPSes, etc.)
    // gets falsely marked Unreachable. Re-runs when relayReady flips.
    if (!token || devices.length === 0 || !relayReady) {
      return;
    }
    probedForCurrentTabOpenRef.current = true;
    let cancelled = false;
    // FAN-OUT. Which machines get a verified path, and in what order, is the
    // account's seeded primary/secondary — not "whichever card you opened".
    // Default is every machine; connectionMode "single" downgrades to one. A
    // metered account is bounded and told what was withheld, because a
    // silently shortened list is the "reachability is only verified for
    // machines you open" problem wearing a policy as an excuse.
    const fanout = planConnectionFanout({
      devices: devices.map((d) => ({ deviceId: d.id, name: d.name, isOnline: d.online })),
      seed: machineRoles.favorite,
      mode: machineRoles.connectionMode,
      isOwner: user?.isOwner === true,
    });
    const byId = new Map(devices.map((d) => [d.id, d]));
    const planned = fanout.targets
      .map((t) => byId.get(t.deviceId))
      .filter((d): d is (typeof devices)[number] => Boolean(d));
    const refreshProbes = async () => {
      const nextEntries = await Promise.all(
        planned.map(async (device) => {
          try {
            const probe = await agentClient.probeDeviceStatus({
              host: device.host,
              port: device.port,
              token,
              deviceId: device.id,
              // ONE shared predicate (lib/endpoints.ts) filters known-dead
              // endpoints — <uuid>.yaver.io has no DNS, *.dev.yaver.io has no
              // cert — before they spam the console with NXDOMAIN/CORS errors.
              tunnelUrls: usableTunnelUrls(device.publicEndpoints, device.tunnelUrl),
            });
            return [device.id, probe] as const;
          } catch (error: any) {
            return [device.id, {
              ok: false,
              checkedAt: new Date().toISOString(),
              error: error?.message || "Probe failed",
              diagnostics: [],
            } satisfies DeviceStatusProbe] as const;
          }
        }),
      );
      if (cancelled) return;
      // Deferred machines get an explicit state rather than being absent: "we
      // did not check this one, and here is why" is a different fact from "we
      // checked and it failed", and only one of them is true.
      const deferredEntries = fanout.deferred.map((d) => [d.deviceId, {
        ok: false,
        checkedAt: new Date().toISOString(),
        error: d.reason,
        diagnostics: [],
      } satisfies DeviceStatusProbe] as const);
      setProbeStates(Object.fromEntries([...nextEntries, ...deferredEntries]));
    };
    void refreshProbes();
    return () => {
      cancelled = true;
    };
  }, [activeTab, token, devices, relayReady, machineRoles.favorite, machineRoles.connectionMode, user?.isOwner]);

  useEffect(() => {
    const u = agentClient.on("connectionState", setConnState);
    setConnState(agentClient.connectionState);
    return u;
  }, []);

  // Tracks which task is currently being live-streamed via SSE.
  // While SSE owns the wheel, the 3-second polling loop in agent-
  // client must NOT also push lines for that task or every chunk
  // double-renders. Polling stays alive for non-active tasks (the
  // sidebar list refresh) and as a backstop after SSE closes.
  const sseActiveTaskRef = useRef<string | null>(null);

  const appendAssistantChunk = useCallback((tid: string, chunk: string) => {
    if (!chunk) return;
    setActiveTask(at => {
      if (!at || tid !== at.id) return at;
      // Stream chunks may carry multiple lines; normalize so
      // outputLines stays one-line-per-entry like the polling path.
      const lines = chunk.split("\n").filter(l => l.length > 0 || chunk.indexOf("\n\n") >= 0);
      setOutputLines(p => [...p, ...lines]);
      setChatMsgs(prev => {
        const next = prev.slice();
        const last = next[next.length - 1];
        if (last && last.role === "assistant") {
          // Capped at the write: this entry absorbs every chunk of the task,
          // so uncapped it grows to the whole session transcript — and the
          // bubble re-strips + re-parses it on every chunk.
          next[next.length - 1] = {
            role: "assistant",
            text: capStreamText(last.text ? last.text + chunk : chunk),
          };
        } else {
          next.push({ role: "assistant", text: capStreamText(chunk) });
        }
        return next;
      });
      return at;
    });
  }, []);

  useEffect(() => {
    const u = agentClient.on("output", (tid, line) => {
      // SSE is faster + survives relay flakes (the 3s poller hits
      // /tasks?limit=5 and on relay 502 falls into 30s reconnect
      // backoff, which is exactly the 2-minute-stuck-spinner bug
      // users were hitting in the chat). When SSE owns the active
      // task, ignore polled emissions for it.
      if (sseActiveTaskRef.current && sseActiveTaskRef.current === tid) return;
      appendAssistantChunk(tid, line);
    });
    return u;
  }, [appendAssistantChunk]);

  // The console buffer is per-TASK: switching tasks must never leak one
  // task's raw tail into another's (mobile keys the reset the same way).
  // Declared BEFORE the stream effect so the reset lands first, then the
  // stream's raw_replay reseeds the fresh task.
  useEffect(() => {
    setRawOutput([]);
    setRawSince(0);
  }, [activeTask?.id]);

  // Live SSE for the active running task. The 3s poller is fine for
  // background sync but caps tail latency at the polling cadence
  // (and stalls during relay outages). Subscribing to /tasks/<id>/
  // output via SSE makes the chat bubble update token-by-token —
  // matches what VibeCodingView already does.
  useEffect(() => {
    if (!activeTask) {
      sseActiveTaskRef.current = null;
      return;
    }
    const tid = activeTask.id;
    const status = String(activeTask.status || "");
    const runnerCoding = status === "running" || status === "queued";
    // Terminal tasks get NO live ladder and NO health banner — but the
    // console still needs its raw tail, seeded in one raw_replay frame
    // (rawSince=0 → the agent's full retained tail; the stream closes on
    // `done`, and the recovery wrapper classifies that as idle — no
    // reattach, no health noise).
    if (!runnerCoding) {
      sseActiveTaskRef.current = null;
      setTaskStreamHealth(null);
    } else {
      sseActiveTaskRef.current = tid;
    }
    // Recovery-wrapped — see lib/taskStreamWithRecovery.ts. Without onEnd a
    // severed stream ends in silence and the transcript freezes mid-answer.
    const taskClient = taskClientFor(activeTask);
    const stop = streamTaskOutputWithRecovery(
      taskClient,
      tid,
      (chunk) => {
        // Terminal replay carries the whole groomed transcript — the raw
        // console is the view now, so only a coding task feeds the bubbles
        // (which stay as the no-raw-lane fallback + the agent-question card).
        if (runnerCoding) appendAssistantChunk(tid, chunk);
      },
      (evt) => {
        if (!evt || typeof evt.type !== "string") return;
        if (isTaskPresentationEvent(evt)) {
          setActiveTask((current) => {
            if (!current || current.id !== tid) return current;
            const presentation = reduceTaskPresentation(current.presentation ?? [], evt);
            return { ...current, presentation };
          });
          setTasks((previous) => previous.map((task) => task.id === tid
            ? { ...task, presentation: reduceTaskPresentation(task.presentation ?? [], evt) }
            : task));
        } else if (evt.type === "agent_question" && evt.question) {
          const q = evt.question as {
            id: string;
            taskId: string;
            prompt: string;
            kind: "text" | "choice" | "secret";
            choices?: string[];
            vaultHint?: string;
          };
          setAgentQuestion(q);
          setAgentAnswerText("");
        } else if (evt.type === "agent_answered" || evt.type === "agent_question_cancelled") {
          const qid = (evt as { questionId?: string }).questionId;
          setAgentQuestion((cur) => (cur && (!qid || cur.id === qid) ? null : cur));
        } else if (evt.type === "done") {
          // Task finished: any open question can no longer be
          // consumed (registry was cancelled by StopTask); close
          // the card.
          setAgentQuestion(null);
        }
      },
      {
        onHealth: runnerCoding ? setTaskStreamHealth : undefined,
        // The raw console lane (ANSI + TUI, ungroomed) is the task view's
        // PRIMARY output — the same bytes mobile's LiveConsoleSection and
        // tvOS render, for EVERY runner. Append into a bounded buffer
        // (2000 lines so a long turn cannot balloon the DOM). raw_replay
        // is the reattach snapshot — replace, not append. The byte cursor
        // rides rawSince so a stream reattach resumes where the console
        // left off.
        rawSince: runnerCoding ? rawSince : 0,
        onRaw: (ev) => {
          if (ev.type === "raw_replay") {
            setRawOutput(ev.text ? [ev.text] : []);
          } else if (ev.text) {
            setRawOutput((p) => {
              const next = [...p, ev.text!];
              return next.length > 2000 ? next.slice(next.length - 2000) : next;
            });
          }
          if (typeof ev.offset === "number") setRawSince(ev.offset);
        },
      },
    );
    // Late-join replay: if the agent already asked while no client
    // was subscribed, the SSE writer replays on connect — but also
    // poll once so the card shows the moment the user opens the task
    // tab without waiting for the next SSE flush.
    if (runnerCoding) {
      void taskClient.getPendingTaskQuestion(tid).then((q) => {
        if (q && q.taskId === tid) {
          setAgentQuestion(q);
          setAgentAnswerText("");
        }
      });
    }
    return () => {
      stop();
      setTaskStreamHealth(null);
      if (sseActiveTaskRef.current === tid) sseActiveTaskRef.current = null;
    };
  }, [activeTask?.id, activeTask?.status, activeTask?.deviceId, appendAssistantChunk, rawSince, taskClientFor]);

  useEffect(() => {
    if (outputRef.current && followOutput) outputRef.current.scrollTop = outputRef.current.scrollHeight;
  }, [outputLines, chatMsgs, rawOutput, followOutput]);

  // Reconcile the activeTask's status from the polled tasks list. With-
  // out this, activeTask.status stays at "running" forever even after
  // the task completes — the SSE handler only appends output chunks,
  // it doesn't mutate status. Symptom: after the assistant's reply
  // streams in, the composer's "Update task" button stays disabled
  // (taskRunning is computed from activeTask.status === "running")
  // and the user can't type a follow-up. This effect re-finds the
  // active task in the polled list and syncs status / runnerId /
  // resultText / costUsd / turns whenever anything changed.
  //
  // BUG FIX (2026-08-09, e2e closed loop): this compared the RAW list row
  // against activeTask, then stored FALLBACK values (placementId ||
  // old placementId). The list endpoint strips placement fields, so the
  // guard saw `undefined !== "abc"` forever while the set stored "abc" —
  // setActiveTask every render → React "Maximum update depth exceeded"
  // (thrown live in the dashboard during a task run). Compute the next
  // value FIRST and compare the value that will actually be stored.
  useEffect(() => {
    if (!activeTask) return;
    const fresh = tasks.find((t) => sameScopedTask(t, activeTask));
    if (!fresh) return;
    const next: typeof activeTask = {
      ...fresh,
      placementId: fresh.placementId || activeTask.placementId,
      placementLane: fresh.placementLane || activeTask.placementLane,
      placementReason: fresh.placementReason || activeTask.placementReason,
      placementCreditLabel: fresh.placementCreditLabel || activeTask.placementCreditLabel,
      pendingCloudBlockedAction: fresh.pendingCloudBlockedAction || activeTask.pendingCloudBlockedAction,
      pendingCloudBlockedReason: fresh.pendingCloudBlockedReason || activeTask.pendingCloudBlockedReason,
      pendingCloudExpiresAt: fresh.pendingCloudExpiresAt || activeTask.pendingCloudExpiresAt,
      pendingCloudTargetDeviceId: fresh.pendingCloudTargetDeviceId || activeTask.pendingCloudTargetDeviceId,
    };
    if (
      next.status !== activeTask.status ||
      next.resultText !== activeTask.resultText ||
      next.presentation?.at(-1)?.updatedAt !== activeTask.presentation?.at(-1)?.updatedAt ||
      next.costUsd !== activeTask.costUsd ||
      next.turns?.length !== activeTask.turns?.length ||
      next.placementId !== activeTask.placementId ||
      // Proof/video lifecycle transitions (capturing→ready/failed, clip id
      // arriving, stale flip) must re-render the TaskProofCard even when
      // status/resultText are already settled.
      next.proofStatus !== activeTask.proofStatus ||
      next.videoStatus !== activeTask.videoStatus ||
      next.videoClipId !== activeTask.videoClipId ||
      next.commitSha !== activeTask.commitSha
    ) {
      setActiveTask(next);
    }
  }, [tasks, activeTask, sameScopedTask]);

  // Project the semantic assistant lane into chat after state commits. Keeping
  // this outside the setActiveTask updater avoids a nested state update during
  // React reconciliation (which can be invoked twice in Strict Mode).
  useEffect(() => {
    const assistant = friendlyTaskPresentation(activeTask?.presentation)
      .filter((message) => message.kind === "message" && message.role === "assistant" && message.text.trim())
      .at(-1)?.text;
    if (!assistant || !activeTask || (activeTask.status !== "running" && activeTask.status !== "queued")) return;
    setChatMsgs((previous) => {
      const next = [...previous];
      const last = next.at(-1);
      if (last?.role === "assistant") {
        if (last.text === assistant) return previous;
        next[next.length - 1] = { ...last, text: assistant };
      } else {
        next.push({ role: "assistant", text: assistant });
      }
      return next;
    });
  }, [activeTask?.id, activeTask?.status, activeTask?.presentation]);

  useEffect(() => {
    if (!token || !activeTask?.placementId) return;
    const nextStatus =
      activeTask.status === "completed"
        ? "completed"
        : activeTask.status === "failed" || activeTask.status === "stopped"
          ? "failed"
          : activeTask.status === "queued"
            ? "queued"
            : "running";
    const key = `${activeTask.placementId}:${nextStatus}`;
    if (placementStatusSyncRef.current.has(key)) return;
    placementStatusSyncRef.current.add(key);
    void markTaskPlacementStatus(token, activeTask.placementId, nextStatus).catch(() => {
      placementStatusSyncRef.current.delete(key);
    });
  }, [token, activeTask?.placementId, activeTask?.status]);

  // Electron gets an explicit task event instead of having to infer state by
  // scraping rendered text. The bridge is absent in normal browsers, so this
  // is a no-op there and the shared task protocol remains unchanged.
  useEffect(() => {
    if (!activeTask) return;
    if (!["ready", "completed", "review", "failed", "stopped"].includes(activeTask.status)) return;
    const bridge = (window as Window & {
      yaver?: { taskStatus?: (payload: { taskId: string; kind: string; title: string }) => boolean };
    }).yaver;
    bridge?.taskStatus?.({
      taskId: activeTask.id,
      kind: activeTask.status,
      title: displayTaskTitle(activeTask.title || ""),
    });
  }, [activeTask?.id, activeTask?.status, activeTask?.title]);

  // Keep selectedRunner valid: prefer the connected device's chosen
  // primary runner, then the agent's default/active runner, then a
  // sensible installed fallback. Clears when the picker's choice
  // disappears (e.g. on reconnect to a different host where the runner
  // isn't installed).
  useEffect(() => {
    const installed = chatRunnerChoices;
    if (installed.length === 0) { setSelectedRunner(""); return; }
    const explicitRunner = connectedDevice ? primaryRunnerByDevice[connectedDevice.id] : "";
    if (explicitRunner && installed.some((runner) => runner.id === explicitRunner) && selectedRunner !== explicitRunner) {
      setSelectedRunner(explicitRunner);
      return;
    }
    if (selectedRunner && installed.some(r => r.id === selectedRunner)) return;
    const ready = installed.filter(r => r.ready !== false);
    const seedIds = ready.length > 0 ? ready.map((runner) => runner.id) : installed.map((runner) => runner.id);
    const seededRunner = connectedDevice
      ? preferredDefaultRunnerForDevice(
          connectedDevice,
          user?.email,
          seedIds,
        )
      : null;
    const preferred =
      ready.find(r => r.id === connectedDevicePrimaryRunner) ||
      installed.find(r => r.id === connectedDevicePrimaryRunner) ||
      ready.find(r => r.id === seededRunner) ||
      (ready.length === 0 ? installed.find(r => r.id === seededRunner) : undefined) ||
      ready.find(r => r.isDefault || r.active) ||
      ready.find(r => r.id === "claude") ||
      ready.find(r => r.id === "opencode") ||
      ready.find(r => r.id === "codex") ||
      installed.find(r => r.isDefault || r.active) ||
      installed[0];
    setSelectedRunner(preferred.id);
  }, [connectedDevice, connectedDevicePrimaryRunner, runners, selectedRunner, user?.email]);

  // Provider metadata comes from the selected machine's OpenCode catalog.
  // The index is compact; models are fetched only for the active provider so
  // large models.dev catalogs remain cheap over relay links and on 4 GB boxes.
  useEffect(() => {
    if (selectedRunner !== "opencode") {
      setOpenCodeCatalogue([]);
      return;
    }
    let cancelled = false;
    const fallback = openCodeProvidersFromRunner(runners.find((row) => row.id === "opencode"));
    if (!isConnected) {
      setOpenCodeCatalogue(fallback);
      return;
    }
    void agentClient.getOpenCodeCatalog().then((rows) => {
      if (cancelled) return;
      const catalogue = rows.length > 0 ? rows.map(openCodeProviderFromAgent) : fallback;
      setOpenCodeCatalogue(catalogue);
      const modelProvider = selectedModel.includes("/") ? selectedModel.slice(0, selectedModel.indexOf("/")) : "";
      const preferred = [opencodeProvider, modelProvider, "deepseek"]
        .find((id) => id && catalogue.some((provider) => provider.id === id));
      if (!opencodeProvider || !catalogue.some((provider) => provider.id === opencodeProvider)) {
        setOpencodeProvider(preferred || catalogue[0]?.id || "");
      }
    }).catch(() => {
      if (!cancelled) setOpenCodeCatalogue(fallback);
    });
    return () => { cancelled = true; };
  }, [selectedRunner, isConnected, runners, connectedDevice?.id]);

  useEffect(() => {
    if (selectedRunner !== "opencode" || !isConnected || !opencodeProvider) return;
    let cancelled = false;
    void agentClient.getOpenCodeCatalog(opencodeProvider).then((rows) => {
      if (cancelled || rows.length === 0) return;
      const detail = openCodeProviderFromAgent(rows[0]);
      setOpenCodeCatalogue((current) => {
        const without = current.filter((provider) => provider.id !== detail.id);
        return [...without, detail].sort((a, b) => a.label.localeCompare(b.label));
      });
    });
    return () => { cancelled = true; };
  }, [selectedRunner, isConnected, opencodeProvider, connectedDevice?.id]);

  useEffect(() => {
    if (selectedRunner !== "opencode") return;
    const provider = openCodeCatalogue.find((row) => row.id === opencodeProvider);
    if (!provider || provider.models.length === 0) return;
    const valid = provider.models.some((model) => `${provider.id}/${model.id}` === selectedModel);
    if (valid) return;
    const first = provider.models[0];
    setSelectedModel(`${provider.id}/${first.id}`);
  }, [selectedRunner, openCodeCatalogue, opencodeProvider, selectedModel]);

  useEffect(() => {
    if (selectedRunner !== "opencode") {
      hydratedOpenCodePrefKeyRef.current = "";
      return;
    }
    const deviceId = connectedDevice?.id || "";
    const preferredMode = deviceId ? primaryModeByDevice[deviceId] || "" : "";
    const explicitModel = deviceId ? primaryModelByDevice[deviceId] || "" : "";
    const derivedProvider =
      explicitModel && explicitModel.includes("/")
        ? explicitModel.slice(0, explicitModel.indexOf("/"))
        : "";
    const preferredProvider = (deviceId ? primaryProviderByDevice[deviceId] || "" : "") || derivedProvider;
    const hydrationKey = `${deviceId}|${preferredProvider}|${explicitModel}|${preferredMode}`;
    if (hydratedOpenCodePrefKeyRef.current === hydrationKey) return;
    hydratedOpenCodePrefKeyRef.current = hydrationKey;
    if (preferredMode !== selectedOpenCodeMode) {
      setSelectedOpenCodeMode(preferredMode);
    }
    if (preferredProvider && preferredProvider !== opencodeProvider) {
      setOpencodeProvider(preferredProvider);
      setOpencodeChangingKey(false);
    }
    if (explicitModel && explicitModel !== selectedModel) {
      setSelectedModel(explicitModel);
    }
  }, [selectedRunner, connectedDevice?.id, primaryModeByDevice, primaryProviderByDevice, primaryModelByDevice, selectedOpenCodeMode, opencodeProvider, selectedModel]);

  useEffect(() => {
    // OpenCode owns its own provider/model selection (see the inline
    // BYOK picker below). Don't let the generic model-syncer reset it.
    if (selectedRunner === "opencode") return;
    const runner = runners.find((r) => r.id === selectedRunner);
    const models = runnerModelOptions(runner, selectedRunner);
    const explicitModel = connectedDevice ? primaryModelByDevice[connectedDevice.id] : "";
    if (explicitModel && models.some((model) => model.id === explicitModel) && selectedModel !== explicitModel) {
      setSelectedModel(explicitModel);
      return;
    }
    if (selectedModel && models.some((m) => m.id === selectedModel)) return;
    const seededModel = connectedDevice
      ? preferredDefaultModelForRunner(selectedRunner, connectedDevice, user?.email)
      : null;
    const preferredModel =
      (explicitModel && models.some((m) => m.id === explicitModel) ? explicitModel : "") ||
      models.find((m) => m.isDefault)?.id ||
      (seededModel && models.some((m) => m.id === seededModel) ? seededModel : "") ||
      models[0]?.id ||
      "";
    setSelectedModel(preferredModel);
  }, [connectedDevice, primaryModelByDevice, runners, selectedRunner, selectedModel, user?.email]);

  const reauthDevice = async (d: Device) => {
    if (!token) return;
    const lifecycle = deriveDeviceLifecycleState(d);
    setReauthBusy(d.id);
    setReauthMsg(null);
    try {
      // For boxes in bootstrap mode (needsAuth=true), the agent
      // doesn't have an auth_token so /auth/recover would 404.
      // Use the new owner-claim flow instead — the agent verifies
      // ownership via Convex round-trip and splices our bearer into
      // the active pair session. One round-trip, no URL paste.
      if (lifecycle === "bootstrap") {
        const claim = await agentClient.ownerClaimDevice(d.id, {
          host: d.host,
          port: d.port,
          tunnelUrl: d.tunnelUrl,
          publicEndpoints: d.publicEndpoints,
          lanIps: d.localIps,
        });
        if (claim.ok) {
          setReauthMsg({
            deviceId: d.id,
            ok: true,
            text: `Paired with ${claim.host || d.name}. Refreshing…`,
          });
          setTimeout(refreshDevices, 1500);
          return;
        }
        // Owner-claim failed — fall through to the legacy reauth
        // path so any non-bootstrap fallback still has a shot.
        setReauthMsg({
          deviceId: d.id,
          ok: false,
          text: `Pair failed: ${claim.error}. Trying recover…`,
        });
      }
      const r = await agentClient.reauthAgent({
        deviceId: d.id,
        hostSessionToken: token,
      });
      if (r.ok) {
        setReauthMsg({
          deviceId: d.id,
          ok: true,
          text: `Re-auth succeeded via ${r.via} (${r.mode}). Refreshing…`,
        });
        // Agent's heartbeat trigger clears needsAuth on Convex side
        // within ~100 ms (auth_recover.go calls TriggerHeartbeat). The
        // refresh below picks it up.
        setTimeout(refreshDevices, 1200);
        // If this re-auth was for the active workspace, every authed
        // call we made before (runners, projects, /info) was 401-ing
        // because the agent could not validate our bearer through
        // Convex. Refetch them now so chat shows the real runner list,
        // /projects shows the real projects, and the "no runner
        // installed" / "no projects detected" copy disappears without
        // forcing the user to reconnect.
        if (connectedDevice?.id === d.id) {
          setTimeout(async () => {
            try { setRunners(await agentClient.getRunners()); } catch {}
            try { setMcpServers((await agentClient.listMcpServers()).filter((server) => server.enabled)); } catch {}
            try { setAgentInfo(await agentClient.getInfo()); } catch {}
          }, 1500);
        }
        setTimeout(() => setReauthMsg((m) => (m?.deviceId === d.id ? null : m)), 6000);
      } else {
        const diagSummary = r.diagnostics
          .map((dx) => `${dx.path}/${dx.step}: ${dx.ok ? "ok" : dx.error || "fail"}`)
          .join(" · ");
        setReauthMsg({
          deviceId: d.id,
          ok: false,
          text: `Re-auth failed${r.error ? `: ${r.error}` : ""}. ${diagSummary}`,
        });
      }
    } catch (e: any) {
      setReauthMsg({
        deviceId: d.id,
        ok: false,
        text: `Re-auth crashed: ${e?.message || String(e)}`,
      });
    } finally {
      setReauthBusy(null);
    }
  };

  useEffect(() => {
    const pending = listPendingCloudDispatches().map(pendingCloudTaskPlaceholder);
    if (pending.length === 0) return;
    setTasks((prev) => [...pending, ...prev.filter((task) => !pending.some((row) => row.id === task.id))]);
    setActiveTask((current) => current ?? pending[0] ?? null);
  }, []);

  const taskDeviceLabel = useCallback((task: Pick<Task, "deviceId" | "deviceName"> | null | undefined): string => {
    const taskDeviceId = String(task?.deviceId || "").trim();
    if (taskDeviceId) {
      const named = devices.find((device) => device.id === taskDeviceId)?.name;
      if (named) return named;
    }
    return String(task?.deviceName || "").trim() || "this machine";
  }, [devices]);

  const refreshAgentTaskSnapshots = useCallback(async () => {
    if (!token) {
      setAgentTaskSnapshots([]);
      return;
    }
    try {
      setAgentTaskSnapshots(await listAgentTaskSnapshots(CONVEX_URL, token));
    } catch {
      // Convex is discovery only. Preserve direct agent state when it is
      // unavailable rather than treating a failed read as an empty snapshot.
    }
  }, [token]);

  useEffect(() => {
    void refreshAgentTaskSnapshots();
    const interval = setInterval(() => void refreshAgentTaskSnapshots(), 5 * 60_000);
    const onVisibility = () => {
      if (document.visibilityState === "visible") void refreshAgentTaskSnapshots();
    };
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      clearInterval(interval);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [refreshAgentTaskSnapshots]);

  useEffect(() => {
    if (agentTaskSnapshots.length === 0) return;
    setTasks((previous) => reconcileTasksWithAgentSnapshots(previous, agentTaskSnapshots));
  }, [agentTaskSnapshots]);

  const refreshTaskHistory = useCallback(async () => {
    const pendingTasks = listPendingCloudDispatches().map(pendingCloudTaskPlaceholder);
    if (!isConnected) {
      setTasks((previous) => reconcileTasksWithAgentSnapshots([
        ...pendingTasks,
        ...previous.filter((task) => !pendingTasks.some((pending) => pending.id === task.id)),
      ], agentTaskSnapshots));
      return;
    }
    // Key clients by the machine that actually receives /tasks, not merely by
    // the card they were opened from. With runner-role routing, two connected
    // cards can both route to one runner and would otherwise fetch and render
    // the same history twice.
    const taskSources = new Map<string, AgentClient>();
    const focusedTaskDeviceId = agentClient.taskRouteDeviceId ?? connectedDevice?.id ?? "";
    if (focusedTaskDeviceId) taskSources.set(focusedTaskDeviceId, agentClient);
    for (const deviceId of connectedDeviceIds) {
      if (!deviceId) continue;
      const pooled = agentClientPool.get(deviceId);
      if (!pooled.isConnected) continue;
      const effectiveTaskDeviceId = pooled.taskRouteDeviceId ?? pooled.connectedDeviceId ?? deviceId;
      if (!taskSources.has(effectiveTaskDeviceId)) taskSources.set(effectiveTaskDeviceId, pooled);
    }
    try {
      const rows = await Promise.all(
        [...taskSources.values()].map(async (client) => {
          try {
            return await client.listTasks(20);
          } catch {
            return [] as Task[];
          }
        }),
      );
      const merged = dedupeScopedTasks(rows
        .flat()
        .sort((a, b) => (b.updatedAt || b.createdAt || 0) - (a.updatedAt || a.createdAt || 0)));
      setTasks((prev) => {
        const previousByKey = new Map(prev.map((task) => [scopedTaskKey(task), task] as const));
        return reconcileTasksWithAgentSnapshots(dedupeScopedTasks([
          ...pendingTasks,
          ...merged
            .filter((task) => !pendingTasks.some((pending) => pending.id === task.id))
            .map((task) => {
              const previous = previousByKey.get(scopedTaskKey(task));
              return previous && (task.turns?.length ?? 0) === 0 && (previous.turns?.length ?? 0) > 0
                ? { ...task, turns: previous.turns }
                : task;
            }),
        ]), agentTaskSnapshots);
      });
    } catch {
      setTasks((previous) => reconcileTasksWithAgentSnapshots([
        ...pendingTasks,
        ...previous.filter((task) => !pendingTasks.some((pending) => pending.id === task.id)),
      ], agentTaskSnapshots));
    }
  }, [isConnected, connectedDevice?.id, connectedDeviceIds, devices, agentTaskSnapshots]);

  useEffect(() => {
    if (!isConnected) return;
    void refreshTaskHistory();
    const iv = setInterval(() => { void refreshTaskHistory(); }, 10000);
    return () => clearInterval(iv);
  }, [isConnected, refreshTaskHistory]);

  useEffect(() => {
    if (!isConnected) return;
    const poll = async () => { try { setTodoCount(await agentClient.todoCount()); } catch {} };
    poll(); const iv = setInterval(poll, 30000); return () => clearInterval(iv);
  }, [isConnected]);

  const activeTaskDeviceName = activeTask ? taskDeviceLabel(activeTask) : (connectedDevice?.name || "this machine");
  const connectedTaskMachineCount = useMemo(() => {
    const ids = new Set<string>();
    if (connectedDevice?.id) ids.add(connectedDevice.id);
    for (const deviceId of connectedDeviceIds) {
      if (deviceId) ids.add(deviceId);
    }
    return ids.size;
  }, [connectedDevice?.id, connectedDeviceIds]);
  const taskMachineLabels = useMemo(() => {
    const labels = new Set<string>();
    for (const task of tasks) {
      const label = taskDeviceLabel(task);
      if (label) labels.add(label);
    }
    return [...labels];
  }, [tasks, taskDeviceLabel]);
  const multiMachineTaskBanner = connectedTaskMachineCount > 1 || taskMachineLabels.length > 1
    ? `${Math.max(connectedTaskMachineCount, taskMachineLabels.length)} machines live. Tasks stay labeled by machine and open on their own runner box.`
    : null;

  // Keep agentInfo LIVE, not frozen at connect time.
  //
  // The sidebar pill renders agentInfo.version, which is genuinely fetched from
  // the box — but only once, when the connection is established. Restart or
  // upgrade the agent under an open tab and the pill keeps showing the version
  // it learned minutes ago: on 2026-07-25 the dashboard read
  // "v1.99.366-custodian-wired" while the Mac mini was serving 1.99.367.
  //
  // Not stale DATA from a bad source — a correct value that stopped being
  // asked for. Which is the more insidious kind, because everything about it
  // looks right. An agent restart is exactly when the version changes AND
  // exactly when nothing re-asks, so the display is wrong precisely when it
  // matters. 30s, same cadence as the other light polls; getInfo is cheap and
  // already called on every connect.
  useEffect(() => {
    if (!isConnected) return;
    const refreshInfo = async () => { try { setAgentInfo(await agentClient.getInfo()); } catch { /* keep the last known value rather than blanking the pill */ } };
    const iv = setInterval(refreshInfo, 30000);
    return () => clearInterval(iv);
  }, [isConnected]);

  // Load the agent's opencode.json provider state so the chat composer
  // can render "✓ Key configured" instead of an empty input every time
  // the user picks a saved provider. P2P: lives at /runner/opencode/
  // config on the agent itself, never round-tripped via Convex (the
  // boolean is innocuous but the key value would be — agent never
  // emits the value, only the boolean).
  const refreshOpencodeKeyState = useCallback(async () => {
    if (!isConnected || selectedRunner !== "opencode") return;
    try {
      const cfg = await agentClient.openCodeConfig();
      const map: Record<string, boolean> = {};
      for (const p of cfg.providers || []) {
        if (p.id) map[p.id] = !!p.hasApiKey;
      }
      setOpencodeKeyState(map);
    } catch {
      // Best-effort. A failed read just means the indicator stays
      // unset; the existing "Save key + use" flow continues to work.
    }
  }, [isConnected, selectedRunner]);

  useEffect(() => {
    void refreshOpencodeKeyState();
  }, [refreshOpencodeKeyState]);

  useEffect(() => {
    if (!isConnected || selectedRunner !== "opencode") {
      setOpenCodeAgents([]);
      return;
    }
    let cancelled = false;
    agentClient.openCodeConfig().then((cfg) => {
      if (cancelled) return;
      setOpenCodeAgents(
        Array.isArray(cfg?.agents)
          ? cfg.agents.map((agent) => ({
              name: String(agent?.name || "").trim(),
              model: typeof agent?.model === "string" ? agent.model : undefined,
              isBuiltin: !!agent?.isBuiltin,
            })).filter((agent) => agent.name.length > 0)
          : [],
      );
    }).catch(() => {
      if (!cancelled) setOpenCodeAgents([]);
    });
    return () => { cancelled = true; };
  }, [isConnected, selectedRunner, connectedDevice?.id]);

  // ── Actions ─────────────────────────────────────────────────────

  const isDifferentUserAuthError = (message: string, diagnostics: Array<{ error?: string }> = []) => {
    const haystack = [message, ...diagnostics.map((diag) => diag?.error || "")]
      .join(" ")
      .toLowerCase();
    return haystack.includes("token belongs to a different user");
  };

  const connectToDeviceNow = async (device: Device) => {
    if (!token) return;
    // Allocate before setConnectedDevice triggers a render. Project and Git
    // views can then bind to this stable per-device client without creating a
    // pool member (and notifying React subscribers) during render.
    const pooledDeviceClient = agentClientPool.get(device.id);
    const switchingDevice = connectedDevice?.id && connectedDevice.id !== device.id;
    if (switchingDevice || connState === "error") {
      try { agentClient.disconnect(); } catch {}
    }
    setConnectedDevice(device);
    setConnectError(null);
    setConnectDiagnostics([]);

    // Wait for relay config to be loaded (web dashboard MUST use relay)
    if (relayReadyPromiseRef.current) {
      await relayReadyPromiseRef.current;
    }

    // ONE shared predicate (lib/endpoints.ts): drop endpoints that are dead
    // before the first packet (<uuid>.yaver.io — no DNS; *.dev.yaver.io — no
    // cert) instead of probing them into console-error spam.
    // A Desktop GUI talking to its own embedded/adopted agent should use the
    // loopback operation directly. Going out through the public relay first is
    // slower, can fail offline, and creates a needless second-client race.
    const localDesktopTarget = isThisDesktopDevice(device.id, desktopSurface);
    const connectionHost = localDesktopTarget ? "127.0.0.1" : device.host;
    const connectionPort = localDesktopTarget ? 18080 : device.port;
    const tunnelUrls = localDesktopTarget ? [] : usableTunnelUrls(device.publicEndpoints, device.tunnelUrl);
    const rememberPooledConnection = async () => {
      const pooled = pooledDeviceClient;
      pooled.setRelayServers(agentClient.configuredRelayServers.map((r) => ({ ...r })));
      if (!pooled.isConnected) {
        await pooled.connect(connectionHost, connectionPort, token, device.id, { tunnelUrls });
      }
    };

    // Proactive re-auth: if Convex still says the device needs auth,
    // recover the session BEFORE we try to connect. Two important
    // guardrails so this doesn't fight the manual Re-auth button:
    //   1. If the device became needsAuth=false in the last poll
    //      (bus presence override), skip — there's nothing to fix.
    //   2. Track the last reauth attempt per-device and skip if the
    //      relay's 5 s rate-limit window is still active. Otherwise
    //      a quick double-click on Open Workspace produces the
    //      "too many recovery attempts — wait 5 seconds" error from
    //      the relay rate-limiter.
    const reauthRateMs = 8_000;
    const lastReauth = lastAutoReauthRef.current.get(device.id) || 0;
    const sinceLast = Date.now() - lastReauth;
    const lifecycle = deriveDeviceLifecycleState(device);
    if (
      lifecycle === "bootstrap" &&
      agentClient.configuredRelayServers.length > 0 &&
      sinceLast > reauthRateMs
    ) {
      lastAutoReauthRef.current.set(device.id, Date.now());
      try {
        const claimed = await agentClient.ownerClaimDevice(device.id, {
          host: device.host,
          port: device.port,
          tunnelUrl: device.tunnelUrl,
          publicEndpoints: device.publicEndpoints,
          lanIps: device.localIps,
        });
        if (!claimed.ok) {
          await agentClient.reauthAgent({
            deviceId: device.id,
            hostSessionToken: token,
            convexSiteUrl: CONVEX_URL,
          });
        }
      } catch {
        // Best-effort. Errors here are not user-actionable — the
        // user-visible failure path is the connect catch below,
        // which already runs reauthAgent again with proper diagnostics.
      }
    }

    try {
      await agentClient.connect(connectionHost, connectionPort, token, device.id, { tunnelUrls });
      void rememberPooledConnection().catch(() => {});
      setConnectDiagnostics(agentClient.lastConnectDiagnostics);
      clearLastFailure(device.id);
      try {
        const info = await agentClient.getInfo();
        setAgentInfo(info);
        // Push the live agentVersion to Convex on every successful
        // connect. Why: the agent's own heartbeat path can stall when
        // its session token expired (it returns 401 from Convex and
        // can't update agentVersion), leaving the dashboard with a
        // stale "v1.99.36" pill on a box that's actually running
        // v1.99.41. The browser is freshly authenticated for /devices/
        // report-version, so we side-channel the truth in. Convex's
        // own change-or-24h gate inside report-version dedups repeat
        // calls so this is cheap.
        const liveVersion = typeof info?.version === "string" ? info.version.trim() : "";
        if (liveVersion && liveVersion !== device.agentVersion && device.id && token) {
          fetch(`${CONVEX_URL}/devices/report-version`, {
            method: "POST",
            headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
            body: JSON.stringify({ deviceId: device.id, agentVersion: liveVersion }),
          }).catch(() => { /* best-effort */ });
        }
      } catch {}
      try { setRunners(await agentClient.getRunners()); } catch {}
      try { setMcpServers((await agentClient.listMcpServers()).filter((server) => server.enabled)); } catch {}
    } catch (err: any) {
      const firstDiagnostics = agentClient.lastConnectDiagnostics;
      const canTryAutoReauth = Boolean(token && device.id && agentClient.configuredRelayServers.length > 0);
      const rawError = err?.message || "Could not connect to device";
      const authOwnedByAnotherUser = isDifferentUserAuthError(rawError, firstDiagnostics);

      if (authOwnedByAnotherUser) {
        const failureSummary = summarizeFailures(firstDiagnostics) || classifyFetchError({ error: rawError });
        recordLastFailure(device.id, {
          reason: failureSummary.reason,
          label: failureSummary.label,
          detail: failureSummary.detail,
        });
        setConnectError(
          "This device is still paired to a different Yaver user. Open Rescue and run the auth reset, then reconnect once the box comes back."
        );
        setConnectDiagnostics(firstDiagnostics);
        return;
      }

      if (canTryAutoReauth) {
        setConnectError("Connection failed. Trying automatic re-auth recovery…");
        setConnectDiagnostics(firstDiagnostics);
        try {
          const recovered = lifecycle === "bootstrap"
            ? await agentClient.ownerClaimDevice(device.id, {
                host: device.host,
                port: device.port,
                tunnelUrl: device.tunnelUrl,
                publicEndpoints: device.publicEndpoints,
                lanIps: device.localIps,
              })
            : await agentClient.reauthAgent({
                deviceId: device.id,
                hostSessionToken: token,
                convexSiteUrl: CONVEX_URL,
              });
          if (recovered.ok) {
            await agentClient.connect(connectionHost, connectionPort, token, device.id, { tunnelUrls });
            void rememberPooledConnection().catch(() => {});
            setConnectError(null);
            setConnectDiagnostics(agentClient.lastConnectDiagnostics);
            clearLastFailure(device.id);
            try { setAgentInfo(await agentClient.getInfo()); } catch {}
            try { setRunners(await agentClient.getRunners()); } catch {}
            try { setMcpServers((await agentClient.listMcpServers()).filter((server) => server.enabled)); } catch {}
            return;
          }
        } catch {}
      }

      setConnectError(rawError);
      const finalDiagnostics = agentClient.lastConnectDiagnostics;
      setConnectDiagnostics(finalDiagnostics);
      const failureSummary = summarizeFailures(finalDiagnostics) || classifyFetchError({ error: rawError });
      recordLastFailure(device.id, {
        reason: failureSummary.reason,
        label: failureSummary.label,
        detail: failureSummary.detail,
      });
    }
  };

  // The focused AgentClient is intentionally a singleton for legacy surfaces.
  // Serialize focus changes so two fast clicks cannot interleave disconnect /
  // connect mutations and leave the UI labelled as one device while requests
  // address another. Per-device pooled connections remain fully concurrent.
  const connectToDevice = (device: Device): Promise<void> => {
    const pending = deviceConnectQueueRef.current
      .catch(() => { /* the next selection still gets its turn */ })
      .then(() => connectToDeviceNow(device));
    deviceConnectQueueRef.current = pending;
    return pending;
  };

  // Cross-machine capability switch (2026-08-13): an MCP server or a git
  // project lives on ONE machine — the task machine attaches MCPs by name
  // from its own local registry (runner_mcp_scope.go) and runs in the
  // project's directory. So picking a REMOTE machine's MCP chip or project
  // row means: switch the chat to that machine (connectToDevice), refresh
  // its runners/MCPs/projects, and only then set the selection — never
  // report a selection the machine we're about to run on cannot honour.
  // Falls back to a connect-error surface when the box is unreachable.
  const switchChatDevice = async (
    target: Device,
    opts?: { selectMcp?: string; selectProjectName?: string },
  ) => {
    if (!token || !target?.id) return;
    if (target.id === connectedDevice?.id) {
      // Already here — just apply the selection (e.g. a chip on the
      // connected device itself).
      if (opts?.selectMcp) {
        setSelectedMcpServers((prev) => (prev.includes(opts.selectMcp!) ? prev : [...prev, opts.selectMcp!]));
      }
      return;
    }
    try {
      await connectToDevice(target);
    } catch {
      return; // connectToDevice already surfaced the error + diagnostics
    }
    await refreshConnectedRunners();
    if (opts?.selectMcp) {
      // Only keep selections that still exist on the new machine's list,
      // then add the one we switched for.
      setSelectedMcpServers((prev) => [
        ...new Set([...prev.filter((n) => mcpServers.some((s) => s.name === n)), opts.selectMcp!]),
      ]);
      void saveMCPServersToConvex(CONVEX_URL, token, {
        deviceId: target.id,
        mcpServers: [...new Set([...selectedMcpServers.filter((n) => mcpServers.some((s) => s.name === n)), opts.selectMcp!])],
        includeYaverMcp,
        updatedAt: Date.now(),
      }).catch(() => {});
    }
    if (opts?.selectProjectName) {
      const match = chatProjects.find(
        (p) => p.name === opts.selectProjectName || p.gitRemote === opts.selectProjectName,
      );
      if (match) setPreferredSurfaceProjectPath(match.path);
    }
    const label = target.name || target.id || "the other machine";
    setSwitchNotice(`Switched to ${label}. The ${opts?.selectMcp ? `MCP server "${opts.selectMcp}"` : "project"} lives there — run the task from this machine.`);
    setTimeout(() => setSwitchNotice((n) => (n?.startsWith(`Switched to ${label}`) ? null : n)), 6000);
  };

  useEffect(() => {
    if (!token) return;
    let cancelled = false;
    const run = async () => {
      const pending = listPendingCloudDispatches();
      if (pending.length === 0) return;
      let placements: TaskPlacementDecision[] = [];
      let pendingRows = pending;
      try {
        placements = await listRecentTaskPlacements(token, { limit: 50 });
      } catch {
        placements = [];
      }
      try {
        pendingRows = mergePendingCloudDispatchIntents(await listTaskDispatchIntents(token, { limit: 80 }));
        const placeholders = pendingRows.map(pendingCloudTaskPlaceholder);
        setTasks((prev) => [
          ...placeholders,
          ...prev.filter((task) => !placeholders.some((pendingTask) => pendingTask.id === task.id)),
        ]);
      } catch {
        pendingRows = pending;
      }
      for (const row of pendingRows) {
        if (cancelled || pendingDispatchRef.current.has(row.localTaskId)) continue;
        let currentRow = row;
        if (currentRow.placementId) {
          try {
            currentRow = mergePendingCloudPlacementStatus(
              currentRow,
              await getTaskPlacementStatus(token, { placementId: currentRow.placementId }),
            );
            updatePendingCloudDispatch(currentRow.localTaskId, currentRow);
            setTasks((prev) => prev.map((task) =>
              task.id === currentRow.localTaskId ? pendingCloudTaskPlaceholder(currentRow) : task,
            ));
          } catch {
            /* placement status is advisory; dispatch intents remain authoritative */
          }
        }
        if (pendingCloudDispatchNeedsUserAction(currentRow)) continue;
        const placement = currentRow.placementId
          ? placements.find((candidate) => candidate.id === currentRow.placementId)
          : undefined;
        const targetDeviceId = placement?.targetDeviceId || currentRow.targetDeviceId || undefined;
        if (placement?.targetDeviceId && placement.targetDeviceId !== currentRow.targetDeviceId) {
          updatePendingCloudDispatch(currentRow.localTaskId, {
            targetDeviceId: placement.targetDeviceId,
            placementLane: placement.lane,
            placementReason: placement.reason,
            placementCreditLabel: placementCreditLabel(placement) ?? undefined,
          });
          void updateTaskDispatchIntent(token, {
            intentId: currentRow.dispatchIntentId,
            localTaskId: currentRow.localTaskId,
            status: "queued",
            targetDeviceId: placement.targetDeviceId,
          }).catch(() => null);
        }
        if (!targetDeviceId) continue;
        if (connectedDevice?.id !== targetDeviceId || connState !== "connected") {
          const target = devices.find((device) => device.id === targetDeviceId && device.online && !device.needsAuth);
          if (target) void connectToDevice(target);
          continue;
        }
        pendingDispatchRef.current.add(currentRow.localTaskId);
        try {
          void updateTaskDispatchIntent(token, {
            intentId: currentRow.dispatchIntentId,
            localTaskId: currentRow.localTaskId,
            status: "dispatching",
            targetDeviceId,
          }).catch(() => null);
          const task = await agentClient.createTask({ ...currentRow.params, allowLocalFallback: true });
          if (placement?.id || currentRow.placementId) {
            await rebindTaskPlacement(token, placement?.id ?? currentRow.placementId!, task.id, "running").catch(() => null);
          }
          void updateTaskDispatchIntent(token, {
            intentId: currentRow.dispatchIntentId,
            localTaskId: currentRow.localTaskId,
            status: "dispatched",
            taskId: task.id,
            targetDeviceId,
          }).catch(() => null);
          removePendingCloudDispatch(currentRow.localTaskId);
          const nextTask = {
            ...task,
            placementId: placement?.id ?? currentRow.placementId,
            placementLane: placement?.lane ?? currentRow.placementLane,
            placementReason: placement?.reason ?? currentRow.placementReason,
            placementCreditLabel: placementCreditLabel(placement) ?? currentRow.placementCreditLabel,
          };
          setTasks((prev) => [nextTask, ...prev.filter((item) => item.id !== currentRow.localTaskId && item.id !== task.id)]);
          setActiveTask((current) => current?.id === currentRow.localTaskId ? nextTask : current);
          setChatMsgs((prev) => {
            if (activeTask?.id !== currentRow.localTaskId) return prev;
            const base = prev.filter((msg) => !msg.queued);
            return [
              ...base,
              { role: "assistant", text: "Remote machine is connected. Dispatching the queued task now…" },
            ];
          });
        } catch (err) {
          const message = err instanceof Error ? err.message : String(err);
          void updateTaskDispatchIntent(token, {
            intentId: currentRow.dispatchIntentId,
            localTaskId: currentRow.localTaskId,
            status: "failed",
            lastError: message,
            bumpAttempt: true,
          }).catch(() => null);
          updatePendingCloudDispatch(currentRow.localTaskId, {
            attempts: currentRow.attempts + 1,
            lastError: message,
          });
          setTasks((prev) => prev.map((task) =>
            task.id === currentRow.localTaskId
              ? pendingCloudTaskPlaceholder({
                  ...currentRow,
                  attempts: currentRow.attempts + 1,
                  lastError: message,
                  updatedAt: Date.now(),
                })
              : task,
          ));
        } finally {
          pendingDispatchRef.current.delete(currentRow.localTaskId);
        }
      }
    };
    void run();
    const id = setInterval(() => void run(), 5000);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [activeTask?.id, connState, connectedDevice?.id, devices, token]);

  const disconnect = () => { agentClient.disconnect(); setConnectedDevice(null); setAgentInfo(null); setTasks([]); setActiveTask(null); setOutputLines([]); setChatMsgs([]); setRunners([]); setMcpServers([]); setSelectedMcpServers([]); setSelectedRunner(""); setSelectedModel(""); setConnectError(null); setPendingFollowUps([]); };

  // Stream C — silent auto-connect on load (parity with mobile/tvOS). Rule:
  // connect to the primary if it's online, else the secondary; if a preference
  // is set but neither is reachable, fall to the machine list (don't silently
  // grab a third box the user didn't designate). With NO primary/secondary
  // configured, connect the best online machine so a lone box still comes up
  // without a manual click. Runs once (autoConnectTriedRef) so a manual
  // disconnect or pick is never overridden — the ref is already spent. Never
  // fights an in-flight or established connection. The existing "Connecting
  // to <name>…" panel narrates it (role-aware below).
  const autoConnectTriedRef = useRef(false);
  useEffect(() => {
    if (autoConnectTriedRef.current) return;
    if (!token || devices.length === 0 || !relayReady) return;
    if (connectedDevice || connState === "connecting" || connState === "connected") return;
    let cancelled = false;
    // BROWSER-REACHABLE, not "online". A heartbeat proves the box can reach
    // Convex; it says nothing about whether THIS browser can reach the box
    // (the dashboard has only the relay — CORS blocks the LAN). Auto-connecting
    // on `d.online` spends the single attempt (autoConnectTriedRef) on a box
    // that cannot answer, and the user is left at a dead "Connecting…" with no
    // retry. So probe the operation, and let the effect re-run as probes land.
    const browserReachable = async (d: Device) => {
      const cached = probeStates[d.id];
      if (cached?.ok === true) return true;
      try {
        const probe = await agentClient.probeDeviceStatus({
          host: d.host,
          port: d.port,
          token,
          deviceId: d.id,
          tunnelUrls: usableTunnelUrls(d.publicEndpoints, d.tunnelUrl),
        });
        if (!cancelled) setProbeStates((prev) => ({ ...prev, [d.id]: probe }));
        return probe.ok === true;
      } catch (error: any) {
        if (!cancelled) {
          setProbeStates((prev) => ({
            ...prev,
            [d.id]: {
              ok: false,
              checkedAt: new Date().toISOString(),
              error: error?.message || "Probe failed",
              diagnostics: [],
            },
          }));
        }
        return false;
      }
    };
    const pick = async (id: string | null) => {
      if (!id) return undefined;
      const d = devices.find((x) => x.id === id);
      return d && (await browserReachable(d)) ? d : undefined;
    };
    void (async () => {
      const hasPref = Boolean(primaryDeviceId || secondaryDeviceId);
      const candidates = hasPref
        ? ([primaryDeviceId, secondaryDeviceId]
            .map((id) => (id ? devices.find((d) => d.id === id) : undefined))
            .filter(Boolean) as Device[])
		: [...devices].sort((a, b) => (a.name || "").localeCompare(b.name || ""));
      for (const candidate of candidates) {
        if (cancelled || autoConnectTriedRef.current) return;
        const target = await pick(candidate.id);
        if (!target) continue;
        if (cancelled || autoConnectTriedRef.current) return;
        autoConnectTriedRef.current = true;
        void connectToDevice(target);
        return;
      }
    })();
    // connectToDevice intentionally omitted from deps — the ref guarantees a
    // single attempt, and we want the latest closure when it fires.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    return () => { cancelled = true; };
  }, [token, devices, probeStates, primaryDeviceId, secondaryDeviceId, connState, connectedDevice, relayReady]);

  const refreshConnectedRunners = async () => {
    if (!isConnected) return;
    try {
      setRunners(await agentClient.getRunners());
    } catch {}
    try {
      setMcpServers((await agentClient.listMcpServers()).filter((server) => server.enabled));
    } catch {}
    // Project list for the chat composer picker — same /projects the
    // Projects tab + mobile read, so the picker shows real repos on the
    // connected box (2026-08-10).
    try {
      setChatProjects((await agentClient.listProjects()).map((p) => ({
        name: p.name || p.path.split(/[\\/]/).filter(Boolean).pop() || p.path,
        path: p.path,
        branch: p.branch,
        framework: p.framework,
        gitRemote: p.gitRemote,
      })));
    } catch {}
  };

  const handleDashboardChatIntent = (text: string): boolean => {
    const intent = parseDashboardChatIntent(text);
    if (!intent) return false;
    setInput("");
    setSending(false);
    setChatMsgs((prev) => [
      ...prev,
      { role: "user", text },
      { role: "assistant", text: intent.response },
    ]);
    if (intent.kind === "webview") {
      if (intent.projectQuery) setPreferredSurfaceProjectPath(intent.projectQuery);
      setPreferredWebviewMode("web");
      setActiveTab("web-reload");
      try {
        const url = new URL(window.location.href);
        url.searchParams.set("tab", "web-reload");
        window.history.replaceState(null, "", url.toString());
      } catch {}
      return true;
    }
    if (intent.kind === "runtime") {
      setRuntimeIntent({
        nonce: Date.now(),
        kind: "runtime",
        projectQuery: intent.projectQuery,
        surface: intent.surface,
        platform: intent.platform,
      });
      setActiveTab("runtime");
      try {
        const url = new URL(window.location.href);
        url.searchParams.set("tab", "runtime");
        window.history.replaceState(null, "", url.toString());
      } catch {}
      return true;
    }
    if (intent.kind === "tmux") {
      setRuntimeIntent({
        nonce: Date.now(),
        kind: "tmux",
        projectQuery: intent.tmuxQuery,
        tmuxQuery: intent.tmuxQuery,
      });
      setActiveTab("runtime");
      try {
        const url = new URL(window.location.href);
        url.searchParams.set("tab", "runtime");
        window.history.replaceState(null, "", url.toString());
      } catch {}
      return true;
    }
    return false;
  };

  const openTaskRunnerControl = async (mode: "model" | "exit") => {
    if (!activeTask || activeTask.id.startsWith("pending-cloud:")) {
      setConnectError(`Start a task before using /${mode}.`);
      return;
    }
    setRunnerControlMode(mode);
    setRunnerControlCatalog(null);
    setRunnerControlStep("models");
    setRunnerControlError("");
    setRunnerControlBusy(true);
    try {
      const catalog = await taskClientFor(activeTask).getTaskRunnerControls(activeTask.id);
      const initialModel = catalog.model || catalog.models.find((item) => item.isDefault)?.id || catalog.models[0]?.id || "";
      const selected = catalog.models.find((item) => item.id === initialModel);
      setRunnerControlCatalog(catalog);
      setRunnerControlModel(initialModel);
      setRunnerControlEffort(catalog.reasoningEffort || selected?.defaultReasoningEffort || "medium");
    } catch (error) {
      setRunnerControlError(error instanceof Error ? error.message : String(error));
    } finally {
      setRunnerControlBusy(false);
    }
  };

  const selectTaskRunnerModel = (model: ModelInfo) => {
    setRunnerControlModel(model.id);
    if (runnerControlCatalog?.runnerId === "codex" && (model.supportedReasoningEfforts?.length || 0) > 0) {
      setRunnerControlEffort(model.defaultReasoningEffort || runnerControlCatalog.reasoningEffort || "medium");
      setRunnerControlStep("effort");
    }
  };

  const applyTaskRunnerModel = async () => {
    if (!activeTask || !runnerControlCatalog || !runnerControlModel) return;
    setRunnerControlBusy(true);
    setRunnerControlError("");
    try {
      const result = await taskClientFor(activeTask).applyTaskRunnerControl(activeTask.id, {
        control: "model",
        model: runnerControlModel,
        ...(runnerControlCatalog.runnerId === "codex" ? { reasoningEffort: runnerControlEffort } : {}),
      });
      const update = (task: Task): Task => sameScopedTask(task, activeTask) ? {
        ...task,
        model: result.model || runnerControlModel,
        reasoningEffort: result.reasoningEffort,
      } : task;
      setTasks((prev) => prev.map(update));
      setActiveTask((task) => task ? update(task) : task);
      setRunnerControlMode(null);
      setConnectError(null);
    } catch (error) {
      setRunnerControlError(error instanceof Error ? error.message : String(error));
    } finally {
      setRunnerControlBusy(false);
    }
  };

  const confirmTaskRunnerExit = async () => {
    if (!activeTask) return;
    setRunnerControlBusy(true);
    setRunnerControlError("");
    try {
      const result = await taskClientFor(activeTask).applyTaskRunnerControl(activeTask.id, { control: "exit", confirmed: true });
      const update = (task: Task): Task => sameScopedTask(task, activeTask) ? { ...task, status: result.status || "stopped", updatedAt: Date.now() } : task;
      setTasks((prev) => prev.map(update));
      setActiveTask((task) => task ? update(task) : task);
      setRunnerControlMode(null);
      setConnectError(null);
    } catch (error) {
      setRunnerControlError(error instanceof Error ? error.message : String(error));
    } finally {
      setRunnerControlBusy(false);
    }
  };

  const handleSend = async (e?: React.FormEvent) => {
    e?.preventDefault();
    const text = input.trim(); if (!text || sending) return;
    const runnerControl = taskRunnerControlForMessage(text);
    if (runnerControl) {
      setInput("");
      await openTaskRunnerControl(runnerControl);
      return;
    }
    if (handleDashboardChatIntent(text)) return;
    // Mid-run sends go STRAIGHT to the agent — it queues follow-ups on the
    // running task (PendingFollowUps) and drains them when the current
    // response finishes, Claude-Desktop style. The old client-side hold
    // ("dispatch when the task leaves running") predates that and meant the
    // second message never reached the box until the turn ended — and never
    // appeared as a blue bubble at all when the transcript was later rebuilt
    // from a turns-stripped list row (2026-07-27).
    const targetRunner = runners.find((r) => r.id === (activeTask?.runnerId || selectedRunner)) || runners.find((r) => r.id === selectedRunner) || null;
    const authIssue = runnerAuthIssue(targetRunner);
    if (authIssue) {
      // Don't drop the user's input on the floor. Surface the reason
      // both as an inline assistant-style notice (so the chat doesn't
      // appear to swallow the prompt) and as the existing connect-error
      // banner — the user typed "hello" once and saw nothing happen,
      // which read as "the chat is broken".
      setChatMsgs((prev) => [
        ...prev,
        { role: "user", text },
        {
          role: "assistant",
          text: `⚠ ${runnerLabel(targetRunner?.id || "")} needs sign-in on ${
            machineRoles.favorite?.runnerDeviceId && machineRoles.favorite.runnerDeviceId !== connectedDevice?.id
              ? (devices.find((d) => d.id === machineRoles.favorite?.runnerDeviceId)?.name || "the AI runner machine")
              : "this device"
          } before it can answer. Opening the sign-in dialog…`,
        },
      ]);
      setConnectError(authIssue);
      if (targetRunner && (targetRunner.id === "claude" || targetRunner.id === "codex")) {
        setChatRunnerAuthModal(targetRunner.id);
      }
      return;
    }
    setInput(""); setSending(true);
    const continuing = !!activeTask && activeTask.status !== "stopped" && activeTask.status !== "failed";
    // Persist the composer's project + MCP choices to Convex when a NEW task
    // starts — the SAME rows mobile writes (defaultRuntimeProjectByDevice /
    // mcpServersByDevice), so a project picked in the web chat is remembered
    // on the phone and vice versa. Never blocks task creation (fire-and-forget,
    // same rule as mobile's saveLastTaskProjectToConvex).
    if (!continuing) {
      const deviceId = connectedDevice?.id;
      if (deviceId && token) {
        const proj = chatProjects.find((p) => p.path === preferredSurfaceProjectPath);
        if (proj) {
          void saveLastProjectToConvex(CONVEX_URL, token, {
            deviceId,
            projectName: proj.name,
            ...(proj.gitRemote ? { gitRemote: proj.gitRemote } : {}),
            ...(proj.branch ? { branch: proj.branch } : {}),
            updatedAt: Date.now(),
          }).catch(() => {});
        }
        void saveMCPServersToConvex(CONVEX_URL, token, {
          deviceId,
          mcpServers: selectedMcpServers,
          includeYaverMcp,
          updatedAt: Date.now(),
        }).catch(() => {});
      }
    }
    // Optimistic user echo — always push the user bubble + empty assistant placeholder
    // so the next streamed line flows into the assistant bubble, not into the last
    // run's response.
    setChatMsgs(prev => {
      const base = continuing ? prev : [];
      return [...base, { role: "user", text }, { role: "assistant", text: "" }];
    });
    if (!continuing) setOutputLines([]);
    let fallbackPendingCloudTask: PendingCloudDispatch | null = null;
    try {
      if (continuing) {
        // The chat Build|Plan control applies to the whole thread, not just
        // the first prompt — a mid-conversation switch from plan to build is
        // exactly a follow-up (2026-08-13). The agent's /continue endpoint
        // accepts mode; the surface must send it.
        const mode = selectedRunner === "opencode" && selectedOpenCodeMode ? selectedOpenCodeMode : undefined;
        await taskClientFor(activeTask).continueTask(activeTask!.id, text, mode);
      } else {
        let placementPreview: TaskPlacementDecision | null = null;
        const placementKind = inferTaskPlacementKind(text);
        const projectSlug = projectSlugForPlacement(preferredSurfaceProjectPath);
        const resourceClass = resourceClassFromDashboardHints({
          kind: placementKind,
          path: preferredSurfaceProjectPath,
        });
        const profileHints: Partial<TaskPlacementRequest> = {
          projectSlug,
          hasNativeMobile: resourceClass === "heavy" && /\b(ios|android|mobile|expo|react-native|hermes)\b/i.test(preferredSurfaceProjectPath || ""),
          hasDocker: /\b(docker|compose)\b/i.test(preferredSurfaceProjectPath || ""),
        };
        if (token && projectSlug) {
          void upsertProjectProfile(token, {
            projectSlug,
            sourceDeviceId: connectedDevice?.id,
            resourceClass,
            hasNativeMobile: profileHints.hasNativeMobile,
            hasDocker: profileHints.hasDocker,
            confidence: 0.5,
          }).catch(() => {});
        }
        const placementRequest = {
          kind: placementKind,
          sourceSurface: "web-dashboard",
          requestedRunner: selectedRunner || undefined,
          targetDeviceId: connectedDevice?.id,
          forceRelaySource: !preferredSurfaceProjectPath,
          ...profileHints,
        };
        if (token) {
          placementPreview = await previewTaskPlacement(token, placementRequest).catch(() => null);
        }
        if (shouldConfirmExpensiveCloudPlacement(placementPreview)) {
          const ok = window.confirm(expensiveCloudPlacementMessage(placementPreview));
          if (!ok) {
            setChatMsgs((prev) => {
              if (prev.length < 2) return prev;
              return prev.slice(0, prev.length - 2);
            });
            setInput(text);
            return;
          }
        }
        const cloudTargetIsCurrent =
          !!placementPreview?.lane?.startsWith("cloud_") &&
          !!placementPreview.targetDeviceId &&
          placementPreview.targetDeviceId === connectedDevice?.id;
        if (token && placementPreview?.lane?.startsWith("cloud_") && (!cloudTargetIsCurrent || shouldDeferTaskForCloudWorkspace(placementPreview))) {
          const pendingId = pendingPlacementTaskId();
          const recorded = await recordTaskPlacement(token, {
            ...placementRequest,
            taskId: pendingId,
          }).catch(() => null);
          const now = Date.now();
          const pending: PendingCloudDispatch = {
            localTaskId: pendingId,
            placementId: recorded?.id,
            placementLane: recorded?.lane ?? placementPreview?.lane ?? undefined,
            placementReason: recorded?.reason ?? placementPreview?.reason ?? undefined,
            placementCreditLabel: placementCreditLabel(recorded ?? placementPreview) ?? undefined,
            targetDeviceId: recorded?.targetDeviceId ?? placementPreview?.targetDeviceId ?? null,
            params: {
              title: text.slice(0, 80),
              description: text,
              runner: selectedRunner || undefined,
              model: selectedModel || undefined,
              reasoningEffort: selectedRunner === "codex" && connectedDevice?.id
                ? primaryReasoningEffortByDevice[connectedDevice.id] || "medium"
                : undefined,
              mode: selectedRunner === "opencode" && selectedOpenCodeMode ? selectedOpenCodeMode : undefined,
              workDir: preferredSurfaceProjectPath || undefined,
              mcpServers: selectedMcpServers,
              includeYaverMcp,
            },
            createdAt: now,
            updatedAt: now,
            attempts: 0,
          };
          savePendingCloudDispatch(pending);
          createTaskDispatchIntent(token, {
            localTaskId: pendingId,
            placementId: recorded?.id,
            sourceSurface: placementRequest.sourceSurface,
            lane: recorded?.lane ?? placementPreview?.lane ?? undefined,
            targetDeviceId: recorded?.targetDeviceId ?? placementPreview?.targetDeviceId ?? null,
            cloudMachineId: recorded?.cloudMachineId ?? placementPreview?.cloudMachineId ?? null,
            requestedRunner: placementRequest.requestedRunner,
            projectSlug: placementRequest.projectSlug,
            reason: recorded?.reason ?? placementPreview?.reason ?? undefined,
          }).then((intent) => {
            updatePendingCloudDispatch(pendingId, {
              dispatchIntentId: intent.id,
              dispatchStatus: intent.status,
              dispatchExpiresAt: intent.expiresAt,
            });
          }).catch(() => null);
          if (recorded?.id) {
            void activateTaskPlacement(token, { placementId: recorded.id }).then((activation) => {
              const blockedReason = activationBlockReason(activation);
              if (!blockedReason) return;
              updatePendingCloudDispatch(pendingId, {
                dispatchStatus: "blocked",
                blockedAction: activation.action,
                blockedReason,
              });
              void updateTaskDispatchIntent(token, {
                localTaskId: pendingId,
                status: "blocked",
                blockedAction: activation.action,
                reason: blockedReason,
              }).catch(() => null);
            }).catch(() => {});
          }
          const nextTask = pendingCloudTaskPlaceholder(pending);
          setActiveTask(nextTask);
          setTasks((p) => [nextTask, ...p]);
          setChatMsgs((prev) => {
            const base = prev.slice(0, Math.max(0, prev.length - 1));
            return [
              ...base,
              {
                role: "assistant",
                text: "Remote machine is preparing. I queued this locally and did not send it to the currently connected machine.",
                queued: true,
              },
            ];
          });
          return;
        }
        const taskParams = {
          title: text.slice(0, 80),
          description: text,
          runner: selectedRunner || undefined,
          model: selectedModel || undefined,
          reasoningEffort: selectedRunner === "codex" && connectedDevice?.id
            ? primaryReasoningEffortByDevice[connectedDevice.id] || "medium"
            : undefined,
          mode: selectedRunner === "opencode" && selectedOpenCodeMode ? selectedOpenCodeMode : undefined,
          workDir: preferredSurfaceProjectPath || undefined,
          mcpServers: selectedMcpServers,
          includeYaverMcp,
        };
        fallbackPendingCloudTask = {
          localTaskId: "",
          params: taskParams,
          createdAt: Date.now(),
          updatedAt: Date.now(),
          attempts: 0,
        };
        const t = await agentClient.createTask(taskParams);
        let nextTask: Task = {
          ...t,
          placementLane: placementPreview?.lane ?? undefined,
          placementReason: placementPreview?.reason ?? undefined,
          placementCreditLabel: placementCreditLabel(placementPreview) ?? undefined,
        };
        if (token) {
          const recorded = await recordTaskPlacement(token, {
            ...placementRequest,
            taskId: t.id,
          }).catch(() => null);
          if (recorded) {
            nextTask = {
              ...nextTask,
              placementId: recorded.id,
              placementLane: recorded.lane,
              placementReason: recorded.reason ?? undefined,
              placementCreditLabel: placementCreditLabel(recorded) ?? undefined,
            };
            if (recorded.id && recorded.lane.startsWith("cloud_")) {
              void activateTaskPlacement(token, { placementId: recorded.id }).catch(() => {});
            }
          }
        }
        setActiveTask(nextTask);
        setTasks(p => [nextTask, ...p]);
      }
    } catch (err: any) {
      if (err instanceof CloudWorkspaceRequiredError && fallbackPendingCloudTask) {
        const pending = saveCloudWorkspaceRequiredDispatch({
          err,
          params: fallbackPendingCloudTask.params,
          token,
          sourceSurface: "web-dashboard",
          projectSlug: preferredSurfaceProjectPath?.split(/[\\/]/).filter(Boolean).pop()?.slice(0, 80),
        });
        const nextTask = pendingCloudTaskPlaceholder(pending);
        setActiveTask(nextTask);
        setTasks((prev) => [nextTask, ...prev.filter((task) => task.id !== nextTask.id)]);
        setChatMsgs((prev) => {
          const base = prev.slice(0, Math.max(0, prev.length - 1));
          return [
            ...base,
            {
              role: "assistant",
              text: "Remote machine is preparing. I queued this locally and did not send it to the currently connected machine.",
              queued: true,
            },
          ];
        });
        return;
      }
      // PARKED is not FAILED. The agent kept this prompt and replays it into the
      // same session once the runner's credential is restored, so peeling the
      // message and handing the text back would make the user resend it — and
      // then it runs twice when the replay fires. Mirrors the queued-dispatch
      // shape above, which is the same situation with a different blocker.
      if (err instanceof ParkedTurnError) {
        const notice = parkedTurnNotice(err);
        setChatMsgs((prev) => {
          const base = prev.slice(0, Math.max(0, prev.length - 1));
          return [...base, { role: "assistant", text: notice.line, queued: true }];
        });
        return;
      }
      setConnectError(err?.message || "Failed to send");
      // Restore the user's text so they don't have to retype it.
      setInput(text);
      // Peel the optimistic user+placeholder we just pushed.
      setChatMsgs(prev => {
        if (prev.length < 2) return prev;
        return prev.slice(0, prev.length - 2);
      });
    } finally {
      setSending(false);
    }
  };

  const handlePendingCloudBlockedAction = useCallback(async (task: Task) => {
    const action = task.pendingCloudBlockedAction;
    if (action === "runner_auth_required") {
      const runner = String(task.runnerId || selectedRunner || "codex").trim();
      if (runner === "claude" || runner === "claude-code" || runner === "codex") {
        setChatRunnerAuthModal(runner === "claude-code" ? "claude" : runner);
      } else {
        setConnectError(`${runnerLabel(runner)} needs sign-in on the selected machine.`);
      }
      return;
    }
    if (action === "yaver_auth_required" || action === "billing_required") {
      window.open("https://yaver.io", "_blank", "noopener,noreferrer");
      return;
    }
    if (action === "resize_required" || action === "resize_failed" || action === "wake_failed") {
      if (!token || !task.placementId) {
        setConnectError("Retry unavailable: this saved task has no placement id.");
        return;
      }
      try {
        const activation = await activateTaskPlacement(token, { placementId: task.placementId });
        const blockedReason = activationBlockReason(activation);
        updatePendingCloudDispatch(task.id, {
          dispatchStatus: blockedReason ? "blocked" : "queued",
          blockedAction: blockedReason ? activation.action : undefined,
          blockedReason: blockedReason || undefined,
          clearedBlockedAction: !blockedReason,
          updatedAt: Date.now(),
        });
        void updateTaskDispatchIntent(token, {
          localTaskId: task.id,
          status: blockedReason ? "blocked" : "dispatching",
          blockedAction: blockedReason ? activation.action : undefined,
          reason: blockedReason || undefined,
          clearBlockedAction: !blockedReason,
        }).catch(() => null);
        const nextTask = pendingCloudTaskPlaceholder({
          localTaskId: task.id,
          placementId: task.placementId,
          placementLane: task.placementLane,
          placementReason: task.placementReason,
          placementCreditLabel: task.placementCreditLabel,
          targetDeviceId: task.pendingCloudTargetDeviceId,
          dispatchStatus: blockedReason ? "blocked" : "queued",
          blockedAction: blockedReason ? activation.action : undefined,
          blockedReason: blockedReason || undefined,
          params: {
            title: task.title,
            description: task.description || task.title,
            runner: task.runnerId,
            model: task.model,
          },
          createdAt: task.createdAt,
          updatedAt: Date.now(),
          attempts: 0,
        });
        setActiveTask(nextTask);
        setTasks((prev) => prev.map((row) => row.id === task.id ? nextTask : row));
      } catch (err: any) {
        setConnectError(err?.message || "Remote machine retry failed.");
      }
      return;
    }
    setConnectError(task.pendingCloudBlockedReason || "This task is waiting for the selected remote machine.");
  }, [selectedRunner, token]);

  // Dispatch queued follow-ups when the active task transitions out
  // of running/queued. Drains one per transition: continueTask kicks
  // the task back into "running", which re-arms this effect for the
  // next item once that turn lands.
  useEffect(() => {
    if (!activeTask) return;
    if (activeTask.status === "running" || activeTask.status === "queued") return;
    if (activeTask.status === "stopped" || activeTask.status === "failed") {
      setPendingFollowUps([]);
      setChatMsgs((prev) =>
        prev.map((msg) =>
          msg.queued
            ? { role: msg.role, text: msg.text }
            : msg,
        ),
      );
      setConnectError("The previous task is stopped. Start a new task to send another prompt.");
      return;
    }
    if (sending) return;
    if (pendingFollowUps.length === 0) return;
    const [next, ...rest] = pendingFollowUps;
    setPendingFollowUps(rest);
    // Promote the matching queued bubble to a normal user bubble + push
    // an empty assistant placeholder so the next streamed chunk lands
    // in a fresh response (mirrors handleSend's optimistic echo).
    setChatMsgs((prev) => {
      const out: ChatMsg[] = [];
      let promoted = false;
      for (const m of prev) {
        if (!promoted && m.queued && m.role === "user" && m.text === next) {
          out.push({ role: "user", text: m.text });
          promoted = true;
        } else {
          out.push(m);
        }
      }
      out.push({ role: "assistant", text: "" });
      return out;
    });
    setSending(true);
    void (async () => {
      try {
        // Queued follow-ups carry the same Build|Plan mode as the composer —
        // the drain is just handleSend arriving late (2026-08-13).
        const mode = selectedRunner === "opencode" && selectedOpenCodeMode ? selectedOpenCodeMode : undefined;
        await taskClientFor(activeTask).continueTask(activeTask.id, next, mode);
      } catch (err: any) {
        setConnectError(err?.message || "Failed to send queued follow-up");
        // Drop the empty assistant placeholder we pushed; keep the
        // user bubble so they can see what was attempted.
        setChatMsgs((prev) => {
          const last = prev[prev.length - 1];
          if (last && last.role === "assistant" && !last.text) return prev.slice(0, -1);
          return prev;
        });
      } finally {
        setSending(false);
      }
    })();
  }, [activeTask, pendingFollowUps, sending]);

  const selectTask = (t: Task) => {
    setActiveTask(t);
    setOutputLines(t.output || []);
    // Switching tasks abandons any queue tied to the previous task —
    // those follow-ups were intended for the old conversation.
    setPendingFollowUps([]);
    // Prefer the task's recorded turns (every user continue + agent reply)
    // so multi-turn history survives a sidebar navigation. Fall back to
    // [initial prompt, flattened output] when the agent didn't expose turns.
    const msgsFromTask = (task: Task): ChatMsg[] => {
      const projected = firstClassTaskConversationTurns(task.turns, task.presentation)
        .map((turn) => ({ role: turn.role, text: turn.content } as ChatMsg));
      if (!projected.some((message) => message.role === "user")) {
        const userText = displayTaskTitle(task.title || "");
        if (userText) projected.unshift({ role: "user", text: userText });
      }
      if (task.status === "running" && !projected.some((message) => message.role === "assistant")) {
        projected.push({ role: "assistant", text: "" });
      }
      return projected;
    };
    setChatMsgs(msgsFromTask(t));
    setActiveTab("chat");
    // The sidebar hands us a LIST row, and the list endpoint STRIPS turns to
    // bound its payload — so building only from `t` loses every follow-up the
    // user sent (their second message existed solely as the runner's prompt
    // echo, which grooming rightly dedupes). Mobile guards this with
    // keepTurns; web now hydrates the full detail and upgrades in place.
    const hydrate = async () => {
      if (t.source === "session-index" && t.deviceId && !agentClientPool.get(t.deviceId).isConnected) {
        const owner = devices.find((device) => device.id === t.deviceId);
        if (!owner) throw new Error("The machine that owns this task is no longer registered.");
        await connectToDevice(owner);
      }
      const fetched = await taskClientFor(t).getTask(t.id);
      const full = { ...fetched, deviceId: fetched.deviceId || t.deviceId, deviceName: fetched.deviceName || t.deviceName };
      setActiveTask((cur) => (sameScopedTask(cur, full) ? { ...cur, ...full } : cur));
      setTasks((previous) => previous.map((row) => sameScopedTask(row, full) ? full : row));
      setChatMsgs((prev) => {
        const upgraded = msgsFromTask(full);
        return upgraded.length >= prev.length ? upgraded : prev;
      });
    };
    void hydrate().catch((error) => {
      setConnectError(error instanceof Error ? error.message : "Could not connect to this task's machine.");
    });
  };

  const stopTaskFromUI = async (task: Task) => {
    if (task.id.startsWith("pending-cloud:")) {
      setConnectError("This task has not reached an agent yet; manage the pending cloud action shown in the task.");
      return;
    }
    setTaskActionBusy(`stop:${task.id}`);
    try {
      await taskClientFor(task).stopTask(task.id);
      const stopped = { ...task, status: "stopped" as const, updatedAt: Date.now() };
      setTasks((prev) => prev.map((row) => sameScopedTask(row, task) ? stopped : row));
      setActiveTask((current) => sameScopedTask(current, task) ? { ...current, ...stopped } : current);
      setPendingFollowUps([]);
    } catch (err) {
      setConnectError(err instanceof Error ? err.message : "Failed to stop task.");
    } finally {
      setTaskActionBusy(null);
    }
  };

  const deleteTaskFromUI = async (task: Task) => {
    if (task.id.startsWith("pending-cloud:")) {
      setConnectError("Pending cloud dispatches cannot be deleted from the agent task history.");
      return;
    }
    if (!window.confirm(`Delete “${displayTaskTitle(task.title || "this task")}”? This removes its local task history.`)) return;
    setTaskActionBusy(`delete:${task.id}`);
    try {
      await taskClientFor(task).deleteTask(task.id);
      setTasks((prev) => prev.filter((row) => !sameScopedTask(row, task)));
      void refreshAgentTaskSnapshots();
      if (sameScopedTask(activeTask, task)) {
        setActiveTask(null);
        setOutputLines([]);
        setRawOutput([]);
        setRawSince(0);
        setChatMsgs([]);
        setPendingFollowUps([]);
      }
    } catch (err) {
      setConnectError(err instanceof Error ? err.message : "Failed to delete task.");
    } finally {
      setTaskActionBusy(null);
    }
  };
  const onTaskCreated = () => { setActiveTab("chat"); void refreshTaskHistory(); };
  const handleSelectPreviewTarget = async (deviceId: string | null) => {
    const target = deviceId ? devices.find((d) => d.id === deviceId) || null : null;
    const previous = previewTargetId;
    setPreviewTargetId(deviceId);
    try {
      await agentClient.setDevServerTarget({
        targetDeviceId: target?.id,
        targetDeviceName: target?.name,
        targetDeviceClass: target?.deviceClass,
      });
    } catch (err: any) {
      // OPTIMISTIC UI + SWALLOWED ERROR = the dashboard claiming a setting the
      // agent never received. The picker moved to the new target, the POST
      // failed, nothing was said, and every later preview went to the OLD
      // device while the UI insisted otherwise — an unfalsifiable state, since
      // the only evidence was on a box the user was not looking at.
      //
      // Optimism is fine; optimism without rollback is a lie. Put the picker
      // back where the agent actually is and name the failure.
      setPreviewTargetId(previous);
      setConnectError(
        `Could not point previews at ${target?.name || "that device"}: ${err?.message || "the agent did not accept the change"}. ` +
        `Still targeting ${previous ? (devices.find((d) => d.id === previous)?.name || "the previous device") : "this machine"}.`,
      );
    }
  };

  // ── Conditional renders (NO hooks below this point) ─────────────

  if (isLoading) return <div className="flex min-h-[80vh] items-center justify-center"><div className="h-6 w-6 animate-spin rounded-full border-2 border-surface-600 border-t-emerald-400" /></div>;

  if (!isAuthenticated) return (
    <div className="flex min-h-[80vh] items-center justify-center">
      <div className="text-center">
        <h2 className="text-lg font-semibold text-surface-100 mb-2">Sign in to continue</h2>
        {sessionExpired ? (
          <p className="mb-4 text-sm text-amber-700 dark:text-amber-300/90">Your session expired — sign in again.</p>
        ) : null}
        <a href="/auth?return=/dashboard" className="rounded-lg bg-surface-100 px-6 py-3 text-sm font-medium text-surface-900 hover:bg-surface-50">Sign In</a>
      </div>
    </div>
  );

  const displayDevices = devices.map((device) => {
    const peer = peerStates[device.id];
    const probe = probeStates[device.id];
    // `workspaceLive` means we are HOLDING A LIVE CONNECTION — nothing weaker.
    // It used to include `connState === "connecting"`, and every field below
    // keyed off it, so a device we were merely *attempting* got stamped
    // probeState:"ok" + online:true + lastSeen:now. During the background
    // reconnect ladder (8 attempts over ~4 min, each flipping the state back to
    // "connecting") that meant a demonstrably failing box was continuously
    // re-marked healthy, overriding real probe data that said unreachable.
    // See docs/architecture/DEVICE_TRUTH.md F4.
    const workspaceLive = connectedDevice?.id === device.id && connState === "connected";
    const next = {
      ...device,
      workspaceLive,
      peerState: peer?.state ?? device.peerState,
      peerLastSeen: peer?.lastSeen ?? device.peerLastSeen,
      // Only a real probe result (or a live connection) may set probeState.
      probeState: workspaceLive ? "ok" : probe?.ok ? "ok" : probe?.authExpired ? "auth-expired" : probe ? "unreachable" : device.probeState,
      probePath: workspaceLive ? device.probePath : probe?.path ?? device.probePath,
      probeCheckedAt: probe?.checkedAt ?? device.probeCheckedAt,
      probeError: probe?.error ?? device.probeError,
      probeInfo: probe?.info ?? device.probeInfo,
      online: workspaceLive || probe?.ok === true || (peer?.state === "online" ? true : device.online),
      lastSeen: (() => {
        // Every entry here must be a timestamp of something that ACTUALLY
        // happened: a live connection, a peer sighting, a completed probe.
        // Never a synthetic "now" for an attempt in flight.
        const workspaceSeen = workspaceLive ? new Date().toISOString() : "";
        const peerSeen = peer?.lastSeen || "";
        const probeSeen = probe?.checkedAt || "";
        const currentSeen = device.lastSeen || "";
        const best = [workspaceSeen, peerSeen, probeSeen, currentSeen]
          .filter(Boolean)
          .sort((a, b) => (Date.parse(b) || 0) - (Date.parse(a) || 0))[0];
        return best || currentSeen;
      })(),
    };
    return next;
  });
  const connectedDeviceNeedsRecovery = connectedDevice
    ? (() => {
        const lifecycle = deriveDeviceLifecycleState(connectedDevice);
        return lifecycle === "bootstrap" || lifecycle === "yaver-auth-expired";
      })()
    : false;
  const runningTask = tasks.find(t => t.status === "running");
  const activeRunnerId = activeTask?.runnerId || selectedRunner;
  const activeConversationLabel = activeTask?.model
    ? `${activeTask.model}${activeTask.reasoningEffort ? ` · ${activeTask.reasoningEffort}` : ""}`
    : runnerLabel(activeRunnerId);
  const activeRunnerRow = runners.find((r) => r.id === activeRunnerId) || null;
  const activeRunnerAuthIssue = runnerAuthIssue(activeRunnerRow);
  const canStartBrowserRunnerAuth = Boolean(activeRunnerRow && (activeRunnerRow.id === "claude" || activeRunnerRow.id === "codex"));
  const mobileWorkers = displayDevices.filter((d) => d.deviceClass === "edge-mobile");
  const sidebarRoleRank = (id: string): number => {
    const fav = machineRoles.favorite;
    if (id === primaryDeviceId) return 0;
    if (id === fav?.runnerDeviceId) return 1;
    if (id === fav?.renderDeviceId) return 2;
    if (id === secondaryDeviceId) return 3;
    if (id === fav?.secondaryRunnerDeviceId || id === fav?.secondaryRenderDeviceId) return 4;
    return 5;
  };
  const duplicateAuthSidebarIds = (() => {
    const byHost = new Map<string, Device[]>();
    for (const device of displayDevices) {
      const key = duplicateHostKey(device);
      if (!key) continue;
      const list = byHost.get(key) || [];
      list.push(device);
      byHost.set(key, list);
    }
    const hidden = new Set<string>();
    for (const group of byHost.values()) {
      if (group.length < 2) continue;
      const canonical = [...group].sort(
        (a, b) =>
          operationRank(a) - operationRank(b) ||
          sidebarRoleRank(a.id) - sidebarRoleRank(b.id) ||
          Number(Boolean(a.needsAuth)) - Number(Boolean(b.needsAuth)) ||
          stableAliasRank(a) - stableAliasRank(b) ||
          String(a.alias || a.id).localeCompare(String(b.alias || b.id)),
      )[0];
      for (const device of group) {
        if (device.id !== canonical.id) hidden.add(device.id);
      }
    }
    return hidden;
  })();
  const isHiddenSidebarDevice = (device: Device): boolean =>
    isDormantUnreachableDevice(device) || duplicateAuthSidebarIds.has(device.id);
  const dormantDevices = displayDevices.filter((d) => isHiddenSidebarDevice(d));
  const visibleDevices = displayDevices.filter((d) => !isHiddenSidebarDevice(d));
  const selectedPreviewTarget = mobileWorkers.find((d) => d.id === previewTargetId) || null;
  // Project paths are machine-local. Use the per-device client instead of the
  // mutable focused singleton so an in-flight project/Git action cannot be
  // retargeted when the user opens another machine in a second surface.
  const projectSurfaceClient = connectedDevice
    ? agentClientPool.peek(connectedDevice.id) || agentClient
    : agentClient;
  // Owner-only experimental hardware cells. Hidden from non-owners so the
  // default dashboard stays the AI coding/preview/deploy product. Owner status
  // is the server-computed user.isOwner flag (no owner identity in the bundle);
  // mirrors the daemon-side gate (mcp_owner_gate.go).
  const isOwnerAccount = user?.isOwner === true;
  const OWNER_ONLY_TABS = new Set(["arm", "appletv", "robot", "circuit", "printer"]);
  // Mesh moved out of the primary nav into Settings (user directive
  // 2026-07-27): it's set-up-once plumbing, not a daily destination. The
  // "network" tab itself stays a valid DashboardTab \u2014 Settings and ?tab=
  // deep links still open it.
  const tabs: { id: typeof activeTab; label: string; icon: string; badge?: number }[] = ([
    { id: "devices", label: "Devices", icon: "\uD83D\uDCBB" },
    { id: "chat", label: "Chat", icon: "\uD83D\uDCAC" },
    { id: "projects", label: "Projects", icon: "\uD83D\uDCC1" },
    { id: "git", label: "Source", icon: "\u2387" },
    { id: "runtime", label: "Vibing", icon: "\u25A3" },
    { id: "downloads", label: "Downloads", icon: "\u2B07" },
  ] as { id: typeof activeTab; label: string; icon: string; badge?: number }[]).filter(
    (t) =>
      isOwnerAccount || !OWNER_ONLY_TABS.has(t.id),
  );

  return (
    <div className="dashboard-shell relative flex min-h-[100vh] flex-col md:h-[100vh] md:min-h-0 md:flex-row">
      {/* Names any failure that would otherwise reach the user as a bare
          `TypeError: Failed to fetch` — including the unhandled rejections
          that `void someAsync()` call sites produce. See RawFailureBanner. */}
      <RawFailureBanner onSignOut={logout} />
      <div className="pointer-events-none absolute inset-y-0 left-0 hidden w-60 border-r border-white/5 md:block" />
      {/* Mobile top bar — visible only below md */}
      <div className="dashboard-mobilebar md:hidden">
        <div className="flex items-center gap-2 px-3 py-2">
          <div className="flex h-6 w-6 items-center justify-center rounded-full bg-surface-800 text-[10px] font-bold text-surface-300">{user?.email?.charAt(0).toUpperCase()}</div>
          <span className="text-xs font-medium text-surface-200 flex-1 truncate">{connectedDevice?.name || "No device"}</span>
          <span className={`h-1.5 w-1.5 rounded-full ${isConnected ? "bg-emerald-400" : "bg-surface-600"}`} />
          <button
            onClick={() => setActiveTab("settings")}
            className="inline-flex items-center gap-1 rounded-md border border-surface-800 px-2 py-1 text-[10px] font-semibold text-surface-200 transition-colors hover:border-surface-700 hover:bg-surface-800"
            title="Account & settings"
            aria-label="Account & settings"
          >
            <span>{(user?.name || user?.email || "?").charAt(0).toUpperCase()}</span>
          </button>
        </div>
        <div className="flex overflow-x-auto no-scrollbar border-t border-surface-800">
          {tabs.map((t) => (
            <button key={t.id} onClick={() => setActiveTab(t.id)}
              className={`flex items-center gap-1 px-3 py-2 text-[11px] whitespace-nowrap ${activeTab === t.id ? "text-indigo-400 border-b-2 border-indigo-400" : "text-surface-400"}`}>
              <span>{t.icon}</span>{t.label}
              {t.badge != null && t.badge > 0 && <span className="ml-1 text-[9px] bg-indigo-500 text-white rounded-full px-1">{t.badge}</span>}
            </button>
          ))}
        </div>
      </div>

      {/* Sidebar — hidden on mobile */}
      <aside className="dashboard-sidebar hidden h-full w-60 shrink-0 overflow-hidden md:flex md:flex-col">
        <div className="flex min-h-0 flex-1 flex-col">
        <div className="min-h-0 flex-1 overflow-hidden p-3">
        <div className="flex h-full min-h-0 flex-col gap-4 overflow-hidden">
          {/* Brand — same wordmark as the landing page header so the
              dashboard reads as the same product, not a separate
              admin-style shell. Lowercase "yaver" bold + muted ".io". */}
          <a
            href="/"
            className="flex flex-col items-start px-3 py-2 leading-none transition-opacity hover:opacity-80"
            title={`Yaver.io home — web ${WEB_BUILD_LABEL}`}
          >
            <span className="text-xl font-bold tracking-tight text-surface-50">
              yaver<span className="font-normal text-surface-500">.io</span>
            </span>
            <span className="mt-0.5 font-mono text-[10px] tracking-wide text-surface-400">
              {WEB_BUILD_LABEL} · {desktopSurface.isDesktop ? "Desktop GUI" : "Web UI"}
            </span>
          </a>

          {/* Nav */}
          <nav className="flex flex-col gap-[2px]">
	            {([
	              { id: "devices",  label: "Devices",  icon: "💻" },
	              { id: "chat",     label: "Chat",     icon: "💬" },
	              { id: "projects", label: "Projects", icon: "📁" },
	              { id: "runtime", label: "Vibing", icon: "▣" },
	              { id: "downloads", label: "Downloads", icon: "⬇" },
	            ] as const).map((it) => (
              <button
                key={it.id}
                onClick={() => setActiveTab(it.id)}
                className={`relative flex items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs transition-colors ${
                  activeTab === it.id
                    ? "bg-brand-soft/60 text-brand-softFg font-medium"
                    : "text-surface-400 hover:bg-surface-800/60 hover:text-surface-200"
                }`}
              >
                {activeTab === it.id ? (
                  <span aria-hidden className="absolute left-0 top-1.5 bottom-1.5 w-[3px] rounded-r-full bg-brand" />
                ) : null}
                <span className="w-4 text-center text-[13px]">{it.icon}</span>
                <span>{it.label}</span>
              </button>
            ))}
          </nav>

          {/* Vibing (tmux) — every live session on the connected box, one click
              from its terminal, plus the cross-device runner-seat ledger from
              Convex (open or closed, every machine — visible even when no box
              is connected). Same data the Vibing tab shows; the sidebar is
              the glanceable half. Hidden entirely when there are none. */}
          {false && ((isConnected && sidebarTmuxSeats.length > 0) || sidebarConvexRows.length > 0) ? (
            <div className="mb-3 shrink-0">
              <div className="mb-1 flex items-center justify-between">
                {/* Folded by default (user directive 2026-07-27): six session
                    rows above the Devices pill pushed the thing people scan
                    for below the fold. The count keeps the folded header
                    informative; the chevron is the fold control. */}
                <button
                  onClick={() => setSidebarVibingOpen((open) => !open)}
                  className="flex items-center gap-1 text-[10px] font-semibold uppercase tracking-widest text-surface-500 hover:text-surface-300"
                  title={sidebarVibingOpen ? "Fold the Vibing list" : "Unfold the Vibing list"}
                  aria-expanded={sidebarVibingOpen}
                >
                  <span aria-hidden className="inline-block w-2 text-[9px]">{sidebarVibingOpen ? "▾" : "▸"}</span>
                  Vibing
                  <span className="rounded-full bg-surface-800 px-1.5 text-[9px] normal-case tracking-normal text-surface-400">
                    {sidebarTmuxSeats.length + sidebarConvexRows.filter((r) => r.status === "open").length}
                  </span>
                </button>
                <button
                  onClick={() => setActiveTab("runtime")}
                  className="text-[10px] text-surface-500 hover:text-surface-300"
                  title="Open the Vibing tab"
                >
                  see all &rarr;
                </button>
              </div>
              {sidebarVibingOpen ? (
              <div className="max-h-40 space-y-1 overflow-y-auto">
                {isConnected ? sidebarTmuxSeats.slice(0, 6).map((t) => (
                  <div
                    key={`${t.name}#${t.paneId || "session"}`}
                    className="flex w-full items-center gap-2 rounded-md border border-surface-800 bg-surface-900/60 px-2 py-1.5 text-left transition-colors hover:border-brand/40"
                    title={`Join Yaver session ${t.name}`}
                  >
                    <button
                      onClick={() => {
                        if (connectedDevice) {
                          setShellTmuxSession(t.name);
                          setShellTmuxTaskId(t.taskId || null);
                          setShellDevice(connectedDevice);
                        }
                      }}
                      className="flex min-w-0 flex-1 items-center gap-2 text-left"
                    >
                      <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${t.attached ? "bg-success animate-live-pulse" : "bg-surface-600"}`} />
                      <span className="truncate text-[11px] text-surface-200">{t.name}{t.paneId ? ` · ${t.paneId}` : ""}</span>
                      <span className="ml-auto shrink-0 text-[9px] text-surface-500">
                        {t.agentType ? t.agentType : `${t.windows ?? 1}w`}
                      </span>
                    </button>
                    {/* Adopt an un-adopted session straight from the sidebar —
                        previously the helper existed but no UI called it. One
                        click brings the live pane under task management (poll +
                        follow-up composer). */}
                    {!t.taskId ? (
                      <button
                        onClick={async (e) => {
                          e.stopPropagation();
                          try {
                            const { taskId } = await agentClient.adoptTmuxSession(t.name, t.paneId);
                            setShellTmuxTaskId(taskId);
                            setSidebarTmux(await agentClient.listTmuxSessions());
                          } catch (err) {
                            setConnectError(err instanceof Error ? err.message : "Failed to adopt session.");
                          }
                        }}
                        className="shrink-0 rounded border border-surface-700 px-1.5 py-0.5 text-[9px] text-surface-400 transition-colors hover:border-brand/50 hover:text-brand-300"
                        title={`Adopt ${t.name} as a Yaver task`}
                      >
                        Adopt
                      </button>
                    ) : (
                      <span className="shrink-0 rounded border border-success/30 px-1.5 py-0.5 text-[9px] text-success">
                        task
                      </span>
                    )}
                  </div>
                )) : null}
                {isConnected && sidebarTmuxSeats.length > 6 ? (
                  <p className="px-2 text-[9px] text-surface-500">+{sidebarTmuxSeats.length - 6} more in Vibing</p>
                ) : null}
                {/* Cross-device ledger rows: other machines + closed seats.
                    Muted vs the connected box's live sessions; attach only
                    works when the seat is on the connected device. */}
                {sidebarConvexRows.length > 0 ? (
                  <>
                    {isConnected && sidebarTmuxSeats.length > 0 ? (
                      <p className="px-2 pt-1 text-[9px] uppercase tracking-widest text-surface-600">All machines</p>
                    ) : null}
                    {sidebarConvexRows.slice(0, 6).map((r) => {
                      const open = r.status === "open";
                      const seat = isRunnerSeat(r);
                      const deviceLabel = r.deviceName || r.deviceId.slice(0, 8);
                      const attachable = open && r.deviceId === connectedDevice?.id;
                      return (
                        <button
                          key={`${r.deviceId}#${r.sessionName}#${r.paneId || "session"}`}
                          onClick={() => {
                            if (!connectedDevice) {
                              setConnectError("Connect to a device before joining its Yaver sessions.");
                              return;
                            }
                            if (r.deviceId !== connectedDevice.id) {
                              setConnectError(`"${r.sessionName}" runs on ${deviceLabel}. Switch to that device in Devices to attach.`);
                              return;
                            }
                            setShellTmuxSession(r.sessionName);
                            setShellTmuxTaskId(null);
                            setShellDevice(connectedDevice);
                          }}
                          className={`flex w-full items-center gap-2 rounded-md border px-2 py-1.5 text-left transition-colors ${open ? "border-surface-800 bg-surface-900/60 hover:border-brand/40" : "border-surface-900 bg-surface-950/40"}`}
                          title={attachable
                            ? `Join Yaver session ${r.sessionName}`
                            : `${r.sessionName} on ${deviceLabel} (${r.status})${connectedDevice && r.deviceId !== connectedDevice.id ? " — switch devices to attach" : ""}`}
                        >
                          <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${open ? (seat ? "bg-success animate-live-pulse" : "bg-surface-600") : "bg-surface-700"}`} />
                          <span className={`truncate text-[11px] ${open ? "text-surface-200" : "text-surface-500 line-through"}`}>{r.sessionName}{r.paneId ? ` · ${r.paneId}` : ""}</span>
                          <span className="ml-auto shrink-0 text-[9px] text-surface-500">
                            {r.runner}{r.origin === "manual" ? " · started in terminal" : ""}{open ? "" : " · closed"}
                          </span>
                          {r.deviceId !== connectedDevice?.id ? (
                            <span className="shrink-0 text-[9px] text-surface-600">{deviceLabel}</span>
                          ) : null}
                        </button>
                      );
                    })}
                    {sidebarConvexRows.length > 6 ? (
                      <p className="px-2 text-[9px] text-surface-500">+{sidebarConvexRows.length - 6} more</p>
                    ) : null}
                  </>
                ) : null}
              </div>
              ) : null}
            </div>
          ) : null}

          {/* Devices (lean) */}
          <div className="shrink-0">
            <div className="mb-1 flex items-center justify-between">
              <p className="text-[10px] font-semibold uppercase tracking-widest text-surface-500">Devices</p>
              <button
                onClick={() => setActiveTab("devices")}
                className="text-[10px] text-surface-500 hover:text-surface-300"
                title="Open the Devices tab"
              >
                see all &rarr;
              </button>
            </div>
            {/* Machine-role split pill — when the account slices work across
                two boxes, the left nav names BOTH, each with its own live
                dot. Two silent sources are two unfalsifiable states; the
                sidebar is the persistent banner for them. */}
            {machineRoles.favorite?.runnerDeviceId && machineRoles.favorite?.renderDeviceId && machineRoles.favorite.renderDeviceId !== machineRoles.favorite.runnerDeviceId ? (
              <button
                onClick={() => setActiveTab("runtime")}
                title="Machine roles — AI tasks and rendering run on different boxes. Click to open Vibing."
                className="mb-1.5 w-full rounded-lg border border-indigo-500/30 bg-indigo-500/5 px-3 py-2 text-left shadow-sm hover:border-indigo-500/50"
              >
                {(["runner", "render"] as const).map((role) => {
                  const id = role === "runner" ? machineRoles.favorite!.runnerDeviceId : machineRoles.favorite!.renderDeviceId!;
                  const row = devices.find((d) => d.id === id);
                  return (
                    <div key={role} className="flex items-center gap-2 py-0.5">
                      <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${row?.online ? "bg-success" : "bg-surface-600"}`} />
                      <span className="w-10 shrink-0 text-[9px] font-semibold uppercase tracking-wide text-indigo-600 dark:text-indigo-300">{role === "runner" ? "AI" : "Render"}</span>
                      <span className="truncate text-[11px] text-surface-200">{row?.name || `${id.slice(0, 8)}…`}</span>
                    </div>
                  );
                })}
              </button>
            ) : null}
            {isConnected && connectedDevice ? (
              (() => {
                // Pill state must reflect *live* needsAuth / lastSeen, not
                // the snapshot we took at connect time. Convex flips
                // needsAuth=false a few hundred ms after a successful
                // re-auth (heartbeat trigger) and refreshDevices replays
                // the row into the devices array — but connectedDevice is
                // a separate piece of state that never syncs unless we
                // look it up here.
                const liveDevice = devices.find((d) => d.id === connectedDevice.id) ?? connectedDevice;
                const connectedNeedsAuth = !!liveDevice.needsAuth;
                const connectedIsReauthing = reauthBusy === liveDevice.id;
                const connectedReauthMsg =
                  reauthMsg && reauthMsg.deviceId === liveDevice.id ? reauthMsg : null;
                const pillBorder = connectedNeedsAuth
                  ? "border-warning/40 bg-warning-soft/40"
                  : "border-success/30 bg-success-soft/30";
                const dotColor = connectedNeedsAuth
                  ? (connectedIsReauthing ? "bg-warning animate-pulse" : "bg-warning")
                  : "bg-success animate-live-pulse";
                return (
                  <div className={`rounded-lg border ${pillBorder} px-3 py-2.5 shadow-sm`}>
                    <div className="flex items-center gap-2">
                      <span className={`h-2 w-2 shrink-0 rounded-full ${dotColor}`} />
                      <span className="truncate text-xs font-medium text-surface-100">{liveDevice.name}</span>
                    </div>
                    <div className="mt-1 flex items-center justify-between gap-2">
                      <span className="truncate text-[10px] text-surface-500">
                        {devicePlatformLabel(liveDevice)}
                        {agentInfo ? ` · v${agentInfo.version}` : ""}
                        {connectedNeedsAuth ? " · needs auth" : ""}
                      </span>
                      <div className="flex shrink-0 items-center gap-2">
                        {connectedNeedsAuth ? (
                          <button
                            onClick={() => reauthDevice(liveDevice)}
                            disabled={connectedIsReauthing}
                            title="Agent's session token expired — re-auth so /projects, runners, and tasks accept your bearer again"
                            className="rounded border border-brand/40 bg-brand-soft px-1.5 py-0.5 text-[9px] font-semibold uppercase tracking-wide text-brand-softFg hover:bg-brand/15 hover:border-brand/60 disabled:opacity-40 transition-colors"
                          >
                            {connectedIsReauthing ? "…" : "Re-auth"}
                          </button>
                        ) : null}
                        <button onClick={disconnect} className="text-[10px] text-danger hover:underline transition-colors">disconnect</button>
                      </div>
                    </div>
                    {connectedNeedsAuth ? (
                      // Agent itself isn't authed — runner sign-in
                      // (Codex/Claude OAuth) can't possibly work
                      // because /runner-auth/* are owner-protected
                      // and the agent has no owner. Hide the runner
                      // row entirely and surface a clear single
                      // action: pair the agent first. Once paired,
                      // the runner row reappears below for the
                      // separate codex/claude OAuth flow.
                      <div className="mt-1 rounded border border-amber-300 bg-amber-50 px-2 py-1.5 text-[10px] dark:border-amber-500/30 dark:bg-amber-500/5">
                        <div className="font-semibold text-amber-800 dark:text-amber-200">
                          Yaver agent needs auth
                        </div>
                        <div className="mt-0.5 text-slate-600 dark:text-surface-400">
                          Pair this device to your account before signing in to Codex / Claude.
                          Yaver auth and coding-agent auth are separate.
                        </div>
                        <button
                          onClick={() => reauthDevice(liveDevice)}
                          disabled={connectedIsReauthing}
                          className="mt-1.5 rounded bg-amber-200 px-2 py-1 text-[10px] font-semibold uppercase tracking-wide text-amber-900 hover:bg-amber-300 disabled:opacity-40 dark:bg-amber-500/30 dark:text-amber-100 dark:hover:bg-amber-500/40"
                        >
                          {connectedIsReauthing ? "Pairing…" : "Pair device"}
                        </button>
                      </div>
                    ) : (() => {
                      // Surface which coding agent this device defaults
                      // to + whether its cloud auth is healthy.  Lets the
                      // user spot "agent is connected but my Claude Code
                      // token expired" without opening Devices tab.
                      const deviceRunnerStates = (liveDevice.runners || []) as Array<{ runnerId?: string; authConfigured?: boolean; needsAuth?: boolean }>;
                      const primary = connectedDevicePrimaryRunner;
                      if (!primary && deviceRunnerStates.length === 0) return null;
                      const primaryRow = primary
                        ? deviceRunnerStates.find((r) => r?.runnerId === primary)
                        : deviceRunnerStates.find((r) => r?.authConfigured) ?? deviceRunnerStates[0];
                      const runnerId = primary || primaryRow?.runnerId || "";
                      if (!runnerId) return null;
                      const isCloud = !runnerId.startsWith("ollama") && runnerId !== "aider-ollama" && runnerId !== "yaver-local";
                      const livePrimaryRow =
                        connectedDevice && connectedDevice.id === liveDevice.id
                          ? (primary
                              ? runners.find((r) => r.id === primary)
                              : runners.find((r) => r.authConfigured) || runners[0])
                          : null;
                      const authed = livePrimaryRow
                        ? livePrimaryRow.ready !== false
                        : primaryRow
                          ? !!primaryRow.authConfigured && !primaryRow.needsAuth
                          : false;
                      // Single-action design: when sign-in is needed
                      // we show ONE button (the call-to-action) with
                      // amber "Sign in {Runner}" copy. When authed,
                      // ONE small ✓ badge (no button — there's
                      // nothing to do). Local runners show "local".
                      // Old design had a status badge AND a button
                      // side-by-side which read as two separate
                      // controls.
                      return (
                        <div className="mt-1.5 flex items-center gap-2 text-[10px]">
                          <span className="text-surface-500">runner:</span>
                          <span className="font-medium text-surface-200">{runnerLabel(runnerId)}</span>
                          {!isCloud ? (
                            <span className="ml-auto rounded-full border border-surface-700 px-1.5 py-px text-[9px] uppercase tracking-wider text-surface-400">
                              local
                            </span>
                          ) : authed ? (
                            <span className="ml-auto rounded-full border border-emerald-500/40 px-1.5 py-px text-[9px] uppercase tracking-wider text-emerald-700 dark:text-emerald-300">
                              ✓ signed in
                            </span>
                          ) : (
                            <button
                              onClick={() => setChatRunnerAuthModal(runnerId)}
                              className="ml-auto whitespace-nowrap rounded-md border border-amber-400/60 bg-amber-100 px-2 py-0.5 text-[10px] font-medium text-amber-800 hover:border-amber-500 hover:bg-amber-200 dark:border-amber-500/40 dark:bg-amber-500/15 dark:text-amber-100 dark:hover:bg-amber-500/25"
                              title={`OAuth-sign-in to the ${runnerLabel(runnerId)} CLI on this device. Separate from Yaver-agent auth.`}
                            >
                              Sign in &rarr;
                            </button>
                          )}
                        </div>
                      );
                    })()}
                    {connectedReauthMsg ? (
                      <div className={`mt-1 text-[10px] leading-tight ${connectedReauthMsg.ok ? "text-emerald-700 dark:text-emerald-300" : "text-red-700 dark:text-red-300"}`}>
                        {connectedReauthMsg.text}
                      </div>
                    ) : null}
                  </div>
                );
              })()
            ) : visibleDevices.length === 0 ? (
              <p className="text-[11px] text-surface-600">No devices yet</p>
            ) : (
              <div className="space-y-0.5">
                {visibleDevices.slice(0, 10).map((d) => {
                  const isSelected = connectedDevice?.id === d.id;
                  const isConnecting = isSelected && connState === "connecting";
                  const hasError = isSelected && connState === "error";
                  const isReauthing = reauthBusy === d.id;
                  const lifecycle = deriveDeviceLifecycleState(d);
                  const needsRecovery = lifecycle === "bootstrap" || lifecycle === "yaver-auth-expired";
                  const readyToConnect = lifecycle === "ready-to-connect" || lifecycle === "connected";
                  const dotClass = hasError
                    ? "bg-red-400"
                    : isConnecting || isReauthing
                      ? "bg-amber-400 animate-pulse"
                      : lifecycle === "bootstrap"
                        ? "bg-violet-400"
                        : lifecycle === "yaver-auth-expired"
                        ? "bg-amber-400"
                        : readyToConnect
                          ? "bg-cyan-400"
                          : "bg-surface-600";
                  const wrapClass = hasError
                    ? "border border-red-500/30 bg-red-500/5"
                    : isConnecting
                      ? "border border-amber-500/30 bg-amber-500/5"
                      : needsRecovery
                        ? "border border-amber-500/30 bg-amber-500/5"
                        : "border border-transparent hover:bg-surface-800/80";
                  const showReauthMsg = reauthMsg && reauthMsg.deviceId === d.id;
                  return (
                    <div key={d.id} className={`rounded-md ${wrapClass}`}>
                      <div className="flex items-center gap-1">
                        <button
                          onClick={() => connectToDevice(d)}
                          className="flex min-w-0 flex-1 items-center gap-2 px-2 py-1.5 text-left text-xs"
                          title={`${d.host}:${d.port}`}
                        >
                          <span className={`h-2 w-2 shrink-0 rounded-full ${dotClass}`} />
                          <span className="min-w-0 flex-1 truncate text-surface-200">{d.name}</span>
                          {primaryDeviceId === d.id ? (
                            <span className="shrink-0 text-[9px] text-indigo-400" title="Primary">&#9733;</span>
                          ) : null}
                        </button>
                        {needsRecovery ? (
                          <button
                            onClick={() => reauthDevice(d)}
                            disabled={isReauthing}
                            title={lifecycle === "bootstrap" ? "Device is in bootstrap mode — reclaim it from the browser" : "Agent session expired — re-auth via the browser"}
                            className={`mr-1 shrink-0 rounded px-1.5 py-0.5 text-[9px] font-semibold uppercase tracking-wide disabled:opacity-40 ${
                              lifecycle === "bootstrap"
                                ? "bg-violet-500/20 text-violet-700 dark:text-violet-200 hover:bg-violet-500/30"
                                : "bg-amber-500/20 text-amber-700 dark:text-amber-200 hover:bg-amber-500/30"
                            }`}
                          >
                            {isReauthing ? "…" : lifecycle === "bootstrap" ? "Reclaim" : "Re-auth"}
                          </button>
                        ) : null}
                      </div>
                      {showReauthMsg ? (
                        <div
                          className={`px-2 pb-1 text-[10px] leading-tight ${
                            reauthMsg!.ok ? "text-emerald-700 dark:text-emerald-300" : "text-red-700 dark:text-red-300"
                          }`}
                        >
                          {reauthMsg!.text}
                        </div>
                      ) : null}
                    </div>
                  );
                })}
                {visibleDevices.length > 10 ? (
                  <button
                    onClick={() => setActiveTab("devices")}
                    className="w-full px-2 text-left text-[10px] text-surface-500 hover:text-surface-300"
                  >
                    +{visibleDevices.length - 10} more
                  </button>
                ) : null}
                {dormantDevices.length > 0 ? (
                  <button
                    onClick={() => setActiveTab("devices")}
                    className="w-full rounded-md border border-amber-500/20 bg-amber-500/5 px-2 py-1.5 text-left text-[10px] text-amber-700 dark:text-amber-200 hover:bg-amber-500/10"
                    title="Open the Devices tab to reveal stale hidden devices"
                  >
                    {dormantDevices.length} stale device{dormantDevices.length === 1 ? "" : "s"} hidden
                  </button>
                ) : null}
              </div>
            )}
          </div>

          {/* Task history is shared with mobile: these are the agent's real
              task rows, not an Electron-only cache. Active/review/completed
              tasks stay selectable across GUI, web, and mobile reconnects. */}
          {isConnected ? (
            <div className="min-h-0 shrink border-t border-surface-800 pt-3">
              <div className="mb-1 flex items-center justify-between">
                <button
                  onClick={() => setActiveTab("chat")}
                  className="text-[10px] font-semibold uppercase tracking-widest text-surface-500 hover:text-surface-300"
                  title="Open task chat and live console"
                >
                  Tasks
                </button>
                <div className="flex items-center gap-1.5">
                  {connectedTaskMachineCount > 1 ? (
                    <span className="rounded-full border border-emerald-500/30 bg-emerald-500/10 px-1.5 text-[9px] text-emerald-300">
                      {connectedTaskMachineCount} live
                    </span>
                  ) : null}
                  <span className="rounded-full bg-surface-800 px-1.5 text-[9px] text-surface-400">{tasks.length}</span>
                </div>
              </div>
              <div className="max-h-52 space-y-0.5 overflow-y-auto pr-0.5">
                {tasks.length === 0 ? (
                  <p className="px-2 py-1 text-[10px] text-surface-600">No tasks yet</p>
                ) : tasks.map((task) => {
                  const live = task.status === "running" || task.status === "queued";
                  const selected = sameScopedTask(activeTask, task);
                  const route = task.model
                    ? `${task.model}${task.reasoningEffort ? ` · ${task.reasoningEffort}` : ""}`
                    : task.runnerId ? runnerLabel(task.runnerId) : "";
                  const taskMeta = [route, taskDeviceLabel(task)].filter(Boolean).join(" · ");
                  return (
                    <button
                      key={scopedTaskKey(task)}
                      onClick={() => selectTask(task)}
                      className={`flex w-full items-start gap-2 rounded-md px-2 py-1.5 text-left transition-colors ${
                        selected ? "bg-brand-soft/60 text-brand-softFg" : "text-surface-400 hover:bg-surface-800/70 hover:text-surface-200"
                      }`}
                      title={`${displayTaskTitle(task.title)} · ${task.status}${taskMeta ? ` · ${taskMeta}` : ""}`}
                    >
                      <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${
                        live ? "animate-pulse bg-amber-400"
                          : task.status === "review" ? "bg-violet-400"
                          : task.status === "completed" ? "bg-emerald-400"
                          : "bg-surface-600"
                      }`} />
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-[11px]">{displayTaskTitle(task.title)}</div>
                        <div className="truncate text-[9px] text-surface-500">{taskMeta}</div>
                      </div>
                      <span className={`shrink-0 pt-0.5 text-[9px] ${statusColor(task.status)}`}>
                        {live ? "ongoing" : task.status}
                      </span>
                    </button>
                  );
                })}
              </div>
            </div>
          ) : null}

          <div className="min-h-6 flex-1" />

        </div>
        </div>
        </div>
      </aside>

      {/* Main */}
      <div className="dashboard-main flex min-w-0 flex-1 flex-col overflow-hidden">
        <div className="dashboard-topbar sticky top-0 z-20 hidden items-center justify-end gap-2 px-4 py-2 md:flex">
          <button onClick={toggleTheme} className="rounded-md p-1.5 text-surface-400 hover:bg-surface-800 hover:text-surface-100" title={theme === "dark" ? "Switch to light mode" : "Switch to dark mode"}>
            {theme === "dark" ? (
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="5"/><line x1="12" y1="1" x2="12" y2="3"/><line x1="12" y1="21" x2="12" y2="23"/><line x1="4.22" y1="4.22" x2="5.64" y2="5.64"/><line x1="18.36" y1="18.36" x2="19.78" y2="19.78"/><line x1="1" y1="12" x2="3" y2="12"/><line x1="21" y1="12" x2="23" y2="12"/><line x1="4.22" y1="19.78" x2="5.64" y2="18.36"/><line x1="18.36" y1="5.64" x2="19.78" y2="4.22"/></svg>
            ) : (
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>
            )}
          </button>
          <div className="relative">
            <button
              onClick={() => setUserMenuOpen((v) => !v)}
              className="inline-flex items-center gap-2 rounded-md border border-surface-800 bg-surface-900 px-2.5 py-1.5 text-[11px] font-semibold text-surface-200 transition-colors hover:border-surface-700 hover:bg-surface-850"
              title="Account menu"
              aria-haspopup="menu"
              aria-expanded={userMenuOpen}
            >
              <span className="flex h-5 w-5 items-center justify-center rounded-full bg-surface-800 text-[10px] font-bold text-surface-300">
                {(user?.name || user?.email || "?").charAt(0).toUpperCase()}
              </span>
              <span className="max-w-[140px] truncate">{user?.name || user?.email || "Account"}</span>
              <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
                <polyline points="6 9 12 15 18 9" />
              </svg>
            </button>
            {userMenuOpen ? (
              <>
                <button
                  type="button"
                  aria-label="Close menu"
                  onClick={() => setUserMenuOpen(false)}
                  className="fixed inset-0 z-30 cursor-default"
                />
                <div
                  role="menu"
                  className="absolute right-0 top-full z-40 mt-1 min-w-[200px] overflow-hidden rounded-md border border-surface-800 bg-surface-900 shadow-lg"
                >
                  <div className="border-b border-surface-800 px-3 py-2 text-[10px] text-surface-500">
                    <div className="truncate text-surface-300">{user?.name || user?.email}</div>
                    {user?.name && user?.email ? (
                      <div className="truncate text-surface-500">{user.email}</div>
                    ) : null}
                  </div>
                  <button
                    role="menuitem"
                    onClick={() => {
                      setUserMenuOpen(false);
                      setActiveTab("settings");
                    }}
                    className="flex w-full items-center gap-2 px-3 py-2 text-left text-[12px] text-surface-200 hover:bg-surface-800"
                  >
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
                      <circle cx="12" cy="12" r="3" />
                      <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
                    </svg>
                    Settings
                  </button>
                  <button
                    role="menuitem"
                    onClick={() => {
                      setUserMenuOpen(false);
                      logout();
                    }}
                    className="flex w-full items-center gap-2 border-t border-surface-800 px-3 py-2 text-left text-[12px] text-red-700 dark:text-red-300 hover:bg-red-500/10 hover:text-red-700 dark:hover:text-red-200"
                  >
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
                      <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
                      <polyline points="16 17 21 12 16 7" />
                      <line x1="21" y1="12" x2="9" y2="12" />
                    </svg>
                    Sign Out
                  </button>
                </div>
              </>
            ) : null}
          </div>
        </div>

        <div className="relative z-[1] flex min-h-0 flex-1 flex-col overflow-hidden">
          {!isConnected && CONNECTION_REQUIRED_TABS.has(activeTab) ? (
            <div className="flex-1 overflow-y-auto px-4 py-6 sm:px-6 lg:px-8">
              <div className="mx-auto w-full max-w-[1680px]">
                <div className="mb-6 text-center">
                  <h2 className="mb-2 text-lg font-semibold text-surface-100">
                    {(() => {
                      const label = tabs.find((t) => t.id === activeTab)?.label;
                      return label ? `Connect a device to use ${label}` : "Connect a device";
                    })()}
                  </h2>
                {connState === "connecting" ? (
                  <div className="flex flex-col items-center gap-3">
                    <div className="h-6 w-6 animate-spin rounded-full border-2 border-surface-600 border-t-amber-400" />
                    <p className="text-sm text-surface-400">
                      {connectedDevice?.id === primaryDeviceId
                        ? `Primary (${connectedDevice?.name}) is online — connecting…`
                        : connectedDevice?.id === secondaryDeviceId
                        ? `Secondary (${connectedDevice?.name}) is online — connecting…`
                        : `Connecting to ${connectedDevice?.name}…`}
                    </p>
                    <p className="text-xs text-surface-600">Trying relay servers</p>
                  </div>
                ) : connState === "error" ? (
                  (() => {
                    const authExpired = connectDiagnostics.some((d) => d.authExpired);
                    const anyHttpAnswered = connectDiagnostics.some((d) => d.status && d.status > 0);
                    const relayTunnelDown = connectDiagnostics.some(
                      (d) => d.path === "relay" && (d.status === 502 || d.status === 503 || d.status === 504),
                    );
                    // A relay-lane 401 whose body is the RELAY's own verdict
                    // ("relay password missing …" / "invalid relay password",
                    // relay/server.go:1903,1916) is a CREDENTIAL failure on
                    // OUR side — the agent never saw the request. Blaming the
                    // agent for it was the 2026-07-28 misattribution incident.
                    // A genuine agent 401 (body e.g. "invalid token") transits
                    // a working relay lane and keeps the agent-rejection copy.
                    const relayCredentialDenied = connectDiagnostics.some((d) => isRelayCredentialDeny(d));
                    const agentRejected = connectDiagnostics.some(
                      (d) => (d.status === 401 || d.status === 403) && !isRelayCredentialDeny(d),
                    );
                    const anyRelayProbeTried = connectDiagnostics.some((d) => d.path === "relay");
                    const anyLoadFailed = connectDiagnostics.some((d) => d.path === "direct" && !anyHttpAnswered);
                    const relayCount = agentClient.configuredRelayServers.length;
                    // Direct from an HTTPS web origin to http://LAN-IP:18080 is always
                    // blocked as mixed content. Surface that explicitly.
                    const mixedContentLikely =
                      anyLoadFailed && typeof window !== "undefined" && window.location.protocol === "https:";
                    const headline = authExpired
                      ? "Agent reachable, but its Convex session is expired"
                      : relayCredentialDenied
                        ? "Relay refused the request — your account's relay password is missing or stale"
                        : relayTunnelDown
                          ? "Relay tunnel down — web cannot reach this machine"
                        : agentRejected
                          ? "Agent responded, but the connection was rejected"
                          : relayCount === 0
                            ? "No relay configured — can't reach this agent from the web"
                            : "Could not reach agent";
                    const reauthCmd = "yaver auth";
                    return (
                      <div className="mx-auto flex w-full max-w-xl flex-col items-center gap-3">
                        <div className="w-full rounded-lg border border-red-500/20 bg-red-500/5 px-4 py-3 text-left">
                          <p className="text-sm text-red-400 font-medium mb-1">{headline}</p>
                          <p className="text-xs text-surface-500">{connectError || "Could not reach agent (direct or via relay)"}</p>

                          {connectDiagnostics.length > 0 ? (
                            <div className="mt-3 space-y-1">
                              {connectDiagnostics.map((d, i) => (
                                <div
                                  key={i}
                                  className="flex items-center gap-2 text-[10px] font-mono text-surface-500"
                                >
                                  <span className={`shrink-0 w-1.5 h-1.5 rounded-full ${d.ok ? "bg-emerald-400" : d.authExpired ? "bg-amber-400" : "bg-red-400"}`} />
                                  <span className="text-surface-400 w-20 shrink-0 truncate">
                                    {d.path === "relay" ? `relay · ${d.relayId || "?"}` : "direct"}
                                  </span>
                                  <span className={`shrink-0 ${d.authExpired ? "text-amber-700 dark:text-amber-300" : d.ok ? "text-emerald-700 dark:text-emerald-300" : "text-red-700 dark:text-red-300"}`}>
                                    {d.authExpired ? "auth expired" : d.ok ? "ok" : d.status ? `HTTP ${d.status}` : (d.error || "error")}
                                  </span>
                                  {d.durationMs !== undefined ? (
                                    <span className="text-surface-700 ml-auto">{d.durationMs}ms</span>
                                  ) : null}
                                </div>
                              ))}
                            </div>
                          ) : null}

                          {/* Why-it-happened explainer */}
                          <div className="mt-3 text-[10px] text-surface-500 space-y-1">
                            <div>
                              <span className="text-surface-400">Relays configured:</span>{" "}
                              <span className={relayCount === 0 ? "text-red-700 dark:text-red-300" : "text-surface-300"}>{relayCount}</span>
                              {relayCount > 0 && !anyRelayProbeTried ? (
                                <span className="ml-2 text-amber-700 dark:text-amber-300">(no relay probe attempted — device has no deviceId?)</span>
                              ) : null}
                            </div>
                            {relayCredentialDenied ? (
                              <div className="text-amber-700 dark:text-amber-300">
                                {RELAY_CREDENTIAL_REMEDY}
                              </div>
                            ) : null}
                            {relayTunnelDown ? (
                              <div className="text-amber-700 dark:text-amber-300">
                                The relay answered, but it has no live tunnel to this agent. From yaver.io, direct LAN HTTP is blocked by the browser, so the relay must be healthy before web connect can work.
                              </div>
                            ) : null}
                            {mixedContentLikely ? (
                              <div className="text-amber-700 dark:text-amber-300">
                                Direct probe returned <code className="rounded bg-surface-900 px-1 font-mono">Load failed</code> because a browser on <code className="rounded bg-surface-900 px-1 font-mono">https://</code> can&apos;t fetch <code className="rounded bg-surface-900 px-1 font-mono">http://</code> LAN IPs (mixed content). The web path has to go through a relay.
                              </div>
                            ) : null}
                          </div>

                          {/* Re-auth — always offered on connection error. */}
                          <div className="mt-3 rounded border border-amber-500/20 bg-amber-500/5 p-2 text-left">
                            <p className="text-[11px] text-amber-700 dark:text-amber-300">
                              {relayTunnelDown
                                ? "Web re-auth cannot run until a relay can reach the box. Try one relay repair, then retry; if the tunnel is still down, run `yaver auth` or restart Yaver on the box."
                                : authExpired
                                ? "Agent accepted the probe but its Convex session is stale. Hand your current session down to the box:"
                                : "Try handing your current session down to the box — works even if the agent's own token is dead, as long as one relay can reach it:"}
                            </p>
                            <div className="mt-2 flex items-center gap-2">
                              {relayTunnelDown ? (
                                <button
                                  disabled={rescueQueuing || !connectedDevice || !token}
                                  onClick={async () => {
                                    if (!connectedDevice || !token) return;
                                    setRescueQueuing(true);
                                    setReauthMessage(null);
                                    try {
                                      const res = await agentClient.queueRescueCommand(connectedDevice.id, "tunnel-reset");
                                      const tail = res.deduped ? "already pending" : `queued ${res.commandId.slice(0, 8)}`;
                                      setReauthMessage({
                                        kind: "ok",
                                        text: `Tunnel reset ${tail}. The agent picks it up on heartbeat, restarts itself, then rebuilds the relay tunnel.`,
                                      });
                                      setTimeout(() => connectToDevice(connectedDevice), 35_000);
                                    } catch (e: any) {
                                      setReauthMessage({ kind: "err", text: e?.message || "Tunnel reset queue failed" });
                                    } finally {
                                      setRescueQueuing(false);
                                    }
                                  }}
                                  className="flex-1 rounded border border-amber-500/40 bg-amber-500/10 px-3 py-1.5 text-[11px] font-medium text-amber-700 dark:text-amber-200 hover:bg-amber-500/20 disabled:opacity-40"
                                >
                                  {rescueQueuing ? "Queueing tunnel reset..." : "Queue tunnel reset"}
                                </button>
                              ) : null}
                              {relayTunnelDown ? (
                                <button
                                  disabled={reauthing || !token}
                                  onClick={async () => {
                                    if (!token) return;
                                    setReauthing(true);
                                    setReauthMessage(null);
                                    try {
                                      const repaired = await repairRelay();
                                      setReauthMessage({
                                        kind: repaired.repaired ? "ok" : "err",
                                        text: repaired.repaired
                                          ? "Relay credentials refreshed — retrying connect..."
                                          : `Relay repair did not change anything${repaired.reason ? `: ${repaired.reason}` : ""}`,
                                      });
                                      if (repaired.repaired && connectedDevice) {
                                        setTimeout(() => connectToDevice(connectedDevice), 400);
                                      }
                                    } catch (e: any) {
                                      setReauthMessage({ kind: "err", text: e?.message || "Relay repair failed" });
                                    } finally {
                                      setReauthing(false);
                                    }
                                  }}
                                  className="flex-1 rounded border border-amber-500/40 bg-amber-500/10 px-3 py-1.5 text-[11px] font-medium text-amber-700 dark:text-amber-200 hover:bg-amber-500/20 disabled:opacity-40"
                                >
                                  {reauthing ? "Repairing relay..." : "Repair relay & retry"}
                                </button>
                              ) : null}
                              <button
                                disabled={reauthing || relayTunnelDown || !connectedDevice || !token || relayCount === 0}
                                onClick={async () => {
                                  if (!connectedDevice || !token) return;
                                  setReauthing(true);
                                  setReauthMessage(null);
                                  try {
                                    const result = await agentClient.reauthAgent({
                                      deviceId: connectedDevice.id,
                                      hostSessionToken: token,
                                      convexSiteUrl: CONVEX_URL,
                                    });
                                    if (result.ok) {
                                      setReauthMessage({
                                        kind: "ok",
                                        text: `Agent accepted via ${result.via} (${result.mode}) — reconnecting…`,
                                      });
                                      setTimeout(() => {
                                        connectToDevice(connectedDevice);
                                      }, 400);
                                    } else {
                                      const lines = result.diagnostics.map(
                                        (d) => `${d.path} · ${d.step}: ${d.ok ? "ok" : d.error || `HTTP ${d.status ?? "?"}`}`,
                                      );
                                      setReauthMessage({
                                        kind: "err",
                                        text: `${result.error || "Re-auth failed"}\n${lines.join("\n")}`,
                                      });
                                    }
                                  } catch (e: any) {
                                    setReauthMessage({ kind: "err", text: e?.message || "Re-auth failed" });
                                  }
                                  setReauthing(false);
                                }}
                                className="flex-1 rounded border border-amber-500/40 bg-amber-500/10 px-3 py-1.5 text-[11px] font-medium text-amber-700 dark:text-amber-200 hover:bg-amber-500/20 disabled:opacity-40"
                              >
                                {relayTunnelDown
                                  ? "Web re-auth unavailable (relay tunnel down)"
                                  : reauthing
                                    ? "Re-authing…"
                                    : relayCount === 0
                                      ? "Re-auth (needs a relay)"
                                      : "Re-auth this device from web"}
                              </button>
                            </div>
                            {reauthMessage ? (
                              <pre className={`mt-2 whitespace-pre-wrap break-words font-mono text-[10px] ${reauthMessage.kind === "ok" ? "text-emerald-700 dark:text-emerald-300" : "text-red-700 dark:text-red-300"}`}>
                                {reauthMessage.text}
                              </pre>
                            ) : null}
                            <div className="mt-3 border-t border-amber-500/20 pt-2">
                              <p className="text-[10px] text-surface-500">Or, from a shell on the remote box:</p>
                              <div className="mt-1 flex items-center gap-2">
                                <code className="flex-1 rounded bg-surface-900 px-2 py-1 text-[11px] text-surface-300 font-mono">{reauthCmd}</code>
                                <button
                                  onClick={() => {
                                    navigator.clipboard?.writeText(reauthCmd);
                                    setCopiedReauth(true);
                                    setTimeout(() => setCopiedReauth(false), 1500);
                                  }}
                                  className="rounded border border-surface-700 px-2 py-1 text-[10px] text-surface-400 hover:text-surface-200"
                                >
                                  {copiedReauth ? "copied" : "copy"}
                                </button>
                              </div>
                            </div>
                          </div>

                          {!anyHttpAnswered && !mixedContentLikely && relayCount > 0 ? (
                            <p className="mt-3 text-xs text-surface-600">
                              Relays are configured but none could reach the agent. Check <code className="rounded bg-surface-800 px-1 py-0.5 text-surface-400">yaver serve</code> is running on this machine and it's registered with the relay.
                            </p>
                          ) : null}
                        </div>
                        <div className="flex gap-2">
                          {connectedDevice && <button onClick={() => connectToDevice(connectedDevice)} className="rounded-md bg-amber-500/10 border border-amber-500/20 px-4 py-2 text-xs text-amber-400 hover:bg-amber-500/20">Retry</button>}
                          <button onClick={disconnect} className="rounded-md border border-surface-700 px-4 py-2 text-xs text-surface-400 hover:text-surface-300">Back</button>
                        </div>
                      </div>
                    );
                  })()
                ) : (
                  <>
                    <p className="mb-6 text-sm text-surface-500">Connect to a device running <code className="rounded bg-surface-800 px-1.5 py-0.5 text-surface-300">yaver serve</code></p>
                    <div className="grid grid-cols-1 gap-4 text-left md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
                      {visibleDevices.map((d) => (
                        <DeviceConnectCard
                          key={d.id}
                          device={d}
                          isPrimary={primaryDeviceId === d.id}
                          isSecondary={secondaryDeviceId === d.id}
                          isSelected={false}
                          isConnecting={false}
                          token={token}
                          onAliasSaved={refreshDevices}
                          onOpenShell={() => { setShellTmuxSession(null); setShellDevice(d); }}
                          onOpenRemoteDesktop={() => setRemoteDesktopDevice(d)}
                          onConnect={() => connectToDevice(d)}
                          onTogglePrimary={token ? async () => {
                            const nextId = primaryDeviceId === d.id ? null : d.id;
                            const prev = primaryDeviceId;
                            setPrimaryDeviceId(nextId);
                            try {
                              const res = await fetch(`${CONVEX_URL}/settings`, {
                                method: "POST",
                                headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
                                body: JSON.stringify({ primaryDeviceId: nextId }),
                              });
                              if (!res.ok) throw new Error(`status ${res.status}`);
                            } catch (e: any) {
                              setPrimaryDeviceId(prev);
                              alert(`Could not update primary: ${e?.message ?? e}`);
                            }
                          } : undefined}
                          canTogglePrimary={!!token}
                          onToggleSecondary={token ? async () => {
                            const nextId = secondaryDeviceId === d.id ? null : d.id;
                            const prev = secondaryDeviceId;
                            setSecondaryDeviceId(nextId);
                            try {
                              const res = await fetch(`${CONVEX_URL}/settings`, {
                                method: "POST",
                                headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
                                body: JSON.stringify({ secondaryDeviceId: nextId }),
                              });
                              if (!res.ok) throw new Error(`status ${res.status}`);
                            } catch (e: any) {
                              setSecondaryDeviceId(prev);
                              alert(`Could not update secondary: ${e?.message ?? e}`);
                            }
                          } : undefined}
                          canToggleSecondary={!!token && primaryDeviceId !== d.id}
                        />
                      ))}
                    </div>
                    {visibleDevices.length === 0 && (
                      <div className="mt-4 rounded-2xl border border-surface-800 bg-surface-900/70 p-5 text-left">
                        <p className="text-sm font-medium text-surface-200">No devices found</p>
                        <p className="mt-2 text-xs leading-5 text-surface-500">
                          Start <code className="rounded bg-surface-800 px-1.5 py-0.5 text-surface-300">yaver serve</code> on a machine signed into this account. If browser OAuth succeeded on that machine but it still does not show up here, run <code className="rounded bg-surface-800 px-1.5 py-0.5 text-surface-300">yaver auth factory-reset</code> and re-auth.
                        </p>
                      </div>
                    )}
                  </>
                )}
                </div>
              </div>
            </div>
          ) : activeTab === "home" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-6xl mx-auto w-full"><OverviewView user={user ?? undefined} onNavigate={(tab) => setActiveTab(tab as typeof activeTab)} /></div>
          ) : activeTab === "projects" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-4xl mx-auto w-full"><ProjectsView client={projectSurfaceClient} key={connectedDevice!.id} connectedDeviceId={connectedDevice!.id} onTaskCreated={onTaskCreated} mobileWorkers={mobileWorkers} selectedPreviewTarget={selectedPreviewTarget} onSelectPreviewTarget={handleSelectPreviewTarget} onReconnect={connectedDevice ? async () => { await connectToDevice(connectedDevice); } : undefined} onRepairRelay={token ? repairRelay : undefined} /></div>
          ) : activeTab === "git" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-6xl mx-auto w-full">
              <GitView
                client={projectSurfaceClient}
                key={connectedDevice!.id}
                connectedDeviceId={connectedDevice!.id}
                devices={devices}
                onOpenSurface={(surface, projectPath) => {
                  setPreferredSurfaceProjectPath(projectPath);
                  setActiveTab(surface);
                }}
              />
            </div>
          ) : activeTab === "runtime" ? (
            <div className="flex-1 min-h-0 overflow-hidden">
              <RuntimeLabView
                intent={runtimeIntent}
                connectedDevice={connectedDevice}
                devices={devices}
                machineRoles={machineRoles.favorite}
                onSaveMachineRoles={machineRoles.save}
                onClearMachineRoles={machineRoles.clear}
                desktopSurface={desktopSurface}
                onReconnect={connectedDevice ? async () => { await connectToDevice(connectedDevice); } : undefined}
                onOpenTmux={(sessionName) => {
                  if (!connectedDevice) {
                    setConnectError("Connect to a device before joining its Yaver session.");
                    return;
                  }
                  setShellTmuxSession(sessionName);
                  setShellDevice(connectedDevice);
                }}
              />
            </div>
          ) : activeTab === "vibe" ? (
            <div className="flex-1 min-h-0 overflow-hidden">
              <VibeCodingView
                devices={devices}
                connectedDevice={connectedDevice}
                connState={connState}
                onSelectDevice={connectToDevice}
                mobileWorkers={mobileWorkers}
                selectedPreviewTarget={selectedPreviewTarget}
                onSelectPreviewTarget={handleSelectPreviewTarget}
                onReconnect={connectedDevice ? async () => { await connectToDevice(connectedDevice); } : undefined}
                onRepairRelay={token ? repairRelay : undefined}
                onQueueTunnelReset={connectedDevice && token ? async () => {
                  const queued = await agentClient.queueRescueCommand(connectedDevice.id, "tunnel-reset");
                  return { ...queued, deviceId: connectedDevice.id };
                } : undefined}
              />
            </div>
          ) : activeTab === "todos" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-4xl mx-auto w-full"><TodosView onTaskCreated={onTaskCreated} /></div>
          ) : activeTab === "feedback" ? (
            <div className="flex-1 min-h-0 w-full max-w-5xl mx-auto"><FeedbackWorkQueueView token={token} agentConnected={connState === "connected"} /></div>
          ) : activeTab === "builds" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-4xl mx-auto w-full space-y-6">
              <BuildsView onTaskCreated={onTaskCreated} preferredProjectPath={preferredSurfaceProjectPath} />
              {/* Per-target deploy capability matrix — rendered
               * below the builds list so the user can see at a
               * glance whether the connected device can actually
               * ship to TestFlight / Play Store / Convex / CF
               * before clicking a deploy button. */}
              <div>
                <h3 className="mb-3 text-sm font-semibold text-surface-200 dark:text-surface-100">
                  Deploy capabilities
                </h3>
                <DeployCapabilitiesView />
              </div>
              {/* Live deploy board from the box's autorun store — what's
                  shipping right now, which build/stage, uploads-today vs cap.
                  Complements "can I deploy?" above with "what is deploying?". */}
              <div>
                <h3 className="mb-3 text-sm font-semibold text-surface-200 dark:text-surface-100">
                  Deploy status
                </h3>
                <DeployStatusView />
              </div>
            </div>
          ) : activeTab === "webview" || activeTab === "preview" || activeTab === "web-reload" ? (
            <div className="flex-1 min-h-0 overflow-hidden">
              <WebviewView
                connectedDevice={connectedDevice}
                connState={connState}
                preferredMode={activeTab === "web-reload" ? "web" : activeTab === "preview" ? "mobile" : preferredWebviewMode}
                preferredProjectPath={preferredSurfaceProjectPath}
                mobileWorkers={mobileWorkers}
                selectedPreviewTarget={selectedPreviewTarget}
                onSelectPreviewTarget={handleSelectPreviewTarget}
                onReconnect={connectedDevice ? async () => { await connectToDevice(connectedDevice); } : undefined}
                onRepairRelay={token ? repairRelay : undefined}
                connectedDeviceNeedsAuth={connectedDeviceNeedsRecovery}
                onSwitchAgent={() => setActiveTab("devices")}
                onTriggerReauth={(runner) => setChatRunnerAuthModal(runner)}
                primaryRunner={connectedDevicePrimaryRunner}
                runnerRows={runners}
              />
            </div>
          ) : activeTab === "health" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-4xl mx-auto w-full"><HealthView /></div>
          ) : activeTab === "screenlog" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-5xl mx-auto w-full"><ScreenMonitorView /></div>
          ) : activeTab === "quality" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-4xl mx-auto w-full"><QualityView /></div>
          ) : activeTab === "data" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-5xl mx-auto w-full"><DataView /></div>
          ) : activeTab === "switch" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-5xl mx-auto w-full"><SwitchView /></div>
          ) : activeTab === "accounts" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-5xl mx-auto w-full"><AccountsView /></div>
          ) : activeTab === "company-ai" ? (
            <div className="flex-1 min-h-0 w-full"><CompanyAIOptionsView /></div>
          ) : activeTab === "infra" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-6xl mx-auto w-full"><InfraView /></div>
          ) : activeTab === "connect" ? (
            <div className="flex-1 min-h-0 w-full">
              <ConnectivityView
                token={token}
                devices={devices}
                connectedDevice={connectedDevice}
                connState={connState}
                connectDiagnostics={connectDiagnostics}
              />
            </div>
          ) : activeTab === "network" ? (
            <div className="flex-1 min-h-0 w-full">
              <NetworkView token={token} />
            </div>
          ) : activeTab === "tools" ? (
            <ToolsView devices={devices} />
          ) : activeTab === "observ" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-6xl mx-auto w-full"><ObservabilityView /></div>
          ) : activeTab === "autoruns" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-6xl mx-auto w-full"><AutorunsView /></div>
          ) : activeTab === "ops" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-6xl mx-auto w-full"><OpsView /></div>
          ) : activeTab === "verbs" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-6xl mx-auto w-full"><ToolPanelView /></div>
          ) : activeTab === "extras" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-6xl mx-auto w-full"><ExtrasView /></div>
          ) : activeTab === "share" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-3xl mx-auto w-full"><ShareView /></div>
          ) : activeTab === "build" && !HIDE_PAID_UI ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-3xl mx-auto w-full">
              <h2 className="text-lg font-semibold text-surface-100">Build your app</h2>
              <p className="mt-1 max-w-2xl text-xs leading-5 text-surface-500">
                Codex writes the code. Yaver handles the app plumbing one step at a time.
              </p>
              <ol className="mt-4 grid gap-2 text-xs sm:grid-cols-3">
                {[
                  ["1", "Pick target", "Phone preview, backend, website, or stores."],
                  ["2", "Fill fields", "Only the selected tool asks for input."],
                  ["3", "Run it", "Watch status, logs, and outputs here."],
                ].map(([n, title, body]) => (
                  <li key={n} className="rounded-md border border-surface-800 bg-surface-900/70 p-3">
                    <div className="flex items-center gap-2">
                      <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-surface-800 text-[10px] font-semibold text-surface-300">
                        {n}
                      </span>
                      <span className="font-medium text-surface-200">{title}</span>
                    </div>
                    <p className="mt-2 leading-5 text-surface-500">{body}</p>
                  </li>
                ))}
              </ol>
              <div className="mt-4">
                <CapabilityShelf token={token} />
              </div>
              <div className="mt-6">
                <StudioPanel />
              </div>
              <div className="mt-6">
                <QAPanel />
              </div>
              <div className="mt-6">
                <WebTestsPanel />
              </div>
            </div>
          ) : activeTab === "convex" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-5xl mx-auto w-full"><ConvexView /></div>
          ) : activeTab === "security" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-3xl mx-auto w-full"><TwoFactorView token={token} autoStart={autoStart2faSetup} /></div>
          ) : activeTab === "settings" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-3xl mx-auto w-full">
              {/* Account plan + relay usage — profile clicks land here. */}
              <PlanUsageCard deviceNames={Object.fromEntries(devices.map((d) => [d.id, d.name]))} />
              {/* Optional runner/render machine slicing — the favorite config. */}
              <MachineRolesCard token={token} devices={devices.map((d) => ({ id: d.id, name: d.name, platform: d.platform }))} roles={machineRoles} desktopSurface={desktopSurface} />
              {/* Mesh lives here now — set-up-once plumbing, not a nav tab. */}
              <button
                type="button"
                onClick={() => setActiveTab("network")}
                className="mb-4 flex w-full items-center justify-between rounded-lg border border-surface-800 bg-surface-900/60 px-4 py-3 text-left transition-colors hover:border-surface-700 hover:bg-surface-800/60"
              >
                <span className="flex items-center gap-2 text-sm text-surface-200">
                  <span aria-hidden>🕸️</span> Mesh network
                  <span className="text-[11px] text-surface-500">device-to-device WireGuard mesh</span>
                </span>
                <span aria-hidden className="text-surface-500">→</span>
              </button>
              <SettingsView
                user={user as any}
                onLogout={logout}
                onOpenTwoFactor={() => setActiveTab("security")}
              />
            </div>
          ) : activeTab === "storage" ? (
            <div className="flex-1 min-h-0 w-full"><StorageView /></div>
          ) : activeTab === "arm" && isOwnerAccount ? (
            <div className="flex-1 min-h-0 w-full overflow-auto p-4"><ArmCellView devices={devices} token={token} /></div>
          ) : activeTab === "appletv" && isOwnerAccount ? (
            <div className="flex-1 min-h-0 w-full overflow-auto p-4"><AppleTVCellView devices={devices} token={token} /></div>
          ) : activeTab === "apikeys" ? (
            <div className="flex-1 min-h-0 w-full max-w-4xl mx-auto"><APIKeysView /></div>
          ) : activeTab === "schedules" ? (
            <div className="flex-1 min-h-0 w-full max-w-4xl mx-auto"><SchedulesView /></div>
          ) : activeTab === "packages" ? (
            <div className="flex-1 min-h-0 w-full max-w-4xl mx-auto overflow-y-auto"><PackagesView /></div>
          ) : activeTab === "phone" ? (
            <div className="flex-1 min-h-0 w-full max-w-6xl mx-auto overflow-auto p-4"><PhoneProjectsView /></div>
          ) : activeTab === "companion" ? (
            <div className="flex-1 min-h-0 w-full overflow-auto"><CompanionView /></div>
          ) : activeTab === "domains" ? (
            <div className="flex-1 min-h-0 w-full max-w-5xl mx-auto">
              {token && user?.id ? <DomainsView token={token} userId={user.id} /> :
                <div className="p-6 text-xs text-surface-500">Sign in to manage custom domains.</div>}
            </div>
          ) : activeTab === "exec" ? (
            <div className="flex-1 min-h-0 w-full"><ExecView /></div>
          ) : activeTab === "downloads" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-4xl mx-auto w-full">
              <DownloadsView />
            </div>
          ) : activeTab === "devices" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-5xl mx-auto w-full space-y-4">
              <PendingClaimsSection
                items={pendingClaims}
                onClaim={async (deviceId, name) => {
                  const result = await claimPending(deviceId, name);
                  if (result.ok) {
                    // The claim flips the row to a real devices entry —
                    // pick it up immediately instead of waiting on the
                    // next 10s poll.
                    await refreshDevices();
                  }
                  return result;
                }}
                onRefresh={refreshPending}
              />
              <DevicesView
                devices={displayDevices}
                devicesLoading={devicesLoading}
                devicesError={devicesError}
                devicesFetchedAt={devicesFetchedAt}
                onRefresh={refreshDevices}
                signedInEmail={user?.email}
                signedInProvider={undefined}
                token={token}
                onOpen={connectToDevice}
                onCloseWorkspace={disconnect}
                activeWorkspaceDeviceId={connectedDevice?.id ?? null}
                connectedDeviceIds={connectedDeviceIds}
                workspaceConnectionState={connState}
                connectError={connectError}
                connectDiagnostics={connectDiagnostics}
                hiddenCount={hiddenIds.size}
                onNavigateCloud={() => setActiveTab("settings")}
                machineRoles={machineRoles}
                desktopSurface={desktopSurface}
              />
            </div>
          ) : (
            <>
              <div className="flex flex-1 min-h-0">
                <div className="flex flex-1 min-w-0 flex-col">
                  {activeTask ? (
                    <>
                      <div className="flex items-center gap-3 border-b border-surface-800 px-4 py-2">
                        <span className={`h-1.5 w-1.5 rounded-full ${activeTask.status === "running" || activeTask.status === "queued" ? "animate-pulse bg-amber-400" : activeTask.status === "review" ? "bg-violet-400" : activeTask.status === "completed" ? "bg-emerald-400" : "bg-surface-600"}`} />
                        <span className="truncate text-sm font-medium text-surface-200">{displayTaskTitle(activeTask.title)}</span>
                        <span className={`text-[10px] ${statusColor(activeTask.status)}`}>{remoteAgentStatusLabel(activeTask.status)}</span>
                        {activeRunnerId ? (
                          <button
                            type="button"
                            onClick={() => void openTaskRunnerControl("model")}
                            className={`inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-[10px] font-medium ${
                              activeTask.status === "running" || activeTask.status === "queued"
                                ? "border-emerald-400/60 bg-emerald-400/10 text-emerald-800 dark:text-emerald-200"
                                : "border-surface-700 bg-surface-900 text-surface-300"
                            }`}
                            title={`Change the model for the next turn on ${activeTaskDeviceName}.`}
                          >
                            <span className={`h-1.5 w-1.5 rounded-full ${activeTask.status === "running" || activeTask.status === "queued" ? "animate-pulse bg-emerald-400" : "bg-surface-500"}`} />
                            {activeTask.model
                              ? `${activeTask.model}${activeTask.reasoningEffort ? ` · ${activeTask.reasoningEffort}` : ""}`
                              : runnerLabel(activeRunnerId)}
                          </button>
                        ) : null}
                        {activeTask.status === "review" ? (
                          <button
                            type="button"
                            onClick={async () => {
                              await taskClientFor(activeTask).completeTask(activeTask.id);
                              const fresh = { ...activeTask, status: "completed" as const };
                              setActiveTask(fresh);
                              setTasks((prev) => prev.map((t) => sameScopedTask(t, fresh) ? fresh : t));
                            }}
                            className="rounded-lg border border-emerald-400/30 bg-emerald-400/10 px-2 py-1 text-[10px] font-semibold text-emerald-700 dark:text-emerald-300 hover:bg-emerald-400/15"
                          >
                            Complete
                          </button>
                        ) : null}
                        {activeTask.status === "running" || activeTask.status === "queued" ? (
                          <button
                            type="button"
                            disabled={taskActionBusy === `stop:${activeTask.id}`}
                            onClick={() => void stopTaskFromUI(activeTask)}
                            className="rounded-lg border border-amber-400/30 bg-amber-400/10 px-2 py-1 text-[10px] font-semibold text-amber-700 hover:bg-amber-400/15 disabled:opacity-40 dark:text-amber-300"
                          >
                            {taskActionBusy === `stop:${activeTask.id}` ? "Stopping…" : "Stop"}
                          </button>
                        ) : null}
                        {!["running", "queued"].includes(activeTask.status) && !activeTask.id.startsWith("pending-cloud:") ? (
                          <button
                            type="button"
                            disabled={taskActionBusy === `delete:${activeTask.id}`}
                            onClick={() => void deleteTaskFromUI(activeTask)}
                            className="rounded-lg border border-red-400/25 px-2 py-1 text-[10px] font-semibold text-red-700 hover:bg-red-500/10 disabled:opacity-40 dark:text-red-300"
                          >
                            {taskActionBusy === `delete:${activeTask.id}` ? "Deleting…" : "Delete"}
                          </button>
                        ) : null}
                        {activeTask.costUsd != null && <span className="text-[10px] text-surface-600">${activeTask.costUsd.toFixed(3)}</span>}
                        {placementLaneLabel(activeTask.placementLane) ? (
                          <span
                            className="max-w-[180px] truncate rounded-md border border-surface-700 bg-surface-900 px-2 py-0.5 text-[10px] font-medium text-surface-300"
                            title={activeTask.placementReason || activeTask.placementCreditLabel || placementLaneLabel(activeTask.placementLane) || undefined}
                          >
                            {[
                              placementLaneLabel(activeTask.placementLane),
                              activeTask.placementCreditLabel,
                            ].filter(Boolean).join(" · ")}
                          </span>
	                        ) : null}
	                      </div>
                      {activeTask.id.startsWith("pending-cloud:") && (activeTask.pendingCloudBlockedAction || activeTask.pendingCloudBlockedReason || activeTask.status === "stopped") ? (
                        <div className="border-b border-amber-500/20 bg-amber-500/10 px-4 py-2 text-xs text-amber-900 dark:text-amber-100">
                          <div className="mx-auto flex max-w-3xl flex-wrap items-center gap-2">
                            <span className="font-semibold">
                              {activeTask.status === "stopped" ? "Remote dispatch expired" : "Needs your action"}
                            </span>
                            <span className="min-w-0 flex-1 text-amber-800/90 dark:text-amber-100/80">
                              {activeTask.pendingCloudBlockedReason || "This task is waiting for the selected remote machine."}
                              {typeof activeTask.pendingCloudExpiresAt === "number" && activeTask.status !== "stopped"
                                ? ` Expires in ~${Math.max(0, Math.ceil((activeTask.pendingCloudExpiresAt - Date.now()) / 3_600_000))}h.`
                                : ""}
                            </span>
                            {activeTask.status !== "stopped" ? (
                              <button
                                type="button"
                                onClick={() => void handlePendingCloudBlockedAction(activeTask)}
                                className="rounded-full border border-amber-400/40 bg-amber-200/40 px-3 py-1 text-[11px] font-semibold text-amber-950 hover:bg-amber-200/70 dark:bg-amber-400/10 dark:text-amber-100"
                              >
                                {activeTask.pendingCloudBlockedAction === "runner_auth_required"
                                  ? `Sign in to ${runnerLabel(activeTask.runnerId || selectedRunner)}`
                                  : activeTask.pendingCloudBlockedAction === "resize_required" ||
                                    activeTask.pendingCloudBlockedAction === "resize_failed" ||
                                    activeTask.pendingCloudBlockedAction === "wake_failed"
                                    ? "Retry"
                                    : "Open Yaver web"}
                              </button>
                            ) : null}
                          </div>
                        </div>
                      ) : null}
	                      <div
                          ref={outputRef}
                          onScroll={(e) => {
                            // Scrolling up to read disengages follow; reaching
                            // the bottom re-engages it (xterm-style).
                            const el = e.currentTarget;
                            const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 48;
                            setFollowOutput(atBottom);
                          }}
                          className="flex-1 overflow-y-auto bg-surface-950 px-4 py-5"
                        >
                        {/* NO Chat|Terminal toggle (2026-08-09, user call — same
                            as mobile): the dashboard task view is chat-only.
                            opencode's raw console look renders inside the
                            bubbles via AnsiConsoleText. */}
                        <StreamHealthNotice health={taskStreamHealth} className="mx-auto mb-4 max-w-3xl" />
                        {(() => {
                          const view = remoteAgentConversationView(activeTask);
                          const tone = view.tone === "error" ? "border-red-500/30 bg-red-500/10" : view.tone === "success" ? "border-emerald-500/30 bg-emerald-500/10" : view.tone === "attention" ? "border-amber-500/30 bg-amber-500/10" : "border-indigo-500/30 bg-indigo-500/10";
                          return <div className={`mx-auto mb-4 w-full max-w-3xl rounded-xl border px-3 py-2.5 ${tone}`}>
                            <div className="text-[10px] font-bold tracking-widest text-surface-400">{view.eyebrow}</div>
                            <div className="mt-0.5 text-sm font-semibold text-surface-100">{view.title}</div>
                            <div className="mt-1 text-xs leading-5 text-surface-400">{view.detail}</div>
                            {view.nextAction ? <div className="mt-2 text-xs font-medium text-surface-200">{view.nextAction}</div> : null}
                          </div>;
                        })()}
                        {activeRunnerAuthIssue ? (
                          <div className="mx-auto mb-4 max-w-3xl rounded-2xl border border-amber-500/30 bg-amber-500/10 p-4 text-sm text-amber-800 dark:text-amber-100">
                            <div className="font-medium">{runnerLabel(activeRunnerId)} needs sign-in on {activeTaskDeviceName}</div>
                            <div className="mt-1 text-xs leading-5 text-amber-700 dark:text-amber-200/80">{activeRunnerAuthIssue}</div>
                            {canStartBrowserRunnerAuth ? (
                              <button
                                type="button"
                                onClick={() => setChatRunnerAuthModal(activeRunnerRow!.id)}
                                className="mt-3 rounded-xl border border-amber-300/30 bg-amber-400/10 px-3 py-2 text-xs font-semibold text-amber-800 dark:text-amber-100 hover:bg-amber-400/15"
                              >
                                Sign in to {runnerLabel(activeRunnerRow?.id)}
                              </button>
                            ) : null}
                          </div>
                        ) : null}
                        {(() => {
                          const status = friendlyTaskPresentation(activeTask.presentation)
                            .filter((message) => message.kind !== "message" && message.text.trim())
                            .at(-1);
                          if (!status) return null;
                          const attention = status.kind === "error" || status.kind === "warning" || status.kind === "action_required";
                          const meta = [status.machine, status.platform, status.runner, status.project].filter(Boolean).join(" · ");
                          return (
                            <div className={`mx-auto mb-4 w-full max-w-3xl rounded-xl border px-3 py-2.5 ${attention ? "border-amber-500/30 bg-amber-500/10" : "border-surface-800 bg-surface-900/70"}`}>
                              <div className="text-sm font-medium text-surface-100">{status.text}</div>
                              {meta ? <div className="mt-1 text-[11px] text-surface-500">{meta}</div> : null}
                            </div>
                          );
                        })()}
                        {/* Semantic conversation is the default. The runner's
                            lossless terminal stream remains available below as
                            folded evidence; older agents with no presentation
                            contract retain the console compatibility branch. */}
                        {true ? (
                          chatMsgs.length === 0 ? (
                            <div className="flex h-full items-center justify-center gap-2 text-[12px] text-surface-600">
                              {(activeTask.status === "running" || activeTask.status === "queued") && <span className="h-3 w-3 animate-spin rounded-full border border-surface-500 border-t-transparent" />}
                              {activeTask.status === "running" || activeTask.status === "queued" ? (
                                <span>
                                  <span className="font-medium text-emerald-700 dark:text-emerald-300">{activeConversationLabel}</span> is working...
                                </span>
                              ) : "No messages yet"}
                            </div>
                          ) : (
                            <div className="mx-auto flex max-w-3xl flex-col gap-3">
                              {chatMsgs.map((m, i) => (
                                m.role === "user" ? (
                                  <div key={i} className="flex justify-end">
                                    <div className={`max-w-[80%] rounded-2xl rounded-br-sm px-3.5 py-2 text-[13px] text-white whitespace-pre-wrap break-words shadow-sm ${m.queued ? "bg-indigo-500/40 italic ring-1 ring-indigo-300/30" : "bg-indigo-500"}`}>
                                      {m.queued ? <span className="mr-1.5 text-[10px] uppercase tracking-wide text-indigo-800 dark:text-indigo-100/80">queued after current run</span> : null}
                                      {m.text}
                                    </div>
                                  </div>
                                ) : (
                                  // Fallback transcript only — see the
                                  // console-first block above.
                                  <div key={i} className="flex justify-start">
                                    <div className="w-full px-1 py-1 text-[12px] leading-5 text-surface-100 break-words">
                                      {m.text ? (
                                        <ChatAssistantMsg
                                          text={m.text}
                                          status={activeTask.status}
                                          isLast={i === chatMsgs.length - 1}
                                        />
                                      ) : activeTask.status === "running" || activeTask.status === "queued" ? (
                                        <span className="inline-flex items-center gap-2 text-surface-400">
                                          <span className="inline-flex items-center gap-1">
                                            <span className="h-2 w-2 animate-pulse rounded-full bg-surface-400" />
                                            <span className="h-2 w-2 animate-pulse rounded-full bg-surface-400 [animation-delay:200ms]" />
                                            <span className="h-2 w-2 animate-pulse rounded-full bg-surface-400 [animation-delay:400ms]" />
                                          </span>
                                          <span className="text-[11px] tracking-wide">
                                            {activeTask.status === "queued"
                                              ? "Waiting for the current run slot..."
                                              : `${activeConversationLabel} is thinking...`}
                                          </span>
                                        </span>
                                      ) : (
                                        <span className="text-surface-500">({activeTask.status || "no response"})</span>
                                      )}
                                    </div>
                                  </div>
                                )
                              ))}
                            </div>
                          )
                        ) : (() => {
                          // Console-first task output: the raw runner stream
                          // painted as-is via AnsiConsoleText, but SUMMARIZED
                          // with the same deterministic reducer mobile uses
                          // (shared _core/ansi) so megabytes of tool noise
                          // don't bury the answer — web budgets are larger
                          // than mobile's because the screen is. A compact
                          // header turns a long stream into a document, and
                          // the follow pill restores tail-tracking after the
                          // user scrolls up to read (xterm-style).
                          const rawJoined = rawOutput.join("\n");
                          // This compatibility branch is nested in an IIFE, so
                          // TypeScript does not retain the outer activeTask
                          // narrowing. Keep the read null-safe even though the
                          // surrounding task panel only mounts for a task.
                          const isRunning = activeTask?.status === "running" || activeTask?.status === "queued";
                          const consoleText = summarizeRawConsole(rawJoined, isRunning, {
                            // Running budget sized so the FIRST `> build`
                            // banner survives summarization: interleaveConsole-
                            // Prompts pairs prompt[i] with the i-th banner-led
                            // response segment, so evicting banner 1 would put
                            // "helo" AFTER its own reply. 300 lines ≈ 3-4
                            // screens of 12px mono on a desktop panel — still
                            // bounded, but normal tasks never evict the head.
                            budgetLines: isRunning ? 300 : 800,
                            budgetChars: isRunning ? 64 * 1024 : 256 * 1024,
                          });
                          const rawLines = rawJoined.split("\n").length;
                          const rawKb = (rawJoined.length / 1024).toFixed(1);
                          return (
                            <div className="mx-auto max-w-3xl">
                              <div className="mb-2 flex flex-wrap items-center gap-x-2 gap-y-1 border-b border-surface-800 pb-1.5 text-[10px] uppercase tracking-widest text-surface-500">
                                <span className="font-semibold text-surface-300">{runnerLabel(activeRunnerId)}</span>
                                {activeTask?.model ? (
                                  <span className="normal-case tracking-normal text-surface-400">{activeTask?.model}</span>
                                ) : null}
                                <span>{rawLines} lines · {rawKb} KB</span>
                                {isRunning ? (
                                  <span className="inline-flex items-center gap-1 text-emerald-600 dark:text-emerald-400">
                                    <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-emerald-500" />
                                    live
                                  </span>
                                ) : null}
                              </div>
                              {/* User prompts interleaved with the response
                                  segment each one triggered (2026-08-13 user
                                  call: every prompt used to render at the TOP
                                  of the console, so a two-turn task read
                                  "all my messages, then all replies" instead
                                  of a conversation). The console is split at
                                  the `> build · <model>` banner lines and
                                  each prompt sits directly above the
                                  response that follows it — chat order, same
                                  flow as the runner's own console. Prompts
                                  stay ALWAYS visible (2026-08-12 report: a
                                  follow-up sent while the task was streaming
                                  vanished completely); a queued follow-up
                                  with no response yet renders after the last
                                  segment, never dropped. */}
                              {interleaveConsolePrompts(
                                consoleText,
                                chatMsgs.filter((m) => m.role === "user").map((m) => m.text),
                              ).map((block, i) =>
                                block.kind === "prompt" ? (
                                  <div key={`prompt-${i}`} className="whitespace-pre-wrap break-words py-0.5 font-mono text-[12px] leading-5 text-emerald-700 dark:text-emerald-300">
                                    <span className="select-none text-surface-600">$ </span>{block.text}
                                  </div>
                                ) : (
                                  <AnsiConsoleText key={`console-${i}`} text={block.text} />
                                ),
                              )}
                              {isRunning ? (
                                // opencode-style working indicator — the purple
                                // gradient orb opencode shows bottom-left while
                                // the model is thinking. Pulsing conic-gradient
                                // ring + breathing dot, so a streamed run reads
                                // as "alive" at a glance even when stdout is
                                // between bursts (2026-08-12 user request).
                                <span className="relative ml-0.5 inline-flex h-4 w-4 items-center justify-center align-[-1px]">
                                  <span className="absolute inset-0 animate-spin rounded-full [background:conic-gradient(from_0deg,#a855f7,#ec4899,#a855f7)] [animation-duration:1.4s]" />
                                  <span className="absolute inset-[3px] animate-pulse rounded-full bg-[#0b0d11]" />
                                </span>
                              ) : null}
                              {!followOutput ? (
                                <div className="sticky bottom-2 mt-2 flex justify-center">
                                  <button
                                    type="button"
                                    onClick={() => { setFollowOutput(true); if (outputRef.current) outputRef.current.scrollTop = outputRef.current.scrollHeight; }}
                                    className="rounded-full border border-surface-700 bg-surface-900/95 px-3 py-1 text-[10px] font-semibold uppercase tracking-widest text-surface-300 shadow-md transition-colors hover:border-emerald-500/50 hover:text-emerald-300"
                                  >
                                    ↓ follow output
                                  </button>
                                </div>
                              ) : null}
                            </div>
                          );
                        })()}
                        {rawOutput.length > 0 ? (
                          <details className="mx-auto mt-4 w-full max-w-3xl rounded-xl border border-surface-800 bg-surface-950/70">
                            <summary className="cursor-pointer select-none px-3 py-2 text-xs font-medium text-surface-400 hover:text-surface-200">
                              Runner details · {rawOutput.join("\n").length > 1024 ? `${(rawOutput.join("\n").length / 1024).toFixed(1)} KB` : `${rawOutput.join("\n").length} B`}
                            </summary>
                            <div className="max-h-[28rem] overflow-auto border-t border-surface-800 p-3">
                              <AnsiConsoleText text={summarizeRawConsole(rawOutput.join("\n"), activeTask?.status === "running" || activeTask?.status === "queued", { budgetLines: 500, budgetChars: 128 * 1024 })} />
                            </div>
                          </details>
                        ) : null}
                        {/* Task-proof card (audit §9.4, B14): the SAME shared
                            component VibeCodingView mounts, so the two web
                            chat surfaces can't drift. Renders under the
                            console once the task lands in completed/review
                            with a proof or demo clip attached. */}
                        {taskProofVisible(activeTask) ? (
                          <div className="mx-auto mt-3 max-w-3xl">
                            <TaskProofCard task={activeTask} agentClient={taskClientFor(activeTask)} />
                          </div>
                        ) : null}
                      </div>
                    </>
                  ) : (
                    <div className="flex flex-1 items-center justify-center text-sm text-surface-400">Describe what you want to build</div>
                  )}
                </div>
              </div>
              <div className="border-t border-surface-800 bg-surface-900/50 px-4 py-3">
                <div className="mx-auto flex max-w-5xl flex-col gap-3">
                  {(() => {
                    const installed = chatRunnerChoices;
                    const selectedRunnerRow = installed.find((r) => r.id === selectedRunner) || null;
                    const selectedRunnerModels = runnerModelOptions(selectedRunnerRow, selectedRunner);
                    if (installed.length === 0) {
                      return (
                        <div className="text-[11px] text-amber-400">
                          No AI runner installed on this machine. Install one of <span className="font-mono">claude</span>, <span className="font-mono">codex</span>, or <span className="font-mono">opencode</span> and reconnect.
                        </div>
                      );
                    }
                    // Collapsed summary row — single line, ~28px tall.
                    // Shows the active selections so the user can confirm
                    // at a glance and click "Edit" to open the full picker.
                    if (!chatPickerExpanded) {
                      const runnerName = runnerLabel(selectedRunner) || selectedRunner || "—";
                      const providerEntry = selectedRunner === "opencode"
                        ? openCodeCatalogue.find((provider) => provider.id === opencodeProvider) || null
                        : null;
                      const modelDisplay = (() => {
                        const sm = selectedModel || "";
                        if (!sm) return "default model";
                        const slash = sm.indexOf("/");
                        const tail = slash >= 0 ? sm.slice(slash + 1) : sm;
                        if (selectedRunner === "opencode" && providerEntry) {
                          const m = providerEntry.models.find((mm) => mm.id === tail);
                          return m?.label || tail;
                        }
                        const m = selectedRunnerModels.find((mm) => mm.id === sm);
                        return m?.name || sm;
                      })();
                      const modeDisplay = selectedRunner === "opencode"
                        ? (selectedOpenCodeMode ? selectedOpenCodeMode.charAt(0).toUpperCase() + selectedOpenCodeMode.slice(1) : "Default")
                        : null;
                      // The provider chip duplicates the model label when the
                      // label already names the provider ("DeepSeek V4 Flash"
                      // ⊃ "DeepSeek") — suppress it then, keep the model chip.
                      const providerLabel = providerEntry?.label || "";
                      const providerDupInModel = !!providerLabel
                        && modelDisplay.toLowerCase().includes(providerLabel.toLowerCase());
                      return (
                        <div className="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-surface-800 bg-surface-950/60 px-3 py-2 text-[11px] text-surface-400">
                          <div className="flex flex-wrap items-center gap-x-1.5 gap-y-1">
                            <span className="text-surface-500">Agent</span>
                            <span className="inline-flex items-center gap-1.5 rounded-full border border-emerald-400/60 bg-emerald-400/10 px-2 py-0.5 text-emerald-800 dark:text-emerald-200" title={`${runnerName} is the active agent for this chat.`}>
                              <span className="h-1.5 w-1.5 rounded-full bg-emerald-400" />
                              {runnerName}
                            </span>
                            {providerEntry && !providerDupInModel ? (
                              <>
                                <span className="text-surface-700">·</span>
                                <span className="rounded-full border border-cyan-400/30 bg-cyan-400/5 px-2 py-0.5 text-cyan-800 dark:text-cyan-100">{providerEntry.label}</span>
                              </>
                            ) : null}
                            <span className="text-surface-700">·</span>
                            <span className="rounded-full border border-fuchsia-400/30 bg-fuchsia-400/5 px-2 py-0.5 text-fuchsia-800 dark:text-fuchsia-100">{modelDisplay}</span>
                            {modeDisplay ? (
                              <>
                                <span className="text-surface-700">·</span>
                                <span className="rounded-full border border-emerald-400/30 bg-emerald-400/5 px-2 py-0.5 text-emerald-800 dark:text-emerald-100">{modeDisplay}</span>
                              </>
                            ) : null}
                          </div>
                          {selectedRunner === "opencode" ? (
                            <div className="flex items-center gap-1.5">
                              {/* Compact Build|Plan segmented control — the
                                  common modes, one tap, persisted the same
                                  way the expanded picker's Save does. Custom
                                  agents / Default mode stay behind ⋯. */}
                              <div className="flex overflow-hidden rounded-lg border border-surface-700 text-[11px] font-semibold">
                                {(["build", "plan"] as const).map((mode) => (
                                  <button
                                    key={mode}
                                    type="button"
                                    onClick={() => {
                                      setSelectedOpenCodeMode(mode);
                                      if (connectedDevice?.id) {
                                        void setPrimaryRunner(
                                          connectedDevice.id,
                                          "opencode",
                                          selectedModel || null,
                                          mode,
                                          opencodeProvider || null,
                                        ).catch(() => {});
                                      }
                                    }}
                                    className={selectedOpenCodeMode === mode
                                      ? "bg-emerald-500 px-3 py-1 text-white"
                                      : "bg-surface-950/60 px-3 py-1 text-surface-400 hover:text-surface-200"}
                                    title={`Run opencode in ${mode} mode`}
                                  >
                                    {mode === "build" ? "Build" : "Plan"}
                                  </button>
                                ))}
                              </div>
                              <button
                                type="button"
                                onClick={() => setChatPickerExpanded(true)}
                                className="rounded-lg border border-surface-700 bg-surface-900 px-2 py-1 text-[11px] text-surface-300 hover:border-surface-500"
                                title="Edit agent, provider, model, mode, or custom agent"
                              >
                                ⋯
                              </button>
                            </div>
                          ) : (
                            <button
                              type="button"
                              onClick={() => setChatPickerExpanded(true)}
                              className="rounded-lg border border-surface-700 bg-surface-900 px-2.5 py-1 text-[11px] text-surface-300 hover:border-surface-500"
                              title="Edit agent, provider, model, and mode"
                            >
                              Edit ▾
                            </button>
                          )}
                        </div>
                      );
                    }
                    return (
                      <div className="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-surface-800 bg-surface-950/60 px-3 py-2">
                        <div className="flex flex-wrap items-center gap-2 text-[11px] text-surface-500">
                          {chatRunnerChoices.length > 0 ? (
                            <>
                              <span>Agent</span>
                              <div className="flex flex-wrap items-center gap-1.5">
                                {chatRunnerChoices.map((runner) => {
                                  const active = runner.id === selectedRunner;
                                  return (
                                    <button
                                      key={runner.id}
                                      type="button"
                                      onClick={() => {
                                        const nextModels = runnerModelOptions(runner, runner.id);
                                        const nextModel =
                                          (selectedModel && nextModels.some((m) => m.id === selectedModel) ? selectedModel : "") ||
                                          nextModels.find((m) => m.isDefault)?.id ||
                                          nextModels[0]?.id ||
                                          "";
                                        setSelectedRunner(runner.id);
                                        if (runner.id !== "opencode") setSelectedModel(nextModel);
                                        setOpencodeSaveMsg(null);
                                        setOpencodeChangingKey(false);
                                        if (connectedDevice?.id) {
                                          void setPrimaryRunner(
                                            connectedDevice.id,
                                            runner.id,
                                            runner.id === "opencode" ? selectedModel || null : nextModel || null,
                                            runner.id === "opencode" ? selectedOpenCodeMode || null : null,
                                            runner.id === "opencode" ? opencodeProvider || null : null,
                                          ).catch((err: any) => setConnectError(err?.message || "Failed to save agent selection"));
                                        }
                                      }}
                                      title={runner.error || runner.warning || runner.name}
                                      className={`rounded-full border px-2.5 py-1 transition ${
                                        active
                                          ? "border-emerald-400/70 bg-emerald-400/10 text-emerald-800 dark:text-emerald-200"
                                          : "border-surface-700 bg-surface-900 text-surface-300 hover:border-surface-500"
                                      }`}
                                    >
                                      {active ? (
                                        <span className="inline-flex items-center gap-1.5">
                                          <span className="h-1.5 w-1.5 rounded-full bg-emerald-400" />
                                          <span className="font-semibold">{runner.name}</span>
                                          <span className="text-[9px] font-semibold uppercase tracking-wider text-emerald-700 dark:text-emerald-300">active</span>
                                        </span>
                                      ) : (
                                        runner.name
                                      )}
                                    </button>
                                  );
                                })}
                              </div>
                            </>
                          ) : null}
                          {selectedRunner !== "opencode" && selectedRunnerModels.length > 0 ? (
                            <>
                              <span>Model</span>
                              <div className="flex flex-wrap items-center gap-1.5">
                                {selectedRunnerModels.map((model) => {
                                  const active = model.id === selectedModel;
                                  return (
                                    <button
                                      key={model.id}
                                      type="button"
                                      onClick={() => {
                                        setSelectedModel(model.id);
                                        if (connectedDevice?.id) {
                                          void setPrimaryRunner(
                                            connectedDevice.id,
                                            selectedRunner || null,
                                            model.id,
                                          ).catch((err: any) => setConnectError(err?.message || "Failed to save model selection"));
                                        }
                                      }}
                                      title={model.description || model.id}
                                      className={`rounded-full border px-2.5 py-1 transition ${
                                        active
                                          ? "border-fuchsia-400/60 bg-fuchsia-400/10 text-fuchsia-800 dark:text-fuchsia-100"
                                          : "border-surface-700 bg-surface-900 text-surface-300 hover:border-surface-500"
                                      }`}
                                    >
                                      {model.name}
                                    </button>
                                  );
                                })}
                              </div>
                            </>
                          ) : null}
                          {activeRunnerId ? (
                            <span className="inline-flex items-center gap-1.5">
                              <span className="text-surface-500">Active:</span>
                              <span className="inline-flex items-center gap-1.5 rounded-full border border-emerald-400/60 bg-emerald-400/10 px-2 py-0.5 font-medium text-emerald-800 dark:text-emerald-200">
                                <span className="h-1.5 w-1.5 rounded-full bg-emerald-400" />
                                {runnerLabel(activeRunnerId)}
                              </span>
                            </span>
                          ) : null}
                        </div>
                        <button
                          type="button"
                          onClick={() => setChatPickerExpanded(false)}
                          className="rounded-lg border border-surface-700 bg-surface-900 px-2.5 py-1 text-[11px] text-surface-300 hover:border-surface-500"
                          title="Collapse picker"
                        >
                          Hide ▴
                        </button>
                      </div>
                    );
                  })()}
                  {/* OpenCode provider + model + key picker. Shows up
                      whenever the user has OpenCode selected as the
                      runner. Keyless providers need no secret; BYOK keys go
                      straight to OpenCode's machine-local auth store (and,
                      for custom providers, its local provider config).
                      Nothing secret is sent to Convex. */}
                  {chatPickerExpanded && selectedRunner === "opencode" ? (() => {
                    const provider = openCodeCatalogue.find((row) => row.id === opencodeProvider) || null;
                    if (!provider) {
                      return (
                        <div className="rounded-xl border border-surface-800 bg-surface-950/60 px-3 py-3 text-[11px] text-surface-400">
                          {isConnected ? "Loading this machine’s OpenCode providers…" : "Connect to the machine to load its OpenCode providers."}
                        </div>
                      );
                    }
                    const currentModelId = (() => {
                      const sm = selectedModel || "";
                      if (!sm) return "";
                      const slash = sm.indexOf("/");
                      return slash >= 0 ? sm.slice(slash + 1) : sm;
                    })();
                    const handleSaveOpenCode = async () => {
                      if (provider.requiresKey && !opencodeApiKey.trim()) {
                        setOpencodeSaveMsg({ ok: false, text: `${provider.label} needs an API key.` });
                        return;
                      }
                      const modelId = currentModelId || provider.models[0]?.id || "";
                      if (!modelId) return;
                      const fullModel = `${provider.id}/${modelId}`;
                      setOpencodeSaving(true);
                      setOpencodeSaveMsg(null);
                      try {
                        const patch: Parameters<typeof agentClient.saveOpenCodeConfig>[0] = {
                          model: fullModel,
                          providers: [
                            {
                              id: provider.id,
                              name: provider.label,
                              ...(!provider.isBuiltin && provider.baseUrl ? { baseUrl: provider.baseUrl } : {}),
                              ...(opencodeApiKey.trim() ? { apiKey: opencodeApiKey.trim() } : {}),
                              ...(provider.isBuiltin ? {} : { models: { [modelId]: {} } }),
                            },
                          ],
                        };
                        const res = await agentClient.saveOpenCodeConfig(patch);
                        if (!res.ok) {
                          setOpencodeSaveMsg({ ok: false, text: res.error || "Save failed" });
                        } else {
                          setSelectedModel(fullModel);
                          setOpencodeApiKey("");
                          setOpencodeChangingKey(false);
                          // Optimistically flip the indicator so the
                          // user sees the "✓ Key configured" badge
                          // immediately, then reconcile with the
                          // agent's view to catch the case where the
                          // patch only updated baseUrl/model and not
                          // the key.
                          if (provider.requiresKey && opencodeApiKey.trim()) {
                            setOpencodeKeyState((prev) => ({ ...prev, [provider.id]: true }));
                          }
                          if (connectedDevice?.id) {
                            void setPrimaryRunner(
                              connectedDevice.id,
                              "opencode",
                              fullModel,
                              selectedOpenCodeMode || null,
                              provider.id,
                            ).catch(() => {});
                          }
                          setOpenCodeAgents(
                            Array.isArray(res.config?.agents)
                              ? res.config!.agents
                                  .map((agent) => ({
                                    name: String(agent?.name || "").trim(),
                                    model: typeof agent?.model === "string" ? agent.model : undefined,
                                    isBuiltin: !!agent?.isBuiltin,
                                  }))
                                  .filter((agent) => agent.name.length > 0)
                              : [],
                          );
                          void refreshOpencodeKeyState();
                          void refreshConnectedRunners();
                          void refreshDevices();
                          setOpencodeSaveMsg({ ok: true, text: `Saved. Using ${provider.label} · ${modelId}.` });
                        }
                      } catch (err: any) {
                        setOpencodeSaveMsg({ ok: false, text: err?.message || "Save failed" });
                      } finally {
                        setOpencodeSaving(false);
                      }
                    };
                    return (
                      <div className="rounded-xl border border-surface-800 bg-surface-950/60 px-3 py-3">
                        <div className="grid gap-2 text-[11px] sm:grid-cols-[80px_minmax(0,1fr)] sm:items-center">
                          <label htmlFor="chat-opencode-provider" className="text-surface-500">Provider</label>
                          <select
                            id="chat-opencode-provider"
                            value={provider.id}
                            onChange={(event) => {
                              const id = event.target.value;
                              setOpencodeProvider(id);
                              setOpencodeApiKey("");
                              setOpencodeChangingKey(false);
                              setOpencodeSaveMsg(null);
                              const next = openCodeCatalogue.find((row) => row.id === id);
                              const model = next?.models[0];
                              if (model) setSelectedModel(`${id}/${model.id}`);
                            }}
                            className="min-w-0 rounded-lg border border-surface-700 bg-surface-950 px-3 py-2 text-surface-100 outline-none focus:border-cyan-400/60"
                          >
                            {openCodeCatalogue.map((row) => (
                              <option key={row.id} value={row.id}>{row.label}</option>
                            ))}
                          </select>
                        </div>
                        <p className="mt-2 text-[11px] text-surface-500">{provider.blurb}</p>
                        <div className="mt-3 grid gap-2 text-[11px] sm:grid-cols-[80px_minmax(0,1fr)] sm:items-center">
                          <label htmlFor="chat-opencode-model" className="text-surface-500">Model</label>
                          {provider.models.length > 0 ? (
                            <select
                              id="chat-opencode-model"
                              value={selectedModel}
                              onChange={(event) => {
                                const fullID = event.target.value;
                                setSelectedModel(fullID);
                                if (connectedDevice?.id) {
                                  void setPrimaryRunner(
                                    connectedDevice.id,
                                    "opencode",
                                    fullID,
                                    selectedOpenCodeMode || null,
                                    provider.id,
                                  ).catch((err: any) => setConnectError(err?.message || "Failed to save model selection"));
                                }
                              }}
                              className="min-w-0 rounded-lg border border-surface-700 bg-surface-950 px-3 py-2 text-surface-100 outline-none focus:border-fuchsia-400/60"
                            >
                              {provider.models.map((model) => (
                                <option key={model.id} value={`${provider.id}/${model.id}`}>{model.label}</option>
                              ))}
                            </select>
                          ) : (
                            <span className="text-surface-500">Loading models from OpenCode…</span>
                          )}
                        </div>
                        <div className="mt-3 flex flex-wrap items-center gap-2 text-[11px]">
                          <span className="text-surface-500">Mode</span>
                          {[{ name: "", isBuiltin: true } as OpenCodeAgentRow]
                            .concat(
                              openCodeAgents.length > 0
                                ? openCodeAgents
                                : [
                                    { name: "build", isBuiltin: true },
                                    { name: "plan", isBuiltin: true },
                                  ],
                            )
                            .map((agent) => {
                              const id = agent.name;
                              const label = id === "" ? "Default" : id.charAt(0).toUpperCase() + id.slice(1);
                              return (
                                <button
                                  key={id || "default"}
                                  type="button"
                                  onClick={() => {
                                    setSelectedOpenCodeMode(id);
                                    if (connectedDevice?.id) {
                                      void setPrimaryRunner(
                                        connectedDevice.id,
                                        "opencode",
                                        selectedModel || null,
                                        id || null,
                                        opencodeProvider || null,
                                      ).catch(() => {});
                                    }
                                  }}
                                  title={
                                    id === ""
                                      ? "Use defaultAgent from opencode.json"
                                      : agent.model
                                        ? `Run with --agent ${id} (${agent.model})`
                                        : `Run with --agent ${id}`
                                  }
                                  className={`rounded-full border px-2.5 py-1 transition ${
                                    selectedOpenCodeMode === id
                                      ? "border-emerald-400/60 bg-emerald-400/10 text-emerald-800 dark:text-emerald-100"
                                      : "border-surface-700 bg-surface-900 text-surface-300 hover:border-surface-500"
                                  } ${!agent.isBuiltin && id !== "" ? "italic" : ""}`}
                                >
                                  {label}
                                </button>
                              );
                            })}
                        </div>
                        {provider.requiresKey ? (() => {
                          const keyAlreadySaved = !!opencodeKeyState[provider.id];
                          // Show the input when (a) no key has been
                          // saved yet for this provider on the agent,
                          // or (b) the user explicitly clicked "Change
                          // key" to replace it. Otherwise just render
                          // the "✓ Key configured · Change" badge so
                          // the user can re-pick the model and start
                          // tasks without re-pasting the secret.
                          const showInput = !keyAlreadySaved || opencodeChangingKey;
                          if (!showInput) {
                            return (
                              <div className="mt-3 flex flex-wrap items-center gap-2">
                                <span className="rounded-full border border-emerald-500/40 bg-emerald-500/10 px-2.5 py-1 text-[11px] font-semibold text-emerald-700 dark:text-emerald-200">
                                  ✓ {provider.keyEnv || "Key"} configured on this device
                                </span>
                                <button
                                  type="button"
                                  onClick={() => {
                                    setOpencodeApiKey("");
                                    setOpencodeChangingKey(true);
                                    setOpencodeSaveMsg(null);
                                  }}
                                  className="rounded-lg border border-surface-700 bg-surface-900 px-2.5 py-1 text-[11px] text-surface-300 hover:border-surface-500"
                                  title="Replace the saved API key on this device. Read-only key state comes from OpenCode on the agent; the key never enters Convex."
                                >
                                  Change key
                                </button>
                                <button
                                  type="button"
                                  onClick={handleSaveOpenCode}
                                  disabled={opencodeSaving}
                                  className="rounded-lg border border-cyan-400/40 bg-cyan-400/10 px-3 py-1.5 text-[11px] font-semibold text-cyan-800 dark:text-cyan-100 hover:bg-cyan-400/20 disabled:opacity-50"
                                  title="Use this provider + model for the next task without changing the saved key."
                                >
                                  {opencodeSaving ? "Saving…" : "Use this provider"}
                                </button>
                              </div>
                            );
                          }
                          return (
                            <div className="mt-3 flex flex-wrap items-center gap-2">
                              <span className="text-[11px] text-surface-500">{provider.keyEnv || "API key"}</span>
                              <input
                                type="password"
                                value={opencodeApiKey}
                                onChange={(e) => setOpencodeApiKey(e.target.value)}
                                placeholder="sk-…"
                                autoComplete="off"
                                className="min-w-[220px] flex-1 rounded-lg border border-surface-700 bg-surface-950 px-3 py-1.5 text-[12px] font-mono text-surface-100 placeholder-surface-600 outline-none focus:border-surface-500"
                              />
                              <button
                                type="button"
                                onClick={handleSaveOpenCode}
                                disabled={opencodeSaving}
                                className="rounded-lg border border-cyan-400/40 bg-cyan-400/10 px-3 py-1.5 text-[11px] font-semibold text-cyan-800 dark:text-cyan-100 hover:bg-cyan-400/20 disabled:opacity-50"
                              >
                                {opencodeSaving ? "Saving…" : "Save key + use"}
                              </button>
                              {keyAlreadySaved && opencodeChangingKey ? (
                                <button
                                  type="button"
                                  onClick={() => {
                                    setOpencodeApiKey("");
                                    setOpencodeChangingKey(false);
                                    setOpencodeSaveMsg(null);
                                  }}
                                  className="rounded-lg border border-surface-700 bg-surface-900 px-2.5 py-1 text-[11px] text-surface-400 hover:border-surface-500"
                                  title="Cancel — keep the previously saved key."
                                >
                                  Cancel
                                </button>
                              ) : null}
                            </div>
                          );
                        })() : (
                          <div className="mt-3 flex flex-wrap items-center gap-2">
                            <span className="text-[11px] text-surface-500">No key needed.</span>
                            <button
                              type="button"
                              onClick={handleSaveOpenCode}
                              disabled={opencodeSaving}
                              className="rounded-lg border border-cyan-400/40 bg-cyan-400/10 px-3 py-1.5 text-[11px] font-semibold text-cyan-800 dark:text-cyan-100 hover:bg-cyan-400/20 disabled:opacity-50"
                            >
                              {opencodeSaving ? "Saving…" : "Use Ollama"}
                            </button>
                          </div>
                        )}
                        {opencodeSaveMsg ? (
                          <p className={`mt-2 text-[11px] ${opencodeSaveMsg.ok ? "text-emerald-700 dark:text-emerald-300" : "text-amber-700 dark:text-amber-300"}`}>
                            {opencodeSaveMsg.text}
                          </p>
                        ) : null}
                      </div>
                    );
                  })() : null}
                  {activeRunnerAuthIssue ? (
                    <div className="rounded-xl border border-amber-500/20 bg-amber-500/5 px-3 py-2 text-[11px] text-amber-700 dark:text-amber-200">
                      <span>{runnerLabel(activeRunnerId)} on {activeTaskDeviceName} is not authenticated.</span>
                      {canStartBrowserRunnerAuth ? (
                        <button
                          type="button"
                          onClick={() => setChatRunnerAuthModal(activeRunnerRow!.id)}
                          className="ml-2 rounded-lg border border-amber-400/30 px-2.5 py-1 font-semibold text-amber-800 dark:text-amber-100 hover:bg-amber-400/10"
                        >
                          Sign in
                        </button>
                      ) : null}
                    </div>
                  ) : null}
                  {preferredSurfaceProjectPath ? (
                    <div className="flex flex-wrap items-center gap-2 rounded-xl border border-fuchsia-500/20 bg-fuchsia-500/5 px-3 py-2 text-[11px] text-fuchsia-800 dark:text-fuchsia-100">
                      <span className="font-semibold uppercase tracking-[0.18em] text-fuchsia-700 dark:text-fuchsia-200/80">Repo</span>
                      <span className="font-mono text-fuchsia-50">{preferredSurfaceProjectPath}</span>
                    </div>
                  ) : null}
                  {multiMachineTaskBanner ? (
                    <div className="rounded-xl border border-emerald-500/20 bg-emerald-500/5 px-3 py-2 text-[11px] text-emerald-700 dark:text-emerald-200">
                      {multiMachineTaskBanner}
                    </div>
                  ) : null}
                  {runnerControlMode && activeTask ? (() => {
                    const selected = runnerControlCatalog?.models.find((item) => item.id === runnerControlModel);
                    const efforts = selected?.supportedReasoningEfforts || [];
                    return (
                      <section className="rounded-xl border border-indigo-400/30 bg-indigo-500/5 p-3" aria-label={`/${runnerControlMode} runner control`}>
                        <div className="flex items-start justify-between gap-3">
                          <div>
                            <div className="text-[10px] font-bold uppercase tracking-[0.16em] text-indigo-700 dark:text-indigo-300">/{runnerControlMode}</div>
                            <div className="mt-0.5 text-sm font-semibold text-surface-100">
                              {runnerControlMode === "exit" ? "Exit this runner session?" : runnerControlStep === "effort" ? `Reasoning for ${runnerControlModel}` : "Choose this conversation’s model"}
                            </div>
                            <div className="mt-1 text-[11px] text-surface-500">
                              {runnerControlMode === "exit"
                                ? "The live runner seat stops; this readable conversation stays in history."
                                : runnerControlCatalog ? `${runnerControlCatalog.runnerId} · ${runnerControlCatalog.modelSource || "this machine"}` : "Checking the task’s machine…"}
                            </div>
                          </div>
                          <button type="button" onClick={() => setRunnerControlMode(null)} className="text-lg leading-none text-surface-500 hover:text-surface-200" aria-label="Close runner control">×</button>
                        </div>
                        {runnerControlBusy && !runnerControlCatalog ? <div className="mt-3 text-xs text-surface-400">Loading live catalog…</div> : null}
                        {runnerControlError ? <div className="mt-3 rounded-lg border border-red-500/25 bg-red-500/10 px-3 py-2 text-xs text-red-700 dark:text-red-200">{runnerControlError}</div> : null}
                        {runnerControlMode === "model" && runnerControlCatalog?.isAdopted ? (
                          <div className="mt-3 rounded-lg border border-amber-500/25 bg-amber-500/10 px-3 py-2 text-xs text-amber-800 dark:text-amber-200">
                            This attached terminal uses the real runner catalog. Yaver will not report a model change unless that live runner can confirm it; use Runner details for this seat.
                          </div>
                        ) : null}
                        {runnerControlMode === "model" && runnerControlCatalog && runnerControlStep === "models" ? (
                          <div className="mt-3 grid max-h-64 gap-2 overflow-y-auto sm:grid-cols-2">
                            {runnerControlCatalog.models.map((item) => (
                              <button key={item.id} type="button" onClick={() => selectTaskRunnerModel(item)} className={`rounded-lg border px-3 py-2 text-left ${item.id === runnerControlModel ? "border-indigo-400/60 bg-indigo-400/10" : "border-surface-700 bg-surface-950 hover:border-surface-500"}`}>
                                <div className="text-xs font-semibold text-surface-100">{item.name || item.id}</div>
                                <div className="mt-0.5 truncate text-[10px] text-surface-500">{item.id}{item.id === runnerControlCatalog.model ? " · current" : ""}</div>
                              </button>
                            ))}
                          </div>
                        ) : null}
                        {runnerControlMode === "model" && runnerControlCatalog && runnerControlStep === "effort" ? (
                          <div className="mt-3">
                            <div className="grid gap-2 sm:grid-cols-2">
                              {efforts.map((item) => (
                                <button key={item.reasoningEffort} type="button" onClick={() => setRunnerControlEffort(item.reasoningEffort)} className={`rounded-lg border px-3 py-2 text-left ${item.reasoningEffort === runnerControlEffort ? "border-indigo-400/60 bg-indigo-400/10" : "border-surface-700 bg-surface-950 hover:border-surface-500"}`}>
                                  <div className="text-xs font-semibold capitalize text-surface-100">{item.reasoningEffort === "xhigh" ? "Extra high" : item.reasoningEffort}</div>
                                  {item.description ? <div className="mt-0.5 text-[10px] text-surface-500">{item.description}</div> : null}
                                </button>
                              ))}
                            </div>
                            <button type="button" onClick={() => setRunnerControlStep("models")} className="mt-2 text-[11px] font-semibold text-surface-400 hover:text-surface-200">← Models</button>
                          </div>
                        ) : null}
                        {runnerControlMode === "model" && runnerControlCatalog ? (
                          <button type="button" disabled={runnerControlBusy || !runnerControlModel || runnerControlCatalog.isAdopted} onClick={() => void applyTaskRunnerModel()} className="mt-3 rounded-lg bg-indigo-500 px-3 py-2 text-xs font-semibold text-white hover:bg-indigo-400 disabled:opacity-40">
                            {runnerControlBusy ? "Applying…" : runnerControlCatalog.runnerId === "codex" ? `Use ${runnerControlModel} · ${runnerControlEffort}` : `Use ${selected?.name || runnerControlModel}`}
                          </button>
                        ) : null}
                        {runnerControlMode === "exit" ? (
                          <div className="mt-3 flex gap-2">
                            <button type="button" disabled={runnerControlBusy} onClick={() => setRunnerControlMode(null)} className="rounded-lg border border-surface-700 px-3 py-2 text-xs font-semibold text-surface-300">Keep session</button>
                            <button type="button" disabled={runnerControlBusy} onClick={() => void confirmTaskRunnerExit()} className="rounded-lg bg-red-600 px-3 py-2 text-xs font-semibold text-white hover:bg-red-500 disabled:opacity-40">{runnerControlBusy ? "Exiting…" : "Exit session"}</button>
                          </div>
                        ) : null}
                      </section>
                    );
                  })() : null}
                  {agentQuestion && agentQuestion.taskId === activeTask?.id ? (
                    <div className="rounded-xl border border-amber-500/30 bg-amber-500/5 p-3">
                      <div className="text-[10px] font-semibold uppercase tracking-[0.18em] text-amber-700 dark:text-amber-200/80">
                        Agent needs your input
                      </div>
                      {agentQuestion.step ? (
                        <div className="mt-2 inline-block rounded bg-surface-700/40 px-2 py-0.5 text-[10px] font-bold tracking-wide text-surface-300">
                          {"⛳ " + String(agentQuestion.step).replace(/_/g, " ")}
                        </div>
                      ) : null}
                      {agentQuestion.screenshot ? (
                        // F3 handoff: show the relevant page region
                        // eslint-disable-next-line @next/next/no-img-element
                        <img
                          src={"data:image/png;base64," + agentQuestion.screenshot}
                          alt="page region needing your input"
                          className="mt-2 w-full rounded-lg border border-surface-700/40 bg-black"
                          style={{ maxHeight: 240, objectFit: "contain" }}
                        />
                      ) : null}
                      <div className="mt-2 text-sm text-surface-100 whitespace-pre-wrap">
                        {agentQuestion.prompt}
                      </div>
                      {agentQuestion.kind === "choice" && (agentQuestion.choices || []).length > 0 ? (
                        <div className="mt-3 flex flex-wrap gap-2">
                          {(agentQuestion.choices || []).map((choice) => (
                            <button
                              key={choice}
                              type="button"
                              disabled={submittingAgentAnswer}
                              onClick={async () => {
                                if (!agentQuestion) return;
                                setSubmittingAgentAnswer(true);
                                const res = await taskClientFor(activeTask).answerTaskQuestion(agentQuestion.taskId, agentQuestion.id, choice);
                                setSubmittingAgentAnswer(false);
                                if (!res.ok) {
                                  alert("Could not deliver answer: " + (res.error || "Unknown error"));
                                  return;
                                }
                                setAgentQuestion(null);
                              }}
                              className="rounded-lg border border-amber-400/30 bg-amber-400/10 px-3 py-1.5 text-xs text-amber-50 hover:bg-amber-400/20 disabled:opacity-50"
                            >
                              {choice}
                            </button>
                          ))}
                        </div>
                      ) : (
                        <div className="mt-3 flex items-end gap-2">
                          <input
                            type={agentQuestion.kind === "secret" ? "password" : "text"}
                            value={agentAnswerText}
                            onChange={(e) => setAgentAnswerText(e.target.value)}
                            onKeyDown={async (e) => {
                              if (e.key !== "Enter" || !agentAnswerText.trim() || submittingAgentAnswer) return;
                              e.preventDefault();
                              setSubmittingAgentAnswer(true);
                              const res = await taskClientFor(activeTask).answerTaskQuestion(agentQuestion.taskId, agentQuestion.id, agentAnswerText);
                              setSubmittingAgentAnswer(false);
                              if (!res.ok) {
                                alert("Could not deliver answer: " + (res.error || "Unknown error"));
                                return;
                              }
                              setAgentQuestion(null);
                              setAgentAnswerText("");
                            }}
                            autoFocus
                            placeholder={agentQuestion.kind === "secret" ? "Secret value (not echoed to other devices)" : "Type your answer…"}
                            className="flex-1 rounded-lg border border-amber-400/30 bg-surface-950 px-3 py-2 text-sm text-surface-100 placeholder-amber-200/40 outline-none focus:border-amber-400/60"
                          />
                          <button
                            type="button"
                            disabled={submittingAgentAnswer || !agentAnswerText.trim()}
                            onClick={async () => {
                              if (!agentQuestion) return;
                              setSubmittingAgentAnswer(true);
                              const res = await taskClientFor(activeTask).answerTaskQuestion(agentQuestion.taskId, agentQuestion.id, agentAnswerText);
                              setSubmittingAgentAnswer(false);
                              if (!res.ok) {
                                alert("Could not deliver answer: " + (res.error || "Unknown error"));
                                return;
                              }
                              setAgentQuestion(null);
                              setAgentAnswerText("");
                            }}
                            className="rounded-lg bg-amber-400/80 px-3 py-2 text-xs font-medium text-amber-950 hover:bg-amber-400 disabled:opacity-50"
                          >
                            {submittingAgentAnswer ? "Sending…" : "Send"}
                          </button>
                        </div>
                      )}
                      {agentQuestion.vaultHint ? (
                        <div className="mt-2 text-[11px] text-amber-700 dark:text-amber-200/70">
                          Hint: agent suggests vault entry <code className="font-mono">{agentQuestion.vaultHint}</code>. Look it up with <code className="font-mono">yaver vault get {agentQuestion.vaultHint}</code>.
                        </div>
                      ) : null}
                      <button
                        type="button"
                        onClick={() => setAgentQuestion(null)}
                        className="mt-2 text-[11px] text-amber-700 dark:text-amber-200/60 hover:text-amber-800 dark:hover:text-amber-100"
                      >
                        Dismiss (the agent will time out and pick a default)
                      </button>
                    </div>
                  ) : null}
                  <form onSubmit={handleSend} className="grid gap-2 md:grid-cols-[minmax(0,1fr),auto] md:items-end">
                    {(() => {
                      const taskRunning = activeTask?.status === "running" || activeTask?.status === "queued";
                      const queuedCount = pendingFollowUps.length;
                      // Cross-machine rows for the composer (2026-08-13):
                      // every OTHER owned device's MCP servers + repos from
                      // the Convex surface catalogs, with the machine label
                      // each chip/row carries. Selecting one switches the
                      // chat to that machine — an MCP attaches by name on
                      // the task machine, and repo paths are machine-local.
                      const remoteMcpRows = devices
                        .filter((d) => d.id !== connectedDevice?.id)
                        .flatMap((d) => (mcpCatalogByDevice[d.id] || []).map((server) => ({
                          device: d,
                          deviceId: d.id,
                          deviceLabel: d.name || d.id || "other machine",
                          server,
                        })))
                        .sort((a, b) => (a.deviceLabel + a.server.name).localeCompare(b.deviceLabel + b.server.name));
                      const remoteProjectRows = devices
                        .filter((d) => d.id !== connectedDevice?.id)
                        .flatMap((d) => (projectCatalogByDevice[d.id] || []).map((proj) => ({
                          device: d,
                          deviceId: d.id,
                          deviceLabel: d.name || d.id || "other machine",
                          name: runtimeProjectDisplayName(proj),
                        })))
                        .sort((a, b) => (a.deviceLabel + a.name).localeCompare(b.deviceLabel + b.name));
                      const placeholder = activeRunnerAuthIssue
                        ? `Sign in to ${runnerLabel(activeRunnerId)} to continue on ${activeTaskDeviceName}...`
                        : taskRunning
                          ? queuedCount > 0
                            ? `Queued ${queuedCount} after the current run; type another follow-up...`
                            : "Type to queue a follow-up; it sends when this turn finishes..."
                          : activeTask
                            ? activeTask.status === "stopped" || activeTask.status === "failed"
                              ? "Start a new task from this prompt..."
                              : "Add a task update or refinement..."
                            : preferredSurfaceProjectPath
                              ? "Describe what to do in this repo..."
                              : "Describe the task you want this machine to run...";
                      const buttonLabel = sending
                        ? "..."
                        : taskRunning
                          ? queuedCount > 0
                            ? `Queue after current run (+${queuedCount})`
                            : "Queue after current run"
                          : activeTask
                            ? activeTask.status === "stopped" || activeTask.status === "failed"
                              ? "Start new task"
                              : "Update task"
                            : "Start task";
                      const disabled = !input.trim()
                        || sending
                        || chatRunnerChoices.length === 0
                        || Boolean(activeRunnerAuthIssue);
                      return (
                        <>
	                          {activeTask && taskRunnerControlSuggestions(input).length ? (
                              <div className="grid gap-1.5 md:col-span-2" role="menu" aria-label="Task commands">
                                {taskRunnerControlSuggestions(input).map((item) => (
                                  <button
                                    key={item.command}
                                    type="button"
                                    role="menuitem"
                                    onClick={() => {
                                      setInput("");
                                      void openTaskRunnerControl(item.control);
                                    }}
                                    className={`flex min-h-12 items-center gap-3 rounded-xl border bg-surface-950 px-3 py-2 text-left hover:bg-surface-900 ${item.destructive ? "border-red-500/40" : "border-surface-700"}`}
                                  >
                                    <span className={`font-mono text-sm font-bold ${item.destructive ? "text-red-300" : "text-violet-300"}`}>{item.command}</span>
                                    <span className="min-w-0 flex-1">
                                      <span className="block text-sm font-semibold text-surface-100">{item.label}</span>
                                      <span className="block text-[11px] text-surface-400">{item.description}</span>
                                    </span>
                                  </button>
                                ))}
                              </div>
                            ) : null}
	                          <textarea ref={inputRef} value={input} onChange={e => setInput(e.target.value)}
                            onKeyDown={e => {
                              // See lib/composerKeys.ts (2026-07-27 "it removed
                              // my second line"): an IME commit and every
                              // non-Shift newline chord used to send instead.
                              const decision = decideComposerKey({
                                key: e.key,
                                shiftKey: e.shiftKey,
                                altKey: e.altKey,
                                ctrlKey: e.ctrlKey,
                                metaKey: e.metaKey,
                                isComposing: e.nativeEvent.isComposing,
                                keyCode: e.keyCode,
                              });
                              if (decision === "send") { e.preventDefault(); handleSend(); return; }
                              if (decision === "newline" && !newlineIsNative({ key: e.key, shiftKey: e.shiftKey })) {
                                e.preventDefault();
                                const field = e.currentTarget;
                                const next = insertNewline(field.value, field.selectionStart, field.selectionEnd);
                                setInput(next.value);
                                requestAnimationFrame(() => {
                                  try { field.setSelectionRange(next.caret, next.caret); } catch {}
                                });
                              }
                            }}
	                            placeholder={placeholder} rows={1}
	                            disabled={Boolean(activeRunnerAuthIssue)}
	                            className="max-h-32 w-full resize-none rounded-xl border border-surface-700 bg-surface-950 px-4 py-3 text-sm text-surface-50 caret-surface-50 placeholder-surface-400 outline-none focus:border-surface-500 disabled:cursor-not-allowed disabled:opacity-60" style={{ minHeight: "48px" }} />
	                          <button type="submit" disabled={disabled}
	                            className="h-12 shrink-0 rounded-xl bg-surface-100 px-5 text-sm font-medium text-surface-900 hover:bg-surface-50 disabled:opacity-30">
	                            {buttonLabel}
	                          </button>
                          {!taskRunning ? (
                            <div className="flex flex-wrap items-center gap-2 text-[11px] text-surface-500 md:col-span-2">
                              {/* Project picker — ALWAYS visible (2026-08-13).
                                  The old `chatProjects.length > 0` gate hid the
                                  entire picker when the connected box reported
                                  no projects — exactly the case where you need
                                  it. Panel = the connected machine's repos
                                  (real paths from /projects) + a per-machine
                                  browse of the OTHER machines' repos from the
                                  Convex catalog (names only — privacy: no
                                  absolute paths off-machine). Picking a remote
                                  repo switches the chat to that machine, where
                                  the real paths live. Feeds task workDir via
                                  preferredSurfaceProjectPath; persisted to
                                  Convex (defaultRuntimeProjectByDevice) on task
                                  start so the phone remembers it too. */}
                              {chatProjectPickerOpen ? (
                                <div className="flex w-full flex-col gap-1.5 rounded-xl border border-surface-700 bg-surface-950/80 p-2">
                                  <div className="flex items-center justify-between px-1">
                                    <span className="font-semibold uppercase tracking-[0.14em]">Project</span>
                                    <button
                                      type="button"
                                      onClick={() => setChatProjectPickerOpen(false)}
                                      className="text-[11px] font-semibold text-surface-400 hover:text-surface-200"
                                    >
                                      Done
                                    </button>
                                  </div>
                                  <div className="grid gap-1">
                                    {chatProjects.length === 0 ? (
                                      <div className="rounded-lg border border-surface-800 bg-surface-950 px-2.5 py-2 text-surface-400">
                                        No repos reported on {connectedDevice?.name || "this machine"} yet. Pick one from another machine below, or run without a project.
                                      </div>
                                    ) : (
                                      chatProjects.map((proj) => {
                                        const active = proj.path === preferredSurfaceProjectPath;
                                        return (
                                          <button
                                            key={proj.path}
                                            type="button"
                                            onClick={() => {
                                              setPreferredSurfaceProjectPath(active ? null : proj.path);
                                              setChatProjectPickerOpen(false);
                                            }}
                                            className={`flex items-center gap-2 rounded-lg border px-2.5 py-1.5 text-left ${
                                              active
                                                ? "border-fuchsia-400/50 bg-fuchsia-400/10 text-fuchsia-100"
                                                : "border-surface-800 bg-surface-950 text-surface-300 hover:border-surface-600"
                                            }`}
                                            title={proj.path}
                                          >
                                            <span className="min-w-0 flex-1 truncate font-semibold">{proj.name}</span>
                                            <span className="min-w-0 flex-1 truncate font-mono text-[10px] text-surface-500">{proj.path}</span>
                                            {active ? <span className="text-fuchsia-300">✓</span> : null}
                                          </button>
                                        );
                                      })
                                    )}
                                    {remoteProjectRows.length > 0 ? (
                                      <>
                                        <div className="mt-1 px-1 text-[10px] font-semibold uppercase tracking-[0.14em] text-surface-500">
                                          Other machines
                                        </div>
                                        {remoteProjectRows.map((row) => (
                                          <button
                                            key={`${row.deviceId}:${row.name}`}
                                            type="button"
                                            onClick={() => {
                                              setChatProjectPickerOpen(false);
                                              void switchChatDevice(row.device, { selectProjectName: row.name });
                                            }}
                                            className="flex items-center gap-2 rounded-lg border border-surface-800 bg-surface-950 px-2.5 py-1.5 text-left text-surface-300 hover:border-surface-600"
                                            title={`${row.name} on ${row.deviceLabel} — switches the chat to that machine`}
                                          >
                                            <span className="min-w-0 flex-1 truncate font-semibold">{row.name}</span>
                                            <span className="shrink-0 truncate font-mono text-[10px] text-surface-500">↗ {row.deviceLabel}</span>
                                          </button>
                                        ))}
                                      </>
                                    ) : null}
                                  </div>
                                </div>
                              ) : (
                                <button
                                  type="button"
                                  onClick={() => setChatProjectPickerOpen(true)}
                                  className={`rounded-full border px-2.5 py-1 font-semibold ${
                                    preferredSurfaceProjectPath
                                      ? "border-fuchsia-400/50 bg-fuchsia-400/10 text-fuchsia-100"
                                      : "border-surface-800 bg-surface-950 text-surface-400 hover:border-surface-700"
                                  }`}
                                  title="Pick the repo this task runs in (task workDir)"
                                >
                                  {preferredSurfaceProjectPath
                                    ? (chatProjects.find((p) => p.path === preferredSurfaceProjectPath)?.name
                                       || preferredSurfaceProjectPath.split(/[\\/]/).filter(Boolean).pop())
                                    : "Project ▾"}
                                </button>
                              )}
                              <button
                                type="button"
                                onClick={() => {
                                  const next = !useLatestProject;
                                  setUseLatestProject(next);
                                  setUseLatestProjectEnabled(next);
                                  if (!next) setPreferredSurfaceProjectPath(null);
                                }}
                                className={`rounded-full border px-2.5 py-1 font-semibold ${useLatestProject ? "border-fuchsia-400/50 bg-fuchsia-400/10 text-fuchsia-100" : "border-surface-800 bg-surface-950 text-surface-500"}`}
                                title="Restore the latest project on this browser"
                              >
                                latest project{useLatestProject ? " ✓" : ""}
                              </button>
                              <button
                                type="button"
                                onClick={() => {
                                  const next = !useLatestMCP;
                                  setUseLatestMCP(next);
                                  setUseLatestMCPEnabled(next);
                                  if (!next) { setSelectedMcpServers([]); setIncludeYaverMcp(false); }
                                }}
                                className={`rounded-full border px-2.5 py-1 font-semibold ${useLatestMCP ? "border-brand/40 bg-brand-soft text-brand-softFg" : "border-surface-800 bg-surface-950 text-surface-500"}`}
                                title="Restore the latest MCP selection on this browser"
                              >
                                latest MCP{useLatestMCP ? " ✓" : ""}
                              </button>
                              {/* Yaver's own MCP doorway is an explicit opt-in.
                                  Off means the runner gets only selected externals. */}
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
                              <span className="font-semibold uppercase tracking-[0.14em]">
                                {selectedMcpServers.length ? `${selectedMcpServers.length} MCP` : "No MCP"}
                              </span>
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
                              {/* MCPs on the user's OTHER machines — the
                                  cross-machine catalog (2026-08-13). An MCP
                                  server lives on exactly one machine and the
                                  task machine attaches it by name from its own
                                  local registry, so picking a remote chip
                                  SWITCHES the chat to that machine (never a
                                  silent no-op). Label carries the machine. */}
                              {remoteMcpRows.length > 0 ? (
                                <>
                                  <span className="font-semibold uppercase tracking-[0.14em]">Other machines</span>
                                  {remoteMcpRows.map((row) => {
                                    const active = selectedMcpServers.includes(row.server.name);
                                    return (
                                      <button
                                        key={`${row.deviceId}:${row.server.name}`}
                                        type="button"
                                        onClick={() => void switchChatDevice(row.device, { selectMcp: row.server.name })}
                                        className={`rounded-full border px-2.5 py-1 font-semibold ${
                                          active
                                            ? "border-amber-400/50 bg-amber-400/10 text-amber-100"
                                            : "border-surface-800 bg-surface-950 text-surface-400 hover:border-amber-600/60 hover:text-amber-100"
                                        }`}
                                        title={`${row.server.url} · ${row.server.toolCount ?? 0} tools · on ${row.deviceLabel} — switches the chat to that machine`}
                                      >
                                        {row.server.name}
                                        <span className="ml-1 opacity-60">· {row.deviceLabel}</span>
                                      </button>
                                    );
                                  })}
                                </>
                              ) : null}
                              {switchNotice ? (
                                <span className="w-full rounded-lg border border-amber-500/30 bg-amber-500/10 px-2.5 py-1.5 text-amber-200/90">
                                  {switchNotice}
                                </span>
                              ) : null}
                            </div>
                          ) : null}
                        </>
                      );
                    })()}
                  </form>
                </div>
              </div>
            </>
          )}
        </div>
      </div>
      {/* Browser-shell modal — opens xterm.js connected to the agent's
          /ws/terminal PTY endpoint via the relay. Only mounted while
          open so the WebSocket lifecycle matches the modal. */}
      {shellDevice ? (
        <WebShellModal
          device={shellDevice}
          tmuxSession={shellTmuxSession || undefined}
          tmuxTaskId={shellTmuxTaskId || undefined}
          isCurrentDeviceSelected={Boolean(connectedDevice && connectedDevice.id === shellDevice.id)}
          isCurrentDeviceConnected={Boolean(connectedDevice && connectedDevice.id === shellDevice.id && connState === "connected")}
          onConnect={() => { void connectToDevice(shellDevice); }}
          onOpenRescue={() => {
            // Devices tab owns the Rescue panel + Reset Auth flow.
            // We can't deep-link to a specific device's open Rescue
            // section from here, but switching tabs gets the user one
            // click away — DevicesView preserves rescueOpenDeviceId
            // state per-card and the cards are sorted with the
            // attention-needed devices on top.
            setActiveTab("devices");
          }}
          onTmuxClosed={() => {
            setShellDevice(null);
            setShellTmuxSession(null);
            setShellTmuxTaskId(null);
            void agentClient.listTmuxSessions().then(setSidebarTmux).catch(() => setSidebarTmux([]));
          }}
          onClose={() => { setShellDevice(null); setShellTmuxSession(null); setShellTmuxTaskId(null); }}
        />
      ) : null}
      {/* Remote Desktop modal — live screen (MJPEG /rd/stream) + optional
          mouse/keyboard control (/rd/input) via the relay. Mounted only while
          open so the stream connection matches the modal lifecycle. */}
      {remoteDesktopDevice ? (
        <RemoteDesktopModal
          device={remoteDesktopDevice}
          isCurrentDeviceSelected={Boolean(connectedDevice && connectedDevice.id === remoteDesktopDevice.id)}
          isCurrentDeviceConnected={Boolean(connectedDevice && connectedDevice.id === remoteDesktopDevice.id && connState === "connected")}
          onConnect={() => { void connectToDevice(remoteDesktopDevice); }}
          onOpenRescue={() => { setActiveTab("devices"); }}
          onClose={() => setRemoteDesktopDevice(null)}
        />
      ) : null}
      {/* Lifted out of the chat-tab branch so the Hot Reload "Sign in
          & reconnect" button can open the modal regardless of which
          tab is active. The modal handles its own backdrop + z-index. */}
      {chatRunnerAuthModal ? (
        <RunnerAuthModal
          runner={chatRunnerAuthModal}
          deviceName={connectedDevice?.name || connectedDevice?.id || "this machine"}
          // Routes /runner-auth/browser/* via /peer/<id> so the OAuth
          // flow runs on the device the dashboard is connected to,
          // even when the dashboard is itself served from a relay
          // (browsers can't dial LAN IPs). When undefined the call
          // hits the connected agent directly — same shape as the
          // mobile RunnerAuthModal target prop.
          target={connectedDevice?.id || undefined}
          onClose={() => {
            setChatRunnerAuthModal(null);
            void refreshConnectedRunners();
          }}
          onCompleted={() => {
            void refreshConnectedRunners();
            // The sidebar device card reads runner authConfigured
            // off Convex's device list (liveDevice.runners), not
            // off the local /agent/runners response. Without this
            // refresh the sidebar keeps showing "Sign in {Codex}"
            // even though sign-in just succeeded — the agent
            // updates Convex via heartbeat after a successful
            // runner-auth, but the dashboard needs to refetch.
            // Wait a beat so Convex has the heartbeat-driven update,
            // then refresh.
            setTimeout(() => { void refreshDevices(); }, 600);
            setTimeout(() => { void refreshDevices(); }, 1800);
            // Also re-establish the device connection in case the
            // session-expired state lingered on the dashboard side.
            if (connectedDevice) {
              void connectToDevice(connectedDevice);
            }
          }}
        />
      ) : null}
    </div>
  );
}

function RunnerAuthModal({
  runner,
  deviceName,
  target,
  onClose,
  onCompleted,
}: {
  runner: string;
  deviceName: string;
  /** Optional peer device id. When set, all `/runner-auth/browser/*`
   *  calls route via `/peer/<target>/...` so the OAuth flow runs on
   *  the named device — used for "sign in to claude on Mac mini" while
   *  the dashboard is connected via relay or to a different machine. */
  target?: string;
  onClose: () => void;
  onCompleted: () => void;
}) {
  const [session, setSession] = useState<RunnerBrowserAuthSession | null>(null);
  const [startError, setStartError] = useState<string | null>(null);
  // action:"noop" from the agent: it declined to start because the runner
  // already looks signed in. An answer, not an error — `reauthable` offers
  // the confirmed restart (switch account), the only path that reaps.
  const [declined, setDeclined] = useState<{ reason: string; reauthable: boolean } | null>(null);
  const [copied, setCopied] = useState(false);
  const [pasteCode, setPasteCode] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const startedRef = useRef(false);

  const startSignIn = useCallback(async (confirm: boolean) => {
    setStartError(null);
    setDeclined(null);
    try {
      const res = await agentClient.runnerBrowserAuthStart(
        {
          runner: runner as "claude" | "codex",
          trigger: confirm ? "confirmed" : "explicit",
          confirm,
        },
        target,
      );
      if (!res.ok) throw new Error(res.error || "Could not start sign-in on the machine.");
      if (res.action === "noop") {
        setDeclined({
          reason: res.reason || `${runner} is already signed in on that machine.`,
          reauthable: res.reauthable !== false,
        });
        return;
      }
      const started = res.session;
      if (!started) throw new Error(res.reason || "The machine did not start a sign-in session and did not say why.");
      setSession(started);
    } catch (error) {
      setStartError(error instanceof Error ? error.message : String(error));
    }
  }, [runner, target]);

  useEffect(() => {
    if (startedRef.current) return;
    startedRef.current = true;
    void startSignIn(false);
  }, [startSignIn]);

  useEffect(() => {
    if (!session) return;
    if (session.status === "completed" || session.status === "failed" || session.status === "cancelled") {
      if (session.status === "completed") onCompleted();
      return;
    }
    const interval = setInterval(async () => {
      try {
        const next = await agentClient.getRunnerBrowserAuthStatus(session.id, target);
        setSession(next);
      } catch {}
    }, 1500);
    return () => clearInterval(interval);
  }, [session, onCompleted, target]);

  const terminal = !!declined || (session && ["completed", "failed", "cancelled"].includes(session.status));

  const copyCode = async () => {
    if (!session?.code) return;
    try {
      await navigator.clipboard.writeText(session.code);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {}
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm"
      onClick={(event) => { if (event.target === event.currentTarget && terminal) onClose(); }}
    >
      <div className="w-full max-w-md rounded-xl border border-surface-800 bg-surface-900 p-5 shadow-2xl">
        <div className="mb-3 flex items-start justify-between gap-3">
          <div>
            <h3 className="text-base font-semibold text-surface-100">Sign in to {runnerLabel(runner)}</h3>
            <p className="text-xs text-surface-500">on <span className="font-mono text-surface-300">{deviceName}</span></p>
          </div>
          <button
            onClick={async () => {
              if (session && !terminal) {
                await agentClient.cancelRunnerBrowserAuth(session.id, target).catch(() => {});
              }
              onClose();
            }}
            className="text-xl leading-none text-surface-500 hover:text-surface-200"
            aria-label="Close"
          >
            ×
          </button>
        </div>

        {startError ? (
          <div className="rounded-lg border border-red-500/30 bg-red-500/5 p-3 text-xs text-red-700 dark:text-red-300">
            <div className="mb-1 font-semibold">Couldn&apos;t start sign-in</div>
            {startError}
          </div>
        ) : declined ? (
          /* The agent declined to start: the runner already looks signed in.
             Informational, not an error — the useful outcome already exists. */
          <div className="rounded-lg border border-surface-800 bg-surface-800/40 p-3 text-xs text-surface-300">
            <div className="mb-1 font-semibold text-surface-100">Already signed in</div>
            <div>{declined.reason}</div>
            <div className="mt-2 flex flex-wrap gap-2">
              {declined.reauthable ? (
                <button
                  onClick={() => void startSignIn(true)}
                  className="rounded-lg border border-surface-700 bg-surface-800/60 px-3 py-1.5 text-xs font-semibold text-surface-200 hover:border-surface-600"
                >
                  Sign in anyway (switch account)
                </button>
              ) : null}
              <button
                onClick={onClose}
                className="rounded-lg border border-surface-700 px-3 py-1.5 text-xs font-semibold text-surface-400 hover:text-surface-200"
              >
                Close
              </button>
            </div>
          </div>
        ) : !session ? (
          <div className="rounded-lg border border-surface-800 bg-surface-800/40 p-3 text-xs text-surface-400">
            Starting the sign-in flow on the remote machine…
          </div>
        ) : session.status === "completed" ? (
          <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-4 text-sm text-emerald-700 dark:text-emerald-200">
            <div className="mb-1 font-semibold">Signed in</div>
            <div className="text-xs text-emerald-700 dark:text-emerald-300/80">{session.detail || "Auth stored on the remote machine."}</div>
          </div>
        ) : session.status === "failed" || session.status === "cancelled" ? (
          <div className="rounded-lg border border-red-500/30 bg-red-500/5 p-3 text-xs text-red-700 dark:text-red-300">
            <div className="mb-1 font-semibold">{session.status === "cancelled" ? "Cancelled" : "Failed"}</div>
            <div>{session.error || session.detail || "The CLI exited before sign-in completed."}</div>
          </div>
        ) : (
          <div className="space-y-3">
            <p className="text-xs text-surface-400">
              Complete sign-in from any browser. Yaver started the remote {runnerLabel(runner)} login flow on this machine.
            </p>
            {session.openUrl ? (
              <a
                href={session.openUrl}
                target="_blank"
                rel="noopener noreferrer"
                className="block truncate rounded-lg border border-indigo-500/40 bg-indigo-500/10 px-3 py-2.5 text-sm font-medium text-indigo-700 dark:text-indigo-200 hover:bg-indigo-500/20"
              >
                ↗ {session.openUrl}
              </a>
            ) : (
              <div className="rounded-lg border border-surface-800 bg-surface-800/30 px-3 py-2.5 text-xs text-surface-500">
                Waiting for the verification URL from the remote CLI…
              </div>
            )}
            {session.code ? (
              <div>
                <div className="mb-1 text-[10px] font-semibold uppercase tracking-widest text-surface-500">Enter this code</div>
                <button
                  onClick={copyCode}
                  className="flex w-full items-center justify-between rounded-lg border border-surface-700 bg-surface-800/60 px-4 py-3 text-left hover:border-surface-600"
                >
                  <span className="font-mono text-xl tracking-[0.2em] text-surface-100">{session.code}</span>
                  <span className="text-[10px] uppercase text-surface-500">{copied ? "copied" : "click to copy"}</span>
                </button>
              </div>
            ) : null}
            {runner === "claude" ? (
              <div className="rounded-lg border border-surface-800 bg-surface-950/40 p-3">
                <div className="mb-1 text-[10px] font-semibold uppercase tracking-widest text-surface-400">
                  Paste the code from {runnerLabel(runner)}
                </div>
                <p className="mb-2 text-[11px] leading-relaxed text-surface-500">
                  After clicking Authorize on platform.claude.com, copy the code from the callback page (it starts with a long base64 string) and paste it here.
                </p>
                <div className="flex gap-2">
                  <input
                    type="text"
                    value={pasteCode}
                    onChange={(e) => { setPasteCode(e.target.value); setSubmitError(null); }}
                    placeholder="paste code here"
                    spellCheck={false}
                    autoComplete="off"
                    className="flex-1 rounded-md border border-surface-700 bg-surface-950 px-3 py-2 text-xs font-mono text-surface-100 outline-none placeholder-surface-700 focus:border-indigo-500/40"
                    onKeyDown={async (e) => {
                      if (e.key === "Enter" && pasteCode.trim() && session && !submitting) {
                        e.preventDefault();
                        setSubmitting(true);
                        setSubmitError(null);
                        try {
                          setSession({
                            ...session,
                            status: "awaiting_browser",
                            detail: "Code submitted. Waiting for the remote Claude Code process to confirm plan sign-in.",
                          });
                          const next = await agentClient.submitRunnerBrowserAuthCode(session.id, pasteCode.trim(), target);
                          setSession(next);
                          setPasteCode("");
                        } catch (err) {
                          setSubmitError(err instanceof Error ? err.message : String(err));
                        } finally {
                          setSubmitting(false);
                        }
                      }
                    }}
                  />
                  <button
                    disabled={!pasteCode.trim() || submitting || !session}
                    onClick={async () => {
                      if (!session) return;
                      setSubmitting(true);
                      setSubmitError(null);
                      try {
                        setSession({
                          ...session,
                          status: "awaiting_browser",
                          detail: "Code submitted. Waiting for the remote Claude Code process to confirm plan sign-in.",
                        });
                        const next = await agentClient.submitRunnerBrowserAuthCode(session.id, pasteCode.trim(), target);
                        setSession(next);
                        setPasteCode("");
                      } catch (err) {
                        setSubmitError(err instanceof Error ? err.message : String(err));
                      } finally {
                        setSubmitting(false);
                      }
                    }}
                    className="rounded-md border border-indigo-500/40 bg-indigo-500/10 px-3 py-2 text-xs font-medium text-indigo-700 dark:text-indigo-200 hover:bg-indigo-500/20 disabled:opacity-40"
                  >
                    {submitting ? "…" : "Submit"}
                  </button>
                </div>
                {submitError ? (
                  <div className="mt-2 text-[11px] text-red-700 dark:text-red-300">{submitError}</div>
                ) : null}
              </div>
            ) : null}
            <p className="text-[10px] leading-relaxed text-surface-600">
              {runner === "codex"
                ? "Codex's device-auth flow auto-completes once you finish in the browser — no paste step. This dialog turns green automatically."
                : "The dialog auto-completes once the remote CLI confirms the token; you can close it then."}
            </p>
          </div>
        )}
      </div>
    </div>
  );
}
