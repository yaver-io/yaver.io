"use client";

import { useAuth } from "@/lib/use-auth";
import { useBetaStatus } from "@/lib/useBetaStatus";
import { acceptBetaInvite } from "@/lib/subscription";
import BetaWorkspaceView from "@/components/dashboard/BetaWorkspaceView";
import { useDevices, usePendingClaims, setDeviceAlias, type Device } from "@/lib/use-devices";
import WebShellModal from "@/components/dashboard/WebShellModal";
import RemoteDesktopModal from "@/components/dashboard/RemoteDesktopModal";
import { agentClient, type Task, type ConnectionState, type Runner, type AgentInfo, type ConnectAttemptDiagnostic, type DeviceStatusProbe } from "@/lib/agent-client";
import { CONVEX_URL } from "@/lib/constants";
import { fetchGuestHosts, acceptGuestInvitation, type GuestInvitation } from "@/lib/guests";
import { useState, useEffect, useRef, useCallback, useMemo } from "react";
import { useRouter } from "next/navigation";
import { useTheme } from "@/components/ThemeProvider";
import ProjectsView from "@/components/dashboard/ProjectsView";
import TodosView from "@/components/dashboard/TodosView";
import BuildsView from "@/components/dashboard/BuildsView";
import { DeployCapabilitiesView } from "@/components/dashboard/DeployCapabilitiesView";
import HealthView from "@/components/dashboard/HealthView";
import ScreenMonitorView from "@/components/dashboard/ScreenMonitorView";
import QualityView from "@/components/dashboard/QualityView";
import ConvexView from "@/components/dashboard/ConvexView";
import DataView from "@/components/dashboard/DataView";
import SwitchView from "@/components/dashboard/SwitchView";
import AccountsView from "@/components/dashboard/AccountsView";
import ObservabilityView from "@/components/dashboard/ObservabilityView";
import OpsView from "@/components/dashboard/OpsView";
import OverviewView from "@/components/dashboard/OverviewView";
import ExtrasView from "@/components/dashboard/ExtrasView";
import ShareView from "@/components/dashboard/ShareView";
import GuestsStatusView from "@/components/dashboard/GuestsStatusView";
import CollabView from "@/components/dashboard/CollabView";
import InfraView from "@/components/dashboard/InfraView";
import ConnectivityView from "@/components/dashboard/ConnectivityView";
import NetworkView from "@/components/dashboard/NetworkView";
import ToolsView from "@/components/dashboard/ToolsView";
import TwoFactorView from "@/components/dashboard/TwoFactorView";
import VaultView from "@/components/dashboard/VaultView";
import APIKeysView from "@/components/dashboard/APIKeysView";
import StorageView from "@/components/dashboard/StorageView";
import ArmCellView from "@/components/dashboard/ArmCellView";
import AppleTVCellView from "@/components/dashboard/AppleTVCellView";
import SchedulesView from "@/components/dashboard/SchedulesView";
import PackagesView from "@/components/dashboard/PackagesView";
import PhoneProjectsView from "@/components/dashboard/PhoneProjectsView";
import VibePreviewView from "@/components/dashboard/VibePreviewView";
import ExecView from "@/components/dashboard/ExecView";
import DomainsView from "@/components/dashboard/DomainsView";
import CompanyAIOptionsView from "@/components/dashboard/CompanyAIOptionsView";
import CompanionView from "@/components/dashboard/CompanionView";
import VibeCodingView, { ASSISTANT_MARKDOWN_COMPONENTS, stripAnsi } from "@/components/dashboard/VibeCodingView";
import ReactMarkdown from "react-markdown";
import PendingClaimsSection from "@/components/dashboard/PendingClaimsSection";
import WebviewView from "@/components/dashboard/WebviewView";
import GitView from "@/components/dashboard/GitView";
import DevicesView, { preferredDefaultModelForRunner, preferredDefaultRunnerForDevice, usePrimaryRunnerByDevice, RUNNER_WHITELIST_SET, OPENCODE_PROVIDER_CATALOGUE } from "@/components/dashboard/DevicesView";
import BillingView from "@/components/dashboard/BillingView";
import StoresView from "@/components/dashboard/StoresView";
import { ManagedCloudPanel } from "@/components/dashboard/ManagedCloudPanel";
import { CapabilityShelf } from "@/components/dashboard/CapabilityShelf";
import StudioPanel from "@/components/dashboard/StudioPanel";
import QAPanel from "@/components/dashboard/QAPanel";
import WebTestsPanel from "@/components/dashboard/WebTestsPanel";
import SettingsView from "@/components/dashboard/SettingsView";
import type { RunnerBrowserAuthSession } from "@/lib/agent-client";
import webPkg from "../../package.json";

const WEB_VERSION = (webPkg as { version?: string }).version ?? "unknown";

function statusColor(s: string) {
  if (s === "running") return "text-amber-400";
  if (s === "review") return "text-violet-400";
  if (s === "completed") return "text-emerald-400";
  return "text-surface-400";
}

type ChatMsg = { role: "user" | "assistant"; text: string; queued?: boolean };
type OpenCodeAgentRow = { name: string; model?: string; isBuiltin?: boolean };

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
  maxAgeMs = 360_000,
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

type DeviceLifecycleState =
  | "offline"
  | "bootstrap"
  | "yaver-auth-expired"
  | "ready-to-connect"
  | "connected";

function deriveDeviceLifecycleState(
  device: Pick<Device, "online" | "needsAuth" | "peerState" | "workspaceLive" | "probeState" | "lastTunnelEvent" | "probeInfo">,
): DeviceLifecycleState {
  if (device.workspaceLive) return "connected";
  const lifecycleState = String(device.probeInfo?.lifecycle?.state || device.probeInfo?.lifecycleState || "");
  if (
    lifecycleState === "bootstrap" ||
    lifecycleState === "yaver-auth-expired" ||
    lifecycleState === "ready-to-connect"
  ) {
    return lifecycleState as DeviceLifecycleState;
  }
  if (device.needsAuth && (device.online || device.peerState === "online" || device.peerState === "stale" || hasRecentLiveSignal(device))) return "bootstrap";
  if (device.probeState === "auth-expired") return "yaver-auth-expired";
  if (
    device.probeState === "ok" ||
    device.peerState === "online" ||
    device.peerState === "stale" ||
    device.online ||
    hasRecentLiveSignal(device)
  ) {
    return "ready-to-connect";
  }
  return "offline";
}

const DORMANT_DEVICE_HIDE_MS = 10 * 60 * 1000;

function isDormantUnreachableDevice(
  device: Pick<Device, "online" | "needsAuth" | "lastSeen" | "publicEndpoints" | "tunnelUrl" | "isGuest" | "peerState" | "workspaceLive" | "probeState" | "probeInfo">,
): boolean {
  if (device.isGuest) return false;
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
  if (device.accessScope === "shared-scoped") return "border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-200";
  if (device.accessScope === "shared-legacy") return "border-violet-500/40 bg-violet-500/10 text-violet-700 dark:text-violet-200";
  if (device.isGuest) return "border-sky-500/40 bg-sky-500/10 text-sky-700 dark:text-sky-200";
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

// Inline alias editor: click the chip to edit, Enter to save, Esc
// to cancel. Shown only for devices the caller owns (the API rejects
// alias writes on guest devices). When no alias is set we render a
// muted "+ alias" affordance so the slot is still discoverable.
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
  const shareSummary = deviceAccessSummary(device);
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
            {!device.isGuest && token ? (
              <DeviceAliasChip
                device={device}
                token={token}
                onSaved={() => { if (onAliasSaved) onAliasSaved(); }}
              />
            ) : device.alias ? (
              <span className="rounded-full border border-emerald-500/20 bg-emerald-500/5 px-2 py-0.5 font-mono text-[10px] text-emerald-700 dark:text-emerald-300/80">@{device.alias}</span>
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
            {device.isGuest && device.hostName ? ` · from ${device.hostName}` : ""}
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
        {accessScopeLabel(device) ? (
          <span className={`rounded-full border px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.14em] ${accessScopeTone(device)}`}>
            {accessScopeLabel(device)}
          </span>
        ) : null}
        {!device.isGuest && device.sharedWithGuests ? (
          <span className="rounded-full border border-sky-500/40 bg-sky-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.14em] text-sky-700 dark:text-sky-200">
            Shared
          </span>
        ) : null}
        {device.deviceClass ? (
          <span className="rounded-full border border-sky-500/30 bg-sky-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.14em] text-sky-700 dark:text-sky-200">
            {device.deviceClass === "edge-mobile" ? "Edge Worker" : device.deviceClass}
          </span>
        ) : null}
        {device.sessionBinding ? (
          <span className={`rounded-full border px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.14em] ${
            device.sessionBinding === "dedicated"
              ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-200"
              : "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-200"
          }`}>
            {device.sessionBinding === "dedicated" ? "Dedicated Session" : "Legacy Session"}
          </span>
        ) : null}
        {isPrimary ? (
          <span className="rounded-full border border-indigo-500/40 bg-indigo-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.14em] text-indigo-700 dark:text-indigo-200">
            Primary
          </span>
        ) : null}
        {device.priorityMode === "spare-capacity" ? (
          <span className="rounded-full border border-violet-500/40 bg-violet-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.14em] text-violet-700 dark:text-violet-200">
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
                ? "border-sky-500/40 bg-sky-500/10 text-sky-700 dark:text-sky-200"
                : "border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-200"
            }`}>
              {shareSummary.projectLabel}
            </span>
          ) : null}
          {shareSummary.projectChips.map((project) => (
            <span key={`${device.id}:project:${project}`} className="rounded-full border border-amber-500/30 bg-amber-500/10 px-2 py-0.5 text-[10px] font-medium text-amber-800 dark:text-amber-100">
              {project}
            </span>
          ))}
        </div>
      ) : null}

      {shareSummary && shareSummary.guestChips.length > 0 ? (
        <div className="mt-2 flex flex-wrap items-center gap-1.5">
          <span className="rounded-full border border-sky-500/40 bg-sky-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.14em] text-sky-700 dark:text-sky-200">
            Shared With
          </span>
          {shareSummary.guestChips.map((guest) => (
            <span key={`${device.id}:guest:${guest}`} className="rounded-full border border-sky-500/30 bg-sky-500/10 px-2 py-0.5 text-[10px] font-medium text-sky-800 dark:text-sky-100">
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
          <span className="rounded-full border border-violet-500/40 bg-violet-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.14em] text-violet-700 dark:text-violet-200">
            Agents
          </span>
          {shareSummary.runnerChips.map((runner) => (
            <span key={`${device.id}:runner:${runner}`} className="rounded-full border border-violet-500/30 bg-violet-500/10 px-2 py-0.5 text-[10px] font-medium text-violet-800 dark:text-violet-100">
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
// billing, cloud, build, settings, security, home, domains, share, guests,
// collab, company-ai, infra) and the self-gating preview tabs (vibe, webview,
// preview, web-reload) are intentionally excluded.
const CONNECTION_REQUIRED_TABS = new Set<string>([
  "chat", "projects", "vault", "storage", "ops", "git", "data", "convex",
  "schedules", "apikeys", "exec", "companion", "builds", "quality", "observ",
  "screenlog", "extras", "accounts", "switch", "tools", "phone", "health",
  "todos", "arm", "appletv",
]);

export default function DashboardPage() {
  // ── ALL hooks unconditionally at the top ────────────────────────
  const { user, token, isLoading, isAuthenticated, sessionExpired, logout } = useAuth();
  const { beta: betaStatus } = useBetaStatus(token);
  const { devices, refreshDevices, hiddenIds } = useDevices(token);
  // Bootstrap-pending claims — boxes that joined the user's relay but
  // don't have a Convex devices row yet. Surfaced to the user so a
  // freshly-installed remote box becomes claimable from the dashboard
  // without ever touching the LAN.
  const { pending: pendingClaims, refreshPending, claimPending } = usePendingClaims(token);
  const { theme, toggle: toggleTheme } = useTheme();
  const router = useRouter();

  const [connState, setConnState] = useState<ConnectionState>("disconnected");
  const [connectedDevice, setConnectedDevice] = useState<Device | null>(null);
  const [agentInfo, setAgentInfo] = useState<AgentInfo | null>(null);
  const [tasks, setTasks] = useState<Task[]>([]);
  const [activeTask, setActiveTask] = useState<Task | null>(null);
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
  const [selectedRunner, setSelectedRunner] = useState<string>("");
  const [selectedModel, setSelectedModel] = useState<string>("");
  const [selectedOpenCodeMode, setSelectedOpenCodeMode] = useState<string>("");
  const [openCodeAgents, setOpenCodeAgents] = useState<OpenCodeAgentRow[]>([]);
  // OpenCode-specific provider + key flow. Only used when
  // selectedRunner === "opencode". The chosen model id is also written
  // to selectedModel so the existing createTask path picks it up.
  const [opencodeProvider, setOpencodeProvider] = useState<string>("anthropic");
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
  // Local queue of follow-up prompts the user typed while the active
  // task was still running. The Yaver agent rejects POST
  // /tasks/<id>/continue with 500 until the prior turn finishes (see
  // handleSend's `continuing` comment), and the runner CLIs we wrap
  // (claude `-p`, codex `exec`, opencode `run`) are one-shot per
  // invocation — no native back-channel to inject a follow-up
  // mid-stream. So we mirror what claude-code / codex / opencode do
  // interactively: keep typing, queue up, dispatch on completion.
  const [pendingFollowUps, setPendingFollowUps] = useState<string[]>([]);
  const [guestCode, setGuestCode] = useState("");
  const [pendingInvites, setPendingInvites] = useState<GuestInvitation[]>([]);
  const [invitesBusy, setInvitesBusy] = useState<string | null>(null);
  const [reauthBusy, setReauthBusy] = useState<string | null>(null);
  const [reauthMsg, setReauthMsg] = useState<{ deviceId: string; ok: boolean; text: string } | null>(null);
  // Browser-shell modal state. We track the device the user clicked
  // "Shell" on so the modal can decide whether agentClient is already
  // pointed at it; if not it offers a "Connect & open shell" affordance
  // instead of silently opening a WS against the wrong baseUrl.
  const [shellDevice, setShellDevice] = useState<Device | null>(null);
  const [remoteDesktopDevice, setRemoteDesktopDevice] = useState<Device | null>(null);
  const [activeTab, setActiveTab] = useState<"home" | "chat" | "projects" | "vibe" | "devices" | "git" | "todos" | "builds" | "webview" | "preview" | "web-reload" | "health" | "quality" | "convex" | "data" | "switch" | "accounts" | "company-ai" | "companion" | "observ" | "ops" | "extras" | "share" | "guests" | "collab" | "infra" | "connect" | "network" | "tools" | "security" | "storage" | "vault" | "apikeys" | "schedules" | "exec" | "phone" | "vibe-preview" | "domains" | "screenlog" | "settings" | "billing" | "stores" | "cloud" | "build" | "arm" | "appletv" | "packages">("devices");
  const [userMenuOpen, setUserMenuOpen] = useState(false);
  const [todoCount, setTodoCount] = useState(0);
  const [connectError, setConnectError] = useState<string | null>(null);
  const [connectDiagnostics, setConnectDiagnostics] = useState<ConnectAttemptDiagnostic[]>([]);
  const [copiedReauth, setCopiedReauth] = useState(false);
  const [reauthing, setReauthing] = useState(false);
  const [reauthMessage, setReauthMessage] = useState<{ kind: "ok" | "err"; text: string } | null>(null);
  const [relayReady, setRelayReady] = useState(false);
  const [previewTargetId, setPreviewTargetId] = useState<string | null>(null);
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
      if (relays.length > 0) agentClient.setRelayServers(relays);
    } catch {
      // non-fatal — next connect will re-read
    }
    return { repaired: !!body.repaired, reason: body.reason || "" };
  }, [token]);

  const inputRef = useRef<HTMLTextAreaElement>(null);
  const outputRef = useRef<HTMLDivElement>(null);
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
    const cached = (connectedDevice?.runners || []) as Array<{ runnerId?: string; authConfigured?: boolean; needsAuth?: boolean; status?: string }>;
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
  }, [runners, connectedDevice]);

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
              if (!cancelled) {
                setPrimaryDeviceId(sd.settings?.primaryDeviceId ?? null);
                setSecondaryDeviceId(sd.settings?.secondaryDeviceId ?? null);
              }
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
    // skips the relay branch and every cross-network device (remote VPSes, etc.)
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
          next[next.length - 1] = { role: "assistant", text: last.text ? last.text + chunk : chunk };
        } else {
          next.push({ role: "assistant", text: chunk });
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
    if (activeTask.status !== "running" && activeTask.status !== "queued") {
      sseActiveTaskRef.current = null;
      return;
    }
    const tid = activeTask.id;
    sseActiveTaskRef.current = tid;
    const stop = agentClient.streamTaskOutput(
      tid,
      (chunk) => {
        appendAssistantChunk(tid, chunk);
      },
      (evt) => {
        if (!evt || typeof evt.type !== "string") return;
        if (evt.type === "agent_question" && evt.question) {
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
    );
    // Late-join replay: if the agent already asked while no client
    // was subscribed, the SSE writer replays on connect — but also
    // poll once so the card shows the moment the user opens the task
    // tab without waiting for the next SSE flush.
    void agentClient.getPendingTaskQuestion(tid).then((q) => {
      if (q && q.taskId === tid) {
        setAgentQuestion(q);
        setAgentAnswerText("");
      }
    });
    return () => {
      stop();
      if (sseActiveTaskRef.current === tid) sseActiveTaskRef.current = null;
    };
  }, [activeTask?.id, activeTask?.status, appendAssistantChunk]);

  useEffect(() => { if (outputRef.current) outputRef.current.scrollTop = outputRef.current.scrollHeight; }, [outputLines, chatMsgs]);

  // Reconcile the activeTask's status from the polled tasks list. With-
  // out this, activeTask.status stays at "running" forever even after
  // the task completes — the SSE handler only appends output chunks,
  // it doesn't mutate status. Symptom: after the assistant's reply
  // streams in, the composer's "Update task" button stays disabled
  // (taskRunning is computed from activeTask.status === "running")
  // and the user can't type a follow-up. This effect re-finds the
  // active task in the polled list and syncs status / runnerId /
  // resultText / costUsd / turns whenever anything changed.
  useEffect(() => {
    if (!activeTask) return;
    const fresh = tasks.find((t) => t.id === activeTask.id);
    if (!fresh) return;
    if (
      fresh.status !== activeTask.status ||
      fresh.resultText !== activeTask.resultText ||
      fresh.costUsd !== activeTask.costUsd ||
      fresh.turns?.length !== activeTask.turns?.length
    ) {
      setActiveTask(fresh);
    }
  }, [tasks, activeTask]);

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
    const seededRunner = connectedDevice
      ? preferredDefaultRunnerForDevice(
          connectedDevice,
          user?.email,
          ready.map((runner) => runner.id).concat(installed.map((runner) => runner.id)),
        )
      : null;
    const preferred =
      ready.find(r => r.id === connectedDevicePrimaryRunner) ||
      installed.find(r => r.id === connectedDevicePrimaryRunner) ||
      ready.find(r => r.id === seededRunner) ||
      installed.find(r => r.id === seededRunner) ||
      ready.find(r => r.isDefault || r.active) ||
      ready.find(r => r.id === "claude") ||
      ready.find(r => r.id === "opencode") ||
      ready.find(r => r.id === "codex") ||
      installed.find(r => r.isDefault || r.active) ||
      installed[0];
    setSelectedRunner(preferred.id);
  }, [connectedDevice, connectedDevicePrimaryRunner, runners, selectedRunner, user?.email]);

  useEffect(() => {
    if (selectedRunner !== "opencode") return;
    const provider = OPENCODE_PROVIDER_CATALOGUE.find((p) => p.id === opencodeProvider) || OPENCODE_PROVIDER_CATALOGUE[0];
    if (!provider) return;
    const valid = provider.models.some((m) => `${provider.id}/${m.id}` === selectedModel);
    if (valid) return;
    const first = provider.models[0];
    if (first) setSelectedModel(`${provider.id}/${first.id}`);
  }, [selectedRunner, opencodeProvider, selectedModel]);

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
    const models = Array.isArray(runner?.models) ? runner.models : [];
    if (models.length === 0) {
      if (selectedModel) setSelectedModel("");
      return;
    }
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
      (seededModel && models.some((m) => m.id === seededModel) ? seededModel : "") ||
      models.find((m) => m.isDefault)?.id ||
      models[0]?.id ||
      "";
    setSelectedModel(preferredModel);
  }, [connectedDevice, primaryModelByDevice, runners, selectedRunner, selectedModel, user?.email]);

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

  const connectToDevice = async (device: Device) => {
    if (!token) return;
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
      !device.isGuest &&
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
      await agentClient.connect(device.host, device.port, token, device.id, { tunnelUrls });
      setConnectDiagnostics(agentClient.lastConnectDiagnostics);
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
        if (liveVersion && liveVersion !== device.agentVersion && !device.isGuest && device.id && token) {
          fetch(`${CONVEX_URL}/devices/report-version`, {
            method: "POST",
            headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
            body: JSON.stringify({ deviceId: device.id, agentVersion: liveVersion }),
          }).catch(() => { /* best-effort */ });
        }
      } catch {}
      try { setRunners(await agentClient.getRunners()); } catch {}
    } catch (err: any) {
      const firstDiagnostics = agentClient.lastConnectDiagnostics;
      const canTryAutoReauth = Boolean(token && device.id && agentClient.configuredRelayServers.length > 0);
      const rawError = err?.message || "Could not connect to device";
      const authOwnedByAnotherUser = isDifferentUserAuthError(rawError, firstDiagnostics);

      if (authOwnedByAnotherUser) {
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
            await agentClient.connect(device.host, device.port, token, device.id, { tunnelUrls });
            setConnectError(null);
            setConnectDiagnostics(agentClient.lastConnectDiagnostics);
            try { setAgentInfo(await agentClient.getInfo()); } catch {}
            try { setRunners(await agentClient.getRunners()); } catch {}
            return;
          }
        } catch {}
      }

      setConnectError(rawError);
      setConnectDiagnostics(agentClient.lastConnectDiagnostics);
    }
  };

  const disconnect = () => { agentClient.disconnect(); setConnectedDevice(null); setAgentInfo(null); setTasks([]); setActiveTask(null); setOutputLines([]); setChatMsgs([]); setRunners([]); setSelectedRunner(""); setSelectedModel(""); setConnectError(null); setPendingFollowUps([]); };

  const refreshConnectedRunners = async () => {
    if (!isConnected) return;
    try {
      setRunners(await agentClient.getRunners());
    } catch {}
  };

  const handleSend = async (e?: React.FormEvent) => {
    e?.preventDefault();
    const text = input.trim(); if (!text || sending) return;
    // Task already running → enqueue the follow-up locally and clear
    // the input so the user can keep typing. Dispatch happens in the
    // effect below when activeTask.status leaves running/queued.
    if (activeTask && (activeTask.status === "running" || activeTask.status === "queued")) {
      setPendingFollowUps((prev) => [...prev, text]);
      setChatMsgs((prev) => [...prev, { role: "user", text, queued: true }]);
      setInput("");
      return;
    }
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
          text: `⚠ ${runnerLabel(targetRunner?.id || "")} needs sign-in on this device before it can answer. Opening the sign-in dialog…`,
        },
      ]);
      setConnectError(authIssue);
      if (targetRunner && (targetRunner.id === "claude" || targetRunner.id === "codex")) {
        setChatRunnerAuthModal(targetRunner.id);
      }
      return;
    }
    setInput(""); setSending(true);
    const continuing = !!activeTask;
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
        const t = await agentClient.createTask({
          title: text.slice(0, 80),
          description: text,
          runner: selectedRunner || undefined,
          model: selectedModel || undefined,
          mode: selectedRunner === "opencode" && selectedOpenCodeMode ? selectedOpenCodeMode : undefined,
          workDir: preferredSurfaceProjectPath || undefined,
        });
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

  // Dispatch queued follow-ups when the active task transitions out
  // of running/queued. Drains one per transition: continueTask kicks
  // the task back into "running", which re-arms this effect for the
  // next item once that turn lands.
  useEffect(() => {
    if (!activeTask) return;
    if (activeTask.status === "running" || activeTask.status === "queued") return;
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
        await agentClient.continueTask(activeTask.id, next);
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
  const connectedDeviceNeedsRecovery = connectedDevice
    ? (() => {
        const lifecycle = deriveDeviceLifecycleState(connectedDevice);
        return lifecycle === "bootstrap" || lifecycle === "yaver-auth-expired";
      })()
    : false;
  const runningTask = tasks.find(t => t.status === "running");
  const activeRunnerId = activeTask?.runnerId || selectedRunner;
  const activeRunnerRow = runners.find((r) => r.id === activeRunnerId) || null;
  const activeRunnerAuthIssue = runnerAuthIssue(activeRunnerRow);
  const canStartBrowserRunnerAuth = Boolean(activeRunnerRow && (activeRunnerRow.id === "claude" || activeRunnerRow.id === "codex"));
  const mobileWorkers = displayDevices.filter((d) => d.deviceClass === "edge-mobile");
  const dormantDevices = displayDevices.filter((d) => isDormantUnreachableDevice(d));
  const visibleDevices = displayDevices.filter((d) => !isDormantUnreachableDevice(d));
  const selectedPreviewTarget = mobileWorkers.find((d) => d.id === previewTargetId) || null;
  // Owner-only experimental hardware cells. Hidden from non-owners so the
  // default dashboard stays the AI coding/preview/deploy product. Owner status
  // is the server-computed user.isOwner flag (no owner identity in the bundle);
  // mirrors the daemon-side gate (mcp_owner_gate.go).
  const isOwnerAccount = user?.isOwner === true;
  const OWNER_ONLY_TABS = new Set(["arm", "appletv", "robot", "circuit", "printer"]);
  const tabs: { id: typeof activeTab; label: string; icon: string; badge?: number }[] = ([
    { id: "devices", label: "Devices", icon: "\uD83D\uDCBB" },
    { id: "build", label: "Build", icon: "\uD83D\uDEE0\uFE0F" },
    { id: "cloud", label: "Cloud", icon: "\u2601\uFE0F" },
    { id: "chat", label: "Chat", icon: "\uD83D\uDCAC" },
    { id: "projects", label: "Projects", icon: "\uD83D\uDCC1" },
    { id: "vibe", label: "Vibe", icon: "\u2328\uFE0F" },
    { id: "todos", label: "Todos", icon: "\u2611\uFE0F", badge: todoCount },
    { id: "webview", label: "Webview", icon: "\uD83D\uDCF1" },
    { id: "health", label: "Health", icon: "\uD83D\uDCCA" },
    { id: "quality", label: "Quality", icon: "\u2705" },
    { id: "data", label: "Data", icon: "\uD83D\uDDC4\uFE0F" },
    { id: "switch", label: "Switch", icon: "\uD83D\uDD04" },
    { id: "accounts", label: "Accounts", icon: "\uD83D\uDD11" },
    { id: "company-ai", label: "Company AI", icon: "AI" },
    { id: "infra", label: "Infra", icon: "\uD83D\uDEE0\uFE0F" },
    { id: "connect", label: "Connect", icon: "\uD83C\uDF10" },
    { id: "network", label: "Mesh", icon: "\uD83D\uDD78\uFE0F" },
    { id: "tools", label: "Tools", icon: "\uD83E\uDDE9" },
    { id: "observ", label: "Observ", icon: "\uD83D\uDCCA" },
    { id: "ops", label: "Ops", icon: "\uD83D\uDE80" },
    { id: "extras", label: "Extras", icon: "\u2699\uFE0F" },
    { id: "share", label: "Share", icon: "\uD83D\uDCE3" },
    { id: "guests", label: "Guests", icon: "\uD83D\uDC65" },
    { id: "collab", label: "People", icon: "\uD83E\uDD1D" },
    { id: "convex", label: "Convex", icon: "\u26A1" },
    { id: "storage", label: "Storage", icon: "\uD83D\uDCC2" },
    { id: "vault", label: "Vault", icon: "\uD83D\uDD12" },
    { id: "apikeys", label: "Yaver Tokens", icon: "\uD83D\uDD11" },
    { id: "schedules", label: "Schedules", icon: "\u23F0" },
    { id: "packages", label: "Packages", icon: "\uD83D\uDCE6" },
    { id: "phone", label: "Phone Backend", icon: "\u26A1" },
    { id: "companion", label: "Companion", icon: "\u23F0" },
    { id: "vibe-preview", label: "Vibe Preview", icon: "\uD83C\uDFAC" },
    { id: "domains", label: "Domains", icon: "\uD83C\uDF10" },
    { id: "exec", label: "Exec", icon: "\u2699\uFE0F" },
    { id: "security", label: "Security", icon: "\uD83D\uDD10" },
    { id: "screenlog", label: "Screen Monitor", icon: "\uD83C\uDFA5" },
    { id: "arm", label: "Robot Arm", icon: "\uD83E\uDDBE" },
    { id: "appletv", label: "Apple TV", icon: "\uD83D\uDCFA" },
  ] as { id: typeof activeTab; label: string; icon: string; badge?: number }[]).filter(
    (t) => isOwnerAccount || !OWNER_ONLY_TABS.has(t.id),
  );

  // Beta users get the focused Beta workspace INSTEAD of the full
  // dashboard \u2014 same coding engine (VibeCodingView), none of the infra/
  // device/guest/git chrome. The invisible owner-infra share is enforced
  // server-side; this is just which surface renders. Placed after all
  // hooks + auth guards, so hook order is unaffected.
  // Pending pre-seeded invite (whitelisted email, not yet approved): show the
  // consent card. Approving creates the real grant; reload flips to the beta
  // workspace. (Placed before the isBeta gate; after all hooks.)
  if (betaStatus?.betaInvite?.pending && !betaStatus.isBeta) {
    const inv = betaStatus.betaInvite;
    return (
      <div style={{ minHeight: "100vh", display: "flex", alignItems: "center", justifyContent: "center", padding: 24, background: "#0b0b0f" }}>
        <div style={{ maxWidth: 460, width: "100%", padding: 28, borderRadius: 16, background: "#15151c", border: "1px solid #6d5efc" }}>
          <h2 style={{ color: "#fff", fontSize: 20, fontWeight: 700, margin: 0 }}>
            ✨ {inv.inviterName} invited you to Yaver Beta
          </h2>
          <p style={{ color: "#a6a6b3", marginTop: 10, lineHeight: 1.5 }}>
            Approve to enable managed AI — no API key needed — and {inv.includedHours} hours on a shared Yaver box. Build a sandbox app and deploy it to Yaver Serverless, right from here.
          </p>
          <button
            onClick={async () => {
              if (!token) return;
              const ok = await acceptBetaInvite(token);
              if (ok) window.location.reload();
            }}
            style={{ marginTop: 18, width: "100%", padding: "12px 16px", borderRadius: 10, border: "none", background: "#6d5efc", color: "#fff", fontWeight: 700, cursor: "pointer" }}
          >
            Approve beta access
          </button>
        </div>
      </div>
    );
  }

  if (betaStatus?.isBeta) {
    return (
      <BetaWorkspaceView
        token={token}
        beta={betaStatus}
        devices={devices}
        connectedDevice={connectedDevice}
        connState={connState}
        onSelectDevice={connectToDevice}
        mobileWorkers={mobileWorkers}
        selectedPreviewTarget={selectedPreviewTarget}
        onSelectPreviewTarget={handleSelectPreviewTarget}
      />
    );
  }

  return (
    <div className="dashboard-shell relative flex min-h-[100vh] flex-col md:h-[100vh] md:min-h-0 md:flex-row">
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
            title={`Yaver.io home — web v${WEB_VERSION}`}
          >
            <span className="text-xl font-bold tracking-tight text-surface-50">
              yaver<span className="font-normal text-surface-500">.io</span>
            </span>
            <span className="mt-0.5 font-mono text-[10px] tracking-wide text-surface-400">
              v{WEB_VERSION}
            </span>
          </a>

          {/* Nav */}
          <nav className="flex flex-col gap-[2px]">
            {([
              { id: "devices",  label: "Devices",  icon: "💻" },
              { id: "build",    label: "Build",    icon: "🛠️" },
              { id: "cloud",    label: "Cloud",    icon: "☁️" },
              { id: "network",  label: "Mesh",     icon: "🕸️" },
              { id: "chat",     label: "Chat",     icon: "💬" },
              { id: "projects", label: "Projects", icon: "📁" },
              { id: "git",      label: "Git",      icon: "⎇" },
              { id: "webview",  label: "Webview", icon: "📱" },
              { id: "vibe-preview", label: "Vibe Preview", icon: "🎬" },
              { id: "guests",   label: "Guests",   icon: "👥" },
              { id: "vault",    label: "Vault",    icon: "🔐" },
              { id: "billing",  label: "Billing",  icon: "💳" },
              { id: "stores",   label: "Publish",  icon: "🚀" },
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
                const connectedNeedsAuth = !!liveDevice.needsAuth && !liveDevice.isGuest;
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
                          {d.isGuest ? (
                            <span className="shrink-0 rounded bg-sky-500/15 px-1 text-[9px] uppercase text-sky-700 dark:text-sky-300">shared</span>
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

          <div className="min-h-6 flex-1" />

          <div className="shrink-0 space-y-3 border-t border-surface-800/60 pt-4">
            <button
              onClick={() => setActiveTab("guests")}
              className="w-full rounded-md border border-brand/30 bg-brand-soft/60 px-3 py-1.5 text-xs font-medium text-brand-softFg hover:bg-brand-soft hover:border-brand/50 transition-colors"
              title="Invite someone to share this machine"
            >
              Invite a guest
            </button>

            <div>
              <p className="mb-1 text-[10px] font-semibold uppercase tracking-widest text-slate-500 dark:text-surface-500">Join as a guest</p>
              <div className="flex gap-1.5">
                <input value={guestCode} onChange={e => setGuestCode(e.target.value.toUpperCase())} maxLength={6}
                  placeholder="CODE" className="min-w-0 flex-[1_1_0%] rounded-md border border-slate-300 bg-white px-2 py-1.5 text-xs text-center font-mono tracking-widest text-slate-700 placeholder-slate-400 outline-none focus:border-indigo-500 dark:border-surface-800 dark:bg-surface-900 dark:text-surface-200 dark:placeholder-surface-600" />
                <button onClick={async () => {
                  if (guestCode.trim().length < 4) return;
                  try {
                    const res = await fetch(`${CONVEX_URL}/guests/accept-code`, { method: "POST", headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` }, body: JSON.stringify({ code: guestCode.trim() }) });
                    const data = await res.json();
                    if (data.ok || data.hostName) { alert(`Joined ${data.hostName || "host"}'s machine!`); setGuestCode(""); refreshDevices(); }
                    else alert(data.error || "Invalid code");
                  } catch (e: any) { alert(e.message); }
                }} disabled={guestCode.trim().length < 4}
                  className="shrink-0 rounded-md bg-indigo-600 px-3 py-1.5 text-[11px] font-medium text-white hover:bg-indigo-700 disabled:opacity-30 dark:bg-indigo-500 dark:hover:bg-indigo-400">Join</button>
              </div>
            </div>
          </div>
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
                            {mixedContentLikely ? (
                              <div className="text-amber-700 dark:text-amber-300">
                                Direct probe returned <code className="rounded bg-surface-900 px-1 font-mono">Load failed</code> because a browser on <code className="rounded bg-surface-900 px-1 font-mono">https://</code> can&apos;t fetch <code className="rounded bg-surface-900 px-1 font-mono">http://</code> LAN IPs (mixed content). The web path has to go through a relay.
                              </div>
                            ) : null}
                          </div>

                          {/* Re-auth — always offered on connection error. */}
                          <div className="mt-3 rounded border border-amber-500/20 bg-amber-500/5 p-2 text-left">
                            <p className="text-[11px] text-amber-700 dark:text-amber-300">
                              {authExpired
                                ? "Agent accepted the probe but its Convex session is stale. Hand your current session down to the box:"
                                : "Try handing your current session down to the box — works even if the agent's own token is dead, as long as one relay can reach it:"}
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
                                className="flex-1 rounded border border-amber-500/40 bg-amber-500/10 px-3 py-1.5 text-[11px] font-medium text-amber-700 dark:text-amber-200 hover:bg-amber-500/20 disabled:opacity-40"
                              >
                                {reauthing ? "Re-authing…" : relayCount === 0 ? "Re-auth (needs a relay)" : "Re-auth this device from web"}
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
                          isSecondary={secondaryDeviceId === d.id}
                          isSelected={false}
                          isConnecting={false}
                          token={token}
                          onAliasSaved={refreshDevices}
                          onOpenShell={() => setShellDevice(d)}
                          onOpenRemoteDesktop={() => setRemoteDesktopDevice(d)}
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
                          onToggleSecondary={!d.isGuest && token ? async () => {
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
                          canToggleSecondary={!d.isGuest && !!token && primaryDeviceId !== d.id}
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
            <div className="flex-1 overflow-y-auto p-6 max-w-4xl mx-auto w-full"><ProjectsView onTaskCreated={onTaskCreated} mobileWorkers={mobileWorkers} selectedPreviewTarget={selectedPreviewTarget} onSelectPreviewTarget={handleSelectPreviewTarget} onReconnect={connectedDevice ? async () => { await connectToDevice(connectedDevice); } : undefined} onRepairRelay={token ? repairRelay : undefined} /></div>
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
          ) : activeTab === "ops" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-6xl mx-auto w-full"><OpsView /></div>
          ) : activeTab === "extras" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-6xl mx-auto w-full"><ExtrasView /></div>
          ) : activeTab === "share" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-3xl mx-auto w-full"><ShareView /></div>
          ) : activeTab === "billing" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-3xl mx-auto w-full"><BillingView token={token} /></div>
          ) : activeTab === "stores" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-3xl mx-auto w-full"><StoresView token={token} /></div>
          ) : activeTab === "build" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-3xl mx-auto w-full">
              <h2 className="text-lg font-semibold text-surface-100">Build your app</h2>
              <p className="mt-1 text-xs text-surface-500">
                Your terminal (Claude Code / Codex) writes the code; Yaver does the
                infra. Turn on only the capabilities you need — see it on your phone,
                add a backend or website, or publish to the stores. Pay fairly from
                one prepaid balance, or run it yourself for free.
              </p>
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
          ) : activeTab === "cloud" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-3xl mx-auto w-full">
              <h2 className="text-lg font-semibold text-surface-100">Yaver Cloud</h2>
              <p className="mt-1 text-xs text-surface-500">
                Optional web-billed infrastructure: saved cloud workspaces,
                private relay, and auto-stop. Self-hosted Yaver remains free.
              </p>
              <ManagedCloudPanel token={token} standalone />
            </div>
          ) : activeTab === "guests" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-3xl mx-auto w-full"><GuestsStatusView /></div>
          ) : activeTab === "collab" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-3xl mx-auto w-full"><CollabView /></div>
          ) : activeTab === "convex" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-5xl mx-auto w-full"><ConvexView /></div>
          ) : activeTab === "security" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-3xl mx-auto w-full"><TwoFactorView token={token} /></div>
          ) : activeTab === "settings" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-3xl mx-auto w-full">
              <SettingsView user={user as any} onLogout={logout} />
            </div>
          ) : activeTab === "storage" ? (
            <div className="flex-1 min-h-0 w-full"><StorageView /></div>
          ) : activeTab === "arm" && isOwnerAccount ? (
            <div className="flex-1 min-h-0 w-full overflow-auto p-4"><ArmCellView devices={devices} token={token} /></div>
          ) : activeTab === "appletv" && isOwnerAccount ? (
            <div className="flex-1 min-h-0 w-full overflow-auto p-4"><AppleTVCellView devices={devices} token={token} /></div>
          ) : activeTab === "vault" ? (
            <div className="flex-1 min-h-0 w-full max-w-4xl mx-auto">
              <VaultView
                needsAuth={connectedDeviceNeedsRecovery}
                onReconnect={connectedDevice ? async () => { await connectToDevice(connectedDevice); } : undefined}
              />
            </div>
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
          ) : activeTab === "vibe-preview" ? (
            <div className="flex-1 min-h-0 w-full"><VibePreviewView /></div>
          ) : activeTab === "domains" ? (
            <div className="flex-1 min-h-0 w-full max-w-5xl mx-auto">
              {token && user?.id ? <DomainsView token={token} userId={user.id} /> :
                <div className="p-6 text-xs text-surface-500">Sign in to manage custom domains.</div>}
            </div>
          ) : activeTab === "exec" ? (
            <div className="flex-1 min-h-0 w-full"><ExecView /></div>
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
                onRefresh={refreshDevices}
                signedInEmail={user?.email}
                signedInProvider={undefined}
                token={token}
                onOpen={connectToDevice}
                onCloseWorkspace={disconnect}
                activeWorkspaceDeviceId={connectedDevice?.id ?? null}
                hiddenCount={hiddenIds.size}
                onNavigateCloud={() => setActiveTab("cloud")}
              />
            </div>
          ) : activeTab === "git" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-[1600px] mx-auto w-full"><GitView
              devices={devices}
              onOpenSurface={(surface, projectPath) => {
                setPreferredSurfaceProjectPath(projectPath);
                if (surface === "preview") {
                  setPreferredWebviewMode("mobile");
                  setActiveTab("webview");
                  return;
                }
                if (surface === "web-reload") {
                  setPreferredWebviewMode("web");
                  setActiveTab("webview");
                  return;
                }
                setActiveTab(surface);
              }}
              onVibePrompt={(projectPath, prompt) => {
                // One-click rebase via Vibing: drop the pre-canned
                // prompt into the chat composer, switch to Chat tab,
                // and pin the project so the runner has the workdir.
                // The user sees the prompt and can edit / Enter to
                // send — nothing fires automatically.
                setPreferredSurfaceProjectPath(projectPath);
                setInput(prompt);
                setActiveTab("chat");
                // Defer focus until after the tab switch + render.
                setTimeout(() => { inputRef.current?.focus(); }, 50);
              }}
            /></div>
          ) : (
            <>
              <div className="flex flex-1 min-h-0">
                <div className="flex flex-1 min-w-0 flex-col">
                  {activeTask ? (
                    <>
                      <div className="flex items-center gap-3 border-b border-surface-800 px-4 py-2">
                        <span className={`h-1.5 w-1.5 rounded-full ${activeTask.status === "running" || activeTask.status === "queued" ? "animate-pulse bg-amber-400" : activeTask.status === "review" ? "bg-violet-400" : activeTask.status === "completed" ? "bg-emerald-400" : "bg-surface-600"}`} />
                        <span className="truncate text-sm font-medium text-surface-200">{displayTaskTitle(activeTask.title)}</span>
                        <span className={`text-[10px] ${statusColor(activeTask.status)}`}>{activeTask.status}</span>
                        {activeTask.status === "review" ? (
                          <button
                            type="button"
                            onClick={async () => {
                              await agentClient.completeTask(activeTask.id);
                              const fresh = { ...activeTask, status: "completed" as const };
                              setActiveTask(fresh);
                              setTasks((prev) => prev.map((t) => t.id === fresh.id ? fresh : t));
                            }}
                            className="rounded-lg border border-emerald-400/30 bg-emerald-400/10 px-2 py-1 text-[10px] font-semibold text-emerald-700 dark:text-emerald-300 hover:bg-emerald-400/15"
                          >
                            Complete
                          </button>
                        ) : null}
                        {activeTask.costUsd != null && <span className="text-[10px] text-surface-600">${activeTask.costUsd.toFixed(3)}</span>}
                      </div>
                      <div ref={outputRef} className="flex-1 overflow-y-auto bg-surface-950 px-4 py-5">
                        {activeRunnerAuthIssue ? (
                          <div className="mx-auto mb-4 max-w-3xl rounded-2xl border border-amber-500/30 bg-amber-500/10 p-4 text-sm text-amber-800 dark:text-amber-100">
                            <div className="font-medium">{runnerLabel(activeRunnerId)} needs sign-in on {connectedDevice?.name || "this machine"}</div>
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
                        {chatMsgs.length === 0 ? (
                          <div className="flex h-full items-center justify-center gap-2 text-[12px] text-surface-600">
                            {(activeTask.status === "running" || activeTask.status === "queued") && <span className="h-3 w-3 animate-spin rounded-full border border-surface-500 border-t-transparent" />}
                            {activeTask.status === "running" || activeTask.status === "queued" ? "Working..." : "No messages yet"}
                          </div>
                        ) : (
                          <div className="mx-auto flex max-w-3xl flex-col gap-3">
                            {chatMsgs.map((m, i) => (
                              m.role === "user" ? (
                                <div key={i} className="flex justify-end">
                                  <div className={`max-w-[80%] rounded-2xl rounded-br-sm px-3.5 py-2 text-[13px] text-white whitespace-pre-wrap break-words shadow-sm ${m.queued ? "bg-indigo-500/40 italic ring-1 ring-indigo-300/30" : "bg-indigo-500"}`}>
                                    {m.queued ? <span className="mr-1.5 text-[10px] uppercase tracking-wide text-indigo-800 dark:text-indigo-100/80">queued</span> : null}
                                    {m.text}
                                  </div>
                                </div>
                              ) : (
                                <div key={i} className="flex justify-start">
                                  <div className="max-w-[90%] rounded-2xl rounded-bl-sm bg-surface-800 px-3.5 py-2 text-[12px] leading-5 text-surface-100 break-words shadow-sm">
                                    {m.text ? (
                                      // Render assistant prose as markdown so
                                      // `**$ <cmd>**` shell pills + ```fenced```
                                      // tool output land as readable cards
                                      // (mirrors mobile/app/(tabs)/tasks.tsx
                                      // and web/components/dashboard/
                                      // VibeCodingView.tsx's ChatBubble).
                                      <div className="prose-invert break-words [&_pre]:whitespace-pre-wrap">
                                        <ReactMarkdown components={ASSISTANT_MARKDOWN_COMPONENTS}>
                                          {stripAnsi(m.text)}
                                        </ReactMarkdown>
                                        {activeTask.status === "running" && i === chatMsgs.length - 1 ? (
                                          <span className="ml-0.5 inline-block h-3 w-1.5 translate-y-[2px] animate-pulse bg-surface-300" aria-hidden />
                                        ) : null}
                                      </div>
                                    ) : activeTask.status === "running" || activeTask.status === "queued" ? (
                                      <span className="inline-flex items-center gap-2 text-surface-400">
                                        <span className="inline-flex items-center gap-1">
                                          <span className="h-2 w-2 animate-pulse rounded-full bg-surface-400" />
                                          <span className="h-2 w-2 animate-pulse rounded-full bg-surface-400 [animation-delay:200ms]" />
                                          <span className="h-2 w-2 animate-pulse rounded-full bg-surface-400 [animation-delay:400ms]" />
                                        </span>
                                        <span className="text-[11px] tracking-wide">
                                          {runnerLabel(activeTask.runnerId || selectedRunner)} is thinking…
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
                    const installed = chatRunnerChoices;
                    const selectedRunnerRow = installed.find((r) => r.id === selectedRunner) || null;
                    const selectedRunnerModels = Array.isArray(selectedRunnerRow?.models) ? selectedRunnerRow.models : [];
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
                        ? OPENCODE_PROVIDER_CATALOGUE.find((p) => p.id === opencodeProvider) || OPENCODE_PROVIDER_CATALOGUE[0]
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
                      return (
                        <div className="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-surface-800 bg-surface-950/60 px-3 py-2 text-[11px] text-surface-400">
                          <div className="flex flex-wrap items-center gap-x-1.5 gap-y-1">
                            <span className="text-surface-500">Agent</span>
                            <span className="rounded-full border border-amber-400/30 bg-amber-400/5 px-2 py-0.5 text-amber-800 dark:text-amber-100">{runnerName}</span>
                            {providerEntry ? (
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
                          <button
                            type="button"
                            onClick={() => setChatPickerExpanded(true)}
                            className="rounded-lg border border-surface-700 bg-surface-900 px-2.5 py-1 text-[11px] text-surface-300 hover:border-surface-500"
                            title="Edit agent, provider, model, and mode"
                          >
                            Edit ▾
                          </button>
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
                                        setSelectedRunner(runner.id);
                                        setOpencodeSaveMsg(null);
                                        setOpencodeChangingKey(false);
                                      }}
                                      title={runner.error || runner.warning || runner.name}
                                      className={`rounded-full border px-2.5 py-1 transition ${
                                        active
                                          ? "border-amber-400/60 bg-amber-400/10 text-amber-800 dark:text-amber-100"
                                          : "border-surface-700 bg-surface-900 text-surface-300 hover:border-surface-500"
                                      }`}
                                    >
                                      {runner.name}
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
                                      onClick={() => setSelectedModel(model.id)}
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
                            <span>
                              Active: <span className="text-surface-300">{runnerLabel(activeRunnerId)}</span>
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
                      runner. Picking an Ollama model needs no key;
                      everything else prompts for a BYOK API key that
                      gets persisted to opencode.json on the agent. */}
                  {chatPickerExpanded && selectedRunner === "opencode" ? (() => {
                    const provider = OPENCODE_PROVIDER_CATALOGUE.find((p) => p.id === opencodeProvider) || OPENCODE_PROVIDER_CATALOGUE[0];
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
                        const builtinAuthProvider = provider.id === "zai-coding-plan";
                        const patch: Parameters<typeof agentClient.saveOpenCodeConfig>[0] = {
                          model: fullModel,
                          providers: [
                            {
                              id: provider.id,
                              name: provider.label,
                              ...(provider.baseUrl ? { baseUrl: provider.baseUrl } : {}),
                              ...(opencodeApiKey.trim() ? { apiKey: opencodeApiKey.trim() } : {}),
                              ...(builtinAuthProvider ? {} : { models: { [modelId]: {} } }),
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
                        <div className="flex flex-wrap items-center gap-2 text-[11px]">
                          <span className="text-surface-500">Provider</span>
                          {OPENCODE_PROVIDER_CATALOGUE.map((p) => {
                            const active = p.id === opencodeProvider;
                            return (
                              <button
                                key={p.id}
                                type="button"
                                onClick={() => {
                                  setOpencodeProvider(p.id);
                                  setOpencodeApiKey("");
                                  setOpencodeChangingKey(false);
                                  setOpencodeSaveMsg(null);
                                  // Seed the model with the provider's first option so the user always has a valid pre-selection.
                                  const m = p.models[0];
                                  if (m) setSelectedModel(`${p.id}/${m.id}`);
                                }}
                                title={p.blurb}
                                className={`rounded-full border px-2.5 py-1 transition ${
                                  active
                                    ? "border-cyan-400/60 bg-cyan-400/10 text-cyan-800 dark:text-cyan-100"
                                    : "border-surface-700 bg-surface-900 text-surface-300 hover:border-surface-500"
                                }`}
                              >
                                {p.label}
                              </button>
                            );
                          })}
                        </div>
                        <p className="mt-2 text-[11px] text-surface-500">{provider.blurb}</p>
                        <div className="mt-3 flex flex-wrap items-center gap-2 text-[11px]">
                          <span className="text-surface-500">Model</span>
                          {provider.models.map((m) => {
                            const fullId = `${provider.id}/${m.id}`;
                            const active = fullId === selectedModel || (!selectedModel && m.id === provider.models[0].id);
                            return (
                              <button
                                key={m.id}
                                type="button"
                                onClick={() => setSelectedModel(fullId)}
                                title={m.hint || m.id}
                                className={`rounded-full border px-2.5 py-1 transition ${
                                  active
                                    ? "border-fuchsia-400/60 bg-fuchsia-400/10 text-fuchsia-800 dark:text-fuchsia-100"
                                    : "border-surface-700 bg-surface-900 text-surface-300 hover:border-surface-500"
                                }`}
                              >
                                {m.label}
                              </button>
                            );
                          })}
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
                                  title="Replace the saved API key on this device. Read-only key state lives on the agent (opencode.json), never in Convex."
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
                      <span>{runnerLabel(activeRunnerId)} on {connectedDevice?.name || "this machine"} is not authenticated.</span>
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
                                const res = await agentClient.answerTaskQuestion(agentQuestion.taskId, agentQuestion.id, choice);
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
                              const res = await agentClient.answerTaskQuestion(agentQuestion.taskId, agentQuestion.id, agentAnswerText);
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
                              const res = await agentClient.answerTaskQuestion(agentQuestion.taskId, agentQuestion.id, agentAnswerText);
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
                      const placeholder = activeRunnerAuthIssue
                        ? `Sign in to ${runnerLabel(activeRunnerId)} to continue on ${connectedDevice?.name || "this machine"}...`
                        : taskRunning
                          ? queuedCount > 0
                            ? `Queued ${queuedCount} — type to queue another…`
                            : "Type to queue a follow-up — sent when this turn finishes…"
                          : activeTask
                            ? "Add a task update or refinement..."
                            : preferredSurfaceProjectPath
                              ? "Describe what to do in this repo..."
                              : "Describe the task you want this machine to run...";
                      const buttonLabel = sending
                        ? "..."
                        : taskRunning
                          ? queuedCount > 0
                            ? `Queue (+${queuedCount})`
                            : "Queue"
                          : activeTask
                            ? "Update task"
                            : "Start task";
                      const disabled = !input.trim()
                        || sending
                        || chatRunnerChoices.length === 0
                        || Boolean(activeRunnerAuthIssue);
                      return (
                        <>
                          <textarea ref={inputRef} value={input} onChange={e => setInput(e.target.value)}
                            onKeyDown={e => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); handleSend(); } }}
                            placeholder={placeholder} rows={1}
                            disabled={Boolean(activeRunnerAuthIssue)}
                            className="max-h-32 w-full resize-none rounded-xl border border-surface-700 bg-surface-950 px-4 py-3 text-sm text-surface-100 placeholder-surface-600 outline-none focus:border-surface-500 disabled:cursor-not-allowed disabled:opacity-60" style={{ minHeight: "48px" }} />
                          <button type="submit" disabled={disabled}
                            className="h-12 shrink-0 rounded-xl bg-surface-100 px-5 text-sm font-medium text-surface-900 hover:bg-surface-50 disabled:opacity-30">
                            {buttonLabel}
                          </button>
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
          onClose={() => setShellDevice(null)}
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
  const [copied, setCopied] = useState(false);
  const [pasteCode, setPasteCode] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const startedRef = useRef(false);

  useEffect(() => {
    if (startedRef.current) return;
    startedRef.current = true;
    (async () => {
      try {
        const started = await agentClient.startRunnerBrowserAuth(runner, target);
        setSession(started);
      } catch (error) {
        setStartError(error instanceof Error ? error.message : String(error));
      }
    })();
  }, [runner, target]);

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
                        const next = await agentClient.submitRunnerBrowserAuthCode(session.id, pasteCode.trim());
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
