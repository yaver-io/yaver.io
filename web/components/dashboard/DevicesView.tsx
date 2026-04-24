"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import type { Device } from "@/lib/use-devices";
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

export default function DevicesView({ devices, onRefresh, signedInEmail, signedInProvider, token }: DevicesViewProps) {
  const { primaryDeviceId, setPrimaryDevice } = usePrimaryDeviceId(token);
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
                  {devicePlatformLabel(device)} -- Last seen {device.lastSeen}
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
                  <div className="mt-2 flex flex-wrap items-center gap-1.5">
                    <span className="rounded border border-sky-500/40 bg-sky-500/10 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-sky-200">
                      Shared With
                    </span>
                    {shareSummary.guestChips.map((guest) => (
                      <span key={`${device.id}:guest:${guest}`} className="rounded border border-sky-500/40 bg-sky-500/10 px-1.5 py-0.5 text-[10px] font-semibold tracking-wider text-sky-200">
                        {guest}
                      </span>
                    ))}
                    {shareSummary.guestOverflow > 0 ? (
                      <span className="rounded border border-surface-700 bg-surface-900 px-1.5 py-0.5 text-[10px] font-semibold tracking-wider text-surface-300">
                        +{shareSummary.guestOverflow} more
                      </span>
                    ) : null}
                  </div>
                ) : null}
                {shareSummary && shareSummary.runnerChips.length > 0 ? (
                  <div className="mt-2 flex flex-wrap items-center gap-1.5">
                    <span className="rounded border border-violet-500/40 bg-violet-500/10 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-violet-200">
                      Agents
                    </span>
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
                {!device.isGuest && token ? (
                  <button
                    onClick={async () => {
                      try {
                        await setPrimaryDevice(primaryDeviceId === device.id ? null : device.id);
                      } catch (e: any) {
                        alert(`Failed to update primary: ${e?.message ?? e}`);
                      }
                    }}
                    className="mt-1 text-xs text-indigo-400 hover:text-indigo-300"
                  >
                    {primaryDeviceId === device.id ? "Unset primary" : "Set as primary"}
                  </button>
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
