"use client";

import Link from "next/link";
import React, { useCallback, useEffect, useRef, useState } from "react";
import { type Device, hideDevice, unhideAll } from "@/lib/use-devices";
import { CONVEX_URL } from "@/lib/constants";
import { agentClient, AgentClient, type RunnerBrowserAuthSession } from "@/lib/agent-client";

function DeviceIcon({ platform }: { platform: string }) {
  const isMobile = platform === "iOS" || platform === "Android";
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

function platformLabel(platform: string): string {
  switch (platform.toLowerCase()) {
    case "darwin":
      return "macOS";
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
      return platform;
  }
}

function isLikelyWSLDevice(device: Pick<Device, "name" | "platform" | "host">): boolean {
  const platform = String(device.platform || "").trim().toLowerCase();
  if (platform !== "linux") return false;
  const name = String(device.name || "").trim().toUpperCase();
  const host = String(device.host || "").trim();
  const windowsHostLike =
    name.startsWith("DESKTOP-") ||
    name.startsWith("LAPTOP-") ||
    name.startsWith("WIN-");
  const wslNatLike = /^172\.(1[6-9]|2\d|3[0-1])\.\d{1,3}\.\d{1,3}$/.test(host);
  return windowsHostLike || wslNatLike;
}

function devicePlatformLabel(device: Pick<Device, "name" | "platform" | "host">): string {
  const base = platformLabel(device.platform);
  if (isLikelyWSLDevice(device)) {
    return "Linux (likely WSL)";
  }
  return base;
}

function formatLastSeen(value: string | undefined): string {
  if (!value) return "unknown";
  const ts = Date.parse(value);
  if (Number.isNaN(ts)) return value;
  const diff = Math.max(0, Date.now() - ts);
  const sec = Math.floor(diff / 1000);
  if (sec < 60) return sec <= 5 ? "just now" : `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const day = Math.floor(hr / 24);
  if (day < 7) return `${day}d ago`;
  return new Date(ts).toLocaleDateString();
}

function lastSeenAgeMs(value: string | undefined): number | null {
  if (!value) return null;
  const ts = Date.parse(value);
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

/**
 * Common coding-agent runner ids we always render on a device card so the
 * user sees, at a glance, whether the agent they want is installed and
 * authenticated. The agent's heartbeat surfaces only the runners it
 * actually detected, so for everything else we render a "not installed"
 * chip — better than the chip just being missing.
 */
const KNOWN_RUNNERS = [
  "claude",
  "codex",
  "aider",
  "aider-ollama",
  "ollama",
  "opencode",
  "goose",
] as const;

type RunnerHealth = "ready" | "needs-auth" | "down" | "not-installed" | "unknown";

interface RunnerChipState {
  id: string;
  label: string;
  health: RunnerHealth;
  hint?: string;
}

function deriveRunnerChipStates(
  device: Pick<Device, "runners" | "sharedRunners">,
): RunnerChipState[] {
  const reported = new Map<string, { status?: string; raw?: any }>();
  for (const r of device.runners || []) {
    const id = formatRunnerChipLabel(String(r?.runnerId || ""));
    if (!id) continue;
    reported.set(id, { status: typeof r?.status === "string" ? r.status : undefined, raw: r });
  }
  // Guests inherit shared runners only — treat them as known-installed
  // (the host wouldn't share a runner that wasn't actually there).
  for (const r of device.sharedRunners || []) {
    const id = formatRunnerChipLabel(String(r));
    if (!id) continue;
    if (!reported.has(id)) reported.set(id, {});
  }

  const seen = new Set<string>();
  const out: RunnerChipState[] = [];

  const classify = (id: string, status?: string): RunnerChipState => {
    const s = (status || "").toLowerCase();
    if (s.includes("needs-auth") || s.includes("needs_auth") || s.includes("unauth") || s.includes("login")) {
      return { id, label: id, health: "needs-auth", hint: status };
    }
    if (s.includes("down") || s.includes("error") || s.includes("fail")) {
      return { id, label: id, health: "down", hint: status };
    }
    if (!status) return { id, label: id, health: "ready" };
    return { id, label: id, health: "ready", hint: status };
  };

  for (const id of KNOWN_RUNNERS) {
    seen.add(id);
    const r = reported.get(id);
    if (r) out.push(classify(id, r.status));
    else out.push({ id, label: id, health: "not-installed", hint: "Not detected on this machine" });
  }
  // Anything reported that isn't in the known set — append at the end.
  for (const [id, r] of reported.entries()) {
    if (seen.has(id)) continue;
    out.push(classify(id, r.status));
  }
  return out;
}

function runnerChipClass(health: RunnerHealth): string {
  switch (health) {
    case "ready":
      return "border-emerald-500/40 bg-emerald-500/10 text-emerald-200";
    case "needs-auth":
      return "border-amber-500/40 bg-amber-500/10 text-amber-200";
    case "down":
      return "border-red-500/40 bg-red-500/10 text-red-200";
    case "not-installed":
      return "border-surface-800 bg-surface-900/40 text-surface-500";
    default:
      return "border-surface-700 bg-surface-900/40 text-surface-400";
  }
}

function runnerChipDotClass(health: RunnerHealth): string {
  switch (health) {
    case "ready": return "bg-emerald-400";
    case "needs-auth": return "bg-amber-400";
    case "down": return "bg-red-400";
    case "not-installed": return "bg-surface-700";
    default: return "bg-surface-600";
  }
}

function runnerChipTitle(state: RunnerChipState): string {
  switch (state.health) {
    case "ready": return `${state.label}: installed and authenticated${state.hint ? ` (${state.hint})` : ""}`;
    case "needs-auth": return `${state.label}: installed but needs auth — set ANTHROPIC_API_KEY / OPENAI_API_KEY / etc. on the host`;
    case "down": return `${state.label}: detected but reporting an error: ${state.hint ?? "unknown"}`;
    case "not-installed": return `${state.label}: not installed on this machine`;
    default: return state.label;
  }
}

function sharedGuestLabels(device: Pick<Device, "sharedGuests">): string[] {
  return (device.sharedGuests || [])
    .map((guest) => guest.name || guest.email || "")
    .map((label) => String(label).trim())
    .filter(Boolean);
}

function deviceShareSummary(device: Pick<Device, "isGuest" | "hostName" | "sharedWithGuests" | "sharedGuests" | "sharesAllProjects" | "sharedProjects" | "sharedRunners" | "runners">) {
  const hasSharedState = device.isGuest || device.sharedWithGuests;
  if (!hasSharedState) return null;
  const sharedProjects = Array.isArray(device.sharedProjects) ? device.sharedProjects.filter(Boolean) : [];
  const guests = sharedGuestLabels(device);
  const viewerIsGuest = !!device.isGuest;
  return {
    viewerIsGuest,
    hostLabel: viewerIsGuest ? device.hostName || "host" : null,
    projectLabel: viewerIsGuest ? (device.sharesAllProjects ? "All Resources" : sharedProjects.length > 0 ? "Projects" : null) : null,
    projectChips: viewerIsGuest && !device.sharesAllProjects ? sharedProjects : [],
    runnerChips: runnerChipsForDevice(device),
    guestChips: guests.slice(0, 3),
    guestOverflow: Math.max(0, guests.length - 3),
  };
}

interface DevicesViewProps {
  devices: Device[];
  onRefresh: () => Promise<void>;
  signedInEmail?: string;
  signedInProvider?: string;
  token?: string | null;
  /**
   * Connect/open this device as the active workspace. Wired through to the
   * dashboard's `connectToDevice` so the prominent "Open Workspace" CTA on
   * each card flips the dashboard into the chat/vibe surface for that
   * machine in one click — instead of users hunting for the small dot in
   * the sidebar.
   */
  onOpen?: (device: Device) => void;
  /** Close the currently-open workspace session. */
  onCloseWorkspace?: () => void;
  /** Device id currently opened as the active workspace, if any. */
  activeWorkspaceDeviceId?: string | null;
  /** Count of devices hidden via the Hide button — surfaced for the "show all" link. */
  hiddenCount?: number;
}

interface DeviceRuntimeInfo {
  hostname?: string;
  version?: string;
  platform?: string;
  workDir?: string;
  autoStart?: string;
  runtime?: Record<string, unknown>;
  system?: Record<string, unknown>;
  [k: string]: unknown;
}

function useDevicePing(device: Device, token: string | null | undefined) {
  const [pingState, setPingState] = useState<{ pinging: boolean; rttMs?: number; ok?: boolean; error?: string }>({ pinging: false });

  const ping = useCallback(async () => {
    if (!token) {
      setPingState({ pinging: false, ok: false, error: "not signed in" });
      return;
    }
    setPingState({ pinging: true });
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
      if (probe.ok) {
        setPingState({ pinging: false, ok: true, rttMs: Date.now() - started });
      } else {
        setPingState({ pinging: false, ok: false, error: probe.error });
      }
    } catch (e: any) {
      setPingState({ pinging: false, ok: false, error: e?.message || "probe failed" });
    }
  }, [device.host, device.id, device.port, device.tunnelUrl, device.publicEndpoints, token]);

  return { pingState, ping };
}

function useDeviceRuntimeInfo(device: Device, enabled: boolean, token: string | null | undefined) {
  const [info, setInfo] = useState<DeviceRuntimeInfo | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!enabled || !token || (!device.online && !device.workspaceLive)) return;
    let cancelled = false;
    setLoading(true);
    setError(null);
    // Direct LAN first (best when browser is on the same Wi-Fi) then fall
    // back to Convex-stored heartbeat fields we already have. We stay on
    // the agent /info endpoint since that's what the mobile app uses for
    // the same "everything about this device" view.
    (async () => {
      try {
        const base = `http://${device.host}:${device.port}`;
        const res = await fetch(`${base}/info`, {
          headers: { Authorization: `Bearer ${token}` },
          signal: AbortSignal.timeout(3_000),
        });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const data = await res.json();
        if (!cancelled) setInfo(data);
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : "fetch failed");
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, [enabled, token, device.id, device.host, device.port, device.online, device.workspaceLive]);

  return { info, error, loading };
}

/**
 * Loads the user's current primaryDeviceId from Convex and exposes a setter
 * that POSTs back to /settings. Shared between the dashboard's device cards
 * so only one settings round-trip is made on mount. Null state ("no primary")
 * is the default for fresh accounts and for anyone who hasn't opted in.
 */
function usePrimaryDeviceId(token: string | null | undefined): {
  primaryDeviceId: string | null;
  setPrimaryDevice: (id: string | null) => Promise<void>;
} {
  const [primaryDeviceId, setPrimaryDeviceId] = useState<string | null>(null);

  useEffect(() => {
    if (!token) return;
    let cancelled = false;
    (async () => {
      try {
        const res = await fetch(`${CONVEX_URL}/settings`, {
          headers: { Authorization: `Bearer ${token}` },
        });
        if (!res.ok) return;
        const data = await res.json();
        if (!cancelled) {
          setPrimaryDeviceId(data?.settings?.primaryDeviceId ?? null);
        }
      } catch {
        // best-effort — UI falls back to "no primary"
      }
    })();
    return () => { cancelled = true; };
  }, [token]);

  const setPrimaryDevice = useCallback(async (id: string | null) => {
    if (!token) return;
    // Optimistic update — roll back on failure.
    const previous = primaryDeviceId;
    setPrimaryDeviceId(id);
    try {
      const res = await fetch(`${CONVEX_URL}/settings`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify({ primaryDeviceId: id }),
      });
      if (!res.ok) throw new Error(`status ${res.status}`);
    } catch (e) {
      setPrimaryDeviceId(previous);
      throw e;
    }
  }, [token, primaryDeviceId]);

  return { primaryDeviceId, setPrimaryDevice };
}

export default function DevicesView({
  devices,
  onRefresh,
  signedInEmail,
  signedInProvider,
  token,
  onOpen,
  onCloseWorkspace,
  activeWorkspaceDeviceId = null,
  hiddenCount = 0,
}: DevicesViewProps) {
  const { primaryDeviceId, setPrimaryDevice } = usePrimaryDeviceId(token);
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [authModal, setAuthModal] = useState<{ device: Device; runner: string } | null>(null);
  const [showDormantDevices, setShowDormantDevices] = useState(false);
  const actionableDevices = devices.filter((device) => !isDormantUnreachableDevice(device));
  const dormantDevices = devices.filter((device) => isDormantUnreachableDevice(device));
  const renderedDevices = showDormantDevices ? devices : actionableDevices;
  return (
    <div className="mb-6">
      <div className="mb-3 flex items-center justify-between">
        <h2 className="text-lg font-semibold text-surface-50">Devices</h2>
        <div className="flex items-center gap-2">
          {dormantDevices.length > 0 ? (
            <button
              onClick={() => setShowDormantDevices((value) => !value)}
              className="rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-1.5 text-xs font-medium text-amber-200 hover:bg-amber-500/15"
              title="Reveal stale devices with no recent agent signal and no usable public path"
            >
              {showDormantDevices ? "Hide stale devices" : `Show stale devices (${dormantDevices.length})`}
            </button>
          ) : null}
          <button
            onClick={() => onRefresh()}
            className="btn-secondary px-3 py-1.5 text-xs"
          >
            Refresh
          </button>
        </div>
      </div>

      {renderedDevices.length === 0 ? (
        <div className="card p-8 text-center">
          <p className="mb-2 text-sm text-surface-400">No devices registered.</p>
          {dormantDevices.length > 0 ? (
            <p className="mb-3 text-xs text-amber-300">
              {dormantDevices.length} stale device{dormantDevices.length === 1 ? "" : "s"} hidden by default because they have no recent agent signal and no public path.
            </p>
          ) : null}
          {signedInEmail ? (
            <p className="mb-3 text-xs text-surface-500">
              Signed in as <span className="font-medium text-surface-300">{signedInEmail}</span>
              {signedInProvider ? ` via ${signedInProvider}` : ""}.
              If you expected devices here, check that this matches the account used on your machines.
            </p>
          ) : null}
          <p className="mb-4 text-xs text-surface-500">
            Install the Yaver CLI on your machine and run <code className="rounded bg-surface-800 px-1.5 py-0.5 text-surface-300">yaver auth</code> to register.
          </p>
          <Link href="/download" className="btn-secondary px-4 py-2 text-sm">
            Download Yaver
          </Link>
        </div>
      ) : (
        <div className="space-y-2">
          {hiddenCount > 0 ? (
            <div className="flex items-center justify-between rounded-lg border border-surface-800 bg-surface-900/40 px-3 py-2 text-xs text-surface-400">
              <span>{hiddenCount} device{hiddenCount === 1 ? "" : "s"} hidden in this browser.</span>
              <button
                onClick={() => unhideAll()}
                className="text-indigo-400 hover:text-indigo-300"
              >
                Show all
              </button>
            </div>
          ) : null}
          {!showDormantDevices && dormantDevices.length > 0 ? (
            <div className="rounded-lg border border-amber-500/20 bg-amber-500/5 px-3 py-2 text-xs text-amber-200">
              {dormantDevices.length} stale device{dormantDevices.length === 1 ? "" : "s"} hidden because they have no recent agent signal and no usable relay/tunnel path.
            </div>
          ) : null}
          {renderedDevices.map((device) => {
            const shareSummary = deviceShareSummary(device);
            const isActiveWorkspace = activeWorkspaceDeviceId === device.id;
            return (
            <div key={device.id} className="card flex items-start gap-4">
              <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-surface-800 text-surface-400">
                <DeviceIcon platform={device.platform} />
              </div>
              <div className="min-w-0 flex-1">
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <h3 className="font-semibold text-surface-50">
                        {device.name}
                      </h3>
                      {device.isGuest ? (
                        <span className="rounded border border-sky-500/40 bg-sky-500/10 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-sky-200">
                          Shared Device
                        </span>
                      ) : device.sharedWithGuests ? (
                        <span className="rounded border border-sky-500/40 bg-sky-500/10 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-sky-200">
                          Shared
                        </span>
                      ) : null}
                      {device.deviceClass ? (
                        <span className="rounded border border-sky-500/30 bg-sky-500/10 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-sky-200">
                          {device.deviceClass === "edge-mobile" ? "Edge Worker" : device.deviceClass}
                        </span>
                      ) : null}
                      {!device.isGuest && device.sessionBinding ? (
                        <span
                          className={`rounded border px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider ${
                            device.sessionBinding === "dedicated"
                              ? "border-emerald-500/40 bg-emerald-500/10 text-emerald-300"
                              : "border-amber-500/40 bg-amber-500/10 text-amber-300"
                          }`}
                        >
                          {device.sessionBinding === "dedicated" ? "Dedicated Session" : "Legacy Shared Session"}
                        </span>
                      ) : null}
                      <span
                        className={`inline-flex h-2 w-2 rounded-full ${
                          device.workspaceLive
                            ? "bg-emerald-300"
                            : device.probeState === "ok"
                              ? "bg-cyan-400"
                              : device.probeState === "auth-expired"
                                ? "bg-amber-400"
                            : device.peerState === "online"
                            ? "bg-cyan-400"
                            : device.online
                              ? "bg-green-400"
                              : device.peerState === "stale"
                                ? "bg-amber-400"
                                : "bg-surface-600"
                        }`}
                      />
                      <span className="text-xs text-surface-500">
                        {device.workspaceLive
                          ? "Workspace Live"
                          : device.probeState === "ok"
                            ? "Probed"
                            : device.probeState === "auth-expired"
                              ? "Needs Auth"
                          : device.peerState === "online"
                          ? "Bus Live"
                          : device.online
                            ? "Online"
                            : device.peerState === "stale"
                              ? "Bus Stale"
                              : "Offline"}
                      </span>
                    </div>
                    <p className="text-sm text-surface-500">
                      {devicePlatformLabel(device)} · Last agent signal {formatLastSeen(device.lastSeen)}
                      {device.probeState === "ok" && device.probePath ? ` · probed via ${device.probePath}` : ""}
                      {device.probeState === "auth-expired" ? " · auth expired" : ""}
                    </p>
                  </div>

                  <div className="flex items-center gap-2">
                    {!device.isGuest && token ? (
                      <button
                        onClick={async () => {
                          try {
                            await setPrimaryDevice(primaryDeviceId === device.id ? null : device.id);
                          } catch (e: any) {
                            alert(`Failed to update primary: ${e?.message ?? e}`);
                          }
                        }}
                        className={`inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-[10px] font-semibold uppercase tracking-wider ${
                          primaryDeviceId === device.id
                            ? "border-amber-500/40 bg-amber-500/10 text-amber-200"
                            : "border-surface-700 bg-surface-900/40 text-surface-300 hover:border-amber-500/30 hover:text-amber-200"
                        }`}
                        title={primaryDeviceId === device.id ? "This is your primary device" : "Mark this device as your primary machine"}
                      >
                        <svg className="h-3.5 w-3.5" viewBox="0 0 24 24" fill="currentColor" aria-hidden>
                          <path d="m12 2.75 2.33 4.72 5.21.76-3.77 3.67.89 5.19L12 14.6l-4.66 2.49.89-5.19-3.77-3.67 5.21-.76L12 2.75Z" />
                        </svg>
                        {primaryDeviceId === device.id ? "Primary" : "Set as primary"}
                      </button>
                    ) : null}
                    <button
                      onClick={() => setExpandedId(expandedId === device.id ? null : device.id)}
                      className="inline-flex items-center gap-1.5 rounded-md border border-surface-800 bg-surface-900/40 px-2.5 py-1 text-[11px] font-medium text-surface-300 hover:border-surface-700 hover:bg-surface-800/60 hover:text-surface-100"
                      aria-expanded={expandedId === device.id}
                      title="Show runtime, hardware, network and sharing details"
                    >
                      <svg className={`h-3.5 w-3.5 transition-transform ${expandedId === device.id ? "rotate-90" : ""}`} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" aria-hidden>
                        <path d="m9 6 6 6-6 6" />
                      </svg>
                      {expandedId === device.id ? "Hide" : "Details"}
                    </button>
                  </div>
                </div>
                {device.edgeProfile ? (
                  <p className="text-xs text-surface-500">
                    {device.edgeProfile.supportsLocalInference ? "Local inference" : "No local inference"} · max {device.edgeProfile.maxModelClass} model · {device.edgeProfile.preferredTasks.slice(0, 3).join(", ")}
                  </p>
                ) : null}
                {shareSummary?.viewerIsGuest && shareSummary?.hostLabel ? (
                  <div className="mt-3">
                    <div className="mb-1.5 text-[10px] font-semibold uppercase tracking-widest text-surface-500">
                      Shared from
                    </div>
                    <div className="flex flex-wrap items-center gap-1.5">
                      <span
                        className="inline-flex items-center gap-1.5 rounded-full border border-sky-500/30 bg-sky-500/10 py-0.5 pl-0.5 pr-2.5 text-xs text-sky-100"
                      >
                        <span className="flex h-5 w-5 items-center justify-center rounded-full bg-sky-500/30 text-[10px] font-semibold uppercase text-sky-50">
                          {shareSummary.hostLabel.split(/\s+/).map((w) => w[0]).join("").slice(0, 2).toUpperCase() || "·"}
                        </span>
                        <span className="truncate max-w-[12rem]">{shareSummary.hostLabel}</span>
                      </span>
                    </div>
                  </div>
                ) : null}
                {shareSummary && (shareSummary.projectLabel || shareSummary.projectChips.length > 0) ? (
                  <div className="mt-2 flex flex-wrap gap-1.5">
                    {shareSummary.projectLabel ? (
                      <span className={`rounded border px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider ${
                        shareSummary.projectLabel === "All Resources"
                          ? "border-sky-500/40 bg-sky-500/10 text-sky-200"
                          : "border-amber-500/40 bg-amber-500/10 text-amber-200"
                      }`}>
                        {shareSummary.projectLabel}
                      </span>
                    ) : null}
                    {shareSummary.projectChips.map((project) => (
                      <span key={`${device.id}:project:${project}`} className="rounded border border-amber-500/40 bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-semibold tracking-wider text-amber-200">
                        {project}
                      </span>
                    ))}
                  </div>
                ) : null}
                {shareSummary && shareSummary.guestChips.length > 0 ? (
                  <div className="mt-3">
                    <div className="mb-1.5 text-[10px] font-semibold uppercase tracking-widest text-surface-500">
                      Shared with
                    </div>
                    <div className="flex flex-wrap items-center gap-1.5">
                      {shareSummary.guestChips.map((guest) => (
                        <span
                          key={`${device.id}:guest:${guest}`}
                          className="inline-flex items-center gap-1.5 rounded-full border border-sky-500/30 bg-sky-500/10 py-0.5 pl-0.5 pr-2.5 text-xs text-sky-100"
                        >
                          <span className="flex h-5 w-5 items-center justify-center rounded-full bg-sky-500/30 text-[10px] font-semibold uppercase text-sky-50">
                            {guest.split(/\s+/).map((w) => w[0]).join("").slice(0, 2).toUpperCase() || "·"}
                          </span>
                          <span className="truncate max-w-[12rem]">{guest}</span>
                        </span>
                      ))}
                      {shareSummary.guestOverflow > 0 ? (
                        <span className="inline-flex items-center rounded-full border border-surface-700 bg-surface-900 px-2.5 py-0.5 text-xs text-surface-400">
                          +{shareSummary.guestOverflow} more
                        </span>
                      ) : null}
                    </div>
                  </div>
                ) : null}
          {(() => {
                  const states = deriveRunnerChipStates(device);
                  if (states.length === 0) return null;
                  return (
                    <div className="mt-3">
                      <div className="mb-1.5 text-[10px] font-semibold uppercase tracking-widest text-surface-500">
                        Coding agents
                      </div>
                      <div className="flex flex-wrap items-center gap-1.5">
                        {states.map((state) => {
                          const supportsRemoteAuth = state.id === "codex" || state.id === "claude";
                          const needsAction = state.health !== "ready";
                          const canAuth = device.online && !device.isGuest && supportsRemoteAuth && needsAction;
                          const base = `inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-[11px] font-medium ${runnerChipClass(state.health)}`;
                          if (!canAuth) {
                            return (
                              <span
                                key={`${device.id}:runner:${state.id}`}
                                className={base}
                                title={runnerChipTitle(state)}
                              >
                                <span className={`h-1.5 w-1.5 rounded-full ${runnerChipDotClass(state.health)}`} />
                                {state.label}
                              </span>
                            );
                          }
                          return (
                            <button
                              key={`${device.id}:runner:${state.id}`}
                              onClick={() =>
                                setAuthModal({
                                  device,
                                  runner: state.id,
                                })
                              }
                              className={`${base} cursor-pointer hover:brightness-110`}
                              title={`${runnerChipTitle(state)}\nClick to sign in from this browser.`}
                            >
                              <span className={`h-1.5 w-1.5 rounded-full ${runnerChipDotClass(state.health)}`} />
                              {state.label}
                              <span className="ml-0.5 text-[10px] opacity-80">· sign in</span>
                            </button>
                          );
                        })}
                      </div>
                    </div>
                  );
                })()}
                <div className="mt-5 flex flex-wrap items-center gap-2">
                  <InlinePingButton device={device} token={token ?? null} />
                  {isActiveWorkspace && onCloseWorkspace ? (
                    <button
                      onClick={onCloseWorkspace}
                      className="inline-flex items-center gap-1.5 rounded-md border border-surface-700 bg-surface-900/60 px-3 py-1.5 text-xs font-semibold text-surface-100 shadow-sm hover:border-surface-600 hover:bg-surface-800"
                      title="Disconnect from this machine and close the active workspace"
                    >
                      <span aria-hidden>×</span>
                      Close Workspace
                    </button>
                  ) : onOpen ? (
                    <button
                      onClick={() => onOpen(device)}
                      className={`inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-semibold shadow-sm ${
                        device.online
                          ? "bg-indigo-500 text-white hover:bg-indigo-400"
                          : "border border-amber-500/30 bg-amber-500/10 text-amber-200 hover:bg-amber-500/20"
                      }`}
                      title={device.online
                        ? "Connect to this machine and start working on it"
                        : "Probe this machine anyway and show relay/direct diagnostics"}
                    >
                      <span aria-hidden>⌨️</span>
                      {device.online ? "Open Workspace" : "Try Connect"}
                    </button>
                  ) : null}
                </div>
                {expandedId === device.id ? (
                  <DeviceDetailsPanel device={device} token={token ?? null} />
                ) : null}
              </div>
            </div>
            );
          })}
        </div>
      )}
      {authModal && token ? (
        <RunnerAuthModal
          runner={authModal.runner}
          device={authModal.device}
          token={token}
          onClose={() => {
            setAuthModal(null);
            void onRefresh();
          }}
        />
      ) : null}
    </div>
  );
}

function InlinePingButton({ device, token }: { device: Device; token: string | null | undefined }) {
  const { pingState, ping } = useDevicePing(device, token);
  return (
    <button
      onClick={() => void ping()}
      disabled={pingState.pinging}
      className="inline-flex items-center gap-1.5 rounded-md border border-surface-700 bg-surface-900/60 px-3 py-1.5 text-xs font-semibold text-surface-200 shadow-sm hover:border-surface-600 hover:bg-surface-800 disabled:opacity-50"
      title="Probe /health via relay first, then direct host"
    >
      {pingState.pinging
        ? "Pinging..."
        : pingState.ok === true
          ? `${pingState.rttMs}ms`
          : pingState.ok === false
            ? "Unreachable"
            : "Ping"}
    </button>
  );
}

function DeviceDetailsPanel({ device, token }: { device: Device; token: string | null }) {
  const { info, error, loading } = useDeviceRuntimeInfo(device, true, token);
  const effectiveInfo = (info || device.probeInfo || null) as DeviceRuntimeInfo | null;
  const { pingState, ping } = useDevicePing(device, token);
  const allRunners = (device.runners || []).map((r) => r?.runnerId || "").filter(Boolean);
  const allSharedRunners = device.sharedRunners || [];
  const allGuests = (device.sharedGuests || []).map((g) => g.name || g.email || "").filter(Boolean);
  const sysUnknown = <span className="text-surface-600">—</span>;
  // Runtime/system blobs come back from the agent's /info when LAN-reachable.
  // Accept loose keys since this shape differs between agent versions (cpu,
  // cpuPct, memory, memUsedPct, uptime, uptimeSec, arch, kernel, ...).
  const runtime = (effectiveInfo?.runtime || {}) as Record<string, any>;
  const system = (effectiveInfo?.system || {}) as Record<string, any>;
  const cpu = system.cpu ?? runtime.cpu ?? effectiveInfo?.cpu;
  const cpuPct = system.cpuPct ?? runtime.cpuPct ?? effectiveInfo?.cpuPct;
  const memTotal = system.memTotal ?? runtime.memTotal ?? effectiveInfo?.memTotal;
  const memUsed = system.memUsed ?? runtime.memUsed ?? effectiveInfo?.memUsed;
  const arch = system.arch ?? runtime.arch ?? effectiveInfo?.arch;
  const kernel = system.kernel ?? runtime.kernel ?? effectiveInfo?.kernel;
  const uptimeSec = system.uptimeSec ?? runtime.uptimeSec ?? effectiveInfo?.uptimeSec;
  const formatBytes = (n?: number) => {
    if (!n || n <= 0) return null;
    const gb = n / (1024 * 1024 * 1024);
    if (gb >= 1) return `${gb.toFixed(1)} GB`;
    const mb = n / (1024 * 1024);
    return `${mb.toFixed(0)} MB`;
  };
  const formatUptime = (s?: number) => {
    if (!s || s <= 0) return null;
    const d = Math.floor(s / 86400);
    const h = Math.floor((s % 86400) / 3600);
    const m = Math.floor((s % 3600) / 60);
    if (d > 0) return `${d}d ${h}h`;
    if (h > 0) return `${h}h ${m}m`;
    return `${m}m`;
  };

  const row = (label: string, value: React.ReactNode) => (
    <div className="flex items-start justify-between gap-3 py-1 text-xs">
      <span className="text-surface-500">{label}</span>
      <span className="text-right text-surface-200">{value || sysUnknown}</span>
    </div>
  );

  return (
    <div className="mt-3 rounded-lg border border-surface-800 bg-surface-900/40 p-3">
      <div className="mb-3 flex justify-end">
        <button
          onClick={() => void ping()}
          disabled={pingState.pinging}
          className="rounded-md border border-surface-700 bg-surface-950 px-2.5 py-1 text-[11px] font-medium text-surface-300 hover:border-surface-600 hover:text-surface-100 disabled:opacity-50"
          title="Probe /health over relay, tunnel, or direct host"
        >
          {pingState.pinging
            ? "Pinging..."
            : pingState.ok === true
              ? `${pingState.rttMs}ms`
              : pingState.ok === false
                ? "Unreachable"
                : "Ping"}
        </button>
      </div>
      <div className="grid gap-6 md:grid-cols-2">
        <div>
          <div className="mb-2 text-[10px] font-semibold uppercase tracking-widest text-surface-500">Identity</div>
          {row("Device ID", <span className="font-mono">{device.id}</span>)}
          {row("Hardware ID", device.hardwareId ? <span className="font-mono">{String(device.hardwareId).slice(0, 16)}…</span> : null)}
          {row("Host", `${device.host}:${device.port}`)}
          {row("LAN IPs", device.localIps?.length ? device.localIps.join(", ") : null)}
          {row("Public endpoints", device.publicEndpoints?.length ? device.publicEndpoints.join(", ") : null)}
          {row("Tunnel URL", device.tunnelUrl ? <span className="break-all font-mono text-[11px]">{device.tunnelUrl}</span> : null)}
          {row("Primary key", device.publicKey ? <span className="font-mono">{String(device.publicKey).slice(0, 16)}…</span> : null)}
          {row("Session binding", device.sessionBinding)}
          {row("Access scope", device.accessScope)}
          {row("Priority mode", device.priorityMode)}
        </div>
        <div>
          <div className="mb-2 text-[10px] font-semibold uppercase tracking-widest text-surface-500">Runtime</div>
          {row("Status", device.workspaceLive ? "Workspace Live" : device.probeState === "ok" ? "Reachable" : device.online ? "Online" : "Offline")}
          {row("Auth", device.needsAuth ? "Needs auth" : effectiveInfo?.authExpired === true || device.probeState === "auth-expired" ? "Expired" : device.workspaceLive ? "Authenticated workspace" : "Authenticated")}
          {row("Agent mode", typeof effectiveInfo?.mode === "string" ? effectiveInfo.mode : null)}
          {row("Live signal", device.lastTunnelEvent?.at ? `${device.lastTunnelEvent.online ? "relay-online" : "relay-offline"} (${formatLastSeen(new Date(device.lastTunnelEvent.at).toISOString())})` : null)}
          {row("Peer bus", device.peerState ? `${device.peerState}${device.peerLastSeen ? ` (${formatLastSeen(device.peerLastSeen)})` : ""}` : null)}
          {row("Authenticated probe", device.probeState ? `${device.probeState}${device.probePath ? ` via ${device.probePath}` : ""}${device.probeCheckedAt ? ` (${formatLastSeen(device.probeCheckedAt)})` : ""}` : null)}
          {row("Reachability", deviceReachabilitySummary(device))}
          {row("Last agent signal", device.lastSeen ? `${formatLastSeen(device.lastSeen)} (${device.lastSeen})` : null)}
          {row("Version", effectiveInfo?.version)}
          {row("Platform", effectiveInfo?.platform || device.platform)}
          {row("Architecture", arch)}
          {row("Kernel", kernel)}
          {row("CPU cores", cpu)}
          {row("CPU usage", cpuPct != null ? `${Number(cpuPct).toFixed(0)}%` : null)}
          {row("Memory used", memUsed ? `${formatBytes(memUsed)} / ${formatBytes(memTotal) ?? "—"}` : formatBytes(memTotal))}
          {row("Uptime", formatUptime(uptimeSec))}
          {row("Work dir", effectiveInfo?.workDir)}
          {row("Auto-start", effectiveInfo?.autoStart)}
        </div>
      </div>
      {(allRunners.length || allSharedRunners.length) ? (
        <div className="mt-3">
          <div className="mb-1 text-[10px] font-semibold uppercase tracking-widest text-surface-500">
            Agents / Runners
          </div>
          <div className="flex flex-wrap gap-1.5">
            {(allRunners.length ? allRunners : allSharedRunners).map((r) => (
              <span key={`rr:${device.id}:${r}`} className="rounded border border-violet-500/40 bg-violet-500/10 px-1.5 py-0.5 text-[10px] font-semibold tracking-wider text-violet-200">
                {formatRunnerChipLabel(r)}
              </span>
            ))}
          </div>
        </div>
      ) : null}
      {allGuests.length ? (
        <div className="mt-3">
          <div className="mb-1 text-[10px] font-semibold uppercase tracking-widest text-surface-500">
            Shared with
          </div>
          <div className="flex flex-wrap gap-1.5">
            {allGuests.map((g) => (
              <span key={`gg:${device.id}:${g}`} className="rounded border border-sky-500/40 bg-sky-500/10 px-1.5 py-0.5 text-[10px] font-semibold tracking-wider text-sky-200">
                {g}
              </span>
            ))}
          </div>
        </div>
      ) : null}
      {device.sharedProjects?.length || device.sharesAllProjects ? (
        <div className="mt-3">
          <div className="mb-1 text-[10px] font-semibold uppercase tracking-widest text-surface-500">
            Shared projects
          </div>
          {device.sharesAllProjects ? (
            <span className="rounded border border-amber-500/40 bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-amber-200">
              All projects
            </span>
          ) : (
            <div className="flex flex-wrap gap-1.5">
              {(device.sharedProjects || []).map((p) => (
                <span key={`pp:${device.id}:${p}`} className="rounded border border-amber-500/40 bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-semibold tracking-wider text-amber-200">
                  {p}
                </span>
              ))}
            </div>
          )}
        </div>
      ) : null}
      {loading ? (
        <p className="mt-3 text-[11px] text-surface-500">Loading runtime info from agent…</p>
      ) : null}
      {error ? (
        <p className="mt-3 text-[11px] text-surface-600">
          Runtime info unavailable from the agent transport ({error}). Showing {device.probeInfo ? "last authenticated probe + cached registry fields" : "cached registry fields only"}.
        </p>
      ) : null}
      <div className="mt-3 flex justify-end border-t border-surface-800/60 pt-2">
        <button
          onClick={() => hideDevice(device.id)}
          className="text-[11px] text-surface-500 hover:text-red-300"
          title="Hide this device from the list — local to this browser"
        >
          Hide this device
        </button>
      </div>
    </div>
  );
}

/**
 * Remote "Sign in" modal. Kicks off `codex login --device-auth` (or
 * `claude auth login --console`) on the connected agent, pulls the
 * URL + one-time code out of the CLI's stdout, and renders them so the
 * user can complete the flow in *their* browser on any device — no
 * SSH, no local env keys, no API key paste.
 *
 * Status machine mirrors runnerBrowserAuthSession on the Go side:
 *   starting → awaiting_browser (url+code filled) → completed | failed | cancelled.
 */
function RunnerAuthModal({
  runner,
  device,
  token,
  onClose,
}: {
  runner: string;
  device: Device;
  token: string;
  onClose: () => void;
}) {
  const [session, setSession] = useState<RunnerBrowserAuthSession | null>(null);
  const [startError, setStartError] = useState<string | null>(null);
  const startedRef = useRef(false);
  const [copied, setCopied] = useState(false);
  // A dedicated AgentClient bound to *this* device. The shared singleton is
  // scoped to the active workspace (the "Open Workspace" flow) and may be
  // disconnected — or connected to a different machine — while the user is
  // on the Devices tab. Creating our own per-modal client means "sign in to
  // Codex on machine X" never depends on "is machine X currently the chat
  // target?" and doesn't clobber the workspace connection if there is one.
  const clientRef = useRef<AgentClient | null>(null);
  if (clientRef.current === null) {
    clientRef.current = new AgentClient();
    // Relay servers + shared relay password live on the workspace singleton
    // (populated from platformConfig + user settings on dashboard mount).
    // Reuse them so the modal can reach remote machines too — direct LAN
    // is never going to work for something like yaver-test-ephemeral.
    clientRef.current.setRelayServers(
      agentClient.configuredRelayServers.map((r) => ({ ...r })),
    );
  }
  const deviceName = device.name || device.id;

  useEffect(() => {
    if (startedRef.current) return;
    startedRef.current = true;
    const client = clientRef.current!;
    (async () => {
      try {
        const tunnelUrls = Array.from(
          new Set(
            [
              ...(Array.isArray(device.publicEndpoints) ? device.publicEndpoints : []),
              ...(device.tunnelUrl ? [device.tunnelUrl] : []),
            ]
              .map((u) => String(u || "").trim())
              .filter(Boolean),
          ),
        );
        await client.connect(device.host, device.port, token, device.id, { tunnelUrls });
        const s = await client.startRunnerBrowserAuth(runner);
        setSession(s);
      } catch (err) {
        setStartError(err instanceof Error ? err.message : String(err));
      }
    })();
    return () => {
      try { client.disconnect(); } catch { /* tearing down anyway */ }
    };
  }, [runner, device.host, device.port, device.id, device.tunnelUrl, token]);

  useEffect(() => {
    if (!session) return;
    if (session.status === "completed" || session.status === "failed" || session.status === "cancelled") return;
    const client = clientRef.current!;
    const iv = setInterval(async () => {
      try {
        const s = await client.getRunnerBrowserAuthStatus(session.id);
        setSession(s);
      } catch {
        // keep polling — transient fetch errors are fine
      }
    }, 1500);
    return () => clearInterval(iv);
  }, [session?.id, session?.status]);

  const terminal = session && ["completed", "failed", "cancelled"].includes(session.status);

  const copyCode = async () => {
    if (!session?.code) return;
    try {
      await navigator.clipboard.writeText(session.code);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard API may be blocked — the code is still visible on screen
    }
  };

  const runnerLabel = runner === "codex" ? "OpenAI Codex" : runner === "claude" ? "Claude Code" : runner;

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm p-4"
      onClick={(e) => { if (e.target === e.currentTarget && terminal) onClose(); }}
    >
      <div className="w-full max-w-md rounded-xl border border-surface-800 bg-surface-900 p-5 shadow-2xl">
        <div className="mb-3 flex items-start justify-between">
          <div>
            <h3 className="text-base font-semibold text-surface-100">Sign in to {runnerLabel}</h3>
            <p className="text-xs text-surface-500">on <span className="font-mono text-surface-300">{deviceName}</span></p>
          </div>
          <button
            onClick={async () => {
              if (session && !terminal) { await clientRef.current?.cancelRunnerBrowserAuth(session.id).catch(() => {}); }
              onClose();
            }}
            className="text-surface-500 hover:text-surface-200 text-xl leading-none"
            aria-label="Close"
          >×</button>
        </div>

        {startError ? (
          <div className="rounded-lg border border-red-500/30 bg-red-500/5 p-3 text-xs text-red-300">
            <div className="font-semibold mb-1">Couldn't start sign-in</div>
            {startError}
          </div>
        ) : !session ? (
          <div className="rounded-lg border border-surface-800 bg-surface-800/40 p-3 text-xs text-surface-400">
            Starting the sign-in flow on the remote machine…
          </div>
        ) : session.status === "completed" ? (
          <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-4 text-sm text-emerald-200">
            <div className="font-semibold mb-1">✓ Signed in</div>
            <div className="text-xs text-emerald-300/80">{session.detail || "Auth stored on the remote machine."}</div>
          </div>
        ) : session.status === "failed" || session.status === "cancelled" ? (
          <div className="rounded-lg border border-red-500/30 bg-red-500/5 p-3 text-xs text-red-300">
            <div className="font-semibold mb-1">{session.status === "cancelled" ? "Cancelled" : "Failed"}</div>
            <div>{session.error || session.detail || "The CLI exited before sign-in completed."}</div>
          </div>
        ) : (
          <div className="space-y-3">
            <p className="text-xs text-surface-400">
              Complete sign-in from any browser — we triggered <code className="rounded bg-surface-800 px-1.5 py-0.5 font-mono text-surface-200">{runner} login --device-auth</code> on the remote machine.
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
                <div className="mb-1 text-[10px] font-semibold uppercase tracking-widest text-surface-500">
                  Enter this code
                </div>
                <button
                  onClick={copyCode}
                  className="flex w-full items-center justify-between rounded-lg border border-surface-700 bg-surface-800/60 px-4 py-3 text-left hover:border-surface-600"
                >
                  <span className="font-mono text-xl tracking-[0.2em] text-surface-100">{session.code}</span>
                  <span className="text-[10px] uppercase text-surface-500">{copied ? "copied" : "click to copy"}</span>
                </button>
              </div>
            ) : null}
            <p className="text-[10px] text-surface-600 leading-relaxed">
              Device codes are a common phishing target. Never share this code. Once you finish in the browser, this dialog turns green automatically.
            </p>
          </div>
        )}
      </div>
    </div>
  );
}
