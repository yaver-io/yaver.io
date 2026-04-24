"use client";

import { useAuth } from "@/lib/use-auth";
import { useDevices, type Device } from "@/lib/use-devices";
import { agentClient, type Task, type ConnectionState, type Runner, type AgentInfo, type ConnectAttemptDiagnostic, type DeviceStatusProbe } from "@/lib/agent-client";
import { CONVEX_URL } from "@/lib/constants";
import { fetchGuestHosts, acceptGuestInvitation, type GuestInvitation } from "@/lib/guests";
import { useState, useEffect, useRef } from "react";
import { useRouter } from "next/navigation";
import { useTheme } from "@/components/ThemeProvider";
import ProjectsView from "@/components/dashboard/ProjectsView";
import TodosView from "@/components/dashboard/TodosView";
import BuildsView from "@/components/dashboard/BuildsView";
import HealthView from "@/components/dashboard/HealthView";
import QualityView from "@/components/dashboard/QualityView";
import ConvexView from "@/components/dashboard/ConvexView";
import DataView from "@/components/dashboard/DataView";
import SwitchView from "@/components/dashboard/SwitchView";
import AccountsView from "@/components/dashboard/AccountsView";
import ConsoleView from "@/components/dashboard/ConsoleView";
import ObservabilityView from "@/components/dashboard/ObservabilityView";
import OpsView from "@/components/dashboard/OpsView";
import OverviewView from "@/components/dashboard/OverviewView";
import ExtrasView from "@/components/dashboard/ExtrasView";
import ShareView from "@/components/dashboard/ShareView";
import GuestsStatusView from "@/components/dashboard/GuestsStatusView";
import InfraView from "@/components/dashboard/InfraView";
import ConnectivityView from "@/components/dashboard/ConnectivityView";
import ToolsView from "@/components/dashboard/ToolsView";
import PreviewPane from "@/components/dashboard/PreviewPane";
import TwoFactorView from "@/components/dashboard/TwoFactorView";
import MorningView from "@/components/dashboard/MorningView";
import VaultView from "@/components/dashboard/VaultView";
import APIKeysView from "@/components/dashboard/APIKeysView";
import StorageView from "@/components/dashboard/StorageView";
import SchedulesView from "@/components/dashboard/SchedulesView";
import PhoneProjectsView from "@/components/dashboard/PhoneProjectsView";
import ExecView from "@/components/dashboard/ExecView";
import DomainsView from "@/components/dashboard/DomainsView";
import VibeCodingView from "@/components/dashboard/VibeCodingView";
import { WebReloadView } from "@/components/dashboard/WebReloadView";
import GitView from "@/components/dashboard/GitView";
import DevicesView from "@/components/dashboard/DevicesView";
import type { RunnerBrowserAuthSession } from "@/lib/agent-client";

function statusColor(s: string) {
  if (s === "running") return "text-amber-400";
  if (s === "completed") return "text-emerald-400";
  return "text-surface-400";
}

type ChatMsg = { role: "user" | "assistant"; text: string };

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

function isLikelyWSLDevice(device: Pick<Device, "name" | "platform" | "host">): boolean {
  const platform = String(device.platform || "").trim().toLowerCase();
  if (platform !== "linux") return false;
  const name = String(device.name || "").trim().toUpperCase();
  const host = String(device.host || "").trim();
  return (
    name.startsWith("DESKTOP-") ||
    name.startsWith("LAPTOP-") ||
    name.startsWith("WIN-") ||
    /^172\.(1[6-9]|2\d|3[0-1])\.\d{1,3}\.\d{1,3}$/.test(host)
  );
}

function devicePlatformLabel(device: Pick<Device, "name" | "platform" | "host">): string {
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

function lastSeenAgeMs(lastSeen?: string): number | null {
  if (!lastSeen) return null;
  const ts = Date.parse(lastSeen);
  if (Number.isNaN(ts)) return null;
  return Math.max(0, Date.now() - ts);
}

function formatAgeShort(ms: number | null): string | null {
  if (ms == null) return null;
  const sec = Math.floor(ms / 1000);
  if (sec < 60) return `${sec}s`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h`;
  const day = Math.floor(hr / 24);
  return `${day}d`;
}

function hasRecentLiveSignal(
  device: Pick<Device, "lastTunnelEvent" | "peerState" | "workspaceLive">,
  maxAgeMs = 90_000,
): boolean {
  if (device.workspaceLive) return true;
  if (device.peerState === "online") return true;
  return Boolean(
    device.lastTunnelEvent &&
    device.lastTunnelEvent.online &&
    device.lastTunnelEvent.at > 0 &&
    (Date.now() - device.lastTunnelEvent.at) < maxAgeMs,
  );
}

function deviceReachabilitySummary(
  device: Pick<Device, "online" | "needsAuth" | "lastSeen" | "publicEndpoints" | "tunnelUrl" | "host" | "lastTunnelEvent" | "peerState" | "workspaceLive" | "probeState" | "probePath" | "probeError">,
): string {
  if (device.workspaceLive) return "Active workspace connection";
  if (device.probeState === "ok") return `Authenticated agent probe succeeded via ${device.probePath || "device path"}`;
  if (device.probeState === "auth-expired") return "Agent reached, but its session is expired";
  if (device.peerState === "online") return "Live bus signal";
  if (hasRecentLiveSignal(device)) return "Live relay signal";
  if (device.peerState === "stale") return "Bus saw this machine recently, but no current transport is healthy";
  if (device.online) return "Recently confirmed by agent";
  if (device.needsAuth) return "Agent session expired; relay recovery may still work";
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
  device: Pick<Device, "online" | "needsAuth" | "lastSeen" | "publicEndpoints" | "tunnelUrl" | "isGuest" | "peerState" | "workspaceLive" | "probeState">,
): boolean {
  if (device.isGuest) return false;
  if (device.online) return false;
  if (device.workspaceLive) return false;
  if (device.probeState === "ok" || device.probeState === "auth-expired") return false;
  if (device.peerState === "online") return false;
  if (device.needsAuth) return false;
  if (Boolean(device.tunnelUrl) || Boolean(device.publicEndpoints?.length)) return false;
  const age = lastSeenAgeMs(device.lastSeen);
  return age !== null && age >= DORMANT_DEVICE_HIDE_MS;
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

function runnerAuthIssue(runner: Pick<Runner, "id" | "installed" | "ready" | "warning" | "error"> | null | undefined): string | null {
  if (!runner || !runner.installed || runner.ready !== false) return null;
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

function runnerChipsForDevice(device: Pick<Device, "sharedRunners" | "runners">): string[] {
  const chips = new Set<string>();
  for (const runner of device.sharedRunners || []) {
    const label = formatRunnerChipLabel(runner);
    if (label) chips.add(label);
  }
  for (const runner of device.runners || []) {
    const label = formatRunnerChipLabel(String(runner?.runnerId || ""));
    if (label) chips.add(label);
  }
  return [...chips];
}

function sharedGuestLabels(device: Pick<Device, "sharedGuests">): string[] {
  return (device.sharedGuests || [])
    .map((guest) => guest.name || guest.email || "")
    .map((label) => String(label).trim())
    .filter(Boolean);
}

function deviceAccessSummary(device: Pick<Device, "isGuest" | "sharedWithGuests" | "sharedGuests" | "sharesAllProjects" | "sharedProjects" | "sharedRunners" | "runners">) {
  const hasSharedState = device.isGuest || device.sharedWithGuests;
  if (!hasSharedState) return null;
  const sharedProjects = Array.isArray(device.sharedProjects) ? device.sharedProjects.filter(Boolean) : [];
  const guests = sharedGuestLabels(device);
  return {
    projectLabel: device.sharesAllProjects ? "All Resources" : sharedProjects.length > 0 ? "Projects" : null,
    projectChips: device.sharesAllProjects ? [] : sharedProjects,
    runnerChips: runnerChipsForDevice(device),
    guestChips: guests.slice(0, 3),
    guestOverflow: Math.max(0, guests.length - 3),
  };
}

function accessScopeTone(device: Pick<Device, "accessScope" | "isGuest">) {
  if (device.accessScope === "shared-scoped") return "border-amber-500/40 bg-amber-500/10 text-amber-200";
  if (device.accessScope === "shared-legacy") return "border-violet-500/40 bg-violet-500/10 text-violet-200";
  if (device.isGuest) return "border-sky-500/40 bg-sky-500/10 text-sky-200";
  return "";
}

function accessScopeLabel(device: Pick<Device, "accessScope" | "isGuest">) {
  if (device.accessScope === "shared-scoped") return "Scoped Access";
  if (device.accessScope === "shared-legacy") return "Legacy Shared";
  if (device.isGuest) return "Shared Device";
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

function DeviceConnectCard({
  device,
  isPrimary,
  isSelected,
  isConnecting,
  connectionError,
  onConnect,
  onTogglePrimary,
  canTogglePrimary,
  compact = false,
}: {
  device: Device;
  isPrimary: boolean;
  isSelected: boolean;
  isConnecting: boolean;
  connectionError?: string | null;
  onConnect: () => void;
  onTogglePrimary?: () => void;
  canTogglePrimary?: boolean;
  compact?: boolean;
}) {
  const shareSummary = deviceAccessSummary(device);
  const heartbeatAge = formatHeartbeatAge(device.lastSeen);
  const reachability = deviceReachabilitySummary(device);
  const liveSignal = hasRecentLiveSignal(device);
  const liveSignalAge = liveSignal && device.lastTunnelEvent?.at
    ? formatHeartbeatAge(new Date(device.lastTunnelEvent.at).toISOString())
    : heartbeatAge;
  const lanIps = lanIpsForDevice(device);
  const isOffline = !device.online;

  return (
    <div
      className={[
        "rounded-2xl border bg-surface-900/80 transition-colors",
        compact ? "p-3" : "p-3.5",
        isSelected
          ? connectionError
            ? "border-red-500/30 bg-red-500/[0.04]"
            : isConnecting
              ? "border-amber-500/30 bg-amber-500/[0.04]"
              : "border-emerald-500/30 bg-emerald-500/[0.05]"
          : "border-surface-800 hover:border-surface-700",
      ].join(" ")}
    >
      <div className="flex items-start gap-2.5">
        <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl border border-surface-800 bg-surface-950 text-surface-400">
          <DeviceIcon platform={device.platform} />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-1.5">
            <h3 className="truncate text-[15px] font-semibold text-surface-100">{device.name}</h3>
            <span
              className={`inline-flex h-2 w-2 rounded-full ${
                connectionError ? "bg-red-400" : isOffline ? "bg-surface-600" : isConnecting ? "bg-amber-400" : "bg-emerald-400"
              }`}
            />
            <span className="text-[11px] text-surface-400">
              {connectionError
                ? "failed"
                : isConnecting
                  ? "connecting"
                  : device.workspaceLive
                    ? "workspace live"
                    : device.peerState === "online"
                      ? "bus live"
                      : device.peerState === "stale"
                        ? "bus stale"
                        : isOffline
                          ? "offline"
                          : liveSignal
                            ? "relay live"
                            : "online"} · {liveSignalAge}
            </span>
          </div>
          <p className="mt-1 text-[11px] leading-5 text-surface-500">
            {devicePlatformLabel(device)}
            {device.host ? ` · ${device.host}:${device.port}` : ""}
            {device.isGuest && device.hostName ? ` · from ${device.hostName}` : ""}
          </p>
          {!connectionError && isOffline ? (
            <p className="mt-1 text-[11px] leading-5 text-amber-300/80">{reachability}</p>
          ) : null}
          {connectionError ? (
            <p className="mt-1 text-[11px] text-red-300/80">{connectionError}</p>
          ) : null}
        </div>
      </div>

      <div className="mt-2.5 flex flex-wrap gap-1.5">
        {accessScopeLabel(device) ? (
          <span className={`rounded-full border px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.14em] ${accessScopeTone(device)}`}>
            {accessScopeLabel(device)}
          </span>
        ) : null}
        {!device.isGuest && device.sharedWithGuests ? (
          <span className="rounded-full border border-sky-500/40 bg-sky-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.14em] text-sky-200">
            Shared
          </span>
        ) : null}
        {device.deviceClass ? (
          <span className="rounded-full border border-sky-500/30 bg-sky-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.14em] text-sky-200">
            {device.deviceClass === "edge-mobile" ? "Edge Worker" : device.deviceClass}
          </span>
        ) : null}
        {device.sessionBinding ? (
          <span className={`rounded-full border px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.14em] ${
            device.sessionBinding === "dedicated"
              ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-200"
              : "border-amber-500/30 bg-amber-500/10 text-amber-200"
          }`}>
            {device.sessionBinding === "dedicated" ? "Dedicated Session" : "Legacy Session"}
          </span>
        ) : null}
        {isPrimary ? (
          <span className="rounded-full border border-indigo-500/40 bg-indigo-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.14em] text-indigo-200">
            Primary
          </span>
        ) : null}
        {device.priorityMode === "spare-capacity" ? (
          <span className="rounded-full border border-violet-500/40 bg-violet-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.14em] text-violet-200">
            Spare Capacity
          </span>
        ) : null}
        {lanIps.map((ip) => (
          <span key={`${device.id}:${ip}`} className="rounded-full border border-surface-700 bg-surface-950 px-2 py-0.5 font-mono text-[10px] text-surface-300">
            {ip}
          </span>
        ))}
      </div>

      {shareSummary ? (
        <div className="mt-2 flex flex-wrap gap-1.5">
          {shareSummary.projectLabel ? (
            <span className={`rounded-full border px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.14em] ${
              shareSummary.projectLabel === "All Resources"
                ? "border-sky-500/40 bg-sky-500/10 text-sky-200"
                : "border-amber-500/40 bg-amber-500/10 text-amber-200"
            }`}>
              {shareSummary.projectLabel}
            </span>
          ) : null}
          {shareSummary.projectChips.map((project) => (
            <span key={`${device.id}:project:${project}`} className="rounded-full border border-amber-500/30 bg-amber-500/10 px-2 py-0.5 text-[10px] font-medium text-amber-100">
              {project}
            </span>
          ))}
        </div>
      ) : null}

      {shareSummary && shareSummary.guestChips.length > 0 ? (
        <div className="mt-2 flex flex-wrap items-center gap-1.5">
          <span className="rounded-full border border-sky-500/40 bg-sky-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.14em] text-sky-200">
            Shared With
          </span>
          {shareSummary.guestChips.map((guest) => (
            <span key={`${device.id}:guest:${guest}`} className="rounded-full border border-sky-500/30 bg-sky-500/10 px-2 py-0.5 text-[10px] font-medium text-sky-100">
              {guest}
            </span>
          ))}
          {shareSummary.guestOverflow > 0 ? (
            <span className="rounded-full border border-surface-700 bg-surface-950 px-2 py-0.5 text-[10px] font-medium text-surface-300">
              +{shareSummary.guestOverflow} more
            </span>
          ) : null}
        </div>
      ) : null}

      {shareSummary && shareSummary.runnerChips.length > 0 ? (
        <div className="mt-2 flex flex-wrap items-center gap-1.5">
          <span className="rounded-full border border-violet-500/40 bg-violet-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.14em] text-violet-200">
            Agents
          </span>
          {shareSummary.runnerChips.map((runner) => (
            <span key={`${device.id}:runner:${runner}`} className="rounded-full border border-violet-500/30 bg-violet-500/10 px-2 py-0.5 text-[10px] font-medium text-violet-100">
              {runner}
            </span>
          ))}
        </div>
      ) : null}

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
              ? "cursor-wait border border-amber-500/20 bg-amber-500/10 text-amber-200"
              : "border border-emerald-500/30 bg-emerald-500/10 text-emerald-200 hover:bg-emerald-500/15"
          }`}
        >
          {isConnecting ? "Connecting…" : "Open Workspace"}
        </button>
        {canTogglePrimary && onTogglePrimary ? (
          <button
            onClick={onTogglePrimary}
            className="rounded-xl border border-indigo-500/30 bg-indigo-500/10 px-3 py-1.5 text-xs font-semibold text-indigo-200 hover:bg-indigo-500/15"
          >
            {isPrimary ? "Unset Primary" : "Make Primary"}
          </button>
        ) : null}
      </div>
    </div>
  );
}

export default function DashboardPage() {
  // ── ALL hooks unconditionally at the top ────────────────────────
  const { user, token, isLoading, isAuthenticated, logout } = useAuth();
  const { devices, refreshDevices, hiddenIds } = useDevices(token);
  const { theme, toggle: toggleTheme } = useTheme();
  const router = useRouter();

  const [connState, setConnState] = useState<ConnectionState>("disconnected");
  const [connectedDevice, setConnectedDevice] = useState<Device | null>(null);
  const [agentInfo, setAgentInfo] = useState<AgentInfo | null>(null);
  const [tasks, setTasks] = useState<Task[]>([]);
  const [activeTask, setActiveTask] = useState<Task | null>(null);
  const [outputLines, setOutputLines] = useState<string[]>([]);
  const [chatMsgs, setChatMsgs] = useState<ChatMsg[]>([]);
  const [runners, setRunners] = useState<Runner[]>([]);
  const [selectedRunner, setSelectedRunner] = useState<string>("");
  const [input, setInput] = useState("");
  const [sending, setSending] = useState(false);
  const [guestCode, setGuestCode] = useState("");
  const [pendingInvites, setPendingInvites] = useState<GuestInvitation[]>([]);
  const [invitesBusy, setInvitesBusy] = useState<string | null>(null);
  const [reauthBusy, setReauthBusy] = useState<string | null>(null);
  const [reauthMsg, setReauthMsg] = useState<{ deviceId: string; ok: boolean; text: string } | null>(null);
  const [activeTab, setActiveTab] = useState<"home" | "chat" | "projects" | "vibe" | "devices" | "git" | "todos" | "builds" | "preview" | "web-reload" | "health" | "quality" | "convex" | "data" | "switch" | "accounts" | "console" | "observ" | "ops" | "extras" | "share" | "guests" | "infra" | "connect" | "tools" | "security" | "morning" | "storage" | "vault" | "apikeys" | "schedules" | "exec" | "phone" | "domains">("devices");
  const [todoCount, setTodoCount] = useState(0);
  const [connectError, setConnectError] = useState<string | null>(null);
  const [connectDiagnostics, setConnectDiagnostics] = useState<ConnectAttemptDiagnostic[]>([]);
  const [copiedReauth, setCopiedReauth] = useState(false);
  const [reauthing, setReauthing] = useState(false);
  const [reauthMessage, setReauthMessage] = useState<{ kind: "ok" | "err"; text: string } | null>(null);
  const [relayReady, setRelayReady] = useState(false);
  const [previewTargetId, setPreviewTargetId] = useState<string | null>(null);
  const [preferredSurfaceProjectPath, setPreferredSurfaceProjectPath] = useState<string | null>(null);
  const [chatRunnerAuthModal, setChatRunnerAuthModal] = useState<string | null>(null);
  const [peerStates, setPeerStates] = useState<Record<string, { state: "online" | "stale" | "offline"; lastSeen?: string }>>({});
  const [probeStates, setProbeStates] = useState<Record<string, DeviceStatusProbe>>({});
  // Primary-device preference — the device auto-connect prefers when the
  // user has more than one online. Also mirrored onto mobile and CLI via
  // the /settings endpoint so every surface picks the same default.
  const [primaryDeviceId, setPrimaryDeviceId] = useState<string | null>(null);

  const inputRef = useRef<HTMLTextAreaElement>(null);
  const outputRef = useRef<HTMLDivElement>(null);
  const relayReadyPromiseRef = useRef<Promise<void> | null>(null);
  const previousActiveTabRef = useRef<string | null>(null);
  const probedForCurrentTabOpenRef = useRef(false);

  const isConnected = connState === "connected";

  useEffect(() => {
    if (isLoading) return;
    if (!isAuthenticated) return;
    if (user?.surveyCompleted === false && devices.length === 0) {
      router.replace("/survey");
    }
  }, [devices.length, isAuthenticated, isLoading, router, user?.surveyCompleted]);

  useEffect(() => {
    let cancelled = false;
    let resolve: () => void;
    relayReadyPromiseRef.current = new Promise<void>(r => { resolve = r; });

    (async () => {
      try {
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
              if (!cancelled) setPrimaryDeviceId(sd.settings?.primaryDeviceId ?? null);
            }
          } catch {}
        }

        if (!cancelled && relays.length > 0) agentClient.setRelayServers(relays);
      } catch {}
      if (!cancelled) setRelayReady(true);
      resolve!();
    })();
    return () => { cancelled = true; };
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
    // skips the relay branch and every cross-network device (Hetzner, etc.)
    // gets falsely marked Unreachable. Re-runs when relayReady flips.
    if (!token || devices.length === 0 || !relayReady) {
      return;
    }
    probedForCurrentTabOpenRef.current = true;
    let cancelled = false;
    const refreshProbes = async () => {
      const nextEntries = await Promise.all(
        devices.map(async (device) => {
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
                    .map((url) => String(url || "").trim())
                    .filter(Boolean),
                ),
              ),
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
      setProbeStates(Object.fromEntries(nextEntries));
    };
    void refreshProbes();
    return () => {
      cancelled = true;
    };
  }, [activeTab, token, devices, relayReady]);

  useEffect(() => { const u = agentClient.on("connectionState", setConnState); return u; }, []);

  useEffect(() => {
    const u = agentClient.on("output", (tid, line) => {
      setActiveTask(at => {
        if (at && tid === at.id) {
          setOutputLines(p => [...p, line]);
          setChatMsgs(prev => {
            const next = prev.slice();
            const last = next[next.length - 1];
            if (last && last.role === "assistant") {
              next[next.length - 1] = { role: "assistant", text: last.text ? last.text + "\n" + line : line };
            } else {
              next.push({ role: "assistant", text: line });
            }
            return next;
          });
        }
        return at;
      });
    });
    return u;
  }, []);

  useEffect(() => { if (outputRef.current) outputRef.current.scrollTop = outputRef.current.scrollHeight; }, [outputLines, chatMsgs]);

  // Keep selectedRunner valid: pick the agent's default if it's installed, else
  // the first installed runner. Clears when the picker's choice disappears
  // (e.g. on reconnect to a different host where the runner isn't installed).
  useEffect(() => {
    const installed = runners.filter(r => r.installed);
    if (installed.length === 0) { setSelectedRunner(""); return; }
    if (selectedRunner && installed.some(r => r.id === selectedRunner)) return;
    const ready = installed.filter(r => r.ready !== false);
    const preferred =
      ready.find(r => r.isDefault || r.active) ||
      ready.find(r => r.id === "claude") ||
      ready.find(r => r.id === "opencode") ||
      ready.find(r => r.id === "codex") ||
      installed.find(r => r.isDefault || r.active) ||
      installed[0];
    setSelectedRunner(preferred.id);
  }, [runners, selectedRunner]);

  useEffect(() => {
    if (!token) { setPendingInvites([]); return; }
    let cancelled = false;
    const load = async () => {
      try {
        const res = await fetchGuestHosts(token);
        if (!cancelled) setPendingInvites(res.pending || []);
      } catch {
        if (!cancelled) setPendingInvites([]);
      }
    };
    load();
    const iv = setInterval(load, 30000);
    return () => { cancelled = true; clearInterval(iv); };
  }, [token]);

  const reauthDevice = async (d: Device) => {
    if (!token) return;
    setReauthBusy(d.id);
    setReauthMsg(null);
    try {
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
        // Agent's next heartbeat clears needsAuth on Convex side.
        setTimeout(refreshDevices, 2500);
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

  const acceptInvite = async (invite: GuestInvitation) => {
    if (!token) return;
    const key = invite.inviteId || invite.inviteCode || invite.hostUserId;
    setInvitesBusy(key);
    try {
      await acceptGuestInvitation(token, invite.hostUserId);
      setPendingInvites((prev) =>
        prev.filter((p) => (p.inviteId || p.inviteCode || p.hostUserId) !== key),
      );
      refreshDevices();
    } catch (e: any) {
      alert(e?.message || "Failed to accept invitation");
    } finally {
      setInvitesBusy(null);
    }
  };

  useEffect(() => {
    if (!isConnected) return;
    const load = async () => { try { setTasks(await agentClient.listTasks(20)); } catch {} };
    load(); const iv = setInterval(load, 10000); return () => clearInterval(iv);
  }, [isConnected]);

  useEffect(() => {
    if (!isConnected) return;
    const poll = async () => { try { setTodoCount(await agentClient.todoCount()); } catch {} };
    poll(); const iv = setInterval(poll, 30000); return () => clearInterval(iv);
  }, [isConnected]);

  // ── Actions ─────────────────────────────────────────────────────

  const connectToDevice = async (device: Device) => {
    if (!token) return;
    setConnectedDevice(device);
    setConnectError(null);
    setConnectDiagnostics([]);

    // Wait for relay config to be loaded (web dashboard MUST use relay)
    if (relayReadyPromiseRef.current) {
      await relayReadyPromiseRef.current;
    }

    const tunnelUrls = Array.from(
      new Set(
        [
          ...(Array.isArray(device.publicEndpoints) ? device.publicEndpoints : []),
          ...(device.tunnelUrl ? [device.tunnelUrl] : []),
        ]
          .map((url) => String(url || "").trim())
          .filter(Boolean),
      ),
    );

    try {
      await agentClient.connect(device.host, device.port, token, device.id, { tunnelUrls });
      setConnectDiagnostics(agentClient.lastConnectDiagnostics);
      try { setAgentInfo(await agentClient.getInfo()); } catch {}
      try { setRunners(await agentClient.getRunners()); } catch {}
    } catch (err: any) {
      const firstDiagnostics = agentClient.lastConnectDiagnostics;
      const canTryAutoReauth = Boolean(token && device.id && agentClient.configuredRelayServers.length > 0);

      if (canTryAutoReauth) {
        setConnectError("Connection failed. Trying automatic re-auth recovery…");
        setConnectDiagnostics(firstDiagnostics);
        try {
          const recovered = await agentClient.reauthAgent({
            deviceId: device.id,
            hostSessionToken: token,
            convexSiteUrl: CONVEX_URL,
          });
          if (recovered.ok) {
            await agentClient.connect(device.host, device.port, token, device.id, { tunnelUrls });
            setConnectError(null);
            setConnectDiagnostics(agentClient.lastConnectDiagnostics);
            try { setAgentInfo(await agentClient.getInfo()); } catch {}
            try { setRunners(await agentClient.getRunners()); } catch {}
            return;
          }
        } catch {}
      }

      setConnectError(err?.message || "Could not connect to device");
      setConnectDiagnostics(agentClient.lastConnectDiagnostics);
    }
  };

  const disconnect = () => { agentClient.disconnect(); setConnectedDevice(null); setAgentInfo(null); setTasks([]); setActiveTask(null); setOutputLines([]); setChatMsgs([]); setRunners([]); setSelectedRunner(""); setConnectError(null); };

  const refreshConnectedRunners = async () => {
    if (!isConnected) return;
    try {
      setRunners(await agentClient.getRunners());
    } catch {}
  };

  const handleSend = async (e?: React.FormEvent) => {
    e?.preventDefault();
    const text = input.trim(); if (!text || sending) return;
    const targetRunner = runners.find((r) => r.id === (activeTask?.runnerId || selectedRunner)) || runners.find((r) => r.id === selectedRunner) || null;
    const authIssue = runnerAuthIssue(targetRunner);
    if (authIssue) {
      if (targetRunner && (targetRunner.id === "claude" || targetRunner.id === "codex")) {
        setChatRunnerAuthModal(targetRunner.id);
      }
      return;
    }
    setInput(""); setSending(true);
    const continuing = activeTask?.status === "running";
    // Optimistic user echo — always push the user bubble + empty assistant placeholder
    // so the next streamed line flows into the assistant bubble, not into the last
    // run's response.
    setChatMsgs(prev => {
      const base = continuing ? prev : [];
      return [...base, { role: "user", text }, { role: "assistant", text: "" }];
    });
    if (!continuing) setOutputLines([]);
    try {
      if (continuing) {
        await agentClient.continueTask(activeTask!.id, text);
      } else {
        const t = await agentClient.sendTask(text.slice(0, 80), text, selectedRunner ? { runner: selectedRunner } : undefined);
        setActiveTask(t);
        setTasks(p => [t, ...p]);
      }
    } catch (err: any) {
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

  const selectTask = (t: Task) => {
    setActiveTask(t);
    setOutputLines(t.output || []);
    // Prefer the task's recorded turns (every user continue + agent reply)
    // so multi-turn history survives a sidebar navigation. Fall back to
    // [initial prompt, flattened output] when the agent didn't expose turns.
    let msgs: ChatMsg[];
    if (t.turns && t.turns.length > 0) {
      msgs = t.turns.map(tn => ({ role: tn.role, text: tn.content }));
    } else {
      msgs = [];
      const userText = displayTaskTitle(t.title || "");
      if (userText) msgs.push({ role: "user", text: userText });
      const out = (t.output || []).join("\n");
      if (out) msgs.push({ role: "assistant", text: out });
      else if (t.status === "running") msgs.push({ role: "assistant", text: "" });
    }
    setChatMsgs(msgs);
    setActiveTab("chat");
  };
  const onTaskCreated = () => { setActiveTab("chat"); agentClient.listTasks().then(setTasks).catch(() => {}); };
  const handleSelectPreviewTarget = async (deviceId: string | null) => {
    const target = deviceId ? devices.find((d) => d.id === deviceId) || null : null;
    setPreviewTargetId(deviceId);
    try {
      await agentClient.setDevServerTarget({
        targetDeviceId: target?.id,
        targetDeviceName: target?.name,
        targetDeviceClass: target?.deviceClass,
      });
    } catch {}
  };

  // ── Conditional renders (NO hooks below this point) ─────────────

  if (isLoading) return <div className="flex min-h-[80vh] items-center justify-center"><div className="h-6 w-6 animate-spin rounded-full border-2 border-surface-600 border-t-emerald-400" /></div>;

  if (!isAuthenticated) return (
    <div className="flex min-h-[80vh] items-center justify-center">
      <div className="text-center">
        <h2 className="text-lg font-semibold text-surface-100 mb-4">Sign in to continue</h2>
        <a href="/auth?return=/dashboard" className="rounded-lg bg-surface-100 px-6 py-3 text-sm font-medium text-surface-900 hover:bg-surface-50">Sign In</a>
      </div>
    </div>
  );

  const displayDevices = devices.map((device) => {
    const peer = peerStates[device.id];
    const probe = probeStates[device.id];
    const workspaceLive =
      connectedDevice?.id === device.id &&
      (connState === "connected" || connState === "connecting");
    const next = {
      ...device,
      workspaceLive,
      peerState: peer?.state ?? device.peerState,
      peerLastSeen: peer?.lastSeen ?? device.peerLastSeen,
      probeState: workspaceLive ? "ok" : probe?.ok ? "ok" : probe?.authExpired ? "auth-expired" : probe ? "unreachable" : device.probeState,
      probePath: workspaceLive ? device.probePath : probe?.path ?? device.probePath,
      probeCheckedAt: probe?.checkedAt ?? device.probeCheckedAt,
      probeError: probe?.error ?? device.probeError,
      probeInfo: probe?.info ?? device.probeInfo,
      online: workspaceLive || probe?.ok === true || (peer?.state === "online" ? true : device.online),
      lastSeen: (() => {
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
  const runningTask = tasks.find(t => t.status === "running");
  const activeRunnerId = activeTask?.runnerId || selectedRunner;
  const activeRunnerRow = runners.find((r) => r.id === activeRunnerId) || null;
  const activeRunnerAuthIssue = runnerAuthIssue(activeRunnerRow);
  const canStartBrowserRunnerAuth = Boolean(activeRunnerRow && (activeRunnerRow.id === "claude" || activeRunnerRow.id === "codex"));
  const mobileWorkers = displayDevices.filter((d) => d.deviceClass === "edge-mobile");
  const dormantDevices = displayDevices.filter((d) => isDormantUnreachableDevice(d));
  const visibleDevices = displayDevices.filter((d) => !isDormantUnreachableDevice(d));
  const selectedPreviewTarget = mobileWorkers.find((d) => d.id === previewTargetId) || null;
  const tabs: { id: typeof activeTab; label: string; icon: string; badge?: number }[] = [
    { id: "devices", label: "Devices", icon: "\uD83D\uDCBB" },
    { id: "chat", label: "Chat", icon: "\uD83D\uDCAC" },
    { id: "projects", label: "Projects", icon: "\uD83D\uDCC1" },
    { id: "vibe", label: "Vibe", icon: "\u2328\uFE0F" },
    { id: "todos", label: "Todos", icon: "\u2611\uFE0F", badge: todoCount },
    { id: "builds", label: "Builds", icon: "\uD83D\uDE80" },
    { id: "preview", label: "Preview", icon: "\uD83C\uDFA8" },
    { id: "web-reload", label: "Web Reload", icon: "\uD83C\uDF10" },
    { id: "health", label: "Health", icon: "\uD83D\uDCCA" },
    { id: "quality", label: "Quality", icon: "\u2705" },
    { id: "data", label: "Data", icon: "\uD83D\uDDC4\uFE0F" },
    { id: "switch", label: "Switch", icon: "\uD83D\uDD04" },
    { id: "accounts", label: "Accounts", icon: "\uD83D\uDD11" },
    { id: "console", label: "Console", icon: "\uD83D\uDCBB" },
    { id: "infra", label: "Infra", icon: "\uD83D\uDEE0\uFE0F" },
    { id: "connect", label: "Connect", icon: "\uD83C\uDF10" },
    { id: "tools", label: "Tools", icon: "\uD83E\uDDE9" },
    { id: "observ", label: "Observ", icon: "\uD83D\uDCCA" },
    { id: "ops", label: "Ops", icon: "\uD83D\uDE80" },
    { id: "extras", label: "Extras", icon: "\u2699\uFE0F" },
    { id: "share", label: "Share", icon: "\uD83D\uDCE3" },
    { id: "guests", label: "Guests", icon: "\uD83D\uDC65" },
    { id: "convex", label: "Convex", icon: "\u26A1" },
    { id: "storage", label: "Storage", icon: "\uD83D\uDCC2" },
    { id: "vault", label: "Vault", icon: "\uD83D\uDD12" },
    { id: "apikeys", label: "Yaver Tokens", icon: "\uD83D\uDD11" },
    { id: "schedules", label: "Schedules", icon: "\u23F0" },
    { id: "phone", label: "Phone Backend", icon: "\u26A1" },
    { id: "domains", label: "Domains", icon: "\uD83C\uDF10" },
    { id: "exec", label: "Exec", icon: "\u2699\uFE0F" },
    { id: "security", label: "Security", icon: "\uD83D\uDD10" },
    { id: "morning", label: "Morning", icon: "\u2600\uFE0F" },
  ];

  return (
    <div className="relative flex min-h-[calc(100vh-4rem)] flex-col md:flex-row">
      {/* Mobile top bar — visible only below md */}
      <div className="md:hidden border-b border-surface-800 bg-surface-900/50">
        <div className="flex items-center gap-2 px-3 py-2">
          <div className="flex h-6 w-6 items-center justify-center rounded-full bg-surface-800 text-[10px] font-bold text-surface-300">{user?.email?.charAt(0).toUpperCase()}</div>
          <span className="text-xs font-medium text-surface-200 flex-1 truncate">{connectedDevice?.name || "No device"}</span>
          <span className={`h-1.5 w-1.5 rounded-full ${isConnected ? "bg-emerald-400" : "bg-surface-600"}`} />
          <button
            onClick={logout}
            className="inline-flex items-center gap-1 rounded-md border border-red-500/30 px-2 py-1 text-[10px] font-semibold text-red-300 transition-colors hover:border-red-400/50 hover:bg-red-500/10 hover:text-red-200"
            title="Sign out of Yaver"
            aria-label="Sign out"
          >
            <svg
              width="12"
              height="12"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
              aria-hidden
            >
              <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
              <polyline points="16 17 21 12 16 7" />
              <line x1="21" y1="12" x2="9" y2="12" />
            </svg>
            <span>Out</span>
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
      <aside className="hidden md:flex w-60 flex-col border-r border-surface-800 bg-surface-900/50 shrink-0 overflow-y-auto">
        <div className="p-3 space-y-4">
          {/* User */}
          <div className="flex items-center gap-2 rounded-lg border border-surface-800/50 px-3 py-2">
            <div className="flex h-6 w-6 items-center justify-center rounded-full bg-surface-800 text-[10px] font-bold text-surface-300">{user?.email?.charAt(0).toUpperCase()}</div>
            <p className="truncate text-xs text-surface-300">{user?.name || user?.email}</p>
          </div>

          {/* Nav */}
          <nav className="flex flex-col gap-[2px]">
            {([
              { id: "devices",  label: "Devices",  icon: "💻" },
              { id: "chat",     label: "Chat",     icon: "💬" },
              { id: "projects", label: "Projects", icon: "📁" },
              { id: "git",      label: "Git",      icon: "⎇" },
              { id: "builds",   label: "Builds",   icon: "🛠️" },
              { id: "preview",  label: "Hot Reload", icon: "📱" },
              { id: "web-reload", label: "Web Reload", icon: "🌐" },
              { id: "guests",   label: "Guests",   icon: "👥" },
              { id: "vault",    label: "Vault",    icon: "🔐" },
            ] as const).map((it) => (
              <button
                key={it.id}
                onClick={() => setActiveTab(it.id)}
                className={`flex items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs transition-colors ${
                  activeTab === it.id
                    ? "bg-indigo-500/10 text-indigo-300"
                    : "text-surface-400 hover:bg-surface-800 hover:text-surface-200"
                }`}
              >
                <span className="w-4 text-center text-[13px]">{it.icon}</span>
                <span>{it.label}</span>
              </button>
            ))}
          </nav>

          {/* Devices (lean) */}
          <div>
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
            {isConnected && connectedDevice ? (
              (() => {
                const connectedNeedsAuth = !!connectedDevice.needsAuth && !connectedDevice.isGuest;
                const connectedIsReauthing = reauthBusy === connectedDevice.id;
                const connectedReauthMsg =
                  reauthMsg && reauthMsg.deviceId === connectedDevice.id ? reauthMsg : null;
                const pillBorder = connectedNeedsAuth
                  ? "border-amber-500/40 bg-amber-500/10"
                  : "border-emerald-500/30 bg-emerald-500/5";
                const dotColor = connectedNeedsAuth
                  ? (connectedIsReauthing ? "bg-amber-400 animate-pulse" : "bg-amber-400")
                  : "bg-emerald-400";
                return (
                  <div className={`rounded-md border ${pillBorder} px-2 py-1.5`}>
                    <div className="flex items-center gap-2">
                      <span className={`h-2 w-2 shrink-0 rounded-full ${dotColor}`} />
                      <span className="truncate text-xs font-medium text-surface-100">{connectedDevice.name}</span>
                    </div>
                    <div className="mt-1 flex items-center justify-between gap-2">
                      <span className="truncate text-[10px] text-surface-500">
                        {devicePlatformLabel(connectedDevice)}
                        {agentInfo ? ` · v${agentInfo.version}` : ""}
                        {connectedNeedsAuth ? " · needs auth" : ""}
                      </span>
                      <div className="flex shrink-0 items-center gap-2">
                        {connectedNeedsAuth ? (
                          <button
                            onClick={() => reauthDevice(connectedDevice)}
                            disabled={connectedIsReauthing}
                            title="Agent's session token expired — re-auth so /projects, runners, and tasks accept your bearer again"
                            className="rounded bg-amber-500/20 px-1.5 py-0.5 text-[9px] font-semibold uppercase tracking-wide text-amber-200 hover:bg-amber-500/30 disabled:opacity-40"
                          >
                            {connectedIsReauthing ? "…" : "Re-auth"}
                          </button>
                        ) : null}
                        <button onClick={disconnect} className="text-[10px] text-red-400 hover:text-red-300">disconnect</button>
                      </div>
                    </div>
                    {connectedReauthMsg ? (
                      <div className={`mt-1 text-[10px] leading-tight ${connectedReauthMsg.ok ? "text-emerald-300" : "text-red-300"}`}>
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
                {visibleDevices.slice(0, 5).map((d) => {
                  const isSelected = connectedDevice?.id === d.id;
                  const isConnecting = isSelected && connState === "connecting";
                  const hasError = isSelected && connState === "error";
                  const needsAuth = !!d.needsAuth && !d.isGuest;
                  const isReauthing = reauthBusy === d.id;
                  const reachableNow = d.workspaceLive || d.probeState === "ok";
                  const authExpired = d.probeState === "auth-expired" || needsAuth;
                  const dotClass = hasError
                    ? "bg-red-400"
                    : isConnecting || isReauthing
                      ? "bg-amber-400 animate-pulse"
                      : authExpired
                        ? "bg-amber-400"
                        : reachableNow
                          ? "bg-cyan-400"
                          : d.online
                            ? "bg-emerald-400"
                          : "bg-surface-600";
                  const wrapClass = hasError
                    ? "border border-red-500/30 bg-red-500/5"
                    : isConnecting
                      ? "border border-amber-500/30 bg-amber-500/5"
                      : authExpired
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
                          {d.isGuest ? (
                            <span className="shrink-0 rounded bg-sky-500/15 px-1 text-[9px] uppercase text-sky-300">shared</span>
                          ) : null}
                        </button>
                        {authExpired ? (
                          <button
                            onClick={() => reauthDevice(d)}
                            disabled={isReauthing}
                            title="Agent's session token expired — click to re-auth via /auth/recover"
                            className="mr-1 shrink-0 rounded bg-amber-500/20 px-1.5 py-0.5 text-[9px] font-semibold uppercase tracking-wide text-amber-200 hover:bg-amber-500/30 disabled:opacity-40"
                          >
                            {isReauthing ? "…" : "Re-auth"}
                          </button>
                        ) : null}
                      </div>
                      {showReauthMsg ? (
                        <div
                          className={`px-2 pb-1 text-[10px] leading-tight ${
                            reauthMsg!.ok ? "text-emerald-300" : "text-red-300"
                          }`}
                        >
                          {reauthMsg!.text}
                        </div>
                      ) : null}
                    </div>
                  );
                })}
                {visibleDevices.length > 5 ? (
                  <button
                    onClick={() => setActiveTab("devices")}
                    className="w-full px-2 text-left text-[10px] text-surface-500 hover:text-surface-300"
                  >
                    +{visibleDevices.length - 5} more
                  </button>
                ) : null}
                {dormantDevices.length > 0 ? (
                  <button
                    onClick={() => setActiveTab("devices")}
                    className="w-full rounded-md border border-amber-500/20 bg-amber-500/5 px-2 py-1.5 text-left text-[10px] text-amber-200 hover:bg-amber-500/10"
                    title="Open the Devices tab to reveal stale hidden devices"
                  >
                    {dormantDevices.length} stale device{dormantDevices.length === 1 ? "" : "s"} hidden
                  </button>
                ) : null}
              </div>
            )}
          </div>

          {/* Invite */}
          <button
            onClick={() => setActiveTab("guests")}
            className="w-full rounded-md border border-indigo-500/30 bg-indigo-500/10 px-3 py-1.5 text-xs font-medium text-indigo-200 hover:bg-indigo-500/15"
            title="Invite someone to share this machine (scope by machines, agents, projects)"
          >
            + Invite guest
          </button>

          {/* Pending invites — auto-match by signed-in email, no code required */}
          {pendingInvites.length > 0 && (
            <div>
              <p className="text-[10px] font-semibold uppercase tracking-widest text-surface-500 mb-1">
                Pending invites
              </p>
              <div className="flex flex-col gap-1.5">
                {pendingInvites.map((inv) => {
                  const key = inv.inviteId || inv.inviteCode || inv.hostUserId;
                  const busy = invitesBusy === key;
                  return (
                    <div
                      key={key}
                      className="rounded-md border border-emerald-500/30 bg-emerald-500/10 p-2"
                    >
                      <div className="text-[11px] font-medium text-emerald-200 truncate">
                        {inv.hostName || "Yaver host"}
                      </div>
                      <div className="text-[10px] text-emerald-400/80 truncate mb-1.5">
                        {inv.hostEmail}
                      </div>
                      <button
                        onClick={() => acceptInvite(inv)}
                        disabled={busy}
                        className="w-full rounded bg-emerald-500 px-2 py-1 text-[11px] font-medium text-white hover:bg-emerald-400 disabled:opacity-40"
                      >
                        {busy ? "Joining…" : "Accept"}
                      </button>
                    </div>
                  );
                })}
              </div>
            </div>
          )}

          {/* Guest code */}
          <div>
            <p className="text-[10px] font-semibold uppercase tracking-widest text-surface-500 mb-1">Join as Guest</p>
            <div className="flex gap-1.5">
              <input value={guestCode} onChange={e => setGuestCode(e.target.value.toUpperCase())} maxLength={6}
                placeholder="CODE" className="flex-1 rounded-md border border-surface-800 bg-surface-900 px-2 py-1.5 text-xs text-center font-mono tracking-widest text-surface-200 placeholder-surface-600 outline-none focus:border-indigo-500" />
              <button onClick={async () => {
                if (guestCode.trim().length < 4) return;
                try {
                  const res = await fetch(`${CONVEX_URL}/guests/accept-code`, { method: "POST", headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` }, body: JSON.stringify({ inviteCode: guestCode.trim() }) });
                  const data = await res.json();
                  if (data.ok || data.hostName) { alert(`Joined ${data.hostName || "host"}'s machine!`); setGuestCode(""); refreshDevices(); }
                  else alert(data.error || "Invalid code");
                } catch (e: any) { alert(e.message); }
              }} disabled={guestCode.trim().length < 4}
                className="rounded-md bg-indigo-500 px-3 py-1.5 text-[11px] font-medium text-white hover:bg-indigo-400 disabled:opacity-30">Join</button>
            </div>
          </div>

          {/* Tasks */}
          {tasks.length > 0 && (
            <div>
              <p className="text-[10px] font-semibold uppercase tracking-widest text-surface-500 mb-1">Tasks</p>
              {tasks.slice(0, 12).map(t => (
                <button key={t.id} onClick={() => selectTask(t)} className={`w-full text-left rounded-md px-2 py-1 text-[11px] truncate mb-0.5 ${activeTask?.id === t.id ? "bg-indigo-500/10 text-indigo-400" : "text-surface-400 hover:bg-surface-800"}`}>
                  <span className={`inline-block h-1 w-1 rounded-full mr-1 ${t.status === "running" ? "bg-amber-400" : t.status === "completed" ? "bg-emerald-400" : "bg-surface-600"}`} />{displayTaskTitle(t.title)}
                </button>
              ))}
            </div>
          )}
        </div>
        <div className="mt-auto border-t border-surface-800 p-3 flex items-center justify-end gap-2">
          <button onClick={toggleTheme} className="rounded-md p-1.5 text-surface-500 hover:text-surface-300 hover:bg-surface-800" title={theme === "dark" ? "Switch to light mode" : "Switch to dark mode"}>
            {theme === "dark" ? (
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="5"/><line x1="12" y1="1" x2="12" y2="3"/><line x1="12" y1="21" x2="12" y2="23"/><line x1="4.22" y1="4.22" x2="5.64" y2="5.64"/><line x1="18.36" y1="18.36" x2="19.78" y2="19.78"/><line x1="1" y1="12" x2="3" y2="12"/><line x1="21" y1="12" x2="23" y2="12"/><line x1="4.22" y1="19.78" x2="5.64" y2="18.36"/><line x1="18.36" y1="5.64" x2="19.78" y2="4.22"/></svg>
            ) : (
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>
            )}
          </button>
        </div>
      </aside>

      {/* Main */}
      <div className="flex min-w-0 flex-1 flex-col overflow-hidden">
        <div className="hidden md:flex sticky top-0 z-20 items-center justify-end gap-2 border-b border-surface-800 bg-surface-950/90 px-4 py-2 backdrop-blur">
          <button onClick={toggleTheme} className="rounded-md p-1.5 text-surface-500 hover:bg-surface-800 hover:text-surface-300" title={theme === "dark" ? "Switch to light mode" : "Switch to dark mode"}>
            {theme === "dark" ? (
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="5"/><line x1="12" y1="1" x2="12" y2="3"/><line x1="12" y1="21" x2="12" y2="23"/><line x1="4.22" y1="4.22" x2="5.64" y2="5.64"/><line x1="18.36" y1="18.36" x2="19.78" y2="19.78"/><line x1="1" y1="12" x2="3" y2="12"/><line x1="21" y1="12" x2="23" y2="12"/><line x1="4.22" y1="19.78" x2="5.64" y2="18.36"/><line x1="18.36" y1="5.64" x2="19.78" y2="4.22"/></svg>
            ) : (
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>
            )}
          </button>
          <button
            onClick={logout}
            className="inline-flex items-center gap-1.5 rounded-md border border-red-500/30 px-2.5 py-1.5 text-[11px] font-semibold text-red-300 transition-colors hover:border-red-400/50 hover:bg-red-500/10 hover:text-red-200"
            title="Sign out of Yaver"
            aria-label="Sign out"
          >
            <svg
              width="13"
              height="13"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
              aria-hidden
            >
              <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
              <polyline points="16 17 21 12 16 7" />
              <line x1="21" y1="12" x2="9" y2="12" />
            </svg>
            <span>Sign Out</span>
          </button>
        </div>

        <div className="flex min-h-0 flex-1 flex-col overflow-y-auto">
          {!isConnected && activeTab === "chat" ? (
            <div className="flex-1 overflow-y-auto px-4 py-6 sm:px-6 lg:px-8">
              <div className="mx-auto w-full max-w-[1680px]">
                <div className="mb-6 text-center">
                  <h2 className="mb-2 text-lg font-semibold text-surface-100">Workspace</h2>
                {connState === "connecting" ? (
                  <div className="flex flex-col items-center gap-3">
                    <div className="h-6 w-6 animate-spin rounded-full border-2 border-surface-600 border-t-amber-400" />
                    <p className="text-sm text-surface-400">Connecting to {connectedDevice?.name}...</p>
                    <p className="text-xs text-surface-600">Trying relay servers</p>
                  </div>
                ) : connState === "error" ? (
                  (() => {
                    const authExpired = connectDiagnostics.some((d) => d.authExpired);
                    const anyReached = connectDiagnostics.some((d) => d.status && d.status > 0);
                    const anyRelayProbeTried = connectDiagnostics.some((d) => d.path === "relay");
                    const anyLoadFailed = connectDiagnostics.some((d) => d.path === "direct" && !anyReached);
                    const relayCount = agentClient.configuredRelayServers.length;
                    // Direct from an HTTPS web origin to http://LAN-IP:18080 is always
                    // blocked as mixed content. Surface that explicitly.
                    const mixedContentLikely =
                      anyLoadFailed && typeof window !== "undefined" && window.location.protocol === "https:";
                    const headline = authExpired
                      ? "Agent reachable, but its Convex session is expired"
                      : anyReached
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
                                  <span className={`shrink-0 ${d.authExpired ? "text-amber-300" : d.ok ? "text-emerald-300" : "text-red-300"}`}>
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
                              <span className={relayCount === 0 ? "text-red-300" : "text-surface-300"}>{relayCount}</span>
                              {relayCount > 0 && !anyRelayProbeTried ? (
                                <span className="ml-2 text-amber-300">(no relay probe attempted — device has no deviceId?)</span>
                              ) : null}
                            </div>
                            {mixedContentLikely ? (
                              <div className="text-amber-300">
                                Direct probe returned <code className="rounded bg-surface-900 px-1 font-mono">Load failed</code> because a browser on <code className="rounded bg-surface-900 px-1 font-mono">https://</code> can&apos;t fetch <code className="rounded bg-surface-900 px-1 font-mono">http://</code> LAN IPs (mixed content). The web path has to go through a relay.
                              </div>
                            ) : null}
                          </div>

                          {/* Re-auth — always offered on connection error. */}
                          <div className="mt-3 rounded border border-amber-500/20 bg-amber-500/5 p-2 text-left">
                            <p className="text-[11px] text-amber-300">
                              {authExpired
                                ? "Agent accepted the probe but its Convex session is stale. Hand your current session down to the box:"
                                : "Try handing your current session down to the box — works even if the agent&apos;s own token is dead, as long as one relay can reach it:"}
                            </p>
                            <div className="mt-2 flex items-center gap-2">
                              <button
                                disabled={reauthing || !connectedDevice || !token || relayCount === 0}
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
                                className="flex-1 rounded border border-amber-500/40 bg-amber-500/10 px-3 py-1.5 text-[11px] font-medium text-amber-200 hover:bg-amber-500/20 disabled:opacity-40"
                              >
                                {reauthing ? "Re-authing…" : relayCount === 0 ? "Re-auth (needs a relay)" : "Re-auth this device from web"}
                              </button>
                            </div>
                            {reauthMessage ? (
                              <pre className={`mt-2 whitespace-pre-wrap break-words font-mono text-[10px] ${reauthMessage.kind === "ok" ? "text-emerald-300" : "text-red-300"}`}>
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

                          {!anyReached && !mixedContentLikely && relayCount > 0 ? (
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
                          isSelected={false}
                          isConnecting={false}
                          onConnect={() => connectToDevice(d)}
                          onTogglePrimary={!d.isGuest && token ? async () => {
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
                          canTogglePrimary={!d.isGuest && !!token}
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
            <div className="flex-1 overflow-y-auto p-6 max-w-6xl mx-auto w-full"><OverviewView user={user ?? undefined} /></div>
          ) : activeTab === "projects" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-4xl mx-auto w-full"><ProjectsView onTaskCreated={onTaskCreated} mobileWorkers={mobileWorkers} selectedPreviewTarget={selectedPreviewTarget} onSelectPreviewTarget={handleSelectPreviewTarget} /></div>
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
              />
            </div>
          ) : activeTab === "todos" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-4xl mx-auto w-full"><TodosView onTaskCreated={onTaskCreated} /></div>
          ) : activeTab === "builds" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-4xl mx-auto w-full"><BuildsView onTaskCreated={onTaskCreated} preferredProjectPath={preferredSurfaceProjectPath} /></div>
          ) : activeTab === "preview" ? (
            <div className="flex-1 min-h-0"><PreviewPane
              selectedPreviewTarget={selectedPreviewTarget}
              onSelectPreviewTarget={handleSelectPreviewTarget}
              mobileWorkers={mobileWorkers}
              preferredProjectPath={preferredSurfaceProjectPath}
              onReconnect={connectedDevice ? async () => { await connectToDevice(connectedDevice); } : undefined}
              onRepairRelay={token ? async () => {
                const res = await fetch(`${CONVEX_URL}/settings/repair-relay`, {
                  method: "POST",
                  headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
                });
                if (!res.ok) {
                  const body = await res.json().catch(() => ({}));
                  throw new Error(body?.error || `repair HTTP ${res.status}`);
                }
                const body = await res.json();
                // Re-fetch relay config so agentClient picks up the
                // freshly-synced password before the next probe.
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
                  if (relays.length > 0) agentClient.setRelayServers(relays);
                } catch { /* non-fatal — next connect will re-read */ }
                return { repaired: !!body.repaired, reason: body.reason || "" };
              } : undefined}
            /></div>
          ) : activeTab === "web-reload" ? (
            <div className="flex-1 min-h-0 overflow-hidden">
              <WebReloadView
                connectedDevice={connectedDevice}
                connState={connState}
                preferredProjectPath={preferredSurfaceProjectPath}
                onReconnect={connectedDevice ? async () => { await connectToDevice(connectedDevice); } : undefined}
                onRepairRelay={token ? async () => {
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
                    if (relays.length > 0) agentClient.setRelayServers(relays);
                  } catch { /* non-fatal — next connect will re-read */ }
                  return { repaired: !!body.repaired, reason: body.reason || "" };
                } : undefined}
              />
            </div>
          ) : activeTab === "health" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-4xl mx-auto w-full"><HealthView /></div>
          ) : activeTab === "quality" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-4xl mx-auto w-full"><QualityView /></div>
          ) : activeTab === "data" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-5xl mx-auto w-full"><DataView /></div>
          ) : activeTab === "switch" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-5xl mx-auto w-full"><SwitchView /></div>
          ) : activeTab === "accounts" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-5xl mx-auto w-full"><AccountsView /></div>
          ) : activeTab === "console" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-6xl mx-auto w-full"><ConsoleView /></div>
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
          ) : activeTab === "tools" ? (
            <ToolsView devices={devices} />
          ) : activeTab === "observ" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-6xl mx-auto w-full"><ObservabilityView /></div>
          ) : activeTab === "ops" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-6xl mx-auto w-full"><OpsView /></div>
          ) : activeTab === "extras" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-6xl mx-auto w-full"><ExtrasView /></div>
          ) : activeTab === "share" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-3xl mx-auto w-full"><ShareView /></div>
          ) : activeTab === "guests" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-3xl mx-auto w-full"><GuestsStatusView /></div>
          ) : activeTab === "convex" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-5xl mx-auto w-full"><ConvexView /></div>
          ) : activeTab === "security" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-3xl mx-auto w-full"><TwoFactorView token={token} /></div>
          ) : activeTab === "morning" ? (
            <div className="flex-1 overflow-hidden w-full"><MorningView /></div>
          ) : activeTab === "storage" ? (
            <div className="flex-1 min-h-0 w-full"><StorageView /></div>
          ) : activeTab === "vault" ? (
            <div className="flex-1 min-h-0 w-full max-w-4xl mx-auto"><VaultView /></div>
          ) : activeTab === "apikeys" ? (
            <div className="flex-1 min-h-0 w-full max-w-4xl mx-auto"><APIKeysView /></div>
          ) : activeTab === "schedules" ? (
            <div className="flex-1 min-h-0 w-full max-w-4xl mx-auto"><SchedulesView /></div>
          ) : activeTab === "phone" ? (
            <div className="flex-1 min-h-0 w-full max-w-6xl mx-auto overflow-auto p-4"><PhoneProjectsView /></div>
          ) : activeTab === "domains" ? (
            <div className="flex-1 min-h-0 w-full max-w-5xl mx-auto">
              {token && user?.id ? <DomainsView token={token} userId={user.id} /> :
                <div className="p-6 text-xs text-surface-500">Sign in to manage custom domains.</div>}
            </div>
          ) : activeTab === "exec" ? (
            <div className="flex-1 min-h-0 w-full"><ExecView /></div>
          ) : activeTab === "devices" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-5xl mx-auto w-full">
              <DevicesView
                devices={displayDevices}
                onRefresh={refreshDevices}
                signedInEmail={user?.email}
                signedInProvider={undefined}
                token={token}
                onOpen={connectToDevice}
                onCloseWorkspace={disconnect}
                activeWorkspaceDeviceId={connectedDevice?.id ?? null}
                hiddenCount={hiddenIds.size}
              />
            </div>
          ) : activeTab === "git" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-[1600px] mx-auto w-full"><GitView onOpenSurface={(surface, projectPath) => {
              setPreferredSurfaceProjectPath(projectPath);
              setActiveTab(surface);
            }} /></div>
          ) : (
            <>
              <div className="flex flex-1 min-h-0">
                <div className="flex flex-1 min-w-0 flex-col">
                  {activeTask ? (
                    <>
                      <div className="flex items-center gap-3 border-b border-surface-800 px-4 py-2">
                        <span className={`h-1.5 w-1.5 rounded-full ${activeTask.status === "running" ? "animate-pulse bg-amber-400" : activeTask.status === "completed" ? "bg-emerald-400" : "bg-surface-600"}`} />
                        <span className="truncate text-sm font-medium text-surface-200">{displayTaskTitle(activeTask.title)}</span>
                        <span className={`text-[10px] ${statusColor(activeTask.status)}`}>{activeTask.status}</span>
                        {activeTask.costUsd != null && <span className="text-[10px] text-surface-600">${activeTask.costUsd.toFixed(3)}</span>}
                      </div>
                      <div ref={outputRef} className="flex-1 overflow-y-auto bg-surface-950 px-4 py-5">
                        {activeRunnerAuthIssue ? (
                          <div className="mx-auto mb-4 max-w-3xl rounded-2xl border border-amber-500/30 bg-amber-500/10 p-4 text-sm text-amber-100">
                            <div className="font-medium">{runnerLabel(activeRunnerId)} needs sign-in on {connectedDevice?.name || "this machine"}</div>
                            <div className="mt-1 text-xs leading-5 text-amber-200/80">{activeRunnerAuthIssue}</div>
                            {canStartBrowserRunnerAuth ? (
                              <button
                                type="button"
                                onClick={() => setChatRunnerAuthModal(activeRunnerRow!.id)}
                                className="mt-3 rounded-xl border border-amber-300/30 bg-amber-400/10 px-3 py-2 text-xs font-semibold text-amber-100 hover:bg-amber-400/15"
                              >
                                Sign in to {runnerLabel(activeRunnerRow?.id)}
                              </button>
                            ) : null}
                          </div>
                        ) : null}
                        {chatMsgs.length === 0 ? (
                          <div className="flex h-full items-center justify-center gap-2 text-[12px] text-surface-600">
                            {activeTask.status === "running" && <span className="h-3 w-3 animate-spin rounded-full border border-surface-500 border-t-transparent" />}
                            {activeTask.status === "running" ? "Working..." : "No messages yet"}
                          </div>
                        ) : (
                          <div className="mx-auto flex max-w-3xl flex-col gap-3">
                            {chatMsgs.map((m, i) => (
                              m.role === "user" ? (
                                <div key={i} className="flex justify-end">
                                  <div className="max-w-[80%] rounded-2xl rounded-br-sm bg-indigo-500 px-3.5 py-2 text-[13px] text-white whitespace-pre-wrap break-words shadow-sm">
                                    {m.text}
                                  </div>
                                </div>
                              ) : (
                                <div key={i} className="flex justify-start">
                                  <div className="max-w-[90%] rounded-2xl rounded-bl-sm bg-surface-800 px-3.5 py-2 font-mono text-[12px] leading-5 text-surface-100 whitespace-pre-wrap break-words shadow-sm">
                                    {m.text
                                      ? m.text
                                      : activeTask.status === "running"
                                        ? (<span className="inline-flex items-center gap-1 text-surface-400">
                                            <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-surface-400" />
                                            <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-surface-400 [animation-delay:150ms]" />
                                            <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-surface-400 [animation-delay:300ms]" />
                                          </span>)
                                        : (<span className="text-surface-500">({activeTask.status || "no response"})</span>)}
                                  </div>
                                </div>
                              )
                            ))}
                          </div>
                        )}
                      </div>
                    </>
                  ) : (
                    <div className="flex flex-1 items-center justify-center text-sm text-surface-600">Describe what you want to build</div>
                  )}
                </div>
              </div>
              <div className="border-t border-surface-800 bg-surface-900/50 px-4 py-3">
                <div className="mx-auto flex max-w-5xl flex-col gap-3">
                  {(() => {
                    const installed = runners.filter(r => r.installed);
                    if (installed.length === 0) {
                      return (
                        <div className="text-[11px] text-amber-400">
                          No AI runner installed on this machine. Install one (claude, codex, aider, opencode, ollama) and reconnect.
                        </div>
                      );
                    }
                    return (
                      <div className="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-surface-800 bg-surface-950/60 px-3 py-2">
                        <div className="flex flex-wrap items-center gap-1.5 text-[11px]">
                          <span className="text-surface-500">Runner</span>
                          {installed.map(r => {
                            const active = r.id === selectedRunner;
                            const warn = !r.ready && (r.error || r.warning);
                            return (
                              <button
                                key={r.id}
                                type="button"
                                onClick={() => setSelectedRunner(r.id)}
                                title={warn || (r.authConfigured ? `auth: configured` : undefined)}
                                className={`rounded-full border px-2.5 py-1 transition ${
                                  active
                                    ? "border-emerald-400/60 bg-emerald-400/10 text-emerald-200"
                                    : warn
                                      ? "border-amber-500/40 bg-surface-900 text-amber-300 hover:border-amber-400/70"
                                      : "border-surface-700 bg-surface-900 text-surface-300 hover:border-surface-500"
                                }`}
                              >
                                {r.name}
                                {warn && <span className="ml-1 opacity-60">!</span>}
                              </button>
                            );
                          })}
                        </div>
                        {activeRunnerId ? (
                          <div className="text-[11px] text-surface-500">
                            Active: <span className="text-surface-300">{runnerLabel(activeRunnerId)}</span>
                          </div>
                        ) : null}
                      </div>
                    );
                  })()}
                  {activeRunnerAuthIssue ? (
                    <div className="rounded-xl border border-amber-500/20 bg-amber-500/5 px-3 py-2 text-[11px] text-amber-200">
                      <span>{runnerLabel(activeRunnerId)} on {connectedDevice?.name || "this machine"} is not authenticated.</span>
                      {canStartBrowserRunnerAuth ? (
                        <button
                          type="button"
                          onClick={() => setChatRunnerAuthModal(activeRunnerRow!.id)}
                          className="ml-2 rounded-lg border border-amber-400/30 px-2.5 py-1 font-semibold text-amber-100 hover:bg-amber-400/10"
                        >
                          Sign in
                        </button>
                      ) : null}
                    </div>
                  ) : null}
                  <form onSubmit={handleSend} className="grid gap-2 md:grid-cols-[minmax(0,1fr),auto] md:items-end">
                    <textarea ref={inputRef} value={input} onChange={e => setInput(e.target.value)}
                      onKeyDown={e => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); handleSend(); } }}
                      placeholder={activeRunnerAuthIssue ? `Sign in to ${runnerLabel(activeRunnerId)} to continue on ${connectedDevice?.name || "this machine"}...` : activeTask ? "Message..." : "Build me a todo app..."} rows={1}
                      disabled={Boolean(activeRunnerAuthIssue)}
                      className="max-h-32 w-full resize-none rounded-xl border border-surface-700 bg-surface-950 px-4 py-3 text-sm text-surface-100 placeholder-surface-600 outline-none focus:border-surface-500" style={{ minHeight: "48px" }} />
                    <button type="submit" disabled={!input.trim() || sending || runners.filter(r => r.installed).length === 0 || Boolean(activeRunnerAuthIssue)}
                      className="h-12 shrink-0 rounded-xl bg-surface-100 px-5 text-sm font-medium text-surface-900 hover:bg-surface-50 disabled:opacity-30">
                      {sending ? "..." : activeTask ? "Send" : "Run"}
                    </button>
                  </form>
                </div>
              </div>
              {chatRunnerAuthModal ? (
                <RunnerAuthModal
                  runner={chatRunnerAuthModal}
                  deviceName={connectedDevice?.name || connectedDevice?.id || "this machine"}
                  onClose={() => {
                    setChatRunnerAuthModal(null);
                    void refreshConnectedRunners();
                  }}
                  onCompleted={() => {
                    void refreshConnectedRunners();
                  }}
                />
              ) : null}
            </>
          )}
        </div>
      </div>
    </div>
  );
}

function RunnerAuthModal({
  runner,
  deviceName,
  onClose,
  onCompleted,
}: {
  runner: string;
  deviceName: string;
  onClose: () => void;
  onCompleted: () => void;
}) {
  const [session, setSession] = useState<RunnerBrowserAuthSession | null>(null);
  const [startError, setStartError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const startedRef = useRef(false);

  useEffect(() => {
    if (startedRef.current) return;
    startedRef.current = true;
    (async () => {
      try {
        const started = await agentClient.startRunnerBrowserAuth(runner);
        setSession(started);
      } catch (error) {
        setStartError(error instanceof Error ? error.message : String(error));
      }
    })();
  }, [runner]);

  useEffect(() => {
    if (!session) return;
    if (session.status === "completed" || session.status === "failed" || session.status === "cancelled") {
      if (session.status === "completed") onCompleted();
      return;
    }
    const interval = setInterval(async () => {
      try {
        const next = await agentClient.getRunnerBrowserAuthStatus(session.id);
        setSession(next);
      } catch {}
    }, 1500);
    return () => clearInterval(interval);
  }, [session, onCompleted]);

  const terminal = session && ["completed", "failed", "cancelled"].includes(session.status);

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
                await agentClient.cancelRunnerBrowserAuth(session.id).catch(() => {});
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
          <div className="rounded-lg border border-red-500/30 bg-red-500/5 p-3 text-xs text-red-300">
            <div className="mb-1 font-semibold">Couldn&apos;t start sign-in</div>
            {startError}
          </div>
        ) : !session ? (
          <div className="rounded-lg border border-surface-800 bg-surface-800/40 p-3 text-xs text-surface-400">
            Starting the sign-in flow on the remote machine…
          </div>
        ) : session.status === "completed" ? (
          <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-4 text-sm text-emerald-200">
            <div className="mb-1 font-semibold">Signed in</div>
            <div className="text-xs text-emerald-300/80">{session.detail || "Auth stored on the remote machine."}</div>
          </div>
        ) : session.status === "failed" || session.status === "cancelled" ? (
          <div className="rounded-lg border border-red-500/30 bg-red-500/5 p-3 text-xs text-red-300">
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
                className="block truncate rounded-lg border border-indigo-500/40 bg-indigo-500/10 px-3 py-2.5 text-sm font-medium text-indigo-200 hover:bg-indigo-500/20"
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
            <p className="text-[10px] leading-relaxed text-surface-600">
              Once sign-in finishes in the browser, this dialog updates automatically and chat will re-enable on this machine.
            </p>
          </div>
        )}
      </div>
    </div>
  );
}
