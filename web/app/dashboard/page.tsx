"use client";

import { useAuth } from "@/lib/use-auth";
import { useDevices, type Device } from "@/lib/use-devices";
import { agentClient, type Task, type ConnectionState, type Runner, type AgentInfo, type ConnectAttemptDiagnostic } from "@/lib/agent-client";
import { CONVEX_URL } from "@/lib/constants";
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
import GitView from "@/components/dashboard/GitView";
import DevicesView from "@/components/dashboard/DevicesView";

function statusColor(s: string) {
  if (s === "running") return "text-amber-400";
  if (s === "completed") return "text-emerald-400";
  return "text-surface-400";
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

function formatRunnerChipLabel(runner: string): string {
  const cleaned = String(runner || "").trim();
  if (!cleaned) return cleaned;
  if (cleaned === "claude-code") return "claude";
  return cleaned;
}

function deviceAccessSummary(device: Pick<Device, "isGuest" | "sharedWithGuests" | "sharesAllProjects" | "sharedProjects" | "sharesAllRunners" | "sharedRunners">) {
  const hasSharedState = device.isGuest || device.sharedWithGuests;
  if (!hasSharedState) return null;
  const sharedProjects = Array.isArray(device.sharedProjects) ? device.sharedProjects.filter(Boolean) : [];
  const sharedRunners = Array.isArray(device.sharedRunners) ? device.sharedRunners.filter(Boolean) : [];
  return {
    projectLabel: device.sharesAllProjects ? "All Resources" : "Project Only",
    projectChips: device.sharesAllProjects ? [] : sharedProjects,
    runnerLabel: device.sharesAllRunners ? "All Agents" : "Some Agents",
    runnerChips: device.sharesAllRunners ? [] : sharedRunners.map(formatRunnerChipLabel),
  };
}

function accessScopeTone(device: Pick<Device, "accessScope" | "isGuest">) {
  if (device.accessScope === "shared-scoped") return "border-amber-500/40 bg-amber-500/10 text-amber-200";
  if (device.accessScope === "shared-legacy") return "border-violet-500/40 bg-violet-500/10 text-violet-200";
  if (device.isGuest) return "border-sky-500/40 bg-sky-500/10 text-sky-200";
  return "border-emerald-500/40 bg-emerald-500/10 text-emerald-200";
}

function accessScopeLabel(device: Pick<Device, "accessScope" | "isGuest">) {
  if (device.accessScope === "shared-scoped") return "Scoped Access";
  if (device.accessScope === "shared-legacy") return "Legacy Shared";
  if (device.isGuest) return "Shared Device";
  return "Owner";
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
  const lanIps = lanIpsForDevice(device);
  const isOffline = !device.online;

  return (
    <div
      className={[
        "rounded-2xl border bg-surface-900/80 transition-colors",
        compact ? "p-3" : "p-4",
        isSelected
          ? connectionError
            ? "border-red-500/30 bg-red-500/[0.04]"
            : isConnecting
              ? "border-amber-500/30 bg-amber-500/[0.04]"
              : "border-emerald-500/30 bg-emerald-500/[0.05]"
          : "border-surface-800 hover:border-surface-700",
      ].join(" ")}
    >
      <div className="flex items-start gap-3">
        <div className="flex h-11 w-11 shrink-0 items-center justify-center rounded-2xl border border-surface-800 bg-surface-950 text-surface-400">
          <DeviceIcon platform={device.platform} />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="truncate text-sm font-semibold text-surface-100">{device.name}</h3>
            <span
              className={`inline-flex h-2.5 w-2.5 rounded-full ${
                connectionError ? "bg-red-400" : isOffline ? "bg-surface-600" : isConnecting ? "bg-amber-400" : "bg-emerald-400"
              }`}
            />
            <span className="text-xs text-surface-400">
              {connectionError ? "failed" : isOffline ? "offline" : isConnecting ? "connecting" : "online"} · {heartbeatAge}
            </span>
          </div>
          <p className="mt-1 text-xs text-surface-500">
            {devicePlatformLabel(device)}
            {device.host ? ` · ${device.host}:${device.port}` : ""}
            {device.isGuest && device.hostName ? ` · from ${device.hostName}` : ""}
          </p>
          {connectionError ? (
            <p className="mt-1 text-xs text-red-300/80">{connectionError}</p>
          ) : null}
        </div>
      </div>

      <div className="mt-3 flex flex-wrap gap-1.5">
        <span className={`rounded-full border px-2 py-1 text-[10px] font-semibold uppercase tracking-[0.16em] ${accessScopeTone(device)}`}>
          {accessScopeLabel(device)}
        </span>
        {device.deviceClass ? (
          <span className="rounded-full border border-sky-500/30 bg-sky-500/10 px-2 py-1 text-[10px] font-semibold uppercase tracking-[0.16em] text-sky-200">
            {device.deviceClass === "edge-mobile" ? "Edge Worker" : device.deviceClass}
          </span>
        ) : null}
        {device.sessionBinding ? (
          <span className={`rounded-full border px-2 py-1 text-[10px] font-semibold uppercase tracking-[0.16em] ${
            device.sessionBinding === "dedicated"
              ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-200"
              : "border-amber-500/30 bg-amber-500/10 text-amber-200"
          }`}>
            {device.sessionBinding === "dedicated" ? "Dedicated Session" : "Legacy Session"}
          </span>
        ) : null}
        {isPrimary ? (
          <span className="rounded-full border border-indigo-500/40 bg-indigo-500/10 px-2 py-1 text-[10px] font-semibold uppercase tracking-[0.16em] text-indigo-200">
            Primary
          </span>
        ) : null}
        {device.priorityMode === "spare-capacity" ? (
          <span className="rounded-full border border-violet-500/40 bg-violet-500/10 px-2 py-1 text-[10px] font-semibold uppercase tracking-[0.16em] text-violet-200">
            Spare Capacity
          </span>
        ) : null}
        {lanIps.map((ip) => (
          <span key={`${device.id}:${ip}`} className="rounded-full border border-surface-700 bg-surface-950 px-2 py-1 font-mono text-[10px] text-surface-300">
            LAN {ip}
          </span>
        ))}
      </div>

      {shareSummary ? (
        <div className="mt-3 flex flex-wrap gap-1.5">
          <span className={`rounded-full border px-2 py-1 text-[10px] font-semibold uppercase tracking-[0.16em] ${
            shareSummary.projectLabel === "All Resources"
              ? "border-sky-500/40 bg-sky-500/10 text-sky-200"
              : "border-amber-500/40 bg-amber-500/10 text-amber-200"
          }`}>
            {shareSummary.projectLabel}
          </span>
          {shareSummary.projectChips.map((project) => (
            <span key={`${device.id}:project:${project}`} className="rounded-full border border-amber-500/30 bg-amber-500/10 px-2 py-1 text-[10px] font-medium text-amber-100">
              {project}
            </span>
          ))}
          <span className={`rounded-full border px-2 py-1 text-[10px] font-semibold uppercase tracking-[0.16em] ${
            shareSummary.runnerLabel === "All Agents"
              ? "border-emerald-500/40 bg-emerald-500/10 text-emerald-200"
              : "border-violet-500/40 bg-violet-500/10 text-violet-200"
          }`}>
            {shareSummary.runnerLabel}
          </span>
          {shareSummary.runnerChips.map((runner) => (
            <span key={`${device.id}:runner:${runner}`} className="rounded-full border border-violet-500/30 bg-violet-500/10 px-2 py-1 text-[10px] font-medium text-violet-100">
              {runner}
            </span>
          ))}
        </div>
      ) : null}

      {device.edgeProfile ? (
        <p className="mt-3 text-[11px] text-surface-500">
          {device.edgeProfile.supportsLocalInference ? "Local inference" : "Remote inference only"} · max {device.edgeProfile.maxModelClass} model
          {device.edgeProfile.preferredTasks.length > 0 ? ` · ${device.edgeProfile.preferredTasks.slice(0, 3).join(", ")}` : ""}
        </p>
      ) : null}

      <div className="mt-3 flex flex-wrap items-center gap-2">
        <button
          onClick={onConnect}
          disabled={isConnecting}
          className={`rounded-xl px-3 py-2 text-xs font-semibold transition-colors ${
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
            className="rounded-xl border border-indigo-500/30 bg-indigo-500/10 px-3 py-2 text-xs font-semibold text-indigo-200 hover:bg-indigo-500/15"
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
  const { devices, refreshDevices } = useDevices(token);
  const { theme, toggle: toggleTheme } = useTheme();
  const router = useRouter();

  const [connState, setConnState] = useState<ConnectionState>("disconnected");
  const [connectedDevice, setConnectedDevice] = useState<Device | null>(null);
  const [agentInfo, setAgentInfo] = useState<AgentInfo | null>(null);
  const [tasks, setTasks] = useState<Task[]>([]);
  const [activeTask, setActiveTask] = useState<Task | null>(null);
  const [outputLines, setOutputLines] = useState<string[]>([]);
  const [runners, setRunners] = useState<Runner[]>([]);
  const [input, setInput] = useState("");
  const [sending, setSending] = useState(false);
  const [guestCode, setGuestCode] = useState("");
  const [activeTab, setActiveTab] = useState<"home" | "chat" | "projects" | "vibe" | "devices" | "git" | "todos" | "builds" | "preview" | "health" | "quality" | "convex" | "data" | "switch" | "accounts" | "console" | "observ" | "ops" | "extras" | "share" | "guests" | "infra" | "connect" | "tools" | "security" | "morning" | "storage" | "vault" | "apikeys" | "schedules" | "exec" | "phone" | "domains">("home");
  const [todoCount, setTodoCount] = useState(0);
  const [connectError, setConnectError] = useState<string | null>(null);
  const [connectDiagnostics, setConnectDiagnostics] = useState<ConnectAttemptDiagnostic[]>([]);
  const [copiedReauth, setCopiedReauth] = useState(false);
  const [reauthing, setReauthing] = useState(false);
  const [reauthMessage, setReauthMessage] = useState<{ kind: "ok" | "err"; text: string } | null>(null);
  const [relayReady, setRelayReady] = useState(false);
  const [previewTargetId, setPreviewTargetId] = useState<string | null>(null);
  // Primary-device preference — the device auto-connect prefers when the
  // user has more than one online. Also mirrored onto mobile and CLI via
  // the /settings endpoint so every surface picks the same default.
  const [primaryDeviceId, setPrimaryDeviceId] = useState<string | null>(null);

  const inputRef = useRef<HTMLTextAreaElement>(null);
  const outputRef = useRef<HTMLDivElement>(null);
  const relayReadyPromiseRef = useRef<Promise<void> | null>(null);

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

  useEffect(() => { const u = agentClient.on("connectionState", setConnState); return u; }, []);

  useEffect(() => {
    const u = agentClient.on("output", (tid, line) => { setActiveTask(at => { if (at && tid === at.id) setOutputLines(p => [...p, line]); return at; }); });
    return u;
  }, []);

  useEffect(() => { if (outputRef.current) outputRef.current.scrollTop = outputRef.current.scrollHeight; }, [outputLines]);

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

    try {
      await agentClient.connect(device.host, device.port, token, device.id);
      setConnectDiagnostics(agentClient.lastConnectDiagnostics);
      try { setAgentInfo(await agentClient.getInfo()); } catch {}
      try { setRunners(await agentClient.getRunners()); } catch {}
    } catch (err: any) {
      setConnectError(err?.message || "Could not connect to device");
      setConnectDiagnostics(agentClient.lastConnectDiagnostics);
    }
  };

  const disconnect = () => { agentClient.disconnect(); setConnectedDevice(null); setAgentInfo(null); setTasks([]); setActiveTask(null); setOutputLines([]); setRunners([]); setConnectError(null); };

  const handleSend = async (e?: React.FormEvent) => {
    e?.preventDefault();
    const text = input.trim(); if (!text || sending) return;
    setInput(""); setSending(true);
    try {
      if (activeTask?.status === "running") { await agentClient.continueTask(activeTask.id, text); }
      else { const t = await agentClient.sendTask(text.slice(0, 80), text); setActiveTask(t); setOutputLines([]); setTasks(p => [t, ...p]); }
    } catch {} setSending(false);
  };

  const selectTask = (t: Task) => { setActiveTask(t); setOutputLines(t.output || []); setActiveTab("chat"); };
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

  const runningTask = tasks.find(t => t.status === "running");
  const mobileWorkers = devices.filter((d) => d.deviceClass === "edge-mobile");
  const visibleDevices = devices;
  const selectedPreviewTarget = mobileWorkers.find((d) => d.id === previewTargetId) || null;
  const tabs: { id: typeof activeTab; label: string; icon: string; badge?: number }[] = [
    { id: "home", label: "Home", icon: "\uD83C\uDFE0" },
    { id: "chat", label: "Chat", icon: "\uD83D\uDCAC" },
    { id: "projects", label: "Projects", icon: "\uD83D\uDCC1" },
    { id: "vibe", label: "Vibe", icon: "\u2328\uFE0F" },
    { id: "todos", label: "Todos", icon: "\u2611\uFE0F", badge: todoCount },
    { id: "builds", label: "Builds", icon: "\uD83D\uDE80" },
    { id: "preview", label: "Preview", icon: "\uD83C\uDFA8" },
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
    <div className="flex flex-col md:flex-row h-screen overflow-hidden">
      {/* Mobile top bar — visible only below md */}
      <div className="md:hidden border-b border-surface-800 bg-surface-900/50">
        <div className="flex items-center gap-2 px-3 py-2">
          <div className="flex h-6 w-6 items-center justify-center rounded-full bg-surface-800 text-[10px] font-bold text-surface-300">{user?.email?.charAt(0).toUpperCase()}</div>
          <span className="text-xs font-medium text-surface-200 flex-1 truncate">{connectedDevice?.name || "No device"}</span>
          <span className={`h-1.5 w-1.5 rounded-full ${isConnected ? "bg-emerald-400" : "bg-surface-600"}`} />
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
              { id: "home",     label: "Home",     icon: "⌂" },
              { id: "chat",     label: "Chat",     icon: "💬" },
              { id: "vibe",     label: "Vibe",     icon: "⌨️" },
              { id: "projects", label: "Projects", icon: "📁" },
              { id: "devices",  label: "Devices",  icon: "💻" },
              { id: "git",      label: "Git",      icon: "⎇" },
              { id: "builds",   label: "Builds",   icon: "🛠️" },
              { id: "preview",  label: "Hot Reload", icon: "📱" },
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
              <div className="rounded-md border border-emerald-500/30 bg-emerald-500/5 px-2 py-1.5">
                <div className="flex items-center gap-2">
                  <span className="h-2 w-2 shrink-0 rounded-full bg-emerald-400" />
                  <span className="truncate text-xs font-medium text-surface-100">{connectedDevice.name}</span>
                </div>
                <div className="mt-1 flex items-center justify-between">
                  <span className="text-[10px] text-surface-500">
                    {devicePlatformLabel(connectedDevice)}
                    {agentInfo ? ` · v${agentInfo.version}` : ""}
                  </span>
                  <button onClick={disconnect} className="text-[10px] text-red-400 hover:text-red-300">disconnect</button>
                </div>
              </div>
            ) : visibleDevices.length === 0 ? (
              <p className="text-[11px] text-surface-600">No devices yet</p>
            ) : (
              <div className="space-y-0.5">
                {visibleDevices.slice(0, 5).map((d) => {
                  const isSelected = connectedDevice?.id === d.id;
                  const isConnecting = isSelected && connState === "connecting";
                  const hasError = isSelected && connState === "error";
                  return (
                    <button
                      key={d.id}
                      onClick={() => connectToDevice(d)}
                      className={`flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs transition-colors ${
                        hasError
                          ? "border border-red-500/30 bg-red-500/5"
                          : isConnecting
                            ? "border border-amber-500/30 bg-amber-500/5"
                            : "border border-transparent hover:bg-surface-800/80"
                      }`}
                      title={`${d.host}:${d.port}`}
                    >
                      <span
                        className={`h-2 w-2 shrink-0 rounded-full ${
                          hasError ? "bg-red-400" : isConnecting ? "bg-amber-400" : d.online ? "bg-emerald-400" : "bg-surface-600"
                        }`}
                      />
                      <span className="min-w-0 flex-1 truncate text-surface-200">{d.name}</span>
                      {primaryDeviceId === d.id ? (
                        <span className="shrink-0 text-[9px] text-indigo-400" title="Primary">&#9733;</span>
                      ) : null}
                      {d.isGuest ? (
                        <span className="shrink-0 rounded bg-sky-500/15 px-1 text-[9px] uppercase text-sky-300">shared</span>
                      ) : null}
                    </button>
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
                  <span className={`inline-block h-1 w-1 rounded-full mr-1 ${t.status === "running" ? "bg-amber-400" : t.status === "completed" ? "bg-emerald-400" : "bg-surface-600"}`} />{t.title}
                </button>
              ))}
            </div>
          )}
        </div>
        <div className="mt-auto border-t border-surface-800 p-3 flex items-center justify-between">
          <button onClick={logout} className="text-[11px] text-red-400 hover:text-red-300">Sign Out</button>
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
      <div className="flex flex-1 min-w-0 flex-col">
        {isConnected && (
          <div className="flex items-center gap-1 border-b border-surface-800 px-3 py-1 bg-surface-900/30 overflow-x-auto">
            {tabs.map(tab => (
              <button key={tab.id} onClick={() => setActiveTab(tab.id)} className={`shrink-0 rounded-md px-2.5 py-1 text-xs font-medium flex items-center gap-1 ${activeTab === tab.id ? "bg-indigo-500/10 text-indigo-400" : "text-surface-500 hover:text-surface-300 hover:bg-surface-800"}`}>
                <span>{tab.icon}</span><span className="hidden sm:inline">{tab.label}</span>
                {tab.badge ? <span className="rounded-full bg-indigo-500/20 px-1 text-[9px] text-indigo-400">{tab.badge}</span> : null}
              </button>
            ))}
            <div className="flex-1" />
            {runningTask && <button onClick={() => agentClient.stopTask(runningTask.id)} className="shrink-0 rounded-md border border-red-500/30 bg-red-500/10 px-2 py-0.5 text-[10px] text-red-300 hover:bg-red-500/20">Stop</button>}
          </div>
        )}

        <div className="flex flex-1 min-h-0 flex-col">
          {!isConnected ? (
            <div className="flex flex-1 items-center justify-center p-8">
              <div className="max-w-md text-center">
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
                    // If /health responded at all, the box is up — box auth is the problem.
                    // If no attempt got a status, the box is either unreachable or all relays rejected the probe (e.g. 404 from relay = device not registered).
                    const headline = authExpired
                      ? "Agent reachable, but its auth is expired"
                      : anyReached
                        ? "Agent responded, but the connection was rejected"
                        : "Could not reach agent";
                    const reauthCmd = "yaver auth";
                    return (
                      <div className="flex flex-col items-center gap-3 w-full max-w-md">
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
                                  <span className="text-surface-400 w-14 shrink-0">
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

                          {authExpired ? (
                            <div className="mt-3 rounded border border-amber-500/20 bg-amber-500/5 p-2 text-left">
                              <p className="text-[11px] text-amber-300">
                                The agent can&apos;t validate its session with Convex anymore. You&apos;re already signed in here &mdash; hand your session down to the box:
                              </p>
                              <div className="mt-2 flex items-center gap-2">
                                <button
                                  disabled={reauthing || !connectedDevice || !token}
                                  onClick={async () => {
                                    if (!connectedDevice || !token) return;
                                    setReauthing(true);
                                    setReauthMessage(null);
                                    try {
                                      const result = await agentClient.reauthDirect({
                                        deviceId: connectedDevice.id,
                                        hostSessionToken: token,
                                      });
                                      if (result.ok) {
                                        setReauthMessage({ kind: "ok", text: "Agent accepted new token — reconnecting…" });
                                        setTimeout(() => {
                                          connectToDevice(connectedDevice);
                                        }, 400);
                                      } else {
                                        setReauthMessage({ kind: "err", text: result.error || "Re-auth failed" });
                                      }
                                    } catch (e: any) {
                                      setReauthMessage({ kind: "err", text: e?.message || "Re-auth failed" });
                                    }
                                    setReauthing(false);
                                  }}
                                  className="flex-1 rounded border border-amber-500/40 bg-amber-500/10 px-3 py-1.5 text-[11px] font-medium text-amber-200 hover:bg-amber-500/20 disabled:opacity-50"
                                >
                                  {reauthing ? "Re-authing…" : "Re-auth this device from web"}
                                </button>
                              </div>
                              {reauthMessage ? (
                                <p className={`mt-2 text-[10px] ${reauthMessage.kind === "ok" ? "text-emerald-300" : "text-red-300"}`}>
                                  {reauthMessage.text}
                                </p>
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
                          ) : !anyReached ? (
                            <p className="mt-3 text-xs text-surface-600">
                              Make sure <code className="rounded bg-surface-800 px-1 py-0.5 text-surface-400">yaver serve</code> is running on this machine and it&apos;s reachable from your relay.
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
                    <div className="space-y-3 text-left">
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
            <div className="flex-1 overflow-y-auto p-6 max-w-4xl mx-auto w-full"><BuildsView onTaskCreated={onTaskCreated} /></div>
          ) : activeTab === "preview" ? (
            <div className="flex-1 min-h-0"><PreviewPane selectedPreviewTarget={selectedPreviewTarget} onSelectPreviewTarget={handleSelectPreviewTarget} mobileWorkers={mobileWorkers} /></div>
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
                devices={devices}
                onRefresh={refreshDevices}
                signedInEmail={user?.email}
                signedInProvider={undefined}
                token={token}
              />
            </div>
          ) : activeTab === "git" ? (
            <div className="flex-1 overflow-y-auto p-6 max-w-3xl mx-auto w-full"><GitView /></div>
          ) : (
            <>
              <div className="flex flex-1 min-h-0">
                <div className="flex flex-1 min-w-0 flex-col">
                  {activeTask ? (
                    <>
                      <div className="flex items-center gap-3 border-b border-surface-800 px-4 py-2">
                        <span className={`h-1.5 w-1.5 rounded-full ${activeTask.status === "running" ? "animate-pulse bg-amber-400" : activeTask.status === "completed" ? "bg-emerald-400" : "bg-surface-600"}`} />
                        <span className="truncate text-sm font-medium text-surface-200">{activeTask.title}</span>
                        <span className={`text-[10px] ${statusColor(activeTask.status)}`}>{activeTask.status}</span>
                        {activeTask.costUsd != null && <span className="text-[10px] text-surface-600">${activeTask.costUsd.toFixed(3)}</span>}
                      </div>
                      <div ref={outputRef} className="flex-1 overflow-y-auto bg-surface-950 p-4 font-mono text-[12px] leading-5 text-surface-300">
                        {outputLines.length === 0 ? (
                          <div className="flex items-center gap-2 text-surface-600">
                            {activeTask.status === "running" && <span className="h-3 w-3 animate-spin rounded-full border border-surface-500 border-t-transparent" />}
                            {activeTask.status === "running" ? "Working..." : "No output"}
                          </div>
                        ) : outputLines.map((line, i) => (
                          <div key={i} className="whitespace-pre-wrap break-all">{line.startsWith("> ") ? <span className="text-emerald-400">{line}</span> : line}</div>
                        ))}
                      </div>
                    </>
                  ) : (
                    <div className="flex flex-1 items-center justify-center text-sm text-surface-600">Describe what you want to build</div>
                  )}
                </div>
              </div>
              <div className="border-t border-surface-800 bg-surface-900/50 p-3">
                <form onSubmit={handleSend} className="mx-auto flex max-w-4xl items-end gap-2">
                  <textarea ref={inputRef} value={input} onChange={e => setInput(e.target.value)}
                    onKeyDown={e => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); handleSend(); } }}
                    placeholder={runningTask ? "Follow up..." : "Build me a todo app..."} rows={1}
                    className="max-h-32 w-full resize-none rounded-lg border border-surface-700 bg-surface-850 px-4 py-2.5 text-sm text-surface-100 placeholder-surface-600 outline-none focus:border-surface-500" style={{ minHeight: "42px" }} />
                  <button type="submit" disabled={!input.trim() || sending}
                    className="shrink-0 rounded-lg bg-surface-100 px-4 py-2.5 text-sm font-medium text-surface-900 hover:bg-surface-50 disabled:opacity-30">
                    {sending ? "..." : runningTask ? "Send" : "Run"}
                  </button>
                </form>
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
