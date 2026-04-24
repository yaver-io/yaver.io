"use client";

import Link from "next/link";
import React, { useCallback, useEffect, useState } from "react";
import { type Device, hideDevice, unhideAll } from "@/lib/use-devices";
import { CONVEX_URL } from "@/lib/constants";

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

function sharedGuestLabels(device: Pick<Device, "sharedGuests">): string[] {
  return (device.sharedGuests || [])
    .map((guest) => guest.name || guest.email || "")
    .map((label) => String(label).trim())
    .filter(Boolean);
}

function deviceShareSummary(device: Pick<Device, "isGuest" | "sharedWithGuests" | "sharedGuests" | "sharesAllProjects" | "sharedProjects" | "sharedRunners" | "runners">) {
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

function useDeviceRuntimeInfo(device: Device, enabled: boolean, token: string | null | undefined) {
  const [info, setInfo] = useState<DeviceRuntimeInfo | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!enabled || !token || !device.online) return;
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
  }, [enabled, token, device.id, device.host, device.port, device.online]);

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

export default function DevicesView({ devices, onRefresh, signedInEmail, signedInProvider, token, onOpen, hiddenCount = 0 }: DevicesViewProps) {
  const { primaryDeviceId, setPrimaryDevice } = usePrimaryDeviceId(token);
  const [expandedId, setExpandedId] = useState<string | null>(null);
  return (
    <div className="mb-6">
      <div className="mb-3 flex items-center justify-between">
        <h2 className="text-lg font-semibold text-surface-50">Devices</h2>
        <button
          onClick={() => onRefresh()}
          className="btn-secondary px-3 py-1.5 text-xs"
        >
          Refresh
        </button>
      </div>

      {devices.length === 0 ? (
        <div className="card p-8 text-center">
          <p className="mb-2 text-sm text-surface-400">No devices registered.</p>
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
          <p className="mb-4 text-xs text-surface-500">
            If browser OAuth already succeeded on the machine but Yaver still shows no devices, run <code className="rounded bg-surface-800 px-1.5 py-0.5 text-surface-300">yaver auth factory-reset</code> there to clear stale auth state and re-sign in against the live backend.
          </p>
          <Link href="/download" className="btn-secondary px-4 py-2 text-sm">
            Download Yaver
          </Link>
        </div>
      ) : (
        <div className="space-y-2">
          <div className="rounded-xl border border-amber-500/20 bg-amber-500/8 px-4 py-3 text-xs text-amber-100">
            If a machine finishes browser OAuth but still shows stale auth locally, run <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-100">yaver auth factory-reset</code> on that machine. MCP clients can call <code className="rounded bg-surface-900 px-1.5 py-0.5 text-surface-100">yaver_auth_factory_reset</code>.
          </div>
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
          {devices.map((device) => {
            const shareSummary = deviceShareSummary(device);
            return (
            <div key={device.id} className="card flex items-start gap-4">
              <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-surface-800 text-surface-400">
                <DeviceIcon platform={device.platform} />
              </div>
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
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
                  {primaryDeviceId === device.id ? (
                    <span className="rounded border border-indigo-500/40 bg-indigo-500/10 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-indigo-300">
                      Primary ★
                    </span>
                  ) : null}
                  <span
                    className={`inline-flex h-2 w-2 rounded-full ${
                      device.online ? "bg-green-400" : "bg-surface-600"
                    }`}
                  />
                  <span className="text-xs text-surface-500">
                    {device.online ? "Online" : "Offline"}
                  </span>
                </div>
                <p className="text-sm text-surface-500">
                  {devicePlatformLabel(device)} · Last seen {formatLastSeen(device.lastSeen)}
                </p>
                {device.edgeProfile ? (
                  <p className="text-xs text-surface-500">
                    {device.edgeProfile.supportsLocalInference ? "Local inference" : "No local inference"} · max {device.edgeProfile.maxModelClass} model · {device.edgeProfile.preferredTasks.slice(0, 3).join(", ")}
                  </p>
                ) : null}
                {shareSummary ? (
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
                {shareSummary && shareSummary.runnerChips.length > 0 ? (
                  <div className="mt-2 flex flex-wrap items-center gap-1.5">
                    {shareSummary.runnerChips.map((runner) => (
                      <span key={`${device.id}:runner:${runner}`} className="rounded border border-violet-500/40 bg-violet-500/10 px-1.5 py-0.5 text-[10px] font-semibold tracking-wider text-violet-200">
                        {runner}
                      </span>
                    ))}
                  </div>
                ) : null}
                <p className="text-xs text-surface-600 font-mono">
                  {device.id.substring(0, 8)}...
                </p>
                <div className="mt-3 flex flex-wrap items-center gap-2">
                  {onOpen && device.online ? (
                    <button
                      onClick={() => onOpen(device)}
                      className="inline-flex items-center gap-1.5 rounded-md bg-indigo-500 px-3 py-1.5 text-xs font-semibold text-white shadow-sm hover:bg-indigo-400"
                      title="Connect to this machine and start working on it"
                    >
                      <span aria-hidden>⌨️</span>
                      Open Workspace
                    </button>
                  ) : null}
                  <button
                    onClick={() => setExpandedId(expandedId === device.id ? null : device.id)}
                    className="text-xs text-surface-400 hover:text-surface-200"
                  >
                    {expandedId === device.id ? "Hide details" : "Details"}
                  </button>
                  {!device.isGuest && token ? (
                    <button
                      onClick={async () => {
                        try {
                          await setPrimaryDevice(primaryDeviceId === device.id ? null : device.id);
                        } catch (e: any) {
                          alert(`Failed to update primary: ${e?.message ?? e}`);
                        }
                      }}
                      className="text-xs text-indigo-400 hover:text-indigo-300"
                    >
                      {primaryDeviceId === device.id ? "Unset primary" : "Set as primary"}
                    </button>
                  ) : null}
                  <button
                    onClick={() => hideDevice(device.id)}
                    className="ml-auto text-xs text-surface-500 hover:text-red-300"
                    title="Hide this device from the list (local to this browser — unaffected on other machines)"
                  >
                    Hide
                  </button>
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
    </div>
  );
}

function DeviceDetailsPanel({ device, token }: { device: Device; token: string | null }) {
  const { info, error, loading } = useDeviceRuntimeInfo(device, true, token);
  const allRunners = (device.runners || []).map((r) => r?.runnerId || "").filter(Boolean);
  const allSharedRunners = device.sharedRunners || [];
  const allGuests = (device.sharedGuests || []).map((g) => g.name || g.email || "").filter(Boolean);
  const sysUnknown = <span className="text-surface-600">—</span>;
  // Runtime/system blobs come back from the agent's /info when LAN-reachable.
  // Accept loose keys since this shape differs between agent versions (cpu,
  // cpuPct, memory, memUsedPct, uptime, uptimeSec, arch, kernel, ...).
  const runtime = (info?.runtime || {}) as Record<string, any>;
  const system = (info?.system || {}) as Record<string, any>;
  const cpu = system.cpu ?? runtime.cpu ?? info?.cpu;
  const cpuPct = system.cpuPct ?? runtime.cpuPct ?? info?.cpuPct;
  const memTotal = system.memTotal ?? runtime.memTotal ?? info?.memTotal;
  const memUsed = system.memUsed ?? runtime.memUsed ?? info?.memUsed;
  const arch = system.arch ?? runtime.arch ?? info?.arch;
  const kernel = system.kernel ?? runtime.kernel ?? info?.kernel;
  const uptimeSec = system.uptimeSec ?? runtime.uptimeSec ?? info?.uptimeSec;
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
          {row("Status", device.online ? "Online" : "Offline")}
          {row("Last heartbeat", device.lastSeen ? `${formatLastSeen(device.lastSeen)} (${device.lastSeen})` : null)}
          {row("Version", info?.version)}
          {row("Platform", info?.platform || device.platform)}
          {row("Architecture", arch)}
          {row("Kernel", kernel)}
          {row("CPU cores", cpu)}
          {row("CPU usage", cpuPct != null ? `${Number(cpuPct).toFixed(0)}%` : null)}
          {row("Memory used", memUsed ? `${formatBytes(memUsed)} / ${formatBytes(memTotal) ?? "—"}` : formatBytes(memTotal))}
          {row("Uptime", formatUptime(uptimeSec))}
          {row("Work dir", info?.workDir)}
          {row("Auto-start", info?.autoStart)}
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
          Runtime info unavailable over LAN ({error}). Reading Convex heartbeat fields only.
        </p>
      ) : null}
    </div>
  );
}
